package agent

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/abdul-hamid-achik/local-agent/internal/capabilityadvisor"
	"github.com/abdul-hamid-achik/local-agent/internal/ecosystem"
	"github.com/abdul-hamid-achik/local-agent/internal/mcp"
)

const (
	capabilityResolverTimeout = 3 * time.Second
	maxCapabilityObjective    = 480
)

// CapabilityActivity is host-owned, bounded context for one turn. ScopeID is
// a stable goal ID when a Goal Runtime exists and otherwise a session ID. No
// field may contain raw files, tool output, credentials, or secret values.
type CapabilityActivity struct {
	ScopeID             string
	Objective           string
	Phase               string
	CurrentActivity     string
	DesiredOutcome      string
	AvailableInputKinds []string
	NonTrivial          bool
	Reconsider          bool
	cacheDiscriminator  [sha256.Size]byte
}

type capabilityAdviser interface {
	Advise(context.Context, capabilityadvisor.Request) capabilityadvisor.Result
}

type capabilityRetryKey [sha256.Size]byte

type capabilityToolRegistry interface {
	ResolveToolName(remoteName string) (string, bool)
	CallTool(ctx context.Context, exposedName string, args map[string]any) (*mcp.ToolResult, error)
}

// CapabilityRoute is an advisory UI event. It is neither a tool execution nor
// evidence that the recommended operation succeeded.
type CapabilityRoute struct {
	Phase  string
	Server string
	Tool   string
}

// CapabilityRouteOutput is optional so headless and test Output
// implementations do not need to grow transcript state for advisory events.
type CapabilityRouteOutput interface {
	CapabilityRoute(CapabilityRoute)
}

// CapabilityActivityFromPrompt creates a conservative activity projection for
// an ordinary turn. It accepts only one short task sentence; multiline/raw or
// credential-like prompts skip host-side resolution and remain available to
// the model-driven MCPHub guidance path.
func CapabilityActivityFromPrompt(scopeID, prompt, phase string, workspaceAvailable bool) CapabilityActivity {
	material, ok := normalizedCapabilityPrompt(prompt)
	if !ok {
		return CapabilityActivity{}
	}
	signal := boundCapabilityText(material, maxCapabilityObjective)
	inputs := capabilityInputKinds(signal, workspaceAvailable)
	resolvedPhase := capabilityPhase(signal, phase)
	objective, current, outcome, nonTrivial := projectCapabilityActivity(signal, resolvedPhase, inputs)
	if !nonTrivial {
		return CapabilityActivity{}
	}
	return CapabilityActivity{
		ScopeID:             strings.TrimSpace(scopeID),
		Objective:           objective,
		Phase:               resolvedPhase,
		CurrentActivity:     current,
		DesiredOutcome:      outcome,
		AvailableInputKinds: inputs,
		NonTrivial:          true,
		Reconsider:          asksToReconsiderCapabilities(signal),
		// The full validated prompt participates only in this local digest. The
		// resolver still receives the bounded host-authored projection above.
		cacheDiscriminator: capabilityMaterialFingerprint(material),
	}
}

// DurableGoalCapabilityActivity preserves the real goal ID while keeping
// current activity and desired outcome host-authored. The objective must still
// pass the same privacy boundary as an ordinary prompt.
func DurableGoalCapabilityActivity(goalID, objective, phase, activityDiscriminator string) CapabilityActivity {
	signal, ok := capabilityPromptObjective(objective)
	if !ok || strings.TrimSpace(goalID) == "" {
		return CapabilityActivity{}
	}
	inputs := capabilityInputKinds(signal, true)
	resolvedPhase := normalizeHostCapabilityPhase(phase)
	projectedObjective, _, _, matched := projectCapabilityActivity(signal, resolvedPhase, inputs)
	if !matched {
		projectedObjective = "Advance a durable workspace goal"
	}
	activity := CapabilityActivity{
		ScopeID: strings.TrimSpace(goalID), Objective: projectedObjective, Phase: resolvedPhase,
		AvailableInputKinds: inputs, NonTrivial: true,
		Reconsider:         asksToReconsiderCapabilities(signal),
		cacheDiscriminator: capabilityMaterialFingerprint(activityDiscriminator),
	}
	activity.CurrentActivity, activity.DesiredOutcome = durableCapabilityWork(resolvedPhase)
	return activity
}

