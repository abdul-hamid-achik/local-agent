package ui

import (
	"github.com/abdul-hamid-achik/local-agent/internal/agent"
	"github.com/abdul-hamid-achik/local-agent/internal/config"
)

// Mode represents the operational authority of the TUI.
type Mode int

const (
	ModeNormal Mode = iota // Interactive work with approval-gated mutations.
	ModePlan               // Read-only exploration and planning.
	ModeAuto               // Durable Goal Runtime supervised until a safe stop.
)

// Legacy source aliases keep embeddings and older tests compiling while saved
// ASK/BUILD values migrate through the session-version boundary below.
const (
	ModeAsk   = ModeNormal
	ModeBuild = ModeAuto
)

// ModeConfig holds the configuration for a single mode.
type ModeConfig struct {
	Label               string
	SystemPromptPrefix  string
	ToolPolicy          agent.ToolPolicy
	PreferredCapability config.ModelCapability
	RouterMode          config.ModeContext
}

// DefaultModeConfigs returns the configuration for each mode.
func DefaultModeConfigs() [3]ModeConfig {
	return [3]ModeConfig{
		{ // ModeNormal
			Label:               "NORMAL",
			SystemPromptPrefix:  "Work interactively with the user. Use tools when useful; every mutation remains subject to the configured approval policy.",
			ToolPolicy:          agent.BuildToolPolicy(),
			PreferredCapability: config.CapabilityAdvanced,
			RouterMode:          config.ModeBuildContext,
		},
		{ // ModePlan
			Label:               "PLAN",
			SystemPromptPrefix:  "Help the user plan and design. Break down tasks into steps. Use tools to read and explore, but do not modify files.",
			ToolPolicy:          agent.PlanToolPolicy(),
			PreferredCapability: config.CapabilityComplex,
			RouterMode:          config.ModePlanContext,
		},
		{ // ModeAuto
			Label:               "AUTO",
			SystemPromptPrefix:  "Execute only the active durable goal under its host budgets, approval policy, Cortex verification, and safe stop conditions.",
			ToolPolicy:          agent.BuildToolPolicy(),
			PreferredCapability: config.CapabilityAdvanced,
			RouterMode:          config.ModeBuildContext,
		},
	}
}
