//go:build !exclude_ump

package umpbin

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
)

// binFS embeds the whole directory, including .gitkeep, so the pattern matches even before `make ump` ran
// A missing binary is then reported by Binary at runtime, and the adapter's Check turns it into a startup error
//
//go:embed all:bin
var binFS embed.FS

func binary(arch string) ([]byte, error) {
	data, err := binFS.ReadFile("bin/ump-linux-" + arch)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("the ump binary for linux/%s is not embedded: run `make ump` before building, or build with -tags exclude_ump", arch)
	}
	return data, err
}
