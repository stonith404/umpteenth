// Package bootstrap wires every module together and runs the application; it is the only package that knows all modules
package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"time"

	"github.com/italypaleale/francis/components"
	francishost "github.com/italypaleale/francis/host"
	"github.com/italypaleale/go-kit/servicerunner"

	"github.com/stonith404/umpteenth/backend/internal/common"
	"github.com/stonith404/umpteenth/backend/internal/config"
	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/httpserver"
	"github.com/stonith404/umpteenth/backend/internal/instanceid"
)

// Bootstrap starts the application with the loaded configuration and blocks until ctx is canceled
func Bootstrap(ctx context.Context, cfg *config.Config) error {
	initLogger(cfg)
	slog.InfoContext(ctx, "Umpteenth is starting", slog.String("version", common.Version), slog.String("database", string(cfg.Database.Provider())))

	// Open the database and bring the schema up to date
	db, err := database.Open(ctx, database.Engine(cfg.Database.Provider()), cfg.Database.ConnectionString)
	if err != nil {
		return err
	}
	defer db.Close()

	err = database.Migrate(ctx, db)
	if err != nil {
		return err
	}

	// The instance ID scopes sandbox ownership and derives the actor cluster key
	instanceID, err := instanceid.Load(ctx, db)
	if err != nil {
		return err
	}

	// Blob storage must be shared between replicas in HA mode
	fileStorage, err := initStorage(ctx, cfg, db)
	if err != nil {
		return fmt.Errorf("failed to initialize file storage: %w", err)
	}
	defer fileStorage.Close()

	// The actor host is created first so modules can register their actors before it runs
	actors, err := newActorHost(cfg, db, instanceID)
	if err != nil {
		return err
	}

	svc, err := initServices(ctx, cfg, db, actors, fileStorage, instanceID)
	if err != nil {
		return err
	}
	defer svc.close()

	_, handler, err := initRouter(cfg, db, svc)
	if err != nil {
		return err
	}

	// Everything that talks to actors waits until the actor host is ready
	actorsRun, actorsReady := actorsRunService(cfg, db, actors)
	services := []servicerunner.Service{
		actorsRun,
		// The API refuses connections that arrive on this replica's addresses on sandbox networks, so sandboxes reach only the broker
		actorsReady.Await(httpService(net.JoinHostPort(cfg.Server.Host, strconv.Itoa(cfg.Server.Port)), handler, refusing(svc.sandboxFacing()))),
		// The broker listens separately, so the public API and the sandbox-facing API can be exposed differently, and shares its port with the SOCKS5 proxy
		actorsReady.Await(httpService(net.JoinHostPort(cfg.Server.Host, strconv.Itoa(cfg.Server.BrokerPort)), svc.broker.Handler(), svc.broker.Listener)),
	}
	for _, bg := range svc.background {
		services = append(services, actorsReady.Await(bg))
	}

	// The runner already drops the context.Canceled every service returns on shutdown
	err = servicerunner.NewServiceRunner(services...).Run(ctx)
	if err != nil {
		return fmt.Errorf("failed to run services: %w", err)
	}
	return nil
}

// actorsRunService runs the actor host as a background service and returns a ready signal other services can wait on
// It always waits for the host to return, since the host still unregisters itself from the database after ctx is canceled
func actorsRunService(cfg *config.Config, db *database.DB, actors francishost.Host) (servicerunner.Service, *servicerunner.Ready) {
	ready := servicerunner.NewReady()
	fn := func(ctx context.Context) error {
		// The host registers only once, so a registration its crashed predecessor left behind has to expire first
		others, err := awaitDeadActorHost(ctx, cfg, db)
		if err != nil {
			return err
		}

		runErr := make(chan error, 1)
		go func() {
			runErr <- actors.Run(ctx)
		}()

		select {
		case <-actors.Ready():
			ready.Signal()
		case err := <-runErr:
			return explainRegistrationError(cfg, err)
		}

		// Replicas join a cluster just by sharing a Postgres database, so its size is logged to make an unexpected member visible
		if clustered(db) {
			slog.InfoContext(ctx, "Joined the replica cluster", slog.Int("replicas", len(others)+1), slog.String("address", actorHostAddress(cfg)))
		}
		return <-runErr
	}
	return fn, ready
}

// explainRegistrationError turns the errors of an actor host that can't join the cluster into what to change
func explainRegistrationError(cfg *config.Config, err error) error {
	switch {
	case errors.Is(err, components.ErrClusterFull):
		return fmt.Errorf("another Umpteenth process is already using this SQLite database, and running more than one replica needs a Postgres database.connection_string (%s)", config.EnvName("database.connection_string"))
	case errors.Is(err, components.ErrHostAlreadyRegistered):
		return fmt.Errorf("another replica is already registered at %s, so give every replica its own ha.actors.host (%s), the address the other replicas reach it at", actorHostAddress(cfg), config.EnvName("ha.actors.host"))
	default:
		return err
	}
}

// httpService serves the handler on addr until ctx is canceled, on the listener wrap returns when it is set
func httpService(addr string, handler http.Handler, wrap func(net.Listener) net.Listener) servicerunner.Service {
	return func(ctx context.Context) error {
		srv := &http.Server{
			Addr:              addr,
			Handler:           handler,
			ReadHeaderTimeout: 10 * time.Second,
			// Idle keep-alive connections are closed, so a sandbox can't pile them up on the broker until the process runs out of file descriptors
			IdleTimeout:    2 * time.Minute,
			MaxHeaderBytes: 1 << 20,
		}
		// The SSE streams never finish on their own, so they end when shutdown starts instead of holding it until its deadline
		httpserver.EndStreamsOnShutdown(srv)
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			return fmt.Errorf("server on %s failed: %w", addr, err)
		}
		if wrap != nil {
			ln = wrap(ln)
		}

		errCh := make(chan error, 1)
		go func() {
			slog.Info("Server listening", slog.String("addr", addr))
			err := srv.Serve(ln)
			if !errors.Is(err, http.ErrServerClosed) {
				errCh <- err
			}
			close(errCh)
		}()

		select {
		case err := <-errCh:
			return fmt.Errorf("server on %s failed: %w", addr, err)
		case <-ctx.Done():
		}

		// The parent context is already canceled, so shutdown gets its own deadline
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx) //nolint:contextcheck
	}
}

// refusing wraps a listener in a filteredListener, or leaves it alone when there is nothing to refuse
func refusing(refuse func(netip.Addr) bool) func(net.Listener) net.Listener {
	if refuse == nil {
		return nil
	}
	return func(ln net.Listener) net.Listener {
		return filteredListener{Listener: ln, refuse: refuse}
	}
}

// filteredListener closes connections that arrive on an address the filter refuses, before any request is read
type filteredListener struct {
	net.Listener
	refuse func(netip.Addr) bool
}

func (l filteredListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		local, err := netip.ParseAddrPort(conn.LocalAddr().String())
		if err == nil && l.refuse(local.Addr().Unmap()) {
			_ = conn.Close()
			continue
		}
		return conn, nil
	}
}
