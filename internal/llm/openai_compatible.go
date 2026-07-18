package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/abdul-hamid-achik/local-agent/internal/netpolicy"
)

const (
	maxOpenAIStreamRecordBytes = 8 << 20
	maxOpenAIResponseBytes     = 16 << 20
	maxOpenAIErrorBytes        = 1 << 20
)

// OpenAICompatibleClient is a streaming chat adapter for OpenAI-compatible
// HTTP APIs (xAI, OpenAI, OpenRouter, local vLLM, etc.). Credentials are
// supplied by the process environment only — never from config files.
type OpenAICompatibleClient struct {
	httpClient *http.Client
	base       *url.URL
	baseURL    string
	model      string
	apiKey     string
	mu         sync.RWMutex
}

// OpenAICompatibleOptions constructs a remote OpenAI-style client.
type OpenAICompatibleOptions struct {
	BaseURL string
	Model   string
	APIKey  string
}

// NewOpenAICompatibleClient builds a client against baseURL (for example
// https://api.x.ai/v1). The API key may be empty only for local open servers.
func NewOpenAICompatibleClient(opts OpenAICompatibleOptions) (*OpenAICompatibleClient, error) {
	model := strings.TrimSpace(opts.Model)
	if model == "" {
		return nil, errors.New("openai-compatible model is empty")
	}
	raw := strings.TrimSpace(opts.BaseURL)
	if raw == "" {
		return nil, errors.New("openai-compatible base_url is empty")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("invalid openai-compatible base url %q", opts.BaseURL)
	}
	u.Path = strings.TrimRight(u.Path, "/")
	client := newOpenAIHTTPClient(u)
	return &OpenAICompatibleClient{
		httpClient: client,
		base:       u,
		baseURL:    strings.TrimSuffix(u.String(), "/"),
		model:      model,
		apiKey:     strings.TrimSpace(opts.APIKey),
	}, nil
}

func newOpenAIHTTPClient(base *url.URL) *http.Client {
	dialer := &net.Dialer{}
	transport := &http.Transport{Proxy: http.ProxyFromEnvironment}
	if defaultTransport, ok := http.DefaultTransport.(*http.Transport); ok {
		transport = defaultTransport.Clone()
	}
	if netpolicy.IsLocalHost(base.Hostname()) {
		transport.Proxy = nil
		transport.DialContext = netpolicy.LocalOnlyDialContext(net.DefaultResolver, dialer.DialContext, "OpenAI-compatible")
	}
	originScheme := strings.ToLower(base.Scheme)
	originHost := strings.ToLower(base.Host)
	return &http.Client{
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			if strings.ToLower(req.URL.Scheme) != originScheme || strings.ToLower(req.URL.Host) != originHost {
				return fmt.Errorf("refusing cross-origin provider redirect to %s", req.URL.Redacted())
			}
			return nil
		},
	}
}

func (c *OpenAICompatibleClient) Model() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.model
}

// SetModel updates the model id for subsequent requests.
func (c *OpenAICompatibleClient) SetModel(model string) error {
	model = strings.TrimSpace(model)
	if model == "" {
		return errors.New("openai-compatible model is empty")
	}
	c.mu.Lock()
	c.model = model
	c.mu.Unlock()
	return nil
}

func (c *OpenAICompatibleClient) BaseURL() string { return c.baseURL }

// Ping checks that the API key and model are usable.
func (c *OpenAICompatibleClient) Ping() error {
	ctx, cancel := context.WithTimeout(context.Background(), pingTimeout)
	defer cancel()
	return c.PingContext(ctx)
}

// PingContext prefers GET /models when available, then falls back to a minimal
// non-streaming chat completion so APIs without a models list still work.
func (c *OpenAICompatibleClient) PingContext(ctx context.Context) error {
	if err := c.pingModels(ctx); err == nil {
		return nil
	}
	return c.pingChat(ctx)
}

