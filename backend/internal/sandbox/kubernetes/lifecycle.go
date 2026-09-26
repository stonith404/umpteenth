package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/stonith404/umpteenth/backend/internal/sandbox"
)

const (
	// envSecretSuffix names the Secret holding a sandbox's environment after its pod
	envSecretSuffix = "-env"
	// createTimeout bounds how long a sandbox may take to become usable, which includes pulling its image
	createTimeout = 10 * time.Minute
	// pollInterval is how often a starting pod is looked at
	pollInterval = 250 * time.Millisecond
	// deadlineSlack lets ump init end a sandbox at its TTL before Kubernetes does
	deadlineSlack = time.Minute
)

// fatalWaitReasons are container states a starting pod never recovers from on its own
var fatalWaitReasons = []string{"ImagePullBackOff", "ErrImageNeverPull", "InvalidImageName", "CreateContainerConfigError", "CreateContainerError", "RunContainerError", "CrashLoopBackOff"}

// Create provisions a sandbox pod, returning once it accepts Exec
func (a *Adapter) Create(ctx context.Context, spec sandbox.Spec) (sandbox.Sandbox, error) {
	// A cluster that doesn't enforce the sandbox NetworkPolicies must not run sandboxes at all
	if err := a.networkProblem(); err != nil {
		return nil, err
	}
	return a.create(ctx, spec)
}

// create provisions a sandbox without consulting the network check, which runs one itself
func (a *Adapter) create(ctx context.Context, spec sandbox.Spec) (sandbox.Sandbox, error) {
	// Fill in defaults and reject what this adapter cannot provide
	if spec.Image == "" {
		spec.Image = a.cfg.DefaultImage
	}
	if spec.AgentUser == "" {
		spec.AgentUser = sandbox.UserAgent
	}
	if spec.Network == "" {
		spec.Network = sandbox.NetworkInternet
	}
	if !slices.Contains(a.networks(), spec.Network) {
		return nil, fmt.Errorf("%w: network policy %q", sandbox.ErrUnsupported, spec.Network)
	}
	if spec.RunID == "" {
		spec.RunID = randomID(8)
	}
	name := objectName(podPrefix, spec.RunID)

	// The environment lives in a Secret, so job secrets and the broker token never sit in the pod spec
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name + envSecretSuffix, Namespace: a.ns, Labels: a.runLabels(spec, roleSandboxEnv)},
		Type:       corev1.SecretTypeOpaque,
		StringData: envMap(sandbox.BrokerEnv(spec, a.brokerAddr(spec.Broker.Port), nil)),
	}
	if err := a.kube.create(ctx, "secrets", secret, &corev1.Secret{}); err != nil {
		return nil, fmt.Errorf("failed to create the sandbox environment: %w", err)
	}
	success := false
	defer func() {
		if !success {
			a.rollbackCreate(context.WithoutCancel(ctx), name)
		}
	}()

	pod, err := a.kube.createPod(ctx, a.podSpec(name, spec))
	if err != nil {
		return nil, fmt.Errorf("failed to create the sandbox pod: %w", err)
	}

	// The pod owns its Secret from now on, so deleting the pod by any means also removes the environment
	stored := &corev1.Secret{}
	err = a.kube.get(ctx, "secrets", secret.Name, stored)
	if err == nil {
		stored.OwnerReferences = []metav1.OwnerReference{{APIVersion: "v1", Kind: "Pod", Name: pod.Name, UID: pod.UID}}
		err = a.kube.update(ctx, "secrets", secret.Name, stored, &corev1.Secret{})
	}
	if err != nil {
		a.log.WarnContext(ctx, "Failed to make the sandbox pod own its environment, maintenance removes it later", "sandbox", name, "error", err)
	}

	sb := &podSandbox{a: a, name: name, agentUser: spec.AgentUser}
	if err := sb.waitReady(ctx); err != nil {
		return nil, err
	}
	success = true
	a.log.DebugContext(ctx, "Sandbox created", "sandbox", name, "run", spec.RunID, "image", spec.Image, "network", spec.Network)
	return sb, nil
}

