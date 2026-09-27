//go:build unit

package docker

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestJobImagesAreOnlyPulledFromTheConfiguredRegistry(t *testing.T) {
	// Without a registry no job image may be pulled, since its name would resolve to Docker Hub
	a := &Adapter{}
	assert.False(t, a.onRegistry("umpteenth/job-0190a1b2-c3d4:0123456789abcdef"))
	assert.False(t, a.onRegistry("docker.io/umpteenth/job-0190a1b2-c3d4:0123456789abcdef"))

	// With a registry only its own repositories are pulled
	a.cfg.Registry = "https://ghcr.io/acme/jobs/"
	assert.True(t, a.onRegistry("ghcr.io/acme/jobs/job-1:abc"))
	assert.False(t, a.onRegistry("ghcr.io/acme/other/job-1:abc"))
	assert.False(t, a.onRegistry("umpteenth/job-1:abc"), "a job image built before the registry was configured is still local only")

	// A Docker Hub namespace as the registry matches the familiar form of the name
	a.cfg.Registry = "acme"
	assert.True(t, a.onRegistry("acme/job-1:abc"))
	assert.True(t, a.onRegistry("docker.io/acme/job-1:abc"))

	a.cfg.Registry = "localhost:5000"
	assert.True(t, a.onRegistry("localhost:5000/job-1:abc"))
}

func TestIsLocalJobImage(t *testing.T) {
	assert.True(t, isLocalJobImage("umpteenth/job-0190a1b2-c3d4:0123456789abcdef"))
	assert.True(t, isLocalJobImage("docker.io/umpteenth/job-1:abc"))
	assert.False(t, isLocalJobImage("debian:trixie-slim"))
	assert.False(t, isLocalJobImage("umpteenth-sandboxtest/job-1:ok"))
	assert.False(t, isLocalJobImage("ghcr.io/umpteenth/job-1:abc"))
}
