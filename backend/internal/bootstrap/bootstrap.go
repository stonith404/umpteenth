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

	francishost "github.com/italypaleale/francis/host"
	"github.com/italypaleale/go-kit/servicerunner"

	"github.com/stonith404/umpteenth/backend/internal/common"
	"github.com/stonith404/umpteenth/backend/internal/config"
	"github.com/stonith404/umpteenth/backend/internal/database"
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
	actorsRun, actorsReady := actorsRunService(actors)
	services := []servicerunner.Service{
		actorsRun,
		// The API refuses connections that arrive on this replica's addresses on sandbox networks, so sandboxes reach only the broker
		actorsReady.Await(httpService(net.JoinHostPort(cfg.Server.Host, strconv.Itoa(cfg.Server.Port)), handler, svc.sandboxFacing())),
		// The broker listens separately, so the public API and the sandbox-facing API can be exposed differently
		actorsReady.Await(httpService(net.JoinHostPort(cfg.Server.Host, strconv.Itoa(cfg.Server.BrokerPort)), svc.broker.Handler(), nil)),
	}
	for _, bg := range svc.backgroundServices() {
		services = append(services, actorsReady.Await(bg))
	}

	err = servicerunner.NewServiceRunner(services...).Run(ctx)
	if err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("failed to run services: %w", err)
	}
	return nil
}

// actorsRunService runs the actor host as a background service and returns a ready signal other services can wait on
func actorsRunService(actors francishost.Host) (servicerunner.Service, *servicerunner.Ready) {
	ready := servicerunner.NewReady()
	fn := func(ctx context.Context) error {
		runErr := make(chan error, 1)
		go func() {
			runErr <- actors.Run(ctx)
		}()

		select {
		case <-actors.Ready():
			ready.Signal()
		case err := <-runErr:
			return err
		case <-ctx.Done():
			return ctx.Err()
		}

		// Stay up until the actor host returns, which also surfaces context cancellation
		return <-runErr
	}
	return fn, ready
}

// httpService serves the handler on addr until ctx is canceled, refusing connections on the addresses refuse reports
func httpService(addr string, handler http.Handler, refuse func(netip.Addr) bool) servicerunner.Service {
	return func(ctx context.Context) error {
		srv := &http.Server{
			Addr:              addr,
			Handler:           handler,
			ReadHeaderTimeout: 10 * time.Second,
			// Idle keep-alive connections are closed, so a sandbox can't pile them up on the broker until the process runs out of file descriptors
			IdleTimeout:    2 * time.Minute,
			MaxHeaderBytes: 1 << 20,
		}
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			return fmt.Errorf("server on %s failed: %w", addr, err)
		}
		if refuse != nil {
			ln = filteredListener{Listener: ln, refuse: refuse}
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
