package ui

import (
	"strings"
	"testing"
	"time"

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
		if m.overlay != OverlayNone || m.goalFormState != nil {
			t.Errorf("AUTO mode switch created goal UI: overlay=%d form=%v", m.overlay, m.goalFormState != nil)
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

func TestExplicitGoalDurationOpensReviewWithoutHiddenCaps(t *testing.T) {
	m := newTestModel(t)
	m.handleCommandAction(command.Result{Action: command.ActionOpenGoal, Goal: &command.GoalRequest{
		Prompt: "polish the model picker", TimeBudget: 45 * time.Minute, TimeExplicit: true,
	}})
	if m.overlay != OverlayGoalForm || m.goalFormState == nil {
		t.Fatalf("goal review overlay=%v form=%v", m.overlay, m.goalFormState != nil)
	}
	values, err := m.goalFormState.Values()
	if err != nil {
		t.Fatalf("goal review values: %v", err)
	}
	if values.TimeBudget != 45*time.Minute || values.TurnBudget != 0 || values.TokenBudget != 0 {
		t.Fatalf("goal budgets = %#v", values)
	}
	if m.goalFormState.active != GoalFieldActions {
		t.Fatalf("complete goal request focused field %v, want actions", m.goalFormState.active)
	}
}

func TestPlanModeCannotStartReviewedGoal(t *testing.T) {
	m := newTestModel(t)
	m.mode = ModePlan
	m.overlay = OverlayGoalForm
	m.goalFormState = NewGoalForm(GoalFormValues{
		Objective: "ship safely", AcceptanceCriteria: "tests pass", TimeBudget: time.Minute,
	}, GoalFormOptions{})
	m.goalFormState.SetActiveField(GoalFieldActions)
	entriesBefore := len(m.entries)
	cmd := m.applyGoalForm(GoalFormEvent{Action: GoalActionSave, Values: GoalFormValues{
		Objective: "ship safely", AcceptanceCriteria: "tests pass", TimeBudget: time.Minute,
	}})
	if cmd != nil || m.goalRuntime != nil || m.mode != ModePlan {
		t.Fatalf("plan goal started: cmd=%v runtime=%v mode=%v", cmd != nil, m.goalRuntime != nil, m.mode)
	}
	if m.overlay != OverlayGoalForm || m.goalFormState == nil || m.goalFormState.ActiveField() != GoalFieldActions {
		t.Fatalf("plan rejection dismissed or moved form: overlay=%v form=%v field=%v", m.overlay, m.goalFormState != nil, m.goalFormState.ActiveField())
	}
	if len(m.entries) != entriesBefore {
		t.Fatalf("plan rejection leaked behind modal: entries=%d, want %d", len(m.entries), entriesBefore)
	}
	for _, want := range []string{"PLAN", "AUTO"} {
		if !strings.Contains(m.goalFormState.Error(), want) {
			t.Fatalf("inline error %q omits %q", m.goalFormState.Error(), want)
		}
		if !strings.Contains(ansi.Strip(m.goalFormState.View()), want) {
			t.Fatalf("rendered form omits %q:\n%s", want, ansi.Strip(m.goalFormState.View()))
		}
	}
	values, err := m.goalFormState.Values()
	if err != nil || values.Objective != "ship safely" || values.AcceptanceCriteria != "tests pass" || values.TimeBudget != time.Minute {
		t.Fatalf("plan rejection changed form values: values=%#v err=%v", values, err)
	}
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

	t.Run("normal_mode_is_unbadged", func(t *testing.T) {
		m.mode = ModeNormal
		status := m.renderStatusLine()
		if strings.Contains(status, "NORMAL") {
			t.Errorf("normal mode should be visually quiet, got %q", status)
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

func TestWelcomeMarksUnavailableOllamaModelOffline(t *testing.T) {
	m := newTestModel(t)
	m.model = "qwen3.5:2b"
	m.ollamaOffline = true
	var view strings.Builder
	m.renderWelcome(&view)
	if got := view.String(); !strings.Contains(got, "qwen3.5:2b · offline") {
		t.Fatalf("offline welcome = %q", got)
	}
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
		if !m.goalFormState.draftFromPrompt {
			t.Fatal("AUTO prompt was not presented as a reviewable draft")
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
