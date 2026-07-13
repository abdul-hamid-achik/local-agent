package ui

import (
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
)

const maxToolPresentationWidth = 48

type toolActionLabels struct {
	running string
	success string
	failure string
}

type toolPresentation struct {
	label string
	raw   string
}

func (p toolPresentation) differsFromRaw() bool {
	return p.raw != "" && p.label != p.raw
}

var toolActionRegistry = map[string]toolActionLabels{
	"read":               {running: "Reading", success: "Read", failure: "Read failed"},
	"read_file":          {running: "Reading", success: "Read", failure: "Read failed"},
	"read_minified_file": {running: "Reading", success: "Read", failure: "Read failed"},
	"view":               {running: "Reading", success: "Read", failure: "Read failed"},
	"cat":                {running: "Reading", success: "Read", failure: "Read failed"},
	"write":              {running: "Writing", success: "Wrote", failure: "Write failed"},
	"write_file":         {running: "Writing", success: "Wrote", failure: "Write failed"},
	"create_file":        {running: "Creating", success: "Created", failure: "Create failed"},
	"edit":               {running: "Editing", success: "Edited", failure: "Edit failed"},
	"edit_file":          {running: "Editing", success: "Edited", failure: "Edit failed"},
	"apply_patch":        {running: "Patching", success: "Patched", failure: "Patch failed"},
	"patch":              {running: "Patching", success: "Patched", failure: "Patch failed"},
	"bash":               {running: "Running", success: "Ran", failure: "Run failed"},
	"exec":               {running: "Running", success: "Ran", failure: "Run failed"},
	"exec_command":       {running: "Running", success: "Ran", failure: "Run failed"},
	"shell":              {running: "Running", success: "Ran", failure: "Run failed"},
	"command":            {running: "Running", success: "Ran", failure: "Run failed"},
	"grep":               {running: "Searching", success: "Searched", failure: "Search failed"},
	"search":             {running: "Searching", success: "Searched", failure: "Search failed"},
	"search_files":       {running: "Searching", success: "Searched", failure: "Search failed"},
	"web_search":         {running: "Searching web", success: "Searched web", failure: "Web search failed"},
	"glob":               {running: "Finding", success: "Found", failure: "Find failed"},
	"find":               {running: "Finding", success: "Found", failure: "Find failed"},
	"ls":                 {running: "Listing", success: "Listed", failure: "List failed"},
	"list":               {running: "Listing", success: "Listed", failure: "List failed"},
	"list_directory":     {running: "Listing", success: "Listed", failure: "List failed"},
	"diff":               {running: "Comparing", success: "Compared", failure: "Compare failed"},
	"mkdir":              {running: "Creating directory", success: "Created directory", failure: "Create directory failed"},
	"make_directory":     {running: "Creating directory", success: "Created directory", failure: "Create directory failed"},
	"remove":             {running: "Removing", success: "Removed", failure: "Remove failed"},
	"delete":             {running: "Removing", success: "Removed", failure: "Remove failed"},
	"delete_file":        {running: "Removing", success: "Removed", failure: "Remove failed"},
	"copy":               {running: "Copying", success: "Copied", failure: "Copy failed"},
	"copy_file":          {running: "Copying", success: "Copied", failure: "Copy failed"},
	"move":               {running: "Moving", success: "Moved", failure: "Move failed"},
	"move_file":          {running: "Moving", success: "Moved", failure: "Move failed"},
	"rename":             {running: "Renaming", success: "Renamed", failure: "Rename failed"},
	"exists":             {running: "Checking", success: "Checked", failure: "Check failed"},
	"file_exists":        {running: "Checking", success: "Checked", failure: "Check failed"},
	"fetch":              {running: "Fetching", success: "Fetched", failure: "Fetch failed"},
	"fetch_url":          {running: "Fetching", success: "Fetched", failure: "Fetch failed"},
	"browse":             {running: "Browsing", success: "Browsed", failure: "Browse failed"},
	"memory_save":        {running: "Saving memory", success: "Saved memory", failure: "Save memory failed"},
	"memory_recall":      {running: "Recalling memory", success: "Recalled memory", failure: "Recall memory failed"},
	"memory_delete":      {running: "Removing memory", success: "Removed memory", failure: "Remove memory failed"},
	"memory_update":      {running: "Updating memory", success: "Updated memory", failure: "Update memory failed"},
	"memory_list":        {running: "Listing memories", success: "Listed memories", failure: "List memories failed"},
	"update_plan":        {running: "Updating plan", success: "Updated plan", failure: "Update plan failed"},
	// Bob (bobcli.dev) repository-factory tools, typically namespaced through MCP.
	"bob_plan":              {running: "Planning repository", success: "Planned repository", failure: "Repository plan failed"},
	"bob_check":             {running: "Checking repository drift", success: "Checked repository drift", failure: "Drift check failed"},
	"bob_apply":             {running: "Applying repository plan", success: "Applied repository plan", failure: "Repository apply failed"},
	"bob_inspect":           {running: "Inspecting repository", success: "Inspected repository", failure: "Repository inspect failed"},
	"bob_stats":             {running: "Reading Bob stats", success: "Read Bob stats", failure: "Bob stats failed"},
	"bob_validate_manifest": {running: "Validating manifest", success: "Validated manifest", failure: "Manifest validation failed"},
	"bob_recipe_describe":   {running: "Describing recipe", success: "Described recipe", failure: "Recipe describe failed"},
	"bob_learn":             {running: "Learning Bob contract", success: "Learned Bob contract", failure: "Bob learn failed"},
	"tool_search":           {running: "Loading tools", success: "Loaded tools", failure: "Load tools failed"},
	"task":                  {running: "Running task", success: "Completed task", failure: "Task failed"},
}

