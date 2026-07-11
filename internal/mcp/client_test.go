package mcp

import (
	"strings"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestRenderToolResultPreservesStructuredAndReceipts(t *testing.T) {
	result := &sdkmcp.CallToolResult{
		Content: []sdkmcp.Content{
			&sdkmcp.TextContent{Text: "summary"},
			&sdkmcp.ImageContent{MIMEType: "image/png", Data: []byte("encoded")},
			&sdkmcp.ResourceLink{URI: "file:///tmp/evidence.json", Name: "evidence", MIMEType: "application/json"},
		},
		StructuredContent: map[string]any{"count": 2, "ok": true},
	}

	got := renderToolResult(result)
	for _, want := range []string{
		"summary",
		"[image: mime=image/png encoded_bytes=7]",
		"[resource: uri=file:///tmp/evidence.json name=evidence mime=application/json]",
		`structured: {"count":2,"ok":true}`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered result %q does not contain %q", got, want)
		}
	}
}

func TestRenderToolResultIsBounded(t *testing.T) {
	got := renderToolResult(&sdkmcp.CallToolResult{
		Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: strings.Repeat("x", maxRenderedMCPResultBytes*2)}},
	})
	if len(got) > maxRenderedMCPResultBytes {
		t.Fatalf("rendered result exceeded cap: %d", len(got))
	}
	if !strings.HasSuffix(got, "... [MCP result truncated]") {
		t.Fatalf("rendered result did not disclose truncation: %q", got[len(got)-80:])
	}
}
