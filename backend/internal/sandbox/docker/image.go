package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/distribution/reference"
	"github.com/docker/docker/api/types/build"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/registry"

	"github.com/stonith404/umpteenth/backend/internal/sandbox"
)

// runningInPattern finds the container the classic builder runs a step in
var runningInPattern = regexp.MustCompile(`Running in ([0-9a-f]{12,64})`)

// BuildImage builds a job image from a Dockerfile, whose build context contains nothing but the Dockerfile (PLAN.md §4.11)
// Digest is the pushed manifest digest when spec.Push is set, and the local image ID otherwise
func (a *Adapter) BuildImage(ctx context.Context, spec sandbox.BuildSpec) (sandbox.Image, error) {
	if spec.Tag == "" || strings.TrimSpace(spec.Dockerfile) == "" {
		return sandbox.Image{}, errors.New("an image build needs a tag and a Dockerfile")
	}
	logs := writerOrDiscard(spec.Logs)
	buildCtx := ctx
	if spec.Timeout > 0 {
		var cancel context.CancelFunc
		buildCtx, cancel = context.WithTimeout(ctx, spec.Timeout)
		defer cancel()
	}
	// Build steps reach the outside only through the egress proxy, so a Dockerfile written by reflection reaches the internet but not private networks, the host or cloud metadata, like an internet sandbox
	// Without a proxy grant they get no network at all
	networkMode, buildArgs, extraHosts := "none", map[string]*string(nil), []string(nil)
	if spec.Broker.Token != "" {
		buildNet, port, brokerIP, err := a.buildNetwork(ctx, spec.Broker.Port)
		if err != nil {
			return sandbox.Image{}, err
		}
		defer func() {
			removeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			if err := a.removeNetwork(removeCtx, buildNet); err != nil {
				a.log.WarnContext(ctx, "Failed to remove a build network", "network", buildNet, "error", err)
			}
		}()
		networkMode, buildArgs = buildNet, proxyBuildArgs(spec.Broker.Token, port)

		// Buildah's step containers may not resolve the alias through Podman's network DNS, so they get the broker's address directly
		if a.podman && brokerIP.IsValid() {
			extraHosts = []string{brokerAlias + ":" + brokerIP.String()}
		}
	}

	// The classic builder is used because BuildKit over the Engine API requires a client session; Podman builds with Buildah either way
	resp, err := a.builder.ImageBuild(buildCtx, dockerfileContext(spec.Dockerfile), build.ImageBuildOptions{
		Tags:        []string{spec.Tag},
		Dockerfile:  "Dockerfile",
		Remove:      true,
		ForceRemove: true,
		Version:     build.BuilderV1,
		Labels:      map[string]string{labelInstance: a.cfg.InstanceID},
		AuthConfigs: a.buildAuthConfigs(),
		NetworkMode: networkMode,
		BuildArgs:   buildArgs,
		ExtraHosts:  extraHosts,
	})
	if err != nil {
		return sandbox.Image{}, buildError(buildCtx, spec, err)
	}

	// Remember the container of the running step, because the engine may keep running it after the client went away
	var stepContainer string
	err = readProgress(resp.Body, func(m progressMessage) {
		if match := runningInPattern.FindStringSubmatch(m.Stream); match != nil {
			stepContainer = match[1]
		}
		writeProgress(logs, m)
	})
	_ = resp.Body.Close()
	if err != nil {
		if buildCtx.Err() != nil && stepContainer != "" {
			// Removing the step's container makes the step fail, so nothing of the abandoned build is committed to the cache
			if rmErr := a.cli.ContainerRemove(context.WithoutCancel(ctx), stepContainer, container.RemoveOptions{Force: true}); rmErr != nil && !isNotFound(rmErr) {
				a.log.WarnContext(ctx, "Failed to stop an abandoned build step", "container", stepContainer, "error", rmErr)
			}
		}
		return sandbox.Image{}, buildError(buildCtx, spec, err)
	}

	// Only the engine knows the final size, so measure it and enforce the limit
	inspect, err := a.cli.ImageInspect(ctx, spec.Tag)
	if err != nil {
		return sandbox.Image{}, fmt.Errorf("failed to inspect the built image: %w", err)
	}
	img := sandbox.Image{Ref: spec.Tag, Digest: inspect.ID, SizeBytes: inspect.Size}
	if spec.MaxSizeBytes > 0 && inspect.Size > spec.MaxSizeBytes {
		if err := a.RemoveImage(context.WithoutCancel(ctx), spec.Tag); err != nil {
			a.log.WarnContext(ctx, "Failed to remove an oversized image", "image", spec.Tag, "error", err)
		}
		return sandbox.Image{}, fmt.Errorf("the image is %d bytes, more than the limit of %d bytes", inspect.Size, spec.MaxSizeBytes)
	}

	// Push so other replicas can pull instead of rebuilding
	if spec.Push {
		digest, err := a.pushImage(buildCtx, spec.Tag, logs)
		if err != nil {
			return sandbox.Image{}, buildError(buildCtx, spec, err)
		}
		img.Digest = digest
	}
	return img, nil
}

