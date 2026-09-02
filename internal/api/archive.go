package api

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mo2iairi/airipress/internal/store"
)

const archiveLimit int64 = 256 << 20

type archiveManifest struct {
	Format     string `json:"format"`
	Version    int    `json:"version"`
	ExportedAt string `json:"exported_at"`
}
type archiveRecords struct {
	Workspaces      []map[string]any `json:"workspaces"`
	Models          []map[string]any `json:"models"`
	Files           []map[string]any `json:"files"`
	Sources         []map[string]any `json:"sources"`
	Chats           []map[string]any `json:"chats"`
	Messages        []map[string]any `json:"messages"`
	MessageVersions []map[string]any `json:"message_versions"`
	Mindmaps        []map[string]any `json:"mindmaps"`
	DeployJobs      []map[string]any `json:"deploy_jobs"`
}

var archiveCols = map[string][]string{
	"workspaces":       {"id", "name", "root_path", "created_at", "updated_at"},
	"models":           {"id", "name", "provider", "model", "api_key", "base_url", "created_at", "updated_at"},
	"files":            {"id", "sha256", "name", "mime", "size", "object_key", "created_at"},
	"sources":          {"id", "workspace_id", "file_id", "relative_path", "source_type", "created_at"},
	"chats":            {"id", "workspace_id", "title", "created_at"},
	"messages":         {"id", "chat_id", "role", "content", "created_at"},
	"message_versions": {"id", "message_id", "content", "is_selected", "created_at"},
	"mindmaps":         {"id", "workspace_id", "content", "created_at", "updated_at"},
	"deploy_jobs":      {"id", "workspace_id", "status", "config", "url", "error", "created_at", "updated_at"},
}

