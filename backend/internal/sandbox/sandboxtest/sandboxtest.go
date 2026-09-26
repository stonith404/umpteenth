// Package sandboxtest is the conformance suite every sandbox adapter must pass against a real backend (PLAN.md §4.2 and §4.6)
package sandboxtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/sandbox"
)

// DefaultImage is the image the suite expects as the adapter's default, and the FROM of its image builds
// Sandboxes need sh, coreutils, procps, curl and python3, which umpteenth-sandbox provides
const DefaultImage = "ghcr.io/stonith404/umpteenth-sandbox:latest"

// Options adapt the suite to a backend
type Options struct {
	// Skip maps checks that cannot pass on a backend, such as "SandboxIsolation/InternetPeer", to the reason, which is reported with the skip
	Skip map[string]string
	// OOMStopsSandbox accepts a whole sandbox stopping when a command runs out of memory, as long as it is reported as OOM
	// gVisor behaves like this, since its kernel and the command share the memory limit
	OOMStopsSandbox bool
}

// suite carries what the checks share
type suite struct {
	a          sandbox.Adapter
	info       sandbox.Info
	newAdapter func(t *testing.T) sandbox.Adapter
	port       int
	skip       map[string]string
	// oomStopsSandbox mirrors Options.OOMStopsSandbox
	oomStopsSandbox bool

	internetOnce sync.Once
	internet     bool
}

// Run runs every contract check against adapters returned by newAdapter
// newAdapter must derive the instance ID from t.Name(), so adapters created for the same test belong to the same instance
// The suite relies on that to simulate a restart (same test) and to check ownership (a subtest is another instance)
// The adapter must route UMP_BROKER_URL to BrokerPort on this machine and use DefaultImage, or an equivalent, when a spec names no image
func Run(t *testing.T, newAdapter func(t *testing.T) sandbox.Adapter) {
	RunWithOptions(t, newAdapter, Options{})
}

// RunWithOptions is Run with backend-specific options
func RunWithOptions(t *testing.T, newAdapter func(t *testing.T) sandbox.Adapter, opts Options) {
	port, err := BrokerPort()
	require.NoError(t, err)

	// Prepare the backend the way the server does at startup
	a := newAdapter(t)
	require.NoError(t, a.Prepare(t.Context()))
	info, err := a.Check(t.Context())
	require.NoError(t, err)
	s := &suite{a: a, info: info, newAdapter: newAdapter, port: port, skip: opts.Skip, oomStopsSandbox: opts.OOMStopsSandbox}

	t.Run("Check", s.testCheck)

	// Most checks share one sandbox, since creating one per check would dominate the run time
	shared := s.create(t, sandbox.Spec{Env: map[string]string{"JOB_SECRET": "job-secret-value"}})
	t.Run("Layout", func(t *testing.T) { s.testLayout(t, shared) })
	t.Run("Env", func(t *testing.T) { s.testEnv(t, shared) })
	t.Run("Exec", func(t *testing.T) { s.testExec(t, shared) })
	t.Run("Streaming", func(t *testing.T) { s.testStreaming(t, shared) })
	t.Run("Users", func(t *testing.T) { s.testUsers(t, shared) })
	t.Run("Timeout", func(t *testing.T) { s.testTimeout(t, shared) })
	t.Run("Cancel", func(t *testing.T) { s.testCancel(t, shared) })
	t.Run("Files", func(t *testing.T) { s.testFiles(t, shared) })
	t.Run("Archive", func(t *testing.T) { s.testArchive(t, shared) })
	t.Run("Attach", func(t *testing.T) { s.testAttach(t, shared) })
	t.Run("ProcEnvIsolation", func(t *testing.T) { s.testProcEnvIsolation(t, shared) })
	t.Run("BrokerReachable", func(t *testing.T) { s.testBroker(t, shared) })
	t.Run("NetworkNone", func(t *testing.T) { s.testNetworkNone(t, shared) })
	t.Run("SandboxIsolation", func(t *testing.T) { s.testSandboxIsolation(t, shared) })
	t.Run("OOM", s.testOOM)
	t.Run("RootUser", s.testRootUser)
	// The parent's t yields an adapter of the same instance, as after a server restart
	t.Run("GetAfterReopen", func(st *testing.T) { s.testGetAfterReopen(st, newAdapter(t), shared) })
	t.Run("ListOwnership", func(t *testing.T) { s.testListOwnership(t, shared) })
	t.Run("DestroyIdempotent", s.testDestroy)
	t.Run("ImageBuilder", s.testImageBuilder)
}

