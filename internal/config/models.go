package config

import "fmt"

type ModelFamily string

const (
	FamilyQwen3   ModelFamily = "qwen3"
	FamilyQwen35  ModelFamily = "qwen3.5"
	FamilyGemma   ModelFamily = "gemma"
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
			Default:     false,
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
			Default:     true,
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
		// Gemma 4 local tiers are listed for VISIBILITY but are memory-unsafe on
		// 16GB (gemma4:e2b is ~7.2GB on disk despite its "2B" name — it OOM'd the
		// machine). isMemoryRiskyModel() flags them: the router never auto-selects
		// them, the picker shows them greyed with a reason, and switching to one
		// is blocked unless LOCAL_AGENT_ALLOW_LARGE_MODELS=1. qwen3.5:9b (~6.6GB)
		// and cloud models remain excluded entirely.
		{
			Name:        "gemma4:e2b",
			Family:      FamilyGemma,
			DisplayName: "Gemma 4 E2B",
			Size:        "7.2GB",
			Parameters:  "2B effective",
			ContextSize: 131072,
			Capability:  CapabilityMedium,
			Speed:       2.2,
			UseCases:    []string{"unavailable_16gb"},
			Description: "Native tool calling, but ~7.2GB — unsafe on 16GB.",
			Default:     false,
		},
		{
			Name:        "gemma4:e4b",
			Family:      FamilyGemma,
			DisplayName: "Gemma 4 E4B",
			Size:        "9.6GB",
			Parameters:  "4B effective",
			ContextSize: 131072,
			Capability:  CapabilityComplex,
			Speed:       1.3,
			UseCases:    []string{"unavailable_16gb"},
			Description: "Stronger Gemma, but ~9.6GB — unsafe on 16GB.",
			Default:     false,
		},
	}
}

func DefaultModelConfig() ModelConfig {
	models := DefaultModels()
	return ModelConfig{
		Models:        models,
		DefaultModel:  "qwen3.5:2b",
		FallbackChain: []string{"qwen3.5:2b", "qwen3.5:0.8b", "qwen3.5:4b"},
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
	if !mc.AutoSelect || len(mc.Models) == 0 {
		return mc.DefaultModel
	}

	switch taskComplexity {
	case "simple":
		return mc.firstSafe(CapabilitySimple, mc.Models[0].Name)
	case "medium":
		return mc.firstSafe(CapabilityMedium, mc.DefaultModel)
	case "complex":
		return mc.firstSafe(CapabilityComplex, mc.DefaultModel)
	case "advanced":
		// There is no safe local tier above 4B on 16GB (larger models OOM), so
		// advanced tasks use the most capable SAFE model — the complex tier.
		return mc.firstSafe(CapabilityComplex, mc.DefaultModel)
	}

	return mc.DefaultModel
}

// firstSafe returns the first model at the given capability that is memory-safe
// to auto-load (skips Gemma/large tiers so the router never OOMs the machine),
// falling back to fallbackName if none match.
func (mc *ModelConfig) firstSafe(cap ModelCapability, fallbackName string) string {
	for _, m := range mc.Models {
		if m.Capability == cap && CheckModelMemorySafe(m.Name) == nil {
			return m.Name
		}
	}
	return fallbackName
}