// waitReady waits for the pod to run and for ump init to have installed the layout, which the first successful exec proves
func (s *podSandbox) waitReady(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, createTimeout)
	defer cancel()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		// Stop at a state the pod never recovers from, and report it with the reason Kubernetes gives
		pod, err := s.a.kube.getPod(ctx, s.name)
		if err != nil {
			return fmt.Errorf("failed to watch the sandbox pod start: %w", err)
		}
		if err := startFailure(pod); err != nil {
			return err
		}
		if containerRunning(pod) {
			// Installing ump is the last thing ump init does, so a working exec means the layout is complete
			res, err := s.run(ctx, []string{sandbox.UmpBinary, "help"}, nil, nil, nil)
			if err == nil && res == 0 {
				return nil
			}
		}

		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return fmt.Errorf("the sandbox pod did not start within %s", createTimeout)
			}
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// startFailure explains why a starting pod will never run, or returns nil while it still may
func startFailure(pod *corev1.Pod) error {
	if pod.DeletionTimestamp != nil {
		return fmt.Errorf("%w: the sandbox pod is being deleted", sandbox.ErrSandboxGone)
	}
	statuses := append(slices.Clone(pod.Status.InitContainerStatuses), pod.Status.ContainerStatuses...)
	for _, cs := range statuses {
		if w := cs.State.Waiting; w != nil && slices.Contains(fatalWaitReasons, w.Reason) {
			return fmt.Errorf("the sandbox pod cannot start its %s container: %s: %s", cs.Name, w.Reason, w.Message)
		}
		if t := cs.State.Terminated; t != nil && (cs.Name == sandboxContainer || t.ExitCode != 0) {
			return fmt.Errorf("the sandbox pod's %s container exited with code %d: %s %s", cs.Name, t.ExitCode, t.Reason, strings.TrimSpace(t.Message))
		}
	}
	if pod.Status.Phase == corev1.PodFailed || pod.Status.Phase == corev1.PodSucceeded {
		return fmt.Errorf("the sandbox pod stopped: %s %s", pod.Status.Reason, pod.Status.Message)
	}
	return nil
}

// containerRunning reports whether the sandbox container runs
func containerRunning(pod *corev1.Pod) bool {
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.Name == sandboxContainer {
			return cs.State.Running != nil
		}
	}
	return false
}

// Get reattaches to a running sandbox of this instance
func (a *Adapter) Get(ctx context.Context, id string) (sandbox.Sandbox, error) {
	pod, err := a.kube.getPod(ctx, id)
	if apierrors.IsNotFound(err) || apierrors.IsInvalid(err) {
		return nil, fmt.Errorf("%w: %s", sandbox.ErrNotFound, id)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get sandbox %s: %w", id, err)
	}

	// Another instance's pod is treated as absent, so instances cannot reach into each other's sandboxes
	if !a.owns(pod.Labels, roleSandbox) || pod.DeletionTimestamp != nil {
		return nil, fmt.Errorf("%w: %s", sandbox.ErrNotFound, id)
	}
	if !containerRunning(pod) {
		return nil, fmt.Errorf("%w: sandbox %s is no longer running", sandbox.ErrNotFound, id)
	}

	agentUser := sandbox.User(pod.Labels[labelUser])
	if agentUser == "" {
		agentUser = sandbox.UserAgent
	}
	return &podSandbox{a: a, name: pod.Name, agentUser: agentUser}, nil
}

