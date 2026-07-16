package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/abdul-hamid-achik/local-agent/internal/config"
	ecosystemPkg "github.com/abdul-hamid-achik/local-agent/internal/ecosystem"
	executionPkg "github.com/abdul-hamid-achik/local-agent/internal/execution"
	"github.com/abdul-hamid-achik/local-agent/internal/expertteam"
	"github.com/abdul-hamid-achik/local-agent/internal/llm"
	mcpPkg "github.com/abdul-hamid-achik/local-agent/internal/mcp"
)

const maxToolCallsPerResponse = 64

var (
	ErrTurnEvalBudgetExhausted   = errors.New("turn evaluation-token budget exhausted")
	ErrTurnContextBudgetExceeded = errors.New("turn context budget exceeded")
	ErrMalformedToolLoop         = errors.New("model repeatedly returned malformed tool requests")
	ErrRepeatedHostRefusal       = errors.New("model repeatedly submitted an identical request refused by the approval host")
)

const maxIdenticalHostRefusals = 2

func prependContinuationContext(base, continuation string) string {
	if continuation == "" {
		return base
	}
	if base == "" {
		return continuation
	}
	return continuation + "\n\n" + base
}

// RepeatedHostRefusalError stops an impossible approval loop without
// misreporting the host's refusal as a user denial.
type RepeatedHostRefusalError struct {
	ToolName      string
	ArgumentsHash string
	Code          string
	Attempts      int
}

func (e *RepeatedHostRefusalError) Error() string {
	if e == nil {
		return ErrRepeatedHostRefusal.Error()
	}
	return fmt.Sprintf("%v: tool=%q arguments=%s code=%q attempts=%d; change the request or approval renderer before retrying",
		ErrRepeatedHostRefusal, e.ToolName, e.ArgumentsHash, e.Code, e.Attempts)
}

func (e *RepeatedHostRefusalError) Unwrap() error { return ErrRepeatedHostRefusal }

// TurnContextBudgetError reports that a bounded turn cannot safely make its
// next provider request without exceeding the active model's context window.
// Bounded turns may not launch an unaccounted summarization generation, so the
// host must compact or replace history before retrying.
type TurnContextBudgetError struct {
	EstimatedPromptTokens int
	ContextWindowTokens   int
}

func (e *TurnContextBudgetError) Error() string {
	if e == nil {
		return ErrTurnContextBudgetExceeded.Error()
	}
	return fmt.Sprintf(
		"%v: estimated prompt uses %d of %d tokens; compact history or start a new conversation, then retry the bounded turn",
		ErrTurnContextBudgetExceeded, e.EstimatedPromptTokens, e.ContextWindowTokens,
	)
}

func (e *TurnContextBudgetError) Unwrap() error {
	return ErrTurnContextBudgetExceeded
}

// TurnLimits are hard, per-turn provider limits supplied by a host scheduler.
// Zero leaves a dimension unlimited. Goal Runtime passes only its remaining
// budget, so each provider iteration and every later tool dispatch fail closed.
type TurnLimits struct {
	MaxEvalTokens int64
	// Deadline is the immutable host admission deadline. Hosts that own an
	// absolute wall budget should set this instead of converting the deadline to
	// a duration before routing, persistence, or command scheduling work.
	Deadline time.Time
	// MaxWallTime is retained for callers that only have a relative per-turn
	// timeout. When both fields are set, the earlier deadline wins.
	MaxWallTime time.Duration
}

// TurnOptions binds hard limits and one optional host-owned capability
// activity to the same admitted turn. Keeping this data per-call avoids a
// mutable "next turn" setter surviving a cancelled preflight.
type TurnOptions struct {
	Limits       TurnLimits
	Capability   CapabilityActivity
	Continuation *ContinuationContext
}

func (limits TurnLimits) validate() error {
	if limits.MaxEvalTokens < 0 || limits.MaxWallTime < 0 {
		return fmt.Errorf("turn limits must not be negative")
	}
	return nil
}

func (limits TurnLimits) bounded() bool {
	return limits.MaxEvalTokens > 0 || !limits.Deadline.IsZero() || limits.MaxWallTime > 0
}

func (limits TurnLimits) effectiveDeadline(now time.Time) (time.Time, bool) {
	deadline := limits.Deadline
	if limits.MaxWallTime > 0 {
		relative := now.Add(limits.MaxWallTime)
		if deadline.IsZero() || relative.Before(deadline) {
			deadline = relative
		}
	}
	return deadline, !deadline.IsZero()
}

// Run executes the ReAct loop: query -> LLM -> tool calls -> observe -> repeat.
// It streams output via the Output interface and returns a terminal error for
// headless callers and automation.
func (a *Agent) Run(ctx context.Context, out Output) error {
	turnID, err := executionPkg.NewTurnID()
	if err != nil {
		out.Error(fmt.Sprintf("execution turn identity: %v", err))
		return fmt.Errorf("execution turn identity: %w", err)
	}
	return a.RunTurn(ctx, out, turnID)
}

// RunTurn executes one ReAct turn under a caller-supplied durable identity.
// Goal Runtime consumes a continuation permit before dispatch, so the host,
// execution ledger, and settled goal receipt must share this exact ID. Callers
// that do not need correlation should use Run.
func (a *Agent) RunTurn(ctx context.Context, out Output, turnID string) error {
	return a.RunTurnWithOptions(ctx, out, turnID, TurnOptions{})
}

// RunTurnWithLimits executes one turn with host-owned hard generation and wall
// limits. It preserves RunTurn's durable identity and execution-ledger rules.
func (a *Agent) RunTurnWithLimits(ctx context.Context, out Output, turnID string, limits TurnLimits) error {
	return a.RunTurnWithOptions(ctx, out, turnID, TurnOptions{Limits: limits})
}

