package ui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestComposerRemainsEditableDuringOrdinaryTurn(t *testing.T) {
	for _, test := range []struct {
		name  string
		state State
	}{
		{name: "waiting", state: StateWaiting},
		{name: "streaming", state: StateStreaming},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := newTestModel(t)
			m.state = test.state

			for _, char := range "next" {
				updated, _ := m.Update(charKey(char))
				m = updated.(*Model)
			}

			if got := m.input.Value(); got != "next" {
				t.Fatalf("running composer draft = %q, want next", got)
			}
			if view := ansi.Strip(m.View().Content); !strings.Contains(view, "next") {
				t.Fatalf("running view hid recoverable draft:\n%s", view)
			}
		})
	}
}

func TestRunningEmptyComposerExplainsFollowUpQueue(t *testing.T) {
	m := newTestModel(t)
	m.state = StateWaiting
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "Write a follow-up · enter queue") {
		t.Fatalf("running composer omitted queue guidance:\n%s", view)
	}
}

func TestComposerQueuesOneFollowUpAndShowsReceipt(t *testing.T) {
	m := newTestModel(t)
	m.state = StateStreaming
	m.input.SetValue("check the tests after this")

	updated, _ := m.Update(enterKey())
	m = updated.(*Model)
	if m.queuedFollowUp == nil || m.queuedFollowUp.Prompt != "check the tests after this" {
		t.Fatalf("queued follow-up = %#v", m.queuedFollowUp)
	}
	if got := m.input.Value(); got != "" {
		t.Fatalf("queued composer was not cleared: %q", got)
	}
	if m.composerEditable() {
		t.Fatal("composer accepted a hidden second follow-up")
	}
	updated, _ = m.Update(charKey('x'))
	m = updated.(*Model)
	if got := m.input.Value(); got != "" {
		t.Fatalf("queued slot accepted a second hidden draft: %q", got)
	}
	if status := ansi.Strip(m.renderStatusLine()); !strings.Contains(status, "follow-up queued") {
		t.Fatalf("working status omitted queue receipt: %q", status)
	}
}

func TestSettledTurnKeepsUnqueuedDraftInComposer(t *testing.T) {
	m := newTestModel(t)
	m.state = StateStreaming
	m.input.SetValue("revise the next instruction")
	_, cancel := context.WithCancel(context.Background())
	m.cancel = cancel

	updated, _ := m.Update(AgentDoneMsg{TurnID: "turn-first"})
	m = updated.(*Model)
	if m.state != StateIdle || m.queuedFollowUp != nil {
		t.Fatalf("settled draft state = %v queue %#v", m.state, m.queuedFollowUp)
	}
	if got := m.input.Value(); got != "revise the next instruction" {
		t.Fatalf("settled turn changed unqueued draft to %q", got)
	}
}

func TestSettledTurnDispatchesQueuedFollowUp(t *testing.T) {
	m := newTestModel(t)
	m.state = StateStreaming
	m.queuedFollowUp = &queuedFollowUp{Prompt: "run the focused checks"}
	_, cancel := context.WithCancel(context.Background())
	m.cancel = cancel

	updated, cmd := m.Update(AgentDoneMsg{TurnID: "turn-first"})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("settled turn did not schedule queued follow-up")
	}
	if m.queuedFollowUp != nil || m.state != StateWaiting {
		t.Fatalf("queued dispatch state = queue %#v state %v", m.queuedFollowUp, m.state)
	}
	if got := m.input.Value(); got != "" {
		t.Fatalf("dispatched follow-up remained in composer: %q", got)
	}
	if len(m.entries) == 0 || m.entries[len(m.entries)-1].Kind != "user" || m.entries[len(m.entries)-1].Content != "run the focused checks" {
		t.Fatalf("queued follow-up was not presented as the next turn: %#v", m.entries)
	}
}

func TestFailedTurnRestoresQueuedFollowUpWithoutRetry(t *testing.T) {
	m := newTestModel(t)
	m.state = StateStreaming
	m.queuedFollowUp = &queuedFollowUp{Prompt: "revise this before retrying"}
	_, cancel := context.WithCancel(context.Background())
	m.cancel = cancel

	updated, _ := m.Update(AgentDoneMsg{TurnID: "turn-failed", Err: errors.New("provider failed")})
	m = updated.(*Model)
	if m.state != StateIdle || m.queuedFollowUp != nil {
		t.Fatalf("failed turn queue state = state %v queue %#v", m.state, m.queuedFollowUp)
	}
	if got := m.input.Value(); got != "revise this before retrying" {
		t.Fatalf("failed turn restored draft = %q", got)
	}
}

func TestCancelledTurnRestoresQueuedFollowUp(t *testing.T) {
	m := newTestModel(t)
	m.state = StateStreaming
	m.queuedFollowUp = &queuedFollowUp{Prompt: "keep this after cancellation"}
	_, cancel := context.WithCancel(context.Background())
	m.cancel = cancel

	updated, _ := m.Update(AgentDoneMsg{TurnID: "turn-cancelled", Err: context.Canceled})
	m = updated.(*Model)
	if m.queuedFollowUp != nil || m.input.Value() != "keep this after cancellation" {
		t.Fatalf("cancelled queue=%#v draft=%q", m.queuedFollowUp, m.input.Value())
	}
}

func TestUndurableSettlementRestoresQueuedFollowUp(t *testing.T) {
	m := newTestModel(t)
	store, _ := attachGoalTestSession(t, m)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	m.state = StateStreaming
	m.queuedFollowUp = &queuedFollowUp{Prompt: "do not strand this follow-up"}
	_, cancel := context.WithCancel(context.Background())
	m.cancel = cancel

	updated, _ := m.Update(AgentDoneMsg{TurnID: "turn-undurable"})
	m = updated.(*Model)
	if m.state != StateIdle || m.queuedFollowUp != nil {
		t.Fatalf("undurable settlement state=%v queue=%#v", m.state, m.queuedFollowUp)
	}
	if got := m.input.Value(); got != "do not strand this follow-up" {
		t.Fatalf("undurable settlement restored draft = %q", got)
	}
}

func TestGoalTurnDoesNotExposeOrdinaryFollowUpQueue(t *testing.T) {
	m := newTestModel(t)
	m.state = StateStreaming
	m.goalTurnID = "goal-turn"
	if m.composerEditable() {
		t.Fatal("goal continuation exposed an ordinary follow-up composer")
	}

	updated, _ := m.Update(charKey('x'))
	m = updated.(*Model)
	if got := m.input.Value(); got != "" {
		t.Fatalf("goal turn accepted hidden composer input: %q", got)
	}
}
