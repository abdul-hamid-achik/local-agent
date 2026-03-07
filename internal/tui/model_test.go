package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/abdul-hamid-achik/local-agent/internal/command"
)

func TestSubmitInput_EmptyReturnsNil(t *testing.T) {
	m := newTestModel(t)
	// Input is empty by default.
	cmd := m.submitInput()
	if cmd != nil {
		t.Error("submitInput with empty input should return nil")
	}
}

func TestHelp_OnlyWhenIdleAndEmpty(t *testing.T) {
	t.Run("idle_empty_opens_help", func(t *testing.T) {
		m := newTestModel(t)
		m.state = StateIdle
		// Input is empty.

		updated, _ := m.Update(charKey('?'))
		m = updated.(*Model)

		if m.overlay != OverlayHelp {
			t.Errorf("? with idle+empty should open help, got overlay=%d", m.overlay)
		}
	})

	t.Run("idle_nonempty_no_help", func(t *testing.T) {
		m := newTestModel(t)
		m.state = StateIdle
		m.input.SetValue("hello")

		updated, _ := m.Update(charKey('?'))
		m = updated.(*Model)

		if m.overlay == OverlayHelp {
			t.Error("? with non-empty input should not open help")
		}
	})

	t.Run("waiting_no_help", func(t *testing.T) {
		m := newTestModel(t)
		m.state = StateWaiting

		updated, _ := m.Update(charKey('?'))
		m = updated.(*Model)

		if m.overlay == OverlayHelp {
			t.Error("? in StateWaiting should not open help")
		}
	})
}

func TestToggleTools_OnlyWhenIdleAndEmpty(t *testing.T) {
	t.Run("idle_empty_toggles", func(t *testing.T) {
		m := newTestModel(t)
		m.state = StateIdle
		before := m.toolsCollapsed

		updated, _ := m.Update(charKey('t'))
		m = updated.(*Model)

		if m.toolsCollapsed == before {
			t.Error("'t' with idle+empty should toggle toolsCollapsed")
		}
	})

	t.Run("idle_nonempty_no_toggle", func(t *testing.T) {
		m := newTestModel(t)
		m.state = StateIdle
		m.input.SetValue("hello")
		before := m.toolsCollapsed

		updated, _ := m.Update(charKey('t'))
		m = updated.(*Model)

		if m.toolsCollapsed != before {
			t.Error("'t' with non-empty input should not toggle tools")
		}
	})
}

func TestESC_CancelOnlyWhenStreamingOrWaiting(t *testing.T) {
	t.Run("idle_no_cancel", func(t *testing.T) {
		m := newTestModel(t)
		m.state = StateIdle
		cancelCalled := false
		m.cancel = func() { cancelCalled = true }

		updated, _ := m.Update(escKey())
		_ = updated.(*Model)

		if cancelCalled {
			t.Error("ESC in idle should not call cancel")
		}
	})

	t.Run("streaming_cancels", func(t *testing.T) {
		m := newTestModel(t)
		m.state = StateStreaming
		cancelCalled := false
		m.cancel = func() { cancelCalled = true }

		updated, _ := m.Update(escKey())
		_ = updated.(*Model)

		if !cancelCalled {
			t.Error("ESC in streaming should call cancel")
		}
	})

	t.Run("waiting_cancels", func(t *testing.T) {
		m := newTestModel(t)
		m.state = StateWaiting
		cancelCalled := false
		m.cancel = func() { cancelCalled = true }

		updated, _ := m.Update(escKey())
		_ = updated.(*Model)

		if !cancelCalled {
			t.Error("ESC in waiting should call cancel")
		}
	})
}

func TestSystemMessageMsg_AppendsEntry(t *testing.T) {
	m := newTestModel(t)
	before := len(m.entries)

	updated, _ := m.Update(SystemMessageMsg{Msg: "hello system"})
	m = updated.(*Model)

	if len(m.entries) != before+1 {
		t.Fatalf("expected %d entries, got %d", before+1, len(m.entries))
	}
	last := m.entries[len(m.entries)-1]
	if last.Kind != "system" {
		t.Errorf("expected kind 'system', got %q", last.Kind)
	}
	if last.Content != "hello system" {
		t.Errorf("expected content 'hello system', got %q", last.Content)
	}
}

func TestErrorMsg_AppendsEntry(t *testing.T) {
	m := newTestModel(t)
	before := len(m.entries)

	updated, _ := m.Update(ErrorMsg{Msg: "something broke"})
	m = updated.(*Model)

	if len(m.entries) != before+1 {
		t.Fatalf("expected %d entries, got %d", before+1, len(m.entries))
	}
	last := m.entries[len(m.entries)-1]
	if last.Kind != "error" {
		t.Errorf("expected kind 'error', got %q", last.Kind)
	}
	if last.Content != "something broke" {
		t.Errorf("expected content 'something broke', got %q", last.Content)
	}
}

