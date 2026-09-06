package config

import "testing"

func TestRemoteOllamaProfileRequiresDestinationLimitsAndConsent(t *testing.T) {
	valid := ProviderProfile{Type: ProviderTypeOllamaRemote, BaseURL: "http://127.0.0.1:11435", Model: "large:35b", ContextSize: 65536, MaxModelBytes: 32 << 30, StreamIdleTimeout: "10m"}
	for _, tc := range []struct {
		name      string
		change    func(*ProviderProfile)
		localOnly bool
		valid     bool
	}{
		{"remote tunnel", func(*ProviderProfile) {}, false, true},
		{"local only", func(*ProviderProfile) {}, true, false},
		{"missing weights", func(p *ProviderProfile) { p.MaxModelBytes = 0 }, false, false},
		{"missing context", func(p *ProviderProfile) { p.ContextSize = 0 }, false, false},
		{"invalid timeout", func(p *ProviderProfile) { p.StreamIdleTimeout = "0s" }, false, false},
		{"ambiguous scheme", func(p *ProviderProfile) { p.BaseURL = "example.test:11434" }, false, false},
		{"plaintext remote", func(p *ProviderProfile) { p.BaseURL = "http://example.test" }, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := valid
			tc.change(&p)
			cfg := Defaults()
			cfg.Privacy.LocalOnly = tc.localOnly
			cfg.Provider = ProviderConfig{Active: "gpu", Profiles: map[string]ProviderProfile{"gpu": p}}
			if err := cfg.Validate(); (err == nil) != tc.valid {
				t.Fatalf("Validate = %v", err)
			}
			if tc.valid {
				resolved := cfg.Provider.ResolvedActive()
				_, got, err := resolved.ActiveProfile()
				if err != nil || got != p || !resolved.IsRemote() {
					t.Fatalf("profile round trip: %+v, %v", got, err)
				}
				flat := p.asConfig().Resolve()
				if flat.MaxModelBytes != p.MaxModelBytes || flat.StreamIdleTimeout != p.StreamIdleTimeout {
					t.Fatal("flat profile lost destination limits")
				}
			}
		})
	}
}
