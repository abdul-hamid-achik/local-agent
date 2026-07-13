package agent

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/local-agent/internal/llm"
)

func TestFormatToolArgsForToolKeepsMCPPayloadsPrivate(t *testing.T) {
	secret := "SECRET_MCP_ARGUMENT"
	tests := []struct {
		name     string
		tool     string
		args     map[string]any
		contains []string
	}{
		{
			name:     "direct MCP is opaque",
			tool:     "cortex__cortex_start_task",
			args:     map[string]any{"goal": secret, "token": secret},
			contains: []string{hiddenToolArguments},
		},
		{
			name:     "security alias is opaque",
			tool:     "tinyvault_get",
			args:     map[string]any{"key": secret},
			contains: []string{hiddenToolArguments},
		},
		{
			name: "gateway retains route",
			tool: "mcphub__mcphub_call_tool",
			args: map[string]any{
				"server": "cortex", "tool": "cortex__investigate",
				"arguments": map[string]any{"query": secret, "authorization": secret},
			},
			contains: []string{`server="cortex"`, `tool="investigate"`},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := FormatToolArgsForTool(test.tool, test.args)
			if strings.Contains(got, secret) {
				t.Fatalf("formatted arguments leaked secret: %q", got)
			}
			for _, fragment := range test.contains {
				if !strings.Contains(got, fragment) {
					t.Fatalf("formatted arguments = %q, missing %q", got, fragment)
				}
			}
		})
	}
}

func TestSafeToolArgsForPersistenceRetainsOnlyMCPHubRoute(t *testing.T) {
	secret := "SECRET_NESTED_ARGUMENT"
	original := map[string]any{
		"server": "cortex",
		"tool":   "cortex__investigate",
		"arguments": map[string]any{
			"query": secret,
			"token": secret,
		},
	}
	safe := SafeToolArgsForPersistence("mcphub__mcphub_call_tool", original)
	encoded, err := json.Marshal(safe)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secret) {
		t.Fatalf("safe gateway arguments leaked secret: %s", encoded)
	}
	if got, want := safe["server"], "cortex"; got != want {
		t.Fatalf("safe server = %#v, want %#v", got, want)
	}
	if got, want := safe["tool"], "investigate"; got != want {
		t.Fatalf("safe tool = %#v, want %#v", got, want)
	}
	if got := safe["arguments"]; !reflect.DeepEqual(got, map[string]any{"redacted": true}) {
		t.Fatalf("nested arguments = %#v", got)
	}
	if original["arguments"].(map[string]any)["query"] != secret {
		t.Fatal("sanitization mutated provider-owned arguments")
	}
}

func TestSanitizeMessagesForPersistenceRedactsSensitiveArguments(t *testing.T) {
	secret := "SESSION_TOOL_SECRET"
	messages := []llm.Message{{
		Role: "assistant",
		ToolCalls: []llm.ToolCall{
			{ID: "mcp", Name: "monitor__monitor_snapshot", Arguments: map[string]any{"token": secret, "filter": secret}},
			{ID: "write", Name: "write", Arguments: map[string]any{"path": "README.md", "content": secret}},
		},
	}}

	safe := SanitizeMessagesForPersistence(messages)
	encoded, err := json.Marshal(safe)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secret) {
		t.Fatalf("persisted messages leaked tool arguments: %s", encoded)
	}
	if got := safe[0].ToolCalls[0].Arguments["redacted"]; got != true {
		t.Fatalf("direct MCP marker = %#v, want true", got)
	}
	if got := safe[0].ToolCalls[1].Arguments["path"]; got != "README.md" {
		t.Fatalf("non-sensitive local path = %#v", got)
	}
	if got := safe[0].ToolCalls[1].Arguments["content"]; got != "[hidden]" {
		t.Fatalf("local content = %#v, want hidden", got)
	}
	if messages[0].ToolCalls[1].Arguments["content"] != secret {
		t.Fatal("message sanitization mutated live model history")
	}
}
