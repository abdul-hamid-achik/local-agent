package ui

import (
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/lipgloss/v2"

	"github.com/abdul-hamid-achik/local-agent/internal/ecosystem"
)

const (
	maxToolCardSummaryWidth = 96
	maxToolCardSummaryBytes = 512
	maxToolCardResultBytes  = 2000
)

// ToolCardKind represents the type of tool operation.
type ToolCardKind int

const (
	ToolCardFile ToolCardKind = iota
	ToolCardBash
	ToolCardSearch
	ToolCardGit
	ToolCardGeneric
)

// ToolCardState represents the execution state.
type ToolCardState int

const (
	ToolCardRunning ToolCardState = iota
	ToolCardSuccess
	ToolCardAttention
	ToolCardError
)

// ToolCard is a fancy tool execution display component.
type ToolCard struct {
	ID      string
	Name    string
	Kind    ToolCardKind
	State   ToolCardState
	Summary string
	Args    string
	Result  string
	// ResultLanguage is a bounded lexer alias derived from trusted host metadata
	// while the tool call is active. It never contains a path or result bytes.
	ResultLanguage string
	StartTime      time.Time
	Duration       time.Duration
	Expanded       bool
	IsDark         bool
	// ExpertProgress is the bounded host projection for the exact built-in
	// consultation call. The adjacent cache avoids rebuilding its multi-line
	// live surface on every spinner tick.
	ExpertProgress              *ExpertProgressState
	expertProgressCache         string
	expertProgressCacheWidth    int
	expertProgressCacheSequence uint64
	Projection                  ecosystem.ToolProjection
	Styles                      ToolCardStyles
}

// ToolCardStyles holds styles for the tool card.
type ToolCardStyles struct {
	BorderRunning   lipgloss.Style
	BorderSuccess   lipgloss.Style
	BorderAttention lipgloss.Style
	BorderError     lipgloss.Style
	TitleRunning    lipgloss.Style
	TitleSuccess    lipgloss.Style
	TitleAttention  lipgloss.Style
	TitleError      lipgloss.Style
	Args            lipgloss.Style
	Result          lipgloss.Style
	Error           lipgloss.Style
	Warning         lipgloss.Style
	Dimmed          lipgloss.Style
	Elapsed         lipgloss.Style
	DiffAdded       lipgloss.Style
	DiffRemoved     lipgloss.Style
	DiffHeader      lipgloss.Style
	SearchPath      lipgloss.Style
	SearchLocation  lipgloss.Style
	SearchMatch     lipgloss.Style
}

// NewToolCardStyles creates styles based on theme.
func NewToolCardStyles(isDark bool) ToolCardStyles {
	palette := outputSemanticPalette(isDark)

	return ToolCardStyles{
		BorderRunning:   lipgloss.NewStyle().Foreground(palette.Accent2),
		BorderSuccess:   lipgloss.NewStyle().Foreground(palette.Success),
		BorderAttention: lipgloss.NewStyle().Foreground(palette.Warning),
		BorderError:     lipgloss.NewStyle().Foreground(palette.Error),
		TitleRunning:    lipgloss.NewStyle().Foreground(palette.Accent).Bold(true),
		TitleSuccess:    lipgloss.NewStyle().Foreground(palette.Success).Bold(true),
		TitleAttention:  lipgloss.NewStyle().Foreground(palette.Warning).Bold(true),
		TitleError:      lipgloss.NewStyle().Foreground(palette.Error).Bold(true),
		Args:            lipgloss.NewStyle().Foreground(palette.Muted),
		Result:          lipgloss.NewStyle().Foreground(palette.Muted),
		Error:           lipgloss.NewStyle().Foreground(palette.Error),
		Warning:         lipgloss.NewStyle().Foreground(palette.Warning),
		Dimmed:          lipgloss.NewStyle().Foreground(palette.Dim),
		Elapsed:         lipgloss.NewStyle().Foreground(palette.Accent2),
		DiffAdded:       lipgloss.NewStyle().Foreground(palette.Success),
		DiffRemoved:     lipgloss.NewStyle().Foreground(palette.Error),
		DiffHeader:      lipgloss.NewStyle().Foreground(palette.Accent),
		SearchPath:      lipgloss.NewStyle().Foreground(palette.Accent),
		SearchLocation:  lipgloss.NewStyle().Foreground(palette.Dim),
		SearchMatch:     lipgloss.NewStyle().Foreground(palette.Special),
	}
}

