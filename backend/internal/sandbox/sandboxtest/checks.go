package sandboxtest

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/sandbox"
)

func (s *suite) testFiles(t *testing.T, sb sandbox.Sandbox) {
	ctx := t.Context()
	secret := "mcp-only-" + randomHex(4)

	// Write files with different modes and owners, including into directories that do not exist yet
	require.NoError(t, sb.PutFiles(ctx, []sandbox.File{
		{Path: "/workspace/deep/er/notes.txt", Mode: 0o640, Owner: sandbox.UserAgent, Content: []byte("hello")},
		{Path: "/workspace/run.sh", Mode: 0o755, Owner: sandbox.UserAgent, Content: []byte("#!/bin/sh\necho ran\n")},
		{Path: "/ump/mcp-secret", Mode: 0o600, Owner: sandbox.UserMCP, Content: []byte(secret)},
		{Path: "/usr/local/lib/sandboxtest/root.txt", Mode: 0o644, Owner: sandbox.UserRoot, Content: []byte("root")},
	}))

	// Modes and owners land as requested, new parents belong to the file's owner, and existing ones are untouched
	out := s.mustSh(t, sb, `stat -c '%n %a %u' /workspace/deep/er/notes.txt /workspace/run.sh /ump/mcp-secret /usr/local/lib/sandboxtest/root.txt /workspace/deep /workspace/deep/er /usr/local/lib/sandboxtest /usr/local/lib /workspace /ump`)
	assert.Equal(t, []string{
		"/workspace/deep/er/notes.txt 640 1000",
		"/workspace/run.sh 755 1000",
		"/ump/mcp-secret 600 1001",
		"/usr/local/lib/sandboxtest/root.txt 644 0",
		"/workspace/deep 755 1000",
		"/workspace/deep/er 755 1000",
		"/usr/local/lib/sandboxtest 755 0",
		"/usr/local/lib 755 0",
		"/workspace 755 1000",
		"/ump 755 1000",
	}, lines(out))
	assert.Equal(t, "ran", strings.TrimSpace(s.mustSh(t, sb, "/workspace/run.sh")))
	s.mustSh(t, sb, "touch /workspace/deep/er/created-by-agent")

	// File permissions separate the users
	if s.info.Caps.SeparateUsers {
		_, _, res := s.sh(t, sb, "cat /ump/mcp-secret")
		assert.NotEqual(t, 0, res.ExitCode, "the agent must not read the mcp user's 0600 file")
		assert.Equal(t, secret, s.mustShReq(t, sb, sandbox.ExecRequest{Cmd: []string{"cat", "/ump/mcp-secret"}, User: sandbox.UserMCP}))
	}

	// Overwriting replaces content and mode
	require.NoError(t, sb.PutFiles(ctx, []sandbox.File{{Path: "/workspace/run.sh", Mode: 0o700, Owner: sandbox.UserAgent, Content: []byte("#!/bin/sh\necho ran again\n")}}))
	assert.Equal(t, "ran again", strings.TrimSpace(s.mustSh(t, sb, "/workspace/run.sh")))
	assert.Equal(t, "700", strings.TrimSpace(s.mustSh(t, sb, "stat -c %a /workspace/run.sh")))

	t.Run("ReadFile", func(t *testing.T) {
		data, err := sb.ReadFile(ctx, "/workspace/deep/er/notes.txt", 1024)
		require.NoError(t, err)
		assert.Equal(t, "hello", string(data))

		// Files written by commands, and symlinks to them, read the same
		s.mustSh(t, sb, "head -c 1000 /dev/zero | tr '\\0' x > /workspace/thousand.txt && ln -sf /workspace/thousand.txt /workspace/link.txt")
		data, err = sb.ReadFile(ctx, "/workspace/thousand.txt", 1000)
		require.NoError(t, err)
		assert.Equal(t, strings.Repeat("x", 1000), string(data))
		data, err = sb.ReadFile(ctx, "/workspace/link.txt", 1000)
		require.NoError(t, err)
		assert.Len(t, data, 1000)
	})
	t.Run("ReadFileLimits", func(t *testing.T) {
		_, err := sb.ReadFile(ctx, "/workspace/thousand.txt", 999)
		assert.ErrorIs(t, err, sandbox.ErrOutputTooLarge)
		_, err = sb.ReadFile(ctx, "/workspace/does-not-exist", 1024)
		assert.ErrorIs(t, err, fs.ErrNotExist)
		_, err = sb.ReadFile(ctx, "/workspace", 1024)
		assert.Error(t, err, "reading a directory must fail")
	})
}

