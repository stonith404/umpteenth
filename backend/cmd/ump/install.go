package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Paths and ids of the sandbox layout, which must match internal/sandbox; ump keeps its own copy since it stays on the standard library
const (
	umpBinaryPath = "/usr/local/bin/ump"
	workspaceDir  = "/workspace"
	umpDir        = "/ump"
	// runDir holds the exec shim's pid files; it lives outside /ump so the agent user cannot swap it out
	runDir        = "/run/ump"
	agentUID      = 1000
	sshConfigPath = "/etc/ssh/ssh_config.d/ump.conf"
	sshConfig     = "# Written by Umpteenth: SSH connections leave the sandbox through the egress proxy\nHost *\n  ProxyCommand " + umpBinaryPath + " connect %h %p\n"
)

// layoutRoot prefixes every layout path; tests point it at a temporary directory
var layoutRoot = "/"

func init() {
	register("install", command{
		Summary:  "Copy the ump binary to a path, e.g. into a volume a sandbox shares with its init container",
		Internal: true,
		Run:      runInstall,
	})
}

// runInstall copies the running binary, which lets an init container hand ump to a sandbox whose image doesn't have it
func runInstall(args []string) int {
	if len(args) != 1 {
		_, _ = fmt.Fprintln(os.Stderr, "Usage: ump install <path>")
		return 2
	}
	if err := copySelf(args[0]); err != nil {
		errorf("install", "%v", err)
		return 1
	}
	return 0
}

// installLayout creates what the Docker adapter copies into a sandbox before it starts: the ump CLI, the agent's directories and the shim's run directory
// Adapters that can only start a sandbox from its image, such as Kubernetes, have ump init do it instead, before any command can run
func installLayout(proxied bool) error {
	// The directories come first, and existing ones get the owner and mode the adapters promise too
	dirs := []struct {
		path string
		mode os.FileMode
		uid  int
	}{
		{workspaceDir, 0o755, agentUID},
		{umpDir, 0o755, agentUID},
		// World-writable and sticky like /tmp, since every user's shim writes its pid files here
		{runDir, 0o777 | os.ModeSticky, 0},
	}
	for _, dir := range dirs {
		path := filepath.Join(layoutRoot, dir.path)
		if err := os.MkdirAll(path, 0o755); err != nil { // #nosec G301 -- the layout directories are world-readable like in every sandbox image
			return err
		}
		if err := os.Lchown(path, dir.uid, dir.uid); err != nil {
			return err
		}
		if err := os.Chmod(path, dir.mode); err != nil {
			return err
		}
	}

	// Proxied sandboxes send SSH through the egress proxy, so git over SSH works without a route of their own
	if proxied {
		path := filepath.Join(layoutRoot, sshConfigPath)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { // #nosec G301 -- every user's ssh reads the directory
			return err
		}
		if err := os.WriteFile(path, []byte(sshConfig), 0o644); err != nil { // #nosec G306 -- every user's ssh reads it
			return err
		}
	}

	// The binary comes last, so the adapter can take its presence as the sign the whole layout is in place
	return copySelf(filepath.Join(layoutRoot, umpBinaryPath))
}

// copySelf writes the running binary to path through a temporary file, so nobody ever runs a half-written copy
func copySelf(path string) error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to locate the ump binary: %w", err)
	}
	src, err := os.Open(self) // #nosec G304 -- the running executable
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { // #nosec G301 G703 -- the directory of an executable every user runs
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".ump-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // #nosec G703 -- the temporary file this function created
	_, copyErr := io.Copy(tmp, src)
	closeErr := tmp.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return fmt.Errorf("failed to copy ump to %s: %w", path, err)
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil { // #nosec G302 G703 -- an executable every user runs
		return err
	}
	return os.Rename(tmp.Name(), path) // #nosec G703 -- the adapter names where ump goes
}
