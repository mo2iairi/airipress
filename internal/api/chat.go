package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/mo2iairi/airipress/internal/store"
)

type messageRecord struct {
	ID        string           `json:"id"`
	Role      string           `json:"role"`
	Content   string           `json:"content"`
	CreatedAt string           `json:"created_at"`
	Versions  []messageVersion `json:"versions,omitempty"`
}

func (a *API) workspaceMessages(workspaceID string) ([]messageRecord, error) {
	rows, err := a.db.Query(`SELECT m.id,m.role,m.content,m.created_at FROM messages m JOIN chats c ON c.id=m.chat_id WHERE c.workspace_id=? ORDER BY m.created_at,m.id`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []messageRecord{}
	for rows.Next() {
		var item messageRecord
		if err = rows.Scan(&item.ID, &item.Role, &item.Content, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (a *API) messages(w http.ResponseWriter, r *http.Request, workspaceID string) {
	if r.Method == http.MethodGet {
		items, err := a.workspaceMessages(workspaceID)
		if err != nil {
			respondError(w, http.StatusInternalServerError, "database_error")
			return
		}
		respond(w, http.StatusOK, items)
		return
	}
	if r.Method != http.MethodPost {
		respondError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	var input struct {
		ModelID string `json:"model_id"`
		Content string `json:"content"`
	}
	if decode(r, &input) != nil || strings.TrimSpace(input.Content) == "" || input.ModelID == "" {
		respondError(w, http.StatusBadRequest, "model_id_and_content_required")
		return
	}
	var chatID string
	err := a.db.QueryRow(`SELECT id FROM chats WHERE workspace_id=? ORDER BY created_at LIMIT 1`, workspaceID).Scan(&chatID)
	if err == sql.ErrNoRows {
		chatID = newID()
		if _, err = a.db.Exec(`INSERT INTO chats(id,workspace_id,title,created_at) VALUES(?,?,?,?)`, chatID, workspaceID, "Workspace chat", store.Now()); err != nil {
			respondError(w, http.StatusNotFound, "workspace_not_found")
			return
		}
	} else if err != nil {
		respondError(w, http.StatusInternalServerError, "database_error")
		return
	}
	userID, now := newID(), store.Now()
	if _, err = a.db.Exec(`INSERT INTO messages(id,chat_id,role,content,created_at) VALUES(?,?,?,?,?)`, userID, chatID, "user", strings.TrimSpace(input.Content), now); err != nil {
		respondError(w, http.StatusInternalServerError, "database_error")
		return
	}
	answer, err := a.complete(r.Context(), workspaceID, input.ModelID, strings.TrimSpace(input.Content))
	if err != nil {
		respondError(w, http.StatusBadGateway, "provider_request_failed")
		return
	}
	if _, err = a.db.Exec(`INSERT INTO messages(id,chat_id,role,content,created_at) VALUES(?,?,?,?,?)`, newID(), chatID, "assistant", answer, store.Now()); err != nil {
		respondError(w, http.StatusInternalServerError, "database_error")
		return
	}
	items, err := a.workspaceMessages(workspaceID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "database_error")
		return
	}
	respond(w, http.StatusCreated, items)
}

func (a *API) sourceContext(ctx context.Context, workspaceID string) string {
	rows, err := a.db.Query(`SELECT s.relative_path,f.mime,f.object_key FROM sources s JOIN files f ON f.id=s.file_id WHERE s.workspace_id=? ORDER BY s.relative_path`, workspaceID)
	if err != nil {
		return ""
	}
	defer rows.Close()
	var result strings.Builder
	for rows.Next() && result.Len() < 60_000 {
		var path, contentType, key string
		if rows.Scan(&path, &contentType, &key) != nil || strings.HasPrefix(contentType, "image/") {
			continue
		}
		reader, openErr := a.blobs.Open(ctx, key)
		if openErr != nil {
			continue
		}
		remaining := 60_000 - result.Len()
		data, _ := io.ReadAll(io.LimitReader(reader, int64(remaining)))
		reader.Close()
		result.WriteString("\n\n--- SOURCE: ")
		result.WriteString(path)
		result.WriteString(" ---\n")
		result.Write(data)
	}
	return result.String()
}

func (a *API) complete(ctx context.Context, workspaceID, modelID, prompt string) (string, error) {
	var provider, model, encryptedKey, baseURL string
	if err := a.db.QueryRow(`SELECT provider,model,api_key,base_url FROM models WHERE id=?`, modelID).Scan(&provider, &model, &encryptedKey, &baseURL); err != nil {
		return "", err
	}
	apiKey, err := a.decrypt(encryptedKey)
	if err != nil {
		return "", err
	}
	contextText := a.sourceContext(ctx, workspaceID)
	systemPrompt := "Answer using the workspace sources when relevant. If the sources do not support a claim, say so."
	if contextText != "" {
		systemPrompt += "\nWorkspace sources:" + contextText
	}
	provider = strings.ToLower(provider)
	if baseURL == "" {
		switch provider {
		case "gemini":
			baseURL = "https://generativelanguage.googleapis.com"
		case "deepseek":
			baseURL = "https://api.deepseek.com/v1"
		default:
			baseURL = "https://api.openai.com/v1"
		}
	}
	var endpoint string
	var payload any
	if provider == "gemini" {
		endpoint = strings.TrimRight(baseURL, "/") + "/v1beta/models/" + url.PathEscape(model) + ":generateContent?key=" + url.QueryEscape(apiKey)
		payload = map[string]any{
			"systemInstruction": map[string]any{"parts": []map[string]string{{"text": systemPrompt}}},
			"contents":          []map[string]any{{"role": "user", "parts": []map[string]string{{"text": prompt}}}},
		}
	} else {
		endpoint = strings.TrimRight(baseURL, "/") + "/chat/completions"
		payload = map[string]any{"model": model, "messages": []map[string]string{{"role": "system", "content": systemPrompt}, {"role": "user", "content": prompt}}}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if provider != "gemini" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 32<<10))
		return "", fmt.Errorf("provider returned %s", resp.Status)
	}
	var output struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&output); err != nil {
		return "", err
	}
	if len(output.Choices) > 0 && output.Choices[0].Message.Content != "" {
		return output.Choices[0].Message.Content, nil
	}
	if len(output.Candidates) > 0 && len(output.Candidates[0].Content.Parts) > 0 && output.Candidates[0].Content.Parts[0].Text != "" {
		return output.Candidates[0].Content.Parts[0].Text, nil
	}
	return "", fmt.Errorf("provider returned no text")
}
