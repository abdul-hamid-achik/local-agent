package goal

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(duration time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(duration)
	c.mu.Unlock()
}

func testClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, time.July, 12, 12, 0, 0, 0, time.UTC)}
}

func testSpec(budget BudgetLimits) Spec {
	return Spec{
		ID:        "goal_test",
		SessionID: 42,
		Objective: "Ship the goal runtime safely",
		AcceptanceCriteria: []AcceptanceCriterion{
			{ID: "tests", Description: "All focused tests pass"},
			{ID: "recovery", Description: "Unknown outcomes block continuation"},
		},
		Budget: budget,
	}
}

func testRuntime(t *testing.T, budget BudgetLimits) (*Runtime, *fakeClock) {
	t.Helper()
	clock := testClock()
	runtime, err := New(testSpec(budget), WithClock(clock))
	if err != nil {
		t.Fatal(err)
	}
	return runtime, clock
}

func mustSnapshot(t *testing.T, runtime *Runtime) Snapshot {
	t.Helper()
	snapshot, err := runtime.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func productiveTurn(id string, tokens int64) TurnReport {
	return TurnReport{TurnID: id, EvalTokens: tokens, Productive: true, Summary: "completed a verified unit of work"}
}

func completionRequest() CompletionRequest {
	return CompletionRequest{
		ValidatedBy: "local-agent-host",
		Summary:     "every acceptance criterion was verified",
		Results: []AcceptanceResult{
			{CriterionID: "tests", Satisfied: true, Evidence: "go test ./internal/goal passed"},
			{CriterionID: "recovery", Satisfied: true, Evidence: "outcome-unknown recovery test passed"},
		},
	}
}

func TestNewValidatesDefinitionAndReturnsIsolatedSnapshots(t *testing.T) {
	runtime, _ := testRuntime(t, BudgetLimits{})
	first := mustSnapshot(t, runtime)
	if first.Version != SnapshotVersion || first.State != StateActive || first.SessionID != 42 {
		t.Fatalf("initial snapshot = %#v", first)
	}
	if !strings.HasPrefix(first.ID, "goal_") && first.ID != "goal_test" {
		t.Fatalf("unexpected goal id %q", first.ID)
	}

	first.AcceptanceCriteria[0].Description = "mutated"
	second := mustSnapshot(t, runtime)
	if second.AcceptanceCriteria[0].Description == "mutated" {
		t.Fatal("snapshot criteria alias runtime state")
	}

	tests := []struct {
		name string
		edit func(*Spec)
	}{
		{name: "session", edit: func(spec *Spec) { spec.SessionID = 0 }},
		{name: "objective", edit: func(spec *Spec) { spec.Objective = " " }},
		{name: "criteria", edit: func(spec *Spec) { spec.AcceptanceCriteria = nil }},
		{name: "duplicate criterion", edit: func(spec *Spec) { spec.AcceptanceCriteria[1].ID = "tests" }},
		{name: "negative budget", edit: func(spec *Spec) { spec.Budget.MaxEvalTokens = -1 }},
		{name: "partial cortex", edit: func(spec *Spec) { spec.Cortex.TaskID = "task_1" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := testSpec(BudgetLimits{})
			test.edit(&spec)
			if _, err := New(spec); !errors.Is(err, ErrInvalid) {
				t.Fatalf("New() error = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestNewGeneratesDistinctGoalIDs(t *testing.T) {
	spec := testSpec(BudgetLimits{})
	spec.ID = ""
	first, err := New(spec)
	if err != nil {
		t.Fatal(err)
	}
	second, err := New(spec)
	if err != nil {
		t.Fatal(err)
	}
	firstSnapshot := mustSnapshot(t, first)
	secondSnapshot := mustSnapshot(t, second)
	if !strings.HasPrefix(firstSnapshot.ID, "goal_") || firstSnapshot.ID == secondSnapshot.ID {
		t.Fatalf("generated goal IDs = %q, %q", firstSnapshot.ID, secondSnapshot.ID)
	}
}

func TestContinuationPermitExactBudgetBoundaryAndIdempotency(t *testing.T) {
	runtime, _ := testRuntime(t, BudgetLimits{MaxContinuationTurns: 1})
	if decision, err := runtime.CanAutoContinue(context.Background()); err != nil || decision.Reason != ContinuationNoTurnReceipt {
		t.Fatalf("initial continuation decision = %#v, %v", decision, err)
	}
	if err := runtime.RecordTurn(context.Background(), productiveTurn("initial", 10)); err != nil {
		t.Fatal(err)
	}
	if decision, _ := runtime.CanAutoContinue(context.Background()); !decision.Allowed {
		t.Fatalf("productive turn did not enable continuation: %#v", decision)
	}

	permit, err := runtime.BeginContinuation(context.Background(), "continuation-1")
	if err != nil {
		t.Fatal(err)
	}
	if permit.Ordinal != 1 {
		t.Fatalf("permit ordinal = %d", permit.Ordinal)
	}
	// Consuming the final unit marks the budget exhausted, but the in-flight
	// permit is the higher-priority safety fact.
	if decision, _ := runtime.CanAutoContinue(context.Background()); decision.Reason != ContinuationTurnPending {
		t.Fatalf("exact-boundary pending decision = %#v, want turn_pending", decision)
	}
	if _, err := runtime.BeginContinuation(context.Background(), "continuation-1"); !errors.Is(err, ErrTurnPending) {
		t.Fatalf("duplicate begin did not fail closed: %v", err)
	}
	if _, err := runtime.BeginContinuation(context.Background(), "other"); !errors.Is(err, ErrTurnPending) {
		t.Fatalf("second pending permit error = %v", err)
	}
	if err := runtime.RecordTurn(context.Background(), productiveTurn("wrong", 1)); !errors.Is(err, ErrTurnPending) {
		t.Fatalf("mismatched receipt error = %v", err)
	}
	continuation := productiveTurn("continuation-1", 20)
	if err := runtime.RecordTurn(context.Background(), continuation); err != nil {
		t.Fatal(err)
	}
	if err := runtime.RecordTurn(context.Background(), continuation); err != nil {
		t.Fatalf("duplicate receipt was not idempotent: %v", err)
	}
	snapshot := mustSnapshot(t, runtime)
	if snapshot.State != StateExhausted || snapshot.Completion != nil || snapshot.PendingContinuation != nil {
		t.Fatalf("settled boundary snapshot = %#v", snapshot)
	}
	if snapshot.Usage.ContinuationTurns != 1 || snapshot.Usage.EvalTokens != 30 {
		t.Fatalf("usage = %#v", snapshot.Usage)
	}
	if decision, _ := runtime.CanAutoContinue(context.Background()); decision.Reason != ContinuationBudget {
		t.Fatalf("post-settlement continuation decision = %#v", decision)
	}
}

func TestRecoverPendingContinuationNeverRedispatchesOrRefunds(t *testing.T) {
	t.Run("restored permit cannot be redispatched", func(t *testing.T) {
		runtime, clock := testRuntime(t, BudgetLimits{MaxContinuationTurns: 2})
		if err := runtime.RecordTurn(context.Background(), productiveTurn("initial", 5)); err != nil {
			t.Fatal(err)
		}
		if _, err := runtime.BeginContinuation(context.Background(), "orphaned"); err != nil {
			t.Fatal(err)
		}
		restored, err := Restore(mustSnapshot(t, runtime), WithClock(clock))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := restored.BeginContinuation(context.Background(), "orphaned"); !errors.Is(err, ErrTurnPending) {
			t.Fatalf("restored permit was eligible for redispatch: %v", err)
		}
		if err := restored.RecoverPendingContinuation(context.Background(), PendingRecovery{
			TurnID: "orphaned", Kind: PendingCancelledBeforeDispatch,
			Reason: "durable dispatch ledger has no requested event", Evidence: "checked through cursor 17",
		}); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("proven pre-dispatch cancellation pauses", func(t *testing.T) {
		runtime, _ := testRuntime(t, BudgetLimits{MaxContinuationTurns: 2})
		if err := runtime.RecordTurn(context.Background(), productiveTurn("initial", 5)); err != nil {
			t.Fatal(err)
		}
		if _, err := runtime.BeginContinuation(context.Background(), "cancelled"); err != nil {
			t.Fatal(err)
		}
		recovery := PendingRecovery{
			TurnID: "cancelled", Kind: PendingCancelledBeforeDispatch,
			Reason: "host cancelled before MCP dispatch", Evidence: "dispatch marker was never written",
		}
		withoutEvidence := recovery
		withoutEvidence.Evidence = ""
		if err := runtime.RecoverPendingContinuation(context.Background(), withoutEvidence); !errors.Is(err, ErrInvalid) {
			t.Fatalf("missing evidence error = %v", err)
		}
		if err := runtime.RecoverPendingContinuation(context.Background(), recovery); err != nil {
			t.Fatal(err)
		}
		if err := runtime.RecoverPendingContinuation(context.Background(), recovery); err != nil {
			t.Fatalf("duplicate recovery was not idempotent: %v", err)
		}
		snapshot := mustSnapshot(t, runtime)
		if snapshot.State != StatePaused || snapshot.PendingContinuation != nil || snapshot.Usage.ContinuationTurns != 1 {
			t.Fatalf("pre-dispatch recovery snapshot = %#v", snapshot)
		}
		if snapshot.LastPendingRecovery == nil || snapshot.LastPendingRecovery.Recovery.Evidence != recovery.Evidence {
			t.Fatalf("recovery evidence was not retained: %#v", snapshot.LastPendingRecovery)
		}
		if err := runtime.Resume(context.Background(), "host explicitly resumed"); err != nil {
			t.Fatal(err)
		}
		if decision, _ := runtime.CanAutoContinue(context.Background()); !decision.Allowed {
			t.Fatalf("explicitly resumed proven cancellation should be eligible: %#v", decision)
		}
	})

	t.Run("uncertain dispatch blocks", func(t *testing.T) {
		runtime, _ := testRuntime(t, BudgetLimits{MaxContinuationTurns: 3})
		if err := runtime.RecordTurn(context.Background(), productiveTurn("initial", 5)); err != nil {
			t.Fatal(err)
		}
		if _, err := runtime.BeginContinuation(context.Background(), "uncertain"); err != nil {
			t.Fatal(err)
		}
		recovery := PendingRecovery{
			TurnID: "uncertain", Kind: PendingOutcomeUnknown, Reason: "connection closed after possible dispatch",
			Evidence: "no downstream terminal receipt", OutcomeRef: "exec_123",
		}
		if err := runtime.RecoverPendingContinuation(context.Background(), recovery); err != nil {
			t.Fatal(err)
		}
		snapshot := mustSnapshot(t, runtime)
		if snapshot.State != StateBlocked || snapshot.Blocker == nil || snapshot.Blocker.Reference != "exec_123" || snapshot.Usage.ContinuationTurns != 1 {
			t.Fatalf("unknown recovery snapshot = %#v", snapshot)
		}
		if decision, _ := runtime.CanAutoContinue(context.Background()); decision.Reason != ContinuationOutcomeUnknown {
			t.Fatalf("unknown outcome decision = %#v", decision)
		}
		if err := runtime.Complete(context.Background(), completionRequest()); !errors.Is(err, ErrOutcomeUnknown) {
			t.Fatalf("completion with unknown outcome error = %v", err)
		}
		if err := runtime.ResolveBlock(context.Background(), BlockResolution{Reference: "exec_123", Reason: "checked workspace"}); !errors.Is(err, ErrOutcomeUnknown) {
			t.Fatalf("unreconciled resolution error = %v", err)
		}
		resolution := BlockResolution{
			Reference: "exec_123", Reason: "workspace inspected", Reconciled: true,
			Evidence: "execution ledger proves the write never started",
		}
		if err := runtime.ResolveBlock(context.Background(), resolution); err != nil {
			t.Fatal(err)
		}
		snapshot = mustSnapshot(t, runtime)
		if snapshot.State != StatePaused || snapshot.LastBlockResolution == nil || snapshot.LastBlockResolution.Resolution.Evidence != resolution.Evidence {
			t.Fatalf("resolved snapshot = %#v", snapshot)
		}
	})
}

func TestUnproductiveTurnRequiresNewHostDirectedProgress(t *testing.T) {
	runtime, _ := testRuntime(t, BudgetLimits{})
	unproductive := TurnReport{TurnID: "stalled", EvalTokens: 12, Summary: "repeated the same hypothesis"}
	if err := runtime.RecordTurn(context.Background(), unproductive); err != nil {
		t.Fatal(err)
	}
	if decision, _ := runtime.CanAutoContinue(context.Background()); decision.Reason != ContinuationUnproductive {
		t.Fatalf("unproductive decision = %#v", decision)
	}
	if _, err := runtime.BeginContinuation(context.Background(), "must-not-run"); !errors.Is(err, ErrAutoContinuationDenied) {
		t.Fatalf("unproductive continuation error = %v", err)
	}
	if err := runtime.Resume(context.Background(), "user supplied new direction"); err != nil {
		t.Fatal(err)
	}
	if decision, _ := runtime.CanAutoContinue(context.Background()); decision.Reason != ContinuationUnproductive {
		t.Fatalf("resume incorrectly converted stale output into progress: %#v", decision)
	}
	if err := runtime.RecordTurn(context.Background(), productiveTurn("user-directed", 8)); err != nil {
		t.Fatal(err)
	}
	if decision, _ := runtime.CanAutoContinue(context.Background()); !decision.Allowed {
		t.Fatalf("new productive turn did not restore eligibility: %#v", decision)
	}
}

func TestManualTransitionMatrixAndGenericBlock(t *testing.T) {
	runtime, _ := testRuntime(t, BudgetLimits{})
	if err := runtime.Pause(context.Background(), "user requested a review break"); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Pause(context.Background(), "again"); !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("second pause error = %v", err)
	}
	if err := runtime.Resume(context.Background(), "user returned"); err != nil {
		t.Fatal(err)
	}
	blocker := Blocker{Kind: BlockDependency, Reference: "ollama", Reason: "required model is unavailable"}
	if err := runtime.Block(context.Background(), blocker); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Block(context.Background(), blocker); err != nil {
		t.Fatalf("identical blocker was not idempotent: %v", err)
	}
	conflict := blocker
	conflict.Reference = "other"
	if err := runtime.Block(context.Background(), conflict); !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("conflicting blocker error = %v", err)
	}
	if decision, _ := runtime.CanAutoContinue(context.Background()); decision.Reason != ContinuationBlocked {
		t.Fatalf("generic blocker decision = %#v", decision)
	}
	if err := runtime.Complete(context.Background(), completionRequest()); !errors.Is(err, ErrBlocked) {
		t.Fatalf("blocked completion error = %v", err)
	}
	if err := runtime.ResolveBlock(context.Background(), BlockResolution{Reference: "other", Reason: "wrong"}); !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("mismatched block resolution error = %v", err)
	}
	if err := runtime.ResolveBlock(context.Background(), BlockResolution{Reference: "ollama", Reason: "model installed"}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Resume(context.Background(), "dependency resolved"); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Drop(context.Background(), "user no longer wants this goal"); err != nil {
		t.Fatal(err)
	}
	if snapshot := mustSnapshot(t, runtime); snapshot.State != StateDropped || snapshot.StateReason == "" {
		t.Fatalf("dropped snapshot = %#v", snapshot)
	}
	for name, operation := range map[string]func() error{
		"pause":  func() error { return runtime.Pause(context.Background(), "late") },
		"resume": func() error { return runtime.Resume(context.Background(), "late") },
		"drop":   func() error { return runtime.Drop(context.Background(), "late") },
		"budget": func() error { return runtime.AmendBudget(context.Background(), BudgetLimits{}, "late") },
		"cortex": func() error {
			return runtime.AttachCortex(context.Background(), CortexCorrelation{TaskID: "task", Actor: "actor"})
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := operation(); !errors.Is(err, ErrTerminal) {
				t.Fatalf("terminal operation error = %v", err)
			}
		})
	}
}

func TestOutcomeUnknownTurnBlocksImmediately(t *testing.T) {
	runtime, _ := testRuntime(t, BudgetLimits{MaxEvalTokens: 100})
	invalid := productiveTurn("unknown", 12)
	invalid.OutcomeUnknown = true
	invalid.OutcomeRef = "exec_1"
	if err := runtime.RecordTurn(context.Background(), invalid); !errors.Is(err, ErrInvalid) {
		t.Fatalf("productive unknown outcome error = %v", err)
	}
	report := TurnReport{
		TurnID: "unknown", EvalTokens: 12, Summary: "write may have started",
		OutcomeUnknown: true, OutcomeRef: "exec_1",
	}
	if err := runtime.RecordTurn(context.Background(), report); err != nil {
		t.Fatal(err)
	}
	snapshot := mustSnapshot(t, runtime)
	if snapshot.State != StateBlocked || snapshot.Blocker == nil || snapshot.Blocker.Kind != BlockOutcomeUnknown || snapshot.Usage.EvalTokens != 12 {
		t.Fatalf("outcome-unknown turn snapshot = %#v", snapshot)
	}
	if err := runtime.Resume(context.Background(), "unsafe"); !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("blocked resume error = %v", err)
	}
}

func TestBudgetExhaustionNeverCompletesGoal(t *testing.T) {
	runtime, _ := testRuntime(t, BudgetLimits{MaxEvalTokens: 100})
	if err := runtime.RecordTurn(context.Background(), productiveTurn("token-limit", 100)); err != nil {
		t.Fatal(err)
	}
	snapshot := mustSnapshot(t, runtime)
	if snapshot.State != StateExhausted || snapshot.Completion != nil {
		t.Fatalf("budget exhaustion implied completion: %#v", snapshot)
	}
	incomplete := completionRequest()
	incomplete.Results = incomplete.Results[:1]
	if err := runtime.Complete(context.Background(), incomplete); !errors.Is(err, ErrAcceptanceIncomplete) {
		t.Fatalf("incomplete acceptance error = %v", err)
	}
	if err := runtime.Complete(context.Background(), completionRequest()); err != nil {
		t.Fatal(err)
	}
	snapshot = mustSnapshot(t, runtime)
	if snapshot.State != StateCompleted || snapshot.Completion == nil || len(snapshot.Completion.Results) != 2 {
		t.Fatalf("explicit completion snapshot = %#v", snapshot)
	}
	if err := runtime.Drop(context.Background(), "too late"); !errors.Is(err, ErrTerminal) {
		t.Fatalf("terminal transition error = %v", err)
	}
}

func TestWallBudgetRestoreAndExplicitReplenishment(t *testing.T) {
	runtime, clock := testRuntime(t, BudgetLimits{MaxWallTime: time.Hour})
	saved := mustSnapshot(t, runtime)
	clock.Advance(time.Hour)
	restored, err := Restore(saved, WithClock(clock))
	if err != nil {
		t.Fatal(err)
	}
	snapshot := mustSnapshot(t, restored)
	if snapshot.State != StateExhausted || len(snapshot.ExhaustedBy) != 1 || snapshot.ExhaustedBy[0] != BudgetWallTime {
		t.Fatalf("wall-time restore snapshot = %#v", snapshot)
	}
	if err := restored.Resume(context.Background(), "without budget"); !errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("resume before amendment error = %v", err)
	}
	if err := restored.AmendBudget(context.Background(), BudgetLimits{MaxWallTime: 2 * time.Hour}, "user extended deadline"); err != nil {
		t.Fatal(err)
	}
	snapshot = mustSnapshot(t, restored)
	if snapshot.State != StateExhausted || len(snapshot.ExhaustedBy) != 0 {
		t.Fatalf("budget amendment silently resumed goal: %#v", snapshot)
	}
	if err := restored.Resume(context.Background(), "budget extension approved"); err != nil {
		t.Fatal(err)
	}
	if snapshot = mustSnapshot(t, restored); snapshot.State != StateActive {
		t.Fatalf("explicit resume state = %s", snapshot.State)
	}
}

func TestCortexCorrelationIsMonotonicAndImmutable(t *testing.T) {
	runtime, _ := testRuntime(t, BudgetLimits{})
	first := CortexCorrelation{TaskID: "task_1", Revision: 3, Actor: "local-agent/session-42"}
	if err := runtime.AttachCortex(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if err := runtime.AttachCortex(context.Background(), first); err != nil {
		t.Fatalf("idempotent correlation failed: %v", err)
	}
	if err := runtime.AttachCortex(context.Background(), CortexCorrelation{TaskID: first.TaskID, Revision: 2, Actor: first.Actor}); !errors.Is(err, ErrCorrelationConflict) {
		t.Fatalf("revision regression error = %v", err)
	}
	if err := runtime.AttachCortex(context.Background(), CortexCorrelation{TaskID: "task_2", Revision: 4, Actor: first.Actor}); !errors.Is(err, ErrCorrelationConflict) {
		t.Fatalf("task replacement error = %v", err)
	}
	updated := first
	updated.Revision = 4
	if err := runtime.AttachCortex(context.Background(), updated); err != nil {
		t.Fatal(err)
	}
	if got := mustSnapshot(t, runtime).Cortex; got != updated {
		t.Fatalf("cortex correlation = %#v", got)
	}
}

func TestSnapshotRoundTripIsolationAndForgeryRejection(t *testing.T) {
	runtime, clock := testRuntime(t, BudgetLimits{MaxContinuationTurns: 3})
	if err := runtime.RecordTurn(context.Background(), productiveTurn("initial", 2)); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.BeginContinuation(context.Background(), "unknown"); err != nil {
		t.Fatal(err)
	}
	if err := runtime.RecoverPendingContinuation(context.Background(), PendingRecovery{
		TurnID: "unknown", Kind: PendingOutcomeUnknown, Reason: "lost receipt",
		Evidence: "transport closed", OutcomeRef: "exec_9",
	}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.ResolveBlock(context.Background(), BlockResolution{
		Reference: "exec_9", Reason: "inspected ledger", Reconciled: true, Evidence: "no dispatch event exists",
	}); err != nil {
		t.Fatal(err)
	}

	snapshot := mustSnapshot(t, runtime)
	if snapshot.LastBlockResolution == nil || snapshot.LastPendingRecovery == nil {
		t.Fatalf("missing recovery records: %#v", snapshot)
	}
	snapshot.LastBlockResolution.Resolution.Evidence = "forged"
	snapshot.LastPendingRecovery.Recovery.Evidence = "forged"
	again := mustSnapshot(t, runtime)
	if again.LastBlockResolution.Resolution.Evidence == "forged" || again.LastPendingRecovery.Recovery.Evidence == "forged" {
		t.Fatal("returned recovery records alias runtime state")
	}

	encoded, err := json.Marshal(again)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Snapshot
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(decoded, WithClock(clock)); err != nil {
		t.Fatalf("valid JSON round trip failed: %v", err)
	}

	forgedReference := cloneSnapshot(again)
	forgedReference.LastBlockResolution.Resolution.Reference = "other"
	if _, err := Restore(forgedReference, WithClock(clock)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("forged resolution reference error = %v", err)
	}
	forgedEvidence := cloneSnapshot(again)
	forgedEvidence.LastBlockResolution.Resolution.Evidence = ""
	if _, err := Restore(forgedEvidence, WithClock(clock)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing reconciliation evidence error = %v", err)
	}
	forgedTime := cloneSnapshot(again)
	forgedTime.LastBlockResolution.ResolvedAt = forgedTime.UpdatedAt.Add(time.Second)
	if _, err := Restore(forgedTime, WithClock(clock)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("forged resolution time error = %v", err)
	}
}

func TestCancelledContextDoesNotMutateRuntime(t *testing.T) {
	runtime, _ := testRuntime(t, BudgetLimits{})
	before := mustSnapshot(t, runtime)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runtime.RecordTurn(ctx, productiveTurn("cancelled", 100)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled context error = %v", err)
	}
	after := mustSnapshot(t, runtime)
	if before.Usage != after.Usage || before.State != after.State || after.LastTurn != nil {
		t.Fatalf("cancelled operation mutated runtime: before=%#v after=%#v", before, after)
	}
}

func TestNilContextFailsClosed(t *testing.T) {
	runtime, _ := testRuntime(t, BudgetLimits{})
	if _, err := runtime.Snapshot(nil); !errors.Is(err, ErrInvalid) { //nolint:staticcheck // intentional fail-closed API boundary test
		t.Fatalf("nil Snapshot context error = %v", err)
	}
	if _, err := runtime.CanAutoContinue(nil); !errors.Is(err, ErrInvalid) { //nolint:staticcheck // intentional fail-closed API boundary test
		t.Fatalf("nil continuation context error = %v", err)
	}
	if err := runtime.Pause(nil, "pause"); !errors.Is(err, ErrInvalid) { //nolint:staticcheck // intentional fail-closed API boundary test
		t.Fatalf("nil Pause context error = %v", err)
	}
}

func TestConcurrentReadersAndCorrelationUpdates(t *testing.T) {
	runtime, _ := testRuntime(t, BudgetLimits{})
	if err := runtime.RecordTurn(context.Background(), productiveTurn("initial", 1)); err != nil {
		t.Fatal(err)
	}
	correlation := CortexCorrelation{TaskID: "task_1", Actor: "actor_1"}
	var wait sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for revision := int64(0); revision < 20; revision++ {
				_, _ = runtime.Snapshot(context.Background())
				_, _ = runtime.CanAutoContinue(context.Background())
				candidate := correlation
				candidate.Revision = revision
				_ = runtime.AttachCortex(context.Background(), candidate)
			}
		}()
	}
	wait.Wait()
	snapshot := mustSnapshot(t, runtime)
	if snapshot.Cortex.TaskID != correlation.TaskID || snapshot.Cortex.Actor != correlation.Actor || snapshot.Cortex.Revision < 0 {
		t.Fatalf("concurrent correlation = %#v", snapshot.Cortex)
	}
}
