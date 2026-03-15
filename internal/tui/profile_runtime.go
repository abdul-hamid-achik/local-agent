package tui

import (
	"fmt"
	"strings"

	"github.com/abdul-hamid-achik/local-agent/internal/config"
)

type modeContextSetter interface {
	SetModeContext(mode config.ModeContext)
}

func (m *Model) SetAgentProfileSource(agentsDir *config.AgentsDir, baseLoadedContext, activeProfile string) {
	m.agentsDir = agentsDir
	m.baseLoadedContext = baseLoadedContext
	m.agentProfile = activeProfile

	m.profileSkills = nil
	if agentsDir == nil || activeProfile == "" {
		return
	}

	if profile := agentsDir.GetAgent(activeProfile); profile != nil {
		m.profileSkills = append(m.profileSkills, profile.Skills...)
	}
}

func (m *Model) syncLoadedContext() {
	var parts []string
	if m.baseLoadedContext != "" {
		parts = append(parts, m.baseLoadedContext)
	}
	if m.manualLoadedContext != "" {
		parts = append(parts, m.manualLoadedContext)
	}
	if m.agentsDir != nil && m.agentProfile != "" {
		if profile := m.agentsDir.GetAgent(m.agentProfile); profile != nil && profile.SystemPrompt != "" {
			parts = append(parts, profile.SystemPrompt)
		}
	}

	if m.agent != nil {
		m.agent.SetLoadedContext(strings.Join(parts, "\n\n"))
	}
}

func (m *Model) applyAgentProfile(name string) error {
	if m.agentsDir == nil {
		return fmt.Errorf("no agent profiles available")
	}

	profile := m.agentsDir.GetAgent(name)
	if profile == nil {
		return fmt.Errorf("unknown agent profile: %s", name)
	}

	if m.skillMgr != nil {
		for _, skillName := range m.profileSkills {
			_ = m.skillMgr.Deactivate(skillName)
		}

		for _, skillName := range profile.Skills {
			if err := m.skillMgr.Activate(skillName); err != nil {
				return fmt.Errorf("activate profile skill %q: %w", skillName, err)
			}
		}

		m.profileSkills = append(m.profileSkills[:0], profile.Skills...)
		if m.agent != nil {
			m.agent.SetSkillContent(m.skillMgr.ActiveContent())
		}
	}

	if profile.Model != "" {
		if m.modelManager != nil {
			if err := m.modelManager.SetCurrentModel(profile.Model); err != nil {
				return fmt.Errorf("switch profile model: %w", err)
			}
		}
		m.model = profile.Model
	}

	m.agentProfile = name
	m.syncLoadedContext()
	return nil
}

func (m *Model) setRouterMode(mode config.ModeContext) {
	if setter, ok := m.router.(modeContextSetter); ok {
		setter.SetModeContext(mode)
	}
}
