package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAICompatibleChatStreamText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hel\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"lo\"},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	client, err := NewOpenAICompatibleClient(OpenAICompatibleOptions{
		BaseURL: server.URL + "/v1",
		Model:   "grok-4.5",
		APIKey:  "test-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	if err := client.ChatStream(context.Background(), ChatOptions{}, func(chunk StreamChunk) error {
		text.WriteString(chunk.Text)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if text.String() != "hello" {
		t.Fatalf("text = %q, want hello", text.String())
	}
}

func TestOpenAICompatibleChatStreamToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		frames := []string{
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"read_file","arguments":"{\"pa"}}]}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"th\":\"a.go\"}"}}]},"finish_reason":"tool_calls"}]}`,
		}
		for _, frame := range frames {
			_, _ = w.Write([]byte("data: " + frame + "\n\n"))
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	client, err := NewOpenAICompatibleClient(OpenAICompatibleOptions{
		BaseURL: server.URL,
		Model:   "grok-4.5",
		APIKey:  "k",
	})
	if err != nil {
		t.Fatal(err)
	}
	var calls []ToolCall
	if err := client.ChatStream(context.Background(), ChatOptions{}, func(chunk StreamChunk) error {
		if len(chunk.ToolCalls) > 0 {
			calls = chunk.ToolCalls
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0].Name != "read_file" {
		t.Fatalf("tool calls = %#v", calls)
	}
	if path, _ := calls[0].Arguments["path"].(string); path != "a.go" {
		t.Fatalf("arguments = %#v", calls[0].Arguments)
	}
}

func TestOpenAICompatibleEncodesToolsAndSystem(t *testing.T) {
	var saw map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&saw); err != nil {
			t.Errorf("decode: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	client, err := NewOpenAICompatibleClient(OpenAICompatibleOptions{
		BaseURL: server.URL,
		Model:   "m",
		APIKey:  "k",
	})
	if err != nil {
		t.Fatal(err)
	}
	err = client.ChatStream(context.Background(), ChatOptions{
		System: "sys",
		Messages: []Message{{
			Role:    "user",
			Content: "hi",
		}},
		Tools: []ToolDef{{
			Name:        "grep",
			Description: "search",
			Parameters:  map[string]any{"type": "object"},
		}},
	}, func(StreamChunk) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if saw["model"] != "m" {
		t.Fatalf("model = %#v", saw["model"])
	}
	messages, _ := saw["messages"].([]any)
	if len(messages) < 2 {
		t.Fatalf("messages = %#v", messages)
	}
	tools, _ := saw["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools = %#v", tools)
	}
}

func TestOpenAICompatibleMissingKeyStillBuilds(t *testing.T) {
	// Local open servers may not need a key.
	client, err := NewOpenAICompatibleClient(OpenAICompatibleOptions{
		BaseURL: "http://127.0.0.1:8000/v1",
		Model:   "local",
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.Model() != "local" {
		t.Fatalf("model = %q", client.Model())
	}
}