func (s *suite) testCheck(t *testing.T) {
	assert.Equal(t, s.a.Type(), s.info.Adapter)
	assert.Contains(t, []string{"amd64", "arm64"}, s.info.Arch)
	assert.NotEmpty(t, s.info.Isolation)
	assert.True(t, s.info.Caps.SupportsNetwork(sandbox.NetworkInternet), "every adapter must support internet access")
	_, builds := s.a.(sandbox.ImageBuilder)
	assert.Equal(t, builds, s.info.ImageBuilds, "ImageBuilds must match whether the adapter implements ImageBuilder")
}

func (s *suite) testLayout(t *testing.T, sb sandbox.Sandbox) {
	out := s.mustSh(t, sb, `pwd; id -u; stat -c %u /workspace; test -w /workspace && echo workspace-writable; touch /ump/probe && echo ump-writable; test -x /usr/local/bin/ump && echo ump-installed`)
	assert.Equal(t, []string{"/workspace", "1000", "1000", "workspace-writable", "ump-writable", "ump-installed"}, lines(out))
}

func (s *suite) testEnv(t *testing.T, sb sandbox.Sandbox) {
	out := s.mustShReq(t, sb, sandbox.ExecRequest{
		Cmd: []string{"sh", "-c", `printf '%s\n' "$JOB_SECRET" "$UMP_RUN_ID" "$UMP_BROKER_URL" "$FROM_REQUEST"; test -n "$UMP_TOKEN" && echo has-token`},
		Env: map[string]string{"FROM_REQUEST": "request-value"},
	})
	got := lines(out)
	require.Len(t, got, 5)
	assert.Equal(t, "job-secret-value", got[0])
	assert.NotEmpty(t, got[1], "UMP_RUN_ID must be set")
	assert.True(t, strings.HasPrefix(got[2], "http://") || strings.HasPrefix(got[2], "https://"), "UMP_BROKER_URL must be a URL, got %q", got[2])
	assert.Equal(t, "request-value", got[3])
	assert.Equal(t, "has-token", got[4])
}

func (s *suite) testExec(t *testing.T, sb sandbox.Sandbox) {
	t.Run("ExitCode", func(t *testing.T) {
		_, _, res := s.sh(t, sb, "exit 3")
		assert.Equal(t, 3, res.ExitCode)
	})
	t.Run("SeparateStreams", func(t *testing.T) {
		stdout, stderr, res := s.sh(t, sb, "echo to-stdout; echo to-stderr >&2")
		assert.Equal(t, 0, res.ExitCode)
		assert.Equal(t, "to-stdout\n", stdout)
		assert.Equal(t, "to-stderr\n", stderr)
	})
	t.Run("WorkDir", func(t *testing.T) {
		out := s.mustShReq(t, sb, sandbox.ExecRequest{Cmd: []string{"pwd"}, WorkDir: "/tmp"})
		assert.Equal(t, "/tmp", strings.TrimSpace(out))
	})
	t.Run("ArgvPassthrough", func(t *testing.T) {
		out := s.mustShReq(t, sb, sandbox.ExecRequest{Cmd: []string{"printf", "%s|", "a b", "c'd", "$HOME"}})
		assert.Equal(t, "a b|c'd|$HOME|", out)
	})
	t.Run("Stdin", func(t *testing.T) {
		out := s.mustShReq(t, sb, sandbox.ExecRequest{Cmd: []string{"cat"}, Stdin: strings.NewReader("hello\nworld")})
		assert.Equal(t, "hello\nworld", out)
	})
	t.Run("LargeInterleavedOutput", func(t *testing.T) {
		stdout, stderr, res := s.sh(t, sb, `(head -c 2000000 /dev/zero | tr '\0' o) & (head -c 1500000 /dev/zero | tr '\0' e >&2); wait`)
		assert.Equal(t, 0, res.ExitCode)
		assert.Equal(t, 2000000, len(stdout))
		assert.Equal(t, 1500000, len(stderr))
		assert.Equal(t, "", strings.Trim(stdout, "o"))
		assert.Equal(t, "", strings.Trim(stderr, "e"))
	})
	t.Run("MissingCommand", func(t *testing.T) {
		res, err := sb.Exec(t.Context(), sandbox.ExecRequest{Cmd: []string{"sandboxtest-no-such-command"}})
		require.NoError(t, err)
		assert.NotEqual(t, 0, res.ExitCode)
	})
}

