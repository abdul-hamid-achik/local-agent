package agent

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/abdul-hamid-achik/local-agent/internal/llm"
	"github.com/abdul-hamid-achik/local-agent/internal/tools"
)

const (
	maxTimeout          = 120 * time.Second
	maxToolCaptureBytes = 1024 * 1024
	maxFileReadBytes    = 8 * 1024 * 1024
	maxCopyBytes        = 64 * 1024 * 1024
)

type cappedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	written := len(p)
	remaining := b.limit - b.buf.Len()
	if remaining <= 0 {
		b.truncated = true
		return written, nil
	}
	if len(p) > remaining {
		p = p[:remaining]
		b.truncated = true
	}
	_, err := b.buf.Write(p)
	return written, err
}

func (b *cappedBuffer) Len() int { return b.buf.Len() }

func (b *cappedBuffer) String() string {
	value := b.buf.String()
	if b.truncated {
		value += "\n... (subprocess output truncated by host)"
	}
	return value
}

func (a *Agent) toolsBuiltinToolDefs() []llm.ToolDef {
	defs := tools.AllToolDefs()
	if a.hasSkillLoader() && a.hasExpertConsultant() {
		return defs
	}
	filtered := make([]llm.ToolDef, 0, len(defs))
	for _, def := range defs {
		if def.Name == "load_skill" && !a.hasSkillLoader() {
			continue
		}
		if def.Name == "consult_experts" && !a.hasExpertConsultant() {
			continue
		}
		filtered = append(filtered, def)
	}
	return filtered
}

func (a *Agent) isToolsTool(name string) bool {
	return tools.IsBuiltinTool(name)
}

func (a *Agent) handleToolsTool(ctx context.Context, tc llm.ToolCall) (string, bool) {
	switch tc.Name {
	case "grep":
		return a.handleGrep(ctx, tc.Arguments)
	case "read":
		return a.handleRead(tc.Arguments)
	case "write":
		return a.handleWrite(tc.Arguments)
	case "glob":
		return a.handleGlob(ctx, tc.Arguments)
	case "bash":
		return a.handleBash(ctx, tc.Arguments)
	case "ls":
		return a.handleLs(ctx, tc.Arguments)
	case "find":
		return a.handleFind(ctx, tc.Arguments)
	case "diff":
		return a.handleDiff(ctx, tc.Arguments)
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
	case "load_skill":
		return a.handleLoadSkill(tc.Arguments)
	case "consult_experts":
		return a.handleConsultExperts(ctx, tc.Arguments)
	default:
		return fmt.Sprintf("unknown tool: %s", tc.Name), true
	}
}

