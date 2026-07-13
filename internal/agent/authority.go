package agent

import (
	"path/filepath"
	"strings"

	"github.com/abdul-hamid-achik/local-agent/internal/config"
	executionpkg "github.com/abdul-hamid-achik/local-agent/internal/execution"
	"github.com/abdul-hamid-achik/local-agent/internal/llm"
	permissionpkg "github.com/abdul-hamid-achik/local-agent/internal/permission"
)

// AuthorityMode is the host-owned authority granted to one conversational
// turn. It is deliberately separate from the model-facing mode prompt and the
// advertised tool set: changing prose or untrusted MCP annotations must never
// widen execution authority.
type AuthorityMode uint8

const (
	// AuthorityNormal keeps every risky operation on the configured permission
	// path. Read-only built-ins retain their existing implicit authorization.
	AuthorityNormal AuthorityMode = iota
	// AuthorityPlan is the typed companion to the read-only planning tool
	// policy. It never grants an automatic mutation by itself.
	AuthorityPlan
	// AuthorityAutoScoped permits only host-catalogued, workspace-scoped
	// operations to bypass an interactive modal. Unknown, destructive, shell,
	// and non-catalogued MCP calls still use the normal permission path.
	AuthorityAutoScoped
)

// Valid reports whether mode is a supported host authority.
func (mode AuthorityMode) Valid() bool {
	switch mode {
	case AuthorityNormal, AuthorityPlan, AuthorityAutoScoped:
		return true
	default:
		return false
	}
}

// SetAuthorityMode installs the authority to snapshot at the start of the next
// turn. Invalid values fail closed to NORMAL. A running turn keeps the value it
// captured at admission, so a concurrent UI mode change cannot widen it.
func (a *Agent) SetAuthorityMode(mode AuthorityMode) {
	if !mode.Valid() {
		mode = AuthorityNormal
	}
	a.mu.Lock()
	a.authorityMode = mode
	a.mu.Unlock()
}

// AuthorityMode returns the authority that the next turn will snapshot.
func (a *Agent) AuthorityMode() AuthorityMode {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if !a.authorityMode.Valid() {
		return AuthorityNormal
	}
	return a.authorityMode
}

type mcpAuthorityContract struct {
	effect          executionpkg.EffectClass
	auto            bool
	workspaceScoped bool
}

type trustedMCPImplementation uint8

const (
	trustedMCPUnknown trustedMCPImplementation = iota
	trustedMCPHub
	trustedCortex
)

// SetTrustedLocalMCPServers derives namespace trust exclusively from the
// host-resolved configuration. A server is trusted only when it is local STDIO
// and its executable basename is exactly mcphub or cortex; remote transports,
// wrapper commands, and lookalike names fail closed. Call this once with the
// same server list used to connect the Registry, before starting turns.
func (a *Agent) SetTrustedLocalMCPServers(servers []config.ServerConfig) {
	trusted := make(map[string]trustedMCPImplementation)
	for _, server := range servers {
		if server.Name == "" || strings.Contains(server.Name, "__") || strings.TrimSpace(server.Name) != server.Name ||
			strings.TrimSpace(server.URL) != "" {
			continue
		}
		transport := strings.ToLower(strings.TrimSpace(server.Transport))
		if transport != "" && transport != "stdio" {
			continue
		}
		command := strings.TrimSpace(server.Command)
		if command == "" || command != server.Command {
			continue
		}
		switch filepath.Base(filepath.Clean(command)) {
		case "mcphub":
			trusted[server.Name] = trustedMCPHub
		case "cortex":
			trusted[server.Name] = trustedCortex
		}
	}
	a.mu.Lock()
	a.trustedMCP = trusted
	a.mu.Unlock()
}

func (a *Agent) trustedMCPImplementation(namespace string) trustedMCPImplementation {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.trustedMCP[namespace]
}

