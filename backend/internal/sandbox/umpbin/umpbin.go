// Package umpbin provides the ump CLI that the sandbox adapters inject into every sandbox
// Release builds embed binaries produced by `make ump`; builds tagged exclude_ump compile them on first use instead
package umpbin

import "fmt"

// Binary returns the static linux ump binary for a sandbox architecture, "amd64" or "arm64"
func Binary(arch string) ([]byte, error) {
	switch arch {
	case "amd64", "arm64":
		return binary(arch)
	default:
		return nil, fmt.Errorf("no ump binary for sandbox architecture %q", arch)
	}
}