func (s *suite) testArchive(t *testing.T, sb sandbox.Sandbox) {
	ctx := t.Context()
	out := s.mustSh(t, sb, `rm -rf /ump/outputs && mkdir -p /ump/outputs/sub && printf a > /ump/outputs/a.txt && head -c 100000 /dev/urandom > /ump/outputs/sub/b.bin && sha256sum /ump/outputs/sub/b.bin | cut -d' ' -f1`)
	wantSum := strings.TrimSpace(out)

	// Entries are relative to the archived directory
	rc, err := sb.Archive(ctx, "/ump/outputs", 10<<20)
	require.NoError(t, err)
	files := map[string][]byte{}
	tr := tar.NewReader(rc)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		assert.False(t, strings.HasPrefix(hdr.Name, "/") || strings.HasPrefix(hdr.Name, "outputs"), "entry %q must be relative to the directory", hdr.Name)
		if hdr.Typeflag == tar.TypeReg {
			data, err := io.ReadAll(tr)
			require.NoError(t, err)
			files[hdr.Name] = data
		}
	}
	require.NoError(t, rc.Close())
	assert.Equal(t, "a", string(files["a.txt"]))
	sum := sha256.Sum256(files["sub/b.bin"])
	assert.Equal(t, wantSum, hex.EncodeToString(sum[:]))

	// Crossing the limit fails the stream
	rc, err = sb.Archive(ctx, "/ump/outputs", 10_000)
	if err == nil {
		_, err = io.Copy(io.Discard, rc)
		_ = rc.Close()
	}
	assert.ErrorIs(t, err, sandbox.ErrOutputTooLarge)

	_, err = sb.Archive(ctx, "/ump/does-not-exist", 1024)
	assert.ErrorIs(t, err, fs.ErrNotExist)
}

func (s *suite) testAttach(t *testing.T, sb sandbox.Sandbox) {
	t.Run("RoundTrip", func(t *testing.T) {
		p, err := sb.Attach(t.Context(), sandbox.ExecRequest{Cmd: []string{"sh", "-c", "echo started >&2; cat"}})
		require.NoError(t, err)
		defer func() { _ = p.Kill() }()

		stderr := bufio.NewReader(p.Stderr())
		line, err := stderr.ReadString('\n')
		require.NoError(t, err)
		assert.Equal(t, "started\n", line)

		// Each line comes back before the next is sent, which proves nothing buffers the stream
		stdout := bufio.NewReader(p.Stdout())
		for _, msg := range []string{"one\n", "two\n", `{"jsonrpc":"2.0","id":1}` + "\n"} {
			_, err := io.WriteString(p.Stdin(), msg)
			require.NoError(t, err)
			got, err := readLineWithin(stdout, 10*time.Second)
			require.NoError(t, err)
			assert.Equal(t, msg, got)
		}

		// Closing stdin ends cat
		require.NoError(t, p.Stdin().Close())
		res, err := p.Wait()
		require.NoError(t, err)
		assert.Equal(t, 0, res.ExitCode)
	})
	t.Run("UserAndEnv", func(t *testing.T) {
		p, err := sb.Attach(t.Context(), sandbox.ExecRequest{Cmd: []string{"sh", "-c", `echo "$ATTACH_VAR"; id -u`}, User: sandbox.UserMCP, Env: map[string]string{"ATTACH_VAR": "attached"}})
		require.NoError(t, err)
		out, err := io.ReadAll(p.Stdout())
		require.NoError(t, err)
		_, err = p.Wait()
		require.NoError(t, err)
		want := "attached\n1000\n"
		if s.info.Caps.SeparateUsers {
			want = "attached\n1001\n"
		}
		assert.Equal(t, want, string(out))
	})
	t.Run("Kill", func(t *testing.T) {
		p, err := sb.Attach(t.Context(), sandbox.ExecRequest{Cmd: []string{"sleep", "300"}})
		require.NoError(t, err)
		start := time.Now()
		require.NoError(t, p.Kill())
		res, err := p.Wait()
		require.NoError(t, err)
		assert.NotEqual(t, 0, res.ExitCode)
		assert.Less(t, time.Since(start), 2500*time.Millisecond)
	})
	t.Run("ContextCancelKills", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		p, err := sb.Attach(ctx, sandbox.ExecRequest{Cmd: []string{"sleep", "300"}})
		require.NoError(t, err)
		time.AfterFunc(300*time.Millisecond, cancel)
		done := make(chan struct{})
		go func() {
			_, _ = p.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = p.Kill()
			t.Fatal("cancelling the context did not end the attached process")
		}
	})
}

