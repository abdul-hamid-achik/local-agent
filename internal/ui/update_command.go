package ui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"github.com/abdul-hamid-achik/local-agent/internal/command"
	"github.com/abdul-hamid-achik/local-agent/internal/safeio"
)

// buildCommandContext creates a Context for slash command execution.
func (m *Model) buildCommandContext() *command.Context {
	artifacts, artifactsTruncated := commandArtifactInfos(m.toolEntries)
	ctx := &command.Context{
		Model:              m.model,
		ModelList:          m.modelList,
		AgentProfile:       m.agentProfile,
		AgentList:          m.agentList,
		ToolCount:          m.toolCount,
		ServerCount:        m.serverCount,
		LoadedFile:         m.loadedFile,
		ICEEnabled:         m.iceEnabled,
		ICEConversations:   m.iceConversations,
		ICESessionID:       m.iceSessionID,
		SessionEvalTotal:   m.sessionEvalTotal,
		SessionPromptTotal: m.sessionPromptTotal,
		LatestPromptTokens: m.promptTokens,
		SessionTurnCount:   m.sessionTurnCount,
		NumCtx:             m.numCtx,
		CurrentModel:       m.model,
		Artifacts:          artifacts,
		ArtifactsTruncated: artifactsTruncated,
		FileChanges:        m.fileChanges,
	}
	if m.agent != nil {
		ctx.Servers = m.commandMCPServers()
		_, _, ctx.MCPToolCount = m.mcpStatusCounts()
		if len(ctx.Servers) == 0 {
			ctx.ServerNames = m.agent.ServerNames()
		}
		ctx.ReadRoots = m.agent.ReadRoots()
		for _, grant := range m.agent.ReadGrants() {
			ctx.ReadGrants = append(ctx.ReadGrants, command.ReadGrantInfo{Path: grant.Path, Kind: string(grant.Kind)})
		}
	}
	if m.goalRuntime != nil {
		if snapshot, err := m.goalRuntime.Snapshot(context.Background()); err == nil {
			ctx.GoalConfigured = true
			ctx.GoalObjective = snapshot.Objective
			ctx.GoalStatus = string(snapshot.State)
			ctx.GoalPending = snapshot.PendingContinuation != nil
			ctx.GoalExhausted = len(snapshot.ExhaustedBy) > 0
			if snapshot.Blocker != nil {
				ctx.GoalBlocker = string(snapshot.Blocker.Kind)
			}
		}
	}
	ctx.GoalPersistenceDirty = m.goalPersistenceDirty
	ctx.GoalBusy = m.goalOperationRunning || m.goalOperation != ""

	if m.skillMgr != nil {
		for _, s := range m.skillMgr.All() {
			ctx.Skills = append(ctx.Skills, command.SkillInfo{
				Name:        s.Name,
				Description: s.Description,
				Active:      s.Active,
			})
		}
	}

	return ctx
}

// handleCommandAction processes a command result's action.
func (m *Model) handleCommandAction(result command.Result) tea.Cmd {
	return m.handleCommandActionWithDraft(result, "")
}

