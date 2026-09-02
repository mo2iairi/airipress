package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/mo2iairi/airipress/internal/store"
)

type discoveredModel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// discoverModels queries a provider without ever returning the credential or
// upstream response body. The response body is deliberately bounded because
// this endpoint accepts data from an external service.
func (a *API) discoverModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	var input struct {
		Provider string `json:"provider"`
		APIKey   string `json:"api_key"`
		BaseURL  string `json:"base_url"`
		ModelID  string `json:"model_id"`
	}
	if decode(r, &input) != nil || !validProvider(input.Provider) {
		respondError(w, http.StatusBadRequest, "invalid_model_config")
		return
	}
	provider := strings.ToLower(strings.TrimSpace(input.Provider))
	apiKey := strings.TrimSpace(input.APIKey)
	storedBaseURL := ""
	if apiKey == "" && strings.TrimSpace(input.ModelID) != "" {
		var encrypted string
		var storedProvider string
		if err := a.db.QueryRow(`SELECT provider,api_key,base_url FROM models WHERE id=?`, strings.TrimSpace(input.ModelID)).Scan(&storedProvider, &encrypted, &storedBaseURL); err != nil {
			respondError(w, http.StatusNotFound, "model_not_found")
			return
		}
		if strings.ToLower(strings.TrimSpace(storedProvider)) != provider {
			respondError(w, http.StatusBadRequest, "model_provider_mismatch")
			return
		}
		var err error
		apiKey, err = a.decrypt(encrypted)
		if err != nil {
			respondError(w, http.StatusInternalServerError, "credential_decryption_failed")
			return
		}
	}
	if apiKey == "" {
		respondError(w, http.StatusBadRequest, "api_key_required")
		return
	}
	base := strings.TrimRight(strings.TrimSpace(input.BaseURL), "/")
	if base == "" {
		base = strings.TrimRight(strings.TrimSpace(storedBaseURL), "/")
	}
	if base == "" {
		switch provider {
		case "openai":
			base = "https://api.openai.com/v1"
		case "deepseek":
			base = "https://api.deepseek.com/v1"
		case "gemini":
			base = "https://generativelanguage.googleapis.com"
		}
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		respondError(w, http.StatusBadRequest, "invalid_base_url")
		return
	}
	endpoint := base + "/models"
	if provider == "gemini" {
		endpoint = base + "/v1beta/models?key=" + url.QueryEscape(apiKey)
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, endpoint, nil)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_base_url")
		return
	}
	if provider != "gemini" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := a.httpClient.Do(req)
	if err != nil {
		respondError(w, http.StatusBadGateway, "provider_unavailable")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 2<<20))
		respondError(w, http.StatusBadGateway, "provider_error")
		return
	}
	const maxBody = 2 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil || len(body) > maxBody {
		respondError(w, http.StatusBadGateway, "provider_response_too_large")
		return
	}
	var models []discoveredModel
	if provider == "gemini" {
		var payload struct {
			Models []struct {
				Name        string   `json:"name"`
				DisplayName string   `json:"displayName"`
				Methods     []string `json:"supportedGenerationMethods"`
			} `json:"models"`
		}
		if err = json.Unmarshal(body, &payload); err != nil {
			respondError(w, http.StatusBadGateway, "invalid_provider_response")
			return
		}
		for _, item := range payload.Models {
			if item.Name == "" || (len(item.Methods) > 0 && !contains(item.Methods, "generateContent")) {
				continue
			}
			id := strings.TrimPrefix(item.Name, "models/")
			name := item.DisplayName
			if name == "" {
				name = id
			}
			models = append(models, discoveredModel{ID: id, Name: name})
		}
	} else {
		var payload struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err = json.Unmarshal(body, &payload); err != nil {
			respondError(w, http.StatusBadGateway, "invalid_provider_response")
			return
		}
		for _, item := range payload.Data {
			if item.ID != "" {
				models = append(models, discoveredModel{ID: item.ID, Name: item.ID})
			}
		}
	}
	respond(w, http.StatusOK, models)
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

