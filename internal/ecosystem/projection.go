package ecosystem

import (
	"encoding/json"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxProjectionIdentifierBytes = 96

// ToolRole is the stable product role a companion tool plays. It deliberately
// describes user intent rather than transport topology: MCPHub may route a
// Cortex call, but Cortex still owns coordination and evidence.
type ToolRole string

const (
	RoleGeneral       ToolRole = "general"
	RoleDiscovery     ToolRole = "discovery"
	RoleStructure     ToolRole = "structure"
	RoleCoordination  ToolRole = "coordination"
	RoleVerification  ToolRole = "verification"
	RoleSecurity      ToolRole = "security"
	RoleArtifact      ToolRole = "artifact"
	RoleObservability ToolRole = "observability"
	RoleBuild         ToolRole = "build"
	RoleGateway       ToolRole = "gateway"
)

// TransportState answers whether the invocation itself reached a terminal
// result. It must never be used as proof that the domain operation succeeded.
type TransportState string

const (
	TransportRunning   TransportState = "running"
	TransportSucceeded TransportState = "succeeded"
	TransportFailed    TransportState = "failed"
)

// DomainState is the interpreted outcome reported by a stable companion-tool
// contract. Unknown is intentionally distinct from success: a verifier whose
// envelope cannot be interpreted must not produce a green receipt.
type DomainState string

const (
	DomainPending   DomainState = "pending"
	DomainUnknown   DomainState = "unknown"
	DomainSucceeded DomainState = "succeeded"
	DomainAttention DomainState = "attention"
	DomainFailed    DomainState = "failed"
	DomainBlocked   DomainState = "blocked"
	DomainConflict  DomainState = "conflict"
	DomainDrift     DomainState = "drift"
)

// EvidenceState is the shared evidence grammar used by the transcript. A
// transport success alone never advances evidence to verified.
type EvidenceState string

const (
	EvidenceNone         EvidenceState = ""
	EvidenceCandidate    EvidenceState = "candidate"
	EvidenceSupported    EvidenceState = "supported"
	EvidenceVerified     EvidenceState = "verified"
	EvidenceContradicted EvidenceState = "contradicted"
	EvidenceStale        EvidenceState = "stale"
)

// ToolRoute is a bounded, non-secret routing receipt. Raw arguments and tool
// results do not belong here. Lazy marks a downstream tool reached through a
// gateway catalog instead of a tool pinned directly into the model prompt.
type ToolRoute struct {
	Gateway string `json:"gateway,omitempty"`
	Server  string `json:"server,omitempty"`
	Tool    string `json:"tool,omitempty"`
	CallID  string `json:"call_id,omitempty"`
	Lazy    bool   `json:"lazy,omitempty"`
}

// ToolProjection is the semantic, persistable projection of one tool call.
// It contains no arbitrary argument or result values and is safe to keep in a
// session transcript after Normalize.
type ToolProjection struct {
	Specialist string         `json:"specialist,omitempty"`
	Operation  string         `json:"operation,omitempty"`
	Role       ToolRole       `json:"role,omitempty"`
	Transport  TransportState `json:"transport,omitempty"`
	Domain     DomainState    `json:"domain,omitempty"`
	Evidence   EvidenceState  `json:"evidence,omitempty"`
	Route      ToolRoute      `json:"route,omitempty"`
}

// RawReceipt is the short-lived parser boundary between an MCP/tool transport
// and the semantic projection. Structured and ErrorMeta must be discarded
// after projection; they may contain large or sensitive downstream fields.
type RawReceipt struct {
	Text           string
	Structured     json.RawMessage
	ErrorMeta      json.RawMessage
	TransportError bool
	ToolError      bool
	// TrustedLocal is true only for Local Agent's own built-in and memory
	// tools. MCP transport success is never sufficient to set domain success.
	TrustedLocal bool
}

var specialistRoles = map[string]ToolRole{
	"mcphub":     RoleGateway,
	"cortex":     RoleCoordination,
	"bob":        RoleBuild,
	"monitor":    RoleObservability,
	"vecgrep":    RoleDiscovery,
	"veclite":    RoleDiscovery,
	"codemap":    RoleStructure,
	"glyphrun":   RoleVerification,
	"glyph":      RoleVerification,
	"cairntrace": RoleVerification,
	"cairn":      RoleVerification,
	"vidtrace":   RoleArtifact,
	"tinyvault":  RoleSecurity,
	"tvault":     RoleSecurity,
	"filecheap":  RoleArtifact,
	"fcheap":     RoleArtifact,
}

// ProjectToolCall creates a bounded semantic projection without retaining raw
// tool arguments. Only MCP routing identifiers are allowlisted from args.
func ProjectToolCall(name string, args map[string]any) ToolProjection {
	key := canonicalIdentifier(name)
	segments := splitToolName(key)
	operation := ""
	if len(segments) > 0 {
		operation = segments[len(segments)-1]
	}

	projection := ToolProjection{
		Operation: operation,
		Transport: TransportRunning,
		Domain:    DomainPending,
	}

	if operation == "mcphub_call_tool" {
		projection.Route.Gateway = "mcphub"
		projection.Route.Lazy = true
		server := argumentIdentifier(args, "server")
		tool := argumentIdentifier(args, "tool")
		if server == "" {
			if before, after, ok := strings.Cut(tool, "__"); ok {
				server, tool = before, after
			}
		} else {
			tool = strings.TrimPrefix(tool, server+"__")
		}
		projection.Route.Server = canonicalIdentifier(server)
		projection.Route.Tool = canonicalIdentifier(tool)
		if projection.Route.Tool != "" {
			projection.Operation = projection.Route.Tool
		}
	} else {
		projectNamedRoute(&projection, segments)
	}

	if projection.Operation == "mcphub_get_result" {
		projection.Route.CallID = argumentIdentifier(args, "callId", "call_id")
	}

	projection.Specialist = inferSpecialist(projection.Route.Server, projection.Operation, segments)
	projection.Role = specialistRoles[projection.Specialist]
	if projection.Role == "" {
		projection.Role = RoleGeneral
	}
	if projection.Route.Server == "" && projection.Specialist != "" && projection.Specialist != "mcphub" {
		projection.Route.Server = projection.Specialist
	}
	if projection.Route.Tool == "" {
		projection.Route.Tool = projection.Operation
	}
	return projection.Normalize()
}

func projectNamedRoute(projection *ToolProjection, segments []string) {
	if len(segments) == 0 {
		return
	}
	for index, segment := range segments {
		if segment != "mcphub" {
			continue
		}
		projection.Route.Gateway = "mcphub"
		projection.Route.Lazy = len(segments) > index+2
		if len(segments) > index+2 {
			projection.Route.Server = segments[index+1]
		}
		return
	}
	if len(segments) > 1 {
		projection.Route.Server = segments[len(segments)-2]
	}
}

func inferSpecialist(server, operation string, segments []string) string {
	for _, candidate := range append([]string{server, operation}, segments...) {
		if identity := specialistIdentity(candidate); identity != "" {
			return identity
		}
	}
	return ""
}

func specialistIdentity(value string) string {
	value = canonicalIdentifier(value)
	if _, ok := specialistRoles[value]; ok {
		return value
	}
	for identity := range specialistRoles {
		if strings.HasPrefix(value, identity+"_") {
			return identity
		}
	}
	return ""
}

// ProjectToolResult applies transport state and authoritative parsers. Known
// verification/build tools fail closed to DomainUnknown when their stable
// envelope is absent, preventing a successful MCP exchange from masquerading
// as verified work.
func ProjectToolResult(projection ToolProjection, result string, isError bool) ToolProjection {
	return ProjectReceipt(projection, RawReceipt{Text: result, TransportError: isError, ToolError: isError})
}

// ProjectReceipt interprets one short-lived raw receipt using exact tool and
// schema contracts. Transport and tool/domain errors remain separate.
func ProjectReceipt(projection ToolProjection, receipt RawReceipt) ToolProjection {
	projection = projection.Normalize()
	if receipt.TransportError {
		projection.Transport = TransportFailed
		projection.Domain = DomainFailed
		projection.Evidence = EvidenceNone
		return projection
	}

	projection.Transport = TransportSucceeded
	if projected, recognized := projectMCPHubReceipt(projection, receipt); recognized {
		return finalizeToolReceipt(projected, receipt.ToolError)
	}
	if _, hasTypedError := exactJSONDocument(receipt.ErrorMeta); hasTypedError {
		projection.Domain = DomainFailed
		projection.Evidence = EvidenceNone
		return projection.Normalize()
	}
	switch projection.Specialist {
	case "bob":
		if domain, ok := projectBobReceipt(projection.Operation, receipt); ok {
			projection.Domain = domain
		} else if envelope, ok := ParseBobEnvelope(receipt.Text); ok {
			projection.Domain = classifyBobDomain(envelope)
		} else {
			projection.Domain = DomainUnknown
		}
	case "glyphrun", "glyph", "cairntrace", "cairn":
		if domain, evidence, ok := projectVerifierReceipt(projection.Specialist, projection.Operation, receipt); ok {
			projection.Domain, projection.Evidence = domain, evidence
		} else {
			projection.Domain = DomainUnknown
			projection.Evidence = EvidenceNone
		}
	case "codemap":
		if domain, evidence, ok := projectCodemapReceipt(projection.Operation, receipt); ok {
			projection.Domain, projection.Evidence = domain, evidence
		} else {
			projection.Domain, projection.Evidence = DomainUnknown, EvidenceNone
		}
	case "monitor":
		if domain, evidence, ok := projectMonitorReceipt(projection.Operation, receipt); ok {
			projection.Domain, projection.Evidence = domain, evidence
		} else {
			projection.Domain, projection.Evidence = DomainUnknown, EvidenceNone
		}
	case "vidtrace":
		if domain, evidence, ok := projectVidtraceReceipt(projection.Operation, receipt); ok {
			projection.Domain, projection.Evidence = domain, evidence
		} else {
			projection.Domain, projection.Evidence = DomainUnknown, EvidenceNone
		}
	case "filecheap", "fcheap":
		if domain, evidence, ok := projectFileCheapReceipt(projection.Operation, receipt); ok {
			projection.Domain, projection.Evidence = domain, evidence
		} else {
			projection.Domain, projection.Evidence = DomainUnknown, EvidenceNone
		}
	case "cortex":
		// Cortex owns coordination, but a successful MCP exchange is not proof
		// that a task started, completed, or verified. Add exact per-operation
		// parsers before promoting these receipts.
		projection.Domain = DomainUnknown
		projection.Evidence = EvidenceNone
	default:
		if receipt.ToolError || len(receipt.ErrorMeta) > 0 {
			projection.Domain = DomainFailed
		} else if receipt.TrustedLocal {
			projection.Domain = DomainSucceeded
		} else {
			projection.Domain = DomainUnknown
			projection.Evidence = EvidenceNone
		}
	}

	if projection.Domain == DomainSucceeded {
		switch projection.Role {
		case RoleDiscovery:
			projection.Evidence = EvidenceCandidate
		case RoleStructure:
			projection.Evidence = EvidenceSupported
		}
	}
	return finalizeToolReceipt(projection, receipt.ToolError)
}

func finalizeToolReceipt(projection ToolProjection, toolError bool) ToolProjection {
	// MCP IsError is an application-level failure signal. Exact structured
	// detail may refine it to conflict/attention/contradicted, but it can never
	// be overridden by an optimistic envelope into success or verification.
	if toolError {
		if projection.Domain == DomainSucceeded || projection.Domain == DomainPending || projection.Domain == DomainUnknown || projection.Domain == "" {
			projection.Domain = DomainFailed
		}
		if projection.Evidence == EvidenceVerified {
			projection.Evidence = EvidenceNone
		}
	}
	return projection.Normalize()
}

// SafeReceiptText is the only model/UI fallback for structured-only tool
// results. Every value is a bounded allowlisted projection identifier; raw
// arguments, structured fields, and free-form server prose are excluded.
func SafeReceiptText(projection ToolProjection) string {
	projection = projection.Normalize()
	parts := []string{"tool receipt"}
	appendField := func(key, value string) {
		if value != "" {
			parts = append(parts, key+"="+value)
		}
	}
	appendField("specialist", projection.Specialist)
	appendField("operation", projection.Operation)
	appendField("transport", string(projection.Transport))
	appendField("domain", string(projection.Domain))
	appendField("evidence", string(projection.Evidence))
	appendField("call_id", projection.Route.CallID)
	return strings.Join(parts, " ")
}

func classifyBobDomain(envelope BobEnvelope) DomainState {
	if len(envelope.Conflicts()) > 0 {
		return DomainConflict
	}
	if info, ok := envelope.ErrorInfo(); ok {
		switch info.Code {
		case "conflicts":
			return DomainConflict
		case "missing_manifest", "manifest_invalid", "input_invalid", "workspace_invalid":
			return DomainBlocked
		default:
			return DomainFailed
		}
	}
	if clean, present := envelope.CleanFlag(); present && !clean {
		return DomainDrift
	}
	for _, action := range envelope.Actions() {
		if action.IsConflict() {
			return DomainConflict
		}
		if action.Code != "" && action.Code != "in_sync" && action.Code != "identical_content" {
			return DomainDrift
		}
	}
	if !envelope.OK {
		return DomainFailed
	}
	return DomainSucceeded
}

// Successful reports a semantically successful terminal outcome.
func (p ToolProjection) Successful() bool {
	return p.Transport == TransportSucceeded && p.Domain == DomainSucceeded
}

// NeedsAttention reports a terminal outcome that should not be painted as a
// successful green receipt even though the transport may have succeeded.
func (p ToolProjection) NeedsAttention() bool {
	switch p.Domain {
	case DomainUnknown, DomainAttention, DomainBlocked, DomainConflict, DomainDrift:
		return true
	default:
		return false
	}
}

// Normalize bounds all persisted identifiers and drops unknown enum values.
func (p ToolProjection) Normalize() ToolProjection {
	p.Specialist = canonicalIdentifier(p.Specialist)
	p.Operation = canonicalIdentifier(p.Operation)
	p.Route.Gateway = canonicalIdentifier(p.Route.Gateway)
	p.Route.Server = canonicalIdentifier(p.Route.Server)
	p.Route.Tool = canonicalIdentifier(p.Route.Tool)
	p.Route.CallID = canonicalIdentifier(p.Route.CallID)
	if !validRole(p.Role) {
		p.Role = RoleGeneral
	}
	if !validTransport(p.Transport) {
		p.Transport = TransportSucceeded
		p.Domain = DomainUnknown
	}
	if !validDomain(p.Domain) {
		p.Domain = DomainUnknown
	}
	if !validEvidence(p.Evidence) {
		p.Evidence = EvidenceNone
		if p.Transport != "" {
			p.Domain = DomainUnknown
		}
	}
	return p
}

func splitToolName(name string) []string {
	parts := strings.Split(name, "__")
	result := parts[:0]
	for _, part := range parts {
		if part = canonicalIdentifier(part); part != "" {
			result = append(result, part)
		}
	}
	return result
}

func argumentIdentifier(args map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := args[key].(string); ok {
			if value = canonicalIdentifier(value); value != "" {
				return value
			}
		}
	}
	return ""
}