func (c *OpenAICompatibleClient) pingModels(ctx context.Context) error {
	req, err := c.newRequest(ctx, http.MethodGet, "/models", nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("provider models: %w", err)
	}
	defer func() {
		// The bounded read below determines the operation result. Closing a
		// fully consumed response body cannot make that response unsuccessful.
		_ = resp.Body.Close()
	}()
	body, err := readBoundedBody(resp.Body, maxOpenAIResponseBytes)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return openAIStatusError(resp, body)
	}
	var listing struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &listing); err != nil {
		return fmt.Errorf("decode provider models: %w", err)
	}
	model := c.Model()
	if len(listing.Data) == 0 {
		return nil // some hosts return empty lists; chat ping will catch hard failures
	}
	for _, item := range listing.Data {
		if item.ID == model {
			return nil
		}
	}
	// List succeeded but model was absent — still allow; hosts vary.
	return nil
}

func (c *OpenAICompatibleClient) pingChat(ctx context.Context) error {
	payload := openAIChatRequest{
		Model: c.Model(),
		Messages: []openAIMessage{{
			Role:    "user",
			Content: "ping",
		}},
		Stream:      false,
		MaxTokens:   1,
		Temperature: floatPtr(0),
	}
	var response openAIChatCompletion
	if err := c.doJSON(ctx, http.MethodPost, "/chat/completions", payload, &response); err != nil {
		return fmt.Errorf("model %q not available: %w", c.Model(), err)
	}
	return nil
}

// Embed is not implemented for the generic OpenAI-compatible chat adapter.
// ICE and local embeddings continue to use Ollama when enabled.
func (c *OpenAICompatibleClient) Embed(context.Context, string, []string) ([][]float32, error) {
	return nil, errors.New("openai-compatible provider does not implement embeddings; keep ICE on Ollama or disable it")
}

// ChatStream streams a chat completion via the OpenAI SSE protocol.
func (c *OpenAICompatibleClient) ChatStream(ctx context.Context, opts ChatOptions, fn func(StreamChunk) error) error {
	if fn == nil {
		return errors.New("openai-compatible stream callback is nil")
	}
	messages, err := convertOpenAIMessages(opts)
	if err != nil {
		return inferenceNotStarted(err)
	}
	payload := openAIChatRequest{
		Model:    c.Model(),
		Messages: messages,
		Stream:   true,
		Tools:    convertOpenAITools(opts.Tools),
	}
	if opts.MaxEvalTokens > 0 {
		payload.MaxTokens = opts.MaxEvalTokens
	}
	// DisableReasoning is best-effort; many hosts ignore unknown fields.
	if opts.DisableReasoning {
		payload.ReasoningEffort = "none"
	}

	req, err := c.newRequest(ctx, http.MethodPost, "/chat/completions", payload)
	if err != nil {
		return inferenceNotStarted(err)
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("provider chat stream: %w", err)
	}
	defer func() {
		// Stream completion/error is established while reading; a later close
		// error carries no additional provider outcome information.
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := readBoundedBody(resp.Body, maxOpenAIErrorBytes)
		return openAIStatusError(resp, body)
	}

	return c.consumeSSE(resp.Body, fn)
}

func (c *OpenAICompatibleClient) consumeSSE(body io.Reader, fn func(StreamChunk) error) error {
	reader := bufio.NewReaderSize(body, 64*1024)
	toolAccum := map[int]*toolCallBuilder{}
	sawDone := false
	idle := time.Now()

	for {
		if time.Since(idle) > streamIdleTimeout {
			return ErrStreamIdle
		}
		line, err := reader.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				if !sawDone {
					// Some servers close without a terminal chunk after the last delta.
					chunk := StreamChunk{Done: true, ToolCalls: finalizeToolCalls(toolAccum)}
					if err := fn(chunk); err != nil {
						return err
					}
				}
				return nil
			}
			return fmt.Errorf("read provider stream: %w", err)
		}
		idle = time.Now()
		line = strings.TrimRight(line, "\r\n")
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			chunk := StreamChunk{Done: true, ToolCalls: finalizeToolCalls(toolAccum)}
			return fn(chunk)
		}
		if len(data) > maxOpenAIStreamRecordBytes {
			return fmt.Errorf("provider stream record exceeds %d-byte limit", maxOpenAIStreamRecordBytes)
		}
		var event openAIStreamChunk
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return fmt.Errorf("decode provider stream: %w", err)
		}
		if event.Error != nil && event.Error.Message != "" {
			return errors.New(event.Error.Message)
		}
		if len(event.Choices) == 0 {
			continue
		}
		choice := event.Choices[0]
		delta := choice.Delta
		chunk := StreamChunk{
			Text:      delta.Content,
			Reasoning: firstNonEmpty(delta.ReasoningContent, delta.Reasoning),
		}
		for _, call := range delta.ToolCalls {
			idx := call.Index
			builder, ok := toolAccum[idx]
			if !ok {
				builder = &toolCallBuilder{}
				toolAccum[idx] = builder
			}
			if call.ID != "" {
				builder.id = call.ID
			}
			if call.Function.Name != "" {
				builder.name += call.Function.Name
			}
			if call.Function.Arguments != "" {
				builder.arguments.WriteString(call.Function.Arguments)
			}
		}
		finish := choice.FinishReason
		if finish == "stop" || finish == "tool_calls" || finish == "length" {
			chunk.Done = true
			chunk.ToolCalls = finalizeToolCalls(toolAccum)
			sawDone = true
			if event.Usage != nil {
				chunk.EvalCount = event.Usage.CompletionTokens
				chunk.PromptEvalCount = event.Usage.PromptTokens
			}
		}
		if err := fn(chunk); err != nil {
			return err
		}
		if chunk.Done {
			return nil
		}
	}
}

