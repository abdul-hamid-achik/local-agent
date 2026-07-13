package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/abdul-hamid-achik/local-agent/internal/safeio"
	"gopkg.in/yaml.v3"
)

const maxStartupConfigBytes int64 = 1 << 20

var configFileReader = safeio.NewReader()
var configFileReadTimeout = safeio.StartupReadTimeout

type Config struct {
	// SourcePath is the host-resolved config selected by repository/XDG
	// precedence. It is runtime metadata only and is never serialized.
	SourcePath   string         `yaml:"-" json:"-"`
	Ollama       OllamaConfig   `yaml:"ollama"`
	Model        ModelConfig    `yaml:"model,omitempty"`
	Agents       AgentsConfig   `yaml:"agents,omitempty"`
	Servers      []ServerConfig `yaml:"servers,omitempty"`
	SkillsDir    string         `yaml:"skills_dir,omitempty"`
	ICE          ICEConfig      `yaml:"ice,omitempty"`
	AgentProfile string         `yaml:"agent_profile,omitempty"`
	Tools        ToolsConfig    `yaml:"tools,omitempty"`
	Privacy      PrivacyConfig  `yaml:"privacy,omitempty"`
}

type PrivacyConfig struct {
	// LocalOnly rejects non-local Ollama and remote MCP endpoints. Approved
	// subprocesses can still access the network; they are an explicit trust
	// boundary surfaced by the tool permission UI.
	LocalOnly bool `yaml:"local_only"`
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
		Privacy: PrivacyConfig{LocalOnly: true},
	}
}

