package ecosystem

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestHitspecCaptureProjectsBoundedDurableArtifact(t *testing.T) {
	projection := ProjectToolCall("mcphub__mcphub_call_tool", map[string]any{
		"server": "hitspec", "tool": "hitspec_capture_webpage",
	})
	structured := json.RawMessage(`{
		"url":"https://example.com/docs?token=secret",
		"final_url":"https://docs.example.com/guide?token=secret",
		"title":"private title","http_status":200,"content_type":"text/html","markdown_bytes":321,
		"stash":{"id":"stash-123","status":"saved","created_at":"2026-07-13T12:00:00Z",
			"content_hash":"provider-specific-hash","file_count":1,"total_size":321,
			"indexed":true,"index_requested":true}
	}`)

	got := ProjectReceipt(projection, RawReceipt{Structured: structured})
	if got.Specialist != "hitspec" || got.Role != RoleArtifact || got.Domain != DomainSucceeded || got.Evidence != EvidenceSupported {
		t.Fatalf("projection = %#v", got)
	}
	if got.Artifact == nil || got.Artifact.Kind != ArtifactDigestHitspecCapture || got.Artifact.ID != "stash-123" ||
		got.Artifact.URI != "fcheap://stash/stash-123" || got.Artifact.SchemaVersion != hitspecCaptureSchema ||
		got.Artifact.FileCount != 1 || got.Artifact.TotalSize != 321 || got.Artifact.ContentSHA256 != "" {
		t.Fatalf("artifact = %#v", got.Artifact)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"example.com", "token", "private title", "provider-specific-hash"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("projection retained %q: %s", forbidden, encoded)
		}
	}
	if summary := SafeReceiptText(got); !strings.Contains(summary, "fcheap://stash/stash-123") || !strings.Contains(summary, "321 bytes") {
		t.Fatalf("safe receipt = %q", summary)
	}
}

func TestHitspecCaptureKeepsStorageAndIndexingOutcomesDistinct(t *testing.T) {
	projection := ProjectToolCall("hitspec__hitspec_capture_webpage", nil)
	structured := json.RawMessage(`{
		"url":"https://example.com","final_url":"https://example.com","title":"Example",
		"http_status":200,"content_type":"text/html","markdown_bytes":12,
		"stash":{"id":"stash-partial","status":"saved_with_failures","file_count":1,"total_size":12,
			"indexed":false,"index_requested":true,
			"failed":[{"id":"index","stage":"index","error":"SECRET_FAILURE_PROSE"}]}
	}`)

	got := ProjectReceipt(projection, RawReceipt{Structured: structured})
	if got.Domain != DomainAttention || got.Evidence != EvidenceSupported || got.Artifact == nil || !got.Artifact.IndexingFailed {
		t.Fatalf("projection = %#v", got)
	}
	if strings.Contains(SafeReceiptText(got), "SECRET_FAILURE_PROSE") || !strings.Contains(SafeReceiptText(got), "indexing incomplete") {
		t.Fatalf("safe receipt = %q", SafeReceiptText(got))
	}
}

func TestHitspecProjectionRejectsWrongOperationOrMalformedCapture(t *testing.T) {
	document := json.RawMessage(`{
		"http_status":200,"markdown_bytes":12,
		"stash":{"id":"stash-1","status":"saved","file_count":1,"total_size":12,"indexed":true,"index_requested":true}
	}`)

	fetch := ProjectReceipt(ProjectToolCall("hitspec__hitspec_fetch", nil), RawReceipt{Structured: document})
	if fetch.Domain != DomainUnknown || fetch.Artifact != nil || fetch.Role != RoleDiscovery {
		t.Fatalf("fetch projection = %#v", fetch)
	}

	malformed := ProjectReceipt(ProjectToolCall("hitspec__hitspec_capture_webpage", nil), RawReceipt{
		Structured: json.RawMessage(`{"http_status":200,"markdown_bytes":12,"stash":{"id":"../escape","status":"saved","file_count":1,"total_size":12,"indexed":true,"index_requested":true}}`),
	})
	if malformed.Domain != DomainUnknown || malformed.Artifact != nil {
		t.Fatalf("malformed projection = %#v", malformed)
	}
}
