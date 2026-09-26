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
)

// SandboxInfoProvider reports the active sandbox adapter, wired in once a sandbox adapter exists
type SandboxInfoProvider interface {
	SandboxInfo(ctx context.Context) (any, error)
}

type Dependencies struct {
	DB *database.DB
	// AllowPrivateNetworkTargets tells clients whether local model and MCP URLs such as http://localhost:11434 can be saved
	AllowPrivateNetworkTargets bool
}

type Module struct {
	db           *database.DB
	allowPrivate bool
	sandbox      SandboxInfoProvider
}

func New(deps Dependencies) *Module {
	return &Module{db: deps.DB, allowPrivate: deps.AllowPrivateNetworkTargets}
}

// SetSandbox wires the sandbox info provider after the sandbox adapter is created
func (m *Module) SetSandbox(p SandboxInfoProvider) {
	m.sandbox = p
}

func (m *Module) RegisterRoutes(api huma.API, mux *http.ServeMux, auth huma.Middlewares) {
	// The health check is a plain handler so it stays cheap and outside the OpenAPI spec
	mux.HandleFunc("GET /healthz", m.healthz)
	httpserver.Register(api, httpserver.Operation("get-system-info", http.MethodGet, "/api/system/info", "System"), auth, m.info)
}

func (m *Module) healthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	err := m.db.Raw().PingContext(ctx)
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
		Sandbox                    any    `json:"sandbox"`
	}
}

func (m *Module) info(ctx context.Context, _ *struct{}) (*infoOutput, error) {
	out := &infoOutput{}
	out.Body.Version = common.Version
	out.Body.Database = string(m.db.Engine())
	out.Body.AllowPrivateNetworkTargets = m.allowPrivate
	if m.sandbox != nil {
		info, err := m.sandbox.SandboxInfo(ctx)
		if err == nil {
			out.Body.Sandbox = info
		}
	}
	return out, nil
}