func durableCapabilityWork(phase string) (current, outcome string) {
	switch phase {
	case "research":
		return "Investigate the next bounded part of the durable goal", "A source-backed finding tied to the goal criteria"
	case "planning":
		return "Design the next repository changes before editing", "A verifiable plan tied to the goal criteria"
	case "verification":
		return "Verify the current workspace state against the goal criteria", "A reproducible verification result"
	case "handoff":
		return "Prepare the verified durable-goal result for handoff", "A bounded durable completion record"
	case "decision":
		return "Clarify the blocked durable-goal decision", "A bounded decision that can safely unblock work"
	default:
		return "Implement the next concrete slice of the durable goal", "Verified progress toward the goal criteria"
	}
}

func (activity CapabilityActivity) request(reconsider bool) capabilityadvisor.Request {
	return capabilityadvisor.Request{
		GoalID:             activity.ScopeID,
		NonTrivial:         activity.NonTrivial,
		Reconsider:         activity.Reconsider || reconsider,
		CacheDiscriminator: activity.cacheDiscriminator,
		Activity: capabilityadvisor.Activity{
			Objective:           activity.Objective,
			Phase:               activity.Phase,
			CurrentActivity:     activity.CurrentActivity,
			DesiredOutcome:      activity.DesiredOutcome,
			AvailableInputKinds: append([]string(nil), activity.AvailableInputKinds...),
		},
	}
}

// scopedCapabilityRegistry prevents the host-side resolver from escaping the
// current profile's MCP scope or an explicit permission deny. It additionally
// requires the resolved namespace to be the host-pinned local MCPHub binary;
// server-authored names and descriptions can never establish this trust. It
// never changes MCPServerScope and exposes no downstream call surface.
type scopedCapabilityRegistry struct {
	agent   *Agent
	backend capabilityToolRegistry
}

func (registry scopedCapabilityRegistry) ResolveToolName(remoteName string) (string, bool) {
	agent := registry.agent
	backend := registry.backend
	if agent == nil || backend == nil {
		return "", false
	}
	exposed, ok := backend.ResolveToolName(remoteName)
	namespace, operation, namespaced := strings.Cut(exposed, "__")
	if !ok || !namespaced || strings.Contains(operation, "__") || operation != remoteName ||
		agent.trustedMCPImplementation(namespace) != trustedMCPHub ||
		!agent.allowsMCPTool(exposed) || agent.authorityPermissionDenied(exposed) {
		return "", false
	}
	return exposed, true
}

func (registry scopedCapabilityRegistry) CallTool(ctx context.Context, exposedName string, args map[string]any) (*mcp.ToolResult, error) {
	expected, ok := registry.ResolveToolName("mcphub_resolve_tool")
	if !ok || exposedName != expected {
		return nil, fmt.Errorf("capability resolver is outside the active MCP scope")
	}
	return registry.backend.CallTool(ctx, exposedName, args)
}

func (a *Agent) resolveTurnCapability(ctx context.Context, out Output, activity CapabilityActivity) (string, *capabilityadvisor.Hint) {
	if a == nil || a.capabilityAdvisor == nil || !activity.NonTrivial {
		return "", nil
	}
	activity.ScopeID = strings.TrimSpace(activity.ScopeID)
	if activity.ScopeID == "" {
		return "", nil
	}

	retryKey := capabilityActivityRetryKey(activity)
	a.mu.Lock()
	_, retry := a.capabilityRetries[retryKey]
	delete(a.capabilityRetries, retryKey)
	a.mu.Unlock()

	resolveCtx, cancel := context.WithTimeout(ctx, capabilityResolverTimeout)
	result := a.capabilityAdvisor.Advise(resolveCtx, activity.request(retry))
	cancel()
	if result.Status != capabilityadvisor.StatusResolved || result.Hint == nil {
		return "", nil
	}
	hint := result.Hint
	if result.Attempted && !hint.Ambiguous {
		if routeOut, ok := out.(CapabilityRouteOutput); ok {
			routeOut.CapabilityRoute(CapabilityRoute{Phase: activity.Phase, Server: hint.Server, Tool: hint.Tool})
		}
	}
	return a.formatCapabilityHint(*hint), hint
}

