package sandboxtest

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
)

// stubBroker stands in for the real broker; it is shared by every test in the process, so adapters can be configured with its port up front
var stubBroker struct {
	once sync.Once
	port int
	err  error

	mu   sync.Mutex
	hits map[string]int
}

// BrokerPort starts the stub broker on first use and returns its port on this machine
// Adapters under test must route UMP_BROKER_URL to this port, e.g. through the Docker adapter's relay
// It listens on loopback on macOS and Windows, whose Docker VMs forward host.docker.internal there, and on all interfaces elsewhere; SANDBOXTEST_BROKER_ADDR overrides the address
func BrokerPort() (int, error) {
	stubBroker.once.Do(func() {
		addr := os.Getenv("SANDBOXTEST_BROKER_ADDR")
		if addr == "" {
			addr = "0.0.0.0:0"
			if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
				addr = "127.0.0.1:0"
			}
		}
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			stubBroker.err = fmt.Errorf("failed to start the stub broker: %w", err)
			return
		}
		stubBroker.port = ln.Addr().(*net.TCPAddr).Port
		stubBroker.hits = map[string]int{}

		mux := http.NewServeMux()
		mux.HandleFunc("GET /ping", func(w http.ResponseWriter, r *http.Request) {
			token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			if !ok || token == "" {
				http.Error(w, "missing token", http.StatusUnauthorized)
				return
			}
			stubBroker.mu.Lock()
			stubBroker.hits[token]++
			stubBroker.mu.Unlock()
			_, _ = io.WriteString(w, "pong\n")
		})
		go func() { _ = http.Serve(ln, mux) }() // #nosec G114 -- a test-only stub broker
	})
	return stubBroker.port, stubBroker.err
}

// brokerHits returns how many authenticated requests carried the token
func brokerHits(token string) int {
	stubBroker.mu.Lock()
	defer stubBroker.mu.Unlock()
	return stubBroker.hits[token]
}
