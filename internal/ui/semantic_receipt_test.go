package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/abdul-hamid-achik/local-agent/internal/ecosystem"
)

func TestSemanticToolReceiptDoesNotPaintBobDriftGreen(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.Update(ToolCallStartMsg{
		ID: "bob-1", Name: "bob__bob_check", Args: map[string]any{}, StartTime: time.Now(),
	})
	m = updated.(*Model)
	projection := ecosystem.ProjectReceipt(ecosystem.ProjectToolCall("bob__bob_check", nil), ecosystem.RawReceipt{
		Structured: []byte(`{"schema_version":1,"ok":true,"clean":false,"conflict_count":0}`),
	})
	updated, _ = m.Update(ToolCallResultMsg{
		ID: "bob-1", Name: "bob__bob_check", Result: `{"schema_version":1,"ok":true,"clean":false}`,
		Duration: 5 * time.Millisecond, Projection: projection,
	})
	m = updated.(*Model)

	if len(m.toolEntries) != 1 || m.toolEntries[0].Status != ToolStatusDone || m.toolEntries[0].IsError {
		t.Fatalf("transport state was collapsed into an error: %#v", m.toolEntries)
	}
	if len(m.toolCardMgr.Cards) != 1 || m.toolCardMgr.Cards[0].State != ToolCardAttention {
		t.Fatalf("Bob drift card = %#v", m.toolCardMgr.Cards)
	}
	plain := ansi.Strip(m.toolCardMgr.Cards[0].View(72))
	for _, want := range []string{"!", "Repository needs convergence", "Drift reported"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("attention receipt missing %q:\n%s", want, plain)
		}
	}
}

func TestExpandedSemanticReceiptShowsDownstreamRouteAndEvidence(t *testing.T) {
	card := NewToolCard("mcphub__cortex__cortex_investigate", ToolCardGeneric, true)
	card.State = ToolCardSuccess
	card.Expanded = true
	card.Duration = time.Millisecond
	card.Projection = ecosystem.ToolProjection{
		Specialist: "cortex", Operation: "cortex_investigate", Role: ecosystem.RoleCoordination,
		Transport: ecosystem.TransportSucceeded, Domain: ecosystem.DomainSucceeded,
		Evidence: ecosystem.EvidenceSupported,
		Route:    ecosystem.ToolRoute{Gateway: "mcphub", Server: "cortex", Tool: "cortex_investigate", Lazy: true},
	}
	plain := ansi.Strip(card.View(76))
	for _, want := range []string{"Investigated", "specialist: Cortex · coordination", "route: Local Agent → MCPHub → Cortex · lazy", "transport: succeeded", "domain: succeeded", "evidence: supported"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("expanded semantic receipt missing %q:\n%s", want, plain)
		}
	}
	assertToolCardLinesFit(t, card.View(76), 76)
}

func TestSemanticAttentionUsesDomainBeforeArbitraryResultText(t *testing.T) {
	card := NewToolCard("bob__bob_check", ToolCardGeneric, true)
	card.State = ToolCardAttention
	card.Expanded = true
	card.Duration = time.Millisecond
	card.Result = "Everything completed successfully\nserver supplied detail"
	card.Projection = ecosystem.ToolProjection{
		Specialist: "bob", Operation: "bob_check", Role: ecosystem.RoleBuild,
		Transport: ecosystem.TransportSucceeded, Domain: ecosystem.DomainConflict,
	}

	plain := ansi.Strip(card.View(76))
	canonical := strings.Index(plain, "Conflict reported")
	arbitrary := strings.Index(plain, "Everything completed successfully")
	if canonical < 0 || arbitrary < 0 || canonical > arbitrary {
		t.Fatalf("authoritative domain state did not precede arbitrary detail:\n%s", plain)
	}
	if header := strings.Split(plain, "\n")[0]; strings.Contains(header, "Checked repository contract") {
		t.Fatalf("attention header reused success copy:\n%s", plain)
	}
}

func TestDeferredMCPHubReceiptShowsStoredResultAndFetchPath(t *testing.T) {
	projection := ecosystem.ToolProjection{
		Specialist: "cortex", Operation: "cortex_start_task", Role: ecosystem.RoleCoordination,
		Transport: ecosystem.TransportSucceeded, Domain: ecosystem.DomainAttention,
		Route: ecosystem.ToolRoute{
			Gateway: "mcphub", Server: "cortex", Tool: "cortex_start_task",
			CallID: "call-123", Lazy: true,
		},
	}
	card := NewToolCard("mcphub__mcphub_call_tool", ToolCardGeneric, true)
	card.State = toolCardStateFromProjection(projection)
	card.Expanded = true
	card.Duration = 2 * time.Millisecond
	card.Projection = projection

	plain := ansi.Strip(card.View(76))
	for _, want := range []string{
		"Result stored", "fetch call-123", "transport: succeeded",
		"domain: attention", "evidence: none", "stored result: call-123",
	} {
		if !strings.Contains(plain, want) {
			t.Fatalf("stored MCPHub receipt missing %q:\n%s", want, plain)
		}
	}
	if strings.Contains(plain, "Started case") {
		t.Fatalf("stored MCPHub receipt retained a success verb:\n%s", plain)
	}
	for _, line := range strings.Split(plain, "\n") {
		if strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "│")) == "result: call-123" {
			t.Fatalf("stored MCPHub receipt used an ambiguous result label:\n%s", plain)
		}
	}
	for _, width := range []int{4, 12, 28, 52, 76} {
		assertToolCardLinesFit(t, card.View(width), width)
	}
}
