package docker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/pkg/stdcopy"

	"github.com/stonith404/umpteenth/backend/internal/sandbox"
)

// Egress filter modes, set with the sandbox.egress_filter option
const (
	// EgressFilterAuto installs the rules where the engine allows it and only warns where it doesn't
	EgressFilterAuto = "auto"
	// EgressFilterRequired refuses internet sandboxes when the rules can't be installed
	EgressFilterRequired = "required"
	// EgressFilterOff never touches the host firewall
	EgressFilterOff = "off"
)

// Chains this adapter owns in the host firewall; their content doesn't depend on the instance, so installations sharing an engine share them
const (
	chainPrivate  = "UMPTEENTH-PRIVATE"
	chainMetadata = "UMPTEENTH-METADATA"
	chainHost     = "UMPTEENTH-HOST"
)

// privateRanges are what an internet sandbox must not reach: private, shared, loopback, link-local, benchmarking and reserved IPv4 space
var privateRanges = []string{
	"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12",
	"192.0.0.0/24", "192.168.0.0/16", "198.18.0.0/15", "224.0.0.0/4", "240.0.0.0/4",
}

// metadataRanges stay blocked even for jobs that may reach the private network, since cloud metadata hands out the host's credentials
var metadataRanges = []string{"169.254.0.0/16", "100.100.100.200/32"}

// firewallTimeout bounds one run of the helper container
const firewallTimeout = time.Minute

// egressPrivate names the egress bridge of jobs that may reach the private network
func (a *Adapter) egressPrivate() string {
	return a.egress + "-private"
}

// firewallScript builds the idempotent shell script the helper runs in the host's namespaces
// Chains are filled rule by rule instead of flushed, so a second installation sharing the engine never sees them empty
func firewallScript(strictBridge, privateBridge string) string {
	var b strings.Builder
	b.WriteString("set -e\nipt='iptables -w 30'\n")
	b.WriteString("add() { chain=$1; shift; $ipt -C \"$chain\" \"$@\" 2>/dev/null || $ipt -A \"$chain\" \"$@\"; }\n")
	b.WriteString("jump() { chain=$1; shift; $ipt -C \"$chain\" \"$@\" 2>/dev/null || $ipt -I \"$chain\" 1 \"$@\"; }\n")
	for _, c := range []string{chainPrivate, chainMetadata, chainHost} {
		fmt.Fprintf(&b, "$ipt -N %s 2>/dev/null || true\n", c)
		// Replies to connections the sandbox opened are always fine
		fmt.Fprintf(&b, "add %s -m conntrack --ctstate RELATED,ESTABLISHED -j RETURN\n", c)
	}
	for _, r := range privateRanges {
		fmt.Fprintf(&b, "add %s -d %s -j REJECT\n", chainPrivate, r)
	}
	for _, r := range metadataRanges {
		fmt.Fprintf(&b, "add %s -d %s -j REJECT\n", chainMetadata, r)
	}
	fmt.Fprintf(&b, "add %s -j REJECT\n", chainHost)

	// Forwarded traffic from the bridges passes DOCKER-USER, while traffic to the host itself passes INPUT
	fmt.Fprintf(&b, "jump DOCKER-USER -i %s -j %s\n", strictBridge, chainPrivate)
	fmt.Fprintf(&b, "jump DOCKER-USER -i %s -j %s\n", privateBridge, chainMetadata)
	fmt.Fprintf(&b, "jump INPUT -i %s -j %s\n", strictBridge, chainHost)
	fmt.Fprintf(&b, "jump INPUT -i %s -j %s\n", privateBridge, chainMetadata)

	// Jumps for bridges that no longer exist, e.g. of removed test networks, are cleaned up
	fmt.Fprintf(&b, `for base in DOCKER-USER INPUT; do
  $ipt -S "$base" | grep -E -- '-j (%s|%s|%s)$' | while read -r rule; do
    dev=$(echo "$rule" | sed -n 's/.* -i \([^ ]*\) .*/\1/p')
    if [ -n "$dev" ] && [ ! -e "/sys/class/net/$dev" ]; then
      $ipt $(echo "$rule" | sed 's/^-A /-D /') || true
    fi
  done
done
`, chainPrivate, chainMetadata, chainHost)
	return b.String()
}

