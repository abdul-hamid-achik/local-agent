package ui

import (
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
)

const (
	maxToolCardSummaryWidth = 96
	maxToolCardSummaryBytes = 512
	maxToolCardResultBytes  = 2000
)

// ToolCardKind represents the type of tool operation.
type ToolCardKind int

const (
	ToolCardFile ToolCardKind = iota
	ToolCardBash
	ToolCardSearch
	ToolCardGit
	ToolCardGeneric
)

// ToolCardState represents the execution state.
type ToolCardState int

const (
	ToolCardRunning ToolCardState = iota
	ToolCardSuccess
	ToolCardError
)

// ToolCard is a fancy tool execution display component.
type ToolCard struct {
	ID        string
	Name      string
	Kind      ToolCardKind
	State     ToolCardState
	Summary   string
	Args      string
	Result    string
	StartTime time.Time
	Duration  time.Duration
	Expanded  bool
	Styles    ToolCardStyles
}

// ToolCardStyles holds styles for the tool card.
type ToolCardStyles struct {
	BorderRunning lipgloss.Style
	BorderSuccess lipgloss.Style
	BorderError   lipgloss.Style
	TitleRunning  lipgloss.Style
	TitleSuccess  lipgloss.Style
	TitleError    lipgloss.Style
	Args          lipgloss.Style
	Result        lipgloss.Style
	Error         lipgloss.Style
	Dimmed        lipgloss.Style
	Elapsed       lipgloss.Style
}

// NewToolCardStyles creates styles based on theme.
func NewToolCardStyles(isDark bool) ToolCardStyles {
	palette := outputSemanticPalette(isDark)

	return ToolCardStyles{
		BorderRunning: lipgloss.NewStyle().Foreground(palette.Accent2),
		BorderSuccess: lipgloss.NewStyle().Foreground(palette.Success),
		BorderError:   lipgloss.NewStyle().Foreground(palette.Error),
		TitleRunning:  lipgloss.NewStyle().Foreground(palette.Accent).Bold(true),
		TitleSuccess:  lipgloss.NewStyle().Foreground(palette.Success).Bold(true),
		TitleError:    lipgloss.NewStyle().Foreground(palette.Error).Bold(true),
		Args:          lipgloss.NewStyle().Foreground(palette.Muted),
		Result:        lipgloss.NewStyle().Foreground(palette.Muted),
		Error:         lipgloss.NewStyle().Foreground(palette.Error),
		Dimmed:        lipgloss.NewStyle().Foreground(palette.Dim),
		Elapsed:       lipgloss.NewStyle().Foreground(palette.Accent2),
	}
}

// NewToolCard creates a new tool card.
func NewToolCard(name string, kind ToolCardKind, isDark bool) ToolCard {
	return ToolCard{
		Name:   name,
		Kind:   kind,
		State:  ToolCardRunning,
		Styles: NewToolCardStyles(isDark),
	}
}

// SetDark updates the theme.
func (c *ToolCard) SetDark(isDark bool) {
	c.Styles = NewToolCardStyles(isDark)
}

// SetSummary stores a bounded, single-line semantic summary for compact and
// running headers. Callers should prefer this over assigning Summary directly;
// rendering applies the same bound defensively either way.
func (c *ToolCard) SetSummary(summary string) {
	c.Summary = boundedToolCardSummary(summary)
}

// statusGlyph returns a clean, single-width fallback glyph (no emoji — emoji
// are double-width in some terminals and clash with the Nord aesthetic). The
// parent may replace the running glyph through ViewWithActivity.
func (c ToolCard) statusGlyph() string {
	switch c.State {
	case ToolCardSuccess:
		return "✓"
	case ToolCardError:
		return "✗"
	default:
		return "●"
	}
}

// getBorderStyle returns the appropriate border style.
func (c ToolCard) getBorderStyle() lipgloss.Style {
	switch c.State {
	case ToolCardRunning:
		return c.Styles.BorderRunning
	case ToolCardSuccess:
		return c.Styles.BorderSuccess
	case ToolCardError:
		return c.Styles.BorderError
	default:
		return c.Styles.BorderRunning
	}
}

// getTitleStyle returns the appropriate title style.
func (c ToolCard) getTitleStyle() lipgloss.Style {
	switch c.State {
	case ToolCardRunning:
		return c.Styles.TitleRunning
	case ToolCardSuccess:
		return c.Styles.TitleSuccess
	case ToolCardError:
		return c.Styles.TitleError
	default:
		return c.Styles.TitleRunning
	}
}

// View renders a stable card suitable for completed receipts, cached transcript
// content, and tests. Running cards use a static activity glyph and intentionally
// omit live elapsed time; the smart parent can provide both via ViewWithActivity.
func (c ToolCard) View(width int) string {
	return c.ViewWithActivity(width, "", 0)
}

