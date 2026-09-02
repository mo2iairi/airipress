package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/mo2iairi/airipress/internal/store"
)

type chatRecord struct {
	ID        string `json:"id"`
	Workspace string `json:"workspace_id"`
	Title     string `json:"title"`
	CreatedAt string `json:"created_at"`
}

type messageVersion struct {
	ID       string `json:"id"`
	Content  string `json:"content"`
	Selected bool   `json:"selected"`
}

func (a *API) chats(w http.ResponseWriter, r *http.Request, workspaceID string, rest []string) {
	if len(rest) == 0 {
		switch r.Method {
		case http.MethodGet:
			rows, err := a.db.Query(`SELECT id,workspace_id,title,created_at FROM chats WHERE workspace_id=? ORDER BY created_at DESC,id DESC`, workspaceID)
			if err != nil {
				respondError(w, http.StatusInternalServerError, "database_error")
				return
			}
			defer rows.Close()
			items := []chatRecord{}
			for rows.Next() {
				var item chatRecord
				if err := rows.Scan(&item.ID, &item.Workspace, &item.Title, &item.CreatedAt); err != nil {
					respondError(w, http.StatusInternalServerError, "database_error")
					return
				}
				items = append(items, item)
			}
			respond(w, http.StatusOK, items)
		case http.MethodPost:
			var input struct {
				Title string `json:"title"`
			}
			if decode(r, &input) != nil {
				respondError(w, http.StatusBadRequest, "invalid_json")
				return
			}
			var exists int
			if err := a.db.QueryRow(`SELECT COUNT(*) FROM workspaces WHERE id=?`, workspaceID).Scan(&exists); err != nil || exists == 0 {
				respondError(w, http.StatusNotFound, "workspace_not_found")
				return
			}
			item := chatRecord{ID: newID(), Workspace: workspaceID, Title: strings.TrimSpace(input.Title), CreatedAt: store.Now()}
			if item.Title == "" {
				item.Title = "新对话"
			}
			if _, err := a.db.Exec(`INSERT INTO chats(id,workspace_id,title,created_at) VALUES(?,?,?,?)`, item.ID, item.Workspace, item.Title, item.CreatedAt); err != nil {
				respondError(w, http.StatusInternalServerError, "database_error")
				return
			}
			respond(w, http.StatusCreated, item)
		default:
			respondError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		}
		return
	}
	chatID := rest[0]
	if len(rest) == 1 {
		switch r.Method {
		case http.MethodDelete:
			result, err := a.db.Exec(`DELETE FROM chats WHERE id=? AND workspace_id=?`, chatID, workspaceID)
			if err != nil {
				respondError(w, http.StatusInternalServerError, "database_error")
				return
			}
			affected, _ := result.RowsAffected()
			if affected == 0 {
				respondError(w, http.StatusNotFound, "chat_not_found")
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case http.MethodPatch:
			var input struct {
				Title string `json:"title"`
			}
			if decode(r, &input) != nil || strings.TrimSpace(input.Title) == "" {
				respondError(w, http.StatusBadRequest, "title_required")
				return
			}
			result, err := a.db.Exec(`UPDATE chats SET title=? WHERE id=? AND workspace_id=?`, strings.TrimSpace(input.Title), chatID, workspaceID)
			if err != nil {
				respondError(w, http.StatusInternalServerError, "database_error")
				return
			}
			affected, _ := result.RowsAffected()
			if affected == 0 {
				respondError(w, http.StatusNotFound, "chat_not_found")
				return
			}
			respond(w, http.StatusOK, map[string]string{"id": chatID, "title": strings.TrimSpace(input.Title)})
		default:
			respondError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		}
		return
	}
	if rest[1] == "messages" {
		a.chatMessages(w, r, workspaceID, chatID, rest[2:])
		return
	}
	if rest[1] == "branch" && len(rest) == 2 {
		a.branchChat(w, r, workspaceID, chatID)
		return
	}
	respondError(w, http.StatusNotFound, "not_found")
}

func (a *API) chatMessages(w http.ResponseWriter, r *http.Request, workspaceID, chatID string, rest []string) {
	if !a.chatExists(workspaceID, chatID) {
		respondError(w, http.StatusNotFound, "chat_not_found")
		return
	}
	if len(rest) == 0 {
		if r.Method == http.MethodGet {
			items, err := a.chatMessagesFor(chatID)
			if err != nil {
				respondError(w, http.StatusInternalServerError, "database_error")
				return
			}
			respond(w, http.StatusOK, items)
			return
		}
		if r.Method == http.MethodPost {
			var input struct {
				ModelID string `json:"model_id"`
				Content string `json:"content"`
			}
			if decode(r, &input) != nil || input.ModelID == "" || strings.TrimSpace(input.Content) == "" {
				respondError(w, http.StatusBadRequest, "model_id_and_content_required")
				return
			}
			a.streamGeneration(w, r, workspaceID, chatID, input.ModelID, strings.TrimSpace(input.Content), true, "")
			return
		}
		respondError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	messageID := rest[0]
	if len(rest) == 2 && rest[1] == "version" && r.Method == http.MethodPatch {
		var input struct {
			VersionID string `json:"version_id"`
		}
		if decode(r, &input) != nil || input.VersionID == "" {
			respondError(w, http.StatusBadRequest, "version_id_required")
			return
		}
		if !a.selectMessageVersion(chatID, messageID, input.VersionID) {
			respondError(w, http.StatusNotFound, "message_version_not_found")
			return
		}
		items, err := a.chatMessagesFor(chatID)
		if err != nil {
			respondError(w, http.StatusInternalServerError, "database_error")
			return
		}
		for _, item := range items {
			if item.ID == messageID {
				respond(w, http.StatusOK, item)
				return
			}
		}
		respondError(w, http.StatusNotFound, "message_not_found")
		return
	}
	if len(rest) == 1 && r.Method == http.MethodPatch {
		var input struct {
			Content string `json:"content"`
			ModelID string `json:"model_id"`
		}
		if decode(r, &input) != nil || input.ModelID == "" || strings.TrimSpace(input.Content) == "" {
			respondError(w, http.StatusBadRequest, "model_id_and_content_required")
			return
		}
		if !a.updateAndTruncateUser(chatID, messageID, strings.TrimSpace(input.Content)) {
			respondError(w, http.StatusBadRequest, "editable_user_message_not_found")
			return
		}
		a.streamGeneration(w, r, workspaceID, chatID, input.ModelID, strings.TrimSpace(input.Content), false, "")
		return
	}
	if len(rest) == 2 && rest[1] == "retry" && r.Method == http.MethodPost {
		var input struct {
			ModelID string `json:"model_id"`
		}
		if decode(r, &input) != nil || input.ModelID == "" {
			respondError(w, http.StatusBadRequest, "model_id_required")
			return
		}
		prompt, versionTarget, ok := a.truncateForRetry(chatID, messageID)
		if !ok {
			respondError(w, http.StatusBadRequest, "retryable_message_not_found")
			return
		}
		a.streamGeneration(w, r, workspaceID, chatID, input.ModelID, prompt, false, versionTarget)
		return
	}
	respondError(w, http.StatusNotFound, "not_found")
}

func (a *API) chatExists(workspaceID, chatID string) bool {
	var count int
	return a.db.QueryRow(`SELECT COUNT(*) FROM chats WHERE id=? AND workspace_id=?`, chatID, workspaceID).Scan(&count) == nil && count == 1
}
func (a *API) chatMessagesFor(chatID string) ([]messageRecord, error) {
	rows, err := a.db.Query(`SELECT m.id,m.role,COALESCE((SELECT v.content FROM message_versions v WHERE v.message_id=m.id AND v.is_selected=TRUE LIMIT 1),m.content),m.created_at FROM messages m WHERE m.chat_id=? ORDER BY m.created_at,m.id`, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []messageRecord{}
	for rows.Next() {
		var item messageRecord
		if err := rows.Scan(&item.ID, &item.Role, &item.Content, &item.CreatedAt); err != nil {
			return nil, err
		}
		if item.Role == "assistant" {
			item.Versions, _ = a.messageVersions(item.ID)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (a *API) updateAndTruncateUser(chatID, messageID, content string) bool {
	items, err := a.chatMessagesFor(chatID)
	if err != nil {
		return false
	}
	index := -1
	for i, item := range items {
		if item.ID == messageID && item.Role == "user" {
			index = i
			break
		}
	}
	if index < 0 {
		return false
	}
	if _, err := a.db.Exec(`UPDATE messages SET content=? WHERE id=? AND chat_id=?`, content, messageID, chatID); err != nil {
		return false
	}
	if index+1 < len(items) {
		_, err = a.db.Exec(`DELETE FROM messages WHERE chat_id=? AND (created_at>? OR (created_at=? AND id>?))`, chatID, items[index].CreatedAt, items[index].CreatedAt, messageID)
		if err != nil {
			return false
		}
	}
	return true
}
func (a *API) truncateForRetry(chatID, messageID string) (string, string, bool) {
	items, err := a.chatMessagesFor(chatID)
	if err != nil {
		return "", "", false
	}
	index := -1
	for i, item := range items {
		if item.ID == messageID {
			index = i
			break
		}
	}
	if index < 0 {
		return "", "", false
	}
	if items[index].Role == "assistant" {
		if index == 0 || items[index-1].Role != "user" {
			return "", "", false
		}
		if index+1 < len(items) {
			if _, err = a.db.Exec(`DELETE FROM messages WHERE chat_id=? AND (created_at>? OR (created_at=? AND id>?))`, chatID, items[index].CreatedAt, items[index].CreatedAt, items[index].ID); err != nil {
				return "", "", false
			}
		}
		return items[index-1].Content, messageID, true
	}
	if index < 0 || items[index].Role != "user" {
		return "", "", false
	}
	if index+1 < len(items) {
		if _, err = a.db.Exec(`DELETE FROM messages WHERE chat_id=? AND (created_at>? OR (created_at=? AND id>?))`, chatID, items[index].CreatedAt, items[index].CreatedAt, items[index].ID); err != nil {
			return "", "", false
		}
	}
	return items[index].Content, "", true
}

func (a *API) messageVersions(messageID string) ([]messageVersion, error) {
	var base string
	if err := a.db.QueryRow(`SELECT content FROM messages WHERE id=?`, messageID).Scan(&base); err != nil {
		return nil, err
	}
	rows, err := a.db.Query(`SELECT id,content,is_selected FROM message_versions WHERE message_id=? ORDER BY created_at,id`, messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []messageVersion{}
	for rows.Next() {
		var item messageVersion
		if err := rows.Scan(&item.ID, &item.Content, &item.Selected); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if len(items) == 0 {
		return []messageVersion{{ID: "base", Content: base, Selected: true}}, rows.Err()
	}
	return items, rows.Err()
}

func (a *API) ensureVersionBaseline(messageID string) error {
	var count int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM message_versions WHERE message_id=?`, messageID).Scan(&count); err != nil || count > 0 {
		return err
	}
	var content string
	if err := a.db.QueryRow(`SELECT content FROM messages WHERE id=?`, messageID).Scan(&content); err != nil {
		return err
	}
	_, err := a.db.Exec(`INSERT INTO message_versions(id,message_id,content,is_selected,created_at) VALUES(?,?,?,?,?)`, newID(), messageID, content, false, store.Now())
	return err
}

func (a *API) selectMessageVersion(chatID, messageID, versionID string) bool {
	if !a.chatExistsForMessage(chatID, messageID) {
		return false
	}
	if versionID == "base" {
		return false
	}
	result, err := a.db.Exec(`UPDATE message_versions SET is_selected=FALSE WHERE message_id=?`, messageID)
	if err != nil {
		return false
	}
	_, _ = result.RowsAffected()
	result, err = a.db.Exec(`UPDATE message_versions SET is_selected=TRUE WHERE id=? AND message_id=?`, versionID, messageID)
	if err != nil {
		return false
	}
	count, _ := result.RowsAffected()
	return count == 1
}
func (a *API) chatExistsForMessage(chatID, messageID string) bool {
	var count int
	return a.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE id=? AND chat_id=?`, messageID, chatID).Scan(&count) == nil && count == 1
}

func (a *API) branchChat(w http.ResponseWriter, r *http.Request, workspaceID, chatID string) {
	if r.Method != http.MethodPost {
		respondError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	var input struct {
		MessageID string `json:"message_id"`
	}
	if decode(r, &input) != nil || input.MessageID == "" {
		respondError(w, http.StatusBadRequest, "message_id_required")
		return
	}
	items, err := a.chatMessagesFor(chatID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "database_error")
		return
	}
	cut := -1
	for i, item := range items {
		if item.ID == input.MessageID {
			cut = i
			break
		}
	}
	if cut < 0 {
		respondError(w, http.StatusNotFound, "message_not_found")
		return
	}
	title := "分支对话"
	for i := cut; i >= 0; i-- {
		if items[i].Role == "user" {
			title = "分支 · " + shortTitle(items[i].Content)
			break
		}
	}
	created := chatRecord{ID: newID(), Workspace: workspaceID, Title: title, CreatedAt: store.Now()}
	tx, err := a.db.Begin()
	if err != nil {
		respondError(w, http.StatusInternalServerError, "database_error")
		return
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT INTO chats(id,workspace_id,title,created_at) VALUES(?,?,?,?)`, created.ID, created.Workspace, created.Title, created.CreatedAt); err == nil {
		for _, item := range items[:cut+1] {
			_, err = tx.Exec(`INSERT INTO messages(id,chat_id,role,content,created_at) VALUES(?,?,?,?,?)`, newID(), created.ID, item.Role, item.Content, item.CreatedAt)
			if err != nil {
				break
			}
		}
	}
	if err != nil || tx.Commit() != nil {
		respondError(w, http.StatusInternalServerError, "database_error")
		return
	}
	respond(w, http.StatusCreated, created)
}
func shortTitle(value string) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) > 28 {
		return string(runes[:28]) + "…"
	}
	return string(runes)
}

func (a *API) streamGeneration(w http.ResponseWriter, r *http.Request, workspaceID, chatID, modelID, prompt string, createUser bool, versionTarget string) {
	if createUser {
		item := messageRecord{ID: newID(), Role: "user", Content: prompt, CreatedAt: store.Now()}
		if _, err := a.db.Exec(`INSERT INTO messages(id,chat_id,role,content,created_at) VALUES(?,?,?,?,?)`, item.ID, chatID, item.Role, item.Content, item.CreatedAt); err != nil {
			respondError(w, http.StatusInternalServerError, "database_error")
			return
		}
		a.setChatTitle(chatID, prompt)
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, ok := w.(http.Flusher)
	if !ok {
		respondError(w, http.StatusInternalServerError, "streaming_unsupported")
		return
	}
	writeStreamEvent(w, "start", map[string]string{"chat_id": chatID})
	flusher.Flush()
	var result strings.Builder
	err := a.completeStream(r.Context(), workspaceID, chatID, modelID, func(delta string) error {
		result.WriteString(delta)
		writeStreamEvent(w, "delta", map[string]string{"content": delta})
		flusher.Flush()
		return nil
	})
	if err != nil {
		writeStreamEvent(w, "error", map[string]string{"error": "provider_request_failed"})
		flusher.Flush()
		return
	}
	answer := strings.TrimSpace(result.String())
	if answer == "" {
		writeStreamEvent(w, "error", map[string]string{"error": "provider_returned_no_text"})
		flusher.Flush()
		return
	}
	assistant := messageRecord{ID: newID(), Role: "assistant", Content: answer, CreatedAt: store.Now()}
	if versionTarget != "" {
		if err = a.ensureVersionBaseline(versionTarget); err == nil {
			_, err = a.db.Exec(`UPDATE message_versions SET is_selected=FALSE WHERE message_id=?`, versionTarget)
		}
		if err == nil {
			_, err = a.db.Exec(`INSERT INTO message_versions(id,message_id,content,is_selected,created_at) VALUES(?,?,?,?,?)`, assistant.ID, versionTarget, assistant.Content, true, assistant.CreatedAt)
		}
		assistant.ID = versionTarget
	} else {
		_, err = a.db.Exec(`INSERT INTO messages(id,chat_id,role,content,created_at) VALUES(?,?,?,?,?)`, assistant.ID, chatID, assistant.Role, assistant.Content, assistant.CreatedAt)
	}
	if err != nil {
		writeStreamEvent(w, "error", map[string]string{"error": "database_error"})
		flusher.Flush()
		return
	}
	writeStreamEvent(w, "done", assistant)
	flusher.Flush()
}
func writeStreamEvent(w io.Writer, event string, value any) {
	encoded, _ := json.Marshal(value)
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, encoded)
}
func (a *API) setChatTitle(chatID, prompt string) {
	var count int
	if a.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE chat_id=?`, chatID).Scan(&count) == nil && count == 1 {
		_, _ = a.db.Exec(`UPDATE chats SET title=? WHERE id=?`, shortTitle(prompt), chatID)
	}
}

func (a *API) completeStream(ctx context.Context, workspaceID, chatID, modelID string, onDelta func(string) error) error {
	var provider, model, encryptedKey, baseURL string
	if err := a.db.QueryRow(`SELECT provider,model,api_key,base_url FROM models WHERE id=?`, modelID).Scan(&provider, &model, &encryptedKey, &baseURL); err != nil {
		return err
	}
	apiKey, err := a.decrypt(encryptedKey)
	if err != nil {
		return err
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
	history, err := a.chatMessagesFor(chatID)
	if err != nil {
		return err
	}
	system := "Answer using the workspace sources when relevant. If the sources do not support a claim, say so.\nWorkspace sources:" + a.sourceContext(ctx, workspaceID)
	var endpoint string
	var payload any
	if provider == "gemini" {
		baseURL = strings.TrimSuffix(strings.TrimRight(baseURL, "/"), "/v1beta")
		endpoint = baseURL + "/v1beta/models/" + url.PathEscape(model) + ":streamGenerateContent?alt=sse&key=" + url.QueryEscape(apiKey)
		contents := []map[string]any{}
		for _, item := range history {
			role := "user"
			if item.Role == "assistant" {
				role = "model"
			}
			contents = append(contents, map[string]any{"role": role, "parts": []map[string]string{{"text": item.Content}}})
		}
		payload = map[string]any{"systemInstruction": map[string]any{"parts": []map[string]string{{"text": system}}}, "contents": contents}
	} else {
		messages := []map[string]string{{"role": "system", "content": system}}
		for _, item := range history {
			messages = append(messages, map[string]string{"role": item.Role, "content": item.Content})
		}
		endpoint = strings.TrimRight(baseURL, "/") + "/chat/completions"
		payload = map[string]any{"model": model, "stream": true, "messages": messages}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if provider != "gemini" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 32<<10))
		return fmt.Errorf("provider returned %s", resp.Status)
	}
	scanner := bufio.NewScanner(io.LimitReader(resp.Body, 16<<20))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var event struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
			Candidates []struct {
				Content struct {
					Parts []struct {
						Text string `json:"text"`
					} `json:"parts"`
				} `json:"content"`
			} `json:"candidates"`
		}
		if json.Unmarshal([]byte(data), &event) != nil {
			continue
		}
		if len(event.Choices) > 0 && event.Choices[0].Delta.Content != "" {
			if err := onDelta(event.Choices[0].Delta.Content); err != nil {
				return err
			}
		}
		if len(event.Candidates) > 0 && len(event.Candidates[0].Content.Parts) > 0 && event.Candidates[0].Content.Parts[0].Text != "" {
			if err := onDelta(event.Candidates[0].Content.Parts[0].Text); err != nil {
				return err
			}
		}
	}
	return scanner.Err()
}
