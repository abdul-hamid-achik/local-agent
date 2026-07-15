package ui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/abdul-hamid-achik/local-agent/internal/agent"
	"github.com/abdul-hamid-achik/local-agent/internal/command"
)

// ReadScopePrompt is transient presentation authority for canonicalized
// external paths. It is never serialized into session state.
type ReadScopePrompt struct {
	Requested  string
	Canonical  string
	Workspace  string
	Draft      string
	Kind       agent.ReadGrantKind
	Grants     []agent.ReadGrant
	Operation  string
	AutoResume bool
}

func (m *Model) beginReadScopeAction(result command.Result, draft string) tea.Cmd {
	if m.agent == nil {
		m.appendReadScopeError("/scope is unavailable before the agent is initialized.")
		return nil
	}
	if m.readScopeOpRunning || m.readScopePrompt != nil {
		m.appendReadScopeError("A /scope change is already in progress.")
		return nil
	}

	operation := "add-read"
	path := strings.TrimSpace(result.Data)
	switch result.Action {
	case command.ActionRemoveReadRoot:
		operation = "remove-read"
	case command.ActionClearReadRoots:
		operation = "clear-read"
	}
	if operation != "clear-read" && path == "" {
		m.appendReadScopeError(fmt.Sprintf("/scope %s: no directory specified", operation))
		return nil
	}
	if strings.TrimSpace(draft) == "" {
		draft = "/scope " + operation
		if path != "" {
			draft += " " + path
		}
	}
	if operation == "add-read" {
		return m.beginReadScopePreview(path, draft)
	}
	return m.beginReadScopeMutation(operation, path, draft)
}

func (m *Model) beginReadScopePreview(path, draft string) tea.Cmd {
	m.readScopeOpToken++
	token := m.readScopeOpToken
	m.readScopeOpRunning = true
	m.readScopeOpLabel = "Checking external read root"
	m.readScopeOpDraft = draft
	m.input.Blur()
	m.recalcViewportHeight()

	workspace := m.agent.WorkDir()
	agentInstance := m.agent
	preview := func() tea.Msg {
		inspection, err := agentInstance.InspectReadPath(path)
		if err == nil && inspection.Kind != agent.ReadGrantDirectory {
			err = fmt.Errorf("read-only root is not a directory: %s", inspection.Path)
		}
		if err == nil && !inspection.External {
			err = fmt.Errorf("read-only root %q overlaps writable workspace %q", inspection.Path, canonicalReadScopeWorkspace(workspace))
		}
		if err == nil && inspection.AlreadyReadable {
			err = fmt.Errorf("read-only root is already inside active read authority: %s", inspection.Path)
		}
		canonicalWorkspace := canonicalReadScopeWorkspace(workspace)
		return ReadScopePreviewResultMsg{
			Token: token, Requested: path, Canonical: inspection.Path,
			Workspace: canonicalWorkspace, Draft: draft, Grant: inspection.Grant(), Err: err,
		}
	}
	return tea.Batch(m.startActivityCmd(), preview)
}

func (m *Model) handleReadScopePreviewResult(msg ReadScopePreviewResultMsg) {
	if !m.readScopeOpRunning || msg.Token != m.readScopeOpToken {
		msg.Grant.Release()
		return
	}
	m.readScopeOpRunning = false
	m.readScopeOpLabel = ""
	if m.shuttingDown {
		msg.Grant.Release()
		m.readScopeOpDraft = ""
		return
	}
	if msg.Err != nil {
		msg.Grant.Release()
		m.restoreReadScopeDraft(msg.Draft)
		m.readScopeOpDraft = ""
		m.appendReadScopeError(fmt.Sprintf("/scope add-read preview failed: %v", msg.Err))
		return
	}
	m.readScopePrompt = &ReadScopePrompt{
		Requested: msg.Requested, Canonical: msg.Canonical,
		Workspace: msg.Workspace, Draft: msg.Draft, Kind: agent.ReadGrantDirectory,
		Grants: []agent.ReadGrant{msg.Grant}, Operation: "add-read",
	}
	m.readScopeOpDraft = ""
	m.input.Blur()
	m.recalcViewportHeight()
}

func (m *Model) confirmReadScopePrompt() tea.Cmd {
	if m.readScopePrompt == nil {
		return nil
	}
	if !m.readScopePromptPathsDistinct() {
		// Resizing recomputes the projection. Keep the exact prompt and draft in
		// place until every authority is visually distinguishable.
		return nil
	}
	prompt := *m.readScopePrompt
	m.readScopePrompt = nil
	if len(prompt.Grants) > 0 {
		operation := prompt.Operation
		if operation == "" {
			operation = "add-intents"
		}
		return m.beginReadGrantMutation(prompt.Grants, prompt.Draft, prompt.AutoResume, operation)
	}
	m.restoreReadScopeDraft(prompt.Draft)
	m.appendReadScopeError("External read-path preview expired; inspect the path again.")
	return nil
}

