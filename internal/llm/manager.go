package llm

import (
	"context"
	"fmt"
	"sync"
)

type ModelManager struct {
	baseURL      string
	numCtx       int
	clients      map[string]*OllamaClient
	currentModel string
	mu           sync.RWMutex
}

var _ Client = (*ModelManager)(nil)

func NewModelManager(baseURL string, numCtx int) *ModelManager {
	return &ModelManager{
		baseURL: baseURL,
		numCtx:  numCtx,
		clients: make(map[string]*OllamaClient),
	}
}

func (m *ModelManager) GetClient(modelName string) (*OllamaClient, error) {
	m.mu.RLock()
	client, exists := m.clients[modelName]
	m.mu.RUnlock()

	if exists {
		return client, nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if client, exists := m.clients[modelName]; exists {
		return client, nil
	}

	client, err := NewOllamaClient(m.baseURL, modelName, m.numCtx)
	if err != nil {
		return nil, fmt.Errorf("create client for %s: %w", modelName, err)
	}

	m.clients[modelName] = client
	return client, nil
}

func (m *ModelManager) SetCurrentModel(model string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	client, err := NewOllamaClient(m.baseURL, model, m.numCtx)
	if err != nil {
		return fmt.Errorf("create client for %s: %w", model, err)
	}

	m.clients[model] = client
	m.currentModel = model
	return nil
}

func (m *ModelManager) CurrentModel() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.currentModel
}

func (m *ModelManager) ChatStream(ctx context.Context, opts ChatOptions, fn func(StreamChunk) error) error {
	m.mu.RLock()
	model := m.currentModel
	m.mu.RUnlock()

	if model == "" {
		return fmt.Errorf("no model selected")
	}

	client, err := m.GetClient(model)
	if err != nil {
		return err
	}
	return client.ChatStream(ctx, opts, fn)
}

func (m *ModelManager) ChatStreamForModel(ctx context.Context, model string, opts ChatOptions, fn func(StreamChunk) error) error {
	client, err := m.GetClient(model)
	if err != nil {
		return err
	}
	return client.ChatStream(ctx, opts, fn)
}

func (m *ModelManager) Ping() error {
	m.mu.RLock()
	model := m.currentModel
	m.mu.RUnlock()

	if model == "" {
		return fmt.Errorf("no model selected")
	}

	client, err := m.GetClient(model)
	if err != nil {
		return err
	}
	return client.Ping()
}

func (m *ModelManager) PingModel(model string) error {
	client, err := m.GetClient(model)
	if err != nil {
		return err
	}
	return client.Ping()
}

func (m *ModelManager) Embed(ctx context.Context, model string, texts []string) ([][]float32, error) {
	client, err := m.GetClient(model)
	if err != nil {
		return nil, err
	}
	return client.Embed(ctx, model, texts)
}

func (m *ModelManager) EmbedWithCurrentModel(ctx context.Context, texts []string) ([][]float32, error) {
	m.mu.RLock()
	model := m.currentModel
	m.mu.RUnlock()

	if model == "" {
		return nil, fmt.Errorf("no model selected")
	}
	return m.Embed(ctx, model, texts)
}

func (m *ModelManager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for range m.clients {
	}
	m.clients = make(map[string]*OllamaClient)
}

func (m *ModelManager) BaseURL() string {
	return m.baseURL
}

func (m *ModelManager) NumCtx() int {
	return m.numCtx
}

func (m *ModelManager) Model() string {
	return m.CurrentModel()
}
