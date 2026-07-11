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
	hints := []keyHint{
		{Key: m.keys.Cancel.Help().Key, Action: m.overlayCloseLabel()},
		{Key: m.keys.CompleteSelect.Help().Key, Action: "select"},
		{Key: "↑/↓", Action: "move"},
	}
	if filterable {
		hints = append(hints, keyHint{Key: "/", Action: "filter"})
	}
	return m.renderKeyHints(width, hints...)
}
