package tui

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const maxDiffSnapshotBytes = 2 * 1024 * 1024
const maxDiffSnapshotWait = 50 * time.Millisecond

var diffSnapshotSlots = make(chan struct{}, 1)

// DiffLineKind represents the type of a diff line.
type DiffLineKind int

const (
	DiffContext DiffLineKind = iota
	DiffAdded
	DiffRemoved
)

// DiffLine is a single line in a unified diff.
type DiffLine struct {
	Kind    DiffLineKind
	Content string
}

// readFileForDiff extracts a file path from tool args and reads its content.
func readFileForDiff(rawArgs map[string]any) string {
	workDir, _ := os.Getwd()
	return readFileForDiffAt(rawArgs, workDir)
}

func readFileForDiffAt(rawArgs map[string]any, workDir string) string {
	// Snapshotting is a UI enhancement, never a reason to block Bubble Tea's
	// Update loop. One abandoned network-filesystem syscall is tolerated; later
	// snapshots fail fast while that bounded slot remains occupied.
	select {
	case diffSnapshotSlots <- struct{}{}:
	default:
		return ""
	}
	done := make(chan string, 1)
	go func() {
		defer func() { <-diffSnapshotSlots }()
		done <- readFileForDiffAtUnbounded(rawArgs, workDir)
	}()
	timer := time.NewTimer(maxDiffSnapshotWait)
	defer timer.Stop()
	select {
	case content := <-done:
		return content
	case <-timer.C:
		return ""
	}
}

func readFileForDiffAtUnbounded(rawArgs map[string]any, workDir string) string {
	for _, key := range []string{"path", "file_path", "filename", "file"} {
		if p, ok := rawArgs[key].(string); ok {
			root, err := filepath.Abs(workDir)
			if err != nil {
				return ""
			}
			if resolved, resolveErr := filepath.EvalSymlinks(root); resolveErr == nil {
				root = resolved
			}
			candidate := p
			if !filepath.IsAbs(candidate) {
				candidate = filepath.Join(root, candidate)
			}
			candidate, err = filepath.EvalSymlinks(candidate)
			if err != nil {
				return ""
			}
			rel, err := filepath.Rel(root, candidate)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return ""
			}
			info, err := os.Stat(candidate)
			if err != nil || !info.Mode().IsRegular() || info.Size() > maxDiffSnapshotBytes {
				return ""
			}
			file, err := os.Open(candidate)
			if err != nil {
				return ""
			}
			openedInfo, statErr := file.Stat()
			if statErr != nil || !openedInfo.Mode().IsRegular() || openedInfo.Size() > maxDiffSnapshotBytes {
				_ = file.Close()
				return ""
			}
			data, err := io.ReadAll(io.LimitReader(file, maxDiffSnapshotBytes+1))
			closeErr := file.Close()
			if err != nil {
				return ""
			}
			if closeErr != nil || len(data) > maxDiffSnapshotBytes {
				return ""
			}
			return string(data)
		}
	}
	return ""
}

// computeDiff computes a line-level diff between before and after text.
// Returns nil if the texts are identical.
func computeDiff(before, after string) []DiffLine {
	if before == after {
		return nil
	}

	beforeLines := splitLines(before)
	afterLines := splitLines(after)
	const maxDiffInputLines = 50_000
	if len(beforeLines)+len(afterLines) > maxDiffInputLines {
		return []DiffLine{
			{Kind: DiffRemoved, Content: fmt.Sprintf("[large diff omitted: %d lines before]", len(beforeLines))},
			{Kind: DiffAdded, Content: fmt.Sprintf("[large diff omitted: %d lines after]", len(afterLines))},
		}
	}
	const maxDiffCells = 4_000_000
	if len(beforeLines) > 0 && len(afterLines) > maxDiffCells/len(beforeLines) {
		return []DiffLine{
			{Kind: DiffRemoved, Content: fmt.Sprintf("[large diff omitted: %d lines before]", len(beforeLines))},
			{Kind: DiffAdded, Content: fmt.Sprintf("[large diff omitted: %d lines after]", len(afterLines))},
		}
	}

	lcs := lcsLines(beforeLines, afterLines)

	var all []DiffLine
	bi, ai, li := 0, 0, 0

	for li < len(lcs) {
		for bi < len(beforeLines) && beforeLines[bi] != lcs[li] {
			all = append(all, DiffLine{DiffRemoved, beforeLines[bi]})
			bi++
		}
		for ai < len(afterLines) && afterLines[ai] != lcs[li] {
			all = append(all, DiffLine{DiffAdded, afterLines[ai]})
			ai++
		}
		all = append(all, DiffLine{DiffContext, lcs[li]})
		bi++
		ai++
		li++
	}
	for bi < len(beforeLines) {
		all = append(all, DiffLine{DiffRemoved, beforeLines[bi]})
		bi++
	}
	for ai < len(afterLines) {
		all = append(all, DiffLine{DiffAdded, afterLines[ai]})
		ai++
	}

	return filterContext(all, 3)
}

// renderDiff renders diff lines with styles, capping output at maxLines.
func renderDiff(lines []DiffLine, styles Styles, maxLines int) string {
	return renderDiffAtWidth(lines, styles, maxLines, 0)
}

// renderDiffAtWidth renders a diff within a concrete terminal-cell boundary.
// A zero width preserves the historical unbounded behavior used by callers
// that do not own a viewport.
func renderDiffAtWidth(lines []DiffLine, styles Styles, maxLines, width int) string {
	if len(lines) == 0 {
		return ""
	}

	var b strings.Builder
	displayed := 0
	indent := 6
	if width > 0 && width < 48 {
		indent = 2
	}
	if width > 0 && indent >= width {
		indent = max(0, width-1)
	}
	fit := func(text string) string {
		if width <= 0 {
			return text
		}
		return truncateDisplay(text, max(1, width-indent))
	}

	for _, line := range lines {
		if maxLines > 0 && displayed >= maxLines {
			b.WriteString(styles.DiffHeader.PaddingLeft(indent).Render(fit(
				fmt.Sprintf("… %d more lines", len(lines)-displayed),
			)))
			b.WriteString("\n")
			break
		}

		switch line.Kind {
		case DiffAdded:
			b.WriteString(styles.DiffAdded.PaddingLeft(indent).Render(fit("+ " + line.Content)))
		case DiffRemoved:
			b.WriteString(styles.DiffRemoved.PaddingLeft(indent).Render(fit("- " + line.Content)))
		case DiffContext:
			b.WriteString(styles.DiffContext.PaddingLeft(indent).Render(fit("  " + line.Content)))
		}
		b.WriteString("\n")
		displayed++
	}

	return b.String()
}

// lcsLines computes the longest common subsequence of two string slices.
func lcsLines(a, b []string) []string {
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

	result := make([]string, dp[m][n])
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
