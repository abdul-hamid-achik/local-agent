package agent

import (
	"sort"
	"strings"
)

// NarrowedToolSystemPrompt gives interactive and headless kits the same
// completion contract. Names come from the validated host policy, not prose.
func NarrowedToolSystemPrompt(base string, policy ToolPolicy) string {
	names := append(policy.BuiltinNames(), policy.MemoryNames()...)
	sort.Strings(names)
	return strings.TrimSpace(base) + "\n\nThe host has narrowed the native tools for this turn. Use an exact tool-and-argument request at most once unless an intervening tool changes the relevant state. After a successful result supplies the requested information, produce visible final text instead of repeating the call.\n" +
		"Resolve relative paths against the working directory; do not prepend a slash or invent path components.\n" +
		"Available built-ins for this turn: " + strings.Join(names, ", ") + "."
}

type ToolPolicy struct {
	AllowMCP    bool
	localTools  map[string]struct{}
	memoryTools map[string]struct{}
}

func NewToolPolicy(localTools, memoryTools []string, allowMCP bool) ToolPolicy {
	policy := ToolPolicy{
		AllowMCP:    allowMCP,
		localTools:  make(map[string]struct{}, len(localTools)),
		memoryTools: make(map[string]struct{}, len(memoryTools)),
	}

	for _, name := range localTools {
		policy.localTools[name] = struct{}{}
	}
	for _, name := range memoryTools {
		policy.memoryTools[name] = struct{}{}
	}

	return policy
}

func DefaultToolPolicy() ToolPolicy {
	return BuildToolPolicy()
}

func AskToolPolicy() ToolPolicy {
	return NewToolPolicy(
		// bash_output only reads a buffer the host already owns, so a read-only
		// mode may still observe a process an earlier BUILD turn started.
		[]string{"read", "grep", "glob", "ls", "find", "diff", "exists", "bash_output", "load_skill", "consult_experts", "ask_user"},
		[]string{"memory_recall"},
		false,
	)
}

func PlanToolPolicy() ToolPolicy {
	policy := AskToolPolicy()
	policy.AllowMCP = true
	return policy
}

func BuildToolPolicy() ToolPolicy {
	return NewToolPolicy(
		[]string{"grep", "read", "write", "edit", "glob", "bash", "bash_output", "ls", "find", "diff", "mkdir", "remove", "copy", "move", "exists", "load_skill", "consult_experts", "ask_user"},
		[]string{"memory_save", "memory_recall", "memory_delete", "memory_update", "memory_list"},
		true,
	)
}

func (p ToolPolicy) AllowsBuiltin(name string) bool {
	_, ok := p.localTools[name]
	return ok
}

// restrictNative intersects a configured kit with the mode's authority.
func (p ToolPolicy) restrictNative(names []string) ToolPolicy {
	if names == nil {
		return p
	}
	restricted := NewToolPolicy(nil, nil, p.AllowMCP)
	for _, name := range names {
		if p.AllowsBuiltin(name) {
			restricted.localTools[name] = struct{}{}
		}
		if p.AllowsMemory(name) {
			restricted.memoryTools[name] = struct{}{}
		}
	}
	return restricted
}

func (p ToolPolicy) AllowsMemory(name string) bool {
	_, ok := p.memoryTools[name]
	return ok
}

func (p ToolPolicy) BuiltinNames() []string {
	names := make([]string, 0, len(p.localTools))
	for name := range p.localTools {
		names = append(names, name)
	}
	return names
}

func (p ToolPolicy) MemoryNames() []string {
	names := make([]string, 0, len(p.memoryTools))
	for name := range p.memoryTools {
		names = append(names, name)
	}
	return names
}
