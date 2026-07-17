package ui

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/abdul-hamid-achik/local-agent/internal/safeio"
)

const (
	maxDiffSnapshotBytes = 2 * 1024 * 1024
	maxDiffSnapshotWait  = 50 * time.Millisecond
	maxDiffInputLines    = 50_000
	maxDiffCells         = 4_000_000
	diffContextLines     = 3
)

var diffSnapshotSlots = make(chan struct{}, 1)

// DiffLineKind represents the type of a diff line.
type DiffLineKind int

const (
	DiffContext DiffLineKind = iota
	DiffAdded
	DiffRemoved
	DiffHunkHeader
	DiffEllipsis
	DiffOmitted
	DiffNoNewline
)

const diffNoNewlineContent = `\ No newline at end of file`

// DiffHunk is the structural range represented by one unified-diff hunk.
// Keeping the range typed means rendering and session restore never need to
// reverse-engineer coordinates from already-styled text.
type DiffHunk struct {
	OldStart int `json:"OldStart,omitempty"`
	OldCount int `json:"OldCount,omitempty"`
	NewStart int `json:"NewStart,omitempty"`
	NewCount int `json:"NewCount,omitempty"`
}

// DiffLine is a single line in a unified diff.
type DiffLine struct {
	Kind    DiffLineKind
	Content string
	OldLine int       `json:"OldLine,omitempty"`
	NewLine int       `json:"NewLine,omitempty"`
	Hunk    *DiffHunk `json:"Hunk,omitempty"`
}

type diffSnapshot struct {
	Content   string
	Available bool
}

// diffTextLine retains whether the source line ended in a newline. Comparing
// termination as part of the typed line is what makes a final-newline-only
// edit observable without smuggling sentinels into user-controlled content.
type diffTextLine struct {
	Content    string
	Terminated bool
}

// diffBuildRequest contains only the bounded, immutable data needed by the
// asynchronous patch builder. Raw tool arguments never cross this boundary.
type diffBuildRequest struct {
	Generation      uint64
	ToolID          string
	ToolName        string
	Path            string
	WorkDir         string
	Before          string
	BeforeAvailable bool
}

type diffBuildResultMsg struct {
	Generation uint64
	ToolID     string
	ToolName   string
	Lines      []DiffLine
	Available  bool
}

func buildFileDiffCmd(request diffBuildRequest) tea.Cmd {
	return func() tea.Msg {
		result := diffBuildResultMsg{
			Generation: request.Generation,
			ToolID:     request.ToolID,
			ToolName:   request.ToolName,
		}
		if request.Path == "" || !request.BeforeAvailable {
			return result
		}

		after := readDiffSnapshotForPathAt(request.Path, request.WorkDir)
		if !after.Available {
			return result
		}
		result.Lines = computeDiff(request.Before, after.Content)
		result.Available = true
		return result
	}
}

// readFileForDiff extracts a file path from tool args and reads its content.
func readFileForDiff(rawArgs map[string]any) string {
	workDir, _ := os.Getwd()
	return readFileForDiffAt(rawArgs, workDir)
}

func readFileForDiffAt(rawArgs map[string]any, workDir string) string {
	return readDiffSnapshotForArgsAt(rawArgs, workDir).Content
}

func readDiffSnapshotForArgsAt(rawArgs map[string]any, workDir string) diffSnapshot {
	return readDiffSnapshotForPathAt(diffPathFromArgs(rawArgs), workDir)
}

func diffPathFromArgs(rawArgs map[string]any) string {
	for _, key := range []string{"path", "file_path", "filename", "file"} {
		if path, ok := rawArgs[key].(string); ok {
			return path
		}
	}
	return ""
}

func readDiffSnapshotForPathAt(path, workDir string) diffSnapshot {
	if strings.TrimSpace(path) == "" {
		return diffSnapshot{}
	}
	// Snapshotting is a UI enhancement executed outside Bubble Tea's Update
	// loop. One abandoned network-filesystem syscall is tolerated; later
	// snapshots fail fast while that bounded slot remains occupied.
	select {
	case diffSnapshotSlots <- struct{}{}:
	default:
		return diffSnapshot{}
	}
	done := make(chan diffSnapshot, 1)
	go func() {
		defer func() { <-diffSnapshotSlots }()
		done <- readDiffSnapshotForPathAtUnbounded(path, workDir)
	}()
	timer := time.NewTimer(maxDiffSnapshotWait)
	defer timer.Stop()
	select {
	case snapshot := <-done:
		return snapshot
	case <-timer.C:
		return diffSnapshot{}
	}
}

