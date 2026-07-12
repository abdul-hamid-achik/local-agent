package ui

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/local-agent/internal/controlplane"
	"github.com/abdul-hamid-achik/local-agent/internal/db"
	"github.com/abdul-hamid-achik/local-agent/internal/goal"
	"github.com/abdul-hamid-achik/local-agent/internal/goaladvisor"
)

func TestCortexDecisionControlItemSurvivesAndResolvesAppendOnly(t *testing.T) {
	workspace := t.TempDir()
	store, err := db.OpenPath(filepath.Join(t.TempDir(), "local-agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	m := newGoalRuntimeTestModel(t, &goalCountingClient{})
	m.agent.SetWorkDir(workspace)
	workspace, err = canonicalWorkspaceID(m.agent.WorkDir())
	if err != nil {
		t.Fatal(err)
	}
	m.SetSessionStore(store)
	session, err := store.CreateSession(context.Background(), db.CreateSessionParams{
		Title: "durable decision", Model: "test", Mode: "AUTO", WorkspaceID: workspace,
	})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := store.AcquireExecutionSessionLease(context.Background(), session.ID, workspace)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Close() })
	m.sessionID = session.ID
	m.executionLease = lease
	m.goalRuntime = newUIGoalRuntime(t, session.ID, goal.BudgetLimits{MaxContinuationTurns: 3})
	if err := m.goalRuntime.AttachCortex(context.Background(), goal.CortexCorrelation{
		TaskID: "task_decision", Revision: 5, Actor: goalActor,
	}); err != nil {
		t.Fatal(err)
	}
	snapshot := snapshotUIGoal(t, m.goalRuntime)
	decision := goaladvisor.Advice{
		TaskID: "task_decision", Revision: 5, Phase: "needs_human_decision",
		Summary: "Choose the forward-only migration", PendingDecision: true,
	}
	if err := m.recordCortexDecisionControlItem(snapshot, decision); err != nil {
		t.Fatal(err)
	}
	if err := m.recordCortexDecisionControlItem(snapshot, decision); err != nil {
		t.Fatalf("exact decision replay failed: %v", err)
	}
	pending, err := store.ListControlStates(context.Background(), controlplane.Query{
		SessionID: session.ID, WorkspaceID: workspace, GoalID: snapshot.ID,
		PendingOnly: true, Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].Item.Kind != controlplane.KindCortexDecision || pending[0].Resolution != nil {
		t.Fatalf("pending decision = %#v", pending)
	}

	changed := decision
	changed.Summary = "A different question at the same immutable revision"
	if err := m.recordCortexDecisionControlItem(snapshot, changed); err == nil {
		t.Fatal("conflicting decision replay was accepted")
	}
	resolvedAdvice := goaladvisor.Advice{
		TaskID: "task_decision", Revision: 6, Phase: "working",
		Summary: "Forward-only migration selected",
	}
	if err := m.resolveCortexDecisionControlItems(snapshot, resolvedAdvice); err != nil {
		t.Fatal(err)
	}
	if err := m.resolveCortexDecisionControlItems(snapshot, resolvedAdvice); err != nil {
		t.Fatalf("exact resolution replay failed: %v", err)
	}
	pending, err = store.ListControlStates(context.Background(), controlplane.Query{
		SessionID: session.ID, WorkspaceID: workspace, PendingOnly: true, Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("resolved decision remains pending: %#v", pending)
	}
	all, err := store.ListControlStates(context.Background(), controlplane.Query{
		SessionID: session.ID, WorkspaceID: workspace, Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Resolution == nil || all[0].Resolution.Outcome != controlplane.OutcomeAnswered {
		t.Fatalf("append-only resolved decision = %#v", all)
	}
	var resolutionEvidence map[string]any
	if err := json.Unmarshal([]byte(all[0].Resolution.EvidenceJSON), &resolutionEvidence); err != nil {
		t.Fatal(err)
	}
	if resolutionEvidence["item_id"] != all[0].Item.ItemID || resolutionEvidence["decision_payload_sha256"] != all[0].Item.PayloadSHA256 {
		t.Fatalf("resolution evidence is not bound to its immutable item: %#v", resolutionEvidence)
	}

	payload, digest, err := controlplane.MarshalDocument(map[string]any{"task_id": "task_other"})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = store.AppendControlItem(context.Background(), lease, controlplane.Item{
		ItemID: "ctrl_other_task", IdempotencyKey: "ctrlidem_other_task",
		Kind: controlplane.KindCortexDecision,
		Identity: controlplane.Identity{
			SessionID: session.ID, WorkspaceID: workspace, GoalID: snapshot.ID,
		},
		ExternalID: "task_other", Summary: "Unrelated task decision",
		PayloadJSON: payload, PayloadSHA256: digest,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.resolveCortexDecisionControlItems(snapshot, resolvedAdvice); err == nil || !strings.Contains(err.Error(), "belongs to task") {
		t.Fatalf("cross-task resolution error = %v", err)
	}
	pending, err = store.ListControlStates(context.Background(), controlplane.Query{
		SessionID: session.ID, WorkspaceID: workspace, PendingOnly: true, Limit: 10,
	})
	if err != nil || len(pending) != 1 || pending[0].Item.ItemID != "ctrl_other_task" {
		t.Fatalf("cross-task decision was not preserved: %#v, %v", pending, err)
	}
}

func TestCortexDecisionControlPlaneRequiresProductionLease(t *testing.T) {
	workspace := t.TempDir()
	store, err := db.OpenPath(filepath.Join(t.TempDir(), "local-agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	m := newGoalRuntimeTestModel(t, &goalCountingClient{})
	m.agent.SetWorkDir(workspace)
	workspace, err = canonicalWorkspaceID(m.agent.WorkDir())
	if err != nil {
		t.Fatal(err)
	}
	m.SetSessionStore(store)
	session, err := store.CreateSession(context.Background(), db.CreateSessionParams{
		Title: "missing lease", Model: "test", Mode: "AUTO", WorkspaceID: workspace,
	})
	if err != nil {
		t.Fatal(err)
	}
	m.goalRuntime = newUIGoalRuntime(t, session.ID, goal.BudgetLimits{})
	snapshot := snapshotUIGoal(t, m.goalRuntime)
	if err := m.recordCortexDecisionControlItem(snapshot, goaladvisor.Advice{
		TaskID: "task_missing_lease", Revision: 1, PendingDecision: true,
	}); err == nil {
		t.Fatal("decision append without session lease was accepted")
	}
}
