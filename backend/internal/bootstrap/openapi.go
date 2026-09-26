package bootstrap

import (
	"context"
	"fmt"

	"github.com/danielgtaylor/huma/v2"

	"github.com/stonith404/umpteenth/backend/internal/config"
	"github.com/stonith404/umpteenth/backend/internal/database"
)

// OpenAPI builds the full API against a throwaway in-memory database and returns its spec, for TS type generation
func OpenAPI() (*huma.OpenAPI, error) {
	ctx := context.Background()
	cfg := config.Default()
	cfg.App.Env = config.AppEnvDevelopment
	cfg.Sandbox.Adapter = "none"
	cfg.FileStorage.Backend = config.FileBackendDatabase
	cfg.Runs.MaxConcurrent = 1
	cfg.HA.Actors.Port = 1
	err := cfg.Validate()
	if err != nil {
		return nil, err
	}

	db, err := database.Open(ctx, database.EngineSQLite, "file:openapi?mode=memory&cache=shared")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	err = database.Migrate(ctx, db)
	if err != nil {
		return nil, err
	}

	actors, err := newActorHost(cfg, db, "openapi")
	if err != nil {
		return nil, err
	}
	fileStorage, err := initStorage(ctx, cfg, db)
	if err != nil {
		return nil, err
	}
	svc, err := initServices(ctx, cfg, db, actors, fileStorage, "openapi")
	if err != nil {
		return nil, fmt.Errorf("failed to build services: %w", err)
	}
	defer svc.close()

	api, _, err := initRouter(cfg, db, svc)
	if err != nil {
		return nil, err
	}
	return api.OpenAPI(), nil
}