// RunTurnWithOptions executes one turn with a single immutable host-owned
// options snapshot.
func (a *Agent) RunTurnWithOptions(ctx context.Context, out Output, turnID string, options TurnOptions) error {
	limits := options.Limits
	if strings.TrimSpace(turnID) == "" || len(turnID) > executionPkg.MaxTurnIDBytes || !utf8.ValidString(turnID) {
		err := fmt.Errorf("execution turn identity is invalid")
		out.Error(err.Error())
		return err
	}
	if err := limits.validate(); err != nil {
		out.Error(err.Error())
		return err
	}
	a.turnMu.Lock()
	if a.closed {
		a.turnMu.Unlock()
		err := fmt.Errorf("agent is closed")
		out.Error(err.Error())
		return err
	}
	if !a.turnRunning.CompareAndSwap(false, true) {
		a.turnMu.Unlock()
		err := fmt.Errorf("another agent turn is already running")
		out.Error(err.Error())
		return err
	}
	turnFilesystem := a.pinTurnFilesystem()
	turnCtx, turnCancel := context.WithCancel(ctx)
	limitCancel := func() {}
	if deadline, ok := limits.effectiveDeadline(time.Now()); ok {
		turnCtx, limitCancel = context.WithDeadline(turnCtx, deadline)
	}
	turnDone := make(chan struct{})
	a.turnCancel = turnCancel
	a.turnDone = turnDone
	a.turnMu.Unlock()
	ctx = turnCtx
	defer func() {
		limitCancel()
		turnCancel()
		close(turnDone)
		a.turnMu.Lock()
		if a.turnDone == turnDone {
			a.turnCancel = nil
			a.turnDone = nil
		}
		a.unpinTurnFilesystem()
		a.turnRunning.Store(false)
		a.turnMu.Unlock()
	}()
	// Transient structured results exist only for provider iterations inside
	// this turn. Settle them before RunTurn returns so a future user turn,
	// checkpoint, or cursor projection can observe only the durable receipts.
	defer a.settleTransientMessages()
	defer a.mcphubResults.Reset()
	defer a.clearBobStoredAdmissions()
	defer a.clearContinuationContracts()
	// A provider such as ModelManager may expose a different context window for
	// each selected model. Resolve it exactly once so every budget decision in
	// this turn stays coherent even if selection changes concurrently.
	turnNumCtx := a.NumCtx()
	authorityMode := a.AuthorityMode()
	modePrefix, turnToolPolicy := a.modeContext()
	execRuntime, err := a.executionRuntime(ctx)
	if err != nil {
		out.Error(err.Error())
		return err
	}
	if a.iceEngine != nil {
		if limits.bounded() {
			// A bounded turn must not overlap an optional provider generation that
			// was launched by the previous turn. Cancel and join it before the main
			// request; the absolute deadline already covers this admission work.
			a.iceEngine.StopAutoMemory()
		} else {
			a.iceEngine.CancelAutoMemory()
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}

	var tools []llm.ToolDef
	var turnMCPSnapshot mcpPkg.ToolSnapshot
	if turnToolPolicy.AllowMCP && a.registry != nil {
		turnMCPSnapshot = a.mcpToolSnapshot()
		tools = append(tools, turnMCPSnapshot.Tools...)
	}
	// Merge memory built-in tools if available.
	if a.memoryStore != nil {
		tools = append(tools, filterToolDefsByName(a.memoryBuiltinToolDefs(), turnToolPolicy.memoryTools)...)
	}
	// Merge built-in file tools according to the active mode policy.
	tools = append(tools, filterToolDefsByName(a.toolsBuiltinToolDefs(), turnToolPolicy.localTools)...)
	skillCatalog := a.skillCatalogPrompt()
	continuationsConfig := a.continuationsConfigSnapshot()
	bobCandidate, bobContextCached := a.reconcileBobWorkspaceContext(out)
	var bobBootstrap bobBootstrapPlan
	var bobBootstrapAvailable bool
	if turnToolPolicy.AllowMCP && !bobContextCached && continuationsConfig.Mode != config.ContinuationOff {
		bobBootstrap, bobBootstrapAvailable = a.planBobWorkspaceBootstrap(bobCandidate, turnMCPSnapshot)
	}

	// ICE: index user message and assemble cross-session context.
	var iceContext string
	a.mu.RLock()
	hasMessages := len(a.messages) > 0
	var lastMsg llm.Message
	if hasMessages {
		lastMsg = a.messages[len(a.messages)-1]
	}
	a.mu.RUnlock()

	capabilityActivity := options.Capability
	if !capabilityActivity.NonTrivial && hasMessages && lastMsg.Role == "user" {
		capabilityActivity = CapabilityActivityFromPrompt(
			capabilityScopeID(execRuntime.sessionID, turnID), lastMsg.Content,
			capabilityPhaseForAuthority(authorityMode), strings.TrimSpace(turnFilesystem.workDir) != "",
		)
	}
	capabilityHintText, capabilityHint := a.resolveTurnCapabilityWithPolicy(ctx, out, capabilityActivity, turnToolPolicy.AllowMCP)
	if err := ctx.Err(); err != nil {
		return err
	}

	if a.iceEngine != nil && hasMessages {
		if lastMsg.Role == "user" {
			if err := a.iceEngine.IndexMessage(ctx, "user", lastMsg.Content); err != nil {
				out.Error(fmt.Sprintf("ICE indexing failed: %v", err))
			}
			if assembled, err := a.iceEngine.AssembleContext(ctx, lastMsg.Content); err == nil {
				iceContext = assembled
			}
		}
	}

	capabilityBaseContextWithoutBob := a.loadedCtx
	if turnToolPolicy.AllowMCP {
		if guidance := a.mcpServerGuidance(); guidance != "" {
			if capabilityBaseContextWithoutBob != "" {
				guidance += "\n\n"
			}
			capabilityBaseContextWithoutBob = guidance + capabilityBaseContextWithoutBob
		}
	}
	capabilityBaseContext := capabilityBaseContextWithoutBob
	if bobContext := a.bobWorkspaceContextPrompt(); bobContext != "" {
		capabilityBaseContext = prependContinuationContext(capabilityBaseContext, bobContext)
	}
	baseLoadedContext := composeCapabilityContext(capabilityHintText, capabilityBaseContext)
	bobBootstrapPreview := ""
	if bobBootstrapAvailable {
		bobBootstrapPreview = bobWorkspaceBootstrapHint(bobBootstrap)
		baseLoadedContext = prependContinuationContext(baseLoadedContext, bobBootstrapPreview)
	}
	loadedContext := baseLoadedContext
	var continuationPreview string
	if turnToolPolicy.AllowMCP && continuationsConfig.Mode != config.ContinuationOff &&
		(continuationsConfig.Mode != config.ContinuationAutoReadOnly || authorityMode != AuthorityAutoScoped) {
		continuationPreview = a.previewContinuationContext(options.Continuation)
		loadedContext = prependContinuationContext(baseLoadedContext, continuationPreview)
	}
	readGrants := a.ReadGrants()
	var system string
	rebuildSystem := func() {
		system = buildSystemPromptForModelBudgetContextWithSkillCatalogAndReadGrants(
			ctx, modePrefix, tools, a.skillContent, skillCatalog, loadedContext,
			a.memoryStore, iceContext, turnFilesystem.workDir, turnFilesystem.ignoreContent,
			a.llmClient.Model(), turnNumCtx, readGrants,
		)
	}
	rebuildSystem()

	const maxRetries = 2
	var lastPromptTokens int
	var lastEvalTokens int
	var totalEvalTokens int64
	var retryCount int
	var malformedToolIterations int
	var capabilityReroutes int
	var expertConsultations int
	hostRefusalCounts := make(map[string]int)
	continuationState := newContinuationTurnState(turnMCPSnapshot.Epoch)
	var autoContinuationState *autoContinuationState
	if turnToolPolicy.AllowMCP && authorityMode == AuthorityAutoScoped &&
		continuationsConfig.Mode == config.ContinuationAutoReadOnly {
		autoContinuationState = newAutoContinuationState(turnMCPSnapshot.Epoch, continuationsConfig.MaxAutoSteps)
	}
	var queuedAutoContinuation *preparedAutoContinuation

	maxIters := a.MaxIterationsForAuthority(authorityMode)

	// A process-independent turn ID threads through logs and every durable tool
	// execution identity for this turn.
	lg := a.logTurn(turnID)
	turnStart := time.Now()
	if lg != nil {
		lg.Info("turn start", "model", a.llmClient.Model(), "num_ctx", turnNumCtx, "tools", len(tools), "max_iters", maxIters)
	}

	// Compact before the next provider request as well as after responses. This
	// protects direct-answer conversations, which may never enter the tool loop,
	// from submitting an already oversized history to Ollama. Bounded turns may
	// not spend an untracked provider generation on compaction, so fail before the
	// first provider request with an explicit recovery path instead.
	admitSystemPrompt := func() error {
		estimated := a.estimatePromptTokens(system, tools)
		if !shouldCompactForContext(estimated, turnNumCtx) {
			return nil
		}
		if limits.bounded() {
			err := &TurnContextBudgetError{
				EstimatedPromptTokens: estimated,
				ContextWindowTokens:   turnNumCtx,
			}
			if lg != nil {
				lg.Warn("context admission denied", "prompt_tokens", estimated, "num_ctx", turnNumCtx, "bounded", true)
			}
			out.Error(err.Error())
			return err
		}
		if lg != nil {
			lg.Info("compaction", "phase", "before_request", "prompt_tokens", estimated, "num_ctx", turnNumCtx)
		}
		if a.compactForContext(ctx, out, turnNumCtx) {
			rebuildSystem()
		}
		return nil
	}
	if err := admitSystemPrompt(); err != nil {
		return err
	}

	// The preview above is non-consuming. Commit it only after context admission
	// succeeds; if another caller consumed or invalidated it concurrently, remove
	// the preview before the provider can observe stale host context.
	if continuationPreview != "" {
		committed := a.continuationContextText(options.Continuation)
		if committed != continuationPreview {
			loadedContext = prependContinuationContext(baseLoadedContext, committed)
			rebuildSystem()
		}
	}

	// Claim an opaque host continuation only after prompt admission succeeds, so
	// a context-budget failure cannot burn its one-shot capability. If the exact
	// action no longer qualifies for automatic read-only execution, preserve the
	// existing suggestion path and rebuild the admitted prompt around that
	// bounded model context.
	if autoContinuationState != nil {
		// An initial host read still needs one later provider iteration to
		// interpret its result. With no room, retain the LA-2 suggestion path.
		if maxIters >= 2 {
			queuedAutoContinuation = a.claimAutoReadOnlyContinuationContextWithSnapshot(
				options.Continuation, turnMCPSnapshot, authorityMode, autoContinuationState,
			)
		}
		if queuedAutoContinuation == nil {
			continuationPreview = a.previewContinuationContext(options.Continuation)
			if continuationPreview != "" {
				loadedContext = prependContinuationContext(baseLoadedContext, continuationPreview)
				rebuildSystem()
				if err := admitSystemPrompt(); err != nil {
					return err
				}
				committed := a.continuationContextText(options.Continuation)
				if committed != continuationPreview {
					loadedContext = prependContinuationContext(baseLoadedContext, committed)
					rebuildSystem()
				}
			}
		}
		if queuedAutoContinuation == nil && bobBootstrapAvailable && maxIters >= 2 && !limits.bounded() {
			queuedAutoContinuation = a.prepareBobWorkspaceBootstrap(
				bobBootstrap, turnMCPSnapshot, authorityMode, autoContinuationState,
			)
			if queuedAutoContinuation != nil && bobBootstrapPreview != "" {
				// The host-owned read is already queued. Remove the suggestion from
				// the provider prompt so the model cannot repeat it after receiving
				// the exact result.
				baseLoadedContext = composeCapabilityContext(capabilityHintText, capabilityBaseContext)
				loadedContext = baseLoadedContext
				rebuildSystem()
				if err := admitSystemPrompt(); err != nil {
					return err
				}
			}
		}
	}
	if bobBootstrapAvailable && queuedAutoContinuation == nil {
		emitContinuationSuggestion(out, turnID, 1, &ValidatedContinuation{
			Tool: "bob_context", ReasonCode: "workspace_context",
		})
	}

	for i := 0; i < maxIters; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		remainingEvalTokens := int64(0)
		requestEvalLimit := 0
		if limits.MaxEvalTokens > 0 {
			remainingEvalTokens = limits.MaxEvalTokens - totalEvalTokens
			if remainingEvalTokens <= 0 {
				return fmt.Errorf("%w: used %d of %d", ErrTurnEvalBudgetExhausted, totalEvalTokens, limits.MaxEvalTokens)
			}
			requestEvalLimit = boundedEvalLimit(remainingEvalTokens)
		}

		// Stream LLM response.
		var textBuf strings.Builder
		var toolCalls []llm.ToolCall
		hostContinuationBatch := queuedAutoContinuation != nil
		activeAutoContinuation := queuedAutoContinuation
		lastEvalTokens = 0
		doneSeen := false
		callbackSeen := false
		reportedEvalTokens := int64(0)

		if hostContinuationBatch {
			toolCalls = []llm.ToolCall{queuedAutoContinuation.detachedCall()}
			queuedAutoContinuation = nil
		} else {
			llmStart := time.Now()
			// Snapshot the message history under the lock: ChatStream runs while
			// other goroutines may AppendMessage, and passing the live slice would
			// race on the backing array (and could realloc mid-stream).
			a.mu.RLock()
			msgsSnapshot := make([]llm.Message, len(a.messages))
			copy(msgsSnapshot, a.messages)
			a.mu.RUnlock()

			err := a.chatStreamWithResolvedImages(ctx, llm.ChatOptions{
				Messages:        msgsSnapshot,
				Tools:           tools,
				System:          system,
				MaxEvalTokens:   requestEvalLimit,
				ExpectedContext: turnNumCtx,
			}, func(chunk llm.StreamChunk) error {
				callbackSeen = true
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
				}
				if doneSeen {
					return fmt.Errorf("provider streamed data after its terminal usage receipt")
				}

				if chunk.Text != "" {
					textBuf.WriteString(chunk.Text)
					out.StreamText(chunk.Text)
				}
				if chunk.Reasoning != "" {
					out.StreamReasoning(chunk.Reasoning)
				}
				if len(chunk.ToolCalls) > 0 {
					if len(chunk.ToolCalls) > maxToolCallsPerResponse-len(toolCalls) {
						return fmt.Errorf("model streamed at least %d tool calls; maximum per response is %d", len(toolCalls)+len(chunk.ToolCalls), maxToolCallsPerResponse)
					}
					toolCalls = append(toolCalls, chunk.ToolCalls...)
				}
				if chunk.Done {
					if chunk.EvalCount < 0 || chunk.PromptEvalCount < 0 {
						return fmt.Errorf("invalid provider token receipt: eval=%d prompt=%d", chunk.EvalCount, chunk.PromptEvalCount)
					}
					if int64(chunk.EvalCount) > math.MaxInt64-totalEvalTokens {
						return fmt.Errorf("provider evaluation-token receipt %d overflows the parent turn counter", chunk.EvalCount)
					}
					doneSeen = true
					lastPromptTokens = chunk.PromptEvalCount
					lastEvalTokens = chunk.EvalCount
					reportedEvalTokens = int64(chunk.EvalCount)
					out.StreamDone(chunk.EvalCount, chunk.PromptEvalCount)
				}
				return nil
			})

			if err != nil {
				if errors.Is(err, llm.ErrInferenceNotStarted) && !callbackSeen {
					if lg != nil {
						lg.Warn("llm request rejected before dispatch", "iter", i, "err", err)
					}
					out.Error(fmt.Sprintf("LLM request not started: %v", err))
					return err
				}
				reservedEvalTokens := int64(0)
				if (!errors.Is(err, llm.ErrNoModelSelected) && !errors.Is(err, llm.ErrInferenceNotStarted)) || callbackSeen {
					reservedEvalTokens = chargeUnknownEvalReservation(out, requestEvalLimit, reportedEvalTokens)
				}
				var reservationErr error
				if reservedEvalTokens > 0 {
					reservationErr = fmt.Errorf(
						"%w: provider stream ended without a trustworthy terminal usage receipt; conservatively charged %d reserved token(s)",
						ErrTurnEvalBudgetExhausted, reservedEvalTokens,
					)
				}
				if ctx.Err() != nil {
					return errors.Join(ctx.Err(), reservationErr)
				}
				if reservationErr != nil {
					out.Error(reservationErr.Error())
					return errors.Join(reservationErr, fmt.Errorf("LLM response: %w", err))
				}
				// Retry on transient JSON parse errors from small models.
				if limits.MaxEvalTokens == 0 && retryCount < maxRetries && isRetryableError(err) {
					retryCount++
					if lg != nil {
						lg.Warn("llm retry", "iter", i, "attempt", retryCount, "err", err)
					}
					out.Error(fmt.Sprintf("LLM produced malformed output, retrying (%d/%d)...", retryCount, maxRetries))
					textBuf.Reset()
					toolCalls = nil
					continue
				}
				if lg != nil {
					lg.Error("llm error", "iter", i, "err", err)
				}
				// Show error and provide a fallback response
				out.Error(fmt.Sprintf("LLM error: %v", err))
				// Send a system message explaining the error
				out.SystemMessage(fmt.Sprintf("⚠️ Model response failed: %v\n\nYou can try:\n- Checking if Ollama is running (`ollama ps`)\n- Switching to a different model (ctrl+o)\n- Reducing context size\n\nTool results are still available above.", err))
				return fmt.Errorf("LLM response: %w", err)
			}
			if !doneSeen {
				reservedEvalTokens := chargeUnknownEvalReservation(out, requestEvalLimit, 0)
				receiptErr := fmt.Errorf("provider stream ended without a terminal usage receipt")
				if reservedEvalTokens > 0 {
					budgetErr := fmt.Errorf(
						"%w: provider stream ended without a terminal usage receipt; conservatively charged %d reserved token(s)",
						ErrTurnEvalBudgetExhausted, reservedEvalTokens,
					)
					out.Error(budgetErr.Error())
					return errors.Join(budgetErr, receiptErr)
				}
				out.Error(receiptErr.Error())
				return receiptErr
			}
			retryCount = 0 // reset on success
			if lastEvalTokens < 0 || int64(lastEvalTokens) > math.MaxInt64-totalEvalTokens {
				return fmt.Errorf("invalid provider evaluation-token receipt %d", lastEvalTokens)
			}
			totalEvalTokens += int64(lastEvalTokens)
			if lg != nil {
				lg.Info("llm response", "iter", i, "ms", time.Since(llmStart).Milliseconds(),
					"prompt_tokens", lastPromptTokens, "eval_tokens", lastEvalTokens, "tool_calls", len(toolCalls))
			}
		}
		if len(toolCalls) > maxToolCallsPerResponse {
			err := fmt.Errorf("model returned %d tool calls; maximum per response is %d", len(toolCalls), maxToolCallsPerResponse)
			out.Error(err.Error())
			return err
		}
		if limits.MaxEvalTokens > 0 && totalEvalTokens > limits.MaxEvalTokens {
			// A provider that violates num_predict has already crossed the requested
			// generation boundary. Preserve only its text and stop before creating
			// any durable execution intents.
			a.AppendMessage(llm.Message{Role: "assistant", Content: textBuf.String()})
			err := fmt.Errorf("%w: provider reported %d used token(s) for a %d-token limit", ErrTurnEvalBudgetExhausted, totalEvalTokens, limits.MaxEvalTokens)
			out.Error(err.Error())
			return err
		}
		if limits.MaxEvalTokens > 0 && totalEvalTokens == limits.MaxEvalTokens && len(toolCalls) > 0 {
			// The provider may return tool requests on the response that consumes
			// the final token allowance. Keep its text, but never create durable
			// dispatch intents or execute effects after the hard boundary.
			a.AppendMessage(llm.Message{Role: "assistant", Content: textBuf.String()})
			err := fmt.Errorf("%w before tool dispatch: used %d of %d", ErrTurnEvalBudgetExhausted, totalEvalTokens, limits.MaxEvalTokens)
			out.Error(err.Error())
			return err
		}
		var providerCallIDs []string
		if !hostContinuationBatch {
			providerCallIDs = make([]string, len(toolCalls))
			for toolIndex := range toolCalls {
				providerCallIDs[toolIndex] = toolCalls[toolIndex].ID
			}
		}
		a.ensureToolCallIDs(toolCalls, turnID, i+1)
		var trackedExecutions []trackedToolExecution
		var err error
		if hostContinuationBatch {
			trackedExecutions, err = a.newTrackedContinuationExecutions(ctx, execRuntime, turnID, i+1, toolCalls)
		} else {
			trackedExecutions, err = a.newTrackedExecutions(ctx, execRuntime, turnID, i+1, toolCalls, providerCallIDs)
		}
		if err != nil {
			out.Error(fmt.Sprintf("record requested tool execution: %v", err))
			return fmt.Errorf("record requested tool execution: %w", err)
		}

		// Record assistant message in conversation history.
		assistantToolCalls := toolCalls
		if hostContinuationBatch {
			assistantToolCalls = []llm.ToolCall{activeAutoContinuation.detachedCall()}
			assistantToolCalls[0].ID = toolCalls[0].ID
		}
		assistantMsg := llm.Message{
			Role:      "assistant",
			Content:   textBuf.String(),
			ToolCalls: assistantToolCalls,
			HostOwned: hostContinuationBatch,
		}
		a.AppendMessage(assistantMsg)

		// ICE: index assistant message.
		if a.iceEngine != nil && assistantMsg.Content != "" {
			if err := a.iceEngine.IndexMessage(ctx, "assistant", assistantMsg.Content); err != nil {
				out.Error(fmt.Sprintf("ICE indexing failed: %v", err))
			}
		}

		// If no tool calls, we're done.
		if len(toolCalls) == 0 {
			// ICE: detect auto-memories from the exchange.
			a.mu.RLock()
			hasEnoughMessages := len(a.messages) >= 2
			var userContent string
			if hasEnoughMessages {
				for idx := len(a.messages) - 2; idx >= 0; idx-- {
					if a.messages[idx].Role == "user" {
						userContent = a.messages[idx].Content
						break
					}
				}
			}
			a.mu.RUnlock()

			if !limits.bounded() && a.iceEngine != nil && hasEnoughMessages && userContent != "" {
				a.iceEngine.DetectAutoMemory(ctx, userContent, assistantMsg.Content)
			}
			estimatedPromptTokens := a.estimatePromptTokens(system, tools)
			if estimatedPromptTokens < lastPromptTokens {
				estimatedPromptTokens = lastPromptTokens
			}
			if !limits.bounded() && shouldCompactForContext(estimatedPromptTokens, turnNumCtx) {
				if lg != nil {
					lg.Info("compaction", "phase", "direct_response", "prompt_tokens", estimatedPromptTokens, "num_ctx", turnNumCtx)
				}
				a.compactForContext(ctx, out, turnNumCtx)
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if lg != nil {
				lg.Info("turn end", "reason", "complete", "iters", i+1, "ms", time.Since(turnStart).Milliseconds())
			}
			return nil
		}

		// Execute tool calls in provider order. Requested/approval/dispatch and
		// terminal transitions are committed synchronously around the backend.
		preflightRejections := 0
		capabilityRouteFailed := false
		for toolIndex, tc := range toolCalls {
			tracked := &trackedExecutions[toolIndex]
			if ctxErr := ctx.Err(); ctxErr != nil {
				if ledgerErr := a.cancelTrackedToolCalls(ctx, execRuntime, trackedExecutions[toolIndex:], toolCalls[toolIndex:], out, ctxErr); ledgerErr != nil {
					return ledgerErr
				}
				return ctxErr
			}

			kind := tracked.identity.Kind
			requiresApproval := true

			// Classify policy/scope before hooks. Hooks may normalize arguments but
			// may not change identity; final arguments are hashed and approved below.
			switch kind {
			case executionPkg.KindMemory:
				if !turnToolPolicy.AllowsMemory(tc.Name) {
					if err := a.appendTerminalExecutionEvent(ctx, execRuntime, *tracked, executionEvent(*tracked, executionPkg.EventDenied, executionPkg.ApprovalPolicyDenied, "", "blocked by active mode")); err != nil {
						return a.stopBeforeDispatchAfterLedgerError(toolCalls[toolIndex:], out, err)
					}
					a.blockedToolCall(tc, out)
					continue
				}
				requiresApproval = memoryToolRequiresApproval(tc.Name)
			case executionPkg.KindBuiltin:
				if !turnToolPolicy.AllowsBuiltin(tc.Name) {
					if err := a.appendTerminalExecutionEvent(ctx, execRuntime, *tracked, executionEvent(*tracked, executionPkg.EventDenied, executionPkg.ApprovalPolicyDenied, "", "blocked by active mode")); err != nil {
						return a.stopBeforeDispatchAfterLedgerError(toolCalls[toolIndex:], out, err)
					}
					a.blockedToolCall(tc, out)
					continue
				}
				requiresApproval = builtinToolRequiresApproval(tc.Name)
			case executionPkg.KindMCP:
				if !turnToolPolicy.AllowMCP {
					if err := a.appendTerminalExecutionEvent(ctx, execRuntime, *tracked, executionEvent(*tracked, executionPkg.EventDenied, executionPkg.ApprovalPolicyDenied, "", "MCP blocked by active mode")); err != nil {
						return a.stopBeforeDispatchAfterLedgerError(toolCalls[toolIndex:], out, err)
					}
					a.blockedToolCall(tc, out)
					continue
				}
				if !a.allowsMCPTool(tc.Name) {
					if err := a.appendTerminalExecutionEvent(ctx, execRuntime, *tracked, executionEvent(*tracked, executionPkg.EventDenied, executionPkg.ApprovalPolicyDenied, "", "blocked by active agent profile MCP scope")); err != nil {
						return a.stopBeforeDispatchAfterLedgerError(toolCalls[toolIndex:], out, err)
					}
					a.deniedToolCall(tc, out, "tool call blocked by active agent profile MCP scope")
					continue
				}
				// MCP annotations are untrusted display metadata. All MCP calls
				// remain on the normal authorization path so explicit policy and
				// Skip-approval decisions retain their durable audit reason.
				requiresApproval = mcpToolRequiresApproval()
			}

			originalID, originalName := tc.ID, tc.Name
			hookCall := tc
			hookCall.Arguments = cloneApprovalArguments(tc.Arguments)
			block, reason := a.runPreHooks(ctx, &hookCall)
			if hookCall.ID != originalID || hookCall.Name != originalName {
				hookCall.ID, hookCall.Name = originalID, originalName
				block = true
				reason = "tool hook attempted to change approved tool identity"
			}
			// A hook may retain the pointer or argument map it received. Detach the
			// effective call immediately after the synchronous hook boundary so later
			// mutation cannot alter the hash, approval, UI, or backend dispatch.
			tc = hookCall
			tc.Arguments = cloneApprovalArguments(hookCall.Arguments)
			effectiveKind, effectiveEffect := a.executionKindForCall(tc)
			if effectiveKind != tracked.identity.Kind || effectiveEffect != tracked.identity.EffectClass {
				block = true
				reason = "tool hook attempted to change durable execution effect"
			}
			effectiveHash, hashErr := executionPkg.HashCanonicalArguments(tc.Arguments)
			if hashErr != nil {
				reason := capToolResultForContext(fmt.Sprintf("invalid effective tool arguments: %v", hashErr), turnNumCtx)
				if err := a.appendTerminalExecutionEvent(ctx, execRuntime, *tracked, executionEvent(*tracked, executionPkg.EventFailed, executionPkg.ApprovalNotApplicable, reason, "effective argument hashing failed")); err != nil {
					return a.stopBeforeDispatchAfterLedgerError(toolCalls[toolIndex:], out, err)
				}
				a.failedToolCall(tc, out, reason, turnNumCtx)
				continue
			}
			tracked.effectiveHash = effectiveHash
			autoContinuationStillEligible := func() bool {
				if !hostContinuationBatch || activeAutoContinuation == nil ||
					activeAutoContinuation.registryEpoch != turnMCPSnapshot.Epoch ||
					activeAutoContinuation.authorityVersion != a.approvalStateSnapshot().hostVersion ||
					!a.continuationFreshnessCurrent(
						&activeAutoContinuation.continuation, activeAutoContinuation.freshnessSequence,
					) ||
					!a.autoReadOnlyContinuationEligible(&activeAutoContinuation.continuation, turnMCPSnapshot, authorityMode) {
					return false
				}
				currentHash, err := executionPkg.HashCanonicalArguments(tc.Arguments)
				return err == nil && currentHash == tracked.originalHash
			}
			if hostContinuationBatch && !autoContinuationStillEligible() {
				block = true
				reason = "host-scheduled continuation changed or lost read-only authority before dispatch"
			}
			toolCalls[toolIndex] = tc
			if block {
				reason = capToolResultForContext(reason, turnNumCtx)
				if err := a.appendTerminalExecutionEvent(ctx, execRuntime, *tracked, executionEvent(*tracked, executionPkg.EventFailed, executionPkg.ApprovalHostRefused, reason, "host pre-tool hook refused request")); err != nil {
					return a.stopBeforeDispatchAfterLedgerError(toolCalls[toolIndex:], out, err)
				}
				a.failedToolCall(tc, out, reason, turnNumCtx)
				if ctxErr := ctx.Err(); ctxErr != nil {
					if ledgerErr := a.cancelTrackedToolCalls(ctx, execRuntime, trackedExecutions[toolIndex+1:], toolCalls[toolIndex+1:], out, ctxErr); ledgerErr != nil {
						return ledgerErr
					}
					return ctxErr
				}
				continue
			}
			if ctxErr := ctx.Err(); ctxErr != nil {
				if ledgerErr := a.cancelTrackedToolCalls(ctx, execRuntime, trackedExecutions[toolIndex:], toolCalls[toolIndex:], out, ctxErr); ledgerErr != nil {
					return ledgerErr
				}
				return ctxErr
			}

			var preflightErr error
			if tc.Name == "consult_experts" && expertConsultations >= 1 {
				preflightErr = errors.New("consult_experts may be dispatched at most once per parent turn")
			} else {
				preflightErr = a.preflightToolCall(kind, tc)
			}
			if preflightErr != nil {
				preflightRejections++
				result := capToolResultForContext(fmt.Sprintf("tool request failed preflight: %v", preflightErr), turnNumCtx)
				if err := a.appendTerminalExecutionEvent(ctx, execRuntime, *tracked, executionEvent(*tracked, executionPkg.EventFailed, executionPkg.ApprovalNotApplicable, result, "preflight rejected request before dispatch")); err != nil {
					return a.stopBeforeDispatchAfterLedgerError(toolCalls[toolIndex:], out, err)
				}
				a.failedToolCall(tc, out, result, turnNumCtx)
				continue
			}

			autoApproved := requiresApproval &&
				a.authorityAutoApproves(authorityMode, tc, tracked.identity.Kind)
			if autoApproved {
				requiresApproval = false
			}
			if hostContinuationBatch && (!autoApproved || !autoContinuationStillEligible()) {
				result := "host-scheduled continuation no longer qualifies for automatic read-only authorization"
				if err := a.appendTerminalExecutionEvent(ctx, execRuntime, *tracked, executionEvent(*tracked, executionPkg.EventDenied, executionPkg.ApprovalPolicyDenied, result, "automatic continuation denied before dispatch")); err != nil {
					return a.stopBeforeDispatchAfterLedgerError(toolCalls[toolIndex:], out, err)
				}
				a.failedToolCall(tc, out, result, turnNumCtx)
				continue
			}
			authorization := toolAuthorization{allowed: true, approval: executionPkg.ApprovalNotApplicable}
			if autoApproved {
				authorization.approval = executionPkg.ApprovalPolicy
			}
			if requiresApproval {
				var authorizationErr error
				authorization, authorizationErr = a.decideToolAuthorization(ctx, tc, func() error {
					return appendExecutionEvent(ctx, execRuntime, executionEvent(*tracked, executionPkg.EventApprovalRequested, executionPkg.ApprovalRequested, "", "interactive approval requested"))
				})
				if authorizationErr != nil {
					return a.stopBeforeDispatchAfterLedgerError(toolCalls[toolIndex:], out, authorizationErr)
				}
			} else if tracked.identity.EffectClass != executionPkg.EffectReadOnly {
				// memory_recall persists LastUsed metadata but remains implicitly
				// allowed by the built-in tool policy.
				authorization.approval = executionPkg.ApprovalPolicy
			}
			if authorization.cancelled {
				cancelErr := ctx.Err()
				if cancelErr == nil {
					cancelErr = context.Canceled
				}
				result := fmt.Sprintf("CANCELLED — NOT DISPATCHED: approval for tool %q was cancelled: %s", tc.Name, authorization.reason)
				if err := a.appendTerminalExecutionEvent(ctx, execRuntime, *tracked, executionEvent(*tracked, executionPkg.EventCancelled, executionPkg.ApprovalCancelled, result, "interactive approval cancelled before dispatch")); err != nil {
					return a.stopBeforeDispatchAfterLedgerError(toolCalls[toolIndex:], out, err)
				}
				a.failedToolCall(tc, out, result, turnNumCtx)
				if ledgerErr := a.cancelTrackedToolCalls(ctx, execRuntime, trackedExecutions[toolIndex+1:], toolCalls[toolIndex+1:], out, cancelErr); ledgerErr != nil {
					return ledgerErr
				}
				return cancelErr
			}
			if !authorization.allowed {
				if authorization.hostRefused {
					code := authorization.refusalCode
					if code == "" {
						code = "host_refused"
					}
					result := capToolResultForContext(fmt.Sprintf("tool request refused by host [%s]: %s. Do not retry unchanged; change the request or approval renderer.", code, authorization.reason), turnNumCtx)
					if err := a.appendTerminalExecutionEvent(ctx, execRuntime, *tracked, executionEvent(*tracked, executionPkg.EventFailed, executionPkg.ApprovalHostRefused, result, "approval host refused request before dispatch")); err != nil {
						return a.stopBeforeDispatchAfterLedgerError(toolCalls[toolIndex:], out, err)
					}
					a.failedToolCall(tc, out, result, turnNumCtx)
					key := tc.Name + "\x00" + tracked.argumentsHash() + "\x00" + code
					hostRefusalCounts[key]++
					if hostRefusalCounts[key] >= maxIdenticalHostRefusals {
						loopErr := &RepeatedHostRefusalError{
							ToolName:      tc.Name,
							ArgumentsHash: tracked.argumentsHash(),
							Code:          code,
							Attempts:      hostRefusalCounts[key],
						}
						if ledgerErr := a.cancelTrackedToolCalls(ctx, execRuntime, trackedExecutions[toolIndex+1:], toolCalls[toolIndex+1:], out, loopErr); ledgerErr != nil {
							return ledgerErr
						}
						out.Error(loopErr.Error())
						return loopErr
					}
					continue
				}
				if err := a.appendTerminalExecutionEvent(ctx, execRuntime, *tracked, executionEvent(*tracked, executionPkg.EventDenied, authorization.approval, authorization.reason, "authorization denied before dispatch")); err != nil {
					return a.stopBeforeDispatchAfterLedgerError(toolCalls[toolIndex:], out, err)
				}
				a.deniedToolCall(tc, out, "tool call denied: "+authorization.reason)
				continue
			}
			if err := appendExecutionEvent(ctx, execRuntime, executionEvent(*tracked, executionPkg.EventApproved, authorization.approval, "", "tool execution authorized")); err != nil {
				return a.stopBeforeDispatchAfterLedgerError(toolCalls[toolIndex:], out, err)
			}
			if ctxErr := ctx.Err(); ctxErr != nil {
				if ledgerErr := a.cancelTrackedToolCalls(ctx, execRuntime, trackedExecutions[toolIndex:], toolCalls[toolIndex:], out, ctxErr); ledgerErr != nil {
					return ledgerErr
				}
				return ctxErr
			}

			if err := appendExecutionEvent(ctx, execRuntime, executionEvent(*tracked, executionPkg.EventStarted, executionPkg.ApprovalNotApplicable, "", "durable dispatch intent committed")); err != nil {
				return a.stopBeforeDispatchAfterLedgerError(toolCalls[toolIndex:], out, err)
			}
			// Once a mutation has a durable dispatch intent, any prior Bob
			// convergence projection is stale even if the backend later fails or
			// cancellation makes the outcome unknown. Read-only and memory metadata
			// operations do not invalidate repository contract state.
			if kind != executionPkg.KindMemory && tracked.identity.EffectClass != executionPkg.EffectReadOnly {
				a.invalidateBobWorkspaceContext(out)
				// The system prompt was assembled before this durable mutation. Remove
				// both the cached Bob digest and any pre-mutation continuation hint
				// before a later provider iteration can observe stale convergence.
				capabilityBaseContext = capabilityBaseContextWithoutBob
				baseLoadedContext = composeCapabilityContext(capabilityHintText, capabilityBaseContext)
				loadedContext = baseLoadedContext
				rebuildSystem()
			}
			bobAdmission := a.captureBobContextAdmission(tc)
			if ctxErr := ctx.Err(); ctxErr != nil {
				if err := a.cancelCommittedDispatchIntent(ctx, execRuntime, *tracked, tc, out, ctxErr, false, turnNumCtx); err != nil {
					a.cancelUndispatchedToolCalls(toolCalls[toolIndex+1:], out, err)
					return err
				}
				if tracked.identity.EffectClass != executionPkg.EffectReadOnly && execRuntime.ledger != nil {
					unresolved := a.unresolvedFor(*tracked, executionPkg.EventOutcomeUnknown, fmt.Errorf("durable dispatch intent for tool %q has an unknown outcome", tc.Name))
					a.latchUnresolvedExecution(unresolved)
					if ledgerErr := a.cancelTrackedToolCalls(ctx, execRuntime, trackedExecutions[toolIndex+1:], toolCalls[toolIndex+1:], out, unresolved); ledgerErr != nil {
						return ledgerErr
					}
					return unresolved
				}
				if ledgerErr := a.cancelTrackedToolCalls(ctx, execRuntime, trackedExecutions[toolIndex+1:], toolCalls[toolIndex+1:], out, ctxErr); ledgerErr != nil {
					return ledgerErr
				}
				return ctxErr
			}
			out.ToolCallStart(tc.ID, tc.Name, cloneApprovalArguments(tc.Arguments))
			if ctxErr := ctx.Err(); ctxErr != nil {
				if err := a.cancelCommittedDispatchIntent(ctx, execRuntime, *tracked, tc, out, ctxErr, true, turnNumCtx); err != nil {
					a.cancelUndispatchedToolCalls(toolCalls[toolIndex+1:], out, err)
					return err
				}
				if tracked.identity.EffectClass != executionPkg.EffectReadOnly && execRuntime.ledger != nil {
					unresolved := a.unresolvedFor(*tracked, executionPkg.EventOutcomeUnknown, fmt.Errorf("durable dispatch intent for tool %q has an unknown outcome", tc.Name))
					a.latchUnresolvedExecution(unresolved)
					if ledgerErr := a.cancelTrackedToolCalls(ctx, execRuntime, trackedExecutions[toolIndex+1:], toolCalls[toolIndex+1:], out, unresolved); ledgerErr != nil {
						return ledgerErr
					}
					return unresolved
				}
				if ledgerErr := a.cancelTrackedToolCalls(ctx, execRuntime, trackedExecutions[toolIndex+1:], toolCalls[toolIndex+1:], out, ctxErr); ledgerErr != nil {
					return ledgerErr
				}
				return ctxErr
			}
			if hostContinuationBatch && !autoContinuationStillEligible() {
				result := "host-scheduled continuation became stale before backend dispatch"
				if err := a.appendTerminalExecutionEvent(ctx, execRuntime, *tracked, executionEvent(*tracked, executionPkg.EventFailed, executionPkg.ApprovalPolicyDenied, result, "automatic continuation stopped after final policy check")); err != nil {
					return a.stopBeforeDispatchAfterLedgerError(toolCalls[toolIndex:], out, err)
				}
				a.failedToolCall(tc, out, result, turnNumCtx)
				continue
			}
			startTime := time.Now()

			var result string
			var isErr bool
			var structured, errorMeta json.RawMessage
			transportErr := false
			var stopAfterTool error
			switch kind {
			case executionPkg.KindBuiltin:
				if tc.Name == "consult_experts" {
					expertConsultations++
					remaining := 0
					if limits.MaxEvalTokens > 0 {
						remaining = boundedEvalLimit(limits.MaxEvalTokens - totalEvalTokens)
					}
					var usage expertteam.Usage
					var usageErr error
					result, isErr, usage, usageErr = a.handleConsultExpertsWithBudget(ctx, tc.Arguments, remaining)
					if usageErr != nil {
						if remaining > 0 {
							out.StreamDone(remaining, 0)
							totalEvalTokens += int64(remaining)
							stopAfterTool = fmt.Errorf("%w: expert consultation returned an invalid usage receipt; conservatively charged %d reserved token(s)", ErrTurnEvalBudgetExhausted, remaining)
						} else {
							stopAfterTool = errors.New("expert consultation usage could not be validated")
						}
						isErr = true
					} else if usage.EvalTokens > 0 || usage.PromptEvalTokens > 0 {
						if int64(usage.EvalTokens) > math.MaxInt64-totalEvalTokens {
							if remaining > 0 {
								out.StreamDone(remaining, 0)
								totalEvalTokens += int64(remaining)
								stopAfterTool = fmt.Errorf("%w: expert consultation usage overflowed the parent counter; conservatively charged %d reserved token(s)", ErrTurnEvalBudgetExhausted, remaining)
							} else {
								stopAfterTool = errors.New("expert consultation usage overflowed the parent turn counter")
							}
							isErr = true
						} else {
							out.StreamDone(usage.EvalTokens, usage.PromptEvalTokens)
							totalEvalTokens += int64(usage.EvalTokens)
							if limits.MaxEvalTokens > 0 && totalEvalTokens >= limits.MaxEvalTokens {
								stopAfterTool = fmt.Errorf("%w after expert consultation: used %d of %d", ErrTurnEvalBudgetExhausted, totalEvalTokens, limits.MaxEvalTokens)
								if totalEvalTokens > limits.MaxEvalTokens {
									isErr = true
									result = "error: expert provider exceeded the remaining evaluation-token budget\n" + result
								}
							}
						}
					}
				} else {
					result, isErr = a.handleBuiltinToolWithCancellation(ctx, tc, tracked.identity.EffectClass != executionPkg.EffectReadOnly)
				}
			case executionPkg.KindMemory:
				result, isErr = a.handleMemoryTool(tc)
			default:
				var toolResult *mcpPkg.ToolResult
				var callErr error
				if hostContinuationBatch {
					toolResult, callErr = a.registry.CallToolAtEpoch(
						ctx, activeAutoContinuation.registryEpoch, tc.Name, cloneApprovalArguments(tc.Arguments),
					)
				} else {
					toolResult, callErr = a.registry.CallTool(ctx, tc.Name, tc.Arguments)
				}
				if callErr != nil {
					result = mcpDispatchErrorReceipt(tc.Name, callErr)
					isErr = true
					transportErr = true
				} else if toolResult == nil {
					result, isErr = "ERROR: MCP tool returned no result", true
					transportErr = true
				} else {
					result = toolResult.Content
					isErr = toolResult.IsError
					structured = toolResult.Structured
					errorMeta = toolResult.ErrorMeta
				}
			}
			duration := time.Since(startTime)
			// Hooks are the result-redaction boundary. Apply them before the
			// durable receipt so secrets removed from UI/model text are not copied
			// into the execution ledger.
			a.runPostHooks(ctx, tc, &result, isErr)
			semanticText := result
			projection := a.projectSemanticToolReceipt(
				tc, semanticText, structured, errorMeta,
				transportErr, isErr, kind == executionPkg.KindBuiltin || kind == executionPkg.KindMemory,
			)
			// An explicitly requested, exact MCPHub describe result may seed the
			// bounded ephemeral schema cache used by LA-2. This never dispatches a
			// follow-up and never widens the host trust catalog.
			a.rememberContinuationContract(tc, projection, structured, turnMCPSnapshot)
			assembly := a.projectMCPHubResultAssembly(tc, projection, structured, isErr)
			projection = assembly.Projection
			bobAdmission = a.resolveBobContextAdmission(bobAdmission, projection, assembly)
			continuationReceipt := ecosystemPkg.RawReceipt{
				Text: semanticText, Structured: structured, ErrorMeta: errorMeta,
				TransportError: transportErr, ToolError: isErr,
			}
			continuationCandidates := assembly.Actions
			continuationSurface := assembly.ContinuationSurface
			sourceRouteVersion := a.mcpRouteVersionSnapshot()
			continuationSourceAuthorized := a.continuationSourceAuthorized(tc, assembly.Bound) &&
				a.allowsMCPTool(tc.Name) && !a.authorityPermissionDeniedForCall(tc)
			if !assembly.Bound {
				if continuationSourceAuthorized {
					continuationCandidates = ecosystemPkg.ProjectContinuationActions(projection, continuationReceipt)
				}
				continuationSurface = continuationSurfacePresent(projection, continuationReceipt)
			}
			var continuation *ValidatedContinuation
			if capabilityRouteOutcomeFailed(projection, isErr) &&
				a.markCapabilityRouteFailed(capabilityActivity, tc.Name, tc.Arguments, capabilityHint) {
				capabilityRouteFailed = true
			}
			// Typed MCP payloads stay inside the parser boundary. Only exact,
			// host-trusted, bounded projections may enter the active provider turn;
			// every durable boundary and the UI receive only the allowlisted receipt.
			modelResult, durableResult := a.semanticToolContents(tc, projection, result, structured, isErr)
			if assembly.Bound {
				// Stored-result pages are serialized CallToolResult fragments, not
				// downstream semantic documents. Keep every partial or rejected page
				// behind the parser boundary. A complete exact route may expose only
				// its validated, bounded model-only projection.
				durableResult = ecosystemPkg.SafeReceiptText(projection)
				modelResult = durableResult
				if assembly.Complete && assembly.Transient != "" {
					modelResult = assembly.Transient
				}
			}
			if continuationSurface {
				// Once an exact action has been normalized, do not also feed Cortex's
				// or Bob's raw command/reason payload to a small model. The bounded
				// semantic receipt plus tool+arguments contract is authoritative.
				durableResult = ecosystemPkg.SafeReceiptText(projection)
				modelResult = durableResult
			}
			answered := a.executionOutcomeAnswered(tc, kind, tracked.identity.EffectClass, result, transportErr, projection)
			terminalType := terminalExecutionEventType(tracked.identity.EffectClass, isErr, answered, ctx.Err())
			// Only a genuinely unverifiable outcome earns the OUTCOME UNKNOWN
			// framing. A backend that answered with a domain error keeps its
			// receipt intact so the model (and the ledger) see what it said.
			if terminalType == executionPkg.EventOutcomeUnknown && !strings.HasPrefix(durableResult, outcomeUnknownReceiptPrefix) {
				durableResult = dispatchedEffectErrorReceipt(tc.Name, durableResult, ctx.Err())
				modelResult = durableResult
			}
			// Cap before the durable append so the terminal event hashes exactly
			// the receipt the session transcript will persist; the projection
			// boundary compares those hashes when advancing the snapshot cursor.
			durableResult = capToolResultForContext(durableResult, turnNumCtx)
			modelResult = capToolResultForContext(modelResult, turnNumCtx)
			terminalDetail := fmt.Sprintf("backend returned after %dms", duration.Milliseconds())
			if err := appendExecutionEvent(ctx, execRuntime, executionEvent(*tracked, terminalType, executionPkg.ApprovalNotApplicable, durableResult, terminalDetail)); err != nil {
				unresolved := a.unresolvedFor(*tracked, executionPkg.EventStarted, err)
				a.latchUnresolvedExecution(unresolved)
				unknownResult := capToolResultForContext(terminalLedgerFailureReceipt(tc.Name, err), turnNumCtx)
				out.ToolCallResult(tc.ID, tc.Name, unknownResult, true, duration)
				a.AppendMessage(llm.Message{Role: "tool", Content: unknownResult, ToolName: tc.Name, ToolCallID: tc.ID})
				a.cancelUndispatchedToolCalls(toolCalls[toolIndex+1:], out, unresolved)
				return unresolved
			}
			a.settleBobContextAdmission(out, bobAdmission, projection, continuationReceipt, assembly, terminalType)

			// A continuation becomes schedulable only after the source outcome is
			// durably terminal. Automatic chains are single-action, successful,
			// exact-route reads and consume both a synthetic iteration and their
			// separate hard two-step budget. Preserve one additional provider
			// iteration after the read so its result can always be interpreted.
			// Every failed auto admission falls back to LA-2 suggestion mode without
			// granting authority.
			if terminalType == executionPkg.EventCompleted && ctx.Err() == nil &&
				len(toolCalls) == 1 && toolIndex == 0 && i < maxIters-2 && autoContinuationState != nil {
				queuedAutoContinuation = a.selectAutoReadOnlyContinuation(
					tc, projection, continuationCandidates, turnMCPSnapshot,
					continuationSourceAuthorized, sourceRouteVersion, authorityMode, autoContinuationState,
				)
			}
			if queuedAutoContinuation == nil && continuationsConfig.Mode != config.ContinuationOff {
				continuation = a.selectContinuation(
					tc, projection, continuationCandidates, turnMCPSnapshot,
					continuationSourceAuthorized, turnToolPolicy.AllowMCP, continuationState,
				)
			}
			if continuationContext := continuation.modelContext(); continuationContext != "" {
				modelResult += "\n\n" + continuationContext
				modelResult = capToolResultForContext(modelResult, turnNumCtx)
			}

			if lg != nil {
				lg.Debug("tool", "name", tc.Name, "kind", kind, "ms", duration.Milliseconds(), "error", isErr)
			}
			emitSemanticToolResult(
				out, tc.ID, tc.Name, durableResult, structured, isErr, transportErr, duration, projection,
			)
			continuationSequence := uint64(i+1)<<32 | uint64(toolIndex+1)
			if continuation != nil || isContinuationSourceProjection(projection) {
				emitContinuationSuggestion(out, turnID, continuationSequence, continuation)
			}
			toolMessage := llm.Message{
				Role:       "tool",
				Content:    modelResult,
				ToolName:   tc.Name,
				ToolCallID: tc.ID,
			}
			if modelResult != durableResult {
				toolMessage.DurableContent = durableResult
			}
			a.AppendMessage(toolMessage)
			if terminalType == executionPkg.EventOutcomeUnknown && execRuntime.ledger != nil {
				unresolved := a.unresolvedFor(*tracked, executionPkg.EventOutcomeUnknown, fmt.Errorf("durable outcome for tool %q is unknown and requires explicit reconciliation", tc.Name))
				a.latchUnresolvedExecution(unresolved)
				if ledgerErr := a.cancelTrackedToolCalls(ctx, execRuntime, trackedExecutions[toolIndex+1:], toolCalls[toolIndex+1:], out, unresolved); ledgerErr != nil {
					return ledgerErr
				}
				return unresolved
			}
			if stopAfterTool != nil {
				if ctxErr := ctx.Err(); ctxErr != nil {
					stopAfterTool = errors.Join(ctxErr, stopAfterTool)
				}
				if ledgerErr := a.cancelTrackedToolCalls(ctx, execRuntime, trackedExecutions[toolIndex+1:], toolCalls[toolIndex+1:], out, stopAfterTool); ledgerErr != nil {
					return ledgerErr
				}
				out.Error(stopAfterTool.Error())
				return stopAfterTool
			}
			if ctxErr := ctx.Err(); ctxErr != nil {
				if ledgerErr := a.cancelTrackedToolCalls(ctx, execRuntime, trackedExecutions[toolIndex+1:], toolCalls[toolIndex+1:], out, ctxErr); ledgerErr != nil {
					return ledgerErr
				}
				return ctxErr
			}
		}
		if !hostContinuationBatch {
			if preflightRejections == len(toolCalls) {
				malformedToolIterations++
				if malformedToolIterations >= 2 {
					err := fmt.Errorf("%w twice consecutively; switch to a larger model or retry with explicit tool arguments", ErrMalformedToolLoop)
					out.Error(err.Error())
					return err
				}
			} else {
				malformedToolIterations = 0
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if capabilityRouteFailed && capabilityReroutes < maxCapabilityReroutesPerTurn && i < maxIters-1 {
			capabilityReroutes++
			capabilityHintText, capabilityHint = a.resolveTurnCapabilityWithPolicy(ctx, out, capabilityActivity, turnToolPolicy.AllowMCP)
			if err := ctx.Err(); err != nil {
				return err
			}
			loadedContext = composeCapabilityContext(capabilityHintText, capabilityBaseContext)
			system = buildSystemPromptForModelBudgetContextWithSkillCatalogAndReadGrants(ctx, modePrefix, tools, a.skillContent, skillCatalog, loadedContext, a.memoryStore, iceContext, turnFilesystem.workDir, turnFilesystem.ignoreContent, a.llmClient.Model(), turnNumCtx, readGrants)
		}

		// A queued host read executes before another provider request. Deferring
		// compaction avoids inserting an untracked summarization generation between
		// the source receipt and its exact continuation.
		if queuedAutoContinuation == nil {
			// Check if we should compact the conversation.
			estimatedPromptTokens := a.estimatePromptTokens(system, tools)
			if estimatedPromptTokens < lastPromptTokens {
				estimatedPromptTokens = lastPromptTokens
			}
			if shouldCompactForContext(estimatedPromptTokens, turnNumCtx) {
				if limits.bounded() {
					err := &TurnContextBudgetError{
						EstimatedPromptTokens: estimatedPromptTokens,
						ContextWindowTokens:   turnNumCtx,
					}
					if lg != nil {
						lg.Warn("context admission denied", "phase", "after_tools", "iter", i, "prompt_tokens", estimatedPromptTokens, "num_ctx", turnNumCtx, "bounded", true)
					}
					out.Error(err.Error())
					return err
				}
				if lg != nil {
					lg.Info("compaction", "iter", i, "prompt_tokens", estimatedPromptTokens, "num_ctx", turnNumCtx)
				}
				if a.compactForContext(ctx, out, turnNumCtx) {
					// Rebuild system prompt after compaction (memory may have changed).
					system = buildSystemPromptForModelBudgetContextWithSkillCatalogAndReadGrants(ctx, modePrefix, tools, a.skillContent, skillCatalog, loadedContext, a.memoryStore, iceContext, turnFilesystem.workDir, turnFilesystem.ignoreContent, a.llmClient.Model(), turnNumCtx, readGrants)
				}
			}
		}

		// Interactive modes surface their deliberately tight turn ceiling. AUTO
		// has a larger host-owned safety budget and stays quiet while using it.
		if authorityMode != AuthorityAutoScoped && i == maxIters-2 {
			out.Error(fmt.Sprintf("approaching iteration limit (%d/%d)", i+2, maxIters))
		}
	}

	if lg != nil {
		lg.Warn("turn end", "reason", "max_iterations", "iters", maxIters, "ms", time.Since(turnStart).Milliseconds())
	}
	limitErr := fmt.Errorf("reached max iterations (%d)", maxIters)
	out.Error(limitErr.Error())
	return limitErr
}

func boundedEvalLimit(remaining int64) int {
	if remaining <= 0 {
		return 0
	}
	maxInt := int64(^uint(0) >> 1)
	if remaining > maxInt {
		return int(maxInt)
	}
	return int(remaining)
}

// chargeUnknownEvalReservation propagates a fail-closed usage receipt when a
// capped provider request does not return trustworthy terminal accounting. The
// exact number of generated tokens is unknowable in that failure mode, so the
// host must consume the unaccounted portion of the per-request reservation
// before it may admit another goal turn.
func chargeUnknownEvalReservation(out Output, requestLimit int, reported int64) int64 {
	if requestLimit <= 0 {
		return 0
	}
	reserved := int64(requestLimit)
	if reported >= reserved {
		return 0
	}
	missing := reserved - max(int64(0), reported)
	out.StreamDone(int(missing), 0)
	return missing
}

func capToolResultForContext(result string, numCtx int) string {
	if numCtx <= 0 {
		return result
	}
	limit := numCtx
	if limit < 2*1024 {
		limit = 2 * 1024
	}
	if limit > 96*1024 {
		limit = 96 * 1024
	}
	const marker = "\n... [tool result truncated to protect model context]"
	if len(result) <= limit {
		return result
	}
	cut := limit - len(marker)
	for cut > 0 && !utf8.ValidString(result[:cut]) {
		cut--
	}
	return result[:cut] + marker
}

func (a *Agent) estimatePromptTokens(system string, tools []llm.ToolDef) int {
	characters := len(system)
	if encoded, err := json.Marshal(tools); err == nil {
		characters += len(encoded)
	}
	a.mu.RLock()
	for _, message := range a.messages {
		characters += len(message.Content) + len(message.ToolName) + len(message.ToolCallID) + 12
		if encoded, err := json.Marshal(message.ToolCalls); err == nil {
			characters += len(encoded)
		}
	}
	a.mu.RUnlock()
	return characters/4 + 1
}

func filterToolDefsByName(defs []llm.ToolDef, allowed map[string]struct{}) []llm.ToolDef {
	if len(allowed) == 0 {
		return nil
	}

	filtered := make([]llm.ToolDef, 0, len(defs))
	for _, def := range defs {
		if _, ok := allowed[def.Name]; ok {
			filtered = append(filtered, def)
		}
	}
	return filtered
}

func (a *Agent) blockedToolCall(tc llm.ToolCall, out Output) {
	errMsg := fmt.Sprintf("tool call blocked in current mode: %s", tc.Name)
	out.ToolCallStart(tc.ID, tc.Name, tc.Arguments)
	out.ToolCallResult(tc.ID, tc.Name, errMsg, true, 0)
	a.AppendMessage(llm.Message{
		Role:       "tool",
		Content:    errMsg,
		ToolName:   tc.Name,
		ToolCallID: tc.ID,
	})
}

func builtinToolRequiresApproval(name string) bool {
	switch name {
	case "write", "edit", "bash", "mkdir", "remove", "copy", "move":
		return true
	default:
		return false
	}
}

func memoryToolRequiresApproval(name string) bool {
	switch name {
	case "memory_save", "memory_delete", "memory_update":
		return true
	default:
		return false
	}
}

func mcpDispatchErrorReceipt(name string, err error) string {
	return fmt.Sprintf(outcomeUnknownReceiptPrefix+" tool %q ended without a result receipt and may have taken effect: %v", name, err)
}

func dispatchedEffectErrorReceipt(name, backendResult string, contextErr error) string {
	if contextErr != nil {
		return fmt.Sprintf(outcomeUnknownReceiptPrefix+" tool %q was cancelled after dispatch and may have taken effect: %v\nBackend result: %s", name, contextErr, backendResult)
	}
	return fmt.Sprintf(outcomeUnknownReceiptPrefix+" tool %q returned an error after dispatch and may have partially taken effect. Do not retry automatically; inspect state first.\nBackend result: %s", name, backendResult)
}

type builtinToolResult struct {
	content string
	isErr   bool
}

func (a *Agent) handleBuiltinToolWithCancellation(ctx context.Context, tc llm.ToolCall, effectful bool) (string, bool) {
	if effectful {
		// Mutations must join so their final/unknown receipt reflects the real
		// backend. Cooperative cancellation is threaded to bash.
		return a.handleToolsTool(ctx, tc)
	}
	// Some filesystem syscalls cannot be interrupted by context (notably a
	// network ReadDir). Read-only calls may be abandoned safely so shutdown
	// remains bounded; the worker has no mutation authority. The slot bounds a
	// permanently blocked syscall to one worker per Agent.
	select {
	case a.readOnlySlots <- struct{}{}:
	case <-ctx.Done():
		return fmt.Sprintf("error: read-only tool %q cancelled before dispatch: %v", tc.Name, ctx.Err()), true
	}
	done := make(chan builtinToolResult, 1)
	go func() {
		defer func() { <-a.readOnlySlots }()
		content, isErr := a.handleToolsTool(ctx, tc)
		done <- builtinToolResult{content: content, isErr: isErr}
	}()
	select {
	case result := <-done:
		return result.content, result.isErr
	case <-ctx.Done():
		return fmt.Sprintf("error: read-only tool %q cancelled before completion: %v", tc.Name, ctx.Err()), true
	}
}

// authorizeToolCall applies one policy path to every risky built-in, memory,
// and MCP operation. The CLI always installs a checker; nil remains an
// explicit embedding opt-out for package users and unit tests.
func (a *Agent) authorizeToolCall(ctx context.Context, tc llm.ToolCall, out Output) bool {
	decision, err := a.decideToolAuthorization(ctx, tc, nil)
	if err != nil || decision.cancelled {
		return false
	}
	if !decision.allowed {
		if decision.hostRefused {
			code := decision.refusalCode
			if code == "" {
				code = "host_refused"
			}
			a.failedToolCall(tc, out, fmt.Sprintf("tool request refused by host [%s]: %s", code, decision.reason), a.NumCtx())
			return false
		}
		a.deniedToolCall(tc, out, "tool call denied: "+decision.reason)
		return false
	}
	return ctx.Err() == nil
}

func (a *Agent) deniedToolCall(tc llm.ToolCall, out Output, reason string) {
	out.ToolCallStart(tc.ID, tc.Name, tc.Arguments)
	out.ToolCallResult(tc.ID, tc.Name, reason, true, 0)
	a.AppendMessage(llm.Message{
		Role:       "tool",
		Content:    reason,
		ToolName:   tc.Name,
		ToolCallID: tc.ID,
	})
}

func (a *Agent) failedToolCall(tc llm.ToolCall, out Output, reason string, numCtx int) {
	result := capToolResultForContext(reason, numCtx)
	out.ToolCallStart(tc.ID, tc.Name, tc.Arguments)
	out.ToolCallResult(tc.ID, tc.Name, result, true, 0)
	a.AppendMessage(llm.Message{
		Role:       "tool",
		Content:    result,
		ToolName:   tc.Name,
		ToolCallID: tc.ID,
	})
}

// cancelUndispatchedToolCalls closes every assistant tool-call edge that is
// still queued when a turn is cancelled. These operations never reached a
// backend, so the receipt deliberately distinguishes them from the
// OUTCOME UNKNOWN receipt used after dispatch.
func (a *Agent) cancelUndispatchedToolCalls(calls []llm.ToolCall, out Output, cause error) {
	for _, tc := range calls {
		result := fmt.Sprintf("CANCELLED — NOT DISPATCHED: tool %q did not start because the turn ended: %v", tc.Name, cause)
		out.ToolCallStart(tc.ID, tc.Name, tc.Arguments)
		out.ToolCallResult(tc.ID, tc.Name, result, true, 0)
		a.AppendMessage(llm.Message{
			Role:       "tool",
			Content:    result,
			ToolName:   tc.Name,
			ToolCallID: tc.ID,
		})
	}
}

// isRetryableError returns true for transient LLM errors that are worth retrying,
// such as JSON parse failures from malformed tool call output.
func isRetryableError(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "parse JSON") || strings.Contains(msg, "unexpected end of JSON")
}

// ensureToolCallIDs gives every invocation a stable, unique correlation key.
// Small local models occasionally omit IDs (or repeat one for a batch), while
// the UI and provider transcript need an unambiguous result association.
func ensureToolCallIDs(calls []llm.ToolCall, turnID string, iteration int) {
	ensureToolCallIDsAgainst(calls, turnID, iteration, nil)
}

func (a *Agent) ensureToolCallIDs(calls []llm.ToolCall, turnID string, iteration int) {
	reserved := make(map[string]struct{})
	a.mu.RLock()
	for _, message := range a.messages {
		if id := strings.TrimSpace(message.ToolCallID); id != "" {
			reserved[id] = struct{}{}
		}
		for _, call := range message.ToolCalls {
			if id := strings.TrimSpace(call.ID); id != "" {
				reserved[id] = struct{}{}
			}
		}
	}
	a.mu.RUnlock()
	ensureToolCallIDsAgainst(calls, turnID, iteration, reserved)
}

func ensureToolCallIDsAgainst(calls []llm.ToolCall, turnID string, iteration int, reserved map[string]struct{}) {
	seen := make(map[string]struct{}, len(reserved)+len(calls))
	for id := range reserved {
		seen[id] = struct{}{}
	}
	for i := range calls {
		id := strings.TrimSpace(calls[i].ID)
		_, duplicate := seen[id]
		if id == "" || duplicate {
			base := fmt.Sprintf("%s-tool-%d-%d", turnID, iteration, i+1)
			id = base
			for suffix := 2; ; suffix++ {
				if _, exists := seen[id]; !exists {
					break
				}
				id = fmt.Sprintf("%s-%d", base, suffix)
			}
		}
		calls[i].ID = id
		seen[id] = struct{}{}
	}
}

// FormatToolArgs formats tool arguments in a human-readable way for display.
// Avoids showing raw JSON by presenting key=value pairs.
func FormatToolArgs(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}

	var parts []string
	for key, value := range args {
		// Format value based on its type
		var valStr string
		switch v := value.(type) {
		case string:
			// Truncate long strings (account for quotes)
			if len(v) > 47 {
				valStr = `"` + v[:44] + `..."`
			} else {
				valStr = `"` + v + `"`
			}
		case int, float64, bool:
			valStr = fmt.Sprintf("%v", v)
		case []any:
			// Show array length
			valStr = fmt.Sprintf("[%d items]", len(v))
		case map[string]any:
			// Show object keys count
			valStr = fmt.Sprintf("{%d fields}", len(v))
		default:
			valStr = fmt.Sprintf("%v", v)
		}
		parts = append(parts, fmt.Sprintf("%s=%s", key, valStr))
	}

	// Sort parts for consistent output
	sort.Strings(parts)

	result := strings.Join(parts, " ")

	// Truncate if too long
	if len(result) > 60 {
		return result[:57] + "..."
	}
	return result
}
