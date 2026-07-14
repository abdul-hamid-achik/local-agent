package agent

import (
	"container/heap"
	"context"
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

const (
	maxAdditionalReadAuthorities = 16
	readDirectoryBatchSize       = 64
)

// ReadGrantKind distinguishes broad directory authority from one exact file.
// Both are temporary and read-only; neither participates in mutation policy.
type ReadGrantKind string

const (
	ReadGrantDirectory ReadGrantKind = "directory"
	ReadGrantExactFile ReadGrantKind = "exact_file"
)

// ReadGrant is the bounded host projection used by system and UI surfaces.
type ReadGrant struct {
	Path string
	Kind ReadGrantKind
	// identity is an opaque preview-time filesystem identity. It is carried by
	// the TUI from InspectReadPath back to AddInspectedReadGrant and is never
	// rendered, serialized, or accepted from the model/user as data.
	identity *readPathIdentity
}

// ReadPathInspection is a non-mutating, canonical host preflight result.
type ReadPathInspection struct {
	Path            string
	Kind            ReadGrantKind
	External        bool
	AlreadyReadable bool
	identity        *readPathIdentity
}

type readPathIdentity struct {
	path string
	info os.FileInfo
}

// Grant returns the opaque commit value for this exact inspection. Callers may
// display Path and Kind, but only AddInspectedReadGrant can consume identity.
func (inspection ReadPathInspection) Grant() ReadGrant {
	return ReadGrant{Path: inspection.Path, Kind: inspection.Kind, identity: inspection.identity}
}

// additionalReadRoot is an explicit, process-local read grant. os.Root pins the
// selected directory and prevents relative operations (including symlinks) from
// escaping it. Additional roots never participate in mutation authorization.
type additionalReadRoot struct {
	path          string
	root          *os.Root
	info          os.FileInfo
	ignoreContent string
}

// additionalReadFile pins the target's parent directory and original file
// identity. Every open revalidates os.SameFile before returning bytes, so a
// replacement cannot inherit authority granted to the original file.
type additionalReadFile struct {
	path   string
	parent string
	name   string
	root   *os.Root
	info   os.FileInfo
}

// readablePath carries the authority needed to open one path. A nil root is the
// ordinary workspace path, which keeps the existing resolver and behavior.
type readablePath struct {
	absolute string
	relative string
	root     *additionalReadRoot
	file     *additionalReadFile
}

// AddReadRoot grants read-only access to one external directory for this Agent
// process. It deliberately rejects overlap with the writable workspace and with
// another grant so ignore and authority boundaries remain unambiguous.
func (a *Agent) AddReadRoot(path string) (string, error) {
	return a.addReadRoot(path, nil)
}

func (a *Agent) addReadRoot(path string, expected *readPathIdentity) (string, error) {
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
	if err := validateInspectedReadIdentity(expected, canonical, info); err != nil {
		return "", err
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
	pinned, err := root.Stat(".")
	if err != nil {
		_ = root.Close()
		return "", fmt.Errorf("inspect opened read-only root: %w", err)
	}
	if !pinned.IsDir() || !os.SameFile(info, pinned) {
		_ = root.Close()
		return "", fmt.Errorf("read-only root changed during authorization: %s", canonical)
	}
	if err := validateInspectedReadIdentity(expected, canonical, pinned); err != nil {
		_ = root.Close()
		return "", err
	}
	grant := &additionalReadRoot{path: canonical, root: root, info: pinned}
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
	if existing := a.readRoots[canonical]; existing != nil {
		a.mu.Unlock()
		_ = root.Close()
		return existing.path, nil
	}
	for _, existing := range a.readRoots {
		if pathsOverlap(existing.path, canonical) {
			a.mu.Unlock()
			_ = root.Close()
			return "", fmt.Errorf("read-only root %q overlaps existing root %q", canonical, existing.path)
		}
	}
	// A newly authorized directory supersedes exact-file grants it contains.
	// Removing those narrower grants keeps the total authority count honest and
	// avoids presenting duplicate scopes for the same path.
	var superseded []*additionalReadFile
	for path, file := range a.readFiles {
		if _, inside, overlapErr := workspaceRelative(canonical, path); overlapErr == nil && inside {
			superseded = append(superseded, file)
			delete(a.readFiles, path)
		}
	}
	if len(a.readRoots)+len(a.readFiles) >= maxAdditionalReadAuthorities {
		for _, file := range superseded {
			a.readFiles[file.path] = file
		}
		a.mu.Unlock()
		_ = root.Close()
		return "", fmt.Errorf("read-only authority limit reached (%d)", maxAdditionalReadAuthorities)
	}
	if a.readRoots == nil {
		a.readRoots = make(map[string]*additionalReadRoot)
	}
	a.readRoots[canonical] = grant
	a.mu.Unlock()
	for _, file := range superseded {
		_ = file.root.Close()
	}
	return canonical, nil
}

// AddReadFile grants read-only authority to one exact existing regular file.
// The parent os.Root is an implementation boundary, not granted authority:
// sibling files remain unavailable.
func (a *Agent) AddReadFile(path string) (string, error) {
	return a.addReadFile(path, nil)
}

func (a *Agent) addReadFile(path string, expected *readPathIdentity) (string, error) {
	canonical, info, err := canonicalExistingReadPath(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("exact read grant requires a regular file: %s", canonical)
	}
	if err := validateInspectedReadIdentity(expected, canonical, info); err != nil {
		return "", err
	}
	workspace, err := a.canonicalWorkDir()
	if err != nil {
		return "", err
	}
	if _, inside, relErr := workspaceRelative(workspace, canonical); relErr == nil && inside {
		return "", fmt.Errorf("exact read file %q is already inside writable workspace %q", canonical, workspace)
	}

	a.mu.RLock()
	if existing := a.readFiles[canonical]; existing != nil {
		a.mu.RUnlock()
		return existing.path, nil
	}
	for _, root := range a.readRoots {
		if _, inside, relErr := workspaceRelative(root.path, canonical); relErr == nil && inside {
			a.mu.RUnlock()
			return canonical, nil
		}
	}
	a.mu.RUnlock()

	parent := filepath.Dir(canonical)
	name := filepath.Base(canonical)
	root, err := os.OpenRoot(parent)
	if err != nil {
		return "", fmt.Errorf("open exact read-file parent: %w", err)
	}
	pinned, err := root.Stat(filepath.ToSlash(name))
	if err != nil {
		_ = root.Close()
		return "", fmt.Errorf("inspect exact read file: %w", err)
	}
	if !pinned.Mode().IsRegular() || !os.SameFile(info, pinned) {
		_ = root.Close()
		return "", fmt.Errorf("exact read file changed during authorization: %s", canonical)
	}
	if err := validateInspectedReadIdentity(expected, canonical, pinned); err != nil {
		_ = root.Close()
		return "", err
	}
	grant := &additionalReadFile{path: canonical, parent: parent, name: name, root: root, info: pinned}

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
	if existing := a.readFiles[canonical]; existing != nil {
		a.mu.Unlock()
		_ = root.Close()
		return existing.path, nil
	}
	for _, directory := range a.readRoots {
		if _, inside, relErr := workspaceRelative(directory.path, canonical); relErr == nil && inside {
			a.mu.Unlock()
			_ = root.Close()
			return canonical, nil
		}
	}
	if len(a.readRoots)+len(a.readFiles) >= maxAdditionalReadAuthorities {
		a.mu.Unlock()
		_ = root.Close()
		return "", fmt.Errorf("read-only authority limit reached (%d)", maxAdditionalReadAuthorities)
	}
	if a.readFiles == nil {
		a.readFiles = make(map[string]*additionalReadFile)
	}
	a.readFiles[canonical] = grant
	a.mu.Unlock()
	return canonical, nil
}

// AddInspectedReadGrant commits only the filesystem object observed by the
// corresponding InspectReadPath call. A path that was replaced or retargeted
// while the approval UI was open fails closed instead of inheriting consent.
func (a *Agent) AddInspectedReadGrant(grant ReadGrant) (string, error) {
	if grant.identity == nil || grant.identity.info == nil {
		return "", errors.New("read grant preview identity is unavailable; inspect the path again")
	}
	switch grant.Kind {
	case ReadGrantDirectory:
		return a.addReadRoot(grant.Path, grant.identity)
	case ReadGrantExactFile:
		return a.addReadFile(grant.Path, grant.identity)
	default:
		return "", fmt.Errorf("unsupported read grant kind %q", grant.Kind)
	}
}

func validateInspectedReadIdentity(expected *readPathIdentity, canonical string, current os.FileInfo) error {
	if expected == nil {
		return nil
	}
	if expected.info == nil || filepath.Clean(expected.path) != filepath.Clean(canonical) ||
		current == nil || !os.SameFile(expected.info, current) {
		return fmt.Errorf("read path changed after approval preview; inspect it again: %s", canonical)
	}
	return nil
}

// InspectReadPath canonicalizes one existing host path without changing
// authority. It is used by local UI preflight and never enters model or MCP
// contextual-routing input.
func (a *Agent) InspectReadPath(path string) (ReadPathInspection, error) {
	canonical, info, err := canonicalExistingReadPath(path)
	if err != nil {
		return ReadPathInspection{}, err
	}
	kind := ReadGrantDirectory
	if info.Mode().IsRegular() {
		kind = ReadGrantExactFile
	} else if !info.IsDir() {
		return ReadPathInspection{}, fmt.Errorf("read path is neither a regular file nor directory: %s", canonical)
	}
	if kind == ReadGrantDirectory && canonical == string(filepath.Separator) {
		return ReadPathInspection{}, errors.New("refusing to grant the filesystem root as read-only scope")
	}
	workspace, err := a.canonicalWorkDir()
	if err != nil {
		return ReadPathInspection{}, err
	}
	identity := &readPathIdentity{path: canonical, info: info}
	if _, inside, relErr := workspaceRelative(workspace, canonical); relErr == nil && inside {
		return ReadPathInspection{Path: canonical, Kind: kind, AlreadyReadable: true, identity: identity}, nil
	}
	if kind == ReadGrantDirectory && pathsOverlap(workspace, canonical) {
		return ReadPathInspection{}, fmt.Errorf("read-only root %q overlaps writable workspace %q", canonical, workspace)
	}

	inspection := ReadPathInspection{Path: canonical, Kind: kind, External: true, identity: identity}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if kind == ReadGrantExactFile && a.readFiles[canonical] != nil {
		inspection.AlreadyReadable = true
	}
	if !inspection.AlreadyReadable {
		for _, root := range a.readRoots {
			if _, inside, relErr := workspaceRelative(root.path, canonical); relErr == nil && inside {
				inspection.AlreadyReadable = true
				break
			}
			if kind == ReadGrantDirectory && pathsOverlap(root.path, canonical) {
				return ReadPathInspection{}, fmt.Errorf("read-only root %q overlaps existing root %q", canonical, root.path)
			}
		}
	}
	return inspection, nil
}

// RemoveReadPath revokes one exact-file or directory grant. It never changes
// the writable workspace or persisted session state.
func (a *Agent) RemoveReadPath(path string) (ReadGrant, error) {
	requested, err := absoluteCleanReadPath(path)
	if err != nil {
		return ReadGrant{}, err
	}
	canonical := requested
	if resolved, resolveErr := filepath.EvalSymlinks(requested); resolveErr == nil {
		canonical = filepath.Clean(resolved)
	}

	a.turnMu.Lock()
	defer a.turnMu.Unlock()
	if a.closed {
		return ReadGrant{}, errors.New("agent is closed")
	}
	if a.turnRunning.Load() {
		return ReadGrant{}, errors.New("read-only scope cannot change during an active agent turn")
	}

	a.mu.Lock()
	directory := a.readRoots[canonical]
	file := a.readFiles[canonical]
	if directory == nil && file == nil && canonical != requested {
		directory = a.readRoots[requested]
		file = a.readFiles[requested]
		canonical = requested
	}
	if directory != nil {
		delete(a.readRoots, canonical)
	} else if file != nil {
		delete(a.readFiles, canonical)
	}
	a.mu.Unlock()
	if directory == nil && file == nil {
		return ReadGrant{}, fmt.Errorf("read-only path is not active: %s", requested)
	}
	if directory != nil {
		result := ReadGrant{Path: directory.path, Kind: ReadGrantDirectory}
		if err := directory.root.Close(); err != nil {
			return result, fmt.Errorf("close read-only root: %w", err)
		}
		return result, nil
	}
	result := ReadGrant{Path: file.path, Kind: ReadGrantExactFile}
	if err := file.root.Close(); err != nil {
		return result, fmt.Errorf("close exact read file: %w", err)
	}
	return result, nil
}

// RemoveReadRoot is the source-compatible string projection of RemoveReadPath.
func (a *Agent) RemoveReadRoot(path string) (string, error) {
	grant, err := a.RemoveReadPath(path)
	return grant.Path, err
}

// ClearReadRoots revokes every temporary directory and exact-file read grant.
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
	files := make([]*additionalReadFile, 0, len(a.readFiles))
	for _, file := range a.readFiles {
		files = append(files, file)
	}
	a.readRoots = make(map[string]*additionalReadRoot)
	a.readFiles = make(map[string]*additionalReadFile)
	a.mu.Unlock()
	var closeErr error
	for _, root := range roots {
		closeErr = errors.Join(closeErr, root.root.Close())
	}
	for _, file := range files {
		closeErr = errors.Join(closeErr, file.root.Close())
	}
	if closeErr != nil {
		return len(roots) + len(files), fmt.Errorf("close read-only grants: %w", closeErr)
	}
	return len(roots) + len(files), nil
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

// ReadGrants returns a stable typed copy for host and system presentation.
func (a *Agent) ReadGrants() []ReadGrant {
	a.mu.RLock()
	grants := make([]ReadGrant, 0, len(a.readRoots)+len(a.readFiles))
	for path := range a.readRoots {
		root := a.readRoots[path]
		grants = append(grants, ReadGrant{
			Path: path, Kind: ReadGrantDirectory,
			identity: &readPathIdentity{path: path, info: root.info},
		})
	}
	for path := range a.readFiles {
		file := a.readFiles[path]
		grants = append(grants, ReadGrant{
			Path: path, Kind: ReadGrantExactFile,
			identity: &readPathIdentity{path: path, info: file.info},
		})
	}
	a.mu.RUnlock()
	sort.Slice(grants, func(i, j int) bool {
		if grants[i].Path != grants[j].Path {
			return grants[i].Path < grants[j].Path
		}
		return grants[i].Kind < grants[j].Kind
	})
	return grants
}

func (a *Agent) closeReadRoots() {
	a.mu.Lock()
	roots := make([]*additionalReadRoot, 0, len(a.readRoots))
	for _, root := range a.readRoots {
		roots = append(roots, root)
	}
	files := make([]*additionalReadFile, 0, len(a.readFiles))
	for _, file := range a.readFiles {
		files = append(files, file)
	}
	a.readRoots = nil
	a.readFiles = nil
	a.mu.Unlock()
	for _, root := range roots {
		_ = root.root.Close()
	}
	for _, file := range files {
		_ = file.root.Close()
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
	path, err := absoluteCleanReadPath(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve read-only root %q: %w", path, err)
	}
	return filepath.Clean(resolved), nil
}

func canonicalExistingReadPath(path string) (string, os.FileInfo, error) {
	absolute, err := absoluteCleanReadPath(path)
	if err != nil {
		return "", nil, err
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", nil, fmt.Errorf("resolve read path %q: %w", absolute, err)
	}
	canonical = filepath.Clean(canonical)
	info, err := os.Stat(canonical)
	if err != nil {
		return "", nil, fmt.Errorf("inspect read path: %w", err)
	}
	return canonical, info, nil
}

func absoluteCleanReadPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("read-only root path is empty")
	}
	if path == "~" || strings.HasPrefix(path, "~"+string(filepath.Separator)) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		if path == "~" {
			path = home
		} else {
			path = filepath.Join(home, strings.TrimPrefix(path, "~"+string(filepath.Separator)))
		}
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
	requested := path
	if path == "~" || strings.HasPrefix(path, "~"+string(filepath.Separator)) {
		var err error
		path, err = absoluteCleanReadPath(path)
		if err != nil {
			return readablePath{}, err
		}
	}
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
	exact := a.readFiles[candidate]
	var grant *additionalReadRoot
	var relative string
	if exact == nil {
		for _, root := range a.readRoots {
			rel, inside, relErr := workspaceRelative(root.path, candidate)
			if relErr == nil && inside {
				grant = root
				relative = rel
				break
			}
		}
	}
	a.mu.RUnlock()
	if exact != nil {
		return readablePath{absolute: candidate, file: exact}, nil
	}
	if grant == nil {
		return readablePath{}, fmt.Errorf("path %q is outside the writable workspace and active temporary read grants; authorize the exact file when prompted or use /scope add-read <directory>", requested)
	}
	if pathIgnoredWithContent(grant.ignoreContent, relative) {
		return readablePath{}, fmt.Errorf("path %q is excluded by %s/.agentignore", requested, grant.path)
	}
	return readablePath{absolute: candidate, relative: relative, root: grant}, nil
}

func (path readablePath) stat() (os.FileInfo, error) {
	if path.file != nil {
		return path.file.currentInfo()
	}
	if path.root == nil {
		return os.Stat(path.absolute)
	}
	return path.root.root.Stat(filepath.ToSlash(path.relative))
}

// readDirBounded returns the same lexicographically-first visible entries as a
// full os.ReadDir followed by a limit, while keeping memory bounded by that
// limit. External roots must not materialize an unbounded host directory before
// ls applies its result cap. The boolean reports whether the directory had any
// entries before ignore filtering, preserving the existing empty-directory
// distinction.
func (path readablePath) readDirBounded(ctx context.Context, limit int, include func(os.DirEntry) bool) ([]os.DirEntry, bool, error) {
	if path.file != nil {
		return nil, false, fmt.Errorf("exact read grant is not a directory: %s", path.absolute)
	}
	if limit <= 0 {
		return nil, false, errors.New("directory result limit must be positive")
	}

	var directory *os.File
	var err error
	if path.root == nil {
		directory, err = os.Open(path.absolute)
	} else {
		directory, err = path.root.root.Open(filepath.ToSlash(path.relative))
	}
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = directory.Close() }()

	selected := make(maxNameDirEntryHeap, 0, min(limit, readDirectoryBatchSize))
	hadEntries := false
	for {
		if err := ctx.Err(); err != nil {
			return nil, hadEntries, err
		}
		batch, readErr := directory.ReadDir(readDirectoryBatchSize)
		if len(batch) > 0 {
			hadEntries = true
		}
		for _, entry := range batch {
			if err := ctx.Err(); err != nil {
				return nil, hadEntries, err
			}
			if include != nil && !include(entry) {
				continue
			}
			if len(selected) < limit {
				heap.Push(&selected, entry)
				continue
			}
			if entry.Name() < selected[0].Name() {
				selected[0] = entry
				heap.Fix(&selected, 0)
			}
		}
		switch {
		case errors.Is(readErr, io.EOF):
			sort.Slice(selected, func(i, j int) bool { return selected[i].Name() < selected[j].Name() })
			return []os.DirEntry(selected), hadEntries, nil
		case readErr != nil:
			return nil, hadEntries, readErr
		case len(batch) == 0:
			return nil, hadEntries, io.ErrNoProgress
		}
	}
}

