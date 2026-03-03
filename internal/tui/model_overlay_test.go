package tui

import (
	"testing"
)

func TestOverlay_ESC_ClosesCompletion(t *testing.T) {
	m := newTestModel(t)

	// Set up active completion state.
	m.completionActive = true
	m.overlay = OverlayCompletion
	m.completionItems = []Completion{
		{Label: "/help", Insert: "/help ", Category: "command"},
		{Label: "/clear", Insert: "/clear ", Category: "command"},
		{Label: "/model", Insert: "/model ", Category: "command"},
	}
	m.completionIndex = 1
	m.completionSelected = map[int]bool{0: true}

	// Send ESC.
	updated, _ := m.Update(escKey())
	m = updated.(*Model)

	// Verify ALL 5 fields reset.
	if m.completionActive {
		t.Error("completionActive should be false after ESC")
	}
	if m.completionItems != nil {
		t.Errorf("completionItems should be nil, got %v", m.completionItems)
	}
	if m.completionIndex != 0 {
		t.Errorf("completionIndex should be 0, got %d", m.completionIndex)
	}
	if m.completionSelected != nil {
		t.Errorf("completionSelected should be nil, got %v", m.completionSelected)
	}
	if m.overlay != OverlayNone {
		t.Errorf("overlay should be OverlayNone, got %d", m.overlay)
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
		m.completionActive = true
		m.overlay = OverlayCompletion
		m.completionItems = []Completion{
			{Label: "/help", Insert: "/help "},
			{Label: "/clear", Insert: "/clear "},
			{Label: "/model", Insert: "/model "},
		}
		m.completionIndex = 0
		m.initListModel("command", m.completionItems)
		return m
	}

	t.Run("down_moves_index", func(t *testing.T) {
		m := setup(t)

		updated, _ := m.Update(downKey())
		m = updated.(*Model)

		if m.completionIndex != 1 {
			t.Errorf("down from 0 should move to 1, got %d", m.completionIndex)
		}
	})

	t.Run("up_at_zero_stays", func(t *testing.T) {
		m := setup(t)

		updated, _ := m.Update(upKey())
		m = updated.(*Model)

		if m.completionIndex != 0 {
			t.Errorf("up at 0 should stay at 0, got %d", m.completionIndex)
		}
	})

	t.Run("down_clamped_at_end", func(t *testing.T) {
		m := setup(t)
		m.completionIndex = 2

		updated, _ := m.Update(downKey())
		m = updated.(*Model)

		if m.completionIndex != 2 {
			t.Errorf("down at last item should stay at 2, got %d", m.completionIndex)
		}
	})

	t.Run("tab_cycles_with_wrap", func(t *testing.T) {
		m := setup(t)
		m.completionIndex = 2

		updated, _ := m.Update(tabKey())
		m = updated.(*Model)

		if m.completionIndex != 0 {
			t.Errorf("tab at 2 should wrap to 0, got %d", m.completionIndex)
		}
	})

	t.Run("tab_increments", func(t *testing.T) {
		m := setup(t)
		m.completionIndex = 0

		updated, _ := m.Update(tabKey())
		m = updated.(*Model)

		if m.completionIndex != 1 {
			t.Errorf("tab from 0 should go to 1, got %d", m.completionIndex)
		}
	})
}

func TestOverlay_CompletionToggle(t *testing.T) {
	t.Run("space_toggles_selection_on", func(t *testing.T) {
		m := newTestModel(t)
		m.completionActive = true
		m.overlay = OverlayCompletion
		m.completionItems = []Completion{
			{Label: "/a", Insert: "/a "},
			{Label: "/b", Insert: "/b "},
		}
		m.completionIndex = 0
		m.completionSelected = make(map[int]bool)
		m.initListModel("attachments", m.completionItems)

		updated, _ := m.Update(spaceKey())
		m = updated.(*Model)

		if !m.completionSelected[0] {
			t.Error("space should toggle selection on for index 0")
		}
	})

	t.Run("space_toggles_selection_off", func(t *testing.T) {
		m := newTestModel(t)
		m.completionActive = true
		m.overlay = OverlayCompletion
		m.completionItems = []Completion{
			{Label: "/a", Insert: "/a "},
			{Label: "/b", Insert: "/b "},
		}
		m.completionIndex = 0
		m.completionSelected = map[int]bool{0: true}
		m.initListModel("attachments", m.completionItems)

		updated, _ := m.Update(spaceKey())
		m = updated.(*Model)

		if m.completionSelected[0] {
			t.Error("space should toggle selection off for index 0")
		}
	})

	t.Run("nil_selected_no_panic", func(t *testing.T) {
		m := newTestModel(t)
		m.completionActive = true
		m.overlay = OverlayCompletion
		m.completionItems = []Completion{
			{Label: "/a", Insert: "/a "},
		}
		m.completionIndex = 0
		m.completionSelected = nil // nil map
		m.initListModel("command", m.completionItems)

		// Should not panic.
		updated, _ := m.Update(spaceKey())
		_ = updated.(*Model)
	})
}

func TestOverlay_CompletionAccept(t *testing.T) {
	t.Run("single_select", func(t *testing.T) {
		m := newTestModel(t)
		m.completionActive = true
		m.overlay = OverlayCompletion
		m.completionType = "command"
		m.completionItems = []Completion{
			{Label: "/help", Insert: "/help "},
			{Label: "/clear", Insert: "/clear "},
		}
		m.completionIndex = 1
		m.initListModel("command", m.completionItems)

		updated, _ := m.Update(enterKey())
		m = updated.(*Model)

		if m.input.Value() != "/clear " {
			t.Errorf("input should be '/clear ', got %q", m.input.Value())
		}
		if m.completionActive {
			t.Error("completion should be closed after accept")
		}
		if m.overlay != OverlayNone {
			t.Errorf("overlay should be OverlayNone, got %d", m.overlay)
		}
	})

	t.Run("multi_select_with_selections", func(t *testing.T) {
		m := newTestModel(t)
		m.completionActive = true
		m.overlay = OverlayCompletion
		m.completionType = "attachments"
		m.completionItems = []Completion{
			{Label: "@file1", Insert: "@file1 "},
			{Label: "@file2", Insert: "@file2 "},
			{Label: "@file3", Insert: "@file3 "},
		}
		m.completionIndex = 0
		m.completionSelected = map[int]bool{0: true, 2: true}
		m.initListModel("attachments", m.completionItems)

		updated, _ := m.Update(enterKey())
		m = updated.(*Model)

		val := m.input.Value()
		// Selected items 0 and 2 should be joined.
		if val == "" {
			t.Error("input should not be empty with multi-select")
		}
		if m.completionActive {
			t.Error("completion should be closed after accept")
		}
	})

	t.Run("multi_select_empty_fallback", func(t *testing.T) {
		m := newTestModel(t)
		m.completionActive = true
		m.overlay = OverlayCompletion
		m.completionType = "attachments"
		m.completionItems = []Completion{
			{Label: "@file1", Insert: "@file1 "},
			{Label: "@file2", Insert: "@file2 "},
		}
		m.completionIndex = 1
		m.completionSelected = map[int]bool{} // empty
		m.initListModel("attachments", m.completionItems)

		updated, _ := m.Update(enterKey())
		m = updated.(*Model)

		// Fallback to current item.
		if m.input.Value() != "@file2 " {
			t.Errorf("should fallback to current item, got %q", m.input.Value())
		}
	})
}
