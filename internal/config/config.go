package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Ollama       OllamaConfig   `yaml:"ollama"`
	Model        ModelConfig    `yaml:"model,omitempty"`
	Agents       AgentsConfig   `yaml:"agents,omitempty"`
	Servers      []ServerConfig `yaml:"servers,omitempty"`
	SkillsDir    string         `yaml:"skills_dir,omitempty"`
	ICE          ICEConfig      `yaml:"ice,omitempty"`
	AgentProfile string         `yaml:"agent_profile,omitempty"`
	Tools        ToolsConfig    `yaml:"tools,omitempty"`
}

type AgentsConfig struct {
	Dir      string `yaml:"dir,omitempty"`
	AutoLoad bool   `yaml:"auto_load"`
}

type ToolsConfig struct {
	Timeout        string `yaml:"timeout,omitempty"` // e.g., "30s", "2m"
	MaxGrepResults int    `yaml:"max_grep_results,omitempty"`
	MaxIterations  int    `yaml:"max_iterations,omitempty"`
}

type ICEConfig struct {
	Enabled    bool   `yaml:"enabled"`
	EmbedModel string `yaml:"embed_model,omitempty"`
	StorePath  string `yaml:"store_path,omitempty"`
}

type OllamaConfig struct {
	Model   string `yaml:"model"`
	BaseURL string `yaml:"base_url"`
	NumCtx  int    `yaml:"num_ctx"`
}

type ServerConfig struct {
	Name      string   `yaml:"name"`
	Command   string   `yaml:"command,omitempty"`
	Args      []string `yaml:"args,omitempty"`
	Env       []string `yaml:"env,omitempty"`
	Transport string   `yaml:"transport,omitempty"`
	URL       string   `yaml:"url,omitempty"`
}

func defaults() Config {
	modelCfg := DefaultModelConfig()
	return Config{
		Ollama: OllamaConfig{
			Model:   "qwen3.5:2b",
			BaseURL: "http://localhost:11434",
			// num_ctx is the KV-cache allocation, not the model's max context.
			// On a 16GB unified-memory Mac the cache scales linearly with this
			// value and competes with weights + the embed model for RAM, so the
			// default is kept modest. Raise it per-tier only when you have headroom;
			// never approach the model's 256K ceiling on 16GB. See config.example.yaml.
			NumCtx: 16384,
		},
		Model: modelCfg,
		Agents: AgentsConfig{
			Dir:      "",
			AutoLoad: true,
		},
		Tools: ToolsConfig{
			Timeout:        "30s",
			MaxGrepResults: 500,
			MaxIterations:  10,
		},
	}
}

func Load() (*Config, error) {
	cfg := defaults()

	localPath := findConfigFile()
	if localPath != "" {
		data, err := os.ReadFile(localPath)
		if err != nil {
			return nil, fmt.Errorf("read config %s: %w", localPath, err)
		}
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("parse config %s: %w", localPath, err)
		}
	}

	agentsDir := cfg.Agents.Dir
	if agentsDir == "" {
		agentsDir = FindAgentsDir()
	}

	var agentsData *AgentsDir
	if agentsDir != "" && cfg.Agents.AutoLoad {
		var err error
		agentsData, err = LoadAgentsDir(agentsDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to load .agents directory: %v\n", err)
		} else {
			if agentsData != nil {
				if cfg.Ollama.Model == "" {
					cfg.Ollama.Model = cfg.Model.DefaultModel
				}

				if len(cfg.Servers) == 0 && agentsData.HasMCP() {
					cfg.Servers = agentsData.GetMCPServers()
				}
			}
		}
	}

	applyEnvOverrides(&cfg)
	clampNumCtxForMemory(&cfg)
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// safeMaxNumCtx bounds the KV-cache allocation on a 16GB Mac. A larger context
// (e.g. a stale config's 262144) allocates a multi-GB cache that, with the
// model weights and embed model, can exhaust memory and crash the machine.
const safeMaxNumCtx = 32768

// clampNumCtxForMemory lowers an unsafe num_ctx to safeMaxNumCtx (overridable
// via LOCAL_AGENT_ALLOW_LARGE_MODELS) and warns. Runs at load so even an old
// config file with a huge num_ctx can't OOM the machine.
func clampNumCtxForMemory(cfg *Config) {
	if largeModelsAllowed() || cfg.Ollama.NumCtx <= safeMaxNumCtx {
		return
	}
	fmt.Fprintf(os.Stderr, "warning: num_ctx %d is unsafe on a 16GB Mac; clamping to %d. Lower it in your config, or set LOCAL_AGENT_ALLOW_LARGE_MODELS=1 to keep your value.\n", cfg.Ollama.NumCtx, safeMaxNumCtx)
	cfg.Ollama.NumCtx = safeMaxNumCtx
}

