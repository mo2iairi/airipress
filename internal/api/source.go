package api

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/mo2iairi/airipress/internal/store"
)

const maxUploadBytes = 64 << 20

var allowedExtensions = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true,
	".md": true, ".markdown": true, ".txt": true, ".go": true, ".py": true,
	".js": true, ".jsx": true, ".ts": true, ".tsx": true, ".json": true,
	".yaml": true, ".yml": true, ".toml": true, ".css": true, ".html": true,
	".rs": true, ".java": true, ".c": true, ".h": true, ".cpp": true, ".sh": true,
}

type sourceRecord struct {
	ID           string `json:"id"`
	WorkspaceID  string `json:"workspace_id"`
	FileID       string `json:"file_id"`
	RelativePath string `json:"relative_path"`
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	MIME         string `json:"mime"`
	Size         int64  `json:"size"`
	SHA256       string `json:"sha256"`
	SourceType   string `json:"source_type"`
	CreatedAt    string `json:"created_at"`
}

type fileRecord struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	MIME        string `json:"mime"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
	SourceCount int64  `json:"source_count"`
	CreatedAt   string `json:"created_at"`
}

func safeRelative(value string) (string, error) {
	clean := filepath.ToSlash(filepath.Clean(strings.TrimPrefix(value, "/")))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || filepath.IsAbs(value) {
		return "", errorsNew("invalid relative path")
	}
	return clean, nil
}

type stringError string

func (e stringError) Error() string { return string(e) }
func errorsNew(value string) error  { return stringError(value) }
func classifyKind(contentType, name string) string {
	if strings.HasPrefix(contentType, "image/") {
		return "image"
	}
	ext := strings.ToLower(filepath.Ext(name))
	if ext == ".md" || ext == ".markdown" {
		return "markdown"
	}
	if ext == ".txt" {
		return "text"
	}
	return "code"
}

func (a *API) storeFile(ctx context.Context, name, contentType string, data []byte) (string, string, error) {
	digestBytes := sha256.Sum256(data)
	digest := hex.EncodeToString(digestBytes[:])
	var identifier string
	if err := a.db.QueryRow(`SELECT id FROM files WHERE sha256=?`, digest).Scan(&identifier); err == nil {
		return identifier, digest, nil
	} else if err != sql.ErrNoRows {
		return "", "", err
	}
	key := store.ObjectKey(digest)
	if err := a.blobs.Put(ctx, key, contentType, data); err != nil {
		return "", "", err
	}
	identifier = newID()
	_, err := a.db.Exec(`INSERT INTO files(id,sha256,name,mime,size,object_key,created_at) VALUES(?,?,?,?,?,?,?)`, identifier, digest, filepath.Base(name), contentType, len(data), key, store.Now())
	if err != nil {
		// Another concurrent upload may have won the unique SHA constraint.
		if queryErr := a.db.QueryRow(`SELECT id FROM files WHERE sha256=?`, digest).Scan(&identifier); queryErr == nil {
			return identifier, digest, nil
		}
		return "", "", err
	}
	return identifier, digest, nil
}

func (a *API) attachSource(workspaceID, fileID, relativePath, sourceType string) (sourceRecord, error) {
	relativePath, err := safeRelative(relativePath)
	if err != nil {
		return sourceRecord{}, err
	}
	var name, contentType, digest string
	var size int64
	if err = a.db.QueryRow(`SELECT name,mime,size,sha256 FROM files WHERE id=?`, fileID).Scan(&name, &contentType, &size, &digest); err != nil {
		return sourceRecord{}, err
	}
	identifier, now := newID(), store.Now()
	_, err = a.db.Exec(`INSERT INTO sources(id,workspace_id,file_id,relative_path,source_type,created_at) VALUES(?,?,?,?,?,?)`, identifier, workspaceID, fileID, relativePath, sourceType, now)
	if err != nil {
		return sourceRecord{}, err
	}
	return sourceRecord{ID: identifier, WorkspaceID: workspaceID, FileID: fileID, RelativePath: relativePath, Name: name, Kind: classifyKind(contentType, name), MIME: contentType, Size: size, SHA256: digest, SourceType: sourceType, CreatedAt: now}, nil
}

func (a *API) sources(w http.ResponseWriter, r *http.Request, workspaceID string, rest []string) {
	if len(rest) == 1 && r.Method == http.MethodDelete {
		result, err := a.db.Exec(`DELETE FROM sources WHERE workspace_id=? AND id=?`, workspaceID, rest[0])
		if err != nil {
			respondError(w, http.StatusInternalServerError, "database_error")
			return
		}
		count, _ := result.RowsAffected()
		if count == 0 {
			respondError(w, http.StatusNotFound, "source_not_found")
			return
		}
		respond(w, http.StatusNoContent, nil)
		return
	}
	if len(rest) != 0 {
		respondError(w, http.StatusNotFound, "not_found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		rows, err := a.db.Query(`SELECT s.id,s.workspace_id,s.file_id,s.relative_path,f.name,f.mime,f.size,f.sha256,s.source_type,s.created_at FROM sources s JOIN files f ON f.id=s.file_id WHERE s.workspace_id=? ORDER BY s.relative_path`, workspaceID)
		if err != nil {
			respondError(w, http.StatusInternalServerError, "database_error")
			return
		}
		defer rows.Close()
		items := []sourceRecord{}
		for rows.Next() {
			var item sourceRecord
			if err = rows.Scan(&item.ID, &item.WorkspaceID, &item.FileID, &item.RelativePath, &item.Name, &item.MIME, &item.Size, &item.SHA256, &item.SourceType, &item.CreatedAt); err != nil {
				respondError(w, http.StatusInternalServerError, "database_error")
				return
			}
			item.Kind = classifyKind(item.MIME, item.Name)
			items = append(items, item)
		}
		respond(w, http.StatusOK, items)
	case http.MethodPost:
		if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			a.uploadAndAttach(w, r, workspaceID)
			return
		}
		var input struct {
			FileID       string `json:"file_id"`
			RelativePath string `json:"relative_path"`
		}
		if decode(r, &input) != nil || input.FileID == "" {
			respondError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		item, err := a.attachSource(workspaceID, input.FileID, input.RelativePath, "reference")
		if err != nil {
			respondError(w, http.StatusConflict, "source_conflict")
			return
		}
		respond(w, http.StatusCreated, item)
	default:
		respondError(w, http.StatusMethodNotAllowed, "method_not_allowed")
	}
}

func (a *API) uploadAndAttach(w http.ResponseWriter, r *http.Request, workspaceID string) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes+1<<20)
	file, header, err := r.FormFile("file")
	if err != nil {
		respondError(w, http.StatusBadRequest, "file_required")
		return
	}
	defer file.Close()
	name := filepath.Base(header.Filename)
	if !allowedExtensions[strings.ToLower(filepath.Ext(name))] {
		respondError(w, http.StatusUnsupportedMediaType, "unsupported_file_type")
		return
	}
	data, err := io.ReadAll(io.LimitReader(file, maxUploadBytes+1))
	if err != nil || len(data) > maxUploadBytes {
		respondError(w, http.StatusRequestEntityTooLarge, "file_too_large")
		return
	}
	contentType := http.DetectContentType(data)
	if guessed := mime.TypeByExtension(strings.ToLower(filepath.Ext(name))); guessed != "" && contentType == "application/octet-stream" {
		contentType = guessed
	}
	fileID, _, err := a.storeFile(r.Context(), name, contentType, data)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "storage_error")
		return
	}
	item, err := a.attachSource(workspaceID, fileID, name, "upload")
	if err != nil {
		respondError(w, http.StatusConflict, "source_path_exists")
		return
	}
	respond(w, http.StatusCreated, item)
}

func (a *API) assets(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		rows, err := a.db.Query(`SELECT f.id,f.name,f.mime,f.size,f.sha256,f.created_at,COUNT(s.id) FROM files f LEFT JOIN sources s ON s.file_id=f.id GROUP BY f.id,f.name,f.mime,f.size,f.sha256,f.created_at ORDER BY f.created_at DESC`)
		if err != nil {
			respondError(w, http.StatusInternalServerError, "database_error")
			return
		}
		defer rows.Close()
		items := []fileRecord{}
		for rows.Next() {
			var item fileRecord
			if err := rows.Scan(&item.ID, &item.Name, &item.MIME, &item.Size, &item.SHA256, &item.CreatedAt, &item.SourceCount); err != nil {
				respondError(w, http.StatusInternalServerError, "database_error")
				return
			}
			item.Kind = classifyKind(item.MIME, item.Name)
			items = append(items, item)
		}
		if err := rows.Err(); err != nil {
			respondError(w, http.StatusInternalServerError, "database_error")
			return
		}
		respond(w, http.StatusOK, items)
		return
	}
	if r.Method != http.MethodPost {
		respondError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes+1<<20)
	file, header, err := r.FormFile("file")
	if err != nil {
		respondError(w, http.StatusBadRequest, "file_required")
		return
	}
	defer file.Close()
	name := filepath.Base(header.Filename)
	if !allowedExtensions[strings.ToLower(filepath.Ext(name))] {
		respondError(w, http.StatusUnsupportedMediaType, "unsupported_file_type")
		return
	}
	data, err := io.ReadAll(io.LimitReader(file, maxUploadBytes+1))
	if err != nil || len(data) > maxUploadBytes {
		respondError(w, http.StatusRequestEntityTooLarge, "file_too_large")
		return
	}
	contentType := http.DetectContentType(data)
	identifier, digest, err := a.storeFile(r.Context(), name, contentType, data)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "storage_error")
		return
	}
	var storedName, storedMIME, createdAt string
	var storedSize int64
	if err := a.db.QueryRow(`SELECT name,mime,size,created_at FROM files WHERE id=?`, identifier).Scan(&storedName, &storedMIME, &storedSize, &createdAt); err != nil {
		respondError(w, http.StatusInternalServerError, "database_error")
		return
	}
	respond(w, http.StatusCreated, fileRecord{ID: identifier, Name: storedName, SHA256: digest, MIME: storedMIME, Size: storedSize, Kind: classifyKind(storedMIME, storedName), SourceCount: 0, CreatedAt: createdAt})
}
