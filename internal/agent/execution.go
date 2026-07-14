package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"

	executionpkg "github.com/abdul-hamid-achik/local-agent/internal/execution"
	"github.com/abdul-hamid-achik/local-agent/internal/llm"
	permissionPkg "github.com/abdul-hamid-achik/local-agent/internal/permission"
)

const executionLedgerTimeout = 5 * time.Second

const (
	// DurableRecoveryContextPrefix identifies the closed, host-authored model
	// context derived from a validated execution reconciliation. Content with
	// this prefix is not trusted unless its in-memory Message is also HostOwned.
	DurableRecoveryContextPrefix = "Local Agent durable recovery receipt"
	// MaxDurableRecoveryContextMessageBytes bounds each receipt independently.
	MaxDurableRecoveryContextMessageBytes = 2 * 1024
	// MaxDurableRecoveryContextMessages and the aggregate bound prevent a long
	// reconciliation history from taking over the provider context window.
	MaxDurableRecoveryContextMessages       = 100
	MaxDurableRecoveryContextAggregateBytes = 64 * 1024
)

var (
	ErrExecutionLedgerRequired            = errors.New("execution ledger is required")
	ErrExecutionRecoveryRecheckDuringTurn = errors.New("execution recovery cannot be rechecked while an agent turn is running")
)

// ExecutionLedger is the durable lifecycle surface required by Agent. Keeping
// it independent of db.Store lets embedded callers and tests provide their own
// implementation without coupling the agent runtime to SQLite.
type ExecutionLedger interface {
	AppendExecutionEvent(context.Context, executionpkg.Event) (executionpkg.Event, bool, error)
	ListExecutionRecoveryHazards(context.Context, int64, string, int64, int) ([]executionpkg.State, error)
}

// UnresolvedExecutionError means an execution cannot be continued safely: it
// either lacks a durable terminal receipt after dispatch intent or has a
// durable outcome_unknown receipt that requires explicit reconciliation. The
// active session is latched until the session scope changes or a host that has
// committed durable reconciliation explicitly requests a recovery recheck.
type UnresolvedExecutionError struct {
	SessionID      int64
	WorkspaceID    string
	SnapshotCursor int64
	TurnID         string
	ExecutionID    string
	ToolName       string
	EventType      executionpkg.EventType
	Cause          error
}

func (e *UnresolvedExecutionError) Error() string {
	if e == nil {
		return "unresolved execution"
	}
	identity := e.ExecutionID
	if identity == "" {
		identity = "unknown"
	}
	message := fmt.Sprintf("execution %s for tool %q is unresolved", identity, e.ToolName)
	if e.Cause != nil {
		message += ": " + e.Cause.Error()
	}
	return message
}

