package tui

import (
	"strings"
	"testing"
)

func TestPlanForm_NewPrefilled(t *testing.T) {
	pf := NewPlanFormState("refactor auth module")

	if len(pf.Fields) != 3 {
		t.Fatalf("expected 3 fields, got %d", len(pf.Fields))
	}

	// Task field should be pre-filled.
	if pf.Fields[0].Input.Value() != "refactor auth module" {
		t.Errorf("task field should be pre-filled, got %q", pf.Fields[0].Input.Value())
	}

	// Scope field should be select with 3 options.
	if pf.Fields[1].Kind != "select" {
		t.Errorf("scope field should be select, got %q", pf.Fields[1].Kind)
	}
	if len(pf.Fields[1].Options) != 3 {
		t.Errorf("scope should have 3 options, got %d", len(pf.Fields[1].Options))
	}

	// Focus field should be text.
	if pf.Fields[2].Kind != "text" {
		t.Errorf("focus field should be text, got %q", pf.Fields[2].Kind)
	}
}

func TestPlanForm_AssemblePrompt(t *testing.T) {
	pf := NewPlanFormState("build a REST API")
	pf.Fields[1].OptionIndex = 1 // "module"
	pf.Fields[2].Input.SetValue("keep backward compat")

	prompt := pf.AssemblePrompt()

	if !strings.Contains(prompt, "build a REST API") {
		t.Error("prompt should contain task")
	}
	if !strings.Contains(prompt, "module") {
		t.Error("prompt should contain scope")
	}
	if !strings.Contains(prompt, "keep backward compat") {
		t.Error("prompt should contain focus")
	}
	if !strings.Contains(prompt, "step-by-step plan") {
		t.Error("prompt should contain plan instruction")
	}
}

func TestPlanForm_AssemblePrompt_NoFocus(t *testing.T) {
	pf := NewPlanFormState("fix the bug")

	prompt := pf.AssemblePrompt()

	if !strings.Contains(prompt, "fix the bug") {
		t.Error("prompt should contain task")
	}
	if strings.Contains(prompt, "Focus:") {
		t.Error("prompt should not contain Focus when empty")
	}
}

func TestPlanForm_OpenClose(t *testing.T) {
	t.Run("open_sets_overlay", func(t *testing.T) {
		m := newTestModel(t)
		m.openPlanForm("test task")

		if m.overlay != OverlayPlanForm {
			t.Errorf("expected OverlayPlanForm, got %d", m.overlay)
		}
		if m.planFormState == nil {
			t.Fatal("planFormState should not be nil")
		}
		if m.planFormState.Fields[0].Input.Value() != "test task" {
			t.Error("task should be pre-filled")
		}
	})

	t.Run("close_resets_state", func(t *testing.T) {
		m := newTestModel(t)
		m.planFormState = NewPlanFormState("test")
		m.overlay = OverlayPlanForm

		m.closePlanForm()

		if m.planFormState != nil {
			t.Error("planFormState should be nil after close")
		}
		if m.overlay != OverlayNone {
			t.Errorf("overlay should be OverlayNone, got %d", m.overlay)
		}
	})
}

func TestPlanForm_EscCancels(t *testing.T) {
	m := newTestModel(t)
	m.openPlanForm("some task")

	updated, _ := m.Update(escKey())
	m = updated.(*Model)

	if m.planFormState != nil {
		t.Error("ESC should close plan form")
	}
	if m.overlay != OverlayNone {
		t.Errorf("overlay should be OverlayNone, got %d", m.overlay)
	}
}

func TestPlanForm_FieldNavigation(t *testing.T) {
	m := newTestModel(t)
	m.openPlanForm("task")

	// Initially on field 0.
	if m.planFormState.ActiveField != 0 {
		t.Fatalf("expected active field 0, got %d", m.planFormState.ActiveField)
	}

	// Tab advances to field 1.
	updated, _ := m.Update(tabKey())
	m = updated.(*Model)

	if m.planFormState.ActiveField != 1 {
		t.Errorf("expected active field 1, got %d", m.planFormState.ActiveField)
	}

	// Tab again advances to field 2.
	updated, _ = m.Update(tabKey())
	m = updated.(*Model)

	if m.planFormState.ActiveField != 2 {
		t.Errorf("expected active field 2, got %d", m.planFormState.ActiveField)
	}

	// Tab at last field stays on last field.
	updated, _ = m.Update(tabKey())
	m = updated.(*Model)

	if m.planFormState.ActiveField != 2 {
		t.Errorf("expected active field to stay at 2, got %d", m.planFormState.ActiveField)
	}
}

