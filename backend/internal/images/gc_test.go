//go:build unit

package images

import (
	"context"
	"strings"
	"testing"

	"github.com/distribution/reference"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/images/imagesdb"
	"github.com/stonith404/umpteenth/backend/internal/sandbox"
	sandboxfake "github.com/stonith404/umpteenth/backend/internal/sandbox/fake"
)

// engineNames wraps the fake engine so it stores and lists every image under the name a real engine lists it by, while still resolving the name it was tagged with, as Docker and Podman do
type engineNames struct {
	*sandboxfake.Adapter
	listed func(ref string) string
}

func (e engineNames) BuildImage(ctx context.Context, spec sandbox.BuildSpec) (sandbox.Image, error) {
	tag := spec.Tag
	spec.Tag = e.listed(tag)
	img, err := e.Adapter.BuildImage(ctx, spec)
	img.Ref = tag
	return img, err
}

func (e engineNames) HasImage(ctx context.Context, ref string) (bool, error) {
	return e.Adapter.HasImage(ctx, e.listed(ref))
}

func (e engineNames) RemoveImage(ctx context.Context, ref string) error {
	return e.Adapter.RemoveImage(ctx, e.listed(ref))
}

// podmanNames is how Podman's compat API lists a build tag, which it normalizes to Docker Hub by default (compat_api_enforce_docker_hub)
func podmanNames(ref string) string {
	return reference.TagNameOnly(mustParse(ref)).String()
}

// podmanLocalNames is how Podman lists a short build tag with compat_api_enforce_docker_hub turned off
func podmanLocalNames(ref string) string {
	if strings.HasPrefix(podmanNames(ref), "docker.io/") && !strings.HasPrefix(ref, "docker.io/") {
		return "localhost/" + ref
	}
	return podmanNames(ref)
}

// dockerNames is how Docker lists a tag, in the familiar form without docker.io/
func dockerNames(ref string) string {
	return reference.FamiliarString(mustParse(ref))
}

func mustParse(ref string) reference.Named {
	named, err := reference.ParseNormalizedNamed(ref)
	if err != nil {
		panic(err)
	}
	return named
}

func TestPruneLocalMatchesTheNamesTheEngineLists(t *testing.T) {
	cases := []struct {
		name     string
		registry string
		listed   func(string) string
	}{
		{"podman without a registry", "", podmanNames},
		{"podman without Docker Hub enforcement", "", podmanLocalNames},
		{"docker with a Docker Hub registry", "docker.io/acme", dockerNames},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			ctx := context.Background()
			engine := engineNames{Adapter: h.builder, listed: tc.listed}
			h.m.deps.Builder = engine
			h.m.deps.Registry = tc.registry

			// A ready image whose row still refers to it, built and recorded the way the build task does
			img := h.seedImage(t, 1000)
			built, err := engine.BuildImage(ctx, sandbox.BuildSpec{Dockerfile: testDockerfile, Tag: h.m.tag(img)})
			require.NoError(t, err)
			require.NoError(t, h.m.queries.MarkReady(ctx, imagesdb.MarkReadyParams{ID: img.ID, Ref: &built.Ref, SizeBytes: new(int64(1)), FinishedAt: new(database.Now())}))

			// An image no row refers to any more
			_, err = engine.BuildImage(ctx, sandbox.BuildSpec{Dockerfile: testDockerfile, Tag: "umpteenth/job-gone:abc"})
			require.NoError(t, err)

			h.m.PruneLocal(ctx)

			ok, err := engine.HasImage(ctx, built.Ref)
			require.NoError(t, err)
			require.True(t, ok, "the image of a ready row must survive, but the engine lists it as %s", tc.listed(built.Ref))
			ok, err = engine.HasImage(ctx, "umpteenth/job-gone:abc")
			require.NoError(t, err)
			require.False(t, ok, "the orphaned image is still removed")
		})
	}
}
