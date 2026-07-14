package agent

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/abdul-hamid-achik/local-agent/internal/config"
)

const maxAdditionalReadRoots = 16

// additionalReadRoot is an explicit, process-local read grant. os.Root pins the
// selected directory and prevents relative operations (including symlinks) from
// escaping it. Additional roots never participate in mutation authorization.
type additionalReadRoot struct {
	path          string
	root          *os.Root
	ignoreContent string
}

// readablePath carries the authority needed to open one path. A nil root is the
// ordinary workspace path, which keeps the existing resolver and behavior.
type readablePath struct {
	absolute string
	relative string
	root     *additionalReadRoot
}

// AddReadRoot grants read-only access to one external directory for this Agent
// process. It deliberately rejects overlap with the writable workspace and with
// another grant so ignore and authority boundaries remain unambiguous.
func (a *Agent) AddReadRoot(path string) (string, error) {
	canonical, err := canonicalReadRootPath(path)
	if err != nil {
		return "", err
	}
	if canonical == string(filepath.Separator) {
		return "", errors.New("refusing to grant the filesystem root as read-only scope")
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", fmt.Errorf("inspect read-only root: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("read-only root is not a directory: %s", canonical)
	}

	workspace, err := a.canonicalWorkDir()
	if err != nil {
		return "", err
	}
	if pathsOverlap(workspace, canonical) {
		return "", fmt.Errorf("read-only root %q overlaps writable workspace %q", canonical, workspace)
	}

	ignore, err := config.LoadIgnoreFileWithError(canonical)
	if err != nil {
		return "", fmt.Errorf("load read-only root ignore policy: %w", err)
	}
	root, err := os.OpenRoot(canonical)
	if err != nil {
		return "", fmt.Errorf("open read-only root: %w", err)
	}
	grant := &additionalReadRoot{path: canonical, root: root}
	if ignore != nil {
		grant.ignoreContent = ignore.Raw()
	}

	// Authority is immutable while a provider turn is active. Check only at the
	// commit boundary so a turn that started during filesystem validation wins.
	a.turnMu.Lock()
	defer a.turnMu.Unlock()
	if a.closed {
		_ = root.Close()
		return "", errors.New("agent is closed")
	}
	if a.turnRunning.Load() {
		_ = root.Close()
		return "", errors.New("read-only scope cannot change during an active agent turn")
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if existing := a.readRoots[canonical]; existing != nil {
		_ = root.Close()
		return existing.path, nil
	}
	if len(a.readRoots) >= maxAdditionalReadRoots {
		_ = root.Close()
		return "", fmt.Errorf("read-only root limit reached (%d)", maxAdditionalReadRoots)
	}
	for _, existing := range a.readRoots {
		if pathsOverlap(existing.path, canonical) {
			_ = root.Close()
			return "", fmt.Errorf("read-only root %q overlaps existing root %q", canonical, existing.path)
		}
	}
	if a.readRoots == nil {
		a.readRoots = make(map[string]*additionalReadRoot)
	}
	a.readRoots[canonical] = grant
	return canonical, nil
}

// RemoveReadRoot revokes one process-local read grant. It never changes the
// writable workspace or persisted session state.
func (a *Agent) RemoveReadRoot(path string) (string, error) {
	requested, err := absoluteCleanPath(path)
	if err != nil {
		return "", err
	}
	canonical := requested
	if resolved, resolveErr := filepath.EvalSymlinks(requested); resolveErr == nil {
		canonical = filepath.Clean(resolved)
	}

	a.turnMu.Lock()
	defer a.turnMu.Unlock()
	if a.closed {
		return "", errors.New("agent is closed")
	}
	if a.turnRunning.Load() {
		return "", errors.New("read-only scope cannot change during an active agent turn")
	}

	a.mu.Lock()
	grant := a.readRoots[canonical]
	if grant == nil && canonical != requested {
		grant = a.readRoots[requested]
		canonical = requested
	}
	if grant != nil {
		delete(a.readRoots, canonical)
	}
	a.mu.Unlock()
	if grant == nil {
		return "", fmt.Errorf("read-only root is not active: %s", requested)
	}
	if err := grant.root.Close(); err != nil {
		return grant.path, fmt.Errorf("close read-only root: %w", err)
	}
	return grant.path, nil
}

// ClearReadRoots revokes every additional process-local read grant.
func (a *Agent) ClearReadRoots() (int, error) {
	a.turnMu.Lock()
	defer a.turnMu.Unlock()
	if a.closed {
		return 0, errors.New("agent is closed")
	}
	if a.turnRunning.Load() {
		return 0, errors.New("read-only scope cannot change during an active agent turn")
	}

	a.mu.Lock()
	roots := make([]*additionalReadRoot, 0, len(a.readRoots))
	for _, root := range a.readRoots {
		roots = append(roots, root)
	}
	a.readRoots = make(map[string]*additionalReadRoot)
	a.mu.Unlock()
	var closeErr error
	for _, root := range roots {
		closeErr = errors.Join(closeErr, root.root.Close())
	}
	if closeErr != nil {
		return len(roots), fmt.Errorf("close read-only roots: %w", closeErr)
	}
	return len(roots), nil
}

// ReadRoots returns a stable, sorted copy suitable for prompt and UI display.
func (a *Agent) ReadRoots() []string {
	a.mu.RLock()
	roots := make([]string, 0, len(a.readRoots))
	for path := range a.readRoots {
		roots = append(roots, path)
	}
	a.mu.RUnlock()
	sort.Strings(roots)
	return roots
}

func (a *Agent) closeReadRoots() {
	a.mu.Lock()
	roots := make([]*additionalReadRoot, 0, len(a.readRoots))
	for _, root := range a.readRoots {
		roots = append(roots, root)
	}
	a.readRoots = nil
	a.mu.Unlock()
	for _, root := range roots {
		_ = root.root.Close()
	}
}

func (a *Agent) canonicalWorkDir() (string, error) {
	workDir := a.workDir
	if workDir == "" {
		var err error
		workDir, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve workspace: %w", err)
		}
	}
	workDir, err := filepath.Abs(workDir)
	if err != nil {
		return "", fmt.Errorf("resolve workspace: %w", err)
	}
	if resolved, resolveErr := filepath.EvalSymlinks(workDir); resolveErr == nil {
		workDir = resolved
	}
	return filepath.Clean(workDir), nil
}

