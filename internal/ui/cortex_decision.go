package ui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/viewport"
	"charm.land/lipgloss/v2"

	"github.com/abdul-hamid-achik/local-agent/internal/goaladvisor"
)

const (
	cortexDecisionCompactHeight = 20
	cortexDecisionCompactWidth  = 64
	cortexDecisionOptionWindow  = 4
)

// cortexDecisionPresentation is presentation-only. The durable control-plane
// item retains the safe binding; this structure is deliberately absent from
// session JSON so restart must recover the request from fresh Cortex status.
type cortexDecisionPresentation struct {
	TaskID         string
	Decision       goaladvisor.PendingDecision
	RequestSHA256  string
	Selected       int
	Notice         string
	Answering      bool
	Refreshing     bool
	OutcomeUnknown bool

	width         int
	height        int
	isDark        bool
	reducedMotion bool
	styles        Styles
	warningStyle  lipgloss.Style
	detail        viewport.Model
	cache         string
	cacheValid    bool
}

func newCortexDecisionPresentation(
	taskID string,
	decision goaladvisor.PendingDecision,
	width, height int,
	isDark, reducedMotion bool,
) (*cortexDecisionPresentation, error) {
	requestSHA256, err := decision.RequestBindingSHA256(taskID)
	if err != nil {
		return nil, err
	}
	decision.Question = sanitizeTerminalSingleLine(decision.Question)
	decision.Requester = sanitizeTerminalSingleLine(decision.Requester)
	decision.Options = append([]goaladvisor.DecisionOption(nil), decision.Options...)
	for index := range decision.Options {
		decision.Options[index].Label = sanitizeTerminalSingleLine(decision.Options[index].Label)
		decision.Options[index].Consequence = sanitizeTerminalSingleLine(decision.Options[index].Consequence)
	}
	presentation := &cortexDecisionPresentation{
		TaskID: taskID, Decision: decision, RequestSHA256: requestSHA256,
		Selected: -1, width: width, height: height, isDark: isDark, reducedMotion: reducedMotion,
	}
	presentation.SetTheme(isDark)
	presentation.resizeDetail(false)
	return presentation, nil
}

func (p *cortexDecisionPresentation) SetTheme(isDark bool) {
	if p == nil {
		return
	}
	p.isDark = isDark
	p.styles = NewStyles(isDark)
	p.warningStyle = lipgloss.NewStyle().
		Foreground(outputSemanticPalette(isDark).Warning).
		Bold(true)
	p.cacheValid = false
}

func (p *cortexDecisionPresentation) SetSize(width, height int) {
	if p == nil || (p.width == width && p.height == height) {
		return
	}
	p.width = width
	p.height = height
	p.resizeDetail(true)
	p.cacheValid = false
}

func (p *cortexDecisionPresentation) compact() bool {
	return p == nil || p.width < cortexDecisionCompactWidth || p.height < cortexDecisionCompactHeight
}

func (p *cortexDecisionPresentation) contentWidth() int {
	if p == nil {
		return 1
	}
	return inlineFormContentWidth(p.width)
}

func (p *cortexDecisionPresentation) detailHeight() int {
	if p.compact() {
		return 1
	}
	return min(3, max(2, p.height/8))
}

func (p *cortexDecisionPresentation) resizeDetail(preserveOffset bool) {
	if p == nil {
		return
	}
	offset := 0
	if preserveOffset {
		offset = p.detail.YOffset()
	}
	width := max(1, p.contentWidth()-2)
	detail := viewport.New(
		viewport.WithWidth(width),
		viewport.WithHeight(p.detailHeight()),
	)
	detail.SetContent(p.detailContent(width))
	detail.SetYOffset(offset)
	p.detail = detail
}

func (p *cortexDecisionPresentation) detailContent(width int) string {
	if p == nil || p.Selected < 0 || p.Selected >= len(p.Decision.Options) {
		return p.styles.OverlayDim.Render(truncateDisplay("Navigate to inspect an option's consequence.", width))
	}
	consequence := p.Decision.Options[p.Selected].Consequence
	if consequence == "" {
		consequence = "No consequence supplied."
	}
	return p.styles.StatusText.Render(wrapText(consequence, width))
}

func (p *cortexDecisionPresentation) move(delta int) bool {
	if p == nil || delta == 0 || p.Answering || p.Refreshing || p.OutcomeUnknown || len(p.Decision.Options) == 0 {
		return false
	}
	if p.Selected < 0 {
		if delta < 0 {
			p.Selected = len(p.Decision.Options) - 1
		} else {
			p.Selected = 0
		}
	} else {
		p.Selected = (p.Selected + delta + len(p.Decision.Options)) % len(p.Decision.Options)
	}
	p.Notice = ""
	p.resizeDetail(false)
	p.cacheValid = false
	return true
}

func (p *cortexDecisionPresentation) selectedOption() (goaladvisor.DecisionOption, bool) {
	if p == nil || p.Selected < 0 || p.Selected >= len(p.Decision.Options) {
		return goaladvisor.DecisionOption{}, false
	}
	return p.Decision.Options[p.Selected], true
}

func (p *cortexDecisionPresentation) confirm() (goaladvisor.DecisionOption, bool) {
	if p == nil || p.Answering || p.Refreshing || p.OutcomeUnknown {
		return goaladvisor.DecisionOption{}, false
	}
	option, ok := p.selectedOption()
	if !ok {
		p.Notice = "Choose an option with ↑/↓ or j/k first."
		p.cacheValid = false
		return goaladvisor.DecisionOption{}, false
	}
	return option, true
}

