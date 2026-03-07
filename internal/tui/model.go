package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/atotto/clipboard"
	"github.com/charmbracelet/log"

	"github.com/abdul-hamid-achik/local-agent/internal/agent"
	"github.com/abdul-hamid-achik/local-agent/internal/command"
	"github.com/abdul-hamid-achik/local-agent/internal/config"
	"github.com/abdul-hamid-achik/local-agent/internal/llm"
	"github.com/abdul-hamid-achik/local-agent/internal/permission"
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
	OverlayPlanForm
	OverlaySessionsPicker
)

// CompletionState holds all state for the interactive completion modal.
type CompletionState struct {
	Kind          string          // "command", "attachments", "skills"
	Filter        textinput.Model // inline filter field
	AllItems      []Completion    // full unfiltered list
	FilteredItems []Completion    // items matching current filter
	Index         int             // cursor in FilteredItems
	Selected      map[int]bool    // multi-select (keys = AllItems indices)
	CurrentPath   string          // for @ file browsing: relative dir path
	SearchResults []Completion    // async vecgrep results
	Searching     bool            // true while vecgrep is in flight
	DebounceTag   int             // cancel stale searches
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
	Name          string
	Args          string         // formatted args string
	RawArgs       map[string]any // original args
	Result        string
	IsError       bool
	Status        ToolStatus
	StartTime     time.Time
	Duration      time.Duration
	Collapsed     bool       // per-entry collapse state
	BeforeContent string     // snapshot before file write (for diff)
	DiffLines     []DiffLine // computed diff (nil = not a file write)
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
	viewport viewport.Model
	input    textarea.Model
	spin     spinner.Model
	scramble ScrambleModel
	styles   Styles
	md       *MarkdownRenderer
	keys     KeyMap

	// State
	state          State
	overlay        OverlayKind
	entries        []ChatEntry
	streamBuf      strings.Builder
	width          int
	height         int
	ready          bool
	isDark         bool
	evalCount      int
	promptTokens   int
	toolsPending   int
	inputLines     int
	userScrolledUp bool
	
	// Scroll anchor system - prevents jitter during streaming
	scrollAnchor      int    // lines from bottom to maintain
	anchorActive      bool   // true when user wants to stay at bottom
	lastContentHeight int    // track content height changes for smooth scrolling

	// Startup
	initializing bool
	startupItems []startupItem
	initCancel   context.CancelFunc

	// Completion modal
	completionState *CompletionState // nil when no overlay

	// File attachments
	attachments []string

	// Tool display
	toolEntries    []ToolEntry
	toolsCollapsed bool
	toolEntryRows  map[int]int // toolIndex → starting row in viewport
	toolCardMgr    ToolCardManager

	// Incremental rendering cache
	cachedEntriesRender string
	cachedEntryCount    int
	cachedToolEntryRows map[int]int
	entryCacheValid     bool

	// Thinking state
	thinkBuf       strings.Builder
	inThinking     bool
	thinkSearchBuf string

	// Terminal title
	doneFlash bool

	// Session persistence
	sessionNoteID       int
	sessionsPickerState *SessionsPickerState

	// Paste detection
	pendingPaste string

	// Responsive layout
	isCompact    bool
	isWide       bool
	forceCompact bool // user-toggled compact mode

	// Mode system
	mode        Mode
	modeConfigs [3]ModeConfig

	// Model management
	modelManager     *llm.ModelManager
	router           *config.Router
	modelPickerState *ModelPickerState
	planFormState    *PlanFormState

	// Logging
	logger *log.Logger

	// Features
	agent       *agent.Agent
	cmdRegistry *command.Registry
	skillMgr    *skill.Manager
	completer   *Completer
	loadedFile  string

	// Runtime
	program *tea.Program
	cancel  context.CancelFunc

	// Display info
	model         string
	modelList     []string
	agentProfile  string
	agentList     []string
	toolCount     int
	serverCount   int
	numCtx        int

	// Toast notifications
	toastMgr *ToastManager
	toastStyles ToastStyles
	failedServers []FailedServer

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
	pendingApproval *ToolApprovalMsg

	// Prompt history
	promptHistory []string // all submitted inputs
	historyIndex  int      // -1 = not browsing, 0 = most recent
	historySaved  string   // saved current input when entering history

	// Welcome animation
	welcomeModel WelcomeModel

	// Side panel
	sidePanel SidePanelModel

	// Animated logo for chat viewport
	logoModel LogoModel

	// Help overlay viewport (scrollable)
	helpViewport viewport.Model

	// Search functionality
	searchState *SearchState

	// Progress tracking
	progressTracker *ProgressTracker

	// Panel resize
	resizer *PanelResizer

	// Context menu
	contextMenu *ContextMenuState

	// Timestamps
	timestampConfig TimestampConfig
	timestampHelper *TimestampHelper

	// Key hints
	keyHints *KeyHints

	// Accessibility
	accessibility *AccessibilityHelper

	// Table helper
	tableHelper *TableHelper
}

// New creates a new TUI Model.
func New(ag *agent.Agent, cmdReg *command.Registry, skillMgr *skill.Manager, completer *Completer, modelManager *llm.ModelManager, router *config.Router, logger *log.Logger) *Model {
	ta := textarea.New()
	ta.Placeholder = "Ask anything... (Enter to send, ctrl+b for sidebar)"
	ta.Focus()
	ta.CharLimit = 4096
	ta.SetHeight(1)
	ta.ShowLineNumbers = false
	ta.Prompt = "❯ "
	
	// Remove background - make completely transparent like Crush
	styles := textarea.DefaultDarkStyles()
	styles.Focused.Base = lipgloss.NewStyle()  // No background
	styles.Focused.CursorLine = lipgloss.NewStyle()  // No background on cursor line
	styles.Blurred.Base = lipgloss.NewStyle()  // No background when blurred
	ta.SetStyles(styles)
	
	// Set prompt color to match Nord theme (cyan)
	styles.Focused.Prompt = lipgloss.NewStyle().Foreground(lipgloss.Color("#88c0d0"))
	ta.SetStyles(styles)

	s := spinner.New(
		spinner.WithSpinner(spinner.MiniDot),
		spinner.WithStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("#88c0d0"))),
	)

	return &Model{
		input:          ta,
		spin:           s,
		scramble:       NewScrambleModel(true),
		welcomeModel:   NewWelcomeModel(true),
		sidePanel:      NewSidePanelModel(true),
		logoModel:      NewLogoModel(true),
		styles:         NewStyles(true),
		keys:           DefaultKeyMap(),
		state:          StateIdle,
		isDark:         true,
		inputLines:     1,
		toolsCollapsed: true,
		initializing:   true,
		mode:           ModeAsk,
		modeConfigs:    DefaultModeConfigs(),
		modelManager:   modelManager,
		router:         router,
		logger:         logger,
		agent:          ag,
		cmdRegistry:    cmdReg,
		skillMgr:       skillMgr,
		completer:      completer,
		historyIndex:   -1,
		toastMgr:       NewToastManager(),
		toastStyles:    DefaultToastStyles(true),
		toolCardMgr:    NewToolCardManager(true),
		searchState:    NewSearchState(),
		progressTracker: NewProgressTracker(true),
		resizer:        NewPanelResizer(20, 60, true),
		contextMenu:    &ContextMenuState{Active: false},
		timestampConfig: DefaultTimestampConfig(),
		timestampHelper: NewTimestampHelper(DefaultTimestampConfig(), true),
		keyHints:       DefaultKeyHints(true),
		accessibility:  NewAccessibilityHelper(true),
		tableHelper:    NewTableHelper(true),
	}
}

