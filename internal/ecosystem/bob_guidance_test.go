package ecosystem

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These documents are byte-for-byte copies of Bob v0.4.0's published
// consumer fixtures at 8639e51828ec1511ba6745a67b31e622470fa837.
func TestBobV040PublishedGuidanceFixtures(t *testing.T) {
	root := filepath.Join("testdata", "bob_v040")
	tests := []struct {
		file, tool string
		want       DomainState
		typed      bool
		toolError  bool
	}{
		{"context-clean-v1.json", "bob__bob_context", DomainSucceeded, true, false},
		{"context-drift-v1.json", "bob__bob_context", DomainDrift, true, false},
		{"context-conflict-v1.json", "bob__bob_context", DomainConflict, true, false},
		{"path-extension-v1.json", "bob__bob_path", DomainSucceeded, true, false},
		{"path-managed-v1.json", "bob__bob_path", DomainSucceeded, true, false},
		{"playbook-ready-v1.json", "bob__bob_playbook", DomainSucceeded, true, false},
		{"playbook-missing-input-v1.json", "bob__bob_playbook", DomainBlocked, true, true},
		{"error-unsupported-schema-v1.json", "bob__bob_context", DomainUnknown, false, false},
	}
	for _, test := range tests {
		t.Run(test.file, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(root, test.file))
			if err != nil {
				t.Fatal(err)
			}
			got := ProjectReceipt(ProjectToolCall(test.tool, nil), RawReceipt{Structured: raw, ToolError: test.toolError})
			if got.Domain != test.want || got.DomainTyped != test.typed {
				t.Fatalf("projection = %#v, want domain=%s typed=%t", got, test.want, test.typed)
			}
			if safe := SafeReceiptText(got); strings.Contains(safe, "/workspace") || strings.Contains(safe, "command_name") {
				t.Fatalf("safe receipt persisted Bob payload detail: %q", safe)
			}
		})
	}
}

func TestBobV040GuidanceReparsesCompleteMCPHubPages(t *testing.T) {
	tests := []struct {
		file string
		want DomainState
	}{
		{"context-clean-v1.json", DomainSucceeded},
		{"context-drift-v1.json", DomainDrift},
		{"context-conflict-v1.json", DomainConflict},
		{"path-managed-v1.json", DomainSucceeded},
		{"playbook-ready-v1.json", DomainSucceeded},
	}
	for _, test := range tests {
		t.Run(test.file, func(t *testing.T) {
			raw := readBobV040Fixture(t, test.file)
			callID := "bob-v040-complete-page"
			document, err := json.Marshal(map[string]any{
				"status": "ok", "callId": callID, "mediaType": "application/json",
				"data": base64.StdEncoding.EncodeToString(raw), "cursor": 0,
				"nextCursor": len(raw), "done": true, "totalBytes": len(raw),
			})
			if err != nil {
				t.Fatal(err)
			}
			got := ProjectReceipt(ProjectToolCall("mcphub__mcphub_get_result", map[string]any{"callId": callID}), RawReceipt{Structured: document})
			if got.Domain != test.want || !got.DomainTyped || got.Digest == nil || !got.Digest.Done {
				t.Fatalf("completed page projection = %#v, want domain=%s typed result", got, test.want)
			}
		})
	}
}

func TestBobV040GuidancePartialPageNeverBecomesDomainResult(t *testing.T) {
	raw := readBobV040Fixture(t, "context-clean-v1.json")
	partial := raw[:len(raw)/2]
	callID := "bob-v040-partial-page"
	document, err := json.Marshal(map[string]any{
		"status": "ok", "callId": callID, "mediaType": "application/json",
		"data": base64.StdEncoding.EncodeToString(partial), "cursor": 0,
		"nextCursor": len(partial), "done": false, "totalBytes": len(raw),
	})
	if err != nil {
		t.Fatal(err)
	}
	got := ProjectReceipt(ProjectToolCall("mcphub__mcphub_get_result", map[string]any{"callId": callID}), RawReceipt{Structured: document})
	if got.Domain != DomainAttention || !got.DomainTyped || got.Digest == nil || got.Digest.Done {
		t.Fatalf("partial page projection = %#v, want typed attention without a completed Bob result", got)
	}
}

func TestBobV040GuidanceContractsFailClosed(t *testing.T) {
	tests := []struct {
		name, file, tool string
		mutate           func(map[string]any)
	}{
		{
			name: "context repository state contradicts clean bit", file: "context-clean-v1.json", tool: "bob__bob_context",
			mutate: func(document map[string]any) {
				document["context"].(map[string]any)["repository"].(map[string]any)["clean"] = false
			},
		},
		{
			name: "path authority selects another workspace", file: "path-managed-v1.json", tool: "bob__bob_path",
			mutate: func(document map[string]any) {
				document["authority"].(map[string]any)["selected_workspace"] = "/other"
			},
		},
		{
			name: "playbook operation and payload disagree", file: "playbook-ready-v1.json", tool: "bob__bob_playbook",
			mutate: func(document map[string]any) {
				document["operation"] = "show"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var document map[string]any
			if err := json.Unmarshal(readBobV040Fixture(t, test.file), &document); err != nil {
				t.Fatal(err)
			}
			test.mutate(document)
			raw, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			got := ProjectReceipt(ProjectToolCall(test.tool, nil), RawReceipt{Structured: raw})
			if got.Domain != DomainUnknown || got.DomainTyped {
				t.Fatalf("malformed contract did not fail closed: %#v", got)
			}
		})
	}
}

func readBobV040Fixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "bob_v040", name))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
