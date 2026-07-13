package agent

import (
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/local-agent/internal/llm"
)

func TestMCPApprovalConsequenceExplainsWhyEffectfulCallsRemainGated(t *testing.T) {
	tests := []struct {
		name string
		meta llm.ToolBehavior
		want string
	}{
		{name: "unknown indirect call", want: "no effect metadata"},
		{name: "additive durable call", meta: llm.ToolBehavior{Declared: true}, want: "durable state"},
		{name: "destructive external call", meta: llm.ToolBehavior{Declared: true, Destructive: true, OpenWorld: true}, want: "destructive changes"},
		{name: "contradictory read destructive call", meta: llm.ToolBehavior{Declared: true, ReadOnly: true, Destructive: true}, want: "declares this read-only"},
		{name: "open-world read", meta: llm.ToolBehavior{Declared: true, ReadOnly: true, OpenWorld: true}, want: "external systems"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mcpApprovalConsequence(tt.meta); !strings.Contains(got, tt.want) {
				t.Fatalf("consequence = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCortexStartTaskApprovalConsequenceIsConservative(t *testing.T) {
	got := mcpApprovalConsequence(llm.ToolBehavior{
		Declared: true, ReadOnly: false, Destructive: false, OpenWorld: false,
	})
	for _, want := range []string{"server metadata", "durable state"} {
		if !strings.Contains(strings.ToLower(got), want) {
			t.Fatalf("cortex_start_task consequence = %q, want %q", got, want)
		}
	}
}

func TestBoundApprovalLabelIsUTF8SafeAndBounded(t *testing.T) {
	got := boundApprovalLabel(strings.Repeat("界", 100))
	if len(got) > 160 || !strings.HasSuffix(got, "...") {
		t.Fatalf("bounded label bytes=%d value=%q", len(got), got)
	}
}