// trustedMCPHubReadContracts and trustedCortexContracts are host-owned exact
// contracts. They intentionally do not consult llm.ToolBehavior: MCP
// annotations and descriptions are server-authored presentation metadata and
// cannot weaken approval or durable recovery policy.
var trustedMCPHubReadContracts = map[string]mcpAuthorityContract{
	"mcphub_list_servers":  {effect: executionpkg.EffectReadOnly, auto: true},
	"mcphub_search_tools":  {effect: executionpkg.EffectReadOnly, auto: true},
	"mcphub_describe_tool": {effect: executionpkg.EffectReadOnly, auto: true},
	"mcphub_resolve_tool":  {effect: executionpkg.EffectReadOnly, auto: true},
	"mcphub_get_result":    {effect: executionpkg.EffectReadOnly, auto: true},
	"mcphub_stats":         {effect: executionpkg.EffectReadOnly, auto: true},
}

var trustedCortexContracts = map[string]mcpAuthorityContract{
	// Exact reads are read-only in the durable ledger as well as the approval
	// layer. An application-level read error therefore terminates as failed or
	// cancelled and never strands the session behind outcome_unknown.
	"cortex_status":        {effect: executionpkg.EffectReadOnly, auto: true},
	"cortex_list_tasks":    {effect: executionpkg.EffectReadOnly, auto: true},
	"cortex_sessions":      {effect: executionpkg.EffectReadOnly, auto: true},
	"cortex_timeline":      {effect: executionpkg.EffectReadOnly, auto: true},
	"cortex_metrics":       {effect: executionpkg.EffectReadOnly, auto: true},
	"cortex_overview":      {effect: executionpkg.EffectReadOnly, auto: true},
	"cortex_handoff":       {effect: executionpkg.EffectReadOnly, auto: true},
	"cortex_read_evidence": {effect: executionpkg.EffectReadOnly, auto: true},
	"cortex_read_artifact": {effect: executionpkg.EffectReadOnly, auto: true},
	"cortex_recall_cases":  {effect: executionpkg.EffectReadOnly, auto: true},

	// Cortex's local lifecycle is an explicit, bounded coordination contract.
	// These operations may update Cortex state in AUTO, but remain effectful in
	// the ledger. Cross-workspace requests fall back to interactive approval.
	"cortex_start_task":       {effect: executionpkg.Effectful, auto: true, workspaceScoped: true},
	"cortex_open_task":        {effect: executionpkg.Effectful, auto: true, workspaceScoped: true},
	"cortex_investigate":      {effect: executionpkg.Effectful, auto: true, workspaceScoped: true},
	"cortex_plan":             {effect: executionpkg.Effectful, auto: true, workspaceScoped: true},
	"cortex_begin_change":     {effect: executionpkg.Effectful, auto: true, workspaceScoped: true},
	"cortex_verify":           {effect: executionpkg.Effectful, auto: true, workspaceScoped: true},
	"cortex_remember":         {effect: executionpkg.Effectful, auto: true, workspaceScoped: true},
	"cortex_resolve":          {effect: executionpkg.Effectful, auto: true, workspaceScoped: true},
	"cortex_note":             {effect: executionpkg.Effectful, auto: true, workspaceScoped: true},
	"cortex_request_decision": {effect: executionpkg.Effectful, auto: true, workspaceScoped: true},
	// answer_decision, archive, unarchive, and abort_task deliberately remain
	// outside this catalog: they represent human authority or lifecycle reversal.
}

// trustedMCPContract resolves only Local Agent's reserved direct/gateway
// routes. Suffix matching is forbidden: `evil__cortex_status` must never gain
// Cortex authority merely by resembling a known operation.
func (a *Agent) trustedMCPContract(call llm.ToolCall) (mcpAuthorityContract, bool) {
	if call.Name == "" || strings.TrimSpace(call.Name) != call.Name {
		return mcpAuthorityContract{}, false
	}
	parts := strings.Split(call.Name, "__")
	switch {
	case len(parts) == 2 && a.trustedMCPImplementation(parts[0]) == trustedCortex:
		contract, ok := trustedCortexContracts[parts[1]]
		return contract, ok
	case len(parts) == 2 && a.trustedMCPImplementation(parts[0]) == trustedMCPHub:
		if parts[1] == "mcphub_call_tool" {
			server, tool, ok := exactLazyMCPHubTarget(call.Arguments)
			if !ok || server != "cortex" {
				return mcpAuthorityContract{}, false
			}
			contract, found := trustedCortexContracts[tool]
			return contract, found
		}
		contract, ok := trustedMCPHubReadContracts[parts[1]]
		return contract, ok
	case len(parts) == 3 && a.trustedMCPImplementation(parts[0]) == trustedMCPHub && parts[1] == "cortex":
		contract, ok := trustedCortexContracts[parts[2]]
		return contract, ok
	default:
		return mcpAuthorityContract{}, false
	}
}

