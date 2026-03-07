package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// TestNoHorizontalScroll verifies that rendered content never exceeds viewport width
func TestNoHorizontalScroll(t *testing.T) {
	tests := []struct {
		name         string
		screenWidth  int
		panelVisible bool
		content      string
	}{
		{
			name:         "long word with panel",
			screenWidth:  120,
			panelVisible: true,
			content:      strings.Repeat("x", 150),
		},
		{
			name:         "multiple long words without panel",
			screenWidth:  100,
			panelVisible: false,
			content:      strings.Repeat("superlongword ", 10),
		},
		{
			name:         "code block with panel",
			screenWidth:  120,
			panelVisible: true,
			content:      "```\n" + strings.Repeat("x", 100) + "\n```",
		},
		{
			name:         "URL without panel",
			screenWidth:  80,
			panelVisible: false,
			content:      "https://example.com/" + strings.Repeat("verylongpathsegment/", 5),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Calculate panel width
			panelWidth := 0
			if tt.panelVisible {
				panelWidth = 30
				if tt.screenWidth < 100 {
					panelWidth = 25
				} else if tt.screenWidth > 160 {
					panelWidth = 40
				}
			}

			// Calculate viewport width (from model.go)
			viewportWidth := tt.screenWidth - 1
			if tt.panelVisible {
				viewportWidth = tt.screenWidth - panelWidth - 2
			}
			if viewportWidth < 20 {
				viewportWidth = 20
			}

			// Calculate content width (from view.go)
			contentW := tt.screenWidth - 4
			if tt.panelVisible {
				contentW = tt.screenWidth - panelWidth - 5
			}
			if contentW < 20 {
				contentW = 20
			}

			// Wrap the content
			wrapped := wrapText(tt.content, contentW)

			// Check each line
			lines := strings.Split(wrapped, "\n")
			for i, line := range lines {
				// Measure visible width (lipgloss.Width handles styling)
				lineWidth := lipgloss.Width(line)
				if lineWidth > viewportWidth {
					t.Errorf("line %d width %d exceeds viewport width %d: %q",
						i, lineWidth, viewportWidth, line[:min(50, len(line))])
				}
				if lineWidth > contentW {
					t.Errorf("line %d width %d exceeds content width %d",
						i, lineWidth, contentW)
				}
			}
		})
	}
}

// TestResponsivePanelToggle verifies no scroll when toggling panel
func TestResponsivePanelToggle(t *testing.T) {
	screenWidth := 120
	content := strings.Repeat("longword ", 20)

	// Calculate widths with panel
	panelWidth := 30
	viewportWithPanel := screenWidth - panelWidth - 2
	contentWithPanel := screenWidth - panelWidth - 5

	// Calculate widths without panel
	viewportWithoutPanel := screenWidth - 1
	contentWithoutPanel := screenWidth - 4

	// Wrap content for both scenarios
	wrappedWithPanel := wrapText(content, contentWithPanel)
	wrappedWithoutPanel := wrapText(content, contentWithoutPanel)

	// Verify both fit within their respective viewports
	for i, line := range strings.Split(wrappedWithPanel, "\n") {
		if lipgloss.Width(line) > viewportWithPanel {
			t.Errorf("with panel: line %d exceeds viewport", i)
		}
	}

	for i, line := range strings.Split(wrappedWithoutPanel, "\n") {
		if lipgloss.Width(line) > viewportWithoutPanel {
			t.Errorf("without panel: line %d exceeds viewport", i)
		}
	}

	// Verify that content without panel is wider (better use of space)
	if contentWithoutPanel <= contentWithPanel {
		t.Error("content width should increase when panel is hidden")
	}
}

// TestEdgeCases verifies width handling at boundary conditions
func TestEdgeCases(t *testing.T) {
	tests := []struct {
		name        string
		screenWidth int
		expectMin   bool // whether minimum width constraint should kick in
	}{
		{"minimum viable", 46, true},  // 25 (panel) + 1 + 20 (min viewport)
		{"just above min", 50, false},
		{"exactly 100", 100, false},
		{"exactly 160", 160, false},
		{"very large", 300, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			panelWidth := 30
			if tt.screenWidth < 100 {
				panelWidth = 25
			} else if tt.screenWidth > 160 {
				panelWidth = 40
			}

			viewportWidth := tt.screenWidth - panelWidth - 2
			if viewportWidth < 20 {
				viewportWidth = 20
			}

			if tt.expectMin && viewportWidth == 20 {
				// Expected minimum enforcement
				if tt.screenWidth-panelWidth-2 >= 20 {
					t.Error("expected minimum width enforcement but calculation would allow larger")
				}
			}

			// Verify viewport never exceeds available space (unless minimum enforced)
			maxAllowed := tt.screenWidth - panelWidth - 1
			if viewportWidth > maxAllowed && !tt.expectMin {
				t.Errorf("viewport %d exceeds max allowed %d", viewportWidth, maxAllowed)
			}
		})
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
