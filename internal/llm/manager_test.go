package llm

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
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
	if err := m.SetCurrentModel("llama3"); err != nil {
		t.Fatalf("SetCurrentModel: %v", err)
	}

	if m.CurrentModel() != "llama3" {
		t.Errorf("CurrentModel() = %q, want %q", m.CurrentModel(), "llama3")
	}
}

func TestModelManagerChatStreamNoModel(t *testing.T) {
	m := NewModelManager("http://localhost:11434", 4096)

	err := m.ChatStream(context.Background(), ChatOptions{}, func(chunk StreamChunk) error {
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

	_, err := m.EmbedWithCurrentModel(context.Background(), []string{"test"})

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

func TestModelManagerListLocalModelsExcludesCloud(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			http.NotFound(w, r)
			return
		}
		_, _ = fmt.Fprint(w, `{"models":[
			{"name":"qwen3.5:2b","model":"qwen3.5:2b","size":123},
			{"name":"gemma-cloud","model":"gemma-cloud","remote_model":"gemma:cloud","size":0},
			{"name":"remote-host-only","model":"remote-host-only","remote_host":"https://cloud.invalid","size":999},
			{"name":"qwen3.5:4b","model":"qwen3.5:4b","size":456}
		]}`)
	}))
	defer server.Close()

	manager := NewModelManager(server.URL, 4096)
	got, err := manager.ListLocalModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"qwen3.5:2b", "qwen3.5:4b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("models = %#v, want %#v", got, want)
	}
}

func TestModelManagerLocalOnlyAdmissionRefreshesAndFailsClosed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			http.NotFound(w, r)
			return
		}
		_, _ = fmt.Fprint(w, `{"models":[
			{"name":"qwen3.5:2b","model":"qwen3.5:2b","size":123},
			{"name":"remote:latest","model":"remote:latest","remote_model":"provider/model","size":0}
		]}`)
	}))
	defer server.Close()

	manager := NewModelManager(server.URL, 4096)
	manager.ConfigureLocalOnly(true, nil, false)
	if err := manager.SetCurrentModel("remote:latest"); err == nil || !strings.Contains(err.Error(), "not installed with local Ollama weights") {
		t.Fatalf("remote alias admission error = %v", err)
	}
	if err := manager.SetCurrentModel("qwen3.5:2b"); err != nil {
		t.Fatalf("local model rejected after inventory refresh: %v", err)
	}
	if _, err := manager.GetClient("remote:latest"); err == nil {
		t.Fatal("GetClient bypassed local-only admission")
	}
}

func TestModelManagerLocalOnlyRejectsUnavailableInventory(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	url := server.URL
	server.Close()
	manager := NewModelManager(url, 4096)
	manager.ConfigureLocalOnly(true, nil, false)
	if err := manager.SetCurrentModel("innocent-alias:latest"); err == nil || !strings.Contains(err.Error(), "inventory is unavailable") {
		t.Fatalf("unverified inventory admission error = %v", err)
	}
}

func TestModelManagerLocalOnlyMatchesImplicitLatestTag(t *testing.T) {
	manager := NewModelManager("http://localhost:11434", 4096)
	manager.ConfigureLocalInventory(true, []LocalModel{{Name: "nomic-embed-text:latest", Size: 1 << 30}}, true)
	if err := manager.ensureModelLocal(context.Background(), "nomic-embed-text"); err != nil {
		t.Fatalf("implicit :latest model was rejected: %v", err)
	}
	if err := manager.ensureModelLocal(context.Background(), "nomic-embed-text:v1"); err == nil {
		t.Fatal("a different explicit tag bypassed local-only admission")
	}
}

func TestModelManagerSwitchRefreshesLocalInventory(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			http.NotFound(w, r)
			return
		}
		_, _ = fmt.Fprint(w, `{"models":[]}`)
	}))
	defer server.Close()

	manager := NewModelManager(server.URL, 4096)
	manager.ConfigureLocalInventory(true, []LocalModel{{Name: "qwen3.5:2b", Size: 2 << 30}}, true)
	if err := manager.SetCurrentModel("qwen3.5:2b"); err == nil {
		t.Fatal("model switch trusted a stale startup inventory")
	}
}

func TestModelManagerRejectsOversizedInventoryWeights(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			http.NotFound(w, r)
			return
		}
		_, _ = fmt.Fprint(w, `{"models":[
			{"name":"mixtral:8x7b","model":"mixtral:8x7b","size":10737418240},
			{"name":"deepseek-r1:latest","model":"deepseek-r1:latest","size":10737418240}
		]}`)
	}))
	defer server.Close()
	manager := NewModelManager(server.URL, 4096)
	manager.ConfigureLocalOnly(true, nil, false)
	for _, model := range []string{"mixtral:8x7b", "deepseek-r1:latest"} {
		if err := manager.SetCurrentModel(model); err == nil || !strings.Contains(err.Error(), "above the 8 GiB") {
			t.Fatalf("oversized model %q admission error = %v", model, err)
		}
	}
}

