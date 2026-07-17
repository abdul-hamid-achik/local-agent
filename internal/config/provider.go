package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
)

// Provider types. Ollama remains the default local runtime.
const (
	ProviderTypeOllama           = "ollama"
	ProviderTypeOpenAICompatible = "openai_compatible"
	ProviderTypeXAI              = "xai"
)

// ProviderConfig selects the chat inference adapter. Secrets are never stored
// here — only environment variable *names*. Prefer TinyVault injection:
//
//	tvault run -p local-agent --only XAI_API_KEY,OPENAI_API_KEY -- local-agent
//
// Two shapes are supported:
//
//  1. Flat (single active provider):
//     provider: { type: xai, model: grok-4.5 }
//
//  2. Multi-profile (named catalog + active):
//     provider:
//       active: xai
//       profiles:
//         ollama: { type: ollama }
//         xai:    { type: xai, model: grok-4.5 }
//         openai: { type: openai_compatible, base_url: https://api.openai.com/v1, model: gpt-4.1, api_key_env: OPENAI_API_KEY }
type ProviderConfig struct {
	// Active is the profile name in use when Profiles is non-empty.
	// LOCAL_AGENT_PROVIDER overrides this and also accepts a type for the flat form.
	Active string `yaml:"active,omitempty"`
	// Profiles is a named catalog of provider definitions. When empty, the
	// flat Type/BaseURL/Model/APIKeyEnv fields describe the sole provider.
	Profiles map[string]ProviderProfile `yaml:"profiles,omitempty"`

	// Flat fields (single-provider form, also the resolved surface after ActiveProfile).
	Type        string `yaml:"type,omitempty"`
	BaseURL     string `yaml:"base_url,omitempty"`
	Model       string `yaml:"model,omitempty"`
	APIKeyEnv   string `yaml:"api_key_env,omitempty"`
	ContextSize int    `yaml:"context_size,omitempty"`
}

// ProviderProfile is one named inference backend definition.
type ProviderProfile struct {
	Type        string `yaml:"type,omitempty"`
	BaseURL     string `yaml:"base_url,omitempty"`
	Model       string `yaml:"model,omitempty"`
	APIKeyEnv   string `yaml:"api_key_env,omitempty"`
	ContextSize int    `yaml:"context_size,omitempty"`
}

// NormalizedProviderType returns the effective type after empty → ollama.
func NormalizedProviderType(typ string) string {
	switch strings.ToLower(strings.TrimSpace(typ)) {
	case "", ProviderTypeOllama:
		return ProviderTypeOllama
	case ProviderTypeOpenAICompatible:
		return ProviderTypeOpenAICompatible
	case ProviderTypeXAI:
		return ProviderTypeXAI
	default:
		return strings.ToLower(strings.TrimSpace(typ))
	}
}

// NormalizedType returns the effective provider type after empty → ollama.
func (c ProviderConfig) NormalizedType() string {
	return NormalizedProviderType(c.Type)
}

// IsRemote reports whether chat inference leaves the local Ollama runtime.
func (c ProviderConfig) IsRemote() bool {
	return ProviderProfile(c.asProfile()).IsRemote()
}

// IsRemote reports whether this profile uses a non-Ollama chat adapter.
func (p ProviderProfile) IsRemote() bool {
	switch NormalizedProviderType(p.Type) {
	case ProviderTypeOpenAICompatible, ProviderTypeXAI:
		return true
	default:
		return false
	}
}

func (c ProviderConfig) asProfile() ProviderProfile {
	return ProviderProfile{
		Type:        c.Type,
		BaseURL:     c.BaseURL,
		Model:       c.Model,
		APIKeyEnv:   c.APIKeyEnv,
		ContextSize: c.ContextSize,
	}
}

// Resolve applies type-specific defaults without mutating the stored config.
func (c ProviderConfig) Resolve() ProviderConfig {
	return c.asProfile().Resolve().asConfig()
}

// Resolve applies type-specific defaults (xai base URL, key env, model).
func (p ProviderProfile) Resolve() ProviderProfile {
	out := p
	out.Type = NormalizedProviderType(out.Type)
	switch out.Type {
	case ProviderTypeXAI:
		if strings.TrimSpace(out.BaseURL) == "" {
			out.BaseURL = "https://api.x.ai/v1"
		}
		if strings.TrimSpace(out.APIKeyEnv) == "" {
			out.APIKeyEnv = "XAI_API_KEY"
		}
		if strings.TrimSpace(out.Model) == "" {
			out.Model = "grok-4.5"
		}
		if out.ContextSize <= 0 {
			out.ContextSize = 131072
		}
	case ProviderTypeOpenAICompatible:
		if out.ContextSize <= 0 {
			out.ContextSize = 128000
		}
	case ProviderTypeOllama:
		// leave empty model to fall back to ollama.model at runtime
	}
	return out
}

