package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/abdul-hamid-achik/local-agent/internal/db"
	"github.com/abdul-hamid-achik/local-agent/internal/llm"
)

const compactThreshold = 0.75 // Trigger compaction at 75% of context window
const keepMessages = 4        // Keep the last N messages intact

type contextCompactionOutput interface {
	ContextCompacted()
}

// shouldCompact returns true if the prompt token count exceeds 75% of the
// currently effective context window.
func (a *Agent) shouldCompact(promptTokens int) bool {
	return shouldCompactForContext(promptTokens, a.NumCtx())
}

// shouldCompactForContext evaluates a turn against its context-window
// snapshot. Keeping the snapshot explicit prevents a model switch from
// changing compaction policy halfway through a turn.
func shouldCompactForContext(promptTokens, numCtx int) bool {
	if numCtx <= 0 || promptTokens <= 0 {
		return false
	}
	return float64(promptTokens) > float64(numCtx)*compactThreshold
}

// compact summarizes older messages into a single recap, keeping the last
// keepMessages intact. Returns true if compaction was performed.
func (a *Agent) compact(ctx context.Context, out Output) bool {
	return a.compactForContext(ctx, out, a.NumCtx())
}

// compactForContext compacts using the immutable context-window snapshot for
// the active turn.
func (a *Agent) compactForContext(ctx context.Context, out Output, numCtx int) bool {
	a.mu.RLock()
	messages := make([]llm.Message, len(a.messages))
	copy(messages, a.messages)
	a.mu.RUnlock()
	msgCount := len(messages)

	if msgCount <= keepMessages+1 {
		return false // Not enough messages to compact.
	}

	splitAt := recentConversationBoundary(messages, keepMessages)
	if splitAt <= 0 {
		return false
	}
	older := append([]llm.Message(nil), messages[:splitAt]...)
	recent := append([]llm.Message(nil), messages[splitAt:]...)

	summary := summarizeMessages(older)
	if budget := optionalPromptBudget(numCtx); budget > 0 {
		summary = boundPromptText(summary, budget)
	}

	// Ask LLM to produce a compact summary.
	var summaryBuf strings.Builder
	err := a.llmClient.ChatStream(ctx, llm.ChatOptions{
		Messages: []llm.Message{
			{Role: "user", Content: summary},
		},
		System:          "You are a conversation summarizer. Produce a concise summary of the conversation so far, capturing all key facts, decisions, tool results, and user requests. Keep it under 500 words. Output only the summary, no preamble.",
		ExpectedContext: numCtx,
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

	// Snapshot the full pre-compaction history first so compaction is
	// non-destructive. If persistence was configured, replacing history without
	// its recovery checkpoint would violate that contract, so fail closed.
	if a.checkpointStore != nil {
		checkpointID, err := a.CreateCheckpoint(ctx, "before compaction", db.CheckpointPreCompaction)
		if err != nil || checkpointID == 0 {
			if err == nil {
				err = fmt.Errorf("checkpoint store returned no checkpoint id")
			}
			if a.logger != nil {
				a.logger.Warn("pre-compaction checkpoint failed", "err", err)
			}
			out.Error(fmt.Sprintf("compaction preserved full history because its recovery checkpoint failed: %v", err))
			return false
		}
	}

	// Replace messages with summary + recent.
	compacted := make([]llm.Message, 0, 1+len(recent))
	compacted = append(compacted, llm.Message{
		Role:    "system",
		Content: fmt.Sprintf("Conversation summary:\n%s", summaryText),
	})
	compacted = append(compacted, recent...)
	a.ReplaceMessages(compacted)

	if reporter, ok := out.(contextCompactionOutput); ok {
		reporter.ContextCompacted()
	}
	out.SystemMessage(fmt.Sprintf("Context compacted: %d messages summarized, %d kept", len(older), len(recent)))
	return true
}

// recentConversationBoundary chooses a user-turn boundary so an assistant
// tool call is never separated from its tool result. Prefer the next user turn
// after the raw keep-N split; otherwise retain the current turn in full.
func recentConversationBoundary(messages []llm.Message, keep int) int {
	if keep <= 0 || len(messages) <= keep {
		return 0
	}
	tentative := len(messages) - keep
	if messages[tentative].Role == "user" {
		return tentative
	}
	for i := tentative + 1; i < len(messages); i++ {
		if messages[i].Role == "user" {
			return i
		}
	}
	for i := tentative - 1; i > 0; i-- {
		if messages[i].Role == "user" {
			return i
		}
	}
	return 0
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
