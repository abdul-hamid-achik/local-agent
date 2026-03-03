package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// SidePanelSectionKind represents the type of section
type SidePanelSectionKind int

const (
	SidePanelLogo SidePanelSectionKind = iota
	SidePanelModels
	SidePanelServers
	SidePanelICE
	SidePanelQuickActions
	SidePanelStartup  // NEW: for startup status messages
)

// SidePanelItem represents an item in the side panel
type SidePanelItem struct {
	Title       string
	Subtitle    string
	Kind        SidePanelSectionKind
	Icon        string
	Status      string // "connected", "failed", "active", ""
	Selectable  bool
	ID          string // for models, servers, etc.
	IsCurrent   bool   // is this the currently active item?
}

func (i SidePanelItem) TitleText() string {
	prefix := ""
	if i.Icon != "" {
		prefix = i.Icon + " "
	}
	if i.IsCurrent {
		prefix = "→ "
	}
	return prefix + i.Title
}

func (i SidePanelItem) Description() string {
	return i.Subtitle
}

func (i SidePanelItem) FilterValue() string {
	return i.Title
}

// SidePanelSection represents a collapsible section
type SidePanelSection struct {
	Title    string
	Kind     SidePanelSectionKind
	Items    []SidePanelItem
	Expanded bool
}

// SidePanelModel holds the state for the side panel
type SidePanelModel struct {
	sections     []SidePanelSection
	startupItems []StartupItem  // Startup status items
	width        int
	height       int
	visible      bool
	cursor       int
	selected     int
	isDark       bool
	styles       SidePanelStyles
}

// StartupItem represents a startup status item
type StartupItem struct {
	Label  string
	Status string // "connecting", "connected", "failed"
	Detail string
}

// SidePanelStyles holds styling for the side panel
type SidePanelStyles struct {
	Border      lipgloss.Style
	Title       lipgloss.Style
	Section     lipgloss.Style
	Item        lipgloss.Style
	Selected    lipgloss.Style
	Current     lipgloss.Style
	Connected   lipgloss.Style
	Failed      lipgloss.Style
	Dimmed      lipgloss.Style
	Logo        lipgloss.Style
	LogoTagline lipgloss.Style
}

// DefaultSidePanelStyles returns default styles
func DefaultSidePanelStyles(isDark bool) SidePanelStyles {
	return SidePanelStyles{
		Border:      lipgloss.NewStyle().Foreground(lipgloss.Color("#4c566a")),
		Title:       lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#88c0d0")),
		Section:     lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#81a1c1")),
		Item:        lipgloss.NewStyle().Foreground(lipgloss.Color("#d8dee9")),
		Selected:    lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#88c0d0")),
		Current:     lipgloss.NewStyle().Foreground(lipgloss.Color("#a3be8c")),
		Connected:   lipgloss.NewStyle().Foreground(lipgloss.Color("#a3be8c")),
		Failed:      lipgloss.NewStyle().Foreground(lipgloss.Color("#bf616a")),
		Dimmed:      lipgloss.NewStyle().Foreground(lipgloss.Color("#4c566a")),
		Logo:        lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#88c0d0")),
		LogoTagline: lipgloss.NewStyle().Foreground(lipgloss.Color("#4c566a")),
	}
}

// NewSidePanelModel creates a new side panel
func NewSidePanelModel(isDark bool) SidePanelModel {
	return SidePanelModel{
		visible:  true,  // Visible by default
		isDark:   isDark,
		styles:   DefaultSidePanelStyles(isDark),
		cursor:   0,
		selected: 0,
	}
}

// SetDark updates colors for theme
func (m *SidePanelModel) SetDark(isDark bool) {
	m.isDark = isDark
	m.styles = DefaultSidePanelStyles(isDark)
}

// SetWidth sets the panel width
func (m *SidePanelModel) SetWidth(w int) {
	m.width = w
}

// SetHeight sets the panel height
func (m *SidePanelModel) SetHeight(h int) {
	m.height = h
}

