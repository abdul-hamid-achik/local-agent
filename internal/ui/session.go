package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/abdul-hamid-achik/local-agent/internal/config"
	"github.com/abdul-hamid-achik/local-agent/internal/db"
	"github.com/abdul-hamid-achik/local-agent/internal/llm"
)

// SessionListItem represents a session in the list.
type SessionListItem struct {
	ID        int64  `json:"id"`
	Title     string `json:"title"`
	CreatedAt string `json:"created_at"`
}

type persistedChatEntry struct {
	Kind              string `json:"kind"`
	Content           string `json:"content,omitempty"`
	Name              string `json:"name,omitempty"`
	IsError           bool   `json:"is_error,omitempty"`
	ToolIndex         int    `json:"tool_index,omitempty"`
	ThinkingContent   string `json:"thinking_content,omitempty"`
	ThinkingCollapsed bool   `json:"thinking_collapsed,omitempty"`
}

// persistedToolEntry deliberately excludes RawArgs and BeforeContent. Those
// ephemeral fields may contain secrets or multi-megabyte file snapshots and
// are not needed to render a completed tool card after resume.
type persistedToolEntry struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	Summary   string        `json:"summary,omitempty"`
	Args      string        `json:"args,omitempty"`
	Result    string        `json:"result,omitempty"`
	IsError   bool          `json:"is_error,omitempty"`
	Status    ToolStatus    `json:"status"`
	StartTime time.Time     `json:"start_time"`
	Duration  time.Duration `json:"duration,omitempty"`
	Collapsed bool          `json:"collapsed,omitempty"`
	DiffLines []DiffLine    `json:"diff_lines,omitempty"`
}

const (
	maxPersistedToolArgsBytes   = 4 * 1024
	maxPersistedToolResultBytes = 16 * 1024
	maxPersistedDiffBytes       = 64 * 1024
	maxPersistedDiffLines       = 2_000
)

type persistedSessionState struct {
	Version             int                  `json:"version"`
	Messages            []llm.Message        `json:"messages"`
	Entries             []persistedChatEntry `json:"entries"`
	ToolEntries         []persistedToolEntry `json:"tool_entries,omitempty"`
	Mode                Mode                 `json:"mode"`
	Model               string               `json:"model,omitempty"`
	ModelPinned         bool                 `json:"model_pinned,omitempty"`
	AgentProfile        string               `json:"agent_profile,omitempty"`
	LoadedFile          string               `json:"loaded_file,omitempty"`
	ManualLoadedContext string               `json:"manual_loaded_context,omitempty"`
	ManualSkills        []string             `json:"manual_skills,omitempty"`
	SessionEvalTotal    int                  `json:"session_eval_total,omitempty"`
	SessionPromptTotal  int                  `json:"session_prompt_total,omitempty"`
	SessionTurnCount    int                  `json:"session_turn_count,omitempty"`
	ExecutionCursor     int64                `json:"execution_cursor,omitempty"`
	FileChanges         map[string]int       `json:"file_changes,omitempty"`
}

func sessionTitle(prompt string) string {
	title := strings.TrimSpace(strings.SplitN(prompt, "\n", 2)[0])
	if title == "" {
		title = "Local agent session " + time.Now().Format("2006-01-02 15:04")
	}
	if len([]rune(title)) > 72 {
		runes := []rune(title)
		title = string(runes[:69]) + "..."
	}
	return title
}

func boundedSessionText(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	const marker = "\n...[truncated in saved session]"
	cut := limit - len(marker)
	if cut < 0 {
		cut = 0
	}
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut] + marker
}

func persistDiffLines(lines []DiffLine) []DiffLine {
	if len(lines) == 0 {
		return nil
	}
	remaining := maxPersistedDiffBytes
	capacity := len(lines)
	if capacity > maxPersistedDiffLines {
		capacity = maxPersistedDiffLines
	}
	result := make([]DiffLine, 0, capacity)
	const perLineOverhead = 32
	for i, line := range lines {
		if len(result) >= maxPersistedDiffLines-1 && i < len(lines)-1 {
			result = append(result, DiffLine{Kind: DiffContext, Content: "...[remaining diff omitted from saved session]"})
			break
		}
		if remaining <= perLineOverhead {
			result = append(result, DiffLine{Kind: DiffContext, Content: "...[remaining diff omitted from saved session]"})
			break
		}
		remaining -= perLineOverhead
		content := boundedSessionText(line.Content, remaining)
		result = append(result, DiffLine{Kind: line.Kind, Content: content})
		remaining -= len(content)
	}
	return result
}

