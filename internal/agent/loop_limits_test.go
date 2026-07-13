package agent

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/local-agent/internal/ice"
	"github.com/abdul-hamid-achik/local-agent/internal/llm"
	"github.com/abdul-hamid-achik/local-agent/internal/memory"
)

type limitedTurnClient struct {
	limit int
	calls atomic.Int64
	block bool
}

func (c *limitedTurnClient) ChatStream(ctx context.Context, options llm.ChatOptions, emit func(llm.StreamChunk) error) error {
	c.calls.Add(1)
	c.limit = options.MaxEvalTokens
	if c.block {
		<-ctx.Done()
		return ctx.Err()
	}
	return emit(llm.StreamChunk{
		Done: true, EvalCount: options.MaxEvalTokens,
		ToolCalls: []llm.ToolCall{{ID: "must-not-dispatch", Name: "write", Arguments: map[string]any{"path": "nope"}}},
	})
}

func (*limitedTurnClient) Ping() error   { return nil }
func (*limitedTurnClient) Model() string { return "limited-test" }
func (*limitedTurnClient) Embed(context.Context, string, []string) ([][]float32, error) {
	return nil, nil
}

type limitOutput struct {
	toolStarts atomic.Int64
	evalTokens atomic.Int64
}

func (*limitOutput) StreamText(string)                                          {}
func (*limitOutput) StreamReasoning(string)                                     {}
func (o *limitOutput) StreamDone(evalTokens, _ int)                             { o.evalTokens.Add(int64(evalTokens)) }
func (o *limitOutput) ToolCallStart(string, string, map[string]any)             { o.toolStarts.Add(1) }
func (*limitOutput) ToolCallResult(string, string, string, bool, time.Duration) {}
func (*limitOutput) SystemMessage(string)                                       {}
func (*limitOutput) Error(string)                                               {}

type contextBudgetOutput struct {
	limitOutput
	errors []string
}

func (o *contextBudgetOutput) Error(message string) {
	o.errors = append(o.errors, message)
}

type partialLimitedClient struct {
	limit int
	calls atomic.Int64
}

func (c *partialLimitedClient) ChatStream(_ context.Context, options llm.ChatOptions, emit func(llm.StreamChunk) error) error {
	c.calls.Add(1)
	c.limit = options.MaxEvalTokens
	if err := emit(llm.StreamChunk{Text: "partial response without a terminal receipt"}); err != nil {
		return err
	}
	return io.ErrUnexpectedEOF
}

func (*partialLimitedClient) Ping() error   { return nil }
func (*partialLimitedClient) Model() string { return "partial-limited-test" }
func (*partialLimitedClient) Embed(context.Context, string, []string) ([][]float32, error) {
	return nil, nil
}

type rejectedLimitedClient struct{}

func (*rejectedLimitedClient) ChatStream(context.Context, llm.ChatOptions, func(llm.StreamChunk) error) error {
	return llm.ErrNoModelSelected
}

func (*rejectedLimitedClient) Ping() error   { return llm.ErrNoModelSelected }
func (*rejectedLimitedClient) Model() string { return "" }
func (*rejectedLimitedClient) Embed(context.Context, string, []string) ([][]float32, error) {
	return nil, llm.ErrNoModelSelected
}

type boundedSideGenerationClient struct {
	calls         atomic.Int64
	uncappedCalls atomic.Int64
}

func (c *boundedSideGenerationClient) ChatStream(_ context.Context, options llm.ChatOptions, emit func(llm.StreamChunk) error) error {
	c.calls.Add(1)
	if options.MaxEvalTokens == 0 {
		c.uncappedCalls.Add(1)
	}
	return emit(llm.StreamChunk{
		Text:      "A sufficiently long direct response that would normally qualify for automatic memory extraction after this turn.",
		Done:      true,
		EvalCount: 1,
	})
}

func (*boundedSideGenerationClient) Ping() error   { return nil }
func (*boundedSideGenerationClient) Model() string { return "bounded-side-generation-test" }
func (*boundedSideGenerationClient) Embed(_ context.Context, _ string, texts []string) ([][]float32, error) {
	result := make([][]float32, len(texts))
	for index := range result {
		result[index] = []float32{1}
	}
	return result, nil
}

