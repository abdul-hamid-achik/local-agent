package agent

import (
	"strings"
	"testing"
)

func TestFormatToolArgs(t *testing.T) {
	tests := []struct {
		name     string
		args     map[string]any
		want     string
		contains []string
		maxLen   int
	}{
		{
			name: "empty map",
			args: map[string]any{},
			want: "{}",
		},
		{
			name:     "simple map",
			args:     map[string]any{"key": "value"},
			contains: []string{"key", "value"},
		},
		{
			name:   "long args truncated at 200",
			args:   map[string]any{"data": strings.Repeat("a", 300)},
			maxLen: 200,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatToolArgs(tt.args)

			if tt.want != "" && got != tt.want {
				t.Errorf("FormatToolArgs() = %q, want %q", got, tt.want)
			}

			for _, substr := range tt.contains {
				if !strings.Contains(got, substr) {
					t.Errorf("FormatToolArgs() = %q, missing %q", got, substr)
				}
			}

			if tt.maxLen > 0 {
				if len(got) > tt.maxLen {
					t.Errorf("FormatToolArgs() len = %d, want <= %d", len(got), tt.maxLen)
				}
				if !strings.HasSuffix(got, "...") {
					t.Errorf("FormatToolArgs() should end with '...' when truncated, got %q", got[len(got)-10:])
				}
			}
		})
	}
}