func (a *Agent) handleGrep(ctx context.Context, args map[string]any) (string, bool) {
	if err := ctx.Err(); err != nil {
		return fmt.Sprintf("error: grep cancelled: %v", err), true
	}
	pattern, _ := args["pattern"].(string)
	if pattern == "" {
		return "error: pattern is required", true
	}

	requestedPath := a.getArgString(args, "path", a.activeWorkDir())
	readable, err := a.resolveReadablePath(requestedPath)
	if err != nil {
		return fmt.Sprintf("error: %v", err), true
	}
	defer func() { _ = readable.close() }()
	path := readable.absolute
	include := a.getArgString(args, "include", "")
	context := a.getArgInt(args, "context", 3)
	if context < 0 {
		context = 0
	}
	if context > 20 {
		context = 20
	}
	maxResults := a.MaxGrepResults()

	if _, err := readable.stat(); err != nil {
		return fmt.Sprintf("error: path does not exist: %s", path), true
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		return fmt.Sprintf("error: invalid regex pattern: %v", err), true
	}

	var results []string
	err = readable.walk(func(filePath string, info os.FileInfo, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		if filePath != path && readable.ignored(a, filePath) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if info.IsDir() {
			if filePath != path && shouldSkipDir(info.Name()) {
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

		content, err := readable.readBoundedAt(filePath, maxFileReadBytes)
		if err != nil {
			return nil
		}

		lines := strings.Split(string(content), "\n")
		for i, line := range lines {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
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
						if len(results) < maxResults {
							results = append(results, fmt.Sprintf("  %d: %s", j+1, lines[j]))
						}
					}
				}
				if context > 0 && i+1 < ctxEnd {
					for j := i + 1; j < ctxEnd; j++ {
						if len(results) < maxResults {
							results = append(results, fmt.Sprintf("  %d: %s", j+1, lines[j]))
						}
					}
				}

				if len(results) >= maxResults {
					results = append(results, fmt.Sprintf("\n... (truncated, max %d results)", maxResults))
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

	readable, err := a.resolveReadablePath(path)
	if err != nil {
		return fmt.Sprintf("error: %v", err), true
	}
	defer func() { _ = readable.close() }()

	data, err := readable.readBounded(maxFileReadBytes)
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
		remaining := len(lines) - limit
		lines = lines[:limit]
		content := strings.Join(lines, "\n")
		content += fmt.Sprintf("\n\n... (%d more lines)", remaining)
		return content, false
	}

	return strings.Join(lines, "\n"), false
}

func (a *Agent) handleWrite(args map[string]any) (string, bool) {
	requestedPath, _ := args["path"].(string)
	content, _ := args["content"].(string)

	if requestedPath == "" {
		return "error: path is required", true
	}
	workspace, err := a.openWorkspaceRoot()
	if err != nil {
		return fmt.Sprintf("error: %v", err), true
	}
	defer func() { _ = workspace.Close() }()
	path, relative, err := workspace.resolve(a, requestedPath, false)
	if err != nil {
		return fmt.Sprintf("error: %v", err), true
	}
	parent, name, err := workspace.openParent(relative, true)
	if err != nil {
		return fmt.Sprintf("error creating directory: %v", err), true
	}
	defer func() { _ = parent.Close() }()

	mode := os.FileMode(0o644)
	if info, statErr := parent.Stat(name); statErr == nil {
		mode = info.Mode().Perm()
	}
	if err := atomicWriteRoot(parent, name, []byte(content), mode); err != nil {
		return fmt.Sprintf("error writing file: %v", err), true
	}

	return fmt.Sprintf("Written to %s (%d bytes)", path, len(content)), false
}

func (a *Agent) handleGlob(ctx context.Context, args map[string]any) (string, bool) {
	if err := ctx.Err(); err != nil {
		return fmt.Sprintf("error: glob cancelled: %v", err), true
	}
	pattern, _ := args["pattern"].(string)
	if pattern == "" {
		return "error: pattern is required", true
	}

	requestedPath := a.getArgString(args, "path", a.activeWorkDir())
	readable, err := a.resolveReadablePath(requestedPath)
	if err != nil {
		return fmt.Sprintf("error: %v", err), true
	}
	defer func() { _ = readable.close() }()
	path := readable.absolute

	if _, err := readable.stat(); err != nil {
		return fmt.Sprintf("error: path does not exist: %s", path), true
	}

	basePattern := filepath.Join(path, pattern)
	_ = basePattern

	matcher, err := regexp.Compile(globPatternToRegex(pattern))
	if err != nil {
		return fmt.Sprintf("error: invalid pattern: %v", err), true
	}

	var matches []string
	err = readable.walk(func(filePath string, info os.FileInfo, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			return nil
		}
		if filePath != path && readable.ignored(a, filePath) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info.IsDir() && filePath != path && shouldSkipDir(info.Name()) {
			return filepath.SkipDir
		}

		rel, err := filepath.Rel(path, filePath)
		if err != nil || rel == "." {
			return nil
		}

		rel = filepath.ToSlash(rel)
		if matcher.MatchString(rel) {
			matches = append(matches, rel)
			if len(matches) >= a.MaxGrepResults() {
				return filepath.SkipAll
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Sprintf("error walking directory: %v", err), true
	}

	if len(matches) == 0 {
		return fmt.Sprintf("No files match pattern: %s", pattern), false
	}

	sort.Strings(matches)
	return strings.Join(matches, "\n"), false
}

func (a *Agent) handleBash(parent context.Context, args map[string]any) (string, bool) {
	command, _ := args["command"].(string)
	if command == "" {
		return "error: command is required", true
	}

	timeout := a.getArgInt(args, "timeout", int(a.ToolTimeout().Seconds()))
	maxTimeoutSecs := int(a.ToolTimeout().Seconds())
	if maxTimeoutSecs > 120 {
		maxTimeoutSecs = 120
	}
	if timeout > maxTimeoutSecs {
		timeout = maxTimeoutSecs
	}
	if timeout < 1 {
		timeout = 1
	}

	ctx, cancel := context.WithTimeout(parent, time.Duration(timeout)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	configureCommandProcessGroup(cmd)
	cmd.WaitDelay = 2 * time.Second
	cmd.Dir = a.activeWorkDir()
	// Do not leak the parent process environment (which may hold API keys,
	// tokens, DB passwords) to LLM-generated shell commands. Pass only a
	// curated allowlist of variables a shell legitimately needs.
	cmd.Env = sanitizedEnv()
	cmd.Stdin = nil

	stdout := cappedBuffer{limit: maxToolCaptureBytes}
	stderr := cappedBuffer{limit: maxToolCaptureBytes}
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
		return fmt.Sprintf(outcomeUnknownReceiptPrefix+" command timed out after %d seconds; its process group was terminated, but effects completed before termination may have occurred", timeout), true
	}
	if ctx.Err() == context.Canceled {
		return outcomeUnknownReceiptPrefix + " command was cancelled after dispatch; its process group was terminated, but effects completed before termination may have occurred", true
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

func (a *Agent) handleLs(ctx context.Context, args map[string]any) (string, bool) {
	if err := ctx.Err(); err != nil {
		return fmt.Sprintf("error: ls cancelled: %v", err), true
	}
	requestedPath := a.getArgString(args, "path", a.activeWorkDir())
	readable, err := a.resolveReadablePath(requestedPath)
	if err != nil {
		return fmt.Sprintf("error: %v", err), true
	}
	defer func() { _ = readable.close() }()
	path := readable.absolute

	entries, hadEntries, err := readable.readDirBounded(ctx, a.MaxGrepResults(), func(entry os.DirEntry) bool {
		return !readable.ignored(a, filepath.Join(path, entry.Name()))
	})
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return fmt.Sprintf("error: ls cancelled: %v", contextErr), true
		}
		return fmt.Sprintf("error reading directory: %v", err), true
	}
	if err := ctx.Err(); err != nil {
		return fmt.Sprintf("error: ls cancelled: %v", err), true
	}

	if !hadEntries {
		return "Directory is empty", false
	}

	var dirs []string
	var files []string

	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return fmt.Sprintf("error: ls cancelled: %v", err), true
		}
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

func (a *Agent) handleFind(ctx context.Context, args map[string]any) (string, bool) {
	if err := ctx.Err(); err != nil {
		return fmt.Sprintf("error: find cancelled: %v", err), true
	}
	name, _ := args["name"].(string)
	if name == "" {
		return "error: name is required", true
	}

	requestedPath := a.getArgString(args, "path", a.activeWorkDir())
	readable, err := a.resolveReadablePath(requestedPath)
	if err != nil {
		return fmt.Sprintf("error: %v", err), true
	}
	defer func() { _ = readable.close() }()
	path := readable.absolute
	fileType := a.getArgString(args, "type", "")

	if _, err := readable.stat(); err != nil {
		return fmt.Sprintf("error: path does not exist: %s", path), true
	}

	matchesName := func(base string) (bool, error) {
		return filepath.Match(name, base)
	}
	if _, err := matchesName("probe"); err != nil {
		return fmt.Sprintf("error: invalid name pattern: %v", err), true
	}

	var results []string
	err = readable.walk(func(filePath string, info os.FileInfo, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			return nil
		}
		if filePath != path && readable.ignored(a, filePath) {
			if info.IsDir() {
				return filepath.SkipDir
			}
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

		matched, matchErr := matchesName(info.Name())
		if matchErr != nil {
			return matchErr
		}
		if matched {
			relPath, _ := filepath.Rel(path, filePath)
			if relPath != "." {
				if isDir {
					results = append(results, relPath+"/")
				} else {
					results = append(results, relPath)
				}
			}
			if len(results) >= a.MaxGrepResults() {
				return filepath.SkipAll
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

func (a *Agent) resolvePath(path string) (string, error) {
	filesystem := a.filesystemContext()
	lexicalRoot := filesystem.workDir
	if lexicalRoot == "" {
		var err error
		lexicalRoot, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve workspace: %w", err)
		}
	}

	lexicalRoot, err := filepath.Abs(lexicalRoot)
	if err != nil {
		return "", fmt.Errorf("resolve workspace: %w", err)
	}
	root := lexicalRoot
	if resolved, resolveErr := filepath.EvalSymlinks(root); resolveErr == nil {
		root = resolved
	}

	candidate := path
	requestedAbsolute := filepath.IsAbs(candidate)
	if !requestedAbsolute {
		candidate = filepath.Join(lexicalRoot, candidate)
	}
	candidate, err = filepath.Abs(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve path %q: %w", path, err)
	}
	lexicalRel, lexicalInside, err := workspaceRelative(lexicalRoot, candidate)
	if err != nil {
		return "", fmt.Errorf("resolve lexical path %q: %w", path, err)
	}
	if !lexicalInside && !requestedAbsolute {
		return "", fmt.Errorf("path %q escapes workspace %q", path, root)
	}
	if lexicalInside && pathIgnoredWithContent(filesystem.ignoreContent, lexicalRel) {
		return "", fmt.Errorf("path %q is excluded by .agentignore", path)
	}
	candidate, err = resolveExistingAncestor(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve path %q: %w", path, err)
	}

	rel, inside, err := physicalCanonicalRelative(root, candidate)
	if err != nil {
		return "", fmt.Errorf("resolve path %q: %w", path, err)
	}
	if !inside {
		return "", fmt.Errorf("path %q escapes workspace %q", path, root)
	}
	if pathIgnoredWithContent(filesystem.ignoreContent, rel) {
		return "", fmt.Errorf("path %q is excluded by .agentignore", path)
	}
	return filepath.Join(root, rel), nil
}

// resolveDestructivePath confines a remove/rename operand by canonicalizing
// its parent while deliberately preserving the final path component. This
// makes approval match the visible object: removing or moving `link` acts on
// the symlink itself, not on the file or directory it points to.
func (a *Agent) resolveDestructivePath(path string) (string, error) {
	filesystem := a.filesystemContext()
	lexicalRoot := filesystem.workDir
	if lexicalRoot == "" {
		var err error
		lexicalRoot, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve workspace: %w", err)
		}
	}
	lexicalRoot, err := filepath.Abs(lexicalRoot)
	if err != nil {
		return "", fmt.Errorf("resolve workspace: %w", err)
	}
	root := lexicalRoot
	if resolved, resolveErr := filepath.EvalSymlinks(root); resolveErr == nil {
		root = resolved
	}

	candidate := path
	requestedAbsolute := filepath.IsAbs(candidate)
	if !requestedAbsolute {
		candidate = filepath.Join(lexicalRoot, candidate)
	}
	candidate, err = filepath.Abs(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve path %q: %w", path, err)
	}
	lexicalRel, lexicalInside, err := workspaceRelative(lexicalRoot, candidate)
	if err != nil {
		return "", fmt.Errorf("resolve lexical path %q: %w", path, err)
	}
	if !lexicalInside && !requestedAbsolute {
		return "", fmt.Errorf("path %q escapes workspace %q", path, root)
	}
	if lexicalInside && pathIgnoredWithContent(filesystem.ignoreContent, lexicalRel) {
		return "", fmt.Errorf("path %q is excluded by .agentignore", path)
	}
	if filepath.Clean(candidate) == filepath.Clean(lexicalRoot) {
		return filepath.Clean(root), nil
	}
	if rootInfo, rootErr := os.Stat(root); rootErr == nil {
		if candidateInfo, candidateErr := os.Lstat(candidate); candidateErr == nil && os.SameFile(rootInfo, candidateInfo) {
			return filepath.Clean(root), nil
		}
	}
	parent, err := resolveExistingAncestor(filepath.Dir(candidate))
	if err != nil {
		return "", fmt.Errorf("resolve parent for %q: %w", path, err)
	}
	parentRel, inside, err := physicalCanonicalRelative(root, parent)
	if err != nil {
		return "", fmt.Errorf("resolve path %q: %w", path, err)
	}
	if !inside {
		return "", fmt.Errorf("path %q escapes workspace %q", path, root)
	}
	parent = filepath.Join(root, parentRel)
	name := filepath.Base(candidate)
	candidate = filepath.Join(parent, name)
	if info, statErr := os.Lstat(candidate); statErr == nil {
		actualName, nameErr := physicalEntryName(parent, name, info)
		if nameErr != nil {
			return "", fmt.Errorf("resolve destructive path entry %q: %w", path, nameErr)
		}
		name = actualName
		candidate = filepath.Join(parent, name)
	} else if !os.IsNotExist(statErr) {
		return "", fmt.Errorf("inspect destructive path %q: %w", path, statErr)
	}
	rel := filepath.Join(parentRel, name)
	if pathIgnoredWithContent(filesystem.ignoreContent, rel) {
		return "", fmt.Errorf("path %q is excluded by .agentignore", path)
	}
	return filepath.Clean(candidate), nil
}

func workspaceRelative(root, candidate string) (string, bool, error) {
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return "", false, err
	}
	inside := rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
	return rel, inside, nil
}

// resolveExistingAncestor canonicalizes symlinks even for a path that does
// not exist yet by resolving its closest existing ancestor first.
func resolveExistingAncestor(path string) (string, error) {
	current := filepath.Clean(path)
	var missing []string
	for {
		_, err := os.Lstat(current)
		if err == nil {
			resolved, err := filepath.EvalSymlinks(current)
			if err != nil {
				return "", err
			}
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return filepath.Clean(resolved), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", err
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

func (a *Agent) pathIgnored(path string) bool {
	return pathIgnoredWithContent(a.filesystemContext().ignoreContent, path)
}

func ignorePatternMatches(pattern, cleanPath string) bool {
	if cleanPath == pattern || strings.HasPrefix(cleanPath, pattern+"/") {
		return true
	}
	if re, err := regexp.Compile(globPatternToRegex(pattern)); err == nil && re.MatchString(cleanPath) {
		return true
	}
	if strings.Contains(pattern, "/") {
		return false
	}
	for _, part := range strings.Split(cleanPath, "/") {
		if matched, _ := filepath.Match(pattern, part); matched {
			return true
		}
	}
	return false
}

// envAllowlist names the environment variables a shell command legitimately
// needs. Everything else (secrets, tokens, provider keys) is withheld from
// LLM-generated commands.
var envAllowlist = []string{
	"PATH", "HOME", "USER", "LOGNAME", "SHELL", "LANG", "LC_ALL", "LC_CTYPE",
	"TERM", "TMPDIR", "PWD", "TZ",
	// Common toolchain roots that are paths, not secrets.
	"GOPATH", "GOROOT", "GOCACHE", "GOMODCACHE",
	"NVM_DIR", "PYENV_ROOT", "RBENV_ROOT", "CARGO_HOME", "RUSTUP_HOME",
	"XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME",
}

// sanitizedEnv returns a minimal environment for subprocesses, copying only
// the allowlisted variables that are actually set in the parent.
func sanitizedEnv() []string {
	env := make([]string, 0, len(envAllowlist))
	for _, k := range envAllowlist {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return env
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

func globPatternToRegex(pattern string) string {
	pattern = filepath.ToSlash(pattern)

	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				i++
				if i+1 < len(pattern) && pattern[i+1] == '/' {
					i++
					b.WriteString("(?:.*/)?")
				} else {
					b.WriteString(".*")
				}
			} else {
				b.WriteString(`[^/]*`)
			}
		case '?':
			b.WriteString(`[^/]`)
		case '.', '+', '(', ')', '|', '^', '$', '{', '}', '[', ']', '\\':
			b.WriteByte('\\')
			b.WriteByte(pattern[i])
		default:
			b.WriteByte(pattern[i])
		}
	}
	b.WriteString("$")
	return b.String()
}

func (a *Agent) handleDiff(ctx context.Context, args map[string]any) (string, bool) {
	if err := ctx.Err(); err != nil {
		return fmt.Sprintf("error: diff cancelled: %v", err), true
	}
	path, _ := args["path"].(string)
	newContent, _ := args["new_content"].(string)

	if path == "" {
		return "error: path is required", true
	}
	if newContent == "" {
		return "error: new_content is required", true
	}

	readable, err := a.resolveReadablePath(path)
	if err != nil {
		return fmt.Sprintf("error: %v", err), true
	}
	defer func() { _ = readable.close() }()

	// Read current content
	oldContent, err := readable.readBounded(maxFileReadBytes)
	if err != nil {
		return fmt.Sprintf("error reading file: %v", err), true
	}

	oldLines := strings.Split(string(oldContent), "\n")
	newLines := strings.Split(newContent, "\n")
	const maxDiffCells = 4_000_000
	if len(newLines) > 0 && len(oldLines) > maxDiffCells/len(newLines) {
		return fmt.Sprintf("error: diff is too large (%d x %d lines); compare a narrower region or use an approved git diff command", len(oldLines), len(newLines)), true
	}

	// Compute diff using simple line-by-line comparison
	diff, err := computeDiff(ctx, oldLines, newLines)
	if err != nil {
		return fmt.Sprintf("error: diff cancelled: %v", err), true
	}

	if diff == "" {
		return "No changes (files are identical)", false
	}

	return diff, false
}

// computeDiff produces a unified diff-like output between old and new lines.
func computeDiff(ctx context.Context, oldLines, newLines []string) (string, error) {
	var result strings.Builder

	oldLen := len(oldLines)
	newLen := len(newLines)

	// Simple diff algorithm: find longest common subsequence
	lcs, err := longestCommonSubsequence(ctx, oldLines, newLines)
	if err != nil {
		return "", err
	}

	oldIdx := 0
	newIdx := 0
	lcsIdx := 0

	for oldIdx < oldLen || newIdx < newLen {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if lcsIdx < len(lcs) {
			// Print removed lines from old
			for oldIdx < oldLen && oldLines[oldIdx] != lcs[lcsIdx] {
				fmt.Fprintf(&result, "-%s\n", oldLines[oldIdx])
				oldIdx++
			}
			// Print added lines from new
			for newIdx < newLen && newLines[newIdx] != lcs[lcsIdx] {
				fmt.Fprintf(&result, "+%s\n", newLines[newIdx])
				newIdx++
			}
			// Print common line
			if oldIdx < oldLen && newIdx < newLen {
				fmt.Fprintf(&result, " %s\n", lcs[lcsIdx])
				oldIdx++
				newIdx++
				lcsIdx++
			}
		} else {
			// No more LCS - print remaining lines
			for oldIdx < oldLen {
				fmt.Fprintf(&result, "-%s\n", oldLines[oldIdx])
				oldIdx++
			}
			for newIdx < newLen {
				fmt.Fprintf(&result, "+%s\n", newLines[newIdx])
				newIdx++
			}
		}
	}

	return result.String(), nil
}

// longestCommonSubsequence finds the LCS of two string slices.
func longestCommonSubsequence(ctx context.Context, a, b []string) ([]string, error) {
	m, n := len(a), len(b)
	// DP table
	dp := make([][]int, m+1)
	for i := range dp {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		dp[i] = make([]int, n+1)
	}

	for i := 1; i <= m; i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
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
		if err := ctx.Err(); err != nil {
			return nil, err
		}
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

	return lcs, nil
}

func (a *Agent) handleEdit(args map[string]any) (string, bool) {
	requestedPath, _ := args["path"].(string)
	patch, _ := args["patch"].(string)

	if requestedPath == "" {
		return "error: path is required", true
	}
	if patch == "" {
		return "error: patch is required", true
	}

	workspace, err := a.openWorkspaceRoot()
	if err != nil {
		return fmt.Sprintf("error: %v", err), true
	}
	defer func() { _ = workspace.Close() }()
	path, relative, err := workspace.resolve(a, requestedPath, false)
	if err != nil {
		return fmt.Sprintf("error: %v", err), true
	}
	parent, name, err := workspace.openParent(relative, false)
	if err != nil {
		return fmt.Sprintf("error reading file: %v", err), true
	}
	defer func() { _ = parent.Close() }()

	// Read current content
	oldContent, info, err := readPinnedRootFile(parent, name, maxFileReadBytes)
	if err != nil {
		return fmt.Sprintf("error reading file: %v", err), true
	}

	// Apply the patch
	newContent, err := applyPatch(string(oldContent), patch)
	if err != nil {
		return fmt.Sprintf("error applying patch: %v", err), true
	}

	if err := atomicWriteRoot(parent, name, []byte(newContent), info.Mode().Perm()); err != nil {
		return fmt.Sprintf("error writing file: %v", err), true
	}

	return fmt.Sprintf("Applied patch to %s (%d bytes)", path, len(newContent)), false
}

func (a *Agent) handleMkdir(args map[string]any) (string, bool) {
	requestedPath, _ := args["path"].(string)
	if requestedPath == "" {
		return "error: path is required", true
	}
	workspace, err := a.openWorkspaceRoot()
	if err != nil {
		return fmt.Sprintf("error: %v", err), true
	}
	defer func() { _ = workspace.Close() }()
	path, relative, err := workspace.resolve(a, requestedPath, false)
	if err != nil {
		return fmt.Sprintf("error: %v", err), true
	}
	if err := workspace.mkdirAll(relative); err != nil {
		return fmt.Sprintf("error creating directory: %v", err), true
	}

	return fmt.Sprintf("Created directory: %s", path), false
}

func (a *Agent) handleRemove(args map[string]any) (string, bool) {
	requestedPath, _ := args["path"].(string)
	if requestedPath == "" {
		return "error: path is required", true
	}
	workspace, err := a.openWorkspaceRoot()
	if err != nil {
		return fmt.Sprintf("error: %v", err), true
	}
	defer func() { _ = workspace.Close() }()
	path, relative, err := workspace.resolve(a, requestedPath, true)
	if err != nil {
		return fmt.Sprintf("error: %v", err), true
	}
	if relative == "." {
		return "error: refusing to remove the workspace root", true
	}
	parent, name, err := workspace.openParent(relative, false)
	if err != nil {
		if os.IsNotExist(err) && a.getArgBool(args, "force", false) {
			return "Removed (ignored nonexistent)", false
		}
		return fmt.Sprintf("error: %v", err), true
	}
	defer func() { _ = parent.Close() }()

	recursive := a.getArgBool(args, "recursive", false)
	force := a.getArgBool(args, "force", false)

	info, err := parent.Lstat(name)
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
			err = parent.RemoveAll(name)
		} else {
			err = parent.Remove(name)
		}
	} else {
		err = parent.Remove(name)
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
	workspace, err := a.openWorkspaceRoot()
	if err != nil {
		return fmt.Sprintf("error: destination: %v", err), true
	}
	defer func() { _ = workspace.Close() }()

	readableSource, err := a.resolveReadablePath(source)
	if err != nil {
		return fmt.Sprintf("error: source: %v", err), true
	}
	defer func() { _ = readableSource.close() }()
	source = readableSource.absolute
	destination, destinationRelative, err := workspace.resolve(a, destination, false)
	if err != nil {
		return fmt.Sprintf("error: destination: %v", err), true
	}

	info, err := readableSource.stat()
	if err != nil {
		return fmt.Sprintf("error: %v", err), true
	}

	if info.IsDir() {
		return "error: copying directories not supported (use bash with cp -r)", true
	}

	srcData, err := readableSource.readBounded(maxCopyBytes)
	if err != nil {
		return fmt.Sprintf("error reading source: %v", err), true
	}

	parent, name, err := workspace.openParent(destinationRelative, true)
	if err != nil {
		return fmt.Sprintf("error creating destination directory: %v", err), true
	}
	defer func() { _ = parent.Close() }()

	err = atomicWriteRoot(parent, name, srcData, info.Mode().Perm())
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
	workspace, err := a.openWorkspaceRoot()
	if err != nil {
		return fmt.Sprintf("error: %v", err), true
	}
	defer func() { _ = workspace.Close() }()
	source, sourceRelative, err := workspace.resolve(a, source, true)
	if err != nil {
		return fmt.Sprintf("error: source: %v", err), true
	}
	if sourceRelative == "." {
		return "error: refusing to move the workspace root", true
	}
	destination, destinationRelative, err := workspace.resolve(a, destination, true)
	if err != nil {
		return fmt.Sprintf("error: destination: %v", err), true
	}
	sourceParent, _, err := workspace.openParent(sourceRelative, false)
	if err != nil {
		return fmt.Sprintf("error: source: %v", err), true
	}
	defer func() { _ = sourceParent.Close() }()
	destinationParent, _, err := workspace.openParent(destinationRelative, true)
	if err != nil {
		return fmt.Sprintf("error creating destination directory: %v", err), true
	}
	defer func() { _ = destinationParent.Close() }()

	err = workspace.root.Rename(sourceRelative, destinationRelative)
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

	readable, err := a.resolveReadablePath(path)
	if err != nil {
		return fmt.Sprintf("error: %v", err), true
	}
	defer func() { _ = readable.close() }()
	path = readable.absolute

	info, err := readable.stat()
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

var hunkHeaderPattern = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// applyPatch applies validated unified-diff hunks while preserving every
// untouched prefix and suffix. Context and removed lines must match exactly;
// a stale model-generated patch therefore fails instead of corrupting a file.
func applyPatch(content, patch string) (string, error) {
	source := strings.Split(content, "\n")
	patchLines := strings.Split(patch, "\n")
	result := make([]string, 0, len(source))
	sourcePos := 0
	applied := false

	for i := 0; i < len(patchLines); {
		match := hunkHeaderPattern.FindStringSubmatch(patchLines[i])
		if match == nil {
			i++
			continue
		}
		applied = true
		oldStart, _ := strconv.Atoi(match[1])
		oldCount := 1
		if match[2] != "" {
			oldCount, _ = strconv.Atoi(match[2])
		}
		newCount := 1
		if match[4] != "" {
			newCount, _ = strconv.Atoi(match[4])
		}

		hunkStart := oldStart
		if hunkStart > 0 {
			hunkStart--
		}
		if hunkStart < sourcePos || hunkStart > len(source) {
			return "", fmt.Errorf("invalid or overlapping hunk at old line %d", oldStart)
		}
		result = append(result, source[sourcePos:hunkStart]...)
		sourcePos = hunkStart
		i++

		oldSeen, newSeen := 0, 0
		for i < len(patchLines) && hunkHeaderPattern.FindStringSubmatch(patchLines[i]) == nil {
			line := patchLines[i]
			if strings.HasPrefix(line, "\\ No newline at end of file") {
				i++
				continue
			}
			if line == "" && i == len(patchLines)-1 {
				break
			}
			if line == "" {
				return "", fmt.Errorf("invalid empty patch line in hunk")
			}

			body := line[1:]
			switch line[0] {
			case ' ':
				if sourcePos >= len(source) || source[sourcePos] != body {
					return "", fmt.Errorf("patch context mismatch at old line %d", sourcePos+1)
				}
				result = append(result, body)
				sourcePos++
				oldSeen++
				newSeen++
			case '-':
				if sourcePos >= len(source) || source[sourcePos] != body {
					return "", fmt.Errorf("patch removal mismatch at old line %d", sourcePos+1)
				}
				sourcePos++
				oldSeen++
			case '+':
				result = append(result, body)
				newSeen++
			default:
				return "", fmt.Errorf("invalid patch line %q", line)
			}
			i++
		}
		if oldSeen != oldCount || newSeen != newCount {
			return "", fmt.Errorf("hunk count mismatch: saw -%d/+%d, header declares -%d/+%d", oldSeen, newSeen, oldCount, newCount)
		}
	}

	if !applied {
		return "", fmt.Errorf("patch contains no hunks")
	}
	result = append(result, source[sourcePos:]...)
	return strings.Join(result, "\n"), nil
}
