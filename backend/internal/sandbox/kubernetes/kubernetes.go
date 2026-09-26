// Package kubernetes implements the sandbox adapter on the Kubernetes API, with one pod per run
//
// Design notes:
//   - An init container copies the ump CLI from the Umpteenth image into a shared volume, and the sandbox runs `ump init --install` as PID 1, which puts ump at /usr/local/bin and creates the layout before anything runs, so any OCI image works
//   - Every command goes through the API server's exec, which has no user, working directory or environment, so the `ump exec` shim takes them as flags and the environment as a JSON line on stdin, keeping secrets off the command line
//   - Files travel as tar streams through `ump files`, since the API has no file copy of its own and images need no tar
//   - The broker is the executing replica's pod IP (sandbox.broker_host), and NetworkPolicies the Helm chart installs keep sandboxes off everything else; a test sandbox proves at startup that the cluster enforces them
//   - Internet access and allow-lists go through the broker's egress proxy exactly as with Docker, while the unrestricted network is a policy that allows public addresses directly
//   - Job images are built by one-shot rootless BuildKit pods that push to sandbox.registry, which every node then pulls from
package kubernetes

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/stonith404/umpteenth/backend/internal/common"
	"github.com/stonith404/umpteenth/backend/internal/sandbox"
)

// TypeKubernetes is the sandbox.adapter value of this adapter
const TypeKubernetes = "kubernetes"

// Labels and annotations put on every object the adapter creates
const (
	labelInstance  = "umpteenth.dev/instance"
	labelHost      = "umpteenth.dev/host"
	labelRun       = "umpteenth.dev/run"
	labelJob       = "umpteenth.dev/job"
	labelWorkspace = "umpteenth.dev/workspace"
	labelRole      = "umpteenth.dev/role"
	// labelNetwork is what the NetworkPolicies select sandboxes by
	labelNetwork = "umpteenth.dev/network"
	// annotationRun holds the exact run ID, since a label value may have to be shortened
	annotationRun     = "umpteenth.dev/run-id"
	annotationExpires = "umpteenth.dev/expires"
)

// Values of labelRole
const (
	roleSandbox      = "sandbox"
	roleSandboxEnv   = "sandbox-env"
	roleBuild        = "build"
	roleBuildContext = "build-context"
	roleRegistryAuth = "registry-auth"
)

const (
	// sandboxContainer is the container commands run in
	sandboxContainer = "sandbox"
	// bootstrapDir is where the init container leaves the ump CLI for the sandbox to install
	bootstrapDir = "/opt/umpteenth"
	// runDir holds the exec shim's pid files; it lives outside /ump so the agent user cannot swap it out
	runDir = "/run/ump"
	// podPrefix starts the name of every sandbox pod
	podPrefix = "ump-"
)

// Default limits, used for every Resources field left at zero
const (
	defaultCPUs     = 1.0
	defaultMemoryMB = 1024
)

// Config configures the adapter
type Config struct {
	// InstanceID labels every object, so two installations sharing a namespace never touch each other's sandboxes
	InstanceID string
	// HostID identifies this replica; it labels the sandboxes it creates
	HostID string
	// Kubeconfig is a kubeconfig file for running outside the cluster; empty uses the in-cluster service account
	Kubeconfig string
	// Namespace is where pods run, the service account's namespace when empty
	Namespace string
	// DefaultImage is used when a spec names no image
	DefaultImage string
	// BootstrapImage holds the ump CLI at /usr/local/bin/ump, the Umpteenth image of this version when empty
	BootstrapImage string
	// BuildImage is the rootless BuildKit image of job image builds
	BuildImage string
	// BuildkitAddress is a BuildKit daemon that builds keep their cache in, e.g. tcp://umpteenth-buildkit:1234; without it every build pod runs a BuildKit of its own
	BuildkitAddress string
	// RuntimeClass is the RuntimeClass of sandbox pods, e.g. gvisor or kata
	RuntimeClass string
	// NodeSelector are key=value labels of the nodes sandbox and build pods run on
	NodeSelector []string
	// Tolerations are taints sandbox and build pods tolerate, as key=value:Effect, key:Effect or key
	Tolerations []string
	// Registry is where job images are pushed, e.g. ghcr.io/acme/umpteenth-jobs; without it the adapter builds no images
	Registry string
	// RegistryUsername and RegistryPassword authenticate pushes, pulls and registry lookups against Registry's host
	RegistryUsername string
	RegistryPassword string
	// InsecureRegistry talks plain HTTP to Registry's host
	InsecureRegistry bool
	// ClusterRanges are the pod and Service networks, which the egress proxy refuses for every job
	ClusterRanges []netip.Prefix
	// BrokerHost is this replica's pod IP, which sandboxes reach the broker on
	BrokerHost string
	// BrokerPort is the port of this replica's broker listener, 8081 by default
	BrokerPort int
	// AllowUnrestricted offers the unrestricted network, which bypasses the egress proxy
	AllowUnrestricted bool
	// RequireNetworkPolicy refuses sandboxes when the test sandbox finds the NetworkPolicies unenforced
	RequireNetworkPolicy bool
	// Logger defaults to slog.Default()
	Logger *slog.Logger
}

