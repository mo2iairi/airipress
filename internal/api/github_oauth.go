package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func (a *API) githubAuthRoute(w http.ResponseWriter, r *http.Request, parts []string) {
	if len(parts) != 1 && !(len(parts) == 3 && parts[0] == "repos" && parts[2] == "branches") && !(len(parts) == 4 && parts[0] == "repos" && parts[3] == "branches") {
		respondError(w, http.StatusNotFound, "not_found")
		return
	}
	switch parts[0] {
	case "start":
		if r.Method != http.MethodGet {
			respondError(w, 405, "method_not_allowed")
			return
		}
		if !a.authenticated(r) {
			respondError(w, 401, "unauthorized")
			return
		}
		if a.githubOAuth.ClientID == "" || a.githubOAuth.RedirectURL == "" {
			respondError(w, 503, "github_oauth_not_configured")
			return
		}
		state := newID()
		a.oauthMu.Lock()
		a.oauthStates[state] = time.Now().Add(10 * time.Minute)
		a.oauthMu.Unlock()
		u, _ := url.Parse(a.githubOAuth.AuthorizeURL)
		q := u.Query()
		q.Set("client_id", a.githubOAuth.ClientID)
		q.Set("redirect_uri", a.githubOAuth.RedirectURL)
		q.Set("scope", "repo")
		q.Set("state", state)
		u.RawQuery = q.Encode()
		http.SetCookie(w, &http.Cookie{Name: "airipress_github_oauth_state", Value: state, Path: "/", HttpOnly: true, Secure: a.cookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: 600})
		http.Redirect(w, r, u.String(), http.StatusFound)
	case "callback":
		a.githubCallback(w, r)
	case "status":
		if r.Method != http.MethodGet || !a.authenticated(r) {
			respondError(w, 401, "unauthorized")
			return
		}
		a.githubStatus(w)
	case "disconnect":
		if r.Method != http.MethodDelete || !a.authenticated(r) {
			respondError(w, 401, "unauthorized")
			return
		}
		if r.Header.Get("X-Airipress-Request") != "1" || !a.originAllowed(r) {
			respondError(w, 403, "csrf_required")
			return
		}
		_, _ = a.db.Exec(`DELETE FROM github_oauth_accounts WHERE id=1`)
		w.WriteHeader(http.StatusNoContent)
	case "repos":
		if !a.authenticated(r) {
			respondError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if (len(parts) == 3 && parts[2] == "branches") || (len(parts) == 4 && parts[3] == "branches") {
			if r.Method != http.MethodGet {
				respondError(w, http.StatusMethodNotAllowed, "method_not_allowed")
				return
			}
			owner, repo := parts[1], parts[2]
			if len(parts) == 3 {
				owner, _ = a.githubAccountLogin()
				repo = parts[1]
			}
			a.githubBranches(w, r, owner, repo)
			return
		}
		if len(parts) != 1 {
			respondError(w, http.StatusNotFound, "not_found")
			return
		}
		if r.Method == http.MethodPost && (r.Header.Get("X-Airipress-Request") != "1" || !a.originAllowed(r)) {
			respondError(w, http.StatusForbidden, "csrf_required")
			return
		}
		if r.Method == http.MethodGet {
			a.githubRepos(w, r)
			return
		}
		if r.Method == http.MethodPost {
			a.githubCreateRepo(w, r)
			return
		}
		respondError(w, http.StatusMethodNotAllowed, "method_not_allowed")
	default:
		respondError(w, 404, "not_found")
	}
}

func (a *API) githubCallback(w http.ResponseWriter, r *http.Request) {
	state, _ := r.Cookie("airipress_github_oauth_state")
	a.oauthMu.Lock()
	expiry, ok := a.oauthStates[r.URL.Query().Get("state")]
	if ok {
		delete(a.oauthStates, r.URL.Query().Get("state"))
	}
	a.oauthMu.Unlock()
	if state == nil || !ok || state.Value != r.URL.Query().Get("state") || time.Now().After(expiry) {
		respondError(w, 400, "invalid_oauth_state")
		return
	}
	if r.URL.Query().Get("error") != "" {
		respondError(w, 400, "github_oauth_denied")
		return
	}
	if a.githubOAuth.ClientID == "" || a.githubOAuth.ClientSecret == "" {
		respondError(w, 503, "github_oauth_not_configured")
		return
	}
	form := url.Values{"client_id": {a.githubOAuth.ClientID}, "client_secret": {a.githubOAuth.ClientSecret}, "code": {r.URL.Query().Get("code")}, "redirect_uri": {a.githubOAuth.RedirectURL}}
	req, _ := http.NewRequest(http.MethodPost, a.githubOAuth.TokenURL, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := a.httpClient.Do(req)
	if err != nil {
		respondError(w, 502, "github_oauth_exchange_failed")
		return
	}
	defer resp.Body.Close()
	var token struct {
		AccessToken string `json:"access_token"`
		Scope       string `json:"scope"`
	}
	if json.NewDecoder(resp.Body).Decode(&token) != nil || resp.StatusCode/100 != 2 || token.AccessToken == "" {
		respondError(w, 502, "github_oauth_exchange_failed")
		return
	}
	ureq, _ := http.NewRequest(http.MethodGet, a.githubOAuth.APIURL, nil)
	ureq.Header.Set("Authorization", "Bearer "+token.AccessToken)
	ureq.Header.Set("Accept", "application/vnd.github+json")
	ur, err := a.httpClient.Do(ureq)
	if err != nil {
		respondError(w, 502, "github_user_lookup_failed")
		return
	}
	defer ur.Body.Close()
	var user struct {
		Login string `json:"login"`
	}
	if json.NewDecoder(ur.Body).Decode(&user) != nil || ur.StatusCode/100 != 2 || user.Login == "" {
		respondError(w, 502, "github_user_lookup_failed")
		return
	}
	ciphertext, err := a.encrypt(token.AccessToken)
	if err != nil {
		respondError(w, 500, "encryption_failed")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = a.db.Exec(`INSERT INTO github_oauth_accounts(id,login,access_token,scopes,created_at,updated_at) VALUES(1,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET login=excluded.login,access_token=excluded.access_token,scopes=excluded.scopes,updated_at=excluded.updated_at`, user.Login, ciphertext, token.Scope, now, now)
	if err != nil {
		respondError(w, 500, "database_error")
		return
	}
	http.Redirect(w, r, "/", http.StatusFound)
}

func (a *API) githubStatus(w http.ResponseWriter) {
	var login, scopes string
	if a.db.QueryRow(`SELECT login,scopes FROM github_oauth_accounts WHERE id=1`).Scan(&login, &scopes) != nil {
		respond(w, 200, map[string]any{"connected": false})
		return
	}
	respond(w, 200, map[string]any{"connected": true, "login": login, "scopes": scopes})
}

func (a *API) githubToken() (string, error) {
	var encrypted string
	if err := a.db.QueryRow(`SELECT access_token FROM github_oauth_accounts WHERE id=1`).Scan(&encrypted); err != nil {
		return "", err
	}
	return a.decrypt(encrypted)
}

// githubEndpoint derives a GitHub REST endpoint from the configured /user URL.
func (a *API) githubEndpoint(path string) (string, error) {
	u, err := url.Parse(a.githubOAuth.APIURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", err
	}
	p, err := url.Parse(path)
	if err != nil {
		return "", err
	}
	u.Path = strings.TrimSuffix(u.Path, "/user") + p.Path
	u.RawQuery = p.RawQuery
	return u.String(), nil
}

func (a *API) githubRequest(method, endpoint string, body io.Reader) (*http.Response, error) {
	token, err := a.githubToken()
	if err != nil || token == "" {
		if err == nil {
			err = errors.New("empty github token")
		}
		return nil, err
	}
	req, err := http.NewRequest(method, endpoint, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return a.httpClient.Do(req)
}

type githubRepo struct {
	Name          string `json:"name"`
	FullName      string `json:"full_name"`
	Owner         string `json:"owner"`
	Private       bool   `json:"private"`
	DefaultBranch string `json:"default_branch"`
	HTMLURL       string `json:"html_url"`
}

// GitHub represents repository.owner as an object; the API intentionally
// exposes only its login so no unrelated account metadata leaks to clients.
func (repo *githubRepo) UnmarshalJSON(data []byte) error {
	var raw struct {
		Name          string          `json:"name"`
		FullName      string          `json:"full_name"`
		Owner         json.RawMessage `json:"owner"`
		Private       bool            `json:"private"`
		DefaultBranch string          `json:"default_branch"`
		HTMLURL       string          `json:"html_url"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	repo.Name, repo.FullName = raw.Name, raw.FullName
	var owner struct {
		Login string `json:"login"`
	}
	if json.Unmarshal(raw.Owner, &owner) == nil && owner.Login != "" {
		repo.Owner = owner.Login
	} else {
		_ = json.Unmarshal(raw.Owner, &repo.Owner) // tolerate simple test doubles
	}
	repo.Private, repo.DefaultBranch, repo.HTMLURL = raw.Private, raw.DefaultBranch, raw.HTMLURL
	return nil
}

func (a *API) githubAccountLogin() (string, error) {
	var login string
	err := a.db.QueryRow(`SELECT login FROM github_oauth_accounts WHERE id=1`).Scan(&login)
	return login, err
}

func (a *API) githubRepos(w http.ResponseWriter, r *http.Request) {
	endpoint, err := a.githubEndpoint("/user/repos?per_page=100&affiliation=owner")
	if err != nil {
		respondError(w, http.StatusServiceUnavailable, "github_oauth_not_configured")
		return
	}
	resp, err := a.githubRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		respondError(w, http.StatusConflict, "github_not_connected")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		respondError(w, http.StatusBadGateway, "github_api_failed")
		return
	}
	var repos []githubRepo
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&repos); err != nil {
		respondError(w, http.StatusBadGateway, "github_api_failed")
		return
	}
	login, _ := a.githubAccountLogin()
	for i := range repos {
		if repos[i].Owner == "" {
			repos[i].Owner = login
		}
	}
	respond(w, http.StatusOK, repos)
}

type githubCreateRepoInput struct {
	Owner       string `json:"owner"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Private     bool   `json:"private"`
	AutoInit    bool   `json:"auto_init"`
}

func (a *API) githubCreateRepo(w http.ResponseWriter, r *http.Request) {
	var input githubCreateRepoInput
	if decode(r, &input) != nil || (input.Owner != "" && !githubName.MatchString(input.Owner)) || !githubName.MatchString(input.Name) {
		respondError(w, http.StatusBadRequest, "invalid_github_repo_request")
		return
	}
	login, err := a.githubAccountLogin()
	if err != nil {
		respondError(w, http.StatusConflict, "github_not_connected")
		return
	}
	if input.Owner != "" && !strings.EqualFold(input.Owner, login) {
		respondError(w, http.StatusForbidden, "github_owner_not_allowed")
		return
	}
	endpoint, err := a.githubEndpoint("/user/repos")
	if err != nil {
		respondError(w, http.StatusServiceUnavailable, "github_oauth_not_configured")
		return
	}
	payload, _ := json.Marshal(struct {
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
		Private     bool   `json:"private"`
		AutoInit    bool   `json:"auto_init"`
	}{input.Name, input.Description, input.Private, input.AutoInit})
	resp, err := a.githubRequest(http.MethodPost, endpoint, strings.NewReader(string(payload)))
	if err != nil {
		respondError(w, http.StatusConflict, "github_not_connected")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		respondError(w, http.StatusBadGateway, "github_api_failed")
		return
	}
	var repo githubRepo
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&repo); err != nil {
		respondError(w, http.StatusBadGateway, "github_api_failed")
		return
	}
	if repo.Owner == "" {
		repo.Owner = login
	}
	respond(w, http.StatusCreated, repo)
}

func (a *API) githubBranches(w http.ResponseWriter, r *http.Request, owner, repo string) {
	login, err := a.githubAccountLogin()
	if err != nil {
		respondError(w, http.StatusConflict, "github_not_connected")
		return
	}
	if !githubName.MatchString(owner) || !githubName.MatchString(repo) || !strings.EqualFold(owner, login) {
		respondError(w, http.StatusForbidden, "github_owner_not_allowed")
		return
	}
	endpoint, err := a.githubEndpoint("/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/branches")
	if err != nil {
		respondError(w, http.StatusServiceUnavailable, "github_oauth_not_configured")
		return
	}
	resp, err := a.githubRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		respondError(w, http.StatusConflict, "github_not_connected")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		respondError(w, http.StatusBadGateway, "github_api_failed")
		return
	}
	var branches []struct {
		Name      string `json:"name"`
		Protected bool   `json:"protected"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&branches); err != nil {
		respondError(w, http.StatusBadGateway, "github_api_failed")
		return
	}
	respond(w, http.StatusOK, branches)
}
