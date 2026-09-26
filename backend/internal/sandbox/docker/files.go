package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"

	"github.com/stonith404/umpteenth/backend/internal/sandbox"
)

// maxSymlinkHops bounds symlink resolution in ReadFile and Archive
const maxSymlinkHops = 8

// PutFiles writes files through the engine's archive API, which works whether Umpteenth runs on the host or in a container
// Missing parent directories are created owned by the file's owner, while existing ones keep their mode and owner
func (s *containerSandbox) PutFiles(ctx context.Context, files []sandbox.File) error {
	if len(files) == 0 {
		return nil
	}

	// Validate every path before touching the sandbox
	paths := make([]string, len(files))
	for i, f := range files {
		p := path.Clean(f.Path)
		if !path.IsAbs(p) || p == "/" {
			return fmt.Errorf("invalid sandbox file path %q", f.Path)
		}
		paths[i] = p
	}

	// Find the parent directories that do not exist yet; once one is missing, everything below it is too
	var newDirs []tar.Header
	exists := map[string]bool{"/": true}
	now := time.Now()
	for i, f := range files {
		missing := false
		for _, dir := range ancestors(paths[i]) {
			if known, ok := exists[dir]; ok {
				missing = !known
				continue
			}
			if !missing {
				found, err := s.pathExists(ctx, dir)
				if err != nil {
					return err
				}
				missing = !found
			}
			exists[dir] = !missing
			if missing {
				uid := f.Owner.UID()
				newDirs = append(newDirs, tar.Header{Typeflag: tar.TypeDir, Name: strings.TrimPrefix(dir, "/") + "/", Mode: 0o755, Uid: uid, Gid: uid, ModTime: now})
			}
		}
	}

	// Build the archive in memory, directories first so their owners are set before the files land in them
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for i := range newDirs {
		if err := tw.WriteHeader(&newDirs[i]); err != nil {
			return err
		}
	}
	for i, f := range files {
		uid := f.Owner.UID()
		mode := f.Mode
		if mode == 0 {
			mode = 0o644
		}
		hdr := &tar.Header{
			Typeflag: tar.TypeReg,
			Name:     strings.TrimPrefix(paths[i], "/"),
			Mode:     tarMode(mode),
			Uid:      uid,
			Gid:      uid,
			Size:     int64(len(f.Content)),
			ModTime:  now,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if _, err := tw.Write(f.Content); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}

	// Without copyUIDGID the engine keeps the owners from the tar headers
	err := s.a.cli.CopyToContainer(ctx, s.id, "/", &buf, container.CopyToContainerOptions{})
	if err != nil {
		return s.translate(ctx, fmt.Errorf("failed to write files: %w", err))
	}
	return nil
}

// ReadFile reads a regular file, failing with ErrOutputTooLarge when it is larger than max bytes
func (s *containerSandbox) ReadFile(ctx context.Context, p string, max int64) ([]byte, error) {
	if max <= 0 {
		return nil, errors.New("ReadFile needs a positive size limit")
	}
	return s.readFile(ctx, p, max)
}

// readFile reads a file whose size is known to be bounded, following symlinks
func (s *containerSandbox) readFile(ctx context.Context, p string, max int64) ([]byte, error) {
	rc, stat, err := s.open(ctx, p)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	if stat.Mode.IsDir() {
		return nil, fmt.Errorf("%s is a directory", p)
	}
	if stat.Size > max {
		return nil, fmt.Errorf("%w: %s is %d bytes, the limit is %d", sandbox.ErrOutputTooLarge, p, stat.Size, max)
	}

	// The engine wraps the file in a tar with a single entry
	tr := tar.NewReader(rc)
	if _, err := tr.Next(); err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", p, err)
	}
	data, err := io.ReadAll(io.LimitReader(tr, max+1))
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", p, err)
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("%w: %s is larger than %d bytes", sandbox.ErrOutputTooLarge, p, max)
	}
	return data, nil
}

