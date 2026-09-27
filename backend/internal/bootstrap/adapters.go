package bootstrap

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/config"
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
	"github.com/stonith404/umpteenth/backend/internal/sandbox/kubernetes"
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

// proxyGranter lets modules without a run open the egress proxy for a sandbox or build of their own, such as an image build or an MCP server test
func proxyGranter(registry *runner.Registry) func(token string, network sandbox.NetworkPolicy) (revoke func()) {
	return func(token string, network sandbox.NetworkPolicy) func() {
		return registry.GrantProxy(token, &runner.ProxyGrant{Network: network})
	}
}

var nonNameChars = regexp.MustCompile(`[^a-zA-Z0-9_.-]`)

// replicaID names this replica; the hostname is stable across restarts, which lets the Docker adapter reuse its relay container
func replicaID(configured string) string {
	if configured != "" {
		return nonNameChars.ReplaceAllString(configured, "-")
	}
	host, _ := os.Hostname()
	host = strings.Trim(nonNameChars.ReplaceAllString(host, "-"), "-.")
	if host == "" {
		return "replica"
	}
	return host
}

// initSandboxAdapter builds the adapter selected by sandbox.adapter, or returns nil for none
func initSandboxAdapter(ctx context.Context, cfg *config.Config, instanceID, hostID string) (sandbox.Adapter, error) {
	switch cfg.Sandbox.Adapter {
	case config.SandboxAdapterNone:
		return nil, nil
	case config.SandboxAdapterDocker:
		adapter, err := docker.New(ctx, docker.Config{
			InstanceID:        instanceID,
			HostID:            hostID,
			Runtime:           cfg.Sandbox.Docker.Runtime,
			DNS:               cfg.Sandbox.Docker.DNS,
			DefaultImage:      cfg.Sandbox.Image,
			Registry:          cfg.Sandbox.Registry.Repository,
			RegistryUsername:  cfg.Sandbox.Registry.Username,
			RegistryPassword:  cfg.Sandbox.Registry.Password,
			BrokerPort:        cfg.Server.BrokerPort,
			BrokerHost:        cfg.Sandbox.BrokerHost,
			AllowUnrestricted: cfg.Sandbox.AllowUnrestrictedNetwork,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create the Docker sandbox adapter: %w", err)
		}
		return adapter, nil
	case config.SandboxAdapterKubernetes:
		k := cfg.Sandbox.Kubernetes
		clusterRanges, err := k.ClusterPrefixes()
		if err != nil {
			return nil, err
		}
		adapter, err := kubernetes.New(ctx, kubernetes.Config{
			InstanceID:           instanceID,
			HostID:               hostID,
			Kubeconfig:           k.Kubeconfig,
			Namespace:            k.Namespace,
			DefaultImage:         cfg.Sandbox.Image,
			BootstrapImage:       k.BootstrapImage,
			BuildImage:           k.BuildImage,
			BuildkitAddress:      k.BuildkitAddress,
			RuntimeClass:         k.RuntimeClass,
			NodeSelector:         k.NodeSelector,
			Tolerations:          k.Tolerations,
			Registry:             cfg.Sandbox.Registry.Repository,
			RegistryUsername:     cfg.Sandbox.Registry.Username,
			RegistryPassword:     cfg.Sandbox.Registry.Password,
			InsecureRegistry:     k.InsecureRegistry,
			ClusterRanges:        clusterRanges,
			BrokerHost:           cfg.Sandbox.BrokerHost,
			BrokerPort:           cfg.Server.BrokerPort,
			AllowUnrestricted:    cfg.Sandbox.AllowUnrestrictedNetwork,
			RequireNetworkPolicy: k.RequireNetworkPolicy,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create the Kubernetes sandbox adapter: %w", err)
		}
		return adapter, nil
	default:
		return nil, fmt.Errorf("unknown sandbox adapter %q", cfg.Sandbox.Adapter)
	}
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

func (s stateStores) ForJob(jobID string) runner.JobState {
	return s.jobs.ForJob(jobID)
}

// currentDockerfile reads the Dockerfile of a job's current playbook version for rebuilds
type currentDockerfile struct {
	playbook *playbook.Module
}

func (c currentDockerfile) CurrentDockerfile(ctx context.Context, jobID string) (string, error) {
	_, content, err := c.playbook.Current(ctx, jobID)
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
	if !m.CanBuild() {
		return nil
	}
	return m
}

// workspaceCleanup releases what a workspace holds outside the rows that cascade with it
type workspaceCleanup struct {
	jobs   *jobs.Module
	runs   *runs.Module
	images *images.Module
}

func (c workspaceCleanup) BeforeDelete(ctx context.Context, workspaceID string) (func(context.Context), error) {
	// Runs in flight hold sandboxes and would keep writing into a workspace that is gone
	active, err := c.runs.ActiveRuns(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	switch {
	case active == 1:
		return nil, apperror.Conflict("The workspace has an active run, wait for it to finish or cancel it first")
	case active > 1:
		return nil, apperror.Conflict(fmt.Sprintf("The workspace has %d active runs, wait for them to finish or cancel them first", active))
	}

	// Schedules live in the job actors, which don't go away with the rows
	// An alarm that survives is harmless, since it finds no job when it fires
	ids, err := c.jobs.ListJobIDs(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		if err := c.jobs.Forget(ctx, id); err != nil {
			slog.WarnContext(ctx, "Failed to release the schedule of a deleted workspace's job", slog.String("job", id), slog.Any("error", err))
		}
	}

	// Files can only go once nothing refers to them anymore
	afterRuns, err := c.runs.ReleaseWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	afterImages, err := c.images.ReleaseWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context) {
		afterRuns(ctx)
		afterImages(ctx)
	}, nil
}
