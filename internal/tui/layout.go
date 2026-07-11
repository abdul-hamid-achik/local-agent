package tui

import (
	"fmt"
	"strings"
)

const (
	// The compact transcript remains usable at 30 columns when the sidebar is
	// hidden. With the sidebar open, preserve at least 25 columns for chat.
	minTerminalWidth            = 30
	minTerminalHeight           = 12
	minTerminalWidthWithSidebar = 52
)

// layoutConfig holds adaptive layout parameters based on terminal size.
type layoutConfig struct {
	ContentPad     int
	ToolIndent     string
	ToolSummaryMax int
	ArgsTruncMax   int
	ResultTruncMax int
	HeaderMode     string // "full" or "compact"
}

// currentLayout returns layout parameters adapted to the current terminal size
// and user compact preference.
func (m *Model) currentLayout() layoutConfig {
	if m.forceCompact || m.width < 80 || m.height < 24 {
		return layoutConfig{
			ContentPad:     2,
			ToolIndent:     "  ",
			ToolSummaryMax: 40,
			ArgsTruncMax:   100,
			ResultTruncMax: 150,
			HeaderMode:     "compact",
		}
	}
	if m.width > 120 {
		return layoutConfig{
			ContentPad:     4,
			ToolIndent:     "      ",
			ToolSummaryMax: 80,
			ArgsTruncMax:   300,
			ResultTruncMax: 500,
			HeaderMode:     "full",
		}
	}
	return layoutConfig{
		ContentPad:     4,
		ToolIndent:     "      ",
		ToolSummaryMax: 60,
		ArgsTruncMax:   200,
		ResultTruncMax: 300,
		HeaderMode:     "full",
	}
}

// chatPaneWidth is the width owned by the viewport component. Keep this in one
// place so welcome text, messages, tool cards, and diffs all obey the same
// boundary as the smart parent model.
func (m *Model) chatPaneWidth() int {
	w := m.width - 1
	if m.sidePanel.IsVisible() {
		w = m.width - m.sidePanel.width - 2
	}
	if w < 1 {
		return 1
	}
	return w
}

// renderedRightPaneWidth includes the extra column used by the footer/ruler
// next to the viewport. It is deliberately separate from chatPaneWidth.
func (m *Model) renderedRightPaneWidth() int {
	w := m.width - 1
	if m.sidePanel.IsVisible() {
		w = m.width - m.sidePanel.width - 1
	}
	if w < 1 {
		return 1
	}
	return w
}

// narrowTerminalHint returns a recovery-oriented empty state instead of
// letting fixed-width components overflow or disappear.
func (m *Model) narrowTerminalHint() string {
	if m.height < minTerminalHeight {
		return fmt.Sprintf("Resize the terminal to at least %d rows.", minTerminalHeight)
	}
	if m.sidePanel.IsVisible() && m.width < minTerminalWidthWithSidebar {
		if m.state != StateIdle {
			return "Press Esc to cancel active work, then Ctrl+B to hide the sidebar."
		}
		return "Press Ctrl+B to hide the sidebar, or widen the terminal."
	}
	if m.width < minTerminalWidth {
		return fmt.Sprintf("Resize the terminal to at least %d columns.", minTerminalWidth)
	}
	return ""
}

// contextProgressBar renders a mini progress bar: █████░░░░░ 42%
func contextProgressBar(pct int) string {
	const barWidth = 10
	filled := pct * barWidth / 100
	if filled > barWidth {
		filled = barWidth
	}
	if filled < 0 {
		filled = 0
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled) + fmt.Sprintf(" %d%%", pct)
}
