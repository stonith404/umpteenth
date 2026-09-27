package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/stonith404/umpteenth/backend/internal/sandbox"
)

const (
	// maxBuildAge is how old a build pod must be before maintenance takes it for the leftover of a crash
	maxBuildAge = 3 * time.Hour
	// buildContainer runs BuildKit
	buildContainer = "build"
	// buildHome is the home of the rootless BuildKit image's user
	buildHome = "/home/user"
)

// BuildImage builds a job image in a one-shot rootless BuildKit pod, which pushes it to the configured registry for every node to pull
func (b *Builder) BuildImage(ctx context.Context, spec sandbox.BuildSpec) (sandbox.Image, error) {
	if spec.Tag == "" || strings.TrimSpace(spec.Dockerfile) == "" {
		return sandbox.Image{}, errors.New("an image build needs a tag and a Dockerfile")
	}

	// Nodes can only run what they can pull, so every image goes to the registry whether or not the caller asked to push
	ref, err := b.parseRef(spec.Tag)
	if err != nil {
		return sandbox.Image{}, err
	}
	if !b.inRegistry(ref) {
		return sandbox.Image{}, fmt.Errorf("the kubernetes adapter pushes every job image to sandbox.registry.repository, so %s must be in %s", spec.Tag, b.cfg.Registry)
	}
	logs := writerOrDiscard(spec.Logs)
	if spec.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, spec.Timeout)
		defer cancel()
	}

	// The Dockerfile reaches the build pod as its whole build context
	id := randomID(6)
	podName := "ump-build-" + id
	labels := map[string]string{labelInstance: labelValue(b.cfg.InstanceID), labelHost: labelValue(b.cfg.HostID), labelRole: roleBuildContext}
	buildContext := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: podName, Namespace: b.ns, Labels: labels},
		Data:       map[string]string{"Dockerfile": spec.Dockerfile},
	}
	if b.cfg.InsecureRegistry {
		buildContext.Data["buildkitd.toml"] = fmt.Sprintf("[registry.%q]\n  http = true\n  insecure = true\n", b.registryHost())
	}
	if err := b.kube.create(ctx, "configmaps", buildContext, &corev1.ConfigMap{}); err != nil {
		return sandbox.Image{}, fmt.Errorf("failed to create the build context: %w", err)
	}

	// The pod and its context go when the build ends, however it ends
	defer func() {
		cleanupCtx, cancel := b.cleanupContext(ctx)
		defer cancel()
		if err := ignoreNotFound(b.kube.delete(cleanupCtx, "pods", podName, new(int64(0)))); err != nil {
			b.log.WarnContext(cleanupCtx, "Failed to remove a build pod", "pod", podName, "error", err)
		}
		if err := ignoreNotFound(b.kube.delete(cleanupCtx, "configmaps", podName, nil)); err != nil {
			b.log.WarnContext(cleanupCtx, "Failed to remove a build context", "configmap", podName, "error", err)
		}
	}()

	if _, err := b.kube.createPod(ctx, b.buildPod(podName, ref, spec)); err != nil {
		return sandbox.Image{}, fmt.Errorf("failed to create the build pod: %w", err)
	}

	// Follow the build's log, then read how the build ended
	if err := b.followBuild(ctx, podName, logs); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) && spec.Timeout > 0 {
			return sandbox.Image{}, fmt.Errorf("the image build did not finish within %s", spec.Timeout)
		}
		return sandbox.Image{}, err
	}

	// The registry has the final word on what was built
	img, err := b.describe(ctx, ref)
	if err != nil {
		return sandbox.Image{}, err
	}
	if spec.MaxSizeBytes > 0 && img.SizeBytes > spec.MaxSizeBytes {
		if err := b.RemoveImage(context.WithoutCancel(ctx), spec.Tag); err != nil {
			b.log.WarnContext(ctx, "Failed to remove an image over the size limit", "image", spec.Tag, "error", err)
		}
		return sandbox.Image{}, fmt.Errorf("the image is %d MB, more than the limit of %d MB", img.SizeBytes>>20, spec.MaxSizeBytes>>20)
	}
	img.Ref = spec.Tag
	return img, nil
}

// cleanupContext outlives a cancelled build, so its pod is removed even then
func (b *Builder) cleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
}

