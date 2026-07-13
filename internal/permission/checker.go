package permission

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/abdul-hamid-achik/local-agent/internal/db"
)

// Policy represents a tool's permission policy.
type Policy string

const (
	PolicyAllow Policy = "allow" // always allow
	PolicyDeny  Policy = "deny"  // always deny
	PolicyAsk   Policy = "ask"   // prompt user each time
)

// Checker manages tool permissions with an in-memory cache backed by SQLite.
type Checker struct {
	store *db.Store
	cache map[string]Policy
	mu    sync.RWMutex
	yolo  bool // auto-approve all
}

// NewChecker creates a permission checker. If store is nil, all tools default to "ask".
// If yolo is true, all tools are auto-approved.
func NewChecker(store *db.Store, yolo bool) *Checker {
	c := &Checker{
		store: store,
		cache: make(map[string]Policy),
		yolo:  yolo,
	}
	if store != nil {
		c.loadFromDB()
	}
	return c
}

// Check returns the policy for the given tool.
func (c *Checker) Check(toolName string) Policy {
	if c.yolo {
		return PolicyAllow
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if p, ok := c.cache[toolName]; ok {
		return p
	}
	return PolicyAsk
}

// SetPolicy updates the policy for a tool and persists it.
func (c *Checker) SetPolicy(toolName string, policy Policy) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.store != nil {
		if _, err := c.store.UpsertToolPermission(context.Background(), db.UpsertToolPermissionParams{
			ToolName: toolName,
			Policy:   string(policy),
		}); err != nil {
			return fmt.Errorf("persist permission for %s: %w", toolName, err)
		}
	}
	c.cache[toolName] = policy
	return nil
}

// IsYolo returns true if the checker auto-approves all tools.
func (c *Checker) IsYolo() bool {
	return c.yolo
}

// AllPolicies returns all explicitly set policies.
func (c *Checker) AllPolicies() map[string]Policy {
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := make(map[string]Policy, len(c.cache))
	for k, v := range c.cache {
		result[k] = v
	}
	return result
}

// Reset clears all stored permissions.
func (c *Checker) Reset() error {
	c.mu.Lock()
	c.cache = make(map[string]Policy)
	c.mu.Unlock()

	if c.store != nil {
		if err := c.store.ResetToolPermissions(context.Background()); err != nil {
			return fmt.Errorf("reset persisted permissions: %w", err)
		}
	}
	return nil
}

func (c *Checker) loadFromDB() {
	perms, err := c.store.ListToolPermissions(context.Background())
	if err != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, p := range perms {
		switch Policy(p.Policy) {
		case PolicyAllow, PolicyDeny, PolicyAsk:
			c.cache[p.ToolName] = Policy(p.Policy)
		}
	}
}

// ApprovalDecision is the host-owned resolution of an interactive approval.
// It deliberately distinguishes a human denial from a host refusal so the
// agent cannot report an inspection/rendering failure as a user decision.
type ApprovalDecision string

const (
	DecisionAllowOnce    ApprovalDecision = "allow_once"
	DecisionAllowSession ApprovalDecision = "allow_session"
	DecisionUserDeny     ApprovalDecision = "user_deny"
	DecisionHostRefuse   ApprovalDecision = "host_refuse"
	DecisionCancelled    ApprovalDecision = "cancelled"
)

func (d ApprovalDecision) Valid() bool {
	switch d {
	case DecisionAllowOnce, DecisionAllowSession, DecisionUserDeny,
		DecisionHostRefuse, DecisionCancelled:
		return true
	default:
		return false
	}
}

// ApprovalPreviewKind lets a host choose a purpose-built renderer without
// reparsing arbitrary tool arguments.
type ApprovalPreviewKind string

const (
	PreviewGeneric    ApprovalPreviewKind = "generic"
	PreviewFileWrite  ApprovalPreviewKind = "file_write"
	PreviewFilePatch  ApprovalPreviewKind = "file_patch"
	PreviewFilesystem ApprovalPreviewKind = "filesystem"
	PreviewCommand    ApprovalPreviewKind = "command"
)

// ApprovalPreview is bounded presentation metadata. The complete arguments
// remain in ApprovalRequest.Args and are cryptographically bound by
// ArgumentsSHA256; hosts may render them in a viewport and must not impose an
// arbitrary whole-request display limit.
type ApprovalPreview struct {
	Kind              ApprovalPreviewKind
	Path              string
	SourcePath        string
	DestinationPath   string
	Command           string
	ByteSize          int64
	ArgumentsSHA256   string
	ContentSHA256     string
	ExistingSHA256    string
	Diff              string
	DiffTruncated     bool
	DiffOmittedReason string
}

// ApprovalScope is the maximum authority represented by an AllowSession
// decision. ScopeExactRequest intentionally starts narrower than path or
// command-prefix grants: it binds the grant to workspace, tool and canonical
// arguments while leaving room for audited scope kinds later.
type ApprovalScope struct {
	Kind      string
	Workspace string
	Resource  string
}

const ScopeExactRequest = "exact_request"

// ApprovalRequest represents a tool requesting user approval.
type ApprovalRequest struct {
	RequestID       string
	ToolName        string
	Args            map[string]any
	ArgumentsSHA256 string
	Preview         ApprovalPreview
	Scope           ApprovalScope
	Response        chan ApprovalResponse
}

