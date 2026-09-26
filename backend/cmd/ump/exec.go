package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// The exec shim exists because the Docker Engine API cannot kill an exec (PLAN.md §4.7.1)
// Protocol with the adapter, all through one pid file per exec:
//   - `ump exec --pidfile P -- cmd...` starts cmd in a new process group and atomically creates P containing the group ID
//   - `ump exec --kill P`, run as a second exec with the same user, sends SIGTERM to the group, then SIGKILL after the grace period
//   - if the kill arrives before the shim registered, it creates P with a cancel marker and the shim cancels the command as soon as it starts
//   - when the command exits the shim removes P, unless the out-of-memory killer ended it; then P holds an OOM marker for the adapter to read
//   - the shim exits with the command's exit code, or 128+signal when the command was killed

// Markers written to the pid file instead of a process group ID
const (
	markerCancelled = "cancelled"
	markerOOM       = "oom"
)

const (
	defaultKillGrace = 1500 * time.Millisecond
	// outputDrainDelay bounds how long output of lingering background processes is forwarded after the command exited
	outputDrainDelay = 500 * time.Millisecond
	killPollInterval = 25 * time.Millisecond
)

// oomEventFiles hold the cgroup's oom_kill counter on cgroup v2 and v1; tests point them elsewhere
var oomEventFiles = []string{"/sys/fs/cgroup/memory.events", "/sys/fs/cgroup/memory/memory.oom_control"}

func init() {
	register("exec", command{
		Summary:  "Run a command in its own process group so the sandbox adapter can cancel it",
		Internal: true,
		Run:      runExec,
	})
}

// shimOptions configure one shimmed command
type shimOptions struct {
	Args    []string
	Pidfile string
	Grace   time.Duration
}

func runExec(args []string) int {
	flags := flag.NewFlagSet("ump exec", flag.ContinueOnError)
	pidfile := flags.String("pidfile", "", "record the command's process group in this file")
	kill := flags.String("kill", "", "terminate the process group recorded in this pid file and exit")
	grace := flags.Duration("grace", defaultKillGrace, "time between SIGTERM and SIGKILL")
	flags.Usage = func() {
		_, _ = fmt.Fprintln(flags.Output(), "Usage: ump exec [--pidfile file] -- command [args...]")
		_, _ = fmt.Fprintln(flags.Output(), "       ump exec --kill file")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return 2
	}

	// Kill mode runs as a separate exec while the shim is still waiting for its command
	if *kill != "" {
		if err := killRecorded(*kill, *grace); err != nil {
			errorf("exec", "%v", err)
			return 1
		}
		return 0
	}

	if flags.NArg() == 0 {
		flags.Usage()
		return 2
	}
	return shim(shimOptions{Args: flags.Args(), Pidfile: *pidfile, Grace: *grace}, os.Stdin, os.Stdout, os.Stderr)
}

// shim runs a command in a new process group, records the group, and returns the exit code the adapter reports
func shim(opts shimOptions, stdin io.Reader, stdout, stderr io.Writer) int {
	// Survive a caller that hangs up, so the command still runs to completion and its status is recorded
	pipeSignals := make(chan os.Signal, 1)
	signal.Notify(pipeSignals, syscall.SIGPIPE)
	defer signal.Stop(pipeSignals)

	// Remember the OOM kill counter so a later SIGKILL can be attributed to the memory limit
	oomBefore, oomKnown := readOOMKills()

	// Start the command as the leader of a new process group, which is what kill mode signals
	// #nosec G204 -- running the caller's command is the purpose of the exec shim
	cmd := exec.Command(opts.Args[0], opts.Args[1:]...)
	cmd.Stdin = stdin
	// Wrapping the writers forces pipes, so background processes cannot hold the adapter's stream open after the command exited
	cmd.Stdout = struct{ io.Writer }{stdout}
	cmd.Stderr = struct{ io.Writer }{stderr}
	cmd.WaitDelay = outputDrainDelay
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		_, _ = fmt.Fprintf(stderr, "ump exec: %v\n", err)
		return startFailureCode(err)
	}
	pgid := cmd.Process.Pid

	// Forward termination signals to the whole group, like a shell does for its foreground job
	forwarded := make(chan os.Signal, 4)
	signal.Notify(forwarded, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP, syscall.SIGQUIT)
	stopForwarding := make(chan struct{})
	defer func() {
		signal.Stop(forwarded)
		close(stopForwarding)
	}()
	go func() {
		for {
			select {
			case s := <-forwarded:
				if sig, ok := s.(syscall.Signal); ok {
					_ = syscall.Kill(-pgid, sig)
				}
			case <-stopForwarding:
				return
			}
		}
	}()

	// Register the group; a kill that arrived first left a marker, so the command is cancelled right away
	if opts.Pidfile != "" {
		err := writeExclusive(opts.Pidfile, strconv.Itoa(pgid))
		switch {
		case errors.Is(err, fs.ErrExist):
			go func() { _ = terminateGroup(pgid, opts.Grace) }()
		case err != nil:
			// A command that cannot be cancelled must not run at all
			_, _ = fmt.Fprintf(stderr, "ump exec: cannot record the process group: %v\n", err)
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
			_ = cmd.Wait()
			return 126
		}
	}

	// Wait for the command; output errors after a hang-up are irrelevant because only the exit status matters
	_ = cmd.Wait()
	if cmd.ProcessState == nil {
		return 1
	}
	status, _ := cmd.ProcessState.Sys().(syscall.WaitStatus)

	// Attribute a SIGKILL to the memory limit when the cgroup's OOM kill counter moved while the command ran
	oom := false
	if oomKnown && status.Signaled() && status.Signal() == syscall.SIGKILL {
		if after, ok := readOOMKills(); ok && after > oomBefore {
			oom = true
		}
	}

	// Leave the OOM marker for the adapter, otherwise clean up so the run directory does not grow
	if opts.Pidfile != "" {
		if oom {
			_ = os.WriteFile(opts.Pidfile, []byte(markerOOM+"\n"), 0o600)
		} else {
			_ = os.Remove(opts.Pidfile)
		}
	}
	return exitCode(status)
}

