//go:build unit

package kubernetes

import (
	"errors"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"

	"github.com/stonith404/umpteenth/backend/internal/sandbox"
)

// testAdapter is an adapter that never talks to a cluster, for the pure parts
func testAdapter(t *testing.T, cfg Config) *Adapter {
	t.Helper()
	if cfg.InstanceID == "" {
		cfg.InstanceID = "instance-1"
	}
	cfg.BrokerHost = "10.1.2.3"
	cfg.BrokerPort = 8081
	cfg.BootstrapImage = "ghcr.io/stonith404/umpteenth:1.0.0"
	selector, err := parseNodeSelector(cfg.NodeSelector)
	require.NoError(t, err)
	selector[corev1.LabelArchStable] = "arm64"
	tolerations, err := parseTolerations(cfg.Tolerations)
	require.NoError(t, err)
	return &Adapter{cfg: cfg, ns: "sandboxes", arch: "arm64", nodeSelector: selector, tolerations: tolerations}
}

func TestPodSpecHardensTheSandbox(t *testing.T) {
	a := testAdapter(t, Config{RuntimeClass: "gvisor", NodeSelector: []string{"pool=sandboxes"}})
	pod := a.podSpec("ump-run-1", sandbox.Spec{RunID: "run-1", JobID: "job", WorkspaceID: "ws", Image: "debian", AgentUser: sandbox.UserAgent, Network: sandbox.NetworkInternet, TTL: time.Hour})

	// No API credentials, no service variables, no restarts, and a deadline behind the TTL
	spec := pod.Spec
	assert.False(t, *spec.AutomountServiceAccountToken)
	assert.False(t, *spec.EnableServiceLinks)
	assert.Equal(t, corev1.RestartPolicyNever, spec.RestartPolicy)
	assert.Equal(t, int64(3660), *spec.ActiveDeadlineSeconds)
	assert.Equal(t, "gvisor", *spec.RuntimeClassName)
	assert.Equal(t, map[string]string{"pool": "sandboxes", corev1.LabelArchStable: "arm64"}, spec.NodeSelector)

	// The sandbox runs ump init as root with only the capabilities the shim needs, and its environment comes from the Secret
	c := spec.Containers[0]
	assert.Equal(t, []string{bootstrapDir + "/ump", "init", "--install", "--proxied", "--ttl", "1h0m0s"}, c.Command)
	assert.Equal(t, int64(0), *c.SecurityContext.RunAsUser)
	assert.False(t, *c.SecurityContext.AllowPrivilegeEscalation)
	assert.Equal(t, []corev1.Capability{"ALL"}, c.SecurityContext.Capabilities.Drop)
	assert.ElementsMatch(t, []corev1.Capability{"CHOWN", "DAC_OVERRIDE", "FOWNER", "SETUID", "SETGID", "KILL"}, c.SecurityContext.Capabilities.Add)
	assert.Empty(t, c.Env)
	assert.Equal(t, "ump-run-1"+envSecretSuffix, c.EnvFrom[0].SecretRef.Name)
	assert.Equal(t, c.Resources.Requests, c.Resources.Limits)
	assert.Equal(t, "1", c.Resources.Limits.Cpu().String())
	assert.Equal(t, "1Gi", c.Resources.Limits.Memory().String())

	// A proxied sandbox resolves nothing itself, so it gets a resolver that fails at once
	assert.Equal(t, corev1.DNSNone, spec.DNSPolicy)

	// The NetworkPolicies select the pod by these labels
	assert.Equal(t, roleSandbox, pod.Labels[labelRole])
	assert.Equal(t, "internet", pod.Labels[labelNetwork])
	assert.Equal(t, "run-1", pod.Annotations[annotationRun])
}

func TestPodSpecOfRootAndUnrestrictedSandboxes(t *testing.T) {
	a := testAdapter(t, Config{AllowUnrestricted: true})
	pod := a.podSpec("ump-run-2", sandbox.Spec{RunID: "run-2", Image: "debian", AgentUser: sandbox.UserRoot, Network: sandbox.NetworkUnrestricted})

	// Root keeps the runtime's default capabilities, and an unrestricted sandbox resolves names through the cluster
	assert.Nil(t, pod.Spec.Containers[0].SecurityContext.Capabilities)
	assert.Empty(t, pod.Spec.DNSPolicy)
	assert.Nil(t, pod.Spec.ActiveDeadlineSeconds)
	assert.NotContains(t, pod.Spec.Containers[0].Command, "--proxied")
}

func TestRootExecsRunTheReadOnlyUmp(t *testing.T) {
	// The sandbox mounts the bootstrap copy read-only, so the agent can't replace what root runs
	a := testAdapter(t, Config{})
	pod := a.podSpec("ump-run-3", sandbox.Spec{RunID: "run-3", Image: "debian", AgentUser: sandbox.UserAgent, Network: sandbox.NetworkNone})
	assert.Equal(t, []corev1.VolumeMount{{Name: "ump", MountPath: bootstrapDir, ReadOnly: true}}, pod.Spec.Containers[0].VolumeMounts)

	// The shim runs as root, so it comes from that volume rather than from the image's /usr/local/bin
	cmd, _, _, err := (&podSandbox{a: a, agentUser: sandbox.UserAgent}).shimCommand(sandbox.ExecRequest{Cmd: []string{"true"}})
	require.NoError(t, err)
	assert.Equal(t, bootstrapUmp, cmd[0])
}

