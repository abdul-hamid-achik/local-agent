package tui

import (
	"charm.land/lipgloss/v2"
)

// ContextMenuItem represents an item in a context menu.
type ContextMenuItem struct {
	Label    string
	Action   string
	Shortcut string
}

// ContextMenuState holds the state for a context menu.
type ContextMenuState struct {
	X, Y     int
	Items     []ContextMenuItem
	Selected  int
	Active    bool
	isDark    bool
	styles    ContextMenuStyles
}

// ContextMenuStyles holds styling for context menus.
type ContextMenuStyles struct {
	Item     lipgloss.Style
	Selected lipgloss.Style
	Shortcut lipgloss.Style
	Border   lipgloss.Style
}

// DefaultContextMenuStyles returns default styles.
func DefaultContextMenuStyles(isDark bool) ContextMenuStyles {
	if isDark {
		return ContextMenuStyles{
			Item:     lipgloss.NewStyle().Foreground(lipgloss.Color("#d8dee9")),
			Selected: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#88c0d0")).Background(lipgloss.Color("#3b4252")),
			Shortcut: lipgloss.NewStyle().Foreground(lipgloss.Color("#4c566a")),
			Border:   lipgloss.NewStyle().Foreground(lipgloss.Color("#4c566a")),
		}
	}
	return ContextMenuStyles{
		Item:     lipgloss.NewStyle().Foreground(lipgloss.Color("#4c566a")),
		Selected: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#4f8f8f")).Background(lipgloss.Color("#e5e9f0")),
		Shortcut: lipgloss.NewStyle().Foreground(lipgloss.Color("#9ca0a8")),
		Border:   lipgloss.NewStyle().Foreground(lipgloss.Color("#9ca0a8")),
	}
}

// NewContextMenuState creates a new context menu state.
func NewContextMenuState(items []ContextMenuItem, x, y int, isDark bool) *ContextMenuState {
	return &ContextMenuState{
		X:        x,
		Y:        y,
		Items:    items,
		Selected: 0,
		Active:   true,
		isDark:   isDark,
		styles:   DefaultContextMenuStyles(isDark),
	}
}

// Activate shows the context menu at position.
func (cm *ContextMenuState) Activate(x, y int, items []ContextMenuItem) {
	cm.X = x
	cm.Y = y
	cm.Items = items
	cm.Selected = 0
	cm.Active = true
}

// Deactivate hides the context menu.
func (cm *ContextMenuState) Deactivate() {
	cm.Active = false
}

// IsActive returns true if the menu is visible.
func (cm *ContextMenuState) IsActive() bool {
	return cm.Active
}

// SelectedAction returns the action of the selected item.
func (cm *ContextMenuState) SelectedAction() string {
	if cm.Selected >= 0 && cm.Selected < len(cm.Items) {
		return cm.Items[cm.Selected].Action
	}
	return ""
}

// MoveUp selects the previous item.
func (cm *ContextMenuState) MoveUp() {
	if cm.Selected > 0 {
		cm.Selected--
	}
}

// MoveDown selects the next item.
func (cm *ContextMenuState) MoveDown() {
	if cm.Selected < len(cm.Items)-1 {
		cm.Selected++
	}
}

// Render returns the context menu view.
func (cm *ContextMenuState) Render(width int) string {
	if !cm.Active {
		return ""
	}

	styles := DefaultContextMenuStyles(cm.isDark)

	var b string
	for i, item := range cm.Items {
		row := "  " + item.Label
		if item.Shortcut != "" {
			row += "  " + styles.Shortcut.Render(item.Shortcut)
		}

		if i == cm.Selected {
			b += styles.Selected.Render(row) + "\n"
		} else {
			b += styles.Item.Render(row) + "\n"
		}
	}

	// Wrap in border
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#4c566a")).
		Padding(0, 1)

	return box.Render(b)
}
