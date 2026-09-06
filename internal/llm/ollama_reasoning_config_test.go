package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/abdul-hamid-achik/local-agent/internal/config"
	"gopkg.in/yaml.v3"
)

func TestConfiguredOllamaReasoningAppliesToInteractiveAndExplicitModel(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(fmt.Sprintf("explicit=%v", explicit), func(t *testing.T) {
			requests := make(chan *bool, 4)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request ollamaChatRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				requests <- request.Think
				_, _ = fmt.Fprintln(w, `{"message":{"role":"assistant","content":"ok"},"done":true}`)
			}))
			defer server.Close()
			manager := NewModelManager(server.URL, 8192)
			if err := manager.SetCurrentModel("small:0.8b"); err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct{ configured, requested bool }{{true, false}, {false, false}, {false, true}} {
				var cfg config.OllamaConfig
				if err := yaml.Unmarshal(fmt.Appendf(nil, "disable_reasoning: %v\n", tc.configured), &cfg); err != nil {
					t.Fatal(err)
				}
				manager.ConfigureOllamaReasoning(cfg.DisableReasoning)
				options := ChatOptions{DisableReasoning: tc.requested}
				emit := func(StreamChunk) error { return nil }
				var err error
				if explicit {
					err = manager.ChatStreamForModel(context.Background(), "small:0.8b", options, emit)
				} else {
					err = manager.ChatStream(context.Background(), options, emit)
				}
				if err != nil {
					t.Fatal(err)
				}
				got := <-requests
				if tc.configured || tc.requested {
					if got == nil || *got {
						t.Fatalf("think=%v, want explicit false", got)
					}
				} else if got != nil {
					t.Fatalf("default think=%v, want omitted", *got)
				}
			}
		})
	}
}
