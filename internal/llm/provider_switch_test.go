package llm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/abdul-hamid-achik/local-agent/internal/config"
)

func TestSwitchProviderRemoteAndBack(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"grok-4.5"}]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	t.Setenv("XAI_API_KEY", "test-key")

	manager := NewModelManager("http://127.0.0.1:9", 4096)
	defer manager.Close()
	manager.ConfigureProviderCatalog(config.ProviderConfig{
		Active: "ollama",
		Profiles: map[string]config.ProviderProfile{
			"ollama": {Type: config.ProviderTypeOllama},
			"xai": {
				Type:    config.ProviderTypeXAI,
				BaseURL: server.URL + "/v1",
				Model:   "grok-4.5",
			},
		},
	}, false, "qwen3.5:2b")

	if err := manager.SwitchProvider("xai"); err != nil {
		t.Fatal(err)
	}
	if !manager.RemoteProvider() || manager.Model() != "grok-4.5" {
		t.Fatalf("remote state: remote=%v model=%q", manager.RemoteProvider(), manager.Model())
	}
	if manager.ActiveProviderName() != "xai" {
		t.Fatalf("active = %q", manager.ActiveProviderName())
	}

	if err := manager.SwitchProvider("ollama"); err != nil {
		t.Fatal(err)
	}
	if manager.RemoteProvider() {
		t.Fatal("expected ollama path")
	}
	if manager.Model() != "qwen3.5:2b" {
		t.Fatalf("restored model = %q", manager.Model())
	}
}

func TestSwitchProviderMissingKey(t *testing.T) {
	t.Setenv("XAI_API_KEY", "")
	manager := NewModelManager("http://127.0.0.1:9", 4096)
	defer manager.Close()
	manager.ConfigureProviderCatalog(config.ProviderConfig{
		Profiles: map[string]config.ProviderProfile{
			"xai": {Type: config.ProviderTypeXAI},
		},
	}, false, "qwen3.5:2b")
	err := manager.SwitchProvider("xai")
	if err == nil || !contains(err.Error(), "XAI_API_KEY") {
		t.Fatalf("expected missing key error, got %v", err)
	}
}

func TestSwitchProviderLocalOnlyErrorDoesNotExposeBaseURL(t *testing.T) {
	manager := NewModelManager("http://127.0.0.1:9", 4096)
	defer manager.Close()
	secretURL := "https://example.com/private/super-secret"
	manager.ConfigureProviderCatalog(config.ProviderConfig{
		Active: "ollama",
		Profiles: map[string]config.ProviderProfile{
			"ollama": {Type: config.ProviderTypeOllama},
			"remote": {
				Type:      config.ProviderTypeOpenAICompatible,
				BaseURL:   secretURL,
				Model:     "test-model",
				APIKeyEnv: "TEST_PROVIDER_API_KEY",
			},
		},
	}, true, "qwen3.5:2b")

	err := manager.SwitchProvider("remote")
	if err == nil || !contains(err.Error(), "local_only") {
		t.Fatalf("expected local_only error, got %v", err)
	}
	if contains(err.Error(), "super-secret") || contains(err.Error(), secretURL) {
		t.Fatalf("provider switch error exposed base URL: %v", err)
	}
	if manager.RemoteProvider() || manager.ActiveProviderName() != "ollama" {
		t.Fatal("rejected provider switch mutated manager state")
	}
}

func TestSwitchProviderContextCanceledBeforeMutation(t *testing.T) {
	t.Setenv("XAI_API_KEY", "test-key")
	manager := NewModelManager("http://127.0.0.1:9", 4096)
	defer manager.Close()
	manager.ConfigureProviderCatalog(config.ProviderConfig{
		Active: "ollama",
		Profiles: map[string]config.ProviderProfile{
			"ollama": {Type: config.ProviderTypeOllama},
			"xai": {
				Type:    config.ProviderTypeXAI,
				BaseURL: "https://api.x.ai/v1",
				Model:   "grok-4.5",
			},
		},
	}, false, "qwen3.5:2b")
	originalProvider := manager.ActiveProviderName()
	originalModel := manager.Model()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := manager.SwitchProviderContext(ctx, "xai")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("SwitchProviderContext error = %v, want context.Canceled", err)
	}
	if manager.RemoteProvider() {
		t.Fatal("canceled switch mutated the active provider")
	}
	if got := manager.ActiveProviderName(); got != originalProvider {
		t.Fatalf("active provider = %q, want unchanged %q", got, originalProvider)
	}
	if got := manager.Model(); got != originalModel {
		t.Fatalf("model = %q, want unchanged %q", got, originalModel)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		(len(s) > 0 && (func() bool {
			for i := 0; i+len(sub) <= len(s); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		})()))
}