func (s *suite) testProcEnvIsolation(t *testing.T, sb sandbox.Sandbox) {
	if !s.info.Caps.SeparateUsers {
		t.Skip("the adapter does not run users with separate uids")
	}
	secret := "mcp-env-" + randomHex(6)

	// A long-lived process of the mcp user holds a secret in its environment, like a stdio MCP server
	p, err := sb.Attach(t.Context(), sandbox.ExecRequest{Cmd: []string{"sh", "-c", "echo ready; exec sleep 300"}, User: sandbox.UserMCP, Env: map[string]string{"MCP_SECRET": secret}})
	require.NoError(t, err)
	defer func() { _ = p.Kill() }()
	line, err := readLineWithin(bufio.NewReader(p.Stdout()), 10*time.Second)
	require.NoError(t, err)
	require.Equal(t, "ready\n", line)

	// The mcp user itself can see the secret, which proves the probe works
	probe := `cat /proc/[0-9]*/environ 2>/dev/null | tr '\0' '\n' | grep -c "^MCP_SECRET=` + secret + `$" || true`
	assert.NotEqual(t, "0", strings.TrimSpace(s.mustShReq(t, sb, sandbox.ExecRequest{Cmd: []string{"sh", "-c", probe}, User: sandbox.UserMCP})))

	// The agent can neither read it nor signal the mcp user's processes
	assert.Equal(t, "0", strings.TrimSpace(s.mustSh(t, sb, probe)))
	out := s.mustSh(t, sb, `for p in $(ps -o pid= -u 1001); do kill -0 $p 2>/dev/null && echo "can-signal $p"; done; echo checked`)
	assert.Equal(t, "checked", strings.TrimSpace(out))
}

