package mcp

import (
	"testing"
)

func TestToLLMToolDef(t *testing.T) {
	tests := []struct {
		name        string
		toolName    string
		description string
		inputSchema any
		wantName    string
		wantDesc    string
		wantParams  bool // true = should have non-nil params
	}{
		{
			name:        "normal with valid schema",
			toolName:    "read_file",
			description: "Read a file",
			inputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{"type": "string"},
				},
			},
			wantName:   "read_file",
			wantDesc:   "Read a file",
			wantParams: true,
		},
		{
			name:        "nil schema uses default",
			toolName:    "noop",
			description: "No-op tool",
			inputSchema: nil,
			wantName:    "noop",
			wantDesc:    "No-op tool",
			wantParams:  true,
		},
		{
			name:        "non-map schema uses default",
			toolName:    "bad_schema",
			description: "Bad schema tool",
			inputSchema: "not a map",
			wantName:    "bad_schema",
			wantDesc:    "Bad schema tool",
			wantParams:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ToLLMToolDef(tt.toolName, tt.description, tt.inputSchema)
			if result.Name != tt.wantName {
				t.Errorf("Name = %q, want %q", result.Name, tt.wantName)
			}
			if result.Description != tt.wantDesc {
				t.Errorf("Description = %q, want %q", result.Description, tt.wantDesc)
			}
			if tt.wantParams && result.Parameters == nil {
				t.Error("Parameters should not be nil")
			}
			// Nil and non-map schemas should get the default object schema.
			if tt.inputSchema == nil || func() bool { _, ok := tt.inputSchema.(map[string]any); return !ok }() {
				if result.Parameters["type"] != "object" {
					t.Errorf("default schema type = %v, want 'object'", result.Parameters["type"])
				}
			}
		})
	}
}
