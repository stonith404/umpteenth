package providers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/llm"
	"github.com/stonith404/umpteenth/backend/internal/providers/providersdb"
	"github.com/stonith404/umpteenth/backend/internal/utils/crypto"
)

// Factory builds an LLM adapter for one provider kind
type Factory func(cfg llm.Config) (llm.Provider, error)

// ModelSeed describes a model to create together with its provider
type ModelSeed struct {
	Model string
	Label string
	Price llm.Price
	Caps  llm.Caps
}

// Model is a resolved model, ready to call
type Model struct {
	ID           string
	Name         string
	ProviderID   string
	ProviderName string
	Kind         string
	Price        llm.Price
	Caps         llm.Caps
}

// Service manages providers and models and builds llm.Provider instances from them
type Service struct {
	db        *database.DB
	queries   *providersdb.Queries
	secretKey []byte
	factories map[string]Factory
	catalog   *catalogStore

	// Adapter instances are stateless HTTP clients, cached by configuration so a key change creates a new one
	mu    sync.Mutex
	cache map[string]llm.Provider
}

func newService(db *database.DB, secretKey []byte, factories map[string]Factory) *Service {
	queries := providersdb.New(db)
	return &Service{db: db, queries: queries, secretKey: secretKey, factories: factories, catalog: newCatalogStore(queries), cache: map[string]llm.Provider{}}
}

// Resolve loads a model and returns a ready provider for it
// A disabled model is refused, while an unlisted one is still tried, since its provider may serve it without listing it
func (s *Service) Resolve(ctx context.Context, workspaceID, modelID string) (llm.Provider, Model, error) {
	return s.resolve(ctx, workspaceID, modelID, false)
}

func (s *Service) resolve(ctx context.Context, workspaceID, modelID string, allowDisabled bool) (llm.Provider, Model, error) {
	row, err := s.queries.GetModel(ctx, providersdb.GetModelParams{WorkspaceID: workspaceID, ID: modelID})
	if database.IsNotFound(err) {
		return nil, Model{}, apperror.NotFound("Model")
	} else if err != nil {
		return nil, Model{}, fmt.Errorf("failed to load model: %w", err)
	}
	if !row.Enabled && !allowDisabled {
		return nil, Model{}, apperror.Conflict(fmt.Sprintf("Model %s of %s is disabled, enable it in Settings → Providers & models", displayName(row.Label, row.Model), row.ProviderName))
	}

	// Adapters look up request details such as OpenAI's max_completion_tokens in the catalog, so a refresh on another replica must reach this one
	s.catalog.ensureFresh(ctx)

	var caps llm.Caps
	_ = json.Unmarshal([]byte(row.Caps), &caps)
	model := Model{
		ID:           row.ID,
		Name:         row.Model,
		ProviderID:   row.ProviderID,
		ProviderName: row.ProviderName,
		Kind:         row.ProviderKind,
		Price:        llm.Price{In: row.PriceIn, Out: row.PriceOut, CacheRead: row.PriceCacheRead, CacheWrite: row.PriceCacheWrite},
		Caps:         caps,
	}

	apiKey, err := s.decryptKey(row.ProviderApiKeyEnc)
	if err != nil {
		return nil, Model{}, err
	}
	provider, err := s.provider(row.ProviderKind, deref(row.ProviderBaseUrl), apiKey)
	if err != nil {
		return nil, Model{}, err
	}
	return modelCaps{Provider: provider, model: model.Name, caps: caps}, model, nil
}

// modelCaps answers Caps for the resolved model from its stored capabilities, so what an admin or a sync set decides how the model is called
// Other model names still get the adapter's own answer
type modelCaps struct {
	llm.Provider
	model string
	caps  llm.Caps
}

func (m modelCaps) Caps(model string) llm.Caps {
	if model == m.model {
		return m.caps
	}
	return m.Provider.Caps(model)
}

func displayName(label *string, model string) string {
	if label != nil && *label != "" {
		return *label
	}
	return model
}

func (s *Service) provider(kind, baseURL, apiKey string) (llm.Provider, error) {
	factory, ok := s.factories[kind]
	if !ok {
		return nil, apperror.Unsupported("Provider kind " + kind + " is not available in this build")
	}

	sum := sha256.Sum256([]byte(kind + "\x00" + baseURL + "\x00" + apiKey))
	cacheKey := hex.EncodeToString(sum[:])

	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.cache[cacheKey]; ok {
		return p, nil
	}
	p, err := factory(llm.Config{Kind: kind, BaseURL: baseURL, APIKey: apiKey})
	if err != nil {
		return nil, fmt.Errorf("failed to create %s provider: %w", kind, err)
	}
	s.cache[cacheKey] = p
	return p, nil
}

