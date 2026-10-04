//go:build integration

package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/client"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/stonith404/umpteenth/backend/internal/egress"
	"github.com/stonith404/umpteenth/backend/internal/sandbox"
	"github.com/stretchr/testify/require"
)

func TestGuardedArchiveImportsAndBuildsLocalSources(t *testing.T) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	require.NoError(t, err)
	t.Cleanup(func() { _ = cli.Close() })
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	info, err := cli.Info(ctx)
	require.NoError(t, err)
	server := httptest.NewServer(registry.New())
	t.Cleanup(server.Close)
	ref, err := name.ParseReference(strings.TrimPrefix(server.URL, "http://") + "/fixture:1")
	require.NoError(t, err)

	// A tiny registry image makes the test independent of public registries and existing host images
	var archive bytes.Buffer
	tw := tar.NewWriter(&archive)
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "fixture", Mode: 0644, Size: 2}))
	_, err = tw.Write([]byte("ok"))
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	layer, err := tarball.LayerFromReader(&archive)
	require.NoError(t, err)
	img, err := mutate.AppendLayers(empty.Image, layer)
	require.NoError(t, err)
	config, err := img.ConfigFile()
	require.NoError(t, err)
	config.OS, config.Architecture = info.OSType, normalizeArch(info.Architecture)
	img, err = mutate.ConfigFile(img, config)
	require.NoError(t, err)
	require.NoError(t, remote.Write(ref, img, remote.WithContext(ctx)))
	guard := egress.New(true)
	a := &Adapter{cli: cli, log: slog.New(slog.DiscardHandler), cfg: Config{InstanceID: "guarded-registry-test", RegistryTransport: guard.HTTPClient(0).Transport}}
	digest, err := a.ResolveDigest(ctx, ref.String())
	require.NoError(t, err)
	pinned := ref.Context().Name() + "@" + digest
	require.NoError(t, a.pullPlatform(ctx, pinned, ""))
	t.Cleanup(func() { _ = a.RemoveImage(context.Background(), a.imageRef(pinned)) })
	inspect, err := a.ensureImage(ctx, pinned)
	require.NoError(t, err)
	require.NotEmpty(t, inspect.ID)
	tag := "umpteenth-source-test:" + randomID(8)
	built, err := a.BuildImage(ctx, sandbox.BuildSpec{Dockerfile: "FROM " + pinned + "\nCOPY --from=" + pinned + " /fixture /copied\nLABEL guarded=true\n", Tag: tag, Timeout: time.Minute})
	require.NoError(t, err)
	t.Cleanup(func() { _ = a.RemoveImage(context.Background(), tag) })
	require.NotEmpty(t, built.Digest)
}