// Adapter manages sandboxes as pods in one namespace
type Adapter struct {
	cfg     Config
	kube    *kubeClient
	rest    *rest.Config
	log     *slog.Logger
	ns      string
	arch    string
	version string

	nodeSelector map[string]string
	tolerations  []corev1.Toleration
	// pullSecret names the Secret that lets nodes pull job images, empty without registry credentials
	pullSecret string

	// stateMu guards what the network check found, which Create reads on every call
	stateMu    sync.Mutex
	networkErr error
}

// Builder is the adapter with image builds, which it has whenever a registry is configured
type Builder struct {
	*Adapter
}

var (
	_ sandbox.Adapter      = (*Adapter)(nil)
	_ sandbox.Maintainer   = (*Adapter)(nil)
	_ sandbox.Containers   = (*Adapter)(nil)
	_ sandbox.ImageBuilder = (*Builder)(nil)
)

// New connects to the cluster, returning an adapter that also builds images when a registry is configured
func New(ctx context.Context, cfg Config) (sandbox.Adapter, error) {
	a, err := newAdapter(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if cfg.Registry != "" {
		return &Builder{a}, nil
	}
	return a, nil
}

// newAdapter validates the configuration and connects to the API server
func newAdapter(ctx context.Context, cfg Config) (*Adapter, error) {
	// Validate the configuration and fill in defaults
	if cfg.InstanceID == "" {
		return nil, errors.New("kubernetes sandbox adapter: InstanceID is required")
	}
	if cfg.DefaultImage == "" {
		return nil, errors.New("kubernetes sandbox adapter: DefaultImage is required")
	}
	if cfg.BrokerHost == "" {
		return nil, errors.New("kubernetes sandbox adapter: the broker host (sandbox.broker_host) is required, set it to the pod IP")
	}
	if cfg.BrokerPort == 0 {
		cfg.BrokerPort = 8081
	}
	if cfg.BootstrapImage == "" {
		cfg.BootstrapImage = defaultBootstrapImage()
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	nodeSelector, err := parseNodeSelector(cfg.NodeSelector)
	if err != nil {
		return nil, err
	}
	tolerations, err := parseTolerations(cfg.Tolerations)
	if err != nil {
		return nil, err
	}

	// The architecture pins every pod, so the ump CLI of the bootstrap image and the images built here match the nodes that run them
	arch := nodeSelector[corev1.LabelArchStable]
	if arch == "" {
		arch = runtime.GOARCH
		nodeSelector[corev1.LabelArchStable] = arch
	}

	// Connect with the kubeconfig when one is given, since the server then runs outside the cluster
	restCfg, namespace, err := clientConfig(cfg.Kubeconfig)
	if err != nil {
		return nil, err
	}
	if cfg.Namespace != "" {
		namespace = cfg.Namespace
	}
	kube, err := newKubeClient(restCfg, namespace)
	if err != nil {
		return nil, err
	}

	// Reach the API server up front, so a wrong configuration fails the start instead of the first run
	v, err := kube.serverVersion(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to reach the Kubernetes API server: %w", err)
	}

	a := &Adapter{
		cfg:          cfg,
		kube:         kube,
		rest:         restCfg,
		log:          cfg.Logger.With("scope", "sandbox", "adapter", TypeKubernetes),
		ns:           namespace,
		arch:         arch,
		version:      "kubernetes " + v,
		nodeSelector: nodeSelector,
		tolerations:  tolerations,
	}
	if cfg.Registry != "" && cfg.RegistryUsername != "" {
		a.pullSecret = "ump-registry-" + shortHash(cfg.InstanceID)
	}
	return a, nil
}

// clientConfig loads the kubeconfig file, or the in-cluster service account without one, and the namespace it names
func clientConfig(kubeconfig string) (*rest.Config, string, error) {
	if kubeconfig == "" {
		restCfg, err := rest.InClusterConfig()
		if err != nil {
			return nil, "", fmt.Errorf("failed to load the in-cluster Kubernetes configuration, set sandbox.kubernetes.kubeconfig when Umpteenth runs outside the cluster: %w", err)
		}
		namespace := "default"
		if data, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace"); err == nil && len(strings.TrimSpace(string(data))) > 0 {
			namespace = strings.TrimSpace(string(data))
		}
		return restCfg, namespace, nil
	}
	loader := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(&clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfig}, &clientcmd.ConfigOverrides{})
	restCfg, err := loader.ClientConfig()
	if err != nil {
		return nil, "", fmt.Errorf("failed to load the kubeconfig %s: %w", kubeconfig, err)
	}
	namespace, _, err := loader.Namespace()
	if err != nil || namespace == "" {
		namespace = "default"
	}
	return restCfg, namespace, nil
}