func persistToolEntries(entries []ToolEntry) []persistedToolEntry {
	result := make([]persistedToolEntry, len(entries))
	for i, entry := range entries {
		result[i] = persistedToolEntry{
			ID:        entry.ID,
			Name:      entry.Name,
			Summary:   boundedToolCardSummary(entry.Summary),
			Args:      boundedSessionText(entry.Args, maxPersistedToolArgsBytes),
			Result:    boundedSessionText(entry.Result, maxPersistedToolResultBytes),
			IsError:   entry.IsError,
			Status:    entry.Status,
			StartTime: entry.StartTime,
			Duration:  entry.Duration,
			Collapsed: entry.Collapsed,
			DiffLines: persistDiffLines(entry.DiffLines),
		}
	}
	return result
}

func restoreToolEntries(entries []persistedToolEntry) []ToolEntry {
	result := make([]ToolEntry, len(entries))
	for i, entry := range entries {
		result[i] = ToolEntry{
			ID:        entry.ID,
			Name:      entry.Name,
			Summary:   restoredToolSummary(entry),
			Args:      entry.Args,
			Result:    entry.Result,
			IsError:   entry.IsError,
			Status:    entry.Status,
			StartTime: entry.StartTime,
			Duration:  entry.Duration,
			Collapsed: entry.Collapsed,
			DiffLines: append([]DiffLine(nil), entry.DiffLines...),
		}
	}
	return result
}

// restoredToolSummary preserves the semantic summary written by current
// snapshots. Version-1 snapshots created before summary persistence omitted
// the field, so their already-bounded display arguments become compact context
// instead of leaving a restored receipt with only a generic action label.
func restoredToolSummary(entry persistedToolEntry) string {
	if summary := boundedToolCardSummary(entry.Summary); summary != "" {
		return summary
	}
	return boundedToolCardSummary(entry.Args)
}

func encodeSessionState(m *Model) (string, error) {
	entries := make([]persistedChatEntry, len(m.entries))
	for i, entry := range m.entries {
		entries[i] = persistedChatEntry{
			Kind:              entry.Kind,
			Content:           entry.Content,
			Name:              entry.Name,
			IsError:           entry.IsError,
			ToolIndex:         entry.ToolIndex,
			ThinkingContent:   entry.ThinkingContent,
			ThinkingCollapsed: entry.ThinkingCollapsed,
		}
	}
	manualSkills := m.manualSkills
	if manualSkills == nil {
		manualSkills = subtractSkillNames(m.activeSkillNames(), m.profileSkills)
	}
	state := persistedSessionState{
		Version:             1,
		Messages:            m.agent.Messages(),
		Entries:             entries,
		ToolEntries:         persistToolEntries(m.toolEntries),
		Mode:                m.mode,
		Model:               m.model,
		ModelPinned:         m.modelPinned,
		AgentProfile:        m.agentProfile,
		LoadedFile:          m.loadedFile,
		ManualLoadedContext: m.manualLoadedContext,
		ManualSkills:        append([]string(nil), manualSkills...),
		SessionEvalTotal:    m.sessionEvalTotal,
		SessionPromptTotal:  m.sessionPromptTotal,
		SessionTurnCount:    m.sessionTurnCount,
		ExecutionCursor:     m.executionCursor,
		FileChanges:         m.fileChanges,
	}
	return marshalPersistedSessionState(state)
}

// EncodeHeadlessSessionState creates a version-1 snapshot that the interactive
// session picker can restore after a non-interactive run. Tool messages remain
// in model history, while the visible transcript stays focused on user and
// assistant text because headless mode has no persisted ToolCard state.
func EncodeHeadlessSessionState(messages []llm.Message, model, agentProfile string, modelPinned bool, executionCursor int64) (string, error) {
	if executionCursor < 0 {
		return "", fmt.Errorf("encode session state: execution cursor must not be negative")
	}
	entries := make([]persistedChatEntry, 0, len(messages))
	for _, message := range messages {
		switch message.Role {
		case "user", "assistant":
			if message.Content != "" {
				entries = append(entries, persistedChatEntry{
					Kind:    message.Role,
					Content: boundedSessionText(message.Content, maxPersistedToolResultBytes),
				})
			}
		}
	}
	return marshalPersistedSessionState(persistedSessionState{
		Version:         1,
		Messages:        append([]llm.Message(nil), messages...),
		Entries:         entries,
		Mode:            ModeBuild,
		Model:           model,
		ModelPinned:     modelPinned,
		AgentProfile:    agentProfile,
		ExecutionCursor: executionCursor,
	})
}

func marshalPersistedSessionState(state persistedSessionState) (string, error) {
	data, err := json.Marshal(state)
	if err != nil {
		return "", fmt.Errorf("encode session state: %w", err)
	}
	return string(data), nil
}

