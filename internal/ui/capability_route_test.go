package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/abdul-hamid-achik/local-agent/internal/agent"
	"github.com/abdul-hamid-achik/local-agent/internal/goaladvisor"
)

func TestCapabilityRouteIsEphemeralAdvisoryFooterState(t *testing.T) {
	m := newTestModel(t)
	m.width = 100
	m.height = 24
	m.state = StateWaiting
	entriesBefore := len(m.entries)
	toolsBefore := len(m.toolEntries)
	pendingBefore := m.toolsPending

	updated, _ := m.Update(CapabilityRouteMsg{Route: agent.CapabilityRoute{
		Phase: "research", Server: "hitspec", Tool: "hitspec_capture_webpage",
	}})
	m = updated.(*Model)
	if len(m.entries) != entriesBefore || len(m.toolEntries) != toolsBefore || m.toolsPending != pendingBefore {
		t.Fatalf("route mutated transcript/tool state: entries=%d tools=%d pending=%d", len(m.entries), len(m.toolEntries), m.toolsPending)
	}
	line := m.renderWorkingLine()
	for _, expected := range []string{"Capability route", "research", "Hitspec", "capture webpage"} {
		if !strings.Contains(line, expected) {
			t.Fatalf("footer missing %q: %q", expected, line)
		}
	}
	for _, forbidden := range []string{"✓", "success", "succeeded", "executed"} {
		if strings.Contains(strings.ToLower(line), forbidden) {
			t.Fatalf("advisory footer implied success: %q", line)
		}
	}
	state, err := encodeSessionState(m)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(state, "hitspec_capture_webpage") || strings.Contains(state, "Capability route") {
		t.Fatalf("ephemeral route entered session state: %s", state)
	}

	updated, _ = m.Update(AgentDoneMsg{})
	m = updated.(*Model)
	if m.capabilityRoute != nil {
		t.Fatalf("completed turn retained route: %#v", m.capabilityRoute)
	}
}

func TestCapabilityRouteFooterRemainsResponsiveAndSanitized(t *testing.T) {
	m := newTestModel(t)
	m.width = 30
	m.height = 12
	m.state = StateWaiting
	updated, _ := m.Update(CapabilityRouteMsg{Route: agent.CapabilityRoute{
		Phase: "research", Server: "hitspec", Tool: strings.Repeat("capture_", 20),
	}})
	m = updated.(*Model)
	line := m.renderWorkingLine()
	if lipgloss.Width(line) > m.chatPaneWidth() {
		t.Fatalf("footer overflow: width=%d pane=%d line=%q", lipgloss.Width(line), m.chatPaneWidth(), line)
	}
	if !strings.Contains(line, "esc") || !strings.Contains(line, "queue") {
		t.Fatalf("narrow footer lost controls: %q", line)
	}

	updated, _ = m.Update(CapabilityRouteMsg{Route: agent.CapabilityRoute{
		Phase: "research\x1b[31m", Server: "hitspec\nspoof", Tool: "capture",
	}})
	m = updated.(*Model)
	if m.capabilityRoute != nil {
		t.Fatalf("control-bearing route was not rejected: %#v", m.capabilityRoute)
	}
}

func TestGoalCapabilityPhaseUsesOnlyTypedPhase(t *testing.T) {
	if got := goalCapabilityPhase(nil); got != "implementation" {
		t.Fatalf("nil phase = %q", got)
	}
	if got := goalCapabilityPhase(&goaladvisor.Advice{Phase: "verifying"}); got != "verification" {
		t.Fatalf("verifying phase = %q", got)
	}
	first := goalCapabilityActivityDiscriminator(&goaladvisor.Advice{Actions: []goaladvisor.Action{{Tool: "cortex_edit"}}})
	second := goalCapabilityActivityDiscriminator(&goaladvisor.Advice{Actions: []goaladvisor.Action{{Tool: "cortex_test"}}})
	if first == "" || second == "" || first == second {
		t.Fatalf("activity discriminators = %q, %q", first, second)
	}
}