type boundedToolResultClient struct {
	calls atomic.Int64
}

func (c *boundedToolResultClient) ChatStream(_ context.Context, _ llm.ChatOptions, emit func(llm.StreamChunk) error) error {
	call := c.calls.Add(1)
	if call == 1 {
		return emit(llm.StreamChunk{
			Done: true, EvalCount: 1,
			ToolCalls: []llm.ToolCall{{
				ID: "expand-result", Name: "exists", Arguments: map[string]any{"path": "."},
			}},
		})
	}
	return emit(llm.StreamChunk{Text: "must not be requested", Done: true, EvalCount: 1})
}

func (*boundedToolResultClient) Ping() error   { return nil }
func (*boundedToolResultClient) Model() string { return "bounded-tool-result-test" }
func (*boundedToolResultClient) NumCtx() int   { return 1_200 }
func (*boundedToolResultClient) Embed(context.Context, string, []string) ([][]float32, error) {
	return nil, nil
}

type expandToolResultHook struct{}

func (*expandToolResultHook) Name() string { return "expand-tool-result" }
func (*expandToolResultHook) PreToolUse(context.Context, *llm.ToolCall) (bool, string) {
	return false, ""
}
func (*expandToolResultHook) PostToolUse(_ context.Context, _ llm.ToolCall, result *string, _ bool) {
	*result = strings.Repeat("tool-output ", 8_192)
}

type joinedAutoMemoryClient struct {
	autoStarted chan struct{}
	autoStopped chan struct{}
	mainCalls   atomic.Int64
}

func (c *joinedAutoMemoryClient) ChatStream(ctx context.Context, options llm.ChatOptions, emit func(llm.StreamChunk) error) error {
	if options.MaxEvalTokens == 0 {
		close(c.autoStarted)
		<-ctx.Done()
		close(c.autoStopped)
		return ctx.Err()
	}
	c.mainCalls.Add(1)
	select {
	case <-c.autoStopped:
	default:
		return errors.New("bounded main generation overlapped auto-memory")
	}
	return emit(llm.StreamChunk{Text: "bounded response", Done: true, EvalCount: 1})
}

func (*joinedAutoMemoryClient) Ping() error   { return nil }
func (*joinedAutoMemoryClient) Model() string { return "joined-auto-memory-test" }
func (*joinedAutoMemoryClient) Embed(_ context.Context, _ string, texts []string) ([][]float32, error) {
	result := make([][]float32, len(texts))
	for index := range result {
		result[index] = []float32{1}
	}
	return result, nil
}

func TestRunTurnWithLimitsCapsProviderAndStopsBeforeToolDispatch(t *testing.T) {
	client := &limitedTurnClient{}
	agent := New(client, nil, 4096)
	agent.SetWorkDir(t.TempDir())
	output := &limitOutput{}

	err := agent.RunTurnWithLimits(context.Background(), output, "turn_limited", TurnLimits{MaxEvalTokens: 5})
	if !errors.Is(err, ErrTurnEvalBudgetExhausted) {
		t.Fatalf("error = %v", err)
	}
	if client.limit != 5 || client.calls.Load() != 1 {
		t.Fatalf("provider limit=%d calls=%d", client.limit, client.calls.Load())
	}
	if output.toolStarts.Load() != 0 {
		t.Fatalf("hard token boundary dispatched %d tools", output.toolStarts.Load())
	}
}

func TestRunTurnWithLimitsAppliesWallDeadline(t *testing.T) {
	client := &limitedTurnClient{block: true}
	agent := New(client, nil, 4096)
	agent.SetWorkDir(t.TempDir())
	started := time.Now()

	err := agent.RunTurnWithLimits(context.Background(), &limitOutput{}, "turn_deadline", TurnLimits{MaxWallTime: 20 * time.Millisecond})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("wall deadline took %s", elapsed)
	}
}