func (a *Agent) formatCapabilityHint(hint capabilityadvisor.Hint) string {
	var builder strings.Builder
	builder.WriteString("## Host capability advisory\n")
	builder.WriteString("This bounded MCPHub recommendation is advisory only. It is not a tool execution, result, success receipt, evidence, or additional authority.\n")
	if hint.Ambiguous {
		builder.WriteString("MCPHub reported an ambiguous route. No capability has been selected. Candidate identifiers: ")
		candidates := append([]string{hint.Namespaced}, hint.Alternatives...)
		builder.WriteString(strings.Join(candidates, ", "))
		if a.toolPolicy.AllowMCP {
			builder.WriteString(". Use the visible mcphub_search_tools resolver companion before choosing a downstream tool.\n")
		} else {
			builder.WriteString(". The current turn policy does not expose MCP downstream tools, so do not treat any candidate as executable in this turn.\n")
		}
	} else {
		fmt.Fprintf(&builder, "MCPHub recommends %s (server %s, tool %s).\n", hint.Namespaced, hint.Server, hint.Tool)
	}
	if len(hint.RequiredFields) > 0 {
		fmt.Fprintf(&builder, "Required argument fields: %s.\n", strings.Join(hint.RequiredFields, ", "))
	}
	if hint.NeedsDescription() && a.toolPolicy.AllowMCP {
		builder.WriteString("The argument template was truncated. Call the visible mcphub_describe_tool before constructing arguments.\n")
	}
	if !hint.Ambiguous && a.toolPolicy.AllowMCP {
		builder.WriteString("If this route still matches the work, fill the arguments and invoke it through the visible mcphub_call_tool gateway. The downstream call must pass through normal scope, privacy, approval, and execution-ledger policy.\n")
	} else if !hint.Ambiguous {
		builder.WriteString("The current turn policy does not expose MCP downstream tools. Keep this as routing advice only and do not claim the recommendation was executed.\n")
	}
	return strings.TrimSpace(builder.String())
}

func (a *Agent) markCapabilityRouteFailed(activity CapabilityActivity, callName string, args map[string]any, hint *capabilityadvisor.Hint) {
	if a == nil || hint == nil || hint.Ambiguous || strings.TrimSpace(activity.ScopeID) == "" {
		return
	}
	server, tool, ok := exactLazyMCPHubTarget(args)
	if !ok || !strings.HasSuffix(callName, "__mcphub_call_tool") || server != hint.Server || tool != hint.Tool {
		return
	}
	a.mu.Lock()
	a.capabilityRetries[capabilityActivityRetryKey(activity)] = struct{}{}
	a.mu.Unlock()
}

func capabilityActivityRetryKey(activity CapabilityActivity) capabilityRetryKey {
	values := []string{
		activity.ScopeID, activity.Objective, activity.Phase,
		activity.CurrentActivity, activity.DesiredOutcome,
	}
	kinds := append([]string(nil), activity.AvailableInputKinds...)
	sort.Strings(kinds)
	values = append(values, kinds...)
	for index := range values {
		values[index] = strings.ToLower(strings.Join(strings.Fields(values[index]), " "))
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(strings.Join(values, "\x00")))
	_, _ = hash.Write(activity.cacheDiscriminator[:])
	var key capabilityRetryKey
	copy(key[:], hash.Sum(nil))
	return key
}

func capabilityRouteOutcomeFailed(projection ecosystem.ToolProjection, toolError bool) bool {
	if toolError || projection.Transport == ecosystem.TransportFailed {
		return true
	}
	switch projection.Domain {
	case ecosystem.DomainFailed, ecosystem.DomainBlocked, ecosystem.DomainConflict, ecosystem.DomainDrift:
		return true
	default:
		return false
	}
}

func capabilityPromptObjective(prompt string) (string, bool) {
	value, ok := normalizedCapabilityPrompt(prompt)
	if !ok {
		return "", false
	}
	return boundCapabilityText(value, maxCapabilityObjective), true
}

