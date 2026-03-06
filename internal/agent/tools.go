package agent

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/abdul-hamid-achik/local-agent/internal/llm"
	"github.com/abdul-hamid-achik/local-agent/internal/tools"
)

const (
	defaultTimeout = 30 * time.Second
	maxTimeout     = 120 * time.Second
	maxGrepResults = 500
)

func (a *Agent) toolsBuiltinToolDefs() []llm.ToolDef {
	return tools.AllToolDefs()
}

func (a *Agent) isToolsTool(name string) bool {
	return tools.IsBuiltinTool(name)
}

func (a *Agent) handleToolsTool(tc llm.ToolCall) (string, bool) {
	switch tc.Name {
	case "grep":
		return a.handleGrep(tc.Arguments)
	case "read":
		return a.handleRead(tc.Arguments)
	case "write":
		return a.handleWrite(tc.Arguments)
	case "glob":
		return a.handleGlob(tc.Arguments)
	case "bash":
		return a.handleBash(tc.Arguments)
	case "ls":
		return a.handleLs(tc.Arguments)
	case "find":
		return a.handleFind(tc.Arguments)
	case "diff":
		return a.handleDiff(tc.Arguments)
	case "edit":
		return a.handleEdit(tc.Arguments)
	case "mkdir":
		return a.handleMkdir(tc.Arguments)
	case "remove":
		return a.handleRemove(tc.Arguments)
	case "copy":
		return a.handleCopy(tc.Arguments)
	case "move":
		return a.handleMove(tc.Arguments)
	case "exists":
		return a.handleExists(tc.Arguments)
	default:
		return fmt.Sprintf("unknown tool: %s", tc.Name), true
	}
}

func (a *Agent) handleGrep(args map[string]any) (string, bool) {
	pattern, _ := args["pattern"].(string)
	if pattern == "" {
		return "error: pattern is required", true
	}

	path := a.getArgString(args, "path", a.workDir)
	include := a.getArgString(args, "include", "")
	context := a.getArgInt(args, "context", 3)

	if _, err := os.Stat(path); err != nil {
		return fmt.Sprintf("error: path does not exist: %s", path), true
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		return fmt.Sprintf("error: invalid regex pattern: %v", err), true
	}

	var results []string
	err = filepath.Walk(path, func(filePath string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		if info.IsDir() {
			if shouldSkipDir(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}

		if include != "" {
			matched, err := filepath.Match(include, info.Name())
			if err != nil || !matched {
				return nil
			}
		}

		if strings.HasPrefix(info.Name(), ".") {
			return nil
		}

		content, err := os.ReadFile(filePath)
		if err != nil {
			return nil
		}

		lines := strings.Split(string(content), "\n")
		for i, line := range lines {
			if re.MatchString(line) {
				relPath, _ := filepath.Rel(path, filePath)

				ctxStart := i - context
				if ctxStart < 0 {
					ctxStart = 0
				}
				ctxEnd := i + context + 1
				if ctxEnd > len(lines) {
					ctxEnd = len(lines)
				}

				results = append(results, fmt.Sprintf("%s:%d: %s", relPath, i+1, line))

				if context > 0 && ctxStart < i {
					for j := ctxStart; j < i; j++ {
						if len(results) < maxGrepResults {
							results = append(results, fmt.Sprintf("  %d: %s", j+1, lines[j]))
						}
					}
				}
				if context > 0 && i+1 < ctxEnd {
					for j := i + 1; j < ctxEnd; j++ {
						if len(results) < maxGrepResults {
							results = append(results, fmt.Sprintf("  %d: %s", j+1, lines[j]))
						}
					}
				}

				if len(results) >= maxGrepResults {
					results = append(results, fmt.Sprintf("\n... (truncated, max %d results)", maxGrepResults))
					return filepath.SkipAll
				}
			}
		}
		return nil
	})

	if err != nil {
		return fmt.Sprintf("error walking directory: %v", err), true
	}

	if len(results) == 0 {
		return fmt.Sprintf("No matches found for pattern: %s", pattern), false
	}

	return strings.Join(results, "\n"), false
}

func (a *Agent) handleRead(args map[string]any) (string, bool) {
	path, _ := args["path"].(string)
	if path == "" {
		return "error: path is required", true
	}

	path = a.resolvePath(path)

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("error reading file: %v", err), true
	}

	lines := strings.Split(string(data), "\n")

	offset := a.getArgInt(args, "offset", 1)
	limit := a.getArgInt(args, "limit", 0)

	if offset > len(lines) {
		return "error: offset beyond file length", true
	}

	if offset > 1 {
		lines = lines[offset-1:]
	}

	if limit > 0 && len(lines) > limit {
		lines = lines[:limit]
		content := strings.Join(lines, "\n")
		content += fmt.Sprintf("\n\n... (%d more lines)", len(lines)-limit)
		return content, false
	}

	return strings.Join(lines, "\n"), false
}

