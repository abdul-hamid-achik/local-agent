package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestAgentQuestionYesNoDefaultsToNoAndOverlayKeepsTranscript(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(*Model)
	m.setTestTranscriptContent("VISIBLE CONVERSATION")
	responses := make(chan AgentQuestionResponse, 1)
	updated, _ = m.Update(AgentQuestionMsg{
		CallID:   "q1",
		Kind:     QuestionKindYesNo,
		Prompt:   "Overwrite README?",
		Options:  []QuestionChoice{{ID: "yes", Label: "Yes"}, {ID: "no", Label: "No"}},
		Response: responses,
	})
	m = updated.(*Model)
	if m.pendingQuestion == nil {
		t.Fatal("question did not open")
	}
	plain := ansi.Strip(m.View().Content)
	for _, want := range []string{"VISIBLE CONVERSATION", "Overwrite README?", "No", "Yes"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("question overlay omitted %q:\n%s", want, plain)
		}
	}
	updated, _ = m.Update(enterKey())
	m = updated.(*Model)
	select {
	case answer := <-responses:
		if answer.Skipped || answer.ID != "no" {
			t.Fatalf("enter answered %#v, want no", answer)
		}
	case <-time.After(time.Second):
		t.Fatal("enter did not answer")
	}
	if m.pendingQuestion != nil {
		t.Fatal("question remained open")
	}
}

func TestAgentQuestionEscapeSkipsWithoutCancellingTurn(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(*Model)
	responses := make(chan AgentQuestionResponse, 1)
	updated, _ = m.Update(AgentQuestionMsg{
		CallID:   "q1",
		Kind:     QuestionKindSingle,
		Prompt:   "Which rollout?",
		Options:  []QuestionChoice{{ID: "canary", Label: "Canary"}, {ID: "all", Label: "All"}},
		Response: responses,
	})
	m = updated.(*Model)
	_, cmd := m.Update(escKey())
	if cmd != nil {
		t.Fatal("skip cancelled the turn")
	}
	select {
	case answer := <-responses:
		if !answer.Skipped {
			t.Fatalf("esc answer = %#v, want skipped", answer)
		}
	case <-time.After(time.Second):
		t.Fatal("esc did not skip")
	}
}

func TestAgentQuestionYShortcutAnswersYes(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(*Model)
	responses := make(chan AgentQuestionResponse, 1)
	updated, _ = m.Update(AgentQuestionMsg{
		Kind:     QuestionKindYesNo,
		Prompt:   "Go?",
		Options:  []QuestionChoice{{ID: "yes", Label: "Yes"}, {ID: "no", Label: "No"}},
		Response: responses,
	})
	m = updated.(*Model)
	_, _ = m.Update(charKey('y'))
	select {
	case answer := <-responses:
		if answer.ID != "yes" {
			t.Fatalf("y answered %#v", answer)
		}
	case <-time.After(time.Second):
		t.Fatal("y did not answer")
	}
}
