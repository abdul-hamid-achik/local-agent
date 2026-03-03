package llm

import (
	"testing"
)

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
						"required": []any{"path"},
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
			}
		})
	}
}

func TestStrFromMap(t *testing.T) {
	tests := []struct {
		name string
		m    map[string]any
		key  string
		want string
	}{
		{
			name: "key present",
			m:    map[string]any{"description": "a desc"},
			key:  "description",
			want: "a desc",
		},
		{
			name: "key missing",
			m:    map[string]any{"other": "value"},
			key:  "description",
			want: "",
		},
		{
			name: "nil map",
			m:    nil,
			key:  "description",
			want: "",
		},
		{
			name: "non-string value",
			m:    map[string]any{"count": 42},
			key:  "count",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := strFromMap(tt.m, tt.key)
			if got != tt.want {
				t.Errorf("strFromMap() = %q, want %q", got, tt.want)
			}
		})
	}
}