func (a *Agent) handleWrite(args map[string]any) (string, bool) {
	path, _ := args["path"].(string)
	content, _ := args["content"].(string)

	if path == "" {
		return "error: path is required", true
	}

	path = a.resolvePath(path)

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Sprintf("error creating directory: %v", err), true
	}

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Sprintf("error writing file: %v", err), true
	}

	return fmt.Sprintf("Written to %s (%d bytes)", path, len(content)), false
}

func (a *Agent) handleGlob(args map[string]any) (string, bool) {
	pattern, _ := args["pattern"].(string)
	if pattern == "" {
		return "error: pattern is required", true
	}

	path := a.getArgString(args, "path", a.workDir)

	if _, err := os.Stat(path); err != nil {
		return fmt.Sprintf("error: path does not exist: %s", path), true
	}

	basePattern := filepath.Join(path, pattern)

	matches, err := filepath.Glob(basePattern)
	if err != nil {
		return fmt.Sprintf("error: invalid pattern: %v", err), true
	}

	if len(matches) == 0 {
		return fmt.Sprintf("No files match pattern: %s", pattern), false
	}

	relMatches := make([]string, 0, len(matches))
	for _, m := range matches {
		rel, err := filepath.Rel(path, m)
		if err != nil {
			continue
		}
		relMatches = append(relMatches, rel)
	}

	return strings.Join(relMatches, "\n"), false
}

func (a *Agent) handleBash(args map[string]any) (string, bool) {
	command, _ := args["command"].(string)
	if command == "" {
		return "error: command is required", true
	}

	timeout := a.getArgInt(args, "timeout", 30)
	if timeout > 120 {
		timeout = 120
	}
	if timeout < 1 {
		timeout = 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = a.workDir
	cmd.Env = os.Environ()

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	output := stdout.String()
	if stderr.Len() > 0 {
		if output != "" {
			output += "\n"
		}
		output += "STDERR:\n" + stderr.String()
	}

	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Sprintf("error: command timed out after %d seconds", timeout), true
	}

	if err != nil {
		if output == "" {
			return fmt.Sprintf("error: %v", err), true
		}
		return fmt.Sprintf("Command exited with error:\n%s", output), true
	}

	if output == "" {
		return "Command completed successfully (no output)", false
	}

	return output, false
}

func (a *Agent) handleLs(args map[string]any) (string, bool) {
	path := a.getArgString(args, "path", a.workDir)
	path = a.resolvePath(path)

	entries, err := os.ReadDir(path)
	if err != nil {
		return fmt.Sprintf("error reading directory: %v", err), true
	}

	if len(entries) == 0 {
		return "Directory is empty", false
	}

	var dirs []string
	var files []string

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			dirs = append(dirs, name+"/")
		} else {
			files = append(files, name)
		}
	}

	var result strings.Builder
	for _, d := range dirs {
		result.WriteString(d + "\n")
	}
	for _, f := range files {
		result.WriteString(f + "\n")
	}

	return result.String(), false
}