// ViewWithActivity renders without mutating card state. The smart parent owns
// animation and elapsed-time updates and may pass one shared activity glyph plus
// an explicit elapsed duration for a running card.
func (c ToolCard) ViewWithActivity(width int, activityGlyph string, elapsed time.Duration) string {
	if width < 4 {
		width = 4
	}
	inner := width - 2 // gutter is "│ "

	titleStyle := c.getTitleStyle()
	presentation := presentTool(c.Name, c.Kind, c.State)

	// Leading glyph and trailing timing meta. Running animation is supplied by
	// the parent so every card can share one Bubbles spinner tick chain.
	var glyph, meta string
	if c.State == ToolCardRunning {
		glyph = strings.TrimSpace(activityGlyph)
		if glyph == "" || lipgloss.Width(glyph) > inner {
			glyph = titleStyle.Render(c.statusGlyph())
		}
		if elapsed > 0 {
			meta = c.Styles.Elapsed.Render(formatDuration(elapsed))
		}
	} else {
		glyph = titleStyle.Render(c.statusGlyph())
		meta = c.Styles.Dimmed.Render("(" + formatDuration(c.Duration) + ")")
	}

	// Keep at least a small, readable name. Timing yields first, then the summary,
	// when the terminal is too narrow for all semantic header fields.
	glyphW := lipgloss.Width(glyph)
	metaW := lipgloss.Width(meta)
	if meta != "" && inner-glyphW-metaW-2 < 1 {
		meta = ""
		metaW = 0
	}
	textBudget := inner - glyphW - 1
	if meta != "" {
		textBudget -= metaW + 1
	}
	nameBudget := max(0, textBudget)
	summary := ""
	summaryBudget := 0
	if (c.State == ToolCardRunning || !c.Expanded) && c.Summary != "" && textBudget >= 7 {
		summary = boundedToolCardSummary(c.Summary)
		summaryW := lipgloss.Width(summary)
		summaryBudget = min(summaryW, max(1, textBudget/2))
		nameBudget = textBudget - summaryBudget - 3 // " · "
		if nameBudget < 1 {
			summary = ""
			summaryBudget = 0
			nameBudget = max(0, textBudget)
		} else if nameW := lipgloss.Width(presentation.label); nameW < nameBudget {
			summaryBudget = min(summaryW, summaryBudget+(nameBudget-nameW))
			nameBudget = nameW
		}
	}
	name := truncateDisplay(presentation.label, max(0, nameBudget))
	header := glyph
	if name != "" {
		header += " " + titleStyle.Render(name)
	}
	if summary != "" && summaryBudget > 0 {
		header += c.Styles.Dimmed.Render(" · " + truncateDisplay(summary, summaryBudget))
	}
	if meta != "" {
		header += " " + meta
	}

	lines := []string{header}

	if c.Expanded && c.State != ToolCardRunning {
		if presentation.differsFromRaw() {
			lines = append(lines, c.Styles.Dimmed.Render(truncateDisplay("tool: "+presentation.raw, inner)))
		}
		if c.Args != "" {
			lines = append(lines, c.Styles.Dimmed.Render(truncateDisplay("args: "+c.Args, inner)))
		}
	}
	if c.State == ToolCardError || (c.Expanded && c.State != ToolCardRunning) {
		result := strings.TrimRight(c.Result, "\n")
		if c.State == ToolCardError && strings.TrimSpace(result) == "" {
			result = "(no error details)"
		}
		if result != "" {
			resultStyle := c.Styles.Result
			if c.State == ToolCardError {
				resultStyle = c.Styles.Error
			}
			for _, resultLine := range strings.Split(result, "\n") {
				lines = append(lines, resultStyle.Render(truncateDisplay(resultLine, inner)))
			}
		}
	}

	// Prefix every line with a state-colored gutter bar.
	bar := c.getBorderStyle().Render("│")
	var b strings.Builder
	for i, ln := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(bar + " " + ln)
	}
	return b.String()
}

func boundedToolCardSummary(summary string) string {
	summary = strings.Join(strings.Fields(summary), " ")
	if len(summary) > maxToolCardSummaryBytes {
		cut := maxToolCardSummaryBytes
		for cut > 0 && !utf8.RuneStart(summary[cut]) {
			cut--
		}
		summary = summary[:cut]
	}
	return truncateDisplay(summary, maxToolCardSummaryWidth)
}

func boundedToolCardResult(result string) string {
	if len(result) <= maxToolCardResultBytes {
		return result
	}
	cut := maxToolCardResultBytes - 3
	for cut > 0 && !utf8.RuneStart(result[cut]) {
		cut--
	}
	return result[:cut] + "..."
}

// ToolCardManager manages tool-card receipts correlated by invocation ID.
type ToolCardManager struct {
	Cards  []ToolCard
	IsDark bool
}

// NewToolCardManager creates a new manager.
func NewToolCardManager(isDark bool) ToolCardManager {
	return ToolCardManager{
		Cards:  []ToolCard{},
		IsDark: isDark,
	}
}

// AddCardWithID adds a card correlated to one concrete tool invocation.
func (m *ToolCardManager) AddCardWithID(id, name string, kind ToolCardKind, startTime time.Time) {
	card := NewToolCard(name, kind, m.IsDark)
	card.ID = id
	card.StartTime = startTime
	m.Cards = append(m.Cards, card)
}

// UpdateCardWithID completes the exact invocation, even when multiple running
// calls use the same tool name.
func (m *ToolCardManager) UpdateCardWithID(id, name string, state ToolCardState, result string, duration time.Duration) {
	for i := len(m.Cards) - 1; i >= 0; i-- {
		if toolCallMatches(id, name, m.Cards[i].ID, m.Cards[i].Name) && m.Cards[i].State == ToolCardRunning {
			m.Cards[i].State = state
			m.Cards[i].Result = result
			m.Cards[i].Duration = duration
			break
		}
	}
}

func toolCallMatches(id, name, candidateID, candidateName string) bool {
	if id != "" || candidateID != "" {
		return id != "" && id == candidateID
	}
	return name == candidateName
}

// SetDark updates theme for all cards.
func (m *ToolCardManager) SetDark(isDark bool) {
	m.IsDark = isDark
	for i := range m.Cards {
		m.Cards[i].SetDark(isDark)
	}
}
