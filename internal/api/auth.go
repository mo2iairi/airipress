package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v3"
)

type authConfigFile struct {
	Version int `yaml:"version"`
	Auth    struct {
		Admin struct {
			Username     string `yaml:"username"`
			PasswordHash string `yaml:"password_hash"`
		} `yaml:"admin"`
		Session struct {
			TTL          string `yaml:"ttl"`
			CookieSecure bool   `yaml:"cookie_secure"`
		} `yaml:"session"`
		GitHub githubOAuthConfig `yaml:"github"`
	} `yaml:"auth"`
	Server struct {
		AllowedOrigins []string `yaml:"allowed_origins"`
	} `yaml:"server"`
	GitHub githubOAuthConfig `yaml:"github"`
	Themes []themeConfig     `yaml:"themes"`
}
type githubOAuthConfig struct {
	ClientID     string `yaml:"client_id"`
	ClientSecret string `yaml:"client_secret"`
	RedirectURL  string `yaml:"redirect_url"`
	AuthorizeURL string `yaml:"authorize_url"`
	TokenURL     string `yaml:"token_url"`
	APIURL       string `yaml:"api_url"`
}
type themeConfig struct {
	ID          string `yaml:"id"`
	Name        string `yaml:"name"`
	Engine      string `yaml:"engine"`
	Repository  string `yaml:"repository"`
	Ref         string `yaml:"ref"`
	PreviewURL  string `yaml:"preview_url"`
	Description string `yaml:"description"`
}

var adminUsernamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

func loadAuthConfig() (string, string, time.Duration, bool, []string, githubOAuthConfig, []themeConfig, error) {
	var c authConfigFile
	p := os.Getenv("AIRIPRESS_CONFIG_FILE")
	if p == "" {
		return "", "", 0, false, nil, githubOAuthConfig{}, nil, errors.New("AIRIPRESS_CONFIG_FILE is required")
	}
	b, e := os.ReadFile(p)
	if e != nil {
		return "", "", 0, false, nil, githubOAuthConfig{}, nil, e
	}
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true)
	if e = dec.Decode(&c); e != nil {
		return "", "", 0, false, nil, githubOAuthConfig{}, nil, e
	}
	if c.Version != 1 {
		return "", "", 0, false, nil, githubOAuthConfig{}, nil, errors.New("config version must be 1")
	}
	u, h := c.Auth.Admin.Username, c.Auth.Admin.PasswordHash
	if v := os.Getenv("AIRIPRESS_ADMIN_USERNAME"); v != "" {
		u = v
	}
	if v := os.Getenv("AIRIPRESS_ADMIN_PASSWORD_HASH"); v != "" {
		h = v
	}
	if !adminUsernamePattern.MatchString(u) {
		return "", "", 0, false, nil, githubOAuthConfig{}, nil, errors.New("auth.admin.username must match [A-Za-z0-9._-]{1,64}")
	}
	cost, e := bcrypt.Cost([]byte(h))
	if e != nil || cost != 12 {
		return "", "", 0, false, nil, githubOAuthConfig{}, nil, errors.New("auth.admin.password_hash must be a cost-12 bcrypt hash")
	}
	ttl := 24 * time.Hour
	if c.Auth.Session.TTL != "" {
		d, parseErr := time.ParseDuration(c.Auth.Session.TTL)
		if parseErr != nil || d < time.Minute || d > 30*24*time.Hour {
			return "", "", 0, false, nil, githubOAuthConfig{}, nil, errors.New("auth.session.ttl must be between 1m and 720h")
		}
		ttl = d
	}
	if v := os.Getenv("AIRIPRESS_SESSION_TTL"); v != "" {
		d, parseErr := time.ParseDuration(v)
		if parseErr != nil || d < time.Minute || d > 30*24*time.Hour {
			return "", "", 0, false, nil, githubOAuthConfig{}, nil, errors.New("AIRIPRESS_SESSION_TTL must be a duration no longer than 720h")
		}
		ttl = d
	}
	secure := c.Auth.Session.CookieSecure || strings.EqualFold(os.Getenv("AIRIPRESS_COOKIE_SECURE"), "true")
	origins := c.Server.AllowedOrigins
	if v := os.Getenv("AIRIPRESS_ALLOWED_ORIGINS"); v != "" {
		origins = nil
		for _, x := range strings.Split(v, ",") {
			if x = strings.TrimSpace(x); x != "" {
				origins = append(origins, x)
			}
		}
	}
	for _, origin := range origins {
		parsed, parseErr := url.Parse(origin)
		if parseErr != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return "", "", 0, false, nil, githubOAuthConfig{}, nil, fmt.Errorf("invalid allowed origin %q", origin)
		}
	}
	if c.GitHub.ClientID == "" && c.GitHub.ClientSecret == "" && c.GitHub.RedirectURL == "" {
		c.GitHub = c.Auth.GitHub
	}
	if c.GitHub.AuthorizeURL == "" {
		c.GitHub.AuthorizeURL = "https://github.com/login/oauth/authorize"
	}
	if c.GitHub.TokenURL == "" {
		c.GitHub.TokenURL = "https://github.com/login/oauth/access_token"
	}
	if c.GitHub.APIURL == "" {
		c.GitHub.APIURL = "https://api.github.com/user"
	}
	seenThemes := map[string]bool{}
	for i := range c.Themes {
		t := &c.Themes[i]
		t.ID = strings.TrimSpace(t.ID)
		t.Name = strings.TrimSpace(t.Name)
		t.Engine = strings.ToLower(strings.TrimSpace(t.Engine))
		t.Repository = strings.TrimSpace(t.Repository)
		t.Ref = strings.TrimSpace(t.Ref)
		if !regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`).MatchString(t.ID) || (t.Engine != "astro" && t.Engine != "hugo") || !regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`).MatchString(t.Repository) || t.Ref == "" || seenThemes[t.ID] {
			return "", "", 0, false, nil, githubOAuthConfig{}, nil, errors.New("invalid themes catalog entry")
		}
		seenThemes[t.ID] = true
		if t.Name == "" {
			t.Name = t.ID
		}
		if t.PreviewURL != "" {
			parsed, err := url.Parse(t.PreviewURL)
			if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
				return "", "", 0, false, nil, githubOAuthConfig{}, nil, errors.New("invalid theme preview_url")
			}
		}
	}
	return u, h, ttl, secure, origins, c.GitHub, c.Themes, nil
}