// Archive streams a directory as a tar whose entry names are relative to that directory
// Reading fails with ErrOutputTooLarge once the archive grows beyond max bytes
func (s *containerSandbox) Archive(ctx context.Context, dir string, max int64) (io.ReadCloser, error) {
	if max <= 0 {
		return nil, errors.New("Archive needs a positive size limit")
	}
	// A directory that is a symlink isn't followed, since the engine reads as root and the link could lead to files the agent may not read, e.g. /ump/outputs pointing at another user's home
	dir = path.Clean(dir)
	if !path.IsAbs(dir) {
		return nil, fmt.Errorf("invalid sandbox path %q", dir)
	}
	rc, stat, err := s.a.cli.CopyFromContainer(ctx, s.id, dir)
	if err != nil {
		return nil, s.translatePath(ctx, dir, err)
	}
	if !stat.Mode.IsDir() {
		_ = rc.Close()
		return nil, fmt.Errorf("%s is not a directory", dir)
	}

	// Re-root the engine's archive, whose entries start with the directory's base name, while counting the bytes produced
	pr, pw := io.Pipe()
	go func() {
		err := rerootArchive(&limitWriter{w: pw, max: max}, rc, stat.Name)
		_ = rc.Close()
		_ = pw.CloseWithError(err)
	}()
	return pr, nil
}

// open fetches a path from the engine, following symlinks the way a process in the sandbox would
func (s *containerSandbox) open(ctx context.Context, p string) (io.ReadCloser, container.PathStat, error) {
	p = path.Clean(p)
	if !path.IsAbs(p) {
		return nil, container.PathStat{}, fmt.Errorf("invalid sandbox path %q", p)
	}
	for range maxSymlinkHops {
		rc, stat, err := s.a.cli.CopyFromContainer(ctx, s.id, p)
		if err != nil {
			return nil, stat, s.translatePath(ctx, p, err)
		}
		if stat.Mode&os.ModeSymlink == 0 {
			return rc, stat, nil
		}
		_ = rc.Close()

		// The engine reports the link target resolved inside the container
		target := stat.LinkTarget
		if !path.IsAbs(target) {
			target = path.Join(path.Dir(p), target)
		}
		p = target
	}
	return nil, container.PathStat{}, fmt.Errorf("too many levels of symbolic links: %s", p)
}

// pathExists reports whether a path exists in the sandbox
func (s *containerSandbox) pathExists(ctx context.Context, p string) (bool, error) {
	_, err := s.a.cli.ContainerStatPath(ctx, s.id, p)
	if err == nil {
		return true, nil
	}
	err = s.translatePath(ctx, p, err)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return false, err
}

// translatePath tells a missing path apart from a missing container, which the archive API both report as not found
func (s *containerSandbox) translatePath(ctx context.Context, p string, err error) error {
	if !isNotFound(err) {
		return s.translate(ctx, err)
	}
	if _, stateErr := s.detachedState(ctx); errors.Is(stateErr, sandbox.ErrSandboxGone) {
		return fmt.Errorf("%w: %w", sandbox.ErrSandboxGone, err)
	}
	return fmt.Errorf("%s: %w", p, fs.ErrNotExist)
}

// rerootArchive copies a tar, dropping the root entry and the base-name prefix of every other entry
func rerootArchive(w io.Writer, r io.Reader, base string) error {
	tr := tar.NewReader(r)
	tw := tar.NewWriter(w)
	prefix := strings.TrimSuffix(base, "/") + "/"
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		name, ok := strings.CutPrefix(hdr.Name, prefix)
		if !ok || name == "" {
			continue
		}
		hdr.Name = name
		// Hard links point at other entries, so their targets move with them
		if hdr.Typeflag == tar.TypeLink {
			hdr.Linkname = strings.TrimPrefix(hdr.Linkname, prefix)
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		// #nosec G110 -- the archive stream is already bounded by the caller's size limit
		if _, err := io.Copy(tw, tr); err != nil {
			return err
		}
	}
	return tw.Close()
}

// limitWriter fails with ErrOutputTooLarge once more than max bytes were written
type limitWriter struct {
	w   io.Writer
	n   int64
	max int64
}

func (l *limitWriter) Write(b []byte) (int, error) {
	if l.n+int64(len(b)) > l.max {
		return 0, fmt.Errorf("%w: archive is larger than %d bytes", sandbox.ErrOutputTooLarge, l.max)
	}
	n, err := l.w.Write(b)
	l.n += int64(n)
	return n, err
}

// ancestors returns the directories above an absolute path, outermost first and without the root
func ancestors(p string) []string {
	var dirs []string
	for dir := path.Dir(p); dir != "/"; dir = path.Dir(dir) {
		dirs = append(dirs, dir)
	}
	slices.Reverse(dirs)
	return dirs
}

// tarMode converts a Go file mode into the Unix mode bits a tar header carries
func tarMode(m fs.FileMode) int64 {
	mode := int64(m.Perm())
	if m&fs.ModeSetuid != 0 {
		mode |= 0o4000
	}
	if m&fs.ModeSetgid != 0 {
		mode |= 0o2000
	}
	if m&fs.ModeSticky != 0 {
		mode |= 0o1000
	}
	return mode
}
