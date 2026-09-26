package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type filesystemStorage struct {
	root *os.Root
	path string
}

// NewFilesystemStorage stores blobs below rootPath, which must be shared between replicas in HA mode
func NewFilesystemStorage(rootPath string) (FileStorage, error) {
	err := os.MkdirAll(rootPath, 0o750)
	if err != nil {
		return nil, fmt.Errorf("failed to create storage directory: %w", err)
	}
	// os.Root confines every operation to the directory, so a crafted key cannot escape it
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open storage directory: %w", err)
	}
	return &filesystemStorage{root: root, path: rootPath}, nil
}

func (s *filesystemStorage) Type() string { return "filesystem" }

func (s *filesystemStorage) Save(_ context.Context, key string, data io.Reader) error {
	key = cleanKey(key)
	err := s.root.MkdirAll(filepath.Dir(key), 0o750)
	if err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	// Write to a temporary file and rename, so readers never see a partial blob
	tmp := key + ".tmp"
	f, err := s.root.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return fmt.Errorf("failed to create blob: %w", err)
	}
	_, err = io.Copy(f, data)
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		_ = s.root.Remove(tmp)
		return fmt.Errorf("failed to write blob: %w", errors.Join(err, closeErr))
	}
	return s.root.Rename(tmp, key)
}

func (s *filesystemStorage) Open(_ context.Context, key string) (io.ReadCloser, int64, error) {
	f, err := s.root.Open(cleanKey(key))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, 0, ErrNotExist
	} else if err != nil {
		return nil, 0, fmt.Errorf("failed to open blob: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, 0, fmt.Errorf("failed to stat blob: %w", err)
	}
	return f, info.Size(), nil
}

func (s *filesystemStorage) Delete(_ context.Context, key string) error {
	err := s.root.Remove(cleanKey(key))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("failed to delete blob: %w", err)
	}
	return nil
}

func (s *filesystemStorage) DeleteAll(_ context.Context, prefix string) error {
	err := s.root.RemoveAll(cleanKey(prefix))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("failed to delete blobs: %w", err)
	}
	return nil
}

func (s *filesystemStorage) List(_ context.Context, prefix string) ([]ObjectInfo, error) {
	prefix = cleanKey(prefix)
	var out []ObjectInfo
	err := fs.WalkDir(s.root.FS(), prefix, func(path string, d fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) {
			return fs.SkipAll
		}
		if err != nil || d.IsDir() || strings.HasSuffix(path, ".tmp") {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		out = append(out, ObjectInfo{Key: path, Size: info.Size(), ModTime: info.ModTime()})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list blobs: %w", err)
	}
	return out, nil
}

func (s *filesystemStorage) Close() error {
	return s.root.Close()
}

func cleanKey(key string) string {
	key = filepath.ToSlash(filepath.Clean("/" + key))
	return strings.TrimPrefix(key, "/")
}
