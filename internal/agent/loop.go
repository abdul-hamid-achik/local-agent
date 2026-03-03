package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/abdulachik/local-agent/internal/llm"
)

// Run executes the ReAct loop: query -> LLM -> tool calls -> observe -> repeat.
// It streams output via the Output interface.
func (a *Agent) Run(ctx context.Context, out Output) {
	tools := a.registry.Tools()
	// Merge memory built-in tools if available.
	if a.memoryStore != nil {
		tools = append(tools, a.memoryBuiltinToolDefs()...)
	}

	// ICE: index user message and assemble cross-session context.
	var iceContext string
	if a.iceEngine != nil && len(a.messages) > 0 {
		lastMsg := a.messages[len(a.messages)-1]
		if lastMsg.Role == "user" {
			_ = a.iceEngine.IndexMessage(ctx, "user", lastMsg.Content)
			if assembled, err := a.iceEngine.AssembleContext(ctx, lastMsg.Content); err == nil {
				iceContext = assembled
			}
		}
	}

	system := buildSystemPrompt(tools, a.skillContent, a.loadedCtx, a.memoryStore, iceContext)

	var lastPromptTokens int

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
			out.Error(fmt.Sprintf("LLM error: %v", err))
			return
		}

		// Record assistant message in conversation history.
		assistantMsg := llm.Message{
			Role:      "assistant",
			Content:   textBuf.String(),
			ToolCalls: toolCalls,
		}
		a.messages = append(a.messages, assistantMsg)

		// ICE: index assistant message.
		if a.iceEngine != nil && assistantMsg.Content != "" {
			_ = a.iceEngine.IndexMessage(ctx, "assistant", assistantMsg.Content)
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
		for _, tc := range toolCalls {
			out.ToolCallStart(tc.Name, tc.Arguments)
			startTime := time.Now()

			// Check if this is a built-in memory tool.
			if a.memoryStore != nil && a.isMemoryTool(tc.Name) {
				result, isErr := a.handleMemoryTool(tc)
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

			result, err := a.registry.CallTool(ctx, tc.Name, tc.Arguments)
			duration := time.Since(startTime)
			if err != nil {
				errMsg := fmt.Sprintf("tool error: %v", err)
				out.ToolCallResult(tc.Name, errMsg, true, duration)
				a.messages = append(a.messages, llm.Message{
					Role:       "tool",
					Content:    errMsg,
					ToolName:   tc.Name,
					ToolCallID: tc.ID,
				})
				continue
			}

			out.ToolCallResult(tc.Name, result.Content, result.IsError, duration)
			a.messages = append(a.messages, llm.Message{
				Role:       "tool",
				Content:    result.Content,
				ToolName:   tc.Name,
				ToolCallID: tc.ID,
			})
		}

		// Check if we should compact the conversation.
		if a.shouldCompact(lastPromptTokens) {
			if a.compact(ctx, out) {
				// Rebuild system prompt after compaction (memory may have changed).
				system = buildSystemPrompt(tools, a.skillContent, a.loadedCtx, a.memoryStore, iceContext)
			}
		}

		// Safety: warn if we're about to hit the iteration limit.
		if i == maxIterations-2 {
			out.Error(fmt.Sprintf("approaching iteration limit (%d/%d)", i+2, maxIterations))
		}
	}

	out.Error(fmt.Sprintf("reached max iterations (%d)", maxIterations))
}

// FormatToolArgs formats tool arguments as a compact JSON string for display.
func FormatToolArgs(args map[string]any) string {
	if len(args) == 0 {
		return "{}"
	}
	b, err := json.Marshal(args)
	if err != nil {
		return "{...}"
	}
	s := string(b)
	if len(s) > 200 {
		return s[:197] + "..."
	}
	return s
}
