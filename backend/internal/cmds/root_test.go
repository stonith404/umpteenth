//go:build unit

package cmds

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/stonith404/umpteenth/backend/internal/config"
)

func TestHealthcheckURL(t *testing.T) {
	tests := []struct {
		host string
		want string
	}{
		{host: "0.0.0.0", want: "http://127.0.0.1:8080/healthz"},
		{host: "::", want: "http://127.0.0.1:8080/healthz"},
		{host: "", want: "http://127.0.0.1:8080/healthz"},
		{host: "::1", want: "http://[::1]:8080/healthz"},
		{host: "10.0.0.5", want: "http://10.0.0.5:8080/healthz"},
		{host: "localhost", want: "http://localhost:8080/healthz"},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			cfg := config.Default()
			cfg.Server.Host = tt.host
			cfg.Server.Port = 8080
			assert.Equal(t, tt.want, healthcheckURL(cfg))
		})
	}
}
