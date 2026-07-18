package ui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/local-agent/internal/config"
	"github.com/abdul-hamid-achik/local-agent/internal/llm"
)

func TestProviderSwitchRunsOutsideUpdateAndAppliesTokenedReceipt(t *testing.T) {
	t.Setenv("XAI_API_KEY", "test-key")
	m := newTestModel(t)
	manager := providerSwitchTestManager(t)
	m.modelManager = manager
	m.model = "qwen3.5:2b"

	idleFrame := m.projectFrame()
	cmd := m.beginProviderSwitch("xai", "")
	if cmd == nil || !m.providerSwitchRunning {
		t.Fatalf("begin switch = cmd %v running %v", cmd != nil, m.providerSwitchRunning)
	}
	busyFrame := m.projectFrame()
	if got, want := m.viewport.Height(), busyFrame.Transcript.Rect.Height(); got != want {
		t.Fatalf("busy provider geometry = viewport %d projection %d", got, want)
	}
	if busyFrame.Footer.Rect.Height() <= idleFrame.Footer.Rect.Height() {
		t.Fatalf(
			"provider busy footer did not claim rows: idle=%d busy=%d",
			idleFrame.Footer.Rect.Height(),
			busyFrame.Footer.Rect.Height(),
		)
	}
	if got := m.activeProviderName(); got != "ollama" {
		t.Fatalf("begin switch mutated provider synchronously: %q", got)
	}

	result := awaitCommandMessage[providerSwitchResultMsg](t, commandMessages(cmd), 2*time.Second)
	if result.Err != nil {
		t.Fatalf("provider switch command: %v", result.Err)
	}
	if m.model != "qwen3.5:2b" {
		t.Fatalf("background command mutated UI model before receipt: %q", m.model)
	}

	updated, _ := m.Update(result)
	m = updated.(*Model)
	if m.providerSwitchRunning || m.providerSwitchCancel != nil {
		t.Fatalf("receipt left switch running: running=%v cancel=%v", m.providerSwitchRunning, m.providerSwitchCancel != nil)
	}
	if got, want := m.viewport.Height(), m.projectFrame().Transcript.Rect.Height(); got != want {
		t.Fatalf("settled provider geometry = viewport %d projection %d", got, want)
	}
	if got := m.activeProviderName(); got != "xai" || m.model != "grok-4" {
		t.Fatalf("receipt did not reconcile provider/model: provider=%q model=%q", got, m.model)
	}
	if got := m.entries[len(m.entries)-1].Content; !strings.Contains(got, "Provider: xai") || !strings.Contains(got, "grok-4") {
		t.Fatalf("provider receipt = %q", got)
	}
}

func TestProviderSwitchCancellationFailsClosedBeforeMutation(t *testing.T) {
	t.Setenv("XAI_API_KEY", "test-key")
	m := newTestModel(t)
	manager := providerSwitchTestManager(t)
	m.modelManager = manager
	m.model = "qwen3.5:2b"

	cmd := m.beginProviderSwitch("xai", "")
	m.providerSwitchCancel()
	result := awaitCommandMessage[providerSwitchResultMsg](t, commandMessages(cmd), 2*time.Second)
	if !errors.Is(result.Err, context.Canceled) {
		t.Fatalf("cancelled switch error = %v, want context.Canceled", result.Err)
	}
	updated, _ := m.Update(result)
	m = updated.(*Model)
	if got := m.activeProviderName(); got != "ollama" {
		t.Fatalf("cancelled switch changed provider to %q", got)
	}
	if m.providerSwitchRunning {
		t.Fatal("cancelled receipt left switch running")
	}
	if got := m.entries[len(m.entries)-1].Content; got != "Provider switch cancelled." {
		t.Fatalf("cancel receipt = %q", got)
	}
}

func TestProviderSwitchIgnoresStaleReceipt(t *testing.T) {
	m := newTestModel(t)
	m.modelManager = providerSwitchTestManager(t)
	first := m.beginProviderSwitch("xai", "")
	if first == nil {
		t.Fatal("first switch returned nil command")
	}
	firstToken := m.providerSwitchToken
	second := m.beginProviderSwitch("ollama", "")
	if second == nil {
		t.Fatal("second switch returned nil command")
	}
	secondToken := m.providerSwitchToken

	updated, _ := m.Update(providerSwitchResultMsg{
		Token: firstToken,
		Name:  "xai",
		Err:   context.Canceled,
	})
	m = updated.(*Model)
	if !m.providerSwitchRunning || m.providerSwitchToken != secondToken {
		t.Fatalf("stale receipt settled current switch: running=%v token=%d", m.providerSwitchRunning, m.providerSwitchToken)
	}

	m.providerSwitchCancel()
	result := awaitCommandMessage[providerSwitchResultMsg](t, commandMessages(second), 2*time.Second)
	updated, _ = m.Update(result)
	m = updated.(*Model)
	if m.providerSwitchRunning {
		t.Fatal("current receipt did not settle switch")
	}
}

func providerSwitchTestManager(t *testing.T) *llm.ModelManager {
	t.Helper()
	manager := llm.NewModelManager("http://127.0.0.1:9", 4096)
	t.Cleanup(manager.Close)
	manager.ConfigureProviderCatalog(config.ProviderConfig{
		Active: "ollama",
		Profiles: map[string]config.ProviderProfile{
			"ollama": {
				Type:  config.ProviderTypeOllama,
				Model: "qwen3.5:2b",
			},
			"xai": {
				Type:    config.ProviderTypeXAI,
				BaseURL: "https://api.x.ai/v1",
				Model:   "grok-4",
			},
		},
	}, false, "qwen3.5:2b")
	return manager
}