func TestRunTurnWithLimitsUsesAbsoluteDeadlineWithoutRebasing(t *testing.T) {
	client := &limitedTurnClient{block: true}
	agent := New(client, nil, 4096)
	agent.SetWorkDir(t.TempDir())

	err := agent.RunTurnWithLimits(context.Background(), &limitOutput{}, "turn_absolute_deadline", TurnLimits{
		Deadline: time.Now().Add(-time.Second),
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v", err)
	}
	if client.calls.Load() != 0 {
		t.Fatalf("expired absolute deadline reached provider %d time(s)", client.calls.Load())
	}
}

func TestRunTurnWithLimitsChargesReservationWhenTerminalUsageIsUnknown(t *testing.T) {
	client := &partialLimitedClient{}
	agent := New(client, nil, 4096)
	agent.SetWorkDir(t.TempDir())
	output := &limitOutput{}

	err := agent.RunTurnWithLimits(context.Background(), output, "turn_partial_receipt", TurnLimits{MaxEvalTokens: 7})
	if !errors.Is(err, ErrTurnEvalBudgetExhausted) || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("error = %v", err)
	}
	if client.limit != 7 || client.calls.Load() != 1 {
		t.Fatalf("provider limit=%d calls=%d", client.limit, client.calls.Load())
	}
	if output.evalTokens.Load() != 7 {
		t.Fatalf("conservative token charge=%d, want full 7-token reservation", output.evalTokens.Load())
	}
	if output.toolStarts.Load() != 0 {
		t.Fatalf("partial stream dispatched %d tools", output.toolStarts.Load())
	}
}

func TestRunTurnWithLimitsDoesNotChargeKnownLocalPreflightRejection(t *testing.T) {
	agent := New(&rejectedLimitedClient{}, nil, 4096)
	agent.SetWorkDir(t.TempDir())
	output := &limitOutput{}

	err := agent.RunTurnWithLimits(context.Background(), output, "turn_local_rejection", TurnLimits{MaxEvalTokens: 7})
	if !errors.Is(err, llm.ErrNoModelSelected) || errors.Is(err, ErrTurnEvalBudgetExhausted) {
		t.Fatalf("error = %v", err)
	}
	if output.evalTokens.Load() != 0 {
		t.Fatalf("local preflight rejection charged %d token(s)", output.evalTokens.Load())
	}
	if output.toolStarts.Load() != 0 {
		t.Fatalf("local preflight rejection dispatched %d tools", output.toolStarts.Load())
	}
}

func TestRunTurnWithLimitsSkipsOptionalProviderGenerations(t *testing.T) {
	client := &boundedSideGenerationClient{}
	dir := t.TempDir()
	memories := memory.NewStore(filepath.Join(dir, "memories.json"))
	engine, err := ice.NewEngine(client, memories, ice.EngineConfig{
		StorePath: filepath.Join(dir, "conversations.json"),
		Workspace: dir,
		NumCtx:    16_384,
	})
	if err != nil {
		t.Fatal(err)
	}
	agent := New(client, nil, 16_384)
	agent.SetWorkDir(dir)
	agent.SetICEEngine(engine)
	defer agent.Close()
	for index := 0; index < 3; index++ {
		agent.AppendMessage(llm.Message{Role: "user", Content: "A long prior user message that makes compaction eligible under the tiny test context."})
		agent.AppendMessage(llm.Message{Role: "assistant", Content: "A long prior assistant response that also contributes to the compaction threshold."})
	}
	agent.AppendMessage(llm.Message{Role: "user", Content: "Please produce a direct answer long enough for automatic memory detection."})

	if err := agent.RunTurnWithLimits(context.Background(), &limitOutput{}, "turn_no_side_generation", TurnLimits{MaxEvalTokens: 8}); err != nil {
		t.Fatal(err)
	}
	// If auto-memory was scheduled, joining it makes its ChatStream call visible
	// before the assertions without relying on sleeps or scheduler timing.
	engine.StopAutoMemory()
	if calls := client.calls.Load(); calls != 1 {
		t.Fatalf("bounded turn made %d provider generations, want only the main response", calls)
	}
	if calls := client.uncappedCalls.Load(); calls != 0 {
		t.Fatalf("bounded turn made %d uncapped provider generations", calls)
	}
}

