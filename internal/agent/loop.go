package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	executionPkg "github.com/abdul-hamid-achik/local-agent/internal/execution"
	"github.com/abdul-hamid-achik/local-agent/internal/llm"
)

const maxToolCallsPerResponse = 64

// Run executes the ReAct loop: query -> LLM -> tool calls -> observe -> repeat.
// It streams output via the Output interface and returns a terminal error for
// headless callers and automation.
func (a *Agent) Run(ctx context.Context, out Output) error {
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
	turnCtx, turnCancel := context.WithCancel(ctx)
	turnDone := make(chan struct{})
	a.turnCancel = turnCancel
	a.turnDone = turnDone
	a.turnMu.Unlock()
	ctx = turnCtx
	defer func() {
		turnCancel()
		close(turnDone)
		a.turnMu.Lock()
		if a.turnDone == turnDone {
			a.turnCancel = nil
			a.turnDone = nil
		}
		a.turnRunning.Store(false)
		a.turnMu.Unlock()
	}()
	execRuntime, err := a.executionRuntime(ctx)
	if err != nil {
		out.Error(err.Error())
		return err
	}
	turnID, err := executionPkg.NewTurnID()
	if err != nil {
		out.Error(fmt.Sprintf("execution turn identity: %v", err))
		return fmt.Errorf("execution turn identity: %w", err)
	}
	if a.iceEngine != nil {
		a.iceEngine.CancelAutoMemory()
	}

	var tools []llm.ToolDef
	if a.toolPolicy.AllowMCP && a.registry != nil {
		tools = append(tools, a.mcpTools()...)
	}
	// Merge memory built-in tools if available.
	if a.memoryStore != nil {
		tools = append(tools, filterToolDefsByName(a.memoryBuiltinToolDefs(), a.toolPolicy.memoryTools)...)
	}
	// Merge built-in file tools according to the active mode policy.
	tools = append(tools, filterToolDefsByName(a.toolsBuiltinToolDefs(), a.toolPolicy.localTools)...)

	// ICE: index user message and assemble cross-session context.
	var iceContext string
	a.mu.RLock()
	hasMessages := len(a.messages) > 0
	var lastMsg llm.Message
	if hasMessages {
		lastMsg = a.messages[len(a.messages)-1]
	}
	a.mu.RUnlock()

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

	system := buildSystemPromptForModelBudgetContext(ctx, a.modePrefix, tools, a.skillContent, a.loadedCtx, a.memoryStore, iceContext, a.workDir, a.ignoreContent, a.llmClient.Model(), a.numCtx)

	const maxRetries = 2
	var lastPromptTokens int
	var lastEvalTokens int
	var retryCount int

	maxIters := a.MaxIterations()

	// A process-independent turn ID threads through logs and every durable tool
	// execution identity for this turn.
	lg := a.logTurn(turnID)
	turnStart := time.Now()
	if lg != nil {
		lg.Info("turn start", "model", a.llmClient.Model(), "tools", len(tools), "max_iters", maxIters)
	}

	// Compact before the next provider request as well as after responses. This
	// protects direct-answer conversations, which may never enter the tool loop,
	// from submitting an already oversized history to Ollama.
	if estimated := a.estimatePromptTokens(system, tools); a.shouldCompact(estimated) {
		if lg != nil {
			lg.Info("compaction", "phase", "before_request", "prompt_tokens", estimated, "num_ctx", a.numCtx)
		}
		if a.compact(ctx, out) {
			system = buildSystemPromptForModelBudgetContext(ctx, a.modePrefix, tools, a.skillContent, a.loadedCtx, a.memoryStore, iceContext, a.workDir, a.ignoreContent, a.llmClient.Model(), a.numCtx)
		}
	}

	for i := 0; i < maxIters; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Stream LLM response.
		var textBuf strings.Builder
		var toolCalls []llm.ToolCall
		llmStart := time.Now()

		// Snapshot the message history under the lock: ChatStream runs while
		// other goroutines may AppendMessage, and passing the live slice would
		// race on the backing array (and could realloc mid-stream).
		a.mu.RLock()
		msgsSnapshot := make([]llm.Message, len(a.messages))
		copy(msgsSnapshot, a.messages)
		a.mu.RUnlock()

		err := a.llmClient.ChatStream(ctx, llm.ChatOptions{
			Messages: msgsSnapshot,
			Tools:    tools,
			System:   system,
		}, func(chunk llm.StreamChunk) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
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
				lastPromptTokens = chunk.PromptEvalCount
				lastEvalTokens = chunk.EvalCount
				out.StreamDone(chunk.EvalCount, chunk.PromptEvalCount)
			}
			return nil
		})

		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			// Retry on transient JSON parse errors from small models.
			if retryCount < maxRetries && isRetryableError(err) {
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
			out.SystemMessage(fmt.Sprintf("⚠️ Model response failed: %v\n\nYou can try:\n- Checking if Ollama is running (`ollama ps`)\n- Switching to a different model (ctrl+m)\n- Reducing context size\n\nTool results are still available above.", err))
			return fmt.Errorf("LLM response: %w", err)
		}
		retryCount = 0 // reset on success
		if lg != nil {
			lg.Info("llm response", "iter", i, "ms", time.Since(llmStart).Milliseconds(),
				"prompt_tokens", lastPromptTokens, "eval_tokens", lastEvalTokens, "tool_calls", len(toolCalls))
		}
		if len(toolCalls) > maxToolCallsPerResponse {
			err := fmt.Errorf("model returned %d tool calls; maximum per response is %d", len(toolCalls), maxToolCallsPerResponse)
			out.Error(err.Error())
			return err
		}
		providerCallIDs := make([]string, len(toolCalls))
		for toolIndex := range toolCalls {
			providerCallIDs[toolIndex] = toolCalls[toolIndex].ID
		}
		a.ensureToolCallIDs(toolCalls, turnID, i+1)
		trackedExecutions, err := a.newTrackedExecutions(ctx, execRuntime, turnID, i+1, toolCalls, providerCallIDs)
		if err != nil {
			out.Error(fmt.Sprintf("record requested tool execution: %v", err))
			return fmt.Errorf("record requested tool execution: %w", err)
		}

		// Record assistant message in conversation history.
		assistantMsg := llm.Message{
			Role:      "assistant",
			Content:   textBuf.String(),
			ToolCalls: toolCalls,
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

			if a.iceEngine != nil && hasEnoughMessages && userContent != "" {
				a.iceEngine.DetectAutoMemory(ctx, userContent, assistantMsg.Content)
			}
			estimatedPromptTokens := a.estimatePromptTokens(system, tools)
			if estimatedPromptTokens < lastPromptTokens {
				estimatedPromptTokens = lastPromptTokens
			}
			if a.shouldCompact(estimatedPromptTokens) {
				if lg != nil {
					lg.Info("compaction", "phase", "direct_response", "prompt_tokens", estimatedPromptTokens, "num_ctx", a.numCtx)
				}
				a.compact(ctx, out)
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
				if !a.toolPolicy.AllowsMemory(tc.Name) {
					if err := a.appendTerminalExecutionEvent(ctx, execRuntime, *tracked, executionEvent(*tracked, executionPkg.EventDenied, executionPkg.ApprovalDenied, "", "blocked by active mode")); err != nil {
						return a.stopBeforeDispatchAfterLedgerError(toolCalls[toolIndex:], out, err)
					}
					a.blockedToolCall(tc, out)
					continue
				}
				requiresApproval = memoryToolRequiresApproval(tc.Name)
			case executionPkg.KindBuiltin:
				if !a.toolPolicy.AllowsBuiltin(tc.Name) {
					if err := a.appendTerminalExecutionEvent(ctx, execRuntime, *tracked, executionEvent(*tracked, executionPkg.EventDenied, executionPkg.ApprovalDenied, "", "blocked by active mode")); err != nil {
						return a.stopBeforeDispatchAfterLedgerError(toolCalls[toolIndex:], out, err)
					}
					a.blockedToolCall(tc, out)
					continue
				}
				requiresApproval = builtinToolRequiresApproval(tc.Name)
			case executionPkg.KindMCP:
				if !a.toolPolicy.AllowMCP {
					if err := a.appendTerminalExecutionEvent(ctx, execRuntime, *tracked, executionEvent(*tracked, executionPkg.EventDenied, executionPkg.ApprovalDenied, "", "MCP blocked by active mode")); err != nil {
						return a.stopBeforeDispatchAfterLedgerError(toolCalls[toolIndex:], out, err)
					}
					a.blockedToolCall(tc, out)
					continue
				}
				if !a.allowsMCPTool(tc.Name) {
					if err := a.appendTerminalExecutionEvent(ctx, execRuntime, *tracked, executionEvent(*tracked, executionPkg.EventDenied, executionPkg.ApprovalDenied, "", "blocked by active agent profile MCP scope")); err != nil {
						return a.stopBeforeDispatchAfterLedgerError(toolCalls[toolIndex:], out, err)
					}
					a.deniedToolCall(tc, out, "tool call blocked by active agent profile MCP scope")
					continue
				}
			}

			originalID, originalName := tc.ID, tc.Name
			block, reason := a.runPreHooks(ctx, &tc)
			if tc.ID != originalID || tc.Name != originalName {
				tc.ID, tc.Name = originalID, originalName
				block = true
				reason = "tool hook attempted to change approved tool identity"
			}
			effectiveHash, hashErr := executionPkg.HashCanonicalArguments(tc.Arguments)
			if hashErr != nil {
				reason := fmt.Sprintf("invalid effective tool arguments: %v", hashErr)
				if err := a.appendTerminalExecutionEvent(ctx, execRuntime, *tracked, executionEvent(*tracked, executionPkg.EventFailed, executionPkg.ApprovalNotApplicable, reason, "effective argument hashing failed")); err != nil {
					return a.stopBeforeDispatchAfterLedgerError(toolCalls[toolIndex:], out, err)
				}
				a.failedToolCall(tc, out, reason)
				continue
			}
			tracked.effectiveHash = effectiveHash
			toolCalls[toolIndex] = tc
			if block {
				if err := a.appendTerminalExecutionEvent(ctx, execRuntime, *tracked, executionEvent(*tracked, executionPkg.EventDenied, executionPkg.ApprovalDenied, reason, "blocked by pre-tool hook")); err != nil {
					return a.stopBeforeDispatchAfterLedgerError(toolCalls[toolIndex:], out, err)
				}
				a.failedToolCall(tc, out, reason)
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

			if preflightErr := a.preflightToolCall(kind, tc); preflightErr != nil {
				result := fmt.Sprintf("tool request failed preflight: %v", preflightErr)
				if err := a.appendTerminalExecutionEvent(ctx, execRuntime, *tracked, executionEvent(*tracked, executionPkg.EventFailed, executionPkg.ApprovalNotApplicable, result, "preflight rejected request before dispatch")); err != nil {
					return a.stopBeforeDispatchAfterLedgerError(toolCalls[toolIndex:], out, err)
				}
				a.failedToolCall(tc, out, result)
				continue
			}

			authorization := toolAuthorization{allowed: true, approval: executionPkg.ApprovalNotApplicable}
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
				ctxErr := ctx.Err()
				if ctxErr == nil {
					ctxErr = context.Canceled
				}
				if ledgerErr := a.cancelTrackedToolCalls(ctx, execRuntime, trackedExecutions[toolIndex:], toolCalls[toolIndex:], out, ctxErr); ledgerErr != nil {
					return ledgerErr
				}
				return ctxErr
			}
			if !authorization.allowed {
				if err := a.appendTerminalExecutionEvent(ctx, execRuntime, *tracked, executionEvent(*tracked, executionPkg.EventDenied, executionPkg.ApprovalDenied, authorization.reason, "authorization denied before dispatch")); err != nil {
					return a.stopBeforeDispatchAfterLedgerError(toolCalls[toolIndex:], out, err)
				}
				a.deniedToolCall(tc, out, "tool call denied: "+authorization.reason)
				continue
			}
			if err := appendExecutionEvent(ctx, execRuntime, executionEvent(*tracked, executionPkg.EventApproved, authorization.approval, "", "tool execution authorized")); err != nil {
				return a.stopBeforeDispatchAfterLedgerError(toolCalls[toolIndex:], out, err)
			}
			if authorization.persistAlways {
				a.persistAlwaysApproval(tc, out)
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
			if ctxErr := ctx.Err(); ctxErr != nil {
				if err := a.cancelCommittedDispatchIntent(ctx, execRuntime, *tracked, tc, out, ctxErr, false); err != nil {
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
			out.ToolCallStart(tc.ID, tc.Name, tc.Arguments)
			if ctxErr := ctx.Err(); ctxErr != nil {
				if err := a.cancelCommittedDispatchIntent(ctx, execRuntime, *tracked, tc, out, ctxErr, true); err != nil {
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
			startTime := time.Now()

			var result string
			var isErr bool
			switch kind {
			case executionPkg.KindBuiltin:
				result, isErr = a.handleBuiltinToolWithCancellation(ctx, tc, tracked.identity.EffectClass != executionPkg.EffectReadOnly)
			case executionPkg.KindMemory:
				result, isErr = a.handleMemoryTool(tc)
			default:
				toolResult, callErr := a.registry.CallTool(ctx, tc.Name, tc.Arguments)
				if callErr != nil {
					result = mcpDispatchErrorReceipt(tc.Name, callErr)
					isErr = true
				} else if toolResult == nil {
					result, isErr = "ERROR: MCP tool returned no result", true
				} else {
					result = toolResult.Content
					isErr = toolResult.IsError
				}
			}
			duration := time.Since(startTime)
			// Hooks are the result-redaction boundary. Apply them before the
			// durable receipt so secrets removed from UI/model text are not copied
			// into the execution ledger.
			a.runPostHooks(ctx, tc, &result, isErr)
			if isErr && tracked.identity.EffectClass != executionPkg.EffectReadOnly && !strings.HasPrefix(result, "OUTCOME UNKNOWN:") {
				result = dispatchedEffectErrorReceipt(tc.Name, result, ctx.Err())
			}

			terminalType := executionPkg.EventCompleted
			if isErr {
				terminalType = executionPkg.EventFailed
				switch {
				case tracked.identity.EffectClass != executionPkg.EffectReadOnly:
					terminalType = executionPkg.EventOutcomeUnknown
				case ctx.Err() != nil:
					terminalType = executionPkg.EventCancelled
				}
			}
			terminalDetail := fmt.Sprintf("backend returned after %dms", duration.Milliseconds())
			if err := appendExecutionEvent(ctx, execRuntime, executionEvent(*tracked, terminalType, executionPkg.ApprovalNotApplicable, result, terminalDetail)); err != nil {
				unresolved := a.unresolvedFor(*tracked, executionPkg.EventStarted, err)
				a.latchUnresolvedExecution(unresolved)
				unknownResult := capToolResultForContext(terminalLedgerFailureReceipt(tc.Name, err), a.numCtx)
				out.ToolCallResult(tc.ID, tc.Name, unknownResult, true, duration)
				a.AppendMessage(llm.Message{Role: "tool", Content: unknownResult, ToolName: tc.Name, ToolCallID: tc.ID})
				a.cancelUndispatchedToolCalls(toolCalls[toolIndex+1:], out, unresolved)
				return unresolved
			}

			result = capToolResultForContext(result, a.numCtx)
			if lg != nil {
				lg.Debug("tool", "name", tc.Name, "kind", kind, "ms", duration.Milliseconds(), "error", isErr)
			}
			out.ToolCallResult(tc.ID, tc.Name, result, isErr, duration)
			a.AppendMessage(llm.Message{
				Role:       "tool",
				Content:    result,
				ToolName:   tc.Name,
				ToolCallID: tc.ID,
			})
			if terminalType == executionPkg.EventOutcomeUnknown && execRuntime.ledger != nil {
				unresolved := a.unresolvedFor(*tracked, executionPkg.EventOutcomeUnknown, fmt.Errorf("durable outcome for tool %q is unknown and requires explicit reconciliation", tc.Name))
				a.latchUnresolvedExecution(unresolved)
				if ledgerErr := a.cancelTrackedToolCalls(ctx, execRuntime, trackedExecutions[toolIndex+1:], toolCalls[toolIndex+1:], out, unresolved); ledgerErr != nil {
					return ledgerErr
				}
				return unresolved
			}
			if ctxErr := ctx.Err(); ctxErr != nil {
				if ledgerErr := a.cancelTrackedToolCalls(ctx, execRuntime, trackedExecutions[toolIndex+1:], toolCalls[toolIndex+1:], out, ctxErr); ledgerErr != nil {
					return ledgerErr
				}
				return ctxErr
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		// Check if we should compact the conversation.
		estimatedPromptTokens := a.estimatePromptTokens(system, tools)
		if estimatedPromptTokens < lastPromptTokens {
			estimatedPromptTokens = lastPromptTokens
		}
		if a.shouldCompact(estimatedPromptTokens) {
			if lg != nil {
				lg.Info("compaction", "iter", i, "prompt_tokens", estimatedPromptTokens, "num_ctx", a.numCtx)
			}
			if a.compact(ctx, out) {
				// Rebuild system prompt after compaction (memory may have changed).
				system = buildSystemPromptForModelBudgetContext(ctx, a.modePrefix, tools, a.skillContent, a.loadedCtx, a.memoryStore, iceContext, a.workDir, a.ignoreContent, a.llmClient.Model(), a.numCtx)
			}
		}

		// Safety: warn if we're about to hit the iteration limit.
		if i == maxIters-2 {
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
	return fmt.Sprintf("OUTCOME UNKNOWN: tool %q ended without a result receipt and may have taken effect: %v", name, err)
}

func dispatchedEffectErrorReceipt(name, backendResult string, contextErr error) string {
	if contextErr != nil {
		return fmt.Sprintf("OUTCOME UNKNOWN: tool %q was cancelled after dispatch and may have taken effect: %v\nBackend result: %s", name, contextErr, backendResult)
	}
	return fmt.Sprintf("OUTCOME UNKNOWN: tool %q returned an error after dispatch and may have partially taken effect. Do not retry automatically; inspect state first.\nBackend result: %s", name, backendResult)
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
		a.deniedToolCall(tc, out, "tool call denied: "+decision.reason)
		return false
	}
	if decision.persistAlways {
		a.persistAlwaysApproval(tc, out)
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

func (a *Agent) failedToolCall(tc llm.ToolCall, out Output, reason string) {
	result := capToolResultForContext(reason, a.numCtx)
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
