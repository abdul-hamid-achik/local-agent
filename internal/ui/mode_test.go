package ui

import (
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/local-agent/internal/command"
	"github.com/abdul-hamid-achik/local-agent/internal/goal"
	"github.com/charmbracelet/x/ansi"
)

func TestCycleMode(t *testing.T) {
	t.Run("cycles_normal_to_plan", func(t *testing.T) {
		m := newTestModel(t)
		if m.mode != ModeNormal {
			t.Fatalf("expected initial mode ModeNormal, got %d", m.mode)
		}

		updated, _ := m.Update(shiftTabKey())
		m = updated.(*Model)

		if m.mode != ModePlan {
			t.Errorf("expected ModePlan after cycling from NORMAL, got %d", m.mode)
		}
	})

	t.Run("cycles_normal_to_plan_explicit", func(t *testing.T) {
		m := newTestModel(t)
		m.mode = ModeNormal

		updated, _ := m.Update(shiftTabKey())
		m = updated.(*Model)

		if m.mode != ModePlan {
			t.Errorf("expected ModePlan after cycling from NORMAL, got %d", m.mode)
		}
	})

	t.Run("cycles_plan_to_auto", func(t *testing.T) {
		m := newTestModel(t)
		m.mode = ModePlan

		updated, _ := m.Update(shiftTabKey())
		m = updated.(*Model)

		if m.mode != ModeAuto {
			t.Errorf("expected ModeAuto after cycling from PLAN, got %d", m.mode)
		}
		if m.overlay != OverlayGoalForm {
			t.Errorf("AUTO without a live goal should open the goal form, overlay=%d", m.overlay)
		}
	})

	t.Run("cycles_auto_to_normal", func(t *testing.T) {
		m := newTestModel(t)
		m.mode = ModeAuto

		updated, _ := m.Update(shiftTabKey())
		m = updated.(*Model)

		if m.mode != ModeNormal || m.overlay != OverlayNone {
			t.Fatalf("AUTO cycle = mode %d overlay %d, want NORMAL/chat", m.mode, m.overlay)
		}
	})

	t.Run("adds_system_message", func(t *testing.T) {
		m := newTestModel(t)
		m.entries = append(m.entries, ChatEntry{Kind: "user", Content: "hello"})
		before := len(m.entries)

		updated, _ := m.Update(shiftTabKey())
		m = updated.(*Model)

		if len(m.entries) <= before {
			t.Fatal("expected system message entry after mode switch")
		}
		last := m.entries[len(m.entries)-1]
		if last.Kind != "system" {
			t.Errorf("expected 'system' kind, got %q", last.Kind)
		}
		if !strings.Contains(last.Content, "Mode · PLAN") {
			t.Errorf("expected mode switch info in content, got %q", last.Content)
		}
	})

	t.Run("no_cycle_when_not_idle", func(t *testing.T) {
		m := newTestModel(t)
		m.state = StateStreaming
		before := m.mode

		updated, _ := m.Update(shiftTabKey())
		m = updated.(*Model)

		if m.mode != before {
			t.Error("should not cycle mode when not idle")
		}
	})
}

func TestModePickerKeepsAllAuthoritiesActionableAtMinimum(t *testing.T) {
	m := newTestModel(t)
	m.width, m.height = minTerminalWidth, minTerminalHeight
	m.openModePicker()
	rendered := m.renderModePicker()
	plain := ansi.Strip(rendered)
	for _, want := range []string{"NORMAL", "PLAN", "AUTO", "esc close", "enter", "↑/↓"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("minimum mode picker omitted %q:\n%s", want, plain)
		}
	}
	assertRenderedLinesFit(t, rendered, minTerminalWidth)
	assertRenderedHeightFits(t, rendered, minTerminalHeight)
}

func TestModeStatusLine(t *testing.T) {
	m := newTestModel(t)
	m.state = StateIdle
	m.entries = []ChatEntry{{Kind: "user", Content: "conversation started"}}

	t.Run("auto_mode_badge", func(t *testing.T) {
		m.mode = ModeAuto
		status := m.renderStatusLine()
		if !strings.Contains(status, "AUTO") {
			t.Errorf("status line should contain AUTO badge, got %q", status)
		}
	})

	t.Run("normal_mode_badge", func(t *testing.T) {
		m.mode = ModeNormal
		status := m.renderStatusLine()
		if !strings.Contains(status, "NORMAL") {
			t.Errorf("status line should contain NORMAL badge, got %q", status)
		}
	})

	t.Run("plan_mode_badge", func(t *testing.T) {
		m.mode = ModePlan
		status := m.renderStatusLine()
		if !strings.Contains(status, "PLAN") {
			t.Errorf("status line should contain PLAN badge, got %q", status)
		}
	})
}

