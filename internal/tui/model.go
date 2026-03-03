package tui

import (
	"context"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/abdulachik/local-agent/internal/agent"
	"github.com/abdulachik/local-agent/internal/command"
	"github.com/abdulachik/local-agent/internal/skill"
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
)

// ToolStatus represents the state of a tool execution.
type ToolStatus int

const (
	ToolStatusRunning ToolStatus = iota
	ToolStatusDone
	ToolStatusError
)

// ToolEntry tracks the lifecycle of a single tool call.
type ToolEntry struct {
	Name      string
	Args      string         // formatted args string
	RawArgs   map[string]any // original args
	Result    string
	IsError   bool
	Status    ToolStatus
	StartTime time.Time
	Duration  time.Duration
}

// ChatEntry is a single item in the chat log.
type ChatEntry struct {
	Kind            string // "user", "assistant", "tool_group", "error", "system"
	Content         string // raw content
	RenderedContent string // cached Glamour output (set once on completion)
	Name            string // tool name for tool entries
	IsError         bool   // for tool_result
	ToolIndex       int    // index into toolEntries for "tool_group" kind
}

// Model is the BubbleTea model for the chat interface.
type Model struct {
	// UI components
	viewport viewport.Model
	input    textarea.Model
	spin     spinner.Model
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

	// Completion modal
	completionActive   bool
	completionType     string // "command", "attachments", "skills"
	completionItems    []Completion
	completionIndex    int
	completionSelected map[int]bool // for multi-select modes
	completionSection  int          // which section is active
	listModel          list.Model   // bubbles/list for rendering

	// File attachments
	attachments []string

	// Tool display
	toolEntries    []ToolEntry
	toolsCollapsed bool

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
	failedServers []FailedServer

	// ICE
	iceEnabled       bool
	iceConversations int
	iceSessionID     string
}

// New creates a new TUI Model.
func New(ag *agent.Agent, cmdReg *command.Registry, skillMgr *skill.Manager, completer *Completer) *Model {
	ta := textarea.New()
	ta.Placeholder = "Ask anything... (Enter to send, ? for help)"
	ta.Focus()
	ta.CharLimit = 4096
	ta.SetHeight(1)
	ta.ShowLineNumbers = false

	s := spinner.New(
		spinner.WithSpinner(spinner.MiniDot),
		spinner.WithStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("#88c0d0"))),
	)

	return &Model{
		input:          ta,
		spin:           s,
		styles:         NewStyles(true),
		keys:           DefaultKeyMap(),
		state:          StateIdle,
		isDark:         true,
		inputLines:     1,
		toolsCollapsed: true,
		agent:          ag,
		cmdRegistry:    cmdReg,
		skillMgr:       skillMgr,
		completer:      completer,
	}
}