func (s *suite) testStreaming(t *testing.T, sb sandbox.Sandbox) {
	w := &timedWriter{start: time.Now()}
	res, err := sb.Exec(t.Context(), sandbox.ExecRequest{Cmd: []string{"sh", "-c", "echo first; sleep 2; echo second"}, Stdout: w})
	require.NoError(t, err)
	assert.Equal(t, 0, res.ExitCode)
	assert.Equal(t, "first\nsecond\n", w.String())
	first, ok := w.firstWriteAfter()
	require.True(t, ok)
	assert.Less(t, first, 1500*time.Millisecond, "output must be streamed while the command runs")
	assert.GreaterOrEqual(t, res.Duration, 2*time.Second)
}

func (s *suite) testUsers(t *testing.T, sb sandbox.Sandbox) {
	assert.Equal(t, "1000", strings.TrimSpace(s.mustSh(t, sb, "id -u")))
	assert.Equal(t, "0", strings.TrimSpace(s.mustShReq(t, sb, sandbox.ExecRequest{Cmd: []string{"id", "-u"}, User: sandbox.UserRoot})))
	if s.info.Caps.SeparateUsers {
		assert.Equal(t, "1001", strings.TrimSpace(s.mustShReq(t, sb, sandbox.ExecRequest{Cmd: []string{"id", "-u"}, User: sandbox.UserMCP})))
	}
}

func (s *suite) testTimeout(t *testing.T, sb sandbox.Sandbox) {
	t.Run("KillsProcessTree", func(t *testing.T) {
		tag := randomHex(4)
		script := `sleep 300 & echo $! > /tmp/bg1-` + tag + `; (sleep 300 & echo $! > /tmp/bg2-` + tag + `); sleep 300`
		start := time.Now()
		res, err := sb.Exec(t.Context(), sandbox.ExecRequest{Cmd: []string{"sh", "-c", script}, Timeout: time.Second})
		require.NoError(t, err)
		assert.True(t, res.TimedOut)
		assert.NotEqual(t, 0, res.ExitCode)
		assert.Less(t, time.Since(start), 3*time.Second, "the tree must be killed within 2s of the timeout")
		s.assertGone(t, sb, "/tmp/bg1-"+tag, "/tmp/bg2-"+tag)
		assert.Equal(t, "still-usable", strings.TrimSpace(s.mustSh(t, sb, "echo still-usable")))
	})
	t.Run("IgnoresSIGTERM", func(t *testing.T) {
		start := time.Now()
		res, err := sb.Exec(t.Context(), sandbox.ExecRequest{Cmd: []string{"sh", "-c", "trap '' TERM; sleep 300"}, Timeout: 500 * time.Millisecond})
		require.NoError(t, err)
		assert.True(t, res.TimedOut)
		// SIGKILL follows 1.5s after SIGTERM, which leaves some room for the kill round trip within the 2s contract
		assert.Less(t, time.Since(start), 500*time.Millisecond+2500*time.Millisecond)
	})
}

func (s *suite) testCancel(t *testing.T, sb sandbox.Sandbox) {
	tag := randomHex(4)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	time.AfterFunc(500*time.Millisecond, cancel)

	start := time.Now()
	_, err := sb.Exec(ctx, sandbox.ExecRequest{Cmd: []string{"sh", "-c", "sleep 300 & echo $! > /tmp/bg3-" + tag + "; sleep 300"}})
	require.ErrorIs(t, err, context.Canceled)
	assert.Less(t, time.Since(start), 500*time.Millisecond+2*time.Second)
	s.assertGone(t, sb, "/tmp/bg3-"+tag)
	assert.Equal(t, "still-usable", strings.TrimSpace(s.mustSh(t, sb, "echo still-usable")))
}

// assertGone checks that the processes whose pids were written to the files no longer run; zombies count as gone
func (s *suite) assertGone(t *testing.T, sb sandbox.Sandbox, pidFiles ...string) {
	t.Helper()
	script := `for f in ` + strings.Join(pidFiles, " ") + `; do
  p=$(cat "$f")
  i=0
  while [ -e /proc/$p ] && [ "$(cut -d' ' -f3 /proc/$p/stat)" != Z ]; do
    i=$((i+1)); if [ $i -gt 20 ]; then echo "alive $p"; exit 1; fi; sleep 0.1
  done
done
echo gone`
	assert.Equal(t, "gone", strings.TrimSpace(s.mustSh(t, sb, script)))
}

