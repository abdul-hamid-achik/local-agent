package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/abdul-hamid-achik/local-agent/internal/command"
)

// renderHelpOverlay builds a centered help modal showing keyboard shortcuts
// and slash commands.
func (m *Model) renderHelpOverlay(contentWidth int) string {
	maxW := 60
	if contentWidth < maxW+4 {
		maxW = contentWidth - 4
	}
	if maxW < 30 {
		maxW = 30
	}

	var b strings.Builder

	// Title.
	b.WriteString(m.styles.OverlayTitle.Render("Help"))
	b.WriteString("\n\n")

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
		{"y", "Copy last response"},
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
			name := "/" + cmd.Name
			if len(cmd.Aliases) > 0 {
				name += " (/" + strings.Join(cmd.Aliases, ", /") + ")"
			}
			fmt.Fprintf(&b, "  %s  %s\n",
				m.styles.FocusIndicator.Width(16).Render("/"+cmd.Name),
				m.styles.OverlayDim.Render(cmd.Description),
			)
		}
	}

	b.WriteString("\n")
	b.WriteString(m.styles.OverlayDim.Render("Press Esc or q to close"))

	// Wrap in a box.
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(m.styles.FocusIndicator.GetForeground()).
		Padding(1, 2).
		Width(maxW)

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
