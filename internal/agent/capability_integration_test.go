package agent

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/abdul-hamid-achik/local-agent/internal/capabilityadvisor"
	"github.com/abdul-hamid-achik/local-agent/internal/config"
	"github.com/abdul-hamid-achik/local-agent/internal/ecosystem"
	"github.com/abdul-hamid-achik/local-agent/internal/llm"
	"github.com/abdul-hamid-achik/local-agent/internal/mcp"
)

type capabilityCaptureClient struct {
	mu      sync.Mutex
	systems []string
}

func (client *capabilityCaptureClient) ChatStream(_ context.Context, options llm.ChatOptions, emit func(llm.StreamChunk) error) error {
	client.mu.Lock()
	client.systems = append(client.systems, options.System)
	client.mu.Unlock()
	return emit(llm.StreamChunk{Text: "done", Done: true, EvalCount: 1})
}

func (*capabilityCaptureClient) Ping() error   { return nil }
func (*capabilityCaptureClient) Model() string { return "capability-test" }
func (*capabilityCaptureClient) Embed(context.Context, string, []string) ([][]float32, error) {
	return nil, nil
}

func (client *capabilityCaptureClient) system() string {
	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.systems) == 0 {
		return ""
	}
	return client.systems[len(client.systems)-1]
}

type staticCapabilityAdviser struct {
	mu       sync.Mutex
	result   capabilityadvisor.Result
	requests []capabilityadvisor.Request
}

func (advisor *staticCapabilityAdviser) Advise(_ context.Context, request capabilityadvisor.Request) capabilityadvisor.Result {
	advisor.mu.Lock()
	advisor.requests = append(advisor.requests, request)
	result := advisor.result
	advisor.mu.Unlock()
	return result
}

func (advisor *staticCapabilityAdviser) callCount() int {
	advisor.mu.Lock()
	defer advisor.mu.Unlock()
	return len(advisor.requests)
}

type capabilityOutputRecorder struct {
	outputRecorder
	routes []CapabilityRoute
}

type capabilityRegistryBackend struct {
	exposed string
	calls   int
}

func (backend *capabilityRegistryBackend) ResolveToolName(string) (string, bool) {
	return backend.exposed, backend.exposed != ""
}

func (backend *capabilityRegistryBackend) CallTool(context.Context, string, map[string]any) (*mcp.ToolResult, error) {
	backend.calls++
	return &mcp.ToolResult{}, nil
}

func (output *capabilityOutputRecorder) CapabilityRoute(route CapabilityRoute) {
	output.routes = append(output.routes, route)
}

func TestCapabilityHintIsTransientAndClearRouteIsAdvisory(t *testing.T) {
	client := &capabilityCaptureClient{}
	advisor := &staticCapabilityAdviser{result: capabilityadvisor.Result{
		Status: capabilityadvisor.StatusResolved, Attempted: true,
		Hint: &capabilityadvisor.Hint{
			Namespaced: "bob__bob_plan", Server: "bob", Tool: "bob_plan",
			RequiredFields: []string{"workspace"},
		},
	}}
	agent := New(client, nil, 0)
	agent.capabilityAdvisor = advisor
	agent.SetModeContext("test", BuildToolPolicy())
	agent.AddUserMessage("design the repository feature")
	output := &capabilityOutputRecorder{}
	activity := CapabilityActivity{
		ScopeID: "goal_1", Objective: "Implement authentication", Phase: "planning",
		CurrentActivity:     "Design repository changes before editing",
		DesiredOutcome:      "A verifiable implementation plan",
		AvailableInputKinds: []string{"workspace"}, NonTrivial: true,
	}

	if err := agent.RunTurnWithOptions(context.Background(), output, "turn_capability", TurnOptions{Capability: activity}); err != nil {
		t.Fatal(err)
	}
	system := client.system()
	for _, expected := range []string{
		"Host capability advisory", "advisory only", "bob__bob_plan",
		"Required argument fields: workspace", "mcphub_call_tool", "approval", "execution-ledger",
	} {
		if !strings.Contains(system, expected) {
			t.Fatalf("system prompt missing %q:\n%s", expected, system)
		}
	}
	if len(output.routes) != 1 || output.routes[0] != (CapabilityRoute{Phase: "planning", Server: "bob", Tool: "bob_plan"}) {
		t.Fatalf("routes = %#v", output.routes)
	}
	for _, message := range agent.Messages() {
		if strings.Contains(message.Content, "Host capability advisory") || strings.Contains(message.DurableContent, "bob__bob_plan") {
			t.Fatalf("transient capability hint entered durable messages: %#v", message)
		}
	}
}

