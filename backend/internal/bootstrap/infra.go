package bootstrap

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/italypaleale/francis/builtin/ratelimit"
	"github.com/italypaleale/francis/components/postgres"
	"github.com/italypaleale/francis/host/local"

	"github.com/stonith404/umpteenth/backend/internal/config"
	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/storage"
	"github.com/stonith404/umpteenth/backend/internal/utils/crypto"
)

func initLogger(cfg *config.Config) {
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

// newActorHost creates the embedded Francis host on the app database
// With SQLite it is a single host; with Postgres and HA enabled any number of replicas join the cluster
func newActorHost(cfg *config.Config, db *database.DB, instanceID string) (*local.Host, error) {
	// The runtime PSK derives the cluster CA for host-to-host mTLS, and is tied to this installation
	psk, err := crypto.DeriveKey([]byte(cfg.App.EncryptionKey), "francis-psk/"+instanceID)
	if err != nil {
		return nil, fmt.Errorf("failed to derive actor PSK: %w", err)
	}

	maxHosts := 1
	if cfg.HA.Enabled {
		maxHosts = 0
	}

	opts := []local.HostOption{
		local.WithAddress(net.JoinHostPort(cfg.HA.Actors.Host, strconv.Itoa(cfg.HA.Actors.Port))),
		local.WithLogger(slog.Default().With("scope", "actors")),
		local.WithRuntimePSKs(psk),
		local.WithShutdownGracePeriod(10 * time.Second),
		local.WithMaxHosts(maxHosts),
	}

	// A replica's hostname can resolve to a network other peers are not on, e.g. a sandbox network Docker restores after a crash, so HA replicas listen everywhere
	bind := cfg.HA.Actors.BindAddress
	if bind == "" && cfg.HA.Enabled {
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

// rateLimiter adapts a Francis rate-limit service to the middleware interface
type rateLimiter struct {
	svc *ratelimit.RateLimitService
}

func (r rateLimiter) Allow(ctx context.Context, key string) (bool, time.Duration, error) {
	return r.svc.Allow(ctx, key)
}

// newRateLimiter registers a Francis rate-limit actor, so limits hold across replicas
func newRateLimiter(host *local.Host, name string, rate int, per time.Duration, burst int) (*rateLimiter, error) {
	rl, err := ratelimit.New(name, ratelimit.WithRate(rate), ratelimit.WithPer(per), ratelimit.WithBurst(burst))
	if err != nil {
		return nil, fmt.Errorf("failed to create rate limiter %s: %w", name, err)
	}
	err = host.RegisterBuiltInActor(rl)
	if err != nil {
		return nil, fmt.Errorf("failed to register rate limiter %s: %w", name, err)
	}
	return &rateLimiter{svc: rl.Service(host.Service())}, nil
}
