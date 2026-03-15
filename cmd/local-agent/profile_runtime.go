package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/abdul-hamid-achik/local-agent/internal/agent"
	"github.com/abdul-hamid-achik/local-agent/internal/config"
	"github.com/abdul-hamid-achik/local-agent/internal/llm"
	"github.com/abdul-hamid-achik/local-agent/internal/skill"
)

func buildBaseLoadedContext(agentsDir *config.AgentsDir) string {
	var parts []string
	if agentsDir != nil && agentsDir.GetGlobalInstructions() != "" {
		parts = append(parts, agentsDir.GetGlobalInstructions())
	}
	if data, err := os.ReadFile("AGENT.md"); err == nil {
		parts = append(parts, string(data))
	}
	return strings.Join(parts, "\n\n")
}

func applyInitialAgentProfile(ag *agent.Agent, skillMgr *skill.Manager, modelManager *llm.ModelManager, agentsDir *config.AgentsDir, baseLoadedContext, profileName string) error {
	if ag != nil {
		ag.SetLoadedContext(baseLoadedContext)
	}

	if profileName == "" || agentsDir == nil {
		if ag != nil && skillMgr != nil {
			ag.SetSkillContent(skillMgr.ActiveContent())
		}
		return nil
	}

	profile := agentsDir.GetAgent(profileName)
	if profile == nil {
		return fmt.Errorf("unknown agent profile: %s", profileName)
	}

	if profile.Model != "" && modelManager != nil {
		if err := modelManager.SetCurrentModel(profile.Model); err != nil {
			return fmt.Errorf("set profile model: %w", err)
		}
	}

	if skillMgr != nil {
		for _, skillName := range profile.Skills {
			if err := skillMgr.Activate(skillName); err != nil {
				return fmt.Errorf("activate profile skill %q: %w", skillName, err)
			}
		}
	}

	loadedContext := baseLoadedContext
	if profile.SystemPrompt != "" {
		if loadedContext != "" {
			loadedContext += "\n\n"
		}
		loadedContext += profile.SystemPrompt
	}

	if ag != nil {
		ag.SetLoadedContext(loadedContext)
		if skillMgr != nil {
			ag.SetSkillContent(skillMgr.ActiveContent())
		}
	}

	return nil
}
