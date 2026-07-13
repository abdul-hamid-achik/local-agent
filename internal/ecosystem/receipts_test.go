package ecosystem

import (
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

func TestMCPHubManagementReceiptsExposeOnlyBoundedDigests(t *testing.T) {
	const secret = "SECRET_ARBITRARY_SERVER_TEXT"
	tests := []struct {
		name       string
		tool       string
		structured string
		kind       ReceiptDigestKind
		want       string
	}{
		{
			name: "servers", tool: "mcphub__mcphub_list_servers", kind: DigestMCPHubServers,
			structured: `{"servers":[{"name":"cortex","connected":true,"description":"` + secret + `"},{"name":"bob","connected":false}],"total_tools":130,"expose":"lazy","pinned":["` + secret + `"]}`,
			want:       "2 servers · 1 connected · 130 tools · lazy exposure · cortex, bob",
		},
		{
			name: "search", tool: "mcphub__mcphub_search_tools", kind: DigestMCPHubSearch,
			structured: `{"query":"` + secret + `","count":2,"matches":[{"namespaced":"cortex__cortex_status","description":"` + secret + `"},{"namespaced":"bob__bob_check","description":"` + secret + `"}]}`,
			want:       "2 matches · cortex__cortex_status, bob__bob_check",
		},
		{
			name: "describe", tool: "mcphub__mcphub_describe_tool", kind: DigestMCPHubDescribe,
			structured: `{"server":"cortex","tool":"cortex_plan","namespaced":"cortex__cortex_plan","description":"` + secret + `","input_schema":{"type":"object","required":["workspace","prompt"],"properties":{"prompt":{"description":"` + secret + `"}}}}`,
			want:       "cortex__cortex_plan · requires workspace, prompt",
		},
		{
			name: "resolve", tool: "mcphub__mcphub_resolve_tool", kind: DigestMCPHubResolve,
			structured: `{"query":"` + secret + `","recommendation":{"namespaced":"cortex__cortex_plan","description":"` + secret + `","required_fields":["workspace"],"argument_template":{"workspace":"` + secret + `"}},"ambiguous":true,"alternatives":[{"namespaced":"cortex__cortex_investigate","description":"` + secret + `"}]}`,
			want:       "recommended cortex__cortex_plan · ambiguous · requires workspace · 1 alternative",
		},
		{
			name: "stats", tool: "mcphub__mcphub_stats", kind: DigestMCPHubStats,
			structured: `{"totals":{"calls":12,"errors":2,"est_tokens":900},"servers":[{"server":"cortex","error":"` + secret + `"},{"server":"bob"}]}`,
			want:       "12 calls · 2 errors · ~900 est. tokens · 2 servers",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := ProjectReceipt(ProjectToolCall(test.tool, nil), RawReceipt{Structured: json.RawMessage(test.structured)})
			if got.Transport != TransportSucceeded || got.Domain != DomainSucceeded || got.Digest == nil || got.Digest.Kind != test.kind {
				t.Fatalf("projection = %#v", got)
			}
			if summary := got.SummaryText(); summary != test.want {
				t.Fatalf("summary = %q, want %q", summary, test.want)
			}
			assertProjectionOmits(t, got, secret)
		})
	}
}

func TestMCPHubStoredAndPagedReceiptsSeparateTransientPayload(t *testing.T) {
	const (
		callID = "3f9a1c2e7b804d5e9f1a2b3c4d5e6f70"
		secret = "SECRET_RESULT_PAGE_PAYLOAD"
	)
	stored := ProjectReceipt(ProjectToolCall("mcphub__mcphub_call_tool", map[string]any{
		"server": "cortex", "tool": "cortex_status",
	}), RawReceipt{Structured: json.RawMessage(`{
		"status":"stored","callId":"` + callID + `","server":"cortex","tool":"cortex_status",
		"originalBytes":40960,"budgetBytes":32768,"preview":"` + secret + `","nextAction":"` + secret + `"
	}`)})
	if stored.Domain != DomainAttention || stored.Route.CallID != callID || stored.Digest == nil || stored.Digest.Kind != DigestMCPHubStored {
		t.Fatalf("stored projection = %#v", stored)
	}
	assertProjectionOmits(t, stored, secret)

	payload := []byte(`{"content":[{"type":"text","text":"` + secret + `"}]}`)
	pageDocument := `{"status":"ok","callId":"` + callID + `","mediaType":"application/json","data":"` +
		base64.StdEncoding.EncodeToString(payload) + `","cursor":0,"nextCursor":` + strconv.Itoa(len(payload)) +
		`,"done":true,"totalBytes":` + strconv.Itoa(len(payload)) + `}`
	page := ProjectReceipt(ProjectToolCall("mcphub__mcphub_get_result", map[string]any{
		"callId": callID, "cursor": 0,
	}), RawReceipt{Structured: json.RawMessage(pageDocument)})
	if page.Domain != DomainSucceeded || page.Digest == nil || page.Digest.Kind != DigestMCPHubPage || !page.Digest.Done {
		t.Fatalf("page projection = %#v", page)
	}
	assertProjectionOmits(t, page, secret)

	transient, ok := TransientModelContent(page, RawReceipt{Structured: json.RawMessage(pageDocument)})
	if !ok || !strings.Contains(transient, secret) || !strings.Contains(transient, "transient; not saved") {
		t.Fatalf("transient page = %q, ok=%v", transient, ok)
	}
	if strings.Contains(SafeReceiptText(page), secret) {
		t.Fatal("safe receipt retained page payload")
	}
}

