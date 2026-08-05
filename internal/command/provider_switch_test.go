package command

import (
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/local-agent/internal/config"
)

// Every provider type the configuration layer considers selectable must be
// reachable from /provider.
//
// This test passes against the code it replaced, and that is the point: the
// fallback here used to be a hand-written copy of the type list that still
// happened to agree with config. The same copy in sonar had already drifted and
// was rejecting its own default provider as "Unknown provider". Asserting the
// property instead of the list means a fourth type cannot become selectable in
// the configuration layer and unknown in the slash command.
func TestProviderCommandAcceptsEverySelectableType(t *testing.T) {
	r := newTestRegistry()
	// The configured profile is named after none of the types under test, so
	// every case reaches the type fallback rather than the name loop above it.
	ctx := &Context{Provider: "work-gateway", ProviderList: []string{"work-gateway"}}

	types := config.KnownProviderTypes()
	if len(types) < 3 {
		t.Fatalf("config exposes %d provider types; the enumeration looks broken", len(types))
	}
	for _, providerType := range types {
		t.Run(providerType, func(t *testing.T) {
			result := r.Execute(ctx, "provider", []string{providerType})
			if result.Error != "" {
				t.Fatalf("/provider %s = %q, want a switch", providerType, result.Error)
			}
			if result.Action != ActionSwitchProvider {
				t.Fatalf("/provider %s action = %d, want ActionSwitchProvider", providerType, result.Action)
			}
			if result.Data != providerType {
				t.Errorf("switch target = %q, want %q", result.Data, providerType)
			}
		})
	}
}

// The named-profile path still wins over type resolution, and a genuinely
// unknown name is still refused — widening the fallback must not turn the
// command into one that accepts anything.
func TestProviderCommandStillRefusesUnknownNames(t *testing.T) {
	r := newTestRegistry()
	ctx := &Context{Provider: "ollama", ProviderList: []string{"work-gateway"}}

	if result := r.Execute(ctx, "provider", []string{"work-gateway"}); result.Data != "work-gateway" {
		t.Fatalf("a configured profile name = %#v, want it selected", result)
	}
	// local-agent runs local models; a hosted catalog name is not a provider
	// here, and must not read as one just because sonar knows it.
	for _, target := range []string{"nope", "deepseek", "anthropic", "open ai", "$OPENAI_API_ENDPOINT"} {
		if result := r.Execute(ctx, "provider", []string{target}); result.Error == "" {
			t.Errorf("/provider %q = %#v, want an error", target, result)
		}
	}
}

// A provider type is a type whatever its casing; the profile-name loop above it
// already matches case-insensitively.
func TestProviderCommandTypeMatchIsCaseInsensitive(t *testing.T) {
	r := newTestRegistry()
	ctx := &Context{Provider: "work-gateway", ProviderList: []string{"work-gateway"}}

	for _, target := range []string{"Ollama", "XAI", "OpenAI_Compatible"} {
		result := r.Execute(ctx, "provider", []string{target})
		if result.Action != ActionSwitchProvider {
			t.Errorf("/provider %q = %#v, want a switch", target, result)
		}
	}
}

// The command must not silently accept an empty target: IsKnownProviderType
// normalizes "" to the default provider, which would make `/provider ""` read
// as a successful switch and then fail asynchronously as "provider name is
// empty" from the manager.
func TestProviderCommandRefusesAnEmptyTarget(t *testing.T) {
	r := newTestRegistry()
	ctx := &Context{Provider: "ollama"}

	for _, target := range []string{"", "   "} {
		result := r.Execute(ctx, "provider", []string{target})
		if result.Action == ActionSwitchProvider {
			t.Errorf("/provider %q switched to nothing", target)
		}
		if result.Error == "" || !strings.Contains(result.Error, "provider") {
			t.Errorf("/provider %q = %#v, want an error naming the problem", target, result)
		}
	}
}
