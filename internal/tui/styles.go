package tui

import (
	"os"
	"strings"

	"charm.land/lipgloss/v2"
)

// noColor detects NO_COLOR environment variable.
var noColor = os.Getenv("NO_COLOR") != ""

// Nord Color Palette (https://www.nordtheme.com/)
// Nord Dark (Polar Night + Frost)
var (
	// Polar Night (dark theme background/text)
	nord0 = "#2E3440" // base background
	nord1 = "#3B4252" // lighter background
	nord2 = "#434C5E" // selection/background elements
	nord3 = "#4C566A" // comments/borders

	// Frost (dark theme foreground/text)
	nord4 = "#D8DEE9" // primary text
	nord5 = "#E5E9F0" // secondary text
	nord6 = "#ECEFF4" // emphasized text

	// Aurora (dark theme accents)
	nord7  = "#BF616A" // red (errors/warnings)
	nord8  = "#D08770" // orange (warnings)
	nord9  = "#EBCB8B" // yellow (warnings/highlights)
	nord10 = "#A3BE8C" // green (success)
	nord11 = "#B48EAD" // purple (special)
	nord12 = "#88C0D0" // cyan (primary accent)
	nord13 = "#81A1C1" // blue (secondary accent)
	nord14 = "#5E81AC" // dark blue (links/details)
)

// Nord Light (Aurora variant for light theme)
var (
	// Light background
	nordLight0 = "#FFFFFF" // base background
	nordLight1 = "#ECEFF4" // lighter background
	nordLight2 = "#E5E9F0" // selection
	nordLight3 = "#D8DEE9" // borders

	// Light text
	nordLight4 = "#4C566A" // primary text
	nordLight5 = "#3B4252" // secondary text
	nordLight6 = "#2E3440" // emphasized text

	// Aurora accents (same as dark, work well on light)
	nordLight7  = "#BF616A" // red
	nordLight8  = "#D08770" // orange
	nordLight9  = "#EBCB8B" // yellow
	nordLight10 = "#A3BE8C" // green
	nordLight11 = "#B48EAD" // purple
	nordLight12 = "#88C0D0" // cyan
	nordLight13 = "#81A1C1" // blue
	nordLight14 = "#5E81AC" // dark blue
)

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
	Divider     lipgloss.Style
	StatusDot   lipgloss.Style
	StatusText  lipgloss.Style
	StatusCheck lipgloss.Style
	StatusError lipgloss.Style
	StreamHint  lipgloss.Style
	ErrorText   lipgloss.Style
	Dimmed      lipgloss.Style

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
	OverlayBorder string
	OverlayAccent lipgloss.Style
	OverlayDim    lipgloss.Style

	// Focus indicators
	FocusIndicator lipgloss.Style
}

// NewStyles creates a Styles set based on the background color.
func NewStyles(isDark bool) Styles {
	if noColor {
		return plainStyles()
	}
	return adaptiveStyles(isDark)
}

