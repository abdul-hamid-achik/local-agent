package goal

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestInferDraftPreservesPromptAndReturnsDeterministicSuggestions(t *testing.T) {
	prompt := "  Improve Cortex recovery\nwithout dispatching work.  "
	budget := BudgetLimits{MaxContinuationTurns: 8, MaxEvalTokens: 12_000, MaxWallTime: 30 * time.Minute}

	first, err := InferDraft(prompt, budget)
	if err != nil {
		t.Fatal(err)
	}
	second, err := InferDraft(prompt, budget)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("draft inference is not deterministic:\nfirst: %#v\nsecond: %#v", first, second)
	}
	if first.SourcePrompt != prompt {
		t.Fatalf("source prompt = %q, want exact %q", first.SourcePrompt, prompt)
	}
	if first.Objective != strings.TrimSpace(prompt) {
		t.Fatalf("objective = %q", first.Objective)
	}
	if first.Source != DraftSourceDeterministic || first.Version != GoalDraftVersion {
		t.Fatalf("draft identity = version %d source %q", first.Version, first.Source)
	}
	if len(first.AcceptanceCriteria) != 2 || first.Budget != budget {
		t.Fatalf("suggestions = %#v", first)
	}
}

func TestInferDraftRejectsInvalidInputAndBudget(t *testing.T) {
	tests := []struct {
		name   string
		prompt string
		budget BudgetLimits
	}{
		{name: "empty", prompt: " \n\t "},
		{name: "nul", prompt: "ship\x00now"},
		{name: "invalid utf8", prompt: string([]byte{0xff})},
		{name: "too large", prompt: strings.Repeat("x", maxDraftSourceBytes+1)},
		{name: "negative budget", prompt: "ship", budget: BudgetLimits{MaxEvalTokens: -1}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := InferDraft(test.prompt, test.budget); !errors.Is(err, ErrInvalid) {
				t.Fatalf("error = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestReviewedSpecRequiresExplicitValidReview(t *testing.T) {
	draft, err := InferDraft("Ship the recovery flow", BudgetLimits{MaxContinuationTurns: 8})
	if err != nil {
		t.Fatal(err)
	}
	spec, err := draft.ReviewedSpec(42, "  Ship safely  ", []string{" Tests pass ", "Recovery is demonstrated"}, BudgetLimits{MaxContinuationTurns: 3})
	if err != nil {
		t.Fatal(err)
	}
	if spec.SessionID != 42 || spec.Objective != "Ship safely" || spec.Budget.MaxContinuationTurns != 3 {
		t.Fatalf("spec = %#v", spec)
	}
	want := []AcceptanceCriterion{
		{ID: "criterion_1", Description: "Tests pass"},
		{ID: "criterion_2", Description: "Recovery is demonstrated"},
	}
	if !reflect.DeepEqual(spec.AcceptanceCriteria, want) {
		t.Fatalf("criteria = %#v, want %#v", spec.AcceptanceCriteria, want)
	}
	if !spec.Cortex.Empty() || spec.ID != "" {
		t.Fatalf("review unexpectedly granted external authority: %#v", spec)
	}

	for _, test := range []struct {
		name      string
		draft     Draft
		sessionID int64
		objective string
		criteria  []string
		budget    BudgetLimits
	}{
		{name: "unsupported draft", draft: Draft{}, sessionID: 1, objective: "ship", criteria: []string{"done"}},
		{name: "missing session", draft: draft, objective: "ship", criteria: []string{"done"}},
		{name: "missing objective", draft: draft, sessionID: 1, criteria: []string{"done"}},
		{name: "missing criteria", draft: draft, sessionID: 1, objective: "ship"},
		{name: "blank criterion", draft: draft, sessionID: 1, objective: "ship", criteria: []string{" "}},
		{name: "invalid budget", draft: draft, sessionID: 1, objective: "ship", criteria: []string{"done"}, budget: BudgetLimits{MaxWallTime: -1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := test.draft.ReviewedSpec(test.sessionID, test.objective, test.criteria, test.budget); !errors.Is(err, ErrInvalid) {
				t.Fatalf("error = %v, want ErrInvalid", err)
			}
		})
	}
}
