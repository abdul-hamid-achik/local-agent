package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/local-agent/internal/controlplane"
	"github.com/abdul-hamid-achik/local-agent/internal/db"
	"github.com/abdul-hamid-achik/local-agent/internal/goal"
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