// List returns every sandbox of this instance, including stopped ones the reaper still has to remove
func (a *Adapter) List(ctx context.Context) ([]sandbox.Summary, error) {
	pods, err := a.kube.listPods(ctx, a.selector(roleSandbox))
	if err != nil {
		return nil, fmt.Errorf("failed to list sandboxes: %w", err)
	}
	summaries := make([]sandbox.Summary, 0, len(pods))
	for _, p := range pods {
		// A pod being deleted is already on its way out
		if p.DeletionTimestamp != nil {
			continue
		}
		summaries = append(summaries, sandbox.Summary{ID: p.Name, RunID: p.Annotations[annotationRun], CreatedAt: p.CreationTimestamp.Time})
	}
	return summaries, nil
}

// Destroy deletes a sandbox pod and its environment; a sandbox that is already gone is not an error
func (a *Adapter) Destroy(ctx context.Context, id string) error {
	pod, err := a.kube.getPod(ctx, id)
	if apierrors.IsNotFound(err) || apierrors.IsInvalid(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to get sandbox %s: %w", id, err)
	}

	// Never delete what another instance owns, even when handed its name
	if !a.owns(pod.Labels, roleSandbox) {
		return fmt.Errorf("pod %s is not a sandbox of this Umpteenth instance", id)
	}
	return a.deleteSandbox(ctx, pod.Name)
}

// deleteSandbox deletes a sandbox pod and its Secret without a grace period
// A pod that lingered while terminating would still accept execs, while a sandbox has nothing to shut down gracefully
func (a *Adapter) deleteSandbox(ctx context.Context, name string) error {
	err := ignoreNotFound(a.kube.delete(ctx, "pods", name, new(int64(0))))
	if err != nil {
		return fmt.Errorf("failed to delete sandbox %s: %w", name, err)
	}
	err = ignoreNotFound(a.kube.delete(ctx, "secrets", name+envSecretSuffix, nil))
	if err != nil {
		return fmt.Errorf("failed to delete the environment of sandbox %s: %w", name, err)
	}
	return nil
}

// rollbackCreate removes whatever a failed Create left behind
func (a *Adapter) rollbackCreate(ctx context.Context, name string) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := a.deleteSandbox(ctx, name); err != nil {
		a.log.WarnContext(ctx, "Failed to remove a sandbox that did not start", "sandbox", name, "error", err)
	}
}

