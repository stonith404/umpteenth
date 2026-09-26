package bootstrap

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/stonith404/umpteenth/backend/internal/agent"
	"github.com/stonith404/umpteenth/backend/internal/config"
	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/egress"
	"github.com/stonith404/umpteenth/backend/internal/images"
	"github.com/stonith404/umpteenth/backend/internal/jobs"
	"github.com/stonith404/umpteenth/backend/internal/llm"
	"github.com/stonith404/umpteenth/backend/internal/llm/anthropic"
	"github.com/stonith404/umpteenth/backend/internal/llm/openai"
	"github.com/stonith404/umpteenth/backend/internal/playbook"
	"github.com/stonith404/umpteenth/backend/internal/providers"
	"github.com/stonith404/umpteenth/backend/internal/reflection"
	"github.com/stonith404/umpteenth/backend/internal/runner"
	"github.com/stonith404/umpteenth/backend/internal/runs"
	"github.com/stonith404/umpteenth/backend/internal/sandbox"
	"github.com/stonith404/umpteenth/backend/internal/sandbox/docker"
	"github.com/stonith404/umpteenth/backend/internal/sandbox/umpbin"
)

// This file adapts module types to the small interfaces of the engine packages, so neither side imports the other

func newAnthropic(cfg llm.Config) (llm.Provider, error) { return anthropic.New(cfg) }
func newOpenAI(cfg llm.Config) (llm.Provider, error)    { return openai.New(cfg) }

// guardedFactories makes every provider connect through the egress guard, which re-checks each address at dial time
// Checking the base URL when it is saved isn't enough, since a redirect or a changed DNS answer can still lead to a private address
func guardedFactories(factories map[string]providers.Factory, guard *egress.Guard) map[string]providers.Factory {
	out := make(map[string]providers.Factory, len(factories))
	for kind, factory := range factories {
		out[kind] = func(cfg llm.Config) (llm.Provider, error) {
			cfg.HTTPClient = guard.HTTPClient(0)
			return factory(cfg)
		}
	}
	return out
}

// seedAPIKeys lets a fresh install pick up provider keys from the configuration on first start
func seedAPIKeys(keys config.Providers) map[string]string {
	return map[string]string{
		llm.KindAnthropic: keys.AnthropicAPIKey,
		llm.KindOpenAI:    keys.OpenAIAPIKey,
	}
}

var nonNameChars = regexp.MustCompile(`[^a-zA-Z0-9_.-]`)

// replicaID names this replica; the hostname is stable across restarts, which lets the Docker adapter reuse its relay container
func replicaID(configured string) string {
	if configured != "" {
		return nonNameChars.ReplaceAllString(configured, "-")
	}
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "replica"
	}
	host = strings.Trim(nonNameChars.ReplaceAllString(host, "-"), "-.")
	if host == "" {
		host = "replica"
	}
	return host
}

// initSandboxAdapter builds the adapter selected by sandbox.adapter, following Pocket ID's storage backend switch
func initSandboxAdapter(ctx context.Context, cfg *config.Config, instanceID, hostID string) (sandbox.Adapter, error) {
	switch cfg.Sandbox.Adapter {
	case "none", "":
		return nil, nil
	case sandbox.TypeDocker:
		adapter, err := docker.New(ctx, docker.Config{
			InstanceID:       instanceID,
			HostID:           hostID,
			Runtime:          cfg.Sandbox.Docker.Runtime,
			DNS:              cfg.Sandbox.Docker.DNS,
			DefaultImage:     cfg.Sandbox.Image,
			Registry:         cfg.Sandbox.Registry.Repository,
			RegistryUsername: cfg.Sandbox.Registry.Username,
			RegistryPassword: cfg.Sandbox.Registry.Password,
			BrokerPort:       cfg.Server.BrokerPort,
			BrokerHost:       cfg.Sandbox.BrokerHost,
			UmpBinary:        umpbin.Binary,
			EgressFilter:     cfg.Sandbox.EgressFilter,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create the Docker sandbox adapter: %w", err)
		}
		return adapter, nil
	default:
		return nil, fmt.Errorf("unknown sandbox adapter %q", cfg.Sandbox.Adapter)
	}
}

// sandboxInfo exposes the adapter's info on the system endpoint
type sandboxInfo struct {
	adapter sandbox.Adapter
}

func (s sandboxInfo) SandboxInfo(ctx context.Context) (any, error) {
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return s.adapter.Check(checkCtx)
}

// modelResolver serves the jobs module's compile step
type modelResolver struct {
	svc *providers.Service
}

func (m modelResolver) ResolveModel(ctx context.Context, workspaceID, modelID string) (llm.Provider, string, error) {
	p, model, err := m.svc.Resolve(ctx, workspaceID, modelID)
	return p, model.Name, err
}

func (m modelResolver) ModelExists(ctx context.Context, workspaceID, modelID string) (bool, error) {
	return m.svc.ModelExists(ctx, workspaceID, modelID)
}

// runnerModels serves the runner
type runnerModels struct {
	svc *providers.Service
}

func (m runnerModels) Resolve(ctx context.Context, workspaceID, modelID string) (llm.Provider, runner.Model, error) {
	p, model, err := m.svc.Resolve(ctx, workspaceID, modelID)
	if err != nil {
		return nil, runner.Model{}, err
	}
	return p, runner.Model{ID: model.ID, Name: model.Name, Price: model.Price, Caps: model.Caps}, nil
}

// stateStores gives the runner and broker the job state store
type stateStores struct {
	jobs *jobs.Module
}

func (s stateStores) ForJob(workspaceID, jobID string) agent.StateStore {
	return s.jobs.ForJob(workspaceID, jobID)
}

// runsTriggerer lets the runs module start retries through the job actor
type runsTriggerer struct {
	jobs *jobs.Module
}

func (t runsTriggerer) Trigger(ctx context.Context, workspaceID, jobID string, req runs.TriggerRequest) (runs.TriggerResult, error) {
	return t.jobs.Trigger(ctx, workspaceID, jobID, req)
}

// currentDockerfile reads the Dockerfile of a job's current playbook version for rebuilds
type currentDockerfile struct {
	db       *database.DB
	playbook *playbook.Module
}

func (c currentDockerfile) CurrentDockerfile(ctx context.Context, jobID string) (string, error) {
	var version int64
	err := c.db.QueryRowContext(ctx, "SELECT playbook_version FROM jobs WHERE id = $1", jobID).Scan(&version)
	if err != nil {
		return "", err
	}
	content, err := c.playbook.Content(ctx, jobID, version)
	if err != nil || content.Dockerfile == nil {
		return "", err
	}
	return *content.Dockerfile, nil
}

// notifiers tells every interested module about a finished run: the job actor releases its slot, and reflection learns from it
type notifiers []runner.Notifier

func (n notifiers) RunFinished(ctx context.Context, run runner.Run, final runner.Final) {
	for _, notifier := range n {
		notifier.RunFinished(ctx, run, final)
	}
}

// imageVerifier lets reflection build a proposed Dockerfile, or reports that nothing can be built when the adapter has no image builder
func imageVerifier(m *images.Module) reflection.ImageVerifier {
	if m == nil || !m.CanBuild() {
		return nil
	}
	return m
}
