package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/mo2iairi/airipress/internal/store"
)

func createChat(t *testing.T, api *API, workspaceID string) string {
	t.Helper()
	recorder := request(t, api, http.MethodPost, "/api/v1/workspaces/"+workspaceID+"/chats", strings.NewReader(`{}`), "application/json")
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create chat: %d %s", recorder.Code, recorder.Body.String())
	}
	var chat chatRecord
	if err := json.Unmarshal(recorder.Body.Bytes(), &chat); err != nil {
		t.Fatal(err)
	}
	return chat.ID
}

func TestChatBranchCopiesOnlyRequestedPrefix(t *testing.T) {
	api, db, _ := testAPI(t)
	workspaceID := createWorkspace(t, api, "chat")
	chatID := createChat(t, api, workspaceID)
	for _, item := range []struct{ id, role, content string }{{"a", "user", "first"}, {"b", "assistant", "answer"}, {"c", "user", "later"}} {
		if _, err := db.Exec(`INSERT INTO messages(id,chat_id,role,content,created_at) VALUES(?,?,?,?,?)`, item.id, chatID, item.role, item.content, store.Now()); err != nil {
			t.Fatal(err)
		}
	}
	recorder := request(t, api, http.MethodPost, "/api/v1/workspaces/"+workspaceID+"/chats/"+chatID+"/branch", strings.NewReader(`{"message_id":"b"}`), "application/json")
	if recorder.Code != http.StatusCreated {
		t.Fatalf("branch: %d %s", recorder.Code, recorder.Body.String())
	}
	var branch chatRecord
	_ = json.Unmarshal(recorder.Body.Bytes(), &branch)
	messages, err := api.chatMessagesFor(branch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || messages[0].Content != "first" || messages[1].Content != "answer" {
		t.Fatalf("unexpected branch messages: %#v", messages)
	}
	if messages[0].ID == "a" || messages[1].ID == "b" {
		t.Fatal("branch must copy messages with new IDs")
	}
}

func TestChatStreamsAndPersistsAssistant(t *testing.T) {
	api, db, _ := testAPI(t)
	workspaceID := createWorkspace(t, api, "stream")
	chatID := createChat(t, api, workspaceID)
	key, err := api.encrypt("key")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO models(id,name,provider,model,api_key,base_url,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, "model", "mock", "openai", "mock", key, "https://provider.test/v1", store.Now(), store.Now()); err != nil {
		t.Fatal(err)
	}
	api.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() != "https://provider.test/v1/chat/completions" {
			t.Fatalf("unexpected endpoint: %s", req.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"hello \"}}]}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"world\"}}]}\n\ndata: [DONE]\n\n"))}, nil
	})}
	recorder := request(t, api, http.MethodPost, "/api/v1/workspaces/"+workspaceID+"/chats/"+chatID+"/messages", strings.NewReader(`{"model_id":"model","content":"hi"}`), "application/json")
	if recorder.Code != http.StatusOK {
		t.Fatalf("stream: %d %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "event: delta") || !strings.Contains(recorder.Body.String(), "hello world") {
		t.Fatalf("missing stream content: %s", recorder.Body.String())
	}
	messages, err := api.chatMessagesFor(chatID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || messages[1].Content != "hello world" {
		t.Fatalf("unexpected persisted messages: %#v", messages)
	}
	retry := request(t, api, http.MethodPost, "/api/v1/workspaces/"+workspaceID+"/chats/"+chatID+"/messages/"+messages[1].ID+"/retry", strings.NewReader(`{"model_id":"model"}`), "application/json")
	if retry.Code != http.StatusOK {
		t.Fatalf("retry: %d %s", retry.Code, retry.Body.String())
	}
	messages, err = api.chatMessagesFor(chatID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || len(messages[1].Versions) != 2 || messages[1].Content != "hello world" {
		t.Fatalf("unexpected versions after retry: %#v", messages)
	}
	firstVersion := messages[1].Versions[0]
	if !api.selectMessageVersion(chatID, messages[1].ID, firstVersion.ID) {
		t.Fatal("could not select earlier answer version")
	}
	messages, err = api.chatMessagesFor(chatID)
	if err != nil || messages[1].Content != "hello world" || !messages[1].Versions[0].Selected {
		t.Fatalf("version selection failed: %#v %v", messages, err)
	}
}
