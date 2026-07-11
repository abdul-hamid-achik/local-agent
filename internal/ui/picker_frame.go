package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

const defaultPickerItemHeight = 2

func (m *Model) renderPickerFrame(content string, maximum int, footer string) string {
	width := pickerContentWidth(m.width, maximum)
	content = strings.TrimRight(content, "\n")
	if footer != "" {
		content += "\n" + m.styles.OverlayDim.Render(footer)
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(m.styles.FocusIndicator.GetForeground()).
		Padding(0, 1).
		Width(width + 2).
		Render(content)
}

func (m *Model) pickerNavigationFooter(maximum int, filterable bool) string {
	width := pickerListWidth(m.width, maximum)
	closeLabel := m.overlayCloseLabel()
	if filterable {
		if width < 24 {
			return "/ filter Esc " + closeLabel
		}
		if width < 42 {
			return "/ filter · Esc " + closeLabel
		}
		return "Type to filter · Enter select · Esc " + closeLabel
	}
	if width < 24 {
		return "↑↓ Enter Esc " + closeLabel
	}
	if width < 42 {
		return "↑/↓ · Enter · Esc " + closeLabel
	}
	return "↑/↓ navigate · Enter select · Esc " + closeLabel
}
