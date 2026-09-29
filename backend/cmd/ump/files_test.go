//go:build unit

package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tarOf builds an archive of regular files owned by the current user
func tarOf(t *testing.T, files map[string]string, mode int64) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, content := range files {
		require.NoError(t, tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: name, Mode: mode, Uid: os.Getuid(), Gid: os.Getgid(), Size: int64(len(content))}))
		_, err := tw.Write([]byte(content))
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	return &buf
}

func TestPutFilesCreatesParentsAndReplacesSymlinks(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "workspace"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "elsewhere"), []byte("keep"), 0o600))
	require.NoError(t, os.Symlink(filepath.Join(root, "elsewhere"), filepath.Join(root, "workspace", "link.txt")))

	require.NoError(t, putFiles(root, tarOf(t, map[string]string{"workspace/deep/er/a.txt": "hello", "workspace/link.txt": "replaced"}, 0o640)))

	// New parents get 0755, while an existing one keeps its mode
	for dir, mode := range map[string]os.FileMode{"workspace/deep": 0o755, "workspace/deep/er": 0o755, "workspace": 0o700} {
		info, err := os.Stat(filepath.Join(root, dir))
		require.NoError(t, err)
		assert.Equal(t, mode, info.Mode().Perm(), dir)
	}
	info, err := os.Stat(filepath.Join(root, "workspace/deep/er/a.txt"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o640), info.Mode().Perm())

	// A symlink in a file's place is replaced, so the file it pointed at stays untouched
	data, err := os.ReadFile(filepath.Join(root, "elsewhere")) // #nosec G304 -- a file in the test's temp directory
	require.NoError(t, err)
	assert.Equal(t, "keep", string(data))
	data, err = os.ReadFile(filepath.Join(root, "workspace/link.txt")) // #nosec G304 -- a file in the test's temp directory
	require.NoError(t, err)
	assert.Equal(t, "replaced", string(data))
}

func TestPutFilesRejectsOtherEntries(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	require.NoError(t, tw.WriteHeader(&tar.Header{Typeflag: tar.TypeSymlink, Name: "workspace/x", Linkname: "/etc/passwd"}))
	require.NoError(t, tw.Close())
	assert.Error(t, putFiles(t.TempDir(), &buf))
}

func TestReadFileReportsMissingLargeAndDirectories(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	require.NoError(t, os.WriteFile(path, []byte("12345"), 0o600))

	var out bytes.Buffer
	require.NoError(t, readFile(&out, path, 5))
	assert.Equal(t, "12345", out.String())

	for want, err := range map[int]error{
		filesExitTooLarge:  readFile(io.Discard, path, 4),
		filesExitNotExist:  readFile(io.Discard, filepath.Join(dir, "missing"), 10),
		filesExitWrongKind: readFile(io.Discard, dir, 10),
	} {
		fe, ok := errors.AsType[*filesError](err)
		require.True(t, ok, "want exit code %d, got %v", want, err)
		assert.Equal(t, want, fe.code)
	}
}

func TestArchiveDirIsRelativeAndLimited(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "out", "sub"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "out", "a.txt"), []byte("a"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "out", "sub", "b.txt"), []byte(strings.Repeat("b", 5000)), 0o600))
	require.NoError(t, os.Symlink("a.txt", filepath.Join(dir, "out", "link")))

	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)
	require.NoError(t, archiveDir(w, filepath.Join(dir, "out"), 1<<20))
	require.NoError(t, w.Flush())
	names := map[string]byte{}
	tr := tar.NewReader(&buf)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		names[hdr.Name] = hdr.Typeflag
	}
	assert.Equal(t, map[string]byte{"a.txt": tar.TypeReg, "link": tar.TypeSymlink, "sub/": tar.TypeDir, "sub/b.txt": tar.TypeReg}, names)

	// Crossing the limit, a missing directory and a symlinked one each get their own exit code
	for want, err := range map[int]error{
		filesExitTooLarge:  archiveDir(io.Discard, filepath.Join(dir, "out"), 2000),
		filesExitNotExist:  archiveDir(io.Discard, filepath.Join(dir, "missing"), 10),
		filesExitWrongKind: archiveDir(io.Discard, filepath.Join(dir, "out", "link"), 10),
	} {
		fe, ok := errors.AsType[*filesError](err)
		require.True(t, ok, "want exit code %d, got %v", want, err)
		assert.Equal(t, want, fe.code)
	}
}

