//go:build unit

package bootstrap

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/italypaleale/francis/host/local"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/config"
	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/testutil"
)

// actorHostTestConfig is a single-node configuration whose actor host listens on a free loopback port
func actorHostTestConfig(t *testing.T) *config.Config {
	t.Helper()
	var lc net.ListenConfig
	lis, err := lc.ListenPacket(t.Context(), "udp", "127.0.0.1:0")
	require.NoError(t, err)
	port := lis.LocalAddr().(*net.UDPAddr).Port
	require.NoError(t, lis.Close())

	cfg := config.Default()
	cfg.App.EncryptionKey = "actor-host-test-encryption-key"
	cfg.HA.Actors.Host = "127.0.0.1"
	cfg.HA.Actors.Port = port
	return cfg
}

// runningActorHost is an actor host started through actorsRunService, as Bootstrap runs it
type runningActorHost struct {
	host   *local.Host
	cancel context.CancelFunc
	// done is closed once the service returned, and err holds what it returned
	done chan struct{}
	err  error
}

// runActorHost starts an actor host the way Bootstrap does, and stops it when the test ends
func runActorHost(t *testing.T, cfg *config.Config, db *database.DB) *runningActorHost {
	t.Helper()
	host, err := newActorHost(cfg, db, "actor-host-test")
	require.NoError(t, err)
	svc, _ := actorsRunService(cfg, db, host)

	ctx, cancel := context.WithCancel(context.Background())
	r := &runningActorHost{host: host, cancel: cancel, done: make(chan struct{})}
	go func() {
		r.err = svc(ctx)
		close(r.done)
	}()
	t.Cleanup(r.stop)
	return r
}

// stop cancels the host and waits until it unregistered and returned
func (r *runningActorHost) stop() {
	r.cancel()
	<-r.done
}

// awaitReady reports whether the host became ready within timeout, and otherwise whether its service returned
func (r *runningActorHost) awaitReady(timeout time.Duration) (ready bool, exited bool) {
	select {
	case <-r.host.Ready():
		return true, false
	case <-r.done:
		return false, true
	case <-time.After(timeout):
		return false, false
	}
}

// leaveDeadRegistration puts a host registration back as a process killed without unregistering leaves it
// Its last health check lies a few seconds short of Francis' 20 s deadline, so the dead registration expires quickly
func leaveDeadRegistration(t *testing.T, db *database.DB, hostID, address string, sinceHealthCheck time.Duration) {
	t.Helper()
	var lastHealthCheck any = time.Now().Add(-sinceHealthCheck).UnixMilli()
	if db.Engine() == database.EnginePostgres {
		lastHealthCheck = time.Now().UTC().Add(-sinceHealthCheck)
	}
	testutil.Exec(t, db, "INSERT INTO francis_hosts (host_id, host_address, host_last_health_check) VALUES ($1, $2, $3)", hostID, address, lastHealthCheck)
}

func TestActorHostStartsAfterUncleanExit(t *testing.T) {
	tests := []struct {
		name string
		ha   bool
	}{
		// A single node allows one host, so the dead registration fills the cluster
		{name: "single node", ha: false},
		// An HA replica restarts at the same address, which the dead registration still holds
		{name: "HA replica with a stable address", ha: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := testutil.NewDatabaseForTest(t)
			cfg := actorHostTestConfig(t)
			cfg.HA.Enabled = tt.ha
			cfg.HA.Actors.BindAddress = cfg.HA.Actors.Host

			// A previous process registers the actor host
			prev := runActorHost(t, cfg, db)
			ready, _ := prev.awaitReady(10 * time.Second)
			require.True(t, ready, "first actor host did not become ready: %v", prev.err)
			var hostID, address string
			require.NoError(t, db.QueryRowContext(t.Context(), "SELECT host_id, host_address FROM francis_hosts").Scan(&hostID, &address))

			// The process dies without unregistering, as after an OOM kill, a panic or SIGKILL, so its registration stays behind until the health check deadline
			prev.stop()
			require.NoError(t, prev.err)
			leaveDeadRegistration(t, db, hostID, address, 17*time.Second)

			// The restarted process must come up once the dead registration expires, instead of exiting because the registration is still counted as live
			next := runActorHost(t, cfg, db)
			ready, exited := next.awaitReady(20 * time.Second)
			if exited {
				t.Fatalf("actor host exited instead of waiting for the dead registration to expire: %v", next.err)
			}
			require.True(t, ready, "timed out waiting for the restarted actor host")
		})
	}
}

func TestActorHostDoesNotJoinALiveSingleNode(t *testing.T) {
	db := testutil.NewDatabaseForTest(t)
	cfg := actorHostTestConfig(t)

	// A live process holds the single-node cluster and keeps sending health checks
	live := runActorHost(t, cfg, db)
	ready, _ := live.awaitReady(10 * time.Second)
	require.True(t, ready, "live actor host did not become ready: %v", live.err)

	// A second process on the same database must never become ready next to it, whether it keeps waiting or gives up
	second := runActorHost(t, cfg, db)
	ready, _ = second.awaitReady(3 * time.Second)
	require.False(t, ready, "a second actor host joined a single-node cluster that is still alive")
	second.stop()

	// The live host keeps the only registration
	var hosts int
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT count(*) FROM francis_hosts").Scan(&hosts))
	require.Equal(t, 1, hosts)
}

func TestActorHostJoinsNextToALiveHAReplica(t *testing.T) {
	db := testutil.NewDatabaseForTest(t)

	// A live replica holds its own address in the cluster
	cfg := actorHostTestConfig(t)
	cfg.HA.Enabled = true
	cfg.HA.Actors.BindAddress = cfg.HA.Actors.Host
	live := runActorHost(t, cfg, db)
	ready, _ := live.awaitReady(10 * time.Second)
	require.True(t, ready, "live actor host did not become ready: %v", live.err)

	// A replica at another address joins right away instead of waiting for the live registration to expire
	other := actorHostTestConfig(t)
	other.HA.Enabled = true
	other.HA.Actors.BindAddress = other.HA.Actors.Host
	next := runActorHost(t, other, db)
	ready, _ = next.awaitReady(2 * time.Second)
	require.True(t, ready, "a replica at another address did not join next to a live one: %v", next.err)
}
