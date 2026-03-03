package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/abdul-hamid-achik/local-agent/internal/agent"
	"github.com/abdul-hamid-achik/local-agent/internal/command"
)

// newTestModel creates a Model with a real command.Registry and Agent, sends WindowSizeMsg to set ready=true.
// Sets initializing=false so tests exercise normal (post-startup) behavior.
func newTestModel(t *testing.T) *Model {
	t.Helper()
	reg := command.NewRegistry()
	command.RegisterBuiltins(reg)
	completer := NewCompleter(reg, []string{"model-a", "model-b"}, []string{"skill-a"}, []string{"agent-x"}, nil)
	ag := agent.New(nil, nil, 0)
	m := New(ag, reg, nil, completer, nil, nil, nil)
	m.initializing = false // skip startup phase for tests
	// Send WindowSizeMsg to set ready=true
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return updated.(*Model)
}

// Key helpers — construct tea.KeyPressMsg directly using the Key struct fields.

func escKey() tea.KeyPressMsg   { return tea.KeyPressMsg{Code: tea.KeyEscape} }
func enterKey() tea.KeyPressMsg { return tea.KeyPressMsg{Code: tea.KeyEnter} }
func tabKey() tea.KeyPressMsg   { return tea.KeyPressMsg{Code: tea.KeyTab} }
func upKey() tea.KeyPressMsg    { return tea.KeyPressMsg{Code: tea.KeyUp} }
func downKey() tea.KeyPressMsg  { return tea.KeyPressMsg{Code: tea.KeyDown} }
func leftKey() tea.KeyPressMsg  { return tea.KeyPressMsg{Code: tea.KeyLeft} }
func rightKey() tea.KeyPressMsg { return tea.KeyPressMsg{Code: tea.KeyRight} }
func spaceKey() tea.KeyPressMsg { return tea.KeyPressMsg{Code: tea.KeySpace} }

func charKey(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: r, Text: string(r)}
}

func ctrlKey(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: r, Mod: tea.ModCtrl}
}

func shiftTabKey() tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
}
