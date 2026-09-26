//go:build unit

package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// syncBuffer is a bytes.Buffer that can be read while the shim writes to it
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// runShim starts the shim in the background and returns a channel with its exit code
func runShim(t *testing.T, opts shimOptions, stdout, stderr *syncBuffer) <-chan int {
	t.Helper()
	if opts.Grace == 0 {
		opts.Grace = defaultKillGrace
	}
	done := make(chan int, 1)
	go func() { done <- shim(opts, strings.NewReader(""), stdout, stderr) }()
	return done
}

// waitFor polls cond until it holds or the timeout expires
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	require.Eventually(t, cond, timeout, 10*time.Millisecond)
}

// processGone reports whether pid no longer exists
func processGone(pid int) bool {
	return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}

func TestShimExitCodeAndOutput(t *testing.T) {
	var stdout, stderr syncBuffer
	code := shim(shimOptions{Args: []string{"sh", "-c", "echo out; echo err >&2; exit 7"}}, strings.NewReader(""), &stdout, &stderr)
	assert.Equal(t, 7, code)
	assert.Equal(t, "out\n", stdout.String())
	assert.Equal(t, "err\n", stderr.String())
}

func TestShimForwardsStdin(t *testing.T) {
	var stdout, stderr syncBuffer
	code := shim(shimOptions{Args: []string{"cat"}}, strings.NewReader("hello\nworld\n"), &stdout, &stderr)
	assert.Equal(t, 0, code)
	assert.Equal(t, "hello\nworld\n", stdout.String())
}

func TestShimCommandNotFound(t *testing.T) {
	var stdout, stderr syncBuffer
	code := shim(shimOptions{Args: []string{"ump-definitely-missing-command"}}, strings.NewReader(""), &stdout, &stderr)
	assert.Equal(t, 127, code)
	assert.Contains(t, stderr.String(), "ump exec:")
}

func TestShimRemovesPidfileAfterExit(t *testing.T) {
	pidfile := filepath.Join(t.TempDir(), "exec.pid")
	var stdout, stderr syncBuffer
	code := shim(shimOptions{Args: []string{"true"}, Pidfile: pidfile}, strings.NewReader(""), &stdout, &stderr)
	assert.Equal(t, 0, code)
	assert.NoFileExists(t, pidfile)
}

func TestShimDoesNotWaitForBackgroundProcesses(t *testing.T) {
	var stdout, stderr syncBuffer
	start := time.Now()
	code := shim(shimOptions{Args: []string{"sh", "-c", "sleep 30 & echo $!"}}, strings.NewReader(""), &stdout, &stderr)
	assert.Equal(t, 0, code)
	assert.Less(t, time.Since(start), 5*time.Second)

	// The background process keeps running, since only cancellation kills the group
	pid, err := strconv.Atoi(strings.TrimSpace(stdout.String()))
	require.NoError(t, err)
	assert.False(t, processGone(pid))
	_ = syscall.Kill(pid, syscall.SIGKILL)
}

func TestKillTerminatesWholeGroup(t *testing.T) {
	pidfile := filepath.Join(t.TempDir(), "exec.pid")
	var stdout, stderr syncBuffer
	done := runShim(t, shimOptions{Args: []string{"sh", "-c", "sleep 60 & echo $!; sleep 60"}, Pidfile: pidfile}, &stdout, &stderr)

	// Wait until the shim registered and the background child reported its pid
	waitFor(t, 5*time.Second, func() bool {
		_, err := os.Stat(pidfile)
		return err == nil && strings.HasSuffix(stdout.String(), "\n")
	})
	bgPid, err := strconv.Atoi(strings.TrimSpace(stdout.String()))
	require.NoError(t, err)

	// Kill mode returns once the group is gone and the shim reports SIGTERM
	start := time.Now()
	require.NoError(t, killRecorded(pidfile, defaultKillGrace))
	select {
	case code := <-done:
		assert.Equal(t, 128+int(syscall.SIGTERM), code)
	case <-time.After(3 * time.Second):
		t.Fatal("shim did not exit after kill")
	}
	assert.Less(t, time.Since(start), 2*time.Second)
	waitFor(t, 2*time.Second, func() bool { return processGone(bgPid) })
	assert.NoFileExists(t, pidfile)
}

func TestKillEscalatesToSigkill(t *testing.T) {
	pidfile := filepath.Join(t.TempDir(), "exec.pid")
	var stdout, stderr syncBuffer
	grace := 300 * time.Millisecond
	done := runShim(t, shimOptions{Args: []string{"sh", "-c", "trap '' TERM; echo ready; sleep 60"}, Pidfile: pidfile, Grace: grace}, &stdout, &stderr)
	waitFor(t, 5*time.Second, func() bool {
		_, err := os.Stat(pidfile)
		return err == nil && stdout.String() == "ready\n"
	})

	require.NoError(t, killRecorded(pidfile, grace))
	select {
	case code := <-done:
		assert.Equal(t, 128+int(syscall.SIGKILL), code)
	case <-time.After(3 * time.Second):
		t.Fatal("shim did not exit after SIGKILL")
	}
}

