package store

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPostgresPlaceholderBinding(t *testing.T) {
	got := bind(`SELECT * FROM files WHERE sha256=? AND size=?`, true)
	if got != `SELECT * FROM files WHERE sha256=$1 AND size=$2` {
		t.Fatalf("unexpected binding: %s", got)
	}
}

func TestLocalBlobLayoutAndTraversalProtection(t *testing.T) {
	root := t.TempDir()
	blobs := &LocalBlobStore{Root: root}
	digest := strings.Repeat("a", 64)
	key := ObjectKey(digest)
	if err := blobs.Put(context.Background(), key, "text/plain", []byte("content")); err != nil {
		t.Fatal(err)
	}
	reader, err := blobs.Open(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(reader)
	reader.Close()
	if string(data) != "content" {
		t.Fatalf("unexpected content: %q", data)
	}
	expected := filepath.Join(root, ".meta", "objects", "sha256", "aa", digest, "manifest.json")
	if _, err = os.Stat(expected); err != nil {
		t.Fatal(err)
	}
	if _, err = blobs.path("../../outside"); err == nil {
		t.Fatal("path traversal accepted")
	}
}