// defaultBootstrapImage is the Umpteenth image of the running version, which carries the matching ump CLI
func defaultBootstrapImage() string {
	tag := common.Version
	if tag == "" || tag == "dev" {
		tag = "latest"
	}
	return "ghcr.io/stonith404/umpteenth:" + strings.TrimPrefix(tag, "v")
}

// Type returns the sandbox.adapter value of this adapter
func (a *Adapter) Type() string {
	return TypeKubernetes
}

// Check verifies the API server and reports what the cluster enforces
func (a *Adapter) Check(ctx context.Context) (sandbox.Info, error) {
	if _, err := a.kube.serverVersion(ctx); err != nil {
		return sandbox.Info{}, fmt.Errorf("failed to reach the Kubernetes API server: %w", err)
	}
	return sandbox.Info{
		Adapter:   TypeKubernetes,
		Version:   a.version,
		Arch:      a.arch,
		Isolation: runtimeIsolation(a.cfg.RuntimeClass),
		Caps: sandbox.Capabilities{
			Networks: a.networks(),
			// Kubernetes limits processes per node rather than per pod
			Limits: sandbox.LimitSet{CPU: true, Memory: true, Pids: false},
		},
		ImageBuilds:  a.cfg.Registry != "",
		RuntimeError: errorText(a.networkProblem()),
	}, nil
}

// networks are the network policies sandboxes can have, where the unrestricted network is up to the operator
func (a *Adapter) networks() []sandbox.NetworkPolicy {
	networks := []sandbox.NetworkPolicy{sandbox.NetworkNone, sandbox.NetworkInternet, sandbox.NetworkAllowlist}
	if a.cfg.AllowUnrestricted {
		networks = append(networks, sandbox.NetworkUnrestricted)
	}
	return networks
}

// Prepare stores the registry credentials, proves the network policies are enforced and cleans up after a crash
func (a *Adapter) Prepare(ctx context.Context) error {
	if err := a.ensurePullSecret(ctx); err != nil {
		return err
	}

	// A cluster that ignores NetworkPolicies would give sandboxes the run of the cluster, so a test sandbox checks before any run starts
	a.checkNetwork(ctx)
	if err := a.Maintain(ctx); err != nil {
		a.log.WarnContext(ctx, "Failed to clean up sandbox leftovers", "error", err)
	}
	return a.networkProblem()
}

// orphanAge is how old an object without its pod must be before maintenance removes it, far longer than creating a sandbox takes
const orphanAge = 10 * time.Minute

// Maintain removes what a crash left behind and retries a network check that could not finish
func (a *Adapter) Maintain(ctx context.Context) error {
	if a.networkProblem() != nil {
		a.checkNetwork(ctx)
	}
	if err := a.ensurePullSecret(ctx); err != nil {
		a.log.WarnContext(ctx, "Failed to store the registry credentials", "error", err)
	}

	// Environment secrets whose sandbox is gone, which the pod's owner reference normally takes care of
	var errs []error
	secrets := &corev1.SecretList{}
	err := a.kube.list(ctx, "secrets", a.selector(roleSandboxEnv), secrets)
	if err != nil {
		errs = append(errs, fmt.Errorf("failed to list sandbox secrets: %w", err))
	} else {
		for _, s := range secrets.Items {
			if time.Since(s.CreationTimestamp.Time) < orphanAge {
				continue
			}
			_, err := a.kube.getPod(ctx, strings.TrimSuffix(s.Name, envSecretSuffix))
			if apierrors.IsNotFound(err) {
				errs = append(errs, ignoreNotFound(a.kube.delete(ctx, "secrets", s.Name, nil)))
			}
		}
	}

	// Build pods and contexts outlive their build only when the replica died mid-build
	builds, err := a.kube.listPods(ctx, a.selector(roleBuild))
	if err != nil {
		errs = append(errs, fmt.Errorf("failed to list build pods: %w", err))
	} else {
		for _, p := range builds {
			if time.Since(p.CreationTimestamp.Time) > maxBuildAge {
				errs = append(errs, ignoreNotFound(a.kube.delete(ctx, "pods", p.Name, nil)))
			}
		}
	}
	contexts := &corev1.ConfigMapList{}
	err = a.kube.list(ctx, "configmaps", a.selector(roleBuildContext), contexts)
	if err != nil {
		errs = append(errs, fmt.Errorf("failed to list build contexts: %w", err))
	} else {
		for _, c := range contexts.Items {
			if time.Since(c.CreationTimestamp.Time) > maxBuildAge {
				errs = append(errs, ignoreNotFound(a.kube.delete(ctx, "configmaps", c.Name, nil)))
			}
		}
	}
	return errors.Join(errs...)
}

