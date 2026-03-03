package llm

import "context"

// Client is the interface for LLM providers.
type Client interface {
	// ChatStream sends messages to the LLM and streams the response.
	// The callback is called for each chunk. Return a non-nil error to abort.
	ChatStream(ctx context.Context, opts ChatOptions, fn func(StreamChunk) error) error

	// Ping checks if the LLM is reachable and the model is available.
	Ping() error

	// Model returns the current model name.
	Model() string

	// Embed generates embeddings for the given texts using the specified model.
	Embed(ctx context.Context, model string, texts []string) ([][]float32, error)
}

// ChatOptions holds parameters for a chat request.
type ChatOptions struct {
	Messages []Message
	Tools    []ToolDef
	System   string
}

// Message represents a conversation message.
type Message struct {
	Role       string     `json:"role"` // system, user, assistant, tool
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolName   string     `json:"tool_name,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// StreamChunk is a piece of a streaming response.
type StreamChunk struct {
	Text            string     // incremental text content
	ToolCalls       []ToolCall // tool calls (usually in final chunk)
	Done            bool       // true on the last chunk
	EvalCount       int        // tokens generated (only on Done)
	PromptEvalCount int        // prompt tokens evaluated (only on Done)
}

// ToolCall represents a tool invocation requested by the LLM.
type ToolCall struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

// ToolDef defines a tool the LLM can call.
type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"` // JSON Schema
}