func (s *suite) testNetworkNone(t *testing.T, shared sandbox.Sandbox) {
	if !s.info.Caps.SupportsNetwork(sandbox.NetworkNone) {
		t.Skip("the adapter does not support network: none")
	}
	sb := s.create(t, sandbox.Spec{Network: sandbox.NetworkNone})

	// The broker stays reachable so ump keeps working
	s.testBroker(t, sb)

	// Nothing else is, neither by IP nor by name
	for _, url := range []string{"https://1.1.1.1", "http://example.com"} {
		_, _, res := s.sh(t, sb, "curl -sS -m 5 -o /dev/null "+url)
		assert.NotEqual(t, 0, res.ExitCode, "%s must be unreachable with network: none", url)
	}

	// The control proves the probe works where internet access is allowed, by name too, which needs working DNS
	if s.hostHasInternet() {
		for _, url := range []string{"https://1.1.1.1", "http://example.com"} {
			_, stderr, res := s.sh(t, shared, "curl -sS -m 10 -o /dev/null "+url)
			assert.Equal(t, 0, res.ExitCode, "internet sandbox cannot reach %s: %s", url, stderr)
		}
	}
}

func (s *suite) testBroker(t *testing.T, sb sandbox.Sandbox) {
	token := strings.TrimSpace(s.mustSh(t, sb, `printf %s "$UMP_TOKEN"`))
	before := brokerHits(token)
	out := s.mustSh(t, sb, `curl -fsS -m 10 -H "Authorization: Bearer $UMP_TOKEN" "$UMP_BROKER_URL/ping"`)
	assert.Equal(t, "pong", strings.TrimSpace(out))
	assert.Greater(t, brokerHits(token), before, "the stub broker must have seen the sandbox's token")
}

func (s *suite) testSandboxIsolation(t *testing.T, shared sandbox.Sandbox) {
	// Serve something in the shared sandbox and prove it answers locally
	s.mustSh(t, shared, `nohup python3 -m http.server 8765 --bind 0.0.0.0 >/dev/null 2>&1 & echo $! > /tmp/httpd.pid`)
	t.Cleanup(func() {
		_, _ = shared.Exec(context.Background(), sandbox.ExecRequest{Cmd: []string{"sh", "-c", "kill $(cat /tmp/httpd.pid)"}})
	})
	s.mustSh(t, shared, `for i in $(seq 1 100); do curl -s -m 1 -o /dev/null http://127.0.0.1:8765/ && exit 0; sleep 0.1; done; exit 1`)
	s.mustSh(t, shared, `echo marker > /workspace/isolation-marker`)

	// Collect every address of the shared sandbox by asking each interface, which works where /proc/net is incomplete, such as under gVisor
	ips := strings.Fields(s.mustSh(t, shared, `python3 -c "
import fcntl, socket, struct
s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
ips = set()
for _, name in socket.if_nameindex():
    try:
        ips.add(socket.inet_ntoa(fcntl.ioctl(s.fileno(), 0x8915, struct.pack('256s', name[:15].encode()))[20:24]))
    except OSError:
        pass
print(' '.join(sorted(ip for ip in ips if not ip.startswith('127.'))))
"`))
	require.NotEmpty(t, ips)

	// Neither a sandbox with internet access nor one without may reach it or see its files
	probe := func(t *testing.T, peer sandbox.Sandbox) {
		for _, ip := range ips {
			_, _, res := s.sh(t, peer, "curl -s -m 3 -o /dev/null http://"+ip+":8765/")
			assert.NotEqual(t, 0, res.ExitCode, "the peer reached the shared sandbox at %s", ip)
		}
		_, _, res := s.sh(t, peer, "test -e /workspace/isolation-marker")
		assert.NotEqual(t, 0, res.ExitCode, "the peer sees the shared sandbox's files")
	}
	t.Run("InternetPeer", func(t *testing.T) {
		s.skipKnownGap(t)
		probe(t, s.create(t, sandbox.Spec{}))
	})
	t.Run("NonePeer", func(t *testing.T) {
		s.skipKnownGap(t)
		if !s.info.Caps.SupportsNetwork(sandbox.NetworkNone) {
			t.Skip("the adapter does not support network: none")
		}
		probe(t, s.create(t, sandbox.Spec{Network: sandbox.NetworkNone}))
	})
}

