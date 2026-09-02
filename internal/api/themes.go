package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/mo2iairi/airipress/internal/store"
)

type themeRecord struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Engine      string `json:"engine"`
	Repository  string `json:"repository,omitempty"`
	Ref         string `json:"ref,omitempty"`
	PreviewURL  string `json:"preview_url,omitempty"`
	Description string `json:"description,omitempty"`
	Installed   bool   `json:"installed"`
	Commit      string `json:"commit,omitempty"`
}

func builtinThemes() []themeRecord {
	return []themeRecord{{ID: "astro-default", Name: "默认 Astro", Engine: "astro", Description: "内置的轻量内容主题", Installed: true}, {ID: "hugo-default", Name: "默认 Hugo", Engine: "hugo", Description: "内置的静态内容主题", Installed: true}}
}

func (a *API) themeRecord(config themeConfig) themeRecord {
	record := themeRecord{ID: config.ID, Name: config.Name, Engine: config.Engine, Repository: config.Repository, Ref: config.Ref, PreviewURL: config.PreviewURL, Description: config.Description}
	if commit, err := os.ReadFile(filepath.Join(a.themeRoot(), config.ID, "commit")); err == nil {
		record.Installed, record.Commit = true, strings.TrimSpace(string(commit))
	}
	return record
}

func (a *API) themeRoot() string { return filepath.Join(a.dataRoot, ".build", "themes") }
func (a *API) configuredTheme(id string) (themeConfig, bool) {
	for _, theme := range a.themeCatalog {
		if theme.ID == id {
			return theme, true
		}
	}
	return themeConfig{}, false
}
func (a *API) themeByID(id string) (themeRecord, bool) {
	for _, builtin := range builtinThemes() {
		if builtin.ID == id {
			return builtin, true
		}
	}
	config, ok := a.configuredTheme(id)
	if ok {
		return a.themeRecord(config), true
	}
	if config, ok = a.manualTheme(id); ok {
		return a.themeRecord(config), true
	}
	return themeRecord{}, false
}

