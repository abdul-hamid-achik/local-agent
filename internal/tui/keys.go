package tui

import "charm.land/bubbles/v2/key"

// KeyMap defines all keyboard shortcuts for the application.
type KeyMap struct {
	Send           key.Binding
	NewLine        key.Binding
	Cancel         key.Binding
	Quit           key.Binding
	ClearView      key.Binding
	NewConvo       key.Binding
	Help           key.Binding
	ToggleTools    key.Binding
	PageUp         key.Binding
	PageDown       key.Binding
	HalfPageUp     key.Binding
	HalfPageDn     key.Binding
	Complete       key.Binding
	CompleteUp     key.Binding
	CompleteDown   key.Binding
	CompleteToggle key.Binding
	CompleteSelect key.Binding
	CopyLast       key.Binding
	CycleMode      key.Binding
	ModelPicker    key.Binding
	HistoryUp          key.Binding
	HistoryDown        key.Binding
	ToggleFocusedTool  key.Binding
	ToggleThinking     key.Binding
}

// DefaultKeyMap returns the default keybindings.
func DefaultKeyMap() KeyMap {
	return KeyMap{
		Send: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "send message"),
		),
		NewLine: key.NewBinding(
			key.WithKeys("shift+enter"),
			key.WithHelp("shift+enter", "new line"),
		),
		Cancel: key.NewBinding(
			key.WithKeys("esc"),
			key.WithHelp("esc", "cancel / close overlay"),
		),
		Quit: key.NewBinding(
			key.WithKeys("ctrl+c"),
			key.WithHelp("ctrl+c", "quit"),
		),
		ClearView: key.NewBinding(
			key.WithKeys("ctrl+l"),
			key.WithHelp("ctrl+l", "clear screen"),
		),
		NewConvo: key.NewBinding(
			key.WithKeys("ctrl+n"),
			key.WithHelp("ctrl+n", "new conversation"),
		),
		Help: key.NewBinding(
			key.WithKeys("?"),
			key.WithHelp("?", "toggle help"),
		),
		ToggleTools: key.NewBinding(
			key.WithKeys("t"),
			key.WithHelp("t", "expand/collapse tool details"),
		),
		PageUp: key.NewBinding(
			key.WithKeys("pgup"),
			key.WithHelp("pgup", "scroll up"),
		),
		PageDown: key.NewBinding(
			key.WithKeys("pgdown"),
			key.WithHelp("pgdown", "scroll down"),
		),
		HalfPageUp: key.NewBinding(
			key.WithKeys("ctrl+u"),
			key.WithHelp("ctrl+u", "half page up"),
		),
		HalfPageDn: key.NewBinding(
			key.WithKeys("ctrl+d"),
			key.WithHelp("ctrl+d", "half page down"),
		),
		Complete: key.NewBinding(
			key.WithKeys("tab", "ctrl+i"),
			key.WithHelp("tab", "autocomplete"),
		),
		CompleteUp: key.NewBinding(
			key.WithKeys("up"),
			key.WithHelp("up", "previous completion"),
		),
		CompleteDown: key.NewBinding(
			key.WithKeys("down"),
			key.WithHelp("down", "next completion"),
		),
		CompleteToggle: key.NewBinding(
			key.WithKeys("tab", "ctrl+i"),
			key.WithHelp("tab", "toggle selection"),
		),
		CompleteSelect: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "select item"),
		),
		CopyLast: key.NewBinding(
			key.WithKeys("y"),
			key.WithHelp("y", "copy last response"),
		),
		CycleMode: key.NewBinding(
			key.WithKeys("shift+tab"),
			key.WithHelp("shift+tab", "cycle mode (ASK/PLAN/BUILD)"),
		),
		ModelPicker: key.NewBinding(
			key.WithKeys("ctrl+m"),
			key.WithHelp("ctrl+m", "quick model switch"),
		),
		HistoryUp: key.NewBinding(
			key.WithKeys("up"),
			key.WithHelp("↑", "previous input"),
		),
		HistoryDown: key.NewBinding(
			key.WithKeys("down"),
			key.WithHelp("↓", "next input"),
		),
		ToggleFocusedTool: key.NewBinding(
			key.WithKeys(" "),
			key.WithHelp("space", "toggle last tool details"),
		),
		ToggleThinking: key.NewBinding(
			key.WithKeys("ctrl+t"),
			key.WithHelp("ctrl+t", "toggle thinking display"),
		),
	}
}

// ShortHelp returns the key groups for the short help view.
func (k KeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Send, k.NewLine, k.Cancel, k.Quit, k.Help}
}

// FullHelp returns the key groups for the full help view.
func (k KeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Send, k.NewLine, k.Cancel, k.Quit},
		{k.ClearView, k.NewConvo, k.Help, k.ToggleTools, k.CopyLast},
		{k.PageUp, k.PageDown, k.HalfPageUp, k.HalfPageDn},
		{k.CycleMode, k.ModelPicker},
		{k.HistoryUp, k.HistoryDown},
		{k.ToggleFocusedTool, k.ToggleThinking},
	}
}
