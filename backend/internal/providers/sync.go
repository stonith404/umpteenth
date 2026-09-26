package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/llm"
	"github.com/stonith404/umpteenth/backend/internal/providers/providersdb"
)

// ModelSource says where a provider's model list comes from
type ModelSource string

const (
	// SourceCatalog providers list the models.dev catalog of their kind, and their models follow its prices and capabilities
	SourceCatalog ModelSource = "catalog"
	// SourceServer providers list what their server's /models endpoint serves, and their models keep the metadata they were discovered with
	SourceServer ModelSource = "server"
	// SourceManual providers have no model list, such as the test fake
	SourceManual ModelSource = "manual"
)

const (
	// listTimeout bounds one server's answer to the model list request
	listTimeout = 30 * time.Second
	// fallbackContext is assumed for server models neither the server nor models.dev knows, which are mostly local models with small default windows
	fallbackContext = 32_768
	// maxProviderModels bounds the models one provider holds, since a sync never deletes one and writes the new ones while holding the database's write lock
	maxProviderModels = llm.MaxListedModels
	// maxModelIDBytes and maxModelLabelChars bound one listed model, so the provider's rows stay small however large the server makes an entry
	maxModelIDBytes    = 256
	maxModelLabelChars = 200
)

// errTooManyModels refuses a sync that would take a provider past maxProviderModels, so a server that lists new IDs on every sync can't grow the database without end
var errTooManyModels = fmt.Errorf("the sync would take the provider past %d models, delete the ones it no longer needs first", maxProviderModels)

// modelSourceFor decides the source by the API a provider talks to
// Anthropic is always its own API or a proxy of it, while an OpenAI-compatible provider is only the OpenAI API without a base URL or with OpenAI's own
func modelSourceFor(kind string, baseURL *string) ModelSource {
	switch kind {
	case llm.KindAnthropic:
		return SourceCatalog
	case llm.KindOpenAI:
		if baseURL == nil || *baseURL == "" {
			return SourceCatalog
		}
		u, err := url.Parse(*baseURL)
		if err == nil && strings.EqualFold(u.Hostname(), "api.openai.com") {
			return SourceCatalog
		}
		return SourceServer
	default:
		return SourceManual
	}
}

// SyncResult counts what a sync changed; Error is set when the source could not be read or lists too many models, in which case nothing changed
type SyncResult struct {
	Added    int    `json:"added"`
	Updated  int    `json:"updated"`
	Unlisted int    `json:"unlisted"`
	Error    string `json:"error,omitempty"`
}

// sourceModel is one model as its source describes it
type sourceModel struct {
	Model string
	Label string
	Price llm.Price
	Caps  llm.Caps
}

// SyncProvider brings a provider's models in line with its source
// A source that can't be read is recorded on the provider and reported in the result rather than as an error, so callers such as provider creation still succeed
func (s *Service) SyncProvider(ctx context.Context, workspaceID, providerID string) (SyncResult, error) {
	p, err := s.queries.GetProvider(ctx, providersdb.GetProviderParams{WorkspaceID: workspaceID, ID: providerID})
	if err != nil {
		return SyncResult{}, fmt.Errorf("failed to load provider: %w", err)
	}

	// Read the source list
	var models []sourceModel
	source := modelSourceFor(p.Kind, p.BaseUrl)
	switch source {
	case SourceCatalog:
		s.catalog.ensureFresh(ctx)
		models = catalogModels(p.Kind)
	case SourceServer:
		models, err = s.serverModels(ctx, p)
		if err != nil {
			msg := err.Error()
			if recErr := s.recordSync(ctx, p, &msg); recErr != nil {
				return SyncResult{}, recErr
			}
			return SyncResult{Error: msg}, nil
		}
	default:
		return SyncResult{}, nil
	}

	// Apply it in one transaction, so a sync that fails half way changes nothing
	var result SyncResult
	err = s.db.InTx(ctx, func(tx *database.Tx) error {
		var txErr error
		result, txErr = applySync(ctx, providersdb.New(tx), p, models, source == SourceCatalog)
		return txErr
	})
	if errors.Is(err, errTooManyModels) {
		// A provider that would outgrow the bound keeps its models, and the reason shows on it like that of a source that can't be read
		msg := err.Error()
		if recErr := s.recordSync(ctx, p, &msg); recErr != nil {
			return SyncResult{}, recErr
		}
		return SyncResult{Error: msg}, nil
	} else if err != nil {
		return SyncResult{}, fmt.Errorf("failed to sync the models of %s: %w", p.Name, err)
	}
	return result, s.recordSync(ctx, p, nil)
}

