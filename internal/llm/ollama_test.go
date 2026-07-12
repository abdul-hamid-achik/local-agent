package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOllamaChatStreamPreservesNativeReasoning(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			http.NotFound(w, r)
			return
		}
		_, _ = fmt.Fprintln(w, `{"message":{"role":"assistant","thinking":"private work","content":"answer"},"done":true,"eval_count":1,"prompt_eval_count":2}`)
	}))
	defer server.Close()
	client, err := NewOllamaClient(server.URL, "qwen", 4096)
	if err != nil {
		t.Fatal(err)
	}
	var got StreamChunk
	if err := client.ChatStream(context.Background(), ChatOptions{}, func(chunk StreamChunk) error {
		got = chunk
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got.Text != "answer" || got.Reasoning != "private work" {
		t.Fatalf("stream chunk = %#v", got)
	}
}

func TestOllamaChatStreamRejectsTruncatedSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintln(w, `{"message":{"role":"assistant","content":"partial"},"done":false}`)
	}))
	defer server.Close()

	client, err := NewOllamaClient(server.URL, "qwen", 4096)
	if err != nil {
		t.Fatal(err)
	}
	var content strings.Builder
	err = client.ChatStream(context.Background(), ChatOptions{}, func(chunk StreamChunk) error {
		content.WriteString(chunk.Text)
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "before done") {
		t.Fatalf("truncated stream error = %v, content = %q", err, content.String())
	}
}

func TestOllamaChatStreamPreservesToolWireShape(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
			return
		}
		if !strings.Contains(string(body), `"index":0`) {
			t.Errorf("tool-call index omitted from wire request: %s", body)
		}
		var request ollamaChatRequest
		if err := json.Unmarshal(body, &request); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		if !request.Stream || request.Model != "qwen" || len(request.Messages) != 3 {
			t.Errorf("chat request = %#v", request)
		}
		if request.Messages[0].Role != "system" || request.Messages[0].Content != "local only" {
			t.Errorf("system message = %#v", request.Messages[0])
		}
		priorCall := request.Messages[1].ToolCalls
		if len(priorCall) != 1 || priorCall[0].ID != "call-prior" || priorCall[0].Function.Name != "read_file" || priorCall[0].Function.Arguments["path"] != "README.md" {
			t.Errorf("prior tool call = %#v", priorCall)
		}
		if len(request.Tools) != 1 || request.Tools[0].Function.Name != "read_file" {
			t.Errorf("tools = %#v", request.Tools)
		}
		if request.Options["num_predict"] != float64(23) {
			t.Errorf("hard generation cap = %#v", request.Options["num_predict"])
		}
		_, _ = fmt.Fprintln(w, `{"message":{"role":"assistant","tool_calls":[{"id":"call-new","function":{"name":"read_file","arguments":{"path":"go.mod"}}}]},"done":true}`)
	}))
	defer server.Close()

	client, err := NewOllamaClient(server.URL, "qwen", 4096)
	if err != nil {
		t.Fatal(err)
	}
	options := ChatOptions{
		System: "local only", MaxEvalTokens: 23,
		Messages: []Message{
			{
				Role: "assistant",
				ToolCalls: []ToolCall{{
					ID: "call-prior", Name: "read_file", Arguments: map[string]any{"path": "README.md"},
				}},
			},
			{Role: "tool", Content: "contents", ToolName: "read_file", ToolCallID: "call-prior"},
		},
		Tools: []ToolDef{{Name: "read_file", Parameters: map[string]any{"type": "object"}}},
	}
	var got StreamChunk
	if err := client.ChatStream(context.Background(), options, func(chunk StreamChunk) error {
		got = chunk
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !got.Done || len(got.ToolCalls) != 1 || got.ToolCalls[0].ID != "call-new" || got.ToolCalls[0].Arguments["path"] != "go.mod" {
		t.Fatalf("tool response = %#v", got)
	}
}

func TestOllamaAPIKeyOnlyLeavesForRemoteHTTPSOrigin(t *testing.T) {
	t.Setenv("OLLAMA_API_KEY", "test-secret")
	tests := []struct {
		url        string
		wantHeader string
	}{
		{url: "https://ollama.example.com", wantHeader: "Bearer test-secret"},
		{url: "http://ollama.example.com", wantHeader: ""},
		{url: "https://localhost", wantHeader: ""},
	}
	for _, test := range tests {
		client, err := NewOllamaClient(test.url, "qwen", 4096)
		if err != nil {
			t.Fatal(err)
		}
		if client.authHeader != test.wantHeader {
			t.Fatalf("auth header for %s = %q, want %q", test.url, client.authHeader, test.wantHeader)
		}
	}
}

func TestOllamaChatStreamPropagatesCallbackError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintln(w, `{"message":{"role":"assistant","content":"answer"},"done":true}`)
	}))
	defer server.Close()
	client, err := NewOllamaClient(server.URL, "qwen", 4096)
	if err != nil {
		t.Fatal(err)
	}
	want := errors.New("stop")
	err = client.ChatStream(context.Background(), ChatOptions{}, func(StreamChunk) error { return want })
	if !errors.Is(err, want) {
		t.Fatalf("callback error = %v", err)
	}
}

