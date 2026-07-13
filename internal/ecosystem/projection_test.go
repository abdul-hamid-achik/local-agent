package ecosystem

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProjectToolCallPreservesLazyMCPHubRouteWithoutArguments(t *testing.T) {
	projection := ProjectToolCall("mcphub__mcphub_call_tool", map[string]any{
		"server": "cortex",
		"tool":   "cortex__cortex_investigate",
		"arguments": map[string]any{
			"query": "SECRET QUERY MUST NOT PERSIST",
		},
	})
	if projection.Specialist != "cortex" || projection.Role != RoleCoordination {
		t.Fatalf("specialist projection = %#v", projection)
	}
	if projection.Route.Gateway != "mcphub" || projection.Route.Server != "cortex" || projection.Route.Tool != "cortex_investigate" || !projection.Route.Lazy {
		t.Fatalf("route = %#v", projection.Route)
	}
	if strings.Contains(strings.ToLower(projection.Operation), "secret") {
		t.Fatalf("projection retained arbitrary arguments: %#v", projection)
	}
}

func TestProjectToolCallUnderstandsNamespacedDirectAndGatewayTools(t *testing.T) {
	tests := []struct {
		name       string
		specialist string
		gateway    string
		server     string
		operation  string
	}{
		{"bob__bob_check", "bob", "", "bob", "bob_check"},
		{"mcphub__cortex__cortex_start_task", "cortex", "mcphub", "cortex", "cortex_start_task"},
		{"vecgrep_search", "vecgrep", "", "vecgrep", "vecgrep_search"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := ProjectToolCall(test.name, nil)
			if got.Specialist != test.specialist || got.Route.Gateway != test.gateway || got.Route.Server != test.server || got.Operation != test.operation {
				t.Fatalf("projection = %#v", got)
			}
		})
	}
}

func TestProjectToolResultSeparatesBobDomainFromTransport(t *testing.T) {
	projection := ProjectToolCall("bob__bob_check", nil)
	result := `{
		"schema_version":1,
		"ok":true,
		"command":"check",
		"data":{"clean":false,"plan":{"actions":[{"path":"README.md","code":"content_update"}]}},
		"warnings":[],
		"next_actions":["bob apply"]
	}`
	got := ProjectToolResult(projection, result, false)
	if got.Transport != TransportSucceeded || got.Domain != DomainDrift || !got.NeedsAttention() || got.Successful() {
		t.Fatalf("drift projection = %#v", got)
	}

	got = ProjectToolResult(projection, "not a stable envelope", false)
	if got.Transport != TransportSucceeded || got.Domain != DomainUnknown || !got.NeedsAttention() {
		t.Fatalf("unknown Bob projection = %#v", got)
	}
}

func TestProjectReceiptPrefersStructuredBobMCPContract(t *testing.T) {
	projection := ProjectToolCall("bob__bob_check", nil)
	got := ProjectReceipt(projection, RawReceipt{
		Text:       "human summary that must not be parsed",
		Structured: json.RawMessage(`{"schema_version":1,"ok":true,"clean":false,"lock_changed":false,"conflict_count":2}`),
		ToolError:  false,
	})
	if got.Transport != TransportSucceeded || got.Domain != DomainConflict || !got.NeedsAttention() {
		t.Fatalf("structured Bob projection = %#v", got)
	}
}

