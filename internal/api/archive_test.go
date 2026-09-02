package api

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/mo2iairi/airipress/internal/store"
)

func exportArchive(t *testing.T, handler http.Handler) []byte {
	t.Helper()
	recorder := request(t, handler, http.MethodGet, "/api/v1/data/export", nil, "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("export: %d %s", recorder.Code, recorder.Body.String())
	}
	return append([]byte(nil), recorder.Body.Bytes()...)
}

func importArchive(t *testing.T, handler http.Handler, data []byte) *bytes.Buffer {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "backup.airipress")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	recorder := request(t, handler, http.MethodPost, "/api/v1/data/import", &body, writer.FormDataContentType())
	if recorder.Code != http.StatusOK {
		t.Fatalf("import: %d %s", recorder.Code, recorder.Body.String())
	}
	return recorder.Body
}

func rewriteArchive(t *testing.T, data []byte, replaceName string, replacement []byte, omitName string, extraName string) []byte {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	for _, entry := range reader.File {
		if entry.Name == omitName {
			continue
		}
		in, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(in)
		in.Close()
		if err != nil {
			t.Fatal(err)
		}
		if entry.Name == replaceName {
			content = replacement
		}
		out, err := writer.Create(entry.Name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = out.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if extraName != "" {
		out, err := writer.Create(extraName)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = out.Write([]byte("unexpected"))
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func TestDataArchiveRoundTripReplacesExistingData(t *testing.T) {
	api, db, _ := testAPI(t)
	workspaceID := createWorkspace(t, api, "kept")
	upload(t, api, workspaceID, "note.md", "# kept\n")
	archive := exportArchive(t, api)
	createWorkspace(t, api, "must disappear")

	importArchive(t, api, archive)
	var workspaces, files, sources int
	if err := db.QueryRow(`SELECT COUNT(*) FROM workspaces`).Scan(&workspaces); err != nil {
		t.Fatal(err)
	}
	_ = db.QueryRow(`SELECT COUNT(*) FROM files`).Scan(&files)
	_ = db.QueryRow(`SELECT COUNT(*) FROM sources`).Scan(&sources)
	if workspaces != 1 || files != 1 || sources != 1 {
		t.Fatalf("unexpected restored counts: workspaces=%d files=%d sources=%d", workspaces, files, sources)
	}
	var name string
	_ = db.QueryRow(`SELECT name FROM workspaces`).Scan(&name)
	if name != "kept" {
		t.Fatalf("restored workspace=%q", name)
	}
}

func TestDataArchiveRehashesOfflineResourceEdits(t *testing.T) {
	api, db, _ := testAPI(t)
	workspaceID := createWorkspace(t, api, "offline")
	file := upload(t, api, workspaceID, "note.md", "before")
	resourceName := "resources/" + file["file_id"].(string) + "/content"
	changed := []byte("edited while offline")
	archive := rewriteArchive(t, exportArchive(t, api), resourceName, changed, "", "")

	importArchive(t, api, archive)
	digestBytes := sha256.Sum256(changed)
	digest := hex.EncodeToString(digestBytes[:])
	var gotDigest, objectKey string
	var size int64
	if err := db.QueryRow(`SELECT sha256,size,object_key FROM files WHERE id=?`, file["file_id"]).Scan(&gotDigest, &size, &objectKey); err != nil {
		t.Fatal(err)
	}
	if gotDigest != digest || size != int64(len(changed)) || objectKey != store.ObjectKey(digest) {
		t.Fatalf("offline edit not normalized: digest=%q size=%d key=%q", gotDigest, size, objectKey)
	}
	reader, err := api.blobs.Open(context.Background(), objectKey)
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := io.ReadAll(reader)
	reader.Close()
	if !bytes.Equal(stored, changed) {
		t.Fatalf("stored resource=%q", stored)
	}
}

func TestInvalidDataArchivesDoNotReplaceDatabase(t *testing.T) {
	api, db, _ := testAPI(t)
	workspaceID := createWorkspace(t, api, "unchanged")
	file := upload(t, api, workspaceID, "note.md", "safe")
	archive := exportArchive(t, api)
	resourceName := "resources/" + file["file_id"].(string) + "/content"

	for name, invalid := range map[string][]byte{
		"missing object":  rewriteArchive(t, archive, "", nil, resourceName, ""),
		"unexpected path": rewriteArchive(t, archive, "", nil, "", "../escape"),
	} {
		t.Run(name, func(t *testing.T) {
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			part, _ := writer.CreateFormFile("file", "bad.airipress")
			_, _ = part.Write(invalid)
			_ = writer.Close()
			recorder := request(t, api, http.MethodPost, "/api/v1/data/import", &body, writer.FormDataContentType())
			if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "invalid_archive") {
				t.Fatalf("invalid archive accepted: %d %s", recorder.Code, recorder.Body.String())
			}
			var count int
			_ = db.QueryRow(`SELECT COUNT(*) FROM workspaces WHERE name='unchanged'`).Scan(&count)
			if count != 1 {
				t.Fatal("database changed after rejected archive")
			}
		})
	}
}