func TestAmbiguousCapabilityHintDoesNotEmitSelectedRoute(t *testing.T) {
	client := &capabilityCaptureClient{}
	advisor := &staticCapabilityAdviser{result: capabilityadvisor.Result{
		Status: capabilityadvisor.StatusResolved, Attempted: true,
		Hint: &capabilityadvisor.Hint{
			Namespaced: "bob__bob_plan", Server: "bob", Tool: "bob_plan", Ambiguous: true,
			Alternatives: []string{"cortex__cortex_plan"}, RequiredFields: []string{"workspace"},
		},
	}}
	agent := New(client, nil, 0)
	agent.capabilityAdvisor = advisor
	agent.SetModeContext("test", BuildToolPolicy())
	agent.AddUserMessage("plan the repository change")
	output := &capabilityOutputRecorder{}
	activity := CapabilityActivityFromPrompt("session_1", "plan the repository change", "planning", true)

	if err := agent.RunTurnWithOptions(context.Background(), output, "turn_ambiguous", TurnOptions{Capability: activity}); err != nil {
		t.Fatal(err)
	}
	if len(output.routes) != 0 {
		t.Fatalf("ambiguous recommendation emitted a selected route: %#v", output.routes)
	}
	system := client.system()
	if !strings.Contains(system, "ambiguous route") || !strings.Contains(system, "No capability has been selected") || !strings.Contains(system, "cortex__cortex_plan") {
		t.Fatalf("ambiguous hint was not explicit:\n%s", system)
	}
}

func TestTrivialChatAndResolverFailureDoNotBlockProvider(t *testing.T) {
	t.Run("trivial chat skips advisor", func(t *testing.T) {
		client := &capabilityCaptureClient{}
		advisor := &staticCapabilityAdviser{result: capabilityadvisor.Result{Status: capabilityadvisor.StatusResolved}}
		agent := New(client, nil, 0)
		agent.capabilityAdvisor = advisor
		agent.AddUserMessage("hello")
		if err := agent.Run(context.Background(), &capabilityOutputRecorder{}); err != nil {
			t.Fatal(err)
		}
		if advisor.callCount() != 0 || strings.Contains(client.system(), "Host capability advisory") {
			t.Fatalf("trivial chat resolved capabilities: calls=%d", advisor.callCount())
		}
	})

	t.Run("resolver unavailable continues", func(t *testing.T) {
		client := &capabilityCaptureClient{}
		advisor := &staticCapabilityAdviser{result: capabilityadvisor.Result{Status: capabilityadvisor.StatusUnavailable, Attempted: true}}
		agent := New(client, nil, 0)
		agent.capabilityAdvisor = advisor
		agent.AddUserMessage("investigate the repository failure and verify the cause")
		if err := agent.Run(context.Background(), &capabilityOutputRecorder{}); err != nil {
			t.Fatal(err)
		}
		if advisor.callCount() != 1 || strings.Contains(client.system(), "Host capability advisory") {
			t.Fatalf("unavailable resolver altered the turn: calls=%d system=%q", advisor.callCount(), client.system())
		}
	})
}

func TestFailedRecommendedGatewayRouteIsReconsidered(t *testing.T) {
	advisor := &staticCapabilityAdviser{result: capabilityadvisor.Result{
		Status: capabilityadvisor.StatusResolved, Attempted: true,
		Hint: &capabilityadvisor.Hint{
			Namespaced: "hitspec__hitspec_capture_webpage", Server: "hitspec", Tool: "hitspec_capture_webpage",
			RequiredFields: []string{"url"},
		},
	}}
	agent := New(&capabilityCaptureClient{}, nil, 0)
	agent.capabilityAdvisor = advisor
	activity := CapabilityActivityFromPrompt(
		"session_1", "capture https://example.com as Markdown", "research", true,
	)
	_, hint := agent.resolveTurnCapability(context.Background(), &capabilityOutputRecorder{}, activity)
	if hint == nil {
		t.Fatal("first recommendation was not resolved")
	}
	agent.markCapabilityRouteFailed(activity, "mcphub__mcphub_call_tool", map[string]any{
		"server": "hitspec", "tool": "hitspec_capture_webpage", "arguments": map[string]any{"url": "https://example.com"},
	}, hint)
	otherActivity := CapabilityActivityFromPrompt(
		"session_1", "capture https://example.org as Markdown", "research", true,
	)
	if otherActivity.CurrentActivity != activity.CurrentActivity {
		t.Fatalf("test requires identical public projection: first=%#v other=%#v", activity, otherActivity)
	}
	_, _ = agent.resolveTurnCapability(context.Background(), &capabilityOutputRecorder{}, otherActivity)
	_, _ = agent.resolveTurnCapability(context.Background(), &capabilityOutputRecorder{}, activity)

	advisor.mu.Lock()
	defer advisor.mu.Unlock()
	if len(advisor.requests) != 3 || advisor.requests[0].Reconsider || advisor.requests[1].Reconsider || !advisor.requests[2].Reconsider {
		t.Fatalf("requests = %#v", advisor.requests)
	}
}