func TestToolCallResultMsg(t *testing.T) {
	t.Run("updates_tool_entry", func(t *testing.T) {
		m := newTestModel(t)
		m.toolEntries = append(m.toolEntries, ToolEntry{
			Name:   "read_file",
			Status: ToolStatusRunning,
		})
		m.toolsPending = 1

		updated, _ := m.Update(ToolCallResultMsg{
			Name:     "read_file",
			Result:   "file contents",
			IsError:  false,
			Duration: 42 * time.Millisecond,
		})
		m = updated.(*Model)

		if m.toolEntries[0].Status != ToolStatusDone {
			t.Errorf("expected ToolStatusDone, got %d", m.toolEntries[0].Status)
		}
		if m.toolEntries[0].Result != "file contents" {
			t.Errorf("expected 'file contents', got %q", m.toolEntries[0].Result)
		}
		if m.toolsPending != 0 {
			t.Errorf("toolsPending should be 0, got %d", m.toolsPending)
		}
	})

	t.Run("truncates_long_result", func(t *testing.T) {
		m := newTestModel(t)
		m.toolEntries = append(m.toolEntries, ToolEntry{
			Name:   "read_file",
			Status: ToolStatusRunning,
		})

		longResult := strings.Repeat("x", 2500)
		updated, _ := m.Update(ToolCallResultMsg{
			Name:   "read_file",
			Result: longResult,
		})
		m = updated.(*Model)

		if len(m.toolEntries[0].Result) != 2000 {
			t.Errorf("result should be truncated to 2000, got %d", len(m.toolEntries[0].Result))
		}
		if !strings.HasSuffix(m.toolEntries[0].Result, "...") {
			t.Error("truncated result should end with '...'")
		}
	})

	t.Run("error_status", func(t *testing.T) {
		m := newTestModel(t)
		m.toolEntries = append(m.toolEntries, ToolEntry{
			Name:   "exec",
			Status: ToolStatusRunning,
		})

		updated, _ := m.Update(ToolCallResultMsg{
			Name:    "exec",
			Result:  "command failed",
			IsError: true,
		})
		m = updated.(*Model)

		if m.toolEntries[0].Status != ToolStatusError {
			t.Errorf("expected ToolStatusError, got %d", m.toolEntries[0].Status)
		}
		if !m.toolEntries[0].IsError {
			t.Error("IsError should be true")
		}
	})
}

func TestAgentDoneMsg(t *testing.T) {
	m := newTestModel(t)
	m.state = StateStreaming
	m.userScrolledUp = true
	m.anchorActive = false

	updated, _ := m.Update(AgentDoneMsg{})
	m = updated.(*Model)

	if m.state != StateIdle {
		t.Errorf("state should be StateIdle, got %d", m.state)
	}
	if m.userScrolledUp {
		t.Error("userScrolledUp should be reset to false")
	}
	if !m.anchorActive {
		t.Error("anchorActive should be reset to true")
	}
}

func TestInitCompleteMsg(t *testing.T) {
	t.Run("basic_fields", func(t *testing.T) {
		m := newTestModel(t)

		updated, _ := m.Update(InitCompleteMsg{
			Model:       "llama3",
			ModelList:   []string{"llama3", "qwen3"},
			AgentProfile: "default",
			AgentList:   []string{"default", "coder"},
			ToolCount:   5,
			ServerCount: 2,
			NumCtx:      8192,
		})
		m = updated.(*Model)

		if m.model != "llama3" {
			t.Errorf("model should be 'llama3', got %q", m.model)
		}
		if len(m.modelList) != 2 {
			t.Errorf("modelList should have 2 items, got %d", len(m.modelList))
		}
		if m.toolCount != 5 {
			t.Errorf("toolCount should be 5, got %d", m.toolCount)
		}
		if m.serverCount != 2 {
			t.Errorf("serverCount should be 2, got %d", m.serverCount)
		}
	})

	t.Run("with_failed_servers", func(t *testing.T) {
		m := newTestModel(t)
		before := len(m.entries)

		updated, _ := m.Update(InitCompleteMsg{
			Model: "llama3",
			FailedServers: []FailedServer{
				{Name: "server1", Reason: "timeout"},
			},
		})
		m = updated.(*Model)

		if len(m.entries) != before+1 {
			t.Fatalf("should append system entry for failed servers, got %d entries", len(m.entries))
		}
		last := m.entries[len(m.entries)-1]
		if last.Kind != "system" {
			t.Errorf("expected kind 'system', got %q", last.Kind)
		}
		if !strings.Contains(last.Content, "server1") {
			t.Errorf("should contain server name, got %q", last.Content)
		}
	})
}

