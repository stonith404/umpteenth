//go:build unit

package docker

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/docker/docker/client"
	"github.com/stonith404/umpteenth/backend/internal/egress"
	"github.com/stretchr/testify/require"
)

func TestGuardedPullRejectsPrivateTokenRealmWithoutEnginePull(t *testing.T) {
	var enginePulls atomic.Int32
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/info") {
			_, _ = w.Write([]byte(`{"OSType":"linux","Architecture":"aarch64"}`))
			return
		}
		enginePulls.Add(1)
		http.Error(w, "engine registry traffic must not happen", 500)
	}))
	t.Cleanup(engine.Close)
	cli, err := client.NewClientWithOpts(client.WithHost("tcp://"+strings.TrimPrefix(engine.URL, "http://")), client.WithVersion("1.47"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = cli.Close() })
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="http://127.0.0.2:80/token",service="registry"`)
		http.Error(w, "authentication required", http.StatusUnauthorized)
	}))
	t.Cleanup(registry.Close)
	guard := egress.New(true, netip.MustParsePrefix("127.0.0.2/32"))
	a := &Adapter{cli: cli, log: slog.New(slog.DiscardHandler), cfg: Config{RegistryTransport: guard.HTTPClient(0).Transport}}
	err = a.pullPlatform(t.Context(), strings.TrimPrefix(registry.URL, "http://")+"/image:1", "")
	require.Error(t, err)
	require.Contains(t, fmt.Sprint(err), "127.0.0.2")
	require.Zero(t, enginePulls.Load())
}
