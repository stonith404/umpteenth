package providers

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/httpserver"
	"github.com/stonith404/umpteenth/backend/internal/listquery"
	"github.com/stonith404/umpteenth/backend/internal/llm"
	"github.com/stonith404/umpteenth/backend/internal/principal"
	"github.com/stonith404/umpteenth/backend/internal/providers/providersdb"
)

type providerDto struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Kind       string  `json:"kind"`
	BaseURL    *string `json:"baseUrl"`
	HasAPIKey  bool    `json:"hasApiKey"`
	ModelCount int64   `json:"modelCount"`
	// EnabledModelCount counts the models that can be picked: enabled and still listed by the source
	EnabledModelCount int64       `json:"enabledModelCount"`
	ModelSource       ModelSource `json:"modelSource" enum:"catalog,server,manual" doc:"catalog: the models.dev catalog of the kind, server: the server's model list, manual: models are added by hand"`
	SyncedAt          *int64      `json:"syncedAt"`
	SyncError         *string     `json:"syncError" doc:"Why the last sync could not read the model list"`
	CreatedAt         int64       `json:"createdAt"`
}

type listProvidersInput struct {
	httpserver.ListParams
}

var providersSpec = &listquery.Spec{
	Select: "SELECT p.id, p.name, p.kind, p.base_url, p.api_key_enc IS NOT NULL, " +
		"(SELECT COUNT(*) FROM models m WHERE m.provider_id = p.id), " +
		"(SELECT COUNT(*) FROM models m WHERE m.provider_id = p.id AND m.enabled AND m.unlisted_at IS NULL), " +
		"p.synced_at, p.sync_error, p.created_at FROM providers p",
	From:        "FROM providers p",
	Sorts:       map[string]string{"name": "p.name", "kind": "p.kind", "createdAt": "p.created_at"},
	DefaultSort: "name",
	Search:      []string{"p.name", "p.kind", "p.base_url"},
	TieBreaker:  "p.id",
}

func (m *Module) listProviders(ctx context.Context, in *listProvidersInput) (*httpserver.PaginatedOutput[providerDto], error) {
	q := listquery.New(providersSpec).WhereEq("p.workspace_id", principal.WorkspaceID(ctx))
	items, total, err := listquery.Run(ctx, m.deps.DB, q, in.ToQuery(), func(rows *sql.Rows) (providerDto, error) {
		var d providerDto
		err := rows.Scan(&d.ID, &d.Name, &d.Kind, &d.BaseURL, &d.HasAPIKey, &d.ModelCount, &d.EnabledModelCount, &d.SyncedAt, &d.SyncError, &d.CreatedAt)
		d.ModelSource = modelSourceFor(d.Kind, d.BaseURL)
		return d, err
	})
	if err != nil {
		return nil, err
	}
	return httpserver.NewPaginated(items, in.ListParams, total), nil
}

type providerIDOutput struct {
	Body struct {
		ID string `json:"id"`
	}
}

type createProviderOutput struct {
	Body struct {
		ID string `json:"id"`
		// Sync reports how listing the new provider's models went
		Sync SyncResult `json:"sync"`
	}
}

type createProviderInput struct {
	Body struct {
		Name    string `json:"name" minLength:"1" maxLength:"100"`
		Kind    string `json:"kind" enum:"anthropic,openai,fake"`
		BaseURL string `json:"baseUrl,omitempty" maxLength:"500"`
		APIKey  string `json:"apiKey,omitempty" maxLength:"1000"`
	}
}

