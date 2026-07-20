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
	// maxToolCardResultDisplayBytes bounds the raw ANSI display variant. Escape
	// sequences inflate byte counts over visible text, so the ceiling is a
	// multiple of the sanitized result bound.
	maxToolCardResultDisplayBytes = 4 * maxToolCardResultBytes
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
	// ResultDisplay is a transient display-only variant of Result, retained
	// only when the raw tool output carried ANSI escapes. It is re-rendered
	// through remapANSI16Line and must never be persisted or written to the
	// terminal directly; the sanitized Result stays the only durable copy.
	ResultDisplay string
	// resultDisplayLines is the production render path: the strict
	// ToolRenderModel adapter has already removed every escape sequence and
	// retained only plain segments plus allowlisted ANSI-16 style tokens.
	// ResultDisplay remains solely for isolated ToolCard compatibility tests.
	resultDisplayLines  [][]ansiRemapSegment
	resultDisplayHidden int
	// ResultLanguage is a bounded lexer alias derived from trusted host metadata
	// while the tool call is active. It never contains a path or result bytes.
	ResultLanguage string
	// OutputDigest is the scalar, persistable description of the original
	// terminal-safe output. OutputAvailable grants no access by itself; it only
	// reports whether the parent still owns a process-local viewer capability.
	OutputDigest    OutputDetailDigest
	OutputAvailable bool
	PreviewMode     ToolPreviewMode
	StartTime       time.Time
	Duration        time.Duration
	Expanded        bool
	IsDark          bool
	GlyphProfile    GlyphProfile
	Lifecycle       ToolLifecycle
	// ExpertProgress is the bounded host projection for the exact built-in
	// consultation call. The adjacent cache avoids rebuilding its multi-line
	// live surface on every spinner tick.
	ExpertProgress              *ExpertProgressState
	expertProgressCache         string
	expertProgressCacheWidth    int
	expertProgressCacheSequence uint64
	expertProgressCacheProfile  GlyphProfile
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
func NewToolCard(name string, kind ToolCardKind, isDark bool, profiles ...GlyphProfile) ToolCard {
	previewMode := ToolPreviewGeneric
	switch kind {
	case ToolCardBash:
		previewMode = ToolPreviewExec
	case ToolCardSearch:
		previewMode = ToolPreviewSearch
	case ToolCardFile:
		if classifyTool(name) == ToolTypeFileWrite {
			previewMode = ToolPreviewEdit
		} else {
			previewMode = ToolPreviewRead
		}
	}
	return ToolCard{
		Name:         name,
		Kind:         kind,
		State:        ToolCardRunning,
		PreviewMode:  previewMode,
		IsDark:       isDark,
		GlyphProfile: resolveGlyphProfile(profiles...),
		Lifecycle:    ToolLifecycleRunning,
		Styles:       NewToolCardStyles(isDark),
	}
}

// ToolCardFromRenderModel constructs the dumb visual component exclusively
// from the strict render projection. It never reads ToolEntry or Model state.
func ToolCardFromRenderModel(model ToolRenderModel, isDark bool, profiles ...GlyphProfile) (ToolCard, error) {
	if err := model.Validate(); err != nil {
		return ToolCard{}, err
	}
	card := NewToolCard(model.ToolName, toolCardKindFromViewKind(model.Kind), isDark, profiles...)
	card.ID = model.InvocationID
	card.State = toolCardStateFromLifecycle(model.Lifecycle)
	card.Lifecycle = model.Lifecycle
	card.SetSummary(model.Summary)
	card.Args = model.Preview.Arguments
	card.Result = model.Preview.Result
	card.ResultLanguage = model.Preview.ResultLanguage
	card.OutputDigest = model.Preview.OutputDigest
	card.OutputAvailable = model.Preview.OutputAvailable
	card.PreviewMode = model.Preview.Mode
	card.StartTime = model.Preview.StartedAt
	card.Duration = model.Duration
	card.Expanded = model.Preview.Expanded
	card.ExpertProgress = cloneExpertProgressState(model.Preview.ExpertProgress)
	card.Projection = model.Projection.Normalize()
	card.Projection.Operation = model.Operation
	card.resultDisplayLines = cloneANSIResultPreview(model.Preview.ansiResultLines)
	card.resultDisplayHidden = model.Preview.ansiHiddenLines
	return card, nil
}