// NewToolCard creates a new tool card.
func NewToolCard(name string, kind ToolCardKind, isDark bool) ToolCard {
	return ToolCard{
		Name:   name,
		Kind:   kind,
		State:  ToolCardRunning,
		IsDark: isDark,
		Styles: NewToolCardStyles(isDark),
	}
}

// SetDark updates the theme.
func (c *ToolCard) SetDark(isDark bool) {
	c.IsDark = isDark
	c.Styles = NewToolCardStyles(isDark)
	if c.ExpertProgress != nil && c.expertProgressCacheWidth > 0 {
		c.setExpertProgress(c.ExpertProgress, c.expertProgressCacheWidth)
	}
}

// SetSummary stores a bounded, single-line semantic summary for compact and
// running headers. Callers should prefer this over assigning Summary directly;
// rendering applies the same bound defensively either way.
func (c *ToolCard) SetSummary(summary string) {
	c.Summary = boundedToolCardSummary(summary)
}

// statusGlyph returns a clean, single-width fallback glyph (no emoji — emoji
// are double-width in some terminals and clash with the Nord aesthetic). The
// parent may replace the running glyph through ViewWithActivity.
func (c ToolCard) statusGlyph() string {
	switch c.State {
	case ToolCardSuccess:
		return "✓"
	case ToolCardAttention:
		// Unknown means the bounded domain projection could not establish an
		// outcome. Keep that visibly distinct from a known conflict, stale
		// evidence, or another explicit attention state without painting it as a
		// failure.
		if c.Projection.Normalize().Domain == ecosystem.DomainUnknown {
			return "?"
		}
		return "!"
	case ToolCardError:
		return "✗"
	default:
		return "…"
	}
}

// getBorderStyle returns the appropriate border style.
func (c ToolCard) getBorderStyle() lipgloss.Style {
	switch c.State {
	case ToolCardRunning:
		return c.Styles.BorderRunning
	case ToolCardSuccess:
		return c.Styles.BorderSuccess
	case ToolCardAttention:
		return c.Styles.BorderAttention
	case ToolCardError:
		return c.Styles.BorderError
	default:
		return c.Styles.BorderRunning
	}
}

// getTitleStyle returns the appropriate title style.
func (c ToolCard) getTitleStyle() lipgloss.Style {
	switch c.State {
	case ToolCardRunning:
		return c.Styles.TitleRunning
	case ToolCardSuccess:
		return c.Styles.TitleSuccess
	case ToolCardAttention:
		return c.Styles.TitleAttention
	case ToolCardError:
		return c.Styles.TitleError
	default:
		return c.Styles.TitleRunning
	}
}

// View renders a stable card suitable for completed receipts, cached transcript
// content, and tests. Running cards use a static activity glyph and intentionally
// omit live elapsed time; the smart parent can provide both via ViewWithActivity.
func (c ToolCard) View(width int) string {
	return c.ViewWithActivity(width, "", 0)
}

