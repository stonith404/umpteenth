// Package fake is an in-memory sandbox adapter for unit tests of the agent loop and the runner
// Nothing is executed: every Exec and Attach is answered by a handler the test registers, and files live in a map
package fake

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/stonith404/umpteenth/backend/internal/sandbox"
)

// Type is the adapter type the fake reports
const Type = "fake"

// Handler answers one command; it writes output to req.Stdout and req.Stderr and should return when ctx ends
type Handler func(ctx context.Context, sb *Sandbox, req sandbox.ExecRequest) (sandbox.ExecResult, error)

// Reply is a canned command result
type Reply struct {
	Stdout    string
	Stderr    string
	ExitCode  int
	OOMKilled bool
	// Delay simulates a slow command; the timeout and ctx cut it short like they would a real one
	Delay time.Duration
	// Err is returned as the exec error, e.g. sandbox.ErrSandboxGone
	Err error
}

// Handler turns the reply into a handler
func (r Reply) Handler() Handler {
	return func(ctx context.Context, _ *Sandbox, req sandbox.ExecRequest) (sandbox.ExecResult, error) {
		if r.Delay > 0 {
			select {
			case <-time.After(r.Delay):
			case <-ctx.Done():
				return sandbox.ExecResult{ExitCode: 143}, ctx.Err()
			}
		}
		_, _ = io.WriteString(req.Stdout, r.Stdout)
		_, _ = io.WriteString(req.Stderr, r.Stderr)
		return sandbox.ExecResult{ExitCode: r.ExitCode, OOMKilled: r.OOMKilled}, r.Err
	}
}

// Call records one Exec or Attach
type Call struct {
	SandboxID string
	Cmd       []string
	User      sandbox.User
	WorkDir   string
	Env       map[string]string
	// Stdin is the input of an Exec; attached processes stream theirs instead
	Stdin  []byte
	Attach bool
}

// route is a registered handler with its matcher
type route struct {
	match   func(cmd []string) bool
	handler Handler
}

// Adapter is the fake sandbox adapter; it is safe for concurrent use
type Adapter struct {
	mu         sync.Mutex
	info       sandbox.Info
	routes     []route
	fallback   Handler
	nextID     int
	sandboxes  map[string]*Sandbox
	calls      []Call
	images     map[string]sandbox.Image
	buildHooks []func(spec sandbox.BuildSpec) error
}

var (
	_ sandbox.Adapter      = (*Adapter)(nil)
	_ sandbox.ImageBuilder = (*Adapter)(nil)
)

// New returns a fake adapter that reports every capability
func New() *Adapter {
	return &Adapter{
		info: sandbox.Info{
			Adapter:   Type,
			Version:   "fake",
			Arch:      "amd64",
			Isolation: sandbox.IsolationContainer,
			Caps: sandbox.Capabilities{
				Networks: []sandbox.NetworkPolicy{sandbox.NetworkNone, sandbox.NetworkInternet, sandbox.NetworkAllowlist, sandbox.NetworkUnrestricted},
				Limits:   sandbox.LimitSet{CPU: true, Memory: true, Pids: true},
			},
			ImageBuilds: true,
		},
		sandboxes: map[string]*Sandbox{},
		images:    map[string]sandbox.Image{},
	}
}

// On answers commands whose space-joined argv contains substr; earlier registrations win
func (a *Adapter) On(substr string, reply Reply) {
	a.OnFunc(func(cmd []string) bool { return strings.Contains(strings.Join(cmd, " "), substr) }, reply.Handler())
}

// OnFunc answers commands the matcher accepts with a handler; earlier registrations win
func (a *Adapter) OnFunc(match func(cmd []string) bool, h Handler) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.routes = append(a.routes, route{match: match, handler: h})
}

// Default answers every command no route matched; without it such commands fail with exit code 127
func (a *Adapter) Default(h Handler) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.fallback = h
}

// OnBuild runs hook for every BuildImage, whose error fails the build
func (a *Adapter) OnBuild(hook func(spec sandbox.BuildSpec) error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.buildHooks = append(a.buildHooks, hook)
}

// Calls returns every Exec and Attach so far, oldest first
func (a *Adapter) Calls() []Call {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.calls)
}

// Type returns the fake's adapter type
func (a *Adapter) Type() string { return Type }

// Check returns the configured info
func (a *Adapter) Check(context.Context) (sandbox.Info, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.info, nil
}

// Prepare does nothing
func (a *Adapter) Prepare(context.Context) error { return nil }

// Close does nothing
func (a *Adapter) Close() error { return nil }