func (a *Agent) handleFind(args map[string]any) (string, bool) {
	name, _ := args["name"].(string)
	if name == "" {
		return "error: name is required", true
	}

	path := a.getArgString(args, "path", a.workDir)
	fileType := a.getArgString(args, "type", "")

	if _, err := os.Stat(path); err != nil {
		return fmt.Sprintf("error: path does not exist: %s", path), true
	}

	re, err := regexp.Compile("^" + strings.ReplaceAll(name, "*", ".*") + "$")
	if err != nil {
		return fmt.Sprintf("error: invalid name pattern: %v", err), true
	}

	var results []string
	err = filepath.Walk(path, func(filePath string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		if shouldSkipDir(info.Name()) && filePath != path {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		isDir := info.IsDir()
		if fileType == "f" && isDir {
			return nil
		}
		if fileType == "d" && !isDir {
			return nil
		}

		if re.MatchString(info.Name()) {
			relPath, _ := filepath.Rel(path, filePath)
			if relPath != "." {
				if isDir {
					results = append(results, relPath+"/")
				} else {
					results = append(results, relPath)
				}
			}
		}
		return nil
	})

	if err != nil {
		return fmt.Sprintf("error walking directory: %v", err), true
	}

	if len(results) == 0 {
		return fmt.Sprintf("No files/directories found matching: %s", name), false
	}

	return strings.Join(results, "\n"), false
}

func (a *Agent) getArgString(args map[string]any, key, defaultValue string) string {
	if v, ok := args[key].(string); ok && v != "" {
		return v
	}
	return defaultValue
}

func (a *Agent) getArgInt(args map[string]any, key string, defaultValue int) int {
	if v, ok := args[key]; ok {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		case string:
			if n == "" {
				return defaultValue
			}
			if i, err := strconv.Atoi(n); err == nil {
				return i
			}
		}
	}
	return defaultValue
}

func (a *Agent) resolvePath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(a.workDir, path)
}

func shouldSkipDir(name string) bool {
	switch name {
	case "node_modules", ".git", "__pycache__", ".venv", "venv",
		"dist", "build", "target", ".cache", ".npm",
		".svn", "CVS", ".hg", ".bzr":
		return true
	}
	return strings.HasPrefix(name, ".")
}

func (a *Agent) handleDiff(args map[string]any) (string, bool) {
	path, _ := args["path"].(string)
	newContent, _ := args["new_content"].(string)

	if path == "" {
		return "error: path is required", true
	}
	if newContent == "" {
		return "error: new_content is required", true
	}

	path = a.resolvePath(path)

	// Read current content
	oldContent, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("error reading file: %v", err), true
	}

	oldLines := strings.Split(string(oldContent), "\n")
	newLines := strings.Split(newContent, "\n")

	// Compute diff using simple line-by-line comparison
	diff := computeDiff(oldLines, newLines)

	if diff == "" {
		return "No changes (files are identical)", false
	}

	return diff, false
}

// computeDiff produces a unified diff-like output between old and new lines.
func computeDiff(oldLines, newLines []string) string {
	var result strings.Builder

	oldLen := len(oldLines)
	newLen := len(newLines)

	// Simple diff algorithm: find longest common subsequence
	lcs := longestCommonSubsequence(oldLines, newLines)

	oldIdx := 0
	newIdx := 0
	lcsIdx := 0

	for oldIdx < oldLen || newIdx < newLen {
		if lcsIdx < len(lcs) {
			// Print removed lines from old
			for oldIdx < oldLen && oldLines[oldIdx] != lcs[lcsIdx] {
				result.WriteString(fmt.Sprintf("-%s\n", oldLines[oldIdx]))
				oldIdx++
			}
			// Print added lines from new
			for newIdx < newLen && newLines[newIdx] != lcs[lcsIdx] {
				result.WriteString(fmt.Sprintf("+%s\n", newLines[newIdx]))
				newIdx++
			}
			// Print common line
			if oldIdx < oldLen && newIdx < newLen {
				result.WriteString(fmt.Sprintf(" %s\n", lcs[lcsIdx]))
				oldIdx++
				newIdx++
				lcsIdx++
			}
		} else {
			// No more LCS - print remaining lines
			for oldIdx < oldLen {
				result.WriteString(fmt.Sprintf("-%s\n", oldLines[oldIdx]))
				oldIdx++
			}
			for newIdx < newLen {
				result.WriteString(fmt.Sprintf("+%s\n", newLines[newIdx]))
				newIdx++
			}
		}
	}

	return result.String()
}

