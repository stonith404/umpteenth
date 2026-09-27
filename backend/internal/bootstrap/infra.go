package bootstrap

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"slices"
	"strconv"
	"time"

	"github.com/italypaleale/francis/builtin/ratelimit"
	"github.com/italypaleale/francis/components"
	"github.com/italypaleale/francis/components/postgres"
	"github.com/italypaleale/francis/components/sqlite"
	"github.com/italypaleale/francis/host/local"

	"github.com/stonith404/umpteenth/backend/internal/config"
	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/storage"
	"github.com/stonith404/umpteenth/backend/internal/utils/crypto"
)

func initLogger(cfg *config.Config) {
	// Validate already rejected unknown levels
	var level slog.Level
	_ = level.UnmarshalText([]byte(cfg.Log.Level))

	opts := &slog.HandlerOptions{Level: level}
	var handler slog.Handler = slog.NewTextHandler(os.Stderr, opts)
	if cfg.Log.JSON {
		handler = slog.NewJSONHandler(os.Stderr, opts)
	}
	slog.SetDefault(slog.New(handler))
}

func initStorage(ctx context.Context, cfg *config.Config, db *database.DB) (storage.FileStorage, error) {
	switch cfg.FileStorage.Backend {
	case config.FileBackendS3:
		return storage.NewS3Storage(ctx, storage.S3Config{
			Endpoint:        cfg.FileStorage.S3.Endpoint,
			Region:          cfg.FileStorage.S3.Region,
			Bucket:          cfg.FileStorage.S3.Bucket,
			AccessKeyID:     cfg.FileStorage.S3.AccessKeyID,
			SecretAccessKey: cfg.FileStorage.S3.SecretAccessKey,
			UseSSL:          cfg.FileStorage.S3.UseSSL,
		})
	case config.FileBackendDatabase:
		return storage.NewDatabaseStorage(db), nil
	default:
		return storage.NewFilesystemStorage(cfg.FileStorage.Path)
	}
}

// clustered reports whether several replicas may share the database, which only Postgres allows, since every replica has to reach it over the network
func clustered(db *database.DB) bool {
	return db.Engine() == database.EnginePostgres
}

// newActorHost creates the embedded Francis host on the app database
// With SQLite it is a single host; with Postgres every replica on the same database joins the cluster
func newActorHost(cfg *config.Config, db *database.DB, instanceID string) (*local.Host, error) {
	// The runtime PSK derives the cluster CA for host-to-host mTLS, and is tied to this installation
	psk, err := crypto.DeriveKey([]byte(cfg.App.EncryptionKey), "francis-psk/"+instanceID)
	if err != nil {
		return nil, fmt.Errorf("failed to derive actor PSK: %w", err)
	}

	maxHosts := 1
	if clustered(db) {
		maxHosts = 0
	}

	opts := []local.HostOption{
		local.WithAddress(actorHostAddress(cfg)),
		local.WithLogger(slog.Default().With("scope", "actors")),
		local.WithRuntimePSKs(psk),
		local.WithShutdownGracePeriod(10 * time.Second),
		local.WithMaxHosts(maxHosts),
	}

	// A replica's hostname can resolve to a network other peers are not on, e.g. a sandbox network Docker restores after a crash, so replicas others can reach listen everywhere
	bind := cfg.HA.Actors.BindAddress
	if bind == "" && clustered(db) && !isLoopback(cfg.HA.Actors.Host) {
		bind = "0.0.0.0"
	}
	if bind != "" {
		opts = append(opts, local.WithBindAddress(bind))
	}

	switch db.Engine() {
	case database.EnginePostgres:
		opts = append(opts, local.WithPostgresProvider(postgres.PostgresProviderOptions{DB: db.Pool()}))
	default:
		opts = append(opts, local.WithSQLiteProvider(local.SQLiteProviderOptions{DB: db.Raw()}))
	}

	host, err := local.NewHost(opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create actor host: %w", err)
	}
	return host, nil
}

// isLoopback reports whether host is a loopback address or localhost, which no other replica can reach
func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsLoopback()
}

// actorHostAddress is the address the actor host registers under, which peers reach it at
func actorHostAddress(cfg *config.Config) string {
	return net.JoinHostPort(cfg.HA.Actors.Host, strconv.Itoa(cfg.HA.Actors.Port))
}

// awaitDeadActorHost waits until the actor host registration of a previous process that died without unregistering, e.g. after an OOM kill, a panic or SIGKILL, has expired
// Francis counts it as live until its health check deadline passes and registers a host only once, so registering sooner fails the start with ErrClusterFull on SQLite or ErrHostAlreadyRegistered for a Postgres replica that keeps its address
// A live host keeps its registration fresh, so the wait ends after one deadline and registering then fails as it should
// It returns the other live replicas, so the caller can report the size of the cluster it joins
func awaitDeadActorHost(ctx context.Context, cfg *config.Config, db *database.DB) ([]components.HostInfo, error) {
	// The host's own provider is private, so a separate one on the same database lists the registered hosts
	logger := slog.Default().With("scope", "actors")
	var provider components.ActorProvider
	var err error
	switch db.Engine() {
	case database.EnginePostgres:
		provider, err = postgres.NewPostgresProvider(logger, postgres.PostgresProviderOptions{DB: db.Pool()}, components.NewProviderConfig())
	default:
		provider, err = sqlite.NewSQLiteProvider(logger, sqlite.SQLiteProviderOptions{DB: db.Raw()}, components.NewProviderConfig())
	}
	if err != nil {
		return nil, fmt.Errorf("failed to create actor provider: %w", err)
	}
	defer func() { _ = provider.Close() }()

	// Initializing applies the Francis migrations, so a new database has a hosts table to list
	err = provider.Init(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to init actor provider: %w", err)
	}

	// On SQLite the cluster admits a single host, so any live registration is in the way, while a Postgres replica only collides with one at its own address
	address := actorHostAddress(cfg)
	blocks := func(h components.HostInfo) bool {
		return !clustered(db) || h.Address == address
	}

	// A dead registration drops out of the list within one deadline, so one that is still listed after that belongs to a live host
	giveUp := time.Now().Add(components.DefaultHostHealthCheckDeadline + time.Second)
	waiting := false
	for {
		hosts, err := provider.ListHosts(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list actor hosts: %w", err)
		}
		if !slices.ContainsFunc(hosts, blocks) || time.Now().After(giveUp) {
			return hosts, nil
		}
		if !waiting {
			logger.InfoContext(ctx, "Waiting for the actor host registration of a previous process to expire")
			waiting = true
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// newRateLimiter registers a Francis rate-limit actor, so limits hold across replicas
// It admits rate requests per minute, and up to burst of them at once
func newRateLimiter(host *local.Host, name string, rate, burst int) (*ratelimit.RateLimitService, error) {
	rl, err := ratelimit.New(name, ratelimit.WithRate(rate), ratelimit.WithPer(time.Minute), ratelimit.WithBurst(burst))
	if err != nil {
		return nil, fmt.Errorf("failed to create rate limiter %s: %w", name, err)
	}
	err = host.RegisterBuiltInActor(rl)
	if err != nil {
		return nil, fmt.Errorf("failed to register rate limiter %s: %w", name, err)
	}
	return rl.Service(host.Service()), nil
}