func (m *Model) resolveReadScopePrompt(outcome string) {
	if m.readScopePrompt == nil {
		return
	}
	prompt := *m.readScopePrompt
	m.readScopePrompt = nil
	releaseReadGrants(prompt.Grants)
	m.restoreReadScopeDraft(prompt.Draft)
	message := "External read path denied; draft restored."
	if outcome == "cancelled" {
		message = "External read path request cancelled; draft restored."
	}
	m.entries = append(m.entries, ChatEntry{Kind: "system", Content: message})
	m.invalidateEntryCache()
	m.viewport.SetContent(m.renderEntries())
	m.gotoBottomIfFollowing()
	m.recalcViewportHeight()
}

func (m *Model) beginReadGrantMutation(grants []agent.ReadGrant, draft string, autoResume bool, operation string) tea.Cmd {
	m.readScopeOpToken++
	token := m.readScopeOpToken
	m.readScopeOpRunning = true
	m.readScopeOpDraft = draft
	m.readScopeOpLabel = "Granting approved read paths"
	m.input.Blur()
	m.recalcViewportHeight()

	agentInstance := m.agent
	approved := append([]agent.ReadGrant(nil), grants...)
	mutate := func() tea.Msg {
		msg := ReadScopeResultMsg{
			Token: token, Operation: operation, AutoResume: autoResume,
		}
		msg.Grants, msg.RolledBack, msg.RollbackErr, msg.Err = applyPromptReadGrantsTransactional(agentInstance, approved)
		msg.Count = len(msg.Grants)
		if len(msg.Grants) == 1 {
			msg.Path = msg.Grants[0].Path
			msg.Kind = string(msg.Grants[0].Kind)
		}
		return msg
	}
	return tea.Batch(m.startActivityCmd(), mutate)
}

// applyPromptReadGrantsTransactional gives a multi-path approval all-or-none
// behavior from the host's perspective. The Agent owns canonicalization and
// authority validation; this layer snapshots only its typed public grants so
// compensation never revokes authority that existed before this approval.
func applyPromptReadGrantsTransactional(agentInstance *agent.Agent, approved []agent.ReadGrant) ([]agent.ReadGrant, int, error, error) {
	if agentInstance == nil {
		releaseReadGrants(approved)
		return nil, 0, nil, errors.New("agent is unavailable")
	}
	defer releaseReadGrants(approved)
	before, snapshotErr := agentInstance.SnapshotReadGrants()
	if snapshotErr != nil {
		return nil, 0, nil, fmt.Errorf("snapshot existing read grants: %w", snapshotErr)
	}
	defer releaseReadGrants(before)
	applied := make([]agent.ReadGrant, 0, len(approved))
	for _, grant := range approved {
		canonical, err := agentInstance.AddInspectedReadGrant(grant)
		if err != nil {
			rolledBack, rollbackErr := restoreReadGrantSnapshot(agentInstance, before)
			return nil, rolledBack, rollbackErr, fmt.Errorf("grant %s read access: %w", grant.Kind, err)
		}
		applied = append(applied, agent.ReadGrant{Path: canonical, Kind: grant.Kind})
	}
	return applied, 0, nil, nil
}

func releaseReadGrants(grants []agent.ReadGrant) {
	for _, grant := range grants {
		grant.Release()
	}
}

func restoreReadGrantSnapshot(agentInstance *agent.Agent, before []agent.ReadGrant) (int, error) {
	beforeByKey := make(map[string]agent.ReadGrant, len(before))
	for _, grant := range before {
		beforeByKey[readGrantKey(grant)] = grant
	}
	current := agentInstance.ReadGrants()
	rolledBack := 0
	var rollbackErr error

	// Remove only authorities introduced by this batch. Directories go first
	// because one may have superseded a pre-existing exact-file grant that must
	// be restored below.
	for _, kind := range []agent.ReadGrantKind{agent.ReadGrantDirectory, agent.ReadGrantExactFile} {
		for _, grant := range current {
			if grant.Kind != kind {
				continue
			}
			if _, existed := beforeByKey[readGrantKey(grant)]; existed {
				continue
			}
			if _, err := agentInstance.RemoveReadPath(grant.Path); err != nil {
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf("revoke new %s %q: %w", grant.Kind, grant.Path, err))
			} else {
				rolledBack++
			}
		}
	}

	currentByKey := make(map[string]struct{})
	for _, grant := range agentInstance.ReadGrants() {
		currentByKey[readGrantKey(grant)] = struct{}{}
	}
	// Restore any narrower pre-existing grant superseded by a newly added
	// directory before a later grant failed.
	for _, kind := range []agent.ReadGrantKind{agent.ReadGrantDirectory, agent.ReadGrantExactFile} {
		for _, grant := range before {
			if grant.Kind != kind {
				continue
			}
			if _, present := currentByKey[readGrantKey(grant)]; present {
				continue
			}
			_, err := agentInstance.AddInspectedReadGrant(grant)
			if err != nil {
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf("restore pre-existing %s %q: %w", grant.Kind, grant.Path, err))
			} else {
				rolledBack++
				currentByKey[readGrantKey(grant)] = struct{}{}
			}
		}
	}
	return rolledBack, rollbackErr
}

