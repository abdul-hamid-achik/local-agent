package agent

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/local-agent/internal/config"
	"github.com/abdul-hamid-achik/local-agent/internal/ecosystem"
	"github.com/abdul-hamid-achik/local-agent/internal/llm"
)

type semanticOutputRecorder struct {
	projection ecosystem.ToolProjection
	result     string
}

func TestSemanticToolContentsFeedsValidatedResultPageOnlyToActiveModel(t *testing.T) {
	const (
		callID = "3f9a1c2e7b804d5e9f1a2b3c4d5e6f70"
		secret = "SECRET_TRANSIENT_PAGE_CONTENT"
	)
	payload := []byte(`{"content":[{"type":"text","text":"` + secret + `"}]}`)
	structured := json.RawMessage(fmt.Sprintf(
		`{"status":"ok","callId":%q,"mediaType":"application/json","data":%q,"cursor":0,"nextCursor":%d,"done":true,"totalBytes":%d}`,
		callID, base64.StdEncoding.EncodeToString(payload), len(payload), len(payload),
	))
	projection := projectSemanticToolReceipt(
		"mcphub__mcphub_get_result", map[string]any{"callId": callID, "cursor": 0},
		"outer MCP text", structured, nil, false, false, false,
	)

	ag := New(nil, nil, 8192)
	ag.SetTrustedLocalMCPServers([]config.ServerConfig{{Name: "mcphub", Command: "mcphub"}})
	call := llm.ToolCall{Name: "mcphub__mcphub_get_result", Arguments: map[string]any{"callId": callID, "cursor": 0}}
	modelResult, durableResult := ag.semanticToolContents(call, projection, "outer MCP text", structured, false)
	if !strings.Contains(modelResult, secret) || !strings.Contains(modelResult, "transient; not saved") {
		t.Fatalf("active model result = %q", modelResult)
	}
	if strings.Contains(durableResult, secret) || durableResult != ecosystem.SafeReceiptText(projection) {
		t.Fatalf("durable result = %q", durableResult)
	}
	safe := SanitizeMessagesForPersistence([]llm.Message{{
		Role: "tool", Content: modelResult, DurableContent: durableResult,
	}})
	if strings.Contains(safe[0].Content, secret) || safe[0].Content != durableResult {
		t.Fatalf("persisted result = %#v", safe[0])
	}
}

func TestSemanticToolContentsFeedsOnlyExactTrustedSuccessfulCortexResults(t *testing.T) {
	const secret = "SECRET_TRUSTED_CORTEX_RESULT"
	structured := json.RawMessage(`{"ok":true,"taskId":"task-123","summary":"` + secret + `","rawAvailable":false}`)
	ag := New(nil, nil, 8192)
	ag.SetTrustedLocalMCPServers([]config.ServerConfig{
		{Name: "cortex", Command: "cortex"},
		{Name: "mcphub", Command: "/opt/homebrew/bin/mcphub"},
	})

	tests := []struct {
		name string
		call llm.ToolCall
	}{
		{name: "direct", call: llm.ToolCall{Name: "cortex__cortex_status"}},
		{name: "pinned mcphub", call: llm.ToolCall{Name: "mcphub__cortex__cortex_investigate"}},
		{name: "lazy mcphub", call: llm.ToolCall{
			Name: "mcphub__mcphub_call_tool",
			Arguments: map[string]any{
				"server": "cortex", "tool": "cortex_plan", "arguments": map[string]any{"taskId": "task-123"},
			},
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			projection := projectSemanticToolReceipt(test.call.Name, test.call.Arguments, "text", structured, nil, false, false, false)
			modelResult, durableResult := ag.semanticToolContents(test.call, projection, string(structured), structured, false)
			if !strings.Contains(modelResult, secret) || !strings.Contains(modelResult, "transient; not saved") {
				t.Fatalf("model result = %q", modelResult)
			}
			if strings.Contains(durableResult, secret) || durableResult != ecosystem.SafeReceiptText(projection) {
				t.Fatalf("durable result = %q", durableResult)
			}
			safe := SanitizeMessagesForPersistence([]llm.Message{{Content: modelResult, DurableContent: durableResult}})
			if strings.Contains(safe[0].Content, secret) || safe[0].Content != durableResult {
				t.Fatalf("persisted result = %#v", safe[0])
			}
		})
	}
}