func TestRunTurnWithLimitsRejectsOversizedPromptBeforeProvider(t *testing.T) {
	client := &boundedSideGenerationClient{}
	agent := New(client, nil, 64)
	agent.SetWorkDir(t.TempDir())
	for index := 0; index < 3; index++ {
		agent.AppendMessage(llm.Message{Role: "user", Content: "A long prior user message that pushes this bounded turn beyond its safe context threshold."})
		agent.AppendMessage(llm.Message{Role: "assistant", Content: "A long prior assistant response that must be compacted before any new provider request."})
	}
	agent.AppendMessage(llm.Message{Role: "user", Content: "Continue the bounded goal."})
	output := &contextBudgetOutput{}

	err := agent.RunTurnWithLimits(context.Background(), output, "turn_context_full", TurnLimits{MaxEvalTokens: 8})
	if !errors.Is(err, ErrTurnContextBudgetExceeded) {
		t.Fatalf("error = %v, want context-budget error", err)
	}
	var detail *TurnContextBudgetError
	if !errors.As(err, &detail) || detail.EstimatedPromptTokens <= 0 || detail.ContextWindowTokens != 64 {
		t.Fatalf("typed context error = %#v", detail)
	}
	if calls := client.calls.Load(); calls != 0 {
		t.Fatalf("oversized bounded turn made %d provider call(s), want zero", calls)
	}
	if len(output.errors) != 1 || !strings.Contains(output.errors[0], "compact history") || !strings.Contains(output.errors[0], "retry") {
		t.Fatalf("recovery message = %#v", output.errors)
	}
}

func TestRunTurnWithLimitsRejectsOversizedToolResultBeforeSecondProviderCall(t *testing.T) {
	client := &boundedToolResultClient{}
	agent := New(client, nil, 1_200)
	agent.SetWorkDir(t.TempDir())
	agent.SetModeContext("test", NewToolPolicy([]string{"exists"}, nil, false))
	agent.AddToolHook(&expandToolResultHook{})
	agent.AddUserMessage("Check whether the workspace exists, then continue the bounded goal.")
	output := &contextBudgetOutput{}

	err := agent.RunTurnWithLimits(context.Background(), output, "turn_tool_context_full", TurnLimits{MaxEvalTokens: 8})
	if !errors.Is(err, ErrTurnContextBudgetExceeded) {
		t.Fatalf("error = %v, provider calls = %d; want context-budget error", err, client.calls.Load())
	}
	if calls := client.calls.Load(); calls != 1 {
		t.Fatalf("provider calls = %d, want exactly one before the tool result filled context", calls)
	}
	if starts := output.toolStarts.Load(); starts != 1 {
		t.Fatalf("tool starts = %d, want one", starts)
	}
	var detail *TurnContextBudgetError
	if !errors.As(err, &detail) || detail.EstimatedPromptTokens <= 0 || detail.ContextWindowTokens != 1_200 {
		t.Fatalf("typed context error = %#v", detail)
	}
}

func TestRunTurnWithLimitsJoinsPriorAutoMemoryBeforeProvider(t *testing.T) {
	client := &joinedAutoMemoryClient{
		autoStarted: make(chan struct{}),
		autoStopped: make(chan struct{}),
	}
	dir := t.TempDir()
	memories := memory.NewStore(filepath.Join(dir, "memories.json"))
	engine, err := ice.NewEngine(client, memories, ice.EngineConfig{
		StorePath: filepath.Join(dir, "conversations.json"),
		Workspace: dir,
		NumCtx:    4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	agent := New(client, nil, 4096)
	agent.SetWorkDir(dir)
	agent.SetICEEngine(engine)
	defer agent.Close()

	engine.DetectAutoMemory(context.Background(),
		"A prior user exchange long enough to trigger automatic memory extraction.",
		"A prior assistant exchange long enough to trigger automatic memory extraction and remain in flight.",
	)
	<-client.autoStarted
	agent.AppendMessage(llm.Message{Role: "user", Content: "Start the bounded goal turn only after optional inference is joined."})

	if err := agent.RunTurnWithLimits(context.Background(), &limitOutput{}, "turn_join_auto_memory", TurnLimits{MaxEvalTokens: 8}); err != nil {
		t.Fatal(err)
	}
	if client.mainCalls.Load() != 1 {
		t.Fatalf("bounded main provider calls=%d", client.mainCalls.Load())
	}
	select {
	case <-client.autoStopped:
	default:
		t.Fatal("bounded turn returned before prior auto-memory stopped")
	}
}