func readGrantKey(grant agent.ReadGrant) string {
	return string(grant.Kind) + "\x00" + filepath.Clean(grant.Path)
}

func (m *Model) beginReadScopeMutation(operation, path, draft string) tea.Cmd {
	m.readScopeOpToken++
	token := m.readScopeOpToken
	m.readScopeOpRunning = true
	m.readScopeOpDraft = draft
	switch operation {
	case "add-read":
		m.readScopeOpLabel = "Adding external read-only root"
	case "add-file":
		m.readScopeOpLabel = "Adding exact read-only file"
	case "remove-read":
		m.readScopeOpLabel = "Removing read-only root"
	case "clear-read":
		m.readScopeOpLabel = "Clearing read-only roots"
	default:
		m.readScopeOpLabel = "Updating read-only scope"
	}
	m.input.Blur()
	m.recalcViewportHeight()

	agentInstance := m.agent
	mutate := func() tea.Msg {
		msg := ReadScopeResultMsg{Token: token, Operation: operation, Path: path}
		switch operation {
		case "add-read":
			msg.Path, msg.Err = agentInstance.AddReadRoot(path)
			msg.Kind = string(agent.ReadGrantDirectory)
		case "add-file":
			msg.Path, msg.Err = agentInstance.AddReadFile(path)
			msg.Kind = string(agent.ReadGrantExactFile)
		case "remove-read":
			var grant agent.ReadGrant
			grant, msg.Err = agentInstance.RemoveReadPath(path)
			msg.Path, msg.Kind = grant.Path, string(grant.Kind)
		case "clear-read":
			msg.Count, msg.Err = agentInstance.ClearReadRoots()
		}
		return msg
	}
	return tea.Batch(m.startActivityCmd(), mutate)
}

func (m *Model) handleReadScopeResult(msg ReadScopeResultMsg) tea.Cmd {
	if !m.readScopeOpRunning || msg.Token != m.readScopeOpToken {
		return nil
	}
	draft := m.readScopeOpDraft
	m.readScopeOpRunning = false
	m.readScopeOpLabel = ""
	m.readScopeOpDraft = ""
	if m.shuttingDown {
		return nil
	}
	m.input.Focus()
	if msg.Err != nil {
		m.restoreReadScopeDraft(draft)
		prefix := fmt.Sprintf("/scope %s failed", msg.Operation)
		if msg.Operation == "add-intents" {
			prefix = "External read-path authorization failed"
		}
		if msg.RollbackErr != nil {
			prefix += fmt.Sprintf("; rollback could not fully restore the previous authority set: %v", msg.RollbackErr)
		} else if msg.RolledBack > 0 {
			changeLabel := "changes"
			if msg.RolledBack == 1 {
				changeLabel = "change"
			}
			prefix += fmt.Sprintf("; rolled back %d authority %s, so no partial approval remains", msg.RolledBack, changeLabel)
		}
		m.entries = append(m.entries, ChatEntry{
			Kind: "error", Content: sanitizeTerminalSingleLine(fmt.Sprintf("%s: %v", prefix, msg.Err)),
		})
	} else {
		var receipt string
		path := terminalSafePathLiteral(msg.Path)
		switch msg.Operation {
		case "add-read":
			receipt = fmt.Sprintf("Added temporary read-only root: %s\nWrite authority remains confined to the working directory. This grant is not saved with sessions.", path)
		case "add-file":
			receipt = fmt.Sprintf("Added temporary exact-file read grant: %s\nSibling files remain unavailable. Write authority remains confined to the working directory. This grant is not saved with sessions.", path)
		case "remove-read":
			kind := "directory"
			if msg.Kind == string(agent.ReadGrantExactFile) {
				kind = "exact-file"
			}
			receipt = fmt.Sprintf("Removed temporary %s read grant: %s", kind, path)
		case "clear-read":
			grantLabel := "grants"
			if msg.Count == 1 {
				grantLabel = "grant"
			}
			receipt = fmt.Sprintf("Cleared %d temporary read-only %s.", msg.Count, grantLabel)
		case "add-intents":
			pathLabel := "paths"
			if msg.Count == 1 {
				pathLabel = "path"
			}
			receipt = fmt.Sprintf("Granted temporary read-only access to %d explicit %s. Sibling files remain unavailable unless a directory was explicitly approved; write authority is unchanged.", msg.Count, pathLabel)
		default:
			receipt = "Updated temporary read-only roots."
		}
		m.entries = append(m.entries, ChatEntry{Kind: "system", Content: receipt})
	}
	m.invalidateEntryCache()
	m.viewport.SetContent(m.renderEntries())
	m.gotoBottomIfFollowing()
	m.recalcViewportHeight()
	if msg.Err == nil && msg.AutoResume && strings.TrimSpace(draft) != "" {
		return m.submitPreparedInput(draft)
	}
	return nil
}