func (s *suite) testImageBuilder(t *testing.T) {
	builder, ok := s.a.(sandbox.ImageBuilder)
	if !ok {
		t.Skip("the adapter does not implement ImageBuilder")
	}
	ctx := t.Context()
	repo := "umpteenth-sandboxtest/job-" + randomHex(4)
	if s.opts.ImageRepository != "" {
		repo = strings.TrimRight(s.opts.ImageRepository, "/") + "/job-" + randomHex(4)
	}

	t.Run("BuildRunRemove", func(t *testing.T) {
		tag := repo + ":ok"
		var logs bytes.Buffer
		img, err := builder.BuildImage(ctx, sandbox.BuildSpec{
			Dockerfile: fmt.Sprintf("FROM %s\nRUN echo build-log-marker && echo built > /built.txt\n", DefaultImage),
			Tag:        tag,
			Logs:       &logs,
			Timeout:    5 * time.Minute,
		})
		require.NoError(t, err, logs.String())
		t.Cleanup(func() { _ = builder.RemoveImage(context.Background(), tag) })
		assert.Equal(t, tag, img.Ref)
		assert.NotEmpty(t, img.Digest)
		assert.Positive(t, img.SizeBytes)
		assert.Contains(t, logs.String(), "build-log-marker")

		has, err := builder.HasImage(ctx, tag)
		require.NoError(t, err)
		assert.True(t, has)

		// Runs use the built image
		sb := s.create(t, sandbox.Spec{Image: tag})
		assert.Equal(t, "built", strings.TrimSpace(s.mustSh(t, sb, "cat /built.txt")))
		require.NoError(t, s.a.Destroy(ctx, sb.ID()))

		// Removal is idempotent, and a local-only tag cannot be pulled back
		require.NoError(t, builder.RemoveImage(ctx, tag))
		require.NoError(t, builder.RemoveImage(ctx, tag))
		has, err = builder.HasImage(ctx, tag)
		if err != nil {
			t.Logf("HasImage after removal: %v", err)
		}
		assert.False(t, has)
	})
	t.Run("FailingBuild", func(t *testing.T) {
		var logs bytes.Buffer
		_, err := builder.BuildImage(ctx, sandbox.BuildSpec{
			Dockerfile: fmt.Sprintf("FROM %s\nRUN echo failing-step && exit 3\n", DefaultImage),
			Tag:        repo + ":fail",
			Logs:       &logs,
		})
		assert.Error(t, err)
		assert.Contains(t, logs.String(), "failing-step")
		t.Cleanup(func() { _ = builder.RemoveImage(context.Background(), repo+":fail") })
	})
	t.Run("MaxSize", func(t *testing.T) {
		tag := repo + ":big"
		_, err := builder.BuildImage(ctx, sandbox.BuildSpec{Dockerfile: fmt.Sprintf("FROM %s\nRUN echo x > /x\n", DefaultImage), Tag: tag, MaxSizeBytes: 1})
		assert.Error(t, err)
		t.Cleanup(func() { _ = builder.RemoveImage(context.Background(), tag) })
		has, _ := builder.HasImage(ctx, tag)
		assert.False(t, has, "an image over the size limit must not be kept")
	})
	t.Run("Timeout", func(t *testing.T) {
		start := time.Now()
		// The random suffix keeps an earlier run's cache from answering instantly
		dockerfile := fmt.Sprintf("FROM %s\nRUN sleep 120 && echo %s\n", DefaultImage, randomHex(4))
		_, err := builder.BuildImage(ctx, sandbox.BuildSpec{Dockerfile: dockerfile, Tag: repo + ":slow", Timeout: 3 * time.Second})
		assert.Error(t, err)
		assert.Less(t, time.Since(start), 30*time.Second)
		t.Cleanup(func() { _ = builder.RemoveImage(context.Background(), repo+":slow") })
	})
	t.Run("ResolveDigest", func(t *testing.T) {
		pinned := "sha256:" + strings.Repeat("0", 64)
		digest, err := builder.ResolveDigest(ctx, "debian@"+pinned)
		require.NoError(t, err)
		assert.Equal(t, pinned, digest)

		if !s.hostHasInternet() {
			t.Skip("resolving a registry digest needs internet access")
		}
		digest, err = builder.ResolveDigest(ctx, "hello-world:latest")
		require.NoError(t, err)
		assert.True(t, strings.HasPrefix(digest, "sha256:"), "got %q", digest)
	})
}

// readLineWithin reads one line, failing instead of hanging when nothing arrives
func readLineWithin(r *bufio.Reader, timeout time.Duration) (string, error) {
	type result struct {
		line string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		line, err := r.ReadString('\n')
		ch <- result{line, err}
	}()
	select {
	case res := <-ch:
		return res.line, res.err
	case <-time.After(timeout):
		return "", fmt.Errorf("no line within %s", timeout)
	}
}