func readDiffSnapshotForPathAtUnbounded(path, workDir string) diffSnapshot {
	root, relative, err := confinedDiffRelativePath(path, workDir)
	if err != nil {
		return diffSnapshot{}
	}
	file, err := safeio.OpenWithinNoFollow(root, relative)
	if errors.Is(err, os.ErrNotExist) {
		// A missing pre-write file is a valid empty side of a new-file patch.
		return diffSnapshot{Available: true}
	}
	if err != nil {
		return diffSnapshot{}
	}
	defer func() { _ = file.Close() }()
	openedInfo, statErr := file.Stat()
	if statErr != nil || !openedInfo.Mode().IsRegular() || openedInfo.Size() > maxDiffSnapshotBytes {
		return diffSnapshot{}
	}
	data, err := io.ReadAll(io.LimitReader(file, maxDiffSnapshotBytes+1))
	if err != nil || len(data) > maxDiffSnapshotBytes {
		return diffSnapshot{}
	}
	return diffSnapshot{Content: string(data), Available: true}
}

func confinedDiffRelativePath(path, workDir string) (string, string, error) {
	if strings.TrimSpace(path) == "" {
		return "", "", errors.New("empty diff path")
	}
	root, err := filepath.Abs(workDir)
	if err != nil {
		return "", "", err
	}
	candidate := path
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, candidate)
	}
	candidate, err = filepath.Abs(candidate)
	if err != nil {
		return "", "", err
	}
	rel, err := filepath.Rel(root, candidate)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", errors.New("diff path escapes workspace")
	}
	return root, rel, nil
}

// computeDiff computes a line-level diff between before and after text.
// Returns nil if the texts are identical.
func computeDiff(before, after string) []DiffLine {
	if before == after {
		return nil
	}
	if !utf8.ValidString(before) || !utf8.ValidString(after) ||
		strings.IndexByte(before, 0) >= 0 || strings.IndexByte(after, 0) >= 0 {
		return omittedBinaryDiffLines(len(before), len(after))
	}

	beforeLines := splitDiffTextLines(before)
	afterLines := splitDiffTextLines(after)
	if len(beforeLines)+len(afterLines) > maxDiffInputLines {
		return omittedDiffLines(len(beforeLines), len(afterLines))
	}
	if len(beforeLines) > 0 && len(afterLines) > maxDiffCells/len(beforeLines) {
		return omittedDiffLines(len(beforeLines), len(afterLines))
	}

	lcs := lcsComparable(beforeLines, afterLines)

	all := make([]DiffLine, 0, len(beforeLines)+len(afterLines))
	bi, ai, li := 0, 0, 0
	oldLine, newLine := 1, 1

	for li < len(lcs) {
		for bi < len(beforeLines) && beforeLines[bi] != lcs[li] {
			all = appendDiffTextLine(all, DiffRemoved, beforeLines[bi], oldLine, 0)
			bi++
			oldLine++
		}
		for ai < len(afterLines) && afterLines[ai] != lcs[li] {
			all = appendDiffTextLine(all, DiffAdded, afterLines[ai], 0, newLine)
			ai++
			newLine++
		}
		all = appendDiffTextLine(all, DiffContext, lcs[li], oldLine, newLine)
		bi++
		ai++
		li++
		oldLine++
		newLine++
	}
	for bi < len(beforeLines) {
		all = appendDiffTextLine(all, DiffRemoved, beforeLines[bi], oldLine, 0)
		bi++
		oldLine++
	}
	for ai < len(afterLines) {
		all = appendDiffTextLine(all, DiffAdded, afterLines[ai], 0, newLine)
		ai++
		newLine++
	}

	return buildDiffHunks(all, diffContextLines)
}

