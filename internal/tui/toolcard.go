package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/lipgloss/v2"
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
	Name         string
	Kind         ToolCardKind
	State        ToolCardState
	Args         string
	Result       string
	StartTime    time.Time
	Duration     time.Duration
	Expanded     bool
	Spinner      spinner.Model
	ElapsedTimer *time.Timer
	Elapsed      time.Duration
	IsDark       bool
	Styles       ToolCardStyles
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
	if isDark {
		return ToolCardStyles{
			BorderRunning: lipgloss.NewStyle().Foreground(lipgloss.Color("#81a1c1")),
			BorderSuccess: lipgloss.NewStyle().Foreground(lipgloss.Color("#a3be8c")),
			BorderError:   lipgloss.NewStyle().Foreground(lipgloss.Color("#bf616a")),
			TitleRunning:  lipgloss.NewStyle().Foreground(lipgloss.Color("#88c0d0")).Bold(true),
			TitleSuccess:  lipgloss.NewStyle().Foreground(lipgloss.Color("#a3be8c")).Bold(true),
			TitleError:    lipgloss.NewStyle().Foreground(lipgloss.Color("#bf616a")).Bold(true),
			Args:          lipgloss.NewStyle().Foreground(lipgloss.Color("#d8dee9")),
			Result:        lipgloss.NewStyle().Foreground(lipgloss.Color("#d8dee9")),
			Error:         lipgloss.NewStyle().Foreground(lipgloss.Color("#bf616a")),
			Dimmed:        lipgloss.NewStyle().Foreground(lipgloss.Color("#4c566a")),
			Elapsed:       lipgloss.NewStyle().Foreground(lipgloss.Color("#81a1c1")),
		}
	}
	return ToolCardStyles{
		BorderRunning: lipgloss.NewStyle().Foreground(lipgloss.Color("#5e81ac")),
		BorderSuccess: lipgloss.NewStyle().Foreground(lipgloss.Color("#4f8f38")),
		BorderError:   lipgloss.NewStyle().Foreground(lipgloss.Color("#c94f4f")),
		TitleRunning:  lipgloss.NewStyle().Foreground(lipgloss.Color("#4f8f8f")).Bold(true),
		TitleSuccess:  lipgloss.NewStyle().Foreground(lipgloss.Color("#4f8f38")).Bold(true),
		TitleError:    lipgloss.NewStyle().Foreground(lipgloss.Color("#c94f4f")).Bold(true),
		Args:          lipgloss.NewStyle().Foreground(lipgloss.Color("#4c566a")),
		Result:        lipgloss.NewStyle().Foreground(lipgloss.Color("#4c566a")),
		Error:         lipgloss.NewStyle().Foreground(lipgloss.Color("#c94f4f")),
		Dimmed:        lipgloss.NewStyle().Foreground(lipgloss.Color("#9ca0a8")),
		Elapsed:       lipgloss.NewStyle().Foreground(lipgloss.Color("#5e81ac")),
	}
}

// NewToolCard creates a new tool card.
func NewToolCard(name string, kind ToolCardKind, isDark bool) ToolCard {
	s := spinner.New(
		spinner.WithSpinner(spinner.MiniDot),
		spinner.WithStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("#88c0d0"))),
	)
	return ToolCard{
		Name:    name,
		Kind:    kind,
		State:   ToolCardRunning,
		Spinner: s,
		IsDark:  isDark,
		Styles:  NewToolCardStyles(isDark),
	}
}

// SetDark updates the theme.
func (c *ToolCard) SetDark(isDark bool) {
	c.IsDark = isDark
	c.Styles = NewToolCardStyles(isDark)
}

// Tick advances the spinner animation.
func (c *ToolCard) Tick() {
	c.Spinner.Tick()
}

// UpdateElapsed updates the elapsed time counter.
func (c *ToolCard) UpdateElapsed() {
	if c.State == ToolCardRunning {
		c.Elapsed = time.Since(c.StartTime)
	}
}