func decodeSessionState(raw string) (persistedSessionState, error) {
	var state persistedSessionState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return state, fmt.Errorf("decode session state: %w", err)
	}
	if state.Version != 1 {
		return state, fmt.Errorf("unsupported session state version %d", state.Version)
	}
	if state.ExecutionCursor < 0 {
		return state, fmt.Errorf("invalid execution cursor %d", state.ExecutionCursor)
	}
	return state, nil
}

func canonicalWorkspaceID(workDir string) (string, error) {
	if workDir == "" {
		var err error
		workDir, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve workspace: %w", err)
		}
	}
	absolute, err := filepath.Abs(workDir)
	if err != nil {
		return "", fmt.Errorf("resolve workspace: %w", err)
	}
	if resolved, resolveErr := filepath.EvalSymlinks(absolute); resolveErr == nil {
		absolute = resolved
	}
	return filepath.Clean(absolute), nil
}

func listPersistedSessions(ctx context.Context, store *db.Store, workspaceID string, limit int64) ([]SessionListItem, error) {
	if store == nil {
		return nil, fmt.Errorf("session persistence is unavailable")
	}
	if workspaceID == "" {
		return nil, fmt.Errorf("workspace identity is unavailable")
	}
	sessions, err := store.ListSessions(ctx, db.ListSessionsParams{WorkspaceID: workspaceID, Limit: limit})
	if err != nil {
		return nil, err
	}
	items := make([]SessionListItem, len(sessions))
	for i, session := range sessions {
		items[i] = SessionListItem{ID: session.ID, Title: session.Title, CreatedAt: session.UpdatedAt}
	}
	return items, nil
}

func loadPersistedSession(ctx context.Context, store *db.Store, id int64, workspaceID string) (db.Session, persistedSessionState, error) {
	if store == nil {
		return db.Session{}, persistedSessionState{}, fmt.Errorf("session persistence is unavailable")
	}
	session, err := store.GetSession(ctx, id)
	if err != nil {
		return db.Session{}, persistedSessionState{}, err
	}
	if workspaceID == "" || session.WorkspaceID != workspaceID {
		return db.Session{}, persistedSessionState{}, fmt.Errorf("session %d belongs to a different workspace", id)
	}
	raw, err := store.GetSessionState(ctx, id)
	if err != nil {
		return db.Session{}, persistedSessionState{}, err
	}
	state, err := decodeSessionState(raw)
	return session, state, err
}

