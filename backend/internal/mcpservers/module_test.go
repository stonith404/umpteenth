//go:build unit

package mcpservers

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/sandbox"
	"github.com/stonith404/umpteenth/backend/internal/sandbox/fake"
)

// denyAll is a limiter the workspace has used up
type denyAll struct{}

func (denyAll) Allow(context.Context, string) (bool, time.Duration, error) {
	return false, 10 * time.Second, nil
}

// countingAdapter counts the sandboxes it is asked to create
type countingAdapter struct {
	*fake.Adapter
	created int
}

func (c *countingAdapter) Create(ctx context.Context, spec sandbox.Spec) (sandbox.Sandbox, error) {
	c.created++
	return c.Adapter.Create(ctx, spec)
}

func TestStdioTestsAreRateLimitedBeforeTheSandboxStarts(t *testing.T) {
	m, ctx, _ := newTestModule(t)
	adapter := &countingAdapter{Adapter: fake.New()}
	m.deps.Adapter = adapter
	m.deps.DefaultImage = func(context.Context, string) string { return "img" }
	m.deps.TestLimiter = denyAll{}

	// A caller over the limit starts no test sandbox
	stdio := addServer(t, m, ctx, serverBody{Name: "local", Transport: "stdio", Command: "npx"})
	_, err := m.test(ctx, &idInput{ID: stdio.ID})
	require.True(t, apperror.IsCode(err, apperror.CodeRateLimited), err)
	require.Zero(t, adapter.created)

	// An HTTP server needs no sandbox, so its test isn't limited
	remote := addServer(t, m, ctx, serverBody{Name: "remote", Transport: "http", URL: "http://127.0.0.1:1/mcp"})
	out, err := m.test(ctx, &idInput{ID: remote.ID})
	require.NoError(t, err)
	require.False(t, out.Body.OK)
}