func TestMCPHubResultPageRejectsMismatchedAndOversizedPayloads(t *testing.T) {
	const callID = "3f9a1c2e7b804d5e9f1a2b3c4d5e6f70"
	projection := ProjectToolCall("mcphub__mcphub_get_result", map[string]any{"callId": callID})

	mismatch := ProjectReceipt(projection, RawReceipt{Structured: json.RawMessage(`{
		"status":"ok","callId":"another-id","mediaType":"application/json","data":"e30=",
		"cursor":0,"nextCursor":2,"done":true,"totalBytes":2
	}`)})
	if mismatch.Domain != DomainFailed || mismatch.Route.CallID != callID || mismatch.Digest == nil || mismatch.Digest.Kind != DigestMCPHubError {
		t.Fatalf("mismatch projection = %#v", mismatch)
	}

	payload := make([]byte, maxMCPHubResultPageBytes+1)
	oversizedDocument := `{"status":"ok","callId":"` + callID + `","mediaType":"application/json","data":"` +
		base64.StdEncoding.EncodeToString(payload) + `","cursor":0,"nextCursor":` + strconv.Itoa(len(payload)) +
		`,"done":true,"totalBytes":` + strconv.Itoa(len(payload)) + `}`
	oversized := ProjectReceipt(projection, RawReceipt{Structured: json.RawMessage(oversizedDocument)})
	if oversized.Domain != DomainUnknown || oversized.Digest != nil {
		t.Fatalf("oversized projection = %#v", oversized)
	}
	if transient, ok := TransientModelContent(oversized, RawReceipt{Structured: json.RawMessage(oversizedDocument)}); ok || transient != "" {
		t.Fatalf("oversized transient = %q, ok=%v", transient, ok)
	}
}

func TestCortexFailureReceiptPersistsNoArbitraryErrorText(t *testing.T) {
	const secret = "SECRET_CORTEX_ERROR_PROSE"
	failed := ProjectReceipt(ProjectToolCall("mcphub__cortex__cortex_status", nil), RawReceipt{
		Structured: json.RawMessage(`{"ok":false,"taskId":"task-123","error":"` + secret + `","summary":"` + secret + `"}`),
	})
	if failed.Domain != DomainFailed || failed.Evidence != EvidenceNone || failed.Digest == nil || failed.Digest.Kind != DigestCortexFailure || failed.Digest.Target != "task-123" {
		t.Fatalf("failure projection = %#v", failed)
	}
	assertProjectionOmits(t, failed, secret)

	succeeded := ProjectReceipt(ProjectToolCall("mcphub__cortex__cortex_status", nil), RawReceipt{
		Structured: json.RawMessage(`{"ok":true,"taskId":"task-123","summary":"` + secret + `"}`),
	})
	if succeeded.Domain != DomainUnknown || succeeded.Evidence != EvidenceNone || succeeded.Digest != nil {
		t.Fatalf("optimistic Cortex receipt was promoted: %#v", succeeded)
	}
}

func assertProjectionOmits(t *testing.T, projection ToolProjection, forbidden string) {
	t.Helper()
	encoded, err := json.Marshal(projection)
	if err != nil {
		t.Fatal(err)
	}
	combined := string(encoded) + "\n" + SafeReceiptText(projection)
	if strings.Contains(combined, forbidden) {
		t.Fatalf("projection leaked %q: %s", forbidden, combined)
	}
}
