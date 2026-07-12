package permission

import (
	"context"
	"fmt"
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

// ApprovalRequest represents a tool requesting user approval.
type ApprovalRequest struct {
	ToolName string
	Args     map[string]any
	Response chan ApprovalResponse
}

// ApprovalResponse represents the user's decision on a tool approval.
type ApprovalResponse struct {
	Allowed bool
	Always  bool // if true, persist as "allow" permanently
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
	if callback == nil {
		return false, false
	}
	ch := make(chan ApprovalResponse, 1)
	request := ApprovalRequest{
		ToolName: toolName,
		Args:     args,
		Response: ch,
	}
	// Dispatch cannot sit in front of the cancellation select: UI adapters or
	// embedding callbacks may block while delivering the prompt. The buffered
	// response channel lets a late answer finish after cancellation as well.
	go callback(request)
	select {
	case resp := <-ch:
		return resp.Allowed, resp.Always
	case <-ctx.Done():
		return false, false
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
	req.Response <- ApprovalResponse{Allowed: true}
}

// ErrDenied is returned when a tool call is denied by permissions.
type ErrDenied struct {
	ToolName string
}

func (e *ErrDenied) Error() string {
	return "tool call denied by permission policy: " + e.ToolName
}
