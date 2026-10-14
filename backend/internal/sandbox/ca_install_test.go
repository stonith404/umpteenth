//go:build unit

package sandbox_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/sandbox"
	"github.com/stonith404/umpteenth/backend/internal/sandbox/fake"
)

func TestTrustCACertificatesInstallsThemAsRoot(t *testing.T) {
	ctx := context.Background()
	adapter := fake.New()
	adapter.Default(fake.Reply{}.Handler())
	sb, err := adapter.Create(ctx, sandbox.Spec{})
	require.NoError(t, err)

	require.NoError(t, sandbox.TrustCACertificates(ctx, sb, []byte("certificates\n")))

	// The agent can't change what every TLS client of the sandbox trusts
	content, mode, owner, ok := sb.(*fake.Sandbox).File(sandbox.CACertificatesPath)
	require.True(t, ok)
	require.Equal(t, "certificates\n", string(content))
	require.EqualValues(t, 0o644, mode)
	require.Equal(t, sandbox.UserRoot, owner)
	calls := adapter.Calls()
	require.Equal(t, []string{sandbox.UmpBinary, "trust", sandbox.CACertificatesPath, sandbox.CABundlePath}, calls[len(calls)-1].Cmd)
	require.Equal(t, sandbox.UserRoot, calls[len(calls)-1].User)

	// A failed install reports what ump said
	adapter.On("trust", fake.Reply{Stderr: "read-only file system", ExitCode: 1})
	require.ErrorContains(t, sandbox.TrustCACertificates(ctx, sb, []byte("certificates\n")), "read-only file system")
}