// statusGlyph returns a clean, single-width status glyph (no emoji — emoji are
// double-width in some terminals and clash with the Nord aesthetic). For the
// running state the animated spinner is used instead of a static glyph.
func (c *ToolCard) statusGlyph() string {
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
func (c *ToolCard) getBorderStyle() lipgloss.Style {
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
func (c *ToolCard) getTitleStyle() lipgloss.Style {
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

// View renders the tool card as a clean left-gutter block (Crush-style) rather
// than a heavy full border box: a colored vertical bar reflecting state, a
// status glyph (or spinner), the tool name, and timing — with an indented,
// width-truncated body when expanded.
func (c *ToolCard) View(width int) string {
	c.UpdateElapsed()
	if width < 12 {
		width = 12
	}
	inner := width - 2 // gutter is "│ "

	titleStyle := c.getTitleStyle()

	// Leading glyph (animated spinner while running) and trailing timing meta.
	var glyph, meta string
	if c.State == ToolCardRunning {
		glyph = c.Spinner.View()
		meta = c.Styles.Elapsed.Render(fmt.Sprintf("%.1fs", c.Elapsed.Seconds()))
	} else {
		glyph = titleStyle.Render(c.statusGlyph())
		meta = c.Styles.Dimmed.Render("(" + formatDuration(c.Duration) + ")")
	}

	// Truncate the name (plain) so the header fits within inner width.
	nameBudget := inner - lipgloss.Width(glyph) - lipgloss.Width(meta) - 2
	name := c.Name
	if nameBudget > 1 && lipgloss.Width(name) > nameBudget {
		name = truncate(name, nameBudget)
	}
	header := glyph + " " + titleStyle.Render(name)
	if meta != "" {
		header += " " + meta
	}

	lines := []string{header}

	if c.Expanded && c.State != ToolCardRunning {
		if c.Args != "" {
			lines = append(lines, c.Styles.Dimmed.Render(truncate("args: "+c.Args, inner)))
		}
		if c.Result != "" {
			resultStyle := c.Styles.Result
			if c.State == ToolCardError {
				resultStyle = c.Styles.Error
			}
			for _, rl := range strings.Split(strings.TrimRight(c.Result, "\n"), "\n") {
				lines = append(lines, resultStyle.Render(truncate(rl, inner)))
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

// ToolCardManager manages multiple tool cards with synchronized animations.
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

// AddCard adds a new tool card.
func (m *ToolCardManager) AddCard(name string, kind ToolCardKind, startTime time.Time) {
	card := NewToolCard(name, kind, m.IsDark)
	card.StartTime = startTime
	m.Cards = append(m.Cards, card)
}

// UpdateCard updates an existing card by name.
func (m *ToolCardManager) UpdateCard(name string, state ToolCardState, result string, duration time.Duration) {
	for i := range m.Cards {
		if m.Cards[i].Name == name && m.Cards[i].State == ToolCardRunning {
			m.Cards[i].State = state
			m.Cards[i].Result = result
			m.Cards[i].Duration = duration
			break
		}
	}
}

// SetExpanded sets a card's expanded state.
func (m *ToolCardManager) SetExpanded(name string, expanded bool) {
	for i := range m.Cards {
		if m.Cards[i].Name == name {
			m.Cards[i].Expanded = expanded
			break
		}
	}
}

// Tick advances all running card spinners.
func (m *ToolCardManager) Tick() {
	for i := range m.Cards {
		if m.Cards[i].State == ToolCardRunning {
			m.Cards[i].Tick()
		}
	}
}

// SetDark updates theme for all cards.
func (m *ToolCardManager) SetDark(isDark bool) {
	m.IsDark = isDark
	for i := range m.Cards {
		m.Cards[i].SetDark(isDark)
	}
}

// View renders all cards.
func (m *ToolCardManager) View(width int) string {
	var lines []string
	for i := range m.Cards {
		lines = append(lines, m.Cards[i].View(width))
	}
	return strings.Join(lines, "\n")
}
