package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/local-agent/internal/controlplane"
	"github.com/abdul-hamid-achik/local-agent/internal/db"
	"github.com/abdul-hamid-achik/local-agent/internal/execution"
	"github.com/abdul-hamid-achik/local-agent/internal/goal"
	"github.com/abdul-hamid-achik/local-agent/internal/reconciliation"
)

func openGoalCommandTestStore(t *testing.T) *db.Store {
	t.Helper()
	store, err := db.OpenPath(filepath.Join(t.TempDir(), "local-agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func createGoalSession(t *testing.T, store *db.Store, workspace, objective string) (db.Session, goal.Snapshot) {
	t.Helper()
	session, err := store.CreateSession(context.Background(), db.CreateSessionParams{
		Title: objective, Model: "test", Mode: "AUTO", WorkspaceID: workspace,
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := goal.New(goal.Spec{
		ID:        "goal_" + strings.ReplaceAll(objective, " ", "_"),
		SessionID: session.ID,
		Objective: objective,
		AcceptanceCriteria: []goal.AcceptanceCriterion{
			{ID: "criterion_1", Description: "the observer reports durable state"},
		},
		Budget: goal.BudgetLimits{MaxContinuationTurns: 3, MaxEvalTokens: 1_000},
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := runtime.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]any{"version": 1, "goal": snapshot})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSessionState(context.Background(), session.ID, string(payload)); err != nil {
		t.Fatal(err)
	}
	return session, snapshot
}

type goalRecoveryFixture struct {
	Session       db.Session
	Record        db.SessionStateRecord
	Snapshot      goal.Snapshot
	TurnID        string
	Group         db.ReconciliationGroup
	ExpectedGroup string
}

func createGoalRecoveryFixture(t *testing.T, store *db.Store, workspace string, withMember, ensureGroup bool) goalRecoveryFixture {
	t.Helper()
	session, err := store.CreateSession(context.Background(), db.CreateSessionParams{
		Title: "recover abandoned turn", Model: "test", Mode: "AUTO", WorkspaceID: workspace,
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := goal.New(goal.Spec{
		ID: fmt.Sprintf("goal_recover_%d", session.ID), SessionID: session.ID,
		Objective:          "Recover the abandoned turn without redispatch",
		AcceptanceCriteria: []goal.AcceptanceCriterion{{ID: "safe", Description: "No unknown effect is retried"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	turnID := fmt.Sprintf("turn_recover_%d", session.ID)
	if _, err := runtime.BeginTurn(context.Background(), turnID, goal.AdmissionInitial); err != nil {
		t.Fatal(err)
	}
	if withMember {
		argumentsSHA, err := execution.HashCanonicalArguments(map[string]any{"path": "recovery.txt"})
		if err != nil {
			t.Fatal(err)
		}
		identity := execution.Identity{
			SessionID: session.ID, WorkspaceID: workspace, RunID: "run_cli_recovery", TurnID: turnID,
			ExecutionID: fmt.Sprintf("exec_recover_%d", session.ID), IdempotencyKey: fmt.Sprintf("idem_recover_%d", session.ID),
			ProviderCallID: "provider_cli_recovery", CanonicalCallID: "call_cli_recovery",
			ToolName: "write", Iteration: 1, Ordinal: 1, Kind: execution.KindBuiltin, EffectClass: execution.Effectful,
		}
		base := execution.Event{
			Identity: identity, Type: execution.EventRequested, Approval: execution.ApprovalNotApplicable,
			ArgumentsSHA256: argumentsSHA, OccurredAt: time.Date(2026, time.July, 12, 17, 0, 0, 0, time.UTC),
		}
		for _, event := range []execution.Event{
			base,
			func() execution.Event {
				value := base
				value.Type = execution.EventApproved
				value.Approval = execution.ApprovalEmbedding
				value.OccurredAt = value.OccurredAt.Add(time.Second)
				return value
			}(),
			func() execution.Event {
				value := base
				value.Type = execution.EventStarted
				value.OccurredAt = value.OccurredAt.Add(2 * time.Second)
				return value
			}(),
			func() execution.Event {
				value := base
				value.Type = execution.EventOutcomeUnknown
				value.Detail = "provider transport closed after dispatch"
				value.OccurredAt = value.OccurredAt.Add(3 * time.Second)
				return value
			}(),
		} {
			if _, _, err := store.AppendExecutionEvent(context.Background(), event); err != nil {
				t.Fatal(err)
			}
		}
		if err := runtime.RecordTurn(context.Background(), goal.TurnReport{
			TurnID: turnID, Summary: "effect outcome is unknown", OutcomeUnknown: true, OutcomeRef: identity.ExecutionID,
		}); err != nil {
			t.Fatal(err)
		}
	} else if err := runtime.RecoverPendingContinuation(context.Background(), goal.PendingRecovery{
		TurnID: turnID, Kind: goal.PendingOutcomeUnknown,
		Reason:   "provider response was lost before a tool lifecycle appeared",
		Evidence: "the admitted turn has no settled provider receipt", OutcomeRef: turnID,
	}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := runtime.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]any{"version": 2, "execution_cursor": 0, "goal": snapshot})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSessionState(context.Background(), session.ID, string(payload)); err != nil {
		t.Fatal(err)
	}
	record, err := store.GetSessionStateRecord(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	identitySHA := controlplane.HashText(fmt.Sprintf("reconciliation-group\x00%d\x00%s\x00%s", session.ID, snapshot.ID, turnID))
	fixture := goalRecoveryFixture{
		Session: session, Record: record, Snapshot: snapshot, TurnID: turnID,
		ExpectedGroup: "recongrp_" + identitySHA[:32],
	}
	if ensureGroup {
		lease, err := store.AcquireExecutionSessionLease(context.Background(), session.ID, workspace)
		if err != nil {
			t.Fatal(err)
		}
		fixture.Group, _, err = store.EnsureReconciliationGroup(context.Background(), lease, db.EnsureReconciliationGroupRequest{
			SessionID: session.ID, WorkspaceID: workspace, ExpectedSessionRevision: record.Revision,
		})
		closeErr := lease.Close()
		if err != nil || closeErr != nil {
			t.Fatalf("ensure recovery group error=%v close=%v", err, closeErr)
		}
	}
	return fixture
}

func goalRecoveryApplyArgs(sessionID int64, itemID, observation, summary string) []string {
	return []string{
		formatSessionID(sessionID), "--apply", "--item", itemID,
		"--observation", observation, "--source", string(reconciliation.SourceOperatorObservation),
		"--reference", "operator-check:cli", "--summary", summary,
		"--observed-at", "2026-07-12T17:30:00Z", "--json",
	}
}

func TestListGoalSummariesFiltersAndValidatesDurableSessions(t *testing.T) {
	store := openGoalCommandTestStore(t)
	workspace := "/workspace/a"
	first, firstGoal := createGoalSession(t, store, workspace, "Polish the durable goal observer")
	createGoalSession(t, store, "/workspace/b", "Other workspace")
	withoutGoal, err := store.CreateSession(context.Background(), db.CreateSessionParams{
		Title: "chat", Model: "test", Mode: "NORMAL", WorkspaceID: workspace,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSessionState(context.Background(), withoutGoal.ID, `{"version":1}`); err != nil {
		t.Fatal(err)
	}
	corrupt, err := store.CreateSession(context.Background(), db.CreateSessionParams{
		Title: "corrupt", Model: "test", Mode: "AUTO", WorkspaceID: workspace,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSessionState(context.Background(), corrupt.ID, `{`); err != nil {
		t.Fatal(err)
	}

	summaries, warnings, err := listGoalSummaries(context.Background(), store, workspace, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 1 || summaries[0].SessionID != first.ID || summaries[0].GoalID != firstGoal.ID {
		t.Fatalf("summaries = %#v", summaries)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0].Error(), "session ") {
		t.Fatalf("warnings = %#v", warnings)
	}
}

func TestGetGoalSummaryEnforcesWorkspaceAndSessionBinding(t *testing.T) {
	store := openGoalCommandTestStore(t)
	session, expected := createGoalSession(t, store, "/workspace/a", "Inspect exact scope")
	summary, err := getGoalSummary(context.Background(), store, "/workspace/a", session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if summary.GoalID != expected.ID || summary.State != goal.StateActive || summary.Snapshot.SessionID != session.ID {
		t.Fatalf("summary = %#v", summary)
	}
	if _, err := getGoalSummary(context.Background(), store, "/workspace/b", session.ID); err == nil || !strings.Contains(err.Error(), "different workspace") {
		t.Fatalf("cross-workspace error = %v", err)
	}

	forged := expected
	forged.SessionID++
	payload, _ := json.Marshal(map[string]any{"version": 1, "goal": forged})
	if err := store.SaveSessionState(context.Background(), session.ID, string(payload)); err != nil {
		t.Fatal(err)
	}
	if _, err := getGoalSummary(context.Background(), store, "/workspace/a", session.ID); err == nil || !strings.Contains(err.Error(), "belongs to session") {
		t.Fatalf("forged session error = %v", err)
	}
}

func TestGoalListAndShowRenderingAndJSON(t *testing.T) {
	store := openGoalCommandTestStore(t)
	workspace := "/workspace/a"
	session, _ := createGoalSession(t, store, workspace, "A very useful Unicode 目标 goal\nwith a compact second line")

	var stdout, stderr bytes.Buffer
	if code := handleGoalList(store, workspace, []string{"--json", "--limit", "10"}, &stdout, &stderr); code != 0 {
		t.Fatalf("list code=%d stderr=%q", code, stderr.String())
	}
	var decoded []goalSummary
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil || len(decoded) != 1 || decoded[0].SessionID != session.ID {
		t.Fatalf("list JSON=%q decoded=%#v err=%v", stdout.String(), decoded, err)
	}

	stdout.Reset()
	stderr.Reset()
	if code := handleGoalList(store, workspace, nil, &stdout, &stderr); code != 0 {
		t.Fatalf("text list code=%d stderr=%q", code, stderr.String())
	}
	for _, want := range []string{"SESSION", "STATE", "A very useful Unicode 目标 goal with a compact second line"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("text list %q lacks %q", stdout.String(), want)
		}
	}

	stdout.Reset()
	stderr.Reset()
	if code := handleGoalShow(store, workspace, []string{"--json", formatSessionID(session.ID)}, &stdout, &stderr); code != 0 {
		t.Fatalf("show code=%d stderr=%q", code, stderr.String())
	}
	var snapshot goal.Snapshot
	if err := json.Unmarshal(stdout.Bytes(), &snapshot); err != nil || snapshot.SessionID != session.ID {
		t.Fatalf("show JSON=%q snapshot=%#v err=%v", stdout.String(), snapshot, err)
	}
}

func TestGoalCommandArgumentFailures(t *testing.T) {
	store := openGoalCommandTestStore(t)
	var stdout, stderr bytes.Buffer
	if code := handleGoalList(store, "/workspace", []string{"--limit", "0"}, &stdout, &stderr); code != 2 {
		t.Fatalf("invalid list limit code=%d stderr=%q", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := handleGoalShow(store, "/workspace", []string{"not-a-session"}, &stdout, &stderr); code != 2 {
		t.Fatalf("invalid show ID code=%d stderr=%q", code, stderr.String())
	}
	if got := compactGoalObjective(strings.Repeat("界", 80), 8); len([]rune(got)) != 8 || !strings.HasSuffix(got, "…") {
		t.Fatalf("compact Unicode objective = %q", got)
	}
	if got := terminalSafeGoalText("safe\x1b[31m\nnext"); strings.ContainsRune(got, '\x1b') || got != "safe[31m next" {
		t.Fatalf("terminal-safe text = %q", got)
	}
	if got := terminalSafeGoalText("safe\u202ereversed"); got != "safereversed" {
		t.Fatalf("terminal bidi-safe text = %q", got)
	}
}

func TestGoalPendingListsOnlyUnresolvedControlItems(t *testing.T) {
	store := openGoalCommandTestStore(t)
	workspace := "/workspace/a"
	session, snapshot := createGoalSession(t, store, workspace, "Resolve durable decisions")
	lease, err := store.AcquireExecutionSessionLease(context.Background(), session.ID, workspace)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Close() })
	const privatePayload = "private-control-envelope-detail"
	payload, digest, err := controlplane.MarshalDocument(map[string]any{"internal_context": privatePayload})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = store.AppendControlItem(context.Background(), lease, controlplane.Item{
		ItemID: "ctrl_pending", IdempotencyKey: "ctrlidem_pending",
		Kind: controlplane.KindCortexDecision,
		Identity: controlplane.Identity{
			SessionID: session.ID, WorkspaceID: workspace, GoalID: snapshot.ID,
		},
		Summary: "Choose a migration strategy", PayloadJSON: payload, PayloadSHA256: digest,
	})
	if err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := handleGoalPending(store, workspace, []string{"--json", formatSessionID(session.ID)}, &stdout, &stderr); code != 0 {
		t.Fatalf("pending code=%d stderr=%q", code, stderr.String())
	}
	var states []pendingControlSummary
	if err := json.Unmarshal(stdout.Bytes(), &states); err != nil || len(states) != 1 || states[0].ItemID != "ctrl_pending" {
		t.Fatalf("pending JSON=%q states=%#v err=%v", stdout.String(), states, err)
	}
	if strings.Contains(stdout.String(), privatePayload) || strings.Contains(strings.ToLower(stdout.String()), "payload") {
		t.Fatalf("pending JSON disclosed private payload envelope: %q", stdout.String())
	}

	evidence, evidenceDigest, err := controlplane.MarshalDocument(map[string]any{"answer": "forward-only"})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = store.ResolveControlItem(context.Background(), lease, controlplane.Resolution{
		ResolutionID: "ctrlres_pending", IdempotencyKey: "ctrlidem_resolution_pending",
		ItemID: "ctrl_pending", SessionID: session.ID, WorkspaceID: workspace,
		Outcome: controlplane.OutcomeAnswered, EvidenceJSON: evidence, EvidenceSHA256: evidenceDigest,
		ResolvedBy: "test", Detail: "decision recorded",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := handleGoalPending(store, workspace, []string{formatSessionID(session.ID)}, &stdout, &stderr); code != 0 {
		t.Fatalf("resolved pending code=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "No pending") {
		t.Fatalf("resolved pending output = %q", stdout.String())
	}
}

func TestGoalRecoverDryRunIsReadOnlyRedactedAndJSONStable(t *testing.T) {
	store := openGoalCommandTestStore(t)
	workspace := "/workspace/recover-dry-run"
	fixture := createGoalRecoveryFixture(t, store, workspace, true, true)
	before, err := store.GetSessionStateRecord(context.Background(), fixture.Session.ID)
	if err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := handleGoalRecover(store, workspace, []string{formatSessionID(fixture.Session.ID), "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("dry-run code=%d stderr=%q", code, stderr.String())
	}
	var view goalRecoveryDryRun
	if err := json.Unmarshal(stdout.Bytes(), &view); err != nil {
		t.Fatalf("dry-run JSON=%q error=%v", stdout.String(), err)
	}
	if !view.DryRun || view.SessionRevision != before.Revision || view.GroupItemID != fixture.Group.GroupItemID ||
		len(view.Members) != 1 || len(view.UnresolvedExecutionItems) != 1 || view.Parent.Ready || view.Parent.Resolved {
		t.Fatalf("dry-run projection = %#v", view)
	}
	if !strings.Contains(view.NoResumeWarning, "never resumes") ||
		strings.Contains(stdout.String(), "payload_json") || strings.Contains(stdout.String(), "evidence_json") {
		t.Fatalf("dry-run leaked authority envelope or warning: %q", stdout.String())
	}
	after, err := store.GetSessionStateRecord(context.Background(), fixture.Session.ID)
	if err != nil || after.Revision != before.Revision || after.StateJSON != before.StateJSON {
		t.Fatalf("dry-run mutated session: before=%#v after=%#v error=%v", before, after, err)
	}
	group, err := store.GetReconciliationGroup(context.Background(), fixture.Session.ID, workspace, fixture.Group.GroupItemID)
	if err != nil || group.ParentResolution != nil || group.Members[0].Resolved {
		t.Fatalf("dry-run mutated group = %#v error=%v", group, err)
	}
}

func TestGoalRecoverDryRunNeverEnsuresMissingGroup(t *testing.T) {
	store := openGoalCommandTestStore(t)
	workspace := "/workspace/recover-no-ensure"
	fixture := createGoalRecoveryFixture(t, store, workspace, false, false)
	var stdout, stderr bytes.Buffer
	if code := handleGoalRecover(store, workspace, []string{formatSessionID(fixture.Session.ID)}, &stdout, &stderr); code != 1 {
		t.Fatalf("missing-group dry-run code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "dry-run never creates") {
		t.Fatalf("missing-group dry-run error = %q", stderr.String())
	}
	if _, err := store.InspectReconciliationGroup(context.Background(), fixture.Session.ID, workspace); !errors.Is(err, db.ErrReconciliationGroupNotFound) {
		t.Fatalf("dry-run created a group: %v", err)
	}
	after, err := store.GetSessionStateRecord(context.Background(), fixture.Session.ID)
	if err != nil || after.Revision != fixture.Record.Revision || after.StateJSON != fixture.Record.StateJSON {
		t.Fatalf("missing-group dry-run mutated session: %#v error=%v", after, err)
	}
}

func TestGoalRecoverApplyZeroToolEnsuresPausesAndExactlyReplays(t *testing.T) {
	store := openGoalCommandTestStore(t)
	workspace := "/workspace/recover-zero-apply"
	fixture := createGoalRecoveryFixture(t, store, workspace, false, false)
	args := goalRecoveryApplyArgs(
		fixture.Session.ID, fixture.ExpectedGroup,
		string(reconciliation.TurnAbandonedAfterInspection), "Inspected the abandoned provider turn and found no execution lifecycle.",
	)
	var stdout, stderr bytes.Buffer
	if code := handleGoalRecover(store, workspace, args, &stdout, &stderr); code != 0 {
		t.Fatalf("zero-tool apply code=%d stderr=%q", code, stderr.String())
	}
	var result goalRecoveryApplyResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("zero-tool JSON=%q error=%v", stdout.String(), err)
	}
	if !result.Applied || !result.Inserted || !result.GoalCleared || result.GoalState != goal.StatePaused ||
		result.GroupItemID != fixture.ExpectedGroup || result.ParentPending || result.RemainingExecutions != 0 {
		t.Fatalf("zero-tool result = %#v", result)
	}
	if !strings.Contains(result.NoResumeWarning, "never resumes") {
		t.Fatalf("zero-tool warning = %q", result.NoResumeWarning)
	}

	stdout.Reset()
	stderr.Reset()
	if code := handleGoalRecover(store, workspace, args, &stdout, &stderr); code != 0 {
		t.Fatalf("exact replay code=%d stderr=%q", code, stderr.String())
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result.Inserted || !result.GoalCleared || result.GoalState != goal.StatePaused {
		t.Fatalf("exact replay result=%#v JSON=%q error=%v", result, stdout.String(), err)
	}

	for _, mutate := range []func([]string) []string{
		func(values []string) []string {
			return replaceGoalRecoverFlag(values, "--summary", "different evidence summary")
		},
		func(values []string) []string {
			return replaceGoalRecoverFlag(values, "--observed-at", "2026-07-12T17:30:01Z")
		},
	} {
		stdout.Reset()
		stderr.Reset()
		if code := handleGoalRecover(store, workspace, mutate(append([]string(nil), args...)), &stdout, &stderr); code != 1 {
			t.Fatalf("conflicting replay code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
		if !strings.Contains(stderr.String(), "conflict") && !strings.Contains(stderr.String(), "differs") {
			t.Fatalf("conflicting replay error = %q", stderr.String())
		}
	}
}

func TestGoalRecoverMemberThenParentGating(t *testing.T) {
	store := openGoalCommandTestStore(t)
	workspace := "/workspace/recover-gating"
	fixture := createGoalRecoveryFixture(t, store, workspace, true, true)
	member := fixture.Group.Members[0]
	parentArgs := goalRecoveryApplyArgs(
		fixture.Session.ID, fixture.Group.GroupItemID,
		string(reconciliation.TurnAbandonedAfterInspection), "Inspected the abandoned turn and every execution member.",
	)
	var stdout, stderr bytes.Buffer
	if code := handleGoalRecover(store, workspace, parentArgs, &stdout, &stderr); code != 1 {
		t.Fatalf("early parent code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "unresolved execution") {
		t.Fatalf("early parent error = %q", stderr.String())
	}

	memberArgs := goalRecoveryApplyArgs(
		fixture.Session.ID, member.ControlItemID,
		string(reconciliation.DispositionEffectNotApplied), "Verified that the external effect was not applied.",
	)
	stdout.Reset()
	stderr.Reset()
	if code := handleGoalRecover(store, workspace, memberArgs, &stdout, &stderr); code != 0 {
		t.Fatalf("member apply code=%d stderr=%q", code, stderr.String())
	}
	var memberResult goalRecoveryApplyResult
	if err := json.Unmarshal(stdout.Bytes(), &memberResult); err != nil || memberResult.GoalCleared || !memberResult.ParentPending || memberResult.RemainingExecutions != 0 {
		t.Fatalf("member result=%#v JSON=%q error=%v", memberResult, stdout.String(), err)
	}

	stdout.Reset()
	stderr.Reset()
	if code := handleGoalRecover(store, workspace, []string{"--json", formatSessionID(fixture.Session.ID)}, &stdout, &stderr); code != 0 {
		t.Fatalf("post-member dry-run code=%d stderr=%q", code, stderr.String())
	}
	var view goalRecoveryDryRun
	if err := json.Unmarshal(stdout.Bytes(), &view); err != nil || len(view.UnresolvedExecutionItems) != 0 || !view.Parent.Ready || !view.Members[0].Resolved {
		t.Fatalf("post-member dry-run=%#v JSON=%q error=%v", view, stdout.String(), err)
	}

	stdout.Reset()
	stderr.Reset()
	if code := handleGoalRecover(store, workspace, parentArgs, &stdout, &stderr); code != 0 {
		t.Fatalf("final parent code=%d stderr=%q", code, stderr.String())
	}
	var final goalRecoveryApplyResult
	if err := json.Unmarshal(stdout.Bytes(), &final); err != nil || !final.GoalCleared || final.GoalState != goal.StatePaused || final.ParentPending {
		t.Fatalf("final parent=%#v JSON=%q error=%v", final, stdout.String(), err)
	}
}

func TestGoalRecoverApplyRequiresLeaseAndRejectsInvalidOrStaleFlags(t *testing.T) {
	t.Run("busy lease", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "recover-busy.db")
		first, err := db.OpenPath(path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = first.Close() }()
		second, err := db.OpenPath(path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = second.Close() }()
		workspace := "/workspace/recover-busy"
		fixture := createGoalRecoveryFixture(t, first, workspace, false, true)
		lease, err := first.AcquireExecutionSessionLease(context.Background(), fixture.Session.ID, workspace)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = lease.Close() }()
		args := goalRecoveryApplyArgs(fixture.Session.ID, fixture.Group.GroupItemID, string(reconciliation.TurnAbandonedAfterInspection), "Inspected busy recovery turn.")
		var stdout, stderr bytes.Buffer
		if code := handleGoalRecover(second, workspace, args, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "busy") {
			t.Fatalf("busy lease code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
	})

	t.Run("invalid flags never ensure", func(t *testing.T) {
		store := openGoalCommandTestStore(t)
		workspace := "/workspace/recover-invalid"
		fixture := createGoalRecoveryFixture(t, store, workspace, false, false)
		var stdout, stderr bytes.Buffer
		if code := handleGoalRecover(store, workspace, []string{formatSessionID(fixture.Session.ID), "--apply", "--item", fixture.ExpectedGroup}, &stdout, &stderr); code != 2 {
			t.Fatalf("missing evidence code=%d stderr=%q", code, stderr.String())
		}
		if _, err := store.InspectReconciliationGroup(context.Background(), fixture.Session.ID, workspace); !errors.Is(err, db.ErrReconciliationGroupNotFound) {
			t.Fatalf("invalid flags ensured group: %v", err)
		}
		stdout.Reset()
		stderr.Reset()
		if code := handleGoalRecover(store, workspace, []string{formatSessionID(fixture.Session.ID), "--force"}, &stdout, &stderr); code != 2 {
			t.Fatalf("force flag code=%d stderr=%q", code, stderr.String())
		}
		invalidTime := goalRecoveryApplyArgs(fixture.Session.ID, fixture.ExpectedGroup, string(reconciliation.TurnAbandonedAfterInspection), "Inspected invalid timestamp turn.")
		invalidTime = replaceGoalRecoverFlag(invalidTime, "--observed-at", "yesterday")
		stdout.Reset()
		stderr.Reset()
		if code := handleGoalRecover(store, workspace, invalidTime, &stdout, &stderr); code != 2 {
			t.Fatalf("invalid time code=%d stderr=%q", code, stderr.String())
		}
	})

	t.Run("stale loaded revision", func(t *testing.T) {
		store := openGoalCommandTestStore(t)
		workspace := "/workspace/recover-stale"
		fixture := createGoalRecoveryFixture(t, store, workspace, false, true)
		wrapped := &staleGoalRecoveryStore{Store: store}
		args := goalRecoveryApplyArgs(fixture.Session.ID, fixture.Group.GroupItemID, string(reconciliation.TurnAbandonedAfterInspection), "Inspected stale recovery turn.")
		var stdout, stderr bytes.Buffer
		if code := handleGoalRecover(wrapped, workspace, args, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "revision") {
			t.Fatalf("stale revision code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
		group, err := store.GetReconciliationGroup(context.Background(), fixture.Session.ID, workspace, fixture.Group.GroupItemID)
		if err != nil || group.ParentResolution != nil {
			t.Fatalf("stale apply mutated parent = %#v error=%v", group.ParentResolution, err)
		}
	})
}

type staleGoalRecoveryStore struct {
	*db.Store
	advanced bool
}

func (s *staleGoalRecoveryStore) GetSessionStateRecord(ctx context.Context, sessionID int64) (db.SessionStateRecord, error) {
	record, err := s.Store.GetSessionStateRecord(ctx, sessionID)
	if err != nil || s.advanced {
		return record, err
	}
	s.advanced = true
	if _, err := s.SaveSessionStateCAS(ctx, sessionID, record.Revision, record.StateJSON); err != nil {
		return db.SessionStateRecord{}, err
	}
	return record, nil
}

func replaceGoalRecoverFlag(values []string, name, replacement string) []string {
	for index := 0; index+1 < len(values); index++ {
		if values[index] == name {
			values[index+1] = replacement
			return values
		}
	}
	return values
}

func TestProjectPendingControlItemsRejectsResolvedAndCrossScopeRows(t *testing.T) {
	payload, digest, err := controlplane.MarshalDocument(map[string]any{"safe": true})
	if err != nil {
		t.Fatal(err)
	}
	item := controlplane.Item{
		ItemID: "ctrl_projection", IdempotencyKey: "ctrlidem_projection",
		Kind:     controlplane.KindDeferredApproval,
		Identity: controlplane.Identity{SessionID: 7, WorkspaceID: "/workspace"},
		Summary:  "Approve the bounded operation", PayloadJSON: payload, PayloadSHA256: digest,
	}
	if _, err := projectPendingControlItems([]controlplane.State{{Item: item}}, 8, "/workspace"); err == nil {
		t.Fatal("cross-session pending row unexpectedly projected")
	}
	if _, err := projectPendingControlItems([]controlplane.State{{Item: item, Resolution: &controlplane.Resolution{}}}, 7, "/workspace"); err == nil {
		t.Fatal("resolved pending row unexpectedly projected")
	}
}

func TestDecodeGoalSummaryRefreshesElapsedWallBudget(t *testing.T) {
	store := openGoalCommandTestStore(t)
	session, snapshot := createGoalSession(t, store, "/workspace", "Expired goal")
	snapshot.Budget.MaxWallTime = time.Nanosecond
	snapshot.CreatedAt = time.Now().Add(-time.Hour).UTC()
	snapshot.UpdatedAt = snapshot.CreatedAt
	payload, _ := json.Marshal(map[string]any{"version": 1, "goal": snapshot})
	if err := store.SaveSessionState(context.Background(), session.ID, string(payload)); err != nil {
		t.Fatal(err)
	}
	summary, err := getGoalSummary(context.Background(), store, "/workspace", session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if summary.State != goal.StateExhausted {
		t.Fatalf("elapsed state = %s, want exhausted", summary.State)
	}
}

func formatSessionID(id int64) string { return strconv.FormatInt(id, 10) }
