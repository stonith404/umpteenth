//go:build e2etest

package skills

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/stonith404/umpteenth/backend/internal/httpserver"
)

// archivesKey holds the fake's address in the kv table, so every replica of an HA test stack downloads from it
const archivesKey = "e2e-skill-archives"

// RegisterTestRoutes mounts the e2e helper that points GitHub downloads at a fake, since specs can't rely on github.com
func (m *Module) RegisterTestRoutes(api huma.API) {
	httpserver.Register(api, httpserver.Operation("test-skill-archives", http.MethodPost, "/api/test/skills/github-archives", "Test"), nil, func(ctx context.Context, in *struct {
		Body struct {
			URL string `json:"url" doc:"The fake's base URL, empty for GitHub's own"`
		}
	}) (*struct{}, error) {
		return nil, m.SetGitHubArchives(ctx, in.Body.URL)
	})
}

// SetGitHubArchives sets where repository tarballs come from, and an empty URL restores GitHub's own
func (m *Module) SetGitHubArchives(ctx context.Context, url string) error {
	if url == "" {
		_, err := m.db.ExecContext(ctx, "DELETE FROM kv WHERE key = $1", archivesKey)
		return err
	}
	_, err := m.db.ExecContext(ctx, "INSERT INTO kv (key, value) VALUES ($1, $2) ON CONFLICT (key) DO UPDATE SET value = excluded.value", archivesKey, url)
	return err
}

// archives is where repository tarballs come from, the fake a spec set up or else GitHub's own
func (m *Module) archives(ctx context.Context) string {
	var url string
	if err := m.db.QueryRowContext(ctx, "SELECT value FROM kv WHERE key = $1", archivesKey).Scan(&url); err != nil {
		// Without a fake set up the row is missing, and GitHub's own serves the tarballs
		return m.githubArchives
	}
	return url
}
