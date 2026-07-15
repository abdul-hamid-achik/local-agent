package config

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/abdul-hamid-achik/local-agent/internal/execpath"
	"github.com/abdul-hamid-achik/local-agent/internal/netpolicy"
	"github.com/abdul-hamid-achik/local-agent/internal/safeio"
	"gopkg.in/yaml.v3"
)

const maxStartupConfigBytes int64 = 1 << 20
const maxRepoMCPExecutableBytes int64 = 256 << 20
const repoMCPTrustEnv = "LOCAL_AGENT_TRUST_REPO_MCP"

var configFileReader = safeio.NewReader()
var repoMCPExecutableReader = safeio.NewReader()
var configFileReadTimeout = safeio.StartupReadTimeout

type Config struct {
	// SourcePath is the host-resolved config selected by repository/XDG
	// precedence. It is runtime metadata only and is never serialized.
	SourcePath string         `yaml:"-" json:"-"`
	Ollama     OllamaConfig   `yaml:"ollama"`
	Model      ModelConfig    `yaml:"model,omitempty"`
	Agents     AgentsConfig   `yaml:"agents,omitempty"`
	Servers    []ServerConfig `yaml:"servers,omitempty"`
	// SkillsDir is decoded only to reject the retired split skill root with a
	// clear migration error instead of silently ignoring an old configuration.
	SkillsDir    string        `yaml:"skills_dir,omitempty"`
	ICE          ICEConfig     `yaml:"ice,omitempty"`
	AgentProfile string        `yaml:"agent_profile,omitempty"`
	Tools        ToolsConfig   `yaml:"tools,omitempty"`
	Experts      ExpertsConfig `yaml:"experts,omitempty"`
	Privacy      PrivacyConfig `yaml:"privacy,omitempty"`
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
	Timeout           string `yaml:"timeout,omitempty"` // e.g., "30s", "2m"
	MaxGrepResults    int    `yaml:"max_grep_results,omitempty"`
	MaxIterations     int    `yaml:"max_iterations,omitempty"`
	AutoMaxIterations int    `yaml:"auto_max_iterations,omitempty"`
}

// ExpertsConfig controls application-level read-only Team/Swarm/MoE
// consultation. Zero concurrency/fan-out values mean machine-adaptive auto;
// explicit values are safety caps and never force the resource planner above
// measured capacity.
type ExpertsConfig struct {
	Enabled                     bool   `yaml:"enabled"`
	MaxConcurrentInference      int    `yaml:"max_concurrent_inference,omitempty"`
	MaxConcurrentDistinctModels int    `yaml:"max_concurrent_distinct_models,omitempty"`
	MaxTeamExperts              int    `yaml:"max_team_experts,omitempty"`
	MaxSwarmWorkers             int    `yaml:"max_swarm_workers,omitempty"`
	MaxMoEExperts               int    `yaml:"max_moe_experts,omitempty"`
	MaxEvalTokens               int    `yaml:"max_eval_tokens,omitempty"`
	Timeout                     string `yaml:"timeout,omitempty"`
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
	Name             string          `yaml:"name" json:"name"`
	Command          string          `yaml:"command,omitempty" json:"command,omitempty"`
	Args             []string        `yaml:"args,omitempty" json:"args,omitempty"`
	Env              []string        `yaml:"env,omitempty" json:"env,omitempty"`
	Transport        string          `yaml:"transport,omitempty" json:"transport,omitempty"`
	URL              string          `yaml:"url,omitempty" json:"url,omitempty"`
	Trust            *MCPTrustConfig `yaml:"trust,omitempty" json:"trust,omitempty"`
	ExecutableSHA256 string          `yaml:"-" json:"-"`
}

// RepoMCPTrustError reports executable MCP authority supplied by a
// repository-local configuration before any server process is started. The
// digest binds consent to both the selected repository path and the exact
// STDIO command, arguments, and environment.
type RepoMCPTrustError struct {
	SourcePath  string
	Digest      string
	ServerCount int
}

func (e *RepoMCPTrustError) Error() string {
	return fmt.Sprintf(
		"repository-local config %q requests %d STDIO MCP server process(es); refusing to start them without explicit trust (re-run with %s=%s for this exact executable configuration)",
		e.SourcePath,
		e.ServerCount,
		repoMCPTrustEnv,
		e.Digest,
	)
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
			Timeout:           "30s",
			MaxGrepResults:    500,
			MaxIterations:     10,
			AutoMaxIterations: 40,
		},
		Experts: ExpertsConfig{
			Enabled:       true,
			MaxEvalTokens: 768,
			Timeout:       "90s",
		},
		Privacy: PrivacyConfig{LocalOnly: true},
	}
}

func Load() (*Config, error) {
	cfg, _, err := loadConfigAndAgents()
	return cfg, err
}