func (e *UnresolvedExecutionError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// RecoveryInspectCommand returns the read-only CLI inspection command for an
// ordinary execution-effect hazard. Completed post-snapshot effects require a
// different projection repair and therefore intentionally return no command.
func (e *UnresolvedExecutionError) RecoveryInspectCommand() string {
	if e == nil || e.SessionID <= 0 || strings.TrimSpace(e.ExecutionID) == "" {
		return ""
	}
	if e.EventType != executionpkg.EventOutcomeUnknown && e.EventType != executionpkg.EventStarted {
		return ""
	}
	return fmt.Sprintf("local-agent execution recover %d %s", e.SessionID, shellQuoteRecoveryArgument(e.ExecutionID))
}

func shellQuoteRecoveryArgument(value string) string {
	if value != "" && !strings.ContainsAny(value, " \t\r\n'\"`$\\;&|<>()[]{}*?!") {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

// SetExecutionLedger installs the append-only lifecycle store. A nil ledger is
// allowed only while strict mode is disabled.
func (a *Agent) SetExecutionLedger(ledger ExecutionLedger) {
	a.mu.Lock()
	a.executionLedger = ledger
	a.mu.Unlock()
}

// SetExecutionSessionID sets the durable session scope. Changing sessions
// clears an unresolved-execution latch because the old hazard belongs to a
// different durable scope.
func (a *Agent) SetExecutionSessionID(sessionID int64) {
	a.mu.Lock()
	if a.executionSessionID != sessionID {
		a.unresolvedExecution = nil
	}
	a.executionSessionID = sessionID
	a.mu.Unlock()
}

// SetExecutionSnapshotCursor identifies the last execution-event row already
// represented by the durable session snapshot. Advancing it is an explicit
// projection/reconciliation boundary and clears the in-memory hazard latch so
// strict Run can re-check the ledger after the new cursor.
func (a *Agent) SetExecutionSnapshotCursor(cursor int64) {
	if cursor < 0 {
		cursor = 0
	}
	a.mu.Lock()
	if a.executionCursor != cursor {
		a.unresolvedExecution = nil
	}
	a.executionCursor = cursor
	a.mu.Unlock()
}

// RecheckExecutionRecovery clears only the in-memory unresolved-execution
// cache. It deliberately keeps the durable session scope and snapshot cursor
// unchanged: the next explicitly requested Run must query the execution ledger
// again before it can perform provider work. Recovery controllers call this
// only after their durable reconciliation transaction commits.
//
// Rechecking during a turn is rejected so a controller cannot invalidate the
// cache between that turn's recovery check and provider dispatch.
func (a *Agent) RecheckExecutionRecovery() error {
	a.turnMu.Lock()
	defer a.turnMu.Unlock()
	if a.turnRunning.Load() {
		return ErrExecutionRecoveryRecheckDuringTurn
	}
	a.mu.Lock()
	a.unresolvedExecution = nil
	a.mu.Unlock()
	return nil
}

// RequireExecutionLedger makes Run fail before provider work unless a ledger
// and a positive session/workspace scope are available.
func (a *Agent) RequireExecutionLedger(required bool) {
	a.mu.Lock()
	a.requireExecutionLog = required
	a.mu.Unlock()
}

type executionRuntime struct {
	ledger         ExecutionLedger
	sessionID      int64
	workspaceID    string
	runID          string
	snapshotCursor int64
	required       bool
}

type trackedToolExecution struct {
	identity      executionpkg.Identity
	originalHash  string
	effectiveHash string
}

func (t trackedToolExecution) argumentsHash() string {
	if t.effectiveHash != "" {
		return t.effectiveHash
	}
	return t.originalHash
}

func (a *Agent) executionRuntime(ctx context.Context) (executionRuntime, error) {
	a.mu.RLock()
	runtime := executionRuntime{
		ledger:         a.executionLedger,
		sessionID:      a.executionSessionID,
		runID:          a.executionRunID,
		snapshotCursor: a.executionCursor,
		required:       a.requireExecutionLog,
	}
	runIDErr := a.executionRunIDErr
	latched := a.unresolvedExecution
	a.mu.RUnlock()

	if latched != nil {
		return runtime, latched
	}
	if runtime.ledger == nil {
		if runtime.required {
			return runtime, fmt.Errorf("%w: no ledger configured", ErrExecutionLedgerRequired)
		}
		return runtime, nil
	}
	if runIDErr != nil {
		return runtime, fmt.Errorf("initialize execution run identity: %w", runIDErr)
	}
	if runtime.sessionID <= 0 {
		return runtime, fmt.Errorf("%w: positive execution session id is required", ErrExecutionLedgerRequired)
	}
	workspaceID, err := a.checkpointWorkspaceID()
	if err != nil || strings.TrimSpace(workspaceID) == "" {
		if err == nil {
			err = errors.New("workspace identity is empty")
		}
		return runtime, fmt.Errorf("%w: resolve execution workspace: %v", ErrExecutionLedgerRequired, err)
	}
	runtime.workspaceID = workspaceID

	if !runtime.required {
		return runtime, nil
	}
	states, err := runtime.ledger.ListExecutionRecoveryHazards(ctx, runtime.sessionID, runtime.workspaceID, runtime.snapshotCursor, 100)
	if err != nil {
		return runtime, fmt.Errorf("inspect execution recovery hazards after snapshot cursor %d: %w", runtime.snapshotCursor, err)
	}
	for _, state := range states {
		unknownOutcome := state.Latest.Type == executionpkg.EventOutcomeUnknown
		missingTerminal := state.Latest.Type == executionpkg.EventStarted && state.Identity.EffectClass != executionpkg.EffectReadOnly
		completedAfterSnapshot := state.Latest.Type == executionpkg.EventCompleted && state.Identity.EffectClass != executionpkg.EffectReadOnly
		if !unknownOutcome && !missingTerminal && !completedAfterSnapshot {
			continue
		}
		reason := "durable state is started without a terminal receipt"
		if unknownOutcome {
			reason = "durable outcome is unknown and requires explicit reconciliation"
		} else if completedAfterSnapshot {
			reason = "completed effect is newer than the session snapshot and must be projected before provider work"
		}
		unresolved := &UnresolvedExecutionError{
			SessionID:      state.Identity.SessionID,
			WorkspaceID:    state.Identity.WorkspaceID,
			SnapshotCursor: runtime.snapshotCursor,
			TurnID:         state.Identity.TurnID,
			ExecutionID:    state.Identity.ExecutionID,
			ToolName:       state.Identity.ToolName,
			EventType:      state.Latest.Type,
			Cause:          errors.New(reason),
		}
		a.latchUnresolvedExecution(unresolved)
		return runtime, unresolved
	}
	return runtime, nil
}

func (a *Agent) latchUnresolvedExecution(err *UnresolvedExecutionError) {
	if err == nil {
		return
	}
	a.mu.Lock()
	if a.unresolvedExecution == nil {
		a.unresolvedExecution = err
	}
	a.mu.Unlock()
}

func appendExecutionEvent(ctx context.Context, runtime executionRuntime, event executionpkg.Event) error {
	if runtime.ledger == nil {
		return nil
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now().UTC()
	}
	if err := event.Validate(); err != nil {
		return fmt.Errorf("validate %s execution event: %w", event.Type, err)
	}
	durableCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), executionLedgerTimeout)
	defer cancel()
	if _, _, err := runtime.ledger.AppendExecutionEvent(durableCtx, event); err != nil {
		return fmt.Errorf("append %s execution event: %w", event.Type, err)
	}
	return nil
}

func (a *Agent) appendTerminalExecutionEvent(ctx context.Context, runtime executionRuntime, tracked trackedToolExecution, event executionpkg.Event) error {
	if err := appendExecutionEvent(ctx, runtime, event); err != nil {
		unresolved := a.unresolvedFor(tracked, event.Type, err)
		a.latchUnresolvedExecution(unresolved)
		return unresolved
	}
	return nil
}

func executionEvent(tracked trackedToolExecution, eventType executionpkg.EventType, approval executionpkg.Approval, result, detail string) executionpkg.Event {
	event := executionpkg.Event{
		Identity:        tracked.identity,
		Type:            eventType,
		Approval:        approval,
		ArgumentsSHA256: tracked.argumentsHash(),
		Detail:          executionpkg.BoundDetail(detail),
	}
	if result != "" {
		event.ResultSHA256 = executionpkg.HashText(result)
		event.ResultReceipt = executionpkg.BoundResultReceipt(result)
	}
	return event
}

func (a *Agent) newTrackedExecutions(ctx context.Context, runtime executionRuntime, turnID string, iteration int, calls []llm.ToolCall, providerIDs []string) ([]trackedToolExecution, error) {
	tracked := make([]trackedToolExecution, len(calls))
	for i := range calls {
		executionID, err := executionpkg.NewExecutionID()
		if err != nil {
			return nil, err
		}
		idempotencyKey, err := executionpkg.NewIdempotencyKey()
		if err != nil {
			return nil, err
		}
		argumentsHash, err := executionpkg.HashCanonicalArguments(calls[i].Arguments)
		if err != nil {
			return nil, err
		}
		kind, effectClass := a.executionKindForCall(calls[i])
		providerID := ""
		if i < len(providerIDs) {
			providerID = providerIDs[i]
		}
		tracked[i] = trackedToolExecution{
			identity: executionpkg.Identity{
				SessionID:       runtime.sessionID,
				WorkspaceID:     runtime.workspaceID,
				RunID:           runtime.runID,
				TurnID:          turnID,
				ExecutionID:     executionID,
				IdempotencyKey:  idempotencyKey,
				ProviderCallID:  providerID,
				CanonicalCallID: calls[i].ID,
				ToolName:        calls[i].Name,
				Iteration:       iteration,
				Ordinal:         i + 1,
				Kind:            kind,
				EffectClass:     effectClass,
			},
			originalHash: argumentsHash,
		}
		if err := appendExecutionEvent(ctx, runtime, executionEvent(tracked[i], executionpkg.EventRequested, executionpkg.ApprovalNotApplicable, "", "model requested tool execution")); err != nil {
			return nil, err
		}
	}
	return tracked, nil
}

func (a *Agent) executionKind(name string) (executionpkg.Kind, executionpkg.EffectClass) {
	return a.executionKindForCall(llm.ToolCall{Name: name})
}

func (a *Agent) executionKindForCall(call llm.ToolCall) (executionpkg.Kind, executionpkg.EffectClass) {
	name := call.Name
	if a.isMemoryTool(name) {
		if name == "memory_list" {
			return executionpkg.KindMemory, executionpkg.EffectReadOnly
		}
		// memory_recall rewrites LastUsed metadata, so it is not a pure read.
		return executionpkg.KindMemory, executionpkg.Effectful
	}
	if a.isToolsTool(name) {
		switch name {
		case "grep", "read", "glob", "ls", "find", "diff", "exists", "load_skill":
			return executionpkg.KindBuiltin, executionpkg.EffectReadOnly
		case "bash":
			return executionpkg.KindBuiltin, executionpkg.EffectUnknown
		default:
			return executionpkg.KindBuiltin, executionpkg.Effectful
		}
	}
	// The exact contract catalog is host-owned and argument-aware. MCP
	// ToolAnnotations remain server-supplied presentation hints and are never
	// consulted here.
	if contract, ok := a.trustedMCPContract(call); ok {
		return executionpkg.KindMCP, contract.effect
	}
	return executionpkg.KindMCP, executionpkg.EffectUnknown
}

// mcpToolRequiresApproval is deliberately independent of server metadata.
// Only a future explicit host-owned trust policy may narrow this invariant.
func mcpToolRequiresApproval() bool { return true }

func terminalExecutionEventType(effect executionpkg.EffectClass, isError bool, contextErr error) executionpkg.EventType {
	if !isError {
		return executionpkg.EventCompleted
	}
	if effect != executionpkg.EffectReadOnly {
		return executionpkg.EventOutcomeUnknown
	}
	if contextErr != nil {
		return executionpkg.EventCancelled
	}
	return executionpkg.EventFailed
}

func (a *Agent) mcpToolDefinition(name string) (llm.ToolDef, bool) {
	for _, def := range a.mcpTools() {
		if def.Name == name {
			return def, true
		}
	}
	return llm.ToolDef{}, false
}

// preflightMCPToolArguments validates a call against a detached copy of the
// exact schema advertised by the MCP server. Resolving without a Loader is
// intentional: preflight must stay pure and must never fetch external schemas.
func preflightMCPToolArguments(def llm.ToolDef, args map[string]any) error {
	schemaJSON, err := json.Marshal(def.Parameters)
	if err != nil {
		return errors.New("MCP tool exposes an invalid input schema")
	}

	var schema jsonschema.Schema
	if err := json.Unmarshal(schemaJSON, &schema); err != nil {
		return errors.New("MCP tool exposes an invalid input schema")
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		return errors.New("MCP tool exposes an invalid or unsupported input schema")
	}
	if err := resolved.Validate(args); err != nil {
		return mcpArgumentSchemaMismatch(&schema, args)
	}
	return nil
}

const mcpArgumentSchemaMismatchMessage = "arguments do not satisfy the MCP tool input schema"

// mcpArgumentSchemaMismatch must not wrap the validator error: jsonschema-go
// includes rejected instance values in many diagnostics. Those values are MCP
// arguments and must stay out of durable receipts, UI output, and tool messages.
// Root required names are safe schema metadata and make the common correction
// actionable; all other failures deliberately use the generic host text.
func mcpArgumentSchemaMismatch(schema *jsonschema.Schema, args map[string]any) error {
	missing := make([]string, 0, len(schema.Required))
	for _, name := range schema.Required {
		if _, exists := args[name]; exists {
			continue
		}
		if !safeMCPRequiredPropertyName(name) || len(missing) >= 8 {
			return errors.New(mcpArgumentSchemaMismatchMessage)
		}
		missing = append(missing, name)
	}
	if len(missing) == 0 {
		return errors.New(mcpArgumentSchemaMismatchMessage)
	}
	sort.Strings(missing)
	for index := range missing {
		missing[index] = strconv.Quote(missing[index])
	}
	return fmt.Errorf("%s; missing required properties: %s", mcpArgumentSchemaMismatchMessage, strings.Join(missing, ", "))
}

func safeMCPRequiredPropertyName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	for _, char := range name {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' {
			continue
		}
		switch char {
		case '_', '-', '.', '$':
		default:
			return false
		}
	}
	return true
}

