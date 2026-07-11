package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/abdul-hamid-achik/local-agent/internal/agent"
	"github.com/abdul-hamid-achik/local-agent/internal/config"
	"github.com/abdul-hamid-achik/local-agent/internal/llm"
	"github.com/abdul-hamid-achik/local-agent/internal/safeio"
	"github.com/abdul-hamid-achik/local-agent/internal/skill"
)

const maxProjectInstructionsBytes int64 = 1 << 20

var projectInstructionsReader = safeio.NewReader()
var projectInstructionsReadTimeout = safeio.StartupReadTimeout

func buildBaseLoadedContext(agentsDir *config.AgentsDir) (string, error) {
	workDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve project instructions directory: %w", err)
	}
	return buildBaseLoadedContextAt(agentsDir, workDir)
}

func buildBaseLoadedContextAt(agentsDir *config.AgentsDir, workDir string) (string, error) {
	var parts []string
	if agentsDir != nil && agentsDir.GetGlobalInstructions() != "" {
		parts = append(parts, agentsDir.GetGlobalInstructions())
	}
	// AGENTS.md is the cross-harness convention. Keep AGENT.md as a legacy
	// fallback so existing local-agent projects continue to work.
	for _, name := range []string{"AGENTS.md", "AGENT.md"} {
		path := filepath.Join(workDir, name)
		data, err := projectInstructionsReader.ReadRegularFileNoFollow(path, maxProjectInstructionsBytes, projectInstructionsReadTimeout)
		if err == nil {
			parts = append(parts, string(data))
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("read project instructions %s: %w", path, err)
		}
	}
	return strings.Join(parts, "\n\n"), nil
}

func applyInitialAgentProfile(ag *agent.Agent, skillMgr *skill.Manager, modelManager *llm.ModelManager, agentsDir *config.AgentsDir, baseLoadedContext, profileName string) error {
	if profileName == "" {
		if ag != nil {
			ag.SetLoadedContext(baseLoadedContext)
			if skillMgr != nil {
				ag.SetSkillContent(skillMgr.ActiveContent())
			}
		}
		return nil
	}
	if agentsDir == nil {
		return fmt.Errorf("agent profile %q requested but no profile directory is available", profileName)
	}

	profile := agentsDir.GetAgent(profileName)
	if profile == nil {
		return fmt.Errorf("unknown agent profile: %s", profileName)
	}
	if skillMgr != nil {
		for _, skillName := range profile.Skills {
			if !skillMgr.Has(skillName) {
				return fmt.Errorf("profile skill %q is not available", skillName)
			}
		}
	}

	if profile.Model != "" && modelManager != nil {
		if err := config.CheckModelMemorySafe(profile.Model); err != nil {
			return fmt.Errorf("profile model: %w", err)
		}
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
		ag.SetMCPServerScope(profile.MCPServers)
		ag.SetLoadedContext(loadedContext)
		if skillMgr != nil {
			ag.SetSkillContent(skillMgr.ActiveContent())
		}
	}

	return nil
}
