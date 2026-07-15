package ui

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"

	"github.com/abdul-hamid-achik/local-agent/internal/agent"
)

const (
	maxPromptPathIntents     = 4
	maxPromptPathScanIntents = 32
)

func (m *Model) beginPromptPathPreflight(draft string) (tea.Cmd, bool) {
	if m == nil || m.agent == nil {
		return nil, false
	}
	scan := scanExplicitPromptPaths(draft)
	if len(scan.Intents) == 0 {
		return nil, false
	}
	m.readScopeOpToken++
	token := m.readScopeOpToken
	m.readScopeOpRunning = true
	m.readScopeOpLabel = "Checking explicit read paths"
	m.readScopeOpDraft = draft
	m.input.Blur()
	m.recalcViewportHeight()

	agentInstance := m.agent
	inspect := func() tea.Msg {
		if scan.MoreCandidates {
			return PromptPathPreflightResultMsg{
				Token: token, Draft: draft, MoreCandidates: true, CandidateLimitExceeded: true,
			}
		}
		grants, tooManyGrants := inspectPromptReadGrantIntents(agentInstance, scan.Intents)
		return PromptPathPreflightResultMsg{
			Token: token, Draft: draft, Grants: grants, MoreCandidates: tooManyGrants,
		}
	}
	return tea.Batch(m.startActivityCmd(), inspect), true
}

func (m *Model) handlePromptPathPreflightResult(msg PromptPathPreflightResultMsg) tea.Cmd {
	if !m.readScopeOpRunning || msg.Token != m.readScopeOpToken {
		releaseReadGrants(msg.Grants)
		return nil
	}
	m.readScopeOpRunning = false
	m.readScopeOpLabel = ""
	m.readScopeOpDraft = ""
	if m.shuttingDown {
		releaseReadGrants(msg.Grants)
		return nil
	}
	if msg.MoreCandidates {
		releaseReadGrants(msg.Grants)
		m.input.SetValue(msg.Draft)
		m.input.CursorEnd()
		m.input.Focus()
		_ = m.reflowInputViewport()
		guidance := "External read preflight requires more than 4 new external read grants. Split the request into smaller groups; no path was authorized and nothing was sent."
		if msg.CandidateLimitExceeded {
			guidance = "External read preflight found more than 32 distinct path candidates. Split the request into smaller groups; no path was authorized and nothing was sent."
		}
		m.entries = append(m.entries, ChatEntry{
			Kind:    "error",
			Content: guidance,
		})
		m.recalcViewportHeight()
		m.viewport.SetContent(m.renderEntries())
		m.gotoBottomIfFollowing()
		return nil
	}
	if len(msg.Grants) == 0 {
		m.input.Focus()
		return m.submitPreparedInput(msg.Draft)
	}
	grants := append([]agent.ReadGrant(nil), msg.Grants...)
	m.readScopePrompt = &ReadScopePrompt{
		Canonical:  grants[0].Path,
		Workspace:  m.agent.WorkDir(),
		Draft:      msg.Draft,
		Kind:       grants[0].Kind,
		Grants:     grants,
		AutoResume: true,
	}
	m.input.Blur()
	m.recalcViewportHeight()
	return nil
}

func inspectPromptReadGrants(agentInstance *agent.Agent, candidates []string) []agent.ReadGrant {
	intents := make([]promptPathIntent, 0, len(candidates))
	for _, candidate := range candidates {
		intents = append(intents, promptPathIntent{Literal: candidate})
	}
	grants, _ := inspectPromptReadGrantIntents(agentInstance, intents)
	return grants
}

func inspectPromptReadGrantIntents(agentInstance *agent.Agent, intents []promptPathIntent) ([]agent.ReadGrant, bool) {
	if agentInstance == nil {
		return nil, false
	}
	grants := make([]agent.ReadGrant, 0, min(len(intents), maxPromptPathIntents))
	seen := make(map[string]struct{}, len(intents))
	for _, intent := range intents {
		inspection, err := agentInstance.InspectReadPath(intent.Literal)
		if err != nil && intent.Fallback != "" && errors.Is(err, fs.ErrNotExist) {
			inspection, err = agentInstance.InspectReadPath(intent.Fallback)
		}
		if err != nil || !inspection.External || inspection.AlreadyReadable {
			inspection.Release()
			continue
		}
		if inspection.Kind != agent.ReadGrantExactFile && inspection.Kind != agent.ReadGrantDirectory {
			inspection.Release()
			continue
		}
		key := string(inspection.Kind) + "\x00" + inspection.Path
		if _, duplicate := seen[key]; duplicate {
			inspection.Release()
			continue
		}
		seen[key] = struct{}{}
		candidateGrant := inspection.Grant()
		grants = mergePromptReadGrant(grants, candidateGrant)
	}
	if len(grants) > maxPromptPathIntents {
		releaseReadGrants(grants)
		return nil, true
	}
	return grants, false
}