// SetStartupItems sets the startup status items
func (m *SidePanelModel) SetStartupItems(items []StartupItem) {
	m.startupItems = items
}

// Toggle visibility
func (m *SidePanelModel) Toggle() {
	m.visible = !m.visible
}

// Show the panel
func (m *SidePanelModel) Show() {
	m.visible = true
}

// Hide the panel
func (m *SidePanelModel) Hide() {
	m.visible = false
}

// IsVisible returns true if panel is shown
func (m *SidePanelModel) IsVisible() bool {
	return m.visible
}

// UpdateSections rebuilds the panel content
func (m *SidePanelModel) UpdateSections(modelName string, modelList []string, serverCount int, toolCount int, iceEnabled bool, iceConversations int) {
	m.sections = []SidePanelSection{
		// Logo section - permanent, always shown
		{
			Title:    "LOCAL AGENT",
			Kind:     SidePanelLogo,
			Expanded: true,
			Items: []SidePanelItem{
				{
					Title:    "LOCAL AGENT",
					Subtitle: "100% local · Your data never leaves",
					Kind:     SidePanelLogo,
					Icon:     "⬡",
				},
			},
		},
		// Models section
		{
			Title:    "Models",
			Kind:     SidePanelModels,
			Expanded: true,
			Items:    []SidePanelItem{},
		},
		// Servers section
		{
			Title:    "Servers",
			Kind:     SidePanelServers,
			Expanded: true,
			Items:    []SidePanelItem{},
		},
		// ICE section
		{
			Title:    "ICE",
			Kind:     SidePanelICE,
			Expanded: true,
			Items:    []SidePanelItem{},
		},
		// Quick Actions
		{
			Title:    "Quick Actions",
			Kind:     SidePanelQuickActions,
			Expanded: true,  // Show by default
			Items: []SidePanelItem{
				{Title: "Help", Subtitle: "Keyboard shortcuts", Kind: SidePanelQuickActions, Icon: "?", Selectable: true, ID: "help"},
				{Title: "Servers", Subtitle: "List connected tools", Kind: SidePanelQuickActions, Icon: "◈", Selectable: true, ID: "servers"},
				{Title: "Model", Subtitle: "Switch model", Kind: SidePanelQuickActions, Icon: "◈", Selectable: true, ID: "model"},
				{Title: "Load", Subtitle: "Add context from file", Kind: SidePanelQuickActions, Icon: "◈", Selectable: true, ID: "load"},
			},
		},
	}

	// Add models
	for _, model := range modelList {
		item := SidePanelItem{
			Title:     model,
			Kind:      SidePanelModels,
			Icon:      "◦",
			Selectable: true,
			ID:        model,
			IsCurrent: model == modelName,
		}
		if model == modelName {
			item.Icon = "→"
		}
		m.sections[1].Items = append(m.sections[1].Items, item)
	}

	// Add servers placeholder
	if serverCount > 0 {
		m.sections[2].Items = append(m.sections[2].Items, SidePanelItem{
			Title:     fmt.Sprintf("%d tools connected", toolCount),
			Kind:      SidePanelServers,
			Icon:      "✓",
			Selectable: false,
		})
	} else {
		m.sections[2].Items = append(m.sections[2].Items, SidePanelItem{
			Title:     "No servers connected",
			Kind:      SidePanelServers,
			Icon:      "○",
			Selectable: false,
		})
	}

	// Add ICE info
	if iceEnabled {
		m.sections[3].Items = append(m.sections[3].Items, SidePanelItem{
			Title:     fmt.Sprintf("%d conversations", iceConversations),
			Subtitle:  "Cross-session memory active",
			Kind:      SidePanelICE,
			Icon:      "✓",
			Selectable: false,
		})
	} else {
		m.sections[3].Items = append(m.sections[3].Items, SidePanelItem{
			Title:     "ICE disabled",
			Subtitle:  "Cross-session memory inactive",
			Kind:      SidePanelICE,
			Icon:      "○",
			Selectable: false,
		})
	}
}

