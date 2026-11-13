package sandbox

import (
	"fmt"
	"path"
	"regexp"
	"strings"
)

// MountDir holds the host folders a job may use, each under the name the job gave the mount
const MountDir = "/ump/folders"

// MaxMounts bounds the mounts of one job
const MaxMounts = 8

// FolderMode is how a folder may be used, where read-only is enforced by the operating system
type FolderMode string

const (
	ModeReadOnly  FolderMode = "ro"
	ModeReadWrite FolderMode = "rw"
)

// Folder is a base folder from the configuration that jobs may mount
type Folder struct {
	// Path is a host path with the docker adapter, or pvc_name/subpath with the kubernetes adapter
	Path string     `json:"path" doc:"A host path with the docker adapter, or pvc_name/subpath with the kubernetes adapter"`
	Mode FolderMode `json:"mode" enum:"ro,rw" doc:"Most a job may ask for: ro for read-only, rw for read-write too"`
}

// Mount is one folder a job may use, mounted at MountDir/Name
type Mount struct {
	Name string     `json:"name" pattern:"^[a-z0-9][a-z0-9-]{0,39}$" doc:"Name of the folder inside the sandbox, mounted at /ump/folders/<name>"`
	Path string     `json:"path" minLength:"1" maxLength:"1024" doc:"The base folder or a subfolder of it"`
	Mode FolderMode `json:"mode" enum:"ro,rw" doc:"ro for read-only, rw for read-write"`
}

// mountName becomes a directory name, so it stays within what every filesystem accepts
var mountName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

// CheckMounts rejects mounts that no configured folder covers, and rw access to a read-only folder
// Every sandbox creation runs it, so a folder the operator removed stops working at the next run instead of staying mounted
func CheckMounts(folders []Folder, mounts []Mount) error {
	if len(mounts) > MaxMounts {
		return fmt.Errorf("a job may mount at most %d folders", MaxMounts)
	}
	names := make(map[string]bool, len(mounts))
	paths := make(map[string]bool, len(mounts))
	for _, m := range mounts {
		if !mountName.MatchString(m.Name) {
			return fmt.Errorf("mount name %q must be lowercase letters, digits and dashes, at most 40 characters", m.Name)
		}
		if names[m.Name] {
			return fmt.Errorf("more than one mount is named %q", m.Name)
		}
		names[m.Name] = true

		// A cleaned path is the only form that matches a folder by prefix, so .. and doubled slashes are refused rather than resolved
		if m.Path == "" || m.Path == "/" || path.Clean(m.Path) != m.Path {
			return fmt.Errorf("mount %q path %q must be a clean path without . or ..", m.Name, m.Path)
		}
		if paths[m.Path] {
			return fmt.Errorf("more than one mount uses %q", m.Path)
		}
		paths[m.Path] = true

		switch m.Mode {
		case ModeReadOnly, ModeReadWrite:
		default:
			return fmt.Errorf("mount %q mode %q must be ro or rw", m.Name, m.Mode)
		}
		folder, ok := CoveringFolder(folders, m.Path)
		if !ok {
			return fmt.Errorf("mount %q path %q is not inside a configured folder", m.Name, m.Path)
		}
		if m.Mode == ModeReadWrite && folder.Mode != ModeReadWrite {
			return fmt.Errorf("mount %q may not be read-write, %s is configured read-only", m.Name, folder.Path)
		}
	}
	return nil
}

// CoveringFolder returns the most specific configured folder covering p, so a narrower folder can be read-only within a writable one
func CoveringFolder(folders []Folder, p string) (Folder, bool) {
	var best Folder
	found := false
	for _, f := range folders {
		// Matching whole segments keeps /var from covering /various
		if p != f.Path && !strings.HasPrefix(p, f.Path+"/") {
			continue
		}
		if !found || len(f.Path) > len(best.Path) {
			best, found = f, true
		}
	}
	return best, found
}

// CheckFolders validates the configured base folders, while each adapter checks the path syntax it can address
func CheckFolders(folders []Folder) error {
	seen := make(map[string]bool, len(folders))
	for _, f := range folders {
		if seen[f.Path] {
			return fmt.Errorf("folder %q is configured more than once", f.Path)
		}
		seen[f.Path] = true
		switch f.Mode {
		case ModeReadOnly, ModeReadWrite:
		default:
			return fmt.Errorf("folder %q mode %q must be ro or rw", f.Path, f.Mode)
		}
	}
	return nil
}
