package docker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/stdcopy"

	"github.com/stonith404/umpteenth/backend/internal/sandbox"
)

const (
	// streamGrace is how long the output stream may take to end after the process tree was killed
	streamGrace = 2 * time.Second
	// exitCodeWait bounds how long the engine may take to record an exit code after the stream ended, and each inspection that explains a failure
	exitCodeWait = 5 * time.Second
	// killTimeout bounds the kill exec, whose shim gives up after its own 1.5s grace period
	killTimeout = 10 * time.Second
	// maxStderrBuffer caps unread stderr of an attached process, so it never blocks stdout
	maxStderrBuffer = 1 << 20
	// exitSIGKILL is how the shim and the engine report a process killed by SIGKILL
	exitSIGKILL = 128 + 9
	// exitLost is what the engine reports when the runtime lost track of a process without a signal
	exitLost = 128
	// stopGrace bounds how long a command that may have died with its sandbox waits for the engine to record the stop
	stopGrace = 500 * time.Millisecond
)

// containerSandbox is one sandbox container
type containerSandbox struct {
	a  *Adapter
	id string
	// agentUser runs commands whose request names no user
	agentUser sandbox.User
}

// execHandle is a running exec and what is needed to kill it
type execHandle struct {
	id      string
	user    string
	pidfile string
	resp    types.HijackedResponse
}

// ID returns the container ID
func (s *containerSandbox) ID() string {
	return s.id
}

// Exec runs a command through the ump exec shim and waits for it
func (s *containerSandbox) Exec(ctx context.Context, req sandbox.ExecRequest) (sandbox.ExecResult, error) {
	h, err := s.startExec(ctx, req, req.Stdin != nil)
	if err != nil {
		return sandbox.ExecResult{}, err
	}
	defer h.resp.Close()

	// Feed stdin until it ends, then half-close so the command sees EOF
	if req.Stdin != nil {
		go func() {
			_, _ = io.Copy(h.resp.Conn, req.Stdin)
			_ = h.resp.CloseWrite()
		}()
	}

	// Demultiplex the engine's stream into the caller's writers while the command runs
	streamDone := make(chan struct{})
	go func() {
		defer close(streamDone)
		_, _ = stdcopy.StdCopy(writerOrDiscard(req.Stdout), writerOrDiscard(req.Stderr), h.resp.Reader)
	}()

	// Wait for the command, its timeout or the caller, whichever comes first
	var expired <-chan time.Time
	if req.Timeout > 0 {
		timer := time.NewTimer(req.Timeout)
		defer timer.Stop()
		expired = timer.C
	}
	var timedOut, cancelled bool
	select {
	case <-streamDone:
	case <-expired:
		timedOut = true
	case <-ctx.Done():
		cancelled = true
	}

	// Kill the process tree through a second exec; the stream ends once the shim has exited
	if timedOut || cancelled {
		s.killExec(h)
		s.awaitStream(h, streamDone)
	}

	res, err := s.execResult(h, timedOut || cancelled)
	res.TimedOut = timedOut
	if cancelled && err == nil {
		err = fmt.Errorf("exec cancelled: %w", context.Cause(ctx))
	}
	return res, err
}

// Attach starts a long-lived process with interactive stdio
// Like exec.CommandContext, cancelling ctx or hitting req.Timeout kills the process tree
func (s *containerSandbox) Attach(ctx context.Context, req sandbox.ExecRequest) (sandbox.Process, error) {
	h, err := s.startExec(ctx, req, true)
	if err != nil {
		return nil, err
	}

	stdoutR, stdoutW := io.Pipe()
	p := &process{
		s:      s,
		h:      h,
		stdin:  &stdinWriter{resp: &h.resp},
		stdout: stdoutR,
		stderr: newBufferedPipe(maxStderrBuffer),
		done:   make(chan struct{}),
	}

	// Stdout pushes back on the process, while stderr is buffered so a caller that never reads it cannot stall stdout
	go func() {
		_, _ = stdcopy.StdCopy(stdoutW, p.stderr, h.resp.Reader)
		_ = stdoutW.Close()
		_ = p.stderr.Close()
		h.resp.Close()
		close(p.done)
	}()

	go p.watch(ctx, req.Timeout)
	return p, nil
}

