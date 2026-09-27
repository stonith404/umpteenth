package kubernetes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/tools/remotecommand"
	utilexec "k8s.io/client-go/util/exec"
	"k8s.io/streaming/pkg/httpstream"

	"github.com/stonith404/umpteenth/backend/internal/sandbox"
)

const (
	// streamGrace is how long the output stream may take to end after the process tree was killed
	streamGrace = 2 * time.Second
	// stateWait bounds each look at the pod that explains a failed or killed command
	stateWait = 5 * time.Second
	// killTimeout bounds the kill exec, whose shim gives up after its own 1.5s grace period
	killTimeout = 10 * time.Second
	// maxStderrBuffer caps unread stderr of an attached process, so it never blocks stdout
	maxStderrBuffer = 1 << 20
	// exitSIGKILL is how the shim reports a process killed by SIGKILL
	exitSIGKILL = 128 + 9
	// stopGrace bounds how long a command that may have died with its sandbox waits for Kubernetes to record the stop
	stopGrace = 2 * time.Second
)

// podSandbox is one sandbox pod
type podSandbox struct {
	a    *Adapter
	name string
	// agentUser runs commands whose request names no user
	agentUser sandbox.User
}

// execHandle is a running exec and what is needed to kill it
type execHandle struct {
	pidfile string
}

// ID returns the pod name
func (s *podSandbox) ID() string {
	return s.name
}

// Exec runs a command through the ump exec shim and waits for it
func (s *podSandbox) Exec(ctx context.Context, req sandbox.ExecRequest) (sandbox.ExecResult, error) {
	cmd, h, header, err := s.shimCommand(req)
	if err != nil {
		return sandbox.ExecResult{}, err
	}

	// The stream outlives the caller's context, so a cancelled command is killed through the shim rather than cut off
	stdin := io.MultiReader(bytes.NewReader(header), readerOrEmpty(req.Stdin))
	streamCtx, cancelStream := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelStream()
	done := make(chan streamResult, 1)
	go func() {
		code, err := s.run(streamCtx, cmd, stdin, writerOrDiscard(req.Stdout), writerOrDiscard(req.Stderr))
		done <- streamResult{code, err}
	}()

	// Wait for the command, its timeout or the caller, whichever comes first
	var expired <-chan time.Time
	if req.Timeout > 0 {
		timer := time.NewTimer(req.Timeout)
		defer timer.Stop()
		expired = timer.C
	}
	var result streamResult
	var timedOut, cancelled bool
	select {
	case result = <-done:
	case <-expired:
		timedOut = true
	case <-ctx.Done():
		cancelled = true
	}

	// Kill the process tree through a second exec; the stream ends once the shim has exited
	if timedOut || cancelled {
		s.killExec(h)
		select {
		case result = <-done:
		case <-time.After(streamGrace):
			cancelStream()
			result = <-done
		}
	}

	res, err := s.execResult(h, result, timedOut || cancelled)
	res.TimedOut = timedOut
	if cancelled && err == nil {
		err = fmt.Errorf("exec cancelled: %w", context.Cause(ctx))
	}
	return res, err
}

// streamResult is how an exec stream ended
type streamResult struct {
	code int
	err  error
}

// shimCommand wraps a request in the ump exec shim, which sets the user, directory and environment the exec API can't
func (s *podSandbox) shimCommand(req sandbox.ExecRequest) ([]string, *execHandle, []byte, error) {
	if len(req.Cmd) == 0 {
		return nil, nil, nil, errors.New("exec request has no command")
	}

	// Users are roles mapped to fixed uids, so images need no passwd entries
	user := req.User
	if user == "" {
		user = s.agentUser
	}
	workDir := req.WorkDir
	if workDir == "" {
		workDir = sandbox.WorkspaceDir
	}
	env := req.Env
	if env == nil {
		env = map[string]string{}
	}
	header, err := json.Marshal(env)
	if err != nil {
		return nil, nil, nil, err
	}

	h := &execHandle{pidfile: path.Join(runDir, randomID(12)+".pid")}
	uid := strconv.Itoa(user.UID())
	cmd := append([]string{sandbox.UmpBinary, "exec", "--pidfile", h.pidfile, "--user", uid + ":" + uid, "--workdir", workDir, "--env-stdin", "--"}, req.Cmd...)
	return cmd, h, append(header, '\n'), nil
}

