package config

// ModelRouter is the shared routing interface used by the CLI and TUI.
type ModelRouter interface {
	SelectModel(query string) string
	SelectModelForMode(query string, mode ModeContext) string
	RecordOverride(query, userModel string)
	GetModelForCapability(capability ModelCapability) string
	ListModels() []Model
}

// PromoteModelForMode promotes a query-selected model to satisfy the minimum
// capability requirements of the current mode.
func PromoteModelForMode(router ModelRouter, selectedModel string, mode ModeContext) string {
	if router == nil || selectedModel == "" {
		return selectedModel
	}

	selectedCapability := CapabilitySimple
	for _, model := range router.ListModels() {
		if model.Name == selectedModel {
			selectedCapability = model.Capability
			break
		}
	}

	minCapability := CapabilitySimple
	switch mode {
	case ModePlanContext:
		minCapability = CapabilityComplex
	case ModeBuildContext:
		minCapability = CapabilityAdvanced
	}

	if selectedCapability >= minCapability {
		return selectedModel
	}

	return router.GetModelForCapability(minCapability)
}
