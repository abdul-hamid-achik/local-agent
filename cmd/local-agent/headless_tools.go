package main

import (
	"fmt"
	"strings"

	"github.com/abdul-hamid-achik/local-agent/internal/agent"
)

// narrowHeadlessToolPolicy validates an exact comma-separated built-in tool
// list against the selected mode. The returned policy intentionally contains
// no memory tools and cannot expose MCP, so this flag can only reduce the
// authority already granted by the mode.
func narrowHeadlessToolPolicy(base agent.ToolPolicy, value string) (agent.ToolPolicy, error) {
	if strings.TrimSpace(value) == "" {
		return agent.ToolPolicy{}, fmt.Errorf("--tools requires one or more comma-separated built-in tool names")
	}

	parts := strings.Split(value, ",")
	names := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	knownBuiltins := agent.BuildToolPolicy()
	for _, part := range parts {
		name := strings.TrimSpace(part)
		if name == "" {
			return agent.ToolPolicy{}, fmt.Errorf("--tools contains an empty tool name")
		}
		if _, duplicate := seen[name]; duplicate {
			return agent.ToolPolicy{}, fmt.Errorf("--tools repeats tool %q", name)
		}
		if !knownBuiltins.AllowsBuiltin(name) {
			return agent.ToolPolicy{}, fmt.Errorf("--tools names unknown built-in tool %q", name)
		}
		if !base.AllowsBuiltin(name) {
			return agent.ToolPolicy{}, fmt.Errorf("--tools tool %q is not allowed by the selected mode", name)
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}

	return agent.NewToolPolicy(names, nil, false), nil
}

// narrowHeadlessSystemPrompt makes the reduced surface explicit to small
// models. It carries no user-controlled prose: names have already passed the
// exact built-in policy validator and are sorted for deterministic prompts.
func narrowHeadlessSystemPrompt(base string, policy agent.ToolPolicy) string {
	return agent.NarrowedToolSystemPrompt(base, policy)
}
