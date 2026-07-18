package ui

import (
	"testing"

	"charm.land/bubbles/v2/viewport"
)

func TestLayoutConfigUsesDerivedDensity(t *testing.T) {
	tests := []struct {
		name       string
		rect       CellRect
		options    LayoutCapabilityOptions
		wantArgs   int
		wantResult int
	}{
		{
			name:       "narrow work rect is compact",
			rect:       NewCellRect(0, 0, 71, 30),
			wantArgs:   100,
			wantResult: 150,
		},
		{
			name:       "short work rect is compact",
			rect:       NewCellRect(0, 0, 100, 23),
			wantArgs:   100,
			wantResult: 150,
		},
		{
			name:       "regular work rect",
			rect:       NewCellRect(0, 0, 100, 24),
			wantArgs:   200,
			wantResult: 300,
		},
		{
			name:       "wide work rect is spacious",
			rect:       NewCellRect(0, 0, 112, 24),
			wantArgs:   300,
			wantResult: 500,
		},
		{
			name:       "explicit compact wins over roomy rect",
			rect:       NewCellRect(0, 0, 200, 80),
			options:    LayoutCapabilityOptions{ForceCompact: true},
			wantArgs:   100,
			wantResult: 150,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			capabilities := DeriveLayoutCapabilities(test.rect, test.options)
			layout := layoutConfigFor(capabilities)
			if layout.ArgsTruncMax != test.wantArgs || layout.ResultTruncMax != test.wantResult {
				t.Fatalf(
					"layout truncation = args %d/result %d, want %d/%d",
					layout.ArgsTruncMax,
					layout.ResultTruncMax,
					test.wantArgs,
					test.wantResult,
				)
			}
			if layout.Capabilities != capabilities {
				t.Fatalf("layout lost its capability snapshot: got=%+v want=%+v", layout.Capabilities, capabilities)
			}
		})
	}
}

func TestCurrentLayoutUsesFinalViewportRect(t *testing.T) {
	m := &Model{
		width:  240,
		height: 80,
		viewport: viewport.New(
			viewport.WithWidth(77),
			viewport.WithHeight(23),
		),
	}
	layout := m.currentLayout()
	if got, want := layout.Capabilities.WorkWidth, 71; got != want {
		t.Fatalf("work width = %d, want viewport width minus transcript chrome = %d", got, want)
	}
	if got, want := layout.Capabilities.WorkHeight, 23; got != want {
		t.Fatalf("work height = %d, want final viewport height %d", got, want)
	}
	if layout.Capabilities.Density != LayoutDensityCompact || layout.ArgsTruncMax != 100 {
		t.Fatalf("outer terminal leaked into layout choice: %+v", layout)
	}
}

func TestCurrentLayoutRespectsForceCompact(t *testing.T) {
	m := &Model{
		width:        240,
		height:       80,
		forceCompact: true,
		viewport: viewport.New(
			viewport.WithWidth(200),
			viewport.WithHeight(40),
		),
	}
	layout := m.currentLayout()
	if layout.Capabilities.WorkWidth != 194 || layout.Capabilities.WorkHeight != 40 {
		t.Fatalf("force compact changed measured work rect: %+v", layout.Capabilities)
	}
	if layout.Capabilities.Density != LayoutDensityCompact || layout.ArgsTruncMax != 100 {
		t.Fatalf("force compact was not applied: %+v", layout)
	}
}
