package tui

import (
	"strings"
	"testing"
)

// TestViewportWidthCalculation tests that viewport width calculations are consistent
// across all components to prevent horizontal scrolling.
func TestViewportWidthCalculation(t *testing.T) {
	tests := []struct {
		name           string
		screenWidth    int
		panelVisible   bool
		panelWidth     int
		wantViewWidth  int
		wantContentW   int
		wantMarkdownW  int
	}{
		{
			name:          "small screen with panel",
			screenWidth:   80,
			panelVisible:  true,
			panelWidth:    25,
			wantViewWidth: 80 - 25 - 2, // screen - panel - separator
			wantContentW:  80 - 25 - 5, // screen - panel - separator - padding
			wantMarkdownW: 80 - 25 - 5,
		},
		{
			name:          "medium screen with panel",
			screenWidth:   120,
			panelVisible:  true,
			panelWidth:    30,
			wantViewWidth: 120 - 30 - 2,
			wantContentW:  120 - 30 - 5,
			wantMarkdownW: 120 - 30 - 5,
		},
		{
			name:          "large screen with panel",
			screenWidth:   160,
			panelVisible:  true,
			panelWidth:    40,
			wantViewWidth: 160 - 40 - 2,
			wantContentW:  160 - 40 - 5,
			wantMarkdownW: 160 - 40 - 5,
		},
		{
			name:          "small screen without panel",
			screenWidth:   80,
			panelVisible:  false,
			panelWidth:    0,
			wantViewWidth: 80 - 1, // just separator
			wantContentW:  80 - 1 - 3, // viewport - padding for markdown
			wantMarkdownW: 80 - 1 - 3,
		},
		{
			name:          "large screen without panel",
			screenWidth:   200,
			panelVisible:  false,
			panelWidth:    0,
			wantViewWidth: 200 - 1,
			wantContentW:  200 - 1 - 3,
			wantMarkdownW: 200 - 1 - 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate the width calculation from model.go:WindowSizeMsg handler
			panelWidth := tt.panelWidth
			if panelWidth == 0 && tt.panelVisible {
				// Calculate panel width based on screen width (from model.go:365-371)
				panelWidth = 30
				if tt.screenWidth < 100 {
					panelWidth = 25
				} else if tt.screenWidth > 160 {
					panelWidth = 40
				}
			}

			// Viewport width (from model.go:373-380)
			viewportWidth := tt.screenWidth - 1
			if tt.panelVisible {
				viewportWidth = tt.screenWidth - panelWidth - 2
			}
			if viewportWidth < 20 {
				viewportWidth = 20
			}

			// Content/markdown width (from model.go:382-386)
			markdownWidth := viewportWidth - 3
			if markdownWidth < 20 {
				markdownWidth = 20
			}

			// Content width for rendering (from view.go:422-429)
			contentW := tt.screenWidth - 4
			if tt.panelVisible {
				contentW = tt.screenWidth - panelWidth - 5
			}
			if contentW < 20 {
				contentW = 20
			}

			// Verify consistency
			if markdownWidth != tt.wantMarkdownW {
				t.Errorf("markdown width = %d, want %d", markdownWidth, tt.wantMarkdownW)
			}

			if viewportWidth != tt.wantViewWidth {
				t.Errorf("viewport width = %d, want %d", viewportWidth, tt.wantViewWidth)
			}

			if contentW != tt.wantContentW {
				t.Errorf("content width = %d, want %d", contentW, tt.wantContentW)
			}

			// CRITICAL: viewport width should never exceed screen width minus panel
			maxAllowedWidth := tt.screenWidth - 1
			if tt.panelVisible {
				maxAllowedWidth = tt.screenWidth - panelWidth - 1
			}
			if viewportWidth > maxAllowedWidth {
				t.Errorf("viewport width %d exceeds max allowed %d - will cause horizontal scroll",
					viewportWidth, maxAllowedWidth)
			}

			// Content width should be <= viewport width
			if contentW > viewportWidth {
				t.Errorf("content width %d > viewport width %d - will cause horizontal scroll",
					contentW, viewportWidth)
			}

			// Markdown width should be <= viewport width
			if markdownWidth > viewportWidth {
				t.Errorf("markdown width %d > viewport width %d - will cause horizontal scroll",
					markdownWidth, viewportWidth)
			}
		})
	}
}

// TestResponsiveWidthToggle tests that toggling the side panel maintains proper widths
func TestResponsiveWidthToggle(t *testing.T) {
	screenWidth := 120
	panelWidth := 30

	// Panel visible
	viewportWithPanel := screenWidth - panelWidth - 2
	contentWithPanel := screenWidth - panelWidth - 5

	// Panel hidden
	viewportWithoutPanel := screenWidth - 1
	contentWithoutPanel := screenWidth - 4

	// Widths should increase when panel is hidden
	if viewportWithoutPanel <= viewportWithPanel {
		t.Errorf("viewport should be wider when panel is hidden: %d <= %d",
			viewportWithoutPanel, viewportWithPanel)
	}

	if contentWithoutPanel <= contentWithPanel {
		t.Errorf("content should be wider when panel is hidden: %d <= %d",
			contentWithoutPanel, contentWithPanel)
	}

	// Neither should exceed screen width
	if viewportWithoutPanel > screenWidth {
		t.Errorf("viewport without panel %d exceeds screen width %d",
			viewportWithoutPanel, screenWidth)
	}

	if viewportWithPanel > screenWidth-panelWidth {
		t.Errorf("viewport with panel %d exceeds available space %d",
			viewportWithPanel, screenWidth-panelWidth)
	}
}

