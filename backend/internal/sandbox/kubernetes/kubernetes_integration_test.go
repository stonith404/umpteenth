//go:build integration

package kubernetes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strconv"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/rest"

	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/sandbox"
	"github.com/stonith404/umpteenth/backend/internal/sandbox/sandboxtest"
)

// The suite runs against the cluster of SANDBOXTEST_KUBECONFIG and creates everything in a namespace of its own, which it deletes afterwards
// Pods must reach this process, so it runs on the cluster's network, e.g. in a container on kind's Docker network, and SANDBOXTEST_BROKER_HOST overrides the address they reach it on
// SANDBOXTEST_BOOTSTRAP_IMAGE holds the ump CLI at /usr/local/bin/ump, and SANDBOXTEST_REGISTRY, with SANDBOXTEST_REGISTRY_INSECURE for plain HTTP, enables image builds

// testRun makes namespaces and instance IDs unique per test process, so leftovers of an aborted run never interfere
var testRun = randomID(4)

// testEnv is the cluster setup every test adapter shares
type testEnv struct {
	kubeconfig string
	namespace  string
	brokerHost string
	port       int
}

func requireCluster(t *testing.T) *testEnv {
	t.Helper()
	if os.Getenv("SANDBOXTEST_KUBERNETES") != "1" {
		t.Skip("set SANDBOXTEST_KUBERNETES=1 and SANDBOXTEST_KUBECONFIG to run the Kubernetes conformance suite")
	}
	kubeconfig := os.Getenv("SANDBOXTEST_KUBECONFIG")
	require.NotEmpty(t, kubeconfig, "SANDBOXTEST_KUBECONFIG must point at the test cluster")
	restCfg, _, err := clientConfig(kubeconfig)
	require.NoError(t, err)

	port, err := sandboxtest.BrokerPort()
	require.NoError(t, err)

	// Pods reach this process on the address it reaches the API server from, unless told otherwise
	brokerHost := os.Getenv("SANDBOXTEST_BROKER_HOST")
	if brokerHost == "" {
		u, err := url.Parse(restCfg.Host)
		require.NoError(t, err)
		conn, err := net.Dial("udp", u.Host)
		require.NoError(t, err)
		brokerHost = conn.LocalAddr().(*net.UDPAddr).IP.String()
		_ = conn.Close()
	}

	env := &testEnv{kubeconfig: kubeconfig, namespace: "umpteenth-sbt-" + testRun + "-" + randomID(2), brokerHost: brokerHost, port: port}
	setupNamespace(t, restCfg, env)
	return env
}

// setupNamespace creates the test namespace with the policies the Helm chart installs, where the broker is this process instead of the Umpteenth pods
func setupNamespace(t *testing.T, restCfg *rest.Config, env *testEnv) {
	t.Helper()
	ctx := context.Background()
	core := restClient(t, restCfg, "/api", corev1.SchemeGroupVersion, corev1.AddToScheme)
	networking := restClient(t, restCfg, "/apis", networkingv1.SchemeGroupVersion, networkingv1.AddToScheme)

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: env.namespace}}
	require.NoError(t, core.Post().Resource("namespaces").Body(ns).Do(ctx).Error())
	t.Cleanup(func() {
		_ = core.Delete().Resource("namespaces").Name(env.namespace).Do(context.Background()).Error()
	})

	tcp, udp := corev1.ProtocolTCP, corev1.ProtocolUDP
	brokerPort := intstr.FromInt(env.port)
	dnsPort := intstr.FromInt32(53)
	private := []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "100.64.0.0/10", "169.254.0.0/16"}
	sandboxes := metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{{Key: labelRole, Operator: metav1.LabelSelectorOpIn, Values: []string{roleSandbox, roleBuild}}}}
	policies := []networkingv1.NetworkPolicy{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "sandbox-base"},
			Spec: networkingv1.NetworkPolicySpec{
				PodSelector: sandboxes,
				PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress},
				Egress: []networkingv1.NetworkPolicyEgressRule{{
					To:    []networkingv1.NetworkPolicyPeer{{IPBlock: &networkingv1.IPBlock{CIDR: env.brokerHost + "/32"}}},
					Ports: []networkingv1.NetworkPolicyPort{{Protocol: &tcp, Port: &brokerPort}},
				}},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "sandbox-unrestricted"},
			Spec: networkingv1.NetworkPolicySpec{
				PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{labelRole: roleSandbox, labelNetwork: string(sandbox.NetworkUnrestricted)}},
				PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
				Egress: []networkingv1.NetworkPolicyEgressRule{
					{To: []networkingv1.NetworkPolicyPeer{{IPBlock: &networkingv1.IPBlock{CIDR: "0.0.0.0/0", Except: private}}}},
					{
						To:    []networkingv1.NetworkPolicyPeer{{NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"kubernetes.io/metadata.name": "kube-system"}}, PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"k8s-app": "kube-dns"}}}},
						Ports: []networkingv1.NetworkPolicyPort{{Protocol: &udp, Port: &dnsPort}, {Protocol: &tcp, Port: &dnsPort}},
					},
				},
			},
		},
		{
			// Builds pull and push directly, here including the test registry on the cluster's network
			ObjectMeta: metav1.ObjectMeta{Name: "sandbox-build"},
			Spec: networkingv1.NetworkPolicySpec{
				PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{labelRole: roleBuild}},
				PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
				Egress:      []networkingv1.NetworkPolicyEgressRule{{}},
			},
		},
	}
	for i := range policies {
		require.NoError(t, networking.Post().Namespace(env.namespace).Resource("networkpolicies").Body(&policies[i]).Do(ctx).Error())
	}
}

