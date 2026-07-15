package ecosystem

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

type cortexV0141Fixture struct {
	Payload struct {
		IsError           bool            `json:"isError"`
		JSONText          string          `json:"jsonText"`
		StructuredContent json.RawMessage `json:"structuredContent"`
	} `json:"payload"`
}

func TestMCPHubResultAssemblerPreservesCortexV0141DomainAcrossPages(t *testing.T) {
	tests := []struct {
		name      string
		fixture   string
		tool      string
		want      DomainState
		toolError bool
	}{
		{name: "success", fixture: "mcp_shared_envelope_success.json", tool: "cortex_open_task", want: DomainSucceeded},
		{name: "rejection", fixture: "mcp_shared_envelope_rejection.json", tool: "cortex_begin_change", want: DomainFailed, toolError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := readCortexV0141Fixture(t, test.fixture)
			payload := serializedCallToolResult(t, fixture.Payload.StructuredContent, fixture.Payload.JSONText+strings.Repeat(" bounded", 900), test.toolError)
			assembler := NewMCPHubResultAssembler()
			const callID = "cortex-v0141-paged-result"
			assembler.Observe(storedResultProjection(t, callID, "cortex", test.tool, len(payload)), RawReceipt{})

			final := feedResultPages(t, assembler, callID, payload, 1216)
			if final.Domain != test.want || !final.DomainTyped || final.Evidence != EvidenceNone ||
				final.Digest == nil || final.Digest.Kind != DigestMCPHubPage || !final.Digest.Done {
				t.Fatalf("final projection = %#v, want typed %s with final-page digest", final, test.want)
			}
		})
	}
}

