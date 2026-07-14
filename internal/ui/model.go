package ui

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"runtime/debug"
	"strconv"
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
	"github.com/abdul-hamid-achik/local-agent/internal/ecosystem"
	"github.com/abdul-hamid-achik/local-agent/internal/execution"
	"github.com/abdul-hamid-achik/local-agent/internal/goal"
	"github.com/abdul-hamid-achik/local-agent/internal/llm"
	"github.com/abdul-hamid-achik/local-agent/internal/permission"
	"github.com/abdul-hamid-achik/local-agent/internal/safeio"
	"github.com/abdul-hamid-achik/local-agent/internal/skill"
)

// State represents the TUI's two possible states.
type State int

const (
	StateIdle      State = iota // waiting for user input
	StateWaiting                // sent to LLM, waiting for first token
	StateStreaming              // LLM is generating a response
)

// OverlayKind represents what overlay (if any) is currently shown.
type OverlayKind int

const (
	OverlayNone OverlayKind = iota
	OverlayHelp
	OverlayCompletion
	OverlayModelPicker
	OverlayCloudConsent
	OverlayPlanForm
	OverlayCortexDecision
	OverlaySessionsPicker
	OverlaySettings
	OverlayAgentPicker
	OverlayModePicker
	OverlayGoalForm
	OverlayRuntimeStatus
	OverlayGoalInspector
	OverlayGoalRecovery
	OverlayModelDetails
	OverlayModelPull
)

// CompletionState holds all state for the composer-owned completion popup.
type CompletionState struct {
	Kind          string          // "command", "attachments", "skills"
	Filter        textinput.Model // inline filter field
	Anchor        completionAnchor
	CommandPrefix string       // exact `/command ` prefix for registry action completion
	BaseItems     []Completion // non-workspace items retained across async searches
	AllItems      []Completion // full unfiltered list
	FilteredItems []Completion // items matching current filter
	Index         int          // cursor in FilteredItems
	Selected      map[int]bool // multi-select (keys = AllItems indices)
	CurrentPath   string       // for @ file browsing: relative dir path
	Searching     bool         // true while vecgrep is in flight
	Generation    uint64       // guards results across close/reopen cycles
	DebounceTag   int          // cancel stale searches
	SearchCancel  context.CancelFunc
	Preview       completionPreview
	PreviewToken  uint64
	PreviewCancel context.CancelFunc
}

// ToolStatus represents the state of a tool execution.
type ToolStatus int

const (
	ToolStatusRunning ToolStatus = iota
	ToolStatusDone
	ToolStatusError
)

// ToolEntry tracks the lifecycle of a single tool call.
type ToolEntry struct {
	ID                      string
	Name                    string
	Summary                 string         // bounded semantic context for compact/restored receipts
	Args                    string         // formatted args string
	RawArgs                 map[string]any `json:"-"` // ephemeral original args
	Result                  string
	IsError                 bool
	Status                  ToolStatus
	StartTime               time.Time
	Duration                time.Duration
	Collapsed               bool                     // per-entry collapse state
	BeforeContent           string                   `json:"-"` // ephemeral snapshot before file write
	BeforeSnapshotAvailable bool                     `json:"-"` // false when the bounded pre-write read was unavailable
	DiffLines               []DiffLine               // computed diff (nil = not a file write)
	DiffPending             bool                     `json:"-"` // post-write read/LCS is running outside Update
	DiffGeneration          uint64                   `json:"-"` // accepts exactly one matching asynchronous result
	Projection              ecosystem.ToolProjection // bounded semantic role, route, and outcome
}

// ChatEntry is a single item in the chat log.
type ChatEntry struct {
	Kind              string // "user", "assistant", "tool_group", "error", "system"
	Content           string // raw content
	RenderedContent   string // cached Glamour output (set once on completion)
	Name              string // tool name for tool entries
	IsError           bool   // for tool_result
	ToolIndex         int    // index into toolEntries for "tool_group" kind
	ThinkingContent   string // extracted <think> content
	ThinkingCollapsed bool   // default: true
}

// toolHitRegion is an exact, ordered transcript row target for one ToolCard
// header. Dense receipts can be adjacent, so hit testing must never infer a
// fixed card height or iterate an unordered map.
type toolHitRegion struct {
	ToolIndex int
	Row       int
	EndCol    int
}

