package llm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/local-agent/internal/config"
)

func TestRemoteOllamaThroughLoopbackProxy(t *testing.T) {
	t.Setenv("OLLAMA_API_KEY", "unrelated-test-credential")
	var mode atomic.Int32
	var chats atomic.Int32
	cancelled := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("remote profile inherited unrelated Ollama credentials")
		}
		switch r.URL.Path {
		case "/api/tags":
			size := int64(24 << 30)
			alias := ""
			if mode.Load() == 3 {
				size = 40 << 30
			}
			if mode.Load() == 4 {
				alias = `,"remote_host":"other.example"`
			}
			_, _ = fmt.Fprintf(w, `{"models":[{"name":"large:35b","size":%d%s}]}`, size, alias)
		case "/api/show":
			native := 131072
			if mode.Load() == 5 {
				native = 4096
			}
			_, _ = fmt.Fprintf(w, `{"model_info":{"test.context_length":%d},"capabilities":["completion","tools"]}`, native)
		case "/api/chat":
			chats.Add(1)
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), `"num_ctx":65536`) {
				t.Errorf("missing remote allocation: %s", body)
			}
			_, _ = fmt.Fprintln(w, `{"message":{"content":"partial"},"done":false}`)
			w.(http.Flusher).Flush()
			if mode.Load() == 1 {
				return
			}
			if mode.Load() == 2 {
				<-r.Context().Done()
				cancelled <- struct{}{}
				return
			}
			_, _ = fmt.Fprintln(w, `{"done":true,"eval_count":1}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	u, _ := url.Parse(server.URL)
	proxy := httptest.NewServer(httputil.NewSingleHostReverseProxy(u))
	defer proxy.Close()
	profile := config.ProviderProfile{Type: config.ProviderTypeOllamaRemote, BaseURL: proxy.URL, Model: "large:35b", ContextSize: 65536, MaxModelBytes: 32 << 30, StreamIdleTimeout: "10m"}
	manager := NewModelManager("http://127.0.0.1:9", 4096)
	defer manager.Close()
	catalog := config.ProviderConfig{Profiles: map[string]config.ProviderProfile{"gpu": profile}}
	manager.ConfigureProviderCatalog(catalog, true, "small")
	if err := manager.SwitchProvider("gpu"); err == nil {
		t.Fatal("local-only accepted SSH remote")
	}
	manager.ConfigureProviderCatalog(catalog, false, "small")
	if err := manager.SwitchProvider("gpu"); err != nil {
		t.Fatal(err)
	}
	if !manager.RemoteProvider() {
		t.Fatal("loopback erased remote provenance")
	}
	manager.ConfigureOllamaRuntimeInventory(false, nil, false)
	if got, _ := manager.EffectiveContext(profile.Model); got != 65536 {
		t.Fatalf("local inventory erased remote context: %d", got)
	}
	if err := manager.SetCurrentModel("another-model"); err == nil {
		t.Fatal("another model inherited endpoint limits")
	}
	for _, failure := range []int32{3, 4, 5} {
		mode.Store(failure)
		err := manager.ChatStream(context.Background(), ChatOptions{}, func(StreamChunk) error { return nil })
		if !errors.Is(err, ErrInferenceNotStarted) || chats.Load() != 0 {
			t.Fatalf("unsafe model dispatched: %v", err)
		}
	}
	localPolicy := manager.ContextPolicy("small")
	if localPolicy.Request != 4096 || localPolicy.Cloud {
		t.Fatalf("remote context leaked into local policy: %+v", localPolicy)
	}
	mode.Store(1)
	err := manager.ChatStream(context.Background(), ChatOptions{}, func(StreamChunk) error { return nil })
	if !errors.Is(err, io.ErrUnexpectedEOF) || !IsRemoteInferenceError(err) {
		t.Fatalf("lost stream provenance: %v", err)
	}
	mode.Store(2)
	ctx, cancel := context.WithCancel(context.Background())
	err = manager.ChatStream(ctx, ChatOptions{}, func(chunk StreamChunk) error {
		if chunk.Text != "" {
			cancel()
		}
		return nil
	})
	cancel()
	if !errors.Is(err, context.Canceled) || IsRetryableTransport(err) {
		t.Fatalf("cancel = %v", err)
	}
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation did not reach upstream")
	}
	profile.StreamIdleTimeout = "30ms"
	impatient, err := NewRemoteProviderClient(profile)
	if err != nil {
		t.Fatal(err)
	}
	err = impatient.ChatStream(context.Background(), ChatOptions{}, func(StreamChunk) error { return nil })
	if !errors.Is(err, ErrStreamIdle) {
		t.Fatalf("profile watchdog did not fire: %v", err)
	}
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("watchdog did not cancel upstream")
	}
	localClient, err := manager.GetClient(profile.Model)
	if err != nil || localClient.numCtx != 4096 {
		t.Fatalf("remote allocation leaked to local client: %v", err)
	}
	mode.Store(0)
	if err := manager.Ping(); err != nil {
		t.Fatal(err)
	}
	done := false
	if err := manager.ChatStream(context.Background(), ChatOptions{ExpectedContext: 65536}, func(chunk StreamChunk) error { done = chunk.Done; return nil }); err != nil || !done {
		t.Fatalf("recovery failed: %v", err)
	}
	manager.ClearRemoteProvider()
	if policy := manager.ContextPolicy(profile.Model); policy.Request != 4096 || policy.Cloud {
		t.Fatalf("remote policy survived returning to local: %+v", policy)
	}

}
