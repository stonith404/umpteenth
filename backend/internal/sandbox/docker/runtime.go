package docker

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/stonith404/umpteenth/backend/internal/sandbox"
)

// runtimeProbeTimeout bounds the throwaway sandbox that checks a custom runtime
const runtimeProbeTimeout = 2 * time.Minute

// gvisorFix is how to configure runsc so the adapter's file transfers work, which the docs explain in detail
const gvisorFix = `configure the runtime in the engine's daemon.json with "runtimeArgs": ["--overlay2=none", "--file-access=shared"] and restart the engine`

// checkRuntime probes a custom runtime once and remembers the outcome, which Create and Check report
// runc needs no probe, since every other check of the adapter already exercises it
func (a *Adapter) checkRuntime(ctx context.Context) {
	if !a.customRuntime() {
		return
	}
	err := a.probeRuntime(ctx)
	if err != nil {
		a.log.ErrorContext(ctx, "Sandboxes can't run under the configured runtime", "runtime", a.cfg.Runtime, "error", err)
	}
	a.mu.Lock()
	a.runtimeErr = err
	a.mu.Unlock()
}

// runtimeProblem returns why the configured runtime can't host sandboxes, if the probe found a reason
func (a *Adapter) runtimeProblem() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.runtimeErr
}

// probeRuntime starts a throwaway sandbox under the configured runtime and checks that files cross between the engine and the sandbox in both directions
// gVisor keeps the root filesystem inside the sandbox by default, so files copied in and files written by commands never reach the other side
func (a *Adapter) probeRuntime(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, runtimeProbeTimeout)
	defer cancel()
	fix := ""
	if a.gvisor() {
		fix = "; " + gvisorFix
	}

	// A runtime that can't start containers at all, such as runsc without its helper binaries, fails here
	sb, err := a.Create(ctx, sandbox.Spec{RunID: "probe-" + randomID(6), Network: sandbox.NetworkNone, TTL: runtimeProbeTimeout})
	if err != nil {
		return fmt.Errorf("a test sandbox under runtime %s failed to start: %w", a.cfg.Runtime, err)
	}
	defer func() { _ = a.Destroy(context.WithoutCancel(ctx), sb.ID()) }()

	// A file copied into the running sandbox must be visible to its commands
	const content = "umpteenth runtime probe"
	err = sb.PutFiles(ctx, []sandbox.File{{Path: sandbox.WorkspaceDir + "/.ump-probe-in", Mode: 0o644, Owner: sandbox.UserAgent, Content: []byte(content)}})
	if err != nil {
		return fmt.Errorf("failed to copy a file into a test sandbox: %w", err)
	}
	var out bytes.Buffer
	res, err := sb.Exec(ctx, sandbox.ExecRequest{Cmd: []string{"cat", sandbox.WorkspaceDir + "/.ump-probe-in"}, Stdout: &out, Timeout: time.Minute})
	if err != nil {
		return fmt.Errorf("failed to run a command in a test sandbox: %w", err)
	}
	if res.ExitCode != 0 || strings.TrimSpace(out.String()) != content {
		return fmt.Errorf("files copied into a running sandbox are invisible to its commands under runtime %s%s", a.cfg.Runtime, fix)
	}

	// A file a command writes must be readable from outside, which is how outputs and artifacts leave the sandbox
	res, err = sb.Exec(ctx, sandbox.ExecRequest{Cmd: []string{"cp", sandbox.WorkspaceDir + "/.ump-probe-in", sandbox.WorkspaceDir + "/.ump-probe-out"}, Timeout: time.Minute})
	if err != nil {
		return fmt.Errorf("failed to run a command in a test sandbox: %w", err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("a command in a test sandbox failed to write a file with exit code %d", res.ExitCode)
	}
	data, err := sb.ReadFile(ctx, sandbox.WorkspaceDir+"/.ump-probe-out", 1024)
	if err != nil || string(data) != content {
		return fmt.Errorf("files written in a sandbox can't be read from outside under runtime %s%s", a.cfg.Runtime, fix)
	}
	return nil
}
