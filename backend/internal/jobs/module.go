// Package jobs owns jobs, their schedule and concurrency actor, job state, webhooks and the compile step (PLAN.md §7)
package jobs

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	francishost "github.com/italypaleale/francis/host"

	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/httpserver"
	"github.com/stonith404/umpteenth/backend/internal/jobs/jobsdb"
	"github.com/stonith404/umpteenth/backend/internal/llm"
	"github.com/stonith404/umpteenth/backend/internal/playbook"
	"github.com/stonith404/umpteenth/backend/internal/runs"
	"github.com/stonith404/umpteenth/backend/internal/sandbox"
	"github.com/stonith404/umpteenth/backend/internal/settings"
)

// RunQueue creates and submits runs, implemented by the runs module
type RunQueue interface {
	Create(ctx context.Context, n runs.NewRun) (string, error)
	Submit(ctx context.Context, runID string) error
	Cancel(ctx context.Context, workspaceID, runID string) error
}

// Playbooks reads and writes playbook versions
type Playbooks interface {
	Content(ctx context.Context, jobID string, version int64) (playbook.Content, error)
	Save(ctx context.Context, workspaceID, jobID string, c playbook.Content, meta playbook.VersionMeta) (int64, error)
}

// SettingsReader returns workspace settings
type SettingsReader interface {
	Get(ctx context.Context, workspaceID string) (settings.WorkspaceSettings, error)
}

// ModelResolver resolves a model to a provider for the compile step, and checks the model a job names
type ModelResolver interface {
	ResolveModel(ctx context.Context, workspaceID, modelID string) (llm.Provider, string, error)
	ModelExists(ctx context.Context, workspaceID, modelID string) (bool, error)
}

// SecretEnv resolves the job's secrets to environment variables for its sandbox
type SecretEnv interface {
	EnvForJob(ctx context.Context, workspaceID, jobID string) (map[string]string, error)
}

// MCPCatalog lists the workspace's MCP servers, used by the compile step to match needs to servers
type MCPCatalog interface {
	ServerNames(ctx context.Context, workspaceID string) ([]string, error)
}

// WebhookLimiter decides whether a webhook call may proceed
type WebhookLimiter interface {
	Allow(ctx context.Context, key string) (bool, time.Duration, error)
}

type Dependencies struct {
	DB        *database.DB
	Actors    francishost.Host
	Runs      RunQueue
	Playbooks Playbooks
	Settings  SettingsReader
	Models    ModelResolver
	Secrets   SecretEnv
	MCP       MCPCatalog
	// SandboxInfo reports the active adapter's capabilities for warnings
	SandboxInfo func(ctx context.Context) (sandbox.Info, error)
	// WebhookLimiter rate-limits webhooks per job
	WebhookLimiter WebhookLimiter
}

type Module struct {
	deps    Dependencies
	db      *database.DB
	queries *jobsdb.Queries
	actors  francishost.Host
	runs    RunQueue
}

func New(deps Dependencies) (*Module, error) {
	m := &Module{deps: deps, db: deps.DB, queries: jobsdb.New(deps.DB), actors: deps.Actors, runs: deps.Runs}

	// One actor per job serializes its schedule and concurrency decisions across all replicas
	err := deps.Actors.RegisterActor(actorType, m.newActor)
	if err != nil {
		return nil, fmt.Errorf("failed to register job actor: %w", err)
	}
	return m, nil
}

// SetRuns wires the run queue, which is created before the jobs module but needs it for retries
func (m *Module) SetRuns(r RunQueue) {
	m.runs = r
	m.deps.Runs = r
}

// maxWebhookBody caps what an unauthenticated caller can make the server read
const maxWebhookBody = 1 << 20

func (m *Module) RegisterRoutes(api huma.API, auth huma.Middlewares, webhookLimit huma.Middlewares) {
	httpserver.Register(api, httpserver.Operation("list-jobs", http.MethodGet, "/api/jobs", "Jobs"), auth, m.list)
	httpserver.Register(api, httpserver.Operation("create-job", http.MethodPost, "/api/jobs", "Jobs"), auth, m.create)
	httpserver.Register(api, httpserver.Operation("compile-job", http.MethodPost, "/api/jobs/compile", "Jobs"), auth, m.compile)
	httpserver.Register(api, httpserver.Operation("get-job", http.MethodGet, "/api/jobs/{id}", "Jobs"), auth, m.get)
	httpserver.Register(api, httpserver.Operation("update-job", http.MethodPatch, "/api/jobs/{id}", "Jobs"), auth, m.update)
	httpserver.Register(api, httpserver.Operation("delete-job", http.MethodDelete, "/api/jobs/{id}", "Jobs"), auth, m.archive)
	httpserver.Register(api, httpserver.Operation("run-job", http.MethodPost, "/api/jobs/{id}/runs", "Jobs"), auth, m.runNow)
	httpserver.Register(api, httpserver.Operation("rotate-webhook-token", http.MethodPost, "/api/jobs/{id}/webhook-token", "Jobs"), auth, m.rotateWebhookToken)
	httpserver.Register(api, httpserver.Operation("list-job-state", http.MethodGet, "/api/jobs/{id}/state", "Job state"), auth, m.listState)
	httpserver.Register(api, httpserver.Operation("get-job-state", http.MethodGet, "/api/jobs/{id}/state/{key}", "Job state"), auth, m.getState)
	httpserver.Register(api, httpserver.Operation("put-job-state", http.MethodPut, "/api/jobs/{id}/state/{key}", "Job state"), auth, m.putState)
	httpserver.Register(api, httpserver.Operation("delete-job-state", http.MethodDelete, "/api/jobs/{id}/state/{key}", "Job state"), auth, m.deleteState)
	// Huma only limits bodies of operations with a typed body, and the webhook reads its raw body before the token is checked
	webhookOp := httpserver.Operation("trigger-webhook", http.MethodPost, "/hooks/{jobId}", "Webhooks")
	webhookOp.MaxBodyBytes = maxWebhookBody
	webhookOp.BodyReadTimeout = 10 * time.Second
	httpserver.Register(api, webhookOp, webhookLimit, m.webhook)

	// Huma marks every raw body as required, but schedulers and `curl -X POST` often call webhooks without one
	// The registered operation shares this RequestBody with the request handler, so clearing the flag here takes effect
	if item := api.OpenAPI().Paths["/hooks/{jobId}"]; item != nil && item.Post != nil && item.Post.RequestBody != nil {
		item.Post.RequestBody.Required = false
	}
}

// RegisterTestRoutes mounts the e2e helper that fires a job's schedule immediately
func (m *Module) RegisterTestRoutes(api huma.API) {
	httpserver.Register(api, httpserver.Operation("test-fire-schedule", http.MethodPost, "/api/test/jobs/{id}/fire-schedule", "Test"), nil, func(ctx context.Context, in *struct {
		ID string `path:"id"`
	}) (*struct{}, error) {
		return nil, m.invoke(ctx, in.ID, methodFireSchedule, nil, nil)
	})
}