type modelRecord struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	BaseURL   string `json:"base_url"`
	HasAPIKey bool   `json:"has_api_key"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

func scanModel(row interface{ Scan(...any) error }) (modelRecord, error) {
	var item modelRecord
	var has int
	err := row.Scan(&item.ID, &item.Name, &item.Provider, &item.Model, &item.BaseURL, &has, &item.CreatedAt, &item.UpdatedAt)
	item.HasAPIKey = has != 0
	return item, err
}

const modelProjection = `id,name,provider,model,base_url,CASE WHEN api_key='' THEN 0 ELSE 1 END,created_at,updated_at`

func validProvider(value string) bool {
	switch strings.ToLower(value) {
	case "openai", "deepseek", "gemini":
		return true
	default:
		return false
	}
}

func (a *API) models(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		rows, err := a.db.Query(`SELECT ` + modelProjection + ` FROM models ORDER BY created_at DESC`)
		if err != nil {
			respondError(w, http.StatusInternalServerError, "database_error")
			return
		}
		defer rows.Close()
		items := []modelRecord{}
		for rows.Next() {
			item, scanErr := scanModel(rows)
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
			Provider string `json:"provider"`
			Model    string `json:"model"`
			APIKey   string `json:"api_key"`
			BaseURL  string `json:"base_url"`
		}
		if decode(r, &input) != nil || !validProvider(input.Provider) || strings.TrimSpace(input.Model) == "" || strings.TrimSpace(input.Name) == "" {
			respondError(w, http.StatusBadRequest, "invalid_model_config")
			return
		}
		encrypted, err := a.encrypt(input.APIKey)
		if err != nil {
			respondError(w, http.StatusInternalServerError, "credential_encryption_failed")
			return
		}
		now, identifier := store.Now(), newID()
		_, err = a.db.Exec(`INSERT INTO models(id,name,provider,model,api_key,base_url,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, identifier, strings.TrimSpace(input.Name), strings.ToLower(input.Provider), strings.TrimSpace(input.Model), encrypted, strings.TrimSpace(input.BaseURL), now, now)
		if err != nil {
			respondError(w, http.StatusInternalServerError, "database_error")
			return
		}
		respond(w, http.StatusCreated, modelRecord{ID: identifier, Name: strings.TrimSpace(input.Name), Provider: strings.ToLower(input.Provider), Model: strings.TrimSpace(input.Model), BaseURL: strings.TrimSpace(input.BaseURL), HasAPIKey: input.APIKey != "", CreatedAt: now, UpdatedAt: now})
	default:
		respondError(w, http.StatusMethodNotAllowed, "method_not_allowed")
	}
}

func (a *API) model(w http.ResponseWriter, r *http.Request, identifier string) {
	if r.Method == http.MethodDelete {
		result, err := a.db.Exec(`DELETE FROM models WHERE id=?`, identifier)
		if err != nil {
			respondError(w, http.StatusConflict, "model_in_use")
			return
		}
		count, _ := result.RowsAffected()
		if count == 0 {
			respondError(w, http.StatusNotFound, "model_not_found")
			return
		}
		respond(w, http.StatusNoContent, nil)
		return
	}
	current, err := scanModel(a.db.QueryRow(`SELECT `+modelProjection+` FROM models WHERE id=?`, identifier))
	if err != nil {
		respondError(w, http.StatusNotFound, "model_not_found")
		return
	}
	if r.Method == http.MethodGet {
		respond(w, http.StatusOK, current)
		return
	}
	if r.Method != http.MethodPatch {
		respondError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	var input struct {
		Name     *string `json:"name"`
		Provider *string `json:"provider"`
		Model    *string `json:"model"`
		APIKey   *string `json:"api_key"`
		BaseURL  *string `json:"base_url"`
	}
	if decode(r, &input) != nil {
		respondError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if input.Name != nil {
		current.Name = strings.TrimSpace(*input.Name)
	}
	if input.Provider != nil {
		current.Provider = strings.ToLower(*input.Provider)
	}
	if input.Model != nil {
		current.Model = strings.TrimSpace(*input.Model)
	}
	if input.BaseURL != nil {
		current.BaseURL = strings.TrimSpace(*input.BaseURL)
	}
	if current.Name == "" || current.Model == "" || !validProvider(current.Provider) {
		respondError(w, http.StatusBadRequest, "invalid_model_config")
		return
	}
	var encrypted string
	if err = a.db.QueryRow(`SELECT api_key FROM models WHERE id=?`, identifier).Scan(&encrypted); err != nil {
		respondError(w, http.StatusNotFound, "model_not_found")
		return
	}
	if input.APIKey != nil {
		encrypted, err = a.encrypt(*input.APIKey)
		if err != nil {
			respondError(w, http.StatusInternalServerError, "credential_encryption_failed")
			return
		}
		current.HasAPIKey = *input.APIKey != ""
	}
	current.UpdatedAt = store.Now()
	_, err = a.db.Exec(`UPDATE models SET name=?,provider=?,model=?,api_key=?,base_url=?,updated_at=? WHERE id=?`, current.Name, current.Provider, current.Model, encrypted, current.BaseURL, current.UpdatedAt, identifier)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "database_error")
		return
	}
	respond(w, http.StatusOK, current)
}
