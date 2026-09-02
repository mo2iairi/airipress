package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/mo2iairi/airipress/internal/store"
)

type mindmapNode struct {
	ID       string        `json:"id"`
	Text     string        `json:"text"`
	Children []mindmapNode `json:"children"`
}

func (a *API) buildMindmap(ctx context.Context, workspaceID string) (mindmapNode, error) {
	var workspaceName string
	if err := a.db.QueryRow(`SELECT name FROM workspaces WHERE id=?`, workspaceID).Scan(&workspaceName); err != nil {
		return mindmapNode{}, err
	}
	root := mindmapNode{ID: "root", Text: workspaceName, Children: []mindmapNode{}}
	rows, err := a.db.Query(`SELECT s.relative_path,f.mime,f.object_key FROM sources s JOIN files f ON f.id=s.file_id WHERE s.workspace_id=? ORDER BY s.relative_path`, workspaceID)
	if err != nil {
		return mindmapNode{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var relativePath, contentType, objectKey string
		if err = rows.Scan(&relativePath, &contentType, &objectKey); err != nil {
			return mindmapNode{}, err
		}
		node := mindmapNode{ID: newID(), Text: relativePath, Children: []mindmapNode{}}
		if !strings.HasPrefix(contentType, "image/") {
			reader, openErr := a.blobs.Open(ctx, objectKey)
			if openErr == nil {
				scanner := bufio.NewScanner(io.LimitReader(reader, 256<<10))
				for scanner.Scan() && len(node.Children) < 24 {
					line := strings.TrimSpace(scanner.Text())
					if strings.HasPrefix(line, "#") {
						label := strings.TrimSpace(strings.TrimLeft(line, "#"))
						if label != "" {
							node.Children = append(node.Children, mindmapNode{ID: newID(), Text: label, Children: []mindmapNode{}})
						}
					}
				}
				reader.Close()
			}
		}
		root.Children = append(root.Children, node)
	}
	return root, rows.Err()
}

func (a *API) mindmap(w http.ResponseWriter, r *http.Request, workspaceID string) {
	if r.Method == http.MethodGet {
		var raw string
		if err := a.db.QueryRow(`SELECT content FROM mindmaps WHERE workspace_id=?`, workspaceID).Scan(&raw); err != nil {
			respondError(w, http.StatusNotFound, "mindmap_not_found")
			return
		}
		var root mindmapNode
		if json.Unmarshal([]byte(raw), &root) != nil {
			respondError(w, http.StatusInternalServerError, "invalid_mindmap")
			return
		}
		respond(w, http.StatusOK, map[string]any{"root": root})
		return
	}
	if r.Method != http.MethodPost {
		respondError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	root, err := a.buildMindmap(r.Context(), workspaceID)
	if err != nil {
		respondError(w, http.StatusNotFound, "workspace_not_found")
		return
	}
	raw, _ := json.Marshal(root)
	now := store.Now()
	_, err = a.db.Exec(`INSERT INTO mindmaps(id,workspace_id,content,created_at,updated_at) VALUES(?,?,?,?,?) ON CONFLICT(workspace_id) DO UPDATE SET content=excluded.content,updated_at=excluded.updated_at`, newID(), workspaceID, string(raw), now, now)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "database_error")
		return
	}
	respond(w, http.StatusCreated, map[string]any{"root": root})
}

type publishInput struct {
	Theme  string `json:"theme"`
	Target string `json:"target"`
	Owner  string `json:"owner"`
	Repo   string `json:"repo"`
	Branch string `json:"branch"`
	Token  string `json:"token"`
}

var githubName = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
var gitBranch = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)

