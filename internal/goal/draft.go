package goal

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	// GoalDraftVersion identifies the review payload produced before a durable
	// goal exists. Drafts have no execution authority and are never persisted as
	// Runtime snapshots.
	GoalDraftVersion = 1

	maxDraftSourceBytes = MaxObjectiveBytes
)

// DraftSource records how a review-only goal suggestion was produced.
type DraftSource string

const (
	DraftSourceDeterministic DraftSource = "deterministic"
)

// Draft is a bounded, review-only suggestion derived from a user's prompt.
// SourcePrompt preserves the exact submitted bytes so presentation code can
// restore the composer without silently replacing the user's words. Objective
// and AcceptanceCriteria are suggestions only; callers must present them for
// explicit review and must not create or dispatch a Runtime from a Draft alone.
type Draft struct {
	Version            int
	Source             DraftSource
	SourcePrompt       string
	Objective          string
	AcceptanceCriteria []string
	Budget             BudgetLimits
}

// InferDraft returns conservative local suggestions without consulting a
// provider, Cortex, the filesystem, or the network. This makes AUTO entry
// deterministic and keeps inference outside the execution authority boundary.
func InferDraft(prompt string, defaults BudgetLimits) (Draft, error) {
	if !utf8.ValidString(prompt) {
		return Draft{}, fmt.Errorf("%w: goal prompt is not valid UTF-8", ErrInvalid)
	}
	if len(prompt) > maxDraftSourceBytes {
		return Draft{}, fmt.Errorf("%w: goal prompt exceeds %d bytes", ErrInvalid, maxDraftSourceBytes)
	}
	if strings.IndexByte(prompt, 0) >= 0 {
		return Draft{}, fmt.Errorf("%w: goal prompt contains a NUL byte", ErrInvalid)
	}
	if err := defaults.Validate(); err != nil {
		return Draft{}, err
	}

	objective := strings.TrimSpace(prompt)
	if objective == "" {
		return Draft{}, fmt.Errorf("%w: goal prompt is required", ErrInvalid)
	}

	return Draft{
		Version:      GoalDraftVersion,
		Source:       DraftSourceDeterministic,
		SourcePrompt: prompt,
		Objective:    objective,
		AcceptanceCriteria: []string{
			"The requested outcome is complete and can be demonstrated.",
			"Relevant verification passes for the changed behavior.",
		},
		Budget: defaults,
	}, nil
}

// ReviewedSpec converts explicitly reviewed values into the immutable portion
// of a Runtime spec. It deliberately requires the caller to supply a durable
// session ID and never creates a Runtime or dispatches work.
func (d Draft) ReviewedSpec(sessionID int64, objective string, criteria []string, budget BudgetLimits) (Spec, error) {
	if d.Version != GoalDraftVersion || d.Source != DraftSourceDeterministic {
		return Spec{}, fmt.Errorf("%w: unsupported goal draft", ErrInvalid)
	}
	if sessionID <= 0 {
		return Spec{}, fmt.Errorf("%w: session id must be positive", ErrInvalid)
	}
	objective = strings.TrimSpace(objective)
	if objective == "" {
		return Spec{}, fmt.Errorf("%w: objective is required", ErrInvalid)
	}
	if len(criteria) == 0 || len(criteria) > MaxCriteria {
		return Spec{}, fmt.Errorf("%w: acceptance criteria count must be between 1 and %d", ErrInvalid, MaxCriteria)
	}
	accepted := make([]AcceptanceCriterion, 0, len(criteria))
	for index, description := range criteria {
		description = strings.TrimSpace(description)
		if description == "" {
			return Spec{}, fmt.Errorf("%w: acceptance criterion %d is required", ErrInvalid, index+1)
		}
		accepted = append(accepted, AcceptanceCriterion{
			ID:          fmt.Sprintf("criterion_%d", index+1),
			Description: description,
		})
	}
	if err := budget.Validate(); err != nil {
		return Spec{}, err
	}

	spec := Spec{SessionID: sessionID, Objective: objective, AcceptanceCriteria: accepted, Budget: budget}
	if err := validateSpec(spec, false); err != nil {
		return Spec{}, err
	}
	return spec, nil
}
