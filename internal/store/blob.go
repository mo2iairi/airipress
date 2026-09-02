package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type BlobStore interface {
	Put(ctx context.Context, key, contentType string, data []byte) error
	Open(ctx context.Context, key string) (io.ReadCloser, error)
}

func ObjectKey(digest string) string {
	return "objects/sha256/" + digest[:2] + "/" + digest + "/content"
}

type LocalBlobStore struct{ Root string }

func (l *LocalBlobStore) path(key string) (string, error) {
	root, err := filepath.Abs(l.Root)
	if err != nil {
		return "", err
	}
	p, err := filepath.Abs(filepath.Join(root, filepath.FromSlash(key)))
	if err != nil || (p != root && !strings.HasPrefix(p, root+string(os.PathSeparator))) {
		return "", fmt.Errorf("invalid object key")
	}
	return p, nil
}

func (l *LocalBlobStore) Put(_ context.Context, key, contentType string, data []byte) error {
	p, err := l.path(filepath.Join(".meta", key))
	if err != nil {
		return err
	}
	if _, err = os.Stat(p); err == nil {
		return nil
	}
	if err = os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".upload-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err = tmp.Write(data); err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(tmpName, p); err != nil {
		return err
	}
	manifest, _ := json.Marshal(map[string]any{"object_key": key, "size": len(data), "content_type": contentType})
	return os.WriteFile(filepath.Join(filepath.Dir(p), "manifest.json"), manifest, 0o640)
}

func (l *LocalBlobStore) Open(_ context.Context, key string) (io.ReadCloser, error) {
	p, err := l.path(filepath.Join(".meta", key))
	if err != nil {
		return nil, err
	}
	return os.Open(p)
}

type S3BlobStore struct {
	Client *minio.Client
	Bucket string
	Prefix string
}

func NewS3BlobStore(endpoint, bucket, region, accessKey, secretKey string, secure bool) (*S3BlobStore, error) {
	endpoint = strings.TrimPrefix(strings.TrimPrefix(endpoint, "https://"), "http://")
	client, err := minio.New(endpoint, &minio.Options{Creds: credentials.NewStaticV4(accessKey, secretKey, ""), Secure: secure, Region: region})
	if err != nil {
		return nil, err
	}
	return &S3BlobStore{Client: client, Bucket: bucket, Prefix: ".meta/"}, nil
}

func (s *S3BlobStore) Put(ctx context.Context, key, contentType string, data []byte) error {
	_, err := s.Client.PutObject(ctx, s.Bucket, s.Prefix+key, bytes.NewReader(data), int64(len(data)), minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		return err
	}
	manifest, err := json.Marshal(map[string]any{"object_key": key, "size": len(data), "content_type": contentType})
	if err != nil {
		return err
	}
	manifestKey := strings.TrimSuffix(s.Prefix+key, "/content") + "/manifest.json"
	_, err = s.Client.PutObject(ctx, s.Bucket, manifestKey, bytes.NewReader(manifest), int64(len(manifest)), minio.PutObjectOptions{ContentType: "application/json"})
	return err
}

func (s *S3BlobStore) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	return s.Client.GetObject(ctx, s.Bucket, s.Prefix+key, minio.GetObjectOptions{})
}
