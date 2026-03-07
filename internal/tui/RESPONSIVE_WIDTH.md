# Responsive Width Implementation

## Overview
This document describes the responsive width calculations implemented to prevent horizontal scrolling in the TUI chat interface.

## Width Calculation Hierarchy

### 1. Viewport Width (Primary Constraint)
The viewport is the main container for chat content. All other widths derive from this.

**Formula** (from `model.go:373-380`):
```go
viewportWidth := screenWidth - 1
if sidePanel.IsVisible() {
    viewportWidth = screenWidth - panelWidth - 2
}
if viewportWidth < 20 {
    viewportWidth = 20  // minimum width
}
```

**Breakdown**:
- `screenWidth - 1`: Full width minus right edge padding (when panel hidden)
- `screenWidth - panelWidth - 2`: Width minus panel and separator line (when panel visible)
- Minimum 20 characters to ensure readability

### 2. Content Width (Text Wrapping)
Used for wrapping text in `renderEntries()`, `renderUserMsg()`, `renderAssistantMsg()`, etc.

**Formula** (from `view.go:422-429`):
```go
contentW := screenWidth - 4
if sidePanel.IsVisible() {
    contentW = screenWidth - panelWidth - 5
}
if contentW < 20 {
    contentW = 20
}
```

**Breakdown**:
- `screenWidth - 4`: Full width with 2-char padding on each side
- `screenWidth - panelWidth - 5`: Accounts for panel, separator, and padding
- Minimum 20 characters

### 3. Markdown Width (Glamour Rendering)
Used for rendering markdown content via Glamour.

**Formula** (from `model.go:382-386`):
```go
markdownWidth := viewportWidth - 3
if markdownWidth < 20 {
    markdownWidth = 20
}
```

**Breakdown**:
- Derived from viewport width minus 3 chars for padding/indentation
- Minimum 20 characters

### 4. Input Width
Matches viewport width exactly for unified appearance.

**Formula** (from `model.go:431`):
```go
input.SetWidth(viewportWidth)
```

## Panel Width Calculation

Panel width is dynamic based on screen size (from `model.go:365-371`):

```go
panelWidth := 30  // default
if screenWidth < 100 {
    panelWidth = 25
} else if screenWidth > 160 {
    panelWidth = 40
}
```

## Layout Constraints

### With Panel Visible
```
┌─────────────────────────────────────────────────┐
│ Panel (25-40) ││ Chat Viewport                 │
│               ││ (screen - panel - 2)           │
│               ││                                │
│               ││ Content wrapped to:            │
│               ││ (screen - panel - 5)           │
└─────────────────────────────────────────────────┘
```

### Without Panel
```
┌─────────────────────────────────────────────────┐
│ Chat Viewport (screen - 1)                     │
│                                                 │
│ Content wrapped to: (screen - 4)               │
└─────────────────────────────────────────────────┘
```

## Critical Invariants

The following invariants are enforced to prevent horizontal scrolling:

1. **viewportWidth ≤ screenWidth - 1** (or `screenWidth - panelWidth - 1` when panel visible)
2. **contentWidth ≤ viewportWidth**
3. **markdownWidth ≤ viewportWidth**
4. **All widths ≥ 20** (minimum readability)

## Test Coverage

Comprehensive tests in `width_test.go` verify:

- `TestViewportWidthCalculation`: Validates width calculations for various screen sizes
- `TestResponsiveWidthToggle`: Ensures widths adjust correctly when panel is toggled
- `TestMinimumWidthConstraints`: Verifies minimum width enforcement on small screens
- `TestRenderedTextWidth`: Tests actual text wrapping behavior
- `TestLayoutConsistency`: Exhaustive testing across screen sizes 40-200 chars

## Example Calculations

### 120-char screen with panel (30 chars)
```
Viewport:   120 - 30 - 2 = 88 chars
Content:    120 - 30 - 5 = 85 chars
Markdown:   88 - 3 = 85 chars
Input:      88 chars
Total:      30 (panel) + 1 (separator) + 88 (viewport) = 119 ✓
```

### 80-char screen without panel
```
Viewport:   80 - 1 = 79 chars
Content:    80 - 4 = 76 chars
Markdown:   79 - 3 = 76 chars
Input:      79 chars
Total:      79 chars ✓
```

### 40-char screen with panel (25 chars) - Edge Case
```
Viewport:   40 - 25 - 2 = 13 → 20 (minimum enforced)
Content:    40 - 25 - 5 = 10 → 20 (minimum enforced)
Markdown:   20 - 3 = 17 → 20 (minimum enforced)
Input:      20 chars
Total:      25 + 1 + 20 = 46 (exceeds screen, but minimum width takes priority)
```

**Note**: On very small screens (< 46 chars with panel), the minimum width constraints take precedence. Users should be advised to use larger terminal windows for optimal experience.

## Responsive Behavior

When the side panel is toggled:
1. Viewport width recalculates immediately
2. Content is re-wrapped to new width via `invalidateRenderedCache()`
3. Markdown renderer is recreated with new width
4. Input field resizes to match viewport

This ensures seamless responsive behavior without horizontal scrolling.