func (a *API) publish(w http.ResponseWriter, r *http.Request, workspaceID string) {
	if r.Method != http.MethodPost {
		respondError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	var input publishInput
	if decode(r, &input) != nil {
		respondError(w, http.StatusBadRequest, "invalid_publish_request")
		return
	}
	if input.Theme == "" {
		input.Theme = "astro-default"
	}
	if input.Target == "" {
		input.Target = "github-pages"
	}
	if input.Branch == "" {
		input.Branch = "gh-pages"
	}
	if input.Token == "" {
		input.Token, _ = a.githubToken()
		if login, err := a.githubAccountLogin(); err == nil {
			if input.Owner != "" && !strings.EqualFold(input.Owner, login) {
				respondError(w, http.StatusForbidden, "github_owner_not_allowed")
				return
			}
			input.Owner = login
		}
	}
	theme, themeOK := a.themeByID(input.Theme)
	if !themeOK || (!theme.Installed && theme.Repository != "") || input.Target != "github-pages" || !githubName.MatchString(input.Owner) || !githubName.MatchString(input.Repo) || !gitBranch.MatchString(input.Branch) || input.Token == "" {
		respondError(w, http.StatusBadRequest, "invalid_github_pages_config")
		return
	}
	var exists string
	if err := a.db.QueryRow(`SELECT id FROM workspaces WHERE id=?`, workspaceID).Scan(&exists); err != nil {
		respondError(w, http.StatusNotFound, "workspace_not_found")
		return
	}
	identifier, now := newID(), store.Now()
	redacted, _ := json.Marshal(map[string]string{"theme": input.Theme, "engine": theme.Engine, "target": input.Target, "owner": input.Owner, "repo": input.Repo, "branch": input.Branch})
	if _, err := a.db.Exec(`INSERT INTO deploy_jobs(id,workspace_id,status,config,url,error,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, identifier, workspaceID, "queued", string(redacted), "", "", now, now); err != nil {
		respondError(w, http.StatusInternalServerError, "database_error")
		return
	}
	go a.publishFunc(identifier, workspaceID, input)
	respond(w, http.StatusAccepted, map[string]string{"id": identifier, "workspace_id": workspaceID, "status": "queued"})
}

func (a *API) job(w http.ResponseWriter, r *http.Request, identifier string) {
	if r.Method != http.MethodGet {
		respondError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	var workspaceID, status, config, publicURL, errorText, createdAt, updatedAt string
	if err := a.db.QueryRow(`SELECT workspace_id,status,config,url,error,created_at,updated_at FROM deploy_jobs WHERE id=?`, identifier).Scan(&workspaceID, &status, &config, &publicURL, &errorText, &createdAt, &updatedAt); err != nil {
		respondError(w, http.StatusNotFound, "job_not_found")
		return
	}
	respond(w, http.StatusOK, map[string]string{"id": identifier, "workspace_id": workspaceID, "status": status, "url": publicURL, "error": errorText, "created_at": createdAt, "updated_at": updatedAt})
}

func (a *API) materializeWorkspace(ctx context.Context, workspaceID, destination string) error {
	rows, err := a.db.Query(`SELECT s.relative_path,f.object_key FROM sources s JOIN files f ON f.id=s.file_id WHERE s.workspace_id=? ORDER BY s.relative_path`, workspaceID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var relativePath, objectKey string
		if err = rows.Scan(&relativePath, &objectKey); err != nil {
			return err
		}
		relativePath, err = safeRelative(relativePath)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, filepath.FromSlash(relativePath))
		if err = os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return err
		}
		reader, openErr := a.blobs.Open(ctx, objectKey)
		if openErr != nil {
			return openErr
		}
		file, createErr := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
		if createErr == nil {
			_, createErr = io.Copy(file, io.LimitReader(reader, maxUploadBytes+1))
		}
		reader.Close()
		if file != nil {
			file.Close()
		}
		if createErr != nil {
			return createErr
		}
	}
	return rows.Err()
}

func (a *API) command(ctx context.Context, stdin any, name string, args ...string) ([]byte, error) {
	encoded, err := json.Marshal(stdin)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = bytes.NewReader(encoded)
	cmd.Dir = filepath.Dir(a.toolsDir)
	cmd.Env = append(os.Environ(), "PYTHONUNBUFFERED=1")
	output, err := cmd.CombinedOutput()
	if len(output) > 16<<10 {
		output = output[len(output)-(16<<10):]
	}
	return output, err
}

func (a *API) runPublish(jobID, workspaceID string, input publishInput) {
	a.db.Exec(`UPDATE deploy_jobs SET status='running',updated_at=? WHERE id=?`, store.Now(), jobID)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	staging := filepath.Join(a.dataRoot, ".build", "studio", jobID)
	workspaceDir, siteDir := filepath.Join(staging, "workspace"), filepath.Join(staging, "site")
	if err := os.MkdirAll(workspaceDir, 0o750); err != nil {
		a.failJob(jobID, err, input.Token)
		return
	}
	if err := a.materializeWorkspace(ctx, workspaceID, workspaceDir); err != nil {
		a.failJob(jobID, err, input.Token)
		return
	}
	python := env("AIRIPRESS_PYTHON_BIN", "python3")
	theme, ok := a.themeByID(input.Theme)
	if !ok {
		a.failJob(jobID, fmt.Errorf("theme is not available"), input.Token)
		return
	}
	themePath, err := a.installedThemePath(input.Theme)
	if err != nil {
		a.failJob(jobID, err, input.Token)
		return
	}
	if output, err := a.command(ctx, map[string]string{"workspace": workspaceDir, "output": siteDir, "title": input.Repo, "engine": theme.Engine, "theme_path": themePath, "theme_id": input.Theme}, python, "-m", "tools", "export"); err != nil {
		a.failJob(jobID, fmt.Errorf("site export failed: %s", output), input.Token)
		return
	}
	artifactDir := filepath.Join(siteDir, "dist")
	if theme.Engine == "astro" {
		toolEnv := []string{"ASTRO_TELEMETRY_DISABLED=1"}
		if output, err := runCommand(ctx, siteDir, toolEnv, "npm", "install", "--ignore-scripts", "--no-audit", "--no-fund"); err != nil {
			a.failJob(jobID, fmt.Errorf("astro dependencies failed: %s", output), input.Token)
			return
		}
		if output, err := runCommand(ctx, siteDir, toolEnv, "npm", "run", "build"); err != nil {
			a.failJob(jobID, fmt.Errorf("astro build failed: %s", output), input.Token)
			return
		}
	} else {
		artifactDir = filepath.Join(siteDir, "public")
		if output, err := runCommand(ctx, siteDir, nil, "hugo", "--minify", "--destination", artifactDir); err != nil {
			a.failJob(jobID, fmt.Errorf("hugo build failed: %s", output), input.Token)
			return
		}
	}
	publishConfig := map[string]string{"site": artifactDir, "owner": input.Owner, "repo": input.Repo, "branch": input.Branch, "token": input.Token}
	output, err := a.command(ctx, publishConfig, python, "-m", "tools", "publish")
	if err != nil {
		a.failJob(jobID, fmt.Errorf("GitHub Pages publish failed: %s", output), input.Token)
		return
	}
	var result struct {
		OK  bool   `json:"ok"`
		URL string `json:"url"`
	}
	if json.Unmarshal(output, &result) != nil || !result.OK {
		a.failJob(jobID, fmt.Errorf("publisher returned an invalid result"), input.Token)
		return
	}
	a.db.Exec(`UPDATE deploy_jobs SET status='succeeded',url=?,updated_at=? WHERE id=?`, result.URL, store.Now(), jobID)
}

func runCommand(ctx context.Context, directory string, environment []string, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = directory
	if environment != nil {
		cmd.Env = append(os.Environ(), environment...)
	}
	output, err := cmd.CombinedOutput()
	if len(output) > 16<<10 {
		output = output[len(output)-(16<<10):]
	}
	return output, err
}

func (a *API) failJob(jobID string, err error, secret string) {
	message := strings.ReplaceAll(err.Error(), secret, "[redacted]")
	if len(message) > 4000 {
		message = message[len(message)-4000:]
	}
	a.db.Exec(`UPDATE deploy_jobs SET status='failed',error=?,updated_at=? WHERE id=?`, message, store.Now(), jobID)
}