type sessionClaims struct {
	Version int    `json:"v"`
	User    string `json:"u"`
	Issued  int64  `json:"iat"`
	Exp     int64  `json:"e"`
	Hash    string `json:"h"`
}

var dummyHash = []byte("$2b$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy")

func (a *API) sessionKey() []byte {
	h := hmac.New(sha256.New, []byte(a.masterKey))
	h.Write([]byte("airipress/session/v1"))
	return h.Sum(nil)
}
func (a *API) signSession(c sessionClaims) string {
	b, _ := json.Marshal(c)
	mac := hmac.New(sha256.New, a.sessionKey())
	mac.Write(b)
	return base64.RawURLEncoding.EncodeToString(b) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func (a *API) authenticated(r *http.Request) bool {
	c, e := a.readSession(r)
	now := time.Now().Unix()
	return e == nil && c.Version == 1 && c.Issued <= now+60 && c.Exp > now && c.User == a.adminUsername && c.Hash == hashPasswordMarker(a.adminPasswordHash)
}
func hashPasswordMarker(s string) string {
	h := sha256.Sum256([]byte(s))
	return base64.RawURLEncoding.EncodeToString(h[:])
}
func (a *API) readSession(r *http.Request) (sessionClaims, error) {
	var c sessionClaims
	ck, e := r.Cookie("airipress_session")
	if e != nil {
		return c, e
	}
	if len(ck.Value) > 4096 {
		return c, os.ErrInvalid
	}
	p := strings.Split(ck.Value, ".")
	if len(p) != 2 {
		return c, os.ErrInvalid
	}
	b, e := base64.RawURLEncoding.DecodeString(p[0])
	if e != nil {
		return c, e
	}
	sig, e := base64.RawURLEncoding.DecodeString(p[1])
	if e != nil {
		return c, e
	}
	mac := hmac.New(sha256.New, a.sessionKey())
	mac.Write(b)
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return c, os.ErrPermission
	}
	e = json.Unmarshal(b, &c)
	return c, e
}
func noStore(w http.ResponseWriter) { w.Header().Set("Cache-Control", "no-store") }
func (a *API) originAllowed(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	for _, x := range a.allowedOrigins {
		if o == x {
			return true
		}
	}
	return false
}