// SyncAll syncs every provider of every workspace, logging the ones that fail so one broken server doesn't stop the rest
func (s *Service) SyncAll(ctx context.Context) {
	providers, err := s.queries.ListAllProviders(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "Failed to list providers for the model sync", slog.Any("error", err))
		return
	}
	for _, p := range providers {
		result, err := s.SyncProvider(ctx, p.WorkspaceID, p.ID)
		switch {
		case err != nil:
			slog.ErrorContext(ctx, "Failed to sync provider models", slog.String("provider", p.ID), slog.Any("error", err))
		case result.Error != "":
			slog.WarnContext(ctx, "Failed to list provider models", slog.String("provider", p.ID), slog.String("error", result.Error))
		case result.Added+result.Updated+result.Unlisted > 0:
			slog.InfoContext(ctx, "Synced provider models", slog.String("provider", p.ID), slog.Int("added", result.Added), slog.Int("updated", result.Updated), slog.Int("unlisted", result.Unlisted))
		}
	}
}

func (s *Service) recordSync(ctx context.Context, p providersdb.Provider, syncError *string) error {
	now := database.Now()
	err := s.queries.SetProviderSync(ctx, providersdb.SetProviderSyncParams{SyncedAt: &now, SyncError: syncError, WorkspaceID: p.WorkspaceID, ID: p.ID})
	if err != nil {
		return fmt.Errorf("failed to record the model sync: %w", err)
	}
	return nil
}

// catalogModels lists the catalog of a provider kind
func catalogModels(kind string) []sourceModel {
	var out []sourceModel
	for _, e := range llm.CatalogFor(kind) {
		out = append(out, sourceModel{Model: e.Model, Label: e.Label, Price: e.Price, Caps: e.Caps})
	}
	return out
}

// serverModels asks an OpenAI-compatible server what it serves
func (s *Service) serverModels(ctx context.Context, p providersdb.Provider) ([]sourceModel, error) {
	apiKey, err := s.decryptKey(p.ApiKeyEnc)
	if err != nil {
		return nil, err
	}
	provider, err := s.provider(p.Kind, deref(p.BaseUrl), apiKey)
	if err != nil {
		return nil, err
	}
	lister, ok := provider.(llm.ModelLister)
	if !ok {
		return nil, fmt.Errorf("%s providers can't list their models", p.Kind)
	}

	listCtx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	listed, err := lister.ListModels(listCtx)
	if err != nil {
		return nil, err
	}
	out := make([]sourceModel, 0, len(listed))
	for _, m := range listed {
		// No real model ID comes close to the bound, and a longer one would let the server grow the database by megabytes per entry
		if len(m.ID) > maxModelIDBytes {
			continue
		}
		out = append(out, serverModel(m))
	}
	return out, nil
}

// serverModel fills in what a server leaves out from what models.dev knows about the model's family, while the server's own answer wins wherever it gives one
func serverModel(m llm.ListedModel) sourceModel {
	caps := llm.Caps{Tools: true, ParallelTools: true}
	if family, ok := llm.LookupFamily(m.ID); ok {
		caps = family
	}
	if m.Tools != nil {
		caps.Tools, caps.ParallelTools = *m.Tools, *m.Tools
	}
	if m.Reasoning != nil {
		caps.Reasoning = *m.Reasoning
	}
	if m.JSONSchema != nil {
		caps.JSONSchema = *m.JSONSchema
	}
	if m.Vision != nil {
		caps.Vision = *m.Vision
	}
	if m.Context > 0 {
		caps.Context = m.Context
	}
	if caps.Context <= 0 {
		caps.Context = fallbackContext
	}

	// Labels are only shown, so an overlong one from the server is cut rather than stored whole
	label := m.Label
	if utf8.RuneCountInString(label) > maxModelLabelChars {
		label = string([]rune(label)[:maxModelLabelChars])
	}

	out := sourceModel{Model: m.ID, Label: label, Caps: caps}
	if m.Price != nil {
		out.Price = *m.Price
	}
	return out
}