type toolCallBuilder struct {
	id        string
	name      string
	arguments strings.Builder
}

func finalizeToolCalls(builders map[int]*toolCallBuilder) []ToolCall {
	if len(builders) == 0 {
		return nil
	}
	// Preserve index order.
	max := -1
	for idx := range builders {
		if idx > max {
			max = idx
		}
	}
	out := make([]ToolCall, 0, len(builders))
	for i := 0; i <= max; i++ {
		builder, ok := builders[i]
		if !ok {
			continue
		}
		args := map[string]any{}
		raw := builder.arguments.String()
		if strings.TrimSpace(raw) != "" {
			if err := json.Unmarshal([]byte(raw), &args); err != nil {
				// Keep a single string field so the host still sees the payload.
				args = map[string]any{"_raw": raw}
			}
		}
		id := builder.id
		if id == "" {
			id = fmt.Sprintf("call_%d", i)
		}
		out = append(out, ToolCall{
			ID:        id,
			Name:      builder.name,
			Arguments: args,
		})
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func floatPtr(v float64) *float64 { return &v }

type openAIChatRequest struct {
	Model           string          `json:"model"`
	Messages        []openAIMessage `json:"messages"`
	Stream          bool            `json:"stream"`
	Tools           []openAITool    `json:"tools,omitempty"`
	MaxTokens       int             `json:"max_tokens,omitempty"`
	Temperature     *float64        `json:"temperature,omitempty"`
	ReasoningEffort string          `json:"reasoning_effort,omitempty"`
}

type openAIMessage struct {
	Role       string           `json:"role"`
	Content    any              `json:"content,omitempty"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
	Name       string           `json:"name,omitempty"`
}

type openAIContentPart struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	ImageURL *openAIImageURL `json:"image_url,omitempty"`
}

type openAIImageURL struct {
	URL string `json:"url"`
}

type openAITool struct {
	Type     string             `json:"type"`
	Function openAIToolFunction `json:"function"`
}

type openAIToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters"`
}

type openAIToolCall struct {
	Index    int                    `json:"index,omitempty"`
	ID       string                 `json:"id,omitempty"`
	Type     string                 `json:"type,omitempty"`
	Function openAIToolCallFunction `json:"function"`
}

type openAIToolCallFunction struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type openAIStreamChunk struct {
	Choices []openAIStreamChoice `json:"choices"`
	Usage   *openAIUsage         `json:"usage,omitempty"`
	Error   *openAIErrorBody     `json:"error,omitempty"`
}

type openAIStreamChoice struct {
	Delta        openAIStreamDelta `json:"delta"`
	FinishReason string            `json:"finish_reason"`
}

type openAIStreamDelta struct {
	Content          string           `json:"content,omitempty"`
	ReasoningContent string           `json:"reasoning_content,omitempty"`
	Reasoning        string           `json:"reasoning,omitempty"`
	ToolCalls        []openAIToolCall `json:"tool_calls,omitempty"`
}

type openAIChatCompletion struct {
	Choices []struct {
		Message openAIMessage `json:"message"`
	} `json:"choices"`
	Error *openAIErrorBody `json:"error,omitempty"`
}

type openAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

type openAIErrorBody struct {
	Message string `json:"message"`
	Type    string `json:"type,omitempty"`
	Code    any    `json:"code,omitempty"`
}

type openAIHTTPError struct {
	StatusCode int
	Status     string
	Message    string
}

func (e *openAIHTTPError) Error() string {
	if e.Message == "" {
		return e.Status
	}
	return fmt.Sprintf("%s: %s", e.Status, e.Message)
}

func openAIStatusError(resp *http.Response, body []byte) error {
	message := strings.TrimSpace(string(body))
	var envelope struct {
		Error openAIErrorBody `json:"error"`
	}
	if json.Unmarshal(body, &envelope) == nil && envelope.Error.Message != "" {
		message = envelope.Error.Message
	}
	return &openAIHTTPError{
		StatusCode: resp.StatusCode,
		Status:     resp.Status,
		Message:    message,
	}
}

func convertOpenAIMessages(opts ChatOptions) ([]openAIMessage, error) {
	out := make([]openAIMessage, 0, len(opts.Messages)+1)
	if opts.System != "" {
		out = append(out, openAIMessage{Role: "system", Content: opts.System})
	}
	for i, message := range opts.Messages {
		converted := openAIMessage{
			Role:       message.Role,
			ToolCallID: message.ToolCallID,
			Name:       message.ToolName,
		}
		if len(message.ToolCalls) > 0 {
			for index, call := range message.ToolCalls {
				args, err := json.Marshal(call.Arguments)
				if err != nil {
					return nil, fmt.Errorf("message %d tool call %d: encode arguments: %w", i, index, err)
				}
				id := call.ID
				if id == "" {
					id = fmt.Sprintf("call_%d", index)
				}
				converted.ToolCalls = append(converted.ToolCalls, openAIToolCall{
					Index: index,
					ID:    id,
					Type:  "function",
					Function: openAIToolCallFunction{
						Name:      call.Name,
						Arguments: string(args),
					},
				})
			}
		}
		if len(message.Images) > 0 {
			parts := make([]openAIContentPart, 0, len(message.Images)+1)
			if message.Content != "" {
				parts = append(parts, openAIContentPart{Type: "text", Text: message.Content})
			}
			for imageIndex, image := range message.Images {
				if err := image.Validate(); err != nil {
					return nil, fmt.Errorf("message %d image %d: %w", i, imageIndex, err)
				}
				dataURL := "data:" + image.MediaType + ";base64," + base64.StdEncoding.EncodeToString(image.Data)
				parts = append(parts, openAIContentPart{
					Type:     "image_url",
					ImageURL: &openAIImageURL{URL: dataURL},
				})
			}
			converted.Content = parts
		} else {
			converted.Content = message.Content
		}
		// OpenAI tool role messages need content string.
		if message.Role == "tool" && converted.Content == nil {
			converted.Content = ""
		}
		out = append(out, converted)
	}
	return out, nil
}

func convertOpenAITools(tools []ToolDef) []openAITool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]openAITool, 0, len(tools))
	for _, tool := range tools {
		params := tool.Parameters
		if params == nil {
			params = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		out = append(out, openAITool{
			Type: "function",
			Function: openAIToolFunction{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  params,
			},
		})
	}
	return out
}

func (c *OpenAICompatibleClient) doJSON(ctx context.Context, method, route string, payload, output any) error {
	req, err := c.newRequest(ctx, method, route, payload)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		// The bounded read below determines the operation result. Closing a
		// fully consumed response body cannot make that response unsuccessful.
		_ = resp.Body.Close()
	}()
	body, err := readBoundedBody(resp.Body, maxOpenAIResponseBytes)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return openAIStatusError(resp, body)
	}
	if output == nil || len(body) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, output); err != nil {
		return fmt.Errorf("decode provider response: %w", err)
	}
	return nil
}

func (c *OpenAICompatibleClient) newRequest(ctx context.Context, method, route string, payload any) (*http.Request, error) {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("encode provider request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	endpoint := c.base.ResolveReference(&url.URL{Path: joinURLPath(c.base.Path, route)})
	req, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err != nil {
		return nil, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("User-Agent", "local-agent/openai-compatible")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	return req, nil
}

func joinURLPath(basePath, route string) string {
	basePath = strings.TrimRight(basePath, "/")
	if !strings.HasPrefix(route, "/") {
		route = "/" + route
	}
	if basePath == "" {
		return route
	}
	return basePath + route
}
