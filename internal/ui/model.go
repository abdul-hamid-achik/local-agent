package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"runtime/debug"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/atotto/clipboard"
	"github.com/charmbracelet/log"

	"github.com/abdul-hamid-achik/local-agent/internal/agent"
	"github.com/abdul-hamid-achik/local-agent/internal/command"
	"github.com/abdul-hamid-achik/local-agent/internal/config"
	"github.com/abdul-hamid-achik/local-agent/internal/db"
	"github.com/abdul-hamid-achik/local-agent/internal/execution"
	"github.com/abdul-hamid-achik/local-agent/internal/goal"
	"github.com/abdul-hamid-achik/local-agent/internal/imageasset"
	"github.com/abdul-hamid-achik/local-agent/internal/llm"
	"github.com/abdul-hamid-achik/local-agent/internal/permission"
	"github.com/abdul-hamid-achik/local-agent/internal/skill"
)

// Model is the BubbleTea model for the chat interface.
type Model struct {
	// UI components
	viewport      viewport.Model
	input         textarea.Model
	spin          spinner.Model
	scramble      ScrambleModel
	styles        Styles
	md            *MarkdownRenderer
	markdownWidth int
	keys          KeyMap

	// State
	state                 State
	overlay               OverlayKind
	overlayParent         OverlayKind
	entries               []ChatEntry
	streamBuf             strings.Builder
	lastStreamPaint       time.Time // throttles per-token re-renders during streaming
	turnStartedAt         time.Time
	lastTurnDuration      time.Duration
	now                   func() time.Time
	width                 int
	height                int
	ready                 bool
	isDark                bool
	reducedMotion         bool
	evalCount             int
	promptTokens          int
	turnEvalTotal         int
	turnPromptTotal       int
	toolsPending          int
	capabilityRoute       *agent.CapabilityRoute
	lastCapabilityRoute   *agent.CapabilityRoute
	continuation          continuationActionState
	bobWorkspaceContext   bobWorkspaceContextState
	inputLines            int
	composerMeasureDigest [32]byte
	composerMeasureW      int
	composerMeasureRows   int
	userScrolledUp        bool

	// Scroll anchor system - prevents jitter during streaming.
	anchorActive bool // true when user wants to stay at bottom

	// Startup
	initializing bool
	startupItems []startupItem
	initCancel   context.CancelFunc

	// Composer-owned completion popup
	completionState           *CompletionState // nil when no overlay
	completionSuppressedDraft string           // exact unchanged draft dismissed with Escape
	completionGeneration      uint64
	completionSearch          func(context.Context, string, string) []Completion
	completionReader          *completionWorkspaceReader

	// Tool display
	toolEntries        []ToolEntry
	toolsCollapsed     bool
	toolHitRegions     []toolHitRegion
	thinkingHitRegions []thinkingHitRegion
	toolCardMgr        ToolCardManager
	diffGeneration     uint64
	// Receipt inspection is an ephemeral affordance for the just-completed
	// turn. It must never point at a stale tool from an earlier no-tool turn.
	turnToolStartIndex          int
	lastTurnToolIndex           int
	receiptInspectActive        bool
	receiptInspectToolIndex     int
	receiptInspectAnchorPaused  bool
	receiptInspectAnchorYOffset int

	// Incremental rendering cache
	cachedEntriesRender      string
	cachedEntryCount         int
	cachedStableCount        int
	cachedPrefixState        entryRenderState
	cachedToolHitRegions     []toolHitRegion
	cachedThinkingHitRegions []thinkingHitRegion
	entryCacheValid          bool

	// Thinking state
	thinkBuf       strings.Builder
	inThinking     bool
	thinkSearchBuf string

	// Terminal title
	doneFlash bool

	// Session persistence
	sessionID                    int64
	activeSessionTitle           string
	executionCursor              int64
	executionLease               *db.ExecutionSessionLease
	sessionStore                 *db.Store
	sessionStateMu               sync.RWMutex
	sessionStateRevision         int64
	sessionStateRevisionKnown    bool
	sessionStatePersistenceDirty bool
	sessionsPickerState          *SessionsPickerState
	sessionLoadToken             uint64
	sessionLoading               bool
	sessionLoadCancel            context.CancelFunc
	pendingSessionSwitch         *pendingSessionSwitch
	sessionListToken             uint64
	sessionListing               bool
	startupResumeSelector        *SessionResumeSelector

	// Paste detection
	pendingPaste *pendingPaste

	// Image attachments are admitted into a private content-addressed store.
	// Provider bytes remain transient; refs are safe to persist and render.
	imageStore          *imageasset.Store
	pendingImages       []pendingImageAttachment
	turnImages          []pendingImageAttachment
	imageAttachToken    uint64
	imageAttachRunning  bool
	imageAttachCancel   context.CancelFunc
	imageAttachFallback string
	imageAttachQueue    []imageFileAttachmentRequest

	// Responsive layout
	forceCompact bool // user-toggled compact mode

	// Mode system
	mode        Mode
	modeConfigs [3]ModeConfig

	// Model management
	modelManager             *llm.ModelManager
	router                   config.ModelRouter
	modelPreferenceStore     ModelPreferenceStore
	modelPickerState         *ModelPickerState
	cloudConsentState        *CloudConsentState
	cloudRestoreAuthorized   string
	modelPullState           *ModelPullState
	modelDetailsState        *OllamaModelDescriptor
	modelPullCancel          context.CancelFunc
	modelPullProgress        <-chan OllamaModelPullProgressMsg
	modelPullRequest         uint64
	modelPullRunning         bool
	modelInventoryRequest    uint64
	settingsPickerState      *SettingsPickerState
	agentPickerState         *AgentPickerState
	modePickerState          *ModePickerState
	runtimeStatusState       *RuntimeStatusState
	planFormState            *PlanFormState
	goalFormState            *GoalForm
	goalInspectorState       *GoalInspector
	goalPlan                 *goalPlanCard
	goalRecoveryState        *GoalRecovery
	goalRecoveryProjection   goalRecoveryProjection
	goalRecoveryLoadToken    uint64
	goalRecoveryLoadRunning  bool
	goalRecoveryLoadScope    goalRecoveryOperationScope
	goalRecoveryApplyToken   uint64
	goalRecoveryApplyRunning bool
	goalRecoveryApplyItemID  string
	standaloneRecovery       *standaloneRecoveryState
	modelPinned              bool

	// Goal Runtime. The host owns continuation, budget, cancellation and
	// persistence; Cortex is an advisory semantic state machine only.
	goalRuntime           *goal.Runtime
	goalAdvisor           GoalAdvisor
	goalOperation         string
	goalOperationToken    uint64
	goalOperationCancel   context.CancelFunc
	goalOperationRunning  bool
	cortexDecision        *cortexDecisionPresentation
	cortexDecisionOp      *cortexDecisionOperation
	cortexDecisionAttempt *cortexDecisionAttempt
	cortexDecisionGen     uint64
	goalTurnID            string
	goalTurnToolCalls     int
	goalTurnSuccesses     int
	goalNeedsEvaluation   bool
	goalPersistenceDirty  bool

	// Logging
	logger *log.Logger

	// Features
	agent               *agent.Agent
	cmdRegistry         *command.Registry
	skillMgr            *skill.Manager
	completer           *Completer
	loadedFile          string
	agentsDir           *config.AgentsDir
	baseLoadedContext   string
	manualLoadedContext string
	manualSkills        []string
	profileSkills       []string

	// Runtime
	program            *tea.Program
	cancel             context.CancelFunc
	commitCancel       context.CancelFunc
	commitRunner       commitEffectRunner
	commitToken        uint64
	commitRunning      bool
	fileOpToken        uint64
	fileLoading        bool
	readScopeOpToken   uint64
	readScopeOpRunning bool
	readScopeOpLabel   string
	readScopeOpDraft   string
	readScopePrompt    *ReadScopePrompt
	// Input crosses a bounded two-phase resume handshake after an undersized
	// terminal becomes supported. Charm does not expose input provenance across
	// its concurrent senders, so quiet windows mitigate reordering while a
	// consumed explicit gesture keeps authority surfaces fail-closed.
	terminalInputResumePhase terminalInputResumePhase
	terminalInputResumeToken uint64
	terminalInputResumeAt    time.Time
	terminalInputTickPending bool
	exportToken              uint64
	exportRunning            bool
	compactingContext        bool
	shuttingDown             bool

	// Display info
	model                     string
	modelList                 []string
	ollamaModels              []OllamaModelDescriptor
	ollamaVersion             string
	localOnly                 bool
	ollamaOffline             bool
	ollamaInventoryAttempted  bool
	pendingOllamaInventory    *OllamaModelInventoryMsg
	ollamaInventoryCommitting bool
	ollamaInventoryCommitID   uint64
	agentProfile              string
	agentList                 []string
	toolCount                 int
	serverCount               int
	numCtx                    int
	approvalPosture           ApprovalPosture
	expertRuntimeSetupFailed  bool

	failedServers []FailedServer
	mcpServers    []MCPServerStatus

	// ICE
	iceEnabled       bool
	iceConversations int
	iceSessionID     string

	// Session token totals
	sessionEvalTotal   int
	sessionPromptTotal int
	sessionTurnCount   int

	// File change tracking
	fileChanges map[string]int // path → number of modifications

	// Tool approval prompt
	pendingApproval    *ToolApprovalMsg
	approvalState      *ApprovalState
	queuedFollowUp     *queuedFollowUp
	turnMessagesBefore []llm.Message
	turnPromptFloor    agent.ContextPromptFloor
	turnPrompt         string
	turnPromptVisible  bool
	turnEntryIndex     int
	turnCheckpointSet  bool
	turnRunContext     context.Context
	turnRunOptions     agent.TurnOptions
	turnLogicalID      string
	turnSegmentID      string
	turnAuthority      Mode
	autoCheckpoints    autoCheckpointSupervisor

	// Prompt history
	promptHistory      []string // all submitted inputs
	historyIndex       int      // -1 = not browsing, 0 = most recent
	historySaved       string   // saved current input when entering history
	clipboardRead      func() (string, error)
	clipboardImageRead func(context.Context) (string, []byte, error)

	// Help overlay viewport (scrollable)
	helpViewport viewport.Model
}

// New creates a new TUI Model.
func New(ag *agent.Agent, cmdReg *command.Registry, skillMgr *skill.Manager, completer *Completer, modelManager *llm.ModelManager, router config.ModelRouter, logger *log.Logger) *Model {
	reducedMotion := reducedMotionRequested()
	ta := textarea.New()
	ta.Placeholder = "Ask, @mention files, or type /help"
	ta.Focus()
	ta.CharLimit = 32 * 1024
	ta.DynamicHeight = true
	ta.MinHeight = 1
	ta.MaxHeight = maxComposerVisibleRows
	// Keep the visible viewport cap separate from admission. Bubbles measures
	// MaxContentHeight in wrapped visual rows, while Local Agent's paste guard
	// owns the logical-line and character limits. A reachable visual-row cap can
	// therefore truncate an otherwise accepted, heavily wrapped paste.
	ta.MaxContentHeight = math.MaxInt
	// Clipboard reads are parent-owned so Ctrl+V produces the same public
	// tea.PasteMsg path as terminal bracketed paste and cannot bypass review.
	ta.KeyMap.Paste.SetEnabled(false)
	ta.SetHeight(1)
	ta.ShowLineNumbers = false
	// A single send marker followed by continuation rails makes multiline
	// drafts read as one composer instead of several submitted messages.
	configureComposerMode(&ta, true, ModeNormal, reducedMotion)

	initialStyles := NewStyles(true)
	s := spinner.New(
		spinner.WithSpinner(spinner.MiniDot),
		spinner.WithStyle(initialStyles.StatusDot),
	)

	return &Model{
		input:                   ta,
		clipboardRead:           clipboard.ReadAll,
		clipboardImageRead:      readClipboardImage,
		spin:                    s,
		scramble:                NewScrambleModel(true),
		styles:                  initialStyles,
		keys:                    DefaultKeyMap(),
		state:                   StateIdle,
		isDark:                  true,
		reducedMotion:           reducedMotion,
		now:                     time.Now,
		inputLines:              1,
		toolsCollapsed:          true,
		initializing:            true,
		approvalPosture:         ApprovalPosturePrompted,
		mode:                    ModeNormal,
		modeConfigs:             DefaultModeConfigs(),
		modelManager:            modelManager,
		router:                  router,
		logger:                  logger,
		agent:                   ag,
		cmdRegistry:             cmdReg,
		skillMgr:                skillMgr,
		completer:               completer,
		completionReader:        newCompletionWorkspaceReader(),
		historyIndex:            -1,
		toolCardMgr:             NewToolCardManager(true),
		lastTurnToolIndex:       -1,
		receiptInspectToolIndex: -1,
		turnEntryIndex:          -1,
		commitRunner:            runCommit,
	}
}