// ToggleSection toggles a section's expanded state
func (m *SidePanelModel) ToggleSection(index int) {
	if index >= 0 && index < len(m.sections) {
		m.sections[index].Expanded = !m.sections[index].Expanded
	}
}

// Init initializes the component
func (m SidePanelModel) Init() tea.Cmd {
	return nil
}

// Update handles messages
func (m SidePanelModel) Update(msg tea.Msg) (SidePanelModel, tea.Cmd) {
	return m, nil
}

// View renders the side panel
func (m SidePanelModel) View() string {
	if !m.visible {
		return ""
	}

	// Ensure we have a minimum width
	width := m.width
	if width < 25 {
		width = 25
	}

	var b strings.Builder

	// Logo section - ALWAYS shown at top - simple clean text
	b.WriteString("\n")
	b.WriteString(m.styles.Logo.Render("  LOCAL AGENT"))
	b.WriteString("\n")
	b.WriteString(m.styles.LogoTagline.Render("  100% local"))
	b.WriteString("\n\n")

	// Startup items (shown during initialization)
	if len(m.startupItems) > 0 {
		for _, item := range m.startupItems {
			icon := "○"
			iconStyle := m.styles.Item
			switch item.Status {
			case "connecting":
				icon = "◌"
				iconStyle = m.styles.Section
			case "connected":
				icon = "✓"
				iconStyle = m.styles.Connected
			case "failed":
				icon = "✗"
				iconStyle = m.styles.Failed
			}
			line := fmt.Sprintf("  %s %s", icon, item.Label)
			if item.Detail != "" {
				detail := item.Detail
				// Truncate detail if too long
				maxDetail := m.width - 15
				if len(detail) > maxDetail && maxDetail > 5 {
					detail = detail[:maxDetail-3] + "..."
				}
				line += m.styles.Dimmed.Render(" · " + detail)
			}
			b.WriteString(iconStyle.Render(line))
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	// Build section-based view (skip logo section at index 0)
	for sectionIdx := 1; sectionIdx < len(m.sections); sectionIdx++ {
		section := m.sections[sectionIdx]

		// Section header
		icon := "▶"
		if section.Expanded {
			icon = "▼"
		}

		header := fmt.Sprintf("  %s %s", icon, section.Title)
		b.WriteString(m.styles.Section.Render(header))
		b.WriteString("\n")

		// Items if expanded
		if section.Expanded {
			for itemIdx, item := range section.Items {
				prefix := "  "
				if item.Icon != "" {
					prefix = fmt.Sprintf("  %s ", item.Icon)
				}

				itemStyle := m.styles.Item
				if item.IsCurrent {
					itemStyle = m.styles.Current
				}

				line := prefix + item.Title
				if item.Subtitle != "" && section.Kind != SidePanelLogo {
					subtitle := item.Subtitle
					// Truncate subtitle if too long
					maxSub := m.width - len(prefix) - len(item.Title) - 3
					if len(subtitle) > maxSub && maxSub > 5 {
						subtitle = subtitle[:maxSub-3] + "..."
					}
					line += m.styles.Dimmed.Render(" · " + subtitle)
				}

				if section.Kind == SidePanelLogo && itemIdx == 0 {
					b.WriteString(m.styles.LogoTagline.Render("  " + item.Subtitle))
				} else {
					b.WriteString(itemStyle.Render(line))
				}
				b.WriteString("\n")
			}
		}

		b.WriteString("\n")
	}

	// Footer hint
	b.WriteString("\n")
	b.WriteString(m.styles.Dimmed.Render("  ────────────────────────"))
	b.WriteString("\n")
	b.WriteString(m.styles.Dimmed.Render("  ctrl+b: toggle"))

	return b.String()
}

// Section list.Item implementation
func (s SidePanelSection) TitleText() string   { return s.Title }
func (s SidePanelSection) Description() string { return "" }
func (s SidePanelSection) FilterValue() string { return s.Title }