// startExec creates and attaches an exec that runs the command through the ump exec shim
func (s *containerSandbox) startExec(ctx context.Context, req sandbox.ExecRequest, attachStdin bool) (*execHandle, error) {
	if len(req.Cmd) == 0 {
		return nil, errors.New("exec request has no command")
	}

	// Users are roles mapped to fixed uids, so images need no passwd entries
	user := req.User
	if user == "" {
		user = s.agentUser
	}
	uid := strconv.Itoa(user.UID())
	workDir := req.WorkDir
	if workDir == "" {
		workDir = sandbox.WorkspaceDir
	}

	h := &execHandle{
		user:    uid + ":" + uid,
		pidfile: path.Join(runDir, randomID(12)+".pid"),
	}
	cmd := append([]string{sandbox.UmpBinary, "exec", "--pidfile", h.pidfile, "--"}, req.Cmd...)
	created, err := s.a.cli.ContainerExecCreate(ctx, s.id, container.ExecOptions{
		User:         h.user,
		WorkingDir:   workDir,
		Env:          envList(req.Env),
		Cmd:          cmd,
		AttachStdin:  attachStdin,
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return nil, s.translate(ctx, fmt.Errorf("failed to create exec: %w", err))
	}
	h.id = created.ID

	// Attaching also starts the exec
	h.resp, err = s.a.cli.ContainerExecAttach(ctx, created.ID, container.ExecAttachOptions{})
	if err != nil {
		// The engine may have started the command before the caller gave up, and the shim's cancel marker stops it even if it has not registered yet
		if ctx.Err() != nil {
			s.killExec(h)
		}
		return nil, s.translate(ctx, fmt.Errorf("failed to start exec: %w", err))
	}
	return h, nil
}

// killExec terminates an exec's process group with `ump exec --kill`, run as the same user so it can signal it
// It returns once the kill mode finished, which is within its 1.5s grace period
func (s *containerSandbox) killExec(h *execHandle) {
	ctx, cancel := context.WithTimeout(context.Background(), killTimeout)
	defer cancel()

	created, err := s.a.cli.ContainerExecCreate(ctx, s.id, container.ExecOptions{
		User:         h.user,
		WorkingDir:   "/",
		Cmd:          []string{sandbox.UmpBinary, "exec", "--kill", h.pidfile},
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		s.a.log.WarnContext(ctx, "Failed to create the kill exec", "sandbox", s.id, "error", err)
		return
	}
	resp, err := s.a.cli.ContainerExecAttach(ctx, created.ID, container.ExecAttachOptions{})
	if err != nil {
		s.a.log.WarnContext(ctx, "Failed to start the kill exec", "sandbox", s.id, "error", err)
		return
	}
	defer resp.Close()

	var out bytes.Buffer
	_, _ = stdcopy.StdCopy(&out, &out, resp.Reader)
	if out.Len() > 0 {
		s.a.log.WarnContext(ctx, "The kill exec reported a problem", "sandbox", s.id, "output", strings.TrimSpace(out.String()))
	}
}

// awaitStream waits for the output stream to end after a kill, and cuts it off when it does not
func (s *containerSandbox) awaitStream(h *execHandle, done <-chan struct{}) {
	select {
	case <-done:
	case <-time.After(streamGrace):
		h.resp.Close()
		<-done
	}
}

// execResult reads the exit code the engine recorded and adds OOM and sandbox-gone detection
func (s *containerSandbox) execResult(h *execHandle, killed bool) (sandbox.ExecResult, error) {
	res := sandbox.ExecResult{ExitCode: -1}
	ctx, cancel := context.WithTimeout(context.Background(), exitCodeWait)
	defer cancel()

	// The stream can end a moment before the engine records the exit code
	for {
		inspect, err := s.a.cli.ContainerExecInspect(ctx, h.id)
		if err != nil {
			return res, s.translate(ctx, fmt.Errorf("failed to inspect exec: %w", err))
		}
		if !inspect.Running {
			res.ExitCode = inspect.ExitCode
			break
		}
		// A killed exec whose stream was cut off may never finish, and neither does one whose sandbox died
		if killed || ctx.Err() != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Only a signal or an unknown status can mean the whole sandbox went down, so the common case skips the extra inspect
	if res.ExitCode >= 0 && res.ExitCode < 128 {
		return res, nil
	}
	stateCtx, stateCancel := context.WithTimeout(context.Background(), exitCodeWait)
	defer stateCancel()
	state, err := s.state(stateCtx)

	// A sandbox that reaches its TTL, or runs out of memory under gVisor, goes down as a whole, and its commands end a moment before the engine records the stop
	// A SIGKILL we did not send waits briefly on every runtime, while exitLost only waits under gVisor, since git and others exit with 128 on ordinary errors
	mayBeStopping := (res.ExitCode == exitSIGKILL && !killed) || (s.a.gvisor() && res.ExitCode == exitLost)
	if err == nil && state.Running && mayBeStopping {
		state, err = s.awaitStop(stateCtx)
	}
	switch {
	case errors.Is(err, sandbox.ErrSandboxGone):
		return res, fmt.Errorf("%w: sandbox %s was removed while running a command", sandbox.ErrSandboxGone, s.id)
	case err != nil:
		s.a.log.WarnContext(stateCtx, "Failed to inspect the sandbox after a command was killed", "sandbox", s.id, "error", err)
	case !state.Running && state.OOMKilled:
		// Under gVisor the whole sandbox counts toward the memory limit, so running out of memory stops it instead of killing one process
		res.OOMKilled = true
		return res, fmt.Errorf("%w: sandbox %s ran out of memory and stopped while running a command", sandbox.ErrSandboxGone, s.id)
	case !state.Running:
		return res, fmt.Errorf("%w: sandbox %s stopped while running a command", sandbox.ErrSandboxGone, s.id)
	case res.ExitCode == exitSIGKILL:
		// SIGKILL in a running sandbox comes from the out-of-memory killer or from our own kill escalation
		res.OOMKilled = s.oomKilled(stateCtx, h, state)
	}
	return res, nil
}

// oomKilled reads the shim's OOM marker, and trusts the engine's sticky OOM flag only when the shim itself was killed
func (s *containerSandbox) oomKilled(ctx context.Context, h *execHandle, state *container.State) bool {
	data, err := s.readFile(ctx, h.pidfile, 64)
	if err != nil {
		return false
	}
	marker := strings.TrimSpace(string(data))
	if marker == "oom" {
		return true
	}
	_, shimKilled := strconv.Atoi(marker)
	return shimKilled == nil && state.OOMKilled
}

// awaitStop gives a sandbox that may be going down stopGrace to stop, and returns its state afterwards
func (s *containerSandbox) awaitStop(ctx context.Context) (*container.State, error) {
	waitCtx, cancel := context.WithTimeout(ctx, stopGrace)
	defer cancel()

	// A sandbox that keeps running ends the wait with the timeout, which is the common case of an ordinary SIGKILL
	waitCh, errCh := s.a.cli.ContainerWait(waitCtx, s.id, container.WaitConditionNotRunning)
	select {
	case <-waitCh:
	case <-errCh:
	}
	return s.state(ctx)
}

// state returns the container's state, or ErrSandboxGone when the container no longer exists
func (s *containerSandbox) state(ctx context.Context) (*container.State, error) {
	info, err := s.a.cli.ContainerInspect(ctx, s.id)
	if isNotFound(err) {
		return nil, sandbox.ErrSandboxGone
	}
	if err != nil {
		return nil, err
	}
	if info.State == nil {
		return nil, sandbox.ErrSandboxGone
	}
	return info.State, nil
}

// translate turns engine errors caused by a vanished or stopped container into ErrSandboxGone
func (s *containerSandbox) translate(ctx context.Context, err error) error {
	if !isNotFound(err) && !isConflict(err) {
		return err
	}
	state, stateErr := s.detachedState(ctx)
	if errors.Is(stateErr, sandbox.ErrSandboxGone) || (stateErr == nil && !state.Running) {
		return fmt.Errorf("%w: %w", sandbox.ErrSandboxGone, err)
	}
	return err
}

// detachedState inspects the sandbox to explain a failed call, even when the caller's ctx already ended
func (s *containerSandbox) detachedState(ctx context.Context) (*container.State, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), exitCodeWait)
	defer cancel()
	return s.state(ctx)
}

// process is an attached long-lived exec
type process struct {
	s      *containerSandbox
	h      *execHandle
	stdin  *stdinWriter
	stdout *io.PipeReader
	stderr *bufferedPipe

	done     chan struct{}
	killOnce sync.Once
}

func (p *process) Stdin() io.WriteCloser { return p.stdin }
func (p *process) Stdout() io.Reader     { return p.stdout }
func (p *process) Stderr() io.Reader     { return p.stderr }

// Kill terminates the process tree and returns once the process is gone
func (p *process) Kill() error {
	p.killOnce.Do(func() {
		select {
		case <-p.done:
			return
		default:
		}
		p.s.killExec(p.h)
		select {
		case <-p.done:
		case <-time.After(streamGrace):
			// Closing stdout drops the output nobody read, since a copy blocked writing it would never finish even after the stream is cut off
			_ = p.stdout.CloseWithError(io.ErrClosedPipe)
			p.h.resp.Close()
			<-p.done
		}
	})
	return nil
}

// watch kills the process when the context ends or the timeout expires
func (p *process) watch(ctx context.Context, timeout time.Duration) {
	var expired <-chan time.Time
	if timeout > 0 {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		expired = timer.C
	}
	select {
	case <-p.done:
		return
	case <-ctx.Done():
	case <-expired:
	}
	_ = p.Kill()
}

// stdinWriter writes to an attached exec and half-closes the connection on Close, which the process sees as EOF
type stdinWriter struct {
	resp *types.HijackedResponse
	once sync.Once
}

func (w *stdinWriter) Write(b []byte) (int, error) {
	return w.resp.Conn.Write(b)
}

func (w *stdinWriter) Close() error {
	var err error
	w.once.Do(func() { err = w.resp.CloseWrite() })
	return err
}

// bufferedPipe is an in-memory pipe whose writes never block; data beyond the limit is dropped
type bufferedPipe struct {
	mu     sync.Mutex
	cond   *sync.Cond
	buf    bytes.Buffer
	limit  int
	closed bool
}

func newBufferedPipe(limit int) *bufferedPipe {
	p := &bufferedPipe{limit: limit}
	p.cond = sync.NewCond(&p.mu)
	return p
}

func (p *bufferedPipe) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return 0, io.ErrClosedPipe
	}
	if room := p.limit - p.buf.Len(); room > 0 {
		p.buf.Write(b[:min(len(b), room)])
		p.cond.Broadcast()
	}
	return len(b), nil
}

func (p *bufferedPipe) Read(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for p.buf.Len() == 0 && !p.closed {
		p.cond.Wait()
	}
	if p.buf.Len() == 0 {
		return 0, io.EOF
	}
	return p.buf.Read(b)
}

func (p *bufferedPipe) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	p.cond.Broadcast()
	return nil
}

// envList converts an env map into the engine's KEY=VALUE list in a stable order
func envList(env map[string]string) []string {
	list := make([]string, 0, len(env))
	for _, key := range slices.Sorted(maps.Keys(env)) {
		list = append(list, key+"="+env[key])
	}
	return list
}

// writerOrDiscard avoids nil checks for optional output writers
func writerOrDiscard(w io.Writer) io.Writer {
	if w == nil {
		return io.Discard
	}
	return w
}