func TestProjectReceiptParsesVersionedVerifierOutcomes(t *testing.T) {
	tests := []struct {
		name       string
		structured string
		toolError  bool
		domain     DomainState
		evidence   EvidenceState
	}{
		{
			name: "glyphrun__glyph_run", toolError: true,
			structured: `{"schemaVersion":1,"runId":"run-1","specName":"smoke","status":"failed","startedAt":"2026-07-13T00:00:00Z","endedAt":"2026-07-13T00:00:01Z","durationMs":1000,"target":{"cmd":["task","dev"]},"terminal":{"cols":80,"rows":24,"profile":"xterm-256color"},"outcomes":[{"id":"launch","status":"failed"}],"artifacts":{},"runDir":"/tmp/run-1","exitCode":1}`,
			domain:     DomainFailed, evidence: EvidenceContradicted,
		},
		{
			name:       "cairntrace__cairn_run",
			structured: `{"$schema":"urn:cairntrace.dev:run:v1","version":"1","runId":"run-1","runDir":"/tmp/run-1","spec":{"name":"smoke","path":"/tmp/smoke.yml"},"environment":"test","backend":"playwright","coldStart":true,"status":"passed","startedAt":"2026-07-13T00:00:00Z","endedAt":"2026-07-13T00:00:01Z","durationMs":1000,"outcomes":[{"id":"launch","status":"passed"}],"steps":[],"artifacts":{"agentContext":"agent-context.md","events":"events.ndjson"},"exitCode":0}`,
			domain:     DomainSucceeded, evidence: EvidenceVerified,
		},
		{
			name:       "cairntrace__cairn_run",
			structured: `{"$schema":"urn:cairntrace.dev:run:v2","version":"2","status":"passed"}`,
			domain:     DomainUnknown, evidence: EvidenceNone,
		},
	}
	for _, test := range tests {
		t.Run(test.name+"/"+string(test.domain), func(t *testing.T) {
			got := ProjectReceipt(ProjectToolCall(test.name, nil), RawReceipt{
				Structured: json.RawMessage(test.structured), ToolError: test.toolError,
			})
			if got.Transport != TransportSucceeded || got.Domain != test.domain || got.Evidence != test.evidence {
				t.Fatalf("verifier projection = %#v", got)
			}
		})
	}
}

func TestProjectReceiptToolErrorCannotBecomeVerifiedOrSuccessful(t *testing.T) {
	passedGlyph := `{"schemaVersion":1,"runId":"run-1","specName":"smoke","status":"passed","startedAt":"2026-07-13T00:00:00Z","endedAt":"2026-07-13T00:00:01Z","durationMs":1000,"target":{"cmd":["task","dev"]},"terminal":{"cols":80,"rows":24,"profile":"xterm-256color"},"outcomes":[{"id":"launch","status":"passed"}],"artifacts":{},"runDir":"/tmp/run-1","exitCode":0}`
	got := ProjectReceipt(ProjectToolCall("glyphrun__glyph_run", nil), RawReceipt{
		Structured: json.RawMessage(passedGlyph), ToolError: true,
	})
	if got.Successful() || got.Domain != DomainFailed || got.Evidence == EvidenceVerified {
		t.Fatalf("tool error was promoted by optimistic verifier envelope: %#v", got)
	}

	stored := ProjectReceipt(ProjectToolCall("mcphub__mcphub_call_tool", map[string]any{
		"server": "cortex", "tool": "cortex_verify",
	}), RawReceipt{
		Structured: json.RawMessage(`{"status":"stored","callId":"call-123"}`), ToolError: true,
	})
	if stored.Successful() || stored.Domain == DomainSucceeded || stored.Evidence == EvidenceVerified {
		t.Fatalf("tool error was promoted by stored gateway receipt: %#v", stored)
	}
}