// ViewWithActivity renders without mutating card state. The smart parent owns
// animation and elapsed-time updates and may pass one shared activity glyph plus
// an explicit elapsed duration for a running card.
func (c ToolCard) ViewWithActivity(width int, activityGlyph string, elapsed time.Duration) string {
	if width < 4 {
		width = 4
	}
	inner := width - 2 // gutter is "│ "

	titleStyle := c.getTitleStyle()
	presentationName := c.Name
	projection := c.Projection.Normalize()
	if operation := projection.Operation; operation != "" {
		presentationName = operation
	}
	presentation := presentTool(presentationName, c.Kind, c.State)
	if deferredMCPHubResult(projection) {
		presentation.label = "Result stored"
	} else if projection.Operation == "mcphub_get_result" && projection.Digest != nil {
		switch projection.Digest.Kind {
		case ecosystem.DigestMCPHubPage:
			presentation.label = "Read result page"
		case ecosystem.DigestMCPHubUnavailable:
			presentation.label = "Stored result unavailable"
		case ecosystem.DigestMCPHubCursorOutOfRange:
			presentation.label = "Result cursor invalid"
		}
	}

	// Leading glyph and trailing timing meta. Running animation is supplied by
	// the parent so every card can share one Bubbles spinner tick chain.
	var glyph, meta string
	if c.State == ToolCardRunning {
		glyph = strings.TrimSpace(activityGlyph)
		if glyph == "" || lipgloss.Width(glyph) > inner {
			glyph = titleStyle.Render(c.statusGlyph())
		}
		if c.ExpertProgress != nil && inner >= lipgloss.Width(glyph)+2 {
			disclosure := "▸"
			if c.Expanded {
				disclosure = "▾"
			}
			glyph = c.Styles.Dimmed.Render(disclosure) + " " + glyph
		}
		if elapsed > 0 {
			meta = c.Styles.Elapsed.Render(formatDuration(elapsed))
		}
	} else {
		glyph = titleStyle.Render(c.statusGlyph())
		// Completed receipts are interactive. Match the reasoning receipt's
		// disclosure grammar so expansion is discoverable without adding a noisy
		// instruction to every tool row. Preserve the lifecycle glyph, and omit
		// only the disclosure mark when the card has fewer than three inner cells.
		if inner >= lipgloss.Width(glyph)+2 {
			disclosure := "▸"
			if c.Expanded {
				disclosure = "▾"
			}
			glyph = c.Styles.Dimmed.Render(disclosure) + " " + glyph
		}
		if c.Duration > 0 {
			meta = c.Styles.Dimmed.Render("(" + formatDuration(c.Duration) + ")")
		}
	}

	// Keep at least a small, readable name. Timing yields first, then the summary,
	// when the terminal is too narrow for all semantic header fields.
	glyphW := lipgloss.Width(glyph)
	metaW := lipgloss.Width(meta)
	if meta != "" && inner-glyphW-metaW-2 < 1 {
		meta = ""
		metaW = 0
	}
	textBudget := inner - glyphW - 1
	if meta != "" {
		textBudget -= metaW + 1
	}
	nameBudget := max(0, textBudget)
	cardSummary := c.Summary
	if projected := projection.SummaryText(); projected != "" && c.State != ToolCardRunning {
		cardSummary = projected
	}
	cardSummary = toolCardSummaryWithoutRepeatedAction(cardSummary, projection.Operation)
	summary := ""
	summaryBudget := 0
	if (c.State == ToolCardRunning || !c.Expanded) && cardSummary != "" && textBudget >= 7 {
		summary = boundedToolCardSummary(cardSummary)
		summaryW := lipgloss.Width(summary)
		summaryBudget = min(summaryW, max(1, textBudget/2))
		nameBudget = textBudget - summaryBudget - 3 // " · "
		if nameBudget < 1 {
			summary = ""
			summaryBudget = 0
			nameBudget = max(0, textBudget)
		} else if nameW := lipgloss.Width(presentation.label); nameW < nameBudget {
			summaryBudget = min(summaryW, summaryBudget+(nameBudget-nameW))
			nameBudget = nameW
		}
	}
	name := truncateDisplay(presentation.label, max(0, nameBudget))
	header := glyph
	if name != "" {
		header += " " + titleStyle.Render(name)
	}
	if summary != "" && summaryBudget > 0 {
		header += c.Styles.Dimmed.Render(" · " + truncateDisplay(summary, summaryBudget))
	}
	if meta != "" {
		header += " " + meta
	}

	lines := []string{header}
	safeResult := boundedToolCardResult(c.Result)
	if c.Expanded && c.ExpertProgress != nil {
		if details := c.expertProgressDetails(inner); details != "" {
			lines = append(lines, strings.Split(details, "\n")...)
		}
	}

	if c.Expanded && c.State != ToolCardRunning {
		if presentation.differsFromRaw() {
			lines = append(lines, c.Styles.Dimmed.Render(truncateDisplay("tool: "+presentation.raw, inner)))
		}
		if c.Args != "" {
			args := sanitizeTerminalSingleLine(c.Args)
			lines = append(lines, c.Styles.Dimmed.Render(truncateDisplay("args: "+args, inner)))
		}
		for _, detail := range toolProjectionDetails(c.Projection) {
			lines = append(lines, c.Styles.Dimmed.Render(truncateDisplay(detail, inner)))
		}
	}
	if c.State == ToolCardError {
		compact := compactToolFailure(c.Name, safeResult)
		if strings.TrimSpace(safeResult) == "" && c.Projection.Specialist != "" {
			compact = describeEcosystemServer(c.Projection.Specialist).label + " reported a failed outcome"
		}
		if compact != "" {
			for _, resultLine := range strings.Split(compact, "\n") {
				lines = append(lines, c.Styles.Error.Render(truncateDisplay(resultLine, inner)))
			}
		}
		if c.Expanded {
			raw := strings.TrimRight(safeResult, "\n")
			if raw != "" && sanitizeVisibleText(raw) != sanitizeVisibleText(compact) {
				lines = append(lines, c.Styles.Dimmed.Render(truncateDisplay("details:", inner)))
				for _, resultLine := range strings.Split(raw, "\n") {
					lines = append(lines, c.Styles.Error.Render(truncateDisplay(resultLine, inner)))
				}
			}
		}
	} else if c.State == ToolCardAttention {
		lines = append(lines, c.Styles.Warning.Render(truncateDisplay(compactToolAttention(c.Projection), inner)))
		if c.Expanded {
			result := strings.TrimRight(safeResult, "\n")
			if result != "" {
				for _, resultLine := range strings.Split(result, "\n") {
					lines = append(lines, c.Styles.Result.Render(truncateDisplay(resultLine, inner)))
				}
			}
		}
	} else if c.Expanded && c.State != ToolCardRunning {
		result := strings.TrimRight(safeResult, "\n")
		if result != "" {
			isDiff := looksLikeUnifiedDiff(result)
			if isDiff {
				for _, resultLine := range strings.Split(result, "\n") {
					lines = append(lines, c.renderUnifiedDiffResultLine(truncateDisplay(resultLine, inner)))
				}
			} else {
				lines = append(lines, c.renderSemanticResultLines(result, inner)...)
			}
		}
	}

	// Prefix every line with a state-colored gutter bar.
	bar := c.getBorderStyle().Render("│")
	var b strings.Builder
	for i, ln := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(bar + " " + ln)
	}
	return b.String()
}

