package config

import (
	"strings"
	"testing"
)

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
	if !cfg.Privacy.LocalOnly {
		t.Error("Privacy.LocalOnly should be true by default")
	}
	if !cfg.Experts.Enabled || cfg.Experts.MaxEvalTokens != 768 || cfg.Experts.Timeout != "90s" {
		t.Errorf("Experts defaults = %#v", cfg.Experts)
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
	c.SkillsDir = "~/.config/local-agent/skills"
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "skills_dir is no longer supported") {
		t.Fatalf("retired skills_dir error = %v", err)
	}

	c = base()
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
	c.Experts.MaxConcurrentInference = -1
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "experts.max_concurrent_inference") {
		t.Fatalf("negative expert cap error = %v", err)
	}
	c = base()
	c.Experts.MaxTeamExperts = 17
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "experts.max_team_experts") {
		t.Fatalf("oversized team cap error = %v", err)
	}
	c = base()
	c.Experts.MaxEvalTokens = 0
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "experts.max_eval_tokens") {
		t.Fatalf("zero expert token cap error = %v", err)
	}
	c = base()
	c.Experts.Timeout = "11m"
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "experts.timeout") {
		t.Fatalf("expert timeout error = %v", err)
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

	c = base()
	c.Servers = []ServerConfig{{Name: "a__b", Command: "tool"}}
	if err := c.Validate(); err == nil {
		t.Error("reserved namespace delimiter in server name should fail validation")
	}

	c = base()
	c.Servers = []ServerConfig{{Name: "same", Command: "one"}, {Name: "same", Command: "two"}}
	if err := c.Validate(); err == nil {
		t.Error("duplicate server names should fail validation")
	}

	c = base()
	c.Ollama.BaseURL = "https://ollama.example.com"
	if err := c.Validate(); err == nil {
		t.Error("local-only config should reject a remote Ollama endpoint")
	}
	c.Privacy.LocalOnly = false
	if err := c.Validate(); err != nil {
		t.Errorf("explicit local_only=false should allow remote Ollama, got: %v", err)
	}

	for _, localHost := range []string{"0.0.0.0", "http://0.0.0.0:11434", "http://[::]:11434"} {
		c = base()
		c.Ollama.BaseURL = localHost
		if err := c.Validate(); err != nil {
			t.Errorf("local-only config should accept unspecified local Ollama host %q, got: %v", localHost, err)
		}
	}

	c = base()
	c.Servers = []ServerConfig{{Name: "remote", Transport: "streamable-http", URL: "https://mcp.example.com/mcp"}}
	if err := c.Validate(); err == nil {
		t.Error("local-only config should reject a remote MCP endpoint")
	}
	c.Servers[0].URL = "https://127.0.0.1:27124/mcp/"
	if err := c.Validate(); err != nil {
		t.Errorf("loopback MCP endpoint should pass, got: %v", err)
	}
}

func TestMemoryRiskyModelGuard(t *testing.T) {
	risky := []string{"gemma4:e4b", "qwen3.5:12b", "llama3:70b", "gemma4:31b-cloud", "qwen3.5:cloud"}
	for _, m := range risky {
		if !isMemoryRiskyModel(m) {
			t.Errorf("expected %q to be flagged memory-risky", m)
		}
	}
	safe := []string{"qwen3.5:0.8b", "qwen3.5:2b", "qwen3.5:4b", "qwen3.5:9b", "ornith:latest", "gemma4:e2b", "phi4-mini", "nomic-embed-text"}
	for _, m := range safe {
		if isMemoryRiskyModel(m) {
			t.Errorf("expected %q to be safe", m)
		}
	}

	// Validate must reject a risky model unless the override env is set.
	c := defaults()
	c.Ollama.Model = "gemma4:e4b"
	if err := c.Validate(); err == nil {
		t.Error("Validate should reject gemma4:e4b on a 16GB profile")
	}
	t.Setenv("LOCAL_AGENT_ALLOW_LARGE_MODELS", "1")
	if err := c.Validate(); err != nil {
		t.Errorf("override env should allow large model, got: %v", err)
	}
	c.Ollama.Model = "qwen3.5:cloud"
	if err := c.Validate(); err != nil {
		t.Errorf("config validation guessed execution location from a tag: %v", err)
	}
	c.Ollama.Model = "company-model:remote"
	if err := c.Validate(); err != nil {
		t.Errorf("config validation guessed remote execution from a tag: %v", err)
	}
	if err := CheckModelMemorySafe("qwen3.5:cloud"); err == nil {
		t.Error("legacy unverified local-only check accepted a cloud alias")
	}
	c.Privacy.LocalOnly = false
	c.Ollama.Model = "qwen3.5:cloud"
	if err := c.Validate(); err != nil {
		t.Errorf("Ollama cloud should be configurable when local-only is disabled: %v", err)
	}
}

func TestLocalModelSizeGuardUsesInventoryBytes(t *testing.T) {
	t.Setenv("LOCAL_AGENT_ALLOW_LARGE_MODELS", "")
	for _, name := range []string{"mixtral:8x7b", "deepseek-r1:latest"} {
		if err := CheckLocalModelSizeSafe(name, 10<<30); err == nil {
			t.Fatalf("oversized ambiguous model %q was accepted", name)
		}
	}
	if err := CheckLocalModelSizeSafe("custom:latest", 7<<30); err != nil {
		t.Fatalf("safe measured model rejected: %v", err)
	}
	if err := CheckLocalModelSizeSafe("custom:latest", 0); err == nil {
		t.Fatal("unverified model size was accepted")
	}
	t.Setenv("LOCAL_AGENT_ALLOW_LARGE_MODELS", "1")
	if err := CheckLocalModelSizeSafe("deepseek-r1:latest", 10<<30); err != nil {
		t.Fatalf("explicit measured-hardware override rejected: %v", err)
	}
	if err := CheckLocalModelSizeSafe("provider-cloud", 1<<30); err != nil {
		t.Fatalf("structured local inventory was overridden by a name heuristic: %v", err)
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