func (p *cortexDecisionPresentation) setAnswering() {
	if p == nil {
		return
	}
	p.Answering = true
	p.Refreshing = false
	p.OutcomeUnknown = false
	p.Notice = ""
	p.cacheValid = false
}

func (p *cortexDecisionPresentation) setRefreshing() {
	if p == nil {
		return
	}
	p.Answering = false
	p.Refreshing = true
	p.OutcomeUnknown = true
	p.Notice = ""
	p.cacheValid = false
}

func (p *cortexDecisionPresentation) setOutcomeUnknown() {
	if p == nil {
		return
	}
	p.Answering = false
	p.Refreshing = false
	p.OutcomeUnknown = true
	p.Notice = "Outcome unknown — refresh Cortex status"
	p.cacheValid = false
}

func (p *cortexDecisionPresentation) navigateDetail(keyName string) bool {
	if p == nil {
		return false
	}
	switch keyName {
	case "pgdown":
		p.detail.PageDown()
	case "pgup":
		p.detail.PageUp()
	case "ctrl+d":
		p.detail.HalfPageDown()
	case "ctrl+u":
		p.detail.HalfPageUp()
	case "home":
		p.detail.GotoTop()
	case "end":
		p.detail.GotoBottom()
	default:
		return false
	}
	p.cacheValid = false
	return true
}

func (p *cortexDecisionPresentation) View(busyMarker string) string {
	if p == nil {
		return ""
	}
	animated := (p.Answering || p.Refreshing) && !p.reducedMotion
	if p.cacheValid && !animated {
		return p.cache
	}
	content := p.renderContent(busyMarker)
	view := renderInlineFormFrame(p.styles, content, p.renderFooter(), p.width)
	if !animated {
		p.cache = view
		p.cacheValid = true
	}
	return view
}

func (p *cortexDecisionPresentation) renderContent(busyMarker string) string {
	width := p.contentWidth()
	var body strings.Builder
	title := "Cortex decision"
	if p.Decision.Sensitive {
		title += " · " + p.warningStyle.Render("sensitive")
	}
	body.WriteString(p.styles.OverlayTitle.Render(title))
	body.WriteString("\n")
	question := "Q · " + p.Decision.Question
	if p.compact() {
		body.WriteString(p.styles.StatusText.Render(truncateDisplay(question, width)))
	} else {
		body.WriteString(p.styles.StatusText.Render(limitWrappedRows(question, width, 2)))
	}
	body.WriteString("\n")

	if p.compact() {
		body.WriteString(p.renderCurrentOption(width))
		body.WriteString("\n")
		body.WriteString(p.detail.View())
	} else {
		body.WriteString(p.renderOptionWindow(width))
		body.WriteString("\n")
		body.WriteString(p.styles.OverlayAccent.Render("Consequence"))
		body.WriteString("\n")
		body.WriteString(p.detail.View())
	}

	status := ""
	switch {
	case p.Answering:
		status = "Recording answer…"
	case p.Refreshing:
		status = "Refreshing Cortex status…"
	case p.OutcomeUnknown:
		status = "Outcome unknown — refresh Cortex status"
	case p.Notice != "":
		status = p.Notice
	}
	if status != "" {
		if busyMarker != "" && (p.Answering || p.Refreshing) && !p.reducedMotion {
			status = busyMarker + " " + status
		}
		body.WriteString("\n")
		style := p.styles.OverlayDim
		if p.OutcomeUnknown && !p.Refreshing {
			style = p.styles.ErrorText
		}
		body.WriteString(style.Render(truncateDisplay(status, width)))
	}
	return body.String()
}

func (p *cortexDecisionPresentation) renderCurrentOption(width int) string {
	option, ok := p.selectedOption()
	if !ok {
		return p.styles.OverlayDim.Render(truncateDisplay("○ No option selected", width))
	}
	text := fmt.Sprintf("● %s · %s", option.ID, option.Label)
	return p.styles.FocusIndicator.Render(truncateDisplay(text, width))
}

func (p *cortexDecisionPresentation) renderOptionWindow(width int) string {
	options := p.Decision.Options
	if len(options) == 0 {
		return p.styles.OverlayDim.Render("No valid options")
	}
	start := 0
	if p.Selected >= cortexDecisionOptionWindow {
		start = p.Selected - cortexDecisionOptionWindow + 1
	}
	end := min(len(options), start+cortexDecisionOptionWindow)
	rows := make([]string, 0, end-start)
	for index := start; index < end; index++ {
		marker := "○"
		style := p.styles.StatusText
		if index == p.Selected {
			marker = "●"
			style = p.styles.FocusIndicator
		}
		row := fmt.Sprintf("%s %s · %s", marker, options[index].ID, options[index].Label)
		rows = append(rows, style.Render(truncateDisplay(row, width)))
	}
	return strings.Join(rows, "\n")
}

func (p *cortexDecisionPresentation) renderFooter() string {
	width := p.contentWidth()
	switch {
	case p.Answering || p.Refreshing:
		return truncateDisplay("esc hide · operation continues", width)
	case p.OutcomeUnknown:
		return truncateDisplay("r refresh status · esc hide", width)
	case p.compact():
		return "↑/↓ j/k choose\n" + truncateDisplay("enter confirm · esc hide", width)
	default:
		return truncateDisplay("↑/↓ or j/k choose · enter confirm · pgup/pgdn details · esc hide", width)
	}
}

func limitWrappedRows(value string, width, maxRows int) string {
	rows := strings.Split(wrapText(value, width), "\n")
	if len(rows) <= maxRows {
		return strings.Join(rows, "\n")
	}
	rows = rows[:maxRows]
	rows[maxRows-1] = truncateDisplay(rows[maxRows-1], max(1, width-1)) + "…"
	return strings.Join(rows, "\n")
}