// Create adds a sandbox with the standard layout
func (a *Adapter) Create(_ context.Context, spec sandbox.Spec) (sandbox.Sandbox, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if spec.AgentUser == "" {
		spec.AgentUser = sandbox.UserAgent
	}
	if spec.Network == "" {
		spec.Network = sandbox.NetworkInternet
	}

	a.nextID++
	sb := &Sandbox{
		a:         a,
		id:        fmt.Sprintf("fake-%d", a.nextID),
		spec:      spec,
		createdAt: time.Now(),
		files:     map[string]*file{},
		dirs:      map[string]sandbox.User{"/": sandbox.UserRoot},
	}
	sb.mkdirAll(sandbox.WorkspaceDir, sandbox.UserAgent)
	sb.mkdirAll(sandbox.UmpDir, sandbox.UserAgent)
	sb.putFile(sandbox.File{Path: sandbox.UmpBinary, Mode: 0o755, Owner: sandbox.UserRoot, Content: []byte("#!fake ump\n")})
	a.sandboxes[sb.id] = sb
	return sb, nil
}

// List returns the live sandboxes
func (a *Adapter) List(context.Context) ([]sandbox.Summary, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	summaries := make([]sandbox.Summary, 0, len(a.sandboxes))
	for _, id := range slices.Sorted(maps.Keys(a.sandboxes)) {
		sb := a.sandboxes[id]
		summaries = append(summaries, sandbox.Summary{ID: id, RunID: sb.spec.RunID, CreatedAt: sb.createdAt})
	}
	return summaries, nil
}

// Destroy removes a sandbox; later calls on it fail with ErrSandboxGone
func (a *Adapter) Destroy(_ context.Context, id string) error {
	a.mu.Lock()
	sb, ok := a.sandboxes[id]
	delete(a.sandboxes, id)
	a.mu.Unlock()
	if ok {
		sb.mu.Lock()
		sb.gone = true
		sb.mu.Unlock()
	}
	return nil
}

// BuildImage records the image after running the build hooks
func (a *Adapter) BuildImage(_ context.Context, spec sandbox.BuildSpec) (sandbox.Image, error) {
	a.mu.Lock()
	hooks := slices.Clone(a.buildHooks)
	a.mu.Unlock()

	if spec.Logs != nil {
		_, _ = fmt.Fprintf(spec.Logs, "fake build of %s\n", spec.Tag)
	}
	for _, hook := range hooks {
		if err := hook(spec); err != nil {
			return sandbox.Image{}, err
		}
	}
	sum := sha256.Sum256([]byte(spec.Dockerfile))
	img := sandbox.Image{Ref: spec.Tag, Digest: "sha256:" + hex.EncodeToString(sum[:]), SizeBytes: int64(len(spec.Dockerfile))}
	if spec.MaxSizeBytes > 0 && img.SizeBytes > spec.MaxSizeBytes {
		return sandbox.Image{}, fmt.Errorf("the image is %d bytes, more than the limit of %d bytes", img.SizeBytes, spec.MaxSizeBytes)
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	a.images[spec.Tag] = img
	return img, nil
}

// HasImage reports whether the image was built and not removed
func (a *Adapter) HasImage(_ context.Context, ref string) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	_, ok := a.images[ref]
	return ok, nil
}

// ResolveDigest returns a pinned digest as is and a deterministic digest for anything else
func (a *Adapter) ResolveDigest(_ context.Context, ref string) (string, error) {
	if _, digest, ok := strings.Cut(ref, "@"); ok {
		return digest, nil
	}
	sum := sha256.Sum256([]byte(ref))
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// ListImages returns the tags of every image built and not removed
func (a *Adapter) ListImages(_ context.Context) ([]string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Collect(maps.Keys(a.images)), nil
}

// RemoveImage forgets an image
func (a *Adapter) RemoveImage(_ context.Context, ref string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.images, ref)
	return nil
}

// handler picks the handler for a command
func (a *Adapter) handler(cmd []string) Handler {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, r := range a.routes {
		if r.match == nil || r.match(cmd) {
			return r.handler
		}
	}
	if a.fallback != nil {
		return a.fallback
	}
	return func(_ context.Context, _ *Sandbox, req sandbox.ExecRequest) (sandbox.ExecResult, error) {
		_, _ = fmt.Fprintf(req.Stderr, "fake sandbox: no handler for %q\n", strings.Join(req.Cmd, " "))
		return sandbox.ExecResult{ExitCode: 127}, nil
	}
}

// record appends a call
func (a *Adapter) record(c Call) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls = append(a.calls, c)
}

// file is one file in a fake sandbox
type file struct {
	mode    fs.FileMode
	owner   sandbox.User
	content []byte
}

// Sandbox is one fake sandbox with an in-memory filesystem
type Sandbox struct {
	a         *Adapter
	id        string
	spec      sandbox.Spec
	createdAt time.Time

	mu    sync.Mutex
	files map[string]*file
	dirs  map[string]sandbox.User
	gone  bool
}

