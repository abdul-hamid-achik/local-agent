package llm

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/abdul-hamid-achik/local-agent/internal/config"
)

// RemoteClient is the shared dispatch surface for native Ollama and
// OpenAI-compatible remote profiles. Embeddings retain their local client.
type RemoteClient interface {
	Client
	PingContext(context.Context) error
	SetModel(string) error
}

func NewRemoteProviderClient(profile config.ProviderProfile) (RemoteClient, error) {
	profile = profile.Resolve()
	if err := config.ValidateProviderProfile("remote", profile, false); err != nil {
		return nil, err
	}
	key, err := profile.ResolveAPIKey()
	if err != nil {
		return nil, err
	}
	if profile.Type != config.ProviderTypeOllamaRemote {
		return NewOpenAICompatibleClient(OpenAICompatibleOptions{BaseURL: profile.BaseURL, Model: profile.Model, APIKey: key})
	}
	client, err := NewOllamaClient(profile.BaseURL, profile.Model, profile.ContextSize)
	if err != nil {
		return nil, err
	}
	client.remote = true
	client.maxModelBytes = profile.MaxModelBytes
	// Only this profile's credential may travel to its destination.
	client.authHeader = ""
	if key != "" {
		client.authHeader = "Bearer " + key
	}
	if profile.StreamIdleTimeout != "" {
		client.streamIdleTimeout, _ = time.ParseDuration(profile.StreamIdleTimeout)
	}
	return client, nil
}

// Native remote profiles pin model and capacity together. Switching to a
// different model requires selecting its profile, never inheriting its budget.
func (o *OllamaClient) SetModel(model string) error {
	if config.CanonicalModelName(model) != config.CanonicalModelName(o.model) {
		return fmt.Errorf("ollama profile serves model %q; select a profile for another model", o.model)
	}
	return nil
}

func (o *OllamaClient) checkRemoteModel(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	models, err := o.listModels(ctx)
	if err != nil {
		return err
	}
	for _, model := range models {
		name := model.Name
		if name == "" {
			name = model.Model
		}
		if config.CanonicalModelName(name) != config.CanonicalModelName(o.model) {
			continue
		}
		// Tags must describe weights on this daemon, not another remote alias.
		if strings.TrimSpace(model.RemoteHost) != "" || strings.TrimSpace(model.RemoteModel) != "" || model.Size <= 0 || model.Size > o.maxModelBytes {
			return fmt.Errorf("remote model %q is outside this endpoint's weight limit or redirects inference", o.model)
		}
		info, err := o.ShowModel(ctx, o.model)
		if err != nil {
			return err
		}
		if !slices.Contains(info.Capabilities, "completion") || !slices.Contains(info.Capabilities, "tools") {
			return fmt.Errorf("remote model %q does not verify completion and tools capabilities", o.model)
		}
		if info.NativeContext < int64(o.numCtx) {
			return fmt.Errorf("remote model %q does not verify the configured context_size", o.model)
		}
		return nil
	}
	return fmt.Errorf("remote model %q is not installed on the configured endpoint", o.model)
}