func archiveRows(a *API, table string) ([]map[string]any, error) {
	cols := archiveCols[table]
	rows, err := a.db.Query("SELECT " + strings.Join(cols, ",") + " FROM " + table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptr := make([]any, len(cols))
		for i := range vals {
			ptr[i] = &vals[i]
		}
		if err = rows.Scan(ptr...); err != nil {
			return nil, err
		}
		m := map[string]any{}
		for i, v := range vals {
			if b, ok := v.([]byte); ok {
				m[cols[i]] = string(b)
			} else {
				m[cols[i]] = v
			}
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (a *API) exportData(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	a.archiveMu.Lock()
	defer a.archiveMu.Unlock()
	a.dataMu.Lock()
	defer a.dataMu.Unlock()
	tmp, err := os.CreateTemp("", "airipress-export-*.airipress")
	if err != nil {
		respondError(w, 500, "archive_failed")
		return
	}
	name := tmp.Name()
	defer os.Remove(name)
	zw := zip.NewWriter(tmp)
	man := archiveManifest{"airipress", 2, time.Now().UTC().Format(time.RFC3339Nano)}
	mb, _ := json.Marshal(man)
	if err = writeZip(zw, "manifest.json", mb); err != nil {
		tmp.Close()
		respondError(w, 500, "archive_failed")
		return
	}
	rec := archiveRecords{}
	for _, x := range []struct {
		n string
		p *[]map[string]any
	}{{"workspaces", &rec.Workspaces}, {"models", &rec.Models}, {"files", &rec.Files}, {"sources", &rec.Sources}, {"chats", &rec.Chats}, {"messages", &rec.Messages}, {"message_versions", &rec.MessageVersions}, {"mindmaps", &rec.Mindmaps}, {"deploy_jobs", &rec.DeployJobs}} {
		*x.p, err = archiveRows(a, x.n)
		if err != nil {
			tmp.Close()
			respondError(w, 500, "archive_failed")
			return
		}
	}
	rb, _ := json.Marshal(rec)
	if err = writeZip(zw, "records.json", rb); err != nil {
		tmp.Close()
		respondError(w, 500, "archive_failed")
		return
	}
	for _, f := range rec.Files {
		id, _ := f["id"].(string)
		key, _ := f["object_key"].(string)
		rd, e := a.blobs.Open(r.Context(), key)
		if e != nil {
			zw.Close()
			tmp.Close()
			respondError(w, 500, "archive_object_missing")
			return
		}
		h := sha256.New()
		ent, e := zw.Create("resources/" + id + "/content")
		var size int64
		if e == nil {
			size, e = io.Copy(io.MultiWriter(ent, h), rd)
		}
		rd.Close()
		if e != nil {
			zw.Close()
			tmp.Close()
			respondError(w, 500, "archive_failed")
			return
		}
		if hex.EncodeToString(h.Sum(nil)) != fmt.Sprint(f["sha256"]) || fmt.Sprint(f["size"]) != fmt.Sprint(size) {
			zw.Close()
			tmp.Close()
			respondError(w, 500, "archive_object_hash_mismatch")
			return
		}
	}
	if err = zw.Close(); err == nil {
		err = tmp.Close()
	}
	if err != nil {
		respondError(w, 500, "archive_failed")
		return
	}
	stat, _ := os.Stat(name)
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename=airipress-export.airipress")
	w.Header().Set("Content-Length", fmt.Sprint(stat.Size()))
	http.ServeFile(w, r, name)
}
func writeZip(z *zip.Writer, n string, b []byte) error {
	x, e := z.Create(n)
	if e == nil {
		_, e = x.Write(b)
	}
	return e
}

func (a *API) importData(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondError(w, 405, "method_not_allowed")
		return
	}
	a.archiveMu.Lock()
	defer a.archiveMu.Unlock()
	a.dataMu.Lock()
	defer a.dataMu.Unlock()
	r.Body = http.MaxBytesReader(w, r.Body, archiveLimit+16<<20)
	if err := r.ParseMultipartForm(archiveLimit + 16<<20); err != nil {
		respondError(w, 413, "archive_too_large")
		return
	}
	defer r.MultipartForm.RemoveAll()
	files := r.MultipartForm.File["file"]
	if len(r.MultipartForm.File) != 1 || len(files) != 1 {
		respondError(w, 400, "file_required")
		return
	}
	var fh *multipart.FileHeader = files[0]
	if fh == nil {
		respondError(w, 400, "file_required")
		return
	}
	f, err := fh.Open()
	if err != nil {
		respondError(w, 400, "invalid_archive")
		return
	}
	tmp, e := os.CreateTemp("", "airipress-import-*.airipress")
	if e != nil {
		f.Close()
		respondError(w, 500, "archive_failed")
		return
	}
	written, e := io.Copy(tmp, io.LimitReader(f, archiveLimit+1))
	f.Close()
	closeErr := tmp.Close()
	name := tmp.Name()
	defer os.Remove(name)
	if e != nil || closeErr != nil || written > archiveLimit {
		respondError(w, 413, "archive_too_large")
		return
	}
	result, e := a.readImport(r.Context(), name)
	if e != nil {
		respondError(w, 400, "invalid_archive")
		return
	}
	respond(w, 200, map[string]any{"imported": result})
}

func (a *API) readImport(ctx context.Context, name string) (int, error) {
	zr, e := zip.OpenReader(name)
	if e != nil {
		return 0, e
	}
	defer zr.Close()
	seen := map[string]bool{}
	var man archiveManifest
	var rec archiveRecords
	objs := map[string][]byte{}
	total := int64(0)
	if len(zr.File) > 10000 {
		return 0, errors.New("too many entries")
	}
	for _, f := range zr.File {
		if seen[f.Name] || strings.HasPrefix(f.Name, "/") || strings.Contains(filepath.ToSlash(f.Name), "../") {
			return 0, errors.New("bad entry")
		}
		seen[f.Name] = true
		if f.FileInfo().IsDir() {
			return 0, errors.New("directory")
		}
		total += int64(f.UncompressedSize64)
		if total > archiveLimit {
			return 0, errors.New("too large")
		}
		rd, e := f.Open()
		if e != nil {
			return 0, e
		}
		b, e := io.ReadAll(io.LimitReader(rd, archiveLimit+1))
		rd.Close()
		if e != nil {
			return 0, e
		}
		switch f.Name {
		case "manifest.json":
			e = decodeStrict(b, &man)
		case "records.json":
			e = decodeStrict(b, &rec)
		default:
			if strings.HasPrefix(f.Name, "resources/") && strings.HasSuffix(f.Name, "/content") {
				id := strings.TrimSuffix(strings.TrimPrefix(f.Name, "resources/"), "/content")
				if id == "" || strings.Contains(id, "/") {
					return 0, errors.New("bad resource")
				}
				objs[id] = b
			} else {
				return 0, errors.New("unknown entry")
			}
		}
		if e != nil {
			return 0, e
		}
	}
	if man.Format != "airipress" || (man.Version != 1 && man.Version != 2) || !seen["manifest.json"] || !seen["records.json"] {
		return 0, errors.New("manifest")
	}
	if _, e := time.Parse(time.RFC3339Nano, man.ExportedAt); e != nil {
		return 0, errors.New("manifest timestamp")
	}
	n, e := a.validateAndReplace(ctx, rec, objs)
	return n, e
}

func decodeStrict(b []byte, v any) error {
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return errors.New("trailing json")
	}
	return nil
}

func checkKeys(rows []map[string]any, table string) error {
	allowed := map[string]bool{}
	for _, k := range archiveCols[table] {
		allowed[k] = true
	}
	for _, r := range rows {
		for k := range r {
			if !allowed[k] {
				return fmt.Errorf("unknown field")
			}
		}
		for _, k := range archiveCols[table] {
			if _, exists := r[k]; !exists {
				return fmt.Errorf("missing field")
			}
		}
	}
	return nil
}
func (a *API) validateAndReplace(ctx context.Context, r archiveRecords, objs map[string][]byte) (int, error) {
	if r.Workspaces == nil || r.Models == nil || r.Files == nil || r.Sources == nil || r.Chats == nil || r.Messages == nil || r.Mindmaps == nil || r.DeployJobs == nil {
		return 0, errors.New("incomplete records")
	}
	if r.MessageVersions == nil {
		r.MessageVersions = []map[string]any{}
	}
	validationGroups := []struct {
		n    string
		rows []map[string]any
	}{{"workspaces", r.Workspaces}, {"models", r.Models}, {"files", r.Files}, {"sources", r.Sources}, {"chats", r.Chats}, {"messages", r.Messages}, {"message_versions", r.MessageVersions}, {"mindmaps", r.Mindmaps}, {"deploy_jobs", r.DeployJobs}}
	for _, g := range validationGroups {
		if e := checkKeys(g.rows, g.n); e != nil {
			return 0, e
		}
	}
	originalFileCount := len(r.Files)
	canonicalByDigest := map[string]string{}
	remappedFileIDs := map[string]string{}
	normalizedFiles := make([]map[string]any, 0, len(r.Files))
	for _, f := range r.Files {
		id, ok := f["id"].(string)
		if !ok || id == "" {
			return 0, errors.New("file id")
		}
		b, ok := objs[id]
		if !ok {
			return 0, errors.New("missing object")
		}
		h := sha256.Sum256(b)
		digest := hex.EncodeToString(h[:])
		f["sha256"] = digest
		f["size"] = int64(len(b))
		f["object_key"] = store.ObjectKey(digest)
		if canonicalID, exists := canonicalByDigest[digest]; exists {
			remappedFileIDs[id] = canonicalID
			continue
		}
		canonicalByDigest[digest] = id
		normalizedFiles = append(normalizedFiles, f)
		if e := a.blobs.Put(ctx, store.ObjectKey(digest), fmt.Sprint(f["mime"]), b); e != nil {
			return 0, e
		}
	}
	if len(objs) != originalFileCount {
		return 0, errors.New("unexpected object")
	}
	for _, source := range r.Sources {
		if fileID, ok := source["file_id"].(string); ok {
			if canonicalID, exists := remappedFileIDs[fileID]; exists {
				source["file_id"] = canonicalID
			}
		}
	}
	r.Files = normalizedFiles
	groups := []struct {
		n    string
		rows []map[string]any
	}{{"workspaces", r.Workspaces}, {"models", r.Models}, {"files", r.Files}, {"sources", r.Sources}, {"chats", r.Chats}, {"messages", r.Messages}, {"message_versions", r.MessageVersions}, {"mindmaps", r.Mindmaps}, {"deploy_jobs", r.DeployJobs}}
	tx, e := a.db.Begin()
	if e != nil {
		return 0, e
	}
	defer tx.Rollback()
	for _, q := range []string{"message_versions", "messages", "sources", "mindmaps", "deploy_jobs", "chats", "files", "models", "workspaces"} {
		if _, e = tx.Exec("DELETE FROM " + q); e != nil {
			return 0, e
		}
	}
	count := 0
	for _, g := range groups {
		cols := archiveCols[g.n]
		for _, row := range g.rows {
			args := make([]any, len(cols))
			for i, c := range cols {
				args[i] = row[c]
			}
			marks := strings.TrimRight(strings.Repeat("?,", len(cols)), ",")
			if _, e = tx.Exec("INSERT INTO "+g.n+"("+strings.Join(cols, ",")+") VALUES("+marks+")", args...); e != nil {
				return 0, e
			}
			count++
		}
	}
	if e = tx.Commit(); e != nil {
		return 0, e
	}
	return count, nil
}
