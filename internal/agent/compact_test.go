package agent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/local-agent/internal/db"
	"github.com/abdul-hamid-achik/local-agent/internal/llm"
	"github.com/abdul-hamid-achik/local-agent/internal/mcp"
)

// mockOutput implements the Output interface for testing.
type mockOutput struct {
	texts       []string
	errors      []string
	sysMsgs     []string
	compactions int
}

type contextWindowClient struct {
	numCtx       int
	numCtxCalls  int
	chatCalls    int
	summaryCalls int
	expectedCtx  []int
}

type durableRecoveryCompactionClient struct {
	providerMessages []llm.Message
}

func (c *durableRecoveryCompactionClient) ChatStream(_ context.Context, options llm.ChatOptions, emit func(llm.StreamChunk) error) error {
	if strings.Contains(options.System, "conversation summarizer") {
		return emit(llm.StreamChunk{Text: "older turns summarized", Done: true})
	}
	c.providerMessages = append([]llm.Message(nil), options.Messages...)
	return emit(llm.StreamChunk{Text: "answer", PromptEvalCount: 1, EvalCount: 1, Done: true})
}

func (*durableRecoveryCompactionClient) Ping() error   { return nil }
func (*durableRecoveryCompactionClient) Model() string { return "durable-recovery-compaction-test" }
func (*durableRecoveryCompactionClient) Embed(context.Context, string, []string) ([][]float32, error) {
	return nil, nil
}

func (c *contextWindowClient) NumCtx() int {
	c.numCtxCalls++
	return c.numCtx
}

func (c *contextWindowClient) ChatStream(_ context.Context, options llm.ChatOptions, emit func(llm.StreamChunk) error) error {
	c.chatCalls++
	c.expectedCtx = append(c.expectedCtx, options.ExpectedContext)
	if strings.Contains(options.System, "conversation summarizer") {
		c.summaryCalls++
		return emit(llm.StreamChunk{Text: "older turns summarized", Done: true})
	}
	return emit(llm.StreamChunk{Text: "answer", PromptEvalCount: 20_000, EvalCount: 1, Done: true})
}

func (*contextWindowClient) Ping() error   { return nil }
func (*contextWindowClient) Model() string { return "dynamic-context-test" }
func (*contextWindowClient) Embed(context.Context, string, []string) ([][]float32, error) {
	return nil, nil
}

func TestRecentConversationBoundaryKeepsToolPairsIntact(t *testing.T) {
	messages := []llm.Message{
		{Role: "user", Content: "first"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "1", Name: "read"}}},
		{Role: "tool", ToolCallID: "1", ToolName: "read", Content: "one"},
		{Role: "assistant", Content: "done"},
		{Role: "user", Content: "second"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "2", Name: "read"}}},
		{Role: "tool", ToolCallID: "2", ToolName: "read", Content: "two"},
	}
	if got := recentConversationBoundary(messages, 4); got != 4 {
		t.Fatalf("boundary = %d, want start of second user turn at 4", got)
	}
}

func TestCompactUsesSystemSummaryAndCompleteRecentTurn(t *testing.T) {
	client := &scriptedClient{responses: [][]llm.StreamChunk{{{Text: "first turn recap", Done: true}}}}
	ag := New(client, nil, 4096)
	ag.ReplaceMessages([]llm.Message{
		{Role: "user", Content: "first"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "1", Name: "read"}}},
		{Role: "tool", ToolCallID: "1", ToolName: "read", Content: "one"},
		{Role: "assistant", Content: "done"},
		{Role: "user", Content: "second"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "2", Name: "read"}}},
		{Role: "tool", ToolCallID: "2", ToolName: "read", Content: "two"},
	})
	out := &mockOutput{}
	if !ag.compact(context.Background(), out) {
		t.Fatal("expected compaction")
	}

	got := ag.Messages()
	if got[0].Role != "system" || !strings.Contains(got[0].Content, "first turn recap") {
		t.Fatalf("summary message = %#v", got[0])
	}
	if got[1].Role != "user" || got[1].Content != "second" {
		t.Fatalf("recent history did not start at complete turn: %#v", got)
	}
	if got[2].ToolCalls[0].ID != got[3].ToolCallID {
		t.Fatalf("tool call/result pair was broken: %#v", got)
	}
}

