package agent

import (
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

var autoCommandAssignment = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=[^=]*$`)
var autoCommandAssignmentValue = regexp.MustCompile(`^[A-Za-z0-9_.-]*$`)
var autoSedPrintProgram = regexp.MustCompile(`^(?:[0-9]+|\$)(?:,(?:[0-9]+|\$))?[pP]$`)

// autoScopedCommandAllowed recognizes a deliberately bounded shell subset for
// AUTO. It is not a shell sandbox: the subprocess still runs under the host's
// ordinary account. The catalog exists to keep routine local build, test,
// formatting, and inspection work flowing while Git, dynamic expansion,
// destructive commands, network-facing CLIs, redirection to files, and
// workspace escapes continue through interactive approval.
func (a *Agent) autoScopedCommandAllowed(command string) bool {
	if strings.TrimSpace(command) == "" || strings.ContainsRune(command, '\r') ||
		hasShellLineContinuation(command) || hasDynamicShellSyntax(command) || hasUnquotedShellGlob(command) {
		return false
	}
	commands, separators, ok := splitStaticShellCommands(command)
	if !ok || len(commands) == 0 {
		return false
	}
	if len(commands) > 1 && staticCommandsContainExecutable(commands, "cd") {
		// A successful cd affects later commands only for the simple all-&& form
		// we can model exactly. Pipelines isolate builtins, while `;`, newlines,
		// and `||` may continue after a failed cd from the previous directory.
		for _, separator := range separators {
			if separator != "&&" {
				return false
			}
		}
	}
	baseDir := a.activeWorkDir()
	for _, words := range commands {
		if !a.autoScopedSimpleCommandAllowed(words, baseDir) {
			return false
		}
		if staticCommandExecutable(words) == "cd" {
			var cdOK bool
			baseDir, cdOK = a.autoScopedCDTarget(words, baseDir)
			if !cdOK {
				return false
			}
		}
	}
	return true
}

func hasShellLineContinuation(command string) bool {
	// POSIX shells remove backslash-newline before tokenization. The bounded
	// scanner must never authorize a different token stream than sh executes.
	return strings.Contains(command, "\\\n") || strings.Contains(command, "\\\r")
}

func hasDynamicShellSyntax(command string) bool {
	var quote rune
	escaped := false
	for _, character := range command {
		if escaped {
			escaped = false
			continue
		}
		if character == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote == '\'' {
			if character == quote {
				quote = 0
			}
			continue
		}
		if quote == '"' {
			switch character {
			case '"':
				quote = 0
			case '$', '`':
				return true
			}
			continue
		}
		switch character {
		case '\'', '"':
			quote = character
		case '$', '`', '(', ')', '{', '}':
			return true
		}
	}
	return false
}

func hasUnquotedShellGlob(command string) bool {
	var quote rune
	escaped := false
	for _, character := range command {
		if escaped {
			escaped = false
			continue
		}
		if character == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote != 0 {
			if character == quote {
				quote = 0
			}
			continue
		}
		switch character {
		case '\'', '"':
			quote = character
		case '*', '?', '[':
			return true
		}
	}
	return false
}