// toolCardSummaryWithoutRepeatedAction keeps a routed tool's specialist or
// target anchor while removing the action already expressed by its semantic
// title. For example, "Capturing webpage artifact · Hitspec" is easier to
// scan than "Capturing webpage artifact · Hitspec · capture webpage".
func toolCardSummaryWithoutRepeatedAction(summary, operation string) string {
	summary = strings.TrimSpace(summary)
	action := strings.TrimSpace(friendlyRemoteAction(operation))
	if summary == "" || action == "" || action == "tool" {
		return summary
	}
	if strings.EqualFold(summary, action) {
		return ""
	}
	suffix := " · " + action
	if len(summary) >= len(suffix) && strings.EqualFold(summary[len(summary)-len(suffix):], suffix) {
		return strings.TrimSpace(summary[:len(summary)-len(suffix)])
	}
	return summary
}

func looksLikeUnifiedDiff(result string) bool {
	lines := strings.Split(result, "\n")
	if len(lines) > 80 {
		lines = lines[:80]
	}
	var oldHeader, newHeader, hunk bool
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			return true
		case strings.HasPrefix(line, "--- "):
			oldHeader = true
		case strings.HasPrefix(line, "+++ "):
			newHeader = true
		case strings.HasPrefix(line, "@@ "):
			hunk = true
		}
	}
	return oldHeader && newHeader && hunk
}

