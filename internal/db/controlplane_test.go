package db

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/local-agent/internal/controlplane"
	"github.com/abdul-hamid-achik/local-agent/internal/execution"
)

var controlTestTime = time.Date(2026, time.July, 12, 9, 30, 0, 456_000_000, time.UTC)

func controlTestItem(t *testing.T, sessionID int64, workspaceID, suffix string, kind controlplane.Kind) controlplane.Item {
	t.Helper()
	payload, digest, err := controlplane.MarshalDocument(map[string]any{
		"prompt": "decision " + suffix,
		"safe":   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return controlplane.Item{
		ItemID: "ctrl_" + suffix, IdempotencyKey: "ctrlidem_" + suffix,
		Kind: kind,
		Identity: controlplane.Identity{
			SessionID: sessionID, WorkspaceID: workspaceID,
			GoalID: "goal_" + suffix, TurnID: "turn_" + suffix,
		},
		ExternalID: "external_" + suffix, Summary: "Resolve " + suffix,
		PayloadJSON: payload, PayloadSHA256: digest,
		CreatedAt: controlTestTime,
	}
}

func controlTestResolution(t *testing.T, item controlplane.Item, suffix string, outcome controlplane.Outcome) controlplane.Resolution {
	t.Helper()
	evidence, digest, err := controlplane.MarshalDocument(map[string]any{
		"receipt":  "operator evidence " + suffix,
		"verified": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return controlplane.Resolution{
		ResolutionID: "ctrlres_" + suffix, IdempotencyKey: "ctrlresidem_" + suffix,
		ItemID: item.ItemID, SessionID: item.Identity.SessionID,
		WorkspaceID: item.Identity.WorkspaceID, Outcome: outcome,
		EvidenceJSON: evidence, EvidenceSHA256: digest,
		ResolvedBy: "local-user", Detail: "reviewed durable evidence",
		ResolvedAt: controlTestTime.Add(time.Minute),
	}
}

func acquireControlTestLease(t *testing.T, store *Store, session Session) *ExecutionSessionLease {
	t.Helper()
	lease, err := store.AcquireExecutionSessionLease(context.Background(), session.ID, session.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := lease.Close(); err != nil {
			t.Errorf("close control test lease: %v", err)
		}
	})
	return lease
}

func TestControlItemAppendReplayConflictsAndLeaseOwnership(t *testing.T) {
	store := testStore(t)
	workspaceID := "/workspace/control-item"
	session := createExecutionTestSession(t, store, workspaceID)
	lease := acquireControlTestLease(t, store, session)
	item := controlTestItem(t, session.ID, workspaceID, "decision", controlplane.KindCortexDecision)
	item.CreatedAt = time.Time{}

	stored, inserted, err := store.AppendControlItem(context.Background(), lease, item)
	if err != nil || !inserted {
		t.Fatalf("append item inserted=%v error=%v", inserted, err)
	}
	if stored.ID <= 0 || stored.RecordedAt.IsZero() || stored.PayloadSHA256 != item.PayloadSHA256 {
		t.Fatalf("incomplete stored item: %#v", stored)
	}
	replayed, inserted, err := store.AppendControlItem(context.Background(), lease, item)
	if err != nil || inserted || replayed.ID != stored.ID {
		t.Fatalf("exact replay = %#v inserted=%v error=%v", replayed, inserted, err)
	}
	changed := item
	changed.Summary = "different immutable summary"
	if _, _, err := store.AppendControlItem(context.Background(), lease, changed); !errors.Is(err, ErrControlItemConflict) {
		t.Fatalf("changed replay error = %v", err)
	}

	second := controlTestItem(t, session.ID, workspaceID, "second", controlplane.KindDeferredApproval)
	if _, _, err := store.AppendControlItem(context.Background(), lease, second); err != nil {
		t.Fatal(err)
	}
	crossCollision := item
	crossCollision.IdempotencyKey = second.IdempotencyKey
	if _, _, err := store.AppendControlItem(context.Background(), lease, crossCollision); !errors.Is(err, ErrControlItemConflict) {
		t.Fatalf("split identity collision error = %v", err)
	}
	if _, _, err := store.AppendControlItem(context.Background(), nil, item); !errors.Is(err, ErrControlLeaseRequired) {
		t.Fatalf("missing lease error = %v", err)
	}

	other := createExecutionTestSession(t, store, workspaceID)
	wrongScope := item
	wrongScope.ItemID, wrongScope.IdempotencyKey = "ctrl_wrong", "ctrlidem_wrong"
	wrongScope.Identity.SessionID = other.ID
	if _, _, err := store.AppendControlItem(context.Background(), lease, wrongScope); !errors.Is(err, ErrControlLeaseScope) {
		t.Fatalf("wrong-session lease error = %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := store.AppendControlItem(cancelled, lease, item); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled append error = %v", err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.AppendControlItem(context.Background(), lease, item); !errors.Is(err, ErrControlLeaseRequired) {
		t.Fatalf("closed lease error = %v", err)
	}
}

func TestControlResolutionAndBoundedStateProjection(t *testing.T) {
	store := testStore(t)
	workspaceID := "/workspace/control-state"
	session := createExecutionTestSession(t, store, workspaceID)
	lease := acquireControlTestLease(t, store, session)

	decision := controlTestItem(t, session.ID, workspaceID, "decision", controlplane.KindCortexDecision)
	approval := controlTestItem(t, session.ID, workspaceID, "approval", controlplane.KindDeferredApproval)
	for _, item := range []controlplane.Item{decision, approval} {
		if _, inserted, err := store.AppendControlItem(context.Background(), lease, item); err != nil || !inserted {
			t.Fatalf("append %s inserted=%v error=%v", item.ItemID, inserted, err)
		}
	}
	resolution := controlTestResolution(t, approval, "approval", controlplane.OutcomeApproved)
	resolution.ResolvedAt = time.Time{}
	stored, inserted, err := store.ResolveControlItem(context.Background(), lease, resolution)
	if err != nil || !inserted {
		t.Fatalf("resolve inserted=%v error=%v", inserted, err)
	}
	if stored.ResolvedAt.IsZero() || stored.RecordedAt.IsZero() {
		t.Fatalf("store-assigned resolution times are missing: %#v", stored)
	}
	replayed, inserted, err := store.ResolveControlItem(context.Background(), lease, resolution)
	if err != nil || inserted || replayed.ID != stored.ID {
		t.Fatalf("resolution replay = %#v inserted=%v error=%v", replayed, inserted, err)
	}
	conflict := resolution
	conflict.Detail = "changed evidence interpretation"
	if _, _, err := store.ResolveControlItem(context.Background(), lease, conflict); !errors.Is(err, ErrControlResolutionConflict) {
		t.Fatalf("changed resolution error = %v", err)
	}
	invalidOutcome := controlTestResolution(t, decision, "invalid", controlplane.OutcomeApproved)
	if _, _, err := store.ResolveControlItem(context.Background(), lease, invalidOutcome); !errors.Is(err, ErrControlResolutionConflict) {
		t.Fatalf("kind-incompatible outcome error = %v", err)
	}

	state, err := store.GetControlState(context.Background(), session.ID, workspaceID, approval.ItemID)
	if err != nil || state.Pending() || state.Resolution.Outcome != controlplane.OutcomeApproved {
		t.Fatalf("resolved state = %#v error=%v", state, err)
	}
	pending, err := store.ListControlStates(context.Background(), controlplane.Query{
		SessionID: session.ID, WorkspaceID: workspaceID, PendingOnly: true, Limit: 10,
	})
	if err != nil || len(pending) != 1 || pending[0].Item.ItemID != decision.ItemID {
		t.Fatalf("pending states = %#v error=%v", pending, err)
	}
	filtered, err := store.ListControlStates(context.Background(), controlplane.Query{
		SessionID: session.ID, WorkspaceID: workspaceID,
		Kind: controlplane.KindDeferredApproval, GoalID: approval.Identity.GoalID,
		TurnID: approval.Identity.TurnID, Limit: 1,
	})
	if err != nil || len(filtered) != 1 || filtered[0].Pending() {
		t.Fatalf("filtered states = %#v error=%v", filtered, err)
	}
	if _, err := store.ListControlStates(context.Background(), controlplane.Query{
		SessionID: session.ID, WorkspaceID: workspaceID, Limit: controlplane.MaxListLimit + 1,
	}); err == nil {
		t.Fatal("unbounded list unexpectedly accepted")
	}
	if _, err := store.GetControlState(context.Background(), session.ID, "/workspace/other", approval.ItemID); !errors.Is(err, ErrExecutionWorkspaceMismatch) {
		t.Fatalf("cross-workspace state read error = %v", err)
	}
}

func TestExecutionReconciliationRequiresHazardAndNeverMutatesLedger(t *testing.T) {
	store := testStore(t)
	workspaceID := "/workspace/reconciliation"
	session := createExecutionTestSession(t, store, workspaceID)
	lease := acquireControlTestLease(t, store, session)

	requested := executionTestEvent(t, session.ID, workspaceID, "hazard", execution.Effectful)
	started := appendStartedExecutionFixture(t, store, requested)
	item := controlTestItem(t, session.ID, workspaceID, "reconcile", controlplane.KindExecutionReconciliation)
	item.Identity.ExecutionID = started.Identity.ExecutionID
	if _, inserted, err := store.AppendControlItem(context.Background(), lease, item); err != nil || !inserted {
		t.Fatalf("append reconciliation inserted=%v error=%v", inserted, err)
	}

	resolution := controlTestResolution(t, item, "reconcile", controlplane.OutcomeReconciled)
	if _, inserted, err := store.ResolveControlItem(context.Background(), lease, resolution); err != nil || !inserted {
		t.Fatalf("resolve reconciliation inserted=%v error=%v", inserted, err)
	}
	events, err := store.ListExecutionEvents(context.Background(), session.ID, workspaceID, started.Identity.ExecutionID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 || events[len(events)-1].Type != execution.EventStarted {
		t.Fatalf("control resolution mutated execution ledger: %#v", events)
	}

	notFound := controlTestItem(t, session.ID, workspaceID, "missing", controlplane.KindExecutionReconciliation)
	notFound.Identity.ExecutionID = "exec_missing"
	if _, _, err := store.AppendControlItem(context.Background(), lease, notFound); !errors.Is(err, ErrControlExecutionNotHazardous) {
		t.Fatalf("missing execution reconciliation error = %v", err)
	}
	completedRequest := executionTestEvent(t, session.ID, workspaceID, "complete", execution.Effectful)
	completed := appendCompletedExecutionFixture(t, store, completedRequest)
	terminal := controlTestItem(t, session.ID, workspaceID, "terminal", controlplane.KindExecutionReconciliation)
	terminal.Identity.ExecutionID = completed.Identity.ExecutionID
	if _, _, err := store.AppendControlItem(context.Background(), lease, terminal); !errors.Is(err, ErrControlExecutionNotHazardous) {
		t.Fatalf("completed execution reconciliation error = %v", err)
	}
}

func TestControlPlanePersistsAcrossRestartAndSQLGuardsAppendOnlyHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control-restart.db")
	first, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	workspaceID := "/workspace/control-restart"
	session := createExecutionTestSession(t, first, workspaceID)
	lease, err := first.AcquireExecutionSessionLease(context.Background(), session.ID, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	item := controlTestItem(t, session.ID, workspaceID, "restart", controlplane.KindDeferredApproval)
	if _, _, err := first.AppendControlItem(context.Background(), lease, item); err != nil {
		t.Fatal(err)
	}
	resolution := controlTestResolution(t, item, "restart", controlplane.OutcomeDenied)
	if _, _, err := first.ResolveControlItem(context.Background(), lease, resolution); err != nil {
		t.Fatal(err)
	}
	if _, err := first.db.Exec(`UPDATE control_items SET summary = 'tampered' WHERE item_id = ?`, item.ItemID); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("direct item update error = %v", err)
	}
	if _, err := first.db.Exec(`DELETE FROM control_resolutions WHERE item_id = ?`, item.ItemID); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("direct resolution delete error = %v", err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	restarted, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.Close() })
	restartedLease := acquireControlTestLease(t, restarted, session)
	replayed, inserted, err := restarted.AppendControlItem(context.Background(), restartedLease, item)
	if err != nil || inserted || replayed.ItemID != item.ItemID {
		t.Fatalf("restart replay = %#v inserted=%v error=%v", replayed, inserted, err)
	}
	state, err := restarted.GetControlState(context.Background(), session.ID, workspaceID, item.ItemID)
	if err != nil || state.Pending() || state.Resolution.Outcome != controlplane.OutcomeDenied {
		t.Fatalf("restored control state = %#v error=%v", state, err)
	}
	if err := restarted.DeleteSession(context.Background(), session.ID); err != nil {
		t.Fatalf("explicit session deletion did not cascade: %v", err)
	}
	var count int
	if err := restarted.db.QueryRow(`SELECT COUNT(*) FROM control_items WHERE session_id = ?`, session.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("control rows after session deletion count=%d error=%v", count, err)
	}
}

func TestControlPlaneSQLScopeAndOutcomeGuards(t *testing.T) {
	store := testStore(t)
	workspaceID := "/workspace/control-sql-guards"
	session := createExecutionTestSession(t, store, workspaceID)
	lease := acquireControlTestLease(t, store, session)
	item := controlTestItem(t, session.ID, workspaceID, "sql-guard", controlplane.KindCortexDecision)
	if _, _, err := store.AppendControlItem(context.Background(), lease, item); err != nil {
		t.Fatal(err)
	}

	if _, err := store.db.Exec(`
		INSERT INTO control_items (
			item_id, idempotency_key, session_id, workspace_id, kind,
			summary, payload_json, payload_sha256, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"ctrl_cross_scope", "ctrlidem_cross_scope", session.ID, "/workspace/other",
		controlplane.KindDeferredApproval, "cross-scope", item.PayloadJSON,
		item.PayloadSHA256, formatExecutionTime(controlTestTime),
	); err == nil || !strings.Contains(err.Error(), "workspace does not match session") {
		t.Fatalf("direct cross-scope item error = %v", err)
	}

	evidence, digest, err := controlplane.MarshalDocument(map[string]any{"operator": "approved"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`
		INSERT INTO control_resolutions (
			resolution_id, idempotency_key, item_id, session_id, workspace_id,
			outcome, evidence_json, evidence_sha256, resolved_by, resolved_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"ctrlres_bad_outcome", "ctrlresidem_bad_outcome", item.ItemID,
		session.ID, workspaceID, controlplane.OutcomeApproved,
		evidence, digest, "direct-sql", formatExecutionTime(controlTestTime),
	); err == nil || !strings.Contains(err.Error(), "outcome does not match item kind") {
		t.Fatalf("direct incompatible outcome error = %v", err)
	}
}
