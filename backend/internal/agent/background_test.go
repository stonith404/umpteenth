//go:build unit

package agent_test

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/agent"
	"github.com/stonith404/umpteenth/backend/internal/llm"
	"github.com/stonith404/umpteenth/backend/internal/sandbox"
	sandboxfake "github.com/stonith404/umpteenth/backend/internal/sandbox/fake"
)

// backgroundTimeout is far above what a command that leaves a background process should take, so hitting it means the call waited for that process
const backgroundTimeout = 20 * time.Second

// localSandbox returns sandbox tools whose commands run on this machine the way ump exec runs them in a sandbox, and the directory that stands in for /ump/logs
// Like the shim, each command gets its own process group and forced pipes that are closed shortly after it exited, and a timeout kills the whole group
func localSandbox(t *testing.T) (*agent.SandboxTools, string) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not installed")
	}
	logDir := t.TempDir()

	// Background processes outlive the call on purpose, so the test ends them
	var mu sync.Mutex
	var groups []int
	t.Cleanup(func() {
		mu.Lock()
		defer mu.Unlock()
		for _, pgid := range groups {
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
		}
	})

	adapter := sandboxfake.New()
	adapter.Default(func(ctx context.Context, _ *sandboxfake.Sandbox, req sandbox.ExecRequest) (sandbox.ExecResult, error) {
		// A login shell would read this machine's profile, and the sandbox's log directory becomes the temporary one
		args := slices.Clone(req.Cmd)
		for i, arg := range args {
			args[i] = strings.ReplaceAll(arg, "/ump/logs", logDir)
		}
		if args[1] == "-lc" {
			args[1] = "-c"
		}

		// #nosec G204 -- the test runs the tool's own script
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Stdin = req.Stdin
		cmd.Stdout = struct{ io.Writer }{req.Stdout}
		cmd.Stderr = struct{ io.Writer }{req.Stderr}
		cmd.WaitDelay = 500 * time.Millisecond
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := cmd.Start(); err != nil {
			return sandbox.ExecResult{}, err
		}
		mu.Lock()
		groups = append(groups, cmd.Process.Pid)
		mu.Unlock()

		// Wait for the command, and kill its group once the timeout ends the context
		done := make(chan struct{})
		go func() {
			_ = cmd.Wait()
			close(done)
		}()
		select {
		case <-done:
			return sandbox.ExecResult{ExitCode: cmd.ProcessState.ExitCode()}, nil
		case <-ctx.Done():
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
			<-done
			return sandbox.ExecResult{ExitCode: 143}, nil
		}
	})
	sb, err := adapter.Create(context.Background(), sandbox.Spec{Image: "img"})
	require.NoError(t, err)
	return &agent.SandboxTools{Sandbox: sb, User: sandbox.UserAgent, DefaultTimeout: backgroundTimeout, MaxTimeout: backgroundTimeout}, logDir
}

// backgroundProcess starts a process that inherits the command's stdout and stderr, as with `server &`, and prints "late" only once release exists
// Waiting for release instead of a fixed time keeps "late" out of the call's own output however slow the machine is
func backgroundProcess(release string) string {
	return "(until [ -e '" + release + "' ]; do sleep 0.05; done; echo late; sleep 1; echo still alive; exec sleep 60) &"
}

// assertReturnsDespiteBackgroundProcess runs a tool whose command leaves backgroundProcess(release) behind, then lets that process print after the call returned
func assertReturnsDespiteBackgroundProcess(t *testing.T, tool agent.Tool, args, release, logDir string) {
	t.Helper()

	// The call returns once its own command is done, with its exit code and output
	start := time.Now()
	res := tool.Run(context.Background(), llm.ToolCall{ID: "c1", Name: tool.Def().Name, Args: json.RawMessage(args)})
	elapsed := time.Since(start)
	assert.Less(t, elapsed, backgroundTimeout/2, "the call waited for the background process: %s", res.Content)
	assert.Equal(t, false, res.Meta["timedOut"], res.Content)
	assert.Equal(t, 3, res.Meta["exitCode"], res.Content)
	assert.Contains(t, res.Content, "started")
	assert.NotContains(t, res.Content, "late")

	// The background process survives the call, and what it prints afterwards still lands in the log
	require.NoError(t, os.WriteFile(release, nil, 0o600))
	logPath := filepath.Join(logDir, "c1.txt")
	assert.Eventually(t, func() bool {
		data, _ := os.ReadFile(logPath) // #nosec G304 -- a file in the test's temporary directory
		return strings.Contains(string(data), "late\nstill alive\n")
	}, 10*time.Second, 50*time.Millisecond)
	data, _ := os.ReadFile(logPath) // #nosec G304 -- a file in the test's temporary directory
	assert.True(t, strings.HasPrefix(string(data), "started\n"), "log: %q", data)
}

func TestBashReturnsWhileABackgroundProcessRuns(t *testing.T) {
	tools, logDir := localSandbox(t)
	bash := tools.Tools()[0]
	require.Equal(t, "bash", bash.Def().Name)

	release := filepath.Join(t.TempDir(), "release")
	args, _ := json.Marshal(map[string]string{"command": backgroundProcess(release) + " echo started; exit 3"})
	assertReturnsDespiteBackgroundProcess(t, bash, string(args), release, logDir)
}

func TestToolkitScriptReturnsWhileABackgroundProcessRuns(t *testing.T) {
	tools, logDir := localSandbox(t)

	// A main script that starts a helper daemon and leaves it running
	dir := t.TempDir()
	release := filepath.Join(dir, "release")
	script := filepath.Join(dir, "main.sh")
	require.NoError(t, os.WriteFile(script, []byte("#!/usr/bin/env bash\n"+backgroundProcess(release)+"\necho started\nexit 3\n"), 0o700)) // #nosec G306 -- the script must be executable
	main := tools.ToolkitTool(agent.ToolkitScript{ToolName: "main", Path: script, Timeout: backgroundTimeout})
	assertReturnsDespiteBackgroundProcess(t, main, `{}`, release, logDir)
}
