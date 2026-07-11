package ice

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/abdul-hamid-achik/local-agent/internal/llm"
	"github.com/abdul-hamid-achik/local-agent/internal/memory"
)

// EngineConfig holds the configuration needed to create an Engine.
type EngineConfig struct {
	EmbedModel string
	StorePath  string
	NumCtx     int
	Workspace  string
}

// Engine is the ICE coordinator. It owns the embedder, conversation store,
// and assembler, and exposes high-level methods for the agent loop.
type Engine struct {
	embedder   *Embedder
	store      *Store
	memStore   *memory.Store
	budgetCfg  BudgetConfig
	sessionID  string
	projectID  string
	turnIndex  int
	autoMemory *AutoMemory
	mu         sync.Mutex
	autoMu     sync.Mutex
	autoCancel context.CancelFunc
	autoWG     sync.WaitGroup
	lifecycle  context.Context
	close      context.CancelFunc
}

// NewEngine creates an ICE engine. Returns an error if the store path
// cannot be determined.
func NewEngine(client llm.Client, memStore *memory.Store, cfg EngineConfig) (*Engine, error) {
	if strings.TrimSpace(cfg.Workspace) == "" {
		return nil, fmt.Errorf("workspace identity is required for scoped ICE retrieval")
	}
	storePath := cfg.StorePath
	if storePath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("determine home dir: %w", err)
		}
		storePath = filepath.Join(home, ".config", "local-agent", "conversations.json")
	}

	embedModel := cfg.EmbedModel
	if embedModel == "" {
		embedModel = defaultEmbedModel
	}

	sessionID := fmt.Sprintf("s_%d", time.Now().UnixNano())
	lifecycle, cancel := context.WithCancel(context.Background())

	store := NewStore(storePath)
	if err := store.Err(); err != nil {
		cancel()
		return nil, err
	}
	return &Engine{
		embedder:   NewEmbedder(client, embedModel),
		store:      store,
		memStore:   memStore,
		budgetCfg:  DefaultBudgetConfig(cfg.NumCtx),
		sessionID:  sessionID,
		projectID:  workspaceID(cfg.Workspace),
		autoMemory: &AutoMemory{client: client, memStore: memStore},
		lifecycle:  lifecycle,
		close:      cancel,
	}, nil
}

// AssembleContext retrieves relevant past context for the given query.
func (e *Engine) AssembleContext(ctx context.Context, query string) (string, error) {
	e.mu.Lock()
	memStore := e.memStore
	e.mu.Unlock()
	a := &Assembler{
		embedder:  e.embedder,
		convStore: e.store,
		memStore:  memStore,
		budgetCfg: e.budgetCfg,
		sessionID: e.sessionID,
		projectID: e.projectID,
	}
	return a.Assemble(ctx, query)
}

// SetMemoryStore swaps the project-scoped store after an explicit legacy
// claim. Background auto-memory is joined first so it cannot retain and later
// persist the pre-claim empty store.
func (e *Engine) SetMemoryStore(store *memory.Store) {
	if e == nil {
		return
	}
	e.StopAutoMemory()
	e.mu.Lock()
	e.memStore = store
	if e.autoMemory != nil {
		e.autoMemory.memStore = store
	}
	e.mu.Unlock()
}

// ScopedEntryCount excludes quarantined provenance-free entries.
func (e *Engine) ScopedEntryCount() int {
	if e == nil || e.store == nil {
		return 0
	}
	return e.store.CountScoped(e.projectID)
}

// IndexMessage embeds and stores a conversation message.
// Content is truncated to 2000 characters before embedding.
func (e *Engine) IndexMessage(ctx context.Context, role, content string) error {
	if content == "" {
		return nil
	}

	text := content
	if len(text) > 2000 {
		text = text[:2000]
	}

	emb, err := e.embedder.Embed(ctx, text)
	if err != nil {
		return fmt.Errorf("embed message: %w", err)
	}

	e.mu.Lock()
	e.turnIndex++
	turnIndex := e.turnIndex
	e.mu.Unlock()
	_, err = e.store.AddScoped(e.projectID, e.sessionID, role, text, emb, turnIndex)
	return err
}

// IndexSummary embeds and stores a compaction summary.
func (e *Engine) IndexSummary(ctx context.Context, summary string) error {
	if summary == "" {
		return nil
	}

	emb, err := e.embedder.Embed(ctx, summary)
	if err != nil {
		return fmt.Errorf("embed summary: %w", err)
	}

	e.mu.Lock()
	turnIndex := e.turnIndex
	e.mu.Unlock()
	_, err = e.store.AddScoped(e.projectID, e.sessionID, "summary", summary, emb, turnIndex)
	return err
}

// DetectAutoMemory runs auto-memory detection in a background goroutine.
//
// It uses the engine lifecycle with its own timeout so normal turn completion
// does not kill it immediately. A new foreground turn cancels the job to yield
// local inference resources, and Close joins it before shutdown.
func (e *Engine) DetectAutoMemory(ctx context.Context, userMsg, assistantMsg string) {
	if e.autoMemory == nil {
		return
	}
	select {
	case <-ctx.Done():
		return
	default:
	}
	e.CancelAutoMemory()
	e.autoMu.Lock()
	jobCtx, cancel := context.WithTimeout(e.lifecycle, 30*time.Second)
	e.autoCancel = cancel
	e.autoWG.Add(1)
	e.autoMu.Unlock()
	go func() {
		defer e.autoWG.Done()
		defer cancel()
		_ = e.autoMemory.Detect(jobCtx, userMsg, assistantMsg)
	}()
}

// CancelAutoMemory yields inference resources to a new foreground turn.
func (e *Engine) CancelAutoMemory() {
	e.autoMu.Lock()
	defer e.autoMu.Unlock()
	if e.autoCancel != nil {
		e.autoCancel()
		e.autoCancel = nil
	}
}

// StopAutoMemory cancels and joins background inference before a model switch.
func (e *Engine) StopAutoMemory() {
	e.CancelAutoMemory()
	e.autoWG.Wait()
}

// Flush persists any pending changes to the conversation store.
func (e *Engine) Flush() error {
	return e.store.Flush()
}

// Close cancels and joins background memory extraction before flushing.
func (e *Engine) Close() error {
	e.close()
	e.StopAutoMemory()
	return e.Flush()
}

// Store returns the underlying conversation store.
func (e *Engine) Store() *Store {
	return e.store
}

// SessionID returns the current session identifier.
func (e *Engine) SessionID() string {
	return e.sessionID
}

func workspaceID(workspace string) string {
	if workspace == "" {
		return ""
	}
	canonical, err := filepath.Abs(workspace)
	if err == nil {
		if resolved, resolveErr := filepath.EvalSymlinks(canonical); resolveErr == nil {
			canonical = resolved
		}
	}
	sum := sha256.Sum256([]byte(canonical))
	return fmt.Sprintf("workspace:%x", sum[:8])
}
