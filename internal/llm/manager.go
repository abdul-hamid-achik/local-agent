package llm

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/abdul-hamid-achik/local-agent/internal/config"
)

type ModelManager struct {
	baseURL      string
	numCtx       int
	clients      map[string]*OllamaClient
	active       map[string]bool
	currentModel string
	mu           sync.RWMutex
	switchMu     sync.Mutex
	inferenceMu  sync.RWMutex
	localMu      sync.RWMutex
	localOnly    bool
	localKnown   bool
	localModels  map[string]int64
	localChecked time.Time
}

const localInventoryTTL = 30 * time.Second

// LocalModel records an Ollama model identity and the byte size of its local
// weights. Remote/cloud entries never appear in this inventory.
type LocalModel struct {
	Name string
	Size int64
}

var _ Client = (*ModelManager)(nil)

func NewModelManager(baseURL string, numCtx int) *ModelManager {
	return &ModelManager{
		baseURL: baseURL,
		numCtx:  numCtx,
		clients: make(map[string]*OllamaClient),
		active:  make(map[string]bool),
	}
}

func (m *ModelManager) GetClient(modelName string) (*OllamaClient, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return m.getClient(ctx, modelName)
}

func (m *ModelManager) getClient(ctx context.Context, modelName string) (*OllamaClient, error) {
	if err := m.ensureModelLocal(ctx, modelName); err != nil {
		return nil, err
	}
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
	inventoryCtx, cancelInventory := context.WithTimeout(context.Background(), 2*time.Second)
	if err := m.ensureModelLocalFresh(inventoryCtx, model); err != nil {
		cancelInventory()
		return err
	}
	cancelInventory()
	m.switchMu.Lock()
	defer m.switchMu.Unlock()
	m.inferenceMu.Lock()
	defer m.inferenceMu.Unlock()

	client, err := NewOllamaClient(m.baseURL, model, m.numCtx)
	if err != nil {
		return fmt.Errorf("create client for %s: %w", model, err)
	}

	m.mu.RLock()
	previousName := m.currentModel
	previousClient := m.clients[previousName]
	previousActive := m.active[previousName]
	m.mu.RUnlock()
	if previousName != "" && previousName != model && previousClient != nil && previousActive {
		unloadCtx, cancelUnload := context.WithTimeout(context.Background(), 5*time.Second)
		err := previousClient.Unload(unloadCtx)
		cancelUnload()
		if err != nil {
			return fmt.Errorf("unload previous model %q before switching to %q: %w", previousName, model, err)
		}
	}

	m.mu.Lock()
	m.clients[model] = client
	m.currentModel = model
	if previousName != "" && previousName != model {
		m.active[previousName] = false
	}
	m.mu.Unlock()
	return nil
}

func (m *ModelManager) CurrentModel() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.currentModel
}

func (m *ModelManager) ChatStream(ctx context.Context, opts ChatOptions, fn func(StreamChunk) error) error {
	m.inferenceMu.RLock()
	defer m.inferenceMu.RUnlock()
	m.mu.RLock()
	model := m.currentModel
	m.mu.RUnlock()

	if model == "" {
		return fmt.Errorf("no model selected")
	}

	client, err := m.getClient(ctx, model)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.active[model] = true
	m.mu.Unlock()
	return client.ChatStream(ctx, opts, fn)
}

func (m *ModelManager) ChatStreamForModel(ctx context.Context, model string, opts ChatOptions, fn func(StreamChunk) error) error {
	m.inferenceMu.RLock()
	defer m.inferenceMu.RUnlock()
	client, err := m.getClient(ctx, model)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.active[model] = true
	m.mu.Unlock()
	return client.ChatStream(ctx, opts, fn)
}