// splitStaticShellCommands accepts quoted words and foreground &&, ||, ; and
// pipe composition. Expansion, grouping, backgrounding, and file redirection
// are rejected before this point or by the scanner. This keeps command-name
// checks meaningful even when several routine development commands are joined.
func splitStaticShellCommands(command string) ([][]string, []string, bool) {
	trimmed := strings.TrimSpace(command)
	if strings.HasSuffix(trimmed, "&&") || strings.HasSuffix(trimmed, "||") ||
		strings.HasSuffix(trimmed, "|") || strings.HasSuffix(trimmed, "&") {
		return nil, nil, false
	}
	var (
		commands   [][]string
		separators []string
		words      []string
		word       strings.Builder
		quote      rune
		escaped    bool
		wordOpen   bool
	)
	flushWord := func() {
		if wordOpen {
			words = append(words, word.String())
			word.Reset()
			wordOpen = false
		}
	}
	flushCommand := func() bool {
		flushWord()
		if len(words) == 0 {
			return false
		}
		commands = append(commands, words)
		words = nil
		return true
	}
	runes := []rune(command)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if escaped {
			word.WriteRune(r)
			wordOpen = true
			escaped = false
			continue
		}
		if r == '\\' && quote != '\'' {
			// Within double quotes, POSIX sh only consumes backslash before
			// $, `, ", and backslash (line continuations were rejected above).
			// Preserve it before every other rune so validation sees the same
			// filesystem token the shell will use.
			if quote == '"' && i+1 < len(runes) && !strings.ContainsRune("$`\"\\", runes[i+1]) {
				word.WriteRune(r)
				wordOpen = true
				continue
			}
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				word.WriteRune(r)
			}
			continue
		}
		if !wordOpen {
			if redirectLength := staticDescriptorRedirectLength(runes, i); redirectLength > 0 {
				i += redirectLength - 1
				continue
			}
		}
		switch r {
		case '\'', '"':
			quote = r
			wordOpen = true
		case ' ', '\t', '\r':
			flushWord()
		case '\n', ';', '|', '&':
			if r == '&' && (i+1 >= len(runes) || runes[i+1] != '&') {
				return nil, nil, false
			}
			separator := string(r)
			if r == '|' && i+1 < len(runes) && runes[i+1] == '|' {
				i++
				separator = "||"
			} else if r == '&' {
				i++
				separator = "&&"
			} else if r == '\n' {
				separator = ";"
			}
			if !flushCommand() {
				return nil, nil, false
			}
			separators = append(separators, separator)
		case '<', '>':
			return nil, nil, false
		default:
			if unicode.IsControl(r) {
				return nil, nil, false
			}
			word.WriteRune(r)
			wordOpen = true
		}
	}
	if escaped || quote != 0 {
		return nil, nil, false
	}
	flushWord()
	if len(words) > 0 {
		commands = append(commands, words)
	}
	if len(separators) != len(commands)-1 {
		return nil, nil, false
	}
	return commands, separators, len(commands) > 0
}

func staticDescriptorRedirectLength(command []rune, offset int) int {
	for _, redirect := range []string{"2>&1", "1>&2"} {
		candidate := []rune(redirect)
		if offset+len(candidate) > len(command) {
			continue
		}
		matched := true
		for index := range candidate {
			if command[offset+index] != candidate[index] {
				matched = false
				break
			}
		}
		if !matched {
			continue
		}
		next := offset + len(candidate)
		if next < len(command) && !unicode.IsSpace(command[next]) && !strings.ContainsRune(";|&\n", command[next]) {
			continue
		}
		return len(candidate)
	}
	return 0
}

func staticCommandExecutable(words []string) string {
	for len(words) > 0 && autoCommandAssignmentAllowed(words[0]) {
		words = words[1:]
	}
	if len(words) == 0 || filepath.Base(words[0]) != words[0] {
		return ""
	}
	return words[0]
}

func staticCommandsContainExecutable(commands [][]string, executable string) bool {
	for _, words := range commands {
		if staticCommandExecutable(words) == executable {
			return true
		}
	}
	return false
}

func (a *Agent) autoScopedCDTarget(words []string, baseDir string) (string, bool) {
	for len(words) > 0 && autoCommandAssignmentAllowed(words[0]) {
		words = words[1:]
	}
	if len(words) == 0 || words[0] != "cd" {
		return "", false
	}
	args := words[1:]
	if len(args) == 2 && args[0] == "--" {
		args = args[1:]
	}
	if len(args) != 1 || rawPathHasParentTraversal(args[0]) {
		return "", false
	}
	target := args[0]
	if target == "-" {
		return "", false
	}
	if !filepath.IsAbs(target) && baseDir != "" {
		target = filepath.Join(baseDir, target)
	}
	resolved, err := a.resolvePath(target)
	return resolved, err == nil
}

