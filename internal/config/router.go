package config

import (
	"strings"
)

type TaskComplexity string

const (
	ComplexitySimple   TaskComplexity = "simple"
	ComplexityMedium   TaskComplexity = "medium"
	ComplexityComplex  TaskComplexity = "complex"
	ComplexityAdvanced TaskComplexity = "advanced"
)

var simpleIndicators = []string{
	"what is", "how do i", "explain", "what does",
	"find", "search", "list", "show", "get",
	"print", "echo", "read", "cat", "ls",
	"simple", "quick", "fast",
}

var mediumIndicators = []string{
	"create", "write", "generate", "add", "modify",
	"change", "update", "fix", "refactor",
	"function", "class", "variable", "test",
	"script", "command", "file", "directory",
}

var complexIndicators = []string{
	"debug", "error", "bug", "issue", "problem",
	"refactor", "architecture", "design", "review",
	"multiple", "several", "across", "migrate",
	"optimize", "performance", "security",
	"explain why", "analyze", "compare",
}

var advancedIndicators = []string{
	"build a", "create a", "implement", "develop",
	"full stack", "system", "infrastructure",
	"multi-step", "complex", "comprehensive",
	"security audit", "architecture design",
}

type Router struct {
	config *ModelConfig
}

func NewRouter(cfg *ModelConfig) *Router {
	return &Router{
		config: cfg,
	}
}

func (r *Router) ClassifyTaskComplexity(query string) TaskComplexity {
	return ClassifyTask(query)
}

func (r *Router) SelectModel(query string) string {
	complexity := r.ClassifyTaskComplexity(query)
	return r.config.SelectModelForTask(string(complexity))
}

func (r *Router) GetFallbackChain(currentModel string) []string {
	chain := r.config.FallbackChain

	for i, model := range chain {
		if model == currentModel {
			return chain[i:]
		}
	}

	return chain
}

func (r *Router) GetModelForCapability(capability ModelCapability) string {
	for _, m := range r.config.Models {
		if m.Capability == capability {
			return m.Name
		}
	}
	return r.config.DefaultModel
}

func (r *Router) ForceModel(name string) (*Model, error) {
	return r.config.GetModel(name)
}

func (r *Router) ListModels() []Model {
	return r.config.Models
}

func (r *Router) GetDefaultModel() string {
	return r.config.DefaultModel
}

func ClassifyTask(query string) TaskComplexity {
	lowerQuery := strings.ToLower(query)
	wordCount := len(strings.Fields(query))

	score := 0

	for _, indicator := range simpleIndicators {
		if strings.Contains(lowerQuery, indicator) {
			score -= 2
		}
	}

	for _, indicator := range mediumIndicators {
		if strings.Contains(lowerQuery, indicator) {
			score += 1
		}
	}

	for _, indicator := range complexIndicators {
		if strings.Contains(lowerQuery, indicator) {
			score += 2
		}
	}

	for _, indicator := range advancedIndicators {
		if strings.Contains(lowerQuery, indicator) {
			score += 3
		}
	}

	if wordCount > 50 {
		score += 2
	}

	if strings.Contains(lowerQuery, "why") || strings.Contains(lowerQuery, "reason") {
		score += 1
	}

	if strings.Contains(lowerQuery, "how") && wordCount > 10 {
		score += 1
	}

	switch {
	case score <= -2:
		return ComplexitySimple
	case score <= 1:
		return ComplexityMedium
	case score <= 4:
		return ComplexityComplex
	default:
		return ComplexityAdvanced
	}
}
