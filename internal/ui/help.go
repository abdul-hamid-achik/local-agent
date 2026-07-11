package ui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/viewport"
	"charm.land/lipgloss/v2"
)

type helpRow struct {
	key  string
	desc string
}

// helpContentWidth returns the inner width for the help modal content.
func (m *Model) helpContentWidth() int {
	w := min(60, m.width-8)
	if w < 1 {
		return 1
	}
	return w
}

// helpViewportHeight returns the viewport height for the help modal.
func (m *Model) helpViewportHeight() int {
	// Leave room for border (2), padding (2), title (2), footer (1)
	h := m.height - 10
	if h < 1 {
		h = 1
	}
	return h
}

// buildHelpContent builds the raw help text (without border/viewport wrapper).
func (m *Model) buildHelpContent(innerW int) string {
	var b strings.Builder

	// Keyboard shortcuts section.
	b.WriteString(m.styles.OverlayAccent.Render("Keyboard Shortcuts"))
	b.WriteString("\n")

	shortcuts := []helpRow{
		{"enter", "Send message"},
		{"shift+enter", "New line in input"},
		{"shift+tab", "Cycle mode (ASK/PLAN/BUILD)"},
		{"ctrl+p", "Open session settings"},
		{"ctrl+m", "Quick model switch"},
		{"esc", "Cancel streaming / close overlay"},
		{"ctrl+c", "Quit"},
		{"ctrl+l", "Clear screen (keep history)"},
		{"ctrl+n", "New conversation"},
		{"?", "Toggle this help (when input empty)"},
		{"t", "Expand/collapse tools (input empty)"},
		{"space", "Toggle last tool (input empty)"},
		{"ctrl+y", "Copy last response"},
		{"ctrl+t", "Toggle thinking display"},
		{"ctrl+k", "Toggle compact mode"},
		{"ctrl+e", "Open input in $EDITOR"},
		{"↑/↓", "Browse input history"},
		{"pgup/pgdown", "Scroll viewport"},
		{"ctrl+u/d", "Half-page scroll"},
		{"tab", "Autocomplete (commands/files/skills)"},
	}

	m.writeHelpRows(&b, shortcuts, innerW)

	b.WriteString("\n")
	b.WriteString(m.styles.OverlayAccent.Render("Input Shortcuts"))
	b.WriteString("\n")

	inputShortcuts := []helpRow{
		{"@file", "Attach file or agent"},
		{"#skill", "Activate skill"},
		{"/cmd", "Run slash command"},
	}

	m.writeHelpRows(&b, inputShortcuts, innerW)

	b.WriteString("\n")
	b.WriteString(m.styles.OverlayAccent.Render("Slash Commands"))
	b.WriteString("\n")

	// Slash commands.
	if m.cmdRegistry != nil {
		commands := make([]helpRow, 0, len(m.cmdRegistry.All()))
		for _, cmd := range m.cmdRegistry.All() {
			commands = append(commands, helpRow{key: "/" + cmd.Name, desc: cmd.Description})
		}
		m.writeHelpRows(&b, commands, innerW)
	}

	return b.String()
}

// writeHelpRows renders aligned rows on normal terminals and stacked rows on
// narrow ones. Descriptions wrap instead of being silently clipped.
func (m *Model) writeHelpRows(b *strings.Builder, rows []helpRow, innerW int) {
	if innerW < 28 {
		for _, row := range rows {
			b.WriteString("  ")
			b.WriteString(m.styles.FocusIndicator.Render(truncateDisplay(row.key, max(1, innerW-3))))
			b.WriteString("\n")
			for _, line := range strings.Split(wrapText(row.desc, max(1, innerW-5)), "\n") {
				b.WriteString("    ")
				b.WriteString(m.styles.OverlayDim.Render(line))
				b.WriteString("\n")
			}
		}
		return
	}

	keyW := 16
	if innerW < 44 {
		keyW = 10
	}
	// Leave the terminal's final cell unused. Writing exactly to the edge can
	// trigger an implicit wrap before the explicit newline in some PTYs.
	descW := max(1, innerW-keyW-5)
	for _, row := range rows {
		descLines := strings.Split(wrapText(row.desc, descW), "\n")
		for i, line := range descLines {
			if i == 0 {
				fmt.Fprintf(b, "  %s  %s\n",
					m.styles.FocusIndicator.Width(keyW).Render(truncateDisplay(row.key, keyW)),
					m.styles.OverlayDim.Render(line),
				)
				continue
			}
			b.WriteString(strings.Repeat(" ", keyW+4))
			b.WriteString(m.styles.OverlayDim.Render(line))
			b.WriteString("\n")
		}
	}
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
	closeHint := "Esc/q " + m.overlayCloseLabel()
	var hint string
	if pct <= 0 {
		hint = closeHint + " · ↓ more"
	} else if pct >= 1.0 {
		hint = closeHint
	} else {
		hint = fmt.Sprintf("%s · %.0f%% · j/k", closeHint, pct*100)
	}
	b.WriteString(m.styles.OverlayDim.Render(hint))

	// Wrap in a box.
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(m.styles.FocusIndicator.GetForeground()).
		Padding(1, 2).
		Width(innerW + 6) // outer box: inner viewport + padding (4) + border (2)

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
		line := strings.Repeat(" ", padLeft) + ol
		if lineW := lipgloss.Width(line); lineW < m.width {
			line += strings.Repeat(" ", m.width-lineW)
		}
		baseLines[row] = line
	}

	return strings.Join(baseLines, "\n")
}