func toolCardKindFromViewKind(kind ToolKind) ToolCardKind {
	switch kind {
	case ToolKindFile:
		return ToolCardFile
	case ToolKindShell:
		return ToolCardBash
	case ToolKindSearch:
		return ToolCardSearch
	case ToolKindGit:
		return ToolCardGit
	default:
		return ToolCardGeneric
	}
}

func toolCardStateFromLifecycle(lifecycle ToolLifecycle) ToolCardState {
	switch lifecycle {
	case ToolLifecycleSucceeded:
		return ToolCardSuccess
	case ToolLifecycleAttention, ToolLifecycleCancelled:
		return ToolCardAttention
	case ToolLifecycleFailed:
		return ToolCardError
	default:
		return ToolCardRunning
	}
}

func cloneANSIResultPreview(lines [][]ansiRemapSegment) [][]ansiRemapSegment {
	if len(lines) == 0 {
		return nil
	}
	result := make([][]ansiRemapSegment, len(lines))
	for index := range lines {
		result[index] = append([]ansiRemapSegment(nil), lines[index]...)
	}
	return result
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
	c.Summary = boundedToolCardSummary(summary, c.GlyphProfile)
}

// statusGlyph returns a clean, single-width fallback glyph (no emoji — emoji
// are double-width in some terminals and clash with the Nord aesthetic). The
// parent may replace the running glyph through ViewWithActivity.
func (c ToolCard) statusGlyph() string {
	glyphs := glyphSet(resolveGlyphProfile(c.GlyphProfile))
	if c.Lifecycle == ToolLifecycleCancelled {
		return glyphs.Cancelled
	}
	switch c.State {
	case ToolCardSuccess:
		return glyphs.Success
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
		return glyphs.Error
	default:
		if c.GlyphProfile == GlyphASCII {
			return glyphs.Running
		}
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
	glyphs := glyphSet(resolveGlyphProfile(c.GlyphProfile))
	truncate := func(value string, cells int) string {
		return truncateDisplayWithGlyphProfile(value, cells, c.GlyphProfile)
	}
	summarySeparator := glyphSeparator(c.GlyphProfile)

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
	headerDuration := c.Duration
	if c.State == ToolCardRunning {
		headerDuration = elapsed
	}
	headerSummary := boundedToolCardSummary(c.Summary, c.GlyphProfile)
	if projected := projection.SummaryText(); projected != "" && c.State != ToolCardRunning {
		headerSummary = boundedToolCardSummary(projected, c.GlyphProfile)
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
			disclosure := glyphs.Collapsed
			if c.Expanded {
				disclosure = glyphs.Expanded
			}
			glyph = c.Styles.Dimmed.Render(disclosure) + " " + glyph
		}
		if headerDuration > 0 {
			meta = c.Styles.Elapsed.Render(formatDuration(headerDuration))
		}
	} else {
		glyph = titleStyle.Render(c.statusGlyph())
		// Completed receipts are interactive. Match the reasoning receipt's
		// disclosure grammar so expansion is discoverable without adding a noisy
		// instruction to every tool row. Preserve the lifecycle glyph, and omit
		// only the disclosure mark when the card has fewer than three inner cells.
		if inner >= lipgloss.Width(glyph)+2 {
			disclosure := glyphs.Collapsed
			if c.Expanded {
				disclosure = glyphs.Expanded
			}
			glyph = c.Styles.Dimmed.Render(disclosure) + " " + glyph
		}
		if headerDuration > 0 {
			meta = c.Styles.Dimmed.Render("(" + formatDuration(headerDuration) + ")")
		}
	}

	// Project the header into terminal-cell budgets before applying styles.
	// Duration is tertiary metadata and disappears first under pressure; the
	// summary starts with at most half and may use only cells a short operation
	// leaves otherwise empty.
	cardSummary := toolCardSummaryWithoutRepeatedAction(
		headerSummary,
		projection.Operation,
		c.GlyphProfile,
	)
	wantSummary := (c.State == ToolCardRunning || !c.Expanded) && cardSummary != ""
	budget := projectToolHeaderCellBudget(
		inner,
		lipgloss.Width(glyph),
		lipgloss.Width(presentation.label),
		lipgloss.Width(cardSummary),
		lipgloss.Width(meta),
		wantSummary,
	)
	if !budget.ShowDuration {
		meta = ""
	}
	name := truncate(presentation.label, budget.NameCells)
	header := glyph
	if name != "" {
		header += " " + titleStyle.Render(name)
	}
	if budget.SummaryCells > 0 {
		header += c.Styles.Dimmed.Render(summarySeparator + truncate(cardSummary, budget.SummaryCells))
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
			lines = append(lines, c.Styles.Dimmed.Render(truncate("tool: "+presentation.raw, inner)))
		}
		if c.Args != "" {
			args := sanitizeTerminalSingleLine(c.Args)
			lines = append(lines, c.Styles.Dimmed.Render(truncate("args: "+args, inner)))
		}
		for _, detail := range toolProjectionDetails(c.Projection, c.GlyphProfile) {
			lines = append(lines, c.Styles.Dimmed.Render(truncate(detail, inner)))
		}
	}
	if c.State == ToolCardError {
		compact := compactToolFailure(c.Name, safeResult)
		if strings.TrimSpace(safeResult) == "" && c.Projection.Specialist != "" {
			compact = describeEcosystemServer(c.Projection.Specialist).label + " reported a failed outcome"
		}
		if compact != "" {
			for _, resultLine := range strings.Split(compact, "\n") {
				lines = append(lines, c.Styles.Error.Render(truncate(resultLine, inner)))
			}
		}
		if c.Expanded {
			raw := strings.TrimRight(safeResult, "\n")
			if raw != "" && sanitizeVisibleText(raw) != sanitizeVisibleText(compact) {
				lines = append(lines, c.Styles.Dimmed.Render(truncate("details:", inner)))
				lines = append(lines, c.renderStyledResultPreview(
					raw,
					inner,
					func(resultLine string) string {
						return c.Styles.Error.Render(truncate(resultLine, inner))
					},
				)...)
			}
		}
	} else if c.State == ToolCardAttention {
		attention := compactToolAttention(c.Projection, c.GlyphProfile)
		if c.Lifecycle == ToolLifecycleCancelled {
			attention = "Cancelled before completion"
		}
		lines = append(lines, c.Styles.Warning.Render(truncate(attention, inner)))
		if c.Expanded {
			if displayLines := c.remappedDisplayResultLines(inner); len(displayLines) > 0 {
				lines = append(lines, displayLines...)
			} else if result := strings.TrimRight(safeResult, "\n"); result != "" {
				lines = append(lines, c.renderStyledResultPreview(
					result,
					inner,
					func(resultLine string) string {
						return c.Styles.Result.Render(truncate(resultLine, inner))
					},
				)...)
			}
		}
	} else if c.Expanded && c.State != ToolCardRunning {
		result := strings.TrimRight(safeResult, "\n")
		if result != "" {
			isDiff := looksLikeUnifiedDiff(result)
			if isDiff {
				lines = append(lines, c.renderStyledResultPreview(
					result,
					inner,
					func(resultLine string) string {
						return c.renderUnifiedDiffResultLine(truncate(resultLine, inner))
					},
				)...)
			} else {
				lines = append(lines, c.renderSemanticResultLines(result, inner)...)
			}
		}
	}

	// Prefix every line with a state-colored gutter bar.
	bar := c.getBorderStyle().Render(glyphs.Vertical)
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
func toolCardSummaryWithoutRepeatedAction(
	summary,
	operation string,
	profiles ...GlyphProfile,
) string {
	profile := resolveGlyphProfile(profiles...)
	summary = toolCardTextForGlyphProfile(strings.TrimSpace(summary), profile)
	action := strings.TrimSpace(friendlyRemoteAction(operation))
	if summary == "" || action == "" || action == "tool" {
		return summary
	}
	if strings.EqualFold(summary, action) {
		return ""
	}
	suffix := glyphSeparator(profile) + action
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

func compactToolAttention(
	projection ecosystem.ToolProjection,
	profiles ...GlyphProfile,
) string {
	projection = projection.Normalize()
	profile := resolveGlyphProfile(profiles...)
	separator := glyphSeparator(profile)
	if deferredMCPHubResult(projection) {
		if summary := projection.SummaryText(); summary != "" {
			return toolCardTextForGlyphProfile(summary, profile)
		}
		return "Result stored" + separator + "fetch " + projection.Route.CallID
	}
	if projection.Operation == "mcphub_get_result" && projection.Digest != nil {
		if summary := projection.SummaryText(); summary != "" {
			return toolCardTextForGlyphProfile(summary, profile)
		}
	}

	// The bounded domain projection is authoritative. Arbitrary server prose is
	// available only in expanded details and must never replace this state line.
	switch projection.Domain {
	case ecosystem.DomainConflict:
		return "Conflict reported" + separator + "expand for remediation details"
	case ecosystem.DomainDrift:
		return "Drift reported" + separator + "review the proposed convergence"
	case ecosystem.DomainBlocked:
		return "Blocked" + separator + "inspect the requirement before retrying"
	case ecosystem.DomainUnknown:
		return "Outcome needs interpretation" + separator + "inspect details before relying on it"
	case ecosystem.DomainAttention:
		return "Attention reported" + separator + "inspect details before continuing"
	default:
		return "Needs attention" + separator + "inspect details before continuing"
	}
}

func deferredMCPHubResult(projection ecosystem.ToolProjection) bool {
	projection = projection.Normalize()
	return projection.Route.Gateway == "mcphub" &&
		projection.Route.CallID != "" &&
		projection.Domain == ecosystem.DomainAttention &&
		projection.Operation != "mcphub_get_result"
}

func toolProjectionDetails(projection ecosystem.ToolProjection, profiles ...GlyphProfile) []string {
	projection = projection.Normalize()
	if projection.Transport == "" {
		return nil
	}
	profile := resolveGlyphProfile(profiles...)
	separator := glyphSeparator(profile)
	details := make([]string, 0, 8)
	if projection.Specialist != "" {
		label := describeEcosystemServer(projection.Specialist).label
		details = append(details, "specialist: "+label+separator+string(projection.Role))
	}
	if projection.Route.Gateway != "" {
		right := glyphSet(profile).Right
		route := "route: Local Agent " + right + " " + describeEcosystemServer(projection.Route.Gateway).label
		if projection.Route.Server != "" && projection.Route.Server != projection.Route.Gateway {
			route += " " + right + " " + describeEcosystemServer(projection.Route.Server).label
		}
		if projection.Route.Lazy {
			route += separator + "lazy"
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
		details = append(details, "receipt: "+toolCardTextForGlyphProfile(summary, profile))
	}
	return details
}

func toolCardTextForGlyphProfile(value string, profile GlyphProfile) string {
	if resolveGlyphProfile(profile) != GlyphASCII {
		return value
	}
	value = strings.ReplaceAll(value, "…", "...")
	return strings.ReplaceAll(value, "·", "|")
}

func boundedToolCardSummary(summary string, profiles ...GlyphProfile) string {
	profile := resolveGlyphProfile(profiles...)
	summary = sanitizeTerminalSingleLine(summary)
	summary = toolCardTextForGlyphProfile(summary, profile)
	if len(summary) > maxToolCardSummaryBytes {
		cut := maxToolCardSummaryBytes
		for cut > 0 && !utf8.RuneStart(summary[cut]) {
			cut--
		}
		summary = summary[:cut]
	}
	return truncateDisplayWithGlyphProfile(summary, maxToolCardSummaryWidth, profile)
}

// boundedToolCardResultDisplay bounds the raw ANSI display variant without
// sanitizing it; remapANSI16Line re-derives safe styled output at render time
// and drops every unrecognized or unterminated escape, so a byte-boundary cut
// inside a sequence cannot leak. Newlines are normalized the same way as the
// sanitized path to keep the two variants line-aligned.
func boundedToolCardResultDisplay(result string) string {
	result = strings.ReplaceAll(strings.ReplaceAll(strings.ToValidUTF8(result, "�"), "\r\n", "\n"), "\r", "\n")
	if len(result) <= maxToolCardResultDisplayBytes {
		return result
	}
	cut := maxToolCardResultDisplayBytes
	for cut > 0 && !utf8.RuneStart(result[cut]) {
		cut--
	}
	return result[:cut]
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

func toolCallMatches(id, name, candidateID, candidateName string) bool {
	if id != "" || candidateID != "" {
		return id != "" && id == candidateID
	}
	return name == candidateName
}
