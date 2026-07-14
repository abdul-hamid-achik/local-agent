package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestCloudModelBoundaryIsVisibleAcrossCoreSurfaces(t *testing.T) {
	m := newTestModel(t)
	m.model = "qwen-cloud:latest"
	m.modelPinned = true
	m.ollamaModels = []OllamaModelDescriptor{{
		Name: "qwen-cloud:latest", DisplayName: "Qwen Cloud", Source: OllamaModelCloud,
		Current: true, Selectable: true, Fit: true, ConsentGranted: true,
	}}

	settings := m.settingsItems()[int(settingsModel)].Title()
	for _, want := range []string{"CLOUD", "remote prompts", "Pinned"} {
		if !strings.Contains(settings, want) {
			t.Fatalf("Settings hid %q in %q", want, settings)
		}
	}

	for _, size := range []struct {
		width, height int
	}{{30, 12}, {80, 24}} {
		m.width, m.height = size.width, size.height
		var welcomeBuilder strings.Builder
		m.renderWelcome(&welcomeBuilder)
		welcome := ansi.Strip(welcomeBuilder.String())
		m.entries = []ChatEntry{{Kind: "user", Content: "hello"}}
		status := ansi.Strip(m.renderStatusLine())
		m.entries = nil
		for surface, rendered := range map[string]string{"welcome": welcome, "status": status} {
			if !strings.Contains(rendered, "CLOUD") || !strings.Contains(rendered, "remote prompts") {
				t.Fatalf("%s at %dx%d hid Cloud boundary:\n%s", surface, size.width, size.height, rendered)
			}
		}
	}
}

func TestGoalFooterPreservesCloudAndSkippedApprovalBoundaries(t *testing.T) {
	m := newTestModel(t)
	m.model = "qwen-cloud:latest"
	m.ollamaModels = []OllamaModelDescriptor{{
		Name: m.model, Source: OllamaModelCloud, Current: true,
	}}
	m.SetApprovalPosture(ApprovalPostureSkipApprovals)
	summary := GoalSummary{Objective: "ship safely", Phase: GoalPhaseActive}

	for _, width := range []int{30, 80} {
		plain := ansi.Strip(m.renderGoalFooterStatus(summary, width))
		for _, want := range []string{"AUTO", "active", "CLOUD"} {
			if !strings.Contains(plain, want) {
				t.Fatalf("goal footer at width %d hid %q: %q", width, want, plain)
			}
		}
		if !strings.Contains(plain, "no prompts") && !strings.Contains(plain, "approval prompts skipped") {
			t.Fatalf("goal footer at width %d hid skipped approvals: %q", width, plain)
		}
		for _, line := range strings.Split(plain, "\n") {
			if len(line) > 0 && ansi.StringWidth(line) > width {
				t.Fatalf("goal footer at width %d overflowed: %q", width, line)
			}
		}
	}
}

func TestCurrentModelSurfaceLabelKeepsLegacyAndRemoteModelsTruthful(t *testing.T) {
	m := newTestModel(t)
	m.model = "legacy:latest"
	if got := m.currentModelSurfaceLabel(false); got != "legacy:latest" {
		t.Fatalf("legacy model label = %q", got)
	}
	m.ollamaModels = []OllamaModelDescriptor{{Name: m.model, Source: OllamaModelRemote}}
	if got := m.currentModelSurfaceLabel(true); got != "REMOTE · remote prompts" {
		t.Fatalf("remote compact label = %q", got)
	}
}