func TestSemanticToolContentsRejectsCortexSpoofsErrorsAndUntrustedManagementContent(t *testing.T) {
	const secret = "SECRET_UNTRUSTED_STRUCTURED_RESULT"
	ag := New(nil, nil, 8192)
	ag.SetTrustedLocalMCPServers([]config.ServerConfig{
		{Name: "cortex", Command: "cortex"},
		{Name: "mcphub", Command: "mcphub"},
		{Name: "remote", Command: "cortex", Transport: "sse", URL: "https://example.test/mcp"},
	})

	tests := []struct {
		name       string
		call       llm.ToolCall
		structured json.RawMessage
		toolError  bool
	}{
		{
			name: "untrusted suffix spoof", call: llm.ToolCall{Name: "evil__cortex_status"},
			structured: json.RawMessage(`{"ok":true,"summary":"` + secret + `"}`),
		},
		{
			name: "remote cortex", call: llm.ToolCall{Name: "remote__cortex_status"},
			structured: json.RawMessage(`{"ok":true,"summary":"` + secret + `"}`),
		},
		{
			name: "explicit rejection", call: llm.ToolCall{Name: "cortex__cortex_status"},
			structured: json.RawMessage(`{"ok":false,"summary":"` + secret + `","error":"rejected"}`),
		},
		{
			name: "tool error despite optimistic body", call: llm.ToolCall{Name: "cortex__cortex_status"}, toolError: true,
			structured: json.RawMessage(`{"ok":true,"summary":"` + secret + `"}`),
		},
		{
			name: "untrusted mcphub search suffix", call: llm.ToolCall{Name: "evil__mcphub_search_tools"},
			structured: json.RawMessage(`{"query":"` + secret + `","count":1,"matches":[{"namespaced":"cortex__cortex_status","description":"` + secret + `"}]}`),
		},
		{
			name: "oversized cortex", call: llm.ToolCall{Name: "cortex__cortex_status"},
			structured: json.RawMessage(`{"ok":true,"summary":"` + strings.Repeat("x", maxTransientCortexResultBytes) + `"}`),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			projection := projectSemanticToolReceipt(test.call.Name, test.call.Arguments, "text", test.structured, nil, false, test.toolError, false)
			modelResult, durableResult := ag.semanticToolContents(test.call, projection, string(test.structured), test.structured, test.toolError)
			if modelResult != durableResult || strings.Contains(modelResult, secret) {
				t.Fatalf("untrusted content crossed model boundary: model=%q durable=%q", modelResult, durableResult)
			}
		})
	}
}

func TestSemanticToolContentsSanitizesTrustedMCPHubDescribeContract(t *testing.T) {
	const (
		secret              = "SECRET_SCHEMA_PROSE"
		contractDescription = "Provide exactly one of url or file at runtime."
	)
	call := llm.ToolCall{
		Name:      "mcphub__mcphub_describe_tool",
		Arguments: map[string]any{"server": "builder", "tool": "build"},
	}
	structured := json.RawMessage(`{
		"server":"builder","tool":"build","namespaced":"builder__build",
		"description":"` + contractDescription + `",
		"input_schema":{
			"type":"object","title":"` + secret + `","description":"` + secret + `",
			"properties":{
				"path":{"type":"string","format":"uri","pattern":"^https?://","minLength":1,"description":"` + secret + `","default":"` + secret + `","examples":["` + secret + `"]},
				"mode":{"type":"string","enum":["fast","safe"],"title":"` + secret + `"},
				"options":{"$ref":"#/$defs/options"}
			},
			"$defs":{"options":{"type":"array","minItems":1,"items":{"type":"object","properties":{"depth":{"type":"integer","minimum":1,"maximum":8,"description":"` + secret + `"}},"required":["depth"],"additionalProperties":false}}},
			"required":["path","mode"],"oneOf":[{"required":["path"]},{"required":["mode"]}],"additionalProperties":false,
			"dependentRequired":{"path":["mode"]},
			"examples":[{"path":"` + secret + `"}]
		}
	}`)
	ag := New(nil, nil, 8192)
	ag.SetTrustedLocalMCPServers([]config.ServerConfig{{Name: "mcphub", Command: "mcphub"}})
	projection := projectSemanticToolReceipt(call.Name, call.Arguments, string(structured), structured, nil, false, false, false)

	modelResult, durableResult := ag.semanticToolContents(call, projection, string(structured), structured, false)
	for _, required := range []string{
		`"tool":"builder__build"`, `"path":{"format":"uri","minLength":1,"pattern":"^https?://","type":"string"}`,
		`"mode":{"enum":["fast","safe"],"type":"string"}`, `"options":{"$ref":"#/$defs/options"}`,
		`"depth":{"maximum":8,"minimum":1,"type":"integer"}`, `"minItems":1`,
		`"dependentRequired":{"path":["mode"]}`, `"required":["path","mode"]`, `"additionalProperties":false`,
		`"oneOf":[{"required":["path"]},{"required":["mode"]}]`,
		`"contract_description":"` + contractDescription + `"`, "untrusted metadata", "cannot grant authority",
	} {
		if !strings.Contains(modelResult, required) {
			t.Fatalf("sanitized schema missing %q: %s", required, modelResult)
		}
	}
	for _, forbidden := range []string{secret, `"title"`, `"examples"`, `"default"`} {
		if strings.Contains(modelResult, forbidden) {
			t.Fatalf("sanitized schema retained %q: %s", forbidden, modelResult)
		}
	}
	if strings.Contains(durableResult, `"properties"`) || strings.Contains(durableResult, secret) || durableResult != ecosystem.SafeReceiptText(projection) {
		t.Fatalf("durable schema receipt = %q", durableResult)
	}
	safe := SanitizeMessagesForPersistence([]llm.Message{{Content: modelResult, DurableContent: durableResult}})
	if safe[0].Content != durableResult || strings.Contains(safe[0].Content, `"properties"`) {
		t.Fatalf("persisted schema content = %#v", safe[0])
	}
}