// applySync adds the models the source lists for the first time, refreshes the metadata of models that follow the catalog, and marks the models the source stopped listing
// Nothing is deleted: an unlisted model keeps its settings and is listed again as it was if the source brings it back
func applySync(ctx context.Context, q *providersdb.Queries, p providersdb.Provider, models []sourceModel, fromCatalog bool) (SyncResult, error) {
	var result SyncResult
	rows, err := q.ListProviderModels(ctx, providersdb.ListProviderModelsParams{WorkspaceID: p.WorkspaceID, ProviderID: p.ID})
	if err != nil {
		return result, err
	}
	existing := make(map[string]providersdb.ListProviderModelsRow, len(rows))
	for _, row := range rows {
		existing[row.Model] = row
	}

	// Refuse a list that would take the provider past its bound before writing anything
	fresh := make(map[string]bool)
	for _, m := range models {
		if _, ok := existing[m.Model]; !ok {
			fresh[m.Model] = true
		}
	}
	if len(rows)+len(fresh) > maxProviderModels {
		return result, errTooManyModels
	}

	listed := make(map[string]bool, len(models))
	for _, m := range models {
		if listed[m.Model] {
			continue
		}
		listed[m.Model] = true
		capsJSON, _ := json.Marshal(m.Caps)
		row, ok := existing[m.Model]

		// A model the source lists for the first time is added and enabled
		if !ok {
			n, err := q.CreateSyncedModel(ctx, providersdb.CreateSyncedModelParams{
				ID: database.NewID(), WorkspaceID: p.WorkspaceID, ProviderID: p.ID, Model: m.Model, Label: nonEmpty(m.Label),
				PriceIn: m.Price.In, PriceOut: m.Price.Out, PriceCacheRead: m.Price.CacheRead, PriceCacheWrite: m.Price.CacheWrite,
				ContextWindow: int64(m.Caps.Context), Caps: string(capsJSON), FollowCatalog: fromCatalog, CreatedAt: database.Now(),
			})
			if err != nil {
				return result, err
			}
			result.Added += int(n)
			continue
		}

		// A model an admin added by hand is adopted once the source lists it, keeping the metadata they set, and a model that was unlisted comes back with its settings
		if !row.Synced || row.UnlistedAt != nil {
			err = q.MarkModelListed(ctx, providersdb.MarkModelListedParams{WorkspaceID: p.WorkspaceID, ID: row.ID})
			if err != nil {
				return result, err
			}
		}

		// Catalog models take the catalog's current metadata unless an admin set their own
		if fromCatalog && row.FollowCatalog && metadataChanged(row, m) {
			err = q.UpdateModelMetadata(ctx, providersdb.UpdateModelMetadataParams{
				Label: nonEmpty(m.Label), PriceIn: m.Price.In, PriceOut: m.Price.Out, PriceCacheRead: m.Price.CacheRead, PriceCacheWrite: m.Price.CacheWrite,
				ContextWindow: int64(m.Caps.Context), Caps: string(capsJSON), WorkspaceID: p.WorkspaceID, ID: row.ID,
			})
			if err != nil {
				return result, err
			}
			result.Updated++
		}
	}

	// Synced models the source no longer lists are marked, and models an admin added by hand are left alone
	now := database.Now()
	for _, row := range rows {
		if row.Synced && row.UnlistedAt == nil && !listed[row.Model] {
			err = q.MarkModelUnlisted(ctx, providersdb.MarkModelUnlistedParams{UnlistedAt: &now, WorkspaceID: p.WorkspaceID, ID: row.ID})
			if err != nil {
				return result, err
			}
			result.Unlisted++
		}
	}
	return result, nil
}

// metadataChanged reports whether the catalog's label, prices or capabilities differ from the stored row
func metadataChanged(row providersdb.ListProviderModelsRow, m sourceModel) bool {
	var caps llm.Caps
	_ = json.Unmarshal([]byte(row.Caps), &caps)
	price := llm.Price{In: row.PriceIn, Out: row.PriceOut, CacheRead: row.PriceCacheRead, CacheWrite: row.PriceCacheWrite}
	return deref(row.Label) != m.Label || price != m.Price || caps != m.Caps
}