func TestKillBeforeRegistrationCancelsCommand(t *testing.T) {
	pidfile := filepath.Join(t.TempDir(), "exec.pid")

	// The kill arrives first and leaves the cancel marker
	require.NoError(t, killRecorded(pidfile, defaultKillGrace))
	data, err := os.ReadFile(pidfile) // #nosec G304 -- test file in a temp dir
	require.NoError(t, err)
	assert.Equal(t, markerCancelled+"\n", string(data))

	// The shim then starts, notices the marker and terminates its command immediately
	var stdout, stderr syncBuffer
	start := time.Now()
	done := runShim(t, shimOptions{Args: []string{"sleep", "60"}, Pidfile: pidfile}, &stdout, &stderr)
	select {
	case code := <-done:
		assert.Equal(t, 128+int(syscall.SIGTERM), code)
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled command kept running")
	}
	assert.Less(t, time.Since(start), 2*time.Second)
	assert.NoFileExists(t, pidfile)
}

func TestKillAfterExitIsNoop(t *testing.T) {
	dir := t.TempDir()

	// A marker means there is nothing left to kill
	oomFile := filepath.Join(dir, "oom.pid")
	require.NoError(t, os.WriteFile(oomFile, []byte(markerOOM+"\n"), 0o600))
	require.NoError(t, killRecorded(oomFile, defaultKillGrace))

	// A group that no longer exists is not an error either
	var stdout, stderr syncBuffer
	pidfile := filepath.Join(dir, "gone.pid")
	done := runShim(t, shimOptions{Args: []string{"sh", "-c", "echo $$"}}, &stdout, &stderr)
	<-done
	require.NoError(t, os.WriteFile(pidfile, []byte(strings.TrimSpace(stdout.String())+"\n"), 0o600))
	require.NoError(t, killRecorded(pidfile, defaultKillGrace))
}

func TestTerminateGroupRejectsDangerousGroups(t *testing.T) {
	assert.Error(t, terminateGroup(0, time.Millisecond))
	assert.Error(t, terminateGroup(1, time.Millisecond))
	assert.Error(t, terminateGroup(-5, time.Millisecond))
}

func TestReadOOMKills(t *testing.T) {
	dir := t.TempDir()
	events := filepath.Join(dir, "memory.events")
	require.NoError(t, os.WriteFile(events, []byte("low 0\nhigh 0\nmax 3\noom 2\noom_kill 2\noom_group_kill 0\n"), 0o600))

	original := oomEventFiles
	t.Cleanup(func() { oomEventFiles = original })

	oomEventFiles = []string{filepath.Join(dir, "missing"), events}
	n, ok := readOOMKills()
	assert.True(t, ok)
	assert.EqualValues(t, 2, n)

	oomEventFiles = []string{filepath.Join(dir, "missing")}
	_, ok = readOOMKills()
	assert.False(t, ok)
}

func TestShimReportsOOMThroughPidfile(t *testing.T) {
	dir := t.TempDir()
	events := filepath.Join(dir, "memory.events")
	require.NoError(t, os.WriteFile(events, []byte("oom_kill 0\n"), 0o600))
	original := oomEventFiles
	t.Cleanup(func() { oomEventFiles = original })
	oomEventFiles = []string{events}

	// Simulate the OOM killer: bump the counter, then SIGKILL the command
	pidfile := filepath.Join(dir, "exec.pid")
	script := "echo 'oom_kill 1' > " + events + "; kill -KILL $$"
	var stdout, stderr syncBuffer
	code := shim(shimOptions{Args: []string{"sh", "-c", script}, Pidfile: pidfile}, strings.NewReader(""), &stdout, &stderr)
	assert.Equal(t, 128+int(syscall.SIGKILL), code)
	data, err := os.ReadFile(pidfile) // #nosec G304 -- test file in a temp dir
	require.NoError(t, err)
	assert.Equal(t, markerOOM+"\n", string(data))
}

func TestDispatch(t *testing.T) {
	var stdout, stderr bytes.Buffer
	assert.Equal(t, 0, run([]string{"help"}, &stdout, &stderr))
	assert.NotContains(t, stdout.String(), "exec", "internal commands must stay hidden")

	stdout.Reset()
	assert.Equal(t, 2, run([]string{"no-such-command"}, &stdout, &stderr))
	assert.Contains(t, stderr.String(), "unknown command")
}