func loadConfigAndAgents() (*Config, *AgentsDir, error) {
	cfg := defaults()

	localPath, data, err := findAndReadConfigFile()
	if err != nil {
		return nil, nil, err
	}
	repoConfig := isRepositoryLocalConfigPath(localPath)
	var repoConfiguredServers []ServerConfig
	repoSelectedAgentsDir := false
	if localPath != "" {
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return nil, nil, fmt.Errorf("parse config %s: %w", localPath, err)
		}
		if repoConfig {
			repoConfiguredServers = append([]ServerConfig(nil), cfg.Servers...)
			repoSelectedAgentsDir = strings.TrimSpace(cfg.Agents.Dir) != ""
		}
		cfg.SourcePath = localPath
		if absolute, absErr := filepath.Abs(localPath); absErr == nil {
			cfg.SourcePath = filepath.Clean(absolute)
		}
	}

	// Environment selection must be applied before loading the shared agents
	// root. Otherwise LOCAL_AGENT_AGENTS_DIR changes the returned config but
	// silently preloads metadata from a different directory.
	applyEnvOverrides(&cfg)
	if os.Getenv("LOCAL_AGENT_AGENTS_DIR") != "" {
		// Process environment is user-controlled startup authority. Do not
		// attribute an environment-selected agents root to repository config.
		repoSelectedAgentsDir = false
	}

	var agentsData *AgentsDir
	if cfg.Agents.AutoLoad {
		agentsDir, resolveErr := resolveAgentsDir(cfg.Agents.Dir)
		if resolveErr != nil {
			return nil, nil, resolveErr
		}
		agentsData, err = LoadAgentsDir(agentsDir)
		if err != nil {
			return nil, nil, fmt.Errorf("load agents directory %s: %w", agentsDir, err)
		}
		if agentsData != nil {
			if cfg.Ollama.Model == "" {
				cfg.Ollama.Model = cfg.Model.DefaultModel
			}

			if len(cfg.Servers) == 0 && agentsData.HasMCP() {
				cfg.Servers = agentsData.GetMCPServers()
			}
		}
	}

	clampNumCtxForMemory(&cfg)
	if err := cfg.Validate(); err != nil {
		return nil, nil, err
	}
	if repoConfig {
		var repoAuthorities []ServerConfig
		if len(repoConfiguredServers) > 0 {
			repoAuthorities = cfg.Servers
		}
		if len(repoConfiguredServers) == 0 && repoSelectedAgentsDir && agentsData != nil {
			repoAuthorities = cfg.Servers
		}
		if err := requireRepositoryMCPTrust(cfg.SourcePath, repoAuthorities); err != nil {
			return nil, nil, err
		}
	}
	return &cfg, agentsData, nil
}

func isRepositoryLocalConfigPath(path string) bool {
	return path == "local-agent.yaml" || path == "local-agent.yml"
}

type repoMCPExecutableAuthority struct {
	Name             string          `json:"name"`
	Command          string          `json:"command"`
	ResolvedCommand  string          `json:"resolved_command"`
	ExecutablePath   string          `json:"executable_path"`
	ExecutableSHA256 string          `json:"executable_sha256"`
	Args             []string        `json:"args,omitempty"`
	Env              []string        `json:"env,omitempty"`
	Trust            *MCPTrustConfig `json:"trust,omitempty"`
}

type repoMCPTrustMaterial struct {
	Version    int                          `json:"version"`
	SourcePath string                       `json:"source_path"`
	Servers    []repoMCPExecutableAuthority `json:"servers"`
}

func requireRepositoryMCPTrust(sourcePath string, servers []ServerConfig) error {
	authorities := make([]repoMCPExecutableAuthority, 0, len(servers))
	serverIndexes := make([]int, 0, len(servers))
	for index, server := range servers {
		if server.Transport != "" && server.Transport != "stdio" {
			continue
		}
		authority, err := repositoryMCPExecutableAuthority(server)
		if err != nil {
			return fmt.Errorf("resolve repository MCP executable %q: %w", server.Name, err)
		}
		authorities = append(authorities, authority)
		serverIndexes = append(serverIndexes, index)
	}
	if len(authorities) == 0 {
		return nil
	}

	// Server ordering does not change process authority. Sort canonical copies
	// while retaining the original indexes used to pin runtime launch paths.
	sortedAuthorities := append([]repoMCPExecutableAuthority(nil), authorities...)
	sort.Slice(sortedAuthorities, func(i, j int) bool {
		left, _ := json.Marshal(sortedAuthorities[i])
		right, _ := json.Marshal(sortedAuthorities[j])
		return string(left) < string(right)
	})
	material, err := json.Marshal(repoMCPTrustMaterial{
		Version:    3,
		SourcePath: filepath.Clean(sourcePath),
		Servers:    sortedAuthorities,
	})
	if err != nil {
		return fmt.Errorf("encode repository MCP trust material: %w", err)
	}
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(material))
	if strings.TrimSpace(os.Getenv(repoMCPTrustEnv)) == digest {
		for authorityIndex, serverIndex := range serverIndexes {
			// Pin startup to the exact symlink-resolved target covered by the
			// digest so neither PATH nor a retargeted launcher symlink can select a
			// different executable.
			servers[serverIndex].Command = authorities[authorityIndex].ExecutablePath
			servers[serverIndex].ExecutableSHA256 = authorities[authorityIndex].ExecutableSHA256
		}
		return nil
	}
	return &RepoMCPTrustError{
		SourcePath:  sourcePath,
		Digest:      digest,
		ServerCount: len(authorities),
	}
}

