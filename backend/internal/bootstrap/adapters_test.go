//go:build unit

package bootstrap

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/egress"
	"github.com/stonith404/umpteenth/backend/internal/llm"
	"github.com/stonith404/umpteenth/backend/internal/providers"
)

func TestProvidersConnectThroughTheEgressGuard(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		http.Error(w, "internal", http.StatusInternalServerError)
	}))
	defer srv.Close()

	// A provider on a loopback address is refused at dial time when private targets are not allowed
	for kind, factory := range guardedFactories(map[string]providers.Factory{llm.KindOpenAI: newOpenAI, llm.KindAnthropic: newAnthropic}, egress.New(false)) {
		p, err := factory(llm.Config{Kind: kind, BaseURL: srv.URL, APIKey: "k"})
		require.NoError(t, err)
		_, err = p.Stream(context.Background(), llm.Request{Model: "m", Messages: []llm.Message{{Role: llm.RoleUser, Parts: []llm.Part{llm.TextPart("hi")}}}, MaxTokens: 10}, nil)
		require.ErrorContains(t, err, "not allowed", kind)
	}
	require.Zero(t, hits.Load())
}