func (c ToolCard) renderUnifiedDiffResultLine(line string) string {
	switch {
	case strings.HasPrefix(line, "diff --git "), strings.HasPrefix(line, "index "),
		strings.HasPrefix(line, "@@ "), strings.HasPrefix(line, "--- "), strings.HasPrefix(line, "+++ "):
		return c.Styles.DiffHeader.Render(line)
	case strings.HasPrefix(line, "+"):
		return c.Styles.DiffAdded.Render(line)
	case strings.HasPrefix(line, "-"):
		return c.Styles.DiffRemoved.Render(line)
	default:
		return c.Styles.Result.Render(line)
	}
}

func toolCardStateFromProjection(projection ecosystem.ToolProjection) ToolCardState {
	projection = projection.Normalize()
	if projection.Transport == ecosystem.TransportFailed || projection.Domain == ecosystem.DomainFailed {
		return ToolCardError
	}
	if projection.Transport == ecosystem.TransportRunning || projection.Domain == ecosystem.DomainPending {
		return ToolCardRunning
	}
	if projection.NeedsAttention() ||
		projection.Evidence == ecosystem.EvidenceContradicted || projection.Evidence == ecosystem.EvidenceStale {
		return ToolCardAttention
	}
	if projection.Transport == "" && projection.Domain == "" && projection.Evidence == ecosystem.EvidenceNone {
		// Older local-only receipts predate semantic projections. Preserve their
		// completed appearance without treating any partial projection as success.
		return ToolCardSuccess
	}
	if projection.Transport == ecosystem.TransportSucceeded && projection.Domain == ecosystem.DomainSucceeded {
		return ToolCardSuccess
	}
	return ToolCardAttention
}

func compactToolAttention(projection ecosystem.ToolProjection) string {
	projection = projection.Normalize()
	if deferredMCPHubResult(projection) {
		if summary := projection.SummaryText(); summary != "" {
			return summary
		}
		return "Result stored · fetch " + projection.Route.CallID
	}
	if projection.Operation == "mcphub_get_result" && projection.Digest != nil {
		if summary := projection.SummaryText(); summary != "" {
			return summary
		}
	}

	// The bounded domain projection is authoritative. Arbitrary server prose is
	// available only in expanded details and must never replace this state line.
	switch projection.Domain {
	case ecosystem.DomainConflict:
		return "Conflict reported · expand for remediation details"
	case ecosystem.DomainDrift:
		return "Drift reported · review the proposed convergence"
	case ecosystem.DomainBlocked:
		return "Blocked · inspect the requirement before retrying"
	case ecosystem.DomainUnknown:
		return "Outcome needs interpretation · inspect details before relying on it"
	case ecosystem.DomainAttention:
		return "Attention reported · inspect details before continuing"
	default:
		return "Needs attention · inspect details before continuing"
	}
}

func deferredMCPHubResult(projection ecosystem.ToolProjection) bool {
	projection = projection.Normalize()
	return projection.Route.Gateway == "mcphub" &&
		projection.Route.CallID != "" &&
		projection.Domain == ecosystem.DomainAttention &&
		projection.Operation != "mcphub_get_result"
}

