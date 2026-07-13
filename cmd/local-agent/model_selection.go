package main

import (
	"fmt"

	"github.com/abdul-hamid-achik/local-agent/internal/config"
)

func shouldPinStartupModel(modelOverride string, autoSelect bool) bool {
	return modelOverride != "" || !autoSelect
}

// resolveStartupModel keeps the manual selection inventory separate from the
// local-only automatic routing inventory. When inventoryKnown is false, it
// preserves the configured selection so the TUI can still start and report an
// offline Ollama diagnostic. The name-based local memory guard remains active
// in that fallback because no verified byte size is available yet; it
// deliberately does not infer execution location.
func resolveStartupModel(
	modelName string,
	modelPinned bool,
	localOnly bool,
	modelConfig *config.ModelConfig,
	manuallySelectable []string,
	autoRoutable []string,
	inventoryKnown bool,
	router availabilityAwareRouter,
) (string, []string, error) {
	if !inventoryKnown {
		if err := config.CheckLocalModelNameMemorySafe(modelName); err != nil {
			return "", nil, err
		}
	}
	if localOnly {
		check := config.CheckLocalModelNameMemorySafe
		if !inventoryKnown {
			check = config.CheckModelMemorySafe
		}
		if err := check(modelName); err != nil {
			return "", nil, err
		}
	}

	var modelList []string
	if !inventoryKnown {
		router.SetAvailableModels([]string{})
		return modelName, modelList, nil
	}

	modelList = uniqueModelNames(manuallySelectable)
	autoModels := uniqueModelNames(autoRoutable)
	router.SetAvailableModels(autoModels)
	if !modelPinned {
		modelName = router.ResolveAvailableModel(modelName)
		if !containsModel(autoModels, modelName) {
			modelName = ""
		}
		if modelName == "" && len(autoModels) > 0 {
			modelName = autoModels[0]
		}
	}
	if modelName == "" {
		return "", modelList, fmt.Errorf("no compatible local chat model is installed; pull a configured model such as %q", modelConfig.DefaultModel)
	}
	if localOnly {
		if err := config.CheckModelAvailableLocally(modelName, manuallySelectable); err != nil {
			return "", modelList, err
		}
	} else if !containsModel(manuallySelectable, modelName) {
		return "", modelList, fmt.Errorf("model %q is not available from the configured Ollama host", modelName)
	}
	return modelName, modelList, nil
}

func uniqueModelNames(models []string) []string {
	result := make([]string, 0, len(models))
	seen := make(map[string]struct{}, len(models))
	for _, model := range models {
		canonical := config.CanonicalModelName(model)
		if canonical == "" {
			continue
		}
		if _, duplicate := seen[canonical]; duplicate {
			continue
		}
		seen[canonical] = struct{}{}
		result = append(result, model)
	}
	return result
}

func containsModel(models []string, wanted string) bool {
	wanted = config.CanonicalModelName(wanted)
	for _, model := range models {
		if config.CanonicalModelName(model) == wanted {
			return true
		}
	}
	return false
}

func selectHeadlessModel(current, prompt string, pinned bool, router config.ModelRouter, mode config.ModeContext) string {
	if pinned || router == nil {
		return current
	}
	return router.SelectModelForMode(prompt, mode)
}
