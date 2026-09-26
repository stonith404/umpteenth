//go:build unit

package fake

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/sandbox"
)

func newSandbox(t *testing.T, a *Adapter) sandbox.Sandbox {
	t.Helper()
	sb, err := a.Create(t.Context(), sandbox.Spec{RunID: "run-1", JobID: "job-1"})
	require.NoError(t, err)
	return sb
}

func TestExecRoutesAndRecordsCalls(t *testing.T) {
	a := New()
	a.On("git status", Reply{Stdout: "clean\n"})
	a.OnFunc(func(cmd []string) bool { return len(cmd) > 0 && cmd[0] == "false" }, Reply{ExitCode: 1, Stderr: "nope\n"}.Handler())
	a.Default(func(_ context.Context, _ *Sandbox, req sandbox.ExecRequest) (sandbox.ExecResult, error) {
		_, err := io.WriteString(req.Stdout, "default:"+strings.Join(req.Cmd, " "))
		return sandbox.ExecResult{}, err
	})
	sb := newSandbox(t, a)

	var stdout, stderr bytes.Buffer
	res, err := sb.Exec(t.Context(), sandbox.ExecRequest{Cmd: []string{"bash", "-lc", "git status"}, Stdout: &stdout, Stdin: strings.NewReader("input")})
	require.NoError(t, err)
	assert.Equal(t, 0, res.ExitCode)
	assert.Equal(t, "clean\n", stdout.String())

	res, err = sb.Exec(t.Context(), sandbox.ExecRequest{Cmd: []string{"false"}, Stderr: &stderr, User: sandbox.UserMCP})
	require.NoError(t, err)
	assert.Equal(t, 1, res.ExitCode)
	assert.Equal(t, "nope\n", stderr.String())

	stdout.Reset()
	_, err = sb.Exec(t.Context(), sandbox.ExecRequest{Cmd: []string{"echo", "hi"}, Stdout: &stdout})
	require.NoError(t, err)
	assert.Equal(t, "default:echo hi", stdout.String())

	calls := a.Calls()
	require.Len(t, calls, 3)
	assert.Equal(t, []byte("input"), calls[0].Stdin)
	assert.Equal(t, sandbox.UserAgent, calls[0].User)
	assert.Equal(t, sandbox.WorkspaceDir, calls[0].WorkDir)
	assert.Equal(t, sandbox.UserMCP, calls[1].User)
}

func TestExecWithoutHandlerFails(t *testing.T) {
	sb := newSandbox(t, New())
	var stderr bytes.Buffer
	res, err := sb.Exec(t.Context(), sandbox.ExecRequest{Cmd: []string{"ls"}, Stderr: &stderr})
	require.NoError(t, err)
	assert.Equal(t, 127, res.ExitCode)
	assert.Contains(t, stderr.String(), "no handler")
}

func TestExecTimeoutAndCancel(t *testing.T) {
	a := New()
	a.On("slow", Reply{Delay: time.Minute})
	sb := newSandbox(t, a)

	res, err := sb.Exec(t.Context(), sandbox.ExecRequest{Cmd: []string{"slow"}, Timeout: 20 * time.Millisecond})
	require.NoError(t, err)
	assert.True(t, res.TimedOut)

	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	_, err = sb.Exec(ctx, sandbox.ExecRequest{Cmd: []string{"slow"}})
	assert.ErrorIs(t, err, context.Canceled)
}

func TestFilesAndArchive(t *testing.T) {
	a := New()
	sb := newSandbox(t, a)
	ctx := t.Context()

	require.NoError(t, sb.PutFiles(ctx, []sandbox.File{
		{Path: "/ump/outputs/report.csv", Mode: 0o640, Owner: sandbox.UserAgent, Content: []byte("a,b\n")},
		{Path: "/ump/outputs/sub/data.json", Owner: sandbox.UserAgent, Content: []byte("{}")},
	}))

	data, err := sb.ReadFile(ctx, "/ump/outputs/report.csv", 100)
	require.NoError(t, err)
	assert.Equal(t, "a,b\n", string(data))

	_, err = sb.ReadFile(ctx, "/ump/outputs/report.csv", 2)
	assert.ErrorIs(t, err, sandbox.ErrOutputTooLarge)
	_, err = sb.ReadFile(ctx, "/nope", 10)
	assert.ErrorIs(t, err, fs.ErrNotExist)

	_, mode, owner, ok := sb.(*Sandbox).File("/ump/outputs/report.csv")
	require.True(t, ok)
	assert.Equal(t, fs.FileMode(0o640), mode)
	assert.Equal(t, sandbox.UserAgent, owner)

	// Entries are relative to the archived directory
	rc, err := sb.Archive(ctx, "/ump/outputs", 1<<20)
	require.NoError(t, err)
	names := map[string]string{}
	tr := tar.NewReader(rc)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		content, _ := io.ReadAll(tr)
		names[hdr.Name] = string(content)
	}
	assert.Equal(t, map[string]string{"sub/": "", "report.csv": "a,b\n", "sub/data.json": "{}"}, names)

	rc, err = sb.Archive(ctx, "/ump/outputs", 100)
	require.NoError(t, err)
	_, err = io.ReadAll(rc)
	assert.ErrorIs(t, err, sandbox.ErrOutputTooLarge)

	_, err = sb.Archive(ctx, "/missing", 100)
	assert.ErrorIs(t, err, fs.ErrNotExist)
}

