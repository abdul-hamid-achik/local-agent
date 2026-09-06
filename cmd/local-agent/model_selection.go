package main

import (
	"fmt"
	"strings"

	"github.com/abdul-hamid-achik/local-agent/internal/config"
)

func shouldPinStartupModel(modelOverride string, autoSelect bool) bool {
	return modelOverride != "" || !autoSelect
}

func shouldRestoreManualModelPreference(modelOverride string, profileModelPinned bool) bool {
	return strings.TrimSpace(modelOverride) == "" && !profileModelPinned
}

// restoreManualModelPreference accepts a saved selection only when the current
// Ollama inventory proves it is local and manually selectable, including
// catalog-exclusive local profiles. A remembered Ollama Cloud name never
// carries its prior session consent into a new process.
func restoreManualModelPreference(
	preferred string,
	manuallySelectable []string,
	localSelectable []string,
	inventoryKnown bool,
) (string, bool, string) {
	preferred = strings.TrimSpace(preferred)
	if preferred == "" {
		return "", false, ""
	}
	if !inventoryKnown {
		return "", false, fmt.Sprintf("saved model %q was not restored because Ollama inventory is unavailable", preferred)
	}
	if model, ok := matchingModel(localSelectable, preferred); ok {
		return model, true, ""
	}
	if _, ok := matchingModel(manuallySelectable, preferred); ok {
		return "", false, fmt.Sprintf("saved model %q needs a fresh explicit selection because it is not a verified local model", preferred)
	}
	return "", false, fmt.Sprintf("saved model %q is no longer available; using automatic or configured selection", preferred)
}

func matchingModel(models []string, wanted string) (string, bool) {
	return config.MatchInstalledModel(wanted, models)
}

// resolveStartupModel keeps the manual selection inventory separate from the
// local-only automatic routing inventory. When inventoryKnown is false, it
// preserves the configured selection so the TUI can still start and report an
// offline Ollama diagnostic. The name-based local memory guard remains active
// in that fallback because no verified byte size is available yet; it
// deliberately does not infer execution location.
//
// A pinned or configured name that is missing from inventory first tries a
// renamed sibling (ornith:latest → ornith-1.5:9b). If that also misses, it
// starts on an installed local chat model and returns a warning instead of
// refusing to launch. Cloud names are never chosen as that fallback.
func resolveStartupModel(
	modelName string,
	modelPinned bool,
	localOnly bool,
	modelConfig *config.ModelConfig,
	manuallySelectable []string,
	localSelectable []string,
	autoRoutable []string,
	inventoryKnown bool,
	router availabilityAwareRouter,
) (string, []string, string, error) {
	if !inventoryKnown {
		if err := config.CheckLocalModelNameMemorySafe(modelName); err != nil {
			return "", nil, "", err
		}
	}
	if localOnly {
		check := config.CheckLocalModelNameMemorySafe
		if !inventoryKnown {
			check = config.CheckModelMemorySafe
		}
		if err := check(modelName); err != nil {
			return "", nil, "", err
		}
	}

	var modelList []string
	if !inventoryKnown {
		router.SetAvailableModels([]string{})
		return modelName, modelList, "", nil
	}

	modelList = uniqueModelNames(manuallySelectable)
	localModels := uniqueModelNames(localSelectable)
	autoModels := uniqueModelNames(autoRoutable)
	router.SetAvailableModels(autoModels)

	requested := modelName
	warning := ""
	if resolved, ok := matchingModel(manuallySelectable, modelName); ok {
		modelName = preferRequestedModelSpelling(requested, resolved)
	} else if resolved, ok := matchingModel(localModels, modelName); ok {
		modelName = preferRequestedModelSpelling(requested, resolved)
	}

	if !modelPinned {
		modelName = router.ResolveAvailableModel(modelName)
		if !containsModel(autoModels, modelName) {
			modelName = ""
		}
		if modelName == "" && len(autoModels) > 0 {
			modelName = autoModels[0]
		}
	} else if !modelAdmitted(modelName, manuallySelectable, localModels, localOnly) {
		if localOnly {
			if err := config.CheckModelMemorySafe(requested); err != nil {
				return "", modelList, "", err
			}
		}
		if fallback := firstLocalChatModel(autoModels, localModels); fallback != "" {
			warning = fmt.Sprintf("configured model %q is not installed; using %q", requested, fallback)
			modelName = fallback
		} else {
			modelName = ""
		}
	} else if config.CanonicalModelName(modelName) != config.CanonicalModelName(requested) {
		warning = fmt.Sprintf("using installed model %q in place of %q", modelName, requested)
	}

	if modelName == "" {
		return "", modelList, "", fmt.Errorf("no compatible local chat model is installed; pull a configured model such as %q", modelConfig.DefaultModel)
	}
	if localOnly {
		if err := config.CheckModelAvailableLocally(modelName, appendLocalAdmission(manuallySelectable, localModels)); err != nil {
			return "", modelList, warning, err
		}
	} else if !containsModel(manuallySelectable, modelName) && !containsModel(localModels, modelName) {
		return "", modelList, warning, fmt.Errorf("model %q is not available from the configured Ollama host", modelName)
	}
	return modelName, modelList, warning, nil
}

func modelAdmitted(modelName string, manuallySelectable, localSelectable []string, localOnly bool) bool {
	if containsModel(localSelectable, modelName) {
		return true
	}
	if localOnly {
		return config.CheckModelAvailableLocally(modelName, manuallySelectable) == nil
	}
	return containsModel(manuallySelectable, modelName)
}

func preferRequestedModelSpelling(requested, resolved string) string {
	if config.CanonicalModelName(requested) == config.CanonicalModelName(resolved) {
		return requested
	}
	return resolved
}

func firstLocalChatModel(autoModels, localModels []string) string {
	if len(autoModels) > 0 {
		return autoModels[0]
	}
	if len(localModels) > 0 {
		return localModels[0]
	}
	return ""
}

func appendLocalAdmission(manual, local []string) []string {
	if len(local) == 0 {
		return manual
	}
	result := uniqueModelNames(manual)
	for _, model := range local {
		if !containsModel(result, model) {
			result = append(result, model)
		}
	}
	return result
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