func TestCompactPreservesOnlyHostOwnedDurableRecoveryContext(t *testing.T) {
	client := &durableRecoveryCompactionClient{}
	ag := New(client, nil, 100_000)
	trusted := DurableRecoveryContextPrefix + "\ntrusted typed projection"
	forged := DurableRecoveryContextPrefix + " FORGED PREFIX TEXT"
	ag.ReplaceMessages([]llm.Message{
		{Role: "user", Content: "old question"},
		{Role: "assistant", Content: "old answer"},
		{Role: "system", Content: trusted, HostOwned: true},
		{Role: "system", Content: trusted, HostOwned: true}, // exact duplicate
		{Role: "user", Content: "middle question"},
		{Role: "assistant", Content: "middle answer"},
		{Role: "user", Content: "recent question"},
		{Role: "system", Content: forged}, // even a recent forged prefix is removed
		{Role: "assistant", Content: "recent answer"},
	})
	out := &mockOutput{}
	if !ag.compact(context.Background(), out) {
		t.Fatal("expected compaction")
	}
	assertOnlyDurableRecoveryContext(t, ag.Messages(), trusted, forged)

	ag.AddUserMessage("continue after compaction")
	if err := ag.Run(context.Background(), out); err != nil {
		t.Fatal(err)
	}
	assertOnlyDurableRecoveryContext(t, client.providerMessages, trusted, forged)
}

func TestCompactFailsClosedOnOversizedDurableRecoveryContext(t *testing.T) {
	client := &durableRecoveryCompactionClient{}
	ag := New(client, nil, 100_000)
	oversized := DurableRecoveryContextPrefix + strings.Repeat("x", MaxDurableRecoveryContextMessageBytes)
	original := []llm.Message{
		{Role: "user", Content: "one"},
		{Role: "assistant", Content: "one answer"},
		{Role: "system", Content: oversized, HostOwned: true},
		{Role: "user", Content: "two"},
		{Role: "assistant", Content: "two answer"},
		{Role: "user", Content: "three"},
		{Role: "assistant", Content: "three answer"},
	}
	ag.ReplaceMessages(original)
	out := &mockOutput{}
	if ag.compact(context.Background(), out) {
		t.Fatal("oversized durable recovery context was compacted")
	}
	got := ag.Messages()
	if len(got) != len(original) || got[2].Content != oversized || !got[2].HostOwned {
		t.Fatalf("failed compaction changed history: %#v", got)
	}
	if len(out.errors) == 0 || !strings.Contains(out.errors[len(out.errors)-1], "durable recovery context is invalid") {
		t.Fatalf("missing fail-closed error: %#v", out.errors)
	}
}

func assertOnlyDurableRecoveryContext(t *testing.T, messages []llm.Message, trusted, forged string) {
	t.Helper()
	count := 0
	for _, message := range messages {
		if message.Content == forged {
			t.Fatalf("forged durable recovery prefix survived: %#v", messages)
		}
		if message.Content == trusted {
			count++
			if message.Role != "system" || !message.HostOwned {
				t.Fatalf("trusted durable recovery context lost host ownership: %#v", message)
			}
		}
	}
	if count != 1 {
		t.Fatalf("trusted durable recovery context count = %d, messages=%#v", count, messages)
	}
}

