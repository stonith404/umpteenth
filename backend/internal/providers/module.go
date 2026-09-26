// Package providers owns the LLM providers and models of a workspace and builds llm.Provider instances from them (PLAN.md §5)
package providers

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/italypaleale/francis/builtin/cronjob"
	francishost "github.com/italypaleale/francis/host"

	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/httpserver"
	"github.com/stonith404/umpteenth/backend/internal/principal"
	"github.com/stonith404/umpteenth/backend/internal/utils/crypto"
)

// EgressGuard rejects provider base URLs the host must not call
type EgressGuard interface {
	CheckURL(ctx context.Context, field, rawURL string) error
}

type Dependencies struct {
	DB            *database.DB
	EncryptionKey []byte
	Factories     map[string]Factory
	Egress        EgressGuard
	// Actors runs the catalog refresh as a cron job, once across the cluster
	Actors francishost.Host
	// RefreshInterval is how often the models.dev catalog is fetched and every provider synced; zero turns the refresh off
	RefreshInterval time.Duration
	// MaintenanceDisabled skips the refresh cron job, like the other maintenance jobs in test mode
	MaintenanceDisabled bool
}

type Module struct {
	service *Service
	deps    Dependencies
}

func New(deps Dependencies) (*Module, error) {
	key, err := crypto.DeriveKey(deps.EncryptionKey, "secrets")
	if err != nil {
		return nil, fmt.Errorf("failed to derive secrets key: %w", err)
	}
	m := &Module{service: newService(deps.DB, key, deps.Factories), deps: deps}

	// The refresh runs right away on its first registration, so a new install doesn't wait a whole interval for current models
	if deps.RefreshInterval > 0 && !deps.MaintenanceDisabled {
		refresh, err := cronjob.New("ModelCatalogRefresh",
			cronjob.WithInterval(deps.RefreshInterval), cronjob.WithImmediate(), cronjob.WithJob(m.refresh),
			cronjob.WithJitter(min(10*time.Minute, deps.RefreshInterval/4)), cronjob.WithLogger(slog.Default()))
		if err != nil {
			return nil, fmt.Errorf("failed to create the model catalog refresh: %w", err)
		}
		err = deps.Actors.RegisterBuiltInActor(refresh)
		if err != nil {
			return nil, fmt.Errorf("failed to register the model catalog refresh: %w", err)
		}
	}
	return m, nil
}

// Service exposes model resolution to the runner and other modules
func (m *Module) Service() *Service { return m.service }

func (m *Module) RegisterRoutes(api huma.API, auth huma.Middlewares) {
	// Members pick models for their jobs, and only admins change which providers and models the workspace has
	admin := httpserver.Access{MinRole: principal.RoleAdmin}
	httpserver.Register(api, httpserver.Operation("list-providers", http.MethodGet, "/api/providers", "Providers"), auth, m.listProviders)
	httpserver.Register(api, httpserver.Restrict(httpserver.Operation("create-provider", http.MethodPost, "/api/providers", "Providers"), admin), auth, m.createProvider)
	httpserver.Register(api, httpserver.Restrict(httpserver.Operation("update-provider", http.MethodPatch, "/api/providers/{id}", "Providers"), admin), auth, m.updateProvider)
	httpserver.Register(api, httpserver.Restrict(httpserver.Operation("delete-provider", http.MethodDelete, "/api/providers/{id}", "Providers"), admin), auth, m.deleteProvider)
	httpserver.Register(api, httpserver.Restrict(httpserver.Operation("test-provider", http.MethodPost, "/api/providers/{id}/test", "Providers"), admin), auth, m.testProvider)
	httpserver.Register(api, httpserver.Restrict(httpserver.Operation("sync-provider-models", http.MethodPost, "/api/providers/{id}/sync", "Providers"), admin), auth, m.syncProvider)
	httpserver.Register(api, httpserver.Restrict(httpserver.Operation("set-provider-models-enabled", http.MethodPut, "/api/providers/{id}/models/enabled", "Providers"), admin), auth, m.setProviderModelsEnabled)
	httpserver.Register(api, httpserver.Operation("list-models", http.MethodGet, "/api/models", "Providers"), auth, m.listModels)
	httpserver.Register(api, httpserver.Restrict(httpserver.Operation("create-model", http.MethodPost, "/api/models", "Providers"), admin), auth, m.createModel)
	httpserver.Register(api, httpserver.Restrict(httpserver.Operation("update-model", http.MethodPatch, "/api/models/{id}", "Providers"), admin), auth, m.updateModel)
	httpserver.Register(api, httpserver.Restrict(httpserver.Operation("delete-model", http.MethodDelete, "/api/models/{id}", "Providers"), admin), auth, m.deleteModel)
	httpserver.Register(api, httpserver.Operation("get-model-catalog", http.MethodGet, "/api/catalog/models", "Providers"), auth, m.catalog)
}

// LoadCatalog installs the catalog the last refresh stored, when it is newer than the one this build bundles
func (m *Module) LoadCatalog(ctx context.Context) error {
	return m.service.catalog.load(ctx)
}

// refresh fetches models.dev and syncs every provider, which also picks up new models on self-hosted servers
// A failed download still syncs the providers, from the catalog in use and from their servers
func (m *Module) refresh(ctx context.Context) error {
	err := m.service.catalog.refresh(ctx)
	if err != nil {
		slog.WarnContext(ctx, "Failed to refresh the model catalog", slog.Any("error", err))
	}
	m.service.SyncAll(ctx)
	return nil
}