// podSpec builds the sandbox pod
func (a *Adapter) podSpec(name string, spec sandbox.Spec) *corev1.Pod {
	labels := a.runLabels(spec, roleSandbox)
	labels[labelUser] = string(spec.AgentUser)
	labels[labelNetwork] = string(spec.Network)
	annotations := map[string]string{annotationRun: spec.RunID}

	// ump init is PID 1, installs the layout and ends the sandbox when the TTL is reached, while the pod's deadline is the backstop
	command := []string{bootstrapDir + "/ump", "init", "--install"}
	if sandbox.Proxied(spec.Network) {
		command = append(command, "--proxied")
	}
	var deadline *int64
	if spec.TTL > 0 {
		command = append(command, "--ttl", spec.TTL.String())
		annotations[annotationExpires] = time.Now().Add(spec.TTL).UTC().Format(time.RFC3339)
		deadline = new(int64((spec.TTL + deadlineSlack) / time.Second))
	}

	// Zero means the documented default, and requests equal limits so the sandbox gets exactly what it may use
	cpus := spec.Resources.CPUs
	if cpus <= 0 {
		cpus = defaultCPUs
	}
	memoryMB := spec.Resources.MemoryMB
	if memoryMB <= 0 {
		memoryMB = defaultMemoryMB
	}
	limits := corev1.ResourceList{
		corev1.ResourceCPU:    *resource.NewMilliQuantity(int64(cpus*1000), resource.DecimalSI),
		corev1.ResourceMemory: *resource.NewQuantity(int64(memoryMB)*1024*1024, resource.BinarySI),
	}

	// Init runs as root so it can reap every user's processes and the shim can switch users, which needs these capabilities and nothing else
	// Root keeps the runtime's default capabilities because it is an explicit opt-in for package installs
	securityContext := &corev1.SecurityContext{
		RunAsUser:                new(int64(0)),
		RunAsGroup:               new(int64(0)),
		RunAsNonRoot:             new(false),
		AllowPrivilegeEscalation: new(false),
	}
	if spec.AgentUser != sandbox.UserRoot {
		securityContext.Capabilities = &corev1.Capabilities{
			Drop: []corev1.Capability{"ALL"},
			Add:  []corev1.Capability{"CHOWN", "DAC_OVERRIDE", "FOWNER", "SETUID", "SETGID", "KILL"},
		}
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: a.ns, Labels: labels, Annotations: annotations},
		Spec: corev1.PodSpec{
			RestartPolicy:                 corev1.RestartPolicyNever,
			AutomountServiceAccountToken:  new(false),
			EnableServiceLinks:            new(false),
			ActiveDeadlineSeconds:         deadline,
			TerminationGracePeriodSeconds: new(int64(5)),
			NodeSelector:                  a.nodeSelector,
			Tolerations:                   a.tolerations,
			SecurityContext:               &corev1.PodSecurityContext{SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}},
			InitContainers: []corev1.Container{{
				Name:            "ump",
				Image:           a.cfg.BootstrapImage,
				ImagePullPolicy: corev1.PullIfNotPresent,
				Command:         []string{sandbox.UmpBinary, "install", bootstrapDir + "/ump"},
				VolumeMounts:    []corev1.VolumeMount{{Name: "ump", MountPath: bootstrapDir}},
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("10m"), corev1.ResourceMemory: resource.MustParse("16Mi")},
					Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("500m"), corev1.ResourceMemory: resource.MustParse("64Mi")},
				},
				SecurityContext: &corev1.SecurityContext{
					RunAsUser:                new(int64(65532)),
					RunAsNonRoot:             new(true),
					AllowPrivilegeEscalation: new(false),
					ReadOnlyRootFilesystem:   new(true),
					Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
				},
			}},
			Containers: []corev1.Container{{
				Name:            sandboxContainer,
				Image:           spec.Image,
				ImagePullPolicy: corev1.PullIfNotPresent,
				Command:         command,
				WorkingDir:      sandbox.WorkspaceDir,
				EnvFrom:         []corev1.EnvFromSource{{SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: name + envSecretSuffix}}}},
				Resources:       corev1.ResourceRequirements{Requests: limits, Limits: limits},
				SecurityContext: securityContext,
				VolumeMounts:    []corev1.VolumeMount{{Name: "ump", MountPath: bootstrapDir, ReadOnly: true}},
			}},
			Volumes: []corev1.Volume{{Name: "ump", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{SizeLimit: new(resource.MustParse("64Mi"))}}}},
		},
	}
	if a.cfg.RuntimeClass != "" {
		pod.Spec.RuntimeClassName = &a.cfg.RuntimeClass
	}
	if a.pullSecret != "" {
		pod.Spec.ImagePullSecrets = []corev1.LocalObjectReference{{Name: a.pullSecret}}
	}

	// Only unrestricted sandboxes resolve names themselves, while the others leave that to the proxy and get a resolver that fails at once instead of timing out
	if spec.Network != sandbox.NetworkUnrestricted {
		pod.Spec.DNSPolicy = corev1.DNSNone
		pod.Spec.DNSConfig = &corev1.PodDNSConfig{Nameservers: []string{"127.0.0.1"}}
	}
	return pod
}

// runLabels are the labels shared by a run's pod and Secret
func (a *Adapter) runLabels(spec sandbox.Spec, role string) map[string]string {
	return map[string]string{
		labelInstance:  labelValue(a.cfg.InstanceID),
		labelHost:      labelValue(a.cfg.HostID),
		labelRun:       labelValue(spec.RunID),
		labelJob:       labelValue(spec.JobID),
		labelWorkspace: labelValue(spec.WorkspaceID),
		labelRole:      role,
	}
}

// envMap turns KEY=VALUE entries into the data of a Secret
func envMap(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		m[key] = value
	}
	return m
}