func TestAttachRoundTripAndKill(t *testing.T) {
	a := New()
	a.OnFunc(func(cmd []string) bool { return cmd[0] == "cat" }, func(ctx context.Context, _ *Sandbox, req sandbox.ExecRequest) (sandbox.ExecResult, error) {
		_, _ = io.WriteString(req.Stderr, "started\n")
		_, err := io.Copy(req.Stdout, req.Stdin)
		return sandbox.ExecResult{}, err
	})
	a.On("forever", Reply{Delay: time.Hour})
	sb := newSandbox(t, a)

	p, err := sb.Attach(t.Context(), sandbox.ExecRequest{Cmd: []string{"cat"}, User: sandbox.UserMCP})
	require.NoError(t, err)
	_, err = io.WriteString(p.Stdin(), "ping\n")
	require.NoError(t, err)
	line, err := bufio.NewReader(p.Stdout()).ReadString('\n')
	require.NoError(t, err)
	assert.Equal(t, "ping\n", line)
	require.NoError(t, p.Stdin().Close())
	res, err := p.Wait()
	require.NoError(t, err)
	assert.Equal(t, 0, res.ExitCode)
	stderr, _ := io.ReadAll(p.Stderr())
	assert.Equal(t, "started\n", string(stderr))

	p, err = sb.Attach(t.Context(), sandbox.ExecRequest{Cmd: []string{"forever"}})
	require.NoError(t, err)
	require.NoError(t, p.Kill())
	res, err = p.Wait()
	require.NoError(t, err)
	assert.Equal(t, 143, res.ExitCode)
}

func TestLifecycle(t *testing.T) {
	a := New()
	ctx := t.Context()
	sb := newSandbox(t, a)

	list, err := a.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "run-1", list[0].RunID)

	got, err := a.Get(ctx, sb.ID())
	require.NoError(t, err)
	assert.Equal(t, sb.ID(), got.ID())

	require.NoError(t, a.Destroy(ctx, sb.ID()))
	require.NoError(t, a.Destroy(ctx, sb.ID()))
	_, err = a.Get(ctx, sb.ID())
	assert.ErrorIs(t, err, sandbox.ErrNotFound)
	_, err = sb.Exec(ctx, sandbox.ExecRequest{Cmd: []string{"true"}})
	assert.ErrorIs(t, err, sandbox.ErrSandboxGone)
}

func TestImageBuilder(t *testing.T) {
	a := New()
	ctx := t.Context()
	var logs bytes.Buffer
	img, err := a.BuildImage(ctx, sandbox.BuildSpec{Dockerfile: "FROM x\n", Tag: "umpteenth/job-1:abc", Logs: &logs})
	require.NoError(t, err)
	assert.Equal(t, "umpteenth/job-1:abc", img.Ref)
	assert.True(t, strings.HasPrefix(img.Digest, "sha256:"))
	assert.Contains(t, logs.String(), "umpteenth/job-1:abc")

	has, err := a.HasImage(ctx, img.Ref)
	require.NoError(t, err)
	assert.True(t, has)
	require.NoError(t, a.RemoveImage(ctx, img.Ref))
	has, _ = a.HasImage(ctx, img.Ref)
	assert.False(t, has)

	digest, err := a.ResolveDigest(ctx, "debian@sha256:abc")
	require.NoError(t, err)
	assert.Equal(t, "sha256:abc", digest)

	a.OnBuild(func(sandbox.BuildSpec) error { return errors.New("build failed") })
	_, err = a.BuildImage(ctx, sandbox.BuildSpec{Dockerfile: "FROM x\n", Tag: "t"})
	assert.EqualError(t, err, "build failed")
}
