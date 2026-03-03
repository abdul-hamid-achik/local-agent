package tui

import (
	"fmt"
	"strings"
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