// skipKnownGap skips a check the options list as impossible on this backend
func (s *suite) skipKnownGap(t *testing.T) {
	t.Helper()
	for name, reason := range s.skip {
		if strings.HasSuffix(t.Name(), "/"+name) {
			t.Skip(reason)
		}
	}
}

func (s *suite) testOOM(t *testing.T) {
	if !s.info.Caps.Limits.Memory {
		t.Skip("the adapter does not enforce memory limits")
	}
	sb := s.create(t, sandbox.Spec{Resources: sandbox.Resources{MemoryMB: 128}})

	res, err := sb.Exec(t.Context(), sandbox.ExecRequest{Cmd: []string{"python3", "-c", "b = bytearray(1024 * 1024 * 1024)"}, Timeout: time.Minute})
	if s.oomStopsSandbox && errors.Is(err, sandbox.ErrSandboxGone) {
		assert.True(t, res.OOMKilled, "a sandbox stopped by running out of memory must be reported as OOMKilled")
		assert.Contains(t, err.Error(), "out of memory")
		return
	}
	require.NoError(t, err)
	assert.True(t, res.OOMKilled, "exceeding the memory limit must be reported as OOMKilled")
	assert.NotEqual(t, 0, res.ExitCode)
	assert.Equal(t, "still-usable", strings.TrimSpace(s.mustSh(t, sb, "echo still-usable")))

	// A SIGKILL that has nothing to do with memory must not be reported as OOM
	res, err = sb.Exec(t.Context(), sandbox.ExecRequest{Cmd: []string{"sh", "-c", "kill -KILL $$"}})
	require.NoError(t, err)
	assert.NotEqual(t, 0, res.ExitCode)
	assert.False(t, res.OOMKilled)
}

func (s *suite) testRootUser(t *testing.T) {
	sb := s.create(t, sandbox.Spec{AgentUser: sandbox.UserRoot})
	assert.Equal(t, "0", strings.TrimSpace(s.mustSh(t, sb, "id -u")))
	assert.Equal(t, "ok", strings.TrimSpace(s.mustSh(t, sb, "touch /etc/sandboxtest-root && echo ok")))
}

func (s *suite) testGetAfterReopen(t *testing.T, reopened sandbox.Adapter, shared sandbox.Sandbox) {
	ctx := t.Context()
	s.mustSh(t, shared, "echo reopen > /workspace/reopen.txt")

	// A restarted server prepares its adapter again and reattaches by ID
	require.NoError(t, reopened.Prepare(ctx))
	got, err := reopened.Get(ctx, shared.ID())
	require.NoError(t, err)
	assert.Equal(t, shared.ID(), got.ID())
	assert.Equal(t, "reopen", strings.TrimSpace(s.mustSh(t, got, "cat /workspace/reopen.txt")))

	_, err = reopened.Get(ctx, "sandboxtest-does-not-exist")
	assert.ErrorIs(t, err, sandbox.ErrNotFound)
}

func (s *suite) testListOwnership(t *testing.T, shared sandbox.Sandbox) {
	ctx := t.Context()
	list, err := s.a.List(ctx)
	require.NoError(t, err)
	var found *sandbox.Summary
	for i := range list {
		if list[i].ID == shared.ID() {
			found = &list[i]
		}
	}
	require.NotNil(t, found, "List must include the instance's own sandbox")
	assert.NotEmpty(t, found.RunID)
	assert.Equal(t, "sandboxtest-job", found.JobID)
	assert.WithinDuration(t, time.Now(), found.CreatedAt, time.Hour)

	// A subtest gets another instance, which must neither see nor touch the sandbox
	other := s.newAdapter(t)
	otherList, err := other.List(ctx)
	require.NoError(t, err)
	for _, sum := range otherList {
		assert.NotEqual(t, shared.ID(), sum.ID, "another instance must not list this instance's sandbox")
	}
	_, err = other.Get(ctx, shared.ID())
	assert.ErrorIs(t, err, sandbox.ErrNotFound)
	_ = other.Destroy(ctx, shared.ID())
	_, err = s.a.Get(ctx, shared.ID())
	assert.NoError(t, err, "another instance's Destroy must not remove this instance's sandbox")
}