func canonicalReadRootPath(path string) (string, error) {
	path, err := absoluteCleanPath(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve read-only root %q: %w", path, err)
	}
	return filepath.Clean(resolved), nil
}

func absoluteCleanPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("read-only root path is empty")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve read-only root %q: %w", path, err)
	}
	return filepath.Clean(absolute), nil
}

func pathsOverlap(first, second string) bool {
	_, firstContainsSecond, firstErr := workspaceRelative(first, second)
	_, secondContainsFirst, secondErr := workspaceRelative(second, first)
	return firstErr == nil && secondErr == nil && (firstContainsSecond || secondContainsFirst)
}

func (a *Agent) resolveReadablePath(path string) (readablePath, error) {
	resolved, workspaceErr := a.resolvePath(path)
	if workspaceErr == nil {
		return readablePath{absolute: resolved}, nil
	}

	workspace, err := a.canonicalWorkDir()
	if err != nil {
		return readablePath{}, err
	}
	candidate := path
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(workspace, candidate)
	}
	candidate, err = filepath.Abs(candidate)
	if err != nil {
		return readablePath{}, fmt.Errorf("resolve readable path %q: %w", path, err)
	}
	candidate = filepath.Clean(candidate)
	candidate, err = resolveExistingAncestor(candidate)
	if err != nil {
		return readablePath{}, fmt.Errorf("resolve readable path %q: %w", path, err)
	}
	if _, insideWorkspace, relErr := workspaceRelative(workspace, candidate); relErr == nil && insideWorkspace {
		// Never let an additional root bypass a workspace .agentignore or a
		// workspace symlink-escape rejection.
		return readablePath{}, workspaceErr
	}

	a.mu.RLock()
	var grant *additionalReadRoot
	var relative string
	for _, root := range a.readRoots {
		rel, inside, relErr := workspaceRelative(root.path, candidate)
		if relErr == nil && inside {
			grant = root
			relative = rel
			break
		}
	}
	a.mu.RUnlock()
	if grant == nil {
		return readablePath{}, fmt.Errorf("path %q is outside the writable workspace and active read-only roots; use /scope add-read <directory>", path)
	}
	if pathIgnoredWithContent(grant.ignoreContent, relative) {
		return readablePath{}, fmt.Errorf("path %q is excluded by %s/.agentignore", path, grant.path)
	}
	return readablePath{absolute: candidate, relative: relative, root: grant}, nil
}

