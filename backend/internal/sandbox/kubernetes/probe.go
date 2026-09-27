package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/stonith404/umpteenth/backend/internal/sandbox"
)

// probeTimeout bounds the whole network check, which includes pulling the default image on a fresh node
const probeTimeout = 5 * time.Minute

// checkNetwork starts a sandbox without network and checks that it reaches the broker but not the Kubernetes API server
// Every pod can reach the API server unless a NetworkPolicy stops it, so reaching it proves the cluster ignores the sandbox policies
func (a *Adapter) checkNetwork(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	err := a.probeNetwork(ctx)
	if ctx.Err() != nil && err != nil && !errors.Is(err, errUnenforced) {
		err = fmt.Errorf("the network check sandbox did not finish: %w", err)
	}

	// Unenforced policies only block sandboxes when the operator requires them, while everything else keeps sandboxes from working anyway
	if errors.Is(err, errUnenforced) && !a.cfg.RequireNetworkPolicy {
		a.log.WarnContext(ctx, "Sandbox NetworkPolicies are not enforced, so sandboxes can reach everything in the cluster", "error", err)
		err = nil
	}
	if err != nil {
		a.log.ErrorContext(ctx, "Sandboxes can't start until the network check passes", "error", err)
	}
	a.stateMu.Lock()
	a.networkErr = err
	a.stateMu.Unlock()
}

// errUnenforced marks a network check that found the NetworkPolicies ignored
var errUnenforced = errors.New("the cluster does not enforce the sandbox NetworkPolicies")

// probeNetwork runs the check and returns why sandboxes must not start, if anything
func (a *Adapter) probeNetwork(ctx context.Context) error {
	apiServer := a.apiServerAddr(ctx)
	if apiServer == "" {
		a.log.WarnContext(ctx, "Skipping the sandbox network check, since the address of the Kubernetes API server inside the cluster is unknown")
		return nil
	}

	// The probe is an ordinary sandbox without network, so it gets exactly the policies every such sandbox gets
	sb, err := a.create(ctx, sandbox.Spec{
		RunID:  "netcheck-" + randomID(4),
		Image:  a.cfg.DefaultImage,
		Broker: sandbox.BrokerAccess{Token: randomID(16)},
		TTL:    probeTimeout,
		// Created without the network check, since it is the network check
		Network: sandbox.NetworkNone,
	})
	if err != nil {
		return fmt.Errorf("failed to start the network check sandbox: %w", err)
	}
	defer func() {
		if err := a.deleteSandbox(context.WithoutCancel(ctx), sb.ID()); err != nil {
			a.log.WarnContext(ctx, "Failed to remove the network check sandbox", "sandbox", sb.ID(), "error", err)
		}
	}()

	var out strings.Builder
	broker := a.brokerAddr()
	_, err = sb.Exec(ctx, sandbox.ExecRequest{
		Cmd:     []string{sandbox.UmpBinary, "netcheck", "--reach", broker, "--unreachable", apiServer},
		User:    sandbox.UserRoot,
		Stdout:  &out,
		Timeout: time.Minute,
	})
	if err != nil {
		return fmt.Errorf("the network check sandbox failed: %w", err)
	}

	// Each line says what one address did, so the outcome is read from them rather than the exit code
	result := map[string]string{}
	for line := range strings.Lines(out.String()) {
		state, addr, _ := strings.Cut(strings.TrimSpace(line), " ")
		result[addr] = state
	}
	if result[apiServer] != "unreachable" {
		return fmt.Errorf("%w: a sandbox without network reached the Kubernetes API server at %s; install a network plugin that enforces NetworkPolicies and the sandbox policies of the Helm chart, or set sandbox.kubernetes.require_network_policy to false to run without them", errUnenforced, apiServer)
	}
	if result[broker] != "reachable" {
		return fmt.Errorf("sandboxes cannot reach the broker at %s, so check that the sandbox NetworkPolicy lets them reach the Umpteenth pods on port %d", broker, a.cfg.BrokerPort)
	}
	return nil
}

// apiServerAddr is the address pods reach the API server on, from the variables every pod gets or from the kubernetes Service
func (a *Adapter) apiServerAddr(ctx context.Context) string {
	if host, port := os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT"); host != "" && port != "" {
		return net.JoinHostPort(host, port)
	}
	svc, err := a.kube.apiServerService(ctx)
	if err != nil || svc.Spec.ClusterIP == "" || len(svc.Spec.Ports) == 0 {
		return ""
	}
	return net.JoinHostPort(svc.Spec.ClusterIP, fmt.Sprint(svc.Spec.Ports[0].Port))
}
