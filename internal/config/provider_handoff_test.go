package config

import (
	"reflect"
	"testing"
)

func TestProcessProviderHandoff(t *testing.T) {
	t.Setenv("LOCAL_AGENT_PROVIDER", "chalupa")
	t.Setenv("LOCAL_AGENT_LOCAL_ONLY", "false")
	t.Setenv("LOCAL_AGENT_PROVIDER_CONFIG", `{"type":"ollama_remote","base_url":"http://127.0.0.1:11435","model":"qwen3.8:27b","context_size":32768,"max_model_bytes":25769803776,"stream_idle_timeout":"10m"}`)
	cfg := defaults()
	cfg.Provider.Profiles = map[string]ProviderProfile{"personal": {Type: "ollama"}}
	originalTools := cfg.Tools
	if err := applyEnvOverrides(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Provider.Active != "chalupa" || cfg.Provider.Profiles["chalupa"].Model != "qwen3.8:27b" || cfg.Privacy.LocalOnly {
		t.Fatal("provider handoff was not applied")
	}
	if cfg.Provider.Profiles["personal"].Type != "ollama" || !reflect.DeepEqual(cfg.Tools, originalTools) {
		t.Fatal("unrelated configuration changed")
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`{"type":"ollama_remote","unknown":"private-value"}`, "type: ollama\n---\ntype: ollama", "[invalid"} {
		t.Setenv("LOCAL_AGENT_PROVIDER_CONFIG", raw)
		if err := applyEnvOverrides(&cfg); err == nil {
			t.Fatal("invalid handoff accepted")
		}
	}
}