func appendDiffTextLine(lines []DiffLine, kind DiffLineKind, line diffTextLine, oldLine, newLine int) []DiffLine {
	lines = append(lines, DiffLine{
		Kind: kind, Content: line.Content, OldLine: oldLine, NewLine: newLine,
	})
	if !line.Terminated {
		lines = append(lines, DiffLine{Kind: DiffNoNewline, Content: diffNoNewlineContent})
	}
	return lines
}

func omittedDiffLines(beforeLines, afterLines int) []DiffLine {
	return []DiffLine{
		{Kind: DiffOmitted, Content: fmt.Sprintf("[large diff omitted: %d lines before]", beforeLines)},
		{Kind: DiffOmitted, Content: fmt.Sprintf("[large diff omitted: %d lines after]", afterLines)},
	}
}

func omittedBinaryDiffLines(beforeBytes, afterBytes int) []DiffLine {
	return []DiffLine{
		{Kind: DiffOmitted, Content: fmt.Sprintf("[binary diff omitted: %d bytes before]", beforeBytes)},
		{Kind: DiffOmitted, Content: fmt.Sprintf("[binary diff omitted: %d bytes after]", afterBytes)},
	}
}

type diffRange struct {
	start int
	end   int
}

// buildDiffHunks groups changed operations with bounded context and emits
// typed headers and omission markers. The body retains exact old/new line
// coordinates for gutters and durable session restore.
func buildDiffHunks(lines []DiffLine, contextLines int) []DiffLine {
	if len(lines) == 0 {
		return nil
	}
	if contextLines < 0 {
		contextLines = 0
	}

	var ranges []diffRange
	for index, line := range lines {
		if line.Kind != DiffAdded && line.Kind != DiffRemoved {
			continue
		}
		candidate := diffRange{
			start: max(0, index-contextLines),
			end:   min(len(lines)-1, index+contextLines),
		}
		if len(ranges) > 0 && candidate.start <= ranges[len(ranges)-1].end+1 {
			ranges[len(ranges)-1].end = max(ranges[len(ranges)-1].end, candidate.end)
			continue
		}
		ranges = append(ranges, candidate)
	}
	if len(ranges) == 0 {
		return nil
	}

	result := make([]DiffLine, 0, len(lines)+len(ranges)*2)
	previousEnd := -1
	for _, span := range ranges {
		if span.start > previousEnd+1 {
			result = append(result, DiffLine{Kind: DiffEllipsis, Content: "… unchanged lines"})
		}
		hunk := diffHunkForLines(lines[span.start : span.end+1])
		result = append(result, DiffLine{
			Kind:    DiffHunkHeader,
			Content: formatDiffHunk(hunk),
			Hunk:    &hunk,
		})
		result = append(result, lines[span.start:span.end+1]...)
		previousEnd = span.end
	}
	if previousEnd < len(lines)-1 {
		result = append(result, DiffLine{Kind: DiffEllipsis, Content: "… unchanged lines"})
	}
	return result
}

func diffHunkForLines(lines []DiffLine) DiffHunk {
	var hunk DiffHunk
	for _, line := range lines {
		if line.OldLine > 0 {
			if hunk.OldStart == 0 {
				hunk.OldStart = line.OldLine
			}
			hunk.OldCount++
		}
		if line.NewLine > 0 {
			if hunk.NewStart == 0 {
				hunk.NewStart = line.NewLine
			}
			hunk.NewCount++
		}
	}
	if hunk.OldCount == 0 && hunk.NewStart > 0 {
		hunk.OldStart = max(0, hunk.NewStart-1)
	}
	if hunk.NewCount == 0 && hunk.OldStart > 0 {
		hunk.NewStart = max(0, hunk.OldStart-1)
	}
	return hunk
}

func formatDiffHunk(hunk DiffHunk) string {
	return fmt.Sprintf("@@ -%d,%d +%d,%d @@", hunk.OldStart, hunk.OldCount, hunk.NewStart, hunk.NewCount)
}

// renderDiff renders diff lines with styles, capping output at maxLines.
func renderDiff(lines []DiffLine, styles Styles, maxLines int) string {
	return renderUnifiedDiffAtWidth("", lines, styles, maxLines, 0)
}

