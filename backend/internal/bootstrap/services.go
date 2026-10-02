package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"time"

	"github.com/italypaleale/francis/builtin/ratelimit"
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
	"github.com/stonith404/umpteenth/backend/internal/mcpapi"
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
	"github.com/stonith404/umpteenth/backend/internal/skills"
	"github.com/stonith404/umpteenth/backend/internal/stats"
	"github.com/stonith404/umpteenth/backend/internal/storage"
	"github.com/stonith404/umpteenth/backend/internal/system"
	"github.com/stonith404/umpteenth/backend/internal/workspaces"
)

type services struct {
	// adapter is nil when sandbox.adapter is none
	adapter sandbox.Adapter

	workspaces    *workspaces.Module
	auth          *auth.Module
	apiTokens     *apitokens.Module
	mcpAPI        *mcpapi.Module
	settings      *settings.Module
	system        *system.Module
	providers     *providers.Module
	playbook      *playbook.Module
	images        *images.Module
	secrets       *secrets.Module
	mcpServers    *mcpservers.Module
	skills        *skills.Module
	runs          *runs.Module
	jobs          *jobs.Module
	stats         *stats.Module
	reflection    *reflection.Module
	notifications *notifications.Module
	broker        *broker.Broker

	loginLimiter *ratelimit.RateLimitService

	// background are the long-running loops started once the actor host is ready
	background []servicerunner.Service
}

// providerFactories lists the LLM adapters of this build; e2etest builds add the fake provider
var providerFactories = map[string]providers.Factory{
	llm.KindAnthropic: newAnthropic,
	llm.KindOpenAI:    newOpenAI,
}

