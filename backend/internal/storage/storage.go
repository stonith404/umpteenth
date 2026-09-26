// Package storage stores blobs (full tool outputs, artifacts, build logs) on a pluggable backend, following Pocket ID's FileStorage
package storage

import (
	"context"
	"errors"
	"io"
	"path"
	"strings"
)

// ErrNotExist is returned when a key does not exist
var ErrNotExist = errors.New("blob does not exist")

// ObjectInfo describes one stored blob
type ObjectInfo struct {
	Key  string
	Size int64
}

// FileStorage is implemented by every blob backend
// Keys are slash-separated relative paths such as "runs/<id>/artifacts/report.csv"
// DeleteAll and List treat their prefix as a directory: "runs/a" covers "runs/a/x" but not "runs/ab/x", on every backend
type FileStorage interface {
	Save(ctx context.Context, key string, data io.Reader) error
	Open(ctx context.Context, key string) (io.ReadCloser, int64, error)
	DeleteAll(ctx context.Context, prefix string) error
	List(ctx context.Context, prefix string) ([]ObjectInfo, error)
	Close() error
}

// ReadAll is a convenience for small blobs
func ReadAll(ctx context.Context, s FileStorage, key string) ([]byte, error) {
	r, _, err := s.Open(ctx, key)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

// cleanKey turns a key into a clean relative path, so no key can climb out of the storage root
func cleanKey(key string) string {
	return strings.TrimPrefix(path.Clean("/"+key), "/")
}

// dirPrefix turns a directory-like prefix into the key prefix of everything below it, which is empty for the root
func dirPrefix(prefix string) string {
	if dir := cleanKey(prefix); dir != "" {
		return dir + "/"
	}
	return ""
}
