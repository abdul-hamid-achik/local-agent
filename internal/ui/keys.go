package ui

import "charm.land/bubbles/v2/key"

// KeyMap defines all keyboard shortcuts for the application.
type KeyMap struct {
	Send              key.Binding
	NewLine           key.Binding
	Cancel            key.Binding
	Quit              key.Binding
	ClearView         key.Binding
	NewConvo          key.Binding
	Help              key.Binding
	ToggleTools       key.Binding
	PageUp            key.Binding
	PageDown          key.Binding
	HalfPageUp        key.Binding
	HalfPageDn        key.Binding
	JumpLatest        key.Binding
	Complete          key.Binding
	CompleteUp        key.Binding
	CompleteDown      key.Binding
	CompleteToggle    key.Binding
	CompleteSelect    key.Binding
	CopyLast          key.Binding
	CycleMode         key.Binding
	ModelPicker       key.Binding
	SettingsPicker    key.Binding
	HistoryUp         key.Binding
	HistoryDown       key.Binding
	ToggleFocusedTool key.Binding
	ToggleThinking    key.Binding
	CompactToggle     key.Binding
	ExternalEditor    key.Binding
}

// DefaultKeyMap returns the default keybindings.
func DefaultKeyMap() KeyMap {
	return KeyMap{
		Send: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "send / queue one follow-up"),
		),
		NewLine: key.NewBinding(
			// Ctrl+J works on terminals that cannot distinguish Shift+Enter from
			// Enter; Alt+Enter is a second ergonomic fallback on enhanced terminals.
			key.WithKeys("shift+enter", "ctrl+j", "alt+enter"),
			key.WithHelp("shift+enter/ctrl+j", "new line"),
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
			key.WithHelp("?", "show help (empty input)"),
		),
		ToggleTools: key.NewBinding(
			key.WithKeys("t"),
			key.WithHelp("t", "toggle all tools (empty input)"),
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
			key.WithHelp("ctrl+u", "edit / half page up"),
		),
		HalfPageDn: key.NewBinding(
			key.WithKeys("ctrl+d"),
			key.WithHelp("ctrl+d", "edit / half page down"),
		),
		JumpLatest: key.NewBinding(
			key.WithKeys("end"),
			key.WithHelp("end", "latest output (empty input)"),
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
			key.WithKeys("ctrl+y"),
			key.WithHelp("ctrl+y", "copy last response (empty input)"),
		),
		CycleMode: key.NewBinding(
			key.WithKeys("shift+tab"),
			key.WithHelp("shift+tab", "cycle mode (NORMAL/PLAN/AUTO)"),
		),
		ModelPicker: key.NewBinding(
			// Ctrl+M is carriage return in ordinary terminals and therefore
			// indistinguishable from Enter without an enhanced keyboard protocol.
			key.WithKeys("ctrl+o"),
			key.WithHelp("ctrl+o", "open Ollama models"),
		),
		SettingsPicker: key.NewBinding(
			key.WithKeys("ctrl+p"),
			key.WithHelp("ctrl+p", "open settings"),
		),
		HistoryUp: key.NewBinding(
			key.WithKeys("up"),
			key.WithHelp("↑", "previous input (empty input)"),
		),
		HistoryDown: key.NewBinding(
			key.WithKeys("down"),
			key.WithHelp("↓", "next input (history active)"),
		),
		ToggleFocusedTool: key.NewBinding(
			key.WithKeys("space"),
			key.WithHelp("space", "toggle last tool (empty input)"),
		),
		ToggleThinking: key.NewBinding(
			key.WithKeys("ctrl+t"),
			key.WithHelp("ctrl+t", "toggle all reasoning (empty input)"),
		),
		CompactToggle: key.NewBinding(
			key.WithKeys("ctrl+k"),
			key.WithHelp("ctrl+k", "toggle compact mode"),
		),
		ExternalEditor: key.NewBinding(
			key.WithKeys("ctrl+e"),
			key.WithHelp("ctrl+e", "open in $EDITOR"),
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
		{k.PageUp, k.PageDown, k.HalfPageUp, k.HalfPageDn, k.JumpLatest},
		{k.CycleMode, k.ModelPicker, k.SettingsPicker},
		{k.HistoryUp, k.HistoryDown},
		{k.ToggleFocusedTool, k.ToggleThinking, k.CompactToggle, k.ExternalEditor, k.Complete},
	}
}