// ContainerAddress reports whether an address lies in the cluster's pod or Service networks, which the egress proxy keeps sandboxes off like Docker's container networks
func (a *Adapter) ContainerAddress(addr netip.Addr) bool {
	addr = addr.Unmap()
	for _, prefix := range a.cfg.ClusterRanges {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// Close releases nothing, since sandboxes keep running for the reaper and the client holds no connection
func (a *Adapter) Close() error {
	return nil
}

// networkProblem is why sandboxes must not start, found by the network check
func (a *Adapter) networkProblem() error {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	return a.networkErr
}

// selector matches this instance's objects of one role
func (a *Adapter) selector(role string) string {
	return labelInstance + "=" + labelValue(a.cfg.InstanceID) + "," + labelRole + "=" + role
}

// owns reports whether an object carries this instance's label and the given role
func (a *Adapter) owns(labels map[string]string, role string) bool {
	return labels[labelInstance] == labelValue(a.cfg.InstanceID) && labels[labelRole] == role
}

// brokerAddr is where sandboxes reach this replica's broker and egress proxy
func (a *Adapter) brokerAddr() string {
	return net.JoinHostPort(strings.Trim(a.cfg.BrokerHost, "[]"), strconv.Itoa(a.cfg.BrokerPort))
}

// parseNodeSelector reads key=value entries
func parseNodeSelector(entries []string) (map[string]string, error) {
	selector := map[string]string{}
	for _, entry := range entries {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || key == "" {
			return nil, fmt.Errorf("node selector entry %q must be key=value", entry)
		}
		selector[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return selector, nil
}

// parseTolerations reads tolerations written like taints: key=value:Effect, key:Effect, key=value or key
func parseTolerations(entries []string) ([]corev1.Toleration, error) {
	tolerations := make([]corev1.Toleration, 0, len(entries))
	for _, entry := range entries {
		rest, effect, hasEffect := strings.Cut(strings.TrimSpace(entry), ":")
		key, value, hasValue := strings.Cut(rest, "=")
		if key == "" {
			return nil, fmt.Errorf("toleration %q needs a key", entry)
		}
		t := corev1.Toleration{Key: key, Operator: corev1.TolerationOpExists}
		if hasValue {
			t.Operator, t.Value = corev1.TolerationOpEqual, value
		}
		if hasEffect {
			switch corev1.TaintEffect(effect) {
			case corev1.TaintEffectNoSchedule, corev1.TaintEffectPreferNoSchedule, corev1.TaintEffectNoExecute:
				t.Effect = corev1.TaintEffect(effect)
			default:
				return nil, fmt.Errorf("toleration %q has an unknown effect, use NoSchedule, PreferNoSchedule or NoExecute", entry)
			}
		}
		tolerations = append(tolerations, t)
	}
	return tolerations, nil
}

// runtimeIsolation derives the isolation level shown in the UI from the RuntimeClass name
func runtimeIsolation(runtimeClass string) sandbox.Isolation {
	name := strings.ToLower(runtimeClass)
	switch {
	case strings.Contains(name, "gvisor"), strings.Contains(name, "runsc"):
		return sandbox.IsolationGVisor
	case strings.Contains(name, "kata"), strings.Contains(name, "firecracker"):
		return sandbox.IsolationMicroVM
	default:
		return sandbox.IsolationContainer
	}
}

// invalidLabelChars are what label values may not contain
var invalidLabelChars = regexp.MustCompile(`[^A-Za-z0-9_.-]`)

// labelValue fits a value into the 63 characters of a label, replacing what labels may not contain and hashing what is too long
func labelValue(v string) string {
	clean := invalidLabelChars.ReplaceAllString(v, "-")
	if len(clean) > 63 {
		clean = clean[:50] + "-" + shortHash(v)
	}
	return strings.Trim(clean, "-_.")
}

// dnsLabel matches a name Kubernetes accepts for pods, secrets and config maps, apart from the length
var dnsLabel = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// objectName builds an object name from a prefix and an ID, hashing IDs that don't fit
func objectName(prefix, id string) string {
	name := prefix + strings.ToLower(id)
	if len(name) <= 52 && dnsLabel.MatchString(name) {
		return name
	}
	return prefix + shortHash(id)
}

// shortHash is a short stable digest of a value
func shortHash(v string) string {
	sum := sha256.Sum256([]byte(v))
	return hex.EncodeToString(sum[:6])
}

// randomID returns a random hex string of n bytes
func randomID(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// ignoreNotFound treats an object that is already gone as success
func ignoreNotFound(err error) error {
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}