func (m *ModelManager) Ping() error {
	m.inferenceMu.RLock()
	defer m.inferenceMu.RUnlock()
	m.mu.RLock()
	model := m.currentModel
	m.mu.RUnlock()

	if model == "" {
		return fmt.Errorf("no model selected")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	client, err := m.getClient(ctx, model)
	if err != nil {
		return err
	}
	return client.Ping()
}

func (m *ModelManager) PingModel(ctx context.Context, model string) error {
	m.inferenceMu.RLock()
	defer m.inferenceMu.RUnlock()
	client, err := m.getClient(ctx, model)
	if err != nil {
		return err
	}
	return client.PingContext(ctx)
}

// ListLocalModels returns only models with local weights. Ollama cloud entries
// are deliberately excluded so a "local-only" routing decision cannot
// silently cross the network.
func (m *ModelManager) ListLocalModels(ctx context.Context) ([]string, error) {
	inventory, err := m.ListLocalModelInventory(ctx)
	if err != nil {
		return nil, err
	}
	models := make([]string, len(inventory))
	for i, model := range inventory {
		models[i] = model.Name
	}
	return models, nil
}

// ListLocalModelInventory returns local identities with their actual weight
// sizes so memory admission never has to infer safety from a tag string.
func (m *ModelManager) ListLocalModelInventory(ctx context.Context) ([]LocalModel, error) {
	client, err := NewOllamaClient(m.baseURL, "", m.numCtx)
	if err != nil {
		return nil, err
	}
	response, err := client.listModels(ctx)
	if err != nil {
		return nil, fmt.Errorf("list Ollama models: %w", err)
	}

	models := make([]LocalModel, 0, len(response))
	seen := make(map[string]struct{}, len(response))
	for _, model := range response {
		if model.RemoteModel != "" || model.RemoteHost != "" || model.Size <= 0 {
			continue
		}
		name := model.Model
		if name == "" {
			name = model.Name
		}
		if name == "" {
			continue
		}
		canonical := config.CanonicalModelName(name)
		if _, duplicate := seen[canonical]; duplicate {
			continue
		}
		seen[canonical] = struct{}{}
		models = append(models, LocalModel{Name: name, Size: model.Size})
	}
	sort.Slice(models, func(i, j int) bool { return models[i].Name < models[j].Name })
	return models, nil
}

// ConfigureLocalOnly requires all inference model names to be proven by an
// Ollama inventory entry with local weights. An unverified inventory keeps the
// UI available for diagnostics, but no client can be selected until a later
// operation successfully refreshes the inventory.
func (m *ModelManager) ConfigureLocalOnly(required bool, models []string, verified bool) {
	inventory := make([]LocalModel, len(models))
	for i, model := range models {
		inventory[i] = LocalModel{Name: model, Size: -1}
	}
	m.ConfigureLocalInventory(required, inventory, verified)
}

// ConfigureLocalInventory installs a verified local-weight inventory. The
// legacy name-only method intentionally records unknown sizes and therefore
// cannot admit inference in local-only mode.
func (m *ModelManager) ConfigureLocalInventory(required bool, models []LocalModel, verified bool) {
	m.localMu.Lock()
	defer m.localMu.Unlock()
	m.localOnly = required
	m.localKnown = verified
	if verified {
		m.localChecked = time.Now()
	} else {
		m.localChecked = time.Time{}
	}
	m.localModels = make(map[string]int64, len(models))
	for _, model := range models {
		if canonical := config.CanonicalModelName(model.Name); canonical != "" {
			m.localModels[canonical] = model.Size
		}
	}
}

func (m *ModelManager) ensureModelLocal(ctx context.Context, model string) error {
	return m.ensureModelLocalWithRefresh(ctx, model, false)
}

func (m *ModelManager) ensureModelLocalFresh(ctx context.Context, model string) error {
	return m.ensureModelLocalWithRefresh(ctx, model, true)
}

func (m *ModelManager) ensureModelLocalWithRefresh(ctx context.Context, model string, forceRefresh bool) error {
	canonical := config.CanonicalModelName(model)
	if canonical == "" {
		return fmt.Errorf("model name is required")
	}
	m.localMu.RLock()
	required := m.localOnly
	known := m.localKnown
	checked := m.localChecked
	size, allowed := m.localModels[canonical]
	m.localMu.RUnlock()
	if !required {
		return nil
	}
	if !known || !allowed || forceRefresh || time.Since(checked) >= localInventoryTTL {
		models, err := m.ListLocalModelInventory(ctx)
		if err != nil {
			return fmt.Errorf("local-only model inventory is unavailable: %w", err)
		}
		m.ConfigureLocalInventory(true, models, true)
		allowed = false
		for _, candidate := range models {
			if config.CanonicalModelName(candidate.Name) == canonical {
				allowed = true
				size = candidate.Size
				break
			}
		}
	}
	if !allowed {
		return fmt.Errorf("model %q is not installed with local Ollama weights", model)
	}
	if err := config.CheckLocalModelSizeSafe(model, size); err != nil {
		return fmt.Errorf("local-only model admission: %w", err)
	}
	return nil
}

func (m *ModelManager) Embed(ctx context.Context, model string, texts []string) ([][]float32, error) {
	m.inferenceMu.RLock()
	defer m.inferenceMu.RUnlock()
	client, err := m.getClient(ctx, model)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	// Embedding weights are resident too. Chat switches only evict the
	// previous current chat model, while Close unloads every resident client.
	m.active[model] = true
	m.mu.Unlock()
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
	m.switchMu.Lock()
	defer m.switchMu.Unlock()
	m.inferenceMu.Lock()
	defer m.inferenceMu.Unlock()

	m.mu.Lock()
	activeClients := make([]*OllamaClient, 0, len(m.active))
	for name, active := range m.active {
		if active && m.clients[name] != nil {
			activeClients = append(activeClients, m.clients[name])
		}
	}
	m.clients = make(map[string]*OllamaClient)
	m.active = make(map[string]bool)
	m.currentModel = ""
	m.mu.Unlock()

	for _, client := range activeClients {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = client.Unload(ctx)
		cancel()
	}
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
