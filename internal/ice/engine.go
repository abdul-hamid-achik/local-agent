package ice

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/abdulachik/local-agent/internal/llm"
	"github.com/abdulachik/local-agent/internal/memory"
)

// EngineConfig holds the configuration needed to create an Engine.
type EngineConfig struct {
	EmbedModel string
	StorePath  string
	NumCtx     int
}

// Engine is the ICE coordinator. It owns the embedder, conversation store,
// and assembler, and exposes high-level methods for the agent loop.
type Engine struct {
	embedder   *Embedder
	store      *Store
	memStore   *memory.Store
	budgetCfg  BudgetConfig
	sessionID  string
	turnIndex  int
	autoMemory *AutoMemory
}

// NewEngine creates an ICE engine. Returns an error if the store path
// cannot be determined.
func NewEngine(client llm.Client, memStore *memory.Store, cfg EngineConfig) (*Engine, error) {
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

	return &Engine{
		embedder:   NewEmbedder(client, embedModel),
		store:      NewStore(storePath),
		memStore:   memStore,
		budgetCfg:  DefaultBudgetConfig(cfg.NumCtx),
		sessionID:  sessionID,
		autoMemory: &AutoMemory{client: client, memStore: memStore},
	}, nil
}

// AssembleContext retrieves relevant past context for the given query.
func (e *Engine) AssembleContext(ctx context.Context, query string) (string, error) {
	a := &Assembler{
		embedder:  e.embedder,
		convStore: e.store,
		memStore:  e.memStore,
		budgetCfg: e.budgetCfg,
		sessionID: e.sessionID,
	}
	return a.Assemble(ctx, query)
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

	e.turnIndex++
	_, err = e.store.Add(e.sessionID, role, text, emb, e.turnIndex)
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

	_, err = e.store.Add(e.sessionID, "summary", summary, emb, e.turnIndex)
	return err
}

// DetectAutoMemory runs auto-memory detection in a background goroutine.
func (e *Engine) DetectAutoMemory(ctx context.Context, userMsg, assistantMsg string) {
	if e.autoMemory == nil {
		return
	}
	go func() {
		_ = e.autoMemory.Detect(ctx, userMsg, assistantMsg)
	}()
}

// Flush persists any pending changes to the conversation store.
func (e *Engine) Flush() error {
	return e.store.Flush()
}

// Store returns the underlying conversation store.
func (e *Engine) Store() *Store {
	return e.store
}

// SessionID returns the current session identifier.
func (e *Engine) SessionID() string {
	return e.sessionID
}
