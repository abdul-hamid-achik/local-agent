package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
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
	if cfg.Tools.MaxIterations != 10 || cfg.Tools.AutoMaxIterations != 40 {
		t.Errorf("Tools iteration defaults = normal:%d auto:%d, want 10/40", cfg.Tools.MaxIterations, cfg.Tools.AutoMaxIterations)
	}
	if cfg.Continuations.Mode != ContinuationSuggest || cfg.Continuations.MaxAutoSteps != MaxAutoContinuationSteps {
		t.Errorf("Continuations defaults = %#v, want mode=%q max_auto_steps=%d", cfg.Continuations, ContinuationSuggest, MaxAutoContinuationSteps)
	}
}

func TestAutoIterationEnvironmentOverride(t *testing.T) {
	t.Setenv("LOCAL_AGENT_TOOLS_AUTO_MAX_ITER", "64")
	cfg := defaults()
	if err := applyEnvOverrides(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Tools.AutoMaxIterations != 64 {
		t.Fatalf("auto max iterations = %d, want 64", cfg.Tools.AutoMaxIterations)
	}
}

func TestProviderXAIDefaultsAndValidation(t *testing.T) {
	cfg := defaults()
	cfg.Provider = ProviderConfig{Type: ProviderTypeXAI}
	cfg.Privacy.LocalOnly = false
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	resolved := cfg.Provider.Resolve()
	if resolved.BaseURL != "https://api.x.ai/v1" {
		t.Fatalf("base_url = %q", resolved.BaseURL)
	}
	if resolved.APIKeyEnv != "XAI_API_KEY" {
		t.Fatalf("api_key_env = %q", resolved.APIKeyEnv)
	}
	if resolved.Model != "grok-4.5" {
		t.Fatalf("model = %q", resolved.Model)
	}
}

func TestProviderMultiProfileCatalog(t *testing.T) {
	cfg := defaults()
	cfg.Privacy.LocalOnly = false
	cfg.Provider = ProviderConfig{
		Active: "xai",
		Profiles: map[string]ProviderProfile{
			"ollama": {Type: ProviderTypeOllama},
			"xai":    {Type: ProviderTypeXAI, Model: "grok-4.5"},
			"openai": {
				Type:      ProviderTypeOpenAICompatible,
				BaseURL:   "https://api.openai.com/v1",
				Model:     "gpt-4.1",
				APIKeyEnv: "OPENAI_API_KEY",
			},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	name, profile, err := cfg.Provider.ActiveProfile()
	if err != nil {
		t.Fatal(err)
	}
	if name != "xai" || profile.APIKeyEnv != "XAI_API_KEY" {
		t.Fatalf("active = %q %#v", name, profile)
	}
	envs := cfg.Provider.AllAPIKeyEnvs()
	if len(envs) != 2 {
		t.Fatalf("api key envs = %#v", envs)
	}
	names := cfg.Provider.ProfileNames()
	if len(names) != 3 {
		t.Fatalf("profiles = %#v", names)
	}
}

func TestProviderActiveMissingProfile(t *testing.T) {
	cfg := defaults()
	cfg.Privacy.LocalOnly = false
	cfg.Provider = ProviderConfig{
		Active: "missing",
		Profiles: map[string]ProviderProfile{
			"ollama": {Type: ProviderTypeOllama},
		},
	}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "active") {
		t.Fatalf("expected active error, got %v", err)
	}
}

func TestProviderRemoteRejectedWhenLocalOnly(t *testing.T) {
	cfg := defaults()
	cfg.Provider = ProviderConfig{Type: ProviderTypeXAI}
	cfg.Privacy.LocalOnly = true
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "local_only") {
		t.Fatalf("expected local_only rejection, got %v", err)
	}
}

func TestProviderAPIKeyFromEnv(t *testing.T) {
	t.Setenv("XAI_API_KEY", "secret-value")
	cfg := ProviderConfig{Type: ProviderTypeXAI}
	got, err := cfg.ResolveAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if got != "secret-value" {
		t.Fatalf("key = %q", got)
	}
}

func TestProviderAPIKeyMissingHintsTinyVault(t *testing.T) {
	t.Setenv("XAI_API_KEY", "")
	cfg := ProviderConfig{Type: ProviderTypeXAI}
	_, err := cfg.ResolveAPIKey()
	if err == nil || !strings.Contains(err.Error(), "tvault run") {
		t.Fatalf("expected tvault hint, got %v", err)
	}
}

func TestProviderEnvOverrides(t *testing.T) {
	t.Setenv("LOCAL_AGENT_PROVIDER", "openai_compatible")
	t.Setenv("LOCAL_AGENT_PROVIDER_BASE_URL", "https://openrouter.ai/api/v1")
	t.Setenv("LOCAL_AGENT_PROVIDER_MODEL", "anthropic/claude-sonnet-4")
	t.Setenv("LOCAL_AGENT_PROVIDER_API_KEY_ENV", "OPENROUTER_API_KEY")
	t.Setenv("LOCAL_AGENT_LOCAL_ONLY", "false")
	cfg := defaults()
	if err := applyEnvOverrides(&cfg); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.Provider.Type != "openai_compatible" || cfg.Provider.Model != "anthropic/claude-sonnet-4" {
		t.Fatalf("provider = %#v", cfg.Provider)
	}
	if cfg.Privacy.LocalOnly {
		t.Fatal("expected local_only false")
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
			if err := applyEnvOverrides(&cfg); err != nil {
				t.Fatal(err)
			}
			got := tt.checkFn(&cfg)
			if got != tt.want {
				t.Errorf("after setting %s=%q, got %q, want %q", tt.envKey, tt.envVal, got, tt.want)
			}
		})
	}
}

