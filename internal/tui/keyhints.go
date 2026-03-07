package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// KeyHint displays a keyboard shortcut hint.
type KeyHint struct {
	Key    string
	Action string
}

// KeyHints renders a row of key hints.
type KeyHints struct {
	hints    []KeyHint
	styles   KeyHintStyles
	maxWidth int
}

// KeyHintStyles holds styling for key hints.
type KeyHintStyles struct {
	Key     lipgloss.Style
	Action  lipgloss.Style
	Divider lipgloss.Style
}

// DefaultKeyHintStyles returns default styles.
func DefaultKeyHintStyles(isDark bool) KeyHintStyles {
	if isDark {
		return KeyHintStyles{
			Key:     lipgloss.NewStyle().Foreground(lipgloss.Color("#88c0d0")).Background(lipgloss.Color("#3b4252")).Padding(0, 1),
			Action:  lipgloss.NewStyle().Foreground(lipgloss.Color("#4c566a")),
			Divider: lipgloss.NewStyle().Foreground(lipgloss.Color("#3b4252")),
		}
	}
	return KeyHintStyles{
		Key:     lipgloss.NewStyle().Foreground(lipgloss.Color("#4f8f8f")).Background(lipgloss.Color("#e5e9f0")).Padding(0, 1),
		Action:  lipgloss.NewStyle().Foreground(lipgloss.Color("#9ca0a8")),
		Divider: lipgloss.NewStyle().Foreground(lipgloss.Color("#d8dee9")),
	}
}

// NewKeyHints creates a new key hints component.
func NewKeyHints(hints []KeyHint, maxWidth int, isDark bool) *KeyHints {
	return &KeyHints{
		hints:    hints,
		styles:   DefaultKeyHintStyles(isDark),
		maxWidth: maxWidth,
	}
}

// SetDark updates theme.
func (kh *KeyHints) SetDark(isDark bool) {
	kh.styles = DefaultKeyHintStyles(isDark)
}

// SetHints updates the hints.
func (kh *KeyHints) SetHints(hints []KeyHint) {
	kh.hints = hints
}

// Render returns the key hints as a single line.
func (kh *KeyHints) Render() string {
	if len(kh.hints) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString(kh.styles.Divider.Render("│"))

	for i, hint := range kh.hints {
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString(kh.styles.Key.Render(hint.Key))
		b.WriteString(" ")
		b.WriteString(kh.styles.Action.Render(hint.Action))
	}

	return b.String()
}

// RenderInline renders hints as inline text (no key box).
func (kh *KeyHints) RenderInline() string {
	if len(kh.hints) == 0 {
		return ""
	}

	var b strings.Builder

	for i, hint := range kh.hints {
		if i > 0 {
			b.WriteString(" · ")
		}
		b.WriteString(hint.Key)
		b.WriteString(" ")
		b.WriteString(kh.styles.Action.Render(hint.Action))
	}

	return b.String()
}

// SetMaxWidth sets the maximum width for wrapping.
func (kh *KeyHints) SetMaxWidth(w int) {
	kh.maxWidth = w
}

// DefaultKeyHints returns common key hints for the application.
func DefaultKeyHints(isDark bool) *KeyHints {
	hints := []KeyHint{
		{Key: "Enter", Action: "send"},
		{Key: "Tab", Action: "complete"},
		{Key: "?", Action: "help"},
		{Key: "Esc", Action: "cancel"},
		{Key: "Ctrl+C", Action: "quit"},
	}
	return NewKeyHints(hints, 60, isDark)
}

// FooterHints returns hints shown in the footer.
func FooterHints(keys KeyMap, isDark bool) *KeyHints {
	hints := []KeyHint{
		{Key: "?", Action: "help"},
		{Key: "Ctrl+N", Action: "new"},
		{Key: "Ctrl+L", Action: "clear"},
	}
	return NewKeyHints(hints, 40, isDark)
}

// InputHints returns hints shown when typing.
func InputHints(keys KeyMap, isDark bool) *KeyHints {
	hints := []KeyHint{
		{Key: "Tab", Action: "complete"},
		{Key: "/", Action: "commands"},
		{Key: "@", Action: "files"},
		{Key: "#", Action: "skills"},
	}
	return NewKeyHints(hints, 40, isDark)
}