// buildNetwork creates an internal network for one build with the broker attached, and returns it with the broker port and address to use there
// The network carries the run-network role, so maintenance prunes it should the process die mid-build
func (a *Adapter) buildNetwork(ctx context.Context, requestedPort int) (string, int, netip.Addr, error) {
	port, err := a.brokerPort(ctx, requestedPort)
	if err != nil {
		return "", 0, netip.Addr{}, err
	}
	if err := a.ensureBrokerPath(ctx); err != nil {
		return "", 0, netip.Addr{}, err
	}
	id := "build-" + randomID(8)
	name := runNetworkPrefix + id
	if err := a.createNetwork(ctx, name, true, a.runLabels(sandbox.Spec{RunID: id}, roleRunNetwork)); err != nil {
		return "", 0, netip.Addr{}, err
	}
	brokerIP, err := a.connectBroker(ctx, name)
	if err != nil {
		_ = a.removeNetwork(context.WithoutCancel(ctx), name)
		return "", 0, netip.Addr{}, err
	}
	return name, port, brokerIP, nil
}

// proxyBuildArgs point build steps at the egress proxy through Docker's predefined proxy arguments, which need no ARG line and stay out of the image history
func proxyBuildArgs(token string, port int) map[string]*string {
	credentials := "ump:" + token + "@" + brokerAlias + ":" + strconv.Itoa(port)
	proxy, socks, noProxy := "http://"+credentials, "socks5h://"+credentials, brokerAlias+",localhost,127.0.0.1"
	args := map[string]*string{}
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
		args[name] = &proxy
	}
	for _, name := range []string{"ALL_PROXY", "all_proxy"} {
		args[name] = &socks
	}
	for _, name := range []string{"NO_PROXY", "no_proxy"} {
		args[name] = &noProxy
	}
	return args
}