func (p ProviderProfile) asConfig() ProviderConfig {
	return ProviderConfig{
		Type:        p.Type,
		BaseURL:     p.BaseURL,
		Model:       p.Model,
		APIKeyEnv:   p.APIKeyEnv,
		ContextSize: p.ContextSize,
	}
}

// HasProfiles reports whether a multi-profile catalog is configured.
func (c ProviderConfig) HasProfiles() bool {
	return len(c.Profiles) > 0
}

// ProfileNames returns sorted profile names. When no catalog is configured,
// returns a single synthetic name for the flat form ("ollama", "xai", …).
func (c ProviderConfig) ProfileNames() []string {
	if !c.HasProfiles() {
		name := c.flatProfileName()
		return []string{name}
	}
	names := make([]string, 0, len(c.Profiles))
	for name := range c.Profiles {
		if strings.TrimSpace(name) != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func (c ProviderConfig) flatProfileName() string {
	typ := c.NormalizedType()
	if typ == ProviderTypeOllama && strings.TrimSpace(c.Type) == "" && !c.IsRemote() {
		return ProviderTypeOllama
	}
	if typ == "" {
		return ProviderTypeOllama
	}
	return typ
}

// ActiveName returns the selected profile name after defaults.
func (c ProviderConfig) ActiveName() string {
	if active := strings.TrimSpace(c.Active); active != "" {
		return active
	}
	if c.HasProfiles() {
		// Prefer an explicit ollama profile, else the first sorted name.
		if _, ok := c.Profiles[ProviderTypeOllama]; ok {
			return ProviderTypeOllama
		}
		names := c.ProfileNames()
		if len(names) > 0 {
			return names[0]
		}
	}
	return c.flatProfileName()
}

// LookupProfile returns the named profile (unresolved defaults).
func (c ProviderConfig) LookupProfile(name string) (ProviderProfile, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return ProviderProfile{}, false
	}
	if c.HasProfiles() {
		profile, ok := c.Profiles[name]
		return profile, ok
	}
	if name == c.flatProfileName() || name == c.NormalizedType() || (name == ProviderTypeOllama && c.NormalizedType() == ProviderTypeOllama) {
		return c.asProfile(), true
	}
	return ProviderProfile{}, false
}

// ActiveProfile returns the resolved active profile and its catalog name.
func (c ProviderConfig) ActiveProfile() (name string, profile ProviderProfile, err error) {
	return c.ResolveProfile(c.ActiveName())
}

// ResolveProfile looks up name and applies type defaults.
func (c ProviderConfig) ResolveProfile(name string) (string, ProviderProfile, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = c.ActiveName()
	}
	// Allow selecting by type when profiles use type-as-name convention.
	profile, ok := c.LookupProfile(name)
	if !ok && c.HasProfiles() {
		// Match profile whose resolved type equals name (e.g. active: xai
		// when the profile is registered under another key is not supported;
		// try case-insensitive name match).
		for catalogName, candidate := range c.Profiles {
			if strings.EqualFold(catalogName, name) {
				profile, ok = candidate, true
				name = catalogName
				break
			}
		}
	}
	if !ok {
		return "", ProviderProfile{}, fmt.Errorf("provider profile %q is not defined (known: %s)", name, strings.Join(c.ProfileNames(), ", "))
	}
	return name, profile.Resolve(), nil
}

// ResolvedActive is the flat ProviderConfig surface for the active profile
// (compat for call sites that still use Resolve()).
func (c ProviderConfig) ResolvedActive() ProviderConfig {
	name, profile, err := c.ActiveProfile()
	if err != nil {
		// Fall back to flat resolve for partial configs; Validate catches errors.
		out := c.Resolve()
		out.Active = c.ActiveName()
		return out
	}
	out := profile.asConfig()
	out.Active = name
	out.Profiles = c.Profiles
	return out
}

// AllAPIKeyEnvs returns unique non-empty api_key_env names across the catalog
// (for tvault --only hints).
func (c ProviderConfig) AllAPIKeyEnvs() []string {
	seen := map[string]struct{}{}
	var out []string
	add := func(env string) {
		env = strings.TrimSpace(env)
		if env == "" {
			return
		}
		if _, ok := seen[env]; ok {
			return
		}
		seen[env] = struct{}{}
		out = append(out, env)
	}
	if c.HasProfiles() {
		for _, name := range c.ProfileNames() {
			_, profile, err := c.ResolveProfile(name)
			if err != nil {
				continue
			}
			if profile.IsRemote() {
				add(profile.APIKeyEnv)
			}
		}
		return out
	}
	resolved := c.Resolve()
	if resolved.IsRemote() {
		add(resolved.APIKeyEnv)
	}
	return out
}