var toolKindRegistry = map[ToolCardKind]toolActionLabels{
	ToolCardFile:    {running: "Accessing file", success: "Accessed file", failure: "File operation failed"},
	ToolCardBash:    {running: "Running command", success: "Ran command", failure: "Command failed"},
	ToolCardSearch:  {running: "Searching", success: "Searched", failure: "Search failed"},
	ToolCardGit:     {running: "Updating repository", success: "Updated repository", failure: "Repository operation failed"},
	ToolCardGeneric: {running: "Running tool", success: "Completed tool", failure: "Tool failed"},
}

func presentTool(name string, kind ToolCardKind, state ToolCardState) toolPresentation {
	raw := safeToolIdentifier(name)
	labels, known := registeredToolLabels(raw)
	if !known && raw == "" {
		labels = toolKindRegistry[kind]
		known = true
	}

	var label string
	if known {
		label = labels.forState(state)
	} else {
		label = unknownToolLabel(humanizeToolIdentifier(raw), kind, state)
	}
	if label == "" {
		label = toolKindRegistry[ToolCardGeneric].forState(state)
	}

	return toolPresentation{
		label: truncateDisplay(label, maxToolPresentationWidth),
		raw:   raw,
	}
}

func (l toolActionLabels) forState(state ToolCardState) string {
	switch state {
	case ToolCardSuccess:
		return l.success
	case ToolCardError:
		return l.failure
	default:
		return l.running
	}
}

func registeredToolLabels(raw string) (toolActionLabels, bool) {
	key := canonicalToolKey(raw)
	if labels, ok := toolActionRegistry[key]; ok {
		return labels, true
	}
	if namespace := strings.LastIndex(key, "__"); namespace >= 0 {
		labels, ok := toolActionRegistry[key[namespace+2:]]
		return labels, ok
	}
	return toolActionLabels{}, false
}

func canonicalToolKey(raw string) string {
	key := strings.ToLower(strings.TrimSpace(raw))
	return strings.NewReplacer("-", "_", " ", "_").Replace(key)
}

func unknownToolLabel(label string, kind ToolCardKind, state ToolCardState) string {
	if label == "" {
		return toolKindRegistry[kind].forState(state)
	}
	if state == ToolCardError {
		return label + " failed"
	}

	object := lowerInitial(label)
	switch kind {
	case ToolCardFile:
		if state == ToolCardSuccess {
			return "Accessed " + object
		}
		return "Accessing " + object
	case ToolCardBash:
		if state == ToolCardSuccess {
			return "Ran " + object
		}
		return "Running " + object
	case ToolCardSearch:
		if state == ToolCardSuccess {
			return "Searched with " + object
		}
		return "Searching with " + object
	case ToolCardGit:
		if state == ToolCardSuccess {
			return "Completed " + object
		}
		return "Running " + object
	default:
		if state == ToolCardSuccess {
			return "Completed " + object
		}
		return "Running " + object
	}
}

func safeToolIdentifier(name string) string {
	name = ansi.Strip(name)
	var b strings.Builder
	for _, r := range name {
		switch {
		case r == '\ufe0e' || r == '\ufe0f' || r == '\u20e3':
			// Drop emoji presentation selectors and keycap composition marks.
		case unicode.IsLetter(r), unicode.IsNumber(r), unicode.IsMark(r), unicode.IsSpace(r):
			b.WriteRune(r)
		case strings.ContainsRune("_-./:@+", r) || r == '\\':
			b.WriteRune(r)
		default:
			b.WriteByte(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func humanizeToolIdentifier(raw string) string {
	words := strings.FieldsFunc(raw, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r) && !unicode.IsMark(r)
	})
	if len(words) == 0 {
		return ""
	}
	label := strings.Join(words, " ")
	if label == strings.ToUpper(label) && label != strings.ToLower(label) {
		label = strings.ToLower(label)
	}
	return upperInitial(label)
}

func upperInitial(text string) string {
	runes := []rune(text)
	if len(runes) > 0 {
		runes[0] = unicode.ToUpper(runes[0])
	}
	return string(runes)
}

func lowerInitial(text string) string {
	runes := []rune(text)
	if len(runes) == 0 || firstWordIsInitialism(runes) {
		return text
	}
	runes[0] = unicode.ToLower(runes[0])
	return string(runes)
}

func firstWordIsInitialism(runes []rune) bool {
	letters := 0
	for _, r := range runes {
		if unicode.IsSpace(r) {
			break
		}
		if !unicode.IsLetter(r) {
			continue
		}
		letters++
		if !unicode.IsUpper(r) {
			return false
		}
	}
	return letters > 1
}