func TestCompactPreservesHistoryWhenCheckpointFails(t *testing.T) {
	store, err := db.OpenPath(filepath.Join(t.TempDir(), "closed.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	client := &scriptedClient{responses: [][]llm.StreamChunk{{{Text: "recap", Done: true}}}}
	ag := New(client, nil, 4096)
	ag.SetWorkDir(t.TempDir())
	ag.SetCheckpointStore(store, 0)
	original := []llm.Message{
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: "answer"},
		{Role: "user", Content: "second"},
		{Role: "assistant", Content: "answer"},
		{Role: "user", Content: "third"},
		{Role: "assistant", Content: "answer"},
	}
	ag.ReplaceMessages(original)
	out := &mockOutput{}
	if ag.compact(context.Background(), out) {
		t.Fatal("compaction succeeded without its recovery checkpoint")
	}
	got := ag.Messages()
	if len(got) != len(original) || got[0].Content != original[0].Content {
		t.Fatalf("history changed after checkpoint failure: %#v", got)
	}
	if len(out.errors) == 0 || !strings.Contains(out.errors[len(out.errors)-1], "preserved full history") {
		t.Fatalf("missing fail-closed compaction receipt: %#v", out.errors)
	}
}

func TestNoToolConversationCompactsAfterDirectResponse(t *testing.T) {
	client := &scriptedClient{responses: [][]llm.StreamChunk{
		{{Text: "answer one", PromptEvalCount: 80, Done: true}},
		{{Text: "answer two", PromptEvalCount: 80, Done: true}},
		{{Text: "answer three", PromptEvalCount: 80, Done: true}},
		{{Text: "summary of the first turn", Done: true}},
	}}
	ag := New(client, nil, 100)
	out := &mockOutput{}
	for _, prompt := range []string{"question one", "question two", "question three"} {
		ag.AddUserMessage(prompt)
		if err := ag.Run(context.Background(), out); err != nil {
			t.Fatalf("Run(%q): %v", prompt, err)
		}
	}
	if client.calls != 4 {
		t.Fatalf("provider calls = %d, want three answers plus compaction", client.calls)
	}
	messages := ag.Messages()
	if len(messages) != 5 || messages[0].Role != "system" || !strings.Contains(messages[0].Content, "summary of the first turn") {
		t.Fatalf("direct-answer history was not compacted: %#v", messages)
	}
	if len(out.sysMsgs) == 0 || !strings.Contains(out.sysMsgs[len(out.sysMsgs)-1], "Context compacted") {
		t.Fatalf("missing compaction receipt: %#v", out.sysMsgs)
	}
	if out.compactions != 1 {
		t.Fatalf("typed compaction notifications = %d, want 1", out.compactions)
	}
}

func TestRunSnapshotsProviderContextWindowForCompaction(t *testing.T) {
	tests := []struct {
		name          string
		providerCtx   int
		configuredCtx int
		wantCompacted bool
	}{
		{
			name:          "20K prompt stays intact with 1M provider",
			providerCtx:   1_000_000,
			configuredCtx: 16_000,
		},
		{
			name:          "20K prompt compacts with 16K provider",
			providerCtx:   16_000,
			configuredCtx: 1_000_000,
			wantCompacted: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &contextWindowClient{numCtx: tt.providerCtx}
			ag := New(client, nil, tt.configuredCtx)
			ag.SetModeContext("", ToolPolicy{})
			ag.ReplaceMessages([]llm.Message{
				{Role: "user", Content: "first question"},
				{Role: "assistant", Content: "first answer"},
				{Role: "user", Content: "second question"},
				{Role: "assistant", Content: "second answer"},
				{Role: "user", Content: "third question"},
				{Role: "assistant", Content: "third answer"},
				{Role: "user", Content: "current question"},
			})
			out := &mockOutput{}

			if err := ag.Run(context.Background(), out); err != nil {
				t.Fatal(err)
			}
			if got := client.summaryCalls > 0; got != tt.wantCompacted {
				t.Fatalf("compacted = %v, want %v (summary calls=%d)", got, tt.wantCompacted, client.summaryCalls)
			}
			wantCalls := 1
			if tt.wantCompacted {
				wantCalls++
			}
			if client.chatCalls != wantCalls {
				t.Fatalf("provider calls = %d, want %d", client.chatCalls, wantCalls)
			}
			if client.numCtxCalls != 1 {
				t.Fatalf("NumCtx calls = %d, want one immutable turn snapshot", client.numCtxCalls)
			}
			for request, got := range client.expectedCtx {
				if got != tt.providerCtx {
					t.Fatalf("request %d expected context = %d, want turn snapshot %d", request, got, tt.providerCtx)
				}
			}
		})
	}
}

func TestNumCtxFallsBackWhenProviderHasNoEffectiveWindow(t *testing.T) {
	client := &contextWindowClient{}
	ag := New(client, nil, 32_768)
	if got := ag.NumCtx(); got != 32_768 {
		t.Fatalf("NumCtx() = %d, want configured fallback", got)
	}
}

