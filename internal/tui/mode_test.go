package tui

import (
	"strings"
	"testing"
)

func TestCycleMode(t *testing.T) {
	t.Run("cycles_build_to_ask", func(t *testing.T) {
		m := newTestModel(t)
		// Default mode is BUILD.
		if m.mode != ModeBuild {
			t.Fatalf("expected initial mode ModeBuild, got %d", m.mode)
		}

		updated, _ := m.Update(shiftTabKey())
		m = updated.(*Model)

		if m.mode != ModeAsk {
			t.Errorf("expected ModeAsk after cycling from BUILD, got %d", m.mode)
		}
	})

	t.Run("cycles_ask_to_plan", func(t *testing.T) {
		m := newTestModel(t)
		m.mode = ModeAsk

		updated, _ := m.Update(shiftTabKey())
		m = updated.(*Model)

		if m.mode != ModePlan {
			t.Errorf("expected ModePlan after cycling from ASK, got %d", m.mode)
		}
	})

	t.Run("cycles_plan_to_build", func(t *testing.T) {
		m := newTestModel(t)
		m.mode = ModePlan

		updated, _ := m.Update(shiftTabKey())
		m = updated.(*Model)

		if m.mode != ModeBuild {
			t.Errorf("expected ModeBuild after cycling from PLAN, got %d", m.mode)
		}
	})

	t.Run("adds_system_message", func(t *testing.T) {
		m := newTestModel(t)
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
		if !strings.Contains(last.Content, "Mode:") {
			t.Errorf("expected mode info in content, got %q", last.Content)
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

func TestModeStatusLine(t *testing.T) {
	m := newTestModel(t)
	m.state = StateIdle

	t.Run("build_mode_badge", func(t *testing.T) {
		m.mode = ModeBuild
		status := m.renderStatusLine()
		if !strings.Contains(status, "BUILD") {
			t.Errorf("status line should contain BUILD badge, got %q", status)
		}
	})

	t.Run("ask_mode_badge", func(t *testing.T) {
		m.mode = ModeAsk
		status := m.renderStatusLine()
		if !strings.Contains(status, "ASK") {
			t.Errorf("status line should contain ASK badge, got %q", status)
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

	if configs[ModeAsk].Label != "ASK" {
		t.Errorf("ModeAsk label should be ASK, got %q", configs[ModeAsk].Label)
	}
	if configs[ModeAsk].AllowTools {
		t.Error("ModeAsk should not allow tools")
	}

	if configs[ModePlan].Label != "PLAN" {
		t.Errorf("ModePlan label should be PLAN, got %q", configs[ModePlan].Label)
	}
	if configs[ModePlan].AllowTools {
		t.Error("ModePlan should not allow tools")
	}

	if configs[ModeBuild].Label != "BUILD" {
		t.Errorf("ModeBuild label should be BUILD, got %q", configs[ModeBuild].Label)
	}
	if !configs[ModeBuild].AllowTools {
		t.Error("ModeBuild should allow tools")
	}
}