// SetProgram sets the tea.Program reference (must be called before Run).
func (m *Model) SetProgram(p *tea.Program) {
	m.program = p
}

// SetModelPinned preserves an explicit CLI or agent-profile model selection.
// Automatic routing remains available after the user runs /model auto.
func (m *Model) SetModelPinned(pinned bool) {
	m.modelPinned = pinned
}

// SetSessionStore enables private SQLite-backed, lossless session resume.
func (m *Model) SetSessionStore(store *db.Store) {
	m.sessionStore = store
}

// SetStartupSessionResume schedules one validated restore after InitComplete.
// It never starts provider work and shares the interactive picker's tokened
// restore authority.
func (m *Model) SetStartupSessionResume(selector SessionResumeSelector) error {
	if !selector.valid() {
		return fmt.Errorf("invalid startup session resume selector")
	}
	copy := selector
	m.startupResumeSelector = &copy
	return nil
}

// SetInitCancel stores the cancel function for the background init goroutine.
func (m *Model) SetInitCancel(cancel context.CancelFunc) {
	m.initCancel = cancel
}

func (m *Model) beginShutdown() tea.Cmd {
	m.shuttingDown = true
	m.cancelTerminalInputResume()
	m.pendingOllamaInventory = nil
	m.cancelPendingCloudSessionRestore()
	if m.goalOperationCancel != nil {
		m.goalOperationCancel()
	}
	m.fileOpToken++
	m.fileLoading = false
	if m.imageAttachCancel != nil {
		m.imageAttachCancel()
	}
	m.clearImageAttachmentQueue()
	if m.initCancel != nil {
		m.initCancel()
	}
	if m.pendingApproval != nil {
		m.resolvePendingApproval(permission.Cancelled("application is shutting down"))
	}
	m.pendingPaste = nil
	m.clearPendingSessionSwitchSnapshot()
	if m.readScopePrompt != nil {
		releaseReadGrants(m.readScopePrompt.Grants)
		releaseWriteGrants(m.readScopePrompt.WriteGrants)
	}
	m.readScopePrompt = nil
	if m.sessionLoading {
		m.cancelSessionLoadForShutdown()
	}
	if m.sessionListing {
		m.cancelSessionList()
	}
	if m.commitCancel != nil {
		m.commitCancel()
	}
	m.cancelModelPull()
	if m.cancel != nil {
		m.cancel()
	}
	if !m.shutdownReady() {
		return m.startActivityCmd()
	}
	return tea.Quit
}

func (m *Model) shutdownReady() bool {
	return m.cancel == nil && !m.commitRunning && !m.exportRunning && !m.goalOperationRunning &&
		!m.modelPullRunning && !m.sessionLoading && !m.imageAttachRunning && !m.ollamaInventoryCommitting && !m.readScopeOpRunning
}

func (m *Model) appendShutdownQuit(commands *[]tea.Cmd) {
	if m.shuttingDown && m.shutdownReady() {
		*commands = append(*commands, tea.Quit)
	}
}

func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{
		textarea.Blink,
		tea.RequestBackgroundColor,
	}
	if m.needsSpinner() {
		cmds = append(cmds, m.spin.Tick)
	}
	return tea.Batch(cmds...)
}

