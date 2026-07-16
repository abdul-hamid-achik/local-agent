package ui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/local-agent/internal/agent"
)

func TestAutoCheckpointSupervisorRequiresNewBoundedProgress(t *testing.T) {
	started := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	var supervisor autoCheckpointSupervisor
	supervisor.reset("turn-root", started)

	first := &agent.AutoIterationCheckpointError{ProgressDigest: "digest-a"}
	if err := supervisor.admit("turn-root", first, started.Add(time.Minute)); err != nil {
		t.Fatalf("first checkpoint: %v", err)
	}
	if err := supervisor.admit("turn-root", first, started.Add(2*time.Minute)); err == nil ||
		!strings.Contains(err.Error(), "repeated") {
		t.Fatalf("repeated checkpoint error = %v", err)
	}
}

func TestAutoCheckpointSupervisorBoundsSegmentsAndElapsedTime(t *testing.T) {
	started := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	var supervisor autoCheckpointSupervisor
	supervisor.reset("turn-root", started)
	for index := 0; index < maxAutoCheckpointSegments; index++ {
		checkpoint := &agent.AutoIterationCheckpointError{ProgressDigest: string(rune('a' + index))}
		if err := supervisor.admit("turn-root", checkpoint, started.Add(time.Duration(index+1)*time.Minute)); err != nil {
			t.Fatalf("checkpoint %d: %v", index, err)
		}
	}
	if err := supervisor.admit("turn-root", &agent.AutoIterationCheckpointError{ProgressDigest: "overflow"}, started.Add(20*time.Minute)); err == nil ||
		!strings.Contains(err.Error(), "segment") {
		t.Fatalf("segment ceiling error = %v", err)
	}

	supervisor.reset("turn-root", started)
	if err := supervisor.admit("turn-root", &agent.AutoIterationCheckpointError{ProgressDigest: "late"}, started.Add(maxAutoCheckpointElapsed)); err == nil ||
		!strings.Contains(err.Error(), "time budget") {
		t.Fatalf("elapsed ceiling error = %v", err)
	}
}

func TestNormalizeLogicalTurnLimitsDoesNotRebaseAcrossSegments(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	limits := normalizeLogicalTurnLimits(agent.TurnLimits{MaxWallTime: 5 * time.Minute}, now)
	if limits.MaxWallTime != 0 || !limits.Deadline.Equal(now.Add(5*time.Minute)) {
		t.Fatalf("normalized limits = %#v", limits)
	}
}

func TestAgentDoneProductiveAutoCheckpointContinuesWithoutSettlement(t *testing.T) {
	m := newTestModel(t)
	started := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return started.Add(time.Minute) }
	m.state = StateStreaming
	m.turnStartedAt = started
	m.turnCheckpointSet = true
	m.turnLogicalID = "turn-root"
	m.turnSegmentID = "turn-root"
	m.turnAuthority = ModeAuto
	m.turnRunContext = context.Background()
	m.turnRunOptions = agent.TurnOptions{Limits: agent.TurnLimits{MaxEvalTokens: 100}}
	m.autoCheckpoints.reset("turn-root", started)
	m.sessionTurnCount = 4
	cancelled := false
	m.cancel = func() { cancelled = true }

	updated, command := m.Update(AgentDoneMsg{
		TurnID: "turn-root", SegmentTurnID: "turn-root",
		Err: &agent.AutoIterationCheckpointError{
			ProgressDigest: "progress-a", EvalTokens: 25,
		},
	})
	m = updated.(*Model)
	if command == nil {
		t.Fatal("productive checkpoint did not schedule the next segment")
	}
	if m.state != StateWaiting || m.turnSegmentID == "turn-root" || m.turnLogicalID != "turn-root" {
		t.Fatalf("continuation identity/state = state %v logical %q segment %q", m.state, m.turnLogicalID, m.turnSegmentID)
	}
	if m.sessionTurnCount != 4 || !m.turnCheckpointSet || cancelled {
		t.Fatalf("checkpoint settled logical turn: count=%d checkpoint=%v cancelled=%v", m.sessionTurnCount, m.turnCheckpointSet, cancelled)
	}
	if got := m.turnRunOptions.Limits.MaxEvalTokens; got != 75 {
		t.Fatalf("remaining eval budget = %d, want 75", got)
	}
	for _, entry := range m.entries {
		if entry.Kind == "error" {
			t.Fatalf("productive checkpoint rendered as error: %#v", m.entries)
		}
	}
}

func TestAgentDoneRepeatedAutoCheckpointStopsAndSettles(t *testing.T) {
	m := newTestModel(t)
	started := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return started.Add(time.Minute) }
	m.state = StateStreaming
	m.turnStartedAt = started
	m.turnCheckpointSet = true
	m.turnLogicalID = "turn-root"
	m.turnSegmentID = "turn-segment"
	m.turnAuthority = ModeAuto
	m.turnRunContext = context.Background()
	m.autoCheckpoints.reset("turn-root", started)
	m.autoCheckpoints.lastDigest = "same-progress"
	m.autoCheckpoints.segmentsContinued = 1

	updated, command := m.Update(AgentDoneMsg{
		TurnID: "turn-root", SegmentTurnID: "turn-segment",
		Err: &agent.AutoIterationCheckpointError{ProgressDigest: "same-progress"},
	})
	m = updated.(*Model)
	if command != nil {
		t.Fatal("repeated checkpoint scheduled another segment")
	}
	if m.state != StateIdle || m.turnCheckpointSet {
		t.Fatalf("repeated checkpoint did not settle: state=%v checkpoint=%v", m.state, m.turnCheckpointSet)
	}
	found := false
	for _, entry := range m.entries {
		if entry.Kind == "error" && strings.Contains(entry.Content, "repeated without new progress") {
			found = true
		}
	}
	if !found {
		t.Fatalf("safe stop receipt missing: %#v", m.entries)
	}
}
