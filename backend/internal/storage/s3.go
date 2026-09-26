package storage

import (
	"context"
	"fmt"
	"io"
	"net/url"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3Config configures an S3-compatible bucket
type S3Config struct {
	Endpoint        string
	Region          string
	Bucket          string
	AccessKeyID     string
	SecretAccessKey string
	UseSSL          bool
}

type s3Storage struct {
	client *minio.Client
	bucket string
}

// NewS3Storage stores blobs in an S3-compatible bucket, which suits HA deployments
func NewS3Storage(ctx context.Context, cfg S3Config) (FileStorage, error) {
	endpoint := cfg.Endpoint
	useSSL := cfg.UseSSL
	// Accept either a bare host or a full URL for the endpoint
	if u, err := url.Parse(endpoint); err == nil && u.Host != "" {
		endpoint = u.Host
		useSSL = u.Scheme == "https"
	}
	if endpoint == "" {
		endpoint = "s3.amazonaws.com"
	}

	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		Secure: useSSL,
		Region: cfg.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create S3 client: %w", err)
	}

	exists, err := client.BucketExists(ctx, cfg.Bucket)
	if err != nil {
		return nil, fmt.Errorf("failed to check S3 bucket: %w", err)
	}
	if !exists {
		err = client.MakeBucket(ctx, cfg.Bucket, minio.MakeBucketOptions{Region: cfg.Region})
		if err != nil {
			return nil, fmt.Errorf("failed to create S3 bucket: %w", err)
		}
	}

	return &s3Storage{client: client, bucket: cfg.Bucket}, nil
}

// uploadPartSize bounds the buffer an upload of unknown size takes, which the client would otherwise size for a 5 TiB object at about 537 MiB per upload
const uploadPartSize = 16 << 20

func (s *s3Storage) Save(ctx context.Context, key string, data io.Reader) error {
	_, err := s.client.PutObject(ctx, s.bucket, cleanKey(key), data, -1, minio.PutObjectOptions{PartSize: uploadPartSize})
	if err != nil {
		return fmt.Errorf("failed to upload blob: %w", err)
	}
	return nil
}

func (s *s3Storage) Open(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	obj, err := s.client.GetObject(ctx, s.bucket, cleanKey(key), minio.GetObjectOptions{})
	if err != nil {
		return nil, 0, fmt.Errorf("failed to open blob: %w", err)
	}
	info, err := obj.Stat()
	if err != nil {
		_ = obj.Close()
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return nil, 0, ErrNotExist
		}
		return nil, 0, fmt.Errorf("failed to stat blob: %w", err)
	}
	return obj, info.Size, nil
}

func (s *s3Storage) Delete(ctx context.Context, key string) error {
	err := s.client.RemoveObject(ctx, s.bucket, cleanKey(key), minio.RemoveObjectOptions{})
	if err != nil && minio.ToErrorResponse(err).Code != "NoSuchKey" {
		return fmt.Errorf("failed to delete blob: %w", err)
	}
	return nil
}

func (s *s3Storage) DeleteAll(ctx context.Context, prefix string) error {
	// A listing error arrives as an object, which must end the delete instead of reaching the remover
	var listErr error
	objects := func(yield func(minio.ObjectInfo) bool) {
		for obj := range s.client.ListObjectsIter(ctx, s.bucket, minio.ListObjectsOptions{Prefix: dirPrefix(prefix), Recursive: true}) {
			if obj.Err != nil {
				listErr = obj.Err
				return
			}
			if !yield(obj) {
				return
			}
		}
	}
	results, err := s.client.RemoveObjectsWithIter(ctx, s.bucket, objects, minio.RemoveObjectsOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete blobs: %w", err)
	}

	// Every result is read, so one failed object doesn't stop the others from being deleted
	var firstErr error
	for res := range results {
		if res.Err != nil && firstErr == nil {
			firstErr = fmt.Errorf("failed to delete blob %s: %w", res.ObjectName, res.Err)
		}
	}
	if firstErr != nil {
		return firstErr
	}
	if listErr != nil {
		return fmt.Errorf("failed to list blobs to delete: %w", listErr)
	}
	return nil
}

func (s *s3Storage) List(ctx context.Context, prefix string) ([]ObjectInfo, error) {
	var out []ObjectInfo
	for obj := range s.client.ListObjectsIter(ctx, s.bucket, minio.ListObjectsOptions{Prefix: dirPrefix(prefix), Recursive: true}) {
		if obj.Err != nil {
			return nil, fmt.Errorf("failed to list blobs: %w", obj.Err)
		}
		out = append(out, ObjectInfo{Key: obj.Key, Size: obj.Size, ModTime: obj.LastModified})
	}
	return out, nil
}

func (s *s3Storage) Close() error { return nil }