// HasImage reports whether a job image is present locally or can be pulled from the configured registry
// It pulls a missing image, since that is what the caller needs next anyway
func (a *Adapter) HasImage(ctx context.Context, ref string) (bool, error) {
	_, err := a.cli.ImageInspect(ctx, ref)
	if err == nil {
		return true, nil
	}
	if !isNotFound(err) {
		return false, fmt.Errorf("failed to inspect image %s: %w", ref, err)
	}

	// A job image outside the configured registry only ever existed in this engine, and pulling its name would look it up on Docker Hub
	if !a.onRegistry(ref) {
		return false, nil
	}
	err = a.pullImage(ctx, ref)
	if errors.Is(err, sandbox.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// ResolveDigest pulls a reference and returns the repository digest behind it, such as sha256:…
func (a *Adapter) ResolveDigest(ctx context.Context, ref string) (string, error) {
	named, err := reference.ParseNormalizedNamed(ref)
	if err != nil {
		return "", fmt.Errorf("invalid image reference %q: %w", ref, err)
	}
	// A reference that pins a digest already is its own answer
	if canonical, ok := named.(reference.Canonical); ok {
		return canonical.Digest().String(), nil
	}

	// Pull to learn the digest the tag points at now; offline, the local image's digest is the best answer
	pullErr := a.pullImage(ctx, ref)
	inspect, err := a.cli.ImageInspect(ctx, ref)
	if err != nil {
		if pullErr != nil {
			return "", pullErr
		}
		return "", fmt.Errorf("failed to inspect image %s: %w", ref, err)
	}
	for _, repoDigest := range inspect.RepoDigests {
		parsed, err := reference.ParseNormalizedNamed(repoDigest)
		if err != nil || parsed.Name() != named.Name() {
			continue
		}
		if canonical, ok := parsed.(reference.Canonical); ok {
			if pullErr != nil {
				a.log.WarnContext(ctx, "Using the local digest of an image that could not be pulled", "image", ref, "error", pullErr)
			}
			return canonical.Digest().String(), nil
		}
	}
	if pullErr != nil {
		return "", pullErr
	}
	return "", fmt.Errorf("%w: image %s has no repository digest", sandbox.ErrNotFound, ref)
}

// ListImages returns the tags of images built by this installation, found by the label every build carries
func (a *Adapter) ListImages(ctx context.Context) ([]string, error) {
	list, err := a.cli.ImageList(ctx, image.ListOptions{Filters: filters.NewArgs(filters.Arg("label", labelInstance+"="+a.cfg.InstanceID))})
	if err != nil {
		return nil, fmt.Errorf("failed to list images: %w", err)
	}
	var refs []string
	for _, img := range list {
		refs = append(refs, img.RepoTags...)
	}
	return refs, nil
}

// RemoveImage removes a local image; one that is already gone is not an error
func (a *Adapter) RemoveImage(ctx context.Context, ref string) error {
	_, err := a.cli.ImageRemove(ctx, ref, image.RemoveOptions{PruneChildren: true})
	if err != nil && !isNotFound(err) {
		return fmt.Errorf("failed to remove image %s: %w", ref, err)
	}
	return nil
}

// ensureImage returns the local image, pulling it first when it is missing
func (a *Adapter) ensureImage(ctx context.Context, ref string) (image.InspectResponse, error) {
	inspect, err := a.cli.ImageInspect(ctx, ref)
	if err == nil {
		return inspect, nil
	}
	if !isNotFound(err) {
		return inspect, fmt.Errorf("failed to inspect image %s: %w", ref, err)
	}

	// A local job image is never pulled, since the same name on Docker Hub belongs to whoever registered it there
	if isLocalJobImage(ref) {
		return inspect, fmt.Errorf("%w: job image %s is missing on this host and exists in no registry, so it has to be rebuilt", sandbox.ErrNotFound, ref)
	}
	if err := a.pullImage(ctx, ref); err != nil {
		return inspect, err
	}
	inspect, err = a.cli.ImageInspect(ctx, ref)
	if err != nil {
		return inspect, fmt.Errorf("failed to inspect image %s: %w", ref, err)
	}
	return inspect, nil
}

// imageArch returns the ump architecture an image needs, pulling the image when it is missing
func (a *Adapter) imageArch(ctx context.Context, ref string) (string, error) {
	inspect, err := a.ensureImage(ctx, ref)
	if err != nil {
		return "", err
	}
	arch := normalizeArch(inspect.Architecture)
	if arch == "" {
		return "", fmt.Errorf("%w: image %s is built for %q", sandbox.ErrUnsupported, ref, inspect.Architecture)
	}
	return arch, nil
}

// pullImage pulls an image, wrapping sandbox.ErrNotFound when the registry has no such image or denies access
func (a *Adapter) pullImage(ctx context.Context, ref string) error {
	a.log.InfoContext(ctx, "Pulling image", "image", ref)
	rc, err := a.cli.ImagePull(ctx, ref, image.PullOptions{RegistryAuth: a.registryAuth(ref)})
	if err == nil {
		err = readProgress(rc, nil)
		_ = rc.Close()
	}
	if err == nil {
		return nil
	}
	if isNotFound(err) || isUnavailableMessage(err.Error()) {
		return fmt.Errorf("%w: image %s cannot be pulled: %w", sandbox.ErrNotFound, ref, err)
	}
	return fmt.Errorf("failed to pull image %s: %w", ref, err)
}

// pushImage pushes an image and returns the manifest digest the registry assigned
func (a *Adapter) pushImage(ctx context.Context, ref string, logs io.Writer) (string, error) {
	auth := a.registryAuth(ref)
	if auth == "" {
		// The engine rejects a push without an auth header, even for registries that need none
		auth, _ = registry.EncodeAuthConfig(registry.AuthConfig{})
	}
	rc, err := a.cli.ImagePush(ctx, ref, image.PushOptions{RegistryAuth: auth})
	if err != nil {
		return "", fmt.Errorf("failed to push image %s: %w", ref, err)
	}
	defer func() { _ = rc.Close() }()

	var digest string
	err = readProgress(rc, func(m progressMessage) {
		var aux struct{ Digest string }
		if len(m.Aux) > 0 && json.Unmarshal(m.Aux, &aux) == nil && aux.Digest != "" {
			digest = aux.Digest
		}
		writeProgress(logs, m)
	})
	if err != nil {
		return "", fmt.Errorf("failed to push image %s: %w", ref, err)
	}
	if digest == "" {
		return "", fmt.Errorf("the registry returned no digest for %s", ref)
	}
	return digest, nil
}

// registryAuth returns the encoded credentials for a reference on the configured registry, or "" for any other registry
func (a *Adapter) registryAuth(ref string) string {
	if a.cfg.Registry == "" || a.cfg.RegistryUsername == "" {
		return ""
	}
	named, err := reference.ParseNormalizedNamed(ref)
	if err != nil || reference.Domain(named) != registryHost(a.cfg.Registry) {
		return ""
	}
	auth, err := registry.EncodeAuthConfig(a.registryAuthConfig())
	if err != nil {
		return ""
	}
	return auth
}

// buildAuthConfigs lets FROM pull from the configured registry
func (a *Adapter) buildAuthConfigs() map[string]registry.AuthConfig {
	if a.cfg.Registry == "" || a.cfg.RegistryUsername == "" {
		return nil
	}
	return map[string]registry.AuthConfig{registryHost(a.cfg.Registry): a.registryAuthConfig()}
}

func (a *Adapter) registryAuthConfig() registry.AuthConfig {
	return registry.AuthConfig{
		Username:      a.cfg.RegistryUsername,
		Password:      a.cfg.RegistryPassword,
		ServerAddress: registryHost(a.cfg.Registry),
	}
}

// localJobImagePrefix is the repository prefix the images module tags job images with when no registry is configured
const localJobImagePrefix = "umpteenth/job-"

// isLocalJobImage reports whether a reference names a job image that was built without a registry and so can only exist locally
func isLocalJobImage(ref string) bool {
	named, err := reference.ParseNormalizedNamed(ref)
	if err != nil {
		return false
	}
	return strings.HasPrefix(reference.FamiliarName(named), localJobImagePrefix)
}

// onRegistry reports whether a reference is a job image on the configured registry, which is the only place job images can be pulled from
func (a *Adapter) onRegistry(ref string) bool {
	if a.cfg.Registry == "" {
		return false
	}
	named, err := reference.ParseNormalizedNamed(ref)
	if err != nil {
		return false
	}

	// The registry setting may name a Docker Hub namespace such as acme, which only matches the familiar form of a name
	prefix := registryRepository(a.cfg.Registry) + "/"
	return strings.HasPrefix(named.Name(), prefix) || strings.HasPrefix(reference.FamiliarName(named), prefix)
}

// registryRepository strips the scheme and trailing slashes an operator may add to a registry setting such as https://ghcr.io/acme/jobs/
func registryRepository(r string) string {
	return strings.TrimRight(strings.TrimPrefix(strings.TrimPrefix(r, "https://"), "http://"), "/")
}

// registryHost extracts the host from a registry setting such as ghcr.io/acme/jobs
func registryHost(r string) string {
	host, _, _ := strings.Cut(registryRepository(r), "/")
	return host
}

// dockerfileContext is a build context holding only the Dockerfile
func dockerfileContext(dockerfile string) io.Reader {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	_ = tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: "Dockerfile", Mode: 0o644, Size: int64(len(dockerfile)), ModTime: time.Now()})
	_, _ = tw.Write([]byte(dockerfile))
	_ = tw.Close()
	return &buf
}

