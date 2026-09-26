//go:build unit

package runner

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/sandbox"
	sandboxfake "github.com/stonith404/umpteenth/backend/internal/sandbox/fake"
)

func TestInjectFilesSkipsToolkitNamesThatEscapeTheToolkitDir(t *testing.T) {
	adapter := sandboxfake.New()
	sb, err := adapter.Create(context.Background(), sandbox.Spec{Image: "img"})
	require.NoError(t, err)
	fakeSb := sb.(*sandboxfake.Sandbox)
	umpBefore, _, _, ok := fakeSb.File(sandbox.UmpBinary)
	require.True(t, ok)

	// A toolkit name that escapes /ump/toolkit must not be able to replace the ump CLI
	job := JobConfig{Toolkit: []ToolkitScript{
		{Name: "fetch.sh", Content: "#!/bin/sh\necho ok\n"},
		{Name: "../../usr/local/bin/ump", Content: "#!/bin/sh\necho pwned\n"},
	}}
	require.NoError(t, (&Runner{}).injectFiles(context.Background(), sb, Run{ID: "r1"}, job))

	content, mode, _, ok := fakeSb.File("/ump/toolkit/fetch.sh")
	require.True(t, ok)
	assert.Equal(t, "#!/bin/sh\necho ok\n", string(content))
	assert.EqualValues(t, 0o755, mode.Perm())

	umpAfter, _, _, ok := fakeSb.File(sandbox.UmpBinary)
	require.True(t, ok)
	assert.Equal(t, string(umpBefore), string(umpAfter))
}