// initServices builds every module in dependency order
func initServices(ctx context.Context, cfg *config.Config, db *database.DB, actors *local.Host, fileStorage storage.FileStorage, instanceID string) (*services, error) {
	svc := &services{}
	hostID := replicaID(cfg.HA.ReplicaID)
	encryptionKey := []byte(cfg.App.EncryptionKey)
	var err error

	// Live notifications cross replicas through Postgres; with SQLite there is only one replica
	var bus events.Bus
	if db.Engine() == database.EnginePostgres {
		bus = events.NewPostgresBus(db.Pool())
	} else {
		bus = events.NewLocalBus()
	}
	svc.background = append(svc.background, bus.Run)
	// Umpteenth's own calls and the sandboxes' egress proxy refuse the same blocked ranges, which Validate already checked
	blocked, err := cfg.Network.BlockedRanges()
	if err != nil {
		return nil, err
	}
	guard := egress.New(cfg.Network.AllowPrivateTargets, blocked...)

	// The registry of this replica's live runs also holds the proxy grants of sandboxes and image builds
	registry := runner.NewRegistry()

	// Rate limiters are actors, so limits hold across replicas
	svc.loginLimiter, err = newRateLimiter(actors, "login", 20, 20)
	if err != nil {
		return nil, err
	}
	webhookLimiter, err := newRateLimiter(actors, "webhook", 60, 30)
	if err != nil {
		return nil, err
	}
	// Compiling a job and testing a stdio MCP server call a model or start a sandbox that no run or spend limit counts, so each person gets a few of each a minute
	expensiveLimiter, err := newRateLimiter(actors, "expensive", 5, 5)
	if err != nil {
		return nil, err
	}

	// With workspaces turned off everyone shares the default workspace, and with them on only a fresh instance gets it, for its first user to own
	svc.workspaces = workspaces.New(workspaces.Dependencies{DB: db, Enabled: cfg.Workspaces.Enabled, AppURL: cfg.App.URL})
	err = svc.workspaces.EnsureDefault(ctx, cfg.Workspaces.Enabled)
	if err != nil {
		return nil, err
	}

	// Settings come before auth, since the session tells every page the unit its workspace shows usage in
	svc.settings = settings.New(settings.Dependencies{DB: db, URLs: guard, Defaults: settings.Defaults{
		Image:         cfg.Sandbox.Image,
		RetentionDays: cfg.Runs.RetentionDays,
	}})
	svc.auth, err = auth.New(auth.Dependencies{
		DB:            db,
		Workspaces:    svc.workspaces,
		Settings:      svc.settings,
		EncryptionKey: encryptionKey,
		Config:        authConfig(cfg),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create auth module: %w", err)
	}
	svc.workspaces.SetSessions(svc.auth)
	svc.apiTokens = apitokens.New(apitokens.Dependencies{DB: db, Roles: svc.workspaces})
	mcpDeps := mcpapi.Dependencies{Tokens: svc.apiTokens, AppURL: cfg.App.URL}
	if id := cfg.MCP.OAuthProvider; id != "" {
		mcpDeps.OAuth = svc.auth
		mcpDeps.OAuthIssuer = cfg.Auth.Providers[id].Issuer
	}
	svc.mcpAPI = mcpapi.New(mcpDeps)

	// Listed GitHub names are tied to their holders in the background, so an unreachable GitHub doesn't hold up the start
	// The service then waits for shutdown, since one that returns stops the others, and test instances skip it since they never sign in through GitHub
	if !cfg.App.Env.IsTest() {
		svc.background = append(svc.background, func(ctx context.Context) error {
			svc.auth.PinGitHubNames(ctx)
			<-ctx.Done()
			return nil
		})
	}

	// The sandbox adapter is chosen once per instance
	svc.adapter, err = initSandboxAdapter(ctx, cfg, instanceID, hostID, guard)
	if err != nil {
		return nil, err
	}
	svc.system = system.New(system.Dependencies{DB: db, AllowPrivateNetworkTargets: cfg.Network.AllowPrivateTargets, SandboxInfo: svc.sandboxInfo})

	svc.providers, err = providers.New(providers.Dependencies{
		DB:                  db,
		EncryptionKey:       encryptionKey,
		Factories:           guardedFactories(providerFactories, guard),
		Egress:              guard,
		Actors:              actors,
		RefreshInterval:     cfg.Models.CatalogRefreshInterval,
		MaintenanceDisabled: cfg.App.Env.IsTest(),
	})
	if err != nil {
		return nil, err
	}
	svc.settings.SetModels(svc.providers.Service())

	// The catalog the last refresh stored replaces the bundled one, so providers added before the next refresh get current models
	err = svc.providers.LoadCatalog(ctx)
	if err != nil {
		slog.WarnContext(ctx, "Failed to load the stored model catalog, using the bundled one", slog.Any("error", err))
	}

	svc.playbook = playbook.New(playbook.Dependencies{DB: db})
	svc.secrets, err = secrets.New(secrets.Dependencies{DB: db, EncryptionKey: encryptionKey})
	if err != nil {
		return nil, err
	}
	svc.mcpServers, err = mcpservers.New(mcpservers.Dependencies{
		DB: db, Egress: guard, Secrets: svc.secrets, Adapter: svc.adapter, EncryptionKey: encryptionKey, AppURL: cfg.App.URL,
		GrantProxy: proxyGranter(registry), TestLimiter: expensiveLimiter,
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

	svc.skills = skills.New(skills.Dependencies{DB: db, Storage: fileStorage, Egress: guard, ImportLimiter: expensiveLimiter})

	svc.runs, err = runs.New(runs.Dependencies{
		DB: db, Actors: actors, Bus: bus, Storage: fileStorage, Adapter: svc.adapter,
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
		Models: modelResolver{svc.providers.Service()}, Secrets: svc.secrets, MCP: svc.mcpServers, Skills: svc.skills,
		SandboxInfo:    svc.sandboxInfo,
		WebhookLimiter: webhookLimiter,
		CompileLimiter: expensiveLimiter,
	})
	if err != nil {
		return nil, err
	}

	// Images are built only when the adapter can build them
	builder, _ := svc.adapter.(sandbox.ImageBuilder)
	svc.images, err = images.New(images.Dependencies{
		DB: db, Actors: actors, Storage: fileStorage, Builder: builder, Registry: cfg.Sandbox.Registry.Repository, GrantProxy: proxyGranter(registry), Egress: guard,
		Jobs: svc.jobs, Playbook: currentDockerfile{svc.playbook},
		MaintenanceDisabled: cfg.App.Env.IsTest(),
	})
	if err != nil {
		return nil, err
	}
	svc.stats = stats.New(stats.Dependencies{DB: db, Jobs: svc.jobs})
	svc.reflection, err = reflection.New(reflection.Dependencies{
		DB: db, Actors: actors, Bus: bus, Storage: fileStorage,
		Jobs: svc.jobs, Models: runnerModels{svc.providers.Service()}, Playbook: svc.playbook, Settings: svc.settings,
		Images:              imageVerifier(svc.images),
		Demotion:            svc.jobs,
		MaintenanceDisabled: cfg.App.Env.IsTest(),
	})
	if err != nil {
		return nil, err
	}
	svc.notifications, err = notifications.New(notifications.Dependencies{
		DB: db, Actors: actors, Settings: svc.settings, Secrets: svc.secrets, Demotion: svc.jobs, Egress: guard, AppURL: cfg.App.URL,
	})
	if err != nil {
		return nil, err
	}

	// Modules the jobs module depends on also call back into it, which closes the cycle once it exists
	svc.runs.SetJobs(svc.jobs, notifiers{svc.jobs, svc.notifications})
	svc.playbook.SetDependencies(svc.jobs, svc.images)
	svc.secrets.SetJobs(svc.jobs)
	svc.mcpServers.SetJobs(svc.jobs)
	svc.skills.SetJobs(svc.jobs)
	svc.workspaces.SetCleanup(workspaceCleanup{jobs: svc.jobs, runs: svc.runs, images: svc.images, skills: svc.skills})

	// The runner executes runs delivered by the taskpool, and the broker serves them to the ump CLI
	if svc.adapter != nil {
		r := runner.New(runner.Deps{
			DB: db, Runs: svc.runs.Store(), Jobs: svc.jobs, Models: runnerModels{svc.providers.Service()},
			State: stateStores{svc.jobs}, Adapter: svc.adapter, Images: svc.images,
			Tools:    []runner.ToolProvider{svc.mcpServers},
			Skills:   svc.skills,
			Notifier: notifiers{svc.jobs, svc.reflection, svc.notifications}, Cancel: svc.runs, Live: registry, Bus: bus, Storage: fileStorage,
			HostID: hostID,
			UtilityModel: func(ctx context.Context, wid string) (string, error) {
				ws, err := svc.settings.Get(ctx, wid)
				if err != nil || ws.UtilityModelID == nil {
					return "", err
				}
				return *ws.UtilityModelID, nil
			},
		})
		svc.runs.SetRunner(r)
		svc.reflection.SetMainTester(r)

		// Per-replica maintenance of host-local resources, the one deliberate exception to Francis cron jobs
		svc.background = append(svc.background, svc.sandboxMaintenance)
	}
	// The broker's egress proxy keeps sandboxes off the backend's containers, which this process can reach
	var containerAddress func(netip.Addr) bool
	if c, ok := svc.adapter.(sandbox.Containers); ok {
		containerAddress = c.ContainerAddress
	}
	svc.broker = broker.New(broker.Dependencies{Runs: svc.runs, Live: registry, State: stateStores{svc.jobs}, Blocked: blocked, ContainerAddress: containerAddress})

	return svc, nil
}

// sandboxFacing returns the adapter's check for this replica's addresses on sandbox networks, or nil when it attaches nothing to them
func (s *services) sandboxFacing() func(netip.Addr) bool {
	if f, ok := s.adapter.(sandbox.SandboxFacing); ok {
		return f.SandboxFacing
	}
	return nil
}

// sandboxInfo reports the active adapter for the networks jobs may pick and the system endpoint
func (s *services) sandboxInfo(ctx context.Context) (sandbox.Info, error) {
	if s.adapter == nil {
		return sandbox.Info{}, errors.New("no sandbox adapter")
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
		s.images.PruneLocal(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}

		// Host-level state such as networks a crash left behind is cleaned up on every tick
		if maintainer != nil {
			if err := maintainer.Maintain(ctx); err != nil {
				slog.WarnContext(ctx, "Failed to maintain the sandbox backend", slog.Any("error", err))
			}
		}
	}
}

// close releases what the modules hold outside the database, after every service has stopped
func (s *services) close() {
	if s.adapter != nil {
		_ = s.adapter.Close()
	}
}

// authConfig hands the sign-in providers and the passkey option of the config to the auth module
func authConfig(cfg *config.Config) auth.Config {
	providers := make([]auth.ProviderConfig, 0, len(cfg.Auth.Providers))
	for id, p := range cfg.Auth.Providers {
		providers = append(providers, auth.ProviderConfig{
			ID:                   id,
			Type:                 p.Type,
			Name:                 p.Name,
			Icon:                 p.Icon,
			Primary:              p.Primary,
			ClientID:             p.ClientID,
			ClientSecret:         p.ClientSecret,
			Issuer:               p.Issuer,
			AllowedGroups:        p.AllowedGroups,
			AdminGroups:          p.AdminGroups,
			AllowedUsers:         p.AllowedUsers,
			AllowedOrganizations: p.AllowedOrganizations,
			AdminUsers:           p.AdminUsers,
			AdminOrganizations:   p.AdminOrganizations,
		})
	}
	c := auth.Config{AppURL: cfg.App.URL, Providers: providers, Passkeys: cfg.Auth.Passkeys.Enabled}

	// MCP clients sign in with access tokens issued for the MCP endpoint, which the provider names as their audience
	if id := cfg.MCP.OAuthProvider; id != "" {
		c.AccessTokens = &auth.AccessTokenConfig{ProviderID: id, Audience: mcpapi.ResourceURL(cfg.App.URL)}
	}
	return c
}
