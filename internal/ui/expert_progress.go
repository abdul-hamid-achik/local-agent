package ui

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/abdul-hamid-achik/local-agent/internal/expertselector"
	"github.com/abdul-hamid-achik/local-agent/internal/expertteam"
	"github.com/abdul-hamid-achik/local-agent/internal/llm"
)

const (
	maxExpertProgressItems      = 16
	maxExpertProgressNameBytes  = 128
	maxExpertProgressModelBytes = 256
	maxExpertProgressTokens     = 10_000_000
)

// ExpertProgressItem is the bounded, host-owned UI projection for one expert.
// It deliberately has no prompt, objective, report, provider error, reasoning,
// path, or arbitrary metadata field.
type ExpertProgressItem struct {
	Index       int                      `json:"index"`
	Expert      string                   `json:"expert"`
	Model       string                   `json:"model"`
	Location    llm.OllamaModelLocation  `json:"location"`
	Phase       expertteam.ProgressPhase `json:"phase"`
	Status      expertteam.ExpertStatus  `json:"status,omitempty"`
	FailureCode string                   `json:"failure_code,omitempty"`
	EvalTokens  int                      `json:"eval_tokens,omitempty"`
}

// ExpertProgressState is the complete bounded consultation projection. The
// slice is fixed to Total (at most sixteen) and replaces an unbounded map.
type ExpertProgressState struct {
	Sequence    uint64                  `json:"sequence"`
	Strategy    expertselector.Strategy `json:"strategy"`
	Total       int                     `json:"total"`
	Parallelism int                     `json:"parallelism"`
	Running     int                     `json:"running"`
	Queued      int                     `json:"queued"`
	Completed   int                     `json:"completed"`
	Failed      int                     `json:"failed"`
	Experts     []ExpertProgressItem    `json:"experts"`
}

func isExpertConsultTool(name string) bool {
	// The progress contract belongs only to Local Agent's exact built-in. A
	// similarly named routed tool must not inherit its presentation authority.
	return name == "consult_experts"
}

func normalizeExpertProgressEvent(event expertteam.ProgressEvent) (expertteam.ProgressEvent, bool) {
	if event.Sequence == 0 || event.Total < 1 || event.Total > maxExpertProgressItems ||
		event.Sequence > uint64(1+2*event.Total) ||
		event.Parallelism < 1 || event.Parallelism > event.Total ||
		!validExpertProgressCount(event.Running, event.Total) ||
		!validExpertProgressCount(event.Queued, event.Total) ||
		!validExpertProgressCount(event.Completed, event.Total) ||
		!validExpertProgressCount(event.Failed, event.Total) ||
		event.Running+event.Queued+event.Completed+event.Failed != event.Total ||
		event.EvalTokens < 0 || event.EvalTokens > maxExpertProgressTokens {
		return expertteam.ProgressEvent{}, false
	}
	if event.Strategy != expertselector.StrategyTeam && event.Strategy != expertselector.StrategySwarm && event.Strategy != expertselector.StrategyMoE {
		return expertteam.ProgressEvent{}, false
	}

	switch event.Phase {
	case expertteam.ProgressPlanned:
		if event.ExpertIndex != -1 || event.Expert != "" || event.Model != "" || event.Status != "" ||
			event.ErrorCode != "" || event.EvalTokens != 0 || event.Running != 0 ||
			event.Queued != event.Total || event.Completed != 0 || event.Failed != 0 {
			return expertteam.ProgressEvent{}, false
		}
		return event, true
	case expertteam.ProgressStarted, expertteam.ProgressCompleted, expertteam.ProgressFailed:
		if event.ExpertIndex < 0 || event.ExpertIndex >= event.Total ||
			!boundedExpertProgressIdentifier(event.Expert, maxExpertProgressNameBytes) ||
			!boundedExpertProgressIdentifier(event.Model, maxExpertProgressModelBytes) ||
			!validExpertProgressLocation(event.Location) {
			return expertteam.ProgressEvent{}, false
		}
		event.Expert = sanitizeTerminalSingleLine(event.Expert)
		event.Model = sanitizeTerminalSingleLine(event.Model)
		if !boundedExpertProgressIdentifier(event.Expert, maxExpertProgressNameBytes) ||
			!boundedExpertProgressIdentifier(event.Model, maxExpertProgressModelBytes) {
			return expertteam.ProgressEvent{}, false
		}
	default:
		return expertteam.ProgressEvent{}, false
	}

	switch event.Phase {
	case expertteam.ProgressStarted:
		if event.Status != "" || event.ErrorCode != "" || event.EvalTokens != 0 {
			return expertteam.ProgressEvent{}, false
		}
	case expertteam.ProgressCompleted:
		if event.Status != expertteam.ExpertCompleted || event.ErrorCode != "" {
			return expertteam.ProgressEvent{}, false
		}
	case expertteam.ProgressFailed:
		if event.Status != expertteam.ExpertFailed || !validExpertFailureCode(event.ErrorCode) {
			return expertteam.ProgressEvent{}, false
		}
	}
	return event, true
}

