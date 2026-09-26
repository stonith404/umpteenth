//go:build unit

package jobs

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/sandbox"
)

func TestCheckNetworkFollowsTheAdapter(t *testing.T) {
	proxiedOnly := sandbox.Info{Caps: sandbox.Capabilities{Networks: []sandbox.NetworkPolicy{sandbox.NetworkNone, sandbox.NetworkInternet, sandbox.NetworkAllowlist}}}
	m := &Module{deps: Dependencies{SandboxInfo: func(context.Context) (sandbox.Info, error) { return proxiedOnly, nil }}}

	// The unrestricted network is refused where the operator turned it off
	require.NoError(t, m.checkNetwork(t.Context(), "internet"))
	err := m.checkNetwork(t.Context(), "unrestricted")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "doesn't offer this network")

	// An adapter that can't be reached doesn't block saving
	m.deps.SandboxInfo = func(context.Context) (sandbox.Info, error) { return sandbox.Info{}, errors.New("engine down") }
	assert.NoError(t, m.checkNetwork(t.Context(), "unrestricted"))
}