func TestCapabilityResolverRequiresTrustedLocalMCPHub(t *testing.T) {
	makeRegistry := func(server config.ServerConfig) (*Agent, *capabilityRegistryBackend, scopedCapabilityRegistry) {
		agent := New(nil, nil, 0)
		agent.SetTrustedLocalMCPServers([]config.ServerConfig{server})
		backend := &capabilityRegistryBackend{exposed: server.Name + "__mcphub_resolve_tool"}
		return agent, backend, scopedCapabilityRegistry{agent: agent, backend: backend}
	}

	for _, test := range []struct {
		name   string
		server config.ServerConfig
	}{
		{name: "remote impostor", server: config.ServerConfig{Name: "gateway", Command: "mcphub", Transport: "streamable-http", URL: "https://example.test/mcp"}},
		{name: "wrapper impostor", server: config.ServerConfig{Name: "gateway", Command: "/bin/sh"}},
		{name: "lookalike binary", server: config.ServerConfig{Name: "gateway", Command: "mcphub-wrapper"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, backend, registry := makeRegistry(test.server)
			if exposed, ok := registry.ResolveToolName("mcphub_resolve_tool"); ok || exposed != "" {
				t.Fatalf("untrusted resolver was exposed: %q", exposed)
			}
			if backend.calls != 0 {
				t.Fatalf("untrusted backend received %d calls", backend.calls)
			}
		})
	}

	agent, backend, registry := makeRegistry(config.ServerConfig{Name: "gateway", Command: "/opt/homebrew/bin/mcphub", Transport: "stdio"})
	exposed, ok := registry.ResolveToolName("mcphub_resolve_tool")
	if !ok || exposed != "gateway__mcphub_resolve_tool" {
		t.Fatalf("trusted local resolver = %q, %v", exposed, ok)
	}
	if _, err := registry.CallTool(context.Background(), exposed, map[string]any{"query": "bounded", "max_hits": 5}); err != nil || backend.calls != 1 {
		t.Fatalf("trusted resolver call: calls=%d err=%v", backend.calls, err)
	}
	agent.DenyAllMCPTools()
	if exposed, ok := registry.ResolveToolName("mcphub_resolve_tool"); ok || exposed != "" {
		t.Fatalf("deny-all scope exposed resolver: %q", exposed)
	}
}

func TestCapabilityRouteOutcomeFailureIsSemantic(t *testing.T) {
	for _, domain := range []ecosystem.DomainState{
		ecosystem.DomainFailed, ecosystem.DomainBlocked, ecosystem.DomainConflict, ecosystem.DomainDrift,
	} {
		if !capabilityRouteOutcomeFailed(ecosystem.ToolProjection{Transport: ecosystem.TransportSucceeded, Domain: domain}, false) {
			t.Fatalf("domain %q did not invalidate the recommendation", domain)
		}
	}
	for _, domain := range []ecosystem.DomainState{ecosystem.DomainSucceeded, ecosystem.DomainUnknown, ecosystem.DomainAttention} {
		if capabilityRouteOutcomeFailed(ecosystem.ToolProjection{Transport: ecosystem.TransportSucceeded, Domain: domain}, false) {
			t.Fatalf("domain %q invalidated a non-failed route", domain)
		}
	}
}

func TestCapabilityActivityPrivacyAndClassification(t *testing.T) {
	activity := CapabilityActivityFromPrompt(
		"session_1", "capture https://example.com/page?token=private as Markdown", "working", true,
	)
	if !activity.NonTrivial || activity.Phase != "research" || !containsAnyWord(strings.Join(activity.AvailableInputKinds, ","), "url", "workspace") {
		t.Fatalf("activity = %#v", activity)
	}
	request := activity.request(false)
	if request.Activity.Objective == "" {
		t.Fatal("safe URL activity was rejected")
	}
	projected := strings.Join([]string{
		request.Activity.Objective, request.Activity.CurrentActivity, request.Activity.DesiredOutcome,
	}, " ")
	for _, private := range []string{"example.com", "token", "private"} {
		if strings.Contains(strings.ToLower(projected), private) {
			t.Fatalf("host projection retained private prompt text %q: %q", private, projected)
		}
	}

	raw := "review this CSV: Alice,alice@example.com,123-45-6789"
	rawActivity := CapabilityActivityFromPrompt("session_1", raw, "working", true)
	if !rawActivity.NonTrivial {
		t.Fatal("non-trivial raw-data review was not classified")
	}
	rawProjection := strings.Join([]string{rawActivity.Objective, rawActivity.CurrentActivity, rawActivity.DesiredOutcome}, " ")
	for _, private := range []string{"alice", "example.com", "123-45-6789", "csv:"} {
		if strings.Contains(strings.ToLower(rawProjection), private) {
			t.Fatalf("raw one-line data crossed the resolver projection: %q", rawProjection)
		}
	}

	if got := CapabilityActivityFromPrompt("session_1", "I was wondering whether cats generally enjoy sunny windows in the afternoon", "working", true); got.NonTrivial {
		t.Fatalf("long casual chat triggered capability resolution: %#v", got)
	}

	for _, unsafe := range []string{
		"hello\nraw file body", "API_KEY=private", `{"raw":"document"}`, "Authorization: Bearer private-token",
	} {
		if got := CapabilityActivityFromPrompt("session_1", unsafe, "working", true); got.NonTrivial || got.Objective != "" {
			t.Fatalf("unsafe prompt was projected: %#v", got)
		}
	}
}