func (m *Model) Update(msg tea.Msg) (retModel tea.Model, retCmd tea.Cmd) {
	// Never let a single message handler panic take down the whole UI and
	// leave the terminal in a broken state. Recover, log the stack, surface
	// the error in the chat, and keep running.
	defer func() {
		if r := recover(); r != nil {
			if m.logger != nil {
				m.logger.Error("panic recovered in Update", "panic", r, "stack", string(debug.Stack()))
			}
			m.entries = append(m.entries, ChatEntry{
				Kind:    "error",
				Content: fmt.Sprintf("internal error (recovered): %v", r),
			})
			m.viewport.SetContent(m.renderEntries())
			retModel = m
			retCmd = nil
		}
	}()

	var cmds []tea.Cmd
	// Resize and terminal input are produced by independent Bubble Tea sources.
	// Gate every terminal-originated event in one place while the size fallback
	// or explicit resume handshake owns the screen. Charm exposes no physical
	// input timestamp, so the consumed re-arm gesture—not inferred event order—
	// keeps authority surfaces closed. Ctrl+C alone retains its owner-specific
	// graceful-shutdown path below.
	if paused, cmd := m.pauseTerminalOriginatedInput(msg); paused {
		return m, cmd
	}

	switch msg := msg.(type) {
	case tea.BackgroundColorMsg:
		m.handleThemeChange(msg)

	case tea.WindowSizeMsg:
		cmds = m.handleWindowSize(msg, cmds)

	case goalRecoveryLoadResultMsg:
		if cmd := m.handleGoalRecoveryLoadResult(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}

	case goalRecoveryApplyResultMsg:
		if cmd := m.handleGoalRecoveryApplyResult(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}

	case standaloneRecoveryInspectResultMsg:
		if cmd := m.handleStandaloneRecoveryInspect(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}

	case standaloneRecoveryApplyResultMsg:
		if cmd := m.handleStandaloneRecoveryApply(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}

	case ShutdownMsg:
		return m, m.beginShutdown()

	case terminalInputResumeMsg:
		if cmd := m.finishTerminalInputResume(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}

	case tea.KeyPressMsg:
		// During startup, only allow Ctrl+C to quit.
		if m.initializing {
			if key.Matches(msg, m.keys.Quit) {
				return m, m.beginShutdown()
			}
			return m, nil
		}
		// Pending tool approval owns the keyboard before every other overlay.
		// Decisions remain typed so a host failure cannot be reported as a human
		// denial, and session authority stays exact-request scoped.
		if m.pendingApproval != nil {
			resumeActivity := false
			switch {
			case key.Matches(msg, m.keys.Quit):
				m.resolvePendingApproval(permission.Cancelled("application is shutting down"))
				return m, m.beginShutdown()
			case key.Matches(msg, m.keys.Cancel):
				m.resolvePendingApproval(permission.Cancelled("approval cancelled by user"))
				if m.cancel != nil {
					m.cancel()
				}
			case strings.EqualFold(msg.String(), "y"):
				m.resolvePendingApproval(permission.AllowOnce())
				resumeActivity = true
			case strings.EqualFold(msg.String(), "n"):
				m.resolvePendingApproval(permission.Deny())
				resumeActivity = true
			case strings.EqualFold(msg.String(), "s"):
				m.resolvePendingApproval(permission.AllowSession())
				resumeActivity = true
			case key.Matches(msg, m.keys.CompleteSelect):
				m.resolvePendingApproval(m.selectedApprovalResponse())
				resumeActivity = true
			case key.Matches(msg, m.keys.CompleteUp), strings.EqualFold(msg.String(), "k"):
				m.moveApprovalChoice(-1)
			case key.Matches(msg, m.keys.CompleteDown), strings.EqualFold(msg.String(), "j"):
				m.moveApprovalChoice(1)
			case strings.EqualFold(msg.String(), "d"):
				m.toggleApprovalDetails()
			default:
				m.navigateApprovalViewport(msg.String())
			}
			if resumeActivity {
				return m, m.startActivityCmd()
			}
			return m, nil
		}

		// External read-root authorization is a host-owned decision. It precedes
		// every overlay and never falls through to composer or agent shortcuts.
		if m.readScopePrompt != nil {
			switch {
			case key.Matches(msg, m.keys.Quit):
				return m, m.beginShutdown()
			case strings.EqualFold(msg.String(), "y"):
				return m, m.confirmReadScopePrompt()
			case strings.EqualFold(msg.String(), "n"):
				m.resolveReadScopePrompt("denied")
			case key.Matches(msg, m.keys.Cancel):
				m.resolveReadScopePrompt("cancelled")
			}
			return m, nil
		}

		// Switching sessions must settle unsent text and images as one atomic
		// draft. This host-owned decision precedes loading and never falls through
		// to ordinary composer or overlay shortcuts.
		if m.pendingSessionSwitch != nil && m.pendingSessionSwitch.Choice == sessionSwitchUndecided {
			switch {
			case key.Matches(msg, m.keys.Quit):
				m.clearPendingSessionSwitchSnapshot()
				return m, m.beginShutdown()
			case key.Matches(msg, m.keys.Cancel):
				m.clearPendingSessionSwitchSnapshot()
				m.input.Focus()
				m.recalcViewportHeight()
			case strings.EqualFold(msg.String(), "k"):
				return m, m.startPendingSessionSwitch(sessionSwitchKeep)
			case strings.EqualFold(msg.String(), "d"):
				return m, m.startPendingSessionSwitch(sessionSwitchDiscard)
			}
			return m, nil
		}

		// Pending paste intercept: y/n/esc before anything else.
		if m.pendingPaste != nil {
			pending := m.pendingPaste
			switch {
			case key.Matches(msg, m.keys.Quit):
				m.pendingPaste = nil
				m.recalcViewportHeight()
				return m, m.beginShutdown()
			case strings.EqualFold(msg.String(), "y"):
				if pending.PlainFits {
					m.clearCompletionSuppression()
					insertion := pending.Content
					if pending.FencedFits {
						insertion = pending.Fenced
					}
					m.input.InsertString(insertion)
					m.pendingPaste = nil
					m.recalcViewportHeight()
					m.syncInputHeight()
					m.activateCortexDecision()
					return m, m.reflowInputViewport()
				}
			case strings.EqualFold(msg.String(), "n"):
				if pending.PlainFits && pending.FencedFits {
					m.clearCompletionSuppression()
					m.input.InsertString(pending.Content)
					m.pendingPaste = nil
					m.recalcViewportHeight()
					m.syncInputHeight()
					m.activateCortexDecision()
					return m, m.reflowInputViewport()
				}
			case key.Matches(msg, m.keys.Cancel):
				m.pendingPaste = nil
				m.recalcViewportHeight()
				m.activateCortexDecision()
			}
			return m, nil
		}

		// Cortex decisions own keys ahead of busy goal-operation guards. Escape
		// hides only the presentation while an exact answer/refresh continues;
		// it must never be reinterpreted as cancellation of that operation.
		if m.cortexDecisionActive() {
			return m, m.updateCortexDecisionKey(msg)
		}

		// End is the transcript's explicit recovery action whenever the composer
		// is empty or temporarily unavailable. Handle it before owned busy-state
		// guards so the advertised action cannot be swallowed by an in-flight
		// session, file, export, or commit operation.
		if key.Matches(msg, m.keys.JumpLatest) && m.canJumpToLatest() {
			m.cancelReceiptInspection(true)
			m.resumeFollow()
			return m, nil
		}

		// Session restoration replaces the complete agent/UI runtime state. Keep
		// input disabled while the DB read is in flight, and let Escape invalidate
		// the generation so a late result cannot overwrite a newer conversation.
		if m.sessionLoading {
			switch {
			case key.Matches(msg, m.keys.Quit):
				return m, m.beginShutdown()
			case key.Matches(msg, m.keys.Cancel):
				m.cancelSessionLoad()
			}
			return m, nil
		}
		if m.sessionListing {
			switch {
			case key.Matches(msg, m.keys.Quit):
				m.cancelSessionList()
				return m, m.beginShutdown()
			case key.Matches(msg, m.keys.Cancel):
				m.cancelSessionList()
				if m.overlay == OverlaySessionsPicker {
					m.closeSessionsPicker()
				}
			}
			return m, nil
		}
		// Context loads and transcript imports replace prompt authority. Keep
		// input disabled until their tokened result arrives; Escape invalidates
		// a late result without blocking the UI on filesystem cancellation.
		if m.fileLoading {
			switch {
			case key.Matches(msg, m.keys.Quit):
				return m, m.beginShutdown()
			case key.Matches(msg, m.keys.Cancel):
				m.fileOpToken++
				m.fileLoading = false
				m.input.Focus()
				m.entries = append(m.entries, ChatEntry{Kind: "system", Content: "File operation cancelled; any late read result will be ignored."})
				m.invalidateEntryCache()
				m.viewport.SetContent(m.renderEntries())
				m.gotoBottomIfFollowing()
			}
			return m, nil
		}
		if m.imageAttachRunning {
			switch {
			case key.Matches(msg, m.keys.Quit):
				return m, m.beginShutdown()
			case key.Matches(msg, m.keys.Cancel):
				fallback := m.imageAttachFallback
				if m.imageAttachCancel != nil {
					m.imageAttachCancel()
				}
				m.imageAttachToken++
				m.imageAttachRunning = false
				m.imageAttachCancel = nil
				m.imageAttachFallback = ""
				m.clearImageAttachmentQueue()
				m.input.Focus()
				m.entries = append(m.entries, ChatEntry{Kind: "system", Content: "Image attachment cancelled."})
				m.invalidateEntryCache()
				m.viewport.SetContent(m.renderEntries())
				m.gotoBottomIfFollowing()
				m.recalcViewportHeight()
				if fallback != "" && m.composerEditable() {
					return m, m.insertPasteWithReview(fallback)
				}
			}
			return m, nil
		}
		// Read-scope preview and commit are serialized host filesystem work. They
		// are intentionally not cancellable halfway through validation/commit;
		// quit waits for the tokened receipt and every other key is ignored.
		if m.readScopeOpRunning {
			if key.Matches(msg, m.keys.Quit) {
				return m, m.beginShutdown()
			}
			return m, nil
		}
		// Export is an owned filesystem effect. Serialize input until its atomic
		// publication receipt returns so a later turn/commit cannot overlap it.
		if m.exportRunning {
			if key.Matches(msg, m.keys.Quit) {
				return m, m.beginShutdown()
			}
			return m, nil
		}
		// /commit owns a cancellable local-model + git transaction. Do not let a
		// model switch or foreground turn race it; Escape cancels and waits for
		// its tokened receipt, while quit follows the same cancel/join path.
		if m.commitRunning {
			switch {
			case key.Matches(msg, m.keys.Quit):
				return m, m.beginShutdown()
			case key.Matches(msg, m.keys.Cancel):
				if m.commitCancel != nil {
					m.commitCancel()
				}
			}
			return m, nil
		}
		// Installing a verified Ollama snapshot can change execution location and
		// the current model's authority. Do not let a new turn or model switch race
		// that reconciliation. If a refresh is waiting behind an active turn,
		// Escape still cancels the foreground turn so the commit can finish.
		if m.ollamaInventoryCommitting {
			switch {
			case key.Matches(msg, m.keys.Quit):
				return m, m.beginShutdown()
			case key.Matches(msg, m.keys.Cancel):
				if m.cancel != nil {
					m.cancel()
				}
			}
			return m, nil
		}
		if m.goalOperation != "" {
			switch {
			case key.Matches(msg, m.keys.Quit):
				m.cancelGoalOperation("Goal operation cancelled during shutdown.")
				return m, m.beginShutdown()
			case key.Matches(msg, m.keys.Cancel):
				m.cancelGoalOperation("Goal operation cancelled; the goal is paused.")
			case key.Matches(msg, m.keys.CycleMode) && m.goalRuntime != nil:
				// A linked goal always retains AUTO authority. Cycling here changes
				// only the ambient mode used after the goal, so it is safe while a
				// host-owned Cortex/status operation settles and should not feel like
				// a dead keyboard shortcut.
				m.cycleMode()
			}
			return m, nil
		}
		// Quit remains global even while a list/modal owns keyboard focus.
		// Bubbles list quit bindings are deliberately disabled so Ctrl+C must
		// follow the application's graceful cancel/join path here.
		if key.Matches(msg, m.keys.Quit) {
			return m, m.beginShutdown()
		}

		// Handle overlay keys first.
		if m.overlay != OverlayNone {
			// ESC always closes the current overlay.
			if key.Matches(msg, m.keys.Cancel) {
				switch m.overlay {
				case OverlayCompletion:
					m.dismissCompletion()
				case OverlayModelPicker:
					if m.modelPickerState != nil && m.modelPickerState.List.FilterState() != list.Unfiltered {
						var cmd tea.Cmd
						m.modelPickerState.List, cmd = m.modelPickerState.List.Update(msg)
						cmds = append(cmds, cmd)
						return m, tea.Batch(cmds...)
					}
					m.closeModelPicker()
				case OverlayCloudConsent:
					m.closeCloudConsent()
					return m, nil
				case OverlayModelDetails:
					m.closeModelDetails()
					return m, nil
				case OverlayModelPull:
					if m.modelPullState != nil && m.modelPullState.Phase == ModelPullRunning {
						m.cancelModelPull()
						m.modelPullState.Apply(OllamaModelPullProgressMsg{Name: m.modelPullState.Name, Err: errors.New("model download cancelled")})
						return m, nil
					}
					m.closeModelPull()
					return m, nil
				case OverlayPlanForm:
					m.closePlanForm()
				case OverlayGoalForm:
					m.closeGoalForm()
				case OverlaySessionsPicker:
					// If the list is filtering, let ESC clear the filter first.
					if m.sessionsPickerState != nil && m.sessionsPickerState.ready() && m.sessionsPickerState.List.FilterState() != list.Unfiltered {
						var cmd tea.Cmd
						m.sessionsPickerState.List, cmd = m.sessionsPickerState.List.Update(msg)
						cmds = append(cmds, cmd)
						return m, tea.Batch(cmds...)
					}
					m.closeSessionsPicker()
				case OverlaySettings:
					m.closeSettingsPicker()
				case OverlayAgentPicker:
					m.closeAgentPicker()
				case OverlayModePicker:
					m.closeModePicker()
				case OverlayRuntimeStatus:
					m.closeRuntimeStatus()
				case OverlayGoalRecovery:
					if m.goalRecoveryState != nil {
						event, cmd := m.goalRecoveryState.Update(msg)
						cmds = append(cmds, cmd, m.handleGoalRecoveryEvent(event))
					} else {
						m.closeGoalRecovery()
					}
					cmds = append(cmds, tea.ClearScreen)
					return m, tea.Batch(cmds...)
				case OverlayGoalInspector:
					if m.goalInspectorState != nil && m.goalInspectorState.CancelConfirmation() {
						return m, nil
					}
					m.closeGoalInspector()
				case OverlayHelp:
					m.closeHelpOverlay()
				default:
					m.dismissOverlay()
				}
				return m, tea.ClearScreen
			}

			// Help overlay: scroll keys forwarded to helpViewport, ? or q to dismiss.
			if m.overlay == OverlayHelp {
				switch msg.String() {
				case "?", "q":
					m.closeHelpOverlay()
					return m, tea.ClearScreen
				default:
					navigateReadOnlyViewport(&m.helpViewport, msg.String())
				}
				return m, nil
			}

			if m.overlay == OverlayRuntimeStatus {
				switch msg.String() {
				case "q":
					m.closeRuntimeStatus()
					return m, tea.ClearScreen
				default:
					if m.runtimeStatusState != nil {
						navigateReadOnlyViewport(&m.runtimeStatusState.Viewport, msg.String())
					}
				}
				return m, nil
			}

			if m.overlay == OverlaySettings && m.settingsPickerState != nil {
				if key.Matches(msg, m.keys.CompleteSelect) {
					if item := m.settingsPickerState.List.SelectedItem(); item != nil {
						cmds = append(cmds, m.activateSettings(item.(settingsItem).action))
					}
				} else {
					var cmd tea.Cmd
					m.settingsPickerState.List, cmd = m.settingsPickerState.List.Update(msg)
					cmds = append(cmds, cmd)
				}
				return m, tea.Batch(cmds...)
			}

			if m.overlay == OverlayAgentPicker && m.agentPickerState != nil {
				if key.Matches(msg, m.keys.CompleteSelect) {
					if item := m.agentPickerState.List.SelectedItem(); item != nil {
						m.selectAgentProfile(item.(agentItem).name)
					}
				} else {
					var cmd tea.Cmd
					m.agentPickerState.List, cmd = m.agentPickerState.List.Update(msg)
					cmds = append(cmds, cmd)
				}
				return m, tea.Batch(cmds...)
			}

			if m.overlay == OverlayModePicker && m.modePickerState != nil {
				if key.Matches(msg, m.keys.CompleteSelect) {
					if item := m.modePickerState.List.SelectedItem(); item != nil {
						selectedMode := item.(modeItem).mode
						m.closeModePicker()
						m.setMode(selectedMode)
					}
				} else {
					var cmd tea.Cmd
					m.modePickerState.List, cmd = m.modePickerState.List.Update(msg)
					cmds = append(cmds, cmd)
				}
				return m, tea.Batch(cmds...)
			}

			// Model picker overlay: forward keys to list, Enter selects.
			if m.overlay == OverlayModelPicker && m.modelPickerState != nil {
				if m.modelPickerState.List.FilterState() == list.Filtering {
					var cmd tea.Cmd
					m.modelPickerState.List, cmd = m.modelPickerState.List.Update(msg)
					cmds = append(cmds, cmd)
					if !key.Matches(msg, m.keys.CompleteSelect) {
						return m, tea.Batch(cmds...)
					}
				}
				handled := false
				switch {
				case msg.String() == "a":
					cmds = append(cmds, m.openModelPull())
					handled = true
				case msg.String() == "d":
					if descriptor, ok := m.modelPickerState.SelectedDescriptor(); ok {
						m.openModelDetails(descriptor)
						cmds = append(cmds, m.requestOllamaModelDetails(descriptor))
					}
					handled = true
				case msg.String() == "r":
					m.modelPickerState.Notice = "Refreshing Ollama inventory…"
					if !m.reducedMotion {
						cmds = append(cmds, m.modelPickerState.List.StartSpinner())
					}
					cmds = append(cmds, m.refreshOllamaInventory())
					handled = true
				case key.Matches(msg, m.keys.CompleteSelect):
					if descriptor, ok := m.modelPickerState.SelectedDescriptor(); ok && descriptor.Selectable && descriptor.Fit {
						m.selectModel(descriptor.Name)
					} else if reason := m.modelPickerState.SelectedReason(); reason != "" {
						m.modelPickerState.List.Title = "Unavailable · " + reason
					}
					handled = true
				}
				if !handled {
					var cmd tea.Cmd
					m.modelPickerState.List, cmd = m.modelPickerState.List.Update(msg)
					cmds = append(cmds, cmd)
				}
				return m, tea.Batch(cmds...)
			}

			if m.overlay == OverlayCloudConsent && m.cloudConsentState != nil {
				if key.Matches(msg, m.keys.CompleteSelect) {
					if item, ok := m.cloudConsentState.List.SelectedItem().(cloudConsentItem); ok {
						if item.action == cloudConsentAllow {
							cmds = append(cmds, m.confirmCloudModel(m.cloudConsentState.ModelName))
						} else {
							m.closeCloudConsent()
						}
					}
				} else {
					var cmd tea.Cmd
					m.cloudConsentState.List, cmd = m.cloudConsentState.List.Update(msg)
					cmds = append(cmds, cmd)
				}
				return m, tea.Batch(cmds...)
			}

			if m.overlay == OverlayModelPull && m.modelPullState != nil {
				cmds = append(cmds, m.modelPullState.Update(msg))
				return m, tea.Batch(cmds...)
			}

			if m.overlay == OverlayModelDetails {
				return m, nil
			}

			// Composer-owned inline Plan form.
			if m.overlay == OverlayPlanForm && m.planFormState != nil {
				anchor := m.captureInlineFormTranscriptAnchor()
				submitted, cancelled := m.updatePlanForm(msg)
				if cancelled {
					m.closePlanForm()
					return m, nil
				}
				if submitted {
					prompt := m.planFormState.AssemblePrompt()
					m.closePlanForm()
					cmd := m.submitPlanFormPrompt(prompt)
					m.restoreInlineFormTranscriptAnchor(anchor)
					return m, cmd
				}
				m.refreshInlineFormLayout(anchor)
				return m, nil
			}

			if m.overlay == OverlayGoalForm && m.goalFormState != nil {
				anchor := m.captureInlineFormTranscriptAnchor()
				event, cmd := m.goalFormState.Update(msg)
				if cmd != nil {
					cmds = append(cmds, cmd)
				}
				switch event.Action {
				case GoalActionCancel:
					m.closeGoalForm()
				case GoalActionSave:
					cmds = append(cmds, m.applyGoalForm(event))
				}
				m.refreshInlineFormLayout(anchor)
				return m, tea.Batch(cmds...)
			}

			if m.overlay == OverlayGoalRecovery && m.goalRecoveryState != nil {
				event, cmd := m.goalRecoveryState.Update(msg)
				cmds = append(cmds, cmd, m.handleGoalRecoveryEvent(event))
				return m, tea.Batch(cmds...)
			}

			if m.overlay == OverlayGoalInspector && m.goalInspectorState != nil {
				event, cmd := m.goalInspectorState.Update(msg)
				if cmd != nil {
					cmds = append(cmds, cmd)
				}
				if event.ActionID == goalInspectorRecoveryActionID {
					m.openGoalRecovery()
				} else if event.Action != command.ActionNone {
					m.closeGoalInspector()
					cmds = append(cmds, m.handleCommandAction(command.Result{Action: event.Action}))
				}
				return m, tea.Batch(cmds...)
			}

			// Sessions picker overlay: forward keys to list, Enter loads.
			if m.overlay == OverlaySessionsPicker && m.sessionsPickerState != nil {
				if !m.sessionsPickerState.ready() {
					return m, nil
				}
				if m.sessionsPickerState.List.FilterState() == list.Filtering {
					var cmd tea.Cmd
					m.sessionsPickerState.List, cmd = m.sessionsPickerState.List.Update(msg)
					cmds = append(cmds, cmd)
					if !key.Matches(msg, m.keys.CompleteSelect) {
						return m, tea.Batch(cmds...)
					}
				}
				if key.Matches(msg, m.keys.CompleteSelect) {
					if item := m.sessionsPickerState.List.SelectedItem(); item != nil {
						si := item.(sessionItem)
						return m, m.beginSessionSwitch(si.id, si.title)
					}
				} else {
					var cmd tea.Cmd
					m.sessionsPickerState.List, cmd = m.sessionsPickerState.List.Update(msg)
					cmds = append(cmds, cmd)
				}
				return m, tea.Batch(cmds...)
			}

			// Completion overlay: handle navigation and filter keys.
			if m.overlay == OverlayCompletion && m.isCompletionActive() {
				cs := m.completionState
				switch {
				case key.Matches(msg, m.keys.CompleteUp):
					if cs.Index > 0 {
						cs.Index--
						return m, m.refreshCompletionPreview()
					}
					return m, nil
				case key.Matches(msg, m.keys.CompleteDown):
					if cs.Index < len(cs.FilteredItems)-1 {
						cs.Index++
						return m, m.refreshCompletionPreview()
					}
					return m, nil
				case key.Matches(msg, m.keys.CompleteSelect):
					// Enter: if item is a folder, drill into it; otherwise accept
					if cs.Index < len(cs.FilteredItems) && cs.Kind == "attachments" && cs.FilteredItems[cs.Index].Category == "folder" {
						return m, m.drillIntoFolder()
					} else {
						m.acceptCompletion()
					}
				case key.Matches(msg, m.keys.CompleteToggle):
					// Tab toggles multi-select
					m.toggleCompletionSelection()
				default:
					// Check for backspace on empty filter => go up directory for @ kind
					if msg.Code == tea.KeyBackspace && cs.Filter.Value() == "" && cs.Kind == "attachments" && cs.CurrentPath != "" {
						return m, m.drillUpFolder()
					}

					// Forward all other keys to filter input
					oldFilter := cs.Filter.Value()
					var cmd tea.Cmd
					cs.Filter, cmd = cs.Filter.Update(msg)
					if cs.Kind == "command" && strings.ContainsAny(cs.Filter.Value(), " \t\n") {
						// Once arguments begin, completion has done its job. Return the
						// entire draft to the composer so Enter executes the command
						// instead of selecting a suggestion and discarding arguments.
						draft, cursorRune := m.completionDraftAndCursor()
						m.closeCompletion()
						m.setComposerDraftAtRune(draft, cursorRune)
						m.completionSuppressedDraft = draft
						return m, cmd
					}

					// Re-filter if text changed
					if cs.Filter.Value() != oldFilter {
						if cs.CommandPrefix != "" {
							cs.BaseItems = m.completer.CompleteStatic(cs.CommandPrefix + cs.Filter.Value())
							cs.AllItems = append([]Completion(nil), cs.BaseItems...)
							cs.FilteredItems = append([]Completion(nil), cs.BaseItems...)
						} else {
							cs.FilteredItems = FilterCompletions(cs.AllItems, cs.Filter.Value())
						}
						cs.Index = 0
						previewCmd := m.refreshCompletionPreview()

						if cs.Kind == "attachments" {
							searchCmd := m.scheduleCompletionSearch(
								cs.Filter.Value(),
								cs.CurrentPath,
								cs.Filter.Value() != "",
							)
							return m, tea.Batch(cmd, previewCmd, searchCmd)
						}
						return m, tea.Batch(cmd, previewCmd)
					}
					return m, cmd
				}
				return m, nil
			}

			// Unknown overlay — swallow.
			return m, nil
		}

		// Transcript paging is parent-owned and must never fall through to the
		// composer. PgUp/PgDn always page the conversation. Ctrl+U/Ctrl+D retain
		// their standard textarea editing behavior while a draft is present, and
		// act as half-page transcript shortcuts only when the composer is empty or
		// unavailable.
		if m.transcriptOwnsScrollKey(msg) {
			return m, m.updateTranscriptScroll(msg)
		}
		if msg.String() == "ctrl+v" && m.composerEditable() {
			return m, m.readClipboardPaste()
		}

		switch {
		case key.Matches(msg, m.keys.Quit):
			return m, m.beginShutdown()

		case key.Matches(msg, m.keys.Cancel):
			// A visible queued follow-up owns the first Escape. Clearing the queue
			// must not also cancel the active run; a later Escape still reaches the
			// ordinary cancellation path below.
			if m.clearQueuedFollowUp() {
				return m, nil
			}
			if (m.state == StateStreaming || m.state == StateWaiting) && m.cancel != nil {
				m.cancel()
			}

		case key.Matches(msg, m.keys.Help):
			// Only toggle help when input is empty.
			if m.state == StateIdle && strings.TrimSpace(m.input.Value()) == "" {
				m.overlayParent = OverlayNone
				m.overlay = OverlayHelp
				m.initHelpViewport()
				m.input.Blur()
				return m, nil
			}

		case key.Matches(msg, m.keys.ToggleTools):
			// Batch-toggle all tools when input is empty and idle.
			if m.state == StateIdle && strings.TrimSpace(m.input.Value()) == "" {
				m.cancelReceiptInspection(true)
				m.toolsCollapsed = !m.toolsCollapsed
				for i := range m.toolEntries {
					m.toolEntries[i].Collapsed = m.toolsCollapsed
				}
				m.invalidateEntryCache()
				m.viewport.SetContent(m.renderEntries())
				m.gotoBottomIfFollowing()
				return m, nil
			}

		case key.Matches(msg, m.keys.ToggleFocusedTool):
			// Toggle last tool entry only when input is empty.
			if m.state == StateIdle && strings.TrimSpace(m.input.Value()) == "" {
				if len(m.toolEntries) > 0 {
					target := len(m.toolEntries) - 1
					if _, ok := m.inspectableToolReceiptAction(); ok {
						target = m.lastTurnToolIndex
					}
					m.toggleToolReceipt(target, true)
				}
				return m, nil
			}

		case key.Matches(msg, m.keys.CompactToggle):
			if m.state == StateIdle {
				m.cancelReceiptInspection(true)
				m.forceCompact = !m.forceCompact
				m.invalidateEntryCache()
				m.viewport.SetContent(m.renderEntries())
				return m, nil
			}

		case key.Matches(msg, m.keys.ToggleThinking):
			// Completed reasoning remains inspectable while the next turn runs. A
			// non-empty draft retains ownership of every control key, and a live
			// Thinking row is never part of this batch operation.
			if m.input.Value() != "" {
				// Bubbles treats Ctrl+T as transpose. This application-level
				// disclosure shortcut must never silently rewrite a draft.
				return m, nil
			}
			m.cancelReceiptInspection(true)
			m.toggleAllThinkingReceipts()
			return m, nil

		case key.Matches(msg, m.keys.ExternalEditor):
			if m.state == StateIdle {
				return m, m.openExternalEditor()
			}

		case key.Matches(msg, m.keys.CopyLast):
			if m.state == StateIdle && strings.TrimSpace(m.input.Value()) == "" {
				if content := m.lastAssistantContent(); content != "" {
					return m, m.copyToClipboard(content)
				}
			}

		case key.Matches(msg, m.keys.ClearView):
			if m.state == StateIdle {
				m.cancelReceiptInspection(true)
				m.viewport.SetContent(m.renderEntries())
				m.resumeFollow()
				return m, nil
			}

		case key.Matches(msg, m.keys.NewConvo):
			if m.state == StateIdle {
				if m.blockSessionReplacementForHeldFollowUp("starting a new conversation") {
					return m, nil
				}
				m.agent.ClearHistory()
				m.entries = nil
				m.toolEntries = nil
				m.resetConversationSession()
				m.invalidateEntryCache()
				m.entries = append(m.entries, ChatEntry{
					Kind:    "system",
					Content: "New conversation started.",
				})
				m.viewport.SetContent(m.renderEntries())
				m.resumeFollow()
				return m, nil
			}

		case key.Matches(msg, m.keys.CycleMode):
			if m.state == StateIdle {
				m.cycleMode()
				return m, nil
			}

		case key.Matches(msg, m.keys.ModelPicker):
			if m.state == StateIdle {
				m.overlayParent = OverlayNone
				m.openModelPicker()
				return m, nil
			}

		case key.Matches(msg, m.keys.SettingsPicker):
			if m.state == StateIdle {
				m.openSettingsPicker()
				return m, nil
			}

		case key.Matches(msg, m.keys.NewLine):
			// Insert newline in textarea (shift+enter).
			if m.composerEditable() {
				m.clearCompletionSuppression()
				m.input.InsertString("\n")
				m.syncInputHeight()
				return m, nil
			}

		case key.Matches(msg, m.keys.Send):
			if m.state == StateIdle {
				return m, m.submitInput()
			}
			if m.composerEditable() {
				return m, m.queueComposerFollowUp()
			}

		case key.Matches(msg, m.keys.Complete):
			// Tab key for autocomplete
			if m.composerEditable() && m.completer != nil && !m.isCompletionActive() {
				// Explicit completion always overrides an earlier Escape dismissal.
				m.completionSuppressedDraft = ""
				return m, m.triggerCompletion(m.input.Value())
			}

		case key.Matches(msg, m.keys.HistoryUp):
			// During an active turn, Up edits the one visible queued follow-up
			// before it can be mistaken for ordinary prompt-history navigation.
			if m.editQueuedFollowUp() {
				return m, nil
			}
			if m.state == StateIdle && m.overlay == OverlayNone {
				if strings.TrimSpace(m.input.Value()) == "" || m.historyIndex != -1 {
					if m.navigateHistory(-1) {
						return m, nil
					}
				}
			}

		case key.Matches(msg, m.keys.HistoryDown):
			if m.state == StateIdle && m.overlay == OverlayNone {
				if m.historyIndex != -1 {
					if m.navigateHistory(1) {
						return m, nil
					}
				}
			}
		}

	case StreamTextMsg:
		cmds = m.handleStreamText(msg, cmds)

	case StreamThinkingMsg:
		cmds = m.handleStreamThinking(msg, cmds)

	case StreamDoneMsg:
		m.handleStreamDone(msg)

	case ContextCompactedMsg:
		m.handleContextCompacted(msg)

	case ContextCompactionStartedMsg:
		cmds = m.handleContextCompactionStarted(msg, cmds)

	case ContextCompactionFinishedMsg:
		m.handleContextCompactionFinished(msg)

	case ToolCallStartMsg:
		cmds = m.handleToolCallStart(msg, cmds)

	case ExpertProgressMsg:
		m.handleExpertProgress(msg)

	case PlanFormCompletedMsg:
		return m, m.submitPlanFormPrompt(msg.Prompt)

	case goalOpenResultMsg:
		return m, m.handleGoalOpenResult(msg)

	case goalStatusResultMsg:
		return m, m.handleGoalStatusResult(msg)

	case cortexDecisionAnswerResultMsg:
		return m, m.handleCortexDecisionAnswerResult(msg)

	case ToolCallResultMsg:
		cmds = m.handleToolCallResult(msg, cmds)

	case diffBuildResultMsg:
		matched := false
		for i := len(m.toolEntries) - 1; i >= 0; i-- {
			entry := &m.toolEntries[i]
			if !toolCallMatches(msg.ToolID, msg.ToolName, entry.ID, entry.Name) ||
				!entry.DiffPending || entry.DiffGeneration != msg.Generation {
				continue
			}
			entry.DiffPending = false
			entry.DiffGeneration = 0
			if msg.Available {
				// The live card and persisted session share one explicit bound. This
				// keeps every retained row inspectable while oversized patches end in
				// a typed omission marker instead of a renderer-only dead end.
				entry.DiffLines = persistDiffLines(msg.Lines)
			}
			matched = true
			break
		}
		if !matched {
			break
		}
		m.invalidateEntryCache()
		m.viewport.SetContent(m.renderEntries())
		m.gotoBottomIfFollowing()

	case SystemMessageMsg:
		m.handleSystemMessage(msg)

	case CapabilityRouteMsg:
		if m.state == StateWaiting || m.state == StateStreaming {
			m.capabilityRoute = nil
			route := sanitizeCapabilityRoute(msg.Route)
			if capabilityRouteRenderable(route) {
				m.capabilityRoute = &route
				last := route
				m.lastCapabilityRoute = &last
			}
		}

	case ContinuationActionMsg:
		m.handleContinuationAction(msg)

	case BobWorkspaceContextMsg:
		m.handleBobWorkspaceContext(msg)

	case ErrorMsg:
		m.handleErrorMsg(msg)

	case AgentDoneMsg:
		cmds = m.handleAgentDone(msg, cmds)

	case OllamaModelPullRequestedMsg:
		cmds = append(cmds, m.startModelPull(msg.Name))
		if m.modelPullState != nil && !m.reducedMotion {
			cmds = append(cmds, m.modelPullState.Spinner.Tick)
		}

	case OllamaModelPullCancelRequestedMsg:
		m.cancelModelPull()
		if m.modelPullState != nil {
			m.modelPullState.Apply(OllamaModelPullProgressMsg{Name: msg.Name, Err: errors.New("model download cancelled")})
		}

	case OllamaModelPullProgressMsg:
		if msg.RequestID != m.modelPullRequest {
			break
		}
		if m.modelPullState == nil {
			if msg.Done || msg.Err != nil {
				m.modelPullRunning = false
				m.cancelModelPull()
				m.appendShutdownQuit(&cmds)
			}
			break
		}
		cmds = append(cmds, m.modelPullState.Apply(msg))
		if msg.Done && msg.Err == nil {
			m.modelPullRunning = false
			m.cancelModelPull()
			cmds = append(cmds, m.refreshOllamaInventory())
			m.appendShutdownQuit(&cmds)
		} else if msg.Err != nil {
			m.modelPullRunning = false
			m.cancelModelPull()
			m.appendShutdownQuit(&cmds)
		} else if m.modelPullProgress != nil {
			cmds = append(cmds, waitModelPullProgress(m.modelPullProgress))
		}

	case OllamaModelInventoryMsg:
		if msg.RequestID != m.modelInventoryRequest {
			break
		}
		if msg.Err != nil || m.modelManager == nil {
			m.applyOllamaInventory(msg)
			break
		}
		if m.ollamaInventoryCommitting {
			copy := msg
			m.pendingOllamaInventory = &copy
			break
		}
		cmds = append(cmds, m.commitOllamaInventory(msg))

	case ollamaModelInventoryCommittedMsg:
		if msg.Inventory.RequestID != m.ollamaInventoryCommitID {
			break
		}
		m.ollamaInventoryCommitting = false
		if !m.shuttingDown && msg.Inventory.RequestID == m.modelInventoryRequest {
			m.applyOllamaInventory(msg.Inventory)
			switch {
			case msg.SelectionChanged:
				m.setCurrentModelProjection(msg.SelectedModel)
				if msg.SelectedModel != "" {
					m.modelPinned = false
				}
				for index := range m.ollamaModels {
					m.ollamaModels[index].Current = config.CanonicalModelName(m.ollamaModels[index].Name) == config.CanonicalModelName(msg.SelectedModel) && msg.SelectedModel != ""
				}
				detail := msg.SelectionReason
				if msg.SelectionErr != nil {
					detail += fmt.Sprintf("; reconciliation warning: %v", msg.SelectionErr)
				}
				if msg.SelectedModel != "" {
					m.appendGoalSystem(fmt.Sprintf("Ollama inventory changed · %s · resumed automatic routing on local model %s", detail, msg.SelectedModel))
				} else {
					m.appendGoalError(fmt.Sprintf("Ollama inventory changed · %s. Model %q was cleared; select a verified model before the next turn.", detail, msg.PreviousModel))
				}
			case msg.RecoveryErr != nil:
				detail := fmt.Sprintf("Ollama inventory recovered, but model %q could not be activated: %v", m.model, msg.RecoveryErr)
				m.appendGoalError(detail)
				if m.modelPickerState != nil {
					m.modelPickerState.Notice = detail
				}
			case msg.RecoveredModel != "":
				m.setCurrentModelProjection(msg.RecoveredModel)
				m.appendGoalSystem(fmt.Sprintf("Ollama reconnected · %s ready", msg.RecoveredModel))
			}
		}
		if !m.shuttingDown && m.pendingOllamaInventory != nil {
			pending := *m.pendingOllamaInventory
			m.pendingOllamaInventory = nil
			if pending.RequestID == m.modelInventoryRequest {
				cmds = append(cmds, m.commitOllamaInventory(pending))
			}
		}
		m.appendShutdownQuit(&cmds)

	case OllamaModelDetailsResultMsg:
		if m.modelDetailsState != nil && config.CanonicalModelName(m.modelDetailsState.Name) == config.CanonicalModelName(msg.Model.Name) {
			if msg.Err != nil {
				m.modelDetailsState.Reason = "Details unavailable: " + msg.Err.Error()
			} else {
				copy := msg.Model
				m.modelDetailsState = &copy
			}
		}

	case StartupStatusMsg:
		if msg.ID == "ollama" {
			m.ollamaOffline = msg.Status == "failed"
		}
		found := false
		for i, item := range m.startupItems {
			if item.ID == msg.ID {
				m.startupItems[i].Status = msg.Status
				m.startupItems[i].Detail = msg.Detail
				found = true
				break
			}
		}
		if !found {
			m.startupItems = append(m.startupItems, startupItem(msg))
		}
		if m.ready {
			m.viewport.SetContent(m.renderEntries())
		}

	case InitCompleteMsg:
		m.setCurrentModelProjection(msg.Model)
		m.ollamaModels = append([]OllamaModelDescriptor(nil), msg.OllamaModels...)
		m.modelList = append([]string(nil), msg.ModelList...)
		if selectable := manuallySelectableOllamaModels(m.ollamaModels); len(selectable) > 0 {
			m.modelList = selectable
		}
		m.ollamaVersion = msg.OllamaVersion
		m.localOnly = msg.LocalOnly
		m.ollamaInventoryAttempted = msg.OllamaInventoryAttempted
		m.setActiveProfileMetadata(msg.AgentProfile)
		m.agentList = msg.AgentList
		m.toolCount = msg.ToolCount
		m.serverCount = msg.ServerCount
		m.numCtx = msg.NumCtx
		m.syncEffectiveContext(false)
		m.applyInitialMCPStatus(msg.MCPServers, msg.FailedServers)
		m.iceEnabled = msg.ICEEnabled
		m.iceConversations = msg.ICEConversations
		m.iceSessionID = msg.ICESessionID

		if m.completer != nil {
			m.completer.UpdateModels(m.modelList)
			m.completer.UpdateAgents(msg.AgentList)
		}

		if len(m.failedServers) > 0 {
			parts := make([]string, 0, len(m.failedServers))
			for _, fs := range m.failedServers {
				parts = append(parts, fs.Name)
			}
			m.entries = append(m.entries, ChatEntry{
				Kind:    "system",
				Content: "MCP unavailable: " + strings.Join(parts, ", ") + ". Open Runtime status for recovery guidance.",
			})
		}

		m.initializing = false
		m.startupItems = nil

		// Startup and the interactive composer reserve different footer geometry.
		// Reflow immediately so the first usable frame does not keep a stale blank
		// row until the next resize or input event.
		m.recalcViewportHeight()
		m.viewport.SetContent(m.renderEntries())
		if m.startupResumeSelector != nil {
			selector := *m.startupResumeSelector
			m.startupResumeSelector = nil
			if !m.shuttingDown {
				cmds = append(cmds, m.requestSessionRestore(selector))
			}
		}

	case MCPStatusSnapshotMsg:
		m.applyMCPStatusSnapshot(msg.Servers)

	case ReadScopePreviewResultMsg:
		m.handleReadScopePreviewResult(msg)
		m.appendShutdownQuit(&cmds)

	case PromptPathPreflightResultMsg:
		if cmd := m.handlePromptPathPreflightResult(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
		m.appendShutdownQuit(&cmds)

	case CommandResultMsg:
		m.handleCommandResult(msg)

	case ImageAttachmentResultMsg:
		if cmd := m.handleImageAttachmentResult(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
		m.appendShutdownQuit(&cmds)

	case ReadScopeResultMsg:
		if cmd := m.handleReadScopeResult(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
		m.appendShutdownQuit(&cmds)

	case ContextLoadResultMsg:
		if !m.fileLoading || msg.Token != m.fileOpToken {
			break
		}
		m.fileLoading = false
		if !m.shuttingDown {
			m.input.Focus()
		}
		if msg.Err != nil {
			m.entries = append(m.entries, ChatEntry{Kind: "error", Content: fmt.Sprintf("Load failed: %v", msg.Err)})
		} else {
			m.loadedFile = msg.Path
			m.manualLoadedContext = msg.Data
			m.syncLoadedContext()
			m.entries = append(m.entries, ChatEntry{Kind: "system", Content: fmt.Sprintf("Loaded context: %s (%d bytes)", msg.Path, len(msg.Data))})
		}
		m.invalidateEntryCache()
		m.viewport.SetContent(m.renderEntries())
		m.gotoBottomIfFollowing()

	case ImportResultMsg:
		m.handleImportResult(msg)

	case ExportResultMsg:
		cmds = m.handleExportResult(msg, cmds)

	case CompletionDebounceTickMsg:
		if m.isCompletionActive() &&
			m.completionState.Generation == msg.Generation &&
			m.completionState.DebounceTag == msg.Tag {
			return m, m.beginCompletionSearch(msg.Generation, msg.Tag, msg.Query, msg.Path)
		}

	case CompletionSearchResultMsg:
		if m.isCompletionActive() &&
			m.completionState.Generation == msg.Generation &&
			m.completionState.DebounceTag == msg.Tag {
			anchor := m.captureCompletionTranscriptAnchor()
			cs := m.completionState
			cs.Searching = false
			cs.SearchCancel = nil
			replaceCompletionItems(cs, mergeCompletionItems(cs.BaseItems, msg.Results))
			cmds = append(cmds, m.refreshCompletionPreview())
			m.recalcViewportHeight()
			m.restoreCompletionTranscriptAnchor(anchor)
		}

	case completionPreviewResultMsg:
		if m.isCompletionActive() &&
			m.completionState.Kind == "attachments" &&
			m.completionState.Generation == msg.Generation &&
			m.completionState.PreviewToken == msg.Token {
			anchor := m.captureCompletionTranscriptAnchor()
			m.completionState.PreviewCancel = nil
			m.completionState.Preview = msg.Preview
			m.recalcViewportHeight()
			m.restoreCompletionTranscriptAnchor(anchor)
		}

	case ToolApprovalMsg:
		if m.shuttingDown {
			// A callback may cross the shutdown boundary after the active turn has
			// already been cancelled. Never reopen interactive authority while the
			// host is joining receipts, and never mislabel host cancellation as a
			// human denial. Production response channels are buffered; the
			// non-blocking send also keeps malformed or already-settled adapters from
			// freezing the Bubble Tea parent during shutdown.
			if msg.Response != nil {
				select {
				case msg.Response <- permission.Cancelled("application is shutting down"):
				default:
				}
			}
			break
		}
		if err := m.openApproval(msg); err != nil {
			if msg.Response != nil {
				msg.Response <- permission.Refuse("approval_preview_unavailable", err.Error())
			}
			m.entries = append(m.entries, ChatEntry{
				Kind:    "error",
				Content: "Approval preview unavailable: " + err.Error(),
			})
			m.invalidateRenderedCache()
			m.viewport.SetContent(m.renderEntries())
			m.gotoBottomIfFollowing()
		}

	case CommitResultMsg:
		if !m.commitRunning || msg.Token != m.commitToken {
			break
		}
		m.commitRunning = false
		if m.commitCancel != nil {
			m.commitCancel()
			m.commitCancel = nil
		}
		if !m.shuttingDown {
			m.input.Focus()
		}
		if msg.Err != nil {
			m.entries = append(m.entries, ChatEntry{
				Kind:    "error",
				Content: fmt.Sprintf("Commit failed: %v", msg.Err),
			})
		} else {
			m.entries = append(m.entries, ChatEntry{
				Kind:    "system",
				Content: fmt.Sprintf("Committed with message:\n%s", msg.Message),
			})
		}
		m.invalidateEntryCache()
		m.viewport.SetContent(m.renderEntries())
		m.gotoBottomIfFollowing()
		m.appendShutdownQuit(&cmds)

	case editorReturnMsg:
		m.clearCompletionSuppression()
		m.input.SetValue(msg.Content)
		m.input.CursorEnd()
		_ = m.reflowInputViewport()
		m.input.Focus()

	case DoneFlashExpiredMsg:
		m.doneFlash = false

	case SessionListMsg:
		m.handleSessionList(msg)

	case SessionLoadedMsg:
		cmds = append(cmds, m.handleSessionLoadedReceipt(msg))
		m.appendShutdownQuit(&cmds)

	case tea.MouseWheelMsg:
		return m, m.handleMouseWheel(msg)

	case tea.MouseClickMsg:
		if cmd, handled := m.handleMouseClickMsg(msg); handled {
			return m, cmd
		}

	case tea.PasteMsg:
		if cmd, handled := m.handlePasteMsg(msg); handled {
			return m, cmd
		}

	case ClipboardImagePasteMsg:
		if cmd, handled := m.handleClipboardImagePaste(msg); handled {
			return m, cmd
		}
	}

	// Waiting owns the scramble clock. Once streaming starts (or the turn
	// finishes), the next queued tick is ignored and the chain terminates.
	if _, ok := msg.(ScrambleTickMsg); ok && m.needsScramble() {
		var cmd tea.Cmd
		m.scramble, cmd = m.scramble.Update(msg)
		cmds = append(cmds, cmd)
	}

	// The parent owns one Bubbles spinner clock for startup, streaming, tools,
	// and owned operations. Static idle/overlay views schedule no repaint loop.
	if _, ok := msg.(spinner.TickMsg); ok && m.needsSpinner() {
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		cmds = append(cmds, cmd)
		if m.toolsPending > 0 && m.ready {
			// The running tool card renders outside the cached stable prefix, so
			// animating it never invalidates or re-renders the whole transcript.
			m.viewport.SetContent(m.renderEntries())
			m.gotoBottomIfFollowing()
		}
	}

	// Visible Charm children own their non-key lifecycle messages (cursor
	// blinks, list filter results, spinner ticks, and progress frames). Key
	// presses stay in the explicit parent branches above so authority-changing
	// actions cannot be hidden inside presentation components.
	if _, isKey := msg.(tea.KeyPressMsg); !isKey && m.overlay != OverlayNone {
		if cmd := m.updateActiveOverlayMessage(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}

	// Update sub-components.
	if m.composerEditable() {
		hadOverflowCue := m.renderComposerOverflowCue() != ""
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		cmds = append(cmds, cmd)

		// Auto-grow textarea based on content.
		m.syncInputHeight()
		if hadOverflowCue != (m.renderComposerOverflowCue() != "") {
			m.recalcViewportHeight()
		}

		// Auto-trigger completion when user types /, @, or #
		newInput := m.input.Value()
		suppressed := m.completionSuppressedDraft != "" && newInput == m.completionSuppressedDraft
		if m.completionSuppressedDraft != "" && !suppressed {
			// Suppression is tied to one exact draft. The first edit restores normal
			// automatic discovery, including reopening for the edited prefix.
			m.completionSuppressedDraft = ""
		}
		if m.completer != nil && newInput != "" && !m.isCompletionActive() && !suppressed {
			cmds = append(cmds, m.triggerCompletion(newInput))
		}
	}

	// A transcript key that reaches this point belongs to the composer (the
	// non-empty Ctrl+U/Ctrl+D case). Do not let the transcript consume it too.
	keyMsg, isKey := msg.(tea.KeyPressMsg)
	composerOwnedScrollKey := isKey && m.transcriptScrollKey(keyMsg)
	if !composerOwnedScrollKey {
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		cmds = append(cmds, cmd)
	}
	m.checkAutoScroll()

	return m, tea.Batch(cmds...)
}

func (m *Model) updateActiveOverlayMessage(msg tea.Msg) tea.Cmd {
	switch m.overlay {
	case OverlayCompletion:
		if m.completionState != nil {
			var cmd tea.Cmd
			m.completionState.Filter, cmd = m.completionState.Filter.Update(msg)
			return cmd
		}
	case OverlaySettings:
		if m.settingsPickerState != nil {
			var cmd tea.Cmd
			m.settingsPickerState.List, cmd = m.settingsPickerState.List.Update(msg)
			return cmd
		}
	case OverlayAgentPicker:
		if m.agentPickerState != nil {
			var cmd tea.Cmd
			m.agentPickerState.List, cmd = m.agentPickerState.List.Update(msg)
			return cmd
		}
	case OverlayModePicker:
		if m.modePickerState != nil {
			var cmd tea.Cmd
			m.modePickerState.List, cmd = m.modePickerState.List.Update(msg)
			return cmd
		}
	case OverlayModelPicker:
		if m.modelPickerState != nil {
			var cmd tea.Cmd
			m.modelPickerState.List, cmd = m.modelPickerState.List.Update(msg)
			return cmd
		}
	case OverlayCloudConsent:
		if m.cloudConsentState != nil {
			var cmd tea.Cmd
			m.cloudConsentState.List, cmd = m.cloudConsentState.List.Update(msg)
			return cmd
		}
	case OverlaySessionsPicker:
		if m.sessionsPickerState != nil && m.sessionsPickerState.ready() {
			var cmd tea.Cmd
			m.sessionsPickerState.List, cmd = m.sessionsPickerState.List.Update(msg)
			return cmd
		}
	case OverlayModelPull:
		if m.modelPullState != nil {
			return m.modelPullState.Update(msg)
		}
	case OverlayGoalForm:
		if m.goalFormState != nil {
			_, cmd := m.goalFormState.Update(msg)
			return cmd
		}
	case OverlayGoalRecovery:
		if m.goalRecoveryState != nil {
			_, cmd := m.goalRecoveryState.Update(msg)
			return cmd
		}
	case OverlayPlanForm:
		if m.planFormState != nil && m.planFormState.ActiveField >= 0 && m.planFormState.ActiveField < len(m.planFormState.Fields) {
			field := &m.planFormState.Fields[m.planFormState.ActiveField]
			if field.Kind == "text" {
				var cmd tea.Cmd
				field.Input, cmd = field.Input.Update(msg)
				return cmd
			}
		}
	}
	return nil
}

func unresolvedExecutionWarning(states []execution.State, goalOwned bool) string {
	for _, state := range states {
		toolName := state.Identity.ToolName
		if toolName == "" {
			toolName = "unknown tool"
		}
		switch {
		case state.Latest.Type == execution.EventOutcomeUnknown:
			return fmt.Sprintf(
				"Recovery blocked: %s has a durable outcome-unknown receipt. Use /recover to inspect and record exact evidence; automatic continuation is disabled.",
				toolName,
			)
		case state.Latest.Type == execution.EventStarted && state.Identity.EffectClass != execution.EffectReadOnly:
			return fmt.Sprintf(
				"Recovery blocked: %s has a durable dispatch marker but no terminal receipt. Its outcome is unknown; use /recover to inspect and record exact evidence.",
				toolName,
			)
		case (state.Latest.Type == execution.EventCompleted || state.Latest.Type == execution.EventFailed) &&
			state.Identity.EffectClass != execution.EffectReadOnly:
			if goalOwned {
				return fmt.Sprintf(
					"Recovery blocked: %s %s after the last saved transcript. Continuation is disabled so the effect cannot be repeated; use the goal recovery inspector to reconcile this goal-owned session.",
					toolName, state.Latest.Type,
				)
			}
			return fmt.Sprintf(
				"Recovery blocked: %s %s after the last saved transcript. Continuation is disabled so the effect cannot be repeated; inspect the workspace, then close this session and run `local-agent session repair %d`.",
				toolName, state.Latest.Type, state.Identity.SessionID,
			)
		}
	}
	return ""
}

func standaloneRecoveryTarget(states []execution.State, snapshotCursor int64) *agent.UnresolvedExecutionError {
	for _, state := range states {
		if state.Latest.Type != execution.EventOutcomeUnknown &&
			(state.Latest.Type != execution.EventStarted || state.Identity.EffectClass == execution.EffectReadOnly) {
			continue
		}
		return &agent.UnresolvedExecutionError{
			SessionID: state.Identity.SessionID, WorkspaceID: state.Identity.WorkspaceID,
			SnapshotCursor: snapshotCursor, TurnID: state.Identity.TurnID,
			ExecutionID: state.Identity.ExecutionID, ToolName: state.Identity.ToolName,
			EventType: state.Latest.Type,
			Cause:     errors.New("durable execution outcome requires explicit reconciliation"),
		}
	}
	return nil
}

func agentTextareaStyles(isDark bool) textarea.Styles {
	return agentTextareaStylesForMode(isDark, ModeNormal)
}

// agentTextareaStylesForMode keeps the composer's focus treatment semantic:
// NORMAL is a quiet neutral rail, PLAN uses Nord purple, and AUTO uses the
// success green already shared by completed work. outputSemanticPalette also
// preserves NO_COLOR behavior.
func agentTextareaStylesForMode(isDark bool, mode Mode) textarea.Styles {
	styles := textarea.DefaultStyles(isDark)
	palette := outputSemanticPalette(isDark)
	// Dim is the quiet neutral token that still meets text contrast. Border is
	// deliberately softer and made the NORMAL send marker hard to read on light
	// terminals.
	promptColor := palette.Dim
	cursorColor := palette.Accent
	switch mode {
	case ModePlan:
		promptColor = palette.Special
		cursorColor = palette.Special
	case ModeAuto:
		promptColor = palette.Success
		cursorColor = palette.Success
	}
	styles.Focused = textarea.StyleState{
		Base:        lipgloss.NewStyle(),
		Text:        lipgloss.NewStyle().Foreground(palette.Text),
		CursorLine:  lipgloss.NewStyle(),
		Placeholder: lipgloss.NewStyle().Foreground(palette.Dim),
		Prompt:      lipgloss.NewStyle().Foreground(promptColor).Bold(mode != ModeNormal),
	}
	styles.Blurred = textarea.StyleState{
		Base:        lipgloss.NewStyle(),
		Text:        lipgloss.NewStyle().Foreground(palette.Muted),
		CursorLine:  lipgloss.NewStyle(),
		Placeholder: lipgloss.NewStyle().Foreground(palette.Dim),
		Prompt:      lipgloss.NewStyle().Foreground(palette.Dim),
	}
	styles.Cursor.Color = cursorColor
	return styles
}

func configureComposerMode(input *textarea.Model, isDark bool, mode Mode, reducedMotion ...bool) {
	styles := agentTextareaStylesForMode(isDark, mode)
	styles.Cursor.Blink = len(reducedMotion) == 0 || !reducedMotion[0]
	input.SetStyles(styles)
	input.SetPromptFunc(3, func(info textarea.PromptInfo) string {
		if info.LineNumber == 0 {
			if mode == ModeNormal {
				return "▏❯ "
			}
			return "▌❯ "
		}
		return " │ "
	})
}

// resetTurnDiagnostics clears presentation derived from the previous turn.
// These values are never part of a saved session, so carrying them across a
// new conversation or a session restore would mislabel the active transcript.
func (m *Model) resetTurnDiagnostics() {
	m.lastTurnDuration = 0
	m.doneFlash = false
	m.evalCount = 0
	m.promptTokens = 0
	m.turnEvalTotal = 0
	m.turnPromptTotal = 0
	m.capabilityRoute = nil
	m.lastCapabilityRoute = nil
	m.clearContinuationAction()
	m.lastTurnToolIndex = -1
	m.receiptInspectActive = false
	m.receiptInspectToolIndex = -1
}

// flushStream moves accumulated stream text into a chat entry with cached rendering.
func (m *Model) flushStream() {
	m.invalidateEntryCache()
	content := sanitizeTerminalMultiline(m.streamBuf.String())
	thinking := strings.Trim(sanitizeTerminalMultiline(m.thinkBuf.String()), "\r\n")
	if strings.TrimSpace(content) != "" || strings.TrimSpace(thinking) != "" {
		var rendered string
		if m.md != nil && strings.TrimSpace(content) != "" {
			rendered = m.md.RenderFull(content)
		}
		entry := ChatEntry{
			Kind:            "assistant",
			Content:         content,
			RenderedContent: rendered,
		}
		// Attach thinking content if present.
		if strings.TrimSpace(thinking) != "" {
			entry.ThinkingContent = thinking
			entry.ThinkingCollapsed = true
		}
		m.entries = append(m.entries, entry)
	}
	// A flush is also a semantic segment boundary (tool call or completed turn).
	// Clear whitespace-only buffers and partial tag search state even when there
	// was nothing worth presenting, otherwise a later segment can inherit a
	// phantom live/assistant block.
	m.streamBuf.Reset()
	m.thinkBuf.Reset()
	m.inThinking = false
	m.thinkSearchBuf = ""
}

// invalidateRenderedCache clears cached renders (e.g. on terminal resize).
func (m *Model) invalidateRenderedCache() {
	for i := range m.entries {
		if m.entries[i].Kind == "assistant" && m.entries[i].RenderedContent != "" {
			if m.md != nil {
				m.entries[i].RenderedContent = m.md.RenderFull(sanitizeTerminalMultiline(m.entries[i].Content))
			}
		}
	}
	m.invalidateEntryCache()
}

// footerHeight returns the total height of the footer area (divider + status + input/hint).
func (m *Model) footerHeight() int {
	height := 1 // divider
	if m.compactCompletionOwnsDivider() {
		height = 0
	}
	if plan := m.renderGoalPlan(); plan != "" {
		height += lipgloss.Height(plan)
	}
	if bob := m.renderBobWorkspaceContext(); bob != "" {
		height += lipgloss.Height(bob)
	}
	if action := m.renderContinuationAction(); action != "" && !m.goalPlanOwnsContinuation() {
		height += lipgloss.Height(action)
	}
	if status := m.renderStatusLine(); status != "" {
		statusRows := lipgloss.Height(status)
		height += statusRows
		if m.activityComposerGap() {
			height++
		}
		if statusRows > 1 {
			// A decision-only footer ends with the top-level terminal safety row;
			// reserve it so wrapped actions never push a 30x12 view past the edge.
			height++
		}
	}
	if m.pendingApproval != nil {
		return height + lipgloss.Height(m.renderApproval())
	}
	if m.readScopePrompt != nil {
		return height + lipgloss.Height(m.renderReadScopePrompt())
	}
	if m.pendingPaste != nil {
		return height
	}
	if m.pendingSessionSwitch != nil {
		return height
	}
	if m.overlay == OverlayCortexDecision && m.cortexDecision != nil {
		return height + lipgloss.Height(m.cortexDecision.View(m.cortexDecisionBusyMarker()))
	}
	if m.overlay == OverlayCompletion && m.isCompletionActive() {
		popup, _ := m.renderCompletionModalView()
		return height + lipgloss.Height(popup) + m.inputLines
	}
	if m.overlay == OverlayPlanForm && m.planFormState != nil {
		form, _ := m.renderPlanFormView()
		return height + lipgloss.Height(form)
	}
	if m.overlay == OverlayGoalForm && m.goalFormState != nil {
		form, _ := m.goalFormState.ViewWithCursor()
		return height + lipgloss.Height(form)
	}
	if m.overlay != OverlayNone {
		return height + m.inputLines
	}
	if m.composerEditable() {
		if m.queuedFollowUpHeld() {
			height += lipgloss.Height(m.renderQueuedFollowUp())
		}
		if m.renderComposerOverflowCue() != "" {
			height++
		}
		return height + m.inputLines
	}
	return height + 1
}

// syncInputHeight mirrors Bubbles' visual-row-aware dynamic height into the
// parent layout and recalculates the transcript allocation when it changes.
func (m *Model) syncInputHeight() {
	lines := max(1, m.input.Height())
	if lines != m.inputLines {
		m.inputLines = lines
		m.recalcViewportHeight()
	}
}

const maxComposerVisibleRows = 8

// composerVisibleRowLimit lets roomy terminals show more of a draft while
// preserving the established five-row minimum-terminal contract.
func composerVisibleRowLimit(terminalHeight int) int {
	if terminalHeight <= 0 {
		return maxComposerVisibleRows
	}
	return min(maxComposerVisibleRows, max(5, terminalHeight/3))
}

// reflowInputViewport lets Bubbles populate its internal viewport with content
// inserted directly by the parent before it repositions around the preserved
// cursor. Without this no-op child update, a large accepted paste can clamp its
// five-row viewport against stale pre-paste content and hide the closing rows.
func (m *Model) reflowInputViewport() tea.Cmd {
	hadOverflowCue := m.renderComposerOverflowCue() != ""
	wasFocused := m.input.Focused()
	if !wasFocused {
		// Textarea.Update intentionally ignores messages while blurred. Parent-
		// owned completion and modal flows still need the same content/viewport
		// reconciliation before focus returns, so focus only for this synchronous
		// no-op update and restore the original presentation state immediately.
		_ = m.input.Focus()
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(inputViewportReflowMsg{})
	if !wasFocused {
		m.input.Blur()
	}
	m.syncInputHeight()
	if hadOverflowCue != (m.renderComposerOverflowCue() != "") {
		m.recalcViewportHeight()
	}
	return cmd
}

type inputViewportReflowMsg struct{}

// invalidateEntryCache marks the incremental entry render cache as stale,
// forcing a full re-render on the next renderEntries() call.
func (m *Model) invalidateEntryCache() {
	m.entryCacheValid = false
	m.cachedEntriesRender = ""
	m.cachedEntryCount = 0
	m.cachedStableCount = 0
	m.cachedPrefixState = entryRenderState{}
	m.cachedToolHitRegions = nil
	m.cachedThinkingHitRegions = nil
}

// checkAutoScroll resets scroll anchor when the viewport is at the bottom,
// allowing auto-scroll to resume during streaming.
func (m *Model) checkAutoScroll() {
	if m.viewport.AtBottom() {
		m.markFollowingLatest()
	}
}

// recalcViewportHeight updates the viewport height based on current footer size.
func (m *Model) recalcViewportHeight() {
	if !m.ready || m.height == 0 {
		return
	}
	paused := m.followPaused()
	yOffset := m.viewport.YOffset()
	m.viewport.SetHeight(m.viewportHeight())
	// Footer reflow must not silently change transcript ownership. Following
	// stays pinned to the newest row; a paused reader keeps the same logical
	// offset, clamped by Bubbles if the new geometry has less scroll range.
	m.restoreFollowPosition(paused, yOffset)
}

// viewportHeight is the single vertical-layout authority shared by terminal
// resize and multiline-composer reflow. The one extra row accounts for the
// newline separating the viewport from the footer.
func (m *Model) viewportHeight() int {
	return max(1, m.height-1-m.footerHeight())
}

// FormatToolArgs formats tool arguments as a compact JSON string for display.
func FormatToolArgs(args map[string]any) string {
	return agent.FormatToolArgs(args)
}

// isCompletionActive returns true when the completion modal is open.
func (m *Model) isCompletionActive() bool {
	return m.completionState != nil
}

// newCompletionState creates a CompletionState with the filter textinput initialized.
func newCompletionState(kind string, items []Completion, multiSelect bool, presentation ...bool) *CompletionState {
	isDark := true
	reducedMotion := false
	if len(presentation) > 0 {
		isDark = presentation[0]
	}
	if len(presentation) > 1 {
		reducedMotion = presentation[1]
	}
	ti := textinput.New()
	ti.SetStyles(semanticTextInputStyles(isDark, reducedMotion))
	ti.Placeholder = "type to narrow"
	ti.Prompt = ""
	ti.Focus()
	ti.CharLimit = 128

	var sel map[int]bool
	if multiSelect {
		sel = make(map[int]bool)
	}

	return &CompletionState{
		Kind:          kind,
		Filter:        ti,
		BaseItems:     append([]Completion(nil), items...),
		AllItems:      items,
		FilteredItems: items,
		Index:         0,
		Selected:      sel,
	}
}

func (m *Model) triggerCompletion(input string) tea.Cmd {
	if m.completer == nil {
		return nil
	}
	cursorRune := utf8.RuneCountInString(input)
	if input == m.input.Value() {
		cursorRune = textareaCursorRuneOffset(input, m.input.Line(), m.input.Column())
	}
	token, ok := completionTokenAtCursor(input, cursorRune)
	if !ok {
		return nil
	}

	source := token.Source
	if source == "" {
		source = string(token.Anchor.Trigger)
	}
	baseItems := m.completer.CompleteStatic(source)
	filteredItems := FilterCompletions(baseItems, token.Query)
	if token.CommandPrefix != "" {
		filteredItems = append([]Completion(nil), baseItems...)
	}
	if token.Kind != "attachments" && len(filteredItems) == 0 {
		return nil
	}

	anchor := m.captureCompletionTranscriptAnchor()
	m.completionGeneration++
	m.completionState = newCompletionState(token.Kind, baseItems, token.Kind != "command", m.isDark, m.reducedMotion)
	m.completionState.Anchor = token.Anchor
	m.completionState.CommandPrefix = token.CommandPrefix
	m.completionState.Generation = m.completionGeneration
	m.completionState.Filter.SetValue(token.Query)
	m.completionState.Filter.CursorEnd()
	m.completionState.FilteredItems = filteredItems
	m.completionState.Filter.SetWidth(completionFilterInputWidth(m.width))
	m.completionSuppressedDraft = ""
	m.overlay = OverlayCompletion
	m.input.Blur()
	m.recalcViewportHeight()
	m.restoreCompletionTranscriptAnchor(anchor)

	previewCmd := m.refreshCompletionPreview()
	if token.Kind == "attachments" {
		return tea.Batch(previewCmd, m.scheduleCompletionSearch(token.Query, "", false))
	}
	return previewCmd
}

func (m *Model) acceptCompletion() {
	cs := m.completionState
	if cs == nil {
		return
	}
	anchorSnapshot := m.captureCompletionTranscriptAnchor()
	anchor := normalizedCompletionAnchor(cs, m.input.Value())
	insertion := completionInsertion(cs, completionAnchorSuffixStartsWithSpace(anchor))
	if insertion == "" {
		return
	}
	draft, cursorRune := replaceCompletionAnchor(anchor, insertion)
	m.setComposerDraftAtRune(draft, cursorRune)
	m.closeCompletion()
	m.restoreCompletionTranscriptAnchor(anchorSnapshot)
}

func (m *Model) toggleCompletionSelection() {
	cs := m.completionState
	if cs == nil || cs.Selected == nil || len(cs.FilteredItems) == 0 {
		return
	}

	// Map the filtered index back to AllItems index
	filteredItem := cs.FilteredItems[cs.Index]
	for i, item := range cs.AllItems {
		if item.Label == filteredItem.Label && item.Insert == filteredItem.Insert {
			if cs.Selected[i] {
				delete(cs.Selected, i)
			} else {
				cs.Selected[i] = true
			}
			break
		}
	}
}

// drillIntoFolder navigates into a subfolder in the @ completion modal.
func (m *Model) drillIntoFolder() tea.Cmd {
	cs := m.completionState
	if cs == nil || cs.Index >= len(cs.FilteredItems) {
		return nil
	}

	anchor := m.captureCompletionTranscriptAnchor()
	item := cs.FilteredItems[cs.Index]
	cs.CurrentPath = strings.Trim(strings.TrimPrefix(completionItemPath(item), "@"), "/")
	cs.BaseItems = nil
	cs.Filter.SetValue("")
	replaceCompletionItems(cs, nil)
	cs.Index = 0
	m.recalcViewportHeight()
	m.restoreCompletionTranscriptAnchor(anchor)
	return tea.Batch(m.refreshCompletionPreview(), m.scheduleCompletionSearch("", cs.CurrentPath, false))
}

// drillUpFolder navigates to the parent folder in the @ completion modal.
func (m *Model) drillUpFolder() tea.Cmd {
	cs := m.completionState
	if cs == nil || cs.CurrentPath == "" {
		return nil
	}

	anchor := m.captureCompletionTranscriptAnchor()
	// Pop last segment
	if idx := strings.LastIndex(cs.CurrentPath, "/"); idx >= 0 {
		cs.CurrentPath = cs.CurrentPath[:idx]
	} else {
		cs.CurrentPath = ""
	}

	if cs.CurrentPath == "" {
		cs.BaseItems = m.completer.CompleteStatic("@")
	} else {
		cs.BaseItems = nil
	}
	cs.Filter.SetValue("")
	replaceCompletionItems(cs, append([]Completion(nil), cs.BaseItems...))
	cs.Index = 0
	m.recalcViewportHeight()
	m.restoreCompletionTranscriptAnchor(anchor)
	return tea.Batch(m.refreshCompletionPreview(), m.scheduleCompletionSearch("", cs.CurrentPath, false))
}

func (m *Model) closeCompletion() {
	anchor := m.captureCompletionTranscriptAnchor()
	if m.completionState != nil {
		if m.completionState.SearchCancel != nil {
			m.completionState.SearchCancel()
		}
		if m.completionState.PreviewCancel != nil {
			m.completionState.PreviewCancel()
		}
	}
	m.completionGeneration++
	m.completionState = nil
	m.clearCompletionSuppression()
	m.overlay = OverlayNone
	if m.composerEditable() {
		m.input.Focus()
	}
	m.recalcViewportHeight()
	m.restoreCompletionTranscriptAnchor(anchor)
}

func (m *Model) clearCompletionSuppression() {
	m.completionSuppressedDraft = ""
}

// dismissCompletion returns every character typed through the completion
// filter to the composer. Automatic discovery remains dismissed only while
// that exact draft is unchanged; editing it or pressing Tab can reopen the
// completion surface.
func (m *Model) dismissCompletion() {
	anchorSnapshot := m.captureCompletionTranscriptAnchor()
	draft, cursorRune := m.completionDraftAndCursor()
	m.closeCompletion()
	m.setComposerDraftAtRune(draft, cursorRune)
	m.completionSuppressedDraft = draft
	m.restoreCompletionTranscriptAnchor(anchorSnapshot)
}

func (m *Model) completionDraftAndCursor() (string, int) {
	cs := m.completionState
	if cs == nil {
		draft := m.input.Value()
		return draft, textareaCursorRuneOffset(draft, m.input.Line(), m.input.Column())
	}

	anchor := normalizedCompletionAnchor(cs, m.input.Value())
	prefix := string(anchor.Trigger)
	if cs.CommandPrefix != "" {
		prefix = cs.CommandPrefix
	}
	query := prefix + cs.Filter.Value()
	queryCursorRune := utf8.RuneCountInString(prefix) + cs.Filter.Position()
	switch cs.Kind {
	case "attachments":
		if cs.CurrentPath != "" {
			prefix := "@" + strings.Trim(cs.CurrentPath, "/") + "/"
			query = prefix + cs.Filter.Value()
			queryCursorRune = utf8.RuneCountInString(prefix) + cs.Filter.Position()
		}
	}
	return replaceCompletionAnchorAt(anchor, query, queryCursorRune)
}

// lastAssistantContent scans entries backwards for the last assistant message.
func (m *Model) lastAssistantContent() string {
	for i := len(m.entries) - 1; i >= 0; i-- {
		if m.entries[i].Kind == "assistant" {
			return m.entries[i].Content
		}
	}
	return ""
}

// copyToClipboard copies text to the system clipboard and returns a status message.
func (m *Model) copyToClipboard(text string) tea.Cmd {
	return func() tea.Msg {
		if err := clipboard.WriteAll(text); err != nil {
			return SystemMessageMsg{Msg: "Clipboard error: " + err.Error()}
		}
		return SystemMessageMsg{Msg: "Copied to clipboard."}
	}
}

// readClipboardPaste keeps explicit Ctrl+V on the same inspected path as a
// terminal bracketed-paste event. The textarea's private clipboard command is
// disabled at construction because its internal message type would otherwise
// insert directly and bypass the parent paste receipt.
func (m *Model) readClipboardPaste() tea.Cmd {
	read := m.clipboardRead
	readImage := m.clipboardImageRead
	return func() tea.Msg {
		var content string
		var textErr error
		if read != nil {
			content, textErr = read()
		}
		if strings.TrimSpace(content) != "" {
			return tea.PasteMsg{Content: content}
		}
		if readImage != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			name, data, err := readImage(ctx)
			if err == nil && len(data) > 0 {
				return ClipboardImagePasteMsg{Name: name, Data: data}
			}
		}
		if textErr != nil {
			return SystemMessageMsg{Msg: "Clipboard text is unavailable and no supported image was found."}
		}
		return SystemMessageMsg{Msg: "Clipboard is empty or has no supported text, PNG, JPEG, or GIF image."}
	}
}

// handleMouseClick hit-tests completed transcript disclosures. Live reasoning
// intentionally has no region and remains non-interactive until it settles.
func (m *Model) handleMouseClick(x, y int) {
	if x < 0 || x >= m.viewport.Width() || y < 0 || y >= m.viewport.Height() {
		return
	}
	// The viewport starts at terminal row zero in the sidebar-free layout.
	vpY := y + m.viewport.YOffset()
	for _, region := range m.toolHitRegions {
		if vpY == region.Row && x < region.EndCol {
			if region.ToolIndex >= 0 && region.ToolIndex < len(m.toolEntries) {
				m.toggleToolReceipt(region.ToolIndex, false)
			}
			return
		}
	}
	for _, region := range m.thinkingHitRegions {
		if vpY != region.Row || x >= region.EndCol {
			continue
		}
		if region.EntryIndex < 0 || region.EntryIndex >= len(m.entries) {
			return
		}
		entry := m.entries[region.EntryIndex]
		if entry.Kind != "assistant" || strings.TrimSpace(entry.ThinkingContent) == "" ||
			reasoningReceiptDigest(entry.ThinkingContent) != region.Digest {
			return
		}
		m.toggleThinkingReceipt(region.EntryIndex)
		return
	}
}

func (m *Model) toggleThinkingReceipt(entryIndex int) bool {
	if entryIndex < 0 || entryIndex >= len(m.entries) {
		return false
	}
	entry := &m.entries[entryIndex]
	if entry.Kind != "assistant" || strings.TrimSpace(entry.ThinkingContent) == "" {
		return false
	}
	paused, yOffset := m.followPaused(), m.viewport.YOffset()
	entry.ThinkingCollapsed = !entry.ThinkingCollapsed
	m.invalidateEntryCache()
	m.viewport.SetContent(m.renderEntries())
	m.restoreFollowPosition(paused, yOffset)
	return true
}

func (m *Model) toggleAllThinkingReceipts() bool {
	targetCollapsed := false
	found := false
	for i := len(m.entries) - 1; i >= 0; i-- {
		entry := m.entries[i]
		if entry.Kind == "assistant" && strings.TrimSpace(entry.ThinkingContent) != "" {
			targetCollapsed = !entry.ThinkingCollapsed
			found = true
			break
		}
	}
	if !found {
		return false
	}
	paused, yOffset := m.followPaused(), m.viewport.YOffset()
	for i := range m.entries {
		if m.entries[i].Kind == "assistant" && strings.TrimSpace(m.entries[i].ThinkingContent) != "" {
			m.entries[i].ThinkingCollapsed = targetCollapsed
		}
	}
	m.invalidateEntryCache()
	m.viewport.SetContent(m.renderEntries())
	m.restoreFollowPosition(paused, yOffset)
	return true
}

// toggleToolReceipt keeps the disclosure and transcript anchor as one parent-
// owned interaction. Keyboard inspection reveals the exact ToolCard header;
// hiding it restores the user's previous follow intent and offset.
func (m *Model) toggleToolReceipt(toolIndex int, reveal bool) {
	if toolIndex < 0 || toolIndex >= len(m.toolEntries) {
		return
	}
	if m.receiptInspectActive && m.receiptInspectToolIndex != toolIndex {
		m.cancelReceiptInspection(false)
	}
	expanding := m.toolEntries[toolIndex].Collapsed
	restoreInspectionAnchor := !expanding && m.receiptInspectActive && m.receiptInspectToolIndex == toolIndex
	if expanding && reveal {
		m.receiptInspectActive = true
		m.receiptInspectToolIndex = toolIndex
		m.receiptInspectAnchorPaused = m.followPaused()
		m.receiptInspectAnchorYOffset = m.viewport.YOffset()
	}
	m.toolEntries[toolIndex].Collapsed = !m.toolEntries[toolIndex].Collapsed
	m.invalidateEntryCache()
	m.viewport.SetContent(m.renderEntries())
	if expanding && reveal {
		m.revealToolReceipt(toolIndex)
		return
	}
	if restoreInspectionAnchor {
		m.restoreFollowPosition(m.receiptInspectAnchorPaused, m.receiptInspectAnchorYOffset)
		m.receiptInspectActive = false
		m.receiptInspectToolIndex = -1
	}
}

func (m *Model) cancelReceiptInspection(collapse bool) {
	if !m.receiptInspectActive {
		return
	}
	toolIndex := m.receiptInspectToolIndex
	if collapse && toolIndex >= 0 && toolIndex < len(m.toolEntries) && !m.toolEntries[toolIndex].Collapsed {
		m.toolEntries[toolIndex].Collapsed = true
		m.invalidateEntryCache()
		m.viewport.SetContent(m.renderEntries())
	}
	m.restoreFollowPosition(m.receiptInspectAnchorPaused, m.receiptInspectAnchorYOffset)
	m.receiptInspectActive = false
	m.receiptInspectToolIndex = -1
}

func (m *Model) revealToolReceipt(toolIndex int) {
	for _, region := range m.toolHitRegions {
		if region.ToolIndex == toolIndex {
			m.viewport.SetYOffset(region.Row)
			m.pauseFollow()
			return
		}
	}
}

func approvalDetail(toolName string, args map[string]any) (string, bool) {
	encoded, err := json.MarshalIndent(args, "", "  ")
	if err != nil {
		return fmt.Sprintf("Refused `%s`: its exact arguments could not be encoded for inspection.", toolName), false
	}
	return fmt.Sprintf("Permission required for `%s`:\n```json\n%s\n```", toolName, encoded), true
}

// Ready returns true if the TUI is fully initialized.
// Exported for testing.
func (m *Model) Ready() bool {
	return m.ready
}

// AnchorActive returns true if scroll anchor is active.
// Exported for testing.
func (m *Model) AnchorActive() bool {
	return m.anchorActive
}