// normalizedCapabilityPrompt validates private task wording for local
// classification and fingerprinting. Callers must not put its return value in
// resolver queries, durable state, UI events, or transcripts authored by the
// host. Only the bounded semantic projection crosses the MCPHub boundary.
func normalizedCapabilityPrompt(prompt string) (string, bool) {
	if !utf8.ValidString(prompt) || strings.ContainsAny(prompt, "\r\n") || strings.Contains(prompt, "```") {
		return "", false
	}
	value := strings.Join(strings.Fields(strings.TrimSpace(prompt)), " ")
	if value == "" || looksLikeRawJSON(value) || containsCredentialValue(value) {
		return "", false
	}
	return value, true
}

// projectCapabilityActivity converts private user wording into a small,
// host-authored semantic description. The original prompt is used only for
// local classification and never crosses the resolver boundary.
func projectCapabilityActivity(signal, phase string, inputKinds []string) (objective, current, outcome string, nonTrivial bool) {
	lower := " " + strings.ToLower(signal)
	hasInput := func(kind string) bool {
		for _, candidate := range inputKinds {
			if candidate == kind {
				return true
			}
		}
		return false
	}

	switch {
	case asksToReconsiderCapabilities(signal):
		return "Reconsider the capability route for the current task",
			"Ask MCPHub to reconsider available capabilities",
			"A fresh bounded capability recommendation", true
	case hasInput("url") && containsAnyWord(lower,
		" capture", " fetch", " download", " preserve", " markdown",
		" capturar", " descargar", " guardar", " conservar"):
		return "Preserve a public webpage as readable Markdown",
			"Capture and normalize content from a public URL",
			"A readable durable Markdown artifact", true
	case hasInput("url"):
		return "Use a public URL for a non-trivial task",
			"Inspect or retrieve content from a public URL",
			"Verifiable information derived from the URL", true
	case hasInput("artifact_id") || containsAnyWord(lower,
		" stored artifact", " saved artifact", " artefacto guardado", " artifact search"):
		return "Find a previously stored artifact",
			"Locate durable data by its artifact metadata",
			"A bounded reference to the stored artifact", true
	case containsAnyWord(lower,
		" web search", " search online", " search the web", " current information", " latest information",
		" búsqueda web", " buscar en línea", " buscar online", " información actual"):
		return "Search the live public web for current information",
			"Discover relevant public web sources",
			"A bounded set of candidate public sources", true
	case hasInput("database") && hasInput("cli"):
		return "Investigate code database and command-line evidence together",
			"Correlate evidence across multiple project sources",
			"A source-backed diagnosis", true
	case phase == "planning" && nonTrivialCapabilityPrompt(signal, inputKinds):
		return "Plan a repository feature before implementation",
			"Design repository changes before editing",
			"A verifiable implementation plan", true
	case phase == "verification" && nonTrivialCapabilityPrompt(signal, inputKinds):
		return "Verify a repository change",
			"Check the requested behavior against available evidence",
			"A reproducible verification result", true
	case phase == "research" && nonTrivialCapabilityPrompt(signal, inputKinds):
		return "Investigate the available project evidence",
			"Research the requested issue using available inputs",
			"A source-backed finding", true
	case phase == "implementation" && nonTrivialCapabilityPrompt(signal, inputKinds):
		return "Implement a repository change",
			"Make the requested workspace change",
			"A verified implementation", true
	case nonTrivialCapabilityPrompt(signal, inputKinds):
		return "Complete a non-trivial workspace task",
			"Work on the requested task using available inputs",
			"A verifiable task result", true
	default:
		return "", "", "", false
	}
}

func looksLikeRawJSON(value string) bool {
	return strings.HasPrefix(value, "{") || strings.HasPrefix(value, "[")
}

func containsCredentialValue(value string) bool {
	lower := strings.ToLower(value)
	if strings.Contains(lower, "bearer ") || strings.Contains(lower, "-----begin ") || strings.Contains(lower, "sk-") || strings.Contains(lower, "ghp_") {
		return true
	}
	for _, name := range []string{"password", "passwd", "secret", "api key", "api_key", "access token", "access_token", "refresh token", "authorization", "cookie", "credential"} {
		for _, separator := range []string{"=", ":", " is ", " es "} {
			if strings.Contains(lower, name+separator) {
				return true
			}
		}
	}
	return false
}