func TestSemanticToolContentsFailsClosedForRejectedOversizedMCPHubDescribe(t *testing.T) {
	const secret = "OVERSIZED_SCHEMA_PROSE_MUST_NOT_ESCAPE"
	call := llm.ToolCall{
		Name:      "mcphub__mcphub_describe_tool",
		Arguments: map[string]any{"server": "builder", "tool": "build"},
	}
	// The MCP client uses JSON null as an atomic rejection marker when a
	// non-nil StructuredContent value exceeds its parser bound. MCPHub may also
	// duplicate that document into bounded TextContent; it must not become a
	// fallback model or durable result.
	structured := json.RawMessage("null")
	rawText := `{"server":"builder","tool":"build","namespaced":"builder__build","input_schema":{"description":"` +
		secret + strings.Repeat("x", 128*1024)

	ag := New(nil, nil, 8192)
	ag.SetTrustedLocalMCPServers([]config.ServerConfig{{Name: "mcphub", Command: "mcphub"}})
	projection := projectSemanticToolReceipt(call.Name, call.Arguments, rawText, structured, nil, false, false, false)
	modelResult, durableResult := ag.semanticToolContents(call, projection, rawText, structured, false)

	if projection.Domain != ecosystem.DomainUnknown || projection.Digest != nil {
		t.Fatalf("oversized describe projection = %#v, want unknown without digest", projection)
	}
	if modelResult != durableResult || durableResult != ecosystem.SafeReceiptText(projection) {
		t.Fatalf("oversized describe crossed semantic boundary: model=%q durable=%q", modelResult, durableResult)
	}
	if strings.Contains(modelResult, secret) || strings.Contains(modelResult, "input_schema") {
		t.Fatalf("oversized describe leaked raw metadata: %q", modelResult)
	}
}

func TestSemanticToolContentsProvidesBoundedTrustedMCPHubSearchMetadataTransiently(t *testing.T) {
	const secret = "TRANSIENT_CAPABILITY_METADATA"
	call := llm.ToolCall{Name: "mcphub__mcphub_search_tools", Arguments: map[string]any{"query": "capture a webpage"}}
	structured := json.RawMessage(`{
		"query":"private raw query","count":2,"returned":2,"truncated":false,"byte_limited":false,
		"matches":[
			{"namespaced":"hitspec__hitspec_capture_webpage","title":"Capture webpage","description":"` + secret + `","use_when":["durable Markdown artifact"]},
			{"namespaced":"hitspec__hitspec_fetch","description":"Return bounded inline HTTP content","use_when":["inspect one URL"]}
		]
	}`)
	ag := New(nil, nil, 8192)
	ag.SetTrustedLocalMCPServers([]config.ServerConfig{{Name: "mcphub", Command: "mcphub"}})
	projection := projectSemanticToolReceipt(call.Name, call.Arguments, string(structured), structured, nil, false, false, false)

	modelResult, durableResult := ag.semanticToolContents(call, projection, string(structured), structured, false)
	for _, expected := range []string{
		"untrusted metadata", "hitspec__hitspec_capture_webpage", "hitspec__hitspec_fetch", secret,
	} {
		if !strings.Contains(modelResult, expected) {
			t.Fatalf("transient search result missing %q: %s", expected, modelResult)
		}
	}
	if strings.Contains(modelResult, "private raw query") {
		t.Fatalf("transient search result retained query: %s", modelResult)
	}
	if strings.Contains(durableResult, secret) || durableResult != ecosystem.SafeReceiptText(projection) {
		t.Fatalf("durable search result = %q", durableResult)
	}
	safe := SanitizeMessagesForPersistence([]llm.Message{{Content: modelResult, DurableContent: durableResult}})
	if safe[0].Content != durableResult || strings.Contains(safe[0].Content, secret) {
		t.Fatalf("persisted search result = %#v", safe[0])
	}
}