// ModelExists reports whether the model belongs to the workspace and can be picked, which means it is enabled and its provider still lists it
func (s *Service) ModelExists(ctx context.Context, workspaceID, modelID string) (bool, error) {
	row, err := s.queries.GetModel(ctx, providersdb.GetModelParams{WorkspaceID: workspaceID, ID: modelID})
	if database.IsNotFound(err) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("failed to load model: %w", err)
	}
	return row.Enabled && row.UnlistedAt == nil, nil
}

// SupportsKind reports whether this build can talk to the provider kind
func (s *Service) SupportsKind(kind string) bool {
	_, ok := s.factories[kind]
	return ok
}

func (s *Service) encryptKey(apiKey string) ([]byte, *string, error) {
	if apiKey == "" {
		return nil, nil, nil
	}
	enc, err := crypto.Encrypt(s.secretKey, []byte(apiKey))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to encrypt API key: %w", err)
	}
	keyID := crypto.KeyIDV1
	return enc, &keyID, nil
}

func (s *Service) decryptKey(enc []byte) (string, error) {
	if len(enc) == 0 {
		return "", nil
	}
	plain, err := crypto.Decrypt(s.secretKey, enc)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt provider API key: %w", err)
	}
	return string(plain), nil
}

// FindModel returns the ID of a model by provider kind and model name
func (s *Service) FindModel(ctx context.Context, workspaceID, kind, model string) (string, bool, error) {
	id, err := s.queries.FindModel(ctx, providersdb.FindModelParams{WorkspaceID: workspaceID, Kind: kind, Model: model})
	if database.IsNotFound(err) {
		return "", false, nil
	} else if err != nil {
		return "", false, err
	}
	return id, true, nil
}

// CreateProvider stores a provider and, optionally, models of its own; the models of its source come from SyncProvider
func (s *Service) CreateProvider(ctx context.Context, workspaceID, name, kind, baseURL, apiKey string, models []ModelSeed) (string, error) {
	enc, keyID, err := s.encryptKey(apiKey)
	if err != nil {
		return "", err
	}

	id := database.NewID()
	now := database.Now()
	err = s.queries.CreateProvider(ctx, providersdb.CreateProviderParams{
		ID:          id,
		WorkspaceID: workspaceID,
		Name:        strings.TrimSpace(name),
		Kind:        kind,
		BaseUrl:     nonEmpty(baseURL),
		ApiKeyEnc:   enc,
		KeyID:       keyID,
		CreatedAt:   now,
	})
	if database.IsUniqueViolation(err) {
		return "", apperror.AlreadyInUse("Provider name")
	} else if err != nil {
		return "", fmt.Errorf("failed to create provider: %w", err)
	}

	for _, m := range models {
		_, err = s.CreateModel(ctx, workspaceID, id, m.Model, m.Label, m.Price, m.Caps)
		if err != nil {
			return "", err
		}
	}
	return id, nil
}

// CreateModel adds a model to a provider by hand, so syncs leave its metadata alone
func (s *Service) CreateModel(ctx context.Context, workspaceID, providerID, model, label string, price llm.Price, caps llm.Caps) (string, error) {
	capsJSON, _ := json.Marshal(caps)
	id := database.NewID()
	err := s.queries.CreateModel(ctx, providersdb.CreateModelParams{
		ID:              id,
		WorkspaceID:     workspaceID,
		ProviderID:      providerID,
		Model:           strings.TrimSpace(model),
		Label:           nonEmpty(label),
		PriceIn:         price.In,
		PriceOut:        price.Out,
		PriceCacheRead:  price.CacheRead,
		PriceCacheWrite: price.CacheWrite,
		ContextWindow:   int64(caps.Context),
		Caps:            string(capsJSON),
		CreatedAt:       database.Now(),
	})
	if database.IsUniqueViolation(err) {
		return "", apperror.AlreadyInUse("Model")
	} else if err != nil {
		return "", fmt.Errorf("failed to create model: %w", err)
	}
	return id, nil
}

func nonEmpty(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// defaultModelNames name the workspace settings that hold a default model
var defaultModelNames = map[string]string{"agentModelId": "agent model", "utilityModelId": "utility model", "reflectionModelId": "reflection model"}

// checkNotDefault refuses to disable a model a workspace default points at, which would make every run that uses the default fail
func (s *Service) checkNotDefault(ctx context.Context, workspaceID string, modelIDs map[string]string) error {
	rows, err := s.queries.ListDefaultModelSettings(ctx, workspaceID)
	if err != nil {
		return fmt.Errorf("failed to load the default models: %w", err)
	}
	for _, row := range rows {
		var id string
		if json.Unmarshal([]byte(row.Value), &id) != nil {
			continue
		}
		if name, ok := modelIDs[id]; ok {
			return apperror.Conflict(fmt.Sprintf("%s is the workspace's %s, pick another one in Settings → General first", name, defaultModelNames[row.Key]))
		}
	}
	return nil
}