func capabilityPhase(objective, fallback string) string {
	lower := strings.ToLower(objective)
	tests := []struct {
		phase string
		terms []string
	}{
		{phase: "verification", terms: []string{" verify", "validate", " test", "probar", "verificar", "validar"}},
		{phase: "planning", terms: []string{" plan", "design", "architecture", "planear", "diseñar", "arquitectura"}},
		{phase: "research", terms: []string{"research", "investigat", "search", "explore", "inspect", "fetch", "capture", "buscar", "investigar", "explorar", "revisar", "descargar", "capturar"}},
		{phase: "implementation", terms: []string{"implement", "build", "fix", "edit", "create", "update", "install", "configure", "implementar", "construir", "arreglar", "editar", "crear", "actualizar", "instalar", "configurar"}},
	}
	padded := " " + lower
	for _, test := range tests {
		for _, term := range test.terms {
			if strings.Contains(padded, term) {
				return test.phase
			}
		}
	}
	fallback = strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(fallback)), "_"))
	if fallback == "" {
		return "working"
	}
	return boundCapabilityText(fallback, 48)
}

func normalizeHostCapabilityPhase(phase string) string {
	phase = strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(phase)), "_"))
	if phase == "" {
		return "working"
	}
	return boundCapabilityText(phase, 48)
}

func capabilityMaterialFingerprint(value string) [sha256.Size]byte {
	var normalized strings.Builder
	space := true
	for _, r := range strings.ToLower(value) {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			normalized.WriteRune(r)
			space = false
			continue
		}
		if !space {
			normalized.WriteByte(' ')
			space = true
		}
	}
	return sha256.Sum256([]byte(strings.TrimSpace(normalized.String())))
}

func capabilityPhaseForAuthority(mode AuthorityMode) string {
	switch mode {
	case AuthorityPlan:
		return "planning"
	case AuthorityAutoScoped:
		return "implementation"
	default:
		return "working"
	}
}

func capabilityInputKinds(objective string, workspaceAvailable bool) []string {
	lower := strings.ToLower(objective)
	set := make(map[string]struct{})
	if workspaceAvailable {
		set["workspace"] = struct{}{}
	}
	if strings.Contains(lower, "https://") || strings.Contains(lower, "http://") || strings.Contains(lower, "www.") {
		set["url"] = struct{}{}
	}
	if containsAnyWord(lower, "database", "postgres", "mysql", "sqlite", "mongodb", "base de datos") {
		set["database"] = struct{}{}
	}
	if containsAnyWord(lower, " cli", "command", "terminal", "shell", "comando", "terminal") {
		set["cli"] = struct{}{}
	}
	if containsAnyWord(lower, "artifact id", "artifact_id", "stash id", "stash_id", "artefacto") {
		set["artifact_id"] = struct{}{}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func nonTrivialCapabilityPrompt(objective string, inputKinds []string) bool {
	if len(inputKinds) > 1 {
		return true
	}
	lower := " " + strings.ToLower(objective)
	return containsAnyWord(lower,
		" implement", " build", " fix", " analyze", " research", " search", " fetch", " capture", " inspect", " read", " plan", " design", " test", " verify", " install", " configure", " diagnose", " create", " update", " review",
		" implementar", " construir", " arreglar", " analizar", " investigar", " buscar", " descargar", " capturar", " revisar", " leer", " planear", " diseñar", " probar", " verificar", " instalar", " configurar", " diagnosticar", " crear", " actualizar")
}

func asksToReconsiderCapabilities(objective string) bool {
	lower := strings.ToLower(objective)
	return (strings.Contains(lower, "reconsider") || strings.Contains(lower, "reconsidera")) &&
		(strings.Contains(lower, "capabilit") || strings.Contains(lower, "tool") || strings.Contains(lower, "herramient"))
}

func containsAnyWord(value string, terms ...string) bool {
	for _, term := range terms {
		if strings.Contains(value, term) {
			return true
		}
	}
	return false
}

func boundCapabilityText(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	cut := limit - len("…")
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return strings.TrimSpace(value[:cut]) + "…"
}

func capabilityScopeID(sessionID int64, turnID string) string {
	if sessionID > 0 {
		return fmt.Sprintf("session_%d", sessionID)
	}
	var builder strings.Builder
	for _, r := range strings.TrimSpace(turnID) {
		if unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_' || r == '-' {
			builder.WriteRune(r)
		}
	}
	if builder.Len() == 0 {
		return "turn"
	}
	return "turn_" + builder.String()
}