// startupItem tracks the progress of a single startup task.
type startupItem struct {
	ID     string
	Label  string
	Status string // "connecting", "connected", "failed"
	Detail string
}

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
	state               State
	overlay             OverlayKind
	overlayParent       OverlayKind
	entries             []ChatEntry
	streamBuf           strings.Builder
	lastStreamPaint     time.Time // throttles per-token re-renders during streaming
	turnStartedAt       time.Time
	lastTurnDuration    time.Duration
	now                 func() time.Time
	width               int
	height              int
	ready               bool
	isDark              bool
	reducedMotion       bool
	evalCount           int
	promptTokens        int
	turnEvalTotal       int
	turnPromptTotal     int
	toolsPending        int
	capabilityRoute     *agent.CapabilityRoute
	lastCapabilityRoute *agent.CapabilityRoute
	inputLines          int
	userScrolledUp      bool

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
	toolEntries    []ToolEntry
	toolsCollapsed bool
	toolHitRegions []toolHitRegion
	toolCardMgr    ToolCardManager
	diffGeneration uint64

	// Incremental rendering cache
	cachedEntriesRender  string
	cachedEntryCount     int
	cachedToolHitRegions []toolHitRegion
	entryCacheValid      bool

	// Thinking state
	thinkBuf       strings.Builder
	inThinking     bool
	thinkSearchBuf string

	// Terminal title
	doneFlash bool

	// Session persistence
	sessionID                    int64
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
	sessionListToken             uint64
	sessionListing               bool
	startupResumeSelector        *SessionResumeSelector

	// Paste detection
	pendingPaste *pendingPaste

	// Responsive layout
	forceCompact bool // user-toggled compact mode

	// Mode system
	mode        Mode
	modeConfigs [3]ModeConfig

	// Model management
	modelManager             *llm.ModelManager
	router                   config.ModelRouter
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
	turnPrompt         string
	turnPromptVisible  bool
	turnCheckpointSet  bool

	// Prompt history
	promptHistory []string // all submitted inputs
	historyIndex  int      // -1 = not browsing, 0 = most recent
	historySaved  string   // saved current input when entering history

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
		input:            ta,
		spin:             s,
		scramble:         NewScrambleModel(true),
		styles:           initialStyles,
		keys:             DefaultKeyMap(),
		state:            StateIdle,
		isDark:           true,
		reducedMotion:    reducedMotion,
		now:              time.Now,
		inputLines:       1,
		toolsCollapsed:   true,
		initializing:     true,
		approvalPosture:  ApprovalPosturePrompted,
		mode:             ModeNormal,
		modeConfigs:      DefaultModeConfigs(),
		modelManager:     modelManager,
		router:           router,
		logger:           logger,
		agent:            ag,
		cmdRegistry:      cmdReg,
		skillMgr:         skillMgr,
		completer:        completer,
		completionReader: newCompletionWorkspaceReader(),
		historyIndex:     -1,
		toolCardMgr:      NewToolCardManager(true),
		commitRunner:     runCommit,
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
	if m.initCancel != nil {
		m.initCancel()
	}
	if m.pendingApproval != nil {
		m.resolvePendingApproval(permission.Cancelled("application is shutting down"))
	}
	m.pendingPaste = nil
	if m.readScopePrompt != nil {
		releaseReadGrants(m.readScopePrompt.Grants)
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
		!m.modelPullRunning && !m.sessionLoading && !m.ollamaInventoryCommitting && !m.readScopeOpRunning
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
		approvalAnchor := m.captureApprovalTranscriptAnchor()
		inlineFormAnchor := m.captureInlineFormTranscriptAnchor()
		m.isDark = msg.IsDark()
		m.styles = NewStyles(m.isDark)
		// Update spinner style for theme.
		m.spin.Style = m.styles.StatusDot
		m.syncComposerAuthority()
		m.scramble.SetDark(msg.IsDark())
		// Update tool card styles for theme.
		m.toolCardMgr.SetDark(msg.IsDark())
		m.restylePickerOverlays()
		if m.goalFormState != nil {
			m.goalFormState.SetTheme(m.isDark)
			m.goalFormState.SetReducedMotion(m.reducedMotion)
		}
		if m.cortexDecision != nil {
			m.cortexDecision.SetTheme(m.isDark)
			m.cortexDecision.reducedMotion = m.reducedMotion
		}
		if m.goalInspectorState != nil {
			m.goalInspectorState.SetTheme(m.isDark)
			m.goalInspectorState.SetReducedMotion(m.reducedMotion)
		}
		if m.goalRecoveryState != nil {
			m.goalRecoveryState.SetTheme(m.isDark)
			m.goalRecoveryState.SetReducedMotion(m.reducedMotion)
		}
		if m.pendingApproval != nil && m.approvalState != nil {
			// Approval previews live in a cached Bubbles viewport. Rebuild its
			// styled content immediately so a live theme switch cannot leave the
			// body on the old palette while the title and choices change.
			m.resizeApproval(true)
		}
		// Recreate markdown renderer for new theme.
		if m.width > 0 {
			m.markdownWidth = m.chatContentWidth()
			m.md = NewMarkdownRenderer(m.markdownWidth, m.isDark)
			m.invalidateRenderedCache()
		}
		if m.ready {
			m.viewport.SetContent(m.renderEntries())
		}
		m.refreshInlineFormLayout(inlineFormAnchor)
		m.restoreApprovalTranscriptAnchor(approvalAnchor)

	case tea.WindowSizeMsg:
		approvalAnchor := m.captureApprovalTranscriptAnchor()
		completionAnchor := m.captureCompletionTranscriptAnchor()
		inlineFormAnchor := m.captureInlineFormTranscriptAnchor()
		wasUndersized := m.ready && m.narrowTerminalHint() != ""
		widthChanged := msg.Width != m.width
		m.width = msg.Width
		m.height = msg.Height
		isUndersized := m.narrowTerminalHint() != ""
		switch {
		case isUndersized:
			m.resetHiddenApprovalChoice()
			m.cancelTerminalInputResume()
		case wasUndersized:
			cmds = append(cmds, m.armTerminalInputResume())
		case m.terminalInputResumeActive():
			cmds = append(cmds, m.armTerminalInputResume())
		}
		if m.goalFormState != nil {
			m.goalFormState.SetSize(m.width, m.height)
		}
		if m.cortexDecision != nil {
			m.cortexDecision.SetSize(m.width, m.height)
		}

		// The conversation always owns the full terminal width. Infrequent
		// controls are presented in overlays.
		viewportWidth := msg.Width - 1
		if viewportWidth < 20 {
			viewportWidth = 20
		}

		contentWidth := m.chatContentWidth()
		markdownChanged := m.md == nil || contentWidth != m.markdownWidth
		if markdownChanged {
			m.markdownWidth = contentWidth
			m.md = NewMarkdownRenderer(contentWidth, m.isDark)
		}

		// Recalculate content height
		contentH := m.viewportHeight()

		if !m.ready {
			m.viewport = viewport.New(
				viewport.WithWidth(viewportWidth),
				viewport.WithHeight(contentH),
			)
			// Override viewport KeyMap: keep only pgup/pgdown/ctrl+u/ctrl+d
			m.viewport.KeyMap.PageDown = key.NewBinding(key.WithKeys("pgdown"))
			m.viewport.KeyMap.PageUp = key.NewBinding(key.WithKeys("pgup"))
			m.viewport.KeyMap.HalfPageUp = key.NewBinding(key.WithKeys("ctrl+u"))
			m.viewport.KeyMap.HalfPageDown = key.NewBinding(key.WithKeys("ctrl+d"))
			m.viewport.KeyMap.Up = key.NewBinding(key.WithDisabled())
			m.viewport.KeyMap.Down = key.NewBinding(key.WithDisabled())
			m.viewport.KeyMap.Left = key.NewBinding(key.WithDisabled())
			m.viewport.KeyMap.Right = key.NewBinding(key.WithDisabled())
			m.viewport.SetContent(m.renderEntries())
			m.ready = true
			// Initialize scroll follow intent at the newest transcript row.
			m.markFollowingLatest()
			// Hit regions are populated by the transcript renderer.
			m.toolHitRegions = nil
		} else {
			m.viewport.SetWidth(viewportWidth)
			m.viewport.SetHeight(contentH)
			if markdownChanged {
				// Re-wrap completed assistant messages only when the actual
				// markdown width changes. Height-only resizes preserve caches.
				m.invalidateRenderedCache()
			} else if widthChanged {
				m.invalidateEntryCache()
			}
			if markdownChanged || widthChanged {
				m.viewport.SetContent(m.renderEntries())
			}
			// Maintain scroll position - if anchor is active, stay at bottom.
			m.gotoBottomIfFollowing()
		}

		// Resize help viewport if it's open.
		if m.overlay == OverlayHelp {
			m.resizeHelpViewport(true)
		}
		m.resizePickerOverlays()
		if m.pendingApproval != nil && m.approvalState != nil {
			m.resizeApproval(true)
			m.recalcViewportHeight()
		}
		if m.goalInspectorState != nil {
			m.goalInspectorState.SetSize(m.width, m.height)
		}
		if m.goalRecoveryState != nil {
			m.goalRecoveryState.SetSize(m.width, m.height)
		}

		// Input width matches viewport exactly - they're one unified area
		if msg.Width < 36 {
			m.input.Placeholder = "Ask or type / for commands"
		} else {
			m.input.Placeholder = "Ask, @mention files, or type /help"
		}
		m.input.SetWidth(viewportWidth)
		m.syncInputHeight()
		m.restoreApprovalTranscriptAnchor(approvalAnchor)
		m.restoreCompletionTranscriptAnchor(completionAnchor)
		m.restoreInlineFormTranscriptAnchor(inlineFormAnchor)

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
						selector, _ := SessionIDResumeSelector(si.id)
						m.overlayParent = OverlayNone
						m.closeSessionsPicker()
						return m, m.requestSessionRestore(selector)
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
				m.toolsCollapsed = !m.toolsCollapsed
				for i := range m.toolEntries {
					m.toolEntries[i].Collapsed = m.toolsCollapsed
				}
				m.invalidateEntryCache()
				m.viewport.SetContent(m.renderEntries())
				return m, nil
			}

		case key.Matches(msg, m.keys.ToggleFocusedTool):
			// Toggle last tool entry only when input is empty.
			if m.state == StateIdle && strings.TrimSpace(m.input.Value()) == "" {
				if len(m.toolEntries) > 0 {
					last := len(m.toolEntries) - 1
					m.toolEntries[last].Collapsed = !m.toolEntries[last].Collapsed
					m.invalidateEntryCache()
					m.viewport.SetContent(m.renderEntries())
				}
				return m, nil
			}

		case key.Matches(msg, m.keys.CompactToggle):
			if m.state == StateIdle {
				m.forceCompact = !m.forceCompact
				m.invalidateEntryCache()
				m.viewport.SetContent(m.renderEntries())
				return m, nil
			}

		case key.Matches(msg, m.keys.ToggleThinking):
			// Every visible disclosure advertises the same shortcut, so one press
			// applies the newest receipt's next state to all reasoning blocks.
			if m.state == StateIdle && strings.TrimSpace(m.input.Value()) == "" {
				targetCollapsed := false
				found := false
				for i := len(m.entries) - 1; i >= 0; i-- {
					if m.entries[i].Kind == "assistant" && strings.TrimSpace(m.entries[i].ThinkingContent) != "" {
						targetCollapsed = !m.entries[i].ThinkingCollapsed
						found = true
						break
					}
				}
				if found {
					for i := range m.entries {
						if m.entries[i].Kind == "assistant" && strings.TrimSpace(m.entries[i].ThinkingContent) != "" {
							m.entries[i].ThinkingCollapsed = targetCollapsed
						}
					}
					m.invalidateEntryCache()
					m.viewport.SetContent(m.renderEntries())
				}
				return m, nil
			}

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
				m.viewport.SetContent(m.renderEntries())
				m.resumeFollow()
				return m, nil
			}

		case key.Matches(msg, m.keys.NewConvo):
			if m.state == StateIdle {
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
		if m.state == StateWaiting {
			m.state = StateStreaming
			cmds = append(cmds, m.startActivityCmd())
		}
		// Route through thinking tag parser.
		mainText, thinkText, outInThinking, outSearchBuf := processStreamChunk(
			msg.Text, m.inThinking, m.thinkSearchBuf,
		)
		m.inThinking = outInThinking
		m.thinkSearchBuf = outSearchBuf
		if mainText != "" {
			m.streamBuf.WriteString(mainText)
		}
		if thinkText != "" {
			m.thinkBuf.WriteString(thinkText)
		}
		// Coalesce repaints to ~30fps. Fast local models emit tokens faster
		// than the terminal can usefully redraw; repainting every token wastes
		// CPU and causes flicker. StreamDoneMsg always repaints, so the final
		// partial is never dropped.
		if now := time.Now(); now.Sub(m.lastStreamPaint) >= 33*time.Millisecond {
			m.lastStreamPaint = now
			m.viewport.SetContent(m.renderEntries())
			m.gotoBottomIfFollowing()
		}

	case StreamThinkingMsg:
		if m.state == StateWaiting {
			m.state = StateStreaming
			cmds = append(cmds, m.startActivityCmd())
		}
		m.thinkBuf.WriteString(msg.Text)
		if now := time.Now(); now.Sub(m.lastStreamPaint) >= 33*time.Millisecond {
			m.lastStreamPaint = now
			m.viewport.SetContent(m.renderEntries())
			m.gotoBottomIfFollowing()
		}

	case StreamDoneMsg:
		m.evalCount = msg.EvalCount
		m.promptTokens = msg.PromptTokens
		m.turnEvalTotal += msg.EvalCount
		m.turnPromptTotal += msg.PromptTokens
		m.sessionEvalTotal += msg.EvalCount
		m.sessionPromptTotal += msg.PromptTokens

	case ContextCompactedMsg:
		m.promptTokens = 0

	case ContextCompactionStartedMsg:
		m.compactingContext = true
		m.viewport.SetContent(m.renderEntries())
		m.gotoBottomIfFollowing()
		cmds = append(cmds, m.startActivityCmd())

	case ContextCompactionFinishedMsg:
		m.compactingContext = false
		m.viewport.SetContent(m.renderEntries())
		m.gotoBottomIfFollowing()

	case ToolCallStartMsg:
		if m.goalTurnID != "" {
			m.goalTurnToolCalls++
		}
		startToolSpinner := m.state != StateStreaming && m.toolsPending == 0
		if m.state == StateWaiting {
			m.state = StateStreaming
		}
		projection := ecosystem.ProjectToolCall(msg.Name, msg.Args)
		te := ToolEntry{
			ID:         msg.ID,
			Name:       msg.Name,
			Args:       agent.FormatToolArgsForTool(msg.Name, msg.Args),
			RawArgs:    agent.SafeToolArgsForPersistence(msg.Name, msg.Args),
			Status:     ToolStatusRunning,
			StartTime:  msg.StartTime,
			Collapsed:  m.toolsCollapsed,
			Projection: projection,
		}
		te.Summary = boundedToolCardSummary(toolSummary(classifyTool(msg.Name), te))
		if classifyTool(msg.Name) == ToolTypeFileWrite {
			// The Adapter captured this before returning control to the tool
			// execution path. Update only installs the immutable result.
			te.BeforeContent = msg.BeforeContent
			te.BeforeSnapshotAvailable = msg.BeforeSnapshotAvailable
		}
		m.toolEntries = append(m.toolEntries, te)
		m.toolsPending++
		if startToolSpinner {
			cmds = append(cmds, m.startActivityCmd())
		}

		// Create tool card for fancy display
		kind := ToolCardGeneric
		switch classifyTool(msg.Name) {
		case ToolTypeFileRead, ToolTypeFileWrite:
			kind = ToolCardFile
		case ToolTypeBash:
			kind = ToolCardBash
		}
		m.toolCardMgr.AddCardWithID(msg.ID, msg.Name, kind, msg.StartTime)
		if len(m.toolCardMgr.Cards) > 0 {
			card := &m.toolCardMgr.Cards[len(m.toolCardMgr.Cards)-1]
			card.Args = te.Args
			card.SetSummary(te.Summary)
			card.Projection = te.Projection
		}

		// Settle the assistant segment before its tool receipt so transcript order
		// remains reasoning/prose → tool. Thinking-only segments render as one
		// compact disclosure without an empty assistant block.
		m.flushStream()
		m.entries = append(m.entries, ChatEntry{
			Kind:      "tool_group",
			ToolIndex: len(m.toolEntries) - 1,
		})
		m.viewport.SetContent(m.renderEntries())
		m.gotoBottomIfFollowing()

	case PlanFormCompletedMsg:
		return m, m.submitPlanFormPrompt(msg.Prompt)

	case goalOpenResultMsg:
		return m, m.handleGoalOpenResult(msg)

	case goalStatusResultMsg:
		return m, m.handleGoalStatusResult(msg)

	case cortexDecisionAnswerResultMsg:
		return m, m.handleCortexDecisionAnswerResult(msg)

	case ToolCallResultMsg:
		m.invalidateEntryCache()
		if m.logger != nil {
			m.logger.Info("tool call", "name", msg.Name, "duration", msg.Duration, "error", msg.IsError)
		}
		matched := false
		result := boundedToolCardResult(msg.Result)
		// Bob envelopes carry stable conflict/error codes and copy-pasteable
		// corrective commands; keep that digest visible ahead of the raw JSON.
		if digest := bobReceiptDigest(msg.Name, msg.Result); digest != "" {
			result = boundedToolCardResult(digest + "\n" + msg.Result)
		}
		var diffCmd tea.Cmd
		for i := len(m.toolEntries) - 1; i >= 0; i-- {
			if toolCallMatches(msg.ID, msg.Name, m.toolEntries[i].ID, m.toolEntries[i].Name) && m.toolEntries[i].Status == ToolStatusRunning {
				matched = true
				projection := msg.Projection.Normalize()
				if projection.Transport == "" {
					projection = ecosystem.ProjectToolResult(m.toolEntries[i].Projection, msg.Result, msg.IsError)
				}
				m.toolEntries[i].Projection = projection
				m.toolEntries[i].Result = result
				m.toolEntries[i].IsError = projection.Transport == ecosystem.TransportFailed || projection.Domain == ecosystem.DomainFailed
				m.toolEntries[i].Duration = msg.Duration
				if m.toolEntries[i].IsError {
					m.toolEntries[i].Status = ToolStatusError
				} else {
					m.toolEntries[i].Status = ToolStatusDone
				}
				// Successful file writes schedule the bounded post-write read and LCS
				// outside Update. The command owns only the path and pre-write bytes;
				// raw arguments and entry snapshots are cleared before Update returns.
				if classifyTool(m.toolEntries[i].Name) == ToolTypeFileWrite && projection.Successful() {
					path := toolSummary(ToolTypeFileWrite, m.toolEntries[i])
					if path != "" {
						if m.fileChanges == nil {
							m.fileChanges = make(map[string]int)
						}
						m.fileChanges[path]++
					}
					beforeAvailable := m.toolEntries[i].BeforeSnapshotAvailable || m.toolEntries[i].BeforeContent != ""
					if diffPath := diffPathFromArgs(m.toolEntries[i].RawArgs); diffPath != "" && beforeAvailable {
						m.diffGeneration++
						m.toolEntries[i].DiffPending = true
						m.toolEntries[i].DiffGeneration = m.diffGeneration
						diffCmd = buildFileDiffCmd(diffBuildRequest{
							Generation:      m.diffGeneration,
							ToolID:          m.toolEntries[i].ID,
							ToolName:        m.toolEntries[i].Name,
							Path:            diffPath,
							WorkDir:         m.agent.WorkDir(),
							Before:          m.toolEntries[i].BeforeContent,
							BeforeAvailable: beforeAvailable,
						})
					}
				}
				// Raw arguments and pre-write snapshots are needed only while the
				// call is active. Do not retain them in memory or session state.
				m.toolEntries[i].RawArgs = nil
				m.toolEntries[i].BeforeContent = ""
				m.toolEntries[i].BeforeSnapshotAvailable = false
				break
			}
		}
		if !matched {
			break
		}
		var completedProjection ecosystem.ToolProjection
		for i := len(m.toolEntries) - 1; i >= 0; i-- {
			if toolCallMatches(msg.ID, msg.Name, m.toolEntries[i].ID, m.toolEntries[i].Name) {
				completedProjection = m.toolEntries[i].Projection
				break
			}
		}
		if m.goalTurnID != "" && completedProjection.Successful() {
			m.goalTurnSuccesses++
		}
		// Update tool card
		cardState := toolCardStateFromProjection(completedProjection)
		m.toolCardMgr.UpdateCardSemanticWithID(msg.ID, msg.Name, cardState, result, msg.Duration, completedProjection)

		if m.toolsPending > 0 {
			m.toolsPending--
		}
		if diffCmd != nil {
			cmds = append(cmds, diffCmd)
		}
		m.viewport.SetContent(m.renderEntries())
		m.gotoBottomIfFollowing()

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
		m.entries = append(m.entries, ChatEntry{
			Kind:    "system",
			Content: msg.Msg,
		})
		// The first startup/recovery notice can add a fixed Settings row at
		// compact heights. Recompute the transcript allocation before painting.
		m.recalcViewportHeight()
		m.viewport.SetContent(m.renderEntries())
		m.gotoBottomIfFollowing()

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

	case ErrorMsg:
		if m.logger != nil {
			m.logger.Error("error", "msg", msg.Msg)
		}
		m.entries = append(m.entries, ChatEntry{
			Kind:    "error",
			Content: msg.Msg,
		})
		m.recalcViewportHeight()
		m.viewport.SetContent(m.renderEntries())
		m.gotoBottomIfFollowing()

	case AgentDoneMsg:
		m.compactingContext = false
		m.capabilityRoute = nil
		if m.logger != nil {
			m.logger.Info("agent done", "eval_tokens", m.evalCount, "err", msg.Err)
		}
		var unresolved *agent.UnresolvedExecutionError
		hasUnresolved := errors.As(msg.Err, &unresolved)
		if hasUnresolved {
			m.rollbackPreflightRejectedPrompt()
		}
		m.clearTurnMessageCheckpoint()
		followWasPaused := m.followPaused()
		followYOffset := m.viewport.YOffset()
		m.flushStream()
		m.settleGoalTurn(msg)
		if msg.Err == nil {
			m.sessionTurnCount++
		}
		if m.cancel != nil {
			m.cancel()
			m.cancel = nil
		}
		m.lastTurnDuration = m.turnElapsed()
		m.state = StateIdle
		if msg.Err != nil {
			m.restoreQueuedFollowUp()
		}
		m.input.Focus()
		if m.queuedFollowUp == nil && strings.TrimSpace(m.input.Value()) == "" {
			m.input.SetHeight(1)
			m.inputLines = 1
		} else {
			m.syncInputHeight()
		}
		m.recalcViewportHeight()
		m.viewport.SetContent(m.renderEntries())
		m.restoreFollowPosition(followWasPaused, followYOffset)
		if msg.Err == nil {
			// Terminal title flash is a success receipt, not a generic stopped state.
			m.doneFlash = true
			cmds = append(cmds, tea.Tick(2*time.Second, func(time.Time) tea.Msg {
				return DoneFlashExpiredMsg{}
			}))
		} else {
			m.doneFlash = false
			switch {
			case hasUnresolved:
				m.entries, _ = appendExecutionRecoveryNotice(m.entries, unresolved)
				m.rememberStandaloneRecovery(unresolved)
			case errors.Is(msg.Err, context.Canceled) && !m.shuttingDown:
				m.entries = append(m.entries, ChatEntry{Kind: "system", Content: "Turn cancelled."})
			}
			m.viewport.SetContent(m.renderEntries())
			m.restoreFollowPosition(followWasPaused, followYOffset)
		}
		// Persist a lossless state snapshot after every settled attempt. Failed
		// turns may contain cancellation or unknown-outcome receipts that must
		// survive restart even though they do not count as completed turns.
		settledPersisted := m.sessionID <= 0 || m.sessionStore == nil
		if m.sessionID > 0 && m.sessionStore != nil {
			previousCursor := m.executionCursor
			var cursorErr error
			cursorStoppedAtRecovery := false
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			if m.executionLease == nil {
				cursorErr = errors.New("execution session lease is unavailable; snapshot cursor was not advanced")
			} else {
				m.executionCursor, cursorErr = m.snapshotExecutionCursor(ctx)
				// An unresolved execution deliberately keeps the snapshot cursor on
				// the safe side of the effect. The transcript can still be saved at
				// that old cursor; presenting the expected boundary stop as a second
				// "Save session" failure makes one recovery condition look like data
				// loss and floods the chat with duplicate red errors.
				cursorStoppedAtRecovery = hasUnresolved && cursorErr != nil
			}
			saveErr := m.persistSessionState(ctx)
			if saveErr != nil {
				m.executionCursor = previousCursor
			} else if cursorErr == nil {
				m.agent.SetExecutionSnapshotCursor(m.executionCursor)
			}
			var usageErr error
			if saveErr == nil && msg.Err == nil {
				_, usageErr = m.sessionStore.RecordTokenUsage(ctx, db.RecordTokenUsageParams{
					SessionID: m.sessionID, Turn: int64(m.sessionTurnCount), EvalCount: int64(m.turnEvalTotal),
					PromptTokens: int64(m.turnPromptTotal), Model: m.model,
				})
			}
			cancel()
			persistErr := errors.Join(saveErr, usageErr)
			if !cursorStoppedAtRecovery {
				persistErr = errors.Join(cursorErr, persistErr)
			}
			if persistErr != nil {
				settledPersisted = false
				if m.goalRuntime != nil {
					m.goalPersistenceDirty = true
				}
				m.entries = append(m.entries, ChatEntry{Kind: "error", Content: fmt.Sprintf("Save session: %v", persistErr)})
				m.viewport.SetContent(m.renderEntries())
				m.restoreFollowPosition(followWasPaused, followYOffset)
			} else {
				settledPersisted = true
				if m.goalRuntime != nil {
					m.goalPersistenceDirty = false
				}
				if cmd := m.ensureCurrentGoalRecoveryProjection(false); cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
		}
		if m.goalNeedsEvaluation && !m.shuttingDown {
			if settledPersisted {
				m.doneFlash = false
				if cmd := m.beginGoalEvaluation(false); cmd != nil {
					cmds = append(cmds, cmd)
				}
			} else if m.goalRuntime != nil {
				m.goalNeedsEvaluation = false
				if snapshot, err := m.goalRuntime.Snapshot(context.Background()); err == nil && snapshot.State == goal.StateActive {
					_ = m.goalRuntime.Pause(context.Background(), "settled goal turn could not be persisted")
				}
				m.appendGoalError("Goal continuation stopped because the settled turn was not durably saved.")
			}
		}
		if msg.Err == nil && !settledPersisted {
			// A queued follow-up may only cross a durable settlement boundary.
			// Return it to the composer when saving fails so it cannot dispatch
			// unexpectedly after some later, unrelated turn.
			m.restoreQueuedFollowUp()
			m.recalcViewportHeight()
		}
		if msg.Err == nil && settledPersisted && !m.goalNeedsEvaluation && !m.shuttingDown && m.queuedFollowUp != nil {
			m.doneFlash = false
			if cmd := m.dispatchQueuedFollowUp(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		m.appendShutdownQuit(&cmds)

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
		if msg.Text != "" {
			m.entries = append(m.entries, ChatEntry{
				Kind:    "system",
				Content: msg.Text,
			})
			m.viewport.SetContent(m.renderEntries())
			m.resumeFollow()
		}

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
		if !m.fileLoading || msg.Token != m.fileOpToken {
			break
		}
		m.fileLoading = false
		if !m.shuttingDown {
			m.input.Focus()
		}
		if msg.Err != nil {
			m.entries = append(m.entries, ChatEntry{Kind: "error", Content: fmt.Sprintf("Import failed: %v", msg.Err)})
			m.invalidateEntryCache()
			m.viewport.SetContent(m.renderEntries())
			m.gotoBottomIfFollowing()
			break
		}

		// Commit the visible and model transcripts together, and detach from
		// the previous persisted session. The typed export intentionally omits
		// tool authority and hidden runtime state.
		m.agent.ReplaceMessages(msg.Messages)
		m.entries = msg.Entries
		m.toolEntries = nil
		m.resetConversationSession()
		m.entries = append(m.entries, ChatEntry{Kind: "system", Content: fmt.Sprintf(
			"Imported %d user/assistant messages into a new session. %d display-only system sections were not sent to the model; %d tool sections were omitted because Markdown does not preserve safe tool-call state.",
			len(msg.Messages), msg.UIOnlySections, msg.ToolSections,
		)})
		m.invalidateEntryCache()
		m.viewport.SetContent(m.renderEntries())
		m.resumeFollow()

	case ExportResultMsg:
		if !m.exportRunning || msg.Token != m.exportToken {
			break
		}
		m.exportRunning = false
		if !m.shuttingDown {
			m.input.Focus()
			if exportWasPublished(msg.Err) {
				m.entries = append(m.entries, ChatEntry{Kind: "system", Content: fmt.Sprintf("Exported conversation to: %s. Durability warning (do not retry blindly): %v", msg.Path, msg.Err)})
			} else if msg.Err != nil {
				m.entries = append(m.entries, ChatEntry{Kind: "error", Content: fmt.Sprintf("Export failed: %v", msg.Err)})
			} else {
				m.entries = append(m.entries, ChatEntry{Kind: "system", Content: fmt.Sprintf("Exported conversation to: %s", msg.Path)})
			}
			m.invalidateEntryCache()
			m.viewport.SetContent(m.renderEntries())
			m.gotoBottomIfFollowing()
		}
		m.appendShutdownQuit(&cmds)

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
		m.syncInputHeight()
		m.input.Focus()

	case DoneFlashExpiredMsg:
		m.doneFlash = false

	case SessionListMsg:
		if !m.sessionListing || msg.ListToken != m.sessionListToken {
			break
		}
		m.sessionListing = false
		if m.state != StateIdle {
			m.sessionsPickerState = nil
			if m.overlay == OverlaySessionsPicker {
				m.overlayParent = OverlayNone
				m.overlay = OverlayNone
			}
			break
		}
		if msg.Err != nil {
			m.sessionsPickerState = newSessionsMessageState(sessionsFailed, msg.Err.Error())
			m.overlay = OverlaySessionsPicker
		} else if len(msg.Sessions) == 0 {
			m.sessionsPickerState = newSessionsMessageState(sessionsEmpty, "")
			m.overlay = OverlaySessionsPicker
		} else {
			m.sessionsPickerState = newSessionsPickerState(msg.Sessions, m.width, m.height, m.isDark, m.reducedMotion)
			m.overlay = OverlaySessionsPicker
		}
		m.input.Blur()

	case SessionLoadedMsg:
		cmds = append(cmds, m.handleSessionLoadedReceipt(msg))
		m.appendShutdownQuit(&cmds)

	case tea.MouseWheelMsg:
		// Inline permission requests own wheel input just like document overlays,
		// but remain in normal layout flow so the transcript stays visible. Scroll
		// their bounded preview without moving or changing follow intent below it.
		if m.pendingApproval != nil {
			if m.approvalState != nil {
				m.approvalState.Viewport, _ = m.approvalState.Viewport.Update(msg)
			}
			return m, nil
		}
		// The external read-scope prompt is an authority-changing inline
		// decision. It is keyboard-first and owns pointer input just like an
		// approval, so the transcript cannot move behind it.
		if m.readScopePrompt != nil {
			return m, nil
		}
		// A visible overlay owns pointer input. Scroll document overlays through
		// their own Bubbles viewports and swallow wheel events for all other
		// overlays so the hidden transcript cannot move underneath a modal.
		if m.overlay != OverlayNone {
			switch m.overlay {
			case OverlayCortexDecision:
				if m.cortexDecision != nil {
					m.cortexDecision.detail, _ = m.cortexDecision.detail.Update(msg)
					m.cortexDecision.cacheValid = false
				}
			case OverlayHelp:
				m.helpViewport, _ = m.helpViewport.Update(msg)
			case OverlayRuntimeStatus:
				if m.runtimeStatusState != nil {
					m.runtimeStatusState.Viewport, _ = m.runtimeStatusState.Viewport.Update(msg)
				}
			case OverlayGoalInspector:
				if m.goalInspectorState != nil {
					m.goalInspectorState.updateViewport(msg)
				}
			case OverlayGoalRecovery:
				if m.goalRecoveryState != nil {
					_, _ = m.goalRecoveryState.Update(msg)
				}
			}
			return m, nil
		}

		beforeOffset := m.viewport.YOffset()
		m.viewport, _ = m.viewport.Update(msg)

		if m.viewport.AtBottom() {
			m.markFollowingLatest()
		} else if m.viewport.YOffset() != beforeOffset {
			m.pauseFollow()
		}
		return m, nil

	case tea.MouseClickMsg:
		// Modal and inline decision surfaces are intentionally keyboard-first.
		// Until a child explicitly owns pointer interaction, clicks are swallowed
		// rather than reaching ToolCards behind an authority-changing prompt.
		if m.overlay != OverlayNone || m.pendingApproval != nil || m.readScopePrompt != nil {
			return m, nil
		}
		if msg.Button == tea.MouseLeft {
			m.handleMouseClick(msg.X, msg.Y)
		}

	case tea.PasteMsg:
		if m.composerEditable() {
			draft := m.input.Value()
			cursor := pasteCursorAt(draft, m.input.Line(), m.input.Column())
			assessment := assessPaste(msg.Content, cursor, m.input.Length(), m.input.LineCount(), m.input.CharLimit)
			if !assessment.PlainFits || assessment.NeedsReview {
				m.pendingPaste = assessment
				m.recalcViewportHeight()
				// The parent owns the safety prompt. Do not forward this PasteMsg to
				// the textarea before the user chooses fenced or plain insertion.
				return m, nil
			}
			m.clearCompletionSuppression()
			m.input.InsertString(msg.Content)
			m.syncInputHeight()
			// The paste was inserted directly, so consume the message here instead
			// of letting the common child update insert it a second time.
			return m, m.reflowInputViewport()
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
			m.invalidateEntryCache()
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
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		cmds = append(cmds, cmd)

		// Auto-grow textarea based on content.
		m.syncInputHeight()

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

	beforeOffset := m.viewport.YOffset()
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	cmds = append(cmds, cmd)

	if keyMsg, ok := msg.(tea.KeyPressMsg); ok && m.transcriptScrollKey(keyMsg) {
		if m.viewport.AtBottom() {
			m.markFollowingLatest()
		} else if m.viewport.YOffset() != beforeOffset {
			m.pauseFollow()
		}
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

func (m *Model) snapshotExecutionCursor(ctx context.Context) (int64, error) {
	workspaceID, err := canonicalWorkspaceID(m.agent.WorkDir())
	if err != nil {
		return m.executionCursor, err
	}
	hazards, err := m.sessionStore.ListExecutionRecoveryHazards(ctx, m.sessionID, workspaceID, m.executionCursor, 100)
	if err != nil {
		return m.executionCursor, fmt.Errorf("inspect execution projection: %w", err)
	}
	messages := m.agent.Messages()
	for _, state := range hazards {
		if state.Latest.Type != execution.EventCompleted && state.Latest.Type != execution.EventFailed {
			return m.executionCursor, fmt.Errorf(
				"execution %s remains %s/%s and cannot cross the snapshot boundary",
				state.Identity.ExecutionID, state.Latest.Type, state.Identity.EffectClass,
			)
		}
		projected := false
		for _, message := range messages {
			resultContent := message.Content
			if message.DurableContent != "" {
				resultContent = message.DurableContent
			}
			if message.Role == "tool" &&
				message.ToolCallID == state.Identity.CanonicalCallID &&
				execution.HashText(resultContent) == state.Latest.ResultSHA256 {
				projected = true
				break
			}
		}
		if !projected {
			return m.executionCursor, fmt.Errorf("%s effect %s is absent from the session snapshot", state.Latest.Type, state.Identity.ExecutionID)
		}
	}
	latest, err := m.sessionStore.LatestExecutionEventID(ctx, m.sessionID, workspaceID)
	if err != nil {
		return m.executionCursor, fmt.Errorf("read execution cursor: %w", err)
	}
	return latest, nil
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

// pushHistory appends text to history, deduplicating consecutive entries, capping at 100.
func (m *Model) pushHistory(text string) {
	if text == "" {
		return
	}
	// Dedup consecutive
	if len(m.promptHistory) > 0 && m.promptHistory[len(m.promptHistory)-1] == text {
		return
	}
	m.promptHistory = append(m.promptHistory, text)
	if len(m.promptHistory) > 100 {
		m.promptHistory = m.promptHistory[len(m.promptHistory)-100:]
	}
	m.historyIndex = -1
}

// navigateHistory moves through history. dir=-1 = older (up), dir=1 = newer (down).
// Returns true if navigation happened.
func (m *Model) navigateHistory(dir int) bool {
	if len(m.promptHistory) == 0 {
		return false
	}

	if dir == -1 { // Up - go to older
		if m.historyIndex == -1 {
			// First time pressing up: save current input and go to newest history
			m.historySaved = m.input.Value()
			m.historyIndex = len(m.promptHistory) - 1
		} else if m.historyIndex > 0 {
			m.historyIndex--
		} else {
			return false // already at oldest
		}
		m.clearCompletionSuppression()
		m.input.SetValue(m.promptHistory[m.historyIndex])
		m.input.CursorEnd()
		return true
	}

	if dir == 1 { // Down - go to newer
		if m.historyIndex == -1 {
			return false // not browsing
		}
		if m.historyIndex < len(m.promptHistory)-1 {
			m.historyIndex++
			m.clearCompletionSuppression()
			m.input.SetValue(m.promptHistory[m.historyIndex])
			m.input.CursorEnd()
		} else {
			// Past newest: restore saved input
			m.historyIndex = -1
			m.clearCompletionSuppression()
			m.input.SetValue(m.historySaved)
			m.input.CursorEnd()
		}
		return true
	}

	return false
}

// submitInput takes the current input, handles slash commands, or starts the agent.
func (m *Model) submitInput() tea.Cmd {
	text := strings.TrimSpace(m.input.Value())
	if text == "" {
		return nil
	}
	if m.readScopeOpRunning || m.readScopePrompt != nil {
		return nil
	}
	// An ordinary outcome-unknown execution owns the next safety decision. Do
	// not send the same (or a new) prompt back through Agent just to rediscover
	// the durable latch and render another error. Keep the draft visible and
	// explain the explicit /recover action; silently replacing Enter with a
	// five-step wizard makes an ordinary question look swallowed.
	if m.standaloneRecovery != nil && m.goalRuntime == nil && !strings.HasPrefix(text, "/") {
		m.remindStandaloneRecoveryDraftPreserved()
		return nil
	}
	// A durable Goal Runtime exclusively owns agent turns until it is dropped or
	// the conversation is reset. Keep ordinary drafts intact and route the user
	// to the inspector instead of starting an unbounded side turn.
	if m.goalRuntime != nil && !strings.HasPrefix(text, "/") {
		return m.rejectPromptWhileGoalAttached(text, false)
	}
	if !strings.HasPrefix(text, "/") {
		if cmd, started := m.beginPromptPathPreflight(text); started {
			return cmd
		}
	}
	return m.submitPreparedInput(text)
}

// submitPreparedInput consumes a draft after host preflight has either found
// no new authority or committed the exact approved grants. It deliberately
// does not invoke path preflight again, which makes auto-resume single-shot.
func (m *Model) submitPreparedInput(text string) tea.Cmd {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	m.pushHistory(text)

	m.clearCompletionSuppression()
	m.input.Reset()
	m.input.SetHeight(1)

	// Handle slash commands.
	if strings.HasPrefix(text, "/") {
		name, args, err := parseSlashCommandInput(text)
		if err != nil {
			m.entries = append(m.entries, ChatEntry{Kind: "error", Content: fmt.Sprintf("command parse error: %v", err)})
			m.viewport.SetContent(m.renderEntries())
			m.resumeFollow()
			return nil
		}

		ctx := m.buildCommandContext()
		result := m.cmdRegistry.Execute(ctx, name, args)

		// Handle command result.
		if result.Error != "" {
			m.entries = append(m.entries, ChatEntry{
				Kind:    "error",
				Content: result.Error,
			})
			m.viewport.SetContent(m.renderEntries())
			m.resumeFollow()
			return nil
		}

		return m.handleCommandActionWithDraft(result, text)
	}

	// Every conversational preset sends the draft immediately. PLAN applies its
	// read-only tool policy in sendToAgentTurnPresented; AUTO applies its normal
	// routing and approval policy. Durable work remains an explicit /goal flow.
	return m.sendToAgent(text)
}

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
		return m.showGoal()

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

func (m *Model) resetConversationSession() {
	m.revokeOllamaCloudConsent()
	if m.goalOperationCancel != nil {
		m.goalOperationCancel()
	}
	m.goalOperationCancel = nil
	m.goalOperation = ""
	m.goalOperationRunning = false
	m.cortexDecision = nil
	m.cortexDecisionOp = nil
	m.cortexDecisionAttempt = nil
	m.cortexDecisionGen++
	m.standaloneRecovery = nil
	m.goalOperationToken++
	m.goalRuntime = nil
	m.syncComposerAuthority()
	m.goalFormState = nil
	m.goalInspectorState = nil
	m.resetGoalRecoveryPresentation()
	m.goalTurnID = ""
	m.goalTurnToolCalls = 0
	m.goalTurnSuccesses = 0
	m.goalNeedsEvaluation = false
	m.goalPersistenceDirty = false
	m.cancelSessionLoad()
	m.cancelSessionList()
	m.sessionID = 0
	m.executionCursor = 0
	m.resetSessionStateRevision()
	_ = m.releaseExecutionSessionLease()
	if m.agent != nil {
		m.agent.SetCheckpointSessionID(0)
		m.agent.SetExecutionSessionID(0)
		m.agent.SetExecutionSnapshotCursor(0)
	}
	m.sessionEvalTotal = 0
	m.sessionPromptTotal = 0
	m.sessionTurnCount = 0
	m.resetTurnDiagnostics()
	m.fileChanges = nil
	m.toolsPending = 0
	m.toolCardMgr.Cards = nil
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
}

// ReleaseExecutionSessionLease releases the cross-process ownership held by
// the active interactive session. The main program calls it after Bubble Tea
// has joined the current turn and before SQLite closes.
func (m *Model) ReleaseExecutionSessionLease() error {
	return m.releaseExecutionSessionLease()
}

func (m *Model) releaseExecutionSessionLease() error {
	if m.executionLease == nil {
		return nil
	}
	lease := m.executionLease
	m.executionLease = nil
	return lease.Close()
}

func (m *Model) cancelSessionLoad() {
	if m.sessionLoadCancel != nil {
		m.sessionLoadCancel()
		m.sessionLoadCancel = nil
	}
	if m.sessionLoading {
		m.sessionLoadToken++
	}
	m.sessionLoading = false
	if !m.sessionListing {
		m.input.Focus()
	}
}

func (m *Model) cancelSessionLoadForShutdown() {
	if m.sessionLoadCancel != nil {
		m.sessionLoadCancel()
		m.sessionLoadCancel = nil
		return
	}
	// Tests and embedders can mark a synthetic load without installing an
	// owned command. There is nothing to join in that case.
	m.sessionLoading = false
}

func (m *Model) cancelSessionList() {
	if m.sessionListing {
		m.sessionListToken++
	}
	m.sessionListing = false
	if !m.sessionLoading {
		m.input.Focus()
	}
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
	if status := m.renderStatusLine(); status != "" {
		statusRows := lipgloss.Height(status)
		height += statusRows
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
		return height + m.inputLines
	}
	return height + 1
}

// syncInputHeight adjusts textarea height to match content (1-5 lines)
// and recalculates viewport if the height changed.
func (m *Model) syncInputHeight() {
	lines := m.input.LineCount()
	if lines < 1 {
		lines = 1
	}
	if lines > 5 {
		lines = 5
	}
	if lines != m.inputLines {
		m.inputLines = lines
		m.input.SetHeight(lines)
		m.recalcViewportHeight()
	}
}

// reflowInputViewport lets Bubbles populate its internal viewport with content
// inserted directly by the parent before it repositions around the preserved
// cursor. Without this no-op child update, a large accepted paste can clamp its
// five-row viewport against stale pre-paste content and hide the closing rows.
func (m *Model) reflowInputViewport() tea.Cmd {
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(inputViewportReflowMsg{})
	return cmd
}

type inputViewportReflowMsg struct{}

// invalidateEntryCache marks the incremental entry render cache as stale,
// forcing a full re-render on the next renderEntries() call.
func (m *Model) invalidateEntryCache() {
	m.entryCacheValid = false
	m.cachedEntriesRender = ""
	m.cachedEntryCount = 0
	m.cachedToolHitRegions = nil
}

// checkAutoScroll resets scroll anchor when the viewport is at the bottom,
// allowing auto-scroll to resume during streaming.
func (m *Model) checkAutoScroll() {
	if m.viewport.AtBottom() {
		m.markFollowingLatest()
	}
}

// openExternalEditor opens $EDITOR with the current input text, then replaces
// the textarea content with whatever the user wrote. tea.ExecProcess owns this
// interactive child synchronously: Bubble Tea cannot process a normal quit or
// restore the terminal until the editor callback returns. Keep interactive
// processes on this path rather than dispatching them as unjoined tea.Cmd work.
func (m *Model) openExternalEditor() tea.Cmd {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}

	// Write current input to a temp file.
	tmpFile, err := os.CreateTemp("", "local-agent-*.md")
	if err != nil {
		return func() tea.Msg {
			return ErrorMsg{Msg: fmt.Sprintf("editor: %v", err)}
		}
	}
	tmpPath := tmpFile.Name()
	if current := m.input.Value(); current != "" {
		if _, err := tmpFile.WriteString(current); err != nil {
			_ = tmpFile.Close()
			_ = os.Remove(tmpPath)
			return func() tea.Msg {
				return ErrorMsg{Msg: fmt.Sprintf("editor: %v", err)}
			}
		}
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return func() tea.Msg {
			return ErrorMsg{Msg: fmt.Sprintf("editor: %v", err)}
		}
	}

	c := exec.Command(editor, tmpPath)
	return tea.ExecProcess(c, func(err error) tea.Msg {
		defer func() { _ = os.Remove(tmpPath) }()
		if err != nil {
			return ErrorMsg{Msg: fmt.Sprintf("editor: %v", err)}
		}
		data, err := os.ReadFile(tmpPath)
		if err != nil {
			return ErrorMsg{Msg: fmt.Sprintf("editor: %v", err)}
		}
		content := strings.TrimRight(string(data), "\n")
		if content == "" {
			return nil
		}
		return editorReturnMsg{Content: content}
	})
}

// editorReturnMsg is sent when the external editor closes.
type editorReturnMsg struct {
	Content string
}

// recalcViewportHeight updates the viewport height based on current footer size.
func (m *Model) recalcViewportHeight() {
	if !m.ready || m.height == 0 {
		return
	}
	m.viewport.SetHeight(m.viewportHeight())
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

// sendToAgent sends a message to the agent, setting mode context first.
func (m *Model) sendToAgent(text string) tea.Cmd {
	if m.goalRuntime != nil {
		return m.rejectPromptWhileGoalAttached(text, true)
	}
	turnID, err := execution.NewTurnID()
	if err != nil {
		return m.failTurnBeforeRun(text, fmt.Sprintf("Create turn identity: %v", err))
	}
	return m.sendToAgentTurn(text, turnID)
}

// sendToAgentTurn dispatches a message under an already-reserved identity.
// Goal continuation permits are consumed before this call, so replacing the
// ID here would sever crash recovery from the execution ledger.
func (m *Model) sendToAgentTurn(text, turnID string) tea.Cmd {
	return m.sendToAgentTurnPresentedWithMode(text, turnID, true, agent.TurnLimits{}, m.mode)
}

func (m *Model) sendGoalToAgentTurn(text, turnID string, limits agent.TurnLimits) tea.Cmd {
	// A durable Goal Runtime owns its execution authority independently from the
	// conversational mode selector. Shift+Tab may prepare the user's eventual
	// post-goal mode, but it must never downgrade or otherwise mutate an already
	// admitted goal turn's tool contract.
	return m.sendToAgentTurnPresentedWithMode(text, turnID, false, limits, ModeAuto)
}

func (m *Model) sendGoalToAgentTurnWithCapability(text, turnID string, limits agent.TurnLimits, capability agent.CapabilityActivity) tea.Cmd {
	return m.sendToAgentTurnPresentedWithCapability(text, turnID, false, limits, ModeAuto, capability)
}

func (m *Model) sendToAgentTurnPresentedWithMode(text, turnID string, visible bool, limits agent.TurnLimits, authority Mode) tea.Cmd {
	return m.sendToAgentTurnPresentedWithCapability(text, turnID, visible, limits, authority, agent.CapabilityActivity{})
}

func (m *Model) sendToAgentTurnPresentedWithCapability(text, turnID string, visible bool, limits agent.TurnLimits, authority Mode, capability agent.CapabilityActivity) tea.Cmd {
	m.cancelSessionLoad()
	m.cancelSessionList()
	messagesBeforeTurn := m.agent.Messages()
	m.turnMessagesBefore = append([]llm.Message(nil), messagesBeforeTurn...)
	m.turnPrompt = text
	m.turnPromptVisible = visible
	m.turnCheckpointSet = true
	createdSession := false
	if authority < ModeNormal || authority > ModeAuto {
		authority = ModeNormal
	}
	cfg := m.modeConfigs[authority]
	if m.logger != nil {
		m.logger.Info("user message", "mode", cfg.Label, "length", len(text))
	}

	m.resumeFollow()
	m.state = StateWaiting
	// Ordinary active turns keep the Bubbles textarea focused so real terminal
	// key events can draft and queue a follow-up. Rendering an editable-looking
	// composer while the child is blurred makes the queue affordance inert.
	// Goal-owned turns still reject child updates through composerEditable.
	m.input.Focus()
	m.turnStartedAt = m.nowTime()
	m.resetTurnDiagnostics()
	m.recalcViewportHeight()
	m.streamBuf.Reset()

	if visible {
		m.entries = append(m.entries, ChatEntry{
			Kind:    "user",
			Content: text,
		})
	}
	m.viewport.SetContent(m.renderEntries())
	m.gotoBottomIfFollowing()

	var sessionErr error
	createdSession, sessionErr = m.ensureExecutionSession(text, cfg.Label)
	if sessionErr != nil {
		return m.failPresentedTurnBeforeRun(text, sessionErr.Error(), visible)
	}

	// Set mode context on the agent.
	m.setRouterMode(cfg.RouterMode)
	if !m.modelPinned && m.router != nil && m.modelManager != nil {
		if newModel := m.router.SelectModelForMode(text, cfg.RouterMode); newModel != "" && newModel != m.model {
			m.prepareModelSwitch()
			if err := m.modelManager.SetCurrentModel(newModel); err == nil {
				m.setCurrentModelProjection(newModel)
			} else {
				m.entries = append(m.entries, ChatEntry{
					Kind:    "error",
					Content: fmt.Sprintf("Failed to switch routed model: %v", err),
				})
				m.viewport.SetContent(m.renderEntries())
				m.gotoBottomIfFollowing()
			}
		}
	}
	m.agent.AddUserMessage(text)
	m.agent.SetModeContext(cfg.SystemPromptPrefix, cfg.ToolPolicy)
	m.agent.SetAuthorityMode(agentAuthorityMode(authority))
	if m.sessionID > 0 && m.sessionStore != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err := m.persistSessionState(ctx)
		cancel()
		if err != nil {
			m.agent.ReplaceMessages(messagesBeforeTurn)
			if createdSession {
				leaseErr := m.releaseExecutionSessionLease()
				cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), time.Second)
				cleanupErr := m.sessionStore.DeleteSession(cleanupCtx, m.sessionID)
				cancelCleanup()
				m.sessionID = 0
				m.executionCursor = 0
				m.resetSessionStateRevision()
				m.agent.SetCheckpointSessionID(0)
				m.agent.SetExecutionSessionID(0)
				m.agent.SetExecutionSnapshotCursor(0)
				if cleanupFailure := errors.Join(leaseErr, cleanupErr); cleanupFailure != nil {
					return m.failPresentedTurnBeforeRun(text, fmt.Sprintf("Save session: %v (cleanup: %v)", err, cleanupFailure), visible)
				}
			}
			return m.failPresentedTurnBeforeRun(text, fmt.Sprintf("Save session: %v", err), visible)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel

	p := m.program

	// Set up the approval callback so tool permission prompts go through the TUI.
	m.agent.SetApprovalCallback(func(req permission.ApprovalRequest) {
		p.Send(ToolApprovalMsg{
			RequestID:       req.RequestID,
			ToolName:        req.ToolName,
			Args:            req.Args,
			ArgumentsSHA256: req.ArgumentsSHA256,
			Preview:         req.Preview,
			Scope:           req.Scope,
			Response:        req.Response,
		})
	})

	runAgent := func() tea.Msg {
		adapter := NewAdapter(p, m.agent.WorkDir())
		err := m.agent.RunTurnWithOptions(ctx, adapter, turnID, agent.TurnOptions{
			Limits: limits, Capability: capability,
		})
		return AgentDoneMsg{TurnID: turnID, Err: err}
	}

	m.scramble.Reset()
	if m.reducedMotion {
		return runAgent
	}
	return tea.Batch(m.scramble.Tick(), runAgent)
}

func (m *Model) failPresentedTurnBeforeRun(text, message string, visible bool) tea.Cmd {
	m.clearTurnMessageCheckpoint()
	if visible {
		return m.failTurnBeforeRun(text, message)
	}
	m.entries = append(m.entries, ChatEntry{Kind: "error", Content: message})
	m.state = StateIdle
	m.input.Focus()
	m.syncInputHeight()
	m.recalcViewportHeight()
	m.invalidateEntryCache()
	m.viewport.SetContent(m.renderEntries())
	m.resumeFollow()
	return nil
}

func (m *Model) rollbackPreflightRejectedPrompt() bool {
	if m == nil || m.agent == nil || !m.turnCheckpointSet {
		return false
	}
	current := m.agent.Messages()
	before := m.turnMessagesBefore
	if len(current) != len(before)+1 || !reflect.DeepEqual(current[:len(before)], before) {
		return false
	}
	last := current[len(current)-1]
	if last.Role != "user" || last.Content != m.turnPrompt || len(last.ToolCalls) != 0 || last.ToolName != "" || last.ToolCallID != "" {
		return false
	}
	m.agent.ReplaceMessages(append([]llm.Message(nil), before...))
	if m.turnPromptVisible {
		if index := len(m.entries) - 1; index >= 0 && m.entries[index].Kind == "user" && m.entries[index].Content == m.turnPrompt {
			m.entries = m.entries[:index]
		}
		m.input.SetValue(m.turnPrompt)
		m.input.CursorEnd()
		m.invalidateEntryCache()
	}
	return true
}

func (m *Model) clearTurnMessageCheckpoint() {
	if m == nil {
		return
	}
	m.turnMessagesBefore = nil
	m.turnPrompt = ""
	m.turnPromptVisible = false
	m.turnCheckpointSet = false
}

// ensureExecutionSession creates or reacquires the durable session boundary
// before a turn (or a Goal Runtime) can own work. Keeping this operation
// separate lets explicit goal creation bind Cortex and persist its state before
// the first provider command is dispatched.
func (m *Model) ensureExecutionSession(title, modeLabel string) (bool, error) {
	if m.sessionStore == nil {
		return false, nil
	}
	if m.sessionID > 0 {
		if m.executionLease != nil {
			return false, nil
		}
		workspaceID, err := canonicalWorkspaceID(m.agent.WorkDir())
		if err != nil {
			return false, fmt.Errorf("lock session: %w", err)
		}
		lease, err := m.sessionStore.AcquireExecutionSessionLease(context.Background(), m.sessionID, workspaceID)
		if err != nil {
			return false, fmt.Errorf("lock session: %w", err)
		}
		m.executionLease = lease
		return false, nil
	}
	m.resetSessionStateRevision()

	workspaceID, err := canonicalWorkspaceID(m.agent.WorkDir())
	if err != nil {
		return false, fmt.Errorf("create session: %w", err)
	}
	session, err := m.sessionStore.CreateSession(context.Background(), db.CreateSessionParams{
		Title: sessionTitle(title), Model: m.model, Mode: modeLabel, WorkspaceID: workspaceID,
	})
	if err != nil {
		return false, fmt.Errorf("create session: %w", err)
	}
	lease, leaseErr := m.sessionStore.AcquireExecutionSessionLease(context.Background(), session.ID, session.WorkspaceID)
	if leaseErr != nil {
		cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), time.Second)
		cleanupErr := m.sessionStore.DeleteSession(cleanupCtx, session.ID)
		cancelCleanup()
		if cleanupErr != nil {
			leaseErr = errors.Join(leaseErr, fmt.Errorf("cleanup session: %w", cleanupErr))
		}
		return false, fmt.Errorf("lock session: %w", leaseErr)
	}
	m.sessionID = session.ID
	m.executionCursor = 0
	m.executionLease = lease
	if err := m.initializeSessionStateRevision(0); err != nil {
		return false, fmt.Errorf("initialize session state revision: %w", err)
	}
	m.agent.SetCheckpointSessionID(session.ID)
	m.agent.SetExecutionSessionID(session.ID)
	m.agent.SetExecutionSnapshotCursor(0)
	return true, nil
}

func (m *Model) failTurnBeforeRun(text, message string) tea.Cmd {
	if last := len(m.entries) - 1; last >= 0 && m.entries[last].Kind == "user" && m.entries[last].Content == text {
		m.entries = m.entries[:last]
	}
	m.entries = append(m.entries, ChatEntry{Kind: "error", Content: message})
	m.state = StateIdle
	m.input.SetValue(text)
	m.input.CursorEnd()
	m.input.Focus()
	m.syncInputHeight()
	m.recalcViewportHeight()
	m.invalidateEntryCache()
	m.viewport.SetContent(m.renderEntries())
	m.resumeFollow()
	return nil
}

// cycleMode advances through NORMAL -> PLAN -> AUTO -> NORMAL.
func (m *Model) cycleMode() {
	m.setMode((m.mode + 1) % 3)
}

// setMode commits one mode transition. Picker navigation never calls this;
// the route, model, and durable transcript change only on selection.
func (m *Model) setMode(mode Mode) {
	if mode < ModeNormal || mode > ModeAuto || mode == m.mode {
		return
	}
	hadConversation := m.conversationStarted()
	m.mode = mode
	ambientConfig := m.modeConfigs[mode]
	m.syncComposerAuthority()
	// With a Goal Runtime attached, Shift+Tab only prepares the ambient mode for
	// work after the goal. The active router/model authority remains AUTO, just
	// like the rail and footer. Otherwise a visible AUTO goal could silently
	// inherit PLAN routing until its next continuation reasserted authority.
	authorityConfig := m.modeConfigs[m.presentedMode()]
	m.setRouterMode(authorityConfig.RouterMode)

	// Auto-select model via router.
	if !m.modelPinned && m.router != nil {
		newModel := m.router.GetModelForCapability(authorityConfig.PreferredCapability)
		if newModel != "" && newModel != m.model {
			if m.modelManager != nil {
				m.prepareModelSwitch()
				if err := m.modelManager.SetCurrentModel(newModel); err == nil {
					m.setCurrentModelProjection(newModel)
				}
			}
		}
	}

	if m.logger != nil {
		m.logger.Info("mode switched", "mode", ambientConfig.Label, "authority", authorityConfig.Label, "model", m.model)
	}

	// The empty-state orientation already owns mode and model. Once a real
	// conversation exists, retain a compact durable receipt for the transition.
	if hadConversation {
		receipt := "Mode · " + ambientConfig.Label
		if m.goalRuntime != nil {
			receipt = "After goal · " + ambientConfig.Label + " · active goal · AUTO"
		}
		if m.model != "" {
			receipt += " · " + m.model
		}
		m.entries = append(m.entries, ChatEntry{Kind: "system", Content: receipt})
	}
	m.viewport.SetContent(m.renderEntries())
	m.resumeFollow()
	if m.overlay == OverlaySettings && m.settingsPickerState != nil {
		// Mode picker selection returns to Settings before this transition is
		// committed. Refresh again so the visible row never reports the mode we
		// just left.
		m.refreshSettingsPicker()
	}
}

// openModelPicker shows the model picker overlay.
func (m *Model) openModelPicker() {
	if m.router == nil {
		return
	}
	if len(m.ollamaModels) > 0 {
		m.modelPickerState = newOllamaModelPickerState(m.ollamaModels, m.model, m.width, m.height, m.isDark, m.reducedMotion)
		if m.ollamaVersion != "" {
			m.modelPickerState.List.Title = ollamaModelPickerTitle(m.ollamaVersion)
		}
		m.overlay = OverlayModelPicker
		m.input.Blur()
		return
	}
	if m.ollamaInventoryAttempted {
		m.modelPickerState = newOllamaModelPickerState(nil, m.model, m.width, m.height, m.isDark, m.reducedMotion)
		if m.ollamaVersion != "" {
			m.modelPickerState.List.Title = ollamaModelPickerTitle(m.ollamaVersion)
		}
		m.overlay = OverlayModelPicker
		m.input.Blur()
		return
	}
	catalog := m.router.ListModels()
	byName := make(map[string]config.Model, len(catalog))
	for _, model := range catalog {
		byName[model.Name] = model
	}
	models := catalog
	if len(m.modelList) > 0 {
		models = make([]config.Model, 0, len(m.modelList))
		for _, name := range m.modelList {
			if model, ok := byName[name]; ok {
				models = append(models, model)
			} else {
				models = append(models, config.Model{
					Name: name, DisplayName: name, Size: "local", Capability: config.CapabilityMedium,
				})
			}
		}
	}
	if len(models) == 0 {
		return
	}

	m.modelPickerState = newModelPickerState(models, m.model, m.width, m.height, m.isDark, m.reducedMotion)
	m.overlay = OverlayModelPicker
	m.input.Blur()
}

// selectModel switches to the given model and closes the picker.
func (m *Model) selectModel(name string) {
	if descriptor, ok := m.ollamaModelDescriptor(name); ok {
		if !descriptor.Selectable || !descriptor.Fit {
			reason := descriptor.Reason
			if reason == "" {
				reason = "model is not admitted by the current Ollama policy"
			}
			m.entries = append(m.entries, ChatEntry{Kind: "error", Content: reason})
			m.closeModelPicker()
			m.viewport.SetContent(m.renderEntries())
			m.resumeFollow()
			return
		}
		if descriptor.RequiresConsent && !descriptor.ConsentGranted {
			m.openCloudConsent(descriptor)
			return
		}
	} else if err := config.CheckModelMemorySafe(name); err != nil {
		m.entries = append(m.entries, ChatEntry{Kind: "error", Content: err.Error()})
		m.closeModelPicker()
		m.viewport.SetContent(m.renderEntries())
		m.resumeFollow()
		return
	}
	m.switchSelectedModel(name)
}

// switchSelectedModel commits a model switch after all admission and consent
// checks have succeeded. Ollama Cloud grants remain exact and session-scoped.
func (m *Model) switchSelectedModel(name string) bool {
	old := m.model
	if config.CanonicalModelName(old) == config.CanonicalModelName(name) && strings.TrimSpace(old) != "" {
		// Selecting the active model is idempotent. This also absorbs duplicate
		// Enter/delivery events without re-preparing the provider or stacking
		// identical `Model` receipts in the transcript.
		m.modelPinned = true
		for index := range m.ollamaModels {
			m.ollamaModels[index].Current = config.CanonicalModelName(m.ollamaModels[index].Name) == config.CanonicalModelName(name)
		}
		m.cloudConsentState = nil
		m.closeModelPicker()
		m.viewport.SetContent(m.renderEntries())
		m.resumeFollow()
		return true
	}
	if m.modelManager != nil {
		m.prepareModelSwitch()
		if err := m.modelManager.SetCurrentModel(name); err != nil {
			if descriptor, ok := m.ollamaModelDescriptor(name); ok && descriptor.ConsentGranted {
				m.modelManager.RevokeOllamaCloudModel(name)
				m.setCloudConsentProjection(name, false)
			}
			if m.overlay == OverlayCloudConsent && m.cloudConsentState != nil {
				m.cloudConsentState.Error = fmt.Sprintf("Could not switch: %v", err)
				return false
			}
			m.entries = append(m.entries, ChatEntry{
				Kind:    "error",
				Content: fmt.Sprintf("Failed to switch model: %v", err),
			})
			m.closeModelPicker()
			return false
		}
	}
	m.setCurrentModelProjection(name)
	m.ollamaOffline = false
	m.modelPinned = true
	for index := range m.ollamaModels {
		m.ollamaModels[index].Current = config.CanonicalModelName(m.ollamaModels[index].Name) == config.CanonicalModelName(name)
	}
	if m.logger != nil {
		m.logger.Info("model switched", "from", old, "to", name)
	}
	// Empty state and the fixed status line already own the current model. Once
	// a conversation exists, retain one compact transition receipt.
	if m.conversationStarted() {
		m.entries = append(m.entries, ChatEntry{Kind: "system", Content: "Model · " + m.currentModelSurfaceLabel(false)})
	}
	m.cloudConsentState = nil
	m.closeModelPicker()
	m.viewport.SetContent(m.renderEntries())
	m.resumeFollow()
	return true
}

func (m *Model) ollamaModelDescriptor(name string) (OllamaModelDescriptor, bool) {
	wanted := config.CanonicalModelName(name)
	for _, descriptor := range m.ollamaModels {
		if config.CanonicalModelName(descriptor.Name) == wanted {
			return descriptor, true
		}
	}
	return OllamaModelDescriptor{}, false
}

func (m *Model) validateModelAdmission(name string) error {
	if descriptor, ok := m.ollamaModelDescriptor(name); ok {
		if descriptor.Source == OllamaModelCloud && m.localOnly && !descriptor.ConsentGranted {
			return fmt.Errorf("model %q requires Ollama Cloud confirmation for this conversation", name)
		}
		if descriptor.Selectable && descriptor.Fit {
			return nil
		}
		if descriptor.Reason != "" {
			return errors.New(descriptor.Reason)
		}
		return fmt.Errorf("model %q is not admitted by the current Ollama policy", name)
	}
	if m.ollamaInventoryAttempted {
		return fmt.Errorf("model %q is absent from the current Ollama inventory", name)
	}
	return config.CheckModelMemorySafe(name)
}

// closeModelPicker dismisses the model picker overlay.
func (m *Model) closeModelPicker() {
	m.modelPickerState = nil
	m.closeOverlayToParent()
}

// openPlanForm gives the inline Plan form temporary composer ownership.
func (m *Model) openPlanForm(task string) {
	anchor := m.captureInlineFormTranscriptAnchor()
	if !m.prepareInlineFormOpen() {
		return
	}
	m.planFormState = NewPlanFormState(task, m.isDark, m.reducedMotion)
	m.restylePickerOverlays()
	m.overlay = OverlayPlanForm
	m.input.Blur()
	m.refreshInlineFormLayout(anchor)
}

// closePlanForm releases composer ownership without changing its saved draft.
func (m *Model) closePlanForm() {
	anchor := m.captureInlineFormTranscriptAnchor()
	m.planFormState = nil
	if m.overlay == OverlayPlanForm {
		m.overlay = OverlayNone
	}
	if m.composerEditable() {
		m.input.Focus()
	} else {
		m.input.Blur()
	}
	m.refreshInlineFormLayout(anchor)
}

// submitPlanFormPrompt sends the assembled plan prompt to the agent.
func (m *Model) submitPlanFormPrompt(prompt string) tea.Cmd {
	return m.sendToAgent(prompt)
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

// handleMouseClick hit-tests tool entries and toggles their collapsed state.
func (m *Model) handleMouseClick(x, y int) {
	if x < 0 || x >= m.viewport.Width() || y < 0 || y >= m.viewport.Height() {
		return
	}
	// The viewport starts at terminal row zero in the sidebar-free layout.
	vpY := y + m.viewport.YOffset()
	for _, region := range m.toolHitRegions {
		if vpY == region.Row && x < region.EndCol {
			if region.ToolIndex >= 0 && region.ToolIndex < len(m.toolEntries) {
				m.toolEntries[region.ToolIndex].Collapsed = !m.toolEntries[region.ToolIndex].Collapsed
				m.invalidateEntryCache()
				m.viewport.SetContent(m.renderEntries())
			}
			return
		}
	}
}

// formatConversationForExport formats the current conversation as markdown.
func (m *Model) formatConversationForExport() string {
	var b strings.Builder
	b.WriteString("# Conversation Export\n\n")
	fmt.Fprintf(&b, "**Date**: %s\n", time.Now().Format("2006-01-02 15:04"))
	fmt.Fprintf(&b, "**Model**: %s\n", m.model)
	portable := portableConversationExport{Version: 2}
	for _, entry := range m.entries {
		switch entry.Kind {
		case "user", "assistant", "system":
			portable.Entries = append(portable.Entries, portableConversationEntry{Kind: entry.Kind, Content: entry.Content})
		}
	}
	if payload, err := json.Marshal(portable); err == nil {
		b.WriteString("<!-- local-agent-export-v2:")
		b.WriteString(base64.RawStdEncoding.EncodeToString(payload))
		b.WriteString(" -->\n")
	}
	b.WriteString("---\n\n")

	for _, entry := range m.entries {
		switch entry.Kind {
		case "user":
			b.WriteString("## User\n\n")
			b.WriteString(entry.Content)
			b.WriteString("\n\n---\n\n")
		case "assistant":
			b.WriteString("## Assistant\n\n")
			b.WriteString(entry.Content)
			b.WriteString("\n\n---\n\n")
		case "system":
			b.WriteString("## System\n\n")
			b.WriteString(entry.Content)
			b.WriteString("\n\n---\n\n")
		case "tool_group":
			if entry.ToolIndex >= 0 && entry.ToolIndex < len(m.toolEntries) {
				te := m.toolEntries[entry.ToolIndex]
				fmt.Fprintf(&b, "## Tool: %s\n\n", te.Name)
				b.WriteString("```\n")
				b.WriteString(te.Args)
				b.WriteString("\n```\n\n")
				if te.Result != "" {
					b.WriteString("**Result**:\n\n")
					b.WriteString("```\n")
					b.WriteString(te.Result)
					b.WriteString("\n```\n\n")
				}
				b.WriteString("---\n\n")
			}
		}
	}

	return b.String()
}

type portableConversationExport struct {
	Version int                         `json:"version"`
	Entries []portableConversationEntry `json:"entries"`
}

type portableConversationEntry struct {
	Kind    string `json:"kind"`
	Content string `json:"content"`
}

const maxPortableConversationEntries = 10_000

// parseImportedConversation reads only the typed v2 payload embedded in a
// human-readable Markdown export. Legacy Markdown is inherently ambiguous:
// model/tool content can contain role-looking headings, so guessing authority
// from headings would enable a tool receipt to become a hidden user message.
func (m *Model) parseImportedConversation(data string) ([]ChatEntry, error) {
	return parseImportedConversationData(data)
}

func parseImportedConversationData(data string) ([]ChatEntry, error) {
	const marker = "<!-- local-agent-export-v2:"
	start := strings.Index(data, marker)
	if start < 0 {
		return nil, fmt.Errorf("legacy Markdown imports are disabled because role headings inside model/tool output are ambiguous; import a v2 file created by this release")
	}
	encoded := data[start+len(marker):]
	end := strings.Index(encoded, " -->")
	if end < 0 {
		return nil, fmt.Errorf("v2 export payload is not terminated")
	}
	payload, err := base64.RawStdEncoding.DecodeString(strings.TrimSpace(encoded[:end]))
	if err != nil {
		return nil, fmt.Errorf("decode v2 export payload: %w", err)
	}
	var portable portableConversationExport
	if err := json.Unmarshal(payload, &portable); err != nil {
		return nil, fmt.Errorf("decode v2 conversation: %w", err)
	}
	if portable.Version != 2 {
		return nil, fmt.Errorf("unsupported conversation export version %d", portable.Version)
	}
	if len(portable.Entries) == 0 || len(portable.Entries) > maxPortableConversationEntries {
		return nil, fmt.Errorf("v2 conversation contains %d entries", len(portable.Entries))
	}
	entries := make([]ChatEntry, 0, len(portable.Entries))
	for _, entry := range portable.Entries {
		switch entry.Kind {
		case "user", "assistant", "system":
		default:
			return nil, fmt.Errorf("v2 conversation contains unsupported entry kind %q", entry.Kind)
		}
		if strings.TrimSpace(entry.Content) != "" {
			entries = append(entries, ChatEntry{Kind: entry.Kind, Content: entry.Content})
		}
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("v2 conversation contains no visible entries")
	}
	return entries, nil
}

func importedConversationMessages(entries []ChatEntry) ([]llm.Message, int, error) {
	messages := make([]llm.Message, 0, len(entries))
	uiOnlySections := 0
	for _, entry := range entries {
		switch entry.Kind {
		case "user", "assistant":
			if strings.TrimSpace(entry.Content) != "" {
				messages = append(messages, llm.Message{Role: entry.Kind, Content: entry.Content})
			}
		case "system":
			uiOnlySections++
		default:
			return nil, 0, fmt.Errorf("unsupported transcript section %q", entry.Kind)
		}
	}
	if len(messages) == 0 {
		return nil, 0, fmt.Errorf("no user or assistant messages were found")
	}
	return messages, uiOnlySections, nil
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