func (path readablePath) stat() (os.FileInfo, error) {
	if path.root == nil {
		return os.Stat(path.absolute)
	}
	return path.root.root.Stat(filepath.ToSlash(path.relative))
}

func (path readablePath) readDir() ([]os.DirEntry, error) {
	if path.root == nil {
		return os.ReadDir(path.absolute)
	}
	directory, err := path.root.root.Open(filepath.ToSlash(path.relative))
	if err != nil {
		return nil, err
	}
	defer func() { _ = directory.Close() }()
	return directory.ReadDir(-1)
}

func (path readablePath) readBounded(limit int64) ([]byte, error) {
	return path.readBoundedAt(path.absolute, limit)
}

func (path readablePath) readBoundedAt(absolute string, limit int64) ([]byte, error) {
	if path.root == nil {
		return readBoundedFile(absolute, limit)
	}
	relative, inside, err := workspaceRelative(path.root.path, absolute)
	if err != nil || !inside {
		return nil, fmt.Errorf("path escapes read-only root: %s", absolute)
	}
	rootRelative := filepath.ToSlash(relative)
	info, err := path.root.root.Stat(rootRelative)
	if err != nil {
		return nil, err
	}
	if err := validateBoundedFileInfo(info, limit); err != nil {
		return nil, err
	}
	file, err := path.root.root.Open(rootRelative)
	if err != nil {
		return nil, err
	}
	return readBoundedOpenFile(file, limit)
}

func (path readablePath) ignored(a *Agent, absolute string) bool {
	if path.root == nil {
		return a.pathIgnoredResolved(absolute)
	}
	relative, inside, err := workspaceRelative(path.root.path, absolute)
	return err != nil || !inside || pathIgnoredWithContent(path.root.ignoreContent, relative)
}

func (path readablePath) walk(fn filepath.WalkFunc) error {
	if path.root == nil {
		return filepath.Walk(path.absolute, fn)
	}
	start := filepath.ToSlash(path.relative)
	return fs.WalkDir(path.root.root.FS(), start, func(relative string, entry fs.DirEntry, walkErr error) error {
		absolute := filepath.Join(path.root.path, filepath.FromSlash(relative))
		if walkErr != nil {
			return fn(absolute, nil, walkErr)
		}
		info, err := entry.Info()
		if err != nil {
			return fn(absolute, nil, err)
		}
		return fn(absolute, info, nil)
	})
}

func readBoundedOpenFile(file *os.File, limit int64) ([]byte, error) {
	if file == nil {
		return nil, errors.New("file is unavailable")
	}
	defer func() { _ = file.Close() }()
	if limit <= 0 {
		return nil, fmt.Errorf("invalid read limit %d", limit)
	}
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if err := validateBoundedFileInfo(info, limit); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("file grew beyond %d-byte limit while reading", limit)
	}
	return data, nil
}

func validateBoundedFileInfo(info os.FileInfo, limit int64) error {
	if limit <= 0 {
		return fmt.Errorf("invalid read limit %d", limit)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("path is not a regular file (%s)", info.Mode().Type())
	}
	if info.Size() > limit {
		return fmt.Errorf("file is %d bytes; limit is %d bytes", info.Size(), limit)
	}
	return nil
}

func pathIgnoredWithContent(ignoreContent, path string) bool {
	if strings.TrimSpace(ignoreContent) == "" {
		return false
	}
	cleanPath := strings.TrimSuffix(filepath.ToSlash(filepath.Clean(path)), "/")
	ancestors := []string{cleanPath}
	for parent := filepath.ToSlash(filepath.Dir(cleanPath)); parent != "." && parent != "/" && parent != ""; parent = filepath.ToSlash(filepath.Dir(parent)) {
		ancestors = append(ancestors, parent)
	}
	ignored := false
	for _, line := range strings.Split(ignoreContent, "\n") {
		pattern := strings.TrimSpace(line)
		if pattern == "" || strings.HasPrefix(pattern, "#") {
			continue
		}
		negated := strings.HasPrefix(pattern, "!")
		pattern = strings.TrimSpace(strings.TrimPrefix(pattern, "!"))
		pattern = strings.TrimPrefix(strings.TrimSuffix(filepath.ToSlash(pattern), "/"), "/")
		if pattern == "" {
			continue
		}
		for _, candidate := range ancestors {
			if ignorePatternMatches(pattern, candidate) {
				ignored = !negated
				break
			}
		}
	}
	return ignored
}