func (a *Agent) autoScopedSimpleCommandAllowed(words []string, baseDir string) bool {
	for len(words) > 0 && autoCommandAssignmentAllowed(words[0]) {
		words = words[1:]
	}
	if len(words) == 0 {
		return false
	}
	executable := filepath.Base(words[0])
	// A path-qualified executable can impersonate a catalogued command (for
	// example, ./git or /tmp/go). Keep executable provenance in the host PATH;
	// only arguments may name workspace files.
	if executable != words[0] {
		return false
	}
	if executable == "." || executable == "source" || executable == "eval" || executable == "exec" || executable == "env" {
		return false
	}
	if !a.autoCommandExecutableAllowed(executable) {
		return false
	}
	args := words[1:]
	for index, word := range args {
		if autoCommandNonPathArgument(executable, args, index) {
			continue
		}
		if !a.autoCommandPathAllowed(word, baseDir) {
			return false
		}
	}

	if !a.autoScopedAttachedPathOptionsAllowed(executable, args, baseDir) {
		return false
	}
	switch executable {
	case "cd":
		if len(args) == 2 && args[0] == "--" {
			args = args[1:]
		}
		return len(args) == 1 && a.autoCommandCandidatePathAllowed(args[0], baseDir)
	case "go":
		return autoScopedGoCommandAllowed(args)
	case "git":
		return false
	case "npm":
		return autoScopedPackageCommandAllowed(args, "test")
	case "pnpm", "yarn":
		return autoScopedPackageCommandAllowed(args, "test", "build", "lint", "check", "typecheck")
	case "bun":
		return autoScopedPackageCommandAllowed(args, "test", "lint")
	case "cargo":
		return autoScopedCargoCommandAllowed(args)
	case "swift":
		return autoScopedSwiftCommandAllowed(args)
	case "sed":
		return autoScopedSedCommandAllowed(args)
	case "find", "rg", "grep", "tree", "du", "ls":
		// Recursive shell inspection can discover or read descendants that the
		// host-owned ignore policy excludes (for example `rg --no-ignore .`,
		// `tree -a .`, or `ls -Ra .`). Built-in list/grep/read operations enforce
		// that policy, so raw search and directory-enumeration processes remain
		// approval-gated in AUTO even for workspace operands.
		return false
	case "sort":
		return !containsLongOptionPrefix(args, "--compress-program", "--files0-from")
	case "file":
		return !containsArg(args, "-C", "-S", "-f", "-z", "-Z", "-m", "-M") &&
			!containsLongOptionPrefix(args, "--compile", "--files-from", "--magic-file", "--no-sandbox", "--uncompress", "--uncompress-noreport") &&
			!containsClusteredShortOption(args, "CSfzZmM", "P")
	case "date":
		return len(args) == 0
	case "wc":
		return !containsLongOptionPrefix(args, "--files0-from")
	case "diff":
		// Directory comparison follows nested symlinks by default on BSD/GNU,
		// and pagination can launch `pr`. The built-in diff and /changes paths
		// provide host-confined inspection, so raw diff stays approval-gated.
		return false
	case "mkdir", "touch":
		return len(args) > 0 && !argumentContainsPath(args, "/dev/null")
	case "eslint":
		return !containsArg(args, "--inspect-config", "--init", "--mcp")
	case "prettier":
		return !containsArg(args, "--plugin")
	case "tail":
		return !containsArg(args, "-f", "-F") &&
			!containsLongOptionPrefix(args, "--follow", "--retry") &&
			!containsClusteredShortOption(args, "fF", "")
	case "tsc":
		return autoScopedTSCCommandAllowed(args)
	case "golangci-lint":
		return autoScopedGolangCILintCommandAllowed(args)
	case "gofmt", "staticcheck",
		"pwd", "cat", "head", "uniq", "cut", "tr", "stat", "which", "basename", "dirname", "realpath", "printf", "echo", "true", "false", "test", "cmp":
		return true
	default:
		return false
	}
}

func autoCommandNonPathArgument(executable string, args []string, index int) bool {
	if index < 0 || index >= len(args) {
		return false
	}
	if executable == "go" && len(args) > 0 && args[0] == "test" {
		argument := args[index]
		if strings.HasPrefix(argument, "-run=") || strings.HasPrefix(argument, "--run=") {
			return true
		}
		return index > 0 && stringIn(args[index-1], "-run", "--run")
	}
	if executable == "rg" || executable == "grep" {
		return autoSearchPatternArgument(args, index, executable == "rg")
	}
	return false
}