func (m *Model) restoreSessionState(state persistedSessionState) error {
	if state.Version != 1 {
		return fmt.Errorf("unsupported session state version %d", state.Version)
	}
	if state.Mode < ModeAsk || state.Mode > ModeBuild {
		return fmt.Errorf("invalid saved mode %d", state.Mode)
	}

	targetManualSkills := uniqueSkillNames(state.ManualSkills)
	if err := m.validateSkillNames(uniqueSkillNames(m.manualSkills, m.profileSkills), "current session"); err != nil {
		return fmt.Errorf("validate current skills: %w", err)
	}
	if err := m.validateSkillNames(targetManualSkills, "saved manual"); err != nil {
		return fmt.Errorf("restore manual skills: %w", err)
	}

	var targetProfile *config.AgentProfile
	var targetProfileSkills []string
	var err error
	if state.AgentProfile != "" {
		targetProfile, err = m.validateAgentProfile(state.AgentProfile)
		if err != nil {
			return fmt.Errorf("restore agent profile: %w", err)
		}
		targetProfileSkills = uniqueSkillNames(targetProfile.Skills)
	}
	if err := m.validateSkillNames(uniqueSkillNames(targetManualSkills, targetProfileSkills), "saved session"); err != nil {
		return fmt.Errorf("restore skills: %w", err)
	}

	targetModel := state.Model
	if targetModel == "" && targetProfile != nil {
		targetModel = targetProfile.Model
	}
	oldModel := m.model
	modelSwitched := false
	if targetModel != "" && targetModel != m.model {
		if err := config.CheckModelMemorySafe(targetModel); err != nil {
			return fmt.Errorf("restore model: %w", err)
		}
		if m.modelManager != nil {
			m.prepareModelSwitch()
			if err := m.modelManager.SetCurrentModel(targetModel); err != nil {
				return fmt.Errorf("restore model: %w", err)
			}
		}
		modelSwitched = true
	}

	// Commit all prompt-affecting skill ownership only after the target model
	// has been admitted. Scope remains unchanged until every fallible operation
	// has succeeded.
	if err := m.setSkillContributions(targetManualSkills, targetProfileSkills); err != nil {
		if modelSwitched && m.modelManager != nil && oldModel != "" {
			_ = m.modelManager.SetCurrentModel(oldModel)
		}
		return fmt.Errorf("restore session skills: %w", err)
	}
	m.loadedFile = state.LoadedFile
	m.manualLoadedContext = state.ManualLoadedContext
	m.agentProfile = state.AgentProfile
	m.syncLoadedContext()
	// Authority scope is the final runtime commit and is never touched on a
	// validation/model/skill failure above.
	if m.agent != nil {
		if targetProfile == nil {
			m.agent.SetMCPServerScope(nil)
		} else {
			m.agent.SetMCPServerScope(targetProfile.MCPServers)
		}
	}
	if targetModel != "" {
		m.model = targetModel
	}

	m.mode = state.Mode
	m.setRouterMode(m.modeConfigs[m.mode].RouterMode)
	m.modelPinned = state.ModelPinned
	m.agent.ReplaceMessages(append([]llm.Message(nil), state.Messages...))
	m.entries = make([]ChatEntry, len(state.Entries))
	for i, entry := range state.Entries {
		m.entries[i] = ChatEntry{
			Kind:              entry.Kind,
			Content:           entry.Content,
			Name:              entry.Name,
			IsError:           entry.IsError,
			ToolIndex:         entry.ToolIndex,
			ThinkingContent:   entry.ThinkingContent,
			ThinkingCollapsed: entry.ThinkingCollapsed,
		}
	}
	m.toolEntries = restoreToolEntries(state.ToolEntries)
	m.sessionEvalTotal = state.SessionEvalTotal
	m.sessionPromptTotal = state.SessionPromptTotal
	m.sessionTurnCount = state.SessionTurnCount
	m.executionCursor = state.ExecutionCursor
	m.fileChanges = make(map[string]int, len(state.FileChanges))
	for path, count := range state.FileChanges {
		m.fileChanges[path] = count
	}

	m.toolsPending = 0
	m.toolCardMgr = NewToolCardManager(m.isDark)
	for i := range m.toolEntries {
		entry := &m.toolEntries[i]
		kind := ToolCardGeneric
		switch classifyTool(entry.Name) {
		case ToolTypeFileRead, ToolTypeFileWrite:
			kind = ToolCardFile
		case ToolTypeBash:
			kind = ToolCardBash
		}
		m.toolCardMgr.AddCardWithID(entry.ID, entry.Name, kind, entry.StartTime)
		card := &m.toolCardMgr.Cards[len(m.toolCardMgr.Cards)-1]
		card.Args = entry.Args
		card.SetSummary(entry.Summary)
		card.Result = entry.Result
		card.Duration = entry.Duration
		switch entry.Status {
		case ToolStatusRunning:
			card.State = ToolCardError
			card.Result = "Interrupted before session was saved"
			entry.Status = ToolStatusError
		case ToolStatusError:
			card.State = ToolCardError
		default:
			card.State = ToolCardSuccess
		}
	}
	return nil
}

// serializeEntries converts chat entries to markdown for storage.
func serializeEntries(entries []ChatEntry) string {
	var b strings.Builder
	for _, e := range entries {
		switch e.Kind {
		case "user":
			b.WriteString("## User\n\n")
			b.WriteString(e.Content)
			b.WriteString("\n\n")
		case "assistant":
			b.WriteString("## Assistant\n\n")
			b.WriteString(e.Content)
			b.WriteString("\n\n")
		case "system":
			b.WriteString("## System\n\n")
			b.WriteString(e.Content)
			b.WriteString("\n\n")
		case "error":
			b.WriteString("## Error\n\n")
			b.WriteString(e.Content)
			b.WriteString("\n\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// deserializeEntries parses markdown back into chat entries.
func deserializeEntries(content string) []ChatEntry {
	if content == "" {
		return nil
	}

	var entries []ChatEntry
	sections := strings.Split(content, "## ")

	for _, section := range sections {
		section = strings.TrimSpace(section)
		if section == "" {
			continue
		}

		nlIdx := strings.Index(section, "\n")
		if nlIdx == -1 {
			continue
		}

		header := strings.TrimSpace(section[:nlIdx])
		body := strings.TrimSpace(section[nlIdx+1:])

		var kind string
		switch header {
		case "User":
			kind = "user"
		case "Assistant":
			kind = "assistant"
		case "System":
			kind = "system"
		case "Error":
			kind = "error"
		default:
			continue
		}

		entries = append(entries, ChatEntry{
			Kind:    kind,
			Content: body,
		})
	}

	return entries
}