// ResolveAPIKey reads the active profile API key from the process environment.
func (c ProviderConfig) ResolveAPIKey() (string, error) {
	_, profile, err := c.ActiveProfile()
	if err != nil {
		return "", err
	}
	return profile.ResolveAPIKey()
}

// ResolveAPIKey reads this profile's key from the environment.
func (p ProviderProfile) ResolveAPIKey() (string, error) {
	resolved := p.Resolve()
	if !resolved.IsRemote() {
		return "", nil
	}
	envName := strings.TrimSpace(resolved.APIKeyEnv)
	if envName == "" {
		return "", errors.New("provider.api_key_env is empty")
	}
	value := strings.TrimSpace(os.Getenv(envName))
	if value == "" {
		return "", fmt.Errorf(
			"%s is unset or empty; store the key in TinyVault and launch with: tvault run -p local-agent --only %s -- local-agent",
			envName,
			envName,
		)
	}
	return value, nil
}

func (c *Config) validateProvider() error {
	if c.Provider.HasProfiles() {
		if len(c.Provider.Profiles) == 0 {
			return fmt.Errorf("config: provider.profiles is empty")
		}
		for name, profile := range c.Provider.Profiles {
			if strings.TrimSpace(name) == "" {
				return fmt.Errorf("config: provider.profiles contains an empty name")
			}
			// Shape-check every profile. privacy.local_only only gates the *active*
			// remote so a catalog can list remotes while defaulting to ollama.
			if err := validateProviderProfile(name, profile.Resolve(), false); err != nil {
				return err
			}
		}
		active := c.Provider.ActiveName()
		if _, ok := c.Provider.LookupProfile(active); !ok {
			if strings.TrimSpace(c.Provider.Active) != "" {
				return fmt.Errorf(
					"config: provider.active %q is not a defined profile (known: %s)",
					c.Provider.Active,
					strings.Join(c.Provider.ProfileNames(), ", "),
				)
			}
		}
		_, activeProfile, err := c.Provider.ActiveProfile()
		if err != nil {
			return fmt.Errorf("config: %w", err)
		}
		if err := validateProviderProfile(active, activeProfile, c.Privacy.LocalOnly); err != nil {
			return err
		}
		return nil
	}
	return validateProviderProfile(c.Provider.ActiveName(), c.Provider.Resolve().asProfile(), c.Privacy.LocalOnly)
}

// ValidateProviderProfile checks one profile against privacy and type rules.
// Used at startup and when switching providers at runtime.
func ValidateProviderProfile(name string, profile ProviderProfile, localOnly bool) error {
	return validateProviderProfile(name, profile.Resolve(), localOnly)
}

func validateProviderProfile(name string, profile ProviderProfile, localOnly bool) error {
	label := name
	if label == "" {
		label = profile.Type
	}
	switch NormalizedProviderType(profile.Type) {
	case ProviderTypeOllama:
		return nil
	case ProviderTypeOpenAICompatible, ProviderTypeXAI:
		// ok
	default:
		return fmt.Errorf(
			"config: provider profile %q type must be %q, %q, or %q, got %q",
			label,
			ProviderTypeOllama,
			ProviderTypeOpenAICompatible,
			ProviderTypeXAI,
			profile.Type,
		)
	}
	if strings.TrimSpace(profile.BaseURL) == "" {
		return fmt.Errorf("config: provider profile %q requires base_url for type %q", label, profile.Type)
	}
	raw := profile.BaseURL
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("config: provider profile %q has invalid base_url %q", label, profile.BaseURL)
	}
	if localOnly && !isLocalHost(u.Hostname()) {
		return fmt.Errorf(
			"config: privacy.local_only rejects remote provider profile %q (%s); set privacy.local_only: false or LOCAL_AGENT_LOCAL_ONLY=false, and launch with tvault run --only %s",
			label,
			profile.BaseURL,
			profile.APIKeyEnv,
		)
	}
	if strings.TrimSpace(profile.Model) == "" {
		return fmt.Errorf("config: provider profile %q requires model for type %q", label, profile.Type)
	}
	if strings.TrimSpace(profile.APIKeyEnv) == "" {
		return fmt.Errorf("config: provider profile %q requires api_key_env for type %q (env var name only; never put the secret in YAML)", label, profile.Type)
	}
	if err := validateEnvVarName(profile.APIKeyEnv); err != nil {
		return fmt.Errorf("config: provider profile %q api_key_env: %w", label, err)
	}
	if profile.ContextSize < 0 {
		return fmt.Errorf("config: provider profile %q context_size cannot be negative", label)
	}
	return nil
}

func validateEnvVarName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("empty name")
	}
	for i, r := range name {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r == '_':
			continue
		case r >= '0' && r <= '9':
			if i == 0 {
				return fmt.Errorf("%q must not start with a digit", name)
			}
		default:
			return fmt.Errorf("%q is not a valid environment variable name", name)
		}
	}
	return nil
}
