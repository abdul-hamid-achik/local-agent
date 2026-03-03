package memory

import "testing"

func TestBuiltinToolDefs(t *testing.T) {
	defs := BuiltinToolDefs()
	if len(defs) != 2 {
		t.Fatalf("BuiltinToolDefs() returned %d defs, want 2", len(defs))
	}

	names := map[string]bool{}
	for _, d := range defs {
		names[d.Name] = true
	}

	if !names["memory_save"] {
		t.Error("missing memory_save tool definition")
	}
	if !names["memory_recall"] {
		t.Error("missing memory_recall tool definition")
	}
}

func TestIsBuiltinTool(t *testing.T) {
	tests := []struct {
		name string
		tool string
		want bool
	}{
		{name: "memory_save", tool: "memory_save", want: true},
		{name: "memory_recall", tool: "memory_recall", want: true},
		{name: "unknown tool", tool: "unknown", want: false},
		{name: "empty string", tool: "", want: false},
		{name: "partial match", tool: "memory_", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsBuiltinTool(tt.tool)
			if got != tt.want {
				t.Errorf("IsBuiltinTool(%q) = %v, want %v", tt.tool, got, tt.want)
			}
		})
	}
}
