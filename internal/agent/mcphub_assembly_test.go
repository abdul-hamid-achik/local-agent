package agent

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/local-agent/internal/config"
	"github.com/abdul-hamid-achik/local-agent/internal/ecosystem"
	"github.com/abdul-hamid-achik/local-agent/internal/llm"
)

func TestAgentReassemblesOnlyExactTrustedMCPHubResults(t *testing.T) {
	fixtureRaw, err := os.ReadFile("../ecosystem/testdata/cortex_v0141/mcp_shared_envelope_rejection.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Payload struct {
			IsError           bool            `json:"isError"`
			JSONText          string          `json:"jsonText"`
			StructuredContent json.RawMessage `json:"structuredContent"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(fixtureRaw, &fixture); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]any{
		"content":           []map[string]any{{"type": "text", "text": fixture.Payload.JSONText + strings.Repeat(" bounded", 900)}},
		"structuredContent": fixture.Payload.StructuredContent,
		"isError":           fixture.Payload.IsError,
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name       string
		storedCall string
		want       ecosystem.DomainState
	}{
		{name: "trusted gateway route", storedCall: "mcphub__cortex__cortex_status", want: ecosystem.DomainFailed},
		{name: "untrusted gateway lookalike", storedCall: "evil__cortex__cortex_status", want: ecosystem.DomainAttention},
	} {
		t.Run(test.name, func(t *testing.T) {
			ag := New(nil, nil, 8192)
			ag.SetTrustedLocalMCPServers([]config.ServerConfig{{Name: "mcphub", Command: "mcphub"}})
			const callID = "trusted-cortex-paged-rejection"
			storedDocument, err := json.Marshal(map[string]any{
				"status": "stored", "callId": callID, "server": "cortex", "tool": "cortex_status",
				"originalBytes": len(payload), "budgetBytes": 4096,
			})
			if err != nil {
				t.Fatal(err)
			}
			storedCall := llm.ToolCall{Name: test.storedCall}
			storedProjection := projectSemanticToolReceipt(storedCall.Name, nil, "", storedDocument, nil, false, false, false)
			storedProjection = ag.projectMCPHubResultAssembly(storedCall, storedProjection, storedDocument, false)
			if storedProjection.Domain != ecosystem.DomainAttention {
				t.Fatalf("stored projection = %#v", storedProjection)
			}

			var final ecosystem.ToolProjection
			for cursor := 0; cursor < len(payload); {
				next := min(cursor+1216, len(payload))
				pageDocument, err := json.Marshal(map[string]any{
					"status": "ok", "callId": callID, "mediaType": "application/json",
					"data":   base64.StdEncoding.EncodeToString(payload[cursor:next]),
					"cursor": cursor, "nextCursor": next, "done": next == len(payload), "totalBytes": len(payload),
				})
				if err != nil {
					t.Fatal(err)
				}
				pageCall := llm.ToolCall{Name: "mcphub__mcphub_get_result", Arguments: map[string]any{"callId": callID, "cursor": cursor}}
				final = projectSemanticToolReceipt(pageCall.Name, pageCall.Arguments, "", pageDocument, nil, false, false, false)
				final = ag.projectMCPHubResultAssembly(pageCall, final, pageDocument, false)
				cursor = next
			}
			if final.Domain != test.want || !final.DomainTyped || final.Evidence != ecosystem.EvidenceNone {
				t.Fatalf("final projection = %#v, want typed %s", final, test.want)
			}
		})
	}
}
