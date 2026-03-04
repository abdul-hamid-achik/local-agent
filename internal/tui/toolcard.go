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
	BorderRunning  lipgloss.Style
	BorderSuccess  lipgloss.Style
	BorderError    lipgloss.Style
	TitleRunning   lipgloss.Style
	TitleSuccess   lipgloss.Style
	TitleError     lipgloss.Style
	Args           lipgloss.Style
	Result         lipgloss.Style
	Error          lipgloss.Style
	Dimmed         lipgloss.Style
	Elapsed        lipgloss.Style
}

// NewToolCardStyles creates styles based on theme.
func NewToolCardStyles(isDark bool) ToolCardStyles {
	if isDark {
		return ToolCardStyles{
			BorderRunning:  lipgloss.NewStyle().Foreground(lipgloss.Color("#81a1c1")),
			BorderSuccess:  lipgloss.NewStyle().Foreground(lipgloss.Color("#a3be8c")),
			BorderError:    lipgloss.NewStyle().Foreground(lipgloss.Color("#bf616a")),
			TitleRunning:   lipgloss.NewStyle().Foreground(lipgloss.Color("#88c0d0")).Bold(true),
			TitleSuccess:   lipgloss.NewStyle().Foreground(lipgloss.Color("#a3be8c")).Bold(true),
			TitleError:     lipgloss.NewStyle().Foreground(lipgloss.Color("#bf616a")).Bold(true),
			Args:           lipgloss.NewStyle().Foreground(lipgloss.Color("#d8dee9")),
			Result:         lipgloss.NewStyle().Foreground(lipgloss.Color("#d8dee9")),
			Error:          lipgloss.NewStyle().Foreground(lipgloss.Color("#bf616a")),
			Dimmed:         lipgloss.NewStyle().Foreground(lipgloss.Color("#4c566a")),
			Elapsed:        lipgloss.NewStyle().Foreground(lipgloss.Color("#81a1c1")),
		}
	}
	return ToolCardStyles{
		BorderRunning:  lipgloss.NewStyle().Foreground(lipgloss.Color("#5e81ac")),
		BorderSuccess:  lipgloss.NewStyle().Foreground(lipgloss.Color("#4f8f38")),
		BorderError:    lipgloss.NewStyle().Foreground(lipgloss.Color("#c94f4f")),
		TitleRunning:   lipgloss.NewStyle().Foreground(lipgloss.Color("#4f8f8f")).Bold(true),
		TitleSuccess:   lipgloss.NewStyle().Foreground(lipgloss.Color("#4f8f38")).Bold(true),
		TitleError:     lipgloss.NewStyle().Foreground(lipgloss.Color("#c94f4f")).Bold(true),
		Args:           lipgloss.NewStyle().Foreground(lipgloss.Color("#4c566a")),
		Result:         lipgloss.NewStyle().Foreground(lipgloss.Color("#4c566a")),
		Error:          lipgloss.NewStyle().Foreground(lipgloss.Color("#c94f4f")),
		Dimmed:         lipgloss.NewStyle().Foreground(lipgloss.Color("#9ca0a8")),
		Elapsed:        lipgloss.NewStyle().Foreground(lipgloss.Color("#5e81ac")),
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

// getIcon returns the appropriate icon for the tool kind and state.
func (c *ToolCard) getIcon() string {
	switch c.Kind {
	case ToolCardFile:
		if c.State == ToolCardRunning {
			return "📄"
		}
		if c.State == ToolCardSuccess {
			return "✓"
		}
		return "✗"
	case ToolCardBash:
		if c.State == ToolCardRunning {
			return "💻"
		}
		if c.State == ToolCardSuccess {
			return "✓"
		}
		return "✗"
	case ToolCardSearch:
		if c.State == ToolCardRunning {
			return "🔍"
		}
		if c.State == ToolCardSuccess {
			return "✓"
		}
		return "✗"
	case ToolCardGit:
		if c.State == ToolCardRunning {
			return "🌿"
		}
		if c.State == ToolCardSuccess {
			return "✓"
		}
		return "✗"
	default:
		if c.State == ToolCardRunning {
			return "◌"
		}
		if c.State == ToolCardSuccess {
			return "✓"
		}
		return "✗"
	}
}

// getStatusText returns the status text based on state.
func (c *ToolCard) getStatusText() string {
	switch c.State {
	case ToolCardRunning:
		return "running..."
	case ToolCardSuccess:
		return fmt.Sprintf("(%s)", formatDuration(c.Duration))
	case ToolCardError:
		return fmt.Sprintf("error (%s)", formatDuration(c.Duration))
	default:
		return ""
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

// View renders the tool card.
func (c *ToolCard) View(width int) string {
	// Update elapsed time for running tools
	c.UpdateElapsed()

	// Build title line
	icon := c.getIcon()
	statusText := c.getStatusText()

	var titleParts []string
	titleParts = append(titleParts, icon)
	titleParts = append(titleParts, c.Name)

	if c.State == ToolCardRunning {
		titleParts = append(titleParts, c.Spinner.View())
		titleParts = append(titleParts, statusText)
		// Show elapsed time for running tools
		elapsedStr := fmt.Sprintf("%.1fs", c.Elapsed.Seconds())
		titleParts = append(titleParts, c.Styles.Elapsed.Render(elapsedStr))
	} else {
		titleParts = append(titleParts, statusText)
	}

	title := strings.Join(titleParts, " ")
	titleStyle := c.getTitleStyle()

	// Create bordered box
	content := titleStyle.Render(title)

	if c.Expanded && c.State != ToolCardRunning {
		// Show args and result when expanded
		var details strings.Builder

		if c.Args != "" {
			args := truncate(c.Args, 80)
			details.WriteString(c.Styles.Args.Render("  args: " + args))
			details.WriteString("\n")
		}

		if c.Result != "" {
			if c.State == ToolCardError {
				details.WriteString(c.Styles.Error.Render("  " + truncate(c.Result, 200)))
			} else {
				details.WriteString(c.Styles.Result.Render("  " + truncate(c.Result, 200)))
			}
			details.WriteString("\n")
		}

		content = lipgloss.JoinVertical(lipgloss.Left, content, details.String())
	}

	// Apply border
	borderStyle := c.getBorderStyle()
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderStyle.GetForeground()).
		Padding(0, 1)

	return box.Render(content)
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