// longestCommonSubsequence finds the LCS of two string slices.
func longestCommonSubsequence(a, b []string) []string {
	m, n := len(a), len(b)
	// DP table
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}

	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if a[i-1] == b[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else {
				if dp[i-1][j] > dp[i][j-1] {
					dp[i][j] = dp[i-1][j]
				} else {
					dp[i][j] = dp[i][j-1]
				}
			}
		}
	}

	// Backtrack to find LCS
	var lcs []string
	i, j := m, n
	for i > 0 && j > 0 {
		if a[i-1] == b[j-1] {
			lcs = append([]string{a[i-1]}, lcs...)
			i--
			j--
		} else if dp[i-1][j] > dp[i][j-1] {
			i--
		} else {
			j--
		}
	}

	return lcs
}

func (a *Agent) handleEdit(args map[string]any) (string, bool) {
	path, _ := args["path"].(string)
	patch, _ := args["patch"].(string)

	if path == "" {
		return "error: path is required", true
	}
	if patch == "" {
		return "error: patch is required", true
	}

	path = a.resolvePath(path)

	// Read current content
	oldContent, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("error reading file: %v", err), true
	}

	// Apply the patch
	newContent, err := applyPatch(string(oldContent), patch)
	if err != nil {
		return fmt.Sprintf("error applying patch: %v", err), true
	}

	// Write the updated content
	if err := os.WriteFile(path, []byte(newContent), 0644); err != nil {
		return fmt.Sprintf("error writing file: %v", err), true
	}

	return fmt.Sprintf("Applied patch to %s (%d bytes)", path, len(newContent)), false
}

func (a *Agent) handleMkdir(args map[string]any) (string, bool) {
	path, _ := args["path"].(string)
	if path == "" {
		return "error: path is required", true
	}

	path = a.resolvePath(path)

	if err := os.MkdirAll(path, 0755); err != nil {
		return fmt.Sprintf("error creating directory: %v", err), true
	}

	return fmt.Sprintf("Created directory: %s", path), false
}

func (a *Agent) handleRemove(args map[string]any) (string, bool) {
	path, _ := args["path"].(string)
	if path == "" {
		return "error: path is required", true
	}

	path = a.resolvePath(path)

	recursive := a.getArgBool(args, "recursive", false)
	force := a.getArgBool(args, "force", false)

	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			if force {
				return "Removed (ignored nonexistent)", false
			}
			return fmt.Sprintf("error: path does not exist: %s", path), true
		}
		return fmt.Sprintf("error: %v", err), true
	}

	if info.IsDir() {
		if recursive {
			err = os.RemoveAll(path)
		} else {
			err = os.Remove(path)
		}
	} else {
		err = os.Remove(path)
	}

	if err != nil {
		return fmt.Sprintf("error removing: %v", err), true
	}
	return fmt.Sprintf("Removed: %s", path), false
}

func (a *Agent) handleCopy(args map[string]any) (string, bool) {
	source, _ := args["source"].(string)
	destination, _ := args["destination"].(string)

	if source == "" || destination == "" {
		return "error: source and destination are required", true
	}

	source = a.resolvePath(source)
	destination = a.resolvePath(destination)

	info, err := os.Stat(source)
	if err != nil {
		return fmt.Sprintf("error: %v", err), true
	}

	if info.IsDir() {
		return "error: copying directories not supported (use bash with cp -r)", true
	}

	srcData, err := os.ReadFile(source)
	if err != nil {
		return fmt.Sprintf("error reading source: %v", err), true
	}

	// Create parent directory if needed
	dir := filepath.Dir(destination)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Sprintf("error creating destination directory: %v", err), true
	}

	err = os.WriteFile(destination, srcData, info.Mode())
	if err != nil {
		return fmt.Sprintf("error writing destination: %v", err), true
	}

	return fmt.Sprintf("Copied: %s -> %s", source, destination), false
}