type loginAttempt struct {
	n     int
	since time.Time
}

var loginMu sync.Mutex
var loginAttempts = map[string]loginAttempt{}

func allowedLogin(ip string) bool {
	loginMu.Lock()
	defer loginMu.Unlock()
	v := loginAttempts[ip]
	now := time.Now()
	for k, x := range loginAttempts {
		if now.Sub(x.since) >= 5*time.Minute {
			delete(loginAttempts, k)
		}
	}
	if v.since.IsZero() || now.Sub(v.since) >= 5*time.Minute {
		loginAttempts[ip] = loginAttempt{since: now}
		return true
	}
	return v.n < 5
}
func recordLogin(ip string) {
	loginMu.Lock()
	defer loginMu.Unlock()
	v := loginAttempts[ip]
	v.n++
	if v.since.IsZero() {
		v.since = time.Now()
	}
	loginAttempts[ip] = v
}
func loginHost(r *http.Request) string {
	host, _, e := net.SplitHostPort(r.RemoteAddr)
	if e == nil {
		return host
	}
	return r.RemoteAddr
}

func (a *API) authRoute(w http.ResponseWriter, r *http.Request, parts []string) {
	noStore(w)
	if len(parts) == 2 && parts[0] == "github" {
		a.githubAuthRoute(w, r, parts[1:])
		return
	}
	if len(parts) != 1 {
		respondError(w, 404, "not_found")
		return
	}
	switch parts[0] {
	case "login":
		if r.Method != http.MethodPost {
			respondError(w, 405, "method_not_allowed")
			return
		}
		if r.Header.Get("X-Airipress-Request") != "1" {
			respondError(w, http.StatusForbidden, "csrf_required")
			return
		}
		if !a.originAllowed(r) {
			respondError(w, 403, "origin_not_allowed")
			return
		}
		ip := loginHost(r)
		if !allowedLogin(ip) {
			respondError(w, 429, "too_many_attempts")
			return
		}
		var in struct{ Username, Password string }
		if decode(r, &in) != nil {
			respondError(w, 400, "invalid_request")
			return
		}
		if len(in.Username) > 64 || len(in.Password) > 72 {
			recordLogin(ip)
			respondError(w, http.StatusUnauthorized, "invalid_credentials")
			return
		}
		ok := in.Username == a.adminUsername && bcrypt.CompareHashAndPassword([]byte(a.adminPasswordHash), []byte(in.Password)) == nil
		if in.Username != a.adminUsername {
			_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(in.Password))
		}
		if !ok {
			recordLogin(ip)
			respondError(w, 401, "invalid_credentials")
			return
		}
		loginMu.Lock()
		delete(loginAttempts, ip)
		loginMu.Unlock()
		c := sessionClaims{Version: 1, User: a.adminUsername, Issued: time.Now().Unix(), Exp: time.Now().Add(a.sessionTTL).Unix(), Hash: hashPasswordMarker(a.adminPasswordHash)}
		http.SetCookie(w, &http.Cookie{Name: "airipress_session", Value: a.signSession(c), Path: "/", HttpOnly: true, Secure: a.cookieSecure, SameSite: http.SameSiteStrictMode, MaxAge: int(a.sessionTTL / time.Second)})
		w.WriteHeader(204)
	case "session":
		if r.Method == http.MethodDelete && (r.Header.Get("X-Airipress-Request") != "1" || !a.originAllowed(r)) {
			respondError(w, http.StatusForbidden, "csrf_required")
			return
		}
		if r.Method == http.MethodGet && a.authenticated(r) {
			respond(w, 200, map[string]string{"username": a.adminUsername})
			return
		}
		if r.Method == http.MethodDelete && a.authenticated(r) {
			http.SetCookie(w, &http.Cookie{Name: "airipress_session", Value: "", Path: "/", HttpOnly: true, Secure: a.cookieSecure, SameSite: http.SameSiteStrictMode, MaxAge: -1})
			w.WriteHeader(204)
			return
		}
		respondError(w, 401, "unauthorized")
	default:
		respondError(w, 404, "not_found")
	}
}