func repositoryMCPExecutableAuthority(server ServerConfig) (repoMCPExecutableAuthority, error) {
	trust, err := ResolveMCPTrust(server)
	if err != nil {
		return repoMCPExecutableAuthority{}, fmt.Errorf("resolve MCP trust: %w", err)
	}
	resolved, err := execpath.Resolve(server.Command)
	if err != nil {
		return repoMCPExecutableAuthority{}, err
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return repoMCPExecutableAuthority{}, fmt.Errorf("make executable path absolute: %w", err)
	}
	resolved = filepath.Clean(resolved)
	realPath, err := filepath.EvalSymlinks(resolved)
	if err != nil {
		return repoMCPExecutableAuthority{}, fmt.Errorf("resolve executable symlinks: %w", err)
	}
	realPath, err = filepath.Abs(realPath)
	if err != nil {
		return repoMCPExecutableAuthority{}, fmt.Errorf("make executable target absolute: %w", err)
	}
	realPath = filepath.Clean(realPath)

	info, err := os.Stat(realPath)
	if err != nil {
		return repoMCPExecutableAuthority{}, fmt.Errorf("inspect executable: %w", err)
	}
	if !info.Mode().IsRegular() {
		return repoMCPExecutableAuthority{}, fmt.Errorf("resolved executable path %q is not a regular file", realPath)
	}
	contents, err := repoMCPExecutableReader.ReadRegularFileNoFollow(
		realPath, maxRepoMCPExecutableBytes, safeio.StartupReadTimeout,
	)
	if err != nil {
		return repoMCPExecutableAuthority{}, fmt.Errorf("read executable for trust digest: %w", err)
	}
	contentHash := sha256.Sum256(contents)

	return repoMCPExecutableAuthority{
		Name:             server.Name,
		Command:          server.Command,
		ResolvedCommand:  resolved,
		ExecutablePath:   realPath,
		ExecutableSHA256: fmt.Sprintf("sha256:%x", contentHash),
		Args:             append([]string(nil), server.Args...),
		Env:              append([]string(nil), server.Env...),
		Trust:            trust,
	}, nil
}

// resolveAgentsDir returns an explicit root whenever shared agent metadata is
// enabled. FindAgentsDir preserves compatibility with an existing selected
// root, while a fresh install consistently targets ~/.agents without creating
// it merely by reading configuration.
func resolveAgentsDir(configured string) (string, error) {
	if configured != "" {
		return configured, nil
	}
	if discovered := FindAgentsDir(); discovered != "" {
		return discovered, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve default agents directory: %w", err)
	}
	return filepath.Join(home, ".agents"), nil
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
	if strings.TrimSpace(c.SkillsDir) != "" {
		return errors.New("config: skills_dir is no longer supported; move skills under the selected agents directory at skills/<name>/SKILL.md")
	}
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
	for name, value := range map[string]int{
		"max_concurrent_inference":       c.Experts.MaxConcurrentInference,
		"max_concurrent_distinct_models": c.Experts.MaxConcurrentDistinctModels,
		"max_team_experts":               c.Experts.MaxTeamExperts,
		"max_swarm_workers":              c.Experts.MaxSwarmWorkers,
		"max_moe_experts":                c.Experts.MaxMoEExperts,
	} {
		if value < 0 || value > 16 {
			return fmt.Errorf("config: experts.%s must be 0..16, got %d", name, value)
		}
	}
	if c.Experts.MaxEvalTokens < 1 || c.Experts.MaxEvalTokens > 8192 {
		return fmt.Errorf("config: experts.max_eval_tokens must be 1..8192, got %d", c.Experts.MaxEvalTokens)
	}
	if d, err := time.ParseDuration(c.Experts.Timeout); err != nil {
		return fmt.Errorf("config: invalid experts.timeout %q: %w", c.Experts.Timeout, err)
	} else if d < time.Second || d > 10*time.Minute {
		return fmt.Errorf("config: experts.timeout must be between 1s and 10m, got %s", c.Experts.Timeout)
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
		if _, err := ResolveMCPTrust(s); err != nil {
			return fmt.Errorf("config: server %q trust: %w", s.Name, err)
		}
	}
	return nil
}

func isLocalHost(host string) bool {
	// Ollama commonly exports OLLAMA_HOST=0.0.0.0 (or ::) to describe its
	// local listen address. Connecting to an unspecified address still targets
	// this host, so it is safe for local-only client routing. Actual LAN/WAN
	// addresses remain rejected.
	return netpolicy.IsLocalHost(host)
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
	return loadConfigAndAgents()
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
	if v := os.Getenv("LOCAL_AGENT_TOOLS_AUTO_MAX_ITER"); v != "" {
		cfg.Tools.AutoMaxIterations = parseEnvInt(v, cfg.Tools.AutoMaxIterations)
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
