package main

import (
	"archive/tar"
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"syscall"
)

// Exit codes of ump files, which the adapter maps back to the errors of the sandbox interface
const (
	filesExitNotExist  = 3
	filesExitTooLarge  = 4
	filesExitWrongKind = 5
)

func init() {
	register("files", command{
		Summary:  "Write, read and archive files for sandbox adapters without a file API of their own",
		Internal: true,
		Run:      runFiles,
	})
}

// filesError carries the exit code the adapter reads the failure from
type filesError struct {
	code int
	err  error
}

func (e *filesError) Error() string { return e.err.Error() }

// runFiles dispatches ump files put, read and archive, which the Kubernetes adapter runs as root because its API can only exec
func runFiles(args []string) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(os.Stderr, "Usage: ump files put | read --max bytes [--user uid[:gid]] <path> | archive --max bytes [--user uid[:gid]] <dir>")
		return 2
	}
	var err error
	switch args[0] {
	case "put":
		err = putFiles(layoutRoot, os.Stdin)
	case "read", "archive":
		flags := flag.NewFlagSet("ump files "+args[0], flag.ContinueOnError)
		limit := flags.Int64("max", 0, "fail once more than this many bytes were produced")
		user := flags.String("user", "", "read as this uid[:gid] instead of root")
		if flags.Parse(args[1:]) != nil || flags.NArg() != 1 || *limit <= 0 {
			_, _ = fmt.Fprintf(os.Stderr, "Usage: ump files %s --max bytes [--user uid[:gid]] <path>\n", args[0])
			return 2
		}

		// Switching users before touching the path makes the kernel check every step of the read, which no check of ours could do without a race
		if *user != "" {
			cred, err := parseUser(*user)
			if err != nil {
				errorf("files", "%v", err)
				return 2
			}
			if err := switchUser(cred); err != nil {
				errorf("files", "%v", err)
				return 1
			}
		}
		out := bufio.NewWriter(os.Stdout)
		if args[0] == "read" {
			err = readFile(out, filepath.Join(layoutRoot, flags.Arg(0)), *limit)
		} else {
			err = archiveDir(out, filepath.Join(layoutRoot, flags.Arg(0)), *limit)
		}
		err = errors.Join(err, out.Flush())
	default:
		errorf("files", "unknown subcommand %q", args[0])
		return 2
	}
	if err == nil {
		return 0
	}
	errorf("files", "%v", err)
	if fe, ok := errors.AsType[*filesError](err); ok {
		return fe.code
	}
	return 1
}

// putFiles writes the regular files of a tar with the mode and owner of their headers
// Missing parent directories are created owned by the file's owner, while existing ones keep their mode and owner, as PutFiles promises
func putFiles(root string, r io.Reader) error {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg {
			return fmt.Errorf("%s: only regular files can be written", hdr.Name)
		}
		clean := path.Clean("/" + hdr.Name)
		if clean == "/" {
			return fmt.Errorf("invalid file path %q", hdr.Name)
		}
		target := filepath.Join(root, clean)

		// Create missing parents outermost first, so each new one belongs to the file's owner
		if err := mkdirParents(root, clean, hdr.Uid, hdr.Gid); err != nil {
			return err
		}

		// A symlink in the file's place is replaced rather than followed, like extracting an archive does
		if info, err := os.Lstat(target); err == nil && info.Mode()&fs.ModeSymlink != 0 {
			if err := os.Remove(target); err != nil {
				return err
			}
		}

		// Write the content, then the owner, then the mode, since changing the owner clears setuid bits
		f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) // #nosec G304 -- writing the adapter's files is the purpose of this command
		if err != nil {
			return err
		}
		// #nosec G110 -- the adapter sends files it built itself
		_, copyErr := io.Copy(f, tr)
		if err := errors.Join(copyErr, f.Close()); err != nil {
			return fmt.Errorf("failed to write %s: %w", clean, err)
		}
		if err := os.Chown(target, hdr.Uid, hdr.Gid); err != nil {
			return err
		}
		if err := os.Chmod(target, hdr.FileInfo().Mode()); err != nil {
			return err
		}
	}
}

// mkdirParents creates the missing directories above an absolute path with the given owner
func mkdirParents(root, p string, uid, gid int) error {
	var missing []string
	for dir := path.Dir(p); dir != "/"; dir = path.Dir(dir) {
		if _, err := os.Stat(filepath.Join(root, dir)); err == nil {
			break
		}
		missing = append(missing, dir)
	}
	for i := len(missing) - 1; i >= 0; i-- {
		dir := filepath.Join(root, missing[i])
		if err := os.Mkdir(dir, 0o755); err != nil && !errors.Is(err, fs.ErrExist) { // #nosec G301 -- new parents get the mode the Docker adapter gives them
			return err
		}
		if err := os.Chown(dir, uid, gid); err != nil {
			return err
		}
		if err := os.Chmod(dir, 0o755); err != nil { // #nosec G302 -- the same mode the Docker adapter gives new parents
			return err
		}
	}
	return nil
}