func TestSandboxEnvPointsAtThePodIP(t *testing.T) {
	a := testAdapter(t, Config{})
	env := envMap(sandbox.BrokerEnv(sandbox.Spec{RunID: "r", Network: sandbox.NetworkInternet, Broker: sandbox.BrokerAccess{Token: "tok"}}, a.brokerAddr(), nil))
	assert.Equal(t, "http://10.1.2.3:8081", env["UMP_BROKER_URL"])
	assert.Equal(t, "http://ump:tok@10.1.2.3:8081", env["HTTPS_PROXY"])
	assert.Equal(t, "10.1.2.3,localhost,127.0.0.1", env["NO_PROXY"])

	// An IPv6 pod IP is bracketed in URLs but not in NO_PROXY
	a.cfg.BrokerHost = "fd00::5"
	env = envMap(sandbox.BrokerEnv(sandbox.Spec{Network: sandbox.NetworkInternet}, a.brokerAddr(), nil))
	assert.Equal(t, "http://[fd00::5]:8081", env["UMP_BROKER_URL"])
	assert.Equal(t, "fd00::5,localhost,127.0.0.1", env["NO_PROXY"])
}

func TestParseTolerations(t *testing.T) {
	got, err := parseTolerations([]string{"dedicated=sandboxes:NoSchedule", "gpu:NoExecute", "spot", "zone=a"})
	require.NoError(t, err)
	assert.Equal(t, []corev1.Toleration{
		{Key: "dedicated", Operator: corev1.TolerationOpEqual, Value: "sandboxes", Effect: corev1.TaintEffectNoSchedule},
		{Key: "gpu", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoExecute},
		{Key: "spot", Operator: corev1.TolerationOpExists},
		{Key: "zone", Operator: corev1.TolerationOpEqual, Value: "a"},
	}, got)

	_, err = parseTolerations([]string{"key:Sometimes"})
	assert.Error(t, err)
	_, err = parseNodeSelector([]string{"no-value"})
	assert.Error(t, err)
}

func TestNamesAndLabelsFitKubernetes(t *testing.T) {
	assert.Equal(t, "ump-01a0ee0b-f936-73b8-bb26-99bbcfb3020d", objectName(podPrefix, "01A0EE0B-f936-73b8-bb26-99bbcfb3020d"))
	hashed := objectName(podPrefix, "run with spaces")
	assert.True(t, strings.HasPrefix(hashed, podPrefix))
	assert.Regexp(t, `^[a-z0-9-]+$`, hashed)

	long := strings.Repeat("x", 80)
	assert.LessOrEqual(t, len(labelValue(long)), 63)
	assert.Equal(t, "a-b", labelValue("a b"))
	assert.NotEqual(t, labelValue(long), labelValue(long+"y"))
}

func TestRuntimeIsolation(t *testing.T) {
	assert.Equal(t, sandbox.IsolationGVisor, runtimeIsolation("gvisor"))
	assert.Equal(t, sandbox.IsolationMicroVM, runtimeIsolation("kata-qemu"))
	assert.Equal(t, sandbox.IsolationContainer, runtimeIsolation(""))
}

func TestContainerAddressCoversTheClusterRanges(t *testing.T) {
	a := testAdapter(t, Config{ClusterRanges: []netip.Prefix{netip.MustParsePrefix("10.244.0.0/16"), netip.MustParsePrefix("10.96.0.0/12")}})
	assert.True(t, a.ContainerAddress(netip.MustParseAddr("10.244.3.7")))
	assert.True(t, a.ContainerAddress(netip.MustParseAddr("::ffff:10.96.0.1")))
	assert.False(t, a.ContainerAddress(netip.MustParseAddr("192.168.1.10")))
}

// recordingTransport notes the hosts it was asked to reach and fails every request
type recordingTransport struct{ hosts []string }

func (r *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r.hosts = append(r.hosts, req.URL.Host)
	return nil, errors.New("refused")
}

// A Dockerfile names the registries base images are resolved in, so every lookup outside the configured registry goes through the guarded transport
func TestRegistryLookupsOutsideTheRegistryUseTheGuardedTransport(t *testing.T) {
	guarded := &recordingTransport{}
	a := testAdapter(t, Config{Registry: "registry.invalid/jobs", RegistryTransport: guarded})

	_, err := (&Builder{a}).ResolveDigest(t.Context(), "10.0.0.5:5000/tools:1")
	require.Error(t, err)
	require.NotEmpty(t, guarded.hosts)
	for _, host := range guarded.hosts {
		assert.Equal(t, "10.0.0.5:5000", host)
	}

	// The configured registry is the operator's choice and keeps the default transport
	own, err := a.parseRef("registry.invalid/jobs/job-1:abc")
	require.NoError(t, err)
	other, err := a.parseRef("ghcr.io/acme/tool:1")
	require.NoError(t, err)
	assert.Len(t, a.remoteOptions(t.Context(), other), len(a.remoteOptions(t.Context(), own))+1)
}

// Unknown cluster ranges could be anywhere in the private network, where a job that may reach it would otherwise reach pods and Services through the broker
func TestContainerAddressFailsClosedWithoutClusterRanges(t *testing.T) {
	a := testAdapter(t, Config{})
	assert.True(t, a.ContainerAddress(netip.MustParseAddr("10.244.3.7")))
	assert.True(t, a.ContainerAddress(netip.MustParseAddr("::ffff:172.20.0.5")))
	assert.True(t, a.ContainerAddress(netip.MustParseAddr("100.64.1.2")))
	assert.False(t, a.ContainerAddress(netip.MustParseAddr("93.184.216.34")))
}
