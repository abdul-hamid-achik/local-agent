package llm

import (
	"context"
	"fmt"
	"net/url"
	"os"

	ollamaapi "github.com/ollama/ollama/api"
)

// OllamaClient implements Client using the official Ollama Go library.
type OllamaClient struct {
	client *ollamaapi.Client
	model  string
	numCtx int
}

// NewOllamaClient creates a new Ollama client.
func NewOllamaClient(baseURL, model string, numCtx int) (*OllamaClient, error) {
	// The official client reads OLLAMA_HOST, but we want to support our config too.
	if baseURL != "" {
		os.Setenv("OLLAMA_HOST", baseURL)
	}

	client, err := ollamaapi.ClientFromEnvironment()
	if err != nil {
		return nil, fmt.Errorf("create ollama client: %w", err)
	}

	return &OllamaClient{
		client: client,
		model:  model,
		numCtx: numCtx,
	}, nil
}

func (o *OllamaClient) Model() string { return o.model }

// Ping checks Ollama is running and the model exists.
func (o *OllamaClient) Ping() error {
	ctx := context.Background()

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
	if v := os.Getenv("OLLAMA_HOST"); v != "" {
		return v
	}
	return "http://localhost:11434"
}

// ParseBaseURL validates the Ollama URL.
func ParseBaseURL(rawURL string) (*url.URL, error) {
	return url.Parse(rawURL)
}
