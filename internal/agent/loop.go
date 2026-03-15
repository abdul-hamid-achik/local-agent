package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/abdul-hamid-achik/local-agent/internal/llm"
	permissionPkg "github.com/abdul-hamid-achik/local-agent/internal/permission"
)

// Run executes the ReAct loop: query -> LLM -> tool calls -> observe -> repeat.
// It streams output via the Output interface.
func (a *Agent) Run(ctx context.Context, out Output) {
	var tools []llm.ToolDef
	if a.toolPolicy.AllowMCP && a.registry != nil {
		tools = append(tools, a.registry.Tools()...)
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

	system := buildSystemPromptForModel(a.modePrefix, tools, a.skillContent, a.loadedCtx, a.memoryStore, iceContext, a.workDir, a.ignoreContent, a.llmClient.Model())

	const maxRetries = 2
	var lastPromptTokens int
	var retryCount int

	maxIters := a.MaxIterations()

	for i := 0; i < maxIters; i++ {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Stream LLM response.
		var textBuf strings.Builder
		var toolCalls []llm.ToolCall

		err := a.llmClient.ChatStream(ctx, llm.ChatOptions{
			Messages: a.messages,
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
			if len(chunk.ToolCalls) > 0 {
				toolCalls = append(toolCalls, chunk.ToolCalls...)
			}
			if chunk.Done {
				lastPromptTokens = chunk.PromptEvalCount
				out.StreamDone(chunk.EvalCount, chunk.PromptEvalCount)
			}
			return nil
		})

		if err != nil {
			if ctx.Err() != nil {
				return
			}
			// Retry on transient JSON parse errors from small models.
			if retryCount < maxRetries && isRetryableError(err) {
				retryCount++
				out.Error(fmt.Sprintf("LLM produced malformed output, retrying (%d/%d)...", retryCount, maxRetries))
				textBuf.Reset()
				toolCalls = nil
				continue
			}
			// Show error and provide a fallback response
			out.Error(fmt.Sprintf("LLM error: %v", err))
			// Send a system message explaining the error
			out.SystemMessage(fmt.Sprintf("⚠️ Model response failed: %v\n\nYou can try:\n- Checking if Ollama is running (`ollama ps`)\n- Switching to a different model (ctrl+m)\n- Reducing context size\n\nTool results are still available above.", err))
			return
		}
		retryCount = 0 // reset on success

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
			return
		}

		// Execute each tool call and feed results back.
		// We categorize tools: memory tools and MCP tools can run in parallel,
		// but built-in file tools run sequentially to avoid filesystem conflicts.

		// First, collect all tool calls that need execution
		type pendingTool struct {
			tc           llm.ToolCall
			isMemoryTool bool
			isMCPTool    bool
		}

		var pending []pendingTool

		for _, tc := range toolCalls {
			// Check if this is a built-in memory tool.
			if a.memoryStore != nil && a.isMemoryTool(tc.Name) {
				if !a.toolPolicy.AllowsMemory(tc.Name) {
					a.blockedToolCall(tc, out)
					continue
				}
				pending = append(pending, pendingTool{tc: tc, isMemoryTool: true})
				continue
			}

			// Check if this is a built-in file tool.
			if a.isToolsTool(tc.Name) {
				if !a.toolPolicy.AllowsBuiltin(tc.Name) {
					a.blockedToolCall(tc, out)
					continue
				}
				// Execute built-in tools sequentially (file access safety).
				out.ToolCallStart(tc.Name, tc.Arguments)
				startTime := time.Now()
				result, isErr := a.handleToolsTool(tc)
				duration := time.Since(startTime)
				out.ToolCallResult(tc.Name, result, isErr, duration)
				a.AppendMessage(llm.Message{
					Role:       "tool",
					Content:    result,
					ToolName:   tc.Name,
					ToolCallID: tc.ID,
				})
				continue
			}

			// External MCP tool - check permissions first.
			if !a.toolPolicy.AllowMCP {
				a.blockedToolCall(tc, out)
				continue
			}
			if a.permChecker != nil {
				switch a.permChecker.ToCheckResult(tc.Name) {
				case permissionPkg.CheckDeny:
					errMsg := "tool call blocked by permission policy"
					out.ToolCallStart(tc.Name, tc.Arguments)
					out.ToolCallResult(tc.Name, errMsg, true, 0)
					a.AppendMessage(llm.Message{
						Role:       "tool",
						Content:    errMsg,
						ToolName:   tc.Name,
						ToolCallID: tc.ID,
					})
					continue
				case permissionPkg.CheckAsk:
					if a.approvalCallback != nil {
						allowed, always := permissionPkg.RequestApproval(tc.Name, tc.Arguments, a.approvalCallback)
						if always {
							a.permChecker.SetPolicy(tc.Name, permissionPkg.PolicyAllow)
						}
						if !allowed {
							errMsg := "tool call denied by user"
							out.ToolCallStart(tc.Name, tc.Arguments)
							out.ToolCallResult(tc.Name, errMsg, true, 0)
							a.AppendMessage(llm.Message{
								Role:       "tool",
								Content:    errMsg,
								ToolName:   tc.Name,
								ToolCallID: tc.ID,
							})
							continue
						}
					}
				}
			}

			pending = append(pending, pendingTool{tc: tc, isMCPTool: true})
		}

		// Execute memory tools and MCP tools in parallel.
		if len(pending) > 0 {
			var wg sync.WaitGroup
			mu := sync.Mutex{}
			results := make([]llm.Message, len(pending))

			for i, p := range pending {
				wg.Add(1)
				go func(idx int, tool pendingTool) {
					defer wg.Done()

					tc := tool.tc
					out.ToolCallStart(tc.Name, tc.Arguments)
					startTime := time.Now()

					var result string
					var isErr bool

					if tool.isMemoryTool {
						result, isErr = a.handleMemoryTool(tc)
					} else if tool.isMCPTool {
						toolResult, err := a.registry.CallTool(ctx, tc.Name, tc.Arguments)
						if err != nil {
							// Format error in a way the LLM can understand and recover from
							result = fmt.Sprintf("ERROR: Tool '%s' failed: %v\nThis tool call failed but you can still complete the task with other available information.", tc.Name, err)
							isErr = true
						} else {
							result = toolResult.Content
							isErr = toolResult.IsError
						}
					}

					duration := time.Since(startTime)
					out.ToolCallResult(tc.Name, result, isErr, duration)

					mu.Lock()
					results[idx] = llm.Message{
						Role:       "tool",
						Content:    result,
						ToolName:   tc.Name,
						ToolCallID: tc.ID,
					}
					mu.Unlock()
				}(i, p)
			}

			wg.Wait()

			// Append all results to messages.
			for _, msg := range results {
				if msg.ToolName != "" {
					a.AppendMessage(msg)
				}
			}
		}

		// Check if we should compact the conversation.
		if a.shouldCompact(lastPromptTokens) {
			if a.compact(ctx, out) {
				// Rebuild system prompt after compaction (memory may have changed).
				system = buildSystemPromptForModel(a.modePrefix, tools, a.skillContent, a.loadedCtx, a.memoryStore, iceContext, a.workDir, a.ignoreContent, a.llmClient.Model())
			}
		}

		// Safety: warn if we're about to hit the iteration limit.
		if i == maxIters-2 {
			out.Error(fmt.Sprintf("approaching iteration limit (%d/%d)", i+2, maxIters))
		}
	}

	out.Error(fmt.Sprintf("reached max iterations (%d)", maxIters))
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
	out.ToolCallStart(tc.Name, tc.Arguments)
	out.ToolCallResult(tc.Name, errMsg, true, 0)
	a.AppendMessage(llm.Message{
		Role:       "tool",
		Content:    errMsg,
		ToolName:   tc.Name,
		ToolCallID: tc.ID,
	})
}

// isRetryableError returns true for transient LLM errors that are worth retrying,
// such as JSON parse failures from malformed tool call output.
func isRetryableError(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "parse JSON") || strings.Contains(msg, "unexpected end of JSON")
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
