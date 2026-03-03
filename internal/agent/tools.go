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