// renderDiffAtWidth renders a diff within a concrete terminal-cell boundary.
// A zero width preserves the historical unbounded behavior used by callers
// that do not own a viewport.
func renderDiffAtWidth(lines []DiffLine, styles Styles, maxLines, width int) string {
	return renderUnifiedDiffAtWidth("", lines, styles, maxLines, width)
}

// renderUnifiedDiffAtWidth renders a full-width unified patch with a file
// header, typed hunk headers, and old/new line-number gutters.
func renderUnifiedDiffAtWidth(path string, lines []DiffLine, styles Styles, maxLines, width int) string {
	if len(lines) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString(renderDiffFileHeader(path, lines, styles, width))
	b.WriteByte('\n')

	displayed := 0
	oldWidth, newWidth := diffLineNumberWidths(lines)
	for index, line := range lines {
		if maxLines > 0 && displayed >= maxLines {
			b.WriteString(renderDiffMetaLine(
				fmt.Sprintf("… %d more lines", len(lines)-index), styles, width,
			))
			b.WriteString("\n")
			break
		}

		switch line.Kind {
		case DiffAdded:
			b.WriteString(renderDiffBodyLine(line, "+", oldWidth, newWidth, styles.DiffAdded, width))
		case DiffRemoved:
			b.WriteString(renderDiffBodyLine(line, "-", oldWidth, newWidth, styles.DiffRemoved, width))
		case DiffContext:
			b.WriteString(renderDiffBodyLine(line, " ", oldWidth, newWidth, styles.DiffContext, width))
		case DiffHunkHeader:
			header := line.Content
			if line.Hunk != nil {
				header = formatDiffHunk(*line.Hunk)
			}
			b.WriteString(renderDiffMetaLine(header, styles, width))
		case DiffEllipsis:
			content := line.Content
			if content == "" {
				content = "… unchanged lines"
			}
			b.WriteString(renderDiffMetaLine(content, styles, width))
		case DiffOmitted:
			b.WriteString(renderDiffMetaLine(line.Content, styles, width))
		case DiffNoNewline:
			b.WriteString(renderDiffMetaLine(diffNoNewlineContent, styles, width))
		default:
			b.WriteString(renderDiffMetaLine(line.Content, styles, width))
		}
		b.WriteString("\n")
		displayed++
	}

	return b.String()
}

func renderDiffLoadingAtWidth(path string, styles Styles, width int) string {
	return renderDiffHeaderStatus(path, "diff loading", styles, width)
}

func renderDiffFileHeader(path string, lines []DiffLine, styles Styles, width int) string {
	added, removed, known := diffTotals(lines)
	status := fmt.Sprintf("+%d -%d", added, removed)
	if !known {
		status = "+? -?"
	}
	return renderDiffHeaderStatus(path, status, styles, width)
}

func renderDiffHeaderStatus(path, status string, styles Styles, width int) string {
	path = sanitizeTerminalSingleLine(path)
	if path == "" {
		path = "patch"
	}
	status = sanitizeTerminalSingleLine(status)
	pathStyle := styles.DiffHeader.PaddingLeft(0)

	if width <= 0 {
		return pathStyle.Render(path) + "  " + renderDiffStatus(status, styles)
	}
	statusWidth := lipglossWidth(status)
	if statusWidth >= width {
		return pathStyle.Render(truncateDisplay(status, width))
	}
	pathBudget := max(1, width-statusWidth-1)
	path = truncateDisplay(path, pathBudget)
	gap := max(1, width-lipglossWidth(path)-statusWidth)
	return pathStyle.Render(path) + strings.Repeat(" ", gap) + renderDiffStatus(status, styles)
}

func renderDiffStatus(status string, styles Styles) string {
	parts := strings.Fields(status)
	if len(parts) == 2 && strings.HasPrefix(parts[0], "+") && strings.HasPrefix(parts[1], "-") {
		return styles.DiffAdded.PaddingLeft(0).Render(parts[0]) + " " +
			styles.DiffRemoved.PaddingLeft(0).Render(parts[1])
	}
	return styles.DiffHeader.PaddingLeft(0).Render(status)
}

