//go:build e2etest

// Package e2etest mounts test-only endpoints used by the Playwright suite; it is only compiled into e2etest builds
package e2etest

import (
	"context"
	"fmt"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/httpserver"
)

// Users creates the test user and its session
type Users interface {
	UpsertUser(ctx context.Context, subject, email, name string) (string, error)
	SessionCookieFor(ctx context.Context, userID string) (http.Cookie, error)
}

// Workspaces recreates the default workspace after a reset
type Workspaces interface {
	Reset()
	EnsureDefault(ctx context.Context) (string, error)
}

type Dependencies struct {
	DB         *database.DB
	Users      Users
	Workspaces Workspaces
	OnReset    []func(ctx context.Context) error
	Extensions []func(api huma.API)
}

// resetTables lists every application table, children before parents
var resetTables = []string{
	"run_events", "images", "playbook_versions", "job_state", "job_secrets", "job_mcp_servers",
	"api_tokens", "runs", "jobs", "mcp_servers", "secrets", "settings", "models", "providers",
	"blobs", "users", "workspaces",
}

func Register(api huma.API, deps Dependencies) {
	httpserver.Register(api, httpserver.Operation("test-reset", http.MethodPost, "/api/test/reset", "Test"), nil, func(ctx context.Context, _ *struct{}) (*struct{}, error) {
		for _, table := range resetTables {
			_, err := deps.DB.ExecContext(ctx, "DELETE FROM "+table)
			if err != nil {
				return nil, fmt.Errorf("failed to reset %s: %w", table, err)
			}
		}
		deps.Workspaces.Reset()
		_, err := deps.Workspaces.EnsureDefault(ctx)
		if err != nil {
			return nil, err
		}
		for _, hook := range deps.OnReset {
			if err := hook(ctx); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})

	type sessionOutput struct {
		SetCookie []http.Cookie `header:"Set-Cookie"`
		Body      struct {
			UserID string `json:"userId"`
		}
	}
	httpserver.Register(api, httpserver.Operation("test-session", http.MethodPost, "/api/test/session", "Test"), nil, func(ctx context.Context, _ *struct{}) (*sessionOutput, error) {
		userID, err := deps.Users.UpsertUser(ctx, "e2e-user", "e2e@example.com", "E2E User")
		if err != nil {
			return nil, err
		}
		cookie, err := deps.Users.SessionCookieFor(ctx, userID)
		if err != nil {
			return nil, err
		}
		out := &sessionOutput{SetCookie: []http.Cookie{cookie}}
		out.Body.UserID = userID
		return out, nil
	})

	for _, ext := range deps.Extensions {
		ext(api)
	}
}
