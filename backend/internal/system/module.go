// Package system exposes health and instance information
package system

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/stonith404/umpteenth/backend/internal/common"
	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/httpserver"
	"github.com/stonith404/umpteenth/backend/internal/sandbox"
)

type Dependencies struct {
	DB *database.DB
	// AllowPrivateNetworkTargets tells clients whether local model and MCP URLs such as http://localhost:11434 can be saved
	AllowPrivateNetworkTargets bool
	// SandboxInfo checks the active sandbox adapter, and fails when there is none
	SandboxInfo func(ctx context.Context) (sandbox.Info, error)
}

type Module struct {
	deps Dependencies
}

func New(deps Dependencies) *Module {
	return &Module{deps: deps}
}

func (m *Module) RegisterRoutes(api huma.API, mux *http.ServeMux, auth huma.Middlewares) {
	// The health check is a plain handler so it stays cheap and outside the OpenAPI spec
	mux.HandleFunc("GET /healthz", m.healthz)
	httpserver.Register(api, httpserver.Operation("get-system-info", http.MethodGet, "/api/system/info", "System"), auth, m.info)
}

func (m *Module) healthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	err := m.deps.DB.Raw().PingContext(ctx)
	if err != nil {
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type infoOutput struct {
	Body struct {
		Version                    string `json:"version"`
		Database                   string `json:"database"`
		AllowPrivateNetworkTargets bool   `json:"allowPrivateNetworkTargets"`
		// Sandbox is left out when no adapter is configured or its engine does not answer
		Sandbox *SandboxInfo `json:"sandbox,omitempty"`
	}
}

// SandboxInfo gives the adapter's report its own name in the API schema
type SandboxInfo sandbox.Info

func (m *Module) info(ctx context.Context, _ *struct{}) (*infoOutput, error) {
	out := &infoOutput{}
	out.Body.Version = common.Version
	out.Body.Database = string(m.deps.DB.Engine())
	out.Body.AllowPrivateNetworkTargets = m.deps.AllowPrivateNetworkTargets

	// A slow engine must not hold up the settings page
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if info, err := m.deps.SandboxInfo(checkCtx); err == nil {
		out.Body.Sandbox = new(SandboxInfo(info))
	}
	return out, nil
}