func renderDiffBodyLine(line DiffLine, marker string, oldWidth, newWidth int, style lipgloss.Style, width int) string {
	oldNumber := ""
	if line.OldLine > 0 {
		oldNumber = strconv.Itoa(line.OldLine)
	}
	newNumber := ""
	if line.NewLine > 0 {
		newNumber = strconv.Itoa(line.NewLine)
	}
	gutter := fmt.Sprintf("%*s %*s │ %s ", oldWidth, oldNumber, newWidth, newNumber, marker)
	content := sanitizeTerminalLine(line.Content)
	if width > 0 {
		content = truncateDisplay(content, max(0, width-lipglossWidth(gutter)))
	}
	return style.PaddingLeft(0).Render(gutter + content)
}

func renderDiffMetaLine(content string, styles Styles, width int) string {
	content = sanitizeTerminalSingleLine(content)
	if width > 0 {
		content = truncateDisplay(content, width)
	}
	return styles.DiffHeader.PaddingLeft(0).Render(content)
}

func diffLineNumberWidths(lines []DiffLine) (int, int) {
	maxOld, maxNew := 1, 1
	for _, line := range lines {
		maxOld = max(maxOld, line.OldLine)
		maxNew = max(maxNew, line.NewLine)
	}
	return len(strconv.Itoa(maxOld)), len(strconv.Itoa(maxNew))
}

func diffTotals(lines []DiffLine) (added, removed int, known bool) {
	known = true
	for _, line := range lines {
		switch line.Kind {
		case DiffAdded:
			added++
		case DiffRemoved:
			removed++
		case DiffOmitted:
			known = false
		}
	}
	return added, removed, known
}

// lipglossWidth is kept behind a tiny wrapper so all width arithmetic in this
// file is visibly terminal-cell based rather than byte or rune based.
func lipglossWidth(value string) int {
	return lipgloss.Width(value)
}

// lcsLines computes the longest common subsequence of two string slices.
func lcsLines(a, b []string) []string {
	return lcsComparable(a, b)
}

func lcsComparable[T comparable](a, b []T) []T {
	m, n := len(a), len(b)
	if m == 0 || n == 0 {
		return nil
	}

	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}

	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if a[i-1] == b[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else if dp[i-1][j] >= dp[i][j-1] {
				dp[i][j] = dp[i-1][j]
			} else {
				dp[i][j] = dp[i][j-1]
			}
		}
	}

	result := make([]T, dp[m][n])
	k := dp[m][n] - 1
	i, j := m, n
	for i > 0 && j > 0 {
		if a[i-1] == b[j-1] {
			result[k] = a[i-1]
			k--
			i--
			j--
		} else if dp[i-1][j] >= dp[i][j-1] {
			i--
		} else {
			j--
		}
	}

	return result
}

// filterContext keeps only diff lines near changes, with contextLines of context.
func filterContext(lines []DiffLine, contextLines int) []DiffLine {
	if len(lines) == 0 {
		return nil
	}

	keep := make([]bool, len(lines))
	for i, line := range lines {
		if line.Kind != DiffContext {
			lo := i - contextLines
			if lo < 0 {
				lo = 0
			}
			hi := i + contextLines
			if hi >= len(lines) {
				hi = len(lines) - 1
			}
			for j := lo; j <= hi; j++ {
				keep[j] = true
			}
		}
	}

	var result []DiffLine
	for i, line := range lines {
		if keep[i] {
			result = append(result, line)
		}
	}

	return result
}

// splitLines splits text into lines, removing a trailing empty line from a trailing newline.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func splitDiffTextLines(s string) []diffTextLine {
	lines := splitLines(s)
	if len(lines) == 0 {
		return nil
	}
	terminatedFinalLine := strings.HasSuffix(s, "\n")
	result := make([]diffTextLine, len(lines))
	for index, line := range lines {
		result[index] = diffTextLine{
			Content:    line,
			Terminated: index < len(lines)-1 || terminatedFinalLine,
		}
	}
	return result
}

// handleDiffBuildResult applies an asynchronously built diff receipt to its
// matching pending tool entry.
func (m *Model) handleDiffBuildResult(msg diffBuildResultMsg) {
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
		return
	}
	m.invalidateEntryCache()
	m.viewport.SetContent(m.renderEntries())
	m.gotoBottomIfFollowing()
}