// buildPod runs a build on the build context and pushes the result
// With a BuildKit daemon configured, the pod is only its client and the daemon keeps its cache across builds, while otherwise the pod runs a rootless BuildKit of its own that starts from an empty cache
// Build steps reach the internet through the egress proxy like an internet sandbox, while BuildKit itself pulls and pushes directly, which the build NetworkPolicy allows
func (b *Builder) buildPod(podName string, ref name.Reference, spec sandbox.BuildSpec) *corev1.Pod {
	output := "type=image,name=" + ref.String() + ",push=true"
	if b.cfg.InsecureRegistry {
		output += ",registry.insecure=true"
	}
	args := []string{
		"build", "--progress=plain", "--frontend=dockerfile.v0",
		"--local", "context=/ctx", "--local", "dockerfile=/ctx",
		"--output", output,
	}
	if spec.Broker.Token != "" {
		for key, value := range sandbox.ProxyVars(spec.Broker.Token, b.brokerAddr()) {
			args = append(args, "--opt", "build-arg:"+key+"="+value)
		}
	}

	var deadline *int64
	if spec.Timeout > 0 {
		deadline = new(int64(spec.Timeout/time.Second) + 1)
	}
	mounts := []corev1.VolumeMount{{Name: "context", MountPath: "/ctx", ReadOnly: true}}
	volumes := []corev1.Volume{
		{Name: "context", VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: podName}}}},
	}

	// The registry credentials become the Docker config BuildKit pushes with
	if b.pullSecret != "" {
		mounts = append(mounts, corev1.VolumeMount{Name: "registry", MountPath: buildHome + "/.docker", ReadOnly: true})
		volumes = append(volumes, corev1.Volume{Name: "registry", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{
			SecretName: b.pullSecret,
			Items:      []corev1.KeyToPath{{Key: corev1.DockerConfigJsonKey, Path: "config.json"}},
		}}})
	}

	container := corev1.Container{
		Name:            buildContainer,
		Image:           b.cfg.BuildImage,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("250m"), corev1.ResourceMemory: resource.MustParse("512Mi")},
		},
		VolumeMounts: mounts,
	}
	if b.cfg.BuildkitAddress != "" {
		// A client needs no privileges at all
		container.Command = []string{"buildctl", "--addr", b.cfg.BuildkitAddress}
		container.Args = args
		container.SecurityContext = &corev1.SecurityContext{
			RunAsUser:                new(int64(1000)),
			RunAsGroup:               new(int64(1000)),
			RunAsNonRoot:             new(true),
			AllowPrivilegeEscalation: new(false),
			SeccompProfile:           &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
			Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		}
	} else {
		// Rootless BuildKit needs its own user namespaces, which the runtime's default seccomp and AppArmor profiles forbid
		daemonFlags := "--oci-worker-no-process-sandbox"
		if b.cfg.InsecureRegistry {
			daemonFlags += " --config=/ctx/buildkitd.toml"
		}
		container.Command = []string{"buildctl-daemonless.sh"}
		container.Args = args
		container.Env = []corev1.EnvVar{{Name: "BUILDKITD_FLAGS", Value: daemonFlags}}
		container.SecurityContext = &corev1.SecurityContext{
			RunAsUser:       new(int64(1000)),
			RunAsGroup:      new(int64(1000)),
			SeccompProfile:  &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeUnconfined},
			AppArmorProfile: &corev1.AppArmorProfile{Type: corev1.AppArmorProfileTypeUnconfined},
		}
		container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{Name: "state", MountPath: buildHome + "/.local/share/buildkit"})
		volumes = append(volumes, corev1.Volume{Name: "state", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}})
	}

	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: podName, Namespace: b.ns, Labels: map[string]string{
			labelInstance: labelValue(b.cfg.InstanceID),
			labelHost:     labelValue(b.cfg.HostID),
			labelRole:     roleBuild,
		}},
		Spec: corev1.PodSpec{
			RestartPolicy:                corev1.RestartPolicyNever,
			AutomountServiceAccountToken: new(false),
			EnableServiceLinks:           new(false),
			ActiveDeadlineSeconds:        deadline,
			NodeSelector:                 b.nodeSelector,
			Tolerations:                  b.tolerations,
			Containers:                   []corev1.Container{container},
			Volumes:                      volumes,
		},
	}
}