func TestDefaultModeConfigs(t *testing.T) {
	configs := DefaultModeConfigs()

	if configs[ModeNormal].Label != "NORMAL" {
		t.Errorf("ModeNormal label should be NORMAL, got %q", configs[ModeNormal].Label)
	}
	if !configs[ModeNormal].ToolPolicy.AllowMCP {
		t.Error("ModeNormal should allow approval-gated MCP tools")
	}

	if configs[ModePlan].Label != "PLAN" {
		t.Errorf("ModePlan label should be PLAN, got %q", configs[ModePlan].Label)
	}
	if configs[ModePlan].ToolPolicy.AllowMCP {
		t.Error("ModePlan should not allow MCP tools")
	}

	if configs[ModeAuto].Label != "AUTO" {
		t.Errorf("ModeAuto label should be AUTO, got %q", configs[ModeAuto].Label)
	}
	if !configs[ModeAuto].ToolPolicy.AllowMCP {
		t.Error("ModeAuto should allow tools under Goal Runtime and permission policy")
	}
}

func TestAutoSubmitEntersOrControlsDurableGoal(t *testing.T) {
	t.Run("new goal opens prefilled form", func(t *testing.T) {
		client := &goalCountingClient{}
		m := newGoalRuntimeTestModel(t, client)
		m.mode = ModeAuto
		m.input.SetValue("ship a verified compact interface")
		if cmd := m.submitInput(); cmd != nil {
			t.Fatal("AUTO goal form unexpectedly dispatched a provider command")
		}
		if m.overlay != OverlayGoalForm || m.goalFormState == nil {
			t.Fatalf("AUTO submit overlay=%d form=%v", m.overlay, m.goalFormState != nil)
		}
		if got := m.goalFormState.objective.Value(); got != "ship a verified compact interface" {
			t.Fatalf("prefilled objective = %q", got)
		}
		if client.calls.Load() != 0 {
			t.Fatalf("AUTO form made %d provider calls", client.calls.Load())
		}
	})

	t.Run("live goal preserves draft and opens inspector", func(t *testing.T) {
		client := &goalCountingClient{}
		m := newGoalRuntimeTestModel(t, client)
		m.mode = ModeAuto
		m.goalRuntime = newUIGoalRuntime(t, 42, goal.BudgetLimits{MaxContinuationTurns: 3})
		m.input.SetValue("one-off instruction that must not bypass the goal")
		if cmd := m.submitInput(); cmd != nil {
			t.Fatal("active AUTO goal unexpectedly dispatched a provider command")
		}
		if m.overlay != OverlayGoalInspector || m.goalInspectorState == nil {
			t.Fatalf("active AUTO submit overlay=%d inspector=%v", m.overlay, m.goalInspectorState != nil)
		}
		if got := m.input.Value(); got != "one-off instruction that must not bypass the goal" {
			t.Fatalf("preserved AUTO draft = %q", got)
		}
		if client.calls.Load() != 0 {
			t.Fatalf("active AUTO draft made %d provider calls", client.calls.Load())
		}
	})

	t.Run("custom prompt command cannot bypass live goal", func(t *testing.T) {
		client := &goalCountingClient{}
		m := newGoalRuntimeTestModel(t, client)
		m.mode = ModeAuto
		m.goalRuntime = newUIGoalRuntime(t, 42, goal.BudgetLimits{MaxContinuationTurns: 3})
		m.input.SetValue("draft remains visible")
		if cmd := m.handleCommandAction(command.Result{
			Action: command.ActionSendPrompt,
			Data:   "expanded custom instruction that must not dispatch",
		}); cmd != nil {
			t.Fatal("AUTO custom prompt returned a provider command")
		}
		if m.overlay != OverlayGoalInspector || m.goalInspectorState == nil {
			t.Fatalf("AUTO custom prompt overlay=%d inspector=%v", m.overlay, m.goalInspectorState != nil)
		}
		if got := m.input.Value(); got != "expanded custom instruction that must not dispatch" {
			t.Fatalf("AUTO custom prompt did not preserve its expanded draft: %q", got)
		}
		if client.calls.Load() != 0 {
			t.Fatalf("AUTO custom prompt made %d provider calls", client.calls.Load())
		}
	})
}
