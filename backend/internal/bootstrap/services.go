package bootstrap

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/italypaleale/francis/host/local"
	"github.com/italypaleale/go-kit/servicerunner"

	"github.com/stonith404/umpteenth/backend/internal/apitokens"
	"github.com/stonith404/umpteenth/backend/internal/auth"
	"github.com/stonith404/umpteenth/backend/internal/broker"
	"github.com/stonith404/umpteenth/backend/internal/config"
	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/egress"
	"github.com/stonith404/umpteenth/backend/internal/events"
	"github.com/stonith404/umpteenth/backend/internal/images"
	"github.com/stonith404/umpteenth/backend/internal/jobs"
	"github.com/stonith404/umpteenth/backend/internal/llm"
	"github.com/stonith404/umpteenth/backend/internal/mcpservers"
	"github.com/stonith404/umpteenth/backend/internal/notifications"
	"github.com/stonith404/umpteenth/backend/internal/playbook"
	"github.com/stonith404/umpteenth/backend/internal/providers"
	"github.com/stonith404/umpteenth/backend/internal/reflection"
	"github.com/stonith404/umpteenth/backend/internal/runner"
	"github.com/stonith404/umpteenth/backend/internal/runs"
	"github.com/stonith404/umpteenth/backend/internal/sandbox"
	"github.com/stonith404/umpteenth/backend/internal/secrets"
	"github.com/stonith404/umpteenth/backend/internal/settings"
	"github.com/stonith404/umpteenth/backend/internal/stats"
	"github.com/stonith404/umpteenth/backend/internal/storage"
	"github.com/stonith404/umpteenth/backend/internal/system"
	"github.com/stonith404/umpteenth/backend/internal/workspaces"
)

type services struct {
	db          *database.DB
	actors      *local.Host
	fileStorage storage.FileStorage
	instanceID  string
	hostID      string
	bus         events.Bus
	egress      *egress.Guard
	adapter     sandbox.Adapter

	workspaces    *workspaces.Module
	auth          *auth.Module
	apiTokens     *apitokens.Module
	settings      *settings.Module
	system        *system.Module
	providers     *providers.Module
	playbook      *playbook.Module
	images        *images.Module
	secrets       *secrets.Module
	mcpServers    *mcpservers.Module
	runs          *runs.Module
	jobs          *jobs.Module
	stats         *stats.Module
	reflection    *reflection.Module
	notifications *notifications.Module
	runner        *runner.Runner
	broker        *broker.Broker

	loginLimiter   *rateLimiter
	webhookLimiter *rateLimiter

	background []servicerunner.Service
	closers    []func()

	// Test-only wiring, used by e2etest builds
	resetHooks []func(ctx context.Context) error //nolint:unused // read by the e2etest build
	testRoutes []func(api huma.API)              //nolint:unused // read by the e2etest build
}

// providerFactories lists the LLM adapters of this build; e2etest builds add the fake provider
var providerFactories = map[string]providers.Factory{
	llm.KindAnthropic: newAnthropic,
	llm.KindOpenAI:    newOpenAI,
}