// killRecorded terminates the process group recorded by a shim
// It is safe at any point of the shim's life: before registration it leaves a cancel marker, after the command exited it does nothing
func killRecorded(path string, grace time.Duration) error {
	// Leave a cancel marker when the shim has not registered yet
	err := writeExclusive(path, markerCancelled)
	if err == nil {
		return nil
	}
	if !errors.Is(err, fs.ErrExist) {
		return err
	}

	// The shim registered, so read its process group
	data, err := os.ReadFile(path) // #nosec G304 -- the pid file path comes from the adapter that runs this shim
	if errors.Is(err, fs.ErrNotExist) {
		// The command finished between the two steps
		return nil
	}
	if err != nil {
		return err
	}
	pgid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		// A marker means the command was already cancelled or has finished
		return nil //nolint:nilerr // the marker is expected content, not a failure
	}
	return terminateGroup(pgid, grace)
}

// terminateGroup sends SIGTERM to a process group and SIGKILL once the grace period is over
func terminateGroup(pgid int, grace time.Duration) error {
	// A corrupted pid file must never make us signal init or every process of the user
	if pgid <= 1 {
		return fmt.Errorf("refusing to signal process group %d", pgid)
	}

	// Ask politely first, and wake stopped processes so they can handle SIGTERM
	if err := syscall.Kill(-pgid, syscall.SIGTERM); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return fmt.Errorf("failed to send SIGTERM to process group %d: %w", pgid, err)
	}
	_ = syscall.Kill(-pgid, syscall.SIGCONT)

	// Poll until the group is gone or the grace period is over
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		time.Sleep(killPollInterval)
		if err := syscall.Kill(-pgid, 0); errors.Is(err, syscall.ESRCH) {
			return nil
		}
	}

	// Force whatever is left
	if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("failed to send SIGKILL to process group %d: %w", pgid, err)
	}
	return nil
}

// writeExclusive atomically creates path with content and fails with fs.ErrExist when it already exists
// Linking a complete temporary file means readers never observe a half-written pid file
func writeExclusive(path, content string) error {
	tmp := fmt.Sprintf("%s.%d.tmp", path, os.Getpid())
	if err := os.WriteFile(tmp, []byte(content+"\n"), 0o600); err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp) }()
	return os.Link(tmp, path)
}

// readOOMKills returns the cgroup's oom_kill counter, or false when no cgroup memory controller is visible
func readOOMKills() (int64, bool) {
	for _, path := range oomEventFiles {
		data, err := os.ReadFile(path) // #nosec G304 -- the paths are the fixed cgroup files, only tests pass others
		if err != nil {
			continue
		}
		for line := range strings.SplitSeq(string(data), "\n") {
			key, value, ok := strings.Cut(line, " ")
			if !ok || key != "oom_kill" {
				continue
			}
			if n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil {
				return n, true
			}
		}
	}
	return 0, false
}

// exitCode maps a wait status to a shell-style exit code
func exitCode(status syscall.WaitStatus) int {
	if status.Signaled() {
		return 128 + int(status.Signal())
	}
	return status.ExitStatus()
}

// startFailureCode uses the shell conventions for commands that cannot be started
func startFailureCode(err error) int {
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
		return 127
	}
	return 126
}