// SetProgram sets the tea.Program reference (must be called before Run).
func (m *Model) SetProgram(p *tea.Program) {
	m.program = p
}

// SetInitCancel stores the cancel function for the background init goroutine.
func (m *Model) SetInitCancel(cancel context.CancelFunc) {
	m.initCancel = cancel
}

// renderStartup renders the logo welcome screen during initialization.
// Startup progress is shown in the sidebar, so the main viewport shows the logo.
func (m *Model) renderStartup(b *strings.Builder) {
	m.renderWelcome(b)
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(
		textarea.Blink,
		tea.RequestBackgroundColor,
		m.spin.Tick,
		// Start sidepanel spinner animation for initialization
		func() tea.Msg {
			return spinnerTickMsg{}
		},
	)
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.BackgroundColorMsg:
		m.isDark = msg.IsDark()
		m.styles = NewStyles(m.isDark)
		// Update spinner style for theme.
		m.spin.Style = m.styles.StatusDot
		m.scramble.SetDark(msg.IsDark())
		// Update toast styles for theme.
		m.toastStyles = DefaultToastStyles(m.isDark)
		m.toastMgr.SetStyles(m.toastStyles)
		// Update tool card styles for theme.
		m.toolCardMgr.SetDark(msg.IsDark())
		// Update new components for theme.
		m.progressTracker.SetDark(msg.IsDark())
		m.resizer.SetDark(msg.IsDark())
		m.timestampHelper.SetDark(msg.IsDark())
		m.keyHints.SetDark(msg.IsDark())
		m.accessibility.SetDark(msg.IsDark())
		m.tableHelper.SetDark(msg.IsDark())
		// Recreate markdown renderer for new theme.
		if m.width > 0 {
			m.md = NewMarkdownRenderer(m.width-2, m.isDark)
			m.invalidateRenderedCache()
		}
		if m.ready {
			m.viewport.SetContent(m.renderEntries())
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.isCompact = msg.Width < 80 || msg.Height < 24
		m.isWide = msg.Width > 120
		
		// Calculate panel width (30 chars or 25% of screen, min 25, max 40)
		panelWidth := 30
		if msg.Width < 100 {
			panelWidth = 25
		} else if msg.Width > 160 {
			panelWidth = 40
		}
		
		// Calculate viewport width (for viewport component)
		viewportWidth := msg.Width - 1
		if m.sidePanel.IsVisible() {
			viewportWidth = msg.Width - panelWidth - 2 // panel + separator
		}
		if viewportWidth < 20 {
			viewportWidth = 20
		}
		
		// Calculate content width for markdown renderer and text wrapping
		// CRITICAL: Must be <= viewport width to prevent horizontal overflow
		// Use conservative padding to account for margins, borders, and indentation
		contentWidth := viewportWidth - 6
		if contentWidth < 14 {
			contentWidth = 14
		}

		m.md = NewMarkdownRenderer(contentWidth, m.isDark)

		// Always update side panel dimensions
		m.sidePanel.SetWidth(panelWidth)
		m.sidePanel.SetHeight(msg.Height - 2) // account for footer

		// Recalculate content height
		contentH := msg.Height - 1 - m.footerHeight()
		if contentH < 1 {
			contentH = 1
		}

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
			// Initialize scroll anchor system
			m.scrollAnchor = 0
			m.anchorActive = true
			m.lastContentHeight = 0
			// Initialize tool entry rows map
			m.toolEntryRows = make(map[int]int, 8)
		} else {
			m.viewport.SetWidth(viewportWidth)
			m.viewport.SetHeight(contentH)
			// Only invalidate cache if significant width change (>5 chars)
			widthDelta := abs(m.width - msg.Width)
			if widthDelta > 5 {
				m.invalidateRenderedCache()
			}
			m.viewport.SetContent(m.renderEntries())
			// Maintain scroll position - if anchor is active, stay at bottom
			if m.anchorActive {
				m.viewport.GotoBottom()
			}
		}

		// Resize help viewport if it's open.
		if m.overlay == OverlayHelp {
			m.initHelpViewport()
		}

		// Input width matches viewport exactly - they're one unified area
		m.input.SetWidth(viewportWidth)
		m.syncInputHeight()

	case tea.KeyPressMsg:
		// During startup, only allow Ctrl+C to quit.
		if m.initializing {
			if key.Matches(msg, m.keys.Quit) {
				if m.initCancel != nil {
					m.initCancel()
				}
				return m, tea.Quit
			}
			return m, nil
		}

		// Pending tool approval intercept: y/n/a before anything else.
		if m.pendingApproval != nil {
			switch msg.String() {
			case "y":
				m.pendingApproval.Response <- ToolApprovalResponse{Allowed: true}
				m.pendingApproval = nil
			case "n":
				m.pendingApproval.Response <- ToolApprovalResponse{Allowed: false}
				m.pendingApproval = nil
			case "a":
				m.pendingApproval.Response <- ToolApprovalResponse{Allowed: true, Always: true}
				m.pendingApproval = nil
			}
			return m, nil
		}

		// Pending paste intercept: y/n/esc before anything else.
		if m.pendingPaste != "" {
			switch {
			case msg.String() == "y":
				m.input.InsertString("```\n" + m.pendingPaste + "\n```")
				m.pendingPaste = ""
				m.syncInputHeight()
			case msg.String() == "n":
				m.input.InsertString(m.pendingPaste)
				m.pendingPaste = ""
				m.syncInputHeight()
			case key.Matches(msg, m.keys.Cancel):
				m.pendingPaste = ""
			}
			return m, nil
		}

		// Handle overlay keys first.
		if m.overlay != OverlayNone {
			// ESC always closes the current overlay.
			if key.Matches(msg, m.keys.Cancel) {
				switch m.overlay {
				case OverlayCompletion:
					m.input.SetValue("")
					m.closeCompletion()
				case OverlayModelPicker:
					m.closeModelPicker()
				case OverlayPlanForm:
					m.closePlanForm()
				case OverlaySessionsPicker:
					// If the list is filtering, let ESC clear the filter first.
					if m.sessionsPickerState != nil && m.sessionsPickerState.List.FilterState() == list.Filtering {
						var cmd tea.Cmd
						m.sessionsPickerState.List, cmd = m.sessionsPickerState.List.Update(msg)
						cmds = append(cmds, cmd)
						return m, tea.Batch(cmds...)
					}
					m.closeSessionsPicker()
				default:
					m.overlay = OverlayNone
					m.input.Focus()
				}
				return m, nil
			}

			// Help overlay: scroll keys forwarded to helpViewport, ? or q to dismiss.
			if m.overlay == OverlayHelp {
				switch msg.String() {
				case "?", "q":
					m.overlay = OverlayNone
					m.input.Focus()
				case "j", "down":
					m.helpViewport.ScrollDown(1)
				case "k", "up":
					m.helpViewport.ScrollUp(1)
				case "pgdown":
					m.helpViewport.PageDown()
				case "pgup":
					m.helpViewport.PageUp()
				case "d":
					m.helpViewport.HalfPageDown()
				case "u":
					m.helpViewport.HalfPageUp()
				case "g":
					m.helpViewport.GotoTop()
				case "G":
					m.helpViewport.GotoBottom()
				}
				return m, nil
			}

			// Model picker overlay: forward keys to list, Enter selects.
			if m.overlay == OverlayModelPicker && m.modelPickerState != nil {
				if key.Matches(msg, m.keys.CompleteSelect) {
					if item := m.modelPickerState.List.SelectedItem(); item != nil {
						mi := item.(modelItem)
						m.selectModel(mi.name)
					}
				} else {
					var cmd tea.Cmd
					m.modelPickerState.List, cmd = m.modelPickerState.List.Update(msg)
					cmds = append(cmds, cmd)
				}
				return m, tea.Batch(cmds...)
			}

			// Plan form overlay.
			if m.overlay == OverlayPlanForm && m.planFormState != nil {
				submitted, cancelled := m.updatePlanForm(msg)
				if cancelled {
					m.closePlanForm()
					return m, nil
				}
				if submitted {
					prompt := m.planFormState.AssemblePrompt()
					m.closePlanForm()
					return m, m.submitPlanFormPrompt(prompt)
				}
				return m, nil
			}

			// Sessions picker overlay: forward keys to list, Enter loads.
			if m.overlay == OverlaySessionsPicker && m.sessionsPickerState != nil {
				if key.Matches(msg, m.keys.CompleteSelect) {
					if item := m.sessionsPickerState.List.SelectedItem(); item != nil {
						si := item.(sessionItem)
						sessionID := si.id
						sessionTitle := si.title
						m.closeSessionsPicker()
						return m, func() tea.Msg {
							note, err := loadSession(sessionID)
							if err != nil {
								return SessionLoadedMsg{Err: err}
							}
							entries := deserializeEntries(note.Content)
							return SessionLoadedMsg{Entries: entries, Title: sessionTitle}
						}
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
					}
				case key.Matches(msg, m.keys.CompleteDown):
					if cs.Index < len(cs.FilteredItems)-1 {
						cs.Index++
					}
				case key.Matches(msg, m.keys.CompleteSelect):
					// Enter: if item is a folder, drill into it; otherwise accept
					if cs.Index < len(cs.FilteredItems) && cs.Kind == "attachments" && cs.FilteredItems[cs.Index].Category == "folder" {
						m.drillIntoFolder()
					} else {
						m.acceptCompletion()
					}
				case key.Matches(msg, m.keys.CompleteToggle):
					// Tab toggles multi-select
					m.toggleCompletionSelection()
				default:
					// Check for backspace on empty filter => go up directory for @ kind
					if msg.Code == tea.KeyBackspace && cs.Filter.Value() == "" && cs.Kind == "attachments" && cs.CurrentPath != "" {
						m.drillUpFolder()
						return m, nil
					}

					// Forward all other keys to filter input
					oldFilter := cs.Filter.Value()
					var cmd tea.Cmd
					cs.Filter, cmd = cs.Filter.Update(msg)

					// Re-filter if text changed
					if cs.Filter.Value() != oldFilter {
						cs.FilteredItems = FilterCompletions(cs.AllItems, cs.Filter.Value())
						cs.Index = 0

						// Schedule debounced vecgrep search for @ kind
						if cs.Kind == "attachments" && cs.Filter.Value() != "" {
							cs.DebounceTag++
							tag := cs.DebounceTag
							query := cs.Filter.Value()
							return m, tea.Batch(cmd, tea.Tick(300*time.Millisecond, func(time.Time) tea.Msg {
								return CompletionDebounceTickMsg{Tag: tag, Query: query}
							}))
						}
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
			if m.cancel != nil {
				m.cancel()
			}
			return m, tea.Quit

		case key.Matches(msg, m.keys.Cancel):
			if (m.state == StateStreaming || m.state == StateWaiting) && m.cancel != nil {
				m.cancel()
			}

		case key.Matches(msg, m.keys.Help):
			// Only toggle help when input is empty.
			if m.state == StateIdle && strings.TrimSpace(m.input.Value()) == "" {
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
			// Toggle thinking collapsed for last assistant entry.
			if m.state == StateIdle && strings.TrimSpace(m.input.Value()) == "" {
				for i := len(m.entries) - 1; i >= 0; i-- {
					if m.entries[i].Kind == "assistant" && m.entries[i].ThinkingContent != "" {
						m.entries[i].ThinkingCollapsed = !m.entries[i].ThinkingCollapsed
						m.invalidateEntryCache()
						m.viewport.SetContent(m.renderEntries())
						break
					}
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
				m.viewport.GotoBottom()
				return m, nil
			}

		case key.Matches(msg, m.keys.NewConvo):
			if m.state == StateIdle {
				m.agent.ClearHistory()
				m.entries = nil
				m.toolEntries = nil
				m.sessionEvalTotal = 0
				m.sessionPromptTotal = 0
				m.sessionTurnCount = 0
				m.fileChanges = nil
				m.invalidateEntryCache()
				m.entries = append(m.entries, ChatEntry{
					Kind:    "system",
					Content: "New conversation started.",
				})
				m.viewport.SetContent(m.renderEntries())
				m.viewport.GotoBottom()
				return m, nil
			}

		case key.Matches(msg, m.keys.CycleMode):
			if m.state == StateIdle {
				m.cycleMode()
				return m, nil
			}

		case key.Matches(msg, m.keys.ModelPicker):
			if m.state == StateIdle {
				m.openModelPicker()
				return m, nil
			}

		case key.Matches(msg, m.keys.NewLine):
			// Insert newline in textarea (shift+enter).
			if m.state == StateIdle {
				m.input.InsertString("\n")
				m.syncInputHeight()
				return m, nil
			}

		case key.Matches(msg, m.keys.Send):
			if m.state == StateIdle {
				return m, m.submitInput()
			}

		case key.Matches(msg, m.keys.Complete):
			// Tab key for autocomplete
			if m.state == StateIdle && m.completer != nil && !m.isCompletionActive() {
				m.triggerCompletion(m.input.Value())
			}

		case key.Matches(msg, m.keys.HistoryUp):
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

		case key.Matches(msg, m.keys.ToggleSidePanel):
			if m.state == StateIdle {
				m.sidePanel.Toggle()
				// Recalculate viewport sizes
				panelWidth := 30
				if m.width < 100 {
					panelWidth = 25
				}
				// Unified chat area width
				contentWidth := m.width - 1
				if m.sidePanel.IsVisible() {
					m.sidePanel.SetWidth(panelWidth)
					m.sidePanel.SetHeight(m.height - 2)
					contentWidth = m.width - panelWidth - 2 // panel + separator
				}
				if contentWidth < 20 {
					contentWidth = 20
				}
				m.viewport.SetWidth(contentWidth)
				m.input.SetWidth(contentWidth) // Unified width
				m.invalidateRenderedCache()
				m.viewport.SetContent(m.renderEntries())
				return m, nil
			}
		}

	case StreamTextMsg:
		if m.state == StateWaiting {
			m.state = StateStreaming
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
		m.viewport.SetContent(m.renderEntries())
		
		// Use scroll anchor system - only auto-scroll if anchor is active
		if m.anchorActive {
			m.viewport.GotoBottom()
		}

	case StreamDoneMsg:
		m.evalCount = msg.EvalCount
		m.promptTokens = msg.PromptTokens
		m.sessionEvalTotal += msg.EvalCount
		m.sessionPromptTotal += msg.PromptTokens
		m.sessionTurnCount++

	case ToolCallStartMsg:
		te := ToolEntry{
			Name:      msg.Name,
			Args:      FormatToolArgs(msg.Args),
			RawArgs:   msg.Args,
			Status:    ToolStatusRunning,
			StartTime: msg.StartTime,
			Collapsed: m.toolsCollapsed,
		}
		// Snapshot file content before write for diff view.
		if classifyTool(msg.Name) == ToolTypeFileWrite {
			te.BeforeContent = readFileForDiff(msg.Args)
		}
		m.toolEntries = append(m.toolEntries, te)
		m.toolsPending++

		// Create tool card for fancy display
		kind := ToolCardGeneric
		switch classifyTool(msg.Name) {
		case ToolTypeFileRead, ToolTypeFileWrite:
			kind = ToolCardFile
		case ToolTypeBash:
			kind = ToolCardBash
		default:
			kind = ToolCardGeneric
		}
		m.toolCardMgr.AddCard(msg.Name, kind, msg.StartTime)

		m.entries = append(m.entries, ChatEntry{
			Kind:      "tool_group",
			ToolIndex: len(m.toolEntries) - 1,
		})
		// Flush any accumulated stream text before tool display.
		m.flushStream()
		m.viewport.SetContent(m.renderEntries())
		// Use scroll anchor system
		if m.anchorActive {
			m.viewport.GotoBottom()
		}

	case PlanFormCompletedMsg:
		return m, m.submitPlanFormPrompt(msg.Prompt)

	case ToolCallResultMsg:
		m.invalidateEntryCache()
		if m.logger != nil {
			m.logger.Info("tool call", "name", msg.Name, "duration", msg.Duration, "error", msg.IsError)
		}
		for i := len(m.toolEntries) - 1; i >= 0; i-- {
			if m.toolEntries[i].Name == msg.Name && m.toolEntries[i].Status == ToolStatusRunning {
				result := msg.Result
				if len(result) > 2000 {
					result = result[:1997] + "..."
				}
				m.toolEntries[i].Result = result
				m.toolEntries[i].IsError = msg.IsError
				m.toolEntries[i].Duration = msg.Duration
				if msg.IsError {
					m.toolEntries[i].Status = ToolStatusError
				} else {
					m.toolEntries[i].Status = ToolStatusDone
				}
				// Compute diff for file writes and track file changes.
				if classifyTool(m.toolEntries[i].Name) == ToolTypeFileWrite && !msg.IsError {
					afterContent := readFileForDiff(m.toolEntries[i].RawArgs)
					m.toolEntries[i].DiffLines = computeDiff(m.toolEntries[i].BeforeContent, afterContent)
					if path := toolSummary(ToolTypeFileWrite, m.toolEntries[i]); path != "" {
						if m.fileChanges == nil {
							m.fileChanges = make(map[string]int)
						}
						m.fileChanges[path]++
					}
				}
				break
			}
		}
		// Update tool card
		cardState := ToolCardSuccess
		if msg.IsError {
			cardState = ToolCardError
		}
		m.toolCardMgr.UpdateCard(msg.Name, cardState, msg.Result, msg.Duration)

		if m.toolsPending > 0 {
			m.toolsPending--
		}
		m.viewport.SetContent(m.renderEntries())
		// Use scroll anchor system
		if m.anchorActive {
			m.viewport.GotoBottom()
		}

	case SystemMessageMsg:
		m.entries = append(m.entries, ChatEntry{
			Kind:    "system",
			Content: msg.Msg,
		})
		m.viewport.SetContent(m.renderEntries())
		// Use scroll anchor system
		if m.anchorActive {
			m.viewport.GotoBottom()
		}

	case ErrorMsg:
		if m.logger != nil {
			m.logger.Error("error", "msg", msg.Msg)
		}
		m.entries = append(m.entries, ChatEntry{
			Kind:    "error",
			Content: msg.Msg,
		})
		m.viewport.SetContent(m.renderEntries())
		// Use scroll anchor system
		if m.anchorActive {
			m.viewport.GotoBottom()
		}

	case AgentDoneMsg:
		if m.logger != nil {
			m.logger.Info("agent done", "eval_tokens", m.evalCount)
		}
		m.flushStream()
		m.state = StateIdle
		m.userScrolledUp = false
		m.anchorActive = true
		m.scrollAnchor = 0
		m.input.Focus()
		m.input.SetHeight(1)
		m.inputLines = 1
		m.recalcViewportHeight()
		m.viewport.SetContent(m.renderEntries())
		m.viewport.GotoBottom()
		// Terminal title flash.
		m.doneFlash = true
		cmds = append(cmds, tea.Tick(2*time.Second, func(time.Time) tea.Msg {
			return DoneFlashExpiredMsg{}
		}))
		// Update session note.
		if m.sessionNoteID > 0 {
			id := m.sessionNoteID
			content := serializeEntries(m.entries)
			cmds = append(cmds, func() tea.Msg {
				_ = updateSessionNote(id, content)
				return nil
			})
		}

	case StartupStatusMsg:
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
			m.startupItems = append(m.startupItems, startupItem{
				ID: msg.ID, Label: msg.Label, Status: msg.Status, Detail: msg.Detail,
			})
		}
		// Update sidebar startup items
		sidePanelItems := make([]StartupItem, len(m.startupItems))
		for i, item := range m.startupItems {
			sidePanelItems[i] = StartupItem{
				Label:  item.Label,
				Status: item.Status,
				Detail: item.Detail,
			}
		}
		m.sidePanel.SetStartupItems(sidePanelItems)
		// Tick the spinner for animation during initialization
		m.sidePanel.SetSpinnerTick()
		if m.ready {
			m.viewport.SetContent(m.renderEntries())
		}
		// Continue animating spinner during initialization
		return m, tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg {
			return spinnerTickMsg{}
		})

	case spinnerTickMsg:
		// Only tick if still initializing or tools are running
		if m.initializing {
			m.sidePanel.Tick()
			// Continue the animation loop
			return m, tea.Tick(80*time.Millisecond, func(time.Time) tea.Msg {
				return spinnerTickMsg{}
			})
		}
		// Tick tool card spinners if tools are running
		if m.toolsPending > 0 {
			m.toolCardMgr.Tick()
			// Continue animating tool spinners
			return m, tea.Tick(80*time.Millisecond, func(time.Time) tea.Msg {
				return spinnerTickMsg{}
			})
		}

	case InitCompleteMsg:
		m.model = msg.Model
		m.modelList = msg.ModelList
		m.agentProfile = msg.AgentProfile
		m.agentList = msg.AgentList
		m.toolCount = msg.ToolCount
		m.serverCount = msg.ServerCount
		m.numCtx = msg.NumCtx
		m.failedServers = msg.FailedServers
		m.iceEnabled = msg.ICEEnabled
		m.iceConversations = msg.ICEConversations
		m.iceSessionID = msg.ICESessionID

		if m.completer != nil {
			m.completer.UpdateModels(msg.ModelList)
			m.completer.UpdateAgents(msg.AgentList)
		}

		if len(msg.FailedServers) > 0 {
			var parts []string
			for _, fs := range msg.FailedServers {
				parts = append(parts, fs.Name+" ("+fs.Reason+")")
			}
			m.entries = append(m.entries, ChatEntry{
				Kind:    "system",
				Content: "Failed to connect: " + strings.Join(parts, ", "),
			})
		}

		m.initializing = false
		m.startupItems = nil

		// Update side panel with initial data
		m.sidePanel.UpdateSections(
			m.model,
			m.modelList,
			m.serverCount,
			m.toolCount,
			m.iceEnabled,
			m.iceConversations,
		)

		// Start logo animation in chat viewport
		m.logoModel.Start()

		m.viewport.SetContent(m.renderEntries())

	case CommandResultMsg:
		if msg.Text != "" {
			m.entries = append(m.entries, ChatEntry{
				Kind:    "system",
				Content: msg.Text,
			})
			m.viewport.SetContent(m.renderEntries())
			m.viewport.GotoBottom()
		}

	case CompletionDebounceTickMsg:
		if m.isCompletionActive() && m.completionState.DebounceTag == msg.Tag {
			cs := m.completionState
			cs.Searching = true
			query := msg.Query
			tag := msg.Tag
			return m, func() tea.Msg {
				results := m.completer.SearchFiles(context.Background(), query)
				return CompletionSearchResultMsg{Tag: tag, Results: results}
			}
		}

	case CompletionSearchResultMsg:
		if m.isCompletionActive() && m.completionState.DebounceTag == msg.Tag {
			cs := m.completionState
			cs.Searching = false
			cs.SearchResults = msg.Results

			// Merge search results into AllItems, deduplicating by Insert
			existing := make(map[string]bool)
			for _, item := range cs.AllItems {
				existing[item.Insert] = true
			}
			for _, result := range msg.Results {
				if !existing[result.Insert] {
					cs.AllItems = append(cs.AllItems, result)
				}
			}
			// Re-filter with current query
			cs.FilteredItems = FilterCompletions(cs.AllItems, cs.Filter.Value())
		}

	case ToolApprovalMsg:
		m.pendingApproval = &msg
		// Status line will show the approval prompt.

	case CommitResultMsg:
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
		m.viewport.GotoBottom()

	case editorReturnMsg:
		m.input.SetValue(msg.Content)
		m.input.CursorEnd()
		m.syncInputHeight()
		m.input.Focus()

	case DoneFlashExpiredMsg:
		m.doneFlash = false

	case SessionCreatedMsg:
		if msg.Err == nil && msg.NoteID > 0 {
			m.sessionNoteID = msg.NoteID
		}

	case SessionListMsg:
		if msg.Err != nil {
			m.entries = append(m.entries, ChatEntry{Kind: "error", Content: fmt.Sprintf("Sessions: %v", msg.Err)})
			m.viewport.SetContent(m.renderEntries())
			m.viewport.GotoBottom()
		} else if len(msg.Sessions) == 0 {
			m.entries = append(m.entries, ChatEntry{Kind: "system", Content: "No saved sessions found."})
			m.viewport.SetContent(m.renderEntries())
			m.viewport.GotoBottom()
		} else {
			m.sessionsPickerState = newSessionsPickerState(msg.Sessions, m.width, m.isDark)
			m.overlay = OverlaySessionsPicker
			m.input.Blur()
		}

	case SessionLoadedMsg:
		m.invalidateEntryCache()
		if msg.Err != nil {
			m.entries = append(m.entries, ChatEntry{Kind: "error", Content: fmt.Sprintf("Load session: %v", msg.Err)})
		} else {
			m.entries = msg.Entries
			m.entries = append([]ChatEntry{{Kind: "system", Content: fmt.Sprintf("Restored session: %s", msg.Title)}}, m.entries...)
		}
		m.viewport.SetContent(m.renderEntries())
		m.viewport.GotoBottom()

	case tea.MouseWheelMsg:
		wasAtBottom := m.viewport.AtBottom()
		m.viewport, _ = m.viewport.Update(msg)
		
		// Use AtBottom() to determine scroll anchor state
		// If viewport was at bottom before scroll up, user is scrolling away
		if msg.Button == tea.MouseWheelUp && wasAtBottom {
			m.anchorActive = false
			m.userScrolledUp = true
			m.scrollAnchor = 5 // Default scroll offset
		} else if m.viewport.AtBottom() {
			// Scrolled back to bottom - re-enable anchor
			m.anchorActive = true
			m.userScrolledUp = false
			m.scrollAnchor = 0
		}

	case tea.MouseClickMsg:
		if msg.Button == tea.MouseLeft {
			m.handleMouseClick(msg.X, msg.Y)
		}

	case tea.PasteMsg:
		lines := strings.Count(msg.Content, "\n") + 1
		if lines > 10 && m.state == StateIdle {
			m.pendingPaste = msg.Content
		} else if m.state == StateIdle {
			m.input.InsertString(msg.Content)
			m.syncInputHeight()
		}
	}

	// Update scramble animation.
	if _, ok := msg.(ScrambleTickMsg); ok {
		var cmd tea.Cmd
		m.scramble, cmd = m.scramble.Update(msg)
		cmds = append(cmds, cmd)
	}

	// Update logo animation
	if !m.logoModel.IsDone() {
		var cmd tea.Cmd
		m.logoModel, cmd = m.logoModel.Update(msg)
		cmds = append(cmds, cmd)
	}

	// Always update spinner so the tick chain doesn't break.
	{
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		cmds = append(cmds, cmd)
	}

	// Update sub-components.
	if m.state == StateIdle && m.overlay == OverlayNone && !m.initializing {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		cmds = append(cmds, cmd)

		// Auto-grow textarea based on content.
		m.syncInputHeight()

		// Auto-trigger completion when user types /, @, or #
		newInput := m.input.Value()
		if m.completer != nil && len(newInput) > 0 {
			first := newInput[0]
			if (first == '/' || first == '@' || first == '#') && !m.isCompletionActive() {
				m.triggerCompletion(newInput)
			}
		}
		// Auto-close if trigger char removed
		if m.isCompletionActive() && (len(newInput) == 0 || (newInput[0] != '/' && newInput[0] != '@' && newInput[0] != '#')) {
			m.closeCompletion()
		}
	}

	wasAtBottom := m.viewport.AtBottom()
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	cmds = append(cmds, cmd)

	// Detect keyboard scroll during streaming: if we moved away from bottom, mark scrolled up.
	if m.state == StateStreaming && wasAtBottom && !m.viewport.AtBottom() {
		m.userScrolledUp = true
	}
	m.checkAutoScroll()

	return m, tea.Batch(cmds...)
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
			m.input.SetValue(m.promptHistory[m.historyIndex])
			m.input.CursorEnd()
		} else {
			// Past newest: restore saved input
			m.historyIndex = -1
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

	m.pushHistory(text)

	m.input.Reset()
	m.input.SetHeight(1)

	// Handle slash commands.
	if strings.HasPrefix(text, "/") {
		parts := strings.Fields(text)
		name := strings.TrimPrefix(parts[0], "/")
		args := parts[1:]

		ctx := m.buildCommandContext()
		result := m.cmdRegistry.Execute(ctx, name, args)

		// Handle command result.
		if result.Error != "" {
			m.entries = append(m.entries, ChatEntry{
				Kind:    "error",
				Content: result.Error,
			})
			m.viewport.SetContent(m.renderEntries())
			m.viewport.GotoBottom()
			return nil
		}

		return m.handleCommandAction(result)
	}

	// Plan mode: show the plan form instead of sending directly.
	if m.mode == ModePlan {
		m.openPlanForm(text)
		return nil
	}

	// Regular message — send to agent.
	return m.sendToAgent(text)
}

// buildCommandContext creates a Context for slash command execution.
func (m *Model) buildCommandContext() *command.Context {
	ctx := &command.Context{
		Model:              m.model,
		ModelList:          m.modelList,
		AgentProfile:       m.agentProfile,
		AgentList:          m.agentList,
		ToolCount:          m.toolCount,
		ServerCount:        m.serverCount,
		ServerNames:        m.agent.ServerNames(),
		LoadedFile:         m.loadedFile,
		ICEEnabled:         m.iceEnabled,
		ICEConversations:   m.iceConversations,
		ICESessionID:       m.iceSessionID,
		SessionEvalTotal:   m.sessionEvalTotal,
		SessionPromptTotal: m.sessionPromptTotal,
		SessionTurnCount:   m.sessionTurnCount,
		NumCtx:             m.numCtx,
		CurrentModel:       m.model,
		FileChanges:        m.fileChanges,
	}

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
	switch result.Action {
	case command.ActionShowHelp:
		m.overlay = OverlayHelp
		m.initHelpViewport()
		return nil

	case command.ActionClear:
		m.agent.ClearHistory()
		m.entries = nil
		m.toolEntries = nil
		m.invalidateEntryCache()
		if result.Text != "" {
			m.entries = append(m.entries, ChatEntry{
				Kind:    "system",
				Content: result.Text,
			})
		}
		m.viewport.SetContent(m.renderEntries())
		m.viewport.GotoBottom()
		return nil

	case command.ActionQuit:
		if m.cancel != nil {
			m.cancel()
		}
		return tea.Quit

	case command.ActionLoadContext:
		// Data format: path\0content
		parts := strings.SplitN(result.Data, "\x00", 2)
		if len(parts) == 2 {
			m.loadedFile = parts[0]
			m.agent.SetLoadedContext(parts[1])
		}
		if result.Text != "" {
			m.entries = append(m.entries, ChatEntry{
				Kind:    "system",
				Content: result.Text,
			})
		}
		m.viewport.SetContent(m.renderEntries())
		m.viewport.GotoBottom()
		return nil

	case command.ActionUnloadContext:
		m.loadedFile = ""
		m.agent.SetLoadedContext("")
		if result.Text != "" {
			m.entries = append(m.entries, ChatEntry{
				Kind:    "system",
				Content: result.Text,
			})
		}
		m.viewport.SetContent(m.renderEntries())
		m.viewport.GotoBottom()
		return nil

	case command.ActionActivateSkill:
		if m.skillMgr != nil {
			if err := m.skillMgr.Activate(result.Data); err != nil {
				m.entries = append(m.entries, ChatEntry{
					Kind:    "error",
					Content: err.Error(),
				})
			} else {
				m.agent.SetSkillContent(m.skillMgr.ActiveContent())
				m.entries = append(m.entries, ChatEntry{
					Kind:    "system",
					Content: result.Text,
				})
			}
		}
		m.viewport.SetContent(m.renderEntries())
		m.viewport.GotoBottom()
		return nil

	case command.ActionDeactivateSkill:
		if m.skillMgr != nil {
			if err := m.skillMgr.Deactivate(result.Data); err != nil {
				m.entries = append(m.entries, ChatEntry{
					Kind:    "error",
					Content: err.Error(),
				})
			} else {
				m.agent.SetSkillContent(m.skillMgr.ActiveContent())
				m.entries = append(m.entries, ChatEntry{
					Kind:    "system",
					Content: result.Text,
				})
			}
		}
		m.viewport.SetContent(m.renderEntries())
		m.viewport.GotoBottom()
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
		if m.modelManager != nil {
			if err := m.modelManager.SetCurrentModel(result.Data); err != nil {
				m.entries = append(m.entries, ChatEntry{
					Kind:    "error",
					Content: fmt.Sprintf("Failed to switch model: %v", err),
				})
				m.viewport.SetContent(m.renderEntries())
				m.viewport.GotoBottom()
				return nil
			}
		}
		if m.logger != nil {
			m.logger.Info("model switched", "from", m.model, "to", result.Data)
		}
		m.model = result.Data
		m.entries = append(m.entries, ChatEntry{
			Kind:    "system",
			Content: result.Text,
		})
		m.viewport.SetContent(m.renderEntries())
		m.viewport.GotoBottom()
		return nil

	case command.ActionShowModelPicker:
		m.openModelPicker()
		return nil

	case command.ActionSendPrompt:
		if result.Text != "" {
			m.entries = append(m.entries, ChatEntry{Kind: "system", Content: result.Text})
		}
		return m.sendToAgent(result.Data)

	case command.ActionCommit:
		m.entries = append(m.entries, ChatEntry{
			Kind:    "system",
			Content: "Generating commit message from staged changes...",
		})
		m.viewport.SetContent(m.renderEntries())
		m.viewport.GotoBottom()
		return runCommit(m.agent.LLMClient(), m.model, result.Data)

	case command.ActionShowSessions:
		return func() tea.Msg {
			sessions, err := listSessions(20)
			return SessionListMsg{Sessions: sessions, Err: err}
		}

	case command.ActionSwitchAgent:
		m.agentProfile = result.Data
		m.entries = append(m.entries, ChatEntry{
			Kind:    "system",
			Content: result.Text,
		})
		m.viewport.SetContent(m.renderEntries())
		m.viewport.GotoBottom()
		return nil

	case command.ActionExport:
		path := result.Data
		if path == "" {
			m.entries = append(m.entries, ChatEntry{
				Kind:    "error",
				Content: "export: no path specified",
			})
			m.viewport.SetContent(m.renderEntries())
			m.viewport.GotoBottom()
			return nil
		}
		content := m.formatConversationForExport()
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			m.entries = append(m.entries, ChatEntry{
				Kind:    "error",
				Content: fmt.Sprintf("export failed: %v", err),
			})
		} else {
			m.entries = append(m.entries, ChatEntry{
				Kind:    "system",
				Content: fmt.Sprintf("Exported conversation to: %s", path),
			})
		}
		m.viewport.SetContent(m.renderEntries())
		m.viewport.GotoBottom()
		return nil

	case command.ActionImport:
		path := result.Data
		if path == "" {
			m.entries = append(m.entries, ChatEntry{
				Kind:    "error",
				Content: "import: no path specified",
			})
			m.viewport.SetContent(m.renderEntries())
			m.viewport.GotoBottom()
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			m.entries = append(m.entries, ChatEntry{
				Kind:    "error",
				Content: fmt.Sprintf("import failed: %v", err),
			})
			m.viewport.SetContent(m.renderEntries())
			m.viewport.GotoBottom()
			return nil
		}
		entries, err := m.parseImportedConversation(string(data))
		if err != nil {
			m.entries = append(m.entries, ChatEntry{
				Kind:    "error",
				Content: fmt.Sprintf("import parse error: %v", err),
			})
			m.viewport.SetContent(m.renderEntries())
			m.viewport.GotoBottom()
			return nil
		}
		m.entries = entries
		m.viewport.SetContent(m.renderEntries())
		m.viewport.GotoBottom()
		return nil

	default:
		// ActionNone — just show text.
		if result.Text != "" {
			m.entries = append(m.entries, ChatEntry{
				Kind:    "system",
				Content: result.Text,
			})
			m.viewport.SetContent(m.renderEntries())
			m.viewport.GotoBottom()
		}
		return nil
	}
}

// flushStream moves accumulated stream text into a chat entry with cached rendering.
func (m *Model) flushStream() {
	m.invalidateEntryCache()
	if m.streamBuf.Len() > 0 || m.thinkBuf.Len() > 0 {
		content := m.streamBuf.String()
		var rendered string
		if m.md != nil && content != "" {
			rendered = m.md.RenderFull(content)
		}
		entry := ChatEntry{
			Kind:            "assistant",
			Content:         content,
			RenderedContent: rendered,
		}
		// Attach thinking content if present.
		if m.thinkBuf.Len() > 0 {
			entry.ThinkingContent = m.thinkBuf.String()
			entry.ThinkingCollapsed = true
		}
		m.entries = append(m.entries, entry)
		m.streamBuf.Reset()
		m.thinkBuf.Reset()
		m.inThinking = false
		m.thinkSearchBuf = ""
	}
}

// invalidateRenderedCache clears cached renders (e.g. on terminal resize).
func (m *Model) invalidateRenderedCache() {
	for i := range m.entries {
		if m.entries[i].Kind == "assistant" && m.entries[i].RenderedContent != "" {
			if m.md != nil {
				m.entries[i].RenderedContent = m.md.RenderFull(m.entries[i].Content)
			}
		}
	}
	m.invalidateEntryCache()
}

// footerHeight returns the total height of the footer area (divider + status + input/hint).
func (m *Model) footerHeight() int {
	if m.state == StateIdle {
		return 2 + m.inputLines // divider + status + input lines
	}
	return 3 // divider + status + streaming hint
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

// invalidateEntryCache marks the incremental entry render cache as stale,
// forcing a full re-render on the next renderEntries() call.
func (m *Model) invalidateEntryCache() {
	m.entryCacheValid = false
	m.cachedEntriesRender = ""
	m.cachedEntryCount = 0
	m.cachedToolEntryRows = nil
}

// checkAutoScroll resets scroll anchor when the viewport is at the bottom,
// allowing auto-scroll to resume during streaming.
func (m *Model) checkAutoScroll() {
	if m.viewport.AtBottom() {
		m.anchorActive = true
		m.userScrolledUp = false
		m.scrollAnchor = 0
	}
}

// getVisibleEntryRange calculates which entries should be rendered based on
// viewport position and content height. This optimizes rendering for long
// conversations by only rendering visible content.
func (m *Model) getVisibleEntryRange() (start, end int) {
	if m.viewport.YOffset() == 0 {
		// At top - render recent entries plus some history
		end = len(m.entries)
		start = max(0, end-100)
		return start, end
	}

	// User has scrolled - estimate visible range
	// Assuming average entry height of ~5 lines
	avgEntryHeight := 5
	viewportH := m.viewport.Height()
	visibleEntries := viewportH / avgEntryHeight
	buffer := visibleEntries / 2

	scrollPos := m.viewport.YOffset()
	estimatedStart := max(0, scrollPos/avgEntryHeight - buffer)
	estimatedEnd := min(len(m.entries), estimatedStart + visibleEntries + buffer*2)

	return estimatedStart, estimatedEnd
}

// openExternalEditor opens $EDITOR with the current input text, then replaces
// the textarea content with whatever the user wrote.
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
		tmpFile.WriteString(current)
	}
	tmpFile.Close()

	c := exec.Command(editor, tmpPath)
	return tea.ExecProcess(c, func(err error) tea.Msg {
		defer os.Remove(tmpPath)
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
	headerH := 3
	contentH := m.height - headerH - m.footerHeight()
	if contentH < 1 {
		contentH = 1
	}
	m.viewport.SetHeight(contentH)
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
func newCompletionState(kind string, items []Completion, multiSelect bool) *CompletionState {
	ti := textinput.New()
	ti.Placeholder = "type to filter..."
	ti.Focus()
	ti.CharLimit = 128

	var sel map[int]bool
	if multiSelect {
		sel = make(map[int]bool)
	}

	return &CompletionState{
		Kind:          kind,
		Filter:        ti,
		AllItems:      items,
		FilteredItems: items,
		Index:         0,
		Selected:      sel,
	}
}

func (m *Model) triggerCompletion(input string) {
	var kind string
	var items []Completion
	var multiSelect bool

	if strings.HasPrefix(input, "/") {
		kind = "command"
		items = m.completer.Complete(input)
	} else if strings.HasPrefix(input, "@") {
		kind = "attachments"
		items = m.completer.Complete(input)
		multiSelect = true
	} else if strings.HasPrefix(input, "#") {
		kind = "skills"
		items = m.completer.Complete(input)
		multiSelect = true
	}

	if len(items) == 0 {
		return
	}

	m.completionState = newCompletionState(kind, items, multiSelect)
	m.overlay = OverlayCompletion
	m.input.Blur()
}

func (m *Model) acceptCompletion() {
	cs := m.completionState
	if cs == nil || len(cs.FilteredItems) == 0 {
		return
	}

	isMultiSelect := cs.Kind == "attachments" || cs.Kind == "skills"

	if isMultiSelect {
		// Collect all selected items (indices reference AllItems)
		var selectedItems []string
		for idx := range cs.Selected {
			if idx < len(cs.AllItems) {
				selectedItems = append(selectedItems, cs.AllItems[idx].Insert)
			}
		}

		// If nothing selected, use current filtered item
		if len(selectedItems) == 0 && cs.Index < len(cs.FilteredItems) {
			selectedItems = append(selectedItems, cs.FilteredItems[cs.Index].Insert)
		}

		m.input.SetValue(strings.Join(selectedItems, " "))
		m.input.CursorEnd()
	} else {
		item := cs.FilteredItems[cs.Index]
		m.input.SetValue(item.Insert)
		m.input.CursorEnd()
	}

	m.closeCompletion()
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
func (m *Model) drillIntoFolder() {
	cs := m.completionState
	if cs == nil || cs.Index >= len(cs.FilteredItems) {
		return
	}

	item := cs.FilteredItems[cs.Index]
	folderName := strings.TrimSuffix(item.Label, "/")

	if cs.CurrentPath != "" {
		cs.CurrentPath += "/" + folderName
	} else {
		cs.CurrentPath = folderName
	}

	// Get new directory contents
	fileItems := m.completer.CompleteFilePath(cs.CurrentPath)
	cs.AllItems = fileItems
	cs.Filter.SetValue("")
	cs.FilteredItems = fileItems
	cs.Index = 0
	cs.SearchResults = nil
}

// drillUpFolder navigates to the parent folder in the @ completion modal.
func (m *Model) drillUpFolder() {
	cs := m.completionState
	if cs == nil || cs.CurrentPath == "" {
		return
	}

	// Pop last segment
	if idx := strings.LastIndex(cs.CurrentPath, "/"); idx >= 0 {
		cs.CurrentPath = cs.CurrentPath[:idx]
	} else {
		cs.CurrentPath = ""
	}

	// Re-list
	var items []Completion
	if cs.CurrentPath == "" {
		// Back to root — show agents + files
		items = m.completer.Complete("@")
	} else {
		items = m.completer.CompleteFilePath(cs.CurrentPath)
	}

	cs.AllItems = items
	cs.Filter.SetValue("")
	cs.FilteredItems = items
	cs.Index = 0
	cs.SearchResults = nil
}

func (m *Model) closeCompletion() {
	m.completionState = nil
	m.overlay = OverlayNone
	m.input.Focus()
}

// sendToAgent sends a message to the agent, setting mode context first.
func (m *Model) sendToAgent(text string) tea.Cmd {
	if m.logger != nil {
		cfg := m.modeConfigs[m.mode]
		m.logger.Info("user message", "mode", cfg.Label, "length", len(text))
	}

	m.input.Blur()
	m.state = StateWaiting
	m.recalcViewportHeight()
	m.streamBuf.Reset()
	m.evalCount = 0
	m.promptTokens = 0

	m.entries = append(m.entries, ChatEntry{
		Kind:    "user",
		Content: text,
	})
	m.viewport.SetContent(m.renderEntries())
	m.viewport.GotoBottom()

	m.agent.AddUserMessage(text)

	// Set mode context on the agent.
	cfg := m.modeConfigs[m.mode]
	m.agent.SetModeContext(cfg.SystemPromptPrefix, cfg.AllowTools)

	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel

	p := m.program

	// Set up the approval callback so tool permission prompts go through the TUI.
	m.agent.SetApprovalCallback(func(req permission.ApprovalRequest) {
		respCh := make(chan ToolApprovalResponse, 1)
		p.Send(ToolApprovalMsg{
			ToolName: req.ToolName,
			Args:     req.Args,
			Response: respCh,
		})
		resp := <-respCh
		req.Response <- permission.ApprovalResponse{
			Allowed: resp.Allowed,
			Always:  resp.Always,
		}
	})

	runAgent := func() tea.Msg {
		adapter := NewAdapter(p)
		m.agent.Run(ctx, adapter)
		return AgentDoneMsg{}
	}

	m.scramble.Reset()

	batchCmds := []tea.Cmd{m.spin.Tick, m.scramble.Tick(), runAgent}

	// Create session note on first message.
	if m.sessionNoteID == 0 && notedAvailable() {
		batchCmds = append(batchCmds, func() tea.Msg {
			ts := time.Now().Format("2006-01-02 15:04")
			id, err := createSessionNote(ts)
			return SessionCreatedMsg{NoteID: id, Err: err}
		})
	}

	return tea.Batch(batchCmds...)
}

// cycleMode advances through ASK -> PLAN -> BUILD -> ASK.
func (m *Model) cycleMode() {
	m.mode = (m.mode + 1) % 3
	cfg := m.modeConfigs[m.mode]

	// Auto-select model via router.
	if m.router != nil {
		newModel := m.router.GetModelForCapability(cfg.PreferredCapability)
		if newModel != "" && newModel != m.model {
			if m.modelManager != nil {
				if err := m.modelManager.SetCurrentModel(newModel); err == nil {
					m.model = newModel
				}
			}
		}
	}

	if m.logger != nil {
		m.logger.Info("mode switched", "mode", cfg.Label, "model", m.model)
	}

	// Show enhanced mode switch notification with toast
	modeColors := map[Mode]string{
		ModeAsk:   "#81a1c1",
		ModePlan:  "#ebcb8b",
		ModeBuild: "#a3be8c",
	}

	_ = modeColors // reserved for future visual enhancements
	toastMsg := fmt.Sprintf("⚡ Mode: %s • Model: %s", cfg.Label, m.model)
	if m.toastMgr != nil {
		m.toastMgr.AddToast(Toast{
			Message: toastMsg,
			Kind:    ToastKindInfo,
		})
	}

	// Also show in chat log for persistence
	m.entries = append(m.entries, ChatEntry{
		Kind:    "system",
		Content: fmt.Sprintf("Mode switched to %s (%s)", cfg.Label, m.model),
	})
	m.viewport.SetContent(m.renderEntries())
	m.viewport.GotoBottom()
}

// openModelPicker shows the model picker overlay.
func (m *Model) openModelPicker() {
	if m.router == nil {
		return
	}
	models := m.router.ListModels()
	if len(models) == 0 {
		return
	}

	m.modelPickerState = newModelPickerState(models, m.model, m.isDark)
	m.overlay = OverlayModelPicker
	m.input.Blur()
}

// selectModel switches to the given model and closes the picker.
func (m *Model) selectModel(name string) {
	old := m.model
	if m.modelManager != nil {
		if err := m.modelManager.SetCurrentModel(name); err != nil {
			m.entries = append(m.entries, ChatEntry{
				Kind:    "error",
				Content: fmt.Sprintf("Failed to switch model: %v", err),
			})
			m.closeModelPicker()
			return
		}
	}
	m.model = name
	if m.logger != nil {
		m.logger.Info("model switched", "from", old, "to", name)
	}
	m.entries = append(m.entries, ChatEntry{
		Kind:    "system",
		Content: fmt.Sprintf("Model: %s", name),
	})
	m.closeModelPicker()
	m.viewport.SetContent(m.renderEntries())
	m.viewport.GotoBottom()
}

// closeModelPicker dismisses the model picker overlay.
func (m *Model) closeModelPicker() {
	m.modelPickerState = nil
	m.overlay = OverlayNone
	m.input.Focus()
}

// openPlanForm shows the plan form pre-filled with the given task text.
func (m *Model) openPlanForm(task string) {
	m.planFormState = NewPlanFormState(task)
	m.overlay = OverlayPlanForm
	m.input.Blur()
}

// closePlanForm dismisses the plan form and returns focus to input.
func (m *Model) closePlanForm() {
	m.planFormState = nil
	m.overlay = OverlayNone
	m.input.Focus()
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
			m.toastMgr.Error("Clipboard error: " + err.Error())
			return SystemMessageMsg{Msg: "Clipboard error: " + err.Error()}
		}
		m.toastMgr.Success("Copied to clipboard")
		return SystemMessageMsg{Msg: "Copied to clipboard."}
	}
}

// handleMouseClick hit-tests tool entries and toggles their collapsed state.
func (m *Model) handleMouseClick(x, y int) {
	// Convert screen Y to viewport-relative position.
	vpY := y - 3 + m.viewport.YOffset() // 3 = header height
	if m.toolEntryRows == nil {
		return
	}

	for toolIdx, startRow := range m.toolEntryRows {
		if vpY >= startRow && vpY < startRow+3 {
			if toolIdx >= 0 && toolIdx < len(m.toolEntries) {
				m.toolEntries[toolIdx].Collapsed = !m.toolEntries[toolIdx].Collapsed
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
	b.WriteString(fmt.Sprintf("**Date**: %s\n", time.Now().Format("2006-01-02 15:04")))
	b.WriteString(fmt.Sprintf("**Model**: %s\n", m.model))
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
				b.WriteString(fmt.Sprintf("## Tool: %s\n\n", te.Name))
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

// parseImportedConversation parses a markdown export back into chat entries.
func (m *Model) parseImportedConversation(data string) ([]ChatEntry, error) {
	var entries []ChatEntry
	lines := strings.Split(data, "\n")

	var currentSection string
	var currentContent strings.Builder

	flushContent := func() {
		if currentContent.Len() > 0 {
			content := strings.TrimSpace(currentContent.String())
			if content != "" {
				entry := ChatEntry{Kind: currentSection, Content: content}
				entries = append(entries, entry)
			}
			currentContent.Reset()
		}
	}

	for _, line := range lines {
		if strings.HasPrefix(line, "## ") {
			flushContent()
			section := strings.TrimPrefix(line, "## ")
			switch section {
			case "User":
				currentSection = "user"
			case "Assistant":
				currentSection = "assistant"
			case "System":
				currentSection = "system"
			default:
				currentSection = "system"
			}
		} else if strings.HasPrefix(line, "---") {
			// Skip separators
		} else {
			currentContent.WriteString(line)
			currentContent.WriteString("\n")
		}
	}
	flushContent()

	// Reset tool entries since we don't import those
	m.toolEntries = nil

	return entries, nil
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