func exactLazyMCPHubTarget(args map[string]any) (server, tool string, ok bool) {
	rawTool, toolOK := args["tool"].(string)
	if !toolOK || rawTool == "" || strings.TrimSpace(rawTool) != rawTool {
		return "", "", false
	}
	rawServer, hasServer := args["server"]
	if hasServer {
		server, ok = rawServer.(string)
		if !ok || strings.TrimSpace(server) != server || strings.Contains(server, "__") {
			return "", "", false
		}
	}
	if !hasServer || server == "" {
		var found bool
		server, tool, found = strings.Cut(rawTool, "__")
		if !found || server == "" || tool == "" {
			return "", "", false
		}
	} else {
		tool = strings.TrimPrefix(rawTool, server+"__")
	}
	if tool == "" || strings.Contains(tool, "__") {
		return "", "", false
	}
	return server, tool, true
}

func (a *Agent) authorityAutoApproves(mode AuthorityMode, call llm.ToolCall, kind executionpkg.Kind) bool {
	if a.authorityPermissionDenied(call.Name) {
		return false
	}
	if kind == executionpkg.KindMCP {
		contract, ok := a.trustedMCPContract(call)
		if !ok || !contract.auto {
			return false
		}
		// A host-catalogued read has the same authority regardless of transport:
		// read/find/grep built-ins do not open mutation approval modals, so the
		// equivalent local MCP read must not become noisier merely because it is
		// routed through MCPHub. Explicit deny above still wins.
		if contract.effect == executionpkg.EffectReadOnly {
			return true
		}
		if mode != AuthorityAutoScoped {
			return false
		}
		return !contract.workspaceScoped || a.cortexWorkspaceWithinAuthority(call)
	}
	if mode != AuthorityAutoScoped {
		return false
	}
	switch kind {
	case executionpkg.KindBuiltin:
		if strings.TrimSpace(a.workDir) == "" {
			return false
		}
		switch call.Name {
		case "write", "edit", "mkdir":
			path, ok := call.Arguments["path"].(string)
			if !ok || strings.TrimSpace(path) == "" {
				return false
			}
			_, err := a.resolvePath(path)
			return err == nil
		default:
			return false
		}
	default:
		return false
	}
}

func (a *Agent) authorityPermissionDenied(toolName string) bool {
	return a.permChecker != nil && a.permChecker.ToCheckResult(toolName) == permissionpkg.CheckDeny
}

func (a *Agent) cortexWorkspaceWithinAuthority(call llm.ToolCall) bool {
	if strings.TrimSpace(a.workDir) == "" {
		return false
	}
	args := call.Arguments
	if a.isTrustedLazyMCPHubCall(call.Name) {
		nested, present := args["arguments"]
		if !present || nested == nil {
			return true
		}
		var ok bool
		args, ok = nested.(map[string]any)
		if !ok {
			return false
		}
	}
	raw, present := args["workspace"]
	if !present || raw == nil {
		return true
	}
	workspace, ok := raw.(string)
	if !ok {
		return false
	}
	if strings.TrimSpace(workspace) == "" {
		return true
	}
	_, err := a.resolvePath(workspace)
	return err == nil
}

func (a *Agent) isTrustedLazyMCPHubCall(name string) bool {
	parts := strings.Split(name, "__")
	return len(parts) == 2 && parts[1] == "mcphub_call_tool" &&
		a.trustedMCPImplementation(parts[0]) == trustedMCPHub
}