func (a *Agent) handleMove(args map[string]any) (string, bool) {
	source, _ := args["source"].(string)
	destination, _ := args["destination"].(string)

	if source == "" || destination == "" {
		return "error: source and destination are required", true
	}

	source = a.resolvePath(source)
	destination = a.resolvePath(destination)

	// Create parent directory if needed
	dir := filepath.Dir(destination)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Sprintf("error creating destination directory: %v", err), true
	}

	err := os.Rename(source, destination)
	if err != nil {
		return fmt.Sprintf("error moving: %v", err), true
	}

	return fmt.Sprintf("Moved: %s -> %s", source, destination), false
}

func (a *Agent) handleExists(args map[string]any) (string, bool) {
	path, _ := args["path"].(string)
	if path == "" {
		return "error: path is required", true
	}

	path = a.resolvePath(path)

	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return fmt.Sprintf("false: %s does not exist", path), false
	}
	if err != nil {
		return fmt.Sprintf("error: %v", err), true
	}

	if info.IsDir() {
		return fmt.Sprintf("true: %s (directory)", path), false
	}
	return fmt.Sprintf("true: %s (file, %d bytes)", path, info.Size()), false
}

func (a *Agent) getArgBool(args map[string]any, key string, defaultValue bool) bool {
	if v, ok := args[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return defaultValue
}

// applyPatch applies a unified-diff style patch to the content.
func applyPatch(content, patch string) (string, error) {
	lines := strings.Split(content, "\n")
	patchLines := strings.Split(patch, "\n")

	var result []string
	i := 0

	for i < len(patchLines) {
		line := patchLines[i]

		// Look for hunk header: @@ -start,count +new_start,new_count @@
		if strings.HasPrefix(line, "@@") {
			// Parse hunk header
			parts := strings.Fields(line)
			if len(parts) < 4 {
				return "", fmt.Errorf("invalid hunk header: %s", line)
			}
			// Parse old start,count from "-start,count"
			oldSpec := strings.TrimPrefix(parts[1], "-")
			oldParts := strings.Split(oldSpec, ",")
			oldStart, _ := strconv.Atoi(oldParts[0])

			// Parse new start,count from "+new_start,new_count"
			newSpec := strings.TrimPrefix(parts[2], "+")
			newParts := strings.Split(newSpec, ",")
			newStart, _ := strconv.Atoi(newParts[0])

			// Convert to 0-based indices
			oldIdx := oldStart - 1
			newIdx := newStart - 1

			i++

			// Process hunk content
			for i < len(patchLines) && !strings.HasPrefix(patchLines[i], "@@") {
				patchLine := patchLines[i]

				if strings.HasPrefix(patchLine, "-") {
					// Remove line
					if oldIdx < len(lines) {
						_ = lines[oldIdx] // consume but don't add
						oldIdx++
					}
				} else if strings.HasPrefix(patchLine, "+") {
					// Add line
					content := strings.TrimPrefix(patchLine, "+")
					result = append(result, content)
					newIdx++
				} else if strings.HasPrefix(patchLine, " ") || patchLine == "" {
					// Context line - keep from original
					if oldIdx < len(lines) {
						result = append(result, lines[oldIdx])
						oldIdx++
					}
				} else {
					// Unknown line type, treat as context
					result = append(result, patchLine)
				}
				i++
			}
			continue
		}

		// Regular line (not in a hunk) - skip
		i++
	}

	// If no patches applied, return original
	if len(result) == 0 {
		return content, nil
	}

	return strings.Join(result, "\n"), nil
}
