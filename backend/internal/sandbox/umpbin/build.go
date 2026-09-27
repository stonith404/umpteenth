//go:build exclude_ump

package umpbin

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// Binaries compiled in this process, keyed by architecture
var (
	buildMu sync.Mutex
	built   = map[string][]byte{}
)

// binary cross-compiles ./cmd/ump with the local Go toolchain the first time an architecture is requested
// This keeps `go run` and `go test` working without `make ump`, at the cost of needing the module source at runtime
func binary(arch string) ([]byte, error) {
	buildMu.Lock()
	defer buildMu.Unlock()
	if data, ok := built[arch]; ok {
		return data, nil
	}

	root, err := moduleRoot()
	if err != nil {
		return nil, err
	}

	// Build into a temporary directory, since only the bytes are kept
	dir, err := os.MkdirTemp("", "umpteenth-umpbin-")
	if err != nil {
		return nil, fmt.Errorf("failed to create a build directory for ump: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	out := filepath.Join(dir, "ump-linux-"+arch)

	// #nosec G204 -- a development-only build of this repository's own ump CLI
	cmd := exec.Command("go", "build", "-trimpath", "-ldflags", "-s -w", "-o", out, "./cmd/ump")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+arch, "CGO_ENABLED=0")
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("failed to build ump for linux/%s: %w: %s", arch, err, strings.TrimSpace(output.String()))
	}

	data, err := os.ReadFile(out) // #nosec G304 -- the path is inside the temporary build directory
	if err != nil {
		return nil, fmt.Errorf("failed to read the ump binary: %w", err)
	}
	built[arch] = data
	return data, nil
}

// moduleRoot locates the backend module, which contains ./cmd/ump
func moduleRoot() (string, error) {
	// The path of this source file is exact unless the server itself was built with -trimpath
	if _, file, _, ok := runtime.Caller(0); ok && filepath.IsAbs(file) {
		root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
		if isUmpModule(root) {
			return root, nil
		}
	}

	// Otherwise ask the toolchain for the module enclosing the working directory
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err == nil {
		gomod := strings.TrimSpace(string(out))
		if gomod != "" && gomod != os.DevNull {
			if root := filepath.Dir(gomod); isUmpModule(root) {
				return root, nil
			}
		}
	}
	return "", errors.New("cannot find the backend module to build ump from: run from inside backend/ or build with `make ump` and without -tags exclude_ump")
}

// isUmpModule reports whether dir contains the ump command's source
func isUmpModule(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "cmd", "ump", "main.go"))
	return err == nil
}
