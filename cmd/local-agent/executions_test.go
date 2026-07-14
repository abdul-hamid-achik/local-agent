package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/local-agent/internal/db"
	"github.com/abdul-hamid-achik/local-agent/internal/execution"
)

type fakeExecutionRecoveryStore struct {
	inspection db.StandaloneExecutionReconciliationInspection
	inspectErr error
	inspected  int
	acquired   int
	pending    []execution.State
	pendingErr error
	listed     int
}

func (s *fakeExecutionRecoveryStore) ListStandaloneExecutionReconciliationPending(context.Context, int64, string, int) ([]execution.State, error) {
	s.listed++
	return s.pending, s.pendingErr
}

func (s *fakeExecutionRecoveryStore) InspectStandaloneExecutionReconciliation(context.Context, int64, string, string) (db.StandaloneExecutionReconciliationInspection, error) {
	s.inspected++
	return s.inspection, s.inspectErr
}

func (s *fakeExecutionRecoveryStore) AcquireExecutionSessionLease(context.Context, int64, string) (*db.ExecutionSessionLease, error) {
	s.acquired++
	return nil, errors.New("not expected")
}

func (s *fakeExecutionRecoveryStore) ResolveStandaloneExecutionReconciliation(context.Context, *db.ExecutionSessionLease, db.ResolveStandaloneExecutionReconciliationRequest) (db.StandaloneExecutionReconciliationReceipt, error) {
	return db.StandaloneExecutionReconciliationReceipt{}, errors.New("not expected")
}

func TestExecutionRecoverInspectionIsReadOnlyAndPrintsExactApplyTokens(t *testing.T) {
	store := &fakeExecutionRecoveryStore{inspection: db.StandaloneExecutionReconciliationInspection{
		SessionID: 17, WorkspaceID: "/workspace/repo", SessionRevision: 4,
		ExecutionID: "exec_timeout", TurnID: "turn_timeout", ToolName: "bash",
		EventID: 29, EventType: execution.EventOutcomeUnknown,
		EffectClass: execution.EffectUnknown, ArgumentsSHA256: strings.Repeat("a", 64),
		ItemID: "ctrl_execution_timeout",
	}}
	var stdout, stderr bytes.Buffer
	code := handleExecutionRecover(store, "/workspace/repo", []string{"17", "exec_timeout"}, &stdout, &stderr)
	if code != 0 || stderr.Len() != 0 || store.inspected != 1 || store.acquired != 0 {
		t.Fatalf("code=%d inspected=%d acquired=%d stdout=%q stderr=%q", code, store.inspected, store.acquired, stdout.String(), stderr.String())
	}
	for _, want := range []string{
		"Inspection is read-only", "--revision 4", "--event-id 29",
		"17 exec_timeout", "effect_not_applied", "verification_check",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("inspection missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestExecutionRecoverAllListsPendingReadOnly(t *testing.T) {
	store := &fakeExecutionRecoveryStore{pending: []execution.State{
		{
			Identity: execution.Identity{
				SessionID: 17, WorkspaceID: "/workspace/repo", ExecutionID: "exec_one",
				ToolName: "bash", TurnID: "turn_one", EffectClass: execution.EffectUnknown,
			},
			Latest: execution.Event{ID: 29, Type: execution.EventOutcomeUnknown},
		},
		{
			Identity: execution.Identity{
				SessionID: 17, WorkspaceID: "/workspace/repo", ExecutionID: "exec_two",
				ToolName: "mcphub__cortex__cortex_open_task", TurnID: "turn_two", EffectClass: execution.Effectful,
			},
			Latest: execution.Event{ID: 41, Type: execution.EventOutcomeUnknown},
		},
	}}
	var stdout, stderr bytes.Buffer
	code := handleExecutionRecover(store, "/workspace/repo", []string{"17", "--all"}, &stdout, &stderr)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
	if store.listed != 1 || store.acquired != 0 {
		t.Fatalf("listing acquired a lease or skipped the query: %#v", store)
	}
	out := stdout.String()
	for _, want := range []string{"exec_one", "exec_two", "2 execution(s) pending reconciliation", "--all --apply"} {
		if !strings.Contains(out, want) {
			t.Fatalf("listing missing %q: %s", want, out)
		}
	}

	empty := &fakeExecutionRecoveryStore{}
	stdout.Reset()
	stderr.Reset()
	if code := handleExecutionRecover(empty, "/workspace/repo", []string{"17", "--all"}, &stdout, &stderr); code != 0 {
		t.Fatalf("empty listing exit=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "No executions are pending reconciliation") {
		t.Fatalf("empty listing output = %q", stdout.String())
	}
}

func TestExecutionRecoverAllApplyRejectsPerExecutionTokens(t *testing.T) {
	store := &fakeExecutionRecoveryStore{}
	var stdout, stderr bytes.Buffer
	code := handleExecutionRecover(store, "/workspace/repo", []string{
		"17", "--all", "--apply", "--revision", "4", "--event-id", "29",
		"--observation", "effect_not_applied", "--source", "verification_check",
		"--reference", "ref", "--summary", "sum", "--observed-at", "2026-07-14T10:00:00Z",
	}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "cannot be combined with --all") {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
	if store.acquired != 0 || store.listed != 0 {
		t.Fatalf("rejected batch apply still touched the store: %#v", store)
	}
}

func TestExecutionRecoverApplyRequiresPriorInspectionTokens(t *testing.T) {
	store := &fakeExecutionRecoveryStore{}
	var stdout, stderr bytes.Buffer
	code := handleExecutionRecover(store, "/workspace/repo", []string{
		"--apply", "--observation", "effect_not_applied", "--source", "verification_check",
		"--reference", "check:absent", "--summary", "Verified effect absence.",
		"--observed-at", "2026-07-13T12:00:00Z", "17", "exec_timeout",
	}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "--revision") || store.acquired != 0 {
		t.Fatalf("code=%d acquired=%d stdout=%q stderr=%q", code, store.acquired, stdout.String(), stderr.String())
	}
}

func TestNormalizeExecutionRecoverArgsKeepsInterspersedFlags(t *testing.T) {
	got, err := normalizeExecutionRecoverArgs([]string{"17", "exec_timeout", "--revision", "4", "--event-id=29", "--apply"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--revision", "4", "--event-id=29", "--apply", "17", "exec_timeout"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("normalized = %#v, want %#v", got, want)
	}
}
