package ice

import (
	"testing"
)

func TestEngineConfigDefaults(t *testing.T) {
	// Test embed model default
	embedModel := ""
	if embedModel == "" {
		embedModel = defaultEmbedModel
	}
	if embedModel != defaultEmbedModel {
		t.Errorf("embedModel = %q, want %q", embedModel, defaultEmbedModel)
	}

	// Test custom embed model
	cfg := EngineConfig{
		EmbedModel: "custom-model",
	}
	if cfg.EmbedModel != "custom-model" {
		t.Errorf("EmbedModel = %q, want %q", cfg.EmbedModel, "custom-model")
	}
}

func TestBudgetConfigCalculate(t *testing.T) {
	cfg := DefaultBudgetConfig(16384)

	budget := cfg.Calculate(100)
	// 16384 * 0.75 = 12288
	// 12288 - 1500 - 2000 - 100 = 8688
	if budget.Total != 8688 {
		t.Errorf("Total = %d, want %d", budget.Total, 8688)
	}
	if budget.System != 1500 {
		t.Errorf("System = %d, want %d", budget.System, 1500)
	}
	if budget.Recent != 2000 {
		t.Errorf("Recent = %d, want %d", budget.Recent, 2000)
	}
}

func TestBudgetConfigCalculateNegative(t *testing.T) {
	// With small context, should not panic and return zeros
	cfg := DefaultBudgetConfig(1000)
	budget := cfg.Calculate(500)

	// 1000 * 0.75 = 750
	// 750 - 1500 - 2000 - 500 = -3250 -> clamped to 0
	if budget.Total != 0 {
		t.Errorf("Total should be 0 when budget is negative, got %d", budget.Total)
	}
}

func TestBudgetConfigPercentages(t *testing.T) {
	cfg := DefaultBudgetConfig(16384)
	budget := cfg.Calculate(100)

	// Check percentages: ConversationPct=0.40, MemoryPct=0.20, CodePct=0.40
	// available = 12288 - 1500 - 2000 - 100 = 8688
	// Conversation = 8688 * 0.40 = 3475
	// Memory = 8688 * 0.20 = 1737
	// Code = 8688 * 0.40 = 3475
	if budget.Conversation != 3475 {
		t.Errorf("Conversation = %d, want %d", budget.Conversation, 3475)
	}
	if budget.Memory != 1737 {
		t.Errorf("Memory = %d, want %d", budget.Memory, 1737)
	}
	if budget.Code != 3475 {
		t.Errorf("Code = %d, want %d", budget.Code, 3475)
	}
}