var _ sandbox.Sandbox = (*Sandbox)(nil)

// ID returns the sandbox ID
func (s *Sandbox) ID() string { return s.id }

// Spec returns the spec the sandbox was created with
func (s *Sandbox) Spec() sandbox.Spec { return s.spec }

// File returns a file's content, mode and owner, for assertions and handlers
func (s *Sandbox) File(p string) (content []byte, mode fs.FileMode, owner sandbox.User, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.files[path.Clean(p)]
	if !ok {
		return nil, 0, "", false
	}
	return slices.Clone(f.content), f.mode, f.owner, true
}

// Exec answers the command with the matching handler, applying the timeout and ctx like a real adapter
func (s *Sandbox) Exec(ctx context.Context, req sandbox.ExecRequest) (sandbox.ExecResult, error) {
	if err := s.alive(); err != nil {
		return sandbox.ExecResult{}, err
	}
	req = s.normalize(req)

	// Record the call with its complete stdin, which the handler then reads from a copy
	var stdin []byte
	if req.Stdin != nil {
		stdin, _ = io.ReadAll(req.Stdin)
		req.Stdin = bytes.NewReader(stdin)
	} else {
		req.Stdin = bytes.NewReader(nil)
	}
	s.a.record(Call{SandboxID: s.id, Cmd: slices.Clone(req.Cmd), User: req.User, WorkDir: req.WorkDir, Env: maps.Clone(req.Env), Stdin: stdin})

	execCtx := ctx
	if req.Timeout > 0 {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}

	res, err := s.a.handler(req.Cmd)(execCtx, s, req)

	// Report the end of the context the way the docker adapter does
	switch {
	case ctx.Err() != nil:
		return res, fmt.Errorf("exec cancelled: %w", context.Cause(ctx))
	case execCtx.Err() != nil:
		res.TimedOut = true
		return res, nil
	}
	return res, err
}

// Attach runs the matching handler in the background with streaming stdio
// Cancelling ctx, hitting the timeout or calling Kill cancels the handler's context
func (s *Sandbox) Attach(ctx context.Context, req sandbox.ExecRequest) (sandbox.Process, error) {
	if err := s.alive(); err != nil {
		return nil, err
	}
	req = s.normalize(req)
	s.a.record(Call{SandboxID: s.id, Cmd: slices.Clone(req.Cmd), User: req.User, WorkDir: req.WorkDir, Env: maps.Clone(req.Env), Attach: true})

	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	stderr := &stderrPipe{}
	stderr.cond = sync.NewCond(&stderr.mu)
	p := &process{stdin: stdinW, stdout: stdoutR, stderr: stderr, done: make(chan struct{})}

	var runCtx context.Context
	var cancel context.CancelFunc
	if req.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, req.Timeout)
	} else {
		runCtx, cancel = context.WithCancel(ctx)
	}
	p.cancel = cancel
	req.Stdin = stdinR
	req.Stdout = stdoutW
	req.Stderr = stderr

	// Run the handler until it returns, then end both output streams
	h := s.a.handler(req.Cmd)
	go func() {
		_, _ = h(runCtx, s, req)
		_ = stdinR.CloseWithError(io.ErrClosedPipe)
		_ = stdoutW.Close()
		_ = stderr.Close()
		cancel()
		close(p.done)
	}()
	return p, nil
}

