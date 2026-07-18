package ui

const (
	// ProseTargetCandidate is the initial readable-line target from the TUI
	// design study. It is deliberately separate from WorkWidth: prose may stop
	// here, while code fences, tables, diffs, logs, and inspectors keep the
	// complete component width.
	ProseTargetCandidate = 96

	transcriptContentChromeColumns = 6
	transcriptMinimumWorkColumns   = 14
	layoutMinUnifiedCodeColumns    = 40
	layoutMinSplitCodeColumns      = 52
	layoutDiffGutterColumns        = 6
	layoutDualGutterColumns        = 12
	layoutSplitGapColumns          = 1
	layoutContextChatColumns       = 72
	layoutContextPanelColumns      = 30
	layoutContextGapColumns        = 1
	layoutContextRows              = 12
	layoutRichHeaderColumns        = 48
	layoutRichHeaderRows           = 6
	layoutAuxiliaryRows            = 4
	layoutAuxiliaryGapRows         = 1
	layoutAgentPreviewColumns      = 48
	layoutAgentPreviewRows         = 8
	layoutAgentRailColumns         = 2
)

// LayoutDensity is a presentation choice made after measuring a component.
// It must not be used as a substitute for a physical capability check.
type LayoutDensity uint8

const (
	LayoutDensityCompact LayoutDensity = iota
	LayoutDensityRegular
	LayoutDensitySpacious
)

// LayoutCapabilityOptions contains explicit user presentation preferences.
// ForceCompact changes density, but not the component's measured capacity.
type LayoutCapabilityOptions struct {
	ForceCompact bool
}

// LayoutCapabilities is the immutable geometry contract for one component.
// WorkRect must be the component's final allocated rectangle, after parent
// splits and insets. Every width capability is calculated from residual work
// cells rather than from the outer terminal width.
type LayoutCapabilities struct {
	WorkRect   CellRect
	WorkWidth  int
	WorkHeight int
	ProseWidth int

	WidthClass  WidthClass
	HeightClass HeightClass
	Density     LayoutDensity

	CanDockContext      bool
	CanShowRichHeader   bool
	CanShowDiffGutters  bool
	CanShowDualGutters  bool
	CanUseSplitDiff     bool
	CanStackAuxiliary   bool
	CanShowAgentPreview bool
}

// DeriveLayoutCapabilities measures the final work rectangle for a component.
// The fixed numbers below are named design tokens. In particular, diff and
// split decisions require the minimum readable code width to remain after
// gutters and gaps have been subtracted.
func DeriveLayoutCapabilities(workRect CellRect, options LayoutCapabilityOptions) LayoutCapabilities {
	workRect = workRect.canonical()
	workWidth := workRect.Width()
	workHeight := workRect.Height()
	widthClass := ClassifyWidth(workWidth)
	heightClass := ClassifyHeight(workHeight)

	density := LayoutDensityRegular
	switch {
	case options.ForceCompact || widthClass <= WidthNarrow || heightClass <= HeightShort:
		density = LayoutDensityCompact
	case widthClass == WidthWide && heightClass >= HeightRegular:
		density = LayoutDensitySpacious
	}

	diffBodyWidth := residualColumns(workWidth, layoutDiffGutterColumns)
	dualGutterBodyWidth := residualColumns(workWidth, layoutDualGutterColumns)
	splitBodyWidth := residualColumns(
		workWidth,
		2*layoutDiffGutterColumns+layoutSplitGapColumns,
	)

	return LayoutCapabilities{
		WorkRect:    workRect,
		WorkWidth:   workWidth,
		WorkHeight:  workHeight,
		ProseWidth:  min(ProseTargetCandidate, workWidth),
		WidthClass:  widthClass,
		HeightClass: heightClass,
		Density:     density,

		CanDockContext: workWidth >= layoutContextChatColumns+
			layoutContextGapColumns+layoutContextPanelColumns &&
			workHeight >= layoutContextRows,
		CanShowRichHeader:  workWidth >= layoutRichHeaderColumns && workHeight >= layoutRichHeaderRows,
		CanShowDiffGutters: diffBodyWidth >= layoutMinUnifiedCodeColumns,
		CanShowDualGutters: dualGutterBodyWidth >= layoutMinUnifiedCodeColumns,
		CanUseSplitDiff:    splitBodyWidth >= 2*layoutMinSplitCodeColumns,
		CanStackAuxiliary: workHeight >= minTranscriptRows+
			layoutAuxiliaryGapRows+layoutAuxiliaryRows,
		CanShowAgentPreview: residualColumns(workWidth, layoutAgentRailColumns) >=
			layoutAgentPreviewColumns && workHeight >= layoutAgentPreviewRows,
	}
}

func residualColumns(width int, reserved ...int) int {
	for _, cells := range reserved {
		width -= max(0, cells)
	}
	return max(0, width)
}