func (s *suite) testDestroy(t *testing.T) {
	ctx := t.Context()
	sb := s.create(t, sandbox.Spec{})

	require.NoError(t, s.a.Destroy(ctx, sb.ID()))
	require.NoError(t, s.a.Destroy(ctx, sb.ID()), "Destroy must be idempotent")
	require.NoError(t, s.a.Destroy(ctx, "sandboxtest-never-existed"))

	_, err := s.a.Get(ctx, sb.ID())
	assert.ErrorIs(t, err, sandbox.ErrNotFound)
	_, err = sb.Exec(ctx, sandbox.ExecRequest{Cmd: []string{"true"}})
	assert.ErrorIs(t, err, sandbox.ErrSandboxGone)

	list, err := s.a.List(ctx)
	require.NoError(t, err)
	for _, sum := range list {
		assert.NotEqual(t, sb.ID(), sum.ID)
	}
}

// create provisions a sandbox for the test and destroys it when the test ends
func (s *suite) create(t *testing.T, spec sandbox.Spec) sandbox.Sandbox {
	t.Helper()
	if spec.RunID == "" {
		spec.RunID = "sbt-" + randomHex(8)
	}
	if spec.JobID == "" {
		spec.JobID = "sandboxtest-job"
	}
	if spec.WorkspaceID == "" {
		spec.WorkspaceID = "sandboxtest-workspace"
	}
	spec.Broker = sandbox.BrokerAccess{Token: "sbt-token-" + randomHex(16), Port: s.port}
	if spec.TTL == 0 {
		spec.TTL = 30 * time.Minute
	}

	sb, err := s.a.Create(t.Context(), spec)
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		assert.NoError(t, s.a.Destroy(ctx, sb.ID()))
	})
	return sb
}

// sh runs a shell script as the sandbox's agent user
func (s *suite) sh(t *testing.T, sb sandbox.Sandbox, script string) (stdout, stderr string, res sandbox.ExecResult) {
	t.Helper()
	return s.run(t, sb, sandbox.ExecRequest{Cmd: []string{"sh", "-c", script}})
}

// run executes a request with a generous timeout and captures its output
func (s *suite) run(t *testing.T, sb sandbox.Sandbox, req sandbox.ExecRequest) (stdout, stderr string, res sandbox.ExecResult) {
	t.Helper()
	var out, errOut strings.Builder
	req.Stdout = &out
	req.Stderr = &errOut
	if req.Timeout == 0 {
		req.Timeout = 2 * time.Minute
	}
	res, err := sb.Exec(t.Context(), req)
	require.NoError(t, err)
	return out.String(), errOut.String(), res
}

// mustSh runs a shell script that must succeed and returns its stdout
func (s *suite) mustSh(t *testing.T, sb sandbox.Sandbox, script string) string {
	t.Helper()
	return s.mustShReq(t, sb, sandbox.ExecRequest{Cmd: []string{"sh", "-c", script}})
}

// mustShReq runs a request that must succeed and returns its stdout
func (s *suite) mustShReq(t *testing.T, sb sandbox.Sandbox, req sandbox.ExecRequest) string {
	t.Helper()
	stdout, stderr, res := s.run(t, sb, req)
	require.Equal(t, 0, res.ExitCode, "command %q failed: %s", req.Cmd, stderr)
	return stdout
}

// hostHasInternet decides whether internet-positive checks can run at all
func (s *suite) hostHasInternet() bool {
	s.internetOnce.Do(func() {
		conn, err := net.DialTimeout("tcp", "1.1.1.1:443", 3*time.Second)
		if err == nil {
			_ = conn.Close()
			s.internet = true
		}
	})
	return s.internet
}

// timedWriter records when the first write arrived
type timedWriter struct {
	mu    sync.Mutex
	start time.Time
	first time.Duration
	buf   strings.Builder
}

func (w *timedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.buf.Len() == 0 {
		w.first = time.Since(w.start)
	}
	return w.buf.Write(p)
}

func (w *timedWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func (w *timedWriter) firstWriteAfter() (time.Duration, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.first, w.buf.Len() > 0
}

// lines splits output into trimmed lines, dropping the trailing newline
func lines(s string) []string {
	return strings.Split(strings.TrimRight(s, "\n"), "\n")
}

// randomHex returns n random bytes as hex
func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
