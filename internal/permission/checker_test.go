package permission

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/local-agent/internal/db"
)

func TestChecker_DefaultPolicy(t *testing.T) {
	c := NewChecker(nil, false)
	if got := c.Check("some_tool"); got != PolicyAsk {
		t.Errorf("Check() = %q, want %q", got, PolicyAsk)
	}
}

func TestChecker_Yolo(t *testing.T) {
	c := NewChecker(nil, true)
	if got := c.Check("any_tool"); got != PolicyAllow {
		t.Errorf("Check() = %q, want %q", got, PolicyAllow)
	}
	if !c.IsYolo() {
		t.Error("expected IsYolo() = true")
	}
}

func TestChecker_SetPolicy(t *testing.T) {
	c := NewChecker(nil, false)
	mustSetPolicy(t, c, "bash", PolicyAllow)
	if got := c.Check("bash"); got != PolicyAllow {
		t.Errorf("Check() = %q, want %q", got, PolicyAllow)
	}
	mustSetPolicy(t, c, "bash", PolicyDeny)
	if got := c.Check("bash"); got != PolicyDeny {
		t.Errorf("Check() = %q, want %q", got, PolicyDeny)
	}
}

func TestChecker_WithDB(t *testing.T) {
	store, err := db.OpenPath(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	c := NewChecker(store, false)
	mustSetPolicy(t, c, "file_write", PolicyAllow)

	// Create a new checker from the same DB to test persistence.
	c2 := NewChecker(store, false)
	if got := c2.Check("file_write"); got != PolicyAllow {
		t.Errorf("persisted Check() = %q, want %q", got, PolicyAllow)
	}
}

func TestChecker_Reset(t *testing.T) {
	c := NewChecker(nil, false)
	mustSetPolicy(t, c, "tool1", PolicyAllow)
	mustSetPolicy(t, c, "tool2", PolicyDeny)
	if err := c.Reset(); err != nil {
		t.Fatalf("reset: %v", err)
	}

	if got := c.Check("tool1"); got != PolicyAsk {
		t.Errorf("after reset Check() = %q, want %q", got, PolicyAsk)
	}
}

func TestChecker_AllPolicies(t *testing.T) {
	c := NewChecker(nil, false)
	mustSetPolicy(t, c, "a", PolicyAllow)
	mustSetPolicy(t, c, "b", PolicyDeny)

	policies := c.AllPolicies()
	if len(policies) != 2 {
		t.Errorf("AllPolicies() len = %d, want 2", len(policies))
	}
	if policies["a"] != PolicyAllow {
		t.Errorf("policies[a] = %q, want %q", policies["a"], PolicyAllow)
	}
}

func TestToCheckResult(t *testing.T) {
	c := NewChecker(nil, false)
	mustSetPolicy(t, c, "allowed", PolicyAllow)
	mustSetPolicy(t, c, "denied", PolicyDeny)

	if c.ToCheckResult("allowed") != CheckAllow {
		t.Error("expected CheckAllow for allowed tool")
	}
	if c.ToCheckResult("denied") != CheckDeny {
		t.Error("expected CheckDeny for denied tool")
	}
	if c.ToCheckResult("unknown") != CheckAsk {
		t.Error("expected CheckAsk for unknown tool")
	}
}

func TestToCheckResult_Nil(t *testing.T) {
	var c *Checker
	if c.ToCheckResult("anything") != CheckAllow {
		t.Error("nil checker should return CheckAllow")
	}
}

func mustSetPolicy(t *testing.T, c *Checker, toolName string, policy Policy) {
	t.Helper()
	if err := c.SetPolicy(toolName, policy); err != nil {
		t.Fatalf("set policy for %s: %v", toolName, err)
	}
}

func TestRequestApprovalFailsClosedWithoutCallback(t *testing.T) {
	allowed, always := RequestApproval("bash", map[string]any{"command": "true"}, nil)
	if allowed || always {
		t.Fatalf("missing callback returned allowed=%v always=%v, want false/false", allowed, always)
	}
}

func TestRequestApprovalContextCancels(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	block := make(chan struct{})
	allowed, always := RequestApprovalContext(ctx, "bash", nil, func(ApprovalRequest) { <-block })
	if allowed || always {
		t.Fatalf("cancelled approval returned allowed=%v always=%v", allowed, always)
	}
	close(block)
}

func TestAlwaysAllowResponds(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	allowed, _ := RequestApprovalContext(ctx, "read", nil, AlwaysAllow)
	if !allowed {
		t.Fatal("AlwaysAllow did not approve")
	}
}

func TestResolveApprovalContextDistinguishesHostUserAndCancellation(t *testing.T) {
	t.Run("host refusal", func(t *testing.T) {
		response := ResolveApprovalContext(context.Background(), ApprovalRequest{ToolName: "write"}, func(request ApprovalRequest) {
			request.Response <- Refuse("preview_unavailable", "could not render diff")
		})
		if response.Decision != DecisionHostRefuse || response.Code != "preview_unavailable" || response.Allowed {
			t.Fatalf("response = %#v", response)
		}
	})

	t.Run("user deny", func(t *testing.T) {
		response := ResolveApprovalContext(context.Background(), ApprovalRequest{ToolName: "write"}, func(request ApprovalRequest) {
			request.Response <- Deny()
		})
		if response.Decision != DecisionUserDeny || response.Allowed {
			t.Fatalf("response = %#v", response)
		}
	})

	t.Run("missing host", func(t *testing.T) {
		response := ResolveApprovalContext(context.Background(), ApprovalRequest{ToolName: "write"}, nil)
		if response.Decision != DecisionHostRefuse || response.Code != "approval_ui_unavailable" {
			t.Fatalf("response = %#v", response)
		}
	})

	t.Run("cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		response := ResolveApprovalContext(ctx, ApprovalRequest{ToolName: "write"}, func(ApprovalRequest) {})
		if response.Decision != DecisionCancelled || response.Allowed {
			t.Fatalf("response = %#v", response)
		}
	})
}

func TestLegacyAlwaysNormalizesToSessionOnly(t *testing.T) {
	response := (ApprovalResponse{Allowed: true, Always: true}).Normalize()
	if response.Decision != DecisionAllowSession || !response.Allowed || !response.Always {
		t.Fatalf("response = %#v", response)
	}
}
