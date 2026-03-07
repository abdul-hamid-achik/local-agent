package llm

import (
	"testing"
)

func TestNewModelManager(t *testing.T) {
	m := NewModelManager("http://localhost:11434", 4096)

	if m.baseURL != "http://localhost:11434" {
		t.Errorf("baseURL = %q, want %q", m.baseURL, "http://localhost:11434")
	}
	if m.numCtx != 4096 {
		t.Errorf("numCtx = %d, want %d", m.numCtx, 4096)
	}
	if m.clients == nil {
		t.Error("clients map should be initialized")
	}
}

func TestModelManagerBaseURL(t *testing.T) {
	m := NewModelManager("http://custom:9999", 2048)
	if m.BaseURL() != "http://custom:9999" {
		t.Errorf("BaseURL() = %q, want %q", m.BaseURL(), "http://custom:9999")
	}
}

func TestModelManagerNumCtx(t *testing.T) {
	m := NewModelManager("http://localhost:11434", 8192)
	if m.NumCtx() != 8192 {
		t.Errorf("NumCtx() = %d, want %d", m.NumCtx(), 8192)
	}
}

func TestModelManagerCurrentModel(t *testing.T) {
	m := NewModelManager("http://localhost:11434", 4096)

	// Should return empty when no model set
	if m.CurrentModel() != "" {
		t.Errorf("CurrentModel() = %q, want %q", m.CurrentModel(), "")
	}

	// Set a model
	m.SetCurrentModel("llama3")

	if m.CurrentModel() != "llama3" {
		t.Errorf("CurrentModel() = %q, want %q", m.CurrentModel(), "llama3")
	}
}

func TestModelManagerChatStreamNoModel(t *testing.T) {
	m := NewModelManager("http://localhost:11434", 4096)

	err := m.ChatStream(nil, ChatOptions{}, func(chunk StreamChunk) error {
		return nil
	})

	if err == nil {
		t.Error("ChatStream should fail when no model is set")
	}
}

func TestModelManagerPingNoModel(t *testing.T) {
	m := NewModelManager("http://localhost:11434", 4096)

	err := m.Ping()

	if err == nil {
		t.Error("Ping should fail when no model is set")
	}
}

func TestModelManagerEmbedWithCurrentModelNoModel(t *testing.T) {
	m := NewModelManager("http://localhost:11434", 4096)

	_, err := m.EmbedWithCurrentModel(nil, []string{"test"})

	if err == nil {
		t.Error("EmbedWithCurrentModel should fail when no model is set")
	}
}

func TestModelManagerClose(t *testing.T) {
	m := NewModelManager("http://localhost:11434", 4096)

	// Should not panic
	m.Close()

	if len(m.clients) != 0 {
		t.Errorf("after Close, clients map should be empty, got %d", len(m.clients))
	}
}
