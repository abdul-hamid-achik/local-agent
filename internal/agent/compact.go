package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/abdul-hamid-achik/local-agent/internal/llm"
)

const compactThreshold = 0.75 // Trigger compaction at 75% of context window
const keepMessages = 4        // Keep the last N messages intact

// shouldCompact returns true if the prompt token count exceeds 75% of numCtx.
func (a *Agent) shouldCompact(promptTokens int) bool {
	if a.numCtx <= 0 || promptTokens <= 0 {
		return false
	}
	return float64(promptTokens) > float64(a.numCtx)*compactThreshold
}

// compact summarizes older messages into a single recap, keeping the last
// keepMessages intact. Returns true if compaction was performed.
func (a *Agent) compact(ctx context.Context, out Output) bool {
	a.mu.RLock()
	msgCount := len(a.messages)
	a.mu.RUnlock()

	if msgCount <= keepMessages+1 {
		return false // Not enough messages to compact.
	}

	a.mu.RLock()
	splitAt := msgCount - keepMessages
	older := make([]llm.Message, splitAt)
	copy(older, a.messages[:splitAt])
	recent := make([]llm.Message, keepMessages)
	copy(recent, a.messages[splitAt:])
	a.mu.RUnlock()

	summary := summarizeMessages(older)

	// Ask LLM to produce a compact summary.
	var summaryBuf strings.Builder
	err := a.llmClient.ChatStream(ctx, llm.ChatOptions{
		Messages: []llm.Message{
			{Role: "user", Content: summary},
		},
		System: "You are a conversation summarizer. Produce a concise summary of the conversation so far, capturing all key facts, decisions, tool results, and user requests. Keep it under 500 words. Output only the summary, no preamble.",
	}, func(chunk llm.StreamChunk) error {
		if chunk.Text != "" {
			summaryBuf.WriteString(chunk.Text)
		}
		return nil
	})

	if err != nil {
		out.Error(fmt.Sprintf("compaction failed: %v", err))
		return false
	}

	summaryText := summaryBuf.String()
	if summaryText == "" {
		return false
	}

	// ICE: persist summary for cross-session retrieval.
	if a.iceEngine != nil {
		if err := a.iceEngine.IndexSummary(ctx, summaryText); err != nil {
			out.Error(fmt.Sprintf("ICE summary indexing failed: %v", err))
		}
	}

	// Replace messages with summary + recent.
	compacted := make([]llm.Message, 0, 1+len(recent))
	compacted = append(compacted, llm.Message{
		Role:    "user",
		Content: fmt.Sprintf("[Conversation summary: %s]", summaryText),
	})
	compacted = append(compacted, recent...)
	a.ReplaceMessages(compacted)

	out.SystemMessage(fmt.Sprintf("Context compacted: %d messages summarized, %d kept", len(older), len(recent)))
	return true
}

// summarizeMessages formats a slice of messages into a human-readable transcript
// for the summarization LLM call.
func summarizeMessages(msgs []llm.Message) string {
	var b strings.Builder
	b.WriteString("Summarize this conversation:\n\n")

	for _, msg := range msgs {
		switch msg.Role {
		case "user":
			fmt.Fprintf(&b, "User: %s\n", msg.Content)
		case "assistant":
			if msg.Content != "" {
				fmt.Fprintf(&b, "Assistant: %s\n", msg.Content)
			}
			for _, tc := range msg.ToolCalls {
				fmt.Fprintf(&b, "Assistant called tool %s(%s)\n", tc.Name, FormatToolArgs(tc.Arguments))
			}
		case "tool":
			content := msg.Content
			if len(content) > 300 {
				content = content[:297] + "..."
			}
			fmt.Fprintf(&b, "Tool %s result: %s\n", msg.ToolName, content)
		}
	}

	return b.String()
}