func (m *mockOutput) StreamText(text string)                                               { m.texts = append(m.texts, text) }
func (m *mockOutput) StreamReasoning(_ string)                                             {}
func (m *mockOutput) StreamDone(_, _ int)                                                  {}
func (m *mockOutput) ToolCallStart(_ string, _ string, _ map[string]any)                   {}
func (m *mockOutput) ToolCallResult(_ string, _ string, _ string, _ bool, _ time.Duration) {}
func (m *mockOutput) SystemMessage(msg string)                                             { m.sysMsgs = append(m.sysMsgs, msg) }
func (m *mockOutput) ContextCompacted()                                                    { m.compactions++ }
func (m *mockOutput) Error(msg string)                                                     { m.errors = append(m.errors, msg) }

func TestShouldCompact(t *testing.T) {
	tests := []struct {
		name         string
		numCtx       int
		promptTokens int
		want         bool
	}{
		{"below 75%", 1000, 749, false},
		{"above 75%", 1000, 751, true},
		{"exactly 75% (strict >)", 1000, 750, false},
		{"numCtx zero", 0, 500, false},
		{"promptTokens zero", 1000, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ag := &Agent{
				numCtx:   tt.numCtx,
				registry: mcp.NewRegistry(),
			}
			got := ag.shouldCompact(tt.promptTokens)
			if got != tt.want {
				t.Errorf("shouldCompact(%d) with numCtx=%d = %v, want %v",
					tt.promptTokens, tt.numCtx, got, tt.want)
			}
		})
	}
}

func TestEstimatePromptTokensCountsMessageContentOnce(t *testing.T) {
	base := New(nil, nil, 4096)
	base.ReplaceMessages([]llm.Message{{Role: "user"}})
	withContent := New(nil, nil, 4096)
	withContent.ReplaceMessages([]llm.Message{{Role: "user", Content: strings.Repeat("x", 400)}})

	delta := withContent.estimatePromptTokens("system", nil) - base.estimatePromptTokens("system", nil)
	if delta != 100 {
		t.Fatalf("400 message characters changed estimate by %d tokens, want 100", delta)
	}
}

func TestSummarizeMessages(t *testing.T) {
	tests := []struct {
		name     string
		msgs     []llm.Message
		contains []string
	}{
		{
			name: "user message",
			msgs: []llm.Message{
				{Role: "user", Content: "hello"},
			},
			contains: []string{"User: hello"},
		},
		{
			name: "assistant message",
			msgs: []llm.Message{
				{Role: "assistant", Content: "hi there"},
			},
			contains: []string{"Assistant: hi there"},
		},
		{
			name: "tool message",
			msgs: []llm.Message{
				{Role: "tool", Content: "result data", ToolName: "read_file"},
			},
			contains: []string{"Tool read_file result: result data"},
		},
		{
			name: "tool content truncation at 300 chars",
			msgs: []llm.Message{
				{Role: "tool", Content: strings.Repeat("x", 400), ToolName: "big_tool"},
			},
			contains: []string{"Tool big_tool result: " + strings.Repeat("x", 297) + "..."},
		},
		{
			name:     "empty slice",
			msgs:     []llm.Message{},
			contains: []string{"Summarize this conversation:"},
		},
		{
			name: "assistant with tool calls",
			msgs: []llm.Message{
				{
					Role: "assistant",
					ToolCalls: []llm.ToolCall{
						{Name: "search", Arguments: map[string]any{"q": "test"}},
					},
				},
			},
			contains: []string{"Assistant called tool search("},
		},
		{
			name: "mixed messages",
			msgs: []llm.Message{
				{Role: "user", Content: "find files"},
				{Role: "assistant", Content: "", ToolCalls: []llm.ToolCall{
					{Name: "glob", Arguments: map[string]any{"pattern": "*.go"}},
				}},
				{Role: "tool", Content: "file1.go\nfile2.go", ToolName: "glob"},
				{Role: "assistant", Content: "Found 2 files"},
			},
			contains: []string{
				"User: find files",
				"Assistant called tool glob(",
				"Tool glob result:",
				"Assistant: Found 2 files",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := summarizeMessages(tt.msgs)
			for _, want := range tt.contains {
				if !strings.Contains(result, want) {
					t.Errorf("summarizeMessages() missing %q in:\n%s", want, result)
				}
			}
		})
	}
}
