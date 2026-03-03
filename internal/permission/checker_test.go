package permission

import (
	"path/filepath"
	"testing"

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
	c.SetPolicy("bash", PolicyAllow)
	if got := c.Check("bash"); got != PolicyAllow {
		t.Errorf("Check() = %q, want %q", got, PolicyAllow)
	}
	c.SetPolicy("bash", PolicyDeny)
	if got := c.Check("bash"); got != PolicyDeny {
		t.Errorf("Check() = %q, want %q", got, PolicyDeny)
	}
}

func TestChecker_WithDB(t *testing.T) {
	store, err := db.OpenPath(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	c := NewChecker(store, false)
	c.SetPolicy("file_write", PolicyAllow)

	// Create a new checker from the same DB to test persistence.
	c2 := NewChecker(store, false)
	if got := c2.Check("file_write"); got != PolicyAllow {
		t.Errorf("persisted Check() = %q, want %q", got, PolicyAllow)
	}
}

func TestChecker_Reset(t *testing.T) {
	c := NewChecker(nil, false)
	c.SetPolicy("tool1", PolicyAllow)
	c.SetPolicy("tool2", PolicyDeny)
	c.Reset()

	if got := c.Check("tool1"); got != PolicyAsk {
		t.Errorf("after reset Check() = %q, want %q", got, PolicyAsk)
	}
}

func TestChecker_AllPolicies(t *testing.T) {
	c := NewChecker(nil, false)
	c.SetPolicy("a", PolicyAllow)
	c.SetPolicy("b", PolicyDeny)

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
	c.SetPolicy("allowed", PolicyAllow)
	c.SetPolicy("denied", PolicyDeny)

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
