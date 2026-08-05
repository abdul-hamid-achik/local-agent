package ui

import (
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/local-agent/internal/llm"
)

// sonar's opening frame said this, verbatim, on a correctly configured
// session:
//
//	main · local-agent   DEEPSEEK · remote prompts · deepseek-v4-flash · 0/1.0M · 0%
//	No local model installed
//	press p to pull qwen3.5:2b (~2.7 GB)
//
// The top bar was right and the body was wrong. The bootstrap check only ever
// asked the Ollama inventory, which is empty by construction on a harness that
// does not use Ollama — so the first thing a new user saw was an instruction
// to download 2.7 GB they did not need, under a line saying the model was
// already resolved.
//
// Bootstrap means having no model at all, not having no local one.
func TestRemoteProviderNeedsNoLocalBootstrap(t *testing.T) {
	m := newTestModel(t)
	// The state the failing frame was rendered from: the inventory ran and
	// found nothing local, because there is nothing local to find.
	m.ollamaInventoryAttempted = true
	m.ollamaModels = nil
	m.ollamaOffline = false

	if !m.needsModelBootstrap() {
		t.Fatal("fixture does not reproduce the empty local inventory")
	}

	withRemoteProvider(t, m)
	if m.needsModelBootstrap() {
		t.Error("a session already served by a remote provider was told to install a local model")
	}
}

// The prompt must survive for the case it exists for: a local-only harness
// with a genuinely empty inventory has nothing to talk to.
func TestLocalHarnessWithNoModelStillPrompts(t *testing.T) {
	m := newTestModel(t)
	m.ollamaInventoryAttempted = true
	m.ollamaModels = nil
	m.ollamaOffline = false

	if !m.needsModelBootstrap() {
		t.Error("a local harness with no installed model lost its bootstrap prompt")
	}

	// An unreachable daemon is a different problem with different copy, and a
	// populated inventory is not a bootstrap at all.
	m.ollamaOffline = true
	if m.needsModelBootstrap() {
		t.Error("an offline daemon was reported as a missing model")
	}
	m.ollamaOffline = false
	m.ollamaModels = []OllamaModelDescriptor{{Name: "qwen3.5:2b", Selectable: true, Fit: true}}
	if m.needsModelBootstrap() {
		t.Error("an installed model still asked to bootstrap")
	}
}

// The whole point is what reaches the screen, so assert the frame and not only
// the predicate.
func TestRemoteWelcomeFrameOmitsThePullHint(t *testing.T) {
	m := newTestModel(t)
	m.ollamaInventoryAttempted = true
	m.ollamaModels = nil
	withRemoteProvider(t, m)

	var b strings.Builder
	m.renderWelcome(&b)
	frame := b.String()
	for _, forbidden := range []string{"No local model installed", "press p to pull"} {
		if strings.Contains(frame, forbidden) {
			t.Errorf("remote welcome frame still says %q:\n%s", forbidden, frame)
		}
	}
}

// withRemoteProvider attaches a remote provider the way a configured harness
// does. ConfigureRemoteProvider takes the concrete client here rather than an
// interface, so this builds a real one pointed at a dead port: nothing in this
// test dispatches, it only needs RemoteProvider() to answer true.
func withRemoteProvider(t *testing.T, m *Model) {
	t.Helper()
	client, err := llm.NewOpenAICompatibleClient(llm.OpenAICompatibleOptions{
		BaseURL: "https://127.0.0.1:1/v1",
		Model:   "grok-4.5",
		APIKey:  "unused-by-this-test",
	})
	if err != nil {
		t.Fatalf("build remote client: %v", err)
	}
	manager := llm.NewModelManager("http://127.0.0.1:1", 8192)
	if err := manager.ConfigureRemoteProvider(client, 256_000, "xai"); err != nil {
		t.Fatalf("attach remote provider: %v", err)
	}
	if !manager.RemoteProvider() {
		t.Fatal("the manager did not report the attached provider as remote")
	}
	m.modelManager = manager
}