func TestHandleCommandAction(t *testing.T) {
	tests := []struct {
		name   string
		result command.Result
		check  func(t *testing.T, m *Model, cmd tea.Cmd)
	}{
		{
			name:   "ActionShowHelp",
			result: command.Result{Action: command.ActionShowHelp},
			check: func(t *testing.T, m *Model, cmd tea.Cmd) {
				if m.overlay != OverlayHelp {
					t.Errorf("expected OverlayHelp, got %d", m.overlay)
				}
			},
		},
		{
			name:   "ActionClear_with_text",
			result: command.Result{Action: command.ActionClear, Text: "Cleared."},
			check: func(t *testing.T, m *Model, cmd tea.Cmd) {
				// entries should be cleared except for the new system message
				if len(m.entries) != 1 {
					t.Errorf("expected 1 entry, got %d", len(m.entries))
				}
				if m.entries[0].Kind != "system" {
					t.Errorf("expected system entry, got %q", m.entries[0].Kind)
				}
			},
		},
		{
			name:   "ActionQuit",
			result: command.Result{Action: command.ActionQuit},
			check: func(t *testing.T, m *Model, cmd tea.Cmd) {
				if cmd == nil {
					t.Error("ActionQuit should return a cmd (tea.Quit)")
				}
			},
		},
		{
			name:   "ActionLoadContext",
			result: command.Result{Action: command.ActionLoadContext, Data: "test.md\x00# Hello", Text: "Loaded."},
			check: func(t *testing.T, m *Model, cmd tea.Cmd) {
				if m.loadedFile != "test.md" {
					t.Errorf("expected loadedFile='test.md', got %q", m.loadedFile)
				}
			},
		},
		{
			name:   "ActionUnloadContext",
			result: command.Result{Action: command.ActionUnloadContext, Text: "Unloaded."},
			check: func(t *testing.T, m *Model, cmd tea.Cmd) {
				if m.loadedFile != "" {
					t.Errorf("expected empty loadedFile, got %q", m.loadedFile)
				}
			},
		},
		{
			name:   "ActionSwitchModel",
			result: command.Result{Action: command.ActionSwitchModel, Data: "gpt-4", Text: "Switched."},
			check: func(t *testing.T, m *Model, cmd tea.Cmd) {
				if m.model != "gpt-4" {
					t.Errorf("expected model='gpt-4', got %q", m.model)
				}
			},
		},
		{
			name:   "ActionSwitchAgent",
			result: command.Result{Action: command.ActionSwitchAgent, Data: "coder", Text: "Switched."},
			check: func(t *testing.T, m *Model, cmd tea.Cmd) {
				if m.agentProfile != "coder" {
					t.Errorf("expected agentProfile='coder', got %q", m.agentProfile)
				}
			},
		},
		{
			name:   "ActionNone_with_text",
			result: command.Result{Action: command.ActionNone, Text: "Info message"},
			check: func(t *testing.T, m *Model, cmd tea.Cmd) {
				if len(m.entries) == 0 {
					t.Fatal("expected at least one entry")
				}
				last := m.entries[len(m.entries)-1]
				if last.Content != "Info message" {
					t.Errorf("expected 'Info message', got %q", last.Content)
				}
			},
		},
		{
			name:   "ActionNone_empty_text",
			result: command.Result{Action: command.ActionNone, Text: ""},
			check: func(t *testing.T, m *Model, cmd tea.Cmd) {
				// Should not add any entry.
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestModel(t)
			// Pre-populate loadedFile for unload test.
			if tt.result.Action == command.ActionUnloadContext {
				m.loadedFile = "old.md"
			}
			cmd := m.handleCommandAction(tt.result)
			tt.check(t, m, cmd)
		})
	}
}

func TestCommandResultMsg(t *testing.T) {
	t.Run("with_text", func(t *testing.T) {
		m := newTestModel(t)
		before := len(m.entries)

		updated, _ := m.Update(CommandResultMsg{Text: "Result info"})
		m = updated.(*Model)

		if len(m.entries) != before+1 {
			t.Fatalf("expected %d entries, got %d", before+1, len(m.entries))
		}
		if m.entries[len(m.entries)-1].Content != "Result info" {
			t.Errorf("expected 'Result info', got %q", m.entries[len(m.entries)-1].Content)
		}
	})

	t.Run("empty_text_no_entry", func(t *testing.T) {
		m := newTestModel(t)
		before := len(m.entries)

		updated, _ := m.Update(CommandResultMsg{Text: ""})
		m = updated.(*Model)

		if len(m.entries) != before {
			t.Errorf("expected %d entries (no change), got %d", before, len(m.entries))
		}
	})
}
