package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/local-agent/internal/llm"
	"github.com/abdul-hamid-achik/local-agent/internal/tools"
)

func TestAskUserToolDefIsBuiltin(t *testing.T) {
	def := tools.AskUserToolDef()
	if def.Name != "ask_user" || !tools.IsBuiltinTool(def.Name) {
		t.Fatalf("ask_user def = %#v builtin=%v", def.Name, tools.IsBuiltinTool(def.Name))
	}
	if !AskToolPolicy().AllowsBuiltin("ask_user") || !BuildToolPolicy().AllowsBuiltin("ask_user") {
		t.Fatal("ask_user is blocked by mode policy")
	}
	if builtinToolRequiresApproval("ask_user") {
		t.Fatal("ask_user must not go through tool-approval; it is the question")
	}
}

func TestParseAskUserYesNoAndSingle(t *testing.T) {
	yesNo, err := parseAskUser(map[string]any{"question": "Overwrite?"})
	if err != nil || yesNo.Kind != UserQuestionYesNo || len(yesNo.Options) != 2 || yesNo.Options[1].ID != "no" {
		t.Fatalf("yes/no parse = %#v err=%v", yesNo, err)
	}
	single, err := parseAskUser(map[string]any{
		"question": "Which?",
		"kind":     "single",
		"options": []any{
			map[string]any{"id": "a", "label": "Alpha"},
			map[string]any{"id": "b", "label": "Beta", "detail": "second"},
		},
	})
	if err != nil || single.Kind != UserQuestionSingle || len(single.Options) != 2 || single.Options[1].Detail != "second" {
		t.Fatalf("single parse = %#v err=%v", single, err)
	}
	if _, err := parseAskUser(map[string]any{"question": "Which?", "kind": "single"}); err == nil {
		t.Fatal("single without options was accepted")
	}
}

func TestAskUserIsHiddenWithoutInteractiveCallback(t *testing.T) {
	ag := New(nil, nil, 8192)
	if hasToolDef(ag.toolsBuiltinToolDefs(), "ask_user") {
		t.Fatal("ask_user was advertised without a question callback")
	}
	result, isErr := ag.handleAskUser(context.Background(), llm.ToolCall{
		ID: "q1", Name: "ask_user", Arguments: map[string]any{"question": "OK?"},
	})
	if !isErr || !strings.Contains(result, "interactive session") {
		t.Fatalf("headless ask_user = %q err=%v", result, isErr)
	}
}

func TestAskUserBlocksUntilAnswer(t *testing.T) {
	ag := New(nil, nil, 8192)
	ag.SetQuestionCallback(func(req UserQuestionRequest) {
		req.Response <- UserQuestionResponse{ID: "no", Label: "No"}
	})
	if !hasToolDef(ag.toolsBuiltinToolDefs(), "ask_user") {
		t.Fatal("ask_user hidden after callback install")
	}
	result, isErr := ag.handleAskUser(context.Background(), llm.ToolCall{
		ID: "q1", Name: "ask_user", Arguments: map[string]any{"question": "Overwrite?"},
	})
	if isErr || !strings.Contains(result, `"id":"no"`) {
		t.Fatalf("answered ask_user = %q err=%v", result, isErr)
	}
}

func TestAskUserCancelUnblocks(t *testing.T) {
	ag := New(nil, nil, 8192)
	started := make(chan struct{})
	ag.SetQuestionCallback(func(UserQuestionRequest) { close(started) })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var result string
	var isErr bool
	go func() {
		result, isErr = ag.handleAskUser(ctx, llm.ToolCall{
			ID: "q1", Name: "ask_user", Arguments: map[string]any{"question": "Wait?"},
		})
		close(done)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("callback did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancelled ask_user did not unblock")
	}
	if !isErr || !strings.Contains(result, "cancelled") {
		t.Fatalf("cancel result = %q err=%v", result, isErr)
	}
}