// bridgeName returns the host interface of a bridge network, which the firewall rules match on
func (a *Adapter) bridgeName(ctx context.Context, name string) (string, error) {
	info, err := a.cli.NetworkInspect(ctx, name, network.InspectOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to inspect network %s: %w", name, err)
	}
	if bridge := info.Options["com.docker.network.bridge.name"]; bridge != "" {
		return bridge, nil
	}
	if len(info.ID) < 12 {
		return "", fmt.Errorf("network %s has an unexpected ID", name)
	}
	return "br-" + info.ID[:12], nil
}

// ensureFirewall installs the host rules that keep internet sandboxes off private networks and the host (PLAN.md §4.7.3)
// The rules are applied again on every maintenance tick, because a restart of the engine or the host firewall drops them
func (a *Adapter) ensureFirewall(ctx context.Context) {
	if a.cfg.EgressFilter == EgressFilterOff {
		a.setFirewall(sandbox.EgressFilterOff, nil)
		return
	}
	err := a.applyFirewall(ctx)
	if err != nil {
		a.log.WarnContext(ctx, "Internet sandboxes can reach private networks, because the egress firewall could not be installed", "error", err)
		a.setFirewall(sandbox.EgressFilterUnavailable, err)
		return
	}
	a.setFirewall(sandbox.EgressFilterActive, nil)
}

func (a *Adapter) applyFirewall(ctx context.Context) error {
	// Podman keeps its own firewall chains, which these rules don't know about
	if a.podman {
		return errors.New("the egress firewall supports Docker only")
	}
	if err := a.ensureEgress(ctx); err != nil {
		return err
	}
	strict, err := a.bridgeName(ctx, a.egress)
	if err != nil {
		return err
	}
	private, err := a.bridgeName(ctx, a.egressPrivate())
	if err != nil {
		return err
	}

	// A privileged helper enters the host's namespaces and uses the host's own iptables, which matches the backend the engine uses
	ctx, cancel := context.WithTimeout(ctx, firewallTimeout)
	defer cancel()
	cfg := &container.Config{
		Image:      a.cfg.DefaultImage,
		Entrypoint: []string{"nsenter"},
		Cmd:        []string{"-t", "1", "-m", "-n", "--", "sh", "-c", firewallScript(strict, private)},
		User:       "0:0",
		Labels:     map[string]string{labelInstance: a.cfg.InstanceID, labelHost: a.cfg.HostID, labelRole: roleFirewall},
	}
	hostCfg := &container.HostConfig{Privileged: true, PidMode: "host", NetworkMode: "host"}
	created, err := a.cli.ContainerCreate(ctx, cfg, hostCfg, nil, nil, "")
	if err != nil {
		return fmt.Errorf("failed to create the firewall helper: %w", err)
	}
	defer func() {
		_ = a.cli.ContainerRemove(context.WithoutCancel(ctx), created.ID, container.RemoveOptions{Force: true})
	}()
	if err := a.cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		return fmt.Errorf("failed to start the firewall helper: %w", err)
	}

	waitCh, errCh := a.cli.ContainerWait(ctx, created.ID, container.WaitConditionNotRunning)
	var code int64
	select {
	case res := <-waitCh:
		code = res.StatusCode
	case err := <-errCh:
		return fmt.Errorf("failed to wait for the firewall helper: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("the firewall helper exited with %d: %s", code, a.containerOutput(ctx, created.ID))
	}
	return nil
}

// containerOutput returns the end of a container's combined output, for error messages
func (a *Adapter) containerOutput(ctx context.Context, id string) string {
	logs, err := a.cli.ContainerLogs(ctx, id, container.LogsOptions{ShowStdout: true, ShowStderr: true, Tail: "20"})
	if err != nil {
		return ""
	}
	defer logs.Close()
	var out bytes.Buffer
	_, _ = stdcopy.StdCopy(&out, &out, logs)
	return strings.TrimSpace(out.String())
}

func (a *Adapter) setFirewall(state sandbox.EgressFilter, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.firewall, a.firewallErr = state, err
	a.firewallChecked = true
}

// firewallState reports the state of the egress rules, installing them the first time a sandbox needs them
func (a *Adapter) firewallState(ctx context.Context) (sandbox.EgressFilter, error) {
	a.mu.Lock()
	checked := a.firewallChecked
	a.mu.Unlock()
	if !checked {
		a.ensureFirewall(ctx)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.firewall, a.firewallErr
}

// Maintain installs the egress rules again, since an engine or firewall restart drops them
func (a *Adapter) Maintain(ctx context.Context) error {
	a.ensureFirewall(ctx)
	return nil
}
