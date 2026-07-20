package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/abdul-hamid-achik/local-agent/internal/config"
	"github.com/abdul-hamid-achik/local-agent/internal/resource"
)

// handleContextWindowCommand implements /context status|auto|set.
func (m *Model) handleContextWindowCommand(spec string) (string, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" || spec == "status" {
		return m.contextWindowStatusReport(), nil
	}
	if m.modelManager == nil {
		return "", fmt.Errorf("model manager unavailable")
	}
	if m.modelManager.RemoteProvider() {
		return "", fmt.Errorf("remote providers own their context window; /context only tunes local Ollama num_ctx")
	}

	switch {
	case spec == "auto":
		rec := m.recommendNumCtx()
		if rec.Recommended <= 0 {
			return "", fmt.Errorf("could not recommend a num_ctx: %s", rec.Reason)
		}
		if rec.Recommended == m.modelManager.ConfiguredNumCtx() {
			return m.formatContextApplied(rec, rec.Recommended, false, "already at recommended value"), nil
		}
		if err := m.applyNumCtx(rec.Recommended); err != nil {
			return "", err
		}
		return m.formatContextApplied(rec, rec.Recommended, true, "applied recommendation for this process"), nil

	case strings.HasPrefix(spec, "set:"):
		raw := strings.TrimPrefix(spec, "set:")
		value, err := config.ParseNumCtxArg(raw)
		if err != nil {
			return "", err
		}
		rec := m.recommendNumCtx()
		if rec.MaxSafe > 0 && value > rec.MaxSafe {
			return "", fmt.Errorf(
				"num_ctx %d exceeds estimated max safe %d on this host (%s total RAM); pick a lower value or free memory",
				value, rec.MaxSafe, config.FormatBytesIEC(rec.TotalRAM),
			)
		}
		if !rec.AllowLarge && value > 32_768 {
			return "", fmt.Errorf(
				"num_ctx %d needs LOCAL_AGENT_ALLOW_LARGE_MODELS=1 (host clamp is 32768 without it)",
				value,
			)
		}
		if err := m.applyNumCtx(value); err != nil {
			return "", err
		}
		return m.formatContextApplied(rec, value, true, "applied explicit value for this process"), nil
	default:
		return "", fmt.Errorf("unknown /context action %q", spec)
	}
}

func (m *Model) applyNumCtx(value int) error {
	if m.modelManager == nil {
		return fmt.Errorf("model manager unavailable")
	}
	if err := m.modelManager.SetNumCtx(value); err != nil {
		return err
	}
	m.syncEffectiveContext(false)
	return nil
}

func (m *Model) saveConfiguredNumCtx() (string, error) {
	if m.modelManager == nil {
		return "", fmt.Errorf("model manager unavailable")
	}
	if m.configSourcePath == "" {
		return "", fmt.Errorf("no host config path is available to save; set ollama.num_ctx in your config.yaml manually")
	}
	value := m.modelManager.ConfiguredNumCtx()
	if value <= 0 {
		return "", fmt.Errorf("no local num_ctx is configured")
	}
	if err := config.UpdateOllamaNumCtxFile(m.configSourcePath, value); err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"Saved ollama.num_ctx: %d\n  file: %s\n  Restart is not required for the current process; new launches load this value.",
		value, m.configSourcePath,
	), nil
}

func (m *Model) recommendNumCtx() config.NumCtxRecommendation {
	var totalRAM int64
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if snap, err := (resource.SystemProbe{}).Snapshot(ctx); err == nil {
		totalRAM = snap.TotalRAMBytes
	}

	current := 0
	modelName := m.model
	var weight int64
	native := 0
	if m.modelManager != nil {
		current = m.modelManager.ConfiguredNumCtx()
		if modelName == "" {
			modelName = m.modelManager.CurrentModel()
		}
		weight = m.modelManager.LocalModelWeightBytes(modelName)
		policy := m.modelManager.ContextPolicy(modelName)
		if policy.NativeKnown {
			native = policy.Native
		}
	}
	return config.RecommendNumCtx(totalRAM, weight, native, current)
}

func (m *Model) contextWindowStatusReport() string {
	rec := m.recommendNumCtx()
	var b strings.Builder
	b.WriteString("Context window (ollama.num_ctx)\n")
	fmt.Fprintf(&b, "  Model:            %s\n", emptyDash(m.model))
	fmt.Fprintf(&b, "  Current:          %d tokens\n", rec.Current)
	if rec.NativeMax > 0 {
		fmt.Fprintf(&b, "  Model native max: %d tokens\n", rec.NativeMax)
	} else {
		b.WriteString("  Model native max: unknown\n")
	}
	if rec.TotalRAM > 0 {
		fmt.Fprintf(&b, "  Host RAM:         %s\n", config.FormatBytesIEC(rec.TotalRAM))
	} else {
		b.WriteString("  Host RAM:         unknown\n")
	}
	if rec.ModelWeight > 0 {
		fmt.Fprintf(&b, "  Model weights:    %s (estimate)\n", config.FormatBytesIEC(rec.ModelWeight))
	}
	fmt.Fprintf(&b, "  Recommended:      %d tokens\n", rec.Recommended)
	fmt.Fprintf(&b, "  Max safe (est.):  %d tokens\n", rec.MaxSafe)
	fmt.Fprintf(&b, "  Large models env: %v\n", rec.AllowLarge)
	fmt.Fprintf(&b, "  Note: %s\n", rec.Reason)
	b.WriteString("\nCommands:\n")
	b.WriteString("  /context auto       apply the recommendation now\n")
	b.WriteString("  /context set 96k    set an explicit window (e.g. 65536, 96k)\n")
	b.WriteString("  /context save       write the active value into config.yaml\n")
	if rec.Current > 0 && rec.Recommended > 0 && rec.Current != rec.Recommended {
		fmt.Fprintf(&b, "\nSuggestion: /context auto  (move %d → %d)\n", rec.Current, rec.Recommended)
	}
	return b.String()
}

func (m *Model) formatContextApplied(rec config.NumCtxRecommendation, applied int, changed bool, detail string) string {
	var b strings.Builder
	if changed {
		fmt.Fprintf(&b, "Context window updated to %d tokens\n", applied)
	} else {
		fmt.Fprintf(&b, "Context window remains %d tokens\n", applied)
	}
	fmt.Fprintf(&b, "  %s\n", detail)
	fmt.Fprintf(&b, "  Effective now: %d\n", m.numCtx)
	if rec.MaxSafe > 0 {
		fmt.Fprintf(&b, "  Max safe est.: %d\n", rec.MaxSafe)
	}
	b.WriteString("  Persist with: /context save\n")
	if rec.Reason != "" {
		fmt.Fprintf(&b, "  Plan note: %s\n", rec.Reason)
	}
	return b.String()
}

func emptyDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "—"
	}
	return value
}
