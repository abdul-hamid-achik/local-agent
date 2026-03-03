package config

import "testing"

func TestModel_IsSimpleTask(t *testing.T) {
	tests := []struct {
		name       string
		capability ModelCapability
		want       bool
	}{
		{name: "CapabilitySimple is simple", capability: CapabilitySimple, want: true},
		{name: "CapabilityMedium is simple", capability: CapabilityMedium, want: true},
		{name: "CapabilityComplex is not simple", capability: CapabilityComplex, want: false},
		{name: "CapabilityAdvanced is not simple", capability: CapabilityAdvanced, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &Model{Capability: tt.capability}
			if got := m.IsSimpleTask(); got != tt.want {
				t.Errorf("Model{Capability: %d}.IsSimpleTask() = %v, want %v", tt.capability, got, tt.want)
			}
		})
	}
}

func TestModel_IsComplexTask(t *testing.T) {
	tests := []struct {
		name       string
		capability ModelCapability
		want       bool
	}{
		{name: "CapabilitySimple is not complex", capability: CapabilitySimple, want: false},
		{name: "CapabilityMedium is not complex", capability: CapabilityMedium, want: false},
		{name: "CapabilityComplex is complex", capability: CapabilityComplex, want: true},
		{name: "CapabilityAdvanced is complex", capability: CapabilityAdvanced, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &Model{Capability: tt.capability}
			if got := m.IsComplexTask(); got != tt.want {
				t.Errorf("Model{Capability: %d}.IsComplexTask() = %v, want %v", tt.capability, got, tt.want)
			}
		})
	}
}

func TestModelConfig_GetModel(t *testing.T) {
	cfg := DefaultModelConfig()

	tests := []struct {
		name    string
		model   string
		wantErr bool
	}{
		{name: "found model", model: "qwen3.5:0.8b", wantErr: false},
		{name: "not found", model: "nonexistent", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := cfg.GetModel(tt.model)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if got.Name != tt.model {
					t.Errorf("GetModel(%q).Name = %q, want %q", tt.model, got.Name, tt.model)
				}
			}
		})
	}
}

func TestModelConfig_GetDefaultModel(t *testing.T) {
	tests := []struct {
		name string
		cfg  ModelConfig
		want string // empty means nil expected
	}{
		{
			name: "model with Default=true",
			cfg: ModelConfig{
				Models: []Model{
					{Name: "a", Default: false},
					{Name: "b", Default: true},
					{Name: "c", Default: false},
				},
			},
			want: "b",
		},
		{
			name: "no default returns last",
			cfg: ModelConfig{
				Models: []Model{
					{Name: "a", Default: false},
					{Name: "b", Default: false},
				},
			},
			want: "b",
		},
		{
			name: "empty slice returns nil",
			cfg:  ModelConfig{Models: []Model{}},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.cfg.GetDefaultModel()
			if tt.want == "" {
				if got != nil {
					t.Errorf("expected nil, got %+v", got)
				}
			} else {
				if got == nil {
					t.Fatal("expected non-nil model, got nil")
				}
				if got.Name != tt.want {
					t.Errorf("GetDefaultModel().Name = %q, want %q", got.Name, tt.want)
				}
			}
		})
	}
}

func TestModelConfig_SelectModelForTask(t *testing.T) {
	cfg := DefaultModelConfig()

	tests := []struct {
		name       string
		complexity string
		autoSelect bool
		want       string
	}{
		{name: "auto simple", complexity: "simple", autoSelect: true, want: "qwen3.5:0.8b"},
		{name: "auto medium", complexity: "medium", autoSelect: true, want: "qwen3.5:2b"},
		{name: "auto complex", complexity: "complex", autoSelect: true, want: "qwen3.5:4b"},
		{name: "auto advanced", complexity: "advanced", autoSelect: true, want: cfg.DefaultModel},
		{name: "no autoselect simple", complexity: "simple", autoSelect: false, want: cfg.DefaultModel},
		{name: "no autoselect complex", complexity: "complex", autoSelect: false, want: cfg.DefaultModel},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg.AutoSelect = tt.autoSelect
			got := cfg.SelectModelForTask(tt.complexity)
			if got != tt.want {
				t.Errorf("SelectModelForTask(%q) = %q, want %q", tt.complexity, got, tt.want)
			}
		})
	}
}
