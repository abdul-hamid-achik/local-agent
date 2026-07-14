package ui

import (
	"github.com/abdul-hamid-achik/local-agent/internal/agent"
	"github.com/abdul-hamid-achik/local-agent/internal/config"
)

// Mode represents the conversational preset selected in the TUI. Durable goal
// execution is an explicit, separate lifecycle entered through /goal.
type Mode int

const (
	ModeNormal Mode = iota // Interactive work with approval-gated mutations.
	ModePlan               // Read-only exploration and planning.
	ModeAuto               // Proactive work with full tools and configured approvals.
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
			SystemPromptPrefix:  "Work proactively toward the user's request. Workspace-confined file changes and host-catalogued local ecosystem operations may proceed automatically. Additional roots explicitly listed in Environment may be read but never mutated; unlisted external paths remain unavailable. Pause for shell commands, deletion, secrets, human decisions, and unknown tools.",
			ToolPolicy:          agent.BuildToolPolicy(),
			PreferredCapability: config.CapabilityAdvanced,
			RouterMode:          config.ModeBuildContext,
		},
	}
}

func agentAuthorityMode(mode Mode) agent.AuthorityMode {
	switch mode {
	case ModePlan:
		return agent.AuthorityPlan
	case ModeAuto:
		return agent.AuthorityAutoScoped
	default:
		return agent.AuthorityNormal
	}
}

// presentedMode is the authority currently communicated by the TUI. The
// ambient selector remains in m.mode so Shift+Tab can prepare a later
// conversational turn, but an attached Goal Runtime owns AUTO authority until
// the session is reset. Rendering the ambient value during that lifecycle
// would claim a PLAN/NORMAL contract that the next goal turn does not use.
func (m *Model) presentedMode() Mode {
	if m != nil && m.goalRuntime != nil {
		return ModeAuto
	}
	if m == nil || m.mode < ModeNormal || m.mode > ModeAuto {
		return ModeNormal
	}
	return m.mode
}

func (m *Model) syncComposerAuthority() {
	if m == nil {
		return
	}
	configureComposerMode(&m.input, m.isDark, m.presentedMode(), m.reducedMotion)
}
