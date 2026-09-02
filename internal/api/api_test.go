package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mo2iairi/airipress/internal/store"
)

func testAPI(t *testing.T) (*API, *store.Store, string) {
	t.Helper()
	root := t.TempDir()
	db, err := store.Open("file:" + filepath.Join(root, "test.db") + "?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	api := newAPI(db, &store.LocalBlobStore{Root: root}, strings.Repeat("k", 32))
	api.dataRoot = root
	return api, db, root
}

func request(t *testing.T, handler http.Handler, method, path string, body io.Reader, contentType string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, body)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func createWorkspace(t *testing.T, api *API, name string) string {
	t.Helper()
	recorder := request(t, api, http.MethodPost, "/api/v1/workspaces", strings.NewReader(`{"name":"`+name+`"}`), "application/json")
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create workspace: %d %s", recorder.Code, recorder.Body.String())
	}
	var result map[string]any
	_ = json.Unmarshal(recorder.Body.Bytes(), &result)
	return result["id"].(string)
}

func upload(t *testing.T, api *API, workspaceID, name, content string) map[string]any {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(part, content)
	_ = writer.Close()
	recorder := request(t, api, http.MethodPost, "/api/v1/workspaces/"+workspaceID+"/sources", &body, writer.FormDataContentType())
	if recorder.Code != http.StatusCreated {
		t.Fatalf("upload: %d %s", recorder.Code, recorder.Body.String())
	}
	var result map[string]any
	_ = json.Unmarshal(recorder.Body.Bytes(), &result)
	return result
}

func TestContentDeduplicationAcrossWorkspaces(t *testing.T) {
	api, db, root := testAPI(t)
	first := createWorkspace(t, api, "one")
	second := createWorkspace(t, api, "two")
	one := upload(t, api, first, "note.md", "# shared\n")
	two := upload(t, api, second, "copy.md", "# shared\n")
	if one["file_id"] != two["file_id"] {
		t.Fatalf("expected shared file id, got %v and %v", one["file_id"], two["file_id"])
	}
	var files, sources int
	_ = db.QueryRow(`SELECT COUNT(*) FROM files`).Scan(&files)
	_ = db.QueryRow(`SELECT COUNT(*) FROM sources`).Scan(&sources)
	if files != 1 || sources != 2 {
		t.Fatalf("files=%d sources=%d", files, sources)
	}
	digest := one["sha256"].(string)
	objectPath := filepath.Join(root, ".meta", "objects", "sha256", digest[:2], digest, "content")
	if _, err := os.Stat(objectPath); err != nil {
		t.Fatalf("content-addressed object missing: %v", err)
	}
}

func TestGlobalFileListAndWorkspaceReference(t *testing.T) {
	api, db, _ := testAPI(t)
	first := createWorkspace(t, api, "library")
	second := createWorkspace(t, api, "reference")
	uploaded := upload(t, api, first, "guide.md", "# guide\n")

	listed := request(t, api, http.MethodGet, "/api/v1/files", nil, "")
	if listed.Code != http.StatusOK {
		t.Fatalf("list files: %d %s", listed.Code, listed.Body.String())
	}
	var files []map[string]any
	if err := json.Unmarshal(listed.Body.Bytes(), &files); err != nil || len(files) != 1 {
		t.Fatalf("unexpected file list: %v %s", err, listed.Body.String())
	}
	if files[0]["id"] != uploaded["file_id"] || files[0]["source_count"] != float64(1) {
		t.Fatalf("unexpected listed file: %#v", files[0])
	}

	attached := request(t, api, http.MethodPost, "/api/v1/workspaces/"+second+"/sources", strings.NewReader(`{"file_id":"`+uploaded["file_id"].(string)+`","relative_path":"notes/guide.md"}`), "application/json")
	if attached.Code != http.StatusCreated {
		t.Fatalf("attach existing file: %d %s", attached.Code, attached.Body.String())
	}
	var fileCount, sourceCount int
	_ = db.QueryRow(`SELECT COUNT(*) FROM files`).Scan(&fileCount)
	_ = db.QueryRow(`SELECT COUNT(*) FROM sources`).Scan(&sourceCount)
	if fileCount != 1 || sourceCount != 2 {
		t.Fatalf("files=%d sources=%d", fileCount, sourceCount)
	}
}

