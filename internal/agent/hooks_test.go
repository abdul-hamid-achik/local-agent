package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/local-agent/internal/llm"
)

func TestSizeCapHookTruncatesOversizedResult(t *testing.T) {
	h := NewSizeCapHook(100)
	result := strings.Repeat("x", 500)
	h.PostToolUse(context.Background(), llm.ToolCall{Name: "read"}, &result, false)

	if len(result) <= 100 {
		t.Fatalf("expected a truncation notice appended, got len=%d", len(result))
	}
	if !strings.HasPrefix(result, strings.Repeat("x", 100)) {
		t.Fatal("expected the first 100 bytes preserved")
	}
	if !strings.Contains(result, "output truncated") {
		t.Fatalf("expected truncation notice, got: %q", result)
	}
}

func TestSizeCapHookLeavesSmallResult(t *testing.T) {
	h := NewSizeCapHook(100)
	result := "small output"
	h.PostToolUse(context.Background(), llm.ToolCall{Name: "read"}, &result, false)
	if result != "small output" {
		t.Fatalf("small result should be untouched, got: %q", result)
	}
}

func TestSizeCapHookDisabledWithZero(t *testing.T) {
	h := NewSizeCapHook(0)
	result := strings.Repeat("y", 1000)
	h.PostToolUse(context.Background(), llm.ToolCall{Name: "read"}, &result, false)
	if len(result) != 1000 {
		t.Fatalf("zero max should disable capping, got len=%d", len(result))
	}
}

// blockHook blocks any tool whose name matches and records post calls.
type blockHook struct {
	blockName string
	postCalls int
}

func (b *blockHook) Name() string { return "test-block" }
func (b *blockHook) PreToolUse(_ context.Context, call *llm.ToolCall) (bool, string) {
	if call.Name == b.blockName {
		return true, "blocked by test hook"
	}
	return false, ""
}
func (b *blockHook) PostToolUse(_ context.Context, _ llm.ToolCall, _ *string, _ bool) {
	b.postCalls++
}

func TestPreHookBlocks(t *testing.T) {
	a := New(nil, nil, 8192)
	bh := &blockHook{blockName: "bash"}
	a.AddToolHook(bh)

	call := llm.ToolCall{Name: "bash"}
	block, reason := a.runPreHooks(context.Background(), &call)
	if !block {
		t.Fatal("expected bash to be blocked")
	}
	if reason != "blocked by test hook" {
		t.Fatalf("unexpected reason: %q", reason)
	}

	allowed := llm.ToolCall{Name: "read"}
	if block, _ := a.runPreHooks(context.Background(), &allowed); block {
		t.Fatal("expected read to be allowed")
	}
}

func TestPostHooksRunInOrderAndMutate(t *testing.T) {
	a := New(nil, nil, 8192)
	a.AddToolHook(NewSizeCapHook(5))

	result := "0123456789"
	a.runPostHooks(context.Background(), llm.ToolCall{Name: "read"}, &result, false)
	if !strings.HasPrefix(result, "01234") || !strings.Contains(result, "truncated") {
		t.Fatalf("expected size cap applied via runPostHooks, got: %q", result)
	}
}
