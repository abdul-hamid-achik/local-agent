package config

import (
	"strings"
	"testing"
)

func TestClassifyTask(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  TaskComplexity
	}{
		{name: "empty query", query: "", want: ComplexityMedium},
		{name: "simple what is", query: "what is Go", want: ComplexitySimple},

		// "create a function": medium "create" +1, "function" +1, advanced "create a" +3 = 5 → advanced
		{name: "create a function is advanced due to overlaps", query: "create a function", want: ComplexityAdvanced},

		// "debug this error across multiple files": complex "debug" +2, "error" +2, "bug" +2 (substring of debug),
		// "multiple" +2, "across" +2 = 10, medium "file" +1 = 11 → advanced
		{name: "debug across files is advanced", query: "debug this error across multiple files", want: ComplexityAdvanced},

		// "implement a full stack system with infrastructure": advanced "implement" +3, "full stack" +3, "system" +3,
		// "infrastructure" +3 = 12 → advanced
		{name: "advanced full stack system", query: "implement a full stack system with infrastructure", want: ComplexityAdvanced},

		// Boundary: "explain" → simple -2 → score -2 → simple
		{name: "boundary simple score -2", query: "explain", want: ComplexitySimple},

		// No indicators → score 0 → medium
		{name: "boundary medium score 0", query: "hello world", want: ComplexityMedium},

		// "create" → medium +1, but also matches advanced "create a"? No, "create" doesn't contain "create a".
		// So just +1 → medium
		{name: "boundary medium score 1", query: "create", want: ComplexityMedium},

		// "debug" alone: complex "debug" +2, "bug" +2 (substring) = 4 → complex
		{name: "debug alone is complex", query: "debug", want: ComplexityComplex},

		// "debug error": "debug" +2, "error" +2, "bug" +2 (substring of debug) = 6 → advanced
		{name: "debug error is advanced", query: "debug error", want: ComplexityAdvanced},

		// Word count >50 bonus (+2) with "debug": "debug" +2, "bug" +2 = 4, +2 word bonus = 6 → advanced
		{
			name:  "word count bonus over 50 with debug",
			query: strings.Repeat("word ", 51) + "debug",
			want:  ComplexityAdvanced,
		},

		// "why does this happen": "why" +1 = 1 → medium
		{name: "why bonus", query: "why does this happen", want: ComplexityMedium},

		// "reason for the crash": "reason" +1 = 1 → medium
		{name: "reason bonus", query: "reason for the crash", want: ComplexityMedium},

		// "how about we think...": no indicators, "how" + >10 words +1 = 1 → medium
		{name: "how with many words", query: "how about we think about the things that are happening right now in the code base", want: ComplexityMedium},

		// Case insensitivity
		{name: "case insensitive WHAT IS", query: "WHAT IS Go", want: ComplexitySimple},
		{name: "case insensitive EXPLAIN", query: "EXPLAIN this code", want: ComplexitySimple},

		// Pure simple: multiple simple indicators
		{name: "multiple simple indicators", query: "what is this simple quick search", want: ComplexitySimple},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyTask(tt.query)
			if got != tt.want {
				t.Errorf("ClassifyTask(%q) = %q, want %q", tt.query, got, tt.want)
			}
		})
	}
}

func TestRouter_GetFallbackChain(t *testing.T) {
	cfg := &ModelConfig{
		FallbackChain: []string{"a", "b", "c", "d"},
	}
	r := NewRouter(cfg)

	tests := []struct {
		name    string
		model   string
		wantLen int
		wantAll bool // true means expect full chain
	}{
		{name: "found at start", model: "a", wantLen: 4},
		{name: "found in middle", model: "c", wantLen: 2},
		{name: "found at end", model: "d", wantLen: 1},
		{name: "not found returns full chain", model: "unknown", wantLen: 4, wantAll: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.GetFallbackChain(tt.model)
			if len(got) != tt.wantLen {
				t.Errorf("GetFallbackChain(%q) returned %d items, want %d", tt.model, len(got), tt.wantLen)
			}
			if tt.wantAll && got[0] != "a" {
				t.Errorf("GetFallbackChain(%q) first element = %q, want %q", tt.model, got[0], "a")
			}
		})
	}
}

func TestRouter_GetModelForCapability(t *testing.T) {
	cfg := &ModelConfig{
		Models: []Model{
			{Name: "fast", Capability: CapabilitySimple},
			{Name: "mid", Capability: CapabilityMedium},
			{Name: "big", Capability: CapabilityComplex},
		},
		DefaultModel: "fallback",
	}
	r := NewRouter(cfg)

	tests := []struct {
		name       string
		capability ModelCapability
		want       string
	}{
		{name: "match simple", capability: CapabilitySimple, want: "fast"},
		{name: "match medium", capability: CapabilityMedium, want: "mid"},
		{name: "match complex", capability: CapabilityComplex, want: "big"},
		{name: "no match returns default", capability: CapabilityAdvanced, want: "fallback"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.GetModelForCapability(tt.capability)
			if got != tt.want {
				t.Errorf("GetModelForCapability(%d) = %q, want %q", tt.capability, got, tt.want)
			}
		})
	}
}

func TestRouter_SelectModel(t *testing.T) {
	cfg := DefaultModelConfig()
	r := NewRouter(&cfg)

	tests := []struct {
		name  string
		query string
		want  string
	}{
		// "what is Go" → simple → first model
		{name: "simple query selects first model", query: "what is Go", want: cfg.Models[0].Name},
		// "debug" → complex → complex-capable model
		{name: "complex query selects complex model", query: "debug", want: "qwen3.5:4b"},
		// "implement a system" → advanced → DefaultModel
		{name: "advanced query selects default model", query: "implement a full stack system", want: cfg.DefaultModel},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.SelectModel(tt.query)
			if got != tt.want {
				t.Errorf("SelectModel(%q) = %q, want %q", tt.query, got, tt.want)
			}
		})
	}
}