func (m *Model) handleCommandActionWithDraft(result command.Result, draft string) tea.Cmd {
	switch result.Action {
	case command.ActionShowHelp:
		m.overlayParent = OverlayNone
		m.overlay = OverlayHelp
		m.initHelpViewport()
		return nil

	case command.ActionClear:
		if m.queuedFollowUpHeld() {
			// submitPreparedInput has already consumed the slash command. Restore
			// it so resolving the old-session owner does not also lose the user's
			// requested reset.
			if draft != "" && strings.TrimSpace(m.input.Value()) == "" {
				m.setComposerDraftAtRune(draft, utf8.RuneCountInString(draft))
			}
			m.blockSessionReplacementForHeldFollowUp("starting a new conversation")
			return nil
		}
		m.agent.ClearHistory()
		m.entries = nil
		m.toolEntries = nil
		m.resetConversationSession()
		m.invalidateEntryCache()
		if result.Text != "" {
			m.entries = append(m.entries, ChatEntry{
				Kind:    "system",
				Content: result.Text,
			})
		}
		m.viewport.SetContent(m.renderEntries())
		m.resumeFollow()
		return nil

	case command.ActionQuit:
		return m.beginShutdown()

	case command.ActionAddReadRoot, command.ActionRemoveReadRoot, command.ActionClearReadRoots:
		return m.beginReadScopeAction(result, draft)

	case command.ActionAttachImage:
		if m.goalRuntime != nil {
			m.entries = append(m.entries, ChatEntry{Kind: "error", Content: "Images cannot be attached to a host-owned goal continuation. Finish or drop the goal, then attach the image to an ordinary prompt."})
			m.viewport.SetContent(m.renderEntries())
			m.resumeFollow()
			return nil
		}
		return m.beginImageFileAttachment(result.Data, "")

	case command.ActionListImages:
		if len(m.pendingImages) == 0 {
			m.entries = append(m.entries, ChatEntry{Kind: "system", Content: "No images are attached to the pending prompt. Paste or drag an image file path, or run /image <path>."})
		} else {
			m.entries = append(m.entries, ChatEntry{Kind: "system", Content: sanitizeTerminalMultiline(m.renderPlainImageList())})
		}
		m.viewport.SetContent(m.renderEntries())
		m.resumeFollow()
		return nil

	case command.ActionClearImages:
		count := m.clearPendingImages()
		m.entries = append(m.entries, ChatEntry{Kind: "system", Content: fmt.Sprintf("Cleared %d pending image attachment%s.", count, pluralSuffix(count))})
		m.viewport.SetContent(m.renderEntries())
		m.resumeFollow()
		return nil

	case command.ActionForgetImageHistory:
		if m.goalRuntime != nil {
			m.entries = append(m.entries, ChatEntry{Kind: "error", Content: "Image history cannot be changed while a durable goal is attached. Finish or drop the goal first."})
			m.viewport.SetContent(m.renderEntries())
			m.resumeFollow()
			return nil
		}
		return m.forgetHistoricalImages()

	case command.ActionLoadContext:
		path := strings.TrimSpace(result.Data)
		if path == "" {
			m.entries = append(m.entries, ChatEntry{Kind: "error", Content: "load: no path specified"})
			m.viewport.SetContent(m.renderEntries())
			m.resumeFollow()
			return nil
		}
		m.fileOpToken++
		token := m.fileOpToken
		m.fileLoading = true
		m.input.Blur()
		m.entries = append(m.entries, ChatEntry{Kind: "system", Content: fmt.Sprintf("Loading context from: %s (Esc cancels)", path)})
		m.invalidateEntryCache()
		m.viewport.SetContent(m.renderEntries())
		m.resumeFollow()
		load := func() tea.Msg {
			data, err := safeio.ReadRegularFileNoFollow(path, maxLoadedContextBytes, safeio.StartupReadTimeout)
			return ContextLoadResultMsg{Token: token, Path: path, Data: string(data), Err: err}
		}
		return tea.Batch(m.startActivityCmd(), load)

	case command.ActionUnloadContext:
		m.loadedFile = ""
		m.manualLoadedContext = ""
		m.syncLoadedContext()
		if result.Text != "" {
			m.entries = append(m.entries, ChatEntry{
				Kind:    "system",
				Content: result.Text,
			})
		}
		m.viewport.SetContent(m.renderEntries())
		m.resumeFollow()
		return nil

	case command.ActionActivateSkill:
		if m.skillMgr != nil {
			if err := m.setManualSkill(result.Data, true); err != nil {
				m.entries = append(m.entries, ChatEntry{
					Kind:    "error",
					Content: err.Error(),
				})
			} else {
				m.entries = append(m.entries, ChatEntry{
					Kind:    "system",
					Content: result.Text,
				})
			}
		}
		m.viewport.SetContent(m.renderEntries())
		m.resumeFollow()
		return nil

	case command.ActionDeactivateSkill:
		if m.skillMgr != nil {
			if err := m.setManualSkill(result.Data, false); err != nil {
				m.entries = append(m.entries, ChatEntry{
					Kind:    "error",
					Content: err.Error(),
				})
			} else {
				m.entries = append(m.entries, ChatEntry{
					Kind:    "system",
					Content: result.Text,
				})
			}
		}
		m.viewport.SetContent(m.renderEntries())
		m.resumeFollow()
		return nil

	case command.ActionSwitchModel:
		// Find last user query for learning
		query := ""
		currentInput := strings.TrimSpace(m.input.Value())
		if currentInput != "" && !strings.HasPrefix(currentInput, "/") {
			query = currentInput
		} else {
			// Find last user message in conversation
			for i := len(m.entries) - 1; i >= 0; i-- {
				if m.entries[i].Kind == "user" {
					query = m.entries[i].Content
					break
				}
			}
		}
		// Record the override for learning
		if m.router != nil && query != "" {
			m.router.RecordOverride(query, result.Data)
		}
		m.selectModel(result.Data)
		return nil

	case command.ActionEnableAutoModel:
		if err := m.enableAutomaticModelRouting(); err != nil {
			m.entries = append(m.entries, ChatEntry{Kind: "error", Content: err.Error()})
		} else {
			m.entries = append(m.entries, ChatEntry{Kind: "system", Content: result.Text})
		}
		m.viewport.SetContent(m.renderEntries())
		m.resumeFollow()
		return nil

	case command.ActionShowModelPicker:
		m.overlayParent = OverlayNone
		m.openModelPicker()
		return nil

	case command.ActionSendPrompt:
		if m.goalRuntime != nil {
			return m.rejectPromptWhileGoalAttached(result.Data, true)
		}
		if result.Text != "" {
			m.entries = append(m.entries, ChatEntry{Kind: "system", Content: result.Text})
		}
		return m.sendToAgent(result.Data)

	case command.ActionCommit:
		if m.commitRunning {
			m.entries = append(m.entries, ChatEntry{
				Kind:    "error",
				Content: "A commit is already in progress. Wait for it to finish before starting another.",
			})
			m.viewport.SetContent(m.renderEntries())
			m.resumeFollow()
			return nil
		}
		m.entries = append(m.entries, ChatEntry{
			Kind:    "system",
			Content: "Generating commit message from staged changes. Automated /commit disables Git hooks, signing, fsmonitor, and background maintenance.",
		})
		m.viewport.SetContent(m.renderEntries())
		m.resumeFollow()
		m.commitToken++
		ctx, cancel := context.WithCancel(context.Background())
		m.commitCancel = cancel
		m.commitRunning = true
		m.input.Blur()
		runner := m.commitRunner
		if runner == nil {
			runner = runCommit
		}
		return tea.Batch(
			m.startActivityCmd(),
			runner(ctx, m.agent.LLMClient(), m.model, result.Data, m.agent.WorkDir(), m.commitToken),
		)

	case command.ActionShowSessions:
		m.overlayParent = OverlayNone
		m.openSessionsPicker()
		return m.requestSessions()

	case command.ActionSwitchAgent:
		if err := m.applyAgentProfile(result.Data); err != nil {
			m.entries = append(m.entries, ChatEntry{
				Kind:    "error",
				Content: err.Error(),
			})
		} else {
			m.entries = append(m.entries, ChatEntry{
				Kind:    "system",
				Content: result.Text,
			})
		}
		m.viewport.SetContent(m.renderEntries())
		m.resumeFollow()
		return nil

	case command.ActionExport:
		path := result.Data
		if path == "" {
			m.entries = append(m.entries, ChatEntry{
				Kind:    "error",
				Content: "export: no path specified",
			})
			m.viewport.SetContent(m.renderEntries())
			m.resumeFollow()
			return nil
		}
		if m.exportRunning {
			m.entries = append(m.entries, ChatEntry{Kind: "error", Content: "An export is already in progress. Wait for its receipt before starting another."})
			m.viewport.SetContent(m.renderEntries())
			m.resumeFollow()
			return nil
		}
		content := []byte(m.formatConversationForExport())
		workDir := m.agent.WorkDir()
		force := result.Force
		m.exportToken++
		token := m.exportToken
		m.exportRunning = true
		m.input.Blur()
		m.entries = append(m.entries, ChatEntry{Kind: "system", Content: fmt.Sprintf("Exporting conversation to: %s", path)})
		m.invalidateEntryCache()
		m.viewport.SetContent(m.renderEntries())
		m.resumeFollow()
		return tea.Batch(m.startActivityCmd(), exportConversationCmd(workDir, path, content, force, token))

	case command.ActionImport:
		if m.queuedFollowUpHeld() {
			// Import replaces both the visible and model transcripts. Keep the
			// consumed slash command recoverable until the old-session follow-up
			// owner is explicitly swapped or cleared.
			if draft != "" && strings.TrimSpace(m.input.Value()) == "" {
				m.setComposerDraftAtRune(draft, utf8.RuneCountInString(draft))
			}
			m.blockSessionReplacementForHeldFollowUp("importing another conversation")
			return nil
		}
		path := result.Data
		if path == "" {
			m.entries = append(m.entries, ChatEntry{
				Kind:    "error",
				Content: "import: no path specified",
			})
			m.viewport.SetContent(m.renderEntries())
			m.resumeFollow()
			return nil
		}
		m.fileOpToken++
		token := m.fileOpToken
		m.fileLoading = true
		m.input.Blur()
		m.entries = append(m.entries, ChatEntry{Kind: "system", Content: fmt.Sprintf("Importing conversation from: %s (Esc cancels)", path)})
		m.invalidateEntryCache()
		m.viewport.SetContent(m.renderEntries())
		m.resumeFollow()
		load := func() tea.Msg {
			data, err := safeio.ReadRegularFile(path, maxImportBytes, safeio.StartupReadTimeout)
			if err != nil {
				return ImportResultMsg{Token: token, Path: path, Err: err}
			}
			entries, err := parseImportedConversationData(string(data))
			if err != nil {
				return ImportResultMsg{Token: token, Path: path, Err: fmt.Errorf("parse transcript: %w", err)}
			}
			messages, uiOnlySections, err := importedConversationMessages(entries)
			if err != nil {
				return ImportResultMsg{Token: token, Path: path, Err: fmt.Errorf("reject transcript: %w", err)}
			}
			toolSections := 0
			for _, line := range strings.Split(string(data), "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "## Tool:") {
					toolSections++
				}
			}
			return ImportResultMsg{
				Token: token, Path: path, Entries: entries, Messages: messages,
				UIOnlySections: uiOnlySections, ToolSections: toolSections,
			}
		}
		return tea.Batch(m.startActivityCmd(), load)

	case command.ActionCheckpoint:
		id, err := m.agent.CreateCheckpoint(context.Background(), result.Data, "manual")
		var note string
		if err != nil {
			note = fmt.Sprintf("checkpoint failed: %v", err)
		} else if id == 0 {
			note = "checkpoints are unavailable (database not open)"
		} else {
			label := result.Data
			if label != "" {
				label = " \"" + label + "\""
			}
			note = fmt.Sprintf("saved checkpoint #%d%s — restore with /restore %d", id, label, id)
		}
		m.entries = append(m.entries, ChatEntry{Kind: "system", Content: note})
		m.viewport.SetContent(m.renderEntries())
		m.resumeFollow()
		return nil

	case command.ActionListCheckpoints:
		cps, err := m.agent.ListCheckpoints(context.Background())
		var b strings.Builder
		if err != nil {
			fmt.Fprintf(&b, "could not list checkpoints: %v", err)
		} else if len(cps) == 0 {
			b.WriteString("No checkpoints yet. Save one with /checkpoint [label].")
		} else {
			fmt.Fprintf(&b, "Checkpoints (%d) — restore with /restore <id>:\n", len(cps))
			for _, c := range cps {
				label := c.Label
				if label == "" {
					label = "(no label)"
				}
				fmt.Fprintf(&b, "  #%d  %s  ·  %s  ·  %d msgs  ·  %s\n", c.ID, label, c.Kind, c.MsgCount, c.CreatedAt)
			}
		}
		m.entries = append(m.entries, ChatEntry{Kind: "system", Content: strings.TrimRight(b.String(), "\n")})
		m.viewport.SetContent(m.renderEntries())
		m.resumeFollow()
		return nil

	case command.ActionRestoreCheckpoint:
		id, perr := strconv.ParseInt(strings.TrimSpace(result.Data), 10, 64)
		if perr != nil {
			m.entries = append(m.entries, ChatEntry{Kind: "error", Content: fmt.Sprintf("restore: %q is not a valid checkpoint id", result.Data)})
			m.viewport.SetContent(m.renderEntries())
			m.resumeFollow()
			return nil
		}
		n, err := m.agent.RestoreCheckpoint(context.Background(), id)
		if err != nil {
			m.entries = append(m.entries, ChatEntry{Kind: "error", Content: fmt.Sprintf("restore failed: %v", err)})
			m.viewport.SetContent(m.renderEntries())
			m.resumeFollow()
			return nil
		}
		// Rebuild the visible transcript from the restored agent history.
		m.entries = entriesFromMessages(m.agent.Messages())
		m.toolEntries = nil
		m.invalidateEntryCache()
		m.entries = append(m.entries, ChatEntry{
			Kind:    "system",
			Content: fmt.Sprintf("restored checkpoint #%d — conversation rewound to %d messages", id, n),
		})
		m.viewport.SetContent(m.renderEntries())
		m.resumeFollow()
		return nil

	case command.ActionOpenPlan:
		if m.goalRuntime != nil {
			return m.rejectPromptWhileGoalAttached(result.Data, true)
		}
		m.setMode(ModePlan)
		m.openPlanForm(result.Data)
		return nil

	case command.ActionOpenGoal:
		var err error
		var goalCmd tea.Cmd
		if result.Goal != nil {
			goalCmd, err = m.openGoalRequestForm(*result.Goal)
		} else {
			err = m.openGoalForm(result.Data, false)
		}
		if err != nil {
			m.appendGoalError(err.Error())
		}
		return goalCmd

	case command.ActionEditGoalBudget:
		if err := m.openGoalForm("", true); err != nil {
			m.appendGoalError(err.Error())
		}
		return nil

	case command.ActionShowGoal:
		// Opening a centered modal over an asynchronously changing empty-state
		// header can otherwise leave terminal cells from the previous frame in
		// the modal gutter. Request one full Charm repaint at the ownership
		// transition; the inspector remains a dumb child and any recovery load
		// still runs beside the presentation command.
		return tea.Batch(m.showGoal(), tea.ClearScreen)

	case command.ActionPauseGoal:
		m.pauseGoal()
		return nil

	case command.ActionResumeGoal:
		return m.resumeGoal()

	case command.ActionDropGoal:
		m.dropGoal()
		return nil

	case command.ActionRecoverExecution:
		return m.openStandaloneRecovery()

	default:
		if result.Action != command.ActionNone {
			m.entries = append(m.entries, ChatEntry{
				Kind:    "error",
				Content: fmt.Sprintf("unsupported command action: %d", result.Action),
			})
			m.viewport.SetContent(m.renderEntries())
			m.resumeFollow()
			return nil
		}
		// ActionNone — just show text.
		if result.Text != "" {
			m.entries = append(m.entries, ChatEntry{
				Kind:    "system",
				Content: result.Text,
			})
			m.viewport.SetContent(m.renderEntries())
			m.resumeFollow()
		}
		return nil
	}
}

// handleCommandResult renders an asynchronous slash-command receipt.
func (m *Model) handleCommandResult(msg CommandResultMsg) {
	if msg.Text != "" {
		m.entries = append(m.entries, ChatEntry{
			Kind:    "system",
			Content: msg.Text,
		})
		m.viewport.SetContent(m.renderEntries())
		m.resumeFollow()
	}
}
