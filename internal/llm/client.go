package llm

import (
	"context"
	"errors"
)

// ErrNoModelSelected is a local preflight rejection. No provider request or
// generation can have started when a Client returns this sentinel.
var ErrNoModelSelected = errors.New("no model selected")

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
	Messages      []Message
	Tools         []ToolDef
	System        string
	MaxEvalTokens int // zero leaves provider generation uncapped
	// ExpectedContext pins a host-side context budget to the request. Provider
	// managers use it to reject a turn whose model policy changed after the
	// agent took its budget snapshot. Direct clients may ignore it.
	ExpectedContext int
}

// Message represents a conversation message.
type Message struct {
	Role    string `json:"role"` // system, user, assistant, tool
	Content string `json:"content"`
	// DurableContent is the bounded replacement for transient tool content when
	// history crosses a persistence or compaction boundary. It is host-only:
	// providers receive Content, while JSON/session/checkpoint writers must call
	// agent.SanitizeMessagesForPersistence before serialization.
	DurableContent string     `json:"-"`
	ToolCalls      []ToolCall `json:"tool_calls,omitempty"`
	ToolName       string     `json:"tool_name,omitempty"`
	ToolCallID     string     `json:"tool_call_id,omitempty"`
	// HostOwned marks a message whose exact contents were validated and
	// authored by the local host. It is deliberately not persisted or sent on
	// the wire: restore code must re-derive the marker from durable state, so a
	// user-authored message cannot forge host authority through JSON history.
	HostOwned bool `json:"-"`
}

// StreamChunk is a piece of a streaming response.
type StreamChunk struct {
	Text            string     // incremental text content
	Reasoning       string     // provider-native thinking/reasoning delta
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
	// DisplayName and Behavior are host-only MCP presentation metadata.
	// They must never be sent to model providers as part of a tool schema.
	DisplayName string       `json:"-"`
	Behavior    ToolBehavior `json:"-"`
}

// ToolBehavior is the bounded presentation projection of standard MCP tool
// annotations. It is untrusted server metadata and must never by itself alter
// authorization, durable effect classification, or recovery semantics.
type ToolBehavior struct {
	Declared    bool
	ReadOnly    bool
	Destructive bool
	Idempotent  bool
	OpenWorld   bool
}
