package goaladvisor

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/abdul-hamid-achik/local-agent/internal/goal"
	"github.com/abdul-hamid-achik/local-agent/internal/mcp"
)

type fakeRegistry struct {
	routes  map[string]string
	name    string
	calls   []string
	args    map[string]any
	history []map[string]any
	result  *mcp.ToolResult
	results map[string]*mcp.ToolResult
	err     error
}

func (f *fakeRegistry) ResolveToolName(remote string) (string, bool) {
	name, ok := f.routes[remote]
	return name, ok
}

func (f *fakeRegistry) CallTool(_ context.Context, name string, args map[string]any) (*mcp.ToolResult, error) {
	f.name = name
	f.calls = append(f.calls, name)
	f.args = args
	f.history = append(f.history, cloneTestArgs(args))
	if f.results != nil {
		return f.results[name], f.err
	}
	return f.result, f.err
}

func cloneTestArgs(args map[string]any) map[string]any {
	payload, _ := json.Marshal(args)
	var cloned map[string]any
	_ = json.Unmarshal(payload, &cloned)
	return cloned
}

func TestCortexOpenUsesLazyGatewayAndStableIdentity(t *testing.T) {
	registry := &fakeRegistry{
		routes: map[string]string{gatewayTool: "mcphub__mcphub_call_tool"},
		result: &mcp.ToolResult{Content: `{"ok":true,"taskId":"task_1","phase":"investigating","summary":"opened"}`},
	}
	advisor := NewCortex(registry, "/work/repo", "local-agent:test")
	advice, err := advisor.Open(context.Background(), OpenRequest{
		GoalID: "goal_1", Objective: "Ship safely",
		AcceptanceCriteria: []goal.AcceptanceCriterion{{ID: "ac_1", Description: "Tests pass"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if advice.TaskID != "task_1" || registry.name != "mcphub__mcphub_call_tool" {
		t.Fatalf("advice/call = %+v %q", advice, registry.name)
	}
	if registry.args["server"] != "cortex" || registry.args["tool"] != openTool {
		t.Fatalf("gateway route = %#v", registry.args)
	}
	downstream, ok := registry.args["arguments"].(map[string]any)
	if !ok {
		t.Fatalf("downstream args = %#v", registry.args["arguments"])
	}
	if downstream["idempotencyKey"] != "goal_1" || downstream["workspace"] != "/work/repo" {
		t.Fatalf("identity args = %#v", downstream)
	}
	if text, _ := downstream["goal"].(string); text != "Ship safely" {
		t.Fatalf("goal text = %q", text)
	}
	criteriaJSON, err := json.Marshal(downstream["acceptanceCriteria"])
	if err != nil {
		t.Fatal(err)
	}
	if string(criteriaJSON) != `[{"id":"ac_1","statement":"Tests pass"}]` {
		t.Fatalf("acceptance criteria = %s", criteriaJSON)
	}
}

func TestCortexOpenSendsImmutableCriteriaAndRetriesIdentically(t *testing.T) {
	registry := &fakeRegistry{
		routes: map[string]string{openTool: "cortex__cortex_open_task"},
		result: &mcp.ToolResult{
			Content:    `{"ok":false,"summary":"stale display text"}`,
			Structured: json.RawMessage(`{"ok":true,"taskId":"task_1","phase":"investigating"}`),
		},
	}
	request := OpenRequest{
		GoalID:    "goal_1",
		Objective: "Ship safely",
		AcceptanceCriteria: []goal.AcceptanceCriterion{
			{ID: "tests", Description: "All tests pass"},
			{ID: "docs", Description: "Public docs match behavior"},
		},
	}
	advisor := NewCortex(registry, "/work/repo", "")
	for attempt := 0; attempt < 2; attempt++ {
		advice, err := advisor.Open(context.Background(), request)
		if err != nil || advice.TaskID != "task_1" {
			t.Fatalf("attempt %d: advice=%+v err=%v", attempt+1, advice, err)
		}
	}
	if len(registry.history) != 2 || !reflect.DeepEqual(registry.history[0], registry.history[1]) {
		t.Fatalf("idempotent retry changed arguments: %#v", registry.history)
	}
	if registry.history[0]["idempotencyKey"] != "goal_1" {
		t.Fatalf("idempotency key = %#v", registry.history[0]["idempotencyKey"])
	}
}

func TestCortexOpenRejectsInvalidImmutableCriteriaBeforeDispatch(t *testing.T) {
	tests := []struct {
		name     string
		criteria []goal.AcceptanceCriterion
	}{
		{name: "missing", criteria: nil},
		{name: "empty id", criteria: []goal.AcceptanceCriterion{{Description: "Tests pass"}}},
		{name: "empty statement", criteria: []goal.AcceptanceCriterion{{ID: "tests"}}},
		{name: "duplicate id", criteria: []goal.AcceptanceCriterion{{ID: "tests", Description: "One"}, {ID: "tests", Description: "Two"}}},
		{name: "oversized statement", criteria: []goal.AcceptanceCriterion{{ID: "tests", Description: strings.Repeat("x", goal.MaxCriterionBytes+1)}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registry := &fakeRegistry{routes: map[string]string{openTool: "open"}}
			_, err := NewCortex(registry, "/work", "").Open(context.Background(), OpenRequest{
				GoalID: "goal_1", Objective: "Ship", AcceptanceCriteria: test.criteria,
			})
			if !errors.Is(err, ErrRejected) || len(registry.calls) != 0 {
				t.Fatalf("err=%v calls=%#v", err, registry.calls)
			}
		})
	}
}

func TestCortexStatusPrefersDirectTool(t *testing.T) {
	registry := &fakeRegistry{
		routes: map[string]string{statusTool: "cortex__cortex_status", gatewayTool: "mcphub__mcphub_call_tool"},
		result: &mcp.ToolResult{Content: `{"ok":true,"taskId":"task_1","revision":7,"phase":"verifying","verificationOutcome":"verified","actions":[{"tool":"cortex_remember","reason":"preserve"}]}` + "\nstructured: {}"},
	}
	advice, err := NewCortex(registry, "/work/repo", "").Status(context.Background(), "task_1")
	if err != nil {
		t.Fatal(err)
	}
	if registry.name != "cortex__cortex_status" || advice.Revision != 7 || advice.VerificationOutcome != "verified" {
		t.Fatalf("direct status = name %q advice %+v", registry.name, advice)
	}
	if !reflect.DeepEqual(advice.Actions, []Action{{Tool: "cortex_remember", Reason: "preserve"}}) {
		t.Fatalf("actions = %#v", advice.Actions)
	}
}

func TestCortexStatusCollectsCriterionBoundHandoffEvidence(t *testing.T) {
	registry := &fakeRegistry{
		routes: map[string]string{
			statusTool:  "cortex__cortex_status",
			handoffTool: "cortex__cortex_handoff",
		},
		results: map[string]*mcp.ToolResult{
			"cortex__cortex_status": {Content: `{"ok":true,"taskId":"task_1","revision":7,"phase":"complete","verificationOutcome":"verified"}`},
			"cortex__cortex_handoff": {Content: `{
				"taskId":"task_1",
				"revision":7,
				"phase":"complete",
				"verification":{"outcome":"verified"},
				"receipts":[
					{"id":"vr_run","batchId":"batch_1","purpose":"verifier_run","status":"passed","binding":"bound","revision":"commit_1","dirtyDigest":"sha256:dirty_1","evidence":["ev_1"]},
					{"id":"vr_claim","batchId":"batch_1","claimId":"criterion_1","claim":"The durable receipt is verified","purpose":"named_claim","status":"passed","binding":"bound","revision":"commit_1","dirtyDigest":"sha256:dirty_1"}
				]
			}`},
		},
	}
	advisor := NewCortex(registry, "/work/repo", "")
	advisor.revision = func(context.Context, string) (WorkspaceRevision, error) {
		return WorkspaceRevision{Commit: "commit_1", DirtyDigest: "sha256:dirty_1"}, nil
	}
	advice, err := advisor.Status(context.Background(), "task_1")
	if err != nil {
		t.Fatal(err)
	}
	proof := advice.CriterionEvidence["criterion_1"]
	refs := proof.Evidence
	if proof.Claim != "The durable receipt is verified" || proof.Revision != "commit_1" || proof.DirtyDigest != "sha256:dirty_1" {
		t.Fatalf("criterion proof = %#v", proof)
	}
	for _, want := range []string{
		"case://task_1/verification/vr_claim",
		"case://task_1/verification/vr_run",
		"ev_1",
	} {
		if !containsString(refs, want) {
			t.Fatalf("criterion evidence %#v missing %q", refs, want)
		}
	}
	if !reflect.DeepEqual(registry.calls, []string{"cortex__cortex_status", "cortex__cortex_handoff"}) {
		t.Fatalf("calls = %#v", registry.calls)
	}
}

func TestCortexStatusDoesNotTrustMismatchedHandoff(t *testing.T) {
	registry := &fakeRegistry{
		routes: map[string]string{statusTool: "status", handoffTool: "handoff"},
		results: map[string]*mcp.ToolResult{
			"status":  {Content: `{"ok":true,"taskId":"task_1","revision":7,"phase":"complete","verificationOutcome":"verified"}`},
			"handoff": {Content: `{"taskId":"task_1","revision":6,"phase":"complete","verification":{"outcome":"verified"}}`},
		},
	}
	advice, err := NewCortex(registry, "/work/repo", "").Status(context.Background(), "task_1")
	if err != nil {
		t.Fatal(err)
	}
	if len(advice.CriterionEvidence) != 0 || len(advice.Warnings) == 0 {
		t.Fatalf("mismatched handoff was trusted: %+v", advice)
	}
}

func TestCortexStatusRejectsReceiptsAfterWorkspaceMutation(t *testing.T) {
	registry := &fakeRegistry{
		routes: map[string]string{statusTool: "status", handoffTool: "handoff"},
		results: map[string]*mcp.ToolResult{
			"status": {Content: `{"ok":true,"taskId":"task_1","revision":7,"phase":"complete","verificationOutcome":"verified"}`},
			"handoff": {Content: `{
				"taskId":"task_1","revision":7,"phase":"complete","verification":{"outcome":"verified"},
				"receipts":[
					{"id":"run_old","batchId":"batch_1","purpose":"verifier_run","status":"passed","binding":"bound","revision":"commit_old","dirtyDigest":"sha256:dirty_old","evidence":["ev_old"]},
					{"id":"claim_old","batchId":"batch_1","claimId":"criterion_1","claim":"Tests pass","purpose":"named_claim","status":"passed","binding":"bound","revision":"commit_old","dirtyDigest":"sha256:dirty_old"}
				]
			}`},
		},
	}
	advisor := NewCortex(registry, "/work/repo", "")
	advisor.revision = func(context.Context, string) (WorkspaceRevision, error) {
		return WorkspaceRevision{Commit: "commit_old", DirtyDigest: "sha256:dirty_after_write"}, nil
	}
	advice, err := advisor.Status(context.Background(), "task_1")
	if err != nil {
		t.Fatal(err)
	}
	if len(advice.CriterionEvidence) != 0 {
		t.Fatalf("stale receipts survived workspace mutation: %#v", advice.CriterionEvidence)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestCortexRejectsErrorEnvelopeWithoutLosingContext(t *testing.T) {
	registry := &fakeRegistry{
		routes: map[string]string{statusTool: "cortex__cortex_status"},
		result: &mcp.ToolResult{Content: `{"ok":false,"taskId":"task_1","phase":"blocked","summary":"decision required"}`, IsError: true},
	}
	advice, err := NewCortex(registry, "", "").Status(context.Background(), "task_1")
	if !errors.Is(err, ErrRejected) || advice.TaskID != "task_1" || advice.Phase != "blocked" {
		t.Fatalf("error advice = %+v err=%v", advice, err)
	}
}

func TestCortexUnavailableWithoutDirectOrGatewayTool(t *testing.T) {
	_, err := NewCortex(&fakeRegistry{routes: map[string]string{}}, "/work", "").Status(context.Background(), "task_1")
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("error = %v", err)
	}
}

func TestCortexStatusFailsClosedOnIdentityPhaseAndDegradedDrift(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{name: "task mismatch", content: `{"ok":true,"taskId":"task_other","phase":"investigating"}`},
		{name: "missing phase", content: `{"ok":true,"taskId":"task_1"}`},
		{name: "unknown phase", content: `{"ok":true,"taskId":"task_1","phase":"teleporting"}`},
		{name: "degraded", content: `{"ok":true,"taskId":"task_1","phase":"investigating","degraded":true}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registry := &fakeRegistry{
				routes: map[string]string{statusTool: "status"},
				result: &mcp.ToolResult{Content: test.content},
			}
			_, err := NewCortex(registry, "/work", "").Status(context.Background(), "task_1")
			if !errors.Is(err, ErrRejected) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestParseAdviceDetectsPendingDecision(t *testing.T) {
	advice, err := parseAdvice(`{"ok":true,"pendingDecision":{"id":"dec_1"}}`)
	if err != nil || !advice.PendingDecision {
		t.Fatalf("advice = %+v err=%v", advice, err)
	}
}

func TestParseAdvicePreservesBoundedVerificationContext(t *testing.T) {
	advice, err := parseAdvice(`{
		"ok":true,
		"taskId":"task_1",
		"revision":9,
		"phase":"verifying",
		"verificationOutcome":"partial",
		"verificationDone":["go test ./..."],
		"missingVerification":["glyph run"],
		"staleVerification":["old vet receipt"],
		"degraded":true,
		"actions":[{"tool":"cortex_verify","arguments":{"taskId":"task_1"},"blockedBy":["approval"]}]
	}`)
	if err != nil {
		t.Fatal(err)
	}
	if !advice.Degraded || advice.Revision != 9 || advice.VerificationOutcome != "partial" {
		t.Fatalf("advice = %+v", advice)
	}
	if !reflect.DeepEqual(advice.VerificationDone, []string{"go test ./..."}) ||
		!reflect.DeepEqual(advice.MissingVerification, []string{"glyph run"}) ||
		!reflect.DeepEqual(advice.StaleVerification, []string{"old vet receipt"}) {
		t.Fatalf("verification context = %+v", advice)
	}
	if len(advice.Actions) != 1 || advice.Actions[0].Arguments["taskId"] != "task_1" ||
		!reflect.DeepEqual(advice.Actions[0].BlockedBy, []string{"approval"}) {
		t.Fatalf("actions = %#v", advice.Actions)
	}
}

func TestParseAdviceRejectsNegativeRevisionAndBoundsLists(t *testing.T) {
	if _, err := parseAdvice(`{"ok":true,"revision":-1}`); err == nil {
		t.Fatal("negative revision was accepted")
	}

	warnings := make([]string, maxAdviceItems+4)
	for index := range warnings {
		warnings[index] = strings.Repeat("界", maxAdviceTextBytes)
	}
	payload, err := json.Marshal(map[string]any{"ok": true, "warnings": warnings})
	if err != nil {
		t.Fatal(err)
	}
	advice, err := parseAdvice(string(payload))
	if err != nil {
		t.Fatal(err)
	}
	if len(advice.Warnings) != maxAdviceItems {
		t.Fatalf("warnings = %d, want %d", len(advice.Warnings), maxAdviceItems)
	}
	for _, warning := range advice.Warnings {
		if len(warning) > maxAdviceTextBytes || !utf8.ValidString(warning) {
			t.Fatalf("warning was not UTF-8 bounded: bytes=%d valid=%v", len(warning), utf8.ValidString(warning))
		}
	}
}
