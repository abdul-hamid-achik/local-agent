package ui

import (
	"image/color"
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
	nord3 = "#4C566A" // comments/borders

	// Frost (dark theme foreground/text)
	nord4 = "#D8DEE9" // primary text
	nord5 = "#E5E9F0" // secondary text
	// Aurora (dark theme accents)
	nord7  = "#BF616A" // red (errors/warnings)
	nord9  = "#EBCB8B" // yellow (warnings/highlights)
	nord10 = "#A3BE8C" // green (success)
	nord11 = "#B48EAD" // purple (special)
	nord12 = "#88C0D0" // cyan (primary accent)
	nord13 = "#81A1C1" // blue (secondary accent)
)

// Nord Light (Aurora variant for light theme)
var (
	// Light background
	nordLight3 = "#D8DEE9" // borders

	// Light text
	nordLight4 = "#4C566A" // primary text
	nordLight5 = "#3B4252" // secondary text
)

// semanticPalette is the single color vocabulary shared by the transcript,
// Bubbles components, overlays, composer, and tool receipts. Components own
// layout, but they should not invent a second meaning for the same state.
type semanticPalette struct {
	Dim     color.Color
	Muted   color.Color
	Text    color.Color
	Accent  color.Color
	Accent2 color.Color
	Error   color.Color
	Success color.Color
	Special color.Color
	Warning color.Color
	Border  color.Color
}

func newSemanticPalette(isDark bool) semanticPalette {
	ld := lipgloss.LightDark(isDark)
	return semanticPalette{
		Dim:   ld(lipgloss.Color("#5B6779"), lipgloss.Color("#8B97AD")),
		Muted: ld(lipgloss.Color(nordLight4), lipgloss.Color(nord4)),
		Text:  ld(lipgloss.Color(nordLight5), lipgloss.Color(nord5)),
		// Light semantic foregrounds retain the Nord-adjacent hues while clearing
		// 4.5:1 against a white terminal background for normal-sized text.
		Accent:  ld(lipgloss.Color("#447C7C"), lipgloss.Color(nord12)),
		Accent2: ld(lipgloss.Color("#50759F"), lipgloss.Color(nord13)),
		Error:   ld(lipgloss.Color("#C34848"), lipgloss.Color(nord7)),
		Success: ld(lipgloss.Color("#477F33"), lipgloss.Color(nord10)),
		Special: ld(lipgloss.Color("#7B5A83"), lipgloss.Color(nord11)),
		Warning: ld(lipgloss.Color("#8A6500"), lipgloss.Color(nord9)),
		Border:  ld(lipgloss.Color(nordLight3), lipgloss.Color(nord3)),
	}
}

func outputSemanticPalette(isDark bool) semanticPalette {
	if !noColor {
		return newSemanticPalette(isDark)
	}
	plain := lipgloss.NoColor{}
	return semanticPalette{
		Dim: plain, Muted: plain, Text: plain, Accent: plain, Accent2: plain,
		Error: plain, Success: plain, Special: plain, Warning: plain, Border: plain,
	}
}

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
	Divider        lipgloss.Style
	StatusDot      lipgloss.Style
	StatusText     lipgloss.Style
	StatusCheck    lipgloss.Style
	StatusError    lipgloss.Style
	StatusWarning  lipgloss.Style
	ApprovalPrompt lipgloss.Style
	StreamHint     lipgloss.Style
	ErrorText      lipgloss.Style
	Dimmed         lipgloss.Style

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
	// Body-muted colors must remain readable; border colors can be subtler.
	// LightDark keeps every semantic token adaptive without hardcoded ANSI.
	palette := newSemanticPalette(isDark)
	colorDim := palette.Dim
	colorMuted := palette.Muted
	colorText := palette.Text
	colorAccent := palette.Accent
	colorAccent2 := palette.Accent2
	colorError := palette.Error
	colorSuccess := palette.Success
	colorSpecial := palette.Special
	colorWarning := palette.Warning
	colorBorder := palette.Border

	return Styles{
		HeaderTitle: lipgloss.NewStyle().
			Bold(true).
			Foreground(colorAccent).
			PaddingLeft(1),
		HeaderInfo: lipgloss.NewStyle().
			Foreground(colorDim).
			PaddingRight(1),
		HeaderRule: lipgloss.NewStyle().
			Foreground(colorBorder),

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
			Foreground(colorBorder),
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
			Foreground(colorBorder),
		StatusDot: lipgloss.NewStyle().
			Foreground(colorAccent).
			PaddingLeft(1),
		StatusText: lipgloss.NewStyle().
			Foreground(colorDim),
		StatusCheck: lipgloss.NewStyle().
			Foreground(colorSuccess).
			PaddingLeft(1),
		StatusError: lipgloss.NewStyle().
			Foreground(colorError).
			PaddingLeft(1),
		// An expected operational posture (for example AUTO's skipped approval
		// prompts) is not a failure; red is reserved for errors and blockers.
		StatusWarning: lipgloss.NewStyle().
			Foreground(colorWarning),
		ApprovalPrompt: lipgloss.NewStyle().
			Foreground(colorAccent).
			Bold(true),
		StreamHint: lipgloss.NewStyle().
			Foreground(colorDim).
			Italic(true),
		ErrorText: lipgloss.NewStyle().
			Foreground(colorError).
			Bold(true).
			PaddingLeft(2),
		Dimmed: lipgloss.NewStyle().
			Foreground(colorDim),

		SystemText: lipgloss.NewStyle().
			Foreground(colorText).
			Italic(true).
			PaddingLeft(2),
		WelcomeHint: lipgloss.NewStyle().
			Foreground(colorAccent2).
			Bold(true),

		CompletionBorder: lipgloss.NewStyle().
			Foreground(colorBorder),
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
			Foreground(colorMuted),
		ModePlan: lipgloss.NewStyle().
			Bold(true).
			Foreground(colorSpecial),
		ModeBuild: lipgloss.NewStyle().
			Bold(true).
			Foreground(colorSuccess),

		ContextPctLow: lipgloss.NewStyle().
			Foreground(colorSuccess),
		ContextPctMid: lipgloss.NewStyle().
			Foreground(colorWarning),
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
			Foreground(colorBorder),

		OverlayTitle: lipgloss.NewStyle().
			Bold(true).
			Foreground(colorAccent),
		OverlayBorder: colorBorder,
		OverlayAccent: lipgloss.NewStyle().
			Foreground(colorAccent2).
			Bold(true),
		OverlayDim: lipgloss.NewStyle().
			Foreground(colorDim),

		FocusIndicator: lipgloss.NewStyle().
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

		Divider:        p,
		StatusDot:      p.PaddingLeft(1),
		StatusText:     p,
		StatusCheck:    p.PaddingLeft(1),
		StatusError:    p.PaddingLeft(1),
		StatusWarning:  p,
		ApprovalPrompt: b,
		StreamHint:     p.Italic(true),
		ErrorText:      b.PaddingLeft(2),
		Dimmed:         p,

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
		OverlayBorder: lipgloss.NoColor{},
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