func TestFilesSwitchUserBeforeReading(t *testing.T) {
	// Switching to the current user changes nothing, while switching away needs root
	current, err := parseUser(strconv.Itoa(os.Geteuid()) + ":" + strconv.Itoa(os.Getegid()))
	require.NoError(t, err)
	require.NoError(t, switchUser(current))
	if os.Geteuid() != 0 {
		other := &syscall.Credential{Uid: current.Uid + 1, Gid: current.Gid}
		assert.Error(t, switchUser(other))
	}

	// A user that isn't numeric is a usage error, reported before anything is read
	assert.Equal(t, 2, run([]string{"files", "archive", "--max", "10", "--user", "agent", t.TempDir()}, io.Discard, io.Discard))
}

func TestReadEnvHeaderLeavesTheRestForTheCommand(t *testing.T) {
	r := bufio.NewReaderSize(strings.NewReader(`{"A":"1","LONG":"`+strings.Repeat("x", 10000)+`"}`+"\nrest of stdin"), 16)
	env, err := readEnvHeader(r)
	require.NoError(t, err)
	assert.Equal(t, "1", env["A"])
	assert.Len(t, env["LONG"], 10000)
	rest, err := io.ReadAll(r)
	require.NoError(t, err)
	assert.Equal(t, "rest of stdin", string(rest))

	_, err = readEnvHeader(bufio.NewReader(strings.NewReader("not json\n")))
	assert.Error(t, err)
}

func TestShimAppliesWorkDirAndEnv(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr syncBuffer
	code := shim(shimOptions{Args: []string{"sh", "-c", `pwd; echo "$FROM_HEADER"`}, WorkDir: dir, Env: map[string]string{"FROM_HEADER": "header-value"}}, strings.NewReader(""), &stdout, &stderr)
	require.Equal(t, 0, code, stderr.String())
	resolved, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	require.Len(t, lines, 2)
	gotDir, err := filepath.EvalSymlinks(lines[0])
	require.NoError(t, err)
	assert.Equal(t, resolved, gotDir)
	assert.Equal(t, "header-value", lines[1])
}

func TestParseUserAndHome(t *testing.T) {
	cred, err := parseUser("1000")
	require.NoError(t, err)
	assert.Equal(t, uint32(1000), cred.Uid)
	assert.Equal(t, uint32(1000), cred.Gid)
	assert.Empty(t, cred.Groups)
	cred, err = parseUser("1001:1002")
	require.NoError(t, err)
	assert.Equal(t, uint32(1002), cred.Gid)
	_, err = parseUser("agent")
	assert.Error(t, err)

	passwd := filepath.Join(t.TempDir(), "passwd")
	require.NoError(t, os.WriteFile(passwd, []byte("root:x:0:0:root:/root:/bin/sh\nagent:x:1000:1000::/home/agent:/bin/bash\n"), 0o600))
	old := passwdFile
	passwdFile = passwd
	t.Cleanup(func() { passwdFile = old })
	assert.Equal(t, "/home/agent", homeOf(1000))
	assert.Equal(t, "/", homeOf(1001))
}

func TestInstallLayoutCreatesTheLayoutAndBinary(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("the layout belongs to other users, which only root can set")
	}
	root := t.TempDir()
	old := layoutRoot
	layoutRoot = root
	t.Cleanup(func() { layoutRoot = old })

	require.NoError(t, installLayout(true))
	for path, mode := range map[string]os.FileMode{workspaceDir: os.ModeDir | 0o755, runDir: os.ModeDir | os.ModeSticky | 0o777, umpBinaryPath: 0o755} {
		info, err := os.Stat(filepath.Join(root, path))
		require.NoError(t, err)
		assert.Equal(t, mode, info.Mode(), path)
	}
	data, err := os.ReadFile(filepath.Join(root, sshConfigPath)) // #nosec G304 -- a file in the test's temp directory
	require.NoError(t, err)
	assert.Contains(t, string(data), "ProxyCommand /usr/local/bin/ump connect %h %p")
}
