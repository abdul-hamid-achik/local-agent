package main

import (
	"fmt"

	"github.com/abdul-hamid-achik/local-agent/internal/config"
)

func shouldPinStartupModel(modelOverride string, autoSelect bool) bool {
	return modelOverride != "" || !autoSelect
}

// resolveStartupModel applies a successful Ollama inventory to automatic
// routing and validates the final selection for local-only mode. When
// inventoryKnown is false, it preserves the configured selection so the TUI
// can still start and report an offline Ollama diagnostic; the independent
// cloud-alias and memory checks still apply.
func resolveStartupModel(
	modelName string,
	modelPinned bool,
	localOnly bool,
	modelConfig *config.ModelConfig,
	discovered []string,
	inventoryKnown bool,
	router availabilityAwareRouter,
) (string, []string, error) {
	if err := config.CheckModelMemorySafe(modelName); err != nil {
		return "", nil, err
	}

	modelList := modelConfig.CatalogChatModels()
	if !inventoryKnown {
		return modelName, modelList, nil
	}

	modelList = modelConfig.AvailableLocalChatModels(discovered)
	router.SetAvailableModels(modelList)
	if !modelPinned {
		modelName = router.ResolveAvailableModel(modelName)
	}
	if modelName == "" {
		return "", modelList, fmt.Errorf("no compatible local chat model is installed; pull a configured model such as %q", modelConfig.DefaultModel)
	}
	if localOnly {
		if err := config.CheckModelAvailableLocally(modelName, discovered); err != nil {
			return "", modelList, err
		}
	}
	return modelName, modelList, nil
}

func selectHeadlessModel(current, prompt string, pinned bool, router config.ModelRouter, mode config.ModeContext) string {
	if pinned || router == nil {
		return current
	}
	return router.SelectModelForMode(prompt, mode)
}