func TestContinuationEnvironmentOverrides(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		t.Setenv("LOCAL_AGENT_CONTINUATIONS_MODE", "auto_read_only")
		t.Setenv("LOCAL_AGENT_CONTINUATIONS_MAX_AUTO_STEPS", "1")
		cfg := defaults()
		if err := applyEnvOverrides(&cfg); err != nil {
			t.Fatalf("apply overrides: %v", err)
		}
		if cfg.Continuations.Mode != ContinuationAutoReadOnly || cfg.Continuations.MaxAutoSteps != 1 {
			t.Fatalf("continuation overrides = %#v", cfg.Continuations)
		}
		if err := cfg.Validate(); err != nil {
			t.Fatalf("validate overrides: %v", err)
		}
	})

	t.Run("invalid mode fails validation", func(t *testing.T) {
		t.Setenv("LOCAL_AGENT_CONTINUATIONS_MODE", "automatic")
		cfg := defaults()
		if err := applyEnvOverrides(&cfg); err != nil {
			t.Fatalf("apply overrides: %v", err)
		}
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "continuations.mode") {
			t.Fatalf("invalid mode error = %v", err)
		}
	})

	t.Run("malformed max fails closed", func(t *testing.T) {
		t.Setenv("LOCAL_AGENT_CONTINUATIONS_MAX_AUTO_STEPS", "many")
		cfg := defaults()
		if err := applyEnvOverrides(&cfg); err == nil || !strings.Contains(err.Error(), "LOCAL_AGENT_CONTINUATIONS_MAX_AUTO_STEPS") {
			t.Fatalf("malformed max error = %v", err)
		}
	})
}

func TestContinuationsConfigValidation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mode    ContinuationMode
		steps   int
		wantErr string
	}{
		{name: "off", mode: ContinuationOff, steps: 0},
		{name: "suggest", mode: ContinuationSuggest, steps: MaxAutoContinuationSteps},
		{name: "auto read only", mode: ContinuationAutoReadOnly, steps: 1},
		{name: "unknown mode", mode: "automatic", steps: 1, wantErr: "continuations.mode"},
		{name: "case sensitive mode", mode: "AUTO_READ_ONLY", steps: 1, wantErr: "continuations.mode"},
		{name: "negative max", mode: ContinuationSuggest, steps: -1, wantErr: "continuations.max_auto_steps"},
		{name: "above hard max", mode: ContinuationAutoReadOnly, steps: MaxAutoContinuationSteps + 1, wantErr: "continuations.max_auto_steps"},
		{name: "auto requires budget", mode: ContinuationAutoReadOnly, steps: 0, wantErr: "at least 1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := defaults()
			cfg.Continuations = ContinuationsConfig{Mode: tc.mode, MaxAutoSteps: tc.steps}
			err := cfg.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Validate() error = %v, want substring %q", err, tc.wantErr)
			}
		})
	}
}

func TestContinuationsConfigStrictYAML(t *testing.T) {
	t.Run("omitted field retains host default", func(t *testing.T) {
		cfg := defaults()
		if err := yaml.Unmarshal([]byte("continuations:\n  mode: auto_read_only\n"), &cfg); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if cfg.Continuations.Mode != ContinuationAutoReadOnly || cfg.Continuations.MaxAutoSteps != MaxAutoContinuationSteps {
			t.Fatalf("decoded continuations = %#v", cfg.Continuations)
		}
	})

	for _, test := range []struct {
		name     string
		document string
		wantErr  string
	}{
		{
			name:     "explicit null mode",
			document: "continuations:\n  mode: null\n",
			wantErr:  "continuations.mode cannot be null",
		},
		{
			name:     "explicit null max auto steps",
			document: "continuations:\n  max_auto_steps: null\n",
			wantErr:  "continuations.max_auto_steps cannot be null",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := defaults()
			err := yaml.Unmarshal([]byte(test.document), &cfg)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("decode error = %v, want substring %q", err, test.wantErr)
			}
		})
	}

	for _, document := range []string{
		"continuations:\n  mode: suggest\n  max_auto_step: 1\n",
		"continuations:\n  mode: suggest\n  mode: off\n",
		"continuations: auto_read_only\n",
		"continuations: null\n",
	} {
		cfg := defaults()
		if err := yaml.Unmarshal([]byte(document), &cfg); err == nil {
			t.Fatalf("invalid continuations YAML decoded:\n%s", document)
		}
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
