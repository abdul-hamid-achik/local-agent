package main

import (
	"reflect"
	"testing"

	"github.com/abdul-hamid-achik/local-agent/internal/config"
)

type selectionRouterStub struct {
	models      []string
	resolved    string
	headless    string
	headlessHit bool
}

func (r *selectionRouterStub) SetAvailableModels(models []string) {
	r.models = append([]string(nil), models...)
}

func (r *selectionRouterStub) ResolveAvailableModel(string) string { return r.resolved }
func (r *selectionRouterStub) SelectModel(string) string           { return r.headless }
func (r *selectionRouterStub) SelectModelForMode(string, config.ModeContext) string {
	r.headlessHit = true
	return r.headless
}
func (r *selectionRouterStub) RecordOverride(string, string) {}
func (r *selectionRouterStub) GetModelForCapability(config.ModelCapability) string {
	return r.headless
}
func (r *selectionRouterStub) ListModels() []config.Model { return config.DefaultModels() }

func TestResolveStartupModelValidatesPinnedLocalInventory(t *testing.T) {
	cfg := config.DefaultModelConfig()
	discovered := []string{"nomic-embed-text", "qwen3.5:2b", "gemma4:e2b", "unknown:latest"}
	router := &selectionRouterStub{resolved: "qwen3.5:2b"}

	selected, models, err := resolveStartupModel("gemma4:e2b", true, true, &cfg, discovered, true, router)
	if err != nil {
		t.Fatalf("locally installed profile pin rejected: %v", err)
	}
	if selected != "gemma4:e2b" {
		t.Fatalf("selected = %q, want profile pin", selected)
	}
	wantModels := []string{"qwen3.5:2b", "gemma4:e2b"}
	if !reflect.DeepEqual(models, wantModels) {
		t.Fatalf("model list = %#v, want %#v", models, wantModels)
	}
	if !reflect.DeepEqual(router.models, wantModels) {
		t.Fatalf("router inventory = %#v, want %#v", router.models, wantModels)
	}

	_, _, err = resolveStartupModel("qwen3.5:cloud", true, true, &cfg, discovered, true, router)
	if err == nil {
		t.Fatal("cloud profile pin accepted")
	}
	_, _, err = resolveStartupModel("qwen3.5:4b", true, true, &cfg, discovered, true, router)
	if err == nil {
		t.Fatal("missing local --model pin accepted")
	}
}

func TestResolveStartupModelAcceptsPinnedImplicitLatestTag(t *testing.T) {
	cfg := config.ModelConfig{
		Models:       []config.Model{{Name: "llama3", Family: config.FamilyLlama}},
		DefaultModel: "llama3",
	}
	router := &selectionRouterStub{resolved: "llama3"}
	selected, models, err := resolveStartupModel("llama3", true, true, &cfg, []string{"llama3:latest"}, true, router)
	if err != nil {
		t.Fatalf("implicit :latest pin rejected: %v", err)
	}
	if selected != "llama3" || !reflect.DeepEqual(models, []string{"llama3"}) {
		t.Fatalf("selection = %q, models=%#v", selected, models)
	}
}

func TestResolveStartupModelAcceptsPinnedOrnith(t *testing.T) {
	cfg := config.DefaultModelConfig()
	router := &selectionRouterStub{resolved: "qwen3.5:2b"}
	selected, models, err := resolveStartupModel(
		"ornith:latest", true, true, &cfg, []string{"ornith:latest"}, true, router,
	)
	if err != nil {
		t.Fatalf("installed Ornith pin rejected: %v", err)
	}
	if selected != "ornith:latest" || !reflect.DeepEqual(models, []string{"ornith:latest"}) {
		t.Fatalf("selection = %q, models=%#v", selected, models)
	}
}

func TestResolveStartupModelEmptyInventoryAndOfflineDiagnostics(t *testing.T) {
	cfg := config.DefaultModelConfig()
	router := &selectionRouterStub{resolved: ""}

	if _, _, err := resolveStartupModel(cfg.DefaultModel, false, true, &cfg, []string{}, true, router); err == nil {
		t.Fatal("known empty inventory accepted an automatic model")
	}

	selected, models, err := resolveStartupModel(cfg.DefaultModel, false, true, &cfg, nil, false, router)
	if err != nil {
		t.Fatalf("offline diagnostic startup rejected: %v", err)
	}
	if selected != cfg.DefaultModel || len(models) == 0 {
		t.Fatalf("offline fallback = %q, %#v", selected, models)
	}

	t.Setenv("LOCAL_AGENT_ALLOW_LARGE_MODELS", "1")
	if _, _, err := resolveStartupModel("qwen3.5:cloud", true, true, &cfg, nil, false, router); err == nil {
		t.Fatal("offline inventory plus hardware override accepted cloud alias")
	}
}

func TestSelectHeadlessModelHonorsAnyPin(t *testing.T) {
	router := &selectionRouterStub{headless: "qwen3.5:4b"}
	if got := selectHeadlessModel("gemma4:e2b", "build it", true, router, config.ModeBuildContext); got != "gemma4:e2b" {
		t.Fatalf("profile pin was routed to %q", got)
	}
	if router.headlessHit {
		t.Fatal("router called for pinned headless model")
	}

	if got := selectHeadlessModel("qwen3.5:2b", "build it", false, router, config.ModeBuildContext); got != "qwen3.5:4b" {
		t.Fatalf("automatic headless selection = %q", got)
	}
	if !router.headlessHit {
		t.Fatal("router not called for automatic headless model")
	}
}

func TestDisabledAutoSelectPinsConfiguredModelAcrossSurfaces(t *testing.T) {
	if !shouldPinStartupModel("", false) {
		t.Fatal("model.auto_select=false did not create a startup pin")
	}
	router := &selectionRouterStub{headless: "qwen3.5:4b"}
	if got := selectHeadlessModel("gemma4:e2b", "build it", shouldPinStartupModel("", false), router, config.ModeBuildContext); got != "gemma4:e2b" {
		t.Fatalf("disabled auto-selection routed headless model to %q", got)
	}
	if router.headlessHit {
		t.Fatal("headless router ran while model.auto_select=false")
	}

	if shouldPinStartupModel("", true) {
		t.Fatal("default automatic routing was unexpectedly pinned")
	}
	if !shouldPinStartupModel("qwen3.5:2b", true) {
		t.Fatal("explicit --model override was not pinned")
	}
}

var _ config.ModelRouter = (*selectionRouterStub)(nil)
var _ availabilityAwareRouter = (*selectionRouterStub)(nil)