func Load() (*Config, error) {
	cfg := defaults()

	localPath, data, err := findAndReadConfigFile()
	if err != nil {
		return nil, err
	}
	if localPath != "" {
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("parse config %s: %w", localPath, err)
		}
		cfg.SourcePath = localPath
		if absolute, absErr := filepath.Abs(localPath); absErr == nil {
			cfg.SourcePath = filepath.Clean(absolute)
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
	if c.Privacy.LocalOnly {
		if err := CheckLocalModelNameMemorySafe(c.Ollama.Model); err != nil {
			return fmt.Errorf("config: %w", err)
		}
	}
	if c.Ollama.BaseURL != "" {
		// Accept the lenient forms Ollama allows: bare host, host:port, or a
		// full URL. Only a scheme is normalized in for parsing.
		raw := c.Ollama.BaseURL
		if !strings.Contains(raw, "://") {
			raw = "http://" + raw
		}
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			return fmt.Errorf("config: invalid ollama.base_url %q: must be like http://localhost:11434 or localhost:11434", c.Ollama.BaseURL)
		}
		if c.Privacy.LocalOnly && !isLocalHost(u.Hostname()) {
			return fmt.Errorf("config: privacy.local_only rejects non-local ollama.base_url %q", c.Ollama.BaseURL)
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
	serverNames := make(map[string]struct{}, len(c.Servers))
	for i, s := range c.Servers {
		if s.Name == "" {
			return fmt.Errorf("config: servers[%d] has an empty name", i)
		}
		if strings.Contains(s.Name, "__") {
			return fmt.Errorf("config: server %q contains reserved namespace delimiter __", s.Name)
		}
		if _, duplicate := serverNames[s.Name]; duplicate {
			return fmt.Errorf("config: duplicate server name %q", s.Name)
		}
		serverNames[s.Name] = struct{}{}
		switch s.Transport {
		case "sse", "streamable-http":
			if s.URL == "" {
				return fmt.Errorf("config: server %q uses %s transport but has no url", s.Name, s.Transport)
			}
			u, err := url.Parse(s.URL)
			if err != nil || u.Scheme == "" || u.Host == "" {
				return fmt.Errorf("config: server %q has invalid url %q", s.Name, s.URL)
			}
			if c.Privacy.LocalOnly && !isLocalHost(u.Hostname()) {
				return fmt.Errorf("config: privacy.local_only rejects non-local MCP server %q (%s)", s.Name, s.URL)
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

func isLocalHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	// Ollama commonly exports OLLAMA_HOST=0.0.0.0 (or ::) to describe its
	// local listen address. Connecting to an unspecified address still targets
	// this host, so it is safe for local-only client routing. Actual LAN/WAN
	// addresses remain rejected.
	return ip != nil && (ip.IsLoopback() || ip.IsUnspecified())
}

// CheckModelMemorySafe rejects cloud and clearly oversized local tiers. The
// 9B Qwen/Ornith and Gemma E2B profiles are allowed as explicit exclusive
// profiles; the router never auto-selects them and ModelManager unloads the
// previous chat model before switching. Override the remaining guard only for
// measured hardware profiles with LOCAL_AGENT_ALLOW_LARGE_MODELS=1.
func CheckModelMemorySafe(model string) error {
	// A hardware override may relax RAM limits, but it must never turn a cloud
	// alias into an allowed model for this local-only harness.
	if isRemoteModelAlias(model) {
		return fmt.Errorf("model %q is a cloud/remote alias and is not allowed by the local-only model policy", model)
	}
	if largeModelsAllowed() || !isMemoryRiskyModel(model) {
		return nil
	}
	return fmt.Errorf("model %q is not enabled for this local profile — cloud models and local tiers >=10B (including Gemma E4B+) can exhaust a 16GB machine. Use Qwen 0.8B/2B/4B, Phi-4 Mini, or an explicit exclusive Qwen/Ornith 9B or Gemma E2B profile. To override after measuring headroom, set LOCAL_AGENT_ALLOW_LARGE_MODELS=1", model)
}

// CheckLocalModelNameMemorySafe applies only the pre-discovery memory-tier
// heuristic. Execution location must come from Ollama inventory rather than
// words such as "cloud" or "remote" in a custom local tag.
func CheckLocalModelNameMemorySafe(model string) error {
	if largeModelsAllowed() || !isLocalMemoryRiskyModel(model) {
		return nil
	}
	return fmt.Errorf("model %q is outside the default local memory profile", model)
}

const maxDefaultLocalModelBytes int64 = 8 << 30

// CheckLocalModelSizeSafe enforces the 16GB profile from Ollama's actual
// on-disk weight size. Names are only hints (for example 8x7b and custom tags
// are ambiguous), so local-only admission must call this after discovery.
func CheckLocalModelSizeSafe(model string, size int64) error {
	if !largeModelsAllowed() && isLocalMemoryRiskyModel(model) {
		return fmt.Errorf("model %q is outside the default local memory profile", model)
	}
	if size <= 0 {
		return fmt.Errorf("model %q has no verified local weight size", model)
	}
	if largeModelsAllowed() || size <= maxDefaultLocalModelBytes {
		return nil
	}
	return fmt.Errorf("model %q uses %.1f GiB of local weights, above the %.0f GiB default budget for this 16GB profile; set LOCAL_AGENT_ALLOW_LARGE_MODELS=1 only after measuring memory headroom", model, float64(size)/(1<<30), float64(maxDefaultLocalModelBytes)/(1<<30))
}

func isLocalMemoryRiskyModel(model string) bool {
	m := strings.ToLower(model)
	if strings.Contains(m, "gemma") && !strings.Contains(m, ":e2b") {
		return true
	}
	if mt := paramBPattern.FindStringSubmatch(m); mt != nil {
		if b, err := strconv.ParseFloat(mt[1], 64); err == nil && b >= 10.0 {
			return true
		}
	}
	return false
}

func isRemoteModelAlias(model string) bool {
	name := strings.ToLower(model)
	return strings.Contains(name, "cloud") || strings.Contains(name, "remote")
}

// largeModelsAllowed reports whether the memory-safety guard is disabled.
func largeModelsAllowed() bool {
	v := os.Getenv("LOCAL_AGENT_ALLOW_LARGE_MODELS")
	return v == "1" || v == "true" || v == "yes"
}

// paramBPattern extracts a parameter count hint like ":9b" or ":0.8b" from a model tag.
var paramBPattern = regexp.MustCompile(`(\d+(?:\.\d+)?)\s*b\b`)

// isMemoryRiskyModel reports whether a model should remain blocked even from
// ordinary manual selection on a 16GB profile. Qwen/Ornith 9B and Gemma E2B
// are handled as exclusive profiles; larger Gemma tiers and >=10B tags remain
// guarded, and cloud entries are always rejected in local-only mode.
func isMemoryRiskyModel(model string) bool {
	m := strings.ToLower(model)
	if strings.Contains(m, "cloud") {
		return true
	}
	if strings.Contains(m, "gemma") && !strings.Contains(m, ":e2b") {
		return true
	}
	if mt := paramBPattern.FindStringSubmatch(m); mt != nil {
		if b, err := strconv.ParseFloat(mt[1], 64); err == nil && b >= 10.0 {
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

func findAndReadConfigFile() (string, []byte, error) {
	for _, path := range configFileCandidates() {
		data, err := configFileReader.ReadRegularFileNoFollow(path, maxStartupConfigBytes, configFileReadTimeout)
		if err == nil {
			return path, data, nil
		}
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		return "", nil, fmt.Errorf("read config %s: %w", path, err)
	}
	return "", nil, nil
}

// configFileCandidates returns config locations in precedence order. A
// repository-local file always wins. XDG_CONFIG_HOME is honored when it is an
// absolute path, followed by the historical ~/.config fallback. Cleaned paths
// are de-duplicated so the common XDG_CONFIG_HOME=$HOME/.config setup is read
// only once.
func configFileCandidates() []string {
	candidates := make([]string, 0, 6)
	seen := make(map[string]struct{}, 6)
	appendCandidate := func(path string) {
		path = filepath.Clean(path)
		if _, exists := seen[path]; exists {
			return
		}
		seen[path] = struct{}{}
		candidates = append(candidates, path)
	}
	appendConfigDir := func(root string) {
		if root == "" {
			return
		}
		dir := filepath.Join(root, "local-agent")
		appendCandidate(filepath.Join(dir, "config.yaml"))
		appendCandidate(filepath.Join(dir, "config.yml"))
	}

	appendCandidate("local-agent.yaml")
	appendCandidate("local-agent.yml")

	if xdgConfigHome := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(xdgConfigHome) {
		appendConfigDir(xdgConfigHome)
	}
	if home, err := os.UserHomeDir(); err == nil {
		appendConfigDir(filepath.Join(home, ".config"))
	}

	return candidates
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
	if v := os.Getenv("LOCAL_AGENT_LOCAL_ONLY"); v != "" {
		if localOnly, err := strconv.ParseBool(v); err == nil {
			cfg.Privacy.LocalOnly = localOnly
		}
	}
}

func parseEnvInt(v string, defaultVal int) int {
	if i, err := strconv.Atoi(v); err == nil {
		return i
	}
	return defaultVal
}