// followBuild streams the build pod's log and returns once the build ended, with an error unless it succeeded
func (b *Builder) followBuild(ctx context.Context, podName string, logs io.Writer) error {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	streamed := false
	for {
		pod, err := b.kube.getPod(ctx, podName)
		if err != nil {
			return fmt.Errorf("failed to watch the build pod: %w", err)
		}

		// A build image that can't be pulled never starts
		for _, cs := range pod.Status.ContainerStatuses {
			if w := cs.State.Waiting; w != nil && slices.Contains(fatalWaitReasons, w.Reason) {
				return fmt.Errorf("the build pod cannot start: %s: %s", w.Reason, w.Message)
			}
		}

		// Follow the log once the container runs, which ends when it exits
		if !streamed && (containerState(pod, buildContainer) != "waiting") {
			streamed = true
			stream, err := b.kube.logs(ctx, podName, &corev1.PodLogOptions{Container: buildContainer, Follow: true})
			if err == nil {
				_, _ = io.Copy(logs, stream)
				_ = stream.Close()
			}
		}

		switch pod.Status.Phase {
		case corev1.PodSucceeded:
			return nil
		case corev1.PodFailed:
			return fmt.Errorf("the image build failed%s", terminationDetail(pod))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// containerState is waiting, running or terminated
func containerState(pod *corev1.Pod, container string) string {
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.Name != container {
			continue
		}
		switch {
		case cs.State.Running != nil:
			return "running"
		case cs.State.Terminated != nil:
			return "terminated"
		}
	}
	return "waiting"
}

// terminationDetail explains how a pod's containers ended
func terminationDetail(pod *corev1.Pod) string {
	for _, cs := range pod.Status.ContainerStatuses {
		if t := cs.State.Terminated; t != nil {
			return fmt.Sprintf(" with exit code %d (%s)", t.ExitCode, t.Reason)
		}
	}
	if pod.Status.Reason != "" {
		return fmt.Sprintf(": %s %s", pod.Status.Reason, pod.Status.Message)
	}
	return ""
}

// describe reads a pushed image's digest and size from the registry, taking the size of the nodes' platform when the push was an index
func (b *Builder) describe(ctx context.Context, ref name.Reference) (sandbox.Image, error) {
	desc, ok, err := b.head(ctx, ref)
	if err != nil {
		return sandbox.Image{}, err
	}
	if !ok {
		return sandbox.Image{}, fmt.Errorf("the build finished, but the registry has no image %s", ref)
	}
	image, err := remote.Image(ref, append(b.remoteOptions(ctx, ref), remote.WithPlatform(v1.Platform{OS: "linux", Architecture: b.arch}))...)
	if err != nil {
		return sandbox.Image{}, fmt.Errorf("failed to read the pushed image %s: %w", ref, err)
	}
	manifest, err := image.Manifest()
	if err != nil {
		return sandbox.Image{}, fmt.Errorf("failed to read the manifest of %s: %w", ref, err)
	}
	size := manifest.Config.Size
	for _, layer := range manifest.Layers {
		size += layer.Size
	}
	return sandbox.Image{Digest: desc.Digest.String(), SizeBytes: size}, nil
}

// HasImage reports whether the registry holds an image, which is where every node pulls it from
func (b *Builder) HasImage(ctx context.Context, ref string) (bool, error) {
	parsed, err := b.parseRef(ref)
	if err != nil {
		return false, err
	}
	_, ok, err := b.head(ctx, parsed)
	return ok, err
}

// ResolveDigest resolves a reference such as debian:trixie-slim to its current digest in its registry
func (b *Builder) ResolveDigest(ctx context.Context, ref string) (string, error) {
	if _, digest, ok := strings.Cut(ref, "@"); ok {
		if _, err := v1.NewHash(digest); err != nil {
			return "", fmt.Errorf("invalid digest in %q: %w", ref, err)
		}
		return digest, nil
	}
	parsed, err := b.parseRef(ref)
	if err != nil {
		return "", err
	}
	desc, ok, err := b.head(ctx, parsed)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("%w: image %s", sandbox.ErrNotFound, ref)
	}
	return desc.Digest.String(), nil
}

// RemoveImage deletes a job image from the registry, where registries that don't offer deletes keep it
func (b *Builder) RemoveImage(ctx context.Context, ref string) error {
	parsed, err := b.parseRef(ref)
	if err != nil {
		return err
	}
	if !b.inRegistry(parsed) {
		return nil
	}

	// Registries delete manifests by digest, and deleting it removes every tag pointing at it
	desc, ok, err := b.head(ctx, parsed)
	if err != nil || !ok {
		return err
	}
	err = remote.Delete(parsed.Context().Digest(desc.Digest.String()), b.remoteOptions(ctx, parsed)...)
	switch {
	case err == nil, isMissing(err):
		return nil
	case isUnsupported(err):
		b.log.DebugContext(ctx, "The registry does not delete images, so it keeps this one", "image", ref)
		return nil
	default:
		return fmt.Errorf("failed to delete %s from the registry: %w", ref, err)
	}
}

// ListImages returns nothing, since job images live in the registry and each node's kubelet removes the ones it no longer uses
func (b *Builder) ListImages(context.Context) ([]string, error) {
	return nil, nil
}
