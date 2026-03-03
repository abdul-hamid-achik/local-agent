package agent

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdulachik/local-agent/internal/llm"
	"github.com/abdulachik/local-agent/internal/mcp"
	"github.com/abdulachik/local-agent/internal/memory"
)

func newTestAgentWithMemory(t *testing.T) *Agent {
	t.Helper()
	store := memory.NewStore(filepath.Join(t.TempDir(), "test-memories.json"))
	return &Agent{
		memoryStore: store,
		registry:    mcp.NewRegistry(),
	}
}

func TestHandleMemoryTool(t *testing.T) {
	tests := []struct {
		name       string
		toolCall   llm.ToolCall
		wantSubstr string
		wantErr    bool
	}{
		{
			name: "dispatch to save",
			toolCall: llm.ToolCall{
				Name:      "memory_save",
				Arguments: map[string]any{"content": "test fact", "tags": []any{"tag1"}},
			},
			wantSubstr: "Memory saved (id:",
			wantErr:    false,
		},
		{
			name: "dispatch to recall",
			toolCall: llm.ToolCall{
				Name:      "memory_recall",
				Arguments: map[string]any{"query": "test"},
			},
			wantSubstr: "No matching memories found.",
			wantErr:    false,
		},
		{
			name: "unknown tool",
			toolCall: llm.ToolCall{
				Name:      "unknown",
				Arguments: map[string]any{},
			},
			wantSubstr: "unknown memory tool: unknown",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ag := newTestAgentWithMemory(t)
			result, isErr := ag.handleMemoryTool(tt.toolCall)
			if isErr != tt.wantErr {
				t.Errorf("handleMemoryTool() isErr = %v, want %v", isErr, tt.wantErr)
			}
			if !strings.Contains(result, tt.wantSubstr) {
				t.Errorf("handleMemoryTool() = %q, want substring %q", result, tt.wantSubstr)
			}
		})
	}
}

func TestHandleMemorySave(t *testing.T) {
	tests := []struct {
		name       string
		args       map[string]any
		wantSubstr string
		wantErr    bool
	}{
		{
			name:       "valid with tags as []any",
			args:       map[string]any{"content": "test fact", "tags": []any{"tag1", "tag2"}},
			wantSubstr: "Memory saved (id:",
			wantErr:    false,
		},
		{
			name:       "valid without tags",
			args:       map[string]any{"content": "another fact"},
			wantSubstr: "Memory saved (id:",
			wantErr:    false,
		},
		{
			name:       "missing content",
			args:       map[string]any{},
			wantSubstr: "error: content is required",
			wantErr:    true,
		},
		{
			name:       "empty content",
			args:       map[string]any{"content": ""},
			wantSubstr: "error: content is required",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ag := newTestAgentWithMemory(t)
			result, isErr := ag.handleMemorySave(tt.args)
			if isErr != tt.wantErr {
				t.Errorf("handleMemorySave() isErr = %v, want %v", isErr, tt.wantErr)
			}
			if !strings.Contains(result, tt.wantSubstr) {
				t.Errorf("handleMemorySave() = %q, want substring %q", result, tt.wantSubstr)
			}
		})
	}
}

func TestHandleMemoryRecall(t *testing.T) {
	tests := []struct {
		name       string
		setup      func(ag *Agent)
		args       map[string]any
		wantSubstr string
		wantErr    bool
	}{
		{
			name: "valid recall finds saved memory",
			setup: func(ag *Agent) {
				_, _ = ag.memoryStore.Save("user prefers Go", []string{"language"})
			},
			args:       map[string]any{"query": "Go"},
			wantSubstr: "Found 1 matching memories:",
			wantErr:    false,
		},
		{
			name:       "missing query",
			setup:      func(ag *Agent) {},
			args:       map[string]any{},
			wantSubstr: "error: query is required",
			wantErr:    true,
		},
		{
			name:       "no matches",
			setup:      func(ag *Agent) {},
			args:       map[string]any{"query": "nonexistent"},
			wantSubstr: "No matching memories found.",
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ag := newTestAgentWithMemory(t)
			tt.setup(ag)
			result, isErr := ag.handleMemoryRecall(tt.args)
			if isErr != tt.wantErr {
				t.Errorf("handleMemoryRecall() isErr = %v, want %v", isErr, tt.wantErr)
			}
			if !strings.Contains(result, tt.wantSubstr) {
				t.Errorf("handleMemoryRecall() = %q, want substring %q", result, tt.wantSubstr)
			}
		})
	}
}
