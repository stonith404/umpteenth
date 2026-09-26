// Package storage stores blobs (full tool outputs, artifacts, build logs) on a pluggable backend, following Pocket ID's FileStorage
package storage

import (
	"context"
	"errors"
	"io"
	"time"
)

// ErrNotExist is returned when a key does not exist
var ErrNotExist = errors.New("blob does not exist")

// ObjectInfo describes one stored blob
type ObjectInfo struct {
	Key     string
	Size    int64
	ModTime time.Time
}

// FileStorage is implemented by every blob backend
// Keys are slash-separated relative paths such as "runs/<id>/artifacts/report.csv"
type FileStorage interface {
	Type() string
	Save(ctx context.Context, key string, data io.Reader) error
	Open(ctx context.Context, key string) (io.ReadCloser, int64, error)
	Delete(ctx context.Context, key string) error
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