// createProvider stores the provider and lists its models right away, so it is ready to pick from
func (m *Module) createProvider(ctx context.Context, in *createProviderInput) (*createProviderOutput, error) {
	b := in.Body
	if strings.TrimSpace(b.Name) == "" {
		return nil, apperror.InvalidField("name", "required", "must not be empty")
	}
	if !m.service.SupportsKind(b.Kind) {
		return nil, apperror.InvalidField("kind", "unsupported", "is not available in this build")
	}
	if b.BaseURL != "" {
		if err := m.checkBaseURL(ctx, b.BaseURL); err != nil {
			return nil, err
		}
	}

	wid := principal.WorkspaceID(ctx)
	id, err := m.service.CreateProvider(ctx, wid, b.Name, b.Kind, b.BaseURL, b.APIKey, nil)
	if err != nil {
		return nil, err
	}
	out := &createProviderOutput{}
	out.Body.ID = id
	out.Body.Sync, err = m.service.SyncProvider(ctx, wid, id)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// checkBaseURL rejects a base URL the host must not call, or one that carries credentials, since every workspace member can read it
func (m *Module) checkBaseURL(ctx context.Context, baseURL string) error {
	// Credentials belong in the API key, which is stored encrypted and never returned
	if u, err := url.Parse(baseURL); err == nil && u.User != nil {
		return apperror.InvalidField("baseUrl", "credentials", "must not contain credentials, set the API key instead")
	}
	return m.deps.Egress.CheckURL(ctx, "baseUrl", baseURL)
}

type updateProviderInput struct {
	ID   string `path:"id"`
	Body struct {
		Name    *string `json:"name,omitempty" minLength:"1" maxLength:"100"`
		BaseURL *string `json:"baseUrl,omitempty" maxLength:"500"`
		// APIKey replaces the stored key; an empty string removes it
		APIKey *string `json:"apiKey,omitempty" maxLength:"1000"`
	}
}

func (m *Module) updateProvider(ctx context.Context, in *updateProviderInput) (*struct{}, error) {
	wid := principal.WorkspaceID(ctx)
	current, err := m.service.queries.GetProvider(ctx, providersdb.GetProviderParams{WorkspaceID: wid, ID: in.ID})
	if database.IsNotFound(err) {
		return nil, apperror.NotFound("Provider")
	} else if err != nil {
		return nil, fmt.Errorf("failed to load provider: %w", err)
	}

	name, baseURL := current.Name, current.BaseUrl
	if in.Body.Name != nil {
		name = strings.TrimSpace(*in.Body.Name)
		if name == "" {
			return nil, apperror.InvalidField("name", "required", "must not be empty")
		}
	}
	if in.Body.BaseURL != nil {
		baseURL = nonEmpty(*in.Body.BaseURL)
		if baseURL != nil {
			if err := m.checkBaseURL(ctx, *baseURL); err != nil {
				return nil, err
			}
		}
	}
	_, err = m.service.queries.UpdateProvider(ctx, providersdb.UpdateProviderParams{Name: name, BaseUrl: baseURL, WorkspaceID: wid, ID: in.ID})
	if database.IsUniqueViolation(err) {
		return nil, apperror.AlreadyInUse("Provider name")
	} else if err != nil {
		return nil, fmt.Errorf("failed to update provider: %w", err)
	}

	if in.Body.APIKey != nil {
		enc, keyID, err := m.service.encryptKey(*in.Body.APIKey)
		if err != nil {
			return nil, err
		}
		_, err = m.service.queries.UpdateProviderKey(ctx, providersdb.UpdateProviderKeyParams{ApiKeyEnc: enc, KeyID: keyID, WorkspaceID: wid, ID: in.ID})
		if err != nil {
			return nil, fmt.Errorf("failed to update provider key: %w", err)
		}
	}

	// A new address or key can change which models the server lists
	if deref(baseURL) != deref(current.BaseUrl) || in.Body.APIKey != nil {
		_, err = m.service.SyncProvider(ctx, wid, in.ID)
		if err != nil {
			return nil, err
		}
	}
	return nil, nil
}

type idInput struct {
	ID string `path:"id"`
}

func (m *Module) deleteProvider(ctx context.Context, in *idInput) (*struct{}, error) {
	n, err := m.service.queries.DeleteProvider(ctx, providersdb.DeleteProviderParams{WorkspaceID: principal.WorkspaceID(ctx), ID: in.ID})
	if err != nil {
		return nil, fmt.Errorf("failed to delete provider: %w", err)
	}
	if n == 0 {
		return nil, apperror.NotFound("Provider")
	}

	// Default models pointing at a deleted model would make every run and reflection fail, so they fall back to unset
	if err := m.service.queries.ClearMissingModelSettings(ctx, principal.WorkspaceID(ctx)); err != nil {
		return nil, fmt.Errorf("failed to clear the default models: %w", err)
	}
	return nil, nil
}

type testProviderInput struct {
	ID   string `path:"id"`
	Body struct {
		ModelID string `json:"modelId" doc:"Model to test with"`
	}
}

type testProviderOutput struct {
	Body struct {
		OK        bool   `json:"ok"`
		Reply     string `json:"reply,omitempty"`
		Error     string `json:"error,omitempty"`
		LatencyMs int64  `json:"latencyMs"`
	}
}

// testProvider sends a tiny prompt so users can verify credentials and connectivity from the settings page
// Disabled models can be tested too, so an admin can check one before enabling it
func (m *Module) testProvider(ctx context.Context, in *testProviderInput) (*testProviderOutput, error) {
	provider, model, err := m.service.resolve(ctx, principal.WorkspaceID(ctx), in.Body.ModelID, true)
	if err != nil {
		return nil, err
	}
	if model.ProviderID != in.ID {
		return nil, apperror.InvalidField("modelId", "invalid", "does not belong to this provider")
	}

	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	out := &testProviderOutput{}
	start := time.Now()
	resp, err := provider.Stream(callCtx, llm.Request{
		Model:     model.Name,
		Messages:  []llm.Message{{Role: llm.RoleUser, Parts: []llm.Part{llm.TextPart("Reply with the single word OK.")}}},
		MaxTokens: 256,
	}, nil)
	out.Body.LatencyMs = time.Since(start).Milliseconds()
	if err != nil {
		out.Body.Error = err.Error()
		return out, nil
	}
	out.Body.OK = true
	out.Body.Reply = resp.Message.Text()
	return out, nil
}

type syncProviderOutput struct {
	Body SyncResult
}

// syncProvider lists a provider's models now instead of waiting for the next refresh, such as after pulling a new model into Ollama
func (m *Module) syncProvider(ctx context.Context, in *idInput) (*syncProviderOutput, error) {
	wid := principal.WorkspaceID(ctx)
	_, err := m.service.queries.GetProvider(ctx, providersdb.GetProviderParams{WorkspaceID: wid, ID: in.ID})
	if database.IsNotFound(err) {
		return nil, apperror.NotFound("Provider")
	} else if err != nil {
		return nil, fmt.Errorf("failed to load provider: %w", err)
	}
	result, err := m.service.SyncProvider(ctx, wid, in.ID)
	if err != nil {
		return nil, err
	}
	return &syncProviderOutput{Body: result}, nil
}

type setEnabledInput struct {
	ID   string `path:"id"`
	Body struct {
		Enabled bool `json:"enabled"`
	}
}

type setEnabledOutput struct {
	Body struct {
		// Changed counts the models the request touched
		Changed int64 `json:"changed"`
	}
}

// setProviderModelsEnabled turns all of a provider's models on or off, for picking a few out of a long server list
func (m *Module) setProviderModelsEnabled(ctx context.Context, in *setEnabledInput) (*setEnabledOutput, error) {
	wid := principal.WorkspaceID(ctx)
	rows, err := m.service.queries.ListProviderModels(ctx, providersdb.ListProviderModelsParams{WorkspaceID: wid, ProviderID: in.ID})
	if err != nil {
		return nil, fmt.Errorf("failed to load models: %w", err)
	}
	if !in.Body.Enabled {
		names := make(map[string]string, len(rows))
		for _, row := range rows {
			names[row.ID] = displayName(row.Label, row.Model)
		}
		if err := m.service.checkNotDefault(ctx, wid, names); err != nil {
			return nil, err
		}
	}

	n, err := m.service.queries.SetProviderModelsEnabled(ctx, providersdb.SetProviderModelsEnabledParams{Enabled: in.Body.Enabled, WorkspaceID: wid, ProviderID: in.ID})
	if err != nil {
		return nil, fmt.Errorf("failed to update models: %w", err)
	}
	out := &setEnabledOutput{}
	out.Body.Changed = n
	return out, nil
}

type modelDto struct {
	ID            string    `json:"id"`
	ProviderID    string    `json:"providerId"`
	ProviderName  string    `json:"providerName"`
	ProviderKind  string    `json:"providerKind"`
	Model         string    `json:"model"`
	Label         *string   `json:"label"`
	Price         llm.Price `json:"price" doc:"Micro-USD per 1M tokens"`
	ContextWindow int64     `json:"contextWindow"`
	Caps          llm.Caps  `json:"caps"`
	// Enabled models can be picked for jobs and defaults; admins turn models off instead of deleting them
	Enabled bool `json:"enabled"`
	// Synced models are listed by their provider's source, which adds them back if they are deleted
	Synced bool `json:"synced"`
	// FollowCatalog models take their prices and capabilities from every catalog refresh
	FollowCatalog bool `json:"followCatalog"`
	// UnlistedAt is when the source stopped listing the model, which can't be picked until it is listed again
	UnlistedAt *int64 `json:"unlistedAt"`
}

type listModelsInput struct {
	httpserver.ListParams
	Provider string `query:"provider" doc:"Comma-separated provider IDs"`
	Status   string `query:"status" doc:"Comma-separated statuses: enabled (can be picked), disabled, unlisted"`
}

var modelsSpec = &listquery.Spec{
	Select: "SELECT m.id, m.provider_id, p.name, p.kind, m.model, m.label, m.price_in, m.price_out, m.price_cache_read, m.price_cache_write, m.context_window, m.caps, " +
		"m.enabled, m.synced, m.follow_catalog, m.unlisted_at FROM models m JOIN providers p ON p.id = m.provider_id",
	From:        "FROM models m JOIN providers p ON p.id = m.provider_id",
	Sorts:       map[string]string{"model": "m.model", "provider": "p.name", "priceIn": "m.price_in", "priceOut": "m.price_out", "contextWindow": "m.context_window", "enabled": "m.enabled"},
	DefaultSort: "provider,model",
	Search:      []string{"m.model", "m.label", "p.name"},
	TieBreaker:  "m.id",
}

// modelStatuses are the SQL conditions of the status filter, which a model matches when any listed status applies
var modelStatuses = map[string]string{
	"enabled":  "(m.enabled AND m.unlisted_at IS NULL)",
	"disabled": "NOT m.enabled",
	"unlisted": "m.unlisted_at IS NOT NULL",
}

func (m *Module) listModels(ctx context.Context, in *listModelsInput) (*httpserver.PaginatedOutput[modelDto], error) {
	q := listquery.New(modelsSpec).WhereEq("m.workspace_id", principal.WorkspaceID(ctx))
	q.WhereIn("m.provider_id", listquery.SplitCSV(in.Provider))
	var conds []string
	for _, status := range listquery.SplitCSV(in.Status) {
		cond, ok := modelStatuses[status]
		if !ok {
			return nil, apperror.InvalidField("status", "invalid", "must be enabled, disabled or unlisted")
		}
		conds = append(conds, cond)
	}
	if len(conds) > 0 {
		q.Where("(" + strings.Join(conds, " OR ") + ")")
	}

	items, total, err := listquery.Run(ctx, m.deps.DB, q, in.ToQuery(), func(rows *sql.Rows) (modelDto, error) {
		var (
			d    modelDto
			caps string
		)
		err := rows.Scan(&d.ID, &d.ProviderID, &d.ProviderName, &d.ProviderKind, &d.Model, &d.Label, &d.Price.In, &d.Price.Out, &d.Price.CacheRead, &d.Price.CacheWrite, &d.ContextWindow, &caps,
			&d.Enabled, &d.Synced, &d.FollowCatalog, &d.UnlistedAt)
		_ = json.Unmarshal([]byte(caps), &d.Caps)
		return d, err
	})
	if err != nil {
		return nil, err
	}
	return httpserver.NewPaginated(items, in.ListParams, total), nil
}

// ModelBody holds the fields of a new model; it is exported so Huma flattens it into request schemas
type ModelBody struct {
	Label *string   `json:"label,omitempty" maxLength:"100"`
	Price llm.Price `json:"price"`
	Caps  llm.Caps  `json:"caps"`
}

type createModelInput struct {
	Body struct {
		ProviderID string `json:"providerId"`
		Model      string `json:"model" minLength:"1" maxLength:"200"`
		ModelBody
	}
}

func (m *Module) createModel(ctx context.Context, in *createModelInput) (*providerIDOutput, error) {
	wid := principal.WorkspaceID(ctx)
	_, err := m.service.queries.GetProvider(ctx, providersdb.GetProviderParams{WorkspaceID: wid, ID: in.Body.ProviderID})
	if database.IsNotFound(err) {
		return nil, apperror.NotFound("Provider")
	} else if err != nil {
		return nil, fmt.Errorf("failed to load provider: %w", err)
	}

	label := ""
	if in.Body.Label != nil {
		label = *in.Body.Label
	}
	id, err := m.service.CreateModel(ctx, wid, in.Body.ProviderID, in.Body.Model, label, in.Body.Price, in.Body.Caps)
	if err != nil {
		return nil, err
	}
	out := &providerIDOutput{}
	out.Body.ID = id
	return out, nil
}

type updateModelInput struct {
	ID   string `path:"id"`
	Body struct {
		// Label replaces the label; an empty string removes it
		Label *string    `json:"label,omitempty" maxLength:"100"`
		Price *llm.Price `json:"price,omitempty"`
		Caps  *llm.Caps  `json:"caps,omitempty"`
		// FollowCatalog true resets the model to the catalog's metadata and keeps it following every refresh; editing the label, prices or capabilities without it stops following
		FollowCatalog *bool `json:"followCatalog,omitempty"`
		Enabled       *bool `json:"enabled,omitempty"`
	}
}

// updateModel changes any of a model's metadata, catalog following and enabled state, leaving the fields it isn't sent as they are
func (m *Module) updateModel(ctx context.Context, in *updateModelInput) (*struct{}, error) {
	wid := principal.WorkspaceID(ctx)
	row, err := m.service.queries.GetModel(ctx, providersdb.GetModelParams{WorkspaceID: wid, ID: in.ID})
	if database.IsNotFound(err) {
		return nil, apperror.NotFound("Model")
	} else if err != nil {
		return nil, fmt.Errorf("failed to load model: %w", err)
	}
	b := in.Body

	// Metadata changes are written together, starting from the stored values so a partial update keeps the rest
	edited := b.Label != nil || b.Price != nil || b.Caps != nil
	if edited || b.FollowCatalog != nil {
		var caps llm.Caps
		_ = json.Unmarshal([]byte(row.Caps), &caps)
		label := row.Label
		price := llm.Price{In: row.PriceIn, Out: row.PriceOut, CacheRead: row.PriceCacheRead, CacheWrite: row.PriceCacheWrite}
		if b.Label != nil {
			label = nonEmpty(*b.Label)
		}
		if b.Price != nil {
			price = *b.Price
		}
		if b.Caps != nil {
			caps = *b.Caps
		}

		// Following the catalog takes its values over whatever was sent
		follow := false
		if b.FollowCatalog != nil && *b.FollowCatalog {
			entry, ok := llm.LookupModel(row.ProviderKind, row.Model)
			if !ok || modelSourceFor(row.ProviderKind, row.ProviderBaseUrl) != SourceCatalog || !row.Synced {
				return nil, apperror.InvalidField("followCatalog", "not_in_catalog", "is only possible for models the catalog lists")
			}
			follow = true
			label, price, caps = nonEmpty(entry.Label), entry.Price, entry.Caps
		}

		capsJSON, _ := json.Marshal(caps)
		_, err = m.service.queries.UpdateModel(ctx, providersdb.UpdateModelParams{
			Label:           label,
			PriceIn:         price.In,
			PriceOut:        price.Out,
			PriceCacheRead:  price.CacheRead,
			PriceCacheWrite: price.CacheWrite,
			ContextWindow:   int64(caps.Context),
			Caps:            string(capsJSON),
			FollowCatalog:   follow,
			WorkspaceID:     wid,
			ID:              in.ID,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to update model: %w", err)
		}
	}

	// Turning a model off must not break the defaults that point at it
	if b.Enabled != nil {
		if !*b.Enabled {
			if err := m.service.checkNotDefault(ctx, wid, map[string]string{row.ID: displayName(row.Label, row.Model)}); err != nil {
				return nil, err
			}
		}
		_, err = m.service.queries.SetModelEnabled(ctx, providersdb.SetModelEnabledParams{Enabled: *b.Enabled, WorkspaceID: wid, ID: in.ID})
		if err != nil {
			return nil, fmt.Errorf("failed to update model: %w", err)
		}
	}
	return nil, nil
}

func (m *Module) deleteModel(ctx context.Context, in *idInput) (*struct{}, error) {
	n, err := m.service.queries.DeleteModel(ctx, providersdb.DeleteModelParams{WorkspaceID: principal.WorkspaceID(ctx), ID: in.ID})
	if err != nil {
		return nil, fmt.Errorf("failed to delete model: %w", err)
	}
	if n == 0 {
		return nil, apperror.NotFound("Model")
	}

	// Default models pointing at a deleted model would make every run and reflection fail, so they fall back to unset
	if err := m.service.queries.ClearMissingModelSettings(ctx, principal.WorkspaceID(ctx)); err != nil {
		return nil, fmt.Errorf("failed to clear the default models: %w", err)
	}
	return nil, nil
}

type catalogInput struct {
	Kind string `query:"kind" enum:"anthropic,openai"`
}

type catalogOutput struct {
	Body []catalogEntryDto
}

type catalogEntryDto struct {
	Model string    `json:"model"`
	Label string    `json:"label"`
	Price llm.Price `json:"price"`
	Caps  llm.Caps  `json:"caps"`
}

func (m *Module) catalog(ctx context.Context, in *catalogInput) (*catalogOutput, error) {
	m.service.catalog.ensureFresh(ctx)
	out := &catalogOutput{Body: []catalogEntryDto{}}
	for _, s := range catalogModels(in.Kind) {
		out.Body = append(out.Body, catalogEntryDto(s))
	}
	return out, nil
}
