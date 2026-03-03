package ice

import "testing"

func TestBudgetConfig_Calculate(t *testing.T) {
	tests := []struct {
		name         string
		cfg          BudgetConfig
		promptTokens int
		wantTotal    int
		wantConv     int
		wantMemory   int
		wantCode     int
	}{
		{
			name: "normal allocation",
			cfg: BudgetConfig{
				NumCtx:          8192,
				SystemReserve:   1500,
				RecentReserve:   2000,
				ConversationPct: 0.40,
				MemoryPct:       0.20,
				CodePct:         0.40,
			},
			promptTokens: 500,
			// available = int(8192*0.75) - 1500 - 2000 - 500 = 6144 - 4000 = 2144
			wantTotal:  2144,
			wantConv:   857,  // int(2144 * 0.40) = 857
			wantMemory: 428,  // int(2144 * 0.20) = 428
			wantCode:   857,  // int(2144 * 0.40) = 857
		},
		{
			name: "large prompt clamps to zero",
			cfg: BudgetConfig{
				NumCtx:          8192,
				SystemReserve:   1500,
				RecentReserve:   2000,
				ConversationPct: 0.40,
				MemoryPct:       0.20,
				CodePct:         0.40,
			},
			promptTokens: 99999,
			wantTotal:    0,
			wantConv:     0,
			wantMemory:   0,
			wantCode:     0,
		},
		{
			name: "exact boundary available is zero",
			cfg: BudgetConfig{
				NumCtx:          8192,
				SystemReserve:   1500,
				RecentReserve:   2000,
				ConversationPct: 0.40,
				MemoryPct:       0.20,
				CodePct:         0.40,
			},
			// int(8192*0.75) - 1500 - 2000 = 2644
			promptTokens: 2644,
			wantTotal:    0,
			wantConv:     0,
			wantMemory:   0,
			wantCode:     0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := tt.cfg.Calculate(tt.promptTokens)
			if b.Total != tt.wantTotal {
				t.Errorf("Total = %d, want %d", b.Total, tt.wantTotal)
			}
			if b.Conversation != tt.wantConv {
				t.Errorf("Conversation = %d, want %d", b.Conversation, tt.wantConv)
			}
			if b.Memory != tt.wantMemory {
				t.Errorf("Memory = %d, want %d", b.Memory, tt.wantMemory)
			}
			if b.Code != tt.wantCode {
				t.Errorf("Code = %d, want %d", b.Code, tt.wantCode)
			}
			if b.System != tt.cfg.SystemReserve {
				t.Errorf("System = %d, want %d", b.System, tt.cfg.SystemReserve)
			}
			if b.Recent != tt.cfg.RecentReserve {
				t.Errorf("Recent = %d, want %d", b.Recent, tt.cfg.RecentReserve)
			}
		})
	}
}

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  int
	}{
		{
			name:  "len/4 heuristic",
			input: "hello world",
			want:  2, // 11/4 = 2
		},
		{
			name:  "single char clamps to 1",
			input: "a",
			want:  1, // 1/4 = 0, clamp to 1
		},
		{
			name:  "empty string",
			input: "",
			want:  0,
		},
		{
			name:  "exactly 4 chars",
			input: "abcd",
			want:  1, // 4/4 = 1
		},
		{
			name:  "three chars clamps to 1",
			input: "abc",
			want:  1, // 3/4 = 0, clamp to 1
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := estimateTokens(tt.input)
			if got != tt.want {
				t.Errorf("estimateTokens(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}
