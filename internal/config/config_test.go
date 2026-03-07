package config

import "testing"

func TestDefaults(t *testing.T) {
	cfg := defaults()

	tests := []struct {
		name string
		got  string
		want string
	}{
		{name: "Ollama.Model", got: cfg.Ollama.Model, want: "qwen3.5:2b"},
		{name: "Ollama.BaseURL", got: cfg.Ollama.BaseURL, want: "http://localhost:11434"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("%s = %q, want %q", tt.name, tt.got, tt.want)
			}
		})
	}

	if cfg.Ollama.NumCtx != 262144 {
		t.Errorf("Ollama.NumCtx = %d, want %d", cfg.Ollama.NumCtx, 262144)
	}

	if !cfg.Model.AutoSelect {
		t.Error("Model.AutoSelect should be true by default")
	}
}

func TestApplyEnvOverrides(t *testing.T) {
	tests := []struct {
		name    string
		envKey  string
		envVal  string
		checkFn func(cfg *Config) string
		want    string
	}{
		{
			name:   "OLLAMA_HOST overrides BaseURL",
			envKey: "OLLAMA_HOST",
			envVal: "http://custom:1234",
			checkFn: func(cfg *Config) string {
				return cfg.Ollama.BaseURL
			},
			want: "http://custom:1234",
		},
		{
			name:   "LOCAL_AGENT_MODEL overrides Model",
			envKey: "LOCAL_AGENT_MODEL",
			envVal: "custom-model",
			checkFn: func(cfg *Config) string {
				return cfg.Ollama.Model
			},
			want: "custom-model",
		},
		{
			name:   "LOCAL_AGENT_AGENTS_DIR overrides AgentsDir",
			envKey: "LOCAL_AGENT_AGENTS_DIR",
			envVal: "/custom/agents",
			checkFn: func(cfg *Config) string {
				return cfg.Agents.Dir
			},
			want: "/custom/agents",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(tt.envKey, tt.envVal)
			cfg := defaults()
			applyEnvOverrides(&cfg)
			got := tt.checkFn(&cfg)
			if got != tt.want {
				t.Errorf("after setting %s=%q, got %q, want %q", tt.envKey, tt.envVal, got, tt.want)
			}
		})
	}
}
