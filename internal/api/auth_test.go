package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func authenticatedAPI(t *testing.T) *API {
	t.Helper()
	a, _, _ := testAPI(t)
	hash, err := bcrypt.GenerateFromPassword([]byte("correct horse battery staple"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	a.authEnabled = true
	a.adminUsername = "admin"
	a.adminPasswordHash = string(hash)
	a.sessionTTL = time.Hour
	a.allowedOrigins = []string{"http://localhost:3000"}
	return a
}

func authRequest(method, path, body string) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Airipress-Request", "1")
	r.Header.Set("Origin", "http://localhost:3000")
	return r
}

func loginCookie(t *testing.T, a *API) *http.Cookie {
	t.Helper()
	recorder := httptest.NewRecorder()
	a.ServeHTTP(recorder, authRequest(http.MethodPost, "/api/v1/auth/login", `{"username":"admin","password":"correct horse battery staple"}`))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("login failed: %d %s", recorder.Code, recorder.Body.String())
	}
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected one session cookie, got %d", len(cookies))
	}
	return cookies[0]
}

func TestSessionLoginCookieAndLogout(t *testing.T) {
	a := authenticatedAPI(t)
	cookie := loginCookie(t, a)
	if cookie.Name != "airipress_session" || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" || cookie.MaxAge <= 0 || cookie.Domain != "" {
		t.Fatalf("unsafe session cookie: %#v", cookie)
	}

	sessionRequest := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
	sessionRequest.AddCookie(cookie)
	sessionRecorder := httptest.NewRecorder()
	a.ServeHTTP(sessionRecorder, sessionRequest)
	if sessionRecorder.Code != http.StatusOK || !strings.Contains(sessionRecorder.Body.String(), `"username":"admin"`) {
		t.Fatalf("session check failed: %d %s", sessionRecorder.Code, sessionRecorder.Body.String())
	}

	logoutRequest := authRequest(http.MethodDelete, "/api/v1/auth/session", "")
	logoutRequest.AddCookie(cookie)
	logoutRecorder := httptest.NewRecorder()
	a.ServeHTTP(logoutRecorder, logoutRequest)
	if logoutRecorder.Code != http.StatusNoContent || len(logoutRecorder.Result().Cookies()) != 1 || logoutRecorder.Result().Cookies()[0].MaxAge >= 0 {
		t.Fatalf("logout did not clear cookie: %d %#v", logoutRecorder.Code, logoutRecorder.Result().Cookies())
	}
}

func TestLoginRejectsBadCredentialsAndCSRF(t *testing.T) {
	a := authenticatedAPI(t)
	withoutMarker := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"correct horse battery staple"}`))
	recorder := httptest.NewRecorder()
	a.ServeHTTP(recorder, withoutMarker)
	if recorder.Code != http.StatusForbidden || !strings.Contains(recorder.Body.String(), "csrf_required") {
		t.Fatalf("login accepted without CSRF marker: %d %s", recorder.Code, recorder.Body.String())
	}

	bad := authRequest(http.MethodPost, "/api/v1/auth/login", `{"username":"missing","password":"wrong"}`)
	bad.RemoteAddr = "192.0.2.10:12345"
	badRecorder := httptest.NewRecorder()
	a.ServeHTTP(badRecorder, bad)
	if badRecorder.Code != http.StatusUnauthorized || !strings.Contains(badRecorder.Body.String(), "invalid_credentials") {
		t.Fatalf("unexpected bad-login response: %d %s", badRecorder.Code, badRecorder.Body.String())
	}
}

func TestAuthenticatedWritesRequireRequestMarker(t *testing.T) {
	a := authenticatedAPI(t)
	cookie := loginCookie(t, a)
	write := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces", strings.NewReader(`{"name":"private"}`))
	write.Header.Set("Content-Type", "application/json")
	write.AddCookie(cookie)
	recorder := httptest.NewRecorder()
	a.ServeHTTP(recorder, write)
	if recorder.Code != http.StatusForbidden || !strings.Contains(recorder.Body.String(), "csrf_required") {
		t.Fatalf("write accepted without request marker: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestGitHubOAuthStartSupportsRegisteredCallbackPath(t *testing.T) {
	a := authenticatedAPI(t)
	a.githubOAuth.ClientID = "client-id"
	a.githubOAuth.RedirectURL = "http://127.0.0.1:3000/api/v1/github/callback"
	a.githubOAuth.AuthorizeURL = "https://github.com/login/oauth/authorize"
	cookie := loginCookie(t, a)
	r := httptest.NewRequest(http.MethodGet, "/api/v1/github/start", nil)
	r.AddCookie(cookie)
	recorder := httptest.NewRecorder()
	a.ServeHTTP(recorder, r)
	if recorder.Code != http.StatusFound || !strings.Contains(recorder.Header().Get("Location"), "redirect_uri=http%3A%2F%2F127.0.0.1%3A3000%2Fapi%2Fv1%2Fgithub%2Fcallback") {
		t.Fatalf("unexpected GitHub OAuth redirect: %d %s", recorder.Code, recorder.Header().Get("Location"))
	}
}
