package mcp

import (
	"context"
	"strings"
	"testing"
)

func TestNewRegistry(t *testing.T) {
	r := NewRegistry()

	if r.ToolCount() != 0 {
		t.Errorf("ToolCount() = %d, want 0", r.ToolCount())
	}
	if r.ServerCount() != 0 {
		t.Errorf("ServerCount() = %d, want 0", r.ServerCount())
	}
	if tools := r.Tools(); len(tools) != 0 {
		t.Errorf("Tools() = %v, want empty", tools)
	}
}

func TestRegistry_CallTool_Unknown(t *testing.T) {
	r := NewRegistry()

	result, err := r.CallTool(context.Background(), "nonexistent_tool", nil)
	if err != nil {
		t.Fatalf("CallTool() unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("CallTool() IsError = false, want true for unknown tool")
	}
	if !strings.Contains(result.Content, "unknown tool") {
		t.Errorf("CallTool() Content = %q, want to contain 'unknown tool'", result.Content)
	}
}