// autoSearchPatternArgument distinguishes regex text from filesystem operands
// for rg/grep. A leading slash in a pattern is data, not an external read; file
// operands and path-taking options still pass through workspace resolution.
func autoSearchPatternArgument(args []string, target int, ripgrep bool) bool {
	// GNU/BSD option parsing permits pattern options after positional operands.
	// If -e/-f (or rg --files mode) exists, every bare positional is a file path;
	// only an explicit -e value is inline pattern data.
	patternSeen := searchHasNoPositionalPattern(args, ripgrep)
	endOptions := false
	nextValue := byte(0) // 'p' pattern, 'o' other option value
	for index, argument := range args {
		if nextValue != 0 {
			if nextValue == 'p' {
				if index == target {
					return true
				}
				patternSeen = true
			}
			nextValue = 0
			continue
		}
		if !endOptions && argument == "--" {
			endOptions = true
			continue
		}
		if !endOptions && strings.HasPrefix(argument, "--") {
			name, _, attached := strings.Cut(argument, "=")
			if name == "--regexp" {
				if attached {
					if index == target {
						return true
					}
					patternSeen = true
				} else {
					nextValue = 'p'
				}
				continue
			}
			if !attached && searchLongOptionTakesValue(name, ripgrep) {
				nextValue = 'o'
			}
			continue
		}
		if !endOptions && len(argument) > 1 && argument[0] == '-' {
			cluster := argument[1:]
			for offset, option := range cluster {
				if option == 'e' {
					if offset+1 < len(cluster) {
						if index == target {
							return true
						}
						patternSeen = true
					} else {
						nextValue = 'p'
					}
					break
				}
				if searchShortOptionTakesValue(option, ripgrep) {
					if offset+1 == len(cluster) {
						nextValue = 'o'
					}
					break
				}
			}
			continue
		}
		if !patternSeen {
			if index == target {
				return true
			}
			patternSeen = true
		}
	}
	return false
}

func searchHasNoPositionalPattern(args []string, ripgrep bool) bool {
	endOptions := false
	skipNext := false
	for _, argument := range args {
		if skipNext {
			skipNext = false
			continue
		}
		if !endOptions && argument == "--" {
			endOptions = true
			continue
		}
		if endOptions {
			continue
		}
		if strings.HasPrefix(argument, "--") {
			name, _, attached := strings.Cut(argument, "=")
			if name == "--regexp" || name == "--file" || ripgrep && name == "--files" {
				return true
			}
			if !attached && searchLongOptionTakesValue(name, ripgrep) {
				skipNext = true
			}
			continue
		}
		if len(argument) <= 1 || argument[0] != '-' {
			continue
		}
		cluster := argument[1:]
		for offset, option := range cluster {
			if option == 'e' || option == 'f' {
				return true
			}
			if searchShortOptionTakesValue(option, ripgrep) {
				if offset+1 == len(cluster) {
					skipNext = true
				}
				break
			}
		}
	}
	return false
}

func searchShortOptionTakesValue(option rune, ripgrep bool) bool {
	if ripgrep {
		return strings.ContainsRune("ABCEMTdfgjmrt", option)
	}
	return strings.ContainsRune("ABCDdfm", option)
}

func searchLongOptionTakesValue(name string, ripgrep bool) bool {
	common := []string{"--after-context", "--before-context", "--context", "--regexp", "--file", "--max-count"}
	if stringIn(name, common...) {
		return true
	}
	if ripgrep {
		return stringIn(name,
			"--encoding", "--max-columns", "--type-not", "--max-depth", "--glob", "--threads",
			"--replace", "--type", "--ignore-file", "--sort", "--sortr", "--path-separator",
			"--field-context-separator", "--field-match-separator", "--engine", "--colors",
			"--hyperlink-format", "--max-filesize",
		)
	}
	return stringIn(name,
		"--devices", "--directories", "--exclude", "--exclude-from", "--include", "--label", "--binary-files",
	)
}

func (a *Agent) autoCommandExecutableAllowed(executable string) bool {
	if stringIn(executable, "cd", "echo", "false", "printf", "pwd", "test", "true") {
		return true
	}
	resolved, err := exec.LookPath(executable)
	if err != nil || !filepath.IsAbs(resolved) {
		return false
	}
	// Reject both a lexical workspace executable and an external symlink whose
	// physical target is inside the workspace. The shell may still prefer a
	// builtin for names above, but every catalogued external command must resolve
	// through a host path the agent cannot create with confined writes.
	if root := strings.TrimSpace(a.activeWorkDir()); root != "" {
		absoluteRoot, rootErr := filepath.Abs(root)
		absoluteResolved, resolvedErr := filepath.Abs(resolved)
		if rootErr != nil || resolvedErr != nil {
			return false
		}
		if _, inside, relativeErr := workspaceRelative(absoluteRoot, absoluteResolved); relativeErr != nil || inside {
			return false
		}
	}
	return !a.pathWithinWorkspace(resolved)
}

