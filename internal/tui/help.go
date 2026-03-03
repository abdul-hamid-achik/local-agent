package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/abdulachik/local-agent/internal/command"
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

	lightDark := lipgloss.LightDark(m.isDark)

	borderColor := lightDark(
		lipgloss.Color("#999999"),
		lipgloss.Color("#616e88"),
	)
	titleColor := lightDark(
		lipgloss.Color("#333333"),
		lipgloss.Color("#88c0d0"),
	)
	keyColor := lightDark(
		lipgloss.Color("#0066cc"),
		lipgloss.Color("#81a1c1"),
	)
	descColor := lightDark(
		lipgloss.Color("#555555"),
		lipgloss.Color("#d8dee9"),
	)
	sectionColor := lightDark(
		lipgloss.Color("#666666"),
		lipgloss.Color("#d8dee9"),
	)

	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(titleColor)
	keyStyle := lipgloss.NewStyle().
		Foreground(keyColor).
		Bold(true)
	descStyle := lipgloss.NewStyle().
		Foreground(descColor)
	sectionStyle := lipgloss.NewStyle().
		Foreground(sectionColor).
		Bold(true)

	var b strings.Builder

	// Title.
	b.WriteString(titleStyle.Render("Help"))
	b.WriteString("\n\n")

	// Keyboard shortcuts section.
	b.WriteString(sectionStyle.Render("Keyboard Shortcuts"))
	b.WriteString("\n")

	shortcuts := []struct{ key, desc string }{
		{"enter", "Send message"},
		{"shift+enter", "New line in input"},
		{"esc", "Cancel streaming / close overlay"},
		{"ctrl+c", "Quit"},
		{"ctrl+l", "Clear screen (keep history)"},
		{"ctrl+n", "New conversation"},
		{"?", "Toggle this help (when input empty)"},
		{"t", "Expand/collapse tool details"},
		{"pgup/pgdown", "Scroll viewport"},
		{"ctrl+u/d", "Half-page scroll"},
	}

	for _, s := range shortcuts {
		fmt.Fprintf(&b, "  %s  %s\n",
			keyStyle.Width(16).Render(s.key),
			descStyle.Render(s.desc),
		)
	}

	b.WriteString("\n")
	b.WriteString(sectionStyle.Render("Slash Commands"))
	b.WriteString("\n")

	// Slash commands.
	if m.cmdRegistry != nil {
		for _, cmd := range m.cmdRegistry.All() {
			name := "/" + cmd.Name
			if len(cmd.Aliases) > 0 {
				name += " (/" + strings.Join(cmd.Aliases, ", /") + ")"
			}
			fmt.Fprintf(&b, "  %s  %s\n",
				keyStyle.Width(16).Render("/"+cmd.Name),
				descStyle.Render(cmd.Description),
			)
		}
	}

	b.WriteString("\n")
	escStyle := lipgloss.NewStyle().Foreground(keyColor)
	b.WriteString(escStyle.Render("Press Esc or q to close"))

	// Wrap in a box.
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
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