func (a *API) manualTheme(id string) (themeConfig, bool) {
	var config themeConfig
	err := a.db.QueryRow(`SELECT id,name,engine,repository,ref,preview_url,description FROM site_themes WHERE id=?`, id).Scan(&config.ID, &config.Name, &config.Engine, &config.Repository, &config.Ref, &config.PreviewURL, &config.Description)
	return config, err == nil
}
func (a *API) manualThemes() []themeConfig {
	rows, err := a.db.Query(`SELECT id,name,engine,repository,ref,preview_url,description FROM site_themes ORDER BY updated_at DESC`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	items := []themeConfig{}
	for rows.Next() {
		var item themeConfig
		if rows.Scan(&item.ID, &item.Name, &item.Engine, &item.Repository, &item.Ref, &item.PreviewURL, &item.Description) == nil {
			items = append(items, item)
		}
	}
	return items
}

func (a *API) themes(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 && r.Method == http.MethodGet {
		items := builtinThemes()
		for _, config := range a.themeCatalog {
			items = append(items, a.themeRecord(config))
		}
		for _, config := range a.manualThemes() {
			items = append(items, a.themeRecord(config))
		}
		respond(w, http.StatusOK, items)
		return
	}
	if len(rest) == 1 && rest[0] == "import" && r.Method == http.MethodPost {
		a.importTheme(w, r)
		return
	}
	if len(rest) == 2 && rest[1] == "install" && r.Method == http.MethodPost {
		record, ok := a.installTheme(r.Context(), rest[0])
		if !ok {
			respondError(w, http.StatusNotFound, "theme_not_in_catalog")
			return
		}
		respond(w, http.StatusOK, record)
		return
	}
	respondError(w, http.StatusNotFound, "not_found")
}

func (a *API) importTheme(w http.ResponseWriter, r *http.Request) {
	var input struct {
		GitURL      string `json:"git_url"`
		Ref         string `json:"ref"`
		Name        string `json:"name"`
		PreviewURL  string `json:"preview_url"`
		Description string `json:"description"`
	}
	if decode(r, &input) != nil || (input.Ref != "" && !regexp.MustCompile(`^[A-Za-z0-9._/-]{1,128}$`).MatchString(input.Ref)) {
		respondError(w, http.StatusBadRequest, "invalid_theme_import")
		return
	}
	repository, ok := githubRepositoryFromGitURL(input.GitURL)
	if !ok {
		respondError(w, http.StatusBadRequest, "invalid_theme_git_url")
		return
	}
	if input.PreviewURL != "" {
		u, err := url.Parse(input.PreviewURL)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			respondError(w, http.StatusBadRequest, "invalid_theme_preview_url")
			return
		}
	}
	token, _ := a.githubToken()
	id := "theme-" + newID()[:12]
	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = strings.Split(repository, "/")[1]
	}
	config := themeConfig{ID: id, Name: name, Repository: repository, Ref: input.Ref, PreviewURL: input.PreviewURL, Description: strings.TrimSpace(input.Description)}
	record, err := a.installThemeConfig(r.Context(), config, token)
	if err != nil {
		respondError(w, http.StatusBadGateway, "theme_install_failed")
		return
	}
	config.Engine = record.Engine
	now := store.Now()
	if _, err = a.db.Exec(`INSERT INTO site_themes(id,name,engine,repository,ref,preview_url,description,git_commit,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, config.ID, config.Name, config.Engine, config.Repository, config.Ref, config.PreviewURL, config.Description, record.Commit, now, now); err != nil {
		respondError(w, http.StatusInternalServerError, "database_error")
		return
	}
	respond(w, http.StatusCreated, record)
}

func githubRepositoryFromGitURL(value string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(value))
	if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Host, "github.com") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", false
	}
	parts := strings.Split(strings.Trim(strings.TrimSuffix(u.Path, "/"), "/"), "/")
	if len(parts) != 2 {
		return "", false
	}
	owner, repo := parts[0], strings.TrimSuffix(parts[1], ".git")
	if !regexp.MustCompile(`^[A-Za-z0-9_.-]+$`).MatchString(owner) || !regexp.MustCompile(`^[A-Za-z0-9_.-]+$`).MatchString(repo) {
		return "", false
	}
	return owner + "/" + repo, true
}

func (a *API) installTheme(ctx context.Context, id string) (themeRecord, bool) {
	config, ok := a.configuredTheme(id)
	if !ok {
		return themeRecord{}, false
	}
	record, err := a.installThemeConfig(ctx, config, "")
	return record, err == nil
}

func (a *API) installThemeConfig(ctx context.Context, config themeConfig, token string) (themeRecord, error) {
	if err := os.MkdirAll(a.themeRoot(), 0750); err != nil {
		return themeRecord{}, err
	}
	root, err := os.MkdirTemp(a.themeRoot(), config.ID+"-")
	if err != nil {
		return themeRecord{}, err
	}
	defer os.RemoveAll(root)
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	remote := "https://github.com/" + config.Repository + ".git"
	args := []string{"clone", "--depth", "1"}
	if config.Ref != "" {
		args = append(args, "--branch", config.Ref)
	}
	args = append(args, remote, filepath.Join(root, "theme"))
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	var askpassDir string
	if token != "" {
		askpassDir, _ = os.MkdirTemp("", "airipress-theme-askpass-")
		if askpassDir != "" {
			defer os.RemoveAll(askpassDir)
			script := filepath.Join(askpassDir, "askpass.sh")
			if os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s' \"$AIRIPRESS_GITHUB_TOKEN\"\n"), 0700) == nil {
				cmd.Env = append(cmd.Env, "GIT_ASKPASS="+script, "AIRIPRESS_GITHUB_TOKEN="+token)
			}
		}
	}
	if output, err := cmd.CombinedOutput(); err != nil || (len(output) == 0 && ctx.Err() != nil) {
		return themeRecord{}, errors.New("theme clone failed")
	}
	path := filepath.Join(root, "theme")
	command := exec.CommandContext(ctx, "git", "-C", path, "rev-parse", "HEAD")
	raw, err := command.Output()
	if err != nil {
		return themeRecord{}, err
	}
	commit := strings.TrimSpace(string(raw))
	if len(commit) != 40 {
		return themeRecord{}, errors.New("invalid theme commit")
	}
	manifest, err := os.ReadFile(filepath.Join(path, "airipress.theme.json"))
	if err != nil {
		return themeRecord{}, err
	}
	var declaration struct {
		Engine string `json:"engine"`
	}
	if json.Unmarshal(manifest, &declaration) != nil {
		return themeRecord{}, errors.New("invalid theme contract")
	}
	declaration.Engine = strings.ToLower(declaration.Engine)
	if declaration.Engine != config.Engine {
		if config.Engine != "" {
			return themeRecord{}, errors.New("theme engine does not match")
		}
		if declaration.Engine != "astro" && declaration.Engine != "hugo" {
			return themeRecord{}, errors.New("unsupported theme engine")
		}
		config.Engine = strings.ToLower(declaration.Engine)
	}
	if err := filepath.WalkDir(path, func(_ string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("theme symlinks are not allowed")
		}
		return nil
	}); err != nil {
		return themeRecord{}, err
	}
	if err := os.RemoveAll(filepath.Join(path, ".git")); err != nil {
		return themeRecord{}, err
	}
	target := filepath.Join(a.themeRoot(), config.ID)
	if err = os.RemoveAll(target); err != nil {
		return themeRecord{}, err
	}
	if err = os.MkdirAll(a.themeRoot(), 0750); err != nil {
		return themeRecord{}, err
	}
	if err = os.Rename(path, target); err != nil {
		return themeRecord{}, err
	}
	if err = os.WriteFile(filepath.Join(target, "commit"), []byte(commit+"\n"), 0640); err != nil {
		return themeRecord{}, err
	}
	return a.themeRecord(config), nil
}

func (a *API) installedThemePath(id string) (string, error) {
	if id == "astro-default" || id == "hugo-default" {
		return "", nil
	}
	record, ok := a.themeByID(id)
	if !ok {
		return "", errors.New("unknown theme")
	}
	if !record.Installed {
		return "", errors.New("theme is not installed")
	}
	return filepath.Join(a.themeRoot(), id), nil
}