func TestProjectReceiptRejectsIncompleteVersionedVerifierEnvelopes(t *testing.T) {
	tests := []struct {
		name       string
		structured string
	}{
		{name: "glyph", structured: `{"schemaVersion":1,"status":"passed","exitCode":0}`},
		{name: "cairntrace", structured: `{"$schema":"urn:cairntrace.dev:run:v1","version":"1","status":"passed","exitCode":0}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := ProjectReceipt(ProjectToolCall(test.name+"__"+test.name+"_run", nil), RawReceipt{
				Structured: json.RawMessage(test.structured),
			})
			if got.Domain != DomainUnknown || got.Evidence != EvidenceNone || got.Successful() {
				t.Fatalf("incomplete envelope was trusted: %#v", got)
			}
		})
	}
}

func TestProjectReceiptProjectsStaleCodemapAndStoredMCPHubResults(t *testing.T) {
	codemap := ProjectReceipt(ProjectToolCall("codemap__codemap_status", nil), RawReceipt{
		Structured: json.RawMessage(`{"registered":true,"indexed":true,"stale":{"changed":1,"new":0,"deleted":0}}`),
	})
	if codemap.Domain != DomainAttention || codemap.Evidence != EvidenceStale {
		t.Fatalf("codemap projection = %#v", codemap)
	}

	hub := ProjectReceipt(ProjectToolCall("mcphub__mcphub_call_tool", map[string]any{
		"server": "cortex", "tool": "cortex_verify",
	}), RawReceipt{Structured: json.RawMessage(`{"status":"stored","callId":"call-123","originalBytes":90000}`)})
	if hub.Domain != DomainAttention || hub.Route.CallID != "call-123" || hub.Evidence != EvidenceNone {
		t.Fatalf("stored MCPHub projection = %#v", hub)
	}
}

func TestProjectReceiptTreatsTypedMCPErrorMetaAsDomainFailure(t *testing.T) {
	got := ProjectReceipt(ProjectToolCall("codemap__codemap_status", nil), RawReceipt{
		Text:      "status could not be loaded",
		ErrorMeta: json.RawMessage(`{"code":"index_unavailable","message":"missing"}`),
		ToolError: true,
	})
	if got.Transport != TransportSucceeded || got.Domain != DomainFailed || got.Evidence != EvidenceNone {
		t.Fatalf("typed MCP error projection = %#v", got)
	}
}

func TestProjectToolResultUsesEvidenceLadderWithoutInventingVerification(t *testing.T) {
	tests := []struct {
		name     string
		domain   DomainState
		evidence EvidenceState
	}{
		{"vecgrep__vecgrep_search", DomainUnknown, EvidenceNone},
		{"codemap__codemap_query", DomainUnknown, EvidenceNone},
		{"glyphrun__glyphrun_run", DomainUnknown, EvidenceNone},
		{"cairntrace__cairn_run", DomainUnknown, EvidenceNone},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := ProjectToolResult(ProjectToolCall(test.name, nil), `{}`, false)
			if got.Domain != test.domain || got.Evidence != test.evidence {
				t.Fatalf("projection = %#v", got)
			}
		})
	}
}

func TestProjectionNormalizeBoundsAndRejectsUnknownEnums(t *testing.T) {
	projection := (ToolProjection{
		Specialist: strings.Repeat("x", 300) + "\x1b]52;c;payload",
		Operation:  "RUN BAD\nOP",
		Role:       "invented",
		Transport:  "invented",
		Domain:     "invented",
		Evidence:   "invented",
		Route:      ToolRoute{CallID: strings.Repeat("z", 300)},
	}).Normalize()
	if len(projection.Specialist) > maxProjectionIdentifierBytes || len(projection.Route.CallID) > maxProjectionIdentifierBytes {
		t.Fatalf("projection identifiers were not bounded: %#v", projection)
	}
	if projection.Role != RoleGeneral || projection.Transport != TransportSucceeded || projection.Domain != DomainUnknown || projection.Evidence != EvidenceNone {
		t.Fatalf("unknown enums survived normalization: %#v", projection)
	}
	if strings.ContainsAny(projection.Operation, "\n\x1b") {
		t.Fatalf("unsafe operation survived: %q", projection.Operation)
	}
}

func TestProjectionIdentifierBoundsRemainValidUTF8(t *testing.T) {
	projection := ProjectToolCall(strings.Repeat("界", maxProjectionIdentifierBytes)+"__glyph_run", nil)
	for name, value := range map[string]string{
		"specialist": projection.Specialist,
		"operation":  projection.Operation,
		"server":     projection.Route.Server,
		"tool":       projection.Route.Tool,
	} {
		if len(value) > maxProjectionIdentifierBytes || !json.Valid([]byte(`"`+value+`"`)) {
			t.Fatalf("%s was not UTF-8 bounded: %q (%d bytes)", name, value, len(value))
		}
	}
}
