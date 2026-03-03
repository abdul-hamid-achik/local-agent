package tui

import (
	"image/color"
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

	// Completion modal
	CompletionFilter    lipgloss.Style
	CompletionCursor    lipgloss.Style
	CompletionCategory  lipgloss.Style
	CompletionFooter    lipgloss.Style
	CompletionSearching lipgloss.Style

	// Startup progress
	StartupCheck  lipgloss.Style
	StartupFail   lipgloss.Style
	StartupLabel  lipgloss.Style
	StartupDetail lipgloss.Style
	StartupSpin   lipgloss.Style

	// Mode badges
	ModeAsk   lipgloss.Style
	ModePlan  lipgloss.Style
	ModeBuild lipgloss.Style

	// Context percentage fuel gauge
	ContextPctLow  lipgloss.Style
	ContextPctMid  lipgloss.Style
	ContextPctHigh lipgloss.Style

	// Tool type rendering
	ToolBashCmd lipgloss.Style

	// Diff view
	DiffAdded   lipgloss.Style
	DiffRemoved lipgloss.Style
	DiffContext lipgloss.Style
	DiffHeader  lipgloss.Style

	// Thinking display
	ThinkingHeader  lipgloss.Style
	ThinkingContent lipgloss.Style
	ThinkingBorder  lipgloss.Style

	// Shared overlay styles (used by help, model picker, sessions, plan form, completion)
	OverlayTitle  lipgloss.Style
	OverlayBorder color.Color
	OverlayAccent lipgloss.Style
	OverlayDim    lipgloss.Style
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

		CompletionFilter: lipgloss.NewStyle().
			Foreground(colorText),
		CompletionCursor: lipgloss.NewStyle().
			Foreground(colorAccent).
			Bold(true),
		CompletionCategory: lipgloss.NewStyle().
			Foreground(colorDim),
		CompletionFooter: lipgloss.NewStyle().
			Foreground(colorDim).
			Italic(true),
		CompletionSearching: lipgloss.NewStyle().
			Foreground(colorSpecial).
			Italic(true),

		StartupCheck: lipgloss.NewStyle().
			Foreground(colorSuccess),
		StartupFail: lipgloss.NewStyle().
			Foreground(colorError),
		StartupLabel: lipgloss.NewStyle().
			Foreground(colorText),
		StartupDetail: lipgloss.NewStyle().
			Foreground(colorDim),
		StartupSpin: lipgloss.NewStyle().
			Foreground(colorAccent),

		ModeAsk: lipgloss.NewStyle().
			Bold(true).
			Foreground(colorAccent2),
		ModePlan: lipgloss.NewStyle().
			Bold(true).
			Foreground(ld(lipgloss.Color("#bb8800"), lipgloss.Color("#ebcb8b"))),
		ModeBuild: lipgloss.NewStyle().
			Bold(true).
			Foreground(colorSuccess),

		ContextPctLow: lipgloss.NewStyle().
			Foreground(colorSuccess),
		ContextPctMid: lipgloss.NewStyle().
			Foreground(ld(lipgloss.Color("#bb8800"), lipgloss.Color("#ebcb8b"))),
		ContextPctHigh: lipgloss.NewStyle().
			Foreground(colorError),

		ToolBashCmd: lipgloss.NewStyle().
			Foreground(colorDim).
			Italic(true),

		DiffAdded: lipgloss.NewStyle().
			Foreground(colorSuccess).
			PaddingLeft(6),
		DiffRemoved: lipgloss.NewStyle().
			Foreground(colorError).
			PaddingLeft(6),
		DiffContext: lipgloss.NewStyle().
			Foreground(colorDim).
			PaddingLeft(6),
		DiffHeader: lipgloss.NewStyle().
			Foreground(colorAccent).
			PaddingLeft(6),

		ThinkingHeader: lipgloss.NewStyle().
			Foreground(colorSpecial).
			Italic(true),
		ThinkingContent: lipgloss.NewStyle().
			Foreground(colorDim).
			PaddingLeft(4),
		ThinkingBorder: lipgloss.NewStyle().
			Foreground(colorMuted),

		OverlayTitle: lipgloss.NewStyle().
			Bold(true).
			Foreground(colorAccent),
		OverlayBorder: ld(lipgloss.Color("#bbbbbb"), lipgloss.Color("#4c566a")),
		OverlayAccent: lipgloss.NewStyle().
			Foreground(colorAccent2).
			Bold(true),
		OverlayDim: lipgloss.NewStyle().
			Foreground(colorDim),
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

		CompletionFilter:    p,
		CompletionCursor:    b,
		CompletionCategory:  p,
		CompletionFooter:    p.Italic(true),
		CompletionSearching: p.Italic(true),

		StartupCheck:  p,
		StartupFail:   b,
		StartupLabel:  p,
		StartupDetail: p,
		StartupSpin:   p,

		ModeAsk:   b,
		ModePlan:  b,
		ModeBuild: b,

		ContextPctLow:  p,
		ContextPctMid:  p,
		ContextPctHigh: p,

		ToolBashCmd: p.Italic(true),

		DiffAdded:   pl4,
		DiffRemoved: pl4,
		DiffContext:  pl4,
		DiffHeader:  pl4,

		ThinkingHeader:  p.Italic(true),
		ThinkingContent: pl4,
		ThinkingBorder:  p,

		OverlayTitle:  b,
		OverlayBorder: lipgloss.Color(""),
		OverlayAccent: b,
		OverlayDim:    p,
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
