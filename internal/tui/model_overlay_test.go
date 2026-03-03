package tui

import (
	"testing"
)

func TestOverlay_ESC_ClosesCompletion(t *testing.T) {
	m := newTestModel(t)

	// Set up active completion state.
	items := []Completion{
		{Label: "/help", Insert: "/help ", Category: "command"},
		{Label: "/clear", Insert: "/clear ", Category: "command"},
		{Label: "/model", Insert: "/model ", Category: "command"},
	}
	m.completionState = newCompletionState("command", items, true)
	m.completionState.Index = 1
	m.completionState.Selected[0] = true
	m.overlay = OverlayCompletion

	// Send ESC.
	updated, _ := m.Update(escKey())
	m = updated.(*Model)

	// Verify completion state is nil.
	if m.isCompletionActive() {
		t.Error("completionState should be nil after ESC")
	}
	if m.overlay != OverlayNone {
		t.Errorf("overlay should be OverlayNone, got %d", m.overlay)
	}
}

func TestOverlay_ESC_ClearsInputToPreventRetrigger(t *testing.T) {
	m := newTestModel(t)

	// Simulate: user typed "/" which triggered completion, then presses ESC.
	m.input.SetValue("/")
	items := []Completion{
		{Label: "/help", Insert: "/help ", Category: "command"},
		{Label: "/clear", Insert: "/clear ", Category: "command"},
	}
	m.completionState = newCompletionState("command", items, false)
	m.overlay = OverlayCompletion

	// Press ESC to close.
	updated, _ := m.Update(escKey())
	m = updated.(*Model)

	// Input must be cleared so auto-trigger doesn't reopen.
	if m.input.Value() != "" {
		t.Errorf("ESC should clear input, got %q", m.input.Value())
	}
	if m.isCompletionActive() {
		t.Error("completion should be closed after ESC")
	}
	if m.overlay != OverlayNone {
		t.Errorf("overlay should be OverlayNone, got %d", m.overlay)
	}
}

func TestOverlay_ESC_NoRetriggerOnSubsequentUpdate(t *testing.T) {
	m := newTestModel(t)

	// Simulate: user typed "/" which triggered completion, then presses ESC.
	m.input.SetValue("/")
	items := []Completion{
		{Label: "/help", Insert: "/help ", Category: "command"},
	}
	m.completionState = newCompletionState("command", items, false)
	m.overlay = OverlayCompletion

	// Press ESC.
	updated, _ := m.Update(escKey())
	m = updated.(*Model)

	// Send another key event (e.g., a harmless key like 'a') to cycle through Update.
	// This exercises the auto-trigger path at lines 968-972.
	updated, _ = m.Update(charKey('a'))
	m = updated.(*Model)

	// Completion must NOT have re-opened.
	if m.isCompletionActive() {
		t.Error("completion should not re-trigger after ESC close")
	}
	if m.overlay != OverlayNone {
		t.Errorf("overlay should still be OverlayNone, got %d", m.overlay)
	}
}

func TestOverlay_ESC_ClosesHelp(t *testing.T) {
	m := newTestModel(t)
	m.overlay = OverlayHelp

	updated, _ := m.Update(escKey())
	m = updated.(*Model)

	if m.overlay != OverlayNone {
		t.Errorf("overlay should be OverlayNone after ESC, got %d", m.overlay)
	}
}

func TestOverlay_HelpDismissal(t *testing.T) {
	t.Run("question_mark_dismisses", func(t *testing.T) {
		m := newTestModel(t)
		m.overlay = OverlayHelp

		updated, _ := m.Update(charKey('?'))
		m = updated.(*Model)

		if m.overlay != OverlayNone {
			t.Errorf("? should dismiss help overlay, got %d", m.overlay)
		}
	})

	t.Run("q_dismisses", func(t *testing.T) {
		m := newTestModel(t)
		m.overlay = OverlayHelp

		updated, _ := m.Update(charKey('q'))
		m = updated.(*Model)

		if m.overlay != OverlayNone {
			t.Errorf("q should dismiss help overlay, got %d", m.overlay)
		}
	})

	t.Run("other_key_swallowed", func(t *testing.T) {
		m := newTestModel(t)
		m.overlay = OverlayHelp

		updated, _ := m.Update(charKey('a'))
		m = updated.(*Model)

		if m.overlay != OverlayHelp {
			t.Errorf("'a' should be swallowed, overlay should remain OverlayHelp, got %d", m.overlay)
		}
	})
}

