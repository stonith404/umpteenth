package kubernetes

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/stonith404/umpteenth/backend/internal/sandbox"
)

// Exit codes of ump files, see cmd/ump/files.go
const (
	filesExitNotExist  = 3
	filesExitTooLarge  = 4
	filesExitWrongKind = 5
)

// maxFilesStderr caps what an ump files command may report
const maxFilesStderr = 16 << 10

// PutFiles writes files through `ump files put`, which creates missing parents owned by the file's owner and leaves existing ones unchanged
func (s *podSandbox) PutFiles(ctx context.Context, files []sandbox.File) error {
	if len(files) == 0 {
		return nil
	}

	// Build the archive in memory, validating every path before touching the sandbox
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	now := time.Now()
	for _, f := range files {
		p := path.Clean(f.Path)
		if !path.IsAbs(p) || p == "/" {
			return fmt.Errorf("invalid sandbox file path %q", f.Path)
		}
		uid := f.Owner.UID()
		mode := f.Mode
		if mode == 0 {
			mode = 0o644
		}
		hdr := &tar.Header{
			Typeflag: tar.TypeReg,
			Name:     strings.TrimPrefix(p, "/"),
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

	// ump runs as root, which the pod keeps just the capabilities for
	stderr := &cappedBuffer{max: maxFilesStderr}
	code, err := s.run(ctx, []string{bootstrapUmp, "files", "put"}, &buf, io.Discard, stderr)
	if err != nil {
		return s.translate(ctx, fmt.Errorf("failed to write files: %w", err))
	}
	if code != 0 {
		return fmt.Errorf("failed to write files: %s", stderr.message(code))
	}
	return nil
}

// ReadFile reads a regular file, following symlinks, and fails with ErrOutputTooLarge when it is larger than max bytes
func (s *podSandbox) ReadFile(ctx context.Context, p string, max int64) ([]byte, error) {
	if max <= 0 {
		return nil, errors.New("ReadFile needs a positive size limit")
	}
	p = path.Clean(p)
	if !path.IsAbs(p) {
		return nil, fmt.Errorf("invalid sandbox path %q", p)
	}

	out := &cappedBuffer{max: int(max) + 1}
	stderr := &cappedBuffer{max: maxFilesStderr}
	code, err := s.run(ctx, []string{bootstrapUmp, "files", "read", "--max", strconv.FormatInt(max, 10), p}, nil, out, stderr)
	if err != nil {
		return nil, s.translate(ctx, fmt.Errorf("failed to read %s: %w", p, err))
	}
	if err := filesError(p, code, stderr); err != nil {
		return nil, err
	}
	if int64(out.Len()) > max {
		return nil, fmt.Errorf("%w: %s is larger than %d bytes", sandbox.ErrOutputTooLarge, p, max)
	}
	return out.Bytes(), nil
}

// Archive streams a directory as a tar whose entry names are relative to that directory
// Reading fails with ErrOutputTooLarge once the archive grows beyond max bytes
func (s *podSandbox) Archive(ctx context.Context, dir string, max int64) (io.ReadCloser, error) {
	if max <= 0 {
		return nil, errors.New("Archive needs a positive size limit")
	}
	dir = path.Clean(dir)
	if !path.IsAbs(dir) {
		return nil, fmt.Errorf("invalid sandbox path %q", dir)
	}

	// The stream runs until the caller closes the reader, and ends with the error ump reported
	streamCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	pr, pw := io.Pipe()
	go func() {
		stderr := &cappedBuffer{max: maxFilesStderr}
		limited := &limitWriter{w: pw, max: max}
		code, err := s.run(streamCtx, []string{bootstrapUmp, "files", "archive", "--max", strconv.FormatInt(max, 10), dir}, nil, limited, stderr)
		switch {
		case limited.exceeded:
			err = fmt.Errorf("%w: archive is larger than %d bytes", sandbox.ErrOutputTooLarge, max)
		case err != nil:
			err = s.translate(ctx, fmt.Errorf("failed to archive %s: %w", dir, err))
		default:
			err = filesError(dir, code, stderr)
		}
		_ = pw.CloseWithError(err)
	}()

	// A missing directory has to fail the call itself, so the first byte or the end of the stream is awaited before returning
	br := bufio.NewReader(pr)
	if _, err := br.Peek(1); err != nil {
		cancel()
		_ = pr.Close()
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("archive of %s is empty", dir)
		}
		return nil, err
	}
	return &archiveReader{Reader: br, pr: pr, cancel: cancel}, nil
}

// archiveReader stops the stream when the caller closes it early
type archiveReader struct {
	*bufio.Reader
	pr     *io.PipeReader
	cancel context.CancelFunc
}

func (r *archiveReader) Close() error {
	r.cancel()
	return r.pr.Close()
}

// filesError maps the exit code of an ump files command to the errors of the sandbox interface
func filesError(p string, code int, stderr *cappedBuffer) error {
	switch code {
	case 0:
		return nil
	case filesExitNotExist:
		return fmt.Errorf("%s: %w", p, fs.ErrNotExist)
	case filesExitTooLarge:
		return fmt.Errorf("%w: %s", sandbox.ErrOutputTooLarge, stderr.message(code))
	case filesExitWrongKind:
		return errors.New(stderr.message(code))
	default:
		return fmt.Errorf("failed to read %s: %s", p, stderr.message(code))
	}
}

// cappedBuffer keeps at most max bytes and silently drops the rest, which callers detect by the length
type cappedBuffer struct {
	bytes.Buffer
	max int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if room := b.max - b.Len(); room > 0 {
		b.Buffer.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

// message is what a failed ump command reported, or its exit code when it said nothing
func (b *cappedBuffer) message(code int) string {
	msg := strings.TrimSpace(b.String())
	msg = strings.TrimPrefix(msg, "ump files: ")
	if msg == "" {
		return "ump exited with code " + strconv.Itoa(code)
	}
	return msg
}

// limitWriter stops accepting output once more than max bytes were written
type limitWriter struct {
	w        io.Writer
	n        int64
	max      int64
	exceeded bool
}

func (l *limitWriter) Write(b []byte) (int, error) {
	if l.n+int64(len(b)) > l.max {
		l.exceeded = true
		return 0, fmt.Errorf("%w: archive is larger than %d bytes", sandbox.ErrOutputTooLarge, l.max)
	}
	n, err := l.w.Write(b)
	l.n += int64(n)
	return n, err
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