func preflightRequiredString(args map[string]any, key string, allowEmpty bool) error {
	value, ok := args[key]
	if !ok {
		return fmt.Errorf("%s is required", key)
	}
	text, ok := value.(string)
	if !ok {
		return fmt.Errorf("%s must be a string", key)
	}
	if !allowEmpty && strings.TrimSpace(text) == "" {
		return fmt.Errorf("%s is required", key)
	}
	return nil
}

func preflightNumericID(args map[string]any) error {
	value, ok := args["id"]
	if !ok {
		return errors.New("id is required")
	}
	switch number := value.(type) {
	case int:
		return nil
	case float64:
		if math.IsNaN(number) || math.IsInf(number, 0) || math.Trunc(number) != number {
			return errors.New("id must be an integer")
		}
		return nil
	default:
		return errors.New("id must be a number")
	}
}

// preflightToolCall is deliberately pure: it validates request shape and MCP
// routing without touching the filesystem, memory store, subprocesses, or a
// remote transport.
func (a *Agent) preflightToolCall(kind executionpkg.Kind, tc llm.ToolCall) error {
	switch kind {
	case executionpkg.KindBuiltin:
		switch tc.Name {
		case "grep":
			return preflightRequiredString(tc.Arguments, "pattern", false)
		case "read", "mkdir", "remove", "exists":
			return preflightRequiredString(tc.Arguments, "path", false)
		case "write":
			if err := preflightRequiredString(tc.Arguments, "path", false); err != nil {
				return err
			}
			return preflightRequiredString(tc.Arguments, "content", true)
		case "glob":
			return preflightRequiredString(tc.Arguments, "pattern", false)
		case "bash":
			return preflightRequiredString(tc.Arguments, "command", false)
		case "ls":
			return nil
		case "find":
			return preflightRequiredString(tc.Arguments, "name", false)
		case "load_skill":
			if !a.hasSkillLoader() {
				return errors.New("skill loading is unavailable")
			}
			if err := preflightRequiredString(tc.Arguments, "name", false); err != nil {
				return err
			}
			name := tc.Arguments["name"].(string)
			if len(tc.Arguments) != 1 || !validModelSkillName(name) {
				return errors.New("name must be one exact catalog name")
			}
			return nil
		case "diff":
			if err := preflightRequiredString(tc.Arguments, "path", false); err != nil {
				return err
			}
			return preflightRequiredString(tc.Arguments, "new_content", true)
		case "edit":
			if err := preflightRequiredString(tc.Arguments, "path", false); err != nil {
				return err
			}
			return preflightRequiredString(tc.Arguments, "patch", false)
		case "copy", "move":
			if err := preflightRequiredString(tc.Arguments, "source", false); err != nil {
				return err
			}
			return preflightRequiredString(tc.Arguments, "destination", false)
		default:
			return fmt.Errorf("unknown built-in tool %q", tc.Name)
		}
	case executionpkg.KindMemory:
		if a.memoryStore == nil {
			return errors.New("memory store is unavailable")
		}
		switch tc.Name {
		case "memory_save":
			return preflightRequiredString(tc.Arguments, "content", false)
		case "memory_recall":
			return preflightRequiredString(tc.Arguments, "query", false)
		case "memory_delete":
			return preflightNumericID(tc.Arguments)
		case "memory_update":
			if err := preflightNumericID(tc.Arguments); err != nil {
				return err
			}
			content, _ := tc.Arguments["content"].(string)
			_, hasTags := tc.Arguments["tags"]
			if content == "" && !hasTags {
				return errors.New("at least one of content or tags is required")
			}
			return nil
		case "memory_list":
			return nil
		default:
			return fmt.Errorf("unknown memory tool %q", tc.Name)
		}
	case executionpkg.KindMCP:
		if a.registry == nil {
			return errors.New("MCP registry is unavailable")
		}
		resolved, ok := a.registry.ResolveToolName(tc.Name)
		if !ok || resolved != tc.Name {
			return fmt.Errorf("unknown MCP tool %q", tc.Name)
		}
		def, ok := a.mcpToolDefinition(tc.Name)
		if !ok {
			return fmt.Errorf("unknown MCP tool %q", tc.Name)
		}
		return preflightMCPToolArguments(def, tc.Arguments)
	default:
		return fmt.Errorf("unknown execution kind %q", kind)
	}
}

