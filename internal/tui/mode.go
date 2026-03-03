package tui

import "github.com/abdulachik/local-agent/internal/config"

// Mode represents the operational mode of the TUI.
type Mode int

const (
	ModeAsk   Mode = iota // Direct Q&A, fast model, no tools
	ModePlan              // Planning, medium model, no tools
	ModeBuild             // Full execution, smart model, all tools
)

// ModeConfig holds the configuration for a single mode.
type ModeConfig struct {
	Label               string
	SystemPromptPrefix  string
	AllowTools          bool
	PreferredCapability config.ModelCapability
}

// DefaultModeConfigs returns the configuration for each mode.
func DefaultModeConfigs() [3]ModeConfig {
	return [3]ModeConfig{
		{ // ModeAsk
			Label:               "ASK",
			SystemPromptPrefix:  "Provide direct, concise answers. Do not use tools.",
			AllowTools:          false,
			PreferredCapability: config.CapabilitySimple,
		},
		{ // ModePlan
			Label:               "PLAN",
			SystemPromptPrefix:  "Help the user plan and design. Break down tasks into steps. Do not execute anything.",
			AllowTools:          false,
			PreferredCapability: config.CapabilityComplex,
		},
		{ // ModeBuild
			Label:               "BUILD",
			SystemPromptPrefix:  "Execute tasks using all available tools.",
			AllowTools:          true,
			PreferredCapability: config.CapabilityAdvanced,
		},
	}
}