// ApprovalResponse represents the user's decision on a tool approval.
type ApprovalResponse struct {
	Decision ApprovalDecision
	Code     string
	Message  string

	// Allowed and Always are a temporary source-compatibility bridge for older
	// embedding callbacks. New code must set Decision. Legacy Always maps to a
	// session-scoped exact-request grant and is never persisted globally.
	Allowed bool
	Always  bool
}

func AllowOnce() ApprovalResponse {
	return ApprovalResponse{Decision: DecisionAllowOnce, Allowed: true}
}

func AllowSession() ApprovalResponse {
	return ApprovalResponse{Decision: DecisionAllowSession, Allowed: true, Always: true}
}

func Deny() ApprovalResponse {
	return ApprovalResponse{Decision: DecisionUserDeny}
}

func Refuse(code, message string) ApprovalResponse {
	return ApprovalResponse{Decision: DecisionHostRefuse, Code: strings.TrimSpace(code), Message: strings.TrimSpace(message)}
}

func Cancelled(message string) ApprovalResponse {
	return ApprovalResponse{Decision: DecisionCancelled, Code: "cancelled", Message: strings.TrimSpace(message)}
}

// Normalize resolves the deprecated bool bridge and validates host output.
// Invalid or incomplete responses fail closed as a host refusal, never as a
// user denial.
func (r ApprovalResponse) Normalize() ApprovalResponse {
	if r.Decision == "" {
		switch {
		case r.Allowed && r.Always:
			r.Decision = DecisionAllowSession
		case r.Allowed:
			r.Decision = DecisionAllowOnce
		default:
			// A zero-value legacy response historically meant deny. Preserve
			// that narrow behavior while typed callers use Deny explicitly.
			r.Decision = DecisionUserDeny
		}
	}
	if !r.Decision.Valid() {
		return Refuse("invalid_approval_decision", fmt.Sprintf("approval host returned invalid decision %q", r.Decision))
	}
	switch r.Decision {
	case DecisionAllowOnce:
		r.Allowed, r.Always = true, false
	case DecisionAllowSession:
		r.Allowed, r.Always = true, true
	default:
		r.Allowed, r.Always = false, false
	}
	if r.Decision == DecisionHostRefuse && strings.TrimSpace(r.Code) == "" {
		r.Code = "host_refused"
	}
	return r
}

func (r ApprovalResponse) IsAllowed() bool {
	switch r.Normalize().Decision {
	case DecisionAllowOnce, DecisionAllowSession:
		return true
	default:
		return false
	}
}

// RequestApproval sends an approval request through the callback and blocks for a response.
// Returns (allowed, alwaysAllow).
func RequestApproval(toolName string, args map[string]any, callback func(ApprovalRequest)) (bool, bool) {
	return RequestApprovalContext(context.Background(), toolName, args, callback)
}

// RequestApprovalContext is the cancellable form used by an active agent
// turn. Missing approval UI fails closed: callers must opt into yolo mode or
// provide an explicit response before a risky tool may execute.
func RequestApprovalContext(ctx context.Context, toolName string, args map[string]any, callback func(ApprovalRequest)) (bool, bool) {
	response := ResolveApprovalContext(ctx, ApprovalRequest{ToolName: toolName, Args: args}, callback)
	return response.Allowed, response.Decision == DecisionAllowSession
}

// ResolveApprovalContext is the typed, cancellable approval boundary used by
// the agent runtime. Missing UI is a host refusal and context cancellation is
// a cancellation; neither is mislabeled as a user denial.
func ResolveApprovalContext(ctx context.Context, request ApprovalRequest, callback func(ApprovalRequest)) ApprovalResponse {
	if callback == nil {
		return Refuse("approval_ui_unavailable", "interactive approval is unavailable")
	}
	ch := make(chan ApprovalResponse, 1)
	request.Response = ch
	// Dispatch cannot sit in front of the cancellation select: UI adapters or
	// embedding callbacks may block while delivering the prompt. The buffered
	// response channel lets a late answer finish after cancellation as well.
	go callback(request)
	select {
	case resp := <-ch:
		return resp.Normalize()
	case <-ctx.Done():
		return Cancelled(ctx.Err().Error())
	}
}

// CheckResult represents the decision from checking a tool's permission.
type CheckResult int

const (
	CheckAllow CheckResult = iota // proceed
	CheckDeny                     // blocked
	CheckAsk                      // needs user approval
)

// ToCheckResult checks the tool and returns the result, simplifying the caller logic.
func (c *Checker) ToCheckResult(toolName string) CheckResult {
	if c == nil || c.yolo {
		return CheckAllow
	}
	switch c.Check(toolName) {
	case PolicyAllow:
		return CheckAllow
	case PolicyDeny:
		return CheckDeny
	default:
		return CheckAsk
	}
}

// NilSafe creates a no-op checker if store is nil, for safe optional use.
func NilSafe(store *db.Store, yolo bool) *Checker {
	return NewChecker(store, yolo)
}

// AlwaysAllow is an explicit auto-approval callback for trusted callers.
var AlwaysAllow = func(req ApprovalRequest) {
	req.Response <- AllowOnce()
}

// ErrDenied is returned when a tool call is denied by permissions.
type ErrDenied struct {
	ToolName string
}

func (e *ErrDenied) Error() string {
	return "tool call denied by permission policy: " + e.ToolName
}
