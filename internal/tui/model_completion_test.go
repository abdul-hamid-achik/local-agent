package tui

import "testing"

func TestTriggerCompletion(t *testing.T) {
	t.Run("slash_triggers_command", func(t *testing.T) {
		m := newTestModel(t)
		m.triggerCompletion("/")

		if !m.completionActive {
			t.Error("/ should activate completion")
		}
		if m.completionType != "command" {
			t.Errorf("expected type 'command', got %q", m.completionType)
		}
		if m.overlay != OverlayCompletion {
			t.Errorf("expected OverlayCompletion, got %d", m.overlay)
		}
		if len(m.completionItems) == 0 {
			t.Error("should have completion items for /")
		}
	})

	t.Run("at_triggers_attachments_with_multiselect", func(t *testing.T) {
		m := newTestModel(t)
		m.triggerCompletion("@")

		// @ triggers agent/file completion.
		// It may or may not find matches depending on agents + cwd.
		// If agents exist, it should activate.
		if m.completionActive {
			if m.completionType != "attachments" {
				t.Errorf("expected type 'attachments', got %q", m.completionType)
			}
			if m.completionSelected == nil {
				t.Error("attachments should initialize completionSelected map")
			}
		}
	})

	t.Run("hash_triggers_skills", func(t *testing.T) {
		m := newTestModel(t)
		m.triggerCompletion("#")

		if !m.completionActive {
			t.Error("# should activate completion for skills")
		}
		if m.completionType != "skills" {
			t.Errorf("expected type 'skills', got %q", m.completionType)
		}
		if m.completionSelected == nil {
			t.Error("skills should initialize completionSelected map")
		}
	})

	t.Run("no_matches_stays_inactive", func(t *testing.T) {
		m := newTestModel(t)
		m.triggerCompletion("/zzzznonexistent")

		if m.completionActive {
			t.Error("should not activate with no matches")
		}
	})

	t.Run("plain_text_no_trigger", func(t *testing.T) {
		m := newTestModel(t)
		m.triggerCompletion("hello")

		if m.completionActive {
			t.Error("plain text should not trigger completion")
		}
	})
}

func TestAcceptCompletion(t *testing.T) {
	t.Run("single_select", func(t *testing.T) {
		m := newTestModel(t)
		m.completionActive = true
		m.completionType = "command"
		m.completionItems = []Completion{
			{Label: "/help", Insert: "/help "},
			{Label: "/clear", Insert: "/clear "},
		}
		m.completionIndex = 0
		m.initListModel("command", m.completionItems)

		m.acceptCompletion()

		if m.input.Value() != "/help " {
			t.Errorf("expected '/help ', got %q", m.input.Value())
		}
		if m.completionActive {
			t.Error("should be inactive after accept")
		}
		if m.overlay != OverlayNone {
			t.Error("overlay should be OverlayNone")
		}
	})

	t.Run("multi_select_with_selections", func(t *testing.T) {
		m := newTestModel(t)
		m.completionActive = true
		m.completionType = "attachments"
		m.completionItems = []Completion{
			{Label: "@a", Insert: "@a "},
			{Label: "@b", Insert: "@b "},
			{Label: "@c", Insert: "@c "},
		}
		m.completionIndex = 0
		m.completionSelected = map[int]bool{1: true}
		m.initListModel("attachments", m.completionItems)

		m.acceptCompletion()

		if m.input.Value() != "@b " {
			t.Errorf("expected '@b ', got %q", m.input.Value())
		}
		if m.completionActive {
			t.Error("should be inactive after accept")
		}
	})

	t.Run("multi_select_empty_fallback", func(t *testing.T) {
		m := newTestModel(t)
		m.completionActive = true
		m.completionType = "attachments"
		m.completionItems = []Completion{
			{Label: "@x", Insert: "@x "},
			{Label: "@y", Insert: "@y "},
		}
		m.completionIndex = 1
		m.completionSelected = map[int]bool{}
		m.initListModel("attachments", m.completionItems)

		m.acceptCompletion()

		if m.input.Value() != "@y " {
			t.Errorf("expected '@y ' as fallback, got %q", m.input.Value())
		}
	})

	t.Run("inactive_noop", func(t *testing.T) {
		m := newTestModel(t)
		m.completionActive = false
		m.input.SetValue("original")

		m.acceptCompletion()

		if m.input.Value() != "original" {
			t.Errorf("inactive accept should be noop, got %q", m.input.Value())
		}
	})
}

func TestCloseCompletion(t *testing.T) {
	m := newTestModel(t)
	m.completionActive = true
	m.completionItems = []Completion{{Label: "test"}}
	m.completionIndex = 5
	m.completionSelected = map[int]bool{0: true}
	m.overlay = OverlayCompletion

	m.closeCompletion()

	if m.completionActive {
		t.Error("completionActive should be false")
	}
	if m.completionItems != nil {
		t.Error("completionItems should be nil")
	}
	if m.completionIndex != 0 {
		t.Errorf("completionIndex should be 0, got %d", m.completionIndex)
	}
	if m.completionSelected != nil {
		t.Error("completionSelected should be nil")
	}
	if m.overlay != OverlayNone {
		t.Errorf("overlay should be OverlayNone, got %d", m.overlay)
	}
}
