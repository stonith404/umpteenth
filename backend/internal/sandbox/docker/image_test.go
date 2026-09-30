//go:build unit

package docker

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/sandbox/dockerfile"
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

// triggerEngine answers image inspections with the ONBUILD triggers of each image it holds
func triggerEngine(t *testing.T, triggers map[string][]string) *client.Client {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{version}/images/{name}/json", func(w http.ResponseWriter, r *http.Request) {
		onBuild, ok := triggers[r.PathValue("name")]
		if !ok {
			http.Error(w, `{"message":"No such image"}`, http.StatusNotFound)
			return
		}
		inspect := image.InspectResponse{Config: &dockerspec.DockerOCIImageConfig{}}
		inspect.Config.OnBuild = onBuild
		_ = json.NewEncoder(w).Encode(inspect)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cli, err := client.NewClientWithOpts(client.WithHost("tcp://"+strings.TrimPrefix(srv.URL, "http://")), client.WithVersion("1.47"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = cli.Close() })
	return cli
}

// The classic builder runs a base image's ONBUILD triggers in the engine, where an ADD would fetch from the host's network
func TestCheckBaseImagesRefusesAddTriggers(t *testing.T) {
	a := &Adapter{log: slog.New(slog.DiscardHandler), cli: triggerEngine(t, map[string][]string{
		"plain:1":   nil,
		"runs:1":    {"RUN echo hi"},
		"fetches:1": {"RUN echo hi", "ADD https://example.com/file /file"},
	})}
	require.NoError(t, a.checkBaseImages(t.Context(), "FROM plain:1 AS base\nFROM runs:1\nFROM base\n"))
	require.ErrorIs(t, a.checkBaseImages(t.Context(), "FROM plain:1\nFROM fetches:1\n"), dockerfile.ErrAdd)
}