func (a *Agent) autoScopedAttachedPathOptionsAllowed(executable string, args []string, baseDir string) bool {
	var options []string
	switch executable {
	case "find", "make", "just", "rg", "grep":
		options = []string{"-f"}
	case "file":
		options = []string{"-f", "-m"}
	case "touch":
		options = []string{"-r"}
	case "task":
		options = []string{"-t", "-d"}
	case "sort":
		options = []string{"-o", "-T"}
	case "tree":
		options = []string{"-o"}
	case "golangci-lint":
		options = []string{"-c"}
	case "eslint":
		options = []string{"-c", "-o"}
	case "tsc":
		options = []string{"-p"}
	}
	if executable == "make" {
		options = append(options, "-C", "-I")
	}
	if executable == "just" {
		options = append(options, "-d")
	}
	for _, argument := range args {
		value, attached := clusteredShortOptionValue(argument, options)
		if attached && !a.autoCommandPathAllowed(value, baseDir) {
			return false
		}
	}
	return true
}

func clusteredShortOptionValue(argument string, options []string) (string, bool) {
	if len(argument) < 3 || argument[0] != '-' || argument[1] == '-' {
		return "", false
	}
	cluster := argument[1:]
	for index := range cluster {
		option := "-" + string(cluster[index])
		if !stringIn(option, options...) {
			continue
		}
		value := strings.TrimPrefix(cluster[index+1:], "=")
		if value == "" {
			return "", false
		}
		return value, true
	}
	return "", false
}

func autoScopedPackageCommandAllowed(args []string, direct ...string) bool {
	return len(args) == 1 && firstArgIn(args, direct...)
}

func autoScopedGoCommandAllowed(args []string) bool {
	if !firstArgIn(args, "build", "test", "vet", "list", "env", "version", "doc", "fmt") &&
		(len(args) < 2 || args[0] != "mod" || !stringIn(args[1], "tidy", "verify", "why", "graph")) {
		return false
	}
	// These options either mutate user-global Go configuration or delegate to
	// another executable. They need an explicit decision even when the outer Go
	// command itself is routine.
	return !containsArgumentNamePrefix(args, "-test.fuzz", "--test.fuzz") && !containsArg(args,
		"-w", "-u", "-exec", "--exec", "-toolexec", "--toolexec", "-vettool", "--vettool",
		"-ldflags", "--ldflags", "-gccgoflags", "--gccgoflags", "-gcflags", "--gcflags", "-asmflags", "--asmflags",
		"-fuzz", "--fuzz",
	)
}

func autoScopedCargoCommandAllowed(args []string) bool {
	if !firstArgIn(args, "build", "test", "check", "fmt", "clippy", "metadata", "doc", "bench") {
		return false
	}
	// Cargo configuration can select runner/compiler wrapper executables; doc
	// --open launches an external application. Both exceed routine build/test
	// authority even when the primary Cargo subcommand is catalogued.
	return !containsArg(args, "--config", "--open")
}

func autoScopedSwiftCommandAllowed(args []string) bool {
	if !firstArgIn(args, "build", "test") || containsResponseFileOperand(args) {
		return false
	}
	return !containsArg(args, "--disable-sandbox", "-Xcc", "-Xswiftc", "-Xlinker", "-Xcxx")
}

func autoScopedTSCCommandAllowed(args []string) bool {
	return !containsResponseFileOperand(args) &&
		!containsArg(args, "-w", "--watch", "--clean", "--typeRoots", "--rootDirs") &&
		!containsClusteredShortOption(args, "w", "")
}

func autoScopedGolangCILintCommandAllowed(args []string) bool {
	if firstArgIn(args, "run", "fmt", "linters", "version") {
		return true
	}
	return len(args) == 2 && args[0] == "config" && args[1] == "verify"
}

func autoScopedSedCommandAllowed(args []string) bool {
	if len(args) < 2 {
		return false
	}
	for len(args) > 0 && stringIn(args[0], "-n", "--quiet", "--silent", "-E", "-r") {
		args = args[1:]
	}
	if len(args) < 2 {
		return false
	}
	// Admit the common read-only inspection form (`sed -n 1,120p file`).
	// General sed programs can use `w` to create files and some variants can
	// execute commands, so they remain approval-gated. GNU getopt may permute a
	// later `-i` or `-e` ahead of the program; reject every post-program option
	// unless an explicit `--` makes the remaining words unambiguous filenames.
	if !autoSedPrintProgram.MatchString(args[0]) {
		return false
	}
	files := args[1:]
	if len(files) > 0 && files[0] == "--" {
		files = files[1:]
		return len(files) > 0
	}
	if len(files) == 0 {
		return false
	}
	for _, file := range files {
		if strings.HasPrefix(file, "-") {
			return false
		}
	}
	return true
}