func canonicalIdentifier(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	for _, r := range value {
		out, keep := r, true
		switch {
		case unicode.IsLetter(r), unicode.IsNumber(r):
		case strings.ContainsRune("_-.:/", r):
		case unicode.IsSpace(r):
			out = '_'
		default:
			keep = false
		}
		if !keep {
			continue
		}
		runeBytes := utf8.RuneLen(out)
		if runeBytes < 0 || builder.Len()+runeBytes > maxProjectionIdentifierBytes {
			break
		}
		builder.WriteRune(out)
	}
	return strings.Trim(builder.String(), "_-.:/")
}

func validRole(value ToolRole) bool {
	switch value {
	case RoleGeneral, RoleDiscovery, RoleStructure, RoleCoordination, RoleVerification, RoleSecurity, RoleArtifact, RoleObservability, RoleBuild, RoleGateway:
		return true
	default:
		return false
	}
}

func validTransport(value TransportState) bool {
	switch value {
	case "", TransportRunning, TransportSucceeded, TransportFailed:
		return true
	default:
		return false
	}
}

func validDomain(value DomainState) bool {
	switch value {
	case "", DomainPending, DomainUnknown, DomainSucceeded, DomainAttention, DomainFailed, DomainBlocked, DomainConflict, DomainDrift:
		return true
	default:
		return false
	}
}

func validEvidence(value EvidenceState) bool {
	switch value {
	case EvidenceNone, EvidenceCandidate, EvidenceSupported, EvidenceVerified, EvidenceContradicted, EvidenceStale:
		return true
	default:
		return false
	}
}