type toolAuthorization struct {
	allowed     bool
	cancelled   bool
	hostRefused bool
	approval    executionpkg.Approval
	decision    permissionPkg.ApprovalDecision
	reason      string
	refusalCode string
}

func (a *Agent) decideToolAuthorization(ctx context.Context, tc llm.ToolCall, beforeAsk func() error) (toolAuthorization, error) {
	if err := ctx.Err(); err != nil {
		return toolAuthorization{cancelled: true, reason: err.Error()}, nil
	}
	if a.permChecker == nil {
		return toolAuthorization{allowed: true, approval: executionpkg.ApprovalEmbedding}, nil
	}

	switch a.permChecker.ToCheckResult(tc.Name) {
	case permissionPkg.CheckAllow:
		if !a.permChecker.IsYolo() && a.approvalCallback == nil {
			return toolAuthorization{
				hostRefused: true,
				approval:    executionpkg.ApprovalHostRefused,
				decision:    permissionPkg.DecisionHostRefuse,
				refusalCode: "approval_ui_unavailable",
				reason:      "interactive approval is unavailable; persisted allows do not apply to headless execution",
			}, nil
		}
		approval := executionpkg.ApprovalPolicy
		if a.permChecker.IsYolo() {
			approval = executionpkg.ApprovalYolo
		}
		return toolAuthorization{allowed: true, approval: approval}, nil
	case permissionPkg.CheckDeny:
		return toolAuthorization{
			approval: executionpkg.ApprovalPolicyDenied,
			decision: permissionPkg.DecisionUserDeny,
			reason:   "tool call blocked by permission policy",
		}, nil
	case permissionPkg.CheckAsk:
		argumentsHash, err := executionpkg.HashCanonicalArguments(tc.Arguments)
		if err != nil {
			return toolAuthorization{
				hostRefused: true,
				approval:    executionpkg.ApprovalHostRefused,
				decision:    permissionPkg.DecisionHostRefuse,
				refusalCode: "approval_arguments_invalid",
				reason:      fmt.Sprintf("tool arguments cannot be bound for approval: %v", err),
			}, nil
		}
		request := a.newApprovalRequest(ctx, tc, argumentsHash)
		if a.hasSessionApproval(request) {
			return toolAuthorization{
				allowed:  true,
				approval: executionpkg.ApprovalSession,
				decision: permissionPkg.DecisionAllowSession,
			}, nil
		}
		if beforeAsk != nil {
			if err := beforeAsk(); err != nil {
				return toolAuthorization{}, err
			}
		}
		response := permissionPkg.ResolveApprovalContext(ctx, request, a.approvalCallback)
		if err := ctx.Err(); err != nil {
			return toolAuthorization{cancelled: true, approval: executionpkg.ApprovalCancelled, decision: permissionPkg.DecisionCancelled, reason: err.Error()}, nil
		}
		switch response.Decision {
		case permissionPkg.DecisionAllowOnce:
			return toolAuthorization{allowed: true, approval: executionpkg.ApprovalOnce, decision: response.Decision}, nil
		case permissionPkg.DecisionAllowSession:
			a.rememberSessionApproval(request)
			return toolAuthorization{allowed: true, approval: executionpkg.ApprovalSession, decision: response.Decision}, nil
		case permissionPkg.DecisionUserDeny:
			reason := response.Message
			if reason == "" {
				reason = "user denied tool execution"
			}
			return toolAuthorization{approval: executionpkg.ApprovalUserDenied, decision: response.Decision, reason: reason}, nil
		case permissionPkg.DecisionCancelled:
			reason := response.Message
			if reason == "" {
				reason = "approval was cancelled"
			}
			return toolAuthorization{cancelled: true, approval: executionpkg.ApprovalCancelled, decision: response.Decision, reason: reason}, nil
		case permissionPkg.DecisionHostRefuse:
			reason := response.Message
			if reason == "" {
				reason = "approval host refused the request"
			}
			return toolAuthorization{
				hostRefused: true,
				approval:    executionpkg.ApprovalHostRefused,
				decision:    response.Decision,
				refusalCode: response.Code,
				reason:      reason,
			}, nil
		default:
			return toolAuthorization{
				hostRefused: true,
				approval:    executionpkg.ApprovalHostRefused,
				decision:    permissionPkg.DecisionHostRefuse,
				refusalCode: "invalid_approval_decision",
				reason:      "approval host returned an invalid decision",
			}, nil
		}
	default:
		return toolAuthorization{
			hostRefused: true,
			approval:    executionpkg.ApprovalHostRefused,
			decision:    permissionPkg.DecisionHostRefuse,
			refusalCode: "unknown_permission_state",
			reason:      "unknown permission state",
		}, nil
	}
}

