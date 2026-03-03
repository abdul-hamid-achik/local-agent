package config

import "fmt"

type ModelFamily string

const (
	FamilyQwen3   ModelFamily = "qwen3"
	FamilyQwen35  ModelFamily = "qwen3.5"
	FamilyLlama   ModelFamily = "llama"
	FamilyMistral ModelFamily = "mistral"
)

type ModelCapability int

const (
	CapabilitySimple ModelCapability = iota
	CapabilityMedium
	CapabilityComplex
	CapabilityAdvanced
)

type Model struct {
	Name        string          `yaml:"name"`
	Family      ModelFamily     `yaml:"family"`
	DisplayName string          `yaml:"display_name"`
	Size        string          `yaml:"size"`
	Parameters  string          `yaml:"parameters"`
	ContextSize int             `yaml:"context_size"`
	Capability  ModelCapability `yaml:"capability"`
	Speed       float64         `yaml:"speed"` // 1.0 = baseline
	UseCases    []string        `yaml:"use_cases"`
	Description string          `yaml:"description"`
	Default     bool            `yaml:"default,omitempty"`
}

type ModelConfig struct {
	Models        []Model  `yaml:"models"`
	DefaultModel  string   `yaml:"default_model"`
	FallbackChain []string `yaml:"fallback_chain"`
	AutoSelect    bool     `yaml:"auto_select"`
	EmbedModel    string   `yaml:"embed_model,omitempty"`
}

func DefaultModels() []Model {
	return []Model{
		{
			Name:        "qwen3.5:0.8b",
			Family:      FamilyQwen35,
			DisplayName: "Qwen 3.5 0.8B",
			Size:        "0.8B",
			Parameters:  "0.8 billion",
			ContextSize: 262144,
			Capability:  CapabilitySimple,
			Speed:       4.0,
			UseCases:    []string{"quick_answers", "simple_tools", "single_file_edits"},
			Description: "Fast, lightweight model for simple tasks and quick answers",
			Default:     true,
		},
		{
			Name:        "qwen3.5:2b",
			Family:      FamilyQwen35,
			DisplayName: "Qwen 3.5 2B",
			Size:        "2B",
			Parameters:  "2 billion",
			ContextSize: 262144,
			Capability:  CapabilityMedium,
			Speed:       2.5,
			UseCases:    []string{"code_completion", "simple_refactoring", "explanations"},
			Description: "Balanced model for medium complexity tasks",
			Default:     false,
		},
		{
			Name:        "qwen3.5:4b",
			Family:      FamilyQwen35,
			DisplayName: "Qwen 3.5 4B",
			Size:        "4B",
			Parameters:  "4 billion",
			ContextSize: 262144,
			Capability:  CapabilityComplex,
			Speed:       1.5,
			UseCases:    []string{"multi_step_reasoning", "code_review", "debugging", "refactoring"},
			Description: "Capable model for complex reasoning and code analysis",
			Default:     false,
		},
		{
			Name:        "qwen3.5:9b",
			Family:      FamilyQwen35,
			DisplayName: "Qwen 3.5 9B",
			Size:        "9B",
			Parameters:  "9 billion",
			ContextSize: 262144,
			Capability:  CapabilityAdvanced,
			Speed:       1.0,
			UseCases:    []string{"complex_reasoning", "architecture", "full_stack", "advanced_debugging"},
			Description: "Full capability model for advanced tasks",
			Default:     false,
		},
	}
}

func DefaultModelConfig() ModelConfig {
	models := DefaultModels()
	return ModelConfig{
		Models:        models,
		DefaultModel:  "qwen3.5:0.8b",
		FallbackChain: []string{"qwen3.5:0.8b", "qwen3.5:2b", "qwen3.5:4b", "qwen3.5:9b"},
		AutoSelect:    true,
		EmbedModel:    "nomic-embed-text",
	}
}

func (m *Model) IsSimpleTask() bool {
	return m.Capability <= CapabilityMedium
}

func (m *Model) IsComplexTask() bool {
	return m.Capability >= CapabilityComplex
}

func (mc *ModelConfig) GetModel(name string) (*Model, error) {
	for _, m := range mc.Models {
		if m.Name == name {
			return &m, nil
		}
	}
	return nil, fmt.Errorf("model not found: %s", name)
}

func (mc *ModelConfig) GetDefaultModel() *Model {
	for _, m := range mc.Models {
		if m.Default {
			return &m
		}
	}
	if len(mc.Models) > 0 {
		return &mc.Models[len(mc.Models)-1]
	}
	return nil
}

func (mc *ModelConfig) SelectModelForTask(taskComplexity string) string {
	if !mc.AutoSelect {
		return mc.DefaultModel
	}

	switch taskComplexity {
	case "simple":
		return mc.Models[0].Name
	case "medium":
		for _, m := range mc.Models {
			if m.Capability == CapabilityMedium {
				return m.Name
			}
		}
	case "complex":
		for _, m := range mc.Models {
			if m.Capability == CapabilityComplex {
				return m.Name
			}
		}
	case "advanced":
		return mc.DefaultModel
	}

	return mc.DefaultModel
}
