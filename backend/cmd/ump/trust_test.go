//go:build unit

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	testSystemCA = "-----BEGIN CERTIFICATE-----\nsystem\n-----END CERTIFICATE-----"
	testExtraCA  = "-----BEGIN CERTIFICATE-----\nextra\n-----END CERTIFICATE-----\n"
)

func TestTrustExtendsEachSystemBundleOnce(t *testing.T) {
	root := t.TempDir()
	t.Cleanup(func() { layoutRoot = "/" })
	layoutRoot = root

	// Debian's bundle, with Alpine's name pointing at it like on many images
	certs := filepath.Join(root, "etc/ssl/certs")
	require.NoError(t, os.MkdirAll(certs, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(certs, "ca-certificates.crt"), []byte(testSystemCA), 0o600))
	require.NoError(t, os.Symlink("certs/ca-certificates.crt", filepath.Join(root, "etc/ssl/cert.pem")))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "usr/local/share/ca-certificates"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(root, "usr/local/share/ca-certificates/umpteenth.crt"), []byte(testExtraCA), 0o600))

	// A second run, e.g. of a retried setup, must not add the certificates again
	for range 2 {
		require.NoError(t, trust("/usr/local/share/ca-certificates/umpteenth.crt", "/etc/ssl/umpteenth/ca-bundle.crt"))
	}

	system, err := os.ReadFile(filepath.Join(certs, "ca-certificates.crt")) // #nosec G304 -- a file in the test's temp directory
	require.NoError(t, err)
	require.Equal(t, testSystemCA+"\n"+testExtraCA, string(system))
	bundle, err := os.ReadFile(filepath.Join(root, "etc/ssl/umpteenth/ca-bundle.crt")) // #nosec G304 -- a file in the test's temp directory
	require.NoError(t, err)
	require.Equal(t, testSystemCA+"\n"+testExtraCA, string(bundle))
}

func TestTrustWithoutSystemBundle(t *testing.T) {
	root := t.TempDir()
	t.Cleanup(func() { layoutRoot = "/" })
	layoutRoot = root
	require.NoError(t, os.WriteFile(filepath.Join(root, "extra.crt"), []byte(testExtraCA), 0o600))

	require.NoError(t, trust("/extra.crt", "/etc/ssl/umpteenth/ca-bundle.crt"))

	bundle, err := os.ReadFile(filepath.Join(root, "etc/ssl/umpteenth/ca-bundle.crt")) // #nosec G304 -- a file in the test's temp directory
	require.NoError(t, err)
	require.Equal(t, testExtraCA, string(bundle))
}