// buildError explains a failed build, calling out the timeout since the engine only reports a closed connection
func buildError(ctx context.Context, spec sandbox.BuildSpec, err error) error {
	if spec.Timeout > 0 && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("the image build timed out after %s", spec.Timeout)
	}
	return fmt.Errorf("the image build failed: %w", err)
}

// isUnavailableMessage recognizes registry errors that mean the image does not exist or may not be pulled
func isUnavailableMessage(msg string) bool {
	msg = strings.ToLower(msg)
	for _, s := range []string{"manifest unknown", "not found", "pull access denied", "repository does not exist", "unauthorized", "denied", "no such image"} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

// progressMessage is one line of the engine's JSON progress stream for pulls, pushes and builds
type progressMessage struct {
	Stream         string `json:"stream"`
	Status         string `json:"status"`
	ID             string `json:"id"`
	Progress       string `json:"progress"`
	ProgressDetail struct {
		Current int64 `json:"current"`
		Total   int64 `json:"total"`
	} `json:"progressDetail"`
	Error       string `json:"error"`
	ErrorDetail *struct {
		Message string `json:"message"`
	} `json:"errorDetail"`
	Aux json.RawMessage `json:"aux"`
}

// readProgress decodes a progress stream and returns the first error the engine reported in it
func readProgress(r io.Reader, fn func(progressMessage)) error {
	dec := json.NewDecoder(r)
	for {
		var m progressMessage
		err := dec.Decode(&m)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("failed to read the engine's progress: %w", err)
		}
		if m.ErrorDetail != nil && m.ErrorDetail.Message != "" {
			return errors.New(m.ErrorDetail.Message)
		}
		if m.Error != "" {
			return errors.New(m.Error)
		}
		if fn != nil {
			fn(m)
		}
	}
}

// writeProgress writes build output and status lines, leaving out per-layer download progress
func writeProgress(w io.Writer, m progressMessage) {
	switch {
	case m.Stream != "":
		_, _ = io.WriteString(w, m.Stream)
	case m.Status != "" && m.Progress == "" && m.ProgressDetail.Current == 0 && m.ProgressDetail.Total == 0:
		if m.ID != "" {
			_, _ = fmt.Fprintf(w, "%s: %s\n", m.ID, m.Status)
		} else {
			_, _ = fmt.Fprintln(w, m.Status)
		}
	}
}
