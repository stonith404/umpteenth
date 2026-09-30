//go:build e2etest

// Package e2etest mounts test-only endpoints used by the Playwright suite; it is only compiled into e2etest builds
package e2etest

import (
	"cmp"
	"context"
	"fmt"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/stonith404/umpteenth/backend/internal/auth"
	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/httpserver"
)

// Users signs in test users without a sign-in provider
type Users interface {
	SignInForTest(ctx context.Context, account auth.TestAccount, providerID, redirect string) (string, http.Cookie, string, error)
}

// Workspaces recreates the default workspace after a reset
type Workspaces interface {
	EnsureDefault(ctx context.Context, onlyIfNone bool) error
}

type Dependencies struct {
	DB         *database.DB
	Users      Users
	Workspaces Workspaces
	// BeforeReset stops what still works on the data a reset is about to delete
	BeforeReset func(ctx context.Context) error
	// OnReset seeds what the tests expect on top of the empty default workspace
	OnReset func(ctx context.Context) error
}

// resetTables lists every application table, children before parents
var resetTables = []string{
	"run_events", "images", "playbook_versions", "job_state", "job_secrets", "job_mcp_servers",
	"api_tokens", "runs", "jobs", "mcp_servers", "secrets", "settings", "models", "providers",
	"blobs", "workspace_invites", "workspace_members", "sessions", "users", "workspaces",
}

func Register(api huma.API, deps Dependencies) {
	httpserver.Register(api, httpserver.Operation("test-reset", http.MethodPost, "/api/test/reset", "Test"), nil, func(ctx context.Context, _ *struct{}) (*struct{}, error) {
		if err := deps.BeforeReset(ctx); err != nil {
			return nil, err
		}
		for _, table := range resetTables {
			_, err := deps.DB.ExecContext(ctx, "DELETE FROM "+table)
			if err != nil {
				return nil, fmt.Errorf("failed to reset %s: %w", table, err)
			}
		}
		// The default workspace comes back without an owner, so the first test user to sign in owns it, whether or not workspaces are turned on
		err := deps.Workspaces.EnsureDefault(ctx, false)
		if err != nil {
			return nil, err
		}
		return nil, deps.OnReset(ctx)
	})

	type sessionOutput struct {
		SetCookie []http.Cookie `header:"Set-Cookie"`
		Body      struct {
			UserID   string `json:"userId"`
			Redirect string `json:"redirect" doc:"Where the login would have sent the browser"`
		}
	}
	type sessionInput struct {
		Provider string `query:"provider" doc:"ID of the sign-in provider the session pretends to have signed in with"`
		// Every field is optional, and an empty body signs in as the default e2e user
		Body *struct {
			Subject       string `json:"subject,omitempty" doc:"Identifies the user, e2e-user when empty"`
			Email         string `json:"email,omitempty"`
			EmailVerified *bool  `json:"emailVerified,omitempty" doc:"Whether the provider vouched for the email address, true when empty"`
			Name          string `json:"name,omitempty"`
			Admin         bool   `json:"admin,omitempty" doc:"Signs in as an instance admin"`
			Redirect      string `json:"redirect,omitempty" doc:"Where the login returns to, such as an invite page"`
		}
	}
	httpserver.Register(api, httpserver.Operation("test-session", http.MethodPost, "/api/test/session", "Test"), nil, func(ctx context.Context, in *sessionInput) (*sessionOutput, error) {
		// The login goes through the same rules as a real one, so invites and personal workspaces behave the same
		account := auth.TestAccount{Issuer: "https://e2e.invalid", Subject: "e2e-user", Email: "e2e@example.com", EmailVerified: true, Name: "E2E User"}
		redirect := ""
		if b := in.Body; b != nil {
			account.Subject = cmp.Or(b.Subject, account.Subject)
			account.Email = cmp.Or(b.Email, account.Email)
			account.Name = cmp.Or(b.Name, account.Name)
			account.Admin = b.Admin
			if b.EmailVerified != nil {
				account.EmailVerified = *b.EmailVerified
			}
			redirect = b.Redirect
		}
		userID, cookie, redirect, err := deps.Users.SignInForTest(ctx, account, in.Provider, redirect)
		if err != nil {
			return nil, err
		}
		out := &sessionOutput{SetCookie: []http.Cookie{cookie}}
		out.Body.UserID = userID
		out.Body.Redirect = redirect
		return out, nil
	})
}