func (a *Agent) unresolvedFor(tracked trackedToolExecution, eventType executionpkg.EventType, cause error) *UnresolvedExecutionError {
	a.mu.RLock()
	cursor := a.executionCursor
	a.mu.RUnlock()
	return &UnresolvedExecutionError{
		SessionID:      tracked.identity.SessionID,
		WorkspaceID:    tracked.identity.WorkspaceID,
		SnapshotCursor: cursor,
		TurnID:         tracked.identity.TurnID,
		ExecutionID:    tracked.identity.ExecutionID,
		ToolName:       tracked.identity.ToolName,
		EventType:      eventType,
		Cause:          cause,
	}
}

func terminalLedgerFailureReceipt(name string, err error) string {
	return fmt.Sprintf("OUTCOME UNKNOWN: tool %q finished without a durable terminal receipt. Do not retry automatically; inspect state first. Ledger error: %v", name, err)
}

func (a *Agent) stopBeforeDispatchAfterLedgerError(calls []llm.ToolCall, out Output, err error) error {
	wrapped := fmt.Errorf("execution ledger failed before dispatch: %w", err)
	out.Error(wrapped.Error())
	a.cancelUndispatchedToolCalls(calls, out, wrapped)
	return wrapped
}

func (a *Agent) cancelTrackedToolCalls(ctx context.Context, runtime executionRuntime, tracked []trackedToolExecution, calls []llm.ToolCall, out Output, cause error) error {
	if cause == nil {
		cause = context.Canceled
	}
	limit := len(calls)
	if len(tracked) < limit {
		limit = len(tracked)
	}
	for i := 0; i < limit; i++ {
		result := fmt.Sprintf("CANCELLED — NOT DISPATCHED: tool %q did not start because the turn ended: %v", calls[i].Name, cause)
		event := executionEvent(tracked[i], executionpkg.EventCancelled, executionpkg.ApprovalNotApplicable, result, "turn ended before dispatch")
		if err := appendExecutionEvent(ctx, runtime, event); err != nil {
			unresolved := a.unresolvedFor(tracked[i], executionpkg.EventCancelled, err)
			a.latchUnresolvedExecution(unresolved)
			a.cancelUndispatchedToolCalls(calls[i:], out, unresolved)
			return unresolved
		}
		out.ToolCallStart(calls[i].ID, calls[i].Name, calls[i].Arguments)
		out.ToolCallResult(calls[i].ID, calls[i].Name, result, true, 0)
		a.AppendMessage(llm.Message{
			Role:       "tool",
			Content:    result,
			ToolName:   calls[i].Name,
			ToolCallID: calls[i].ID,
		})
	}
	if len(calls) > limit {
		a.cancelUndispatchedToolCalls(calls[limit:], out, cause)
	}
	return nil
}

