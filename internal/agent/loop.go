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
	if a.toolsEnabled {
		tools = a.registry.Tools()
		// Merge memory built-in tools if available.
		if a.memoryStore != nil {
			tools = append(tools, a.memoryBuiltinToolDefs()...)
		}
		// Merge built-in file tools (grep, read, write, glob, bash, ls, find).
		tools = append(tools, a.toolsBuiltinToolDefs()...)
	}

	// ICE: index user message and assemble cross-session context.
	var iceContext string
	if a.iceEngine != nil && len(a.messages) > 0 {
		lastMsg := a.messages[len(a.messages)-1]
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

	for i := range maxIterations {
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
			out.Error(fmt.Sprintf("LLM error: %v", err))
			return
		}
		retryCount = 0 // reset on success

		// Record assistant message in conversation history.
		assistantMsg := llm.Message{
			Role:      "assistant",
			Content:   textBuf.String(),
			ToolCalls: toolCalls,
		}
		a.messages = append(a.messages, assistantMsg)

		// ICE: index assistant message.
		if a.iceEngine != nil && assistantMsg.Content != "" {
			if err := a.iceEngine.IndexMessage(ctx, "assistant", assistantMsg.Content); err != nil {
				out.Error(fmt.Sprintf("ICE indexing failed: %v", err))
			}
		}

		// If no tool calls, we're done.
		if len(toolCalls) == 0 {
			// ICE: detect auto-memories from the exchange.
			if a.iceEngine != nil && len(a.messages) >= 2 {
				userContent := ""
				for idx := len(a.messages) - 2; idx >= 0; idx-- {
					if a.messages[idx].Role == "user" {
						userContent = a.messages[idx].Content
						break
					}
				}
				if userContent != "" {
					a.iceEngine.DetectAutoMemory(ctx, userContent, assistantMsg.Content)
				}
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
				pending = append(pending, pendingTool{tc: tc, isMemoryTool: true})
				continue
			}

			// Check if this is a built-in file tool.
			if a.isToolsTool(tc.Name) {
				// Execute built-in tools sequentially (file access safety).
				out.ToolCallStart(tc.Name, tc.Arguments)
				startTime := time.Now()
				result, isErr := a.handleToolsTool(tc)
				duration := time.Since(startTime)
				out.ToolCallResult(tc.Name, result, isErr, duration)
				a.messages = append(a.messages, llm.Message{
					Role:       "tool",
					Content:    result,
					ToolName:   tc.Name,
					ToolCallID: tc.ID,
				})
				continue
			}

			// External MCP tool - check permissions first.
			if a.permChecker != nil {
				switch a.permChecker.ToCheckResult(tc.Name) {
				case permissionPkg.CheckDeny:
					errMsg := "tool call blocked by permission policy"
					out.ToolCallStart(tc.Name, tc.Arguments)
					out.ToolCallResult(tc.Name, errMsg, true, 0)
					a.messages = append(a.messages, llm.Message{
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
							a.messages = append(a.messages, llm.Message{
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
							result = fmt.Sprintf("tool error: %v", err)
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
					a.messages = append(a.messages, msg)
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
		if i == maxIterations-2 {
			out.Error(fmt.Sprintf("approaching iteration limit (%d/%d)", i+2, maxIterations))
		}
	}

	out.Error(fmt.Sprintf("reached max iterations (%d)", maxIterations))
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
