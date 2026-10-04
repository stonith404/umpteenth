//go:build unit

package sandbox

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRegistrySourcesBelongToOneJobOrTheConfiguredDefault(t *testing.T) {
	registry, defaultImage := "ghcr.io/acme/jobs", "ghcr.io/stonith404/umpteenth-sandbox:latest"
	for _, ref := range []string{"ghcr.io/acme/jobs/job-1:ready", defaultImage, defaultImage + "@sha256:" + strings.Repeat("a", 64), "docker.io/library/alpine:3"} {
		require.NoError(t, CheckRegistrySource(ref, registry, "1", defaultImage), ref)
	}
	for _, ref := range []string{"ghcr.io/acme/jobs/job-2:ready", "ghcr.io/acme/sibling:1", "ghcr.io/acme/jobs-other:1", "ghcr.io/stonith404/umpteenth-sandbox:other"} {
		require.Error(t, CheckRegistrySource(ref, registry, "1", defaultImage), ref)
	}
	require.NoError(t, CheckRegistrySource("localhost:5000/job-1:ready", "localhost:5000", "1", ""))
	require.Error(t, CheckRegistrySource("localhost:5000/job-2:ready", "localhost:5000", "1", ""))
	require.Error(t, CheckRegistrySource("ghcr.io/acme/jobs/job-1:ready", registry, "", defaultImage), "an MCP test has no originating job")
}