func TestCapabilityActivityTracksPrivateMaterialChangesWithoutSendingThem(t *testing.T) {
	first := CapabilityActivityFromPrompt("session_1", "implement authentication", "implementation", true)
	second := CapabilityActivityFromPrompt("session_1", "implement logging", "implementation", true)
	cosmetic := CapabilityActivityFromPrompt("session_1", "IMPLEMENT, authentication!!!", "implementation", true)
	if !first.NonTrivial || !second.NonTrivial || !cosmetic.NonTrivial {
		t.Fatalf("activities were not classified: first=%#v second=%#v cosmetic=%#v", first, second, cosmetic)
	}
	if first.request(false).CacheDiscriminator == second.request(false).CacheDiscriminator {
		t.Fatal("materially different ordinary tasks share a cache discriminator")
	}
	if first.request(false).CacheDiscriminator != cosmetic.request(false).CacheDiscriminator {
		t.Fatal("case and punctuation-only changes did not reuse the cache discriminator")
	}
	for _, request := range []capabilityadvisor.Request{first.request(false), second.request(false)} {
		projection := request.Activity.Objective + request.Activity.CurrentActivity + request.Activity.DesiredOutcome
		if strings.Contains(strings.ToLower(projection), "authentication") || strings.Contains(strings.ToLower(projection), "logging") {
			t.Fatalf("private task wording entered resolver activity: %q", projection)
		}
	}
}

func TestCapabilityActivityFingerprintIncludesMaterialPromptTail(t *testing.T) {
	prefix := "implement repository support " + strings.Repeat("with bounded workspace context ", 24)
	first := CapabilityActivityFromPrompt("session_1", prefix+"for authentication", "implementation", true)
	second := CapabilityActivityFromPrompt("session_1", prefix+"for audit logging", "implementation", true)
	if !first.NonTrivial || !second.NonTrivial {
		t.Fatalf("long activities were not classified: first=%#v second=%#v", first, second)
	}
	if first.request(false).CacheDiscriminator == second.request(false).CacheDiscriminator {
		t.Fatal("material prompt changes beyond the resolver projection bound reused the cache discriminator")
	}
	for _, request := range []capabilityadvisor.Request{first.request(false), second.request(false)} {
		projection := request.Activity.Objective + request.Activity.CurrentActivity + request.Activity.DesiredOutcome
		for _, private := range []string{"authentication", "audit logging", "bounded workspace context"} {
			if strings.Contains(strings.ToLower(projection), private) {
				t.Fatalf("private prompt tail entered resolver activity: %q", projection)
			}
		}
	}
}

func TestDurableGoalCapabilityHonorsHostPhaseAndActivityChanges(t *testing.T) {
	for _, phase := range []string{"research", "planning", "implementation", "verification"} {
		activity := DurableGoalCapabilityActivity("goal_1", "Implement authentication", phase, "cortex_"+phase)
		if !activity.NonTrivial || activity.Phase != phase {
			t.Fatalf("phase %q projected as %#v", phase, activity)
		}
	}
	first := DurableGoalCapabilityActivity("goal_1", "Implement authentication", "implementation", "cortex_edit")
	second := DurableGoalCapabilityActivity("goal_1", "Implement authentication", "implementation", "cortex_test")
	if first.request(false).CacheDiscriminator == second.request(false).CacheDiscriminator {
		t.Fatal("same-phase durable action transition reused the cache discriminator")
	}
}

func TestHeadlessCapabilityRouteStaysOnStderr(t *testing.T) {
	var stdout, stderr strings.Builder
	output := newHeadlessOutput(&stdout, &stderr)
	output.CapabilityRoute(CapabilityRoute{Phase: "research", Server: "hitspec", Tool: "hitspec_capture_webpage"})
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "advisory") || !strings.Contains(stderr.String(), "hitspec__hitspec_capture_webpage") {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}
