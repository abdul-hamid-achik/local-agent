package tui

import (
	"charm.land/lipgloss/v2"
)

// PanelResizer manages the side panel resizing state.
type PanelResizer struct {
	isResizing    bool
	resizeStartX  int
	originalWidth int
	minWidth      int
	maxWidth      int
	isDark        bool
	styles        ResizeStyles
}

// ResizeStyles holds styling for resize indicators.
type ResizeStyles struct {
	Handle       lipgloss.Style
	HandleActive lipgloss.Style
}

// DefaultResizeStyles returns default styles.
func DefaultResizeStyles(isDark bool) ResizeStyles {
	if isDark {
		return ResizeStyles{
			Handle:       lipgloss.NewStyle().Foreground(lipgloss.Color("#4c566a")),
			HandleActive: lipgloss.NewStyle().Foreground(lipgloss.Color("#88c0d0")),
		}
	}
	return ResizeStyles{
		Handle:       lipgloss.NewStyle().Foreground(lipgloss.Color("#9ca0a8")),
		HandleActive: lipgloss.NewStyle().Foreground(lipgloss.Color("#4f8f8f")),
	}
}

// NewPanelResizer creates a new panel resizer.
func NewPanelResizer(minWidth, maxWidth int, isDark bool) *PanelResizer {
	return &PanelResizer{
		isResizing:    false,
		resizeStartX:  0,
		originalWidth: 30,
		minWidth:      minWidth,
		maxWidth:      maxWidth,
		isDark:        isDark,
		styles:        DefaultResizeStyles(isDark),
	}
}

// SetDark updates theme.
func (pr *PanelResizer) SetDark(isDark bool) {
	pr.isDark = isDark
	pr.styles = DefaultResizeStyles(isDark)
}

// StartResize begins a resize operation.
func (pr *PanelResizer) StartResize(x int, currentWidth int) {
	pr.isResizing = true
	pr.resizeStartX = x
	pr.originalWidth = currentWidth
}

// UpdateResize updates the panel width based on mouse movement.
func (pr *PanelResizer) UpdateResize(x int) int {
	if !pr.isResizing {
		return pr.originalWidth
	}

	delta := x - pr.resizeStartX
	newWidth := pr.originalWidth + delta

	// Clamp to min/max
	if newWidth < pr.minWidth {
		newWidth = pr.minWidth
	}
	if newWidth > pr.maxWidth {
		newWidth = pr.maxWidth
	}

	return newWidth
}

// EndResize ends the resize operation.
func (pr *PanelResizer) EndResize() {
	pr.isResizing = false
}

// IsResizing returns true if currently resizing.
func (pr *PanelResizer) IsResizing() bool {
	return pr.isResizing
}

// RenderHandle returns the resize handle visual.
func (pr *PanelResizer) RenderHandle(height int, isActive bool) string {
	style := pr.styles.Handle
	if isActive || pr.isResizing {
		style = pr.styles.HandleActive
	}

	// Create a vertical bar with grip dots
	var b string
	for i := 0; i < height; i++ {
		if i%2 == 0 {
			b += style.Render("│")
		} else {
			b += pr.styles.Handle.Render("│")
		}
	}
	return b
}

// CanResizeAt checks if x is within the resize zone (3 characters from divider).
func (pr *PanelResizer) CanResizeAt(x, dividerX int) bool {
	// Resize zone is 3 chars to the left of the divider
	return x >= dividerX-3 && x <= dividerX
}