// initServices builds every module in dependency order
func initServices(ctx context.Context, cfg *config.Config, db *database.DB, actors *local.Host, fileStorage storage.FileStorage, instanceID string) (*services, error) {
	svc := &services{db: db, actors: actors, fileStorage: fileStorage, instanceID: instanceID, hostID: replicaID(cfg.HA.ReplicaID)}
	encryptionKey := []byte(cfg.App.EncryptionKey)
	var err error

	// Live notifications cross replicas through Postgres; with SQLite there is only one replica
	if db.Engine() == database.EnginePostgres {
		svc.bus = events.NewPostgresBus(db.Pool())
	} else {
		svc.bus = events.NewLocalBus()
	}
	svc.background = append(svc.background, svc.bus.Run)
	svc.egress = egress.New(cfg.Network.AllowPrivateTargets)

	// Rate limiters are actors, so limits hold across replicas
	svc.loginLimiter, err = newRateLimiter(actors, "login", 20, time.Minute, 20)
	if err != nil {
		return nil, err
	}
	svc.webhookLimiter, err = newRateLimiter(actors, "webhook", 60, time.Minute, 30)
	if err != nil {
		return nil, err
	}

	// v1 has one workspace, created on first start
	svc.workspaces = workspaces.New(workspaces.Dependencies{DB: db})
	defaultWorkspace, err := svc.workspaces.EnsureDefault(ctx)
	if err != nil {
		return nil, err
	}

	svc.auth, err = auth.New(auth.Dependencies{
		DB:            db,
		Workspaces:    svc.workspaces,
		EncryptionKey: encryptionKey,
		Config: auth.Config{
			AppURL:        cfg.App.URL,
			Issuer:        cfg.OIDC.Issuer,
			ClientID:      cfg.OIDC.ClientID,
			ClientSecret:  cfg.OIDC.ClientSecret,
			AllowedGroups: cfg.OIDC.AllowedGroups,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create auth module: %w", err)
	}
	svc.apiTokens = apitokens.New(apitokens.Dependencies{DB: db})
	svc.settings = settings.New(settings.Dependencies{DB: db, Defaults: settings.Defaults{
		Image:           cfg.Sandbox.Image,
		DailySpendLimit: cfg.Runs.DailySpendLimitUSD,
		RetentionDays:   cfg.Runs.RetentionDays,
	}})
	svc.system = system.New(system.Dependencies{DB: db, AllowPrivateNetworkTargets: cfg.Network.AllowPrivateTargets})

	// The sandbox adapter is chosen once per instance
	svc.adapter, err = initSandboxAdapter(ctx, cfg, instanceID, svc.hostID)
	if err != nil {
		return nil, err
	}
	if svc.adapter != nil {
		svc.system.SetSandbox(sandboxInfo{svc.adapter})
		svc.closers = append(svc.closers, func() { _ = svc.adapter.Close() })
	}

	svc.providers, err = providers.New(providers.Dependencies{
		DB:                  db,
		EncryptionKey:       encryptionKey,
		Factories:           guardedFactories(providerFactories, svc.egress),
		Settings:            svc.settings,
		Egress:              svc.egress,
		Actors:              actors,
		RefreshInterval:     cfg.Models.CatalogRefreshInterval,
		MaintenanceDisabled: cfg.App.Env.IsTest(),
		DefaultAgentModel:   llm.DefaultAgentModel,
		DefaultUtilityModel: llm.DefaultUtilityModel,
		SeedAPIKeys:         seedAPIKeys(cfg.Providers),
	})
	if err != nil {
		return nil, err
	}
	svc.settings.SetModels(svc.providers.Service())

	// The catalog the last refresh stored replaces the bundled one before the first workspace is seeded from it
	err = svc.providers.LoadCatalog(ctx)
	if err != nil {
		slog.WarnContext(ctx, "Failed to load the stored model catalog, using the bundled one", slog.Any("error", err))
	}
	err = svc.providers.SeedDefaults(ctx, defaultWorkspace)
	if err != nil {
		return nil, fmt.Errorf("failed to seed providers: %w", err)
	}

	svc.playbook = playbook.New(playbook.Dependencies{DB: db})
	var builder sandbox.ImageBuilder
	if b, ok := svc.adapter.(sandbox.ImageBuilder); ok {
		builder = b
	}
	svc.images, err = images.New(images.Dependencies{
		DB: db, Actors: actors, Storage: fileStorage, Builder: builder, Registry: cfg.Sandbox.Registry.Repository,
		MaintenanceDisabled: cfg.App.Env.IsTest(),
	})
	if err != nil {
		return nil, err
	}
	svc.secrets, err = secrets.New(secrets.Dependencies{DB: db, EncryptionKey: encryptionKey})
	if err != nil {
		return nil, err
	}
	svc.mcpServers, err = mcpservers.New(mcpservers.Dependencies{
		DB: db, Egress: svc.egress, Secrets: svc.secrets, Adapter: svc.adapter, EncryptionKey: encryptionKey, AppURL: cfg.App.URL,
		DefaultImage: func(ctx context.Context, wid string) string {
			ws, err := svc.settings.Get(ctx, wid)
			if err != nil {
				return cfg.Sandbox.Image
			}
			return ws.DefaultImage
		},
	})
	if err != nil {
		return nil, err
	}

	svc.runs, err = runs.New(runs.Dependencies{
		DB: db, Actors: actors, Bus: svc.bus, Storage: fileStorage, Adapter: svc.adapter,
		MaxConcurrentRuns:   cfg.Runs.MaxConcurrent,
		MaintenanceDisabled: cfg.App.Env.IsTest(),
		RetentionDays: func(ctx context.Context, wid string) int {
			ws, err := svc.settings.Get(ctx, wid)
			if err != nil {
				return cfg.Runs.RetentionDays
			}
			return ws.RetentionDays
		},
	})
	if err != nil {
		return nil, err
	}

	svc.jobs, err = jobs.New(jobs.Dependencies{
		DB: db, Actors: actors, Runs: svc.runs, Playbooks: svc.playbook, Settings: svc.settings,
		Models: modelResolver{svc.providers.Service()}, Secrets: svc.secrets, MCP: svc.mcpServers,
		SandboxInfo:    svc.sandboxInfo,
		WebhookLimiter: svc.webhookLimiter,
	})
	if err != nil {
		return nil, err
	}
	svc.stats = stats.New(stats.Dependencies{DB: db})
	svc.reflection, err = reflection.New(reflection.Dependencies{
		DB: db, Actors: actors, Bus: svc.bus, Storage: fileStorage,
		Jobs: svc.jobs, Models: runnerModels{svc.providers.Service()}, Playbook: svc.playbook, Settings: svc.settings,
		Images:              imageVerifier(svc.images),
		Demotion:            svc.jobs,
		MaintenanceDisabled: cfg.App.Env.IsTest(),
	})
	if err != nil {
		return nil, err
	}
	svc.notifications, err = notifications.New(notifications.Dependencies{
		DB: db, Actors: actors, Settings: svc.settings, Secrets: svc.secrets, Demotion: svc.jobs, Egress: svc.egress, AppURL: cfg.App.URL,
	})
	if err != nil {
		return nil, err
	}

	// Modules that reference each other are wired once all of them exist
	svc.runs.SetJobs(runsTriggerer{svc.jobs}, notifiers{svc.jobs, svc.notifications})
	svc.settings.SetURLChecker(svc.egress)
	svc.playbook.SetDependencies(svc.jobs, svc.images)
	svc.images.SetDependencies(svc.jobs, currentDockerfile{db: db, playbook: svc.playbook})
	svc.secrets.SetJobs(svc.jobs)
	svc.mcpServers.SetJobs(svc.jobs)
	svc.stats.SetJobs(svc.jobs)

	// The runner executes runs delivered by the taskpool, and the broker serves them to the ump CLI
	registry := runner.NewRegistry()
	if svc.adapter != nil {
		svc.runner = runner.New(runner.Deps{
			DB: db, Runs: svc.runs.Store(), Jobs: svc.jobs, Models: runnerModels{svc.providers.Service()},
			State: stateStores{svc.jobs}, Adapter: svc.adapter, Images: svc.images,
			Tools:    []runner.ToolProvider{svc.mcpServers},
			Notifier: notifiers{svc.jobs, svc.reflection, svc.notifications}, Cancel: svc.runs, Live: registry, Bus: svc.bus, Storage: fileStorage,
			HostID: svc.hostID, BrokerPort: cfg.Server.BrokerPort,
			UtilityModel: func(ctx context.Context, wid string) (string, error) {
				ws, err := svc.settings.Get(ctx, wid)
				if err != nil || ws.UtilityModelID == nil {
					return "", err
				}
				return *ws.UtilityModelID, nil
			},
		})
		svc.runs.SetRunner(svc.runner)
		svc.reflection.SetMainTester(svc.runner)
	}
	svc.broker = broker.New(broker.Dependencies{Runs: svc.runs, Live: registry, State: stateStores{svc.jobs}})

	// Per-replica maintenance of host-local resources, the one deliberate exception to Francis cron jobs (PLAN.md §3.3)
	if svc.adapter != nil {
		svc.background = append(svc.background, svc.sandboxMaintenance)
	}
	return svc, nil
}

// sandboxFacing returns the adapter's check for this replica's addresses on sandbox networks, or nil when it attaches nothing to them
func (s *services) sandboxFacing() func(netip.Addr) bool {
	if f, ok := s.adapter.(sandbox.SandboxFacing); ok {
		return f.SandboxFacing
	}
	return nil
}

// sandboxInfo reports the active adapter for jobs' capability warnings
func (s *services) sandboxInfo(ctx context.Context) (sandbox.Info, error) {
	if s.adapter == nil {
		return sandbox.Info{}, fmt.Errorf("no sandbox adapter")
	}
	return s.adapter.Check(ctx)
}

// sandboxMaintenance prepares the backend, then reaps orphaned sandboxes on this replica's backend
func (s *services) sandboxMaintenance(ctx context.Context) error {
	err := s.adapter.Prepare(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "Failed to prepare the sandbox backend", slog.Any("error", err))
	}

	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	maintainer, _ := s.adapter.(sandbox.Maintainer)
	for {
		s.runs.ReapSandboxes(ctx)
		if s.images != nil {
			s.images.PruneLocal(ctx)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}

		// Host-level state such as the egress firewall is repaired on every tick, since an engine restart drops it; Prepare set it up already
		if maintainer != nil {
			if err := maintainer.Maintain(ctx); err != nil {
				slog.WarnContext(ctx, "Failed to maintain the sandbox backend", slog.Any("error", err))
			}
		}
	}
}

// backgroundServices are long-running loops started once the actor host is ready
func (s *services) backgroundServices() []servicerunner.Service {
	return s.background
}

func (s *services) close() {
	for i := len(s.closers) - 1; i >= 0; i-- {
		s.closers[i]()
	}
}