func TestPlanForm_SelectFieldLeftRight(t *testing.T) {
	m := newTestModel(t)
	m.openPlanForm("task")

	// Tab to scope field.
	updated, _ := m.Update(tabKey())
	m = updated.(*Model)

	if m.planFormState.ActiveField != 1 {
		t.Fatalf("expected field 1, got %d", m.planFormState.ActiveField)
	}
	if m.planFormState.Fields[1].OptionIndex != 0 {
		t.Fatalf("expected option 0, got %d", m.planFormState.Fields[1].OptionIndex)
	}

	// Right should advance to option 1.
	updated, _ = m.Update(rightKey())
	m = updated.(*Model)

	if m.planFormState.Fields[1].OptionIndex != 1 {
		t.Errorf("expected option 1 after right, got %d", m.planFormState.Fields[1].OptionIndex)
	}

	// Left should go back to option 0.
	updated, _ = m.Update(leftKey())
	m = updated.(*Model)

	if m.planFormState.Fields[1].OptionIndex != 0 {
		t.Errorf("expected option 0 after left, got %d", m.planFormState.Fields[1].OptionIndex)
	}
}

func TestPlanForm_SelectFieldBounds(t *testing.T) {
	m := newTestModel(t)
	m.openPlanForm("task")

	// Tab to scope field.
	updated, _ := m.Update(tabKey())
	m = updated.(*Model)

	// Up at 0 stays at 0.
	updated, _ = m.Update(upKey())
	m = updated.(*Model)

	if m.planFormState.Fields[1].OptionIndex != 0 {
		t.Errorf("expected option 0 after up at boundary, got %d", m.planFormState.Fields[1].OptionIndex)
	}

	// Left at 0 stays at 0.
	updated, _ = m.Update(leftKey())
	m = updated.(*Model)

	if m.planFormState.Fields[1].OptionIndex != 0 {
		t.Errorf("expected option 0 after left at boundary, got %d", m.planFormState.Fields[1].OptionIndex)
	}

	// Navigate to last option.
	updated, _ = m.Update(downKey())
	m = updated.(*Model)
	updated, _ = m.Update(downKey())
	m = updated.(*Model)

	if m.planFormState.Fields[1].OptionIndex != 2 {
		t.Fatalf("expected option 2, got %d", m.planFormState.Fields[1].OptionIndex)
	}

	// Down at last stays at last.
	updated, _ = m.Update(downKey())
	m = updated.(*Model)

	if m.planFormState.Fields[1].OptionIndex != 2 {
		t.Errorf("expected option 2 after down at boundary, got %d", m.planFormState.Fields[1].OptionIndex)
	}

	// Right at last stays at last.
	updated, _ = m.Update(rightKey())
	m = updated.(*Model)

	if m.planFormState.Fields[1].OptionIndex != 2 {
		t.Errorf("expected option 2 after right at boundary, got %d", m.planFormState.Fields[1].OptionIndex)
	}
}

func TestPlanForm_SelectField(t *testing.T) {
	m := newTestModel(t)
	m.openPlanForm("task")

	// Navigate to scope field (index 1).
	m.Update(tabKey())
	updated, _ := m.Update(tabKey())
	m = updated.(*Model)

	// Oops, we need to re-get m after first tab. Let me redo:
	m2 := newTestModel(t)
	m2.openPlanForm("task")

	// Tab to scope field.
	updated, _ = m2.Update(tabKey())
	m2 = updated.(*Model)

	if m2.planFormState.ActiveField != 1 {
		t.Fatalf("expected field 1, got %d", m2.planFormState.ActiveField)
	}

	// Down should cycle scope option.
	if m2.planFormState.Fields[1].OptionIndex != 0 {
		t.Fatalf("expected option 0, got %d", m2.planFormState.Fields[1].OptionIndex)
	}

	updated, _ = m2.Update(downKey())
	m2 = updated.(*Model)

	if m2.planFormState.Fields[1].OptionIndex != 1 {
		t.Errorf("expected option 1 after down, got %d", m2.planFormState.Fields[1].OptionIndex)
	}
}
