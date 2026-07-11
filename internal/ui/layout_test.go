package ui

import "testing"

func TestCurrentLayout_Compact(t *testing.T) {
	m := &Model{width: 60, height: 20}
	layout := m.currentLayout()
	if layout.ArgsTruncMax != 100 {
		t.Errorf("compact ArgsTruncMax = %d, want 100", layout.ArgsTruncMax)
	}
}

func TestCurrentLayout_Normal(t *testing.T) {
	m := &Model{width: 100, height: 30}
	layout := m.currentLayout()
	if layout.ArgsTruncMax != 200 {
		t.Errorf("normal ArgsTruncMax = %d, want 200", layout.ArgsTruncMax)
	}
}

func TestCurrentLayout_Wide(t *testing.T) {
	m := &Model{width: 150, height: 40}
	layout := m.currentLayout()
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
	if layout.ArgsTruncMax != 100 {
		t.Errorf("short ArgsTruncMax = %d, want 100", layout.ArgsTruncMax)
	}
}