// Validate checks the loaded configuration for problems that would otherwise
// surface as confusing runtime failures, and fails fast with a clear message.
func (c *Config) Validate() error {
	if c.Ollama.Model == "" {
		return fmt.Errorf("config: ollama.model is empty (set a model, e.g. qwen3.5:2b)")
	}
	if err := CheckModelMemorySafe(c.Ollama.Model); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if c.Ollama.BaseURL != "" {
		// Accept the lenient forms Ollama allows: bare host, host:port, or a
		// full URL. Only a scheme is normalized in for parsing.
		raw := c.Ollama.BaseURL
		if !strings.Contains(raw, "://") {
			raw = "http://" + raw
		}
		if u, err := url.Parse(raw); err != nil || u.Host == "" {
			return fmt.Errorf("config: invalid ollama.base_url %q: must be like http://localhost:11434 or localhost:11434", c.Ollama.BaseURL)
		}
	}
	if c.Ollama.NumCtx <= 0 {
		return fmt.Errorf("config: ollama.num_ctx must be positive, got %d", c.Ollama.NumCtx)
	}
	if c.Tools.Timeout != "" {
		if d, err := time.ParseDuration(c.Tools.Timeout); err != nil {
			return fmt.Errorf("config: invalid tools.timeout %q: %w", c.Tools.Timeout, err)
		} else if d <= 0 {
			return fmt.Errorf("config: tools.timeout must be positive, got %s", c.Tools.Timeout)
		}
	}
	for i, s := range c.Servers {
		if s.Name == "" {
			return fmt.Errorf("config: servers[%d] has an empty name", i)
		}
		switch s.Transport {
		case "sse", "streamable-http":
			if s.URL == "" {
				return fmt.Errorf("config: server %q uses %s transport but has no url", s.Name, s.Transport)
			}
		case "", "stdio":
			if s.Command == "" {
				return fmt.Errorf("config: server %q uses stdio transport but has no command", s.Name)
			}
		default:
			return fmt.Errorf("config: server %q has unknown transport %q (want stdio, sse, or streamable-http)", s.Name, s.Transport)
		}
	}
	return nil
}

// CheckModelMemorySafe returns an error if loading model locally would risk an
// out-of-memory crash on a 16GB Mac (large local model or, by preference, a
// cloud model). It is enforced both at config load and on runtime /model
// switches. Override with LOCAL_AGENT_ALLOW_LARGE_MODELS=1.
func CheckModelMemorySafe(model string) error {
	if largeModelsAllowed() || !isMemoryRiskyModel(model) {
		return nil
	}
	return fmt.Errorf("model %q is unsafe on a 16GB Mac — large local models (>=6B, Gemma's ~7GB tiers) can exhaust memory and crash the machine, and cloud models are disabled by preference. Use a small Qwen tier (qwen3.5:0.8b/2b/4b). To override anyway, set LOCAL_AGENT_ALLOW_LARGE_MODELS=1", model)
}

// largeModelsAllowed reports whether the memory-safety guard is disabled.
func largeModelsAllowed() bool {
	v := os.Getenv("LOCAL_AGENT_ALLOW_LARGE_MODELS")
	return v == "1" || v == "true" || v == "yes"
}

// paramBPattern extracts a parameter count hint like ":9b" or ":0.8b" from a model tag.
var paramBPattern = regexp.MustCompile(`(\d+(?:\.\d+)?)\s*b\b`)

// isMemoryRiskyModel reports whether a model is unsafe to load locally on a
// 16GB Mac. It flags cloud models (excluded by preference), Gemma local tiers
// (gemma4:e2b is ~7.2GB despite its "2B effective" name), and any tag whose
// parameter hint is >= 6B. The footprint floor for safe operation is the 4B
// Qwen tier; everything above it risks an OOM crash.
func isMemoryRiskyModel(model string) bool {
	m := strings.ToLower(model)
	if strings.Contains(m, "cloud") {
		return true
	}
	if strings.Contains(m, "gemma") {
		// Gemma 4's smallest local tier (e2b) is ~7.2GB on disk; the "B" count
		// in the tag understates the real footprint, so block the family.
		return true
	}
	if mt := paramBPattern.FindStringSubmatch(m); mt != nil {
		if b, err := strconv.ParseFloat(mt[1], 64); err == nil && b >= 6.0 {
			return true
		}
	}
	return false
}

func LoadWithAgentsDir() (*Config, *AgentsDir, error) {
	cfg, err := Load()
	if err != nil {
		return nil, nil, err
	}

	agentsDir := cfg.Agents.Dir
	if agentsDir == "" {
		agentsDir = FindAgentsDir()
	}

	var agents *AgentsDir
	if agentsDir != "" && cfg.Agents.AutoLoad {
		agents, _ = LoadAgentsDir(agentsDir)
	}

	return cfg, agents, nil
}

func findConfigFile() string {
	candidates := []string{
		"local-agent.yaml",
		"local-agent.yml",
	}

	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates,
			filepath.Join(home, ".config", "local-agent", "config.yaml"),
			filepath.Join(home, ".config", "local-agent", "config.yml"),
		)
	}

	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("OLLAMA_HOST"); v != "" {
		cfg.Ollama.BaseURL = v
	}
	if v := os.Getenv("LOCAL_AGENT_MODEL"); v != "" {
		cfg.Ollama.Model = v
	}
	if v := os.Getenv("LOCAL_AGENT_AGENTS_DIR"); v != "" {
		cfg.Agents.Dir = v
	}
	if v := os.Getenv("LOCAL_AGENT_TOOLS_TIMEOUT"); v != "" {
		cfg.Tools.Timeout = v
	}
	if v := os.Getenv("LOCAL_AGENT_TOOLS_MAX_GREP"); v != "" {
		cfg.Tools.MaxGrepResults = parseEnvInt(v, cfg.Tools.MaxGrepResults)
	}
	if v := os.Getenv("LOCAL_AGENT_TOOLS_MAX_ITER"); v != "" {
		cfg.Tools.MaxIterations = parseEnvInt(v, cfg.Tools.MaxIterations)
	}
	if v := os.Getenv("LOCAL_AGENT_ICE_EMBED_MODEL"); v != "" {
		cfg.ICE.EmbedModel = v
	}
}

func parseEnvInt(v string, defaultVal int) int {
	if i, err := strconv.Atoi(v); err == nil {
		return i
	}
	return defaultVal
}
