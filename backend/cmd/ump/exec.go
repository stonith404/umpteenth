package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// The exec shim exists because the Docker Engine API cannot kill an exec
// Protocol with the adapter, all through one pid file per exec:
//   - `ump exec --pidfile P -- cmd...` starts cmd in a new process group and atomically creates P containing the group ID
//   - `ump exec --kill P`, run as a second exec with the same user, sends SIGTERM to the group, then SIGKILL after the grace period
//   - if the kill arrives before the shim registered, it creates P with a cancel marker and the shim cancels the command as soon as it starts
//   - when the command exits the shim removes P, unless the out-of-memory killer ended it; then P holds an OOM marker for the adapter to read
//   - the shim exits with the command's exit code, or 128+signal when the command was killed
// Adapters whose exec API has no user, working directory or environment, such as Kubernetes, pass them to the shim instead:
//   - --user and --workdir set the command's uid and directory, while the shim itself keeps running as root so the kill mode can always signal it
//   - --env-stdin reads the environment as one JSON line ahead of the command's stdin, which keeps secrets off the command line other users can read in /proc

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

// maxEnvHeader bounds the JSON line --env-stdin reads before the command's stdin
const maxEnvHeader = 1 << 20

// shimOptions configure one shimmed command
type shimOptions struct {
	Args    []string
	Pidfile string
	Grace   time.Duration
	// User runs the command as another user when set
	User *syscall.Credential
	// WorkDir is the command's working directory, the shim's own when empty
	WorkDir string
	// Env is added to the shim's environment
	Env map[string]string
}

func runExec(args []string) int {
	flags := flag.NewFlagSet("ump exec", flag.ContinueOnError)
	pidfile := flags.String("pidfile", "", "record the command's process group in this file")
	kill := flags.String("kill", "", "terminate the process group recorded in this pid file and exit")
	grace := flags.Duration("grace", defaultKillGrace, "time between SIGTERM and SIGKILL")
	user := flags.String("user", "", "run the command as this uid[:gid]")
	workDir := flags.String("workdir", "", "run the command in this directory")
	envStdin := flags.Bool("env-stdin", false, "read the command's environment as a JSON object on the first line of stdin")
	flags.Usage = func() {
		_, _ = fmt.Fprintln(flags.Output(), "Usage: ump exec [--pidfile file] [--user uid[:gid]] [--workdir dir] [--env-stdin] -- command [args...]")
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
	opts := shimOptions{Args: flags.Args(), Pidfile: *pidfile, Grace: *grace, WorkDir: *workDir}
	if *user != "" {
		cred, err := parseUser(*user)
		if err != nil {
			errorf("exec", "%v", err)
			return 2
		}
		opts.User = cred
	}

	// The environment comes first on stdin, and whatever follows is the command's own input
	var stdin io.Reader = os.Stdin
	if *envStdin {
		reader := bufio.NewReader(os.Stdin)
		env, err := readEnvHeader(reader)
		if err != nil {
			errorf("exec", "%v", err)
			return 126
		}
		opts.Env = env
		stdin = reader
	}
	return shim(opts, stdin, os.Stdout, os.Stderr)
}

// parseUser reads a uid[:gid] pair, where the gid defaults to the uid like the sandbox's users have it
func parseUser(value string) (*syscall.Credential, error) {
	uidText, gidText, hasGID := strings.Cut(value, ":")
	uid, err := strconv.ParseUint(uidText, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("invalid user %q, use a numeric uid[:gid]", value)
	}
	gid := uid
	if hasGID {
		gid, err = strconv.ParseUint(gidText, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("invalid user %q, use a numeric uid[:gid]", value)
		}
	}
	// An empty group list drops root's supplementary groups
	return &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid), Groups: []uint32{}}, nil
}

// readEnvHeader reads the JSON object on the first line of stdin
func readEnvHeader(r *bufio.Reader) (map[string]string, error) {
	var line []byte
	for {
		chunk, err := r.ReadSlice('\n')
		line = append(line, chunk...)
		if len(line) > maxEnvHeader {
			return nil, errors.New("the environment header is too long")
		}
		if err == nil {
			break
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return nil, fmt.Errorf("failed to read the environment header: %w", err)
		}
	}
	env := map[string]string{}
	if err := json.Unmarshal(line, &env); err != nil {
		return nil, fmt.Errorf("invalid environment header: %w", err)
	}
	return env, nil
}

// commandEnv is the shim's environment with the requested variables on top
// A command that runs as another user gets that user's home, since the shim's own is root's
func commandEnv(opts shimOptions) []string {
	env := map[string]string{}
	for _, entry := range os.Environ() {
		key, value, _ := strings.Cut(entry, "=")
		env[key] = value
	}
	if opts.User != nil && int(opts.User.Uid) != os.Getuid() {
		env["HOME"] = homeOf(opts.User.Uid)
	}
	maps.Copy(env, opts.Env)
	list := make([]string, 0, len(env))
	for _, key := range slices.Sorted(maps.Keys(env)) {
		list = append(list, key+"="+env[key])
	}
	return list
}

// passwdFile lists the image's users; tests point it elsewhere
var passwdFile = "/etc/passwd"

// homeOf looks a uid's home directory up in the image's passwd file, falling back to / like Docker does for users without an entry
func homeOf(uid uint32) string {
	data, err := os.ReadFile(passwdFile)
	if err != nil {
		return "/"
	}
	want := strconv.FormatUint(uint64(uid), 10)
	for line := range strings.Lines(string(data)) {
		fields := strings.Split(strings.TrimSpace(line), ":")
		if len(fields) >= 6 && fields[2] == want && fields[5] != "" {
			return fields[5]
		}
	}
	return "/"
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
	cmd.Dir = opts.WorkDir
	if opts.User != nil || opts.Env != nil {
		cmd.Env = commandEnv(opts)
	}
	stdinPipe, err := commandStdin(cmd, stdin)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "ump exec: %v\n", err)
		return 126
	}
	// Wrapping the writers forces pipes, so background processes cannot hold the adapter's stream open after the command exited
	cmd.Stdout = struct{ io.Writer }{stdout}
	cmd.Stderr = struct{ io.Writer }{stderr}
	cmd.WaitDelay = outputDrainDelay
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Credential: opts.User}
	err = cmd.Start()
	if stdinPipe != nil {
		_ = stdinPipe.Close()
	}
	if err != nil {
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

// commandStdin connects stdin to the command, returning the read end the shim must close once the command started
// A reader that is not a file is copied through a pipe of the shim's own, because exec.Cmd would wait for that copy and a caller that keeps stdin open would delay every exit
func commandStdin(cmd *exec.Cmd, stdin io.Reader) (*os.File, error) {
	if stdin == nil {
		return nil, nil
	}
	if f, ok := stdin.(*os.File); ok {
		cmd.Stdin = f
		return nil, nil
	}
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	go func() {
		_, _ = io.Copy(w, stdin)
		_ = w.Close()
	}()
	cmd.Stdin = r
	return r, nil
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
