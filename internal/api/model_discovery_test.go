package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestDiscoverModelsNormalizesOpenAIAndReusesCredential(t *testing.T) {
	api, _, _ := testAPI(t)
	created := request(t, api, http.MethodPost, "/api/v1/models", strings.NewReader(`{"name":"saved","provider":"openai","model":"old","api_key":"secret","base_url":"https://provider.test/v1"}`), "application/json")
	if created.Code != http.StatusCreated {
		t.Fatalf("create model: %d %s", created.Code, created.Body.String())
	}
	var config map[string]any
	if err := json.Unmarshal(created.Body.Bytes(), &config); err != nil {
		t.Fatal(err)
	}
	api.httpClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://provider.test/v1/models" || r.Header.Get("Authorization") != "Bearer secret" {
			t.Fatalf("unexpected provider request: %s auth=%q", r.URL, r.Header.Get("Authorization"))
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"gpt-a"},{"id":"gpt-b"}]}`))}, nil
	})}
	recorder := request(t, api, http.MethodPost, "/api/v1/models/discover", strings.NewReader(`{"provider":"openai","model_id":"`+config["id"].(string)+`"}`), "application/json")
	if recorder.Code != http.StatusOK || strings.Contains(recorder.Body.String(), "secret") {
		t.Fatalf("discovery failed or leaked credential: %d %s", recorder.Code, recorder.Body.String())
	}
	var models []discoveredModel
	if err := json.Unmarshal(recorder.Body.Bytes(), &models); err != nil || len(models) != 2 || models[0].Name != "gpt-a" {
		t.Fatalf("unexpected models: %s", recorder.Body.String())
	}
}

func TestDiscoverGeminiFiltersUnsupportedModels(t *testing.T) {
	api, _, _ := testAPI(t)
	api.httpClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v1beta/models" || r.URL.Query().Get("key") != "gem-key" || r.Header.Get("Authorization") != "" {
			t.Fatalf("unexpected Gemini request: %s", r.URL)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"models":[{"name":"models/gemini-ok","displayName":"Gemini OK","supportedGenerationMethods":["generateContent"]},{"name":"models/embed","displayName":"Embed","supportedGenerationMethods":["embedContent"]}]}`))}, nil
	})}
	recorder := request(t, api, http.MethodPost, "/api/v1/models/discover", strings.NewReader(`{"provider":"gemini","api_key":"gem-key","base_url":"https://generativelanguage.googleapis.com"}`), "application/json")
	if recorder.Code != http.StatusOK {
		t.Fatalf("discovery: %d %s", recorder.Code, recorder.Body.String())
	}
	var models []discoveredModel
	if err := json.Unmarshal(recorder.Body.Bytes(), &models); err != nil || len(models) != 1 || models[0].ID != "gemini-ok" || models[0].Name != "Gemini OK" {
		t.Fatalf("unexpected models: %s", recorder.Body.String())
	}
}