func TestMCPHubResultAssemblerReusesExactBobParser(t *testing.T) {
	structured, err := os.ReadFile("testdata/bob_v040/context-clean-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	payload := serializedCallToolResult(t, structured, strings.Repeat("bob bounded content ", 400), false)
	assembler := NewMCPHubResultAssembler()
	const callID = "bob-v040-paged-result"
	assembler.Observe(storedResultProjection(t, callID, "bob", "bob_context", len(payload)), RawReceipt{})
	final := feedResultPages(t, assembler, callID, payload, 913)
	if final.Domain != DomainSucceeded || !final.DomainTyped || final.Evidence != EvidenceNone {
		t.Fatalf("Bob final projection = %#v", final)
	}
}

func TestMCPHubResultAssemblerFailsClosedOnSequenceAndPayloadDefects(t *testing.T) {
	fixture := readCortexV0141Fixture(t, "mcp_shared_envelope_rejection.json")
	payload := serializedCallToolResult(t, fixture.Payload.StructuredContent, fixture.Payload.JSONText+strings.Repeat(" bounded", 500), true)
	const callID = "cortex-invalid-page-sequence"

	t.Run("out of order", func(t *testing.T) {
		assembler := NewMCPHubResultAssembler()
		assembler.Observe(storedResultProjection(t, callID, "cortex", "cortex_begin_change", len(payload)), RawReceipt{})
		projection, receipt := resultPage(t, callID, payload, 1216, min(2432, len(payload)))
		got := assembler.Observe(projection, receipt)
		if got.Domain != DomainFailed || !got.DomainTyped {
			t.Fatalf("out-of-order projection = %#v", got)
		}
	})

	t.Run("duplicate page", func(t *testing.T) {
		assembler := NewMCPHubResultAssembler()
		assembler.Observe(storedResultProjection(t, callID, "cortex", "cortex_begin_change", len(payload)), RawReceipt{})
		projection, receipt := resultPage(t, callID, payload, 0, 1216)
		if got := assembler.Observe(projection, receipt); got.Domain != DomainAttention {
			t.Fatalf("first page projection = %#v", got)
		}
		if got := assembler.Observe(projection, receipt); got.Domain != DomainFailed || !got.DomainTyped {
			t.Fatalf("duplicate page projection = %#v", got)
		}
	})

	t.Run("reset discards partial bytes", func(t *testing.T) {
		assembler := NewMCPHubResultAssembler()
		assembler.Observe(storedResultProjection(t, callID, "cortex", "cortex_begin_change", len(payload)), RawReceipt{})
		projection, receipt := resultPage(t, callID, payload, 0, 1216)
		if got := assembler.Observe(projection, receipt); got.Domain != DomainAttention {
			t.Fatalf("first page projection = %#v", got)
		}
		assembler.Reset()
		final := feedResultPagesFrom(t, assembler, callID, payload, 1216, 1216)
		if final.Domain != DomainAttention || !final.DomainTyped {
			t.Fatalf("post-reset projection = %#v", final)
		}
	})

	t.Run("unsupported serialized result", func(t *testing.T) {
		unsupported := []byte(`{"content":[{"type":"text","text":"no structured content"}]}`)
		assembler := NewMCPHubResultAssembler()
		assembler.Observe(storedResultProjection(t, callID, "cortex", "cortex_status", len(unsupported)), RawReceipt{})
		got := feedResultPages(t, assembler, callID, unsupported, 32)
		if got.Domain != DomainUnknown || got.DomainTyped || got.Evidence != EvidenceNone {
			t.Fatalf("unsupported result projection = %#v", got)
		}
	})
}

func TestMCPHubResultAssemblerBoundsActiveAndOversizedResults(t *testing.T) {
	assembler := NewMCPHubResultAssembler()
	for index := 0; index < maxMCPHubActiveAssemblies+1; index++ {
		callID := "bounded-result-" + string(rune('a'+index))
		assembler.Observe(storedResultProjection(t, callID, "cortex", "cortex_status", 64), RawReceipt{})
	}
	assembler.mu.Lock()
	if len(assembler.entries) != maxMCPHubActiveAssemblies {
		t.Fatalf("active assemblies = %d, want %d", len(assembler.entries), maxMCPHubActiveAssemblies)
	}
	if _, retained := assembler.entries["bounded-result-a"]; retained {
		t.Fatal("oldest result was not evicted")
	}
	assembler.mu.Unlock()

	assembler.Observe(storedResultProjection(t, "too-large", "cortex", "cortex_status", maxMCPHubAssembledResultBytes+1), RawReceipt{})
	assembler.mu.Lock()
	if _, retained := assembler.entries["too-large"]; retained {
		t.Fatal("oversized result allocated an assembly")
	}
	assembler.mu.Unlock()
}

func TestMCPHubResultAssemblerDiscardsUnavailableResult(t *testing.T) {
	assembler := NewMCPHubResultAssembler()
	const callID = "expired-stored-result"
	assembler.Observe(storedResultProjection(t, callID, "cortex", "cortex_status", 64), RawReceipt{})
	unavailable := ProjectReceipt(ProjectToolCall("mcphub__mcphub_get_result", map[string]any{"callId": callID}), RawReceipt{
		Structured: json.RawMessage(`{"status":"unavailable","callId":"expired-stored-result","reason":"expired"}`),
	})
	assembler.Observe(unavailable, RawReceipt{})
	assembler.mu.Lock()
	_, retained := assembler.entries[callID]
	assembler.mu.Unlock()
	if retained {
		t.Fatal("unavailable result retained a partial assembly")
	}
}

func storedResultProjection(t *testing.T, callID, server, tool string, size int) ToolProjection {
	t.Helper()
	document, err := json.Marshal(map[string]any{
		"status": "stored", "callId": callID, "server": server, "tool": tool,
		"originalBytes": size, "budgetBytes": 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	projection := ProjectReceipt(ProjectToolCall("mcphub__mcphub_call_tool", map[string]any{
		"server": server, "tool": tool,
	}), RawReceipt{Structured: document})
	if projection.Digest == nil || projection.Digest.Kind != DigestMCPHubStored {
		t.Fatalf("stored projection = %#v", projection)
	}
	return projection
}

func feedResultPages(t *testing.T, assembler *MCPHubResultAssembler, callID string, payload []byte, pageSize int) ToolProjection {
	t.Helper()
	return feedResultPagesFrom(t, assembler, callID, payload, pageSize, 0)
}

func feedResultPagesFrom(t *testing.T, assembler *MCPHubResultAssembler, callID string, payload []byte, pageSize, start int) ToolProjection {
	t.Helper()
	var final ToolProjection
	for cursor := start; cursor < len(payload); {
		next := min(cursor+pageSize, len(payload))
		projection, receipt := resultPage(t, callID, payload, cursor, next)
		final = assembler.Observe(projection, receipt)
		if next < len(payload) && final.Domain != DomainAttention {
			t.Fatalf("partial page %d-%d projection = %#v", cursor, next, final)
		}
		cursor = next
	}
	return final
}

func resultPage(t *testing.T, callID string, payload []byte, cursor, next int) (ToolProjection, RawReceipt) {
	t.Helper()
	document, err := json.Marshal(map[string]any{
		"status": "ok", "callId": callID, "mediaType": "application/json",
		"data":   base64.StdEncoding.EncodeToString(payload[cursor:next]),
		"cursor": cursor, "nextCursor": next, "done": next == len(payload), "totalBytes": len(payload),
	})
	if err != nil {
		t.Fatal(err)
	}
	receipt := RawReceipt{Structured: document}
	projection := ProjectReceipt(ProjectToolCall("mcphub__mcphub_get_result", map[string]any{
		"callId": callID, "cursor": cursor,
	}), receipt)
	return projection, receipt
}

func serializedCallToolResult(t *testing.T, structured json.RawMessage, text string, toolError bool) []byte {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"content":           []map[string]any{{"type": "text", "text": text}},
		"structuredContent": structured, "isError": toolError,
	})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func readCortexV0141Fixture(t *testing.T, name string) cortexV0141Fixture {
	t.Helper()
	raw, err := os.ReadFile("testdata/cortex_v0141/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var fixture cortexV0141Fixture
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	return fixture
}