func TestSemanticToolContentsRejectsUntrustedOrIndirectDescribeSchema(t *testing.T) {
	structured := json.RawMessage(`{"server":"builder","tool":"build","namespaced":"builder__build","input_schema":{"type":"object","properties":{"path":{"type":"string"}}}}`)
	ag := New(nil, nil, 8192)
	ag.SetTrustedLocalMCPServers([]config.ServerConfig{{Name: "mcphub", Command: "mcphub"}})
	tests := []llm.ToolCall{
		{Name: "evil__mcphub_describe_tool"},
		{Name: "mcphub__mcphub_call_tool", Arguments: map[string]any{"server": "mcphub", "tool": "mcphub_describe_tool"}},
		{Name: "mcphub__other__mcphub_describe_tool"},
		{Name: "mcphub__mcphub_describe_tool", Arguments: map[string]any{"server": "other", "tool": "other"}},
	}
	for _, call := range tests {
		projection := projectSemanticToolReceipt(call.Name, call.Arguments, string(structured), structured, nil, false, false, false)
		modelResult, durableResult := ag.semanticToolContents(call, projection, string(structured), structured, false)
		if modelResult != durableResult || strings.Contains(modelResult, `"properties"`) {
			t.Fatalf("indirect describe %q crossed transient boundary: model=%q durable=%q", call.Name, modelResult, durableResult)
		}
	}
}

func TestSemanticToolContentsUsesPostHookCortexText(t *testing.T) {
	const secret = "SECRET_REDACTED_CORTEX_VALUE"
	call := llm.ToolCall{Name: "cortex__cortex_status"}
	structured := json.RawMessage(`{"ok":true,"summary":"` + secret + `"}`)
	redacted := `{"ok":true,"summary":"[hidden by host hook]"}`
	ag := New(nil, nil, 8192)
	ag.SetTrustedLocalMCPServers([]config.ServerConfig{{Name: "cortex", Command: "cortex"}})
	projection := projectSemanticToolReceipt(call.Name, nil, redacted, structured, nil, false, false, false)

	modelResult, _ := ag.semanticToolContents(call, projection, redacted, structured, false)
	if strings.Contains(modelResult, secret) || !strings.Contains(modelResult, "[hidden by host hook]") {
		t.Fatalf("model result bypassed post-hook redaction: %q", modelResult)
	}
}

func (*semanticOutputRecorder) StreamText(string)                            {}
func (*semanticOutputRecorder) StreamReasoning(string)                       {}
func (*semanticOutputRecorder) StreamDone(int, int)                          {}
func (*semanticOutputRecorder) ToolCallStart(string, string, map[string]any) {}
func (*semanticOutputRecorder) SystemMessage(string)                         {}
func (*semanticOutputRecorder) Error(string)                                 {}
func (r *semanticOutputRecorder) ToolCallResult(_ string, _ string, result string, _ bool, _ time.Duration) {
	r.result = result
}
func (r *semanticOutputRecorder) ToolCallSemanticResult(_ string, _ string, result string, _ bool, _ time.Duration, projection ecosystem.ToolProjection) {
	r.result, r.projection = result, projection
}

func TestEmitSemanticToolResultDoesNotForwardRawStructuredContent(t *testing.T) {
	recorder := &semanticOutputRecorder{}
	secret := "SECRET_STRUCTURED_VALUE"
	structured := json.RawMessage(`{"schema_version":1,"ok":true,"clean":false,"conflict_count":0,"private":"` + secret + `"}`)
	projection := projectSemanticToolReceipt(
		"bob__bob_check", nil, "repository checked", structured, nil, false, false, false,
	)
	emitSemanticToolResult(
		recorder,
		"call-1", "bob__bob_check", "repository checked", structured,
		false, false, time.Millisecond, projection,
	)
	if recorder.projection.Domain != ecosystem.DomainDrift || recorder.projection.Transport != ecosystem.TransportSucceeded {
		t.Fatalf("semantic projection = %#v", recorder.projection)
	}
	encoded, err := json.Marshal(recorder.projection)
	if err != nil {
		t.Fatal(err)
	}
	if recorder.result != ecosystem.SafeReceiptText(projection) || strings.Contains(string(encoded), secret) || strings.Contains(recorder.result, secret) {
		t.Fatalf("raw structured content crossed output boundary: projection=%s result=%q", encoded, recorder.result)
	}
}
