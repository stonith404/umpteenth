//go:build unit

package sandbox

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCheckMountsMatchesTheMostSpecificFolder(t *testing.T) {
	folders := []Folder{
		{Path: "/var", Mode: ModeReadWrite},
		{Path: "/var/www", Mode: ModeReadOnly},
		{Path: "backups/db", Mode: ModeReadWrite},
	}

	ok := []Mount{
		{Name: "logs", Path: "/var/log", Mode: ModeReadWrite},
		{Name: "www", Path: "/var/www", Mode: ModeReadOnly},
		{Name: "site", Path: "/var/www/site/assets", Mode: ModeReadOnly},
		{Name: "dumps", Path: "backups/db/dumps", Mode: ModeReadWrite},
	}
	for _, m := range ok {
		require.NoError(t, CheckMounts(folders, []Mount{m}), m.Name)
	}

	// The narrower read-only folder wins over the writable one around it
	require.ErrorContains(t, CheckMounts(folders, []Mount{{Name: "www", Path: "/var/www/site", Mode: ModeReadWrite}}), "configured read-only")

	// Matching whole segments and staying inside a folder, so lookalikes and siblings are refused
	for _, path := range []string{"/various", "/var/www/../etc", "/var/www/", "/var//log", "backups", "backups/dbx", "/etc"} {
		require.Error(t, CheckMounts(folders, []Mount{{Name: "x", Path: path, Mode: ModeReadOnly}}), path)
	}
}

func TestCheckMountsRejectsDuplicateNamesAndPaths(t *testing.T) {
	folders := []Folder{{Path: "/srv", Mode: ModeReadWrite}}

	require.ErrorContains(t, CheckMounts(folders, []Mount{
		{Name: "a", Path: "/srv/one", Mode: ModeReadOnly},
		{Name: "a", Path: "/srv/two", Mode: ModeReadOnly},
	}), "named")

	require.ErrorContains(t, CheckMounts(folders, []Mount{
		{Name: "a", Path: "/srv/one", Mode: ModeReadOnly},
		{Name: "b", Path: "/srv/one", Mode: ModeReadOnly},
	}), "uses")

	// A name becomes a directory, so only what every filesystem accepts is allowed
	require.ErrorContains(t, CheckMounts(folders, []Mount{{Name: "Not A Name", Path: "/srv/one", Mode: ModeReadOnly}}), "name")
}

func TestCheckFoldersRejectsUnknownModesAndDuplicates(t *testing.T) {
	require.ErrorContains(t, CheckFolders([]Folder{{Path: "/srv", Mode: "write"}}), "must be ro or rw")
	require.ErrorContains(t, CheckFolders([]Folder{{Path: "/srv", Mode: ModeReadOnly}, {Path: "/srv", Mode: ModeReadWrite}}), "more than once")
	require.NoError(t, CheckFolders([]Folder{{Path: "/srv", Mode: ModeReadOnly}, {Path: "backups/db", Mode: ModeReadWrite}}))
}