// run execs a command in the sandbox container as root and returns its exit code
func (s *podSandbox) run(ctx context.Context, cmd []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	execURL := s.a.kube.execURL(s.name, &corev1.PodExecOptions{
		Container: sandboxContainer,
		Command:   cmd,
		Stdin:     stdin != nil,
		Stdout:    true,
		Stderr:    true,
	})

	// WebSockets carry stdin's end to the command, and SPDY is the fallback for proxies that can't upgrade to them
	ws, err := remotecommand.NewWebSocketExecutor(s.a.rest, "GET", execURL.String())
	if err != nil {
		return -1, err
	}
	spdy, err := remotecommand.NewSPDYExecutor(s.a.rest, "POST", execURL)
	if err != nil {
		return -1, err
	}
	executor, err := remotecommand.NewFallbackExecutor(ws, spdy, func(err error) bool {
		return httpstream.IsUpgradeFailure(err) || httpstream.IsHTTPSProxyError(err)
	})
	if err != nil {
		return -1, err
	}

	err = executor.StreamWithContext(ctx, remotecommand.StreamOptions{Stdin: stdin, Stdout: writerOrDiscard(stdout), Stderr: writerOrDiscard(stderr)})
	if err == nil {
		return 0, nil
	}
	if exitErr, ok := errors.AsType[utilexec.ExitError](err); ok {
		return exitErr.ExitStatus(), nil
	}
	return -1, err
}

// killExec terminates an exec's process group with `ump exec --kill`, which runs as root and can signal every user
// It returns once the kill mode finished, which is within its 1.5s grace period
func (s *podSandbox) killExec(h *execHandle) {
	ctx, cancel := context.WithTimeout(context.Background(), killTimeout)
	defer cancel()
	var out bytes.Buffer
	code, err := s.run(ctx, []string{sandbox.UmpBinary, "exec", "--kill", h.pidfile}, nil, &out, &out)
	if err != nil || code != 0 {
		s.a.log.WarnContext(ctx, "The kill exec reported a problem", "sandbox", s.name, "code", code, "output", strings.TrimSpace(out.String()), "error", err)
	}
}

// execResult turns how a stream ended into the command's result, adding OOM and sandbox-gone detection
func (s *podSandbox) execResult(h *execHandle, result streamResult, killed bool) (sandbox.ExecResult, error) {
	res := sandbox.ExecResult{ExitCode: result.code}
	ctx, cancel := context.WithTimeout(context.Background(), stateWait)
	defer cancel()

	// A stream that broke instead of ending with a code usually means the pod is gone
	if result.err != nil {
		return res, s.translate(ctx, fmt.Errorf("exec failed: %w", result.err))
	}

	// Only a signal can mean the whole sandbox went down, so the common case skips looking at the pod
	if res.ExitCode >= 0 && res.ExitCode < 128 {
		return res, nil
	}

	// A sandbox that reaches its TTL or runs out of memory as a whole goes down, and its commands end a moment before Kubernetes records the stop
	pod, err := s.pod(ctx)
	if err == nil && podAlive(pod) && res.ExitCode == exitSIGKILL && !killed {
		pod, err = s.awaitStop(ctx)
	}
	switch {
	case errors.Is(err, sandbox.ErrSandboxGone):
		return res, fmt.Errorf("%w: sandbox %s was removed while running a command", sandbox.ErrSandboxGone, s.name)
	case err != nil:
		s.a.log.WarnContext(ctx, "Failed to look at the sandbox after a command was killed", "sandbox", s.name, "error", err)
	case !podAlive(pod) && oomKilled(pod):
		res.OOMKilled = true
		return res, fmt.Errorf("%w: sandbox %s ran out of memory and stopped while running a command", sandbox.ErrSandboxGone, s.name)
	case !podAlive(pod):
		return res, fmt.Errorf("%w: sandbox %s stopped while running a command", sandbox.ErrSandboxGone, s.name)
	case res.ExitCode == exitSIGKILL:
		// SIGKILL in a running sandbox comes from the out-of-memory killer or from our own kill escalation, which the shim tells apart
		res.OOMKilled = s.oomMarker(ctx, h)
	}
	return res, nil
}