func (m *Model) restoreReadScopeDraft(draft string) {
	if strings.TrimSpace(draft) == "" {
		return
	}
	m.input.SetValue(draft)
	m.input.CursorEnd()
	m.input.Focus()
	_ = m.reflowInputViewport()
}

func (m *Model) appendReadScopeError(message string) {
	m.entries = append(m.entries, ChatEntry{Kind: "error", Content: sanitizeTerminalSingleLine(message)})
	m.invalidateEntryCache()
	m.viewport.SetContent(m.renderEntries())
	m.resumeFollow()
}

func canonicalReadScopeWorkspace(workspace string) string {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		if current, err := os.Getwd(); err == nil {
			workspace = current
		}
	}
	if absolute, err := filepath.Abs(workspace); err == nil {
		workspace = absolute
	}
	if resolved, err := filepath.EvalSymlinks(workspace); err == nil {
		workspace = resolved
	}
	return filepath.Clean(workspace)
}

func canonicalReadScopePreview(requested, workspace string, existing []string) (string, string, error) {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return "", "", errors.New("read-only root path is empty")
	}
	if requested == "~" || strings.HasPrefix(requested, "~"+string(filepath.Separator)) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", "", fmt.Errorf("resolve home directory: %w", err)
		}
		if requested == "~" {
			requested = home
		} else {
			requested = filepath.Join(home, strings.TrimPrefix(requested, "~"+string(filepath.Separator)))
		}
	}
	absolute, err := filepath.Abs(requested)
	if err != nil {
		return "", "", fmt.Errorf("resolve read-only root %q: %w", requested, err)
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", "", fmt.Errorf("resolve read-only root %q: %w", absolute, err)
	}
	canonical = filepath.Clean(canonical)
	if filepath.Dir(canonical) == canonical {
		return "", "", errors.New("refusing to grant a filesystem root as read-only scope")
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", "", fmt.Errorf("inspect read-only root: %w", err)
	}
	if !info.IsDir() {
		return "", "", fmt.Errorf("read-only root is not a directory: %s", canonical)
	}

	canonicalWorkspace := strings.TrimSpace(workspace)
	if canonicalWorkspace == "" {
		canonicalWorkspace, err = os.Getwd()
		if err != nil {
			return "", "", fmt.Errorf("resolve workspace: %w", err)
		}
	}
	canonicalWorkspace, err = filepath.Abs(canonicalWorkspace)
	if err != nil {
		return "", "", fmt.Errorf("resolve workspace: %w", err)
	}
	if resolved, resolveErr := filepath.EvalSymlinks(canonicalWorkspace); resolveErr == nil {
		canonicalWorkspace = resolved
	}
	canonicalWorkspace = filepath.Clean(canonicalWorkspace)
	if readScopePathsOverlap(canonicalWorkspace, canonical) {
		return "", "", fmt.Errorf("read-only root %q overlaps writable workspace %q", canonical, canonicalWorkspace)
	}
	for _, root := range existing {
		if readScopePathsOverlap(root, canonical) {
			return "", "", fmt.Errorf("read-only root %q overlaps existing root %q", canonical, root)
		}
	}
	return canonical, canonicalWorkspace, nil
}

func readScopePathsOverlap(first, second string) bool {
	contains := func(parent, candidate string) bool {
		relative, err := filepath.Rel(parent, candidate)
		if err != nil {
			return false
		}
		return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
	}
	return contains(first, second) || contains(second, first)
}

