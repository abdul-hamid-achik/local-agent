package tui

import (
	"testing"
)

func TestLastAssistantContent(t *testing.T) {
	t.Run("found", func(t *testing.T) {
		m := newTestModel(t)
		m.entries = []ChatEntry{
			{Kind: "user", Content: "hello"},
			{Kind: "assistant", Content: "world"},
		}
		got := m.lastAssistantContent()
		if got != "world" {
			t.Errorf("expected 'world', got %q", got)
		}
	})

	t.Run("not_found", func(t *testing.T) {
		m := newTestModel(t)
		m.entries = []ChatEntry{
			{Kind: "user", Content: "hello"},
			{Kind: "system", Content: "info"},
		}
		got := m.lastAssistantContent()
		if got != "" {
			t.Errorf("expected empty string, got %q", got)
		}
	})

	t.Run("returns_last", func(t *testing.T) {
		m := newTestModel(t)
		m.entries = []ChatEntry{
			{Kind: "assistant", Content: "first"},
			{Kind: "user", Content: "question"},
			{Kind: "assistant", Content: "second"},
		}
		got := m.lastAssistantContent()
		if got != "second" {
			t.Errorf("expected 'second', got %q", got)
		}
	})

	t.Run("empty_entries", func(t *testing.T) {
		m := newTestModel(t)
		m.entries = nil
		got := m.lastAssistantContent()
		if got != "" {
			t.Errorf("expected empty string, got %q", got)
		}
	})
}

func TestCopyLast_OnlyWhenIdleAndEmpty(t *testing.T) {
	t.Run("idle_empty_with_assistant", func(t *testing.T) {
		m := newTestModel(t)
		m.state = StateIdle
		m.entries = []ChatEntry{
			{Kind: "assistant", Content: "response text"},
		}
		m.input.SetValue("")

		_, cmd := m.Update(charKey('y'))
		if cmd == nil {
			t.Error("expected a command to be returned for copy")
		}
	})

	t.Run("non_empty_input_no_trigger", func(t *testing.T) {
		m := newTestModel(t)
		m.state = StateIdle
		m.entries = []ChatEntry{
			{Kind: "assistant", Content: "response text"},
		}
		m.input.SetValue("some text")

		_, cmd := m.Update(charKey('y'))
		// When input is non-empty, 'y' is typed into the input, not a copy command.
		// The cmd may be non-nil (textarea update), but no copy should occur.
		// Verify no system message about clipboard appears.
		if cmd != nil {
			msg := cmd()
			if sysMsg, ok := msg.(SystemMessageMsg); ok {
				if sysMsg.Msg == "Copied to clipboard." {
					t.Error("should not trigger copy when input is non-empty")
				}
			}
		}
	})

	t.Run("non_idle_no_trigger", func(t *testing.T) {
		m := newTestModel(t)
		m.state = StateStreaming
		m.entries = []ChatEntry{
			{Kind: "assistant", Content: "response text"},
		}
		m.input.SetValue("")

		initialEntryCount := len(m.entries)
		m.Update(charKey('y'))
		// Should not add any system message about clipboard
		if len(m.entries) > initialEntryCount {
			t.Error("should not trigger copy when not idle")
		}
	})

	t.Run("no_assistant_entries", func(t *testing.T) {
		m := newTestModel(t)
		m.state = StateIdle
		m.entries = []ChatEntry{
			{Kind: "user", Content: "hello"},
		}
		m.input.SetValue("")

		_, cmd := m.Update(charKey('y'))
		// Should not return a copy command when there's no assistant content
		if cmd != nil {
			msg := cmd()
			if sysMsg, ok := msg.(SystemMessageMsg); ok {
				if sysMsg.Msg == "Copied to clipboard." {
					t.Error("should not trigger copy when no assistant content")
				}
			}
		}
	})
}
