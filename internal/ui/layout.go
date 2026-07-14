package ui

import (
	"fmt"
)

const (
	// The compact transcript remains usable at 30 columns. Configuration and
	// runtime detail live in overlays, so the conversation always owns the
	// complete terminal width.
	minTerminalWidth  = 30
	minTerminalHeight = 12
)

// layoutConfig holds adaptive layout parameters based on terminal size.
type layoutConfig struct {
	ToolIndent     string
	ToolSummaryMax int
	ArgsTruncMax   int
	ResultTruncMax int
}

// currentLayout returns layout parameters adapted to the current terminal size
// and user compact preference.
func (m *Model) currentLayout() layoutConfig {
	if m.forceCompact || m.width < 80 || m.height < 24 {
		return layoutConfig{
			ToolIndent:     "  ",
			ToolSummaryMax: 40,
			ArgsTruncMax:   100,
			ResultTruncMax: 150,
		}
	}
	if m.width > 120 {
		return layoutConfig{
			ToolIndent:     "      ",
			ToolSummaryMax: 80,
			ArgsTruncMax:   300,
			ResultTruncMax: 500,
		}
	}
	return layoutConfig{
		ToolIndent:     "      ",
		ToolSummaryMax: 60,
		ArgsTruncMax:   200,
		ResultTruncMax: 300,
	}
}

// chatPaneWidth is the width owned by the viewport component. Keep this in one
// place so welcome text, messages, tool cards, and diffs all obey the same
// boundary as the smart parent model.
func (m *Model) chatPaneWidth() int {
	w := m.width - 1
	if w < 1 {
		return 1
	}
	return w
}

// chatContentWidth is the single wrapping width used by both Glamour and the
// transcript renderer. Keeping it stable across height-only resizes preserves
// completed-message caches and avoids unnecessary full transcript work.
func (m *Model) chatContentWidth() int {
	width := m.chatPaneWidth() - 6
	if width < 14 {
		width = 14
	}
	return width
}

// narrowTerminalHint returns a recovery-oriented empty state instead of
// letting fixed-width components overflow or disappear.
func (m *Model) narrowTerminalHint() string {
	if m.height < minTerminalHeight && m.width < minTerminalWidth {
		return fmt.Sprintf("Resize the terminal to at least %d columns × %d rows.", minTerminalWidth, minTerminalHeight)
	}
	if m.height < minTerminalHeight {
		return fmt.Sprintf("Resize the terminal to at least %d rows.", minTerminalHeight)
	}
	if m.width < minTerminalWidth {
		return fmt.Sprintf("Resize the terminal to at least %d columns.", minTerminalWidth)
	}
	return ""
}

// terminalInteractionPaused keeps both terminal safety fallbacks honest: while
// the decision/composer surfaces cannot be rendered, they cannot consume input.
// Resize and asynchronous receipts are still processed by the parent model;
// Ctrl+C deliberately falls through to the ordinary graceful-shutdown path.
func (m *Model) terminalInteractionPaused() bool {
	return m.ready && (m.narrowTerminalHint() != "" || m.terminalInputResumeActive())
}
