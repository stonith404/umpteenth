//go:build unit && exclude_ump

package umpbin

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBinaryBuildsStaticLinuxExecutable(t *testing.T) {
	data, err := Binary("arm64")
	require.NoError(t, err)
	assert.True(t, bytes.HasPrefix(data, []byte("\x7fELF")), "expected an ELF binary")

	// The second call is served from the cache
	again, err := Binary("arm64")
	require.NoError(t, err)
	assert.Equal(t, len(data), len(again))
}

func TestBinaryRejectsUnknownArch(t *testing.T) {
	_, err := Binary("riscv64")
	assert.Error(t, err)
}