func TestModelManagerUnloadsPreviousModelOnSwitch(t *testing.T) {
	unloaded := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/generate" {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		unloaded <- string(body)
		_, _ = fmt.Fprintln(w, `{"done":true}`)
	}))
	defer server.Close()

	manager := NewModelManager(server.URL, 4096)
	if err := manager.SetCurrentModel("qwen3.5:2b"); err != nil {
		t.Fatal(err)
	}
	manager.mu.Lock()
	manager.active["qwen3.5:2b"] = true
	manager.mu.Unlock()
	if err := manager.SetCurrentModel("gemma4:e2b"); err != nil {
		t.Fatal(err)
	}

	select {
	case body := <-unloaded:
		if !strings.Contains(body, `"model":"qwen3.5:2b"`) || !strings.Contains(body, `"keep_alive":"0s"`) {
			t.Fatalf("unload request = %s", body)
		}
	case <-time.After(time.Second):
		t.Fatal("model switch did not unload the previous model")
	}
}

func TestModelManagerSwitchFailsIfPreviousModelCannotUnload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/generate" {
			http.Error(w, "cannot unload", http.StatusInternalServerError)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	manager := NewModelManager(server.URL, 4096)
	if err := manager.SetCurrentModel("model-a"); err != nil {
		t.Fatal(err)
	}
	manager.mu.Lock()
	manager.active["model-a"] = true
	manager.mu.Unlock()
	if err := manager.SetCurrentModel("model-b"); err == nil {
		t.Fatal("model switch succeeded after unload failure")
	}
	if got := manager.CurrentModel(); got != "model-a" {
		t.Fatalf("current model changed after failed unload: %q", got)
	}
}

func TestModelManagerCloseUnloadsActiveModels(t *testing.T) {
	unloaded := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/generate" {
			http.NotFound(w, r)
			return
		}
		unloaded <- struct{}{}
		_, _ = fmt.Fprintln(w, `{"done":true}`)
	}))
	defer server.Close()

	manager := NewModelManager(server.URL, 4096)
	if err := manager.SetCurrentModel("qwen3.5:2b"); err != nil {
		t.Fatal(err)
	}
	manager.mu.Lock()
	manager.active["qwen3.5:2b"] = true
	manager.mu.Unlock()
	manager.Close()

	select {
	case <-unloaded:
	case <-time.After(time.Second):
		t.Fatal("Close did not unload active model")
	}
	if manager.CurrentModel() != "" {
		t.Fatalf("current model survived close: %q", manager.CurrentModel())
	}
}

func TestModelManagerCloseUnloadsEmbeddingModel(t *testing.T) {
	unloaded := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/embed":
			_, _ = fmt.Fprintln(w, `{"model":"nomic-embed-text","embeddings":[[0.1,0.2]]}`)
		case "/api/generate":
			body, _ := io.ReadAll(r.Body)
			unloaded <- string(body)
			_, _ = fmt.Fprintln(w, `{"done":true}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	manager := NewModelManager(server.URL, 4096)
	if _, err := manager.Embed(context.Background(), "nomic-embed-text", []string{"hello"}); err != nil {
		t.Fatal(err)
	}
	manager.Close()
	select {
	case body := <-unloaded:
		if !strings.Contains(body, `"model":"nomic-embed-text"`) {
			t.Fatalf("unloaded wrong model: %s", body)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not unload resident embedding model")
	}
}

func TestModelManagerSwitchWaitsForActiveInference(t *testing.T) {
	chatStarted := make(chan struct{}, 1)
	releaseChat := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/chat":
			chatStarted <- struct{}{}
			<-releaseChat
			_, _ = fmt.Fprintln(w, `{"message":{"role":"assistant","content":"done"},"done":true}`)
		case "/api/generate":
			_, _ = fmt.Fprintln(w, `{"done":true}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	manager := NewModelManager(server.URL, 4096)
	if err := manager.SetCurrentModel("model-a"); err != nil {
		t.Fatal(err)
	}
	chatDone := make(chan error, 1)
	go func() {
		chatDone <- manager.ChatStream(context.Background(), ChatOptions{}, func(StreamChunk) error { return nil })
	}()
	select {
	case <-chatStarted:
	case <-time.After(time.Second):
		t.Fatal("chat did not start")
	}

	switchDone := make(chan error, 1)
	go func() { switchDone <- manager.SetCurrentModel("model-b") }()
	select {
	case err := <-switchDone:
		t.Fatalf("model switched during active inference: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	close(releaseChat)
	select {
	case err := <-chatDone:
		if err != nil {
			t.Fatalf("ChatStream: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("chat did not finish")
	}
	select {
	case err := <-switchDone:
		if err != nil {
			t.Fatalf("SetCurrentModel: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("model switch stayed blocked after inference completed")
	}
}