// oomMarker reads the marker the shim leaves when the out-of-memory killer ended its command
func (s *podSandbox) oomMarker(ctx context.Context, h *execHandle) bool {
	data, err := s.ReadFile(ctx, h.pidfile, 64)
	return err == nil && strings.TrimSpace(string(data)) == "oom"
}

// awaitStop gives a sandbox that may be going down stopGrace to stop, and returns its state afterwards
func (s *podSandbox) awaitStop(ctx context.Context) (*corev1.Pod, error) {
	deadline := time.Now().Add(stopGrace)
	for {
		pod, err := s.pod(ctx)
		if err != nil || !podAlive(pod) || time.Now().After(deadline) {
			return pod, err
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// pod returns the sandbox pod, or ErrSandboxGone when it no longer exists
func (s *podSandbox) pod(ctx context.Context) (*corev1.Pod, error) {
	pod, err := s.a.kube.getPod(ctx, s.name)
	if apierrors.IsNotFound(err) {
		return nil, sandbox.ErrSandboxGone
	}
	return pod, err
}

// podAlive reports whether the sandbox container still runs and nobody is deleting the pod
func podAlive(pod *corev1.Pod) bool {
	return pod.DeletionTimestamp == nil && containerRunning(pod)
}

// oomKilled reports whether the sandbox container was ended by the out-of-memory killer
func oomKilled(pod *corev1.Pod) bool {
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.Name == sandboxContainer && cs.State.Terminated != nil {
			return cs.State.Terminated.Reason == "OOMKilled"
		}
	}
	return false
}

// translate turns failures caused by a vanished or stopped pod into ErrSandboxGone
func (s *podSandbox) translate(ctx context.Context, err error) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), stateWait)
	defer cancel()
	pod, podErr := s.pod(ctx)
	if errors.Is(podErr, sandbox.ErrSandboxGone) || (podErr == nil && !podAlive(pod)) {
		return fmt.Errorf("%w: %w", sandbox.ErrSandboxGone, err)
	}
	return err
}

// Attach starts a long-lived process with interactive stdio
// Like exec.CommandContext, cancelling ctx or hitting req.Timeout kills the process tree
func (s *podSandbox) Attach(ctx context.Context, req sandbox.ExecRequest) (sandbox.Process, error) {
	cmd, h, header, err := s.shimCommand(req)
	if err != nil {
		return nil, err
	}

	// The environment header goes ahead of everything the caller writes
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	p := &process{
		s:      s,
		h:      h,
		stdin:  stdinW,
		stdout: stdoutR,
		stderr: newBufferedPipe(maxStderrBuffer),
		done:   make(chan struct{}),
	}
	streamCtx, cancelStream := context.WithCancel(context.WithoutCancel(ctx))
	p.cancelStream = cancelStream
	stdin := io.MultiReader(bytes.NewReader(header), stdinR)

	// Stdout pushes back on the process, while stderr is buffered so a caller that never reads it cannot stall stdout
	go func() {
		_, _ = s.run(streamCtx, cmd, stdin, stdoutW, p.stderr)
		_ = stdoutW.Close()
		_ = p.stderr.Close()
		_ = stdinR.Close()
		cancelStream()
		close(p.done)
	}()

	go p.watch(ctx, req.Timeout)
	return p, nil
}

// process is an attached long-lived exec
type process struct {
	s            *podSandbox
	h            *execHandle
	stdin        *io.PipeWriter
	stdout       *io.PipeReader
	stderr       *bufferedPipe
	cancelStream context.CancelFunc

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
			p.cancelStream()
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

// writerOrDiscard avoids nil checks for optional output writers
func writerOrDiscard(w io.Writer) io.Writer {
	if w == nil {
		return io.Discard
	}
	return w
}

// readerOrEmpty avoids nil checks for optional input
func readerOrEmpty(r io.Reader) io.Reader {
	if r == nil {
		return bytes.NewReader(nil)
	}
	return r
}