// PutFiles stores files and creates missing parents owned by the file's owner
func (s *Sandbox) PutFiles(_ context.Context, files []sandbox.File) error {
	if err := s.alive(); err != nil {
		return err
	}
	for _, f := range files {
		if p := path.Clean(f.Path); !path.IsAbs(p) || p == "/" {
			return fmt.Errorf("invalid sandbox file path %q", f.Path)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, f := range files {
		s.putFile(f)
	}
	return nil
}

// ReadFile returns a file's content, failing with ErrOutputTooLarge when it exceeds max
func (s *Sandbox) ReadFile(_ context.Context, p string, max int64) ([]byte, error) {
	if err := s.alive(); err != nil {
		return nil, err
	}
	if max <= 0 {
		return nil, errors.New("ReadFile needs a positive size limit")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p = path.Clean(p)
	if _, ok := s.dirs[p]; ok {
		return nil, fmt.Errorf("%s is a directory", p)
	}
	f, ok := s.files[p]
	if !ok {
		return nil, fmt.Errorf("%s: %w", p, fs.ErrNotExist)
	}
	if int64(len(f.content)) > max {
		return nil, fmt.Errorf("%w: %s is %d bytes, the limit is %d", sandbox.ErrOutputTooLarge, p, len(f.content), max)
	}
	return slices.Clone(f.content), nil
}

// Archive returns a tar of a directory with entry names relative to it, failing once it exceeds max bytes
func (s *Sandbox) Archive(_ context.Context, dir string, max int64) (io.ReadCloser, error) {
	if err := s.alive(); err != nil {
		return nil, err
	}
	if max <= 0 {
		return nil, errors.New("Archive needs a positive size limit")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	dir = path.Clean(dir)
	if _, ok := s.dirs[dir]; !ok {
		if _, isFile := s.files[dir]; isFile {
			return nil, fmt.Errorf("%s is not a directory", dir)
		}
		return nil, fmt.Errorf("%s: %w", dir, fs.ErrNotExist)
	}

	// Write subdirectories before files, in sorted order, like a real archive of the tree
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	prefix := strings.TrimSuffix(dir, "/") + "/"
	for _, d := range slices.Sorted(maps.Keys(s.dirs)) {
		if rel, ok := strings.CutPrefix(d, prefix); ok && rel != "" {
			uid := s.dirs[d].UID()
			_ = tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: rel + "/", Mode: 0o755, Uid: uid, Gid: uid, ModTime: s.createdAt})
		}
	}
	for _, p := range slices.Sorted(maps.Keys(s.files)) {
		rel, ok := strings.CutPrefix(p, prefix)
		if !ok {
			continue
		}
		f := s.files[p]
		uid := f.owner.UID()
		_ = tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: rel, Mode: int64(f.mode.Perm()), Uid: uid, Gid: uid, Size: int64(len(f.content)), ModTime: s.createdAt})
		_, _ = tw.Write(f.content)
	}
	_ = tw.Close()

	// Deliver the first max bytes and then fail, like a streaming adapter would
	data := buf.Bytes()
	if int64(len(data)) > max {
		tooLarge := fmt.Errorf("%w: archive is larger than %d bytes", sandbox.ErrOutputTooLarge, max)
		return io.NopCloser(io.MultiReader(bytes.NewReader(data[:max]), errReader{tooLarge})), nil
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

// alive fails once the sandbox was destroyed
func (s *Sandbox) alive() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gone {
		return fmt.Errorf("%w: %s", sandbox.ErrSandboxGone, s.id)
	}
	return nil
}

// normalize fills in the defaults a real adapter applies
func (s *Sandbox) normalize(req sandbox.ExecRequest) sandbox.ExecRequest {
	if req.User == "" {
		req.User = s.spec.AgentUser
	}
	if req.WorkDir == "" {
		req.WorkDir = sandbox.WorkspaceDir
	}
	if req.Stdout == nil {
		req.Stdout = io.Discard
	}
	if req.Stderr == nil {
		req.Stderr = io.Discard
	}
	return req
}

// putFile stores a file; callers must hold s.mu
func (s *Sandbox) putFile(f sandbox.File) {
	p := path.Clean(f.Path)
	mode := f.Mode
	if mode == 0 {
		mode = 0o644
	}
	owner := f.Owner
	if owner == "" {
		owner = sandbox.UserAgent
	}
	s.mkdirAll(path.Dir(p), owner)
	s.files[p] = &file{mode: mode, owner: owner, content: slices.Clone(f.Content)}
}

// mkdirAll records a directory and its missing parents; callers must hold s.mu
func (s *Sandbox) mkdirAll(dir string, owner sandbox.User) {
	for d := path.Clean(dir); ; d = path.Dir(d) {
		if _, ok := s.dirs[d]; ok {
			return
		}
		s.dirs[d] = owner
	}
}

// process is an attached fake command
type process struct {
	stdin  *io.PipeWriter
	stdout *io.PipeReader
	stderr *stderrPipe
	cancel context.CancelFunc
	done   chan struct{}
}

func (p *process) Stdin() io.WriteCloser { return p.stdin }
func (p *process) Stdout() io.Reader     { return p.stdout }
func (p *process) Stderr() io.Reader     { return p.stderr }

// Kill cancels the handler and waits for it
func (p *process) Kill() error {
	p.cancel()
	<-p.done
	return nil
}

// stderrPipe buffers without limit so a test that never reads stderr cannot block the handler
type stderrPipe struct {
	mu     sync.Mutex
	cond   *sync.Cond
	buf    bytes.Buffer
	closed bool
}

func (p *stderrPipe) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return 0, io.ErrClosedPipe
	}
	p.buf.Write(b)
	p.cond.Broadcast()
	return len(b), nil
}

func (p *stderrPipe) Read(b []byte) (int, error) {
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

func (p *stderrPipe) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	p.cond.Broadcast()
	return nil
}

// errReader fails every read with err
type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }
