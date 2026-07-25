package main

import (
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/local-agent/internal/config"
)

func localOnlyCatalogConfig() *config.Config {
	base := config.Defaults()
	cfg := &base
	cfg.Privacy.LocalOnly = true
	cfg.Ollama.Model = "qwen3.5:2b"
	cfg.Provider = config.ProviderConfig{
		Active: "ollama",
		Profiles: map[string]config.ProviderProfile{
			"ollama": {Type: string(config.ProviderTypeOllama), BaseURL: "http://localhost:11434", Model: "qwen3.5:2b"},
			"remote": {Type: string(config.ProviderTypeOpenAICompatible), BaseURL: "https://api.example.com/v1", Model: "gpt-x", APIKeyEnv: "EXAMPLE_KEY"},
		},
	}
	return cfg
}

// privacy.local_only is enforced in Config.Validate, which runs during load.
// A saved /provider selection is applied afterwards, so without a second
// validation a remembered remote provider re-attaches on a later launch and
// prompts leave the machine.
func TestSavedProviderPreferenceCannotBypassLocalOnly(t *testing.T) {
	cfg := localOnlyCatalogConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("baseline local-only config must be valid: %v", err)
	}

	warning := restoreManualProviderPreference(cfg, "remote")

	if warning == "" {
		t.Fatal("a remote provider was restored under privacy.local_only with no warning")
	}
	if !strings.Contains(warning, "remote") {
		t.Fatalf("warning does not name the rejected provider: %q", warning)
	}
	if cfg.Provider.Active != "ollama" {
		t.Fatalf("rejected preference still changed the active provider to %q", cfg.Provider.Active)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("config was left invalid after a rejected preference: %v", err)
	}
}

// A local profile must still restore normally, or the guard would be useless.
func TestSavedProviderPreferenceRestoresALocalProfile(t *testing.T) {
	cfg := localOnlyCatalogConfig()
	cfg.Provider.Active = "remote-placeholder"
	cfg.Provider.Profiles["remote-placeholder"] = cfg.Provider.Profiles["ollama"]

	if warning := restoreManualProviderPreference(cfg, "ollama"); warning != "" {
		t.Fatalf("a local profile was rejected: %s", warning)
	}
	if cfg.Provider.Active != "ollama" {
		t.Fatalf("active provider = %q, want ollama", cfg.Provider.Active)
	}
}

func TestSavedProviderPreferenceIgnoresUnknownNames(t *testing.T) {
	cfg := localOnlyCatalogConfig()
	if warning := restoreManualProviderPreference(cfg, "not-a-profile"); warning == "" {
		t.Fatal("an unknown provider name was accepted silently")
	}
	if cfg.Provider.Active != "ollama" {
		t.Fatalf("unknown preference changed the active provider to %q", cfg.Provider.Active)
	}
}
