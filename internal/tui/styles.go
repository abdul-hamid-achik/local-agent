package tui

import (
	"os"
	"strings"

	"charm.land/lipgloss/v2"
)

// noColor detects NO_COLOR environment variable.
var noColor = os.Getenv("NO_COLOR") != ""

// Styles holds all pre-built lipgloss styles.
type Styles struct {
	// Header
	HeaderTitle lipgloss.Style
	HeaderInfo  lipgloss.Style
	HeaderRule  lipgloss.Style

	// Messages
	UserLabel    lipgloss.Style
	UserContent  lipgloss.Style
	AsstLabel    lipgloss.Style
	AsstContent  lipgloss.Style
	RoleRule     lipgloss.Style
	StreamCursor lipgloss.Style

	// Tools
	ToolCallIcon    lipgloss.Style
	ToolCallText    lipgloss.Style
	ToolResultIcon  lipgloss.Style
	ToolResultText  lipgloss.Style
	ToolErrorIcon   lipgloss.Style
	ToolErrorText   lipgloss.Style
	ToolDoneIcon    lipgloss.Style
	ToolDoneText    lipgloss.Style
	ToolRunningText lipgloss.Style
	ToolDetailText  lipgloss.Style

	// Footer
	Divider    lipgloss.Style
	StatusDot  lipgloss.Style
	StatusText lipgloss.Style
	StreamHint lipgloss.Style
	ErrorText  lipgloss.Style

	// System messages
	SystemText  lipgloss.Style
	WelcomeHint lipgloss.Style

	// Completion popup
	CompletionBorder   lipgloss.Style
	CompletionSelected lipgloss.Style
}

// NewStyles creates a Styles set based on the background color.
func NewStyles(isDark bool) Styles {
	if noColor {
		return plainStyles()
	}
	return adaptiveStyles(isDark)
}

func adaptiveStyles(isDark bool) Styles {
	ld := lipgloss.LightDark(isDark)

	colorDim := ld(lipgloss.Color("#888888"), lipgloss.Color("#7b88a1"))
	colorMuted := ld(lipgloss.Color("#aaaaaa"), lipgloss.Color("#616e88"))
	colorText := ld(lipgloss.Color("#333333"), lipgloss.Color("#d8dee9"))
	colorAccent := ld(lipgloss.Color("#0088bb"), lipgloss.Color("#88c0d0"))
	colorAccent2 := ld(lipgloss.Color("#0066bb"), lipgloss.Color("#81a1c1"))
	colorError := ld(lipgloss.Color("#cc3333"), lipgloss.Color("#bf616a"))
	colorSuccess := ld(lipgloss.Color("#228822"), lipgloss.Color("#a3be8c"))
	colorSpecial := ld(lipgloss.Color("#9944bb"), lipgloss.Color("#b48ead"))

	return Styles{
		HeaderTitle: lipgloss.NewStyle().
			Bold(true).
			Foreground(colorAccent).
			PaddingLeft(1),
		HeaderInfo: lipgloss.NewStyle().
			Foreground(colorDim).
			PaddingRight(1),
		HeaderRule: lipgloss.NewStyle().
			Foreground(colorMuted),

		UserLabel: lipgloss.NewStyle().
			Bold(true).
			Foreground(colorAccent2).
			PaddingLeft(2),
		UserContent: lipgloss.NewStyle().
			Foreground(colorText).
			PaddingLeft(2),
		AsstLabel: lipgloss.NewStyle().
			Bold(true).
			Foreground(colorSuccess).
			PaddingLeft(2),
		AsstContent: lipgloss.NewStyle().
			Foreground(colorText).
			PaddingLeft(4),
		RoleRule: lipgloss.NewStyle().
			Foreground(colorMuted),
		StreamCursor: lipgloss.NewStyle().
			Foreground(colorAccent).
			Bold(true),

		ToolCallIcon: lipgloss.NewStyle().
			Foreground(colorSpecial).
			PaddingLeft(4),
		ToolCallText: lipgloss.NewStyle().
			Foreground(colorSpecial),
		ToolResultIcon: lipgloss.NewStyle().
			Foreground(colorDim).
			PaddingLeft(4),
		ToolResultText: lipgloss.NewStyle().
			Foreground(colorDim),
		ToolErrorIcon: lipgloss.NewStyle().
			Foreground(colorError).
			PaddingLeft(4),
		ToolErrorText: lipgloss.NewStyle().
			Foreground(colorError),
		ToolDoneIcon: lipgloss.NewStyle().
			Foreground(colorSuccess).
			PaddingLeft(4),
		ToolDoneText: lipgloss.NewStyle().
			Foreground(colorDim),
		ToolRunningText: lipgloss.NewStyle().
			Foreground(colorAccent),
		ToolDetailText: lipgloss.NewStyle().
			Foreground(colorMuted),

		Divider: lipgloss.NewStyle().
			Foreground(colorMuted),
		StatusDot: lipgloss.NewStyle().
			Foreground(colorAccent).
			PaddingLeft(1),
		StatusText: lipgloss.NewStyle().
			Foreground(colorDim),
		StreamHint: lipgloss.NewStyle().
			Foreground(colorDim).
			Italic(true),
		ErrorText: lipgloss.NewStyle().
			Foreground(colorError).
			Bold(true).
			PaddingLeft(2),

		SystemText: lipgloss.NewStyle().
			Foreground(colorText).
			Italic(true).
			PaddingLeft(2),
		WelcomeHint: lipgloss.NewStyle().
			Foreground(colorAccent2).
			Bold(true),

		CompletionBorder: lipgloss.NewStyle().
			Foreground(colorMuted),
		CompletionSelected: lipgloss.NewStyle().
			Foreground(colorAccent).
			Bold(true),
	}
}

func plainStyles() Styles {
	p := lipgloss.NewStyle()
	b := lipgloss.NewStyle().Bold(true)
	pl2 := lipgloss.NewStyle().PaddingLeft(2)
	pl4 := lipgloss.NewStyle().PaddingLeft(4)
	return Styles{
		HeaderTitle: b.PaddingLeft(1),
		HeaderInfo:  p.PaddingRight(1),
		HeaderRule:  p,

		UserLabel:    b.PaddingLeft(2),
		UserContent:  pl2,
		AsstLabel:    b.PaddingLeft(2),
		AsstContent:  pl2,
		RoleRule:     p,
		StreamCursor: b,

		ToolCallIcon:    pl4,
		ToolCallText:    p,
		ToolResultIcon:  pl4,
		ToolResultText:  p,
		ToolErrorIcon:   pl4,
		ToolErrorText:   b,
		ToolDoneIcon:    pl4,
		ToolDoneText:    p,
		ToolRunningText: p,
		ToolDetailText:  p,

		Divider:    p,
		StatusDot:  p.PaddingLeft(1),
		StatusText: p,
		StreamHint: p.Italic(true),
		ErrorText:  b.PaddingLeft(2),

		SystemText:  p.PaddingLeft(2).Italic(true),
		WelcomeHint: b,

		CompletionBorder:   p,
		CompletionSelected: b,
	}
}

// rule generates a horizontal line of the given width using a thin character.
func rule(width int) string {
	if width < 1 {
		return ""
	}
	return strings.Repeat("─", width)
}

// thickRule generates a horizontal line using a thick character.
func thickRule(width int) string {
	if width < 1 {
		return ""
	}
	return strings.Repeat("━", width)
}
