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

	// Default is intentionally modest: KV-cache must fit alongside weights +
	// embed model on a 16GB unified-memory Mac. See config.go for rationale.
	if cfg.Ollama.NumCtx != 16384 {
		t.Errorf("Ollama.NumCtx = %d, want %d", cfg.Ollama.NumCtx, 16384)
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

func TestValidate(t *testing.T) {
	base := func() *Config { c := defaults(); return &c }

	if err := base().Validate(); err != nil {
		t.Fatalf("default config should validate, got: %v", err)
	}

	c := base()
	c.Ollama.Model = ""
	if err := c.Validate(); err == nil {
		t.Error("empty model should fail validation")
	}

	c = base()
	c.Ollama.NumCtx = 0
	if err := c.Validate(); err == nil {
		t.Error("zero num_ctx should fail validation")
	}

	c = base()
	c.Ollama.BaseURL = "not a url"
	if err := c.Validate(); err == nil {
		t.Error("malformed base_url should fail validation")
	}

	c = base()
	c.Tools.Timeout = "banana"
	if err := c.Validate(); err == nil {
		t.Error("unparseable tools.timeout should fail validation")
	}

	c = base()
	c.Servers = []ServerConfig{{Name: "x", Transport: "sse"}}
	if err := c.Validate(); err == nil {
		t.Error("sse server without url should fail validation")
	}

	c = base()
	c.Servers = []ServerConfig{{Name: "ok", Command: "noted"}}
	if err := c.Validate(); err != nil {
		t.Errorf("valid stdio server should pass, got: %v", err)
	}
}

func TestMemoryRiskyModelGuard(t *testing.T) {
	risky := []string{"gemma4:e2b", "gemma4:e4b", "qwen3.5:9b", "llama3:70b", "gemma4:31b-cloud", "qwen3.5:cloud"}
	for _, m := range risky {
		if !isMemoryRiskyModel(m) {
			t.Errorf("expected %q to be flagged memory-risky", m)
		}
	}
	safe := []string{"qwen3.5:0.8b", "qwen3.5:2b", "qwen3.5:4b", "phi4-mini", "nomic-embed-text"}
	for _, m := range safe {
		if isMemoryRiskyModel(m) {
			t.Errorf("expected %q to be safe", m)
		}
	}

	// Validate must reject a risky model unless the override env is set.
	c := defaults()
	c.Ollama.Model = "gemma4:e2b"
	if err := c.Validate(); err == nil {
		t.Error("Validate should reject gemma4:e2b on a 16GB profile")
	}
	t.Setenv("LOCAL_AGENT_ALLOW_LARGE_MODELS", "1")
	if err := c.Validate(); err != nil {
		t.Errorf("override env should allow large model, got: %v", err)
	}
}

func TestClampNumCtxForMemory(t *testing.T) {
	c := defaults()
	c.Ollama.NumCtx = 262144
	clampNumCtxForMemory(&c)
	if c.Ollama.NumCtx != safeMaxNumCtx {
		t.Errorf("expected clamp to %d, got %d", safeMaxNumCtx, c.Ollama.NumCtx)
	}

	// Safe values untouched.
	c2 := defaults()
	c2.Ollama.NumCtx = 16384
	clampNumCtxForMemory(&c2)
	if c2.Ollama.NumCtx != 16384 {
		t.Errorf("safe num_ctx should be untouched, got %d", c2.Ollama.NumCtx)
	}

	// Override keeps the large value.
	t.Setenv("LOCAL_AGENT_ALLOW_LARGE_MODELS", "1")
	c3 := defaults()
	c3.Ollama.NumCtx = 262144
	clampNumCtxForMemory(&c3)
	if c3.Ollama.NumCtx != 262144 {
		t.Errorf("override should keep value, got %d", c3.Ollama.NumCtx)
	}
}
