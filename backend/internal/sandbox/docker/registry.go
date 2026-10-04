package docker

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
)

// localImageRef gives digest-addressed archives a stable local tag since Docker load does not import registry digests
func localImageRef(ref string) string {
	if !strings.Contains(ref, "@") {
		return ref
	}
	sum := sha256.Sum256([]byte(ref))
	return fmt.Sprintf("umpteenth-source:%x", sum)
}

// pullGuarded downloads registry data through the egress guard and imports it without letting Docker contact that registry
func (a *Adapter) pullGuarded(ctx context.Context, ref, platform string) error {
	parsed, err := name.ParseReference(ref)
	if err != nil {
		return err
	}
	opts := []remote.Option{remote.WithContext(ctx), remote.WithAuth(authn.Anonymous), remote.WithTransport(a.cfg.RegistryTransport)}
	// Pick the engine's platform rather than the server's, which may run on a different machine
	if platform == "" {
		info, err := a.cli.Info(ctx)
		if err != nil {
			return err
		}
		platform = info.OSType + "/" + normalizeArch(info.Architecture)
	}
	parts := strings.Split(platform, "/")
	if len(parts) < 2 || len(parts) > 3 {
		return fmt.Errorf("invalid image platform %q", platform)
	}
	target := v1.Platform{OS: parts[0], Architecture: parts[1]}
	if len(parts) == 3 {
		target.Variant = parts[2]
	}
	opts = append(opts, remote.WithPlatform(target))
	img, err := remote.Image(parsed, opts...)
	if err != nil {
		return fmt.Errorf("failed to fetch image %s: %w", ref, err)
	}
	tag, err := name.ParseReference(localImageRef(ref))
	if err != nil {
		return err
	}

	// Stream the archive without buffering layers in host memory, and unblock its writer whenever the engine stops reading
	reader, writer := io.Pipe()
	defer func() { _ = reader.Close() }()
	done := make(chan error, 1)
	go func() { err := tarball.Write(tag, img, writer); _ = writer.CloseWithError(err); done <- err }()
	stop := context.AfterFunc(ctx, func() { _ = reader.CloseWithError(ctx.Err()) })
	defer stop()
	response, err := a.cli.ImageLoad(ctx, reader)
	if err != nil {
		_ = reader.CloseWithError(err)
		<-done
		return err
	}
	err = readProgress(response.Body, nil)
	_ = response.Body.Close()
	_ = reader.CloseWithError(err)
	archiveErr := <-done
	if err != nil {
		return err
	}
	return archiveErr
}

// imageRef preserves registry digests for trusted engine pulls and uses local tags only for guarded archive imports
func (a *Adapter) imageRef(ref string) string {
	if a.cfg.RegistryTransport != nil && !a.onRegistry(ref) {
		return localImageRef(ref)
	}
	return ref
}
