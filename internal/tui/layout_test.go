package tui

import "testing"

func TestCurrentLayout_Compact(t *testing.T) {
	m := &Model{width: 60, height: 20}
	layout := m.currentLayout()
	if layout.HeaderMode != "compact" {
		t.Errorf("small terminal should use compact mode, got %q", layout.HeaderMode)
	}
	if layout.ArgsTruncMax != 100 {
		t.Errorf("compact ArgsTruncMax = %d, want 100", layout.ArgsTruncMax)
	}
}

func TestCurrentLayout_Normal(t *testing.T) {
	m := &Model{width: 100, height: 30}
	layout := m.currentLayout()
	if layout.HeaderMode != "full" {
		t.Errorf("normal terminal should use full mode, got %q", layout.HeaderMode)
	}
	if layout.ArgsTruncMax != 200 {
		t.Errorf("normal ArgsTruncMax = %d, want 200", layout.ArgsTruncMax)
	}
}

func TestCurrentLayout_Wide(t *testing.T) {
	m := &Model{width: 150, height: 40}
	layout := m.currentLayout()
	if layout.HeaderMode != "full" {
		t.Errorf("wide terminal should use full mode, got %q", layout.HeaderMode)
	}
	if layout.ArgsTruncMax != 300 {
		t.Errorf("wide ArgsTruncMax = %d, want 300", layout.ArgsTruncMax)
	}
	if layout.ResultTruncMax != 500 {
		t.Errorf("wide ResultTruncMax = %d, want 500", layout.ResultTruncMax)
	}
}

func TestCurrentLayout_CompactHeight(t *testing.T) {
	// Wide but short terminal should be compact.
	m := &Model{width: 120, height: 20}
	layout := m.currentLayout()
	if layout.HeaderMode != "compact" {
		t.Errorf("short terminal should use compact mode, got %q", layout.HeaderMode)
	}
}

func TestContextProgressBar(t *testing.T) {
	tests := []struct {
		pct  int
		want string
	}{
		{0, "░░░░░░░░░░ 0%"},
		{50, "█████░░░░░ 50%"},
		{100, "██████████ 100%"},
	}
	for _, tt := range tests {
		got := contextProgressBar(tt.pct)
		if got != tt.want {
			t.Errorf("contextProgressBar(%d) = %q, want %q", tt.pct, got, tt.want)
		}
	}
}

func TestContextProgressBar_Overflow(t *testing.T) {
	// Should not panic or produce weird output for >100%
	result := contextProgressBar(150)
	if result == "" {
		t.Error("overflow should still produce output")
	}
}