func validExpertProgressCount(value, total int) bool { return value >= 0 && value <= total }

func boundedExpertProgressIdentifier(value string, limit int) bool {
	if !utf8.ValidString(value) || value == "" || len(value) > limit || strings.TrimSpace(value) != value {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func validExpertProgressLocation(location llm.OllamaModelLocation) bool {
	switch location {
	case llm.OllamaModelLocationUnknown, llm.OllamaModelLocationLocal,
		llm.OllamaModelLocationCloud, llm.OllamaModelLocationRemote:
		return true
	default:
		return false
	}
}

func validExpertFailureCode(code string) bool {
	switch code {
	case "cancelled", "timed_out", "model_unavailable", "budget_exceeded", "inference_failed",
		"missing_usage_receipt", "no_visible_report":
		return true
	default:
		return false
	}
}

func cloneExpertProgressState(state *ExpertProgressState) *ExpertProgressState {
	if state == nil {
		return nil
	}
	copy := *state
	copy.Experts = append([]ExpertProgressItem(nil), state.Experts...)
	return &copy
}

func (state *ExpertProgressState) apply(event expertteam.ProgressEvent) bool {
	if state == nil || event.Sequence != state.Sequence+1 || event.Total != state.Total ||
		event.Strategy != state.Strategy || event.Parallelism != state.Parallelism ||
		event.Completed < state.Completed || event.Failed < state.Failed {
		return false
	}
	item := state.Experts[event.ExpertIndex]
	if item.Expert != "" && (item.Expert != event.Expert || item.Model != event.Model || item.Location != event.Location) {
		return false
	}

	switch event.Phase {
	case expertteam.ProgressStarted:
		if item.Phase != "" || event.Running != state.Running+1 || event.Queued != state.Queued-1 ||
			event.Completed != state.Completed || event.Failed != state.Failed {
			return false
		}
	case expertteam.ProgressCompleted:
		if item.Phase != expertteam.ProgressStarted || event.Running != state.Running-1 || event.Queued != state.Queued ||
			event.Completed != state.Completed+1 || event.Failed != state.Failed {
			return false
		}
	case expertteam.ProgressFailed:
		switch item.Phase {
		case expertteam.ProgressStarted:
			if event.Running != state.Running-1 || event.Queued != state.Queued {
				return false
			}
		case "":
			if event.Running != state.Running || event.Queued != state.Queued-1 {
				return false
			}
		default:
			return false
		}
		if event.Completed != state.Completed || event.Failed != state.Failed+1 {
			return false
		}
	default:
		return false
	}

	state.Sequence = event.Sequence
	state.Running = event.Running
	state.Queued = event.Queued
	state.Completed = event.Completed
	state.Failed = event.Failed
	state.Experts[event.ExpertIndex] = ExpertProgressItem{
		Index: event.ExpertIndex, Expert: event.Expert, Model: event.Model, Location: event.Location,
		Phase: event.Phase, Status: event.Status, FailureCode: event.ErrorCode, EvalTokens: event.EvalTokens,
	}
	return true
}

func newExpertProgressState(event expertteam.ProgressEvent) *ExpertProgressState {
	if event.Phase != expertteam.ProgressPlanned || event.Sequence != 1 {
		return nil
	}
	return &ExpertProgressState{
		Sequence: event.Sequence, Strategy: event.Strategy, Total: event.Total, Parallelism: event.Parallelism,
		Running: event.Running, Queued: event.Queued, Completed: event.Completed, Failed: event.Failed,
		Experts: make([]ExpertProgressItem, event.Total),
	}
}

func sanitizeExpertProgressState(state *ExpertProgressState, requireSettled bool) *ExpertProgressState {
	state = cloneExpertProgressState(state)
	if state == nil || state.Sequence == 0 || state.Total < 1 || state.Total > maxExpertProgressItems ||
		state.Sequence > uint64(1+2*state.Total) ||
		state.Parallelism < 1 || state.Parallelism > state.Total || len(state.Experts) != state.Total ||
		!validExpertProgressCount(state.Running, state.Total) || !validExpertProgressCount(state.Queued, state.Total) ||
		!validExpertProgressCount(state.Completed, state.Total) || !validExpertProgressCount(state.Failed, state.Total) ||
		state.Running+state.Queued+state.Completed+state.Failed != state.Total ||
		(state.Strategy != expertselector.StrategyTeam && state.Strategy != expertselector.StrategySwarm && state.Strategy != expertselector.StrategyMoE) ||
		(requireSettled && (state.Running != 0 || state.Queued != 0)) {
		return nil
	}
	counts := struct{ running, queued, completed, failed int }{}
	for index := range state.Experts {
		item := &state.Experts[index]
		if item.Expert == "" && item.Model == "" && item.Phase == "" {
			if item.Index != 0 || item.Location != llm.OllamaModelLocationUnknown || item.Status != "" ||
				item.FailureCode != "" || item.EvalTokens != 0 {
				return nil
			}
			counts.queued++
			continue
		}
		if item.Index != index || !boundedExpertProgressIdentifier(item.Expert, maxExpertProgressNameBytes) ||
			!boundedExpertProgressIdentifier(item.Model, maxExpertProgressModelBytes) || !validExpertProgressLocation(item.Location) ||
			item.EvalTokens < 0 || item.EvalTokens > maxExpertProgressTokens {
			return nil
		}
		item.Expert = sanitizeTerminalSingleLine(item.Expert)
		item.Model = sanitizeTerminalSingleLine(item.Model)
		if !boundedExpertProgressIdentifier(item.Expert, maxExpertProgressNameBytes) ||
			!boundedExpertProgressIdentifier(item.Model, maxExpertProgressModelBytes) {
			return nil
		}
		switch item.Phase {
		case expertteam.ProgressStarted:
			if item.Status != "" || item.FailureCode != "" || item.EvalTokens != 0 {
				return nil
			}
			counts.running++
		case expertteam.ProgressCompleted:
			if item.Status != expertteam.ExpertCompleted || item.FailureCode != "" {
				return nil
			}
			counts.completed++
		case expertteam.ProgressFailed:
			if item.Status != expertteam.ExpertFailed || !validExpertFailureCode(item.FailureCode) {
				return nil
			}
			counts.failed++
		default:
			return nil
		}
	}
	if counts.running != state.Running || counts.queued != state.Queued || counts.completed != state.Completed || counts.failed != state.Failed {
		return nil
	}
	return state
}

func (state *ExpertProgressState) summary() string {
	if state == nil || state.Total == 0 {
		return ""
	}
	if state.Completed+state.Failed == state.Total {
		parts := []string{fmt.Sprintf("%d completed", state.Completed)}
		if state.Failed > 0 {
			parts = append(parts, fmt.Sprintf("%d failed", state.Failed))
		}
		return strings.Join(parts, " · ")
	}
	parts := []string{fmt.Sprintf("%d/%d finished", state.Completed+state.Failed, state.Total)}
	if state.Running > 0 {
		parts = append(parts, fmt.Sprintf("%d active", state.Running))
	}
	if state.Queued > 0 {
		parts = append(parts, fmt.Sprintf("%d queued", state.Queued))
	}
	return strings.Join(parts, " · ")
}

func (state *ExpertProgressState) cardState(fallback ToolCardState) ToolCardState {
	if state == nil || state.Running > 0 || state.Queued > 0 {
		return fallback
	}
	if state.Completed == 0 && state.Failed > 0 {
		return ToolCardError
	}
	if state.Failed > 0 {
		return ToolCardAttention
	}
	return fallback
}

func (state *ExpertProgressState) renderDetails(width int, styles ToolCardStyles) string {
	if state == nil {
		return ""
	}
	width = max(1, width)
	lines := make([]string, 0, state.Total*2+1)
	known := 0
	for _, item := range state.Experts {
		if item.Expert == "" {
			continue
		}
		known++
		glyph, status, style := "…", "running", styles.TitleRunning
		switch item.Phase {
		case expertteam.ProgressCompleted:
			glyph, status, style = "✓", "completed", styles.TitleSuccess
		case expertteam.ProgressFailed:
			glyph, status, style = "✗", expertFailureLabel(item.FailureCode), styles.TitleError
		}
		tokens := ""
		if item.EvalTokens > 0 {
			tokens = fmt.Sprintf(" · %d tok", item.EvalTokens)
		}
		roleLine := glyph + " " + item.Expert + " · " + status + tokens
		modelLine := item.Model + " · " + string(item.Location)
		if width >= 54 {
			line := roleLine + " · " + modelLine
			lines = append(lines, style.Render(truncateDisplay(line, width)))
			continue
		}
		lines = append(lines, style.Render(truncateDisplay(roleLine, width)))
		if width >= 12 {
			lines = append(lines, styles.Dimmed.Render(truncateDisplay("  "+modelLine, width)))
		}
	}
	if known == 0 {
		label := fmt.Sprintf("%d experts queued · %d at a time", state.Queued, state.Parallelism)
		lines = append(lines, styles.Dimmed.Render(truncateDisplay(label, width)))
	} else if state.Queued > 0 {
		lines = append(lines, styles.Dimmed.Render(truncateDisplay(fmt.Sprintf("%d more queued", state.Queued), width)))
	}
	return strings.Join(lines, "\n")
}

func expertFailureLabel(code string) string {
	switch code {
	case "no_visible_report":
		return "no visible report"
	case "model_unavailable":
		return "model unavailable"
	case "budget_exceeded":
		return "budget exceeded"
	case "missing_usage_receipt":
		return "usage missing"
	case "timed_out":
		return "timed out"
	case "cancelled":
		return "cancelled"
	default:
		return "failed"
	}
}

func (card *ToolCard) setExpertProgress(state *ExpertProgressState, width int) {
	if card == nil {
		return
	}
	card.ExpertProgress = state
	card.expertProgressCache = ""
	card.expertProgressCacheWidth = 0
	card.expertProgressCacheSequence = 0
	if state == nil {
		return
	}
	width = max(1, width)
	card.expertProgressCache = state.renderDetails(width, card.Styles)
	card.expertProgressCacheWidth = width
	card.expertProgressCacheSequence = state.Sequence
}

func (card ToolCard) expertProgressDetails(width int) string {
	if card.ExpertProgress == nil {
		return ""
	}
	width = max(1, width)
	if card.expertProgressCacheWidth == width && card.expertProgressCacheSequence == card.ExpertProgress.Sequence {
		return card.expertProgressCache
	}
	return card.ExpertProgress.renderDetails(width, card.Styles)
}

func (m *Model) handleExpertProgress(msg ExpertProgressMsg) {
	event, ok := normalizeExpertProgressEvent(msg.Event)
	if !ok || msg.CallID == "" {
		return
	}
	for index := len(m.toolEntries) - 1; index >= 0; index-- {
		entry := &m.toolEntries[index]
		if entry.ID != msg.CallID || !isExpertConsultTool(entry.Name) || entry.Status != ToolStatusRunning {
			continue
		}
		var next *ExpertProgressState
		if entry.ExpertProgress == nil {
			next = newExpertProgressState(event)
			if next == nil {
				return
			}
		} else {
			next = cloneExpertProgressState(entry.ExpertProgress)
			if !next.apply(event) {
				return
			}
		}
		entry.ExpertProgress = next
		entry.Summary = boundedToolCardSummary(next.summary())
		// The live expert surface starts open so keyboard-only users do not need
		// a running-state-only shortcut to discover role/model status.
		entry.Collapsed = false
		for cardIndex := len(m.toolCardMgr.Cards) - 1; cardIndex >= 0; cardIndex-- {
			card := &m.toolCardMgr.Cards[cardIndex]
			if card.ID == msg.CallID && isExpertConsultTool(card.Name) && card.State == ToolCardRunning {
				card.SetSummary(entry.Summary)
				card.setExpertProgress(next, max(1, m.chatPaneWidth()-6))
				break
			}
		}
		m.invalidateEntryCache()
		m.viewport.SetContent(m.renderEntries())
		m.gotoBottomIfFollowing()
		return
	}
}

func (m *Model) runningExpertActivity() (workingActivity, bool) {
	for index := len(m.toolEntries) - 1; index >= 0; index-- {
		entry := m.toolEntries[index]
		if entry.Status != ToolStatusRunning || !isExpertConsultTool(entry.Name) {
			continue
		}
		activity := workingActivity{label: "Consulting experts", compactLabel: "Experts", cancellable: true, static: true}
		if entry.ExpertProgress != nil {
			activity.detail = entry.ExpertProgress.summary()
		}
		return activity, true
	}
	return workingActivity{}, false
}
