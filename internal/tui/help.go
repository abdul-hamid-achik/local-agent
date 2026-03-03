package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/viewport"
	"charm.land/lipgloss/v2"

	"github.com/abdul-hamid-achik/local-agent/internal/command"
)

// helpContentWidth returns the inner width for the help modal content.
func (m *Model) helpContentWidth() int {
	maxW := 60
	if m.width < maxW+8 {
		maxW = m.width - 8
	}
	if maxW < 30 {
		maxW = 30
	}
	return maxW
}

// helpViewportHeight returns the viewport height for the help modal.
func (m *Model) helpViewportHeight() int {
	// Leave room for border (2), padding (2), title (2), footer (1)
	h := m.height - 10
	if h < 5 {
		h = 5
	}
	return h
}

// buildHelpContent builds the raw help text (without border/viewport wrapper).
func (m *Model) buildHelpContent(innerW int) string {
	var b strings.Builder

	// Keyboard shortcuts section.
	b.WriteString(m.styles.OverlayAccent.Render("Keyboard Shortcuts"))
	b.WriteString("\n")

	shortcuts := []struct{ key, desc string }{
		{"enter", "Send message"},
		{"shift+enter", "New line in input"},
		{"shift+tab", "Cycle mode (ASK/PLAN/BUILD)"},
		{"ctrl+m", "Quick model switch"},
		{"esc", "Cancel streaming / close overlay"},
		{"ctrl+c", "Quit"},
		{"ctrl+l", "Clear screen (keep history)"},
		{"ctrl+n", "New conversation"},
		{"?", "Toggle this help (when input empty)"},
		{"t", "Expand/collapse all tools"},
		{"space", "Toggle last tool details"},
		{"ctrl+y", "Copy last response"},
		{"ctrl+t", "Toggle thinking display"},
		{"ctrl+k", "Toggle compact mode"},
		{"ctrl+e", "Open input in $EDITOR"},
		{"↑/↓", "Browse input history"},
		{"pgup/pgdown", "Scroll viewport"},
		{"ctrl+u/d", "Half-page scroll"},
		{"tab", "Autocomplete (commands/files/skills)"},
	}

	for _, s := range shortcuts {
		fmt.Fprintf(&b, "  %s  %s\n",
			m.styles.FocusIndicator.Width(16).Render(s.key),
			m.styles.OverlayDim.Render(s.desc),
		)
	}

	b.WriteString("\n")
	b.WriteString(m.styles.OverlayAccent.Render("Input Shortcuts"))
	b.WriteString("\n")

	inputShortcuts := []struct{ key, desc string }{
		{"@file", "Attach file or agent"},
		{"#skill", "Activate skill"},
		{"/cmd", "Run slash command"},
	}

	for _, s := range inputShortcuts {
		fmt.Fprintf(&b, "  %s  %s\n",
			m.styles.FocusIndicator.Width(16).Render(s.key),
			m.styles.OverlayDim.Render(s.desc),
		)
	}

	b.WriteString("\n")
	b.WriteString(m.styles.OverlayAccent.Render("Slash Commands"))
	b.WriteString("\n")

	// Slash commands.
	if m.cmdRegistry != nil {
		for _, cmd := range m.cmdRegistry.All() {
			fmt.Fprintf(&b, "  %s  %s\n",
				m.styles.FocusIndicator.Width(16).Render("/"+cmd.Name),
				m.styles.OverlayDim.Render(cmd.Description),
			)
		}
	}

	return b.String()
}

// initHelpViewport creates and populates the help viewport for scrolling.
func (m *Model) initHelpViewport() {
	innerW := m.helpContentWidth()
	vpH := m.helpViewportHeight()

	m.helpViewport = viewport.New(
		viewport.WithWidth(innerW),
		viewport.WithHeight(vpH),
	)
	// Disable default arrow key bindings (we handle j/k/up/down ourselves via parent)
	m.helpViewport.KeyMap.Up.SetEnabled(false)
	m.helpViewport.KeyMap.Down.SetEnabled(false)
	m.helpViewport.KeyMap.PageUp.SetEnabled(false)
	m.helpViewport.KeyMap.PageDown.SetEnabled(false)
	m.helpViewport.KeyMap.HalfPageUp.SetEnabled(false)
	m.helpViewport.KeyMap.HalfPageDown.SetEnabled(false)

	content := m.buildHelpContent(innerW)
	m.helpViewport.SetContent(content)
}

// renderHelpOverlay builds a centered, scrollable help modal.
func (m *Model) renderHelpOverlay(contentWidth int) string {
	innerW := m.helpContentWidth()

	var b strings.Builder

	// Title.
	b.WriteString(m.styles.OverlayTitle.Render("Help"))
	b.WriteString("\n\n")

	// Viewport content (scrollable).
	b.WriteString(m.helpViewport.View())
	b.WriteString("\n")

	// Scroll indicator / footer.
	pct := m.helpViewport.ScrollPercent()
	var hint string
	if pct <= 0 {
		hint = "↓ scroll for more"
	} else if pct >= 1.0 {
		hint = "Esc or q to close"
	} else {
		hint = fmt.Sprintf("%.0f%% · j/k to scroll", pct*100)
	}
	b.WriteString(m.styles.OverlayDim.Render(hint))

	// Wrap in a box.
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(m.styles.FocusIndicator.GetForeground()).
		Padding(1, 2).
		Width(innerW + 6) // +6 for padding (2*2) + border (2)

	return box.Render(b.String())
}

// overlayOnContent renders the overlay centered on the viewport area.
func (m *Model) overlayOnContent(base, overlay string) string {
	baseLines := strings.Split(base, "\n")
	overlayLines := strings.Split(overlay, "\n")

	// Center vertically.
	startY := (len(baseLines) - len(overlayLines)) / 2
	if startY < 0 {
		startY = 0
	}

	for i, ol := range overlayLines {
		row := startY + i
		if row >= len(baseLines) {
			break
		}
		// Center horizontally.
		olW := lipgloss.Width(ol)
		padLeft := (m.width - olW) / 2
		if padLeft < 0 {
			padLeft = 0
		}
		baseLines[row] = strings.Repeat(" ", padLeft) + ol
	}

	return strings.Join(baseLines, "\n")
}

// commandHelpEntries extracts SkillInfo from commands for display.
func commandHelpEntries(reg *command.Registry) []struct{ Name, Desc string } {
	var entries []struct{ Name, Desc string }
	if reg == nil {
		return entries
	}
	for _, cmd := range reg.All() {
		entries = append(entries, struct{ Name, Desc string }{
			Name: "/" + cmd.Name,
			Desc: cmd.Description,
		})
	}
	return entries
}
