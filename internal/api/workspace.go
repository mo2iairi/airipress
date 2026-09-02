package api

import (
	"database/sql"
	"net/http"
	"strings"

	"github.com/mo2iairi/airipress/internal/store"
)

type workspaceRecord struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	RootPath  string `json:"root_path"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

func scanWorkspace(row interface{ Scan(...any) error }) (workspaceRecord, error) {
	var value workspaceRecord
	err := row.Scan(&value.ID, &value.Name, &value.RootPath, &value.CreatedAt, &value.UpdatedAt)
	return value, err
}

func (a *API) workspaces(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		rows, err := a.db.Query(`SELECT id,name,root_path,created_at,updated_at FROM workspaces ORDER BY created_at DESC`)
		if err != nil {
			respondError(w, http.StatusInternalServerError, "database_error")
			return
		}
		defer rows.Close()
		items := []workspaceRecord{}
		for rows.Next() {
			item, scanErr := scanWorkspace(rows)
			if scanErr != nil {
				respondError(w, http.StatusInternalServerError, "database_error")
				return
			}
			items = append(items, item)
		}
		respond(w, http.StatusOK, items)
	case http.MethodPost:
		var input struct {
			Name     string `json:"name"`
			RootPath string `json:"root_path"`
		}
		if decode(r, &input) != nil || strings.TrimSpace(input.Name) == "" {
			respondError(w, http.StatusBadRequest, "name_required")
			return
		}
		now, identifier := store.Now(), newID()
		if _, err := a.db.Exec(`INSERT INTO workspaces(id,name,root_path,created_at,updated_at) VALUES(?,?,?,?,?)`, identifier, strings.TrimSpace(input.Name), input.RootPath, now, now); err != nil {
			respondError(w, http.StatusInternalServerError, "database_error")
			return
		}
		respond(w, http.StatusCreated, workspaceRecord{ID: identifier, Name: strings.TrimSpace(input.Name), RootPath: input.RootPath, CreatedAt: now, UpdatedAt: now})
	default:
		respondError(w, http.StatusMethodNotAllowed, "method_not_allowed")
	}
}

func (a *API) workspace(w http.ResponseWriter, r *http.Request, identifier string) {
	switch r.Method {
	case http.MethodGet:
		item, err := scanWorkspace(a.db.QueryRow(`SELECT id,name,root_path,created_at,updated_at FROM workspaces WHERE id=?`, identifier))
		if errorsIsNotFound(err) {
			respondError(w, http.StatusNotFound, "workspace_not_found")
			return
		}
		if err != nil {
			respondError(w, http.StatusInternalServerError, "database_error")
			return
		}
		respond(w, http.StatusOK, item)
	case http.MethodPatch:
		current, err := scanWorkspace(a.db.QueryRow(`SELECT id,name,root_path,created_at,updated_at FROM workspaces WHERE id=?`, identifier))
		if err != nil {
			respondError(w, http.StatusNotFound, "workspace_not_found")
			return
		}
		var input struct {
			Name     *string `json:"name"`
			RootPath *string `json:"root_path"`
		}
		if decode(r, &input) != nil {
			respondError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		if input.Name != nil {
			if strings.TrimSpace(*input.Name) == "" {
				respondError(w, http.StatusBadRequest, "name_required")
				return
			}
			current.Name = strings.TrimSpace(*input.Name)
		}
		if input.RootPath != nil {
			current.RootPath = *input.RootPath
		}
		current.UpdatedAt = store.Now()
		if _, err = a.db.Exec(`UPDATE workspaces SET name=?,root_path=?,updated_at=? WHERE id=?`, current.Name, current.RootPath, current.UpdatedAt, identifier); err != nil {
			respondError(w, http.StatusInternalServerError, "database_error")
			return
		}
		respond(w, http.StatusOK, current)
	case http.MethodDelete:
		result, err := a.db.Exec(`DELETE FROM workspaces WHERE id=?`, identifier)
		if err != nil {
			respondError(w, http.StatusInternalServerError, "database_error")
			return
		}
		count, _ := result.RowsAffected()
		if count == 0 {
			respondError(w, http.StatusNotFound, "workspace_not_found")
			return
		}
		respond(w, http.StatusNoContent, nil)
	default:
		respondError(w, http.StatusMethodNotAllowed, "method_not_allowed")
	}
}

func errorsIsNotFound(err error) bool { return err == sql.ErrNoRows }