func autoCommandAssignmentAllowed(assignment string) bool {
	if !autoCommandAssignment.MatchString(assignment) {
		return false
	}
	name, value, _ := strings.Cut(assignment, "=")
	return stringIn(name, "CI", "NO_COLOR", "FORCE_COLOR") && autoCommandAssignmentValue.MatchString(value)
}

func (a *Agent) autoCommandPathAllowed(word, baseDir string) bool {
	allowed := func(candidate string) bool {
		return a.autoCommandCandidatePathAllowed(candidate, baseDir)
	}
	if !allowed(word) {
		return false
	}
	if _, value, found := strings.Cut(word, "="); found {
		return allowed(value)
	}
	return true
}

func (a *Agent) autoCommandCandidatePathAllowed(candidate, baseDir string) bool {
	if candidate == "" || candidate == "/dev/null" {
		return true
	}
	if strings.HasPrefix(candidate, "~") || rawPathHasParentTraversal(candidate) {
		return false
	}
	if !filepath.IsAbs(candidate) && baseDir != "" {
		candidate = filepath.Join(baseDir, candidate)
	}
	// Resolve even relative operands so a workspace symlink cannot turn an
	// apparently confined shell command into an external read or write.
	return a.pathWithinWorkspace(candidate)
}

func rawPathHasParentTraversal(path string) bool {
	for _, component := range strings.FieldsFunc(path, func(character rune) bool {
		return character == '/' || character == '\\'
	}) {
		if component == ".." {
			return true
		}
	}
	return false
}

func (a *Agent) pathWithinWorkspace(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	_, err := a.resolvePath(path)
	return err == nil
}

func firstArgIn(args []string, allowed ...string) bool {
	return len(args) > 0 && stringIn(args[0], allowed...)
}

func stringIn(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func containsArg(args []string, denied ...string) bool {
	for _, arg := range args {
		for _, candidate := range denied {
			if arg == candidate || strings.HasPrefix(arg, candidate+"=") {
				return true
			}
		}
	}
	return false
}

// containsLongOptionPrefix fails closed for GNU-style unique long-option
// abbreviations. Several catalogued tools accept prefixes such as
// --files0-fro for --files0-from, so exact string matching is insufficient.
func containsLongOptionPrefix(args []string, denied ...string) bool {
	for _, argument := range args {
		name, _, _ := strings.Cut(argument, "=")
		if !strings.HasPrefix(name, "--") || name == "--" {
			continue
		}
		for _, full := range denied {
			if strings.HasPrefix(full, name) {
				return true
			}
		}
	}
	return false
}

func containsArgumentNamePrefix(args []string, denied ...string) bool {
	for _, argument := range args {
		name, _, _ := strings.Cut(argument, "=")
		for _, prefix := range denied {
			if strings.HasPrefix(name, prefix) {
				return true
			}
		}
	}
	return false
}

func containsResponseFileOperand(args []string) bool {
	for _, argument := range args {
		if strings.HasPrefix(argument, "@") {
			return true
		}
	}
	return false
}

func argumentContainsPath(args []string, path string) bool {
	for _, argument := range args {
		if strings.Contains(argument, path) {
			return true
		}
	}
	return false
}

// containsClusteredShortOption finds denied flags inside POSIX short-option
// clusters (for example, `-bz` and `-Pz`). Once an option that consumes the
// rest of its token is encountered, later bytes are its value rather than
// flags and are deliberately ignored.
func containsClusteredShortOption(args []string, denied, valueTaking string) bool {
	for _, argument := range args {
		if argument == "--" {
			return false
		}
		if len(argument) < 3 || argument[0] != '-' || argument[1] == '-' {
			continue
		}
		for _, option := range argument[1:] {
			if strings.ContainsRune(denied, option) {
				return true
			}
			if strings.ContainsRune(valueTaking, option) {
				break
			}
		}
	}
	return false
}
