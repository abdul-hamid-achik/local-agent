package ice

// BudgetConfig controls how the context window is divided among sources.
type BudgetConfig struct {
	NumCtx          int
	SystemReserve   int     // tokens reserved for system prompt
	RecentReserve   int     // tokens reserved for recent conversation
	ConversationPct float64 // fraction of remaining budget for past conversations
	MemoryPct       float64 // fraction of remaining budget for memories
	CodePct         float64 // fraction of remaining budget for code context
}

// DefaultBudgetConfig returns sensible defaults for a given context window.
func DefaultBudgetConfig(numCtx int) BudgetConfig {
	return BudgetConfig{
		NumCtx:          numCtx,
		SystemReserve:   1500,
		RecentReserve:   2000,
		ConversationPct: 0.40,
		MemoryPct:       0.20,
		CodePct:         0.40,
	}
}

// Calculate allocates token budgets given how many tokens the current prompt uses.
func (bc BudgetConfig) Calculate(promptTokens int) Budget {
	// Use 75% of numCtx as total available.
	available := int(float64(bc.NumCtx) * 0.75)
	available -= bc.SystemReserve
	available -= bc.RecentReserve
	available -= promptTokens

	if available < 0 {
		available = 0
	}

	return Budget{
		Total:        available,
		System:       bc.SystemReserve,
		Recent:       bc.RecentReserve,
		Conversation: int(float64(available) * bc.ConversationPct),
		Memory:       int(float64(available) * bc.MemoryPct),
		Code:         int(float64(available) * bc.CodePct),
	}
}

// estimateTokens returns a rough token count for a string (chars / 4).
func estimateTokens(s string) int {
	n := len(s) / 4
	if n == 0 && len(s) > 0 {
		n = 1
	}
	return n
}
