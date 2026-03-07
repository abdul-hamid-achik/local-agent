package tui

import (
	"charm.land/lipgloss/v2"
	tea "charm.land/bubbletea/v2"
)

// MouseHandler provides enhanced mouse interaction handling.
type MouseHandler struct {
	isDark        bool
	styles        MouseHandlerStyles
	resizer       *PanelResizer
	lastClickX    int
	lastClickY    int
	lastClickTime int64
	clickCount    int
}

// MouseHandlerStyles holds styling.
type MouseHandlerStyles struct {
	Hover      lipgloss.Style
	Selected   lipgloss.Style
	ResizeHint lipgloss.Style
}

// DefaultMouseHandlerStyles returns default styles.
func DefaultMouseHandlerStyles(isDark bool) MouseHandlerStyles {
	if isDark {
		return MouseHandlerStyles{
			Hover:      lipgloss.NewStyle().Background(lipgloss.Color("#3b4252")),
			Selected:   lipgloss.NewStyle().Background(lipgloss.Color("#4c566a")),
			ResizeHint: lipgloss.NewStyle().Foreground(lipgloss.Color("#88c0d0")),
		}
	}
	return MouseHandlerStyles{
		Hover:      lipgloss.NewStyle().Background(lipgloss.Color("#e5e9f0")),
		Selected:   lipgloss.NewStyle().Background(lipgloss.Color("#d8dee9")),
		ResizeHint: lipgloss.NewStyle().Foreground(lipgloss.Color("#4f8f8f")),
	}
}

// NewMouseHandler creates a new mouse handler.
func NewMouseHandler(isDark bool, panelMinWidth, panelMaxWidth int) *MouseHandler {
	return &MouseHandler{
		isDark:   isDark,
		styles:   DefaultMouseHandlerStyles(isDark),
		resizer:  NewPanelResizer(panelMinWidth, panelMaxWidth, isDark),
	}
}

// SetDark updates theme.
func (mh *MouseHandler) SetDark(isDark bool) {
	mh.isDark = isDark
	mh.styles = DefaultMouseHandlerStyles(isDark)
}

// ResizePanel handles resize operations.
func (mh *MouseHandler) ResizePanel() *PanelResizer {
	return mh.resizer
}

// HandleClick processes a mouse click at the given coordinates.
// Returns an action describing what happened.
func (mh *MouseHandler) HandleClick(msg tea.MouseClickMsg, panelWidth, panelDividerX int) MouseAction {
	x, y := int(msg.X), int(msg.Y)

	// Check for double-click
	isDoubleClick := mh.isDoubleClick(x, y)
	if isDoubleClick {
		mh.clickCount++
	} else {
		mh.clickCount = 1
	}
	mh.lastClickX = x
	mh.lastClickY = y

	// Check if clicking on resize handle (within 3 chars of panel divider)
	if panelWidth > 0 && mh.resizer.CanResizeAt(x, panelDividerX) {
		if msg.Button == tea.MouseLeft {
			mh.resizer.StartResize(x, panelWidth)
			return MouseAction{Type: ResizeStart}
		}
	}

	// Check for right-click (context menu)
	if msg.Button == tea.MouseRight {
		return MouseAction{
			Type:    ContextMenu,
			X:       x,
			Y:       y,
			Context: mh.getClickContext(x, y, panelWidth),
		}
	}

	return MouseAction{Type: None}
}

// HandleRelease handles mouse release events.
func (mh *MouseHandler) HandleRelease() {
	mh.resizer.EndResize()
}

// isDoubleClick checks if this is a double-click.
func (mh *MouseHandler) isDoubleClick(x, y int) bool {
	// Simple double-click detection: same position
	dist := abs(x-mh.lastClickX) + abs(y-mh.lastClickY)
	return dist < 2 && mh.clickCount > 1
}

// getClickContext returns context information about the click location.
func (mh *MouseHandler) getClickContext(x, y, panelWidth int) string {
	// Determine what was clicked based on coordinates
	if panelWidth > 0 && x < panelWidth {
		return "sidepanel"
	}
	return "main"
}

// MouseAction describes a mouse action.
type MouseAction struct {
	Type    MouseActionType
	X, Y    int
	Context string
}

// MouseActionType describes the type of mouse action.
type MouseActionType int

const (
	None MouseActionType = iota
	ResizeStart
	ContextMenu
	SelectEntry
	ToggleCollapse
	CopyText
)

// abs returns the absolute value.
func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