func TestOverlay_CompletionNavigation(t *testing.T) {
	setup := func(t *testing.T) *Model {
		t.Helper()
		m := newTestModel(t)
		items := []Completion{
			{Label: "/help", Insert: "/help "},
			{Label: "/clear", Insert: "/clear "},
			{Label: "/model", Insert: "/model "},
		}
		m.completionState = newCompletionState("command", items, false)
		m.overlay = OverlayCompletion
		return m
	}

	t.Run("down_moves_index", func(t *testing.T) {
		m := setup(t)

		updated, _ := m.Update(downKey())
		m = updated.(*Model)

		if m.completionState.Index != 1 {
			t.Errorf("down from 0 should move to 1, got %d", m.completionState.Index)
		}
	})

	t.Run("up_at_zero_stays", func(t *testing.T) {
		m := setup(t)

		updated, _ := m.Update(upKey())
		m = updated.(*Model)

		if m.completionState.Index != 0 {
			t.Errorf("up at 0 should stay at 0, got %d", m.completionState.Index)
		}
	})

	t.Run("down_clamped_at_end", func(t *testing.T) {
		m := setup(t)
		m.completionState.Index = 2

		updated, _ := m.Update(downKey())
		m = updated.(*Model)

		if m.completionState.Index != 2 {
			t.Errorf("down at last item should stay at 2, got %d", m.completionState.Index)
		}
	})
}

func TestOverlay_CompletionToggle(t *testing.T) {
	t.Run("tab_toggles_selection_on", func(t *testing.T) {
		m := newTestModel(t)
		items := []Completion{
			{Label: "/a", Insert: "/a "},
			{Label: "/b", Insert: "/b "},
		}
		m.completionState = newCompletionState("attachments", items, true)
		m.overlay = OverlayCompletion

		updated, _ := m.Update(tabKey())
		m = updated.(*Model)

		if !m.completionState.Selected[0] {
			t.Error("tab should toggle selection on for index 0")
		}
	})

	t.Run("tab_toggles_selection_off", func(t *testing.T) {
		m := newTestModel(t)
		items := []Completion{
			{Label: "/a", Insert: "/a "},
			{Label: "/b", Insert: "/b "},
		}
		m.completionState = newCompletionState("attachments", items, true)
		m.completionState.Selected[0] = true
		m.overlay = OverlayCompletion

		updated, _ := m.Update(tabKey())
		m = updated.(*Model)

		if m.completionState.Selected[0] {
			t.Error("tab should toggle selection off for index 0")
		}
	})

	t.Run("nil_selected_no_panic", func(t *testing.T) {
		m := newTestModel(t)
		items := []Completion{
			{Label: "/a", Insert: "/a "},
		}
		m.completionState = newCompletionState("command", items, false)
		// Selected is nil for single-select mode
		m.overlay = OverlayCompletion

		// Should not panic.
		updated, _ := m.Update(tabKey())
		_ = updated.(*Model)
	})
}

func TestOverlay_CompletionAccept(t *testing.T) {
	t.Run("single_select", func(t *testing.T) {
		m := newTestModel(t)
		items := []Completion{
			{Label: "/help", Insert: "/help "},
			{Label: "/clear", Insert: "/clear "},
		}
		m.completionState = newCompletionState("command", items, false)
		m.completionState.Index = 1
		m.overlay = OverlayCompletion

		updated, _ := m.Update(enterKey())
		m = updated.(*Model)

		if m.input.Value() != "/clear " {
			t.Errorf("input should be '/clear ', got %q", m.input.Value())
		}
		if m.isCompletionActive() {
			t.Error("completion should be closed after accept")
		}
		if m.overlay != OverlayNone {
			t.Errorf("overlay should be OverlayNone, got %d", m.overlay)
		}
	})

	t.Run("multi_select_with_selections", func(t *testing.T) {
		m := newTestModel(t)
		items := []Completion{
			{Label: "@file1", Insert: "@file1 "},
			{Label: "@file2", Insert: "@file2 "},
			{Label: "@file3", Insert: "@file3 "},
		}
		m.completionState = newCompletionState("attachments", items, true)
		m.completionState.Selected[0] = true
		m.completionState.Selected[2] = true
		m.overlay = OverlayCompletion

		updated, _ := m.Update(enterKey())
		m = updated.(*Model)

		val := m.input.Value()
		// Selected items 0 and 2 should be joined.
		if val == "" {
			t.Error("input should not be empty with multi-select")
		}
		if m.isCompletionActive() {
			t.Error("completion should be closed after accept")
		}
	})

	t.Run("multi_select_empty_fallback", func(t *testing.T) {
		m := newTestModel(t)
		items := []Completion{
			{Label: "@file1", Insert: "@file1 "},
			{Label: "@file2", Insert: "@file2 "},
		}
		m.completionState = newCompletionState("attachments", items, true)
		m.completionState.Index = 1
		m.overlay = OverlayCompletion

		updated, _ := m.Update(enterKey())
		m = updated.(*Model)

		// Fallback to current item.
		if m.input.Value() != "@file2 " {
			t.Errorf("should fallback to current item, got %q", m.input.Value())
		}
	})
}