// restClient builds a client for one API group, which the test needs beyond the adapter's core client
func restClient(t *testing.T, base *rest.Config, apiPath string, gv schema.GroupVersion, add func(*runtime.Scheme) error) *rest.RESTClient {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, add(scheme))
	cfg := rest.CopyConfig(base)
	cfg.APIPath = apiPath
	cfg.GroupVersion = &gv
	cfg.NegotiatedSerializer = serializer.NewCodecFactory(scheme).WithoutConversion()
	rc, err := rest.RESTClientFor(cfg)
	require.NoError(t, err)
	return rc
}

// newTestAdapter builds an adapter whose instance ID is derived from the test name, as the conformance suite requires
func (env *testEnv) newTestAdapter(t *testing.T) sandbox.Adapter {
	t.Helper()
	sum := sha256.Sum256([]byte(t.Name()))
	insecure, _ := strconv.ParseBool(os.Getenv("SANDBOXTEST_REGISTRY_INSECURE"))
	a, err := New(context.Background(), Config{
		InstanceID:           "sbt-" + testRun + "-" + hex.EncodeToString(sum[:4]),
		HostID:               "sandboxtest",
		Kubeconfig:           env.kubeconfig,
		Namespace:            env.namespace,
		DefaultImage:         sandboxtest.DefaultImage,
		BootstrapImage:       os.Getenv("SANDBOXTEST_BOOTSTRAP_IMAGE"),
		BuildImage:           "moby/buildkit:v0.33.0-rootless",
		RuntimeClass:         os.Getenv("SANDBOXTEST_RUNTIME_CLASS"),
		Registry:             os.Getenv("SANDBOXTEST_REGISTRY"),
		InsecureRegistry:     insecure,
		BrokerHost:           env.brokerHost,
		BrokerPort:           env.port,
		AllowUnrestricted:    true,
		RequireNetworkPolicy: true,
		Logger:               slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = a.Close() })
	return a
}

func TestConformance(t *testing.T) {
	env := requireCluster(t)
	sandboxtest.Run(t, env.newTestAdapter, sandboxtest.Options{
		// The kubelet kills a container's whole cgroup when it runs out of memory, unless it runs with singleProcessOOMKill
		OOMStopsSandbox: true,
		ImageRepository: os.Getenv("SANDBOXTEST_REGISTRY"),
	})
}

func TestSecurityDefaults(t *testing.T) {
	env := requireCluster(t)
	a := env.newTestAdapter(t)
	ctx := t.Context()
	require.NoError(t, a.Prepare(ctx))

	runID := "sbt-sec-" + randomID(4)
	sb, err := a.Create(ctx, sandbox.Spec{RunID: runID, JobID: "job-sec", WorkspaceID: "ws", Env: map[string]string{"JOB_SECRET": "hidden"}, Broker: sandbox.BrokerAccess{Token: "tok"}, TTL: time.Hour})
	require.NoError(t, err)
	t.Cleanup(func() { _ = a.Destroy(context.Background(), sb.ID()) })

	// The pod carries the ownership labels, keeps secrets out of its spec and gets no API credentials
	k := adapterOf(a)
	pod, err := k.kube.getPod(ctx, sb.ID())
	require.NoError(t, err)
	require.Equal(t, labelValue(k.cfg.InstanceID), pod.Labels[labelInstance])
	require.Equal(t, runID, pod.Annotations[annotationRun])
	require.Equal(t, string(sandbox.NetworkInternet), pod.Labels[labelNetwork])
	require.False(t, *pod.Spec.AutomountServiceAccountToken)
	for _, c := range pod.Spec.Containers {
		require.Empty(t, c.Env, "the environment must come from the Secret")
		require.False(t, *c.SecurityContext.AllowPrivilegeEscalation)
	}

	// The job's secret reaches commands, but no command line carries the request's environment, which the bracket keeps the probe's own from matching
	var out stringsBuilder
	_, err = sb.Exec(ctx, sandbox.ExecRequest{Cmd: []string{"sh", "-c", `echo "$JOB_SECRET" "$REQUEST_VAR"; cat /proc/[0-9]*/cmdline 2>/dev/null | tr '\0' ' ' | grep -c 'request-[s]ecret' || true`}, Env: map[string]string{"REQUEST_VAR": "request-secret"}, Stdout: &out})
	require.NoError(t, err)
	require.Equal(t, "hidden request-secret\n0\n", out.String())
}

// adapterOf unwraps the adapter behind New's result
func adapterOf(a sandbox.Adapter) *Adapter {
	if b, ok := a.(*Builder); ok {
		return b.Adapter
	}
	return a.(*Adapter)
}

// stringsBuilder is a writer the test can read back
type stringsBuilder struct{ b []byte }

func (s *stringsBuilder) Write(p []byte) (int, error) { s.b = append(s.b, p...); return len(p), nil }
func (s *stringsBuilder) String() string              { return string(s.b) }
