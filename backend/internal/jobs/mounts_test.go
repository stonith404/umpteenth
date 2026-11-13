//go:build unit

package jobs

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/principal"
	"github.com/stonith404/umpteenth/backend/internal/sandbox"
)

func TestCheckMountPermissionKeepsFoldersWithInstanceAdmins(t *testing.T) {
	mounts := []sandbox.Mount{{Name: "www", Path: "/var/www", Mode: sandbox.ModeReadOnly}}

	// A member, and an unauthenticated caller, may not grant a folder
	require.Error(t, checkMountPermission(t.Context(), mounts))
	member := principal.WithPrincipal(t.Context(), principal.Principal{Role: principal.RoleOwner})
	require.Error(t, checkMountPermission(member, mounts))

	// An instance admin may, while an empty list needs no permission at all, so members keep saving jobs without folders
	admin := principal.WithPrincipal(t.Context(), principal.Principal{InstanceAdmin: true})
	require.NoError(t, checkMountPermission(admin, mounts))
	require.NoError(t, checkMountPermission(member, nil))
}

func TestCheckMountsFollowsTheAdapterFolders(t *testing.T) {
	info := sandbox.Info{Folders: []sandbox.Folder{
		{Path: "/var/www", Mode: sandbox.ModeReadWrite},
		{Path: "/srv/audit", Mode: sandbox.ModeReadOnly},
	}}
	m := &Module{deps: Dependencies{SandboxInfo: func(context.Context) (sandbox.Info, error) { return info, nil }}}

	require.NoError(t, m.checkMounts(t.Context(), []sandbox.Mount{{Name: "www", Path: "/var/www/site", Mode: sandbox.ModeReadWrite}}))

	// A folder the operator didn't configure, and a mode its base doesn't allow, are refused where the editor sees them
	err := m.checkMounts(t.Context(), []sandbox.Mount{{Name: "secrets", Path: "/etc", Mode: sandbox.ModeReadOnly}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not inside a configured folder")

	err = m.checkMounts(t.Context(), []sandbox.Mount{{Name: "audit", Path: "/srv/audit", Mode: sandbox.ModeReadWrite}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "configured read-only")

	// An adapter that can't be reached doesn't block saving
	m.deps.SandboxInfo = func(context.Context) (sandbox.Info, error) { return sandbox.Info{}, errors.New("engine down") }
	assert.NoError(t, m.checkMounts(t.Context(), []sandbox.Mount{{Name: "www", Path: "/var/www", Mode: sandbox.ModeReadOnly}}))
}
