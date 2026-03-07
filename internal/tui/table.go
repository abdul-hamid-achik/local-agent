package tui

import (
	"strings"

	"charm.land/bubbles/v2/table"
	"charm.land/lipgloss/v2"
)

// TableHelper provides utilities for rendering structured data as tables.
type TableHelper struct {
	isDark  bool
	styles  TableStyles
}

// TableStyles holds styling for tables.
type TableStyles struct {
	Header    lipgloss.Style
	Row       lipgloss.Style
	RowAlt    lipgloss.Style
	Selected  lipgloss.Style
	Border    lipgloss.Style
	Focused   lipgloss.Style
}

// DefaultTableStyles returns default styles.
func DefaultTableStyles(isDark bool) TableStyles {
	if isDark {
		return TableStyles{
			Header:   lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#88c0d0")),
			Row:       lipgloss.NewStyle().Foreground(lipgloss.Color("#d8dee9")),
			RowAlt:    lipgloss.NewStyle().Foreground(lipgloss.Color("#d8dee9")),
			Selected:  lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#88c0d0")),
			Border:    lipgloss.NewStyle().Foreground(lipgloss.Color("#4c566a")),
			Focused:   lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#81a1c1")),
		}
	}
	return TableStyles{
		Header:   lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#4f8f8f")),
		Row:      lipgloss.NewStyle().Foreground(lipgloss.Color("#4c566a")),
		RowAlt:   lipgloss.NewStyle().Foreground(lipgloss.Color("#4c566a")),
		Selected: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#4f8f8f")),
		Border:   lipgloss.NewStyle().Foreground(lipgloss.Color("#9ca0a8")),
		Focused:  lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#5e81ac")),
	}
}

// NewTableHelper creates a new table helper.
func NewTableHelper(isDark bool) *TableHelper {
	return &TableHelper{
		isDark:  isDark,
		styles:  DefaultTableStyles(isDark),
	}
}

// SetDark updates theme.
func (th *TableHelper) SetDark(isDark bool) {
	th.isDark = isDark
	th.styles = DefaultTableStyles(isDark)
}

// ParseMarkdownTable attempts to extract a table from markdown text.
// Returns nil if no valid table found.
func (th *TableHelper) ParseMarkdownTable(text string) [][]string {
	lines := strings.Split(text, "\n")
	var rows [][]string
	inTable := false

	for _, line := range lines {
		line = strings.TrimSpace(line)
		// Check for table start (contains |)
		if strings.Contains(line, "|") {
			// Skip separator line (contains only -, |, :)
			if strings.Contains(line, "---") {
				inTable = true
				continue
			}
			// Parse row
			row := th.parseTableRow(line)
			if len(row) > 0 {
				rows = append(rows, row)
			}
		} else if inTable && len(rows) > 0 {
			// End of table
			break
		}
	}

	if len(rows) < 2 {
		return nil // Need at least header + 1 row
	}
	return rows
}

// parseTableRow parses a single table row.
func (th *TableHelper) parseTableRow(line string) []string {
	// Remove leading/trailing |
	line = strings.Trim(line, "|")
	parts := strings.Split(line, "|")
	
	var row []string
	for _, part := range parts {
		cell := strings.TrimSpace(part)
		if cell != "" || len(row) > 0 {
			row = append(row, cell)
		}
	}
	return row
}

// RenderTable creates a Bubble Tea table from parsed data.
func (th *TableHelper) RenderTable(rows [][]string, width int) string {
	if len(rows) < 2 {
		return ""
	}

	headers := rows[0]
	cols := make([]table.Column, len(headers))
	for i, h := range headers {
		w := len(h)
		// Calculate max width for this column
		for _, row := range rows[1:] {
			if i < len(row) && len(row[i]) > w {
				w = len(row[i])
			}
		}
		// Distribute remaining width
		if w < 10 {
			w = 10
		}
		cols[i] = table.Column{Width: w}
	}

	t := table.New(
		table.WithColumns(cols),
		table.WithRows(parseRows(rows[1:])),
		table.WithFocused(true),
		table.WithHeight(len(rows)-1),
	)

	return t.View()
}

// parseRows converts string rows to table.Row type.
func parseRows(rows [][]string) []table.Row {
	result := make([]table.Row, len(rows))
	for i, row := range rows {
		result[i] = table.Row(row)
	}
	return result
}

// DetectJSONArray attempts to parse and render JSON arrays as tables.
func (th *TableHelper) DetectJSONArray(text string) (string, bool) {
	// Simple JSON array detection - looks for [ at start and ] at end
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "[") || !strings.HasSuffix(text, "]") {
		return "", false
	}

	// For now, return empty - full JSON parsing would require the json package
	// This is a placeholder for future enhancement
	return "", false
}
