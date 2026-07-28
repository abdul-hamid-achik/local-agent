package ecosystem

import (
	"encoding/json"
	"testing"
)

func TestBobRecipeRefAcceptsGoAgentToolV5(t *testing.T) {
	for version, want := range map[int]bool{2: false, 3: true, 4: true, 5: true, 6: false} {
		raw, _ := json.Marshal(map[string]any{"id": "go-agent-tool", "version": version})
		if _, ok := validBobRecipeRef(raw); ok != want {
			t.Fatalf("go-agent-tool v%d accepted=%v, want %v", version, ok, want)
		}
	}
	stack, _ := json.Marshal(map[string]any{"id": "ts-app", "version": 2})
	if _, ok := validBobRecipeRef(stack); ok {
		t.Fatal("stack recipes have no manifest validator yet and must stay unrecognized")
	}
}

func TestBobQualifiedPlanDigestBindsToLegacyDigest(t *testing.T) {
	digest := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if !validBobQualifiedPlanDigest("", digest) {
		t.Fatal("absent qualified digest must stay valid for older bob releases")
	}
	if !validBobQualifiedPlanDigest("sha256:"+digest, digest) {
		t.Fatal("matching qualified digest must be accepted")
	}
	if validBobQualifiedPlanDigest("sha256:"+digest, "f"+digest[1:]) {
		t.Fatal("a qualified digest disagreeing with the legacy field must fail closed")
	}
	if validBobQualifiedPlanDigest(digest, digest) {
		t.Fatal("an unprefixed qualified digest is not the documented spelling")
	}
}

func TestCortexHandoffProjection(t *testing.T) {
	handoff := ProjectReceipt(ProjectToolCall("cortex__cortex_handoff", nil), RawReceipt{
		Structured: json.RawMessage(`{"schemaVersion":1,"generatedAt":"2026-07-27T00:00:00Z","taskId":"task_a1","revision":8,"goal":"repair callback contract","phase":"complete","mode":"change","risk":"low","actor":"agent-a"}`),
	})
	if handoff.Domain != DomainSucceeded || !handoff.DomainTyped || handoff.Evidence != EvidenceNone {
		t.Fatalf("handoff projection = %+v", handoff)
	}
	if handoff.Digest == nil || handoff.Digest.Kind != DigestCortexReceipt || handoff.Digest.Target != "task_a1" {
		t.Fatalf("handoff digest = %+v", handoff.Digest)
	}

	wrongVersion := ProjectReceipt(ProjectToolCall("cortex__cortex_handoff", nil), RawReceipt{
		Structured: json.RawMessage(`{"schemaVersion":2,"taskId":"task_a1","revision":8,"phase":"complete"}`),
	})
	if wrongVersion.Domain != DomainUnknown {
		t.Fatalf("future handoff schema must fail closed, got %+v", wrongVersion)
	}

	missingPhase := ProjectReceipt(ProjectToolCall("cortex__cortex_handoff", nil), RawReceipt{
		Structured: json.RawMessage(`{"schemaVersion":1,"taskId":"task_a1","revision":8}`),
	})
	if missingPhase.Domain != DomainUnknown {
		t.Fatalf("incomplete handoff must fail closed, got %+v", missingPhase)
	}
}

func TestMCPHubDetachedCallLifecycle(t *testing.T) {
	accepted := ProjectReceipt(ProjectToolCall("mcphub__mcphub_call_tool", map[string]any{
		"server": "hitspec", "tool": "capture_webpage",
	}), RawReceipt{
		Structured: json.RawMessage(`{"status":"accepted","callId":"call_9f","server":"hitspec","tool":"capture_webpage","namespaced":"hitspec__capture_webpage","timeoutMs":600000,"nextAction":"mcphub_poll_result"}`),
	})
	if accepted.Domain != DomainPending || !accepted.DomainTyped {
		t.Fatalf("accepted projection = %+v", accepted)
	}
	if accepted.Digest == nil || accepted.Digest.Kind != DigestMCPHubDetached || accepted.Digest.State != "accepted" {
		t.Fatalf("accepted digest = %+v", accepted.Digest)
	}
	if accepted.Route.CallID != "call_9f" {
		t.Fatalf("accepted route = %+v", accepted.Route)
	}

	pending := ProjectReceipt(ProjectToolCall("mcphub__mcphub_poll_result", map[string]any{"callId": "call_9f"}), RawReceipt{
		Structured: json.RawMessage(`{"status":"pending","callId":"call_9f","namespaced":"hitspec__capture_webpage","elapsedMs":1500,"hint":"poll again"}`),
	})
	if pending.Domain != DomainPending || pending.Digest == nil || pending.Digest.State != "pending" {
		t.Fatalf("pending projection = %+v digest=%+v", pending, pending.Digest)
	}

	failed := ProjectReceipt(ProjectToolCall("mcphub__mcphub_poll_result", map[string]any{"callId": "call_9f"}), RawReceipt{
		Structured: json.RawMessage(`{"status":"failed","callId":"call_9f","namespaced":"hitspec__capture_webpage","error":"downstream exited","elapsedMs":9000}`),
	})
	if failed.Domain != DomainFailed || failed.Digest == nil || failed.Digest.State != "failed" {
		t.Fatalf("failed projection = %+v digest=%+v", failed, failed.Digest)
	}

	lost := ProjectReceipt(ProjectToolCall("mcphub__mcphub_poll_result", map[string]any{"callId": "call_gone"}), RawReceipt{
		Structured: json.RawMessage(`{"status":"unknown","callId":"call_gone","reason":"call id not found"}`),
	})
	if lost.Domain != DomainAttention || lost.Digest == nil || lost.Digest.State != "unknown" {
		t.Fatalf("unknown projection = %+v digest=%+v", lost, lost.Digest)
	}
}

func TestMCPHubDetachedEnvelopeFailsClosed(t *testing.T) {
	// An acceptance without a positive timeout is not the published contract.
	badTimeout := ProjectReceipt(ProjectToolCall("mcphub__mcphub_call_tool", map[string]any{
		"server": "hitspec", "tool": "capture_webpage",
	}), RawReceipt{
		Structured: json.RawMessage(`{"status":"accepted","callId":"call_9f","timeoutMs":0}`),
	})
	if badTimeout.Digest != nil && badTimeout.Digest.Kind == DigestMCPHubDetached {
		t.Fatalf("invalid acceptance must not produce a detached digest: %+v", badTimeout)
	}

	// Poll-state envelopes are only trusted on the exact management route,
	// never when echoed by an arbitrary downstream tool.
	echoed := ProjectReceipt(ProjectToolCall("hitspec__hitspec_fetch", nil), RawReceipt{
		Structured: json.RawMessage(`{"status":"pending","callId":"call_9f","elapsedMs":10}`),
	})
	if echoed.Digest != nil && echoed.Digest.Kind == DigestMCPHubDetached {
		t.Fatalf("downstream echo must not gain the detached parser: %+v", echoed)
	}
}