func TestOllamaClientRejectsCrossOriginRedirect(t *testing.T) {
	reached := make(chan struct{}, 1)
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		select {
		case reached <- struct{}{}:
		default:
		}
	}))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer origin.Close()
	client, err := NewOllamaClient(origin.URL, "qwen", 4096)
	if err != nil {
		t.Fatal(err)
	}
	err = client.ChatStream(context.Background(), ChatOptions{}, func(StreamChunk) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "cross-origin") {
		t.Fatalf("redirect error = %v", err)
	}
	select {
	case <-reached:
		t.Fatal("cross-origin redirect reached its target")
	default:
	}
}

func TestOllamaClientBoundsNonStreamingResponses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", maxOllamaResponseBytes+1)))
	}))
	defer server.Close()
	client, err := NewOllamaClient(server.URL, "qwen", 4096)
	if err != nil {
		t.Fatal(err)
	}
	err = client.PingContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized response error = %v", err)
	}
}

func TestConvertTools(t *testing.T) {
	tests := []struct {
		name      string
		input     []ToolDef
		wantNil   bool
		wantCount int
	}{
		{
			name:    "nil input",
			input:   nil,
			wantNil: true,
		},
		{
			name: "single tool with properties and required",
			input: []ToolDef{
				{
					Name:        "read_file",
					Description: "Read a file",
					Parameters: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"path": map[string]any{
								"type":        "string",
								"description": "file path",
							},
						},
						"required": []string{"path"},
					},
				},
			},
			wantCount: 1,
		},
		{
			name: "tool without properties in parameters",
			input: []ToolDef{
				{
					Name:        "noop",
					Description: "Does nothing",
					Parameters:  map[string]any{"type": "object"},
				},
			},
			wantCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertTools(tt.input)
			if tt.wantNil {
				if result != nil {
					t.Errorf("convertTools() = %v, want nil", result)
				}
				return
			}
			if len(result) != tt.wantCount {
				t.Errorf("convertTools() returned %d tools, want %d", len(result), tt.wantCount)
			}
			if tt.wantCount > 0 {
				tool := result[0]
				if tool.Function.Name != tt.input[0].Name {
					t.Errorf("tool name = %q, want %q", tool.Function.Name, tt.input[0].Name)
				}
				if tool.Function.Description != tt.input[0].Description {
					t.Errorf("tool description = %q, want %q", tool.Function.Description, tt.input[0].Description)
				}
				if tool.Type != "function" {
					t.Errorf("tool type = %q, want %q", tool.Type, "function")
				}
				if tt.name == "single tool with properties and required" {
					required, ok := tool.Function.Parameters["required"].([]string)
					if !ok || len(required) != 1 || required[0] != "path" {
						t.Errorf("required = %#v, want [path]", tool.Function.Parameters["required"])
					}
				}
			}
		})
	}
}

func TestConvertToolsPreservesNestedSchema(t *testing.T) {
	tools := convertTools([]ToolDef{{
		Name: "plan",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"hypotheses": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"statement": map[string]any{"type": "string"},
						},
						"required": []string{"statement"},
					},
				},
			},
			"required": []string{"hypotheses"},
		},
	}})

	data, err := json.Marshal(tools[0].Function.Parameters)
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(data)
	for _, want := range []string{`"hypotheses"`, `"items"`, `"statement"`, `"required":["statement"]`} {
		if !strings.Contains(encoded, want) {
			t.Errorf("converted schema missing %s: %s", want, encoded)
		}
	}
}

func TestNormalizeOllamaURL(t *testing.T) {
	cases := []struct {
		in, wantHost, wantScheme string
		wantErr                  bool
	}{
		{in: "0.0.0.0", wantHost: "0.0.0.0:11434", wantScheme: "http"},
		{in: "localhost:11434", wantHost: "localhost:11434", wantScheme: "http"},
		{in: "http://localhost", wantHost: "localhost:11434", wantScheme: "http"},
		{in: "http://localhost:9999", wantHost: "localhost:9999", wantScheme: "http"},
		{in: "https://remote.example.com", wantHost: "remote.example.com", wantScheme: "https"}, // must NOT get :11434
		{in: "https://remote.example.com:8443", wantHost: "remote.example.com:8443", wantScheme: "https"},
		{in: "not a url", wantErr: true},
	}
	for _, c := range cases {
		u, err := normalizeOllamaURL(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("normalizeOllamaURL(%q): expected error, got %v", c.in, u)
			}
			continue
		}
		if err != nil {
			t.Errorf("normalizeOllamaURL(%q): unexpected error %v", c.in, err)
			continue
		}
		if u.Host != c.wantHost || u.Scheme != c.wantScheme {
			t.Errorf("normalizeOllamaURL(%q) = scheme=%q host=%q; want scheme=%q host=%q", c.in, u.Scheme, u.Host, c.wantScheme, c.wantHost)
		}
	}
}