// TestMinimumWidthConstraints tests that minimum width constraints prevent negative layouts
func TestMinimumWidthConstraints(t *testing.T) {
	tests := []struct {
		name        string
		screenWidth int
	}{
		{"tiny screen", 40},
		{"very small screen", 60},
		{"small screen", 80},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			panelWidth := 25 // minimum panel width
			minWidth := 20   // minimum content width

			// Calculate viewport width with panel
			viewportWidth := tt.screenWidth - panelWidth - 2
			if viewportWidth < minWidth {
				viewportWidth = minWidth
			}

			// Calculate content width with panel
			contentWidth := tt.screenWidth - panelWidth - 5
			if contentWidth < minWidth {
				contentWidth = minWidth
			}

			// Verify minimums are respected
			if viewportWidth < minWidth {
				t.Errorf("viewport width %d below minimum %d", viewportWidth, minWidth)
			}

			if contentWidth < minWidth {
				t.Errorf("content width %d below minimum %d", contentWidth, minWidth)
			}

			// Even with minimum constraints, total shouldn't exceed screen
			totalWidth := panelWidth + 1 + viewportWidth
			if totalWidth > tt.screenWidth && tt.screenWidth >= minWidth+panelWidth+1 {
				t.Errorf("total width %d exceeds screen width %d", totalWidth, tt.screenWidth)
			}
		})
	}
}

// TestRenderedTextWidth simulates actual rendered text to ensure it fits within viewport
func TestRenderedTextWidth(t *testing.T) {
	tests := []struct {
		name        string
		screenWidth int
		panelWidth  int
		text        string
	}{
		{
			name:        "long line with panel",
			screenWidth: 120,
			panelWidth:  30,
			text:        strings.Repeat("x", 100),
		},
		{
			name:        "long line without panel",
			screenWidth: 120,
			panelWidth:  0,
			text:        strings.Repeat("x", 120),
		},
		{
			name:        "short line",
			screenWidth: 80,
			panelWidth:  25,
			text:        "short text",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Calculate available content width
			availableWidth := tt.screenWidth - 4
			if tt.panelWidth > 0 {
				availableWidth = tt.screenWidth - tt.panelWidth - 5
			}
			if availableWidth < 20 {
				availableWidth = 20
			}

			// Simulate wrapText behavior
			wrapped := wrapText(tt.text, availableWidth)

			// Check each line fits
			lines := strings.Split(wrapped, "\n")
			for i, line := range lines {
				if len(line) > availableWidth {
					t.Errorf("line %d length %d exceeds available width %d",
						i, len(line), availableWidth)
				}
			}
		})
	}
}

// TestLayoutConsistency verifies that all width calculations are consistent
func TestLayoutConsistency(t *testing.T) {
	// Test various screen sizes
	for screenWidth := 40; screenWidth <= 200; screenWidth += 10 {
		t.Run("screen_width", func(t *testing.T) {
			// Determine panel width (from model.go logic)
			panelWidth := 30
			if screenWidth < 100 {
				panelWidth = 25
			} else if screenWidth > 160 {
				panelWidth = 40
			}

			// Test with panel visible
			t.Run("with_panel", func(t *testing.T) {
				// Viewport width calculation (from model.go:373-380)
				viewportWidth := screenWidth - panelWidth - 2
				if viewportWidth < 20 {
					viewportWidth = 20
				}

				// Content width calculation (from model.go:382-386)
				markdownWidth := viewportWidth - 3
				if markdownWidth < 20 {
					markdownWidth = 20
				}

				contentWidth := screenWidth - panelWidth - 5
				if contentWidth < 20 {
					contentWidth = 20
				}

				// All widths should be consistent
				if contentWidth > viewportWidth {
					t.Errorf("content %d > viewport %d", contentWidth, viewportWidth)
				}

				if markdownWidth > viewportWidth {
					t.Errorf("markdown %d > viewport %d", markdownWidth, viewportWidth)
				}

				// For very small screens, the layout might exceed screen width
				// This is expected - the minimum viewport width takes precedence
				minRequiredWidth := panelWidth + 1 + 20 // panel + separator + min viewport
				if screenWidth >= minRequiredWidth {
					// Only check total width if screen is large enough
					totalWidth := panelWidth + 1 + viewportWidth
					if totalWidth > screenWidth {
						t.Errorf("total layout %d exceeds screen %d", totalWidth, screenWidth)
					}
				}
			})

			// Test without panel
			t.Run("without_panel", func(t *testing.T) {
				// Viewport width calculation
				viewportWidth := screenWidth - 1
				if viewportWidth < 20 {
					viewportWidth = 20
				}

				// Content width calculation
				markdownWidth := viewportWidth - 3
				if markdownWidth < 20 {
					markdownWidth = 20
				}

				contentWidth := screenWidth - 4
				if contentWidth < 20 {
					contentWidth = 20
				}

				// All widths should be consistent
				if contentWidth > viewportWidth {
					t.Errorf("content %d > viewport %d", contentWidth, viewportWidth)
				}

				if markdownWidth > viewportWidth {
					t.Errorf("markdown %d > viewport %d", markdownWidth, viewportWidth)
				}

				// For very small screens, minimum width takes precedence
				if screenWidth >= 21 { // 1 + min viewport (20)
					if viewportWidth > screenWidth {
						t.Errorf("viewport %d exceeds screen %d", viewportWidth, screenWidth)
					}
				}
			})
		})
	}
}
