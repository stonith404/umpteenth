//go:build unit

package docker

import (
	"archive/tar"
	"bytes"
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

// tarEntries lists the names, link targets and modes of a tar stream
func tarEntries(t *testing.T, r io.Reader) map[string]*tar.Header {
	t.Helper()
	entries := map[string]*tar.Header{}
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return entries
		}
		require.NoError(t, err)
		entries[hdr.Name] = hdr
	}
}

func TestRerootArchive(t *testing.T) {
	// The engine prefixes every entry with the directory's base name
	var src bytes.Buffer
	tw := tar.NewWriter(&src)
	for _, hdr := range []*tar.Header{
		{Name: "outputs/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "outputs/a.txt", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1},
		{Name: "outputs/sub/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "outputs/sub/b", Typeflag: tar.TypeReg, Mode: 0o600, Size: 0},
		{Name: "outputs/c", Typeflag: tar.TypeLink, Linkname: "outputs/a.txt"},
	} {
		require.NoError(t, tw.WriteHeader(hdr))
		if hdr.Size > 0 {
			_, _ = tw.Write([]byte("a"))
		}
	}
	require.NoError(t, tw.Close())

	var dst bytes.Buffer
	require.NoError(t, rerootArchive(&dst, &src, "outputs"))
	entries := tarEntries(t, &dst)
	assert.Len(t, entries, 4)
	assert.Contains(t, entries, "a.txt")
	assert.Contains(t, entries, "sub/")
	assert.Contains(t, entries, "sub/b")
	require.Contains(t, entries, "c")
	assert.Equal(t, "a.txt", entries["c"].Linkname)
}

func TestLimitWriter(t *testing.T) {
	var buf bytes.Buffer
	w := &limitWriter{w: &buf, max: 5}
	_, err := w.Write([]byte("abc"))
	require.NoError(t, err)
	_, err = w.Write([]byte("de"))
	require.NoError(t, err)
	_, err = w.Write([]byte("f"))
	assert.ErrorIs(t, err, sandbox.ErrOutputTooLarge)
	assert.Equal(t, "abcde", buf.String())
}

func TestAncestorsAndTarMode(t *testing.T) {
	assert.Equal(t, []string{"/ump", "/ump/toolkit"}, ancestors("/ump/toolkit/x.sh"))
	assert.Empty(t, ancestors("/top"))

	assert.EqualValues(t, 0o755, tarMode(0o755))
	assert.EqualValues(t, 0o4755, tarMode(0o755|fs.ModeSetuid))
	assert.EqualValues(t, 0o1777, tarMode(0o777|fs.ModeSticky))
}

func TestLayoutArchive(t *testing.T) {
	entries := tarEntries(t, bytes.NewReader(layoutArchive([]byte("ump"), false)))
	require.Contains(t, entries, "workspace/")
	assert.Equal(t, 1000, entries["workspace/"].Uid)
	require.Contains(t, entries, "ump/")
	assert.Equal(t, 1000, entries["ump/"].Uid)
	require.Contains(t, entries, "run/ump/")
	assert.EqualValues(t, 0o1777, entries["run/ump/"].Mode)
	require.Contains(t, entries, "usr/local/bin/ump")
	assert.EqualValues(t, 0o755, entries["usr/local/bin/ump"].Mode)
	assert.Equal(t, 0, entries["usr/local/bin/ump"].Uid)
	assert.NotContains(t, entries, "etc/ssh/ssh_config.d/ump.conf")
}

func TestProxiedSandboxesSendSSHThroughTheProxy(t *testing.T) {
	archive := layoutArchive([]byte("ump"), true)
	entries := tarEntries(t, bytes.NewReader(archive))
	require.Contains(t, entries, "etc/ssh/ssh_config.d/ump.conf")
	assert.EqualValues(t, 0o644, entries["etc/ssh/ssh_config.d/ump.conf"].Mode)
	assert.Equal(t, 0, entries["etc/ssh/ssh_config.d/ump.conf"].Uid)
	assert.Contains(t, sandbox.SSHConfig, "Host *\n  ProxyCommand /usr/local/bin/ump connect %h %p\n")
}

func TestSandboxEnvKeepsBrokerVariables(t *testing.T) {
	env := sandboxEnv(sandbox.Spec{
		RunID:  "run-1",
		Env:    map[string]string{"B": "2", "A": "1", "UMP_TOKEN": "forged", "BAD=KEY": "x"},
		Broker: sandbox.BrokerAccess{Token: "real"},
	}, 8081, nil)
	assert.Equal(t, []string{"A=1", "B=2", "UMP_BROKER_URL=http://umpteenth:8081", "UMP_TOKEN=real", "UMP_RUN_ID=run-1"}, env)
}

func TestProxiedSandboxesGetTheProxy(t *testing.T) {
	for _, network := range []sandbox.NetworkPolicy{sandbox.NetworkInternet, sandbox.NetworkAllowlist} {
		env := sandboxEnv(sandbox.Spec{Network: network, Broker: sandbox.BrokerAccess{Token: "tok"}, Env: map[string]string{"HTTPS_PROXY": "http://mine", "ALL_PROXY": "socks5://mine", "API_KEY": "x"}}, 8081, nil)
		assert.Contains(t, env, "HTTPS_PROXY=http://ump:tok@umpteenth:8081", network)
		assert.Contains(t, env, "ALL_PROXY=socks5h://ump:tok@umpteenth:8081", network)
		assert.Contains(t, env, "NO_PROXY=umpteenth,localhost,127.0.0.1", network)
		assert.Contains(t, env, "API_KEY=x", network)

		// A job can't send its traffic past the egress proxy through a proxy of its own
		assert.NotContains(t, env, "HTTPS_PROXY=http://mine", network)
		assert.NotContains(t, env, "ALL_PROXY=socks5://mine", network)
	}

	// Sandboxes with a route of their own keep a proxy a job sets up for itself
	env := sandboxEnv(sandbox.Spec{Network: sandbox.NetworkUnrestricted, Env: map[string]string{"HTTPS_PROXY": "http://mine"}}, 8081, nil)
	assert.Contains(t, env, "HTTPS_PROXY=http://mine")
	assert.NotContains(t, strings.Join(env, "\n"), "umpteenth:8081@")

	// Resolvers only go to sandboxes that resolve names themselves, and a job can't replace them
	dns := []string{"9.9.9.9", "1.1.1.1"}
	env = sandboxEnv(sandbox.Spec{Network: sandbox.NetworkUnrestricted, Env: map[string]string{"UMP_DNS": "10.0.0.1"}}, 8081, dns)
	assert.Contains(t, env, "UMP_DNS=9.9.9.9,1.1.1.1")
	assert.NotContains(t, env, "UMP_DNS=10.0.0.1")
	for _, network := range []sandbox.NetworkPolicy{sandbox.NetworkInternet, sandbox.NetworkAllowlist, sandbox.NetworkNone} {
		env = sandboxEnv(sandbox.Spec{Network: network}, 8081, dns)
		assert.NotContains(t, strings.Join(env, "\n"), "UMP_DNS", network)
	}
}

func TestProxyBuildArgsPointBuildStepsAtTheProxy(t *testing.T) {
	args := proxyBuildArgs("tok", 8081)
	require.NotNil(t, args["HTTPS_PROXY"])
	assert.Equal(t, "http://ump:tok@umpteenth:8081", *args["https_proxy"])
	assert.Equal(t, "socks5h://ump:tok@umpteenth:8081", *args["ALL_PROXY"])
	assert.Equal(t, "umpteenth,localhost,127.0.0.1", *args["NO_PROXY"])
}

func TestUnrestrictedNetworkIsUpToTheOperator(t *testing.T) {
	assert.NotContains(t, (&Adapter{}).networks(), sandbox.NetworkUnrestricted)
	assert.Contains(t, (&Adapter{cfg: Config{AllowUnrestricted: true}}).networks(), sandbox.NetworkUnrestricted)

	// Create turns the policy away before touching the engine when it isn't offered
	_, err := (&Adapter{}).Create(t.Context(), sandbox.Spec{Network: sandbox.NetworkUnrestricted, Image: "example"})
	assert.ErrorIs(t, err, sandbox.ErrUnsupported)
}

func TestContainerIDPattern(t *testing.T) {
	id := strings.Repeat("ab12", 16)
	for _, line := range []string{
		"1234 1200 0:52 /docker/containers/" + id + "/hostname /etc/hostname rw,relatime - ext4 /dev/vda1 rw",
		"12:memory:/docker/" + id,
		"0::/system.slice/docker-" + id + ".scope",
	} {
		m := containerIDPattern.FindStringSubmatch(line)
		require.NotNil(t, m, line)
		assert.Equal(t, id, m[1])
	}
	assert.Nil(t, containerIDPattern.FindStringSubmatch("0::/user.slice/user-1000.slice/session-2.scope"))
}

func TestNormalizeArchAndIsolation(t *testing.T) {
	assert.Equal(t, "amd64", normalizeArch("x86_64"))
	assert.Equal(t, "arm64", normalizeArch("aarch64"))
	assert.Equal(t, "arm64", normalizeArch("arm64"))
	assert.Equal(t, "", normalizeArch("s390x"))

	assert.Equal(t, sandbox.IsolationGVisor, runtimeIsolation("runsc"))
	assert.Equal(t, sandbox.IsolationMicroVM, runtimeIsolation("kata-runtime"))
	assert.Equal(t, sandbox.IsolationContainer, runtimeIsolation("crun"))
}

func TestBufferedPipeNeverBlocksWriter(t *testing.T) {
	p := newBufferedPipe(4)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = p.Write([]byte("abcdef"))
		_, _ = p.Write([]byte("gh"))
		_ = p.Close()
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("writing to an unread buffered pipe blocked")
	}
	data, err := io.ReadAll(p)
	require.NoError(t, err)
	assert.Equal(t, "abcd", string(data))
}

func TestReadProgress(t *testing.T) {
	stream := `{"stream":"Step 1/2 : FROM x\n"}
{"status":"Downloading","progressDetail":{"current":1,"total":2},"progress":"[=>  ]","id":"abc"}
{"status":"Pull complete","progressDetail":{},"id":"abc"}
{"stream":" ---> Running in 0123456789ab\n"}
{"errorDetail":{"message":"exit code 3"},"error":"exit code 3"}
{"stream":"never reached\n"}`
	var logs bytes.Buffer
	var step string
	err := readProgress(strings.NewReader(stream), func(m progressMessage) {
		if match := runningInPattern.FindStringSubmatch(m.Stream); match != nil {
			step = match[1]
		}
		writeProgress(&logs, m)
	})
	assert.EqualError(t, err, "exit code 3")
	assert.Equal(t, "0123456789ab", step)
	assert.Equal(t, "Step 1/2 : FROM x\nabc: Pull complete\n ---> Running in 0123456789ab\n", logs.String())
}

func TestRegistryAuth(t *testing.T) {
	a := &Adapter{cfg: Config{Registry: "https://ghcr.io/acme/jobs", RegistryUsername: "u", RegistryPassword: "p"}}
	assert.Equal(t, "ghcr.io", registryHost(a.cfg.Registry))
	assert.NotEmpty(t, a.registryAuth("ghcr.io/acme/jobs/job-1:abc"))
	assert.Empty(t, a.registryAuth("debian:trixie-slim"), "credentials must only go to the configured registry")
	assert.Contains(t, a.buildAuthConfigs(), "ghcr.io")

	a.cfg.RegistryUsername = ""
	assert.Empty(t, a.registryAuth("ghcr.io/acme/jobs/job-1:abc"))
	assert.Nil(t, a.buildAuthConfigs())
}

func TestIsUnavailableMessage(t *testing.T) {
	assert.True(t, isUnavailableMessage("pull access denied for umpteenth-sandboxtest/job, repository does not exist"))
	assert.True(t, isUnavailableMessage("manifest unknown"))
	assert.False(t, isUnavailableMessage("dial tcp: lookup registry-1.docker.io: no such host"))
}

func TestIsMissingDependency(t *testing.T) {
	assert.True(t, isMissingDependency(errors.New("failed to set up container networking: network ump-unrestricted not found")))
	assert.True(t, isMissingDependency(errors.New("Error response from daemon: No such container: umpteenth-relay-x")))
	assert.False(t, isMissingDependency(errors.New("conflict: name already in use")))
}

func TestContainerConfigDisablesInheritedHealthcheck(t *testing.T) {
	a := &Adapter{cfg: Config{InstanceID: "test", HostID: "host"}}
	cfg, _ := a.containerConfig(sandbox.Spec{RunID: "run", Image: "example", AgentUser: sandbox.UserAgent, Network: sandbox.NetworkInternet}, "run-network", 8081)

	require.NotNil(t, cfg.Healthcheck)
	assert.Equal(t, []string{"NONE"}, cfg.Healthcheck.Test)
}
