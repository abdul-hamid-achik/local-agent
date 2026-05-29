package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	ollamaapi "github.com/ollama/ollama/api"
)

// pingTimeout bounds the startup/availability check so an unreachable or hung
// Ollama server can never block application startup indefinitely.
const pingTimeout = 10 * time.Second

// OllamaClient implements Client using the official Ollama Go library.
type OllamaClient struct {
	client  *ollamaapi.Client
	model   string
	numCtx  int
	baseURL string
}

// NewOllamaClient creates a new Ollama client.
//
// The base URL is passed directly to the client constructor rather than via
// os.Setenv("OLLAMA_HOST", ...): mutating the global environment is not
// thread-safe and races across concurrent client creation.
func NewOllamaClient(baseURL, model string, numCtx int) (*OllamaClient, error) {
	var client *ollamaapi.Client
	resolvedURL := baseURL
	if baseURL != "" {
		u, err := normalizeOllamaURL(baseURL)
		if err != nil {
			return nil, fmt.Errorf("invalid ollama base url %q: %w", baseURL, err)
		}
		client = ollamaapi.NewClient(u, http.DefaultClient)
		resolvedURL = u.String()
	} else {
		var err error
		client, err = ollamaapi.ClientFromEnvironment()
		if err != nil {
			return nil, fmt.Errorf("create ollama client: %w", err)
		}
		if v := os.Getenv("OLLAMA_HOST"); v != "" {
			resolvedURL = v
		}
	}

	return &OllamaClient{
		client:  client,
		model:   model,
		numCtx:  numCtx,
		baseURL: resolvedURL,
	}, nil
}

func (o *OllamaClient) Model() string { return o.model }

// Ping checks Ollama is running and the model exists.
func (o *OllamaClient) Ping() error {
	ctx, cancel := context.WithTimeout(context.Background(), pingTimeout)
	defer cancel()

	// Check the model is available by requesting a show.
	req := &ollamaapi.ShowRequest{Model: o.model}
	_, err := o.client.Show(ctx, req)
	if err != nil {
		return fmt.Errorf("model %q not available: %w", o.model, err)
	}
	return nil
}

// ChatStream sends a chat request and streams the response via callback.
func (o *OllamaClient) ChatStream(ctx context.Context, opts ChatOptions, fn func(StreamChunk) error) error {

	messages := make([]ollamaapi.Message, 0, len(opts.Messages)+1)
	if opts.System != "" {
		messages = append(messages, ollamaapi.Message{
			Role:    "system",
			Content: opts.System,
		})
	}
	for _, m := range opts.Messages {
		msg := ollamaapi.Message{
			Role:       m.Role,
			Content:    m.Content,
			ToolName:   m.ToolName,
			ToolCallID: m.ToolCallID,
		}
		// Convert tool calls for assistant messages.
		for _, tc := range m.ToolCalls {
			args := ollamaapi.NewToolCallFunctionArguments()
			for k, v := range tc.Arguments {
				args.Set(k, v)
			}
			msg.ToolCalls = append(msg.ToolCalls, ollamaapi.ToolCall{
				ID: tc.ID,
				Function: ollamaapi.ToolCallFunction{
					Name:      tc.Name,
					Arguments: args,
				},
			})
		}
		messages = append(messages, msg)
	}

	tools := convertTools(opts.Tools)
	req := &ollamaapi.ChatRequest{
		Model:    o.model,
		Messages: messages,
		Tools:    tools,
		Options: map[string]any{
			"num_ctx": o.numCtx,
		},
	}

	return o.client.Chat(ctx, req, func(resp ollamaapi.ChatResponse) error {
		chunk := StreamChunk{
			Text: resp.Message.Content,
			Done: resp.Done,
		}
		if resp.Done {
			chunk.EvalCount = resp.EvalCount
			chunk.PromptEvalCount = resp.PromptEvalCount
		}
		// Collect tool calls from the response.
		for _, tc := range resp.Message.ToolCalls {
			chunk.ToolCalls = append(chunk.ToolCalls, ToolCall{
				ID:        tc.ID,
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments.ToMap(),
			})
		}
		return fn(chunk)
	})
}

// Embed generates embeddings for the given texts using the specified model.
func (o *OllamaClient) Embed(ctx context.Context, model string, texts []string) ([][]float32, error) {
	resp, err := o.client.Embed(ctx, &ollamaapi.EmbedRequest{
		Model: model,
		Input: texts,
	})
	if err != nil {
		return nil, fmt.Errorf("embedding failed: %w", err)
	}
	return resp.Embeddings, nil
}

// convertTools transforms our ToolDef slice into Ollama's Tools format.
func convertTools(defs []ToolDef) ollamaapi.Tools {
	if len(defs) == 0 {
		return nil
	}

	tools := make(ollamaapi.Tools, 0, len(defs))
	for _, d := range defs {
		props := ollamaapi.NewToolPropertiesMap()
		var required []string

		// Extract properties from JSON Schema.
		if propsRaw, ok := d.Parameters["properties"].(map[string]any); ok {
			for name, schema := range propsRaw {
				schemaMap, _ := schema.(map[string]any)
				prop := ollamaapi.ToolProperty{
					Description: strFromMap(schemaMap, "description"),
				}
				if t, ok := schemaMap["type"].(string); ok {
					prop.Type = ollamaapi.PropertyType{t}
				}
				if enumRaw, ok := schemaMap["enum"].([]any); ok {
					prop.Enum = enumRaw
				}
				props.Set(name, prop)
			}
		}

		// Extract required fields.
		if reqRaw, ok := d.Parameters["required"].([]any); ok {
			for _, r := range reqRaw {
				if s, ok := r.(string); ok {
					required = append(required, s)
				}
			}
		}

		tools = append(tools, ollamaapi.Tool{
			Type: "function",
			Function: ollamaapi.ToolFunction{
				Name:        d.Name,
				Description: d.Description,
				Parameters: ollamaapi.ToolFunctionParameters{
					Type:       "object",
					Properties: props,
					Required:   required,
				},
			},
		})
	}
	return tools
}

func strFromMap(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	s, _ := m[key].(string)
	return s
}

// BaseURL returns the configured Ollama base URL for display.
func (o *OllamaClient) BaseURL() string {
	if o.baseURL != "" {
		return o.baseURL
	}
	return "http://localhost:11434"
}

// ParseBaseURL validates the Ollama URL.
func ParseBaseURL(rawURL string) (*url.URL, error) {
	return url.Parse(rawURL)
}

// normalizeOllamaURL accepts the same lenient forms the Ollama CLI does for
// OLLAMA_HOST — a bare host ("0.0.0.0"), host:port ("localhost:11434"), or a
// full URL — and returns a fully-qualified *url.URL. A missing scheme defaults
// to http and a missing port to Ollama's default 11434.
func normalizeOllamaURL(raw string) (*url.URL, error) {
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if u.Host == "" {
		return nil, fmt.Errorf("missing host")
	}
	// Default to Ollama's port only for plain http with no explicit port. Never
	// force it onto https/remote endpoints (which almost always listen on 443).
	if u.Scheme == "http" && u.Port() == "" {
		u.Host = u.Host + ":11434"
	}
	return u, nil
}
