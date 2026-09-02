package api

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/mo2iairi/airipress/internal/store"
)

type API struct {
	db                *store.Store
	blobs             store.BlobStore
	httpClient        *http.Client
	masterKey         string
	authEnabled       bool
	adminUsername     string
	adminPasswordHash string
	sessionTTL        time.Duration
	cookieSecure      bool
	allowedOrigins    []string
	githubOAuth       githubOAuthConfig
	themeCatalog      []themeConfig
	oauthMu           sync.Mutex
	oauthStates       map[string]time.Time
	dataRoot          string
	toolsDir          string
	publishFunc       func(string, string, publishInput)
	archiveMu         sync.Mutex
	dataMu            sync.RWMutex
}

func NewChecked(db *store.Store) (*API, error) {
	secretFile := env("AIRIPRESS_SECRET_FILE", "config/secrets/airipress_secret")
	secret, err := os.ReadFile(secretFile)
	if err != nil || len(strings.TrimSpace(string(secret))) < 32 {
		return nil, errors.New("AIRIPRESS_SECRET_FILE must contain at least 32 bytes")
	}
	masterKey := strings.TrimSpace(string(secret))
	username, passwordHash, ttl, secure, origins, github, themes, err := loadAuthConfig()
	if err != nil {
		return nil, fmt.Errorf("load auth config: %w", err)
	}
	if username == "" || passwordHash == "" {
		return nil, errors.New("admin username and bcrypt password_hash are required")
	}
	dataRoot := env("AIRIPRESS_DATA_ROOT", "data")
	var blobs store.BlobStore = &store.LocalBlobStore{Root: dataRoot}
	if endpoint := os.Getenv("AIRIPRESS_S3_ENDPOINT"); endpoint != "" {
		u, err := url.Parse(endpoint)
		if err != nil || u.Host == "" {
			return nil, errors.New("invalid AIRIPRESS_S3_ENDPOINT")
		}
		bucket := os.Getenv("AIRIPRESS_S3_BUCKET")
		if bucket == "" {
			return nil, errors.New("AIRIPRESS_S3_BUCKET is required with S3 storage")
		}
		s3, err := store.NewS3BlobStore(endpoint, bucket, env("AIRIPRESS_S3_REGION", "auto"), os.Getenv("AIRIPRESS_S3_ACCESS_KEY"), os.Getenv("AIRIPRESS_S3_SECRET_KEY"), u.Scheme == "https")
		if err != nil {
			return nil, err
		}
		blobs = s3
	}
	a := newAPI(db, blobs, masterKey)
	a.authEnabled, a.adminUsername, a.adminPasswordHash, a.sessionTTL, a.cookieSecure, a.allowedOrigins, a.githubOAuth, a.themeCatalog = true, username, passwordHash, ttl, secure, origins, github, themes
	return a, nil
}

func newAPI(db *store.Store, blobs store.BlobStore, masterKey string) *API {
	api := &API{
		db: db, blobs: blobs, httpClient: &http.Client{Timeout: 10 * time.Minute},
		masterKey:   masterKey,
		dataRoot:    env("AIRIPRESS_DATA_ROOT", "data"),
		toolsDir:    env("AIRIPRESS_TOOLS_DIR", "tools"),
		oauthStates: make(map[string]time.Time),
	}
	api.publishFunc = api.runPublish
	return api
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func (a *API) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if origin != "" && a.originAllowed(r) {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set("Vary", "Origin")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Airipress-Request")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
	}
	if r.Method == http.MethodOptions {
		if origin != "" && !a.originAllowed(r) {
			respondError(w, http.StatusForbidden, "origin_not_allowed")
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	path := strings.Trim(r.URL.Path, "/")
	if path == "health" || path == "api/v1/health" {
		respond(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}
	if !strings.HasPrefix(path, "api/v1/") {
		respondError(w, http.StatusNotFound, "not_found")
		return
	}
	parts := strings.Split(strings.TrimPrefix(path, "api/v1/"), "/")
	// GitHub's registered callback is intentionally short and public. The
	// callback validates its one-time state cookie before exchanging the code.
	if len(parts) >= 2 && parts[0] == "github" {
		a.githubAuthRoute(w, r, parts[1:])
		return
	}
	if parts[0] == "auth" {
		a.authRoute(w, r, parts[1:])
		return
	}
	if a.authEnabled && !a.authenticated(r) {
		noStore(w)
		respondError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if a.authEnabled && r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get("X-Airipress-Request") != "1" {
		noStore(w)
		respondError(w, http.StatusForbidden, "csrf_required")
		return
	}
	if a.authEnabled && !a.originAllowed(r) {
		noStore(w)
		respondError(w, http.StatusForbidden, "origin_not_allowed")
		return
	}
	if len(parts) >= 2 && parts[0] == "data" && (parts[1] == "import" || parts[1] == "export") {
		a.route(w, r, parts)
		return
	}
	a.dataMu.RLock()
	defer a.dataMu.RUnlock()
	a.route(w, r, parts)
}

func (a *API) route(w http.ResponseWriter, r *http.Request, parts []string) {
	if len(parts) == 0 {
		respondError(w, http.StatusNotFound, "not_found")
		return
	}
	switch parts[0] {
	case "workspaces":
		if len(parts) == 1 {
			a.workspaces(w, r)
			return
		}
		if len(parts) == 2 {
			a.workspace(w, r, parts[1])
			return
		}
		switch parts[2] {
		case "chats":
			a.chats(w, r, parts[1], parts[3:])
		case "sources":
			a.sources(w, r, parts[1], parts[3:])
		case "messages":
			a.messages(w, r, parts[1])
		case "studio":
			if len(parts) == 4 && parts[3] == "mindmap" {
				a.mindmap(w, r, parts[1])
				return
			}
			respondError(w, http.StatusNotFound, "not_found")
		case "publish":
			a.publish(w, r, parts[1])
		default:
			respondError(w, http.StatusNotFound, "not_found")
		}
	case "models", "model-configs":
		if len(parts) == 1 {
			a.models(w, r)
		} else if len(parts) == 2 && parts[1] == "discover" {
			a.discoverModels(w, r)
		} else if len(parts) == 2 {
			a.model(w, r, parts[1])
		} else {
			respondError(w, http.StatusNotFound, "not_found")
		}
	case "themes":
		a.themes(w, r, parts[1:])
	case "assets", "files":
		a.assets(w, r)
	case "jobs":
		if len(parts) == 2 {
			a.job(w, r, parts[1])
		} else {
			respondError(w, http.StatusNotFound, "not_found")
		}
	case "data":
		if len(parts) == 2 && parts[1] == "export" {
			a.exportData(w, r)
			return
		}
		if len(parts) == 2 && parts[1] == "import" {
			a.importData(w, r)
			return
		}
	default:
		respondError(w, http.StatusNotFound, "not_found")
	}
}

func decode(r *http.Request, value any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	return decoder.Decode(value)
}

func respond(w http.ResponseWriter, status int, value any) {
	if status == http.StatusNoContent {
		w.WriteHeader(status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func respondError(w http.ResponseWriter, status int, code string) {
	respond(w, status, map[string]string{"error": code})
}

func newID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(raw[:])
}

func (a *API) encrypt(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	key := sha256.Sum256([]byte(a.masterKey))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.RawStdEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte(value), nil)), nil
}

func (a *API) decrypt(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	raw, err := base64.RawStdEncoding.DecodeString(value)
	if err != nil {
		return "", err
	}
	key := sha256.Sum256([]byte(a.masterKey))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(raw) < gcm.NonceSize() {
		return "", errors.New("invalid encrypted credential")
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	return string(plain), err
}