func (a *Agent) cancelCommittedDispatchIntent(ctx context.Context, runtime executionRuntime, tracked trackedToolExecution, call llm.ToolCall, out Output, cause error, startEmitted bool, numCtx int) error {
	if cause == nil {
		cause = context.Canceled
	}
	eventType := executionpkg.EventCancelled
	result := fmt.Sprintf("CANCELLED: read-only tool %q did not enter its backend because the turn ended after dispatch intent: %v", call.Name, cause)
	if tracked.identity.EffectClass != executionpkg.EffectReadOnly {
		eventType = executionpkg.EventOutcomeUnknown
		result = fmt.Sprintf("OUTCOME UNKNOWN: tool %q crossed the durable dispatch-intent barrier before cancellation. This process did not invoke the backend; do not retry automatically without reconciliation: %v", call.Name, cause)
	}
	event := executionEvent(tracked, eventType, executionpkg.ApprovalNotApplicable, result, "cancellation observed after dispatch intent and before backend invocation")
	if err := a.appendTerminalExecutionEvent(ctx, runtime, tracked, event); err != nil {
		unknown := terminalLedgerFailureReceipt(call.Name, err)
		if startEmitted {
			out.ToolCallResult(call.ID, call.Name, unknown, true, 0)
			a.AppendMessage(llm.Message{Role: "tool", Content: unknown, ToolName: call.Name, ToolCallID: call.ID})
		} else {
			a.failedToolCall(call, out, unknown, numCtx)
		}
		return err
	}
	if startEmitted {
		out.ToolCallResult(call.ID, call.Name, result, true, 0)
		a.AppendMessage(llm.Message{Role: "tool", Content: result, ToolName: call.Name, ToolCallID: call.ID})
	} else {
		a.failedToolCall(call, out, result, numCtx)
	}
	return nil
}
