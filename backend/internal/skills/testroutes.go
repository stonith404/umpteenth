//go:build e2etest

package skills

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/stonith404/umpteenth/backend/internal/httpserver"
)

// RegisterTestRoutes mounts the e2e helper that points GitHub downloads at a fake, since specs can't rely on github.com
func (m *Module) RegisterTestRoutes(api huma.API) {
	httpserver.Register(api, httpserver.Operation("test-skill-archives", http.MethodPost, "/api/test/skills/github-archives", "Test"), nil, func(_ context.Context, in *struct {
		Body struct {
			URL string `json:"url" doc:"The fake's base URL, empty for GitHub's own"`
		}
	}) (*struct{}, error) {
		m.SetGitHubArchives(in.Body.URL)
		return nil, nil
	})
}

// SetGitHubArchives sets where repository tarballs come from, and an empty URL restores GitHub's own
func (m *Module) SetGitHubArchives(url string) {
	if url == "" {
		url = defaultGitHubArchives
	}
	m.archivesMu.Lock()
	defer m.archivesMu.Unlock()
	m.githubArchives = url
}
