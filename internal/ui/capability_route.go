package ui

import (
	"strings"
	"unicode"

	"github.com/abdul-hamid-achik/local-agent/internal/agent"
	"github.com/abdul-hamid-achik/local-agent/internal/goaladvisor"
)

const (
	maxCapabilityPhaseWidth  = 24
	maxCapabilityServerWidth = 32
	maxCapabilityToolWidth   = 48
)

func sanitizeCapabilityRoute(route agent.CapabilityRoute) agent.CapabilityRoute {
	if strings.IndexFunc(route.Phase, unicode.IsControl) >= 0 ||
		strings.IndexFunc(route.Server, unicode.IsControl) >= 0 ||
		strings.IndexFunc(route.Tool, unicode.IsControl) >= 0 {
		return agent.CapabilityRoute{}
	}
	route.Phase = truncateDisplay(sanitizeTerminalSingleLine(route.Phase), maxCapabilityPhaseWidth)
	route.Server = truncateDisplay(safeToolIdentifier(route.Server), maxCapabilityServerWidth)
	route.Tool = truncateDisplay(safeToolIdentifier(route.Tool), maxCapabilityToolWidth)
	return route
}

func capabilityRouteDetail(route agent.CapabilityRoute) string {
	route = sanitizeCapabilityRoute(route)
	server := ""
	if ecosystemIdentity(route.Server) != "" {
		server = describeEcosystemServer(route.Server).label
	}
	if strings.TrimSpace(server) == "" {
		server = humanizeToolIdentifier(route.Server)
	}
	action := friendlyRemoteAction(strings.TrimPrefix(route.Tool, route.Server+"_"))
	return route.Phase + " → " + server + " · " + action
}

func goalCapabilityPhase(advice *goaladvisor.Advice) string {
	if advice == nil {
		return "implementation"
	}
	switch strings.ToLower(strings.TrimSpace(advice.Phase)) {
	case "orienting", "investigating":
		return "research"
	case "planned":
		return "planning"
	case "changing":
		return "implementation"
	case "verifying":
		return "verification"
	case "persisting":
		return "handoff"
	case "needs_human_decision", "blocked":
		return "decision"
	default:
		return "implementation"
	}
}

// goalCapabilityActivityDiscriminator is local-only cache metadata. Cortex
// prose, arguments, and tool output never enter the resolver query; a bounded
// typed action identifier merely tells the host that work changed materially
// within the same phase.
func goalCapabilityActivityDiscriminator(advice *goaladvisor.Advice) string {
	if advice == nil || len(advice.Actions) == 0 {
		return ""
	}
	tool := strings.TrimSpace(advice.Actions[0].Tool)
	if tool == "" {
		return "action"
	}
	if len(tool) > 256 {
		tool = tool[:256]
	}
	return tool
}
