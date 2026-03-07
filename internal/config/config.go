package config

import (
	"fmt"
	"os"
	"path/filepath"

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
}

type AgentsConfig struct {
	Dir      string `yaml:"dir,omitempty"`
	AutoLoad bool   `yaml:"auto_load"`
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
			NumCtx:  262144,
		},
		Model: modelCfg,
		Agents: AgentsConfig{
			Dir:      "",
			AutoLoad: true,
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
	return &cfg, nil
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
}