func toolProjectionDetails(projection ecosystem.ToolProjection) []string {
	projection = projection.Normalize()
	if projection.Transport == "" {
		return nil
	}
	details := make([]string, 0, 8)
	if projection.Specialist != "" {
		label := describeEcosystemServer(projection.Specialist).label
		details = append(details, "specialist: "+label+" · "+string(projection.Role))
	}
	if projection.Route.Gateway != "" {
		route := "route: Local Agent → " + describeEcosystemServer(projection.Route.Gateway).label
		if projection.Route.Server != "" && projection.Route.Server != projection.Route.Gateway {
			route += " → " + describeEcosystemServer(projection.Route.Server).label
		}
		if projection.Route.Lazy {
			route += " · lazy"
		}
		details = append(details, route)
	}
	details = append(details, "transport: "+string(projection.Transport))
	domain := string(projection.Domain)
	if domain == "" {
		domain = string(ecosystem.DomainUnknown)
	}
	details = append(details, "domain: "+domain)
	evidence := string(projection.Evidence)
	if evidence == "" {
		evidence = "none"
	}
	details = append(details, "evidence: "+evidence)
	if projection.Route.CallID != "" {
		details = append(details, "stored result: "+projection.Route.CallID)
	}
	if summary := projection.SummaryText(); summary != "" {
		details = append(details, "receipt: "+summary)
	}
	return details
}

func boundedToolCardSummary(summary string) string {
	summary = sanitizeTerminalSingleLine(summary)
	if len(summary) > maxToolCardSummaryBytes {
		cut := maxToolCardSummaryBytes
		for cut > 0 && !utf8.RuneStart(summary[cut]) {
			cut--
		}
		summary = summary[:cut]
	}
	return truncateDisplay(summary, maxToolCardSummaryWidth)
}

func boundedToolCardResult(result string) string {
	result = sanitizeTerminalMultiline(result)
	if len(result) <= maxToolCardResultBytes {
		return result
	}
	cut := maxToolCardResultBytes - 3
	for cut > 0 && !utf8.RuneStart(result[cut]) {
		cut--
	}
	return result[:cut] + "..."
}

// ToolCardManager manages tool-card receipts correlated by invocation ID.
type ToolCardManager struct {
	Cards  []ToolCard
	IsDark bool
}

// NewToolCardManager creates a new manager.
func NewToolCardManager(isDark bool) ToolCardManager {
	return ToolCardManager{
		Cards:  []ToolCard{},
		IsDark: isDark,
	}
}

// AddCardWithID adds a card correlated to one concrete tool invocation.
func (m *ToolCardManager) AddCardWithID(id, name string, kind ToolCardKind, startTime time.Time) {
	card := NewToolCard(name, kind, m.IsDark)
	card.ID = id
	card.StartTime = startTime
	m.Cards = append(m.Cards, card)
}

// UpdateCardWithID completes the exact invocation, even when multiple running
// calls use the same tool name.
func (m *ToolCardManager) UpdateCardWithID(id, name string, state ToolCardState, result string, duration time.Duration) {
	m.UpdateCardSemanticWithID(id, name, state, result, duration, ecosystem.ToolProjection{})
}

// UpdateCardSemanticWithID completes a card and attaches the bounded semantic
// projection derived before raw structured MCP data was discarded.
func (m *ToolCardManager) UpdateCardSemanticWithID(id, name string, state ToolCardState, result string, duration time.Duration, projection ecosystem.ToolProjection) {
	for i := len(m.Cards) - 1; i >= 0; i-- {
		if toolCallMatches(id, name, m.Cards[i].ID, m.Cards[i].Name) && m.Cards[i].State == ToolCardRunning {
			m.Cards[i].State = state
			m.Cards[i].Result = result
			m.Cards[i].Duration = duration
			m.Cards[i].Projection = projection.Normalize()
			break
		}
	}
}

func toolCallMatches(id, name, candidateID, candidateName string) bool {
	if id != "" || candidateID != "" {
		return id != "" && id == candidateID
	}
	return name == candidateName
}

// SetDark updates theme for all cards.
func (m *ToolCardManager) SetDark(isDark bool) {
	m.IsDark = isDark
	for i := range m.Cards {
		m.Cards[i].SetDark(isDark)
	}
}