// maxNameDirEntryHeap keeps the lexicographically-largest selected name at the
// root so a streaming scan retains only the first N names.
type maxNameDirEntryHeap []os.DirEntry

func (entries maxNameDirEntryHeap) Len() int           { return len(entries) }
func (entries maxNameDirEntryHeap) Less(i, j int) bool { return entries[i].Name() > entries[j].Name() }
func (entries maxNameDirEntryHeap) Swap(i, j int)      { entries[i], entries[j] = entries[j], entries[i] }
func (entries *maxNameDirEntryHeap) Push(value any)    { *entries = append(*entries, value.(os.DirEntry)) }
func (entries *maxNameDirEntryHeap) Pop() any {
	old := *entries
	last := old[len(old)-1]
	*entries = old[:len(old)-1]
	return last
}

func (path readablePath) readBounded(limit int64) ([]byte, error) {
	return path.readBoundedAt(path.absolute, limit)
}

func (path readablePath) readBoundedAt(absolute string, limit int64) ([]byte, error) {
	if path.file != nil {
		if filepath.Clean(absolute) != path.file.path {
			return nil, fmt.Errorf("path escapes exact read-file grant: %s", absolute)
		}
		file, err := path.file.openVerified()
		if err != nil {
			return nil, err
		}
		return readBoundedOpenFile(file, limit)
	}
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
	if path.file != nil {
		return filepath.Clean(absolute) != path.file.path
	}
	if path.root == nil {
		return a.pathIgnoredResolved(absolute)
	}
	relative, inside, err := workspaceRelative(path.root.path, absolute)
	return err != nil || !inside || pathIgnoredWithContent(path.root.ignoreContent, relative)
}

func (path readablePath) walk(fn filepath.WalkFunc) error {
	if path.file != nil {
		info, err := path.file.currentInfo()
		return fn(path.file.path, info, err)
	}
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

func (file *additionalReadFile) currentInfo() (os.FileInfo, error) {
	if file == nil || file.root == nil {
		return nil, errors.New("exact read file is unavailable")
	}
	current, err := file.root.Stat(filepath.ToSlash(file.name))
	if err != nil {
		return nil, err
	}
	if !current.Mode().IsRegular() || !os.SameFile(file.info, current) {
		return nil, fmt.Errorf("exact read file changed after authorization: %s", file.path)
	}
	return current, nil
}

func (file *additionalReadFile) openVerified() (*os.File, error) {
	if _, err := file.currentInfo(); err != nil {
		return nil, err
	}
	opened, err := file.root.Open(filepath.ToSlash(file.name))
	if err != nil {
		return nil, err
	}
	info, err := opened.Stat()
	if err != nil {
		_ = opened.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() || !os.SameFile(file.info, info) {
		_ = opened.Close()
		return nil, fmt.Errorf("exact read file changed after authorization: %s", file.path)
	}
	return opened, nil
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