// readFile copies a file, following symlinks, and fails when it is larger than limit bytes
func readFile(w io.Writer, p string, limit int64) error {
	info, err := os.Stat(p) // #nosec G703 -- reading the adapter's path is the purpose of this command
	if errors.Is(err, fs.ErrNotExist) {
		return &filesError{filesExitNotExist, err}
	}
	if err != nil {
		return err
	}
	if info.IsDir() {
		return &filesError{filesExitWrongKind, fmt.Errorf("%s is a directory", p)}
	}
	if info.Size() > limit {
		return &filesError{filesExitTooLarge, fmt.Errorf("%s is %d bytes, the limit is %d", p, info.Size(), limit)}
	}

	f, err := os.Open(p) // #nosec G304 G703 -- reading the adapter's path is the purpose of this command
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	n, err := io.Copy(w, io.LimitReader(f, limit+1))
	if err != nil {
		return err
	}
	if n > limit {
		return &filesError{filesExitTooLarge, fmt.Errorf("%s is larger than %d bytes", p, limit)}
	}
	return nil
}

// archiveDir writes a tar of a directory with entry names relative to it, and fails once more than limit bytes were produced
// A directory that is a symlink isn't followed, since ump reads as root and the link could lead to files the agent may not read
func archiveDir(w io.Writer, dir string, limit int64) error {
	info, err := os.Lstat(dir) // #nosec G703 -- archiving the adapter's directory is the purpose of this command
	if errors.Is(err, fs.ErrNotExist) {
		return &filesError{filesExitNotExist, err}
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return &filesError{filesExitWrongKind, fmt.Errorf("%s is not a directory", dir)}
	}

	tw := tar.NewWriter(&limitedWriter{w: w, limit: limit})
	// #nosec G703 -- archiving the adapter's directory is the purpose of this command
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == dir {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}

		// Only files, directories and symlinks travel, since nothing else is an output
		var link string
		switch {
		case info.Mode().IsRegular(), info.IsDir():
		case info.Mode()&fs.ModeSymlink != 0:
			if link, err = os.Readlink(p); err != nil {
				return err
			}
		default:
			return nil
		}
		hdr, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		hdr.Name = filepath.ToSlash(rel)
		if info.IsDir() {
			hdr.Name += "/"
		}
		if st, ok := info.Sys().(*syscall.Stat_t); ok {
			hdr.Uid, hdr.Gid = int(st.Uid), int(st.Gid)
		}
		hdr.Uname, hdr.Gname = "", ""
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		f, err := os.Open(p) // #nosec G304 G122 -- the adapter archives as the agent, so a symlink swapped in mid-walk reaches nothing the agent couldn't read itself
		if err != nil {
			return err
		}
		_, err = io.Copy(tw, f)
		return errors.Join(err, f.Close())
	})
	if err != nil {
		return err
	}
	return tw.Close()
}

// switchUser makes the process run as another user for good, which only root can do
func switchUser(cred *syscall.Credential) error {
	if int(cred.Uid) == os.Geteuid() && int(cred.Gid) == os.Getegid() {
		return nil
	}

	// The groups go first and the uid last, since giving up root's uid takes away the right to change the others
	if err := syscall.Setgroups([]int{}); err != nil {
		return fmt.Errorf("failed to drop the supplementary groups: %w", err)
	}
	if err := syscall.Setgid(int(cred.Gid)); err != nil {
		return fmt.Errorf("failed to switch to gid %d: %w", cred.Gid, err)
	}
	if err := syscall.Setuid(int(cred.Uid)); err != nil {
		return fmt.Errorf("failed to switch to uid %d: %w", cred.Uid, err)
	}
	return nil
}

// limitedWriter fails with the too-large exit code once more than limit bytes were written
type limitedWriter struct {
	w     io.Writer
	n     int64
	limit int64
}

func (l *limitedWriter) Write(b []byte) (int, error) {
	if l.n+int64(len(b)) > l.limit {
		return 0, &filesError{filesExitTooLarge, fmt.Errorf("the archive is larger than %d bytes", l.limit)}
	}
	n, err := l.w.Write(b)
	l.n += int64(n)
	return n, err
}