// SetProgram sets the tea.Program reference (must be called before Run).
func (m *Model) SetProgram(p *tea.Program) {
	m.program = p
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(
		textarea.Blink,
		tea.RequestBackgroundColor,
		m.spin.Tick,
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
		if m.ready {
			m.viewport.SetContent(m.renderEntries())
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.md = NewMarkdownRenderer(msg.Width - 2)

		headerH := 3 // title + thick rule + gap
		contentH := msg.Height - headerH - m.footerHeight()
		if contentH < 1 {
			contentH = 1
		}

		if !m.ready {
			m.viewport = viewport.New(viewport.WithWidth(msg.Width), viewport.WithHeight(contentH))
			m.viewport.SetContent(m.renderEntries())
			m.ready = true
		} else {
			m.viewport.SetWidth(msg.Width)
			m.viewport.SetHeight(contentH)
			// Re-render cached entries for new width.
			m.invalidateRenderedCache()
			m.viewport.SetContent(m.renderEntries())
		}

		m.input.SetWidth(msg.Width - 2)

	case tea.KeyPressMsg:
		// Handle overlay keys first.
		if m.overlay != OverlayNone {
			// ESC always closes the current overlay.
			if key.Matches(msg, m.keys.Cancel) {
				if m.overlay == OverlayCompletion {
					m.closeCompletion()
				} else {
					m.overlay = OverlayNone
				}
				return m, nil
			}

			// Help overlay: ? or q to dismiss, swallow everything else.
			if m.overlay == OverlayHelp {
				if msg.String() == "?" || msg.String() == "q" {
					m.overlay = OverlayNone
				}
				return m, nil
			}

			// Completion overlay: handle navigation keys.
			if m.overlay == OverlayCompletion && m.completionActive {
				switch {
				case key.Matches(msg, m.keys.CompleteUp):
					if m.completionIndex > 0 {
						m.completionIndex--
						m.listModel.Select(m.completionIndex)
					}
				case key.Matches(msg, m.keys.CompleteDown):
					if m.completionIndex < len(m.completionItems)-1 {
						m.completionIndex++
						m.listModel.Select(m.completionIndex)
					}
				case key.Matches(msg, m.keys.CompleteToggle):
					m.toggleCompletionSelection()
				case key.Matches(msg, m.keys.Send):
					m.acceptCompletion()
				case key.Matches(msg, m.keys.Complete):
					// Tab cycles to next item.
					if len(m.completionItems) > 0 {
						m.completionIndex = (m.completionIndex + 1) % len(m.completionItems)
						m.listModel.Select(m.completionIndex)
					}
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
				return m, nil
			}

		case key.Matches(msg, m.keys.ToggleTools):
			// Only toggle tools when input is empty and idle.
			if m.state == StateIdle && strings.TrimSpace(m.input.Value()) == "" {
				m.toolsCollapsed = !m.toolsCollapsed
				m.viewport.SetContent(m.renderEntries())
				return m, nil
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
				m.entries = append(m.entries, ChatEntry{
					Kind:    "system",
					Content: "New conversation started.",
				})
				m.viewport.SetContent(m.renderEntries())
				m.viewport.GotoBottom()
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
				// If completion is active, accept it first
				if m.completionActive && len(m.completionItems) > 0 {
					m.acceptCompletion()
					return m, nil
				}
				return m, m.submitInput()
			}

		case key.Matches(msg, m.keys.Complete):
			// Tab key for autocomplete
			if m.state == StateIdle && m.completer != nil {
				input := m.input.Value()
				if m.completionActive {
					// Cycle to next completion
					m.completionIndex = (m.completionIndex + 1) % len(m.completionItems)
					m.listModel.Select(m.completionIndex)
				} else {
					// Start completion
					m.triggerCompletion(input)
				}
			}
		}

	case ToolCallResultMsg:
		for i := len(m.toolEntries) - 1; i >= 0; i-- {
			if m.toolEntries[i].Name == msg.Name && m.toolEntries[i].Status == ToolStatusRunning {
				result := msg.Result
				if len(result) > 500 {
					result = result[:497] + "..."
				}
				m.toolEntries[i].Result = result
				m.toolEntries[i].IsError = msg.IsError
				m.toolEntries[i].Duration = msg.Duration
				if msg.IsError {
					m.toolEntries[i].Status = ToolStatusError
				} else {
					m.toolEntries[i].Status = ToolStatusDone
				}
				break
			}
		}
		if m.toolsPending > 0 {
			m.toolsPending--
		}
		m.viewport.SetContent(m.renderEntries())
		if !m.userScrolledUp {
			m.viewport.GotoBottom()
		}

	case SystemMessageMsg:
		m.entries = append(m.entries, ChatEntry{
			Kind:    "system",
			Content: msg.Msg,
		})
		m.viewport.SetContent(m.renderEntries())
		if !m.userScrolledUp {
			m.viewport.GotoBottom()
		}

	case ErrorMsg:
		m.entries = append(m.entries, ChatEntry{
			Kind:    "error",
			Content: msg.Msg,
		})
		m.viewport.SetContent(m.renderEntries())
		if !m.userScrolledUp {
			m.viewport.GotoBottom()
		}

	case AgentDoneMsg:
		m.flushStream()
		m.state = StateIdle
		m.userScrolledUp = false
		m.input.Focus()
		m.input.SetHeight(1)
		m.inputLines = 1
		m.recalcViewportHeight()
		m.viewport.SetContent(m.renderEntries())
		m.viewport.GotoBottom()

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
	}

	// Always update spinner so the tick chain doesn't break.
	{
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		cmds = append(cmds, cmd)
	}

	// Update sub-components.
	if m.state == StateIdle && m.overlay == OverlayNone {
		// Check input before update to detect trigger characters
		oldInput := m.input.Value()

		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		cmds = append(cmds, cmd)

		// Auto-grow textarea based on content.
		m.syncInputHeight()

		// Auto-trigger completion when user types /, @, or #
		newInput := m.input.Value()
		if m.completer != nil && !m.completionActive && len(newInput) > 0 {
			// Check if we should trigger completion
			shouldTrigger := false

			if strings.HasPrefix(newInput, "/") && !strings.HasPrefix(oldInput, "/") {
				shouldTrigger = true
			} else if strings.HasPrefix(newInput, "@") && !strings.HasPrefix(oldInput, "@") {
				shouldTrigger = true
			} else if strings.HasPrefix(newInput, "#") && !strings.HasPrefix(oldInput, "#") {
				shouldTrigger = true
			}

			if shouldTrigger {
				m.triggerCompletion(newInput)
			}
		}
	}

	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

// submitInput takes the current input, handles slash commands, or starts the agent.
func (m *Model) submitInput() tea.Cmd {
	text := strings.TrimSpace(m.input.Value())
	if text == "" {
		return nil
	}

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

	// Regular message — send to agent.
	m.input.Blur()
	m.state = StateWaiting
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

	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel

	p := m.program
	runAgent := func() tea.Msg {
		adapter := NewAdapter(p)
		m.agent.Run(ctx, adapter)
		return AgentDoneMsg{}
	}

	// Restart spinner tick chain + run agent concurrently.
	return tea.Batch(m.spin.Tick, runAgent)
}

// buildCommandContext creates a Context for slash command execution.
func (m *Model) buildCommandContext() *command.Context {
	ctx := &command.Context{
		Model:            m.model,
		ModelList:        m.modelList,
		AgentProfile:     m.agentProfile,
		AgentList:        m.agentList,
		ToolCount:        m.toolCount,
		ServerCount:      m.serverCount,
		ServerNames:      m.agent.ServerNames(),
		LoadedFile:       m.loadedFile,
		ICEEnabled:       m.iceEnabled,
		ICEConversations: m.iceConversations,
		ICESessionID:     m.iceSessionID,
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
		return nil

	case command.ActionClear:
		m.agent.ClearHistory()
		m.entries = nil
		m.toolEntries = nil
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
		m.model = result.Data
		m.entries = append(m.entries, ChatEntry{
			Kind:    "system",
			Content: result.Text,
		})
		m.viewport.SetContent(m.renderEntries())
		m.viewport.GotoBottom()
		return nil

	case command.ActionSwitchAgent:
		m.agentProfile = result.Data
		m.entries = append(m.entries, ChatEntry{
			Kind:    "system",
			Content: result.Text,
		})
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
	if m.streamBuf.Len() > 0 {
		content := m.streamBuf.String()
		var rendered string
		if m.md != nil {
			rendered = m.md.RenderFull(content)
		}
		m.entries = append(m.entries, ChatEntry{
			Kind:            "assistant",
			Content:         content,
			RenderedContent: rendered,
		})
		m.streamBuf.Reset()
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

// completionItem implements list.Item for the completion modal
type completionItem struct {
	title       string
	description string
	insert      string
	selected    bool
}

func (i completionItem) FilterValue() string { return i.title }

func (i completionItem) Title() string { return i.title }

func (i completionItem) Description() string { return i.description }

func (i completionItem) Selected() bool { return i.selected }

func (i completionItem) SetSelected(b bool) completionItem {
	i.selected = b
	return i
}

func (m *Model) initListModel(completionType string, items []Completion) {
	// Create list items from completions
	listItems := make([]list.Item, len(items))
	for i, item := range items {
		listItems[i] = completionItem{
			title:       item.Label,
			description: item.Category,
			insert:      item.Insert,
			selected:    false,
		}
	}

	// Determine title
	var title string
	switch completionType {
	case "command":
		title = "Commands"
	case "attachments":
		title = "Attach Files & Agents"
	case "skill":
		title = "Skills"
	default:
		title = "Complete"
	}

	// Create delegate with styling
	delegate := list.NewDefaultDelegate()
	delegate.ShowDescription = true

	// Initialize list model
	m.listModel = list.New(listItems, delegate, 50, 20)
	m.listModel.Title = title
	m.listModel.SetShowStatusBar(true)
	m.listModel.SetFilteringEnabled(false)
	m.listModel.Select(0)
}

func (m *Model) triggerCompletion(input string) {
	// Check if input starts with / for commands
	if strings.HasPrefix(input, "/") {
		m.completionType = "command"
		m.completionItems = m.completer.Complete(input)
		if len(m.completionItems) > 0 {
			m.completionActive = true
			m.completionIndex = 0
			m.completionSelected = nil
			m.overlay = OverlayCompletion
			m.initListModel("command", m.completionItems)
		}
		return
	}

	// Check if input starts with @ for agents/files
	if strings.HasPrefix(input, "@") {
		m.completionType = "attachments"
		m.completionItems = m.completer.Complete(input)
		if len(m.completionItems) > 0 {
			m.completionActive = true
			m.completionIndex = 0
			m.completionSelected = make(map[int]bool)
			m.overlay = OverlayCompletion
			m.initListModel("attachments", m.completionItems)
		}
		return
	}

	// Check if input starts with # for skills
	if strings.HasPrefix(input, "#") {
		m.completionType = "skills"
		m.completionItems = m.completer.Complete(input)
		if len(m.completionItems) > 0 {
			m.completionActive = true
			m.completionIndex = 0
			m.completionSelected = make(map[int]bool)
			m.overlay = OverlayCompletion
			m.initListModel("skills", m.completionItems)
		}
		return
	}
}

func (m *Model) acceptCompletion() {
	if !m.completionActive || len(m.completionItems) == 0 {
		return
	}

	isMultiSelect := m.completionType == "attachments" || m.completionType == "skills"

	if isMultiSelect {
		// For multi-select modes, Enter means "done" - collect all selected items
		var selectedItems []string
		for idx := range m.completionSelected {
			if idx < len(m.completionItems) {
				selectedItems = append(selectedItems, m.completionItems[idx].Insert)
			}
		}

		// If nothing selected, use current item
		if len(selectedItems) == 0 && m.completionIndex < len(m.completionItems) {
			selectedItems = append(selectedItems, m.completionItems[m.completionIndex].Insert)
		}

		// Build the input from selected items
		m.input.SetValue(strings.Join(selectedItems, " "))
		m.input.CursorEnd()
	} else {
		// Single select - just use the selected item
		item := m.completionItems[m.completionIndex]
		m.input.SetValue(item.Insert)
		m.input.CursorEnd()
	}

	// Close completion modal
	m.completionActive = false
	m.completionItems = nil
	m.completionIndex = 0
	m.completionSelected = nil
	m.overlay = OverlayNone
}

func (m *Model) toggleCompletionSelection() {
	if m.completionSelected == nil {
		return
	}

	// Toggle current item selection
	if m.completionSelected[m.completionIndex] {
		delete(m.completionSelected, m.completionIndex)
	} else {
		m.completionSelected[m.completionIndex] = true
	}
}

func (m *Model) closeCompletion() {
	m.completionActive = false
	m.completionItems = nil
	m.completionIndex = 0
	m.completionSelected = nil
	m.overlay = OverlayNone
}