// mergePromptReadGrant keeps the smallest explicit authority set. A directory
// covers every exact file or narrower directory below it; an exact file can
// never broaden authority to a parent or sibling.
func mergePromptReadGrant(grants []agent.ReadGrant, candidate agent.ReadGrant) []agent.ReadGrant {
	for _, existing := range grants {
		if existing.Kind == agent.ReadGrantDirectory && readScopePathContains(existing.Path, candidate.Path) {
			candidate.Release()
			return grants
		}
	}
	if candidate.Kind != agent.ReadGrantDirectory {
		return append(grants, candidate)
	}
	kept := grants[:0]
	for _, existing := range grants {
		if readScopePathContains(candidate.Path, existing.Path) {
			existing.Release()
			continue
		}
		kept = append(kept, existing)
	}
	return append(kept, candidate)
}

func explicitPromptPathCandidates(text string) []string {
	scan := scanExplicitPromptPaths(text)
	result := make([]string, 0, len(scan.Intents))
	for _, intent := range scan.Intents {
		result = append(result, intent.Literal)
	}
	return result
}

type promptPathScan struct {
	Intents        []promptPathIntent
	MoreCandidates bool
}

type promptPathIntent struct {
	Literal  string
	Fallback string
}

func scanExplicitPromptPaths(text string) promptPathScan {
	if strings.TrimSpace(text) == "" {
		return promptPathScan{}
	}
	remaining := []rune(text)
	result := make([]promptPathIntent, 0, maxPromptPathScanIntents)
	seen := make(map[string]int, maxPromptPathScanIntents)
	moreCandidates := false
	appendCandidate := func(candidate string, exact, trimWrapper bool) {
		intent := normalizePromptPathIntent(candidate, exact, trimWrapper)
		if !looksLikeExplicitHostPath(intent.Literal) {
			return
		}
		if index, duplicate := seen[intent.Literal]; duplicate {
			// Any exact occurrence wins over a free-token punctuation fallback.
			if intent.Fallback == "" {
				result[index].Fallback = ""
			}
			return
		}
		if len(result) >= maxPromptPathScanIntents {
			moreCandidates = true
			return
		}
		seen[intent.Literal] = len(result)
		result = append(result, intent)
	}

	for index := 0; index < len(remaining); index++ {
		quote := remaining[index]
		if quote != '\'' && quote != '"' && quote != '`' {
			continue
		}
		end := index + 1
		for ; end < len(remaining); end++ {
			if remaining[end] == quote && (end == index+1 || remaining[end-1] != '\\') {
				break
			}
		}
		if end >= len(remaining) {
			continue
		}
		appendCandidate(string(remaining[index+1:end]), true, false)
		for cursor := index; cursor <= end; cursor++ {
			remaining[cursor] = ' '
		}
		index = end
	}

	for _, token := range splitPromptPathTokens(remaining) {
		appendCandidate(token.Value, token.EscapedWhitespace, true)
	}
	return promptPathScan{Intents: result, MoreCandidates: moreCandidates}
}

// splitPromptPathTokens recognizes the backslash-escaped whitespace emitted by
// macOS drag-and-drop without invoking a shell or interpreting substitutions.
type promptPathToken struct {
	Value             string
	EscapedWhitespace bool
}

func splitPromptPathTokens(input []rune) []promptPathToken {
	result := make([]promptPathToken, 0, maxPromptPathScanIntents)
	var current strings.Builder
	escapedWhitespace := false
	flush := func() {
		if current.Len() == 0 {
			return
		}
		result = append(result, promptPathToken{Value: current.String(), EscapedWhitespace: escapedWhitespace})
		current.Reset()
		escapedWhitespace = false
	}
	for index := 0; index < len(input); index++ {
		character := input[index]
		if character == '\\' && index+1 < len(input) && unicode.IsSpace(input[index+1]) {
			current.WriteRune(input[index+1])
			escapedWhitespace = true
			index++
			continue
		}
		if unicode.IsSpace(character) {
			flush()
			continue
		}
		current.WriteRune(character)
	}
	flush()
	return result
}

func normalizePromptPathIntent(value string, exact, trimWrapper bool) promptPathIntent {
	if trimWrapper {
		value = strings.TrimLeft(value, "@([{<")
	}
	intent := promptPathIntent{Literal: value}
	if exact {
		return intent
	}
	fallback := strings.TrimRight(value, ".,;:!?)]}>")
	if fallback != value && looksLikeExplicitHostPath(fallback) {
		intent.Fallback = fallback
	}
	return intent
}

func looksLikeExplicitHostPath(value string) bool {
	if value == "" || value == "~" || strings.HasPrefix(value, "//") {
		return false
	}
	if strings.HasPrefix(value, "~/") || strings.HasPrefix(value, `~\`) {
		return true
	}
	return filepath.IsAbs(value)
}

func readScopePathContains(parent, candidate string) bool {
	relative, err := filepath.Rel(parent, candidate)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}
