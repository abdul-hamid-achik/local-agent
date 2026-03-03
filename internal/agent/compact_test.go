package agent

import (
	"strings"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/local-agent/internal/llm"
	"github.com/abdul-hamid-achik/local-agent/internal/mcp"
)

// mockOutput implements the Output interface for testing.
type mockOutput struct {
	texts   []string
	errors  []string
	sysMsgs []string
}

func (m *mockOutput) StreamText(text string)                                     { m.texts = append(m.texts, text) }
func (m *mockOutput) StreamDone(_, _ int)                                        {}
func (m *mockOutput) ToolCallStart(_ string, _ map[string]any)                   {}
func (m *mockOutput) ToolCallResult(_ string, _ string, _ bool, _ time.Duration) {}
func (m *mockOutput) SystemMessage(msg string)                                   { m.sysMsgs = append(m.sysMsgs, msg) }
func (m *mockOutput) Error(msg string)                                           { m.errors = append(m.errors, msg) }

func TestShouldCompact(t *testing.T) {
	tests := []struct {
		name         string
		numCtx       int
		promptTokens int
		want         bool
	}{
		{"below 75%", 1000, 749, false},
		{"above 75%", 1000, 751, true},
		{"exactly 75% (strict >)", 1000, 750, false},
		{"numCtx zero", 0, 500, false},
		{"promptTokens zero", 1000, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ag := &Agent{
				numCtx:   tt.numCtx,
				registry: mcp.NewRegistry(),
			}
			got := ag.shouldCompact(tt.promptTokens)
			if got != tt.want {
				t.Errorf("shouldCompact(%d) with numCtx=%d = %v, want %v",
					tt.promptTokens, tt.numCtx, got, tt.want)
			}
		})
	}
}

func TestSummarizeMessages(t *testing.T) {
	tests := []struct {
		name     string
		msgs     []llm.Message
		contains []string
	}{
		{
			name: "user message",
			msgs: []llm.Message{
				{Role: "user", Content: "hello"},
			},
			contains: []string{"User: hello"},
		},
		{
			name: "assistant message",
			msgs: []llm.Message{
				{Role: "assistant", Content: "hi there"},
			},
			contains: []string{"Assistant: hi there"},
		},
		{
			name: "tool message",
			msgs: []llm.Message{
				{Role: "tool", Content: "result data", ToolName: "read_file"},
			},
			contains: []string{"Tool read_file result: result data"},
		},
		{
			name: "tool content truncation at 300 chars",
			msgs: []llm.Message{
				{Role: "tool", Content: strings.Repeat("x", 400), ToolName: "big_tool"},
			},
			contains: []string{"Tool big_tool result: " + strings.Repeat("x", 297) + "..."},
		},
		{
			name:     "empty slice",
			msgs:     []llm.Message{},
			contains: []string{"Summarize this conversation:"},
		},
		{
			name: "assistant with tool calls",
			msgs: []llm.Message{
				{
					Role: "assistant",
					ToolCalls: []llm.ToolCall{
						{Name: "search", Arguments: map[string]any{"q": "test"}},
					},
				},
			},
			contains: []string{"Assistant called tool search("},
		},
		{
			name: "mixed messages",
			msgs: []llm.Message{
				{Role: "user", Content: "find files"},
				{Role: "assistant", Content: "", ToolCalls: []llm.ToolCall{
					{Name: "glob", Arguments: map[string]any{"pattern": "*.go"}},
				}},
				{Role: "tool", Content: "file1.go\nfile2.go", ToolName: "glob"},
				{Role: "assistant", Content: "Found 2 files"},
			},
			contains: []string{
				"User: find files",
				"Assistant called tool glob(",
				"Tool glob result:",
				"Assistant: Found 2 files",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := summarizeMessages(tt.msgs)
			for _, want := range tt.contains {
				if !strings.Contains(result, want) {
					t.Errorf("summarizeMessages() missing %q in:\n%s", want, result)
				}
			}
		})
	}
}