func TestModelCredentialEncryptedAndNeverReturned(t *testing.T) {
	api, db, _ := testAPI(t)
	payload := `{"name":"primary","provider":"openai","model":"gpt-test","api_key":"top-secret","base_url":"https://example.invalid/v1"}`
	recorder := request(t, api, http.MethodPost, "/api/v1/models", strings.NewReader(payload), "application/json")
	if recorder.Code != http.StatusCreated || strings.Contains(recorder.Body.String(), "top-secret") {
		t.Fatalf("unsafe model response: %d %s", recorder.Code, recorder.Body.String())
	}
	var encrypted string
	_ = db.QueryRow(`SELECT api_key FROM models`).Scan(&encrypted)
	if encrypted == "" || encrypted == "top-secret" {
		t.Fatalf("credential was not encrypted")
	}
	plain, err := api.decrypt(encrypted)
	if err != nil || plain != "top-secret" {
		t.Fatalf("credential cannot be decrypted: %q %v", plain, err)
	}
}

func TestOpenAICompatibleAndGeminiRequests(t *testing.T) {
	api, _, _ := testAPI(t)
	workspaceID := createWorkspace(t, api, "chat")
	received := make(chan string, 6)
	api.httpClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(r.Body)
		received <- r.URL.String() + "\n" + r.Header.Get("Authorization") + "\n" + string(body)
		responseBody := `{"choices":[{"message":{"content":"compatible answer"}}]}`
		if strings.Contains(r.URL.Path, "generateContent") {
			responseBody = `{"candidates":[{"content":{"parts":[{"text":"gemini answer"}]}}]}`
		}
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: make(http.Header), Body: io.NopCloser(strings.NewReader(responseBody))}, nil
	})}
	for _, kind := range []string{"openai", "deepseek", "gemini"} {
		t.Run(kind, func(t *testing.T) {
			modelBody := fmt.Sprintf(`{"name":"%s","provider":"%s","model":"test-model","api_key":"key-%s","base_url":"https://provider.test"}`, kind, kind, kind)
			created := request(t, api, http.MethodPost, "/api/v1/models", strings.NewReader(modelBody), "application/json")
			if created.Code != http.StatusCreated {
				t.Fatalf("model: %d %s", created.Code, created.Body.String())
			}
			var model map[string]any
			_ = json.Unmarshal(created.Body.Bytes(), &model)
			chatBody := fmt.Sprintf(`{"model_id":%q,"content":"question"}`, model["id"].(string))
			chat := request(t, api, http.MethodPost, "/api/v1/workspaces/"+workspaceID+"/messages", strings.NewReader(chatBody), "application/json")
			if chat.Code != http.StatusCreated {
				t.Fatalf("chat: %d %s", chat.Code, chat.Body.String())
			}
			requestDump := <-received
			if kind == "gemini" {
				if !strings.Contains(requestDump, ":generateContent?key=key-") || !strings.Contains(requestDump, "systemInstruction") {
					t.Fatalf("invalid Gemini request: %s", requestDump)
				}
			} else if !strings.Contains(requestDump, "Bearer key-"+kind) || !strings.Contains(requestDump, "/chat/completions") {
				t.Fatalf("invalid compatible request: %s", requestDump)
			}
		})
	}
}

func TestAuthentication(t *testing.T) {
	api, _, _ := testAPI(t)
	api.authEnabled = true
	api.adminUsername = "admin"
	api.adminPasswordHash = "$2b$12$LQv3c1yqBWiwLfx5F4cA8uJm0h0jM6c9gZ2t4R7zX5vP1nQ6kL8eS"
	api.sessionTTL = time.Hour
	unauthorized := request(t, api, http.MethodGet, "/api/v1/workspaces", nil, "")
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", unauthorized.Code)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces", nil)
	req.Header.Set("Authorization", "Bearer private-token")
	recorder := httptest.NewRecorder()
	api.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("bearer token bypassed authentication: %d", recorder.Code)
	}
}

func TestPublishAcceptsOnlyStructuredConfigAndDoesNotPersistToken(t *testing.T) {
	api, db, _ := testAPI(t)
	api.publishFunc = func(string, string, publishInput) {}
	workspaceID := createWorkspace(t, api, "publish")
	unsafe := request(t, api, http.MethodPost, "/api/v1/workspaces/"+workspaceID+"/publish", strings.NewReader(`{"command":"rm -rf anything"}`), "application/json")
	if unsafe.Code != http.StatusBadRequest {
		t.Fatalf("arbitrary command accepted: %d", unsafe.Code)
	}
	valid := request(t, api, http.MethodPost, "/api/v1/workspaces/"+workspaceID+"/publish", strings.NewReader(`{"theme":"astro-default","target":"github-pages","owner":"owner","repo":"repo","branch":"gh-pages","token":"github-secret"}`), "application/json")
	if valid.Code != http.StatusAccepted {
		t.Fatalf("valid publish rejected: %d %s", valid.Code, valid.Body.String())
	}
	var config string
	if err := db.QueryRow(`SELECT config FROM deploy_jobs`).Scan(&config); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(config, "github-secret") {
		t.Fatal("GitHub token persisted in job config")
	}
}