func adaptiveStyles(isDark bool) Styles {
	// Select Nord palette based on theme
	var (
		colorDim     string
		colorMuted   string
		colorText    string
		colorAccent  string
		colorAccent2 string
		colorError   string
		colorSuccess string
		colorSpecial string
		colorBorder  string
	)

	if isDark {
		// Nord Dark Theme (Polar Night + Frost + Aurora)
		colorDim = nord3      // #4C566A - comments/borders
		colorMuted = nord4    // #D8DEE9 - primary text (muted)
		colorText = nord5     // #E5E9F0 - secondary text
		colorAccent = nord12  // #88C0D0 - cyan (primary accent)
		colorAccent2 = nord13 // #81A1C1 - blue (secondary accent)
		colorError = nord7    // #BF616A - red
		colorSuccess = nord10 // #A3BE8C - green
		colorSpecial = nord11 // #B48EAD - purple
		colorBorder = nord3
	} else {
		// Nord Light Theme (Aurora)
		colorDim = nordLight3      // #D8DEE9 - borders
		colorMuted = nordLight4    // #4C566A - primary text (muted)
		colorText = nordLight5     // #3B4252 - secondary text
		colorAccent = nordLight12  // #88C0D0 - cyan
		colorAccent2 = nordLight13 // #81A1C1 - blue
		colorError = nordLight7    // #BF616A - red
		colorSuccess = nordLight10 // #A3BE8C - green
		colorSpecial = nordLight11 // #B48EAD - purple
		colorBorder = nordLight3
	}

	// Helper for theme-specific colors
	nordColor := func(dark, light string) string {
		if isDark {
			return dark
		}
		return light
	}

	return Styles{
		HeaderTitle: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color(colorAccent)).
			PaddingLeft(1),
		HeaderInfo: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorDim)).
			PaddingRight(1),
		HeaderRule: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorDim)),

		UserLabel: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color(colorAccent2)).
			PaddingLeft(2),
		UserContent: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorText)).
			PaddingLeft(2),
		AsstLabel: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color(colorSuccess)).
			PaddingLeft(2),
		AsstContent: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorText)).
			PaddingLeft(4),
		RoleRule: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorDim)),
		StreamCursor: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorAccent)).
			Bold(true),

		ToolCallIcon: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorSpecial)).
			PaddingLeft(4),
		ToolCallText: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorSpecial)),
		ToolResultIcon: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorDim)).
			PaddingLeft(4),
		ToolResultText: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorDim)),
		ToolErrorIcon: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorError)).
			PaddingLeft(4),
		ToolErrorText: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorError)),
		ToolDoneIcon: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorSuccess)).
			PaddingLeft(4),
		ToolDoneText: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorDim)),
		ToolRunningText: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorAccent)),
		ToolDetailText: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorMuted)),

		Divider: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorDim)),
		StatusDot: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorAccent)).
			PaddingLeft(1),
		StatusText: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorDim)),
		StatusCheck: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorSuccess)).
			PaddingLeft(1),
		StatusError: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorError)).
			PaddingLeft(1),
		StreamHint: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorDim)).
			Italic(true),
		ErrorText: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorError)).
			Bold(true).
			PaddingLeft(2),
		Dimmed: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorDim)),

		SystemText: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorText)).
			Italic(true).
			PaddingLeft(2),
		WelcomeHint: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorAccent2)).
			Bold(true),

		CompletionBorder: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorDim)),
		CompletionSelected: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorAccent)).
			Bold(true),

		CompletionFilter: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorText)),
		CompletionCursor: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorAccent)).
			Bold(true),
		CompletionCategory: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorDim)),
		CompletionFooter: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorDim)).
			Italic(true),
		CompletionSearching: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorSpecial)).
			Italic(true),

		StartupCheck: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorSuccess)),
		StartupFail: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorError)),
		StartupLabel: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorText)),
		StartupDetail: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorDim)),
		StartupSpin: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorAccent)),

		ModeAsk: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color(colorAccent2)),
		ModePlan: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color(nordColor(nord9, nordLight9))), // yellow
		ModeBuild: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color(colorSuccess)),

		ContextPctLow: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorSuccess)),
		ContextPctMid: lipgloss.NewStyle().
			Foreground(lipgloss.Color(nordColor(nord9, nordLight9))),
		ContextPctHigh: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorError)),

		ToolBashCmd: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorDim)).
			Italic(true),

		DiffAdded: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorSuccess)).
			PaddingLeft(6),
		DiffRemoved: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorError)).
			PaddingLeft(6),
		DiffContext: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorDim)).
			PaddingLeft(6),
		DiffHeader: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorAccent)).
			PaddingLeft(6),

		ThinkingHeader: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorSpecial)).
			Italic(true),
		ThinkingContent: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorDim)).
			PaddingLeft(4),
		ThinkingBorder: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorDim)),

		OverlayTitle: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color(colorAccent)),
		OverlayBorder: colorBorder,
		OverlayAccent: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorAccent2)).
			Bold(true),
		OverlayDim: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorDim)),

		FocusIndicator: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorAccent)).
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
		StatusCheck: p.PaddingLeft(1),
		StatusError: p.PaddingLeft(1),
		StreamHint: p.Italic(true),
		ErrorText:  b.PaddingLeft(2),
		Dimmed:     p,

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
		DiffContext: pl4,
		DiffHeader:  pl4,

		ThinkingHeader:  p.Italic(true),
		ThinkingContent: pl4,
		ThinkingBorder:  p,

		OverlayTitle:  b,
		OverlayBorder: "",
		OverlayAccent: b,
		OverlayDim:    p,

		FocusIndicator: b,
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