func (m *Model) renderReadScopePrompt() string {
	if m.readScopePrompt == nil {
		return ""
	}
	width := max(1, m.chatPaneWidth()-4)
	prompt := m.readScopePrompt
	workspace := compactWorkspacePath(prompt.Workspace, width)
	compact := m.width <= 40 || m.height <= 16
	grants := readScopePromptGrants(prompt)
	pathLabels, pathsDistinct := m.readScopePromptPathLabels(grants)
	subject := "external read-only directory"
	if len(grants) > 1 {
		subject = fmt.Sprintf("%d explicit external read paths", len(grants))
	} else if grants[0].Kind == agent.ReadGrantExactFile {
		subject = "exact external read-only file"
	}
	lines := []string{m.styles.ApprovalPrompt.Render(truncateDisplay("Allow "+subject+"?", width))}
	if compact {
		for index, grant := range grants {
			kind := "directory"
			if grant.Kind == agent.ReadGrantExactFile {
				kind = "exact file"
			}
			lines = append(lines, m.styles.OverlayDim.Render(truncateDisplay(kind+" · "+pathLabels[index], width)))
		}
		lines = append(lines,
			m.styles.StatusText.Render(truncateDisplay("Temporary · not saved · read-only", width)),
			m.styles.StatusText.Render(truncateDisplay("Writes stay in the workspace", width)),
		)
	} else {
		for index, grant := range grants {
			label := "Directory"
			if grant.Kind == agent.ReadGrantExactFile {
				label = "Exact file"
			}
			lines = append(lines, m.runtimeStatusRow(label, pathLabels[index], width))
		}
		lines = append(lines,
			m.runtimeStatusRow("Access", "Temporary read-only authority; exact files never include siblings and grants are not saved with sessions", width),
			m.runtimeStatusRow("Writes", "Never granted here; write authority remains confined to "+workspace, width),
		)
	}
	if !pathsDistinct {
		lines = append(lines, m.styles.ApprovalPrompt.Render(truncateDisplay("Widen · y disabled", width)))
	}
	if width < 36 {
		if pathsDistinct {
			lines = append(lines,
				m.renderKeyHints(width, keyHint{Key: "y", Action: "allow"}, keyHint{Key: "n", Action: "deny"}),
				m.renderKeyHints(width, keyHint{Key: "esc", Action: "cancel"}),
			)
		} else {
			lines = append(lines, m.renderKeyHints(width,
				keyHint{Key: "n", Action: "deny"}, keyHint{Key: "esc", Action: "cancel"},
			))
		}
	} else {
		hints := []keyHint{{Key: "n", Action: "deny"}, {Key: "esc", Action: "cancel"}}
		if pathsDistinct {
			hints = append([]keyHint{{Key: "y", Action: "allow read-only"}}, hints...)
		}
		lines = append(lines, m.renderKeyHints(width, hints...))
	}
	return indentApprovalSurface(strings.Join(lines, "\n"), 2, m.chatPaneWidth())
}

func readScopePromptGrants(prompt *ReadScopePrompt) []agent.ReadGrant {
	if prompt == nil {
		return nil
	}
	grants := append([]agent.ReadGrant(nil), prompt.Grants...)
	if len(grants) == 0 {
		grants = []agent.ReadGrant{{Path: prompt.Canonical, Kind: prompt.Kind}}
	}
	return grants
}

func (m *Model) readScopePromptPathsDistinct() bool {
	if m == nil || m.readScopePrompt == nil {
		return false
	}
	_, distinct := m.readScopePromptPathLabels(readScopePromptGrants(m.readScopePrompt))
	return distinct
}

// readScopePromptPathLabels projects every exact authority with the same width
// budget used by the renderer. A compact collision disables confirmation until
// a resize makes the identities distinguishable; it never changes the grants.
func (m *Model) readScopePromptPathLabels(grants []agent.ReadGrant) ([]string, bool) {
	width := max(1, m.chatPaneWidth()-4)
	compact := m.width <= 40 || m.height <= 16
	labels := make([]string, len(grants))
	seen := make(map[string]string, len(grants))
	distinct := true
	for index, grant := range grants {
		pathWidth := runtimeStatusValueWidth(width)
		if compact {
			kind := "directory"
			if grant.Kind == agent.ReadGrantExactFile {
				kind = "exact file"
			}
			pathWidth = max(1, width-lipgloss.Width(kind)-3)
		}
		label := compactWorkspacePath(grant.Path, pathWidth)
		labels[index] = label
		if prior, exists := seen[label]; exists && prior != grant.Path {
			distinct = false
		} else {
			seen[label] = grant.Path
		}
	}
	return labels, distinct
}
