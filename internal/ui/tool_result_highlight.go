package ui

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"charm.land/lipgloss/v2"
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
)

const (
	maxToolResultPreviewLines = 80
	maxToolResultRenderCache  = 128
)

type toolResultRenderKind uint8

const (
	toolResultPlain toolResultRenderKind = iota
	toolResultCode
	toolResultSearch
)

type toolResultRenderCacheKey struct {
	Digest   [sha256.Size]byte
	Kind     toolResultRenderKind
	Language string
	Width    int
	Dark     bool
	NoColor  bool
	// Display marks the ANSI-16 remapped variant so a cached plain render can
	// never be served for the colored body or vice versa.
	Display bool
}

// Tool result highlighting is an ephemeral presentation cache. Keys contain a
// digest rather than result text, values are derived only after terminal
// sanitization, and the hard entry ceiling prevents a long session from
// retaining an unbounded amount of output.
var semanticToolResultCache = struct {
	sync.RWMutex
	entries map[toolResultRenderCacheKey][]string
}{entries: make(map[toolResultRenderCacheKey][]string)}

var searchResultLocation = regexp.MustCompile(`^(.+?):([0-9]+)(?::([0-9]+))?:(.*)$`)

var trustedResultLanguagesByExtension = map[string]string{
	".bash": "bash", ".sh": "bash", ".zsh": "bash",
	".c": "c", ".h": "c", ".cc": "cpp", ".cpp": "cpp", ".cxx": "cpp", ".hpp": "cpp",
	".css": "css", ".scss": "scss",
	".go": "go", ".graphql": "graphql", ".gql": "graphql",
	".html": "html", ".htm": "html",
	".java": "java", ".js": "javascript", ".jsx": "jsx",
	".json": "json", ".jsonl": "json",
	".kt": "kotlin", ".kts": "kotlin",
	".lua": "lua", ".md": "markdown", ".mdx": "markdown",
	".php": "php", ".py": "python", ".rb": "ruby", ".rs": "rust",
	".sql": "sql", ".swift": "swift",
	".toml": "toml", ".ts": "typescript", ".tsx": "tsx",
	".xml": "xml", ".yaml": "yaml", ".yml": "yaml",
}

var trustedResultLanguages = func() map[string]struct{} {
	result := make(map[string]struct{}, len(trustedResultLanguagesByExtension))
	for _, language := range trustedResultLanguagesByExtension {
		result[language] = struct{}{}
	}
	return result
}()

// resultRenderKind classifies the result body exactly as the semantic render
// path does; only the plain kind may use the ANSI-16 display variant.
func (c ToolCard) resultRenderKind() toolResultRenderKind {
	switch {
	case c.Kind == ToolCardSearch:
		return toolResultSearch
	case c.Kind == ToolCardFile && normalizeTrustedResultLanguage(c.ResultLanguage) != "":
		return toolResultCode
	default:
		return toolResultPlain
	}
}

// remappedDisplayResultLines regenerates the ANSI-16 display variant of a
// plain result body from the raw bytes retained at intake. It returns nil
// when no display variant applies so callers fall back to the sanitized
// plain rendering.
func (c ToolCard) remappedDisplayResultLines(width int) []string {
	if noColor || width <= 0 || c.resultRenderKind() != toolResultPlain {
		return nil
	}
	display := strings.TrimRight(c.ResultDisplay, "\n")
	if display == "" {
		return nil
	}
	all := strings.Split(display, "\n")
	hidden := 0
	if len(all) > maxToolResultPreviewLines {
		hidden = len(all) - maxToolResultPreviewLines
		all = all[:maxToolResultPreviewLines]
	}
	palette := newSemanticPalette(c.IsDark)
	rendered := make([]string, 0, len(all)+1)
	for _, line := range all {
		rendered = append(rendered, remapANSI16Line(line, palette, width))
	}
	if hidden > 0 {
		rendered = append(rendered, c.Styles.Dimmed.Render(
			truncateDisplay(fmt.Sprintf("… %d more lines", hidden), width),
		))
	}
	return rendered
}

func (c ToolCard) renderSemanticResultLines(result string, width int) []string {
	if width <= 0 || strings.TrimSpace(result) == "" {
		return nil
	}

	language := normalizeTrustedResultLanguage(c.ResultLanguage)
	kind := c.resultRenderKind()
	displayVariant := kind == toolResultPlain && !noColor && c.ResultDisplay != ""
	key := toolResultRenderCacheKey{
		Digest:   sha256.Sum256([]byte(result)),
		Kind:     kind,
		Language: language,
		Width:    width,
		Dark:     c.IsDark,
		NoColor:  noColor,
		Display:  displayVariant,
	}
	if displayVariant {
		key.Digest = sha256.Sum256([]byte(c.ResultDisplay))
	}
	if cached, ok := cachedSemanticToolResult(key); ok {
		return cached
	}

	var rendered []string
	if displayVariant {
		rendered = c.remappedDisplayResultLines(width)
	}
	if len(rendered) == 0 {
		plainLines, hidden := boundedToolResultPreview(result, width)
		rendered = make([]string, 0, len(plainLines)+1)
		switch kind {
		case toolResultCode:
			if !noColor {
				if highlighted, ok := highlightToolCode(plainLines, language, c.IsDark); ok {
					rendered = highlighted
				}
			}
		case toolResultSearch:
			for _, line := range plainLines {
				rendered = append(rendered, c.renderSearchResultLine(line, width))
			}
		}
		if len(rendered) == 0 {
			for _, line := range plainLines {
				rendered = append(rendered, c.Styles.Result.Render(line))
			}
		}
		if hidden > 0 {
			rendered = append(rendered, c.Styles.Dimmed.Render(
				truncateDisplay(fmt.Sprintf("… %d more lines", hidden), width),
			))
		}
	}
	cacheSemanticToolResult(key, rendered)
	return append([]string(nil), rendered...)
}

func boundedToolResultPreview(result string, width int) ([]string, int) {
	result = strings.TrimRight(sanitizeTerminalMultiline(result), "\n")
	if result == "" {
		return nil, 0
	}
	all := strings.Split(result, "\n")
	hidden := 0
	if len(all) > maxToolResultPreviewLines {
		hidden = len(all) - maxToolResultPreviewLines
		all = all[:maxToolResultPreviewLines]
	}
	lines := make([]string, len(all))
	for i, line := range all {
		// A literal tab advances to terminal-dependent stops and can visually
		// escape an otherwise exact width. Four spaces keep code structure clear
		// and make the width contract deterministic.
		line = strings.ReplaceAll(sanitizeTerminalLine(line), "\t", "    ")
		lines[i] = truncateDisplay(line, width)
	}
	return lines, hidden
}

func highlightToolCode(lines []string, language string, isDark bool) ([]string, bool) {
	lexer := lexers.Get(language)
	if lexer == nil || lexer == lexers.Fallback {
		return nil, false
	}
	iterator, err := chroma.Coalesce(lexer).Tokenise(nil, strings.Join(lines, "\n"))
	if err != nil {
		return nil, false
	}
	var output bytes.Buffer
	if err := formatters.TTY16m.Format(&output, toolCodeStyle(isDark), iterator); err != nil {
		return nil, false
	}
	highlighted := strings.Split(strings.TrimSuffix(output.String(), "\n"), "\n")
	if len(highlighted) != len(lines) {
		return nil, false
	}
	return highlighted, true
}

func toolCodeStyle(isDark bool) *chroma.Style {
	palette := newSemanticPalette(isDark)
	text := colorHex(palette.Text)
	muted := colorHex(palette.Muted)
	dim := colorHex(palette.Dim)
	accent := colorHex(palette.Accent)
	accent2 := colorHex(palette.Accent2)
	errorColor := colorHex(palette.Error)
	success := colorHex(palette.Success)
	special := colorHex(palette.Special)
	warning := colorHex(palette.Warning)

	// This is the same LightDark-derived Nord vocabulary as the rest of Local
	// Agent. There is deliberately no background token: the user's terminal
	// remains responsible for its own background and contrast mode.
	return chroma.MustNewStyle("local-agent-nord", chroma.StyleEntries{
		chroma.Background:          text,
		chroma.Text:                text,
		chroma.TextWhitespace:      muted,
		chroma.Error:               errorColor,
		chroma.Keyword:             "bold " + accent2,
		chroma.KeywordPseudo:       accent2,
		chroma.KeywordType:         accent2,
		chroma.Name:                text,
		chroma.NameAttribute:       accent,
		chroma.NameBuiltin:         accent2,
		chroma.NameClass:           accent,
		chroma.NameConstant:        special,
		chroma.NameDecorator:       warning,
		chroma.NameException:       errorColor,
		chroma.NameFunction:        accent,
		chroma.NameLabel:           accent,
		chroma.NameNamespace:       accent,
		chroma.NameProperty:        accent,
		chroma.NameTag:             accent2,
		chroma.NameVariable:        text,
		chroma.LiteralString:       success,
		chroma.LiteralStringDoc:    "italic " + dim,
		chroma.LiteralStringEscape: warning,
		chroma.LiteralNumber:       special,
		chroma.Operator:            accent2,
		chroma.OperatorWord:        "bold " + accent2,
		chroma.Punctuation:         text,
		chroma.Comment:             "italic " + dim,
		chroma.CommentPreproc:      accent2,
		chroma.GenericDeleted:      errorColor,
		chroma.GenericError:        errorColor,
		chroma.GenericHeading:      "bold " + accent,
		chroma.GenericInserted:     success,
		chroma.GenericOutput:       muted,
		chroma.GenericPrompt:       "bold " + dim,
		chroma.GenericSubheading:   "bold " + accent,
		chroma.GenericTraceback:    errorColor,
	})
}

func (c ToolCard) renderSearchResultLine(line string, width int) string {
	match := searchResultLocation.FindStringSubmatch(line)
	if len(match) == 0 {
		if looksLikeSearchResultPath(line) {
			return c.Styles.SearchPath.Render(line)
		}
		return c.Styles.Result.Render(line)
	}
	path := match[1]
	location := match[2]
	if match[3] != "" {
		location += ":" + match[3]
	}
	prefixWidth := lipgloss.Width(path) + 1 + lipgloss.Width(location) + 1
	if prefixWidth >= width {
		return c.Styles.SearchPath.Render(truncateDisplay(line, width))
	}
	body := truncateDisplay(match[4], max(1, width-prefixWidth))
	return c.Styles.SearchPath.Render(path) +
		c.Styles.SearchLocation.Render(":"+location+":") +
		c.Styles.SearchMatch.Render(body)
}

func looksLikeSearchResultPath(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" || strings.ContainsAny(line, " \t") {
		return false
	}
	return trustedResultLanguageFromPath(line) != ""
}

func cachedSemanticToolResult(key toolResultRenderCacheKey) ([]string, bool) {
	semanticToolResultCache.RLock()
	value, ok := semanticToolResultCache.entries[key]
	semanticToolResultCache.RUnlock()
	if !ok {
		return nil, false
	}
	return append([]string(nil), value...), true
}

func cacheSemanticToolResult(key toolResultRenderCacheKey, value []string) {
	semanticToolResultCache.Lock()
	if len(semanticToolResultCache.entries) >= maxToolResultRenderCache {
		clear(semanticToolResultCache.entries)
	}
	semanticToolResultCache.entries[key] = append([]string(nil), value...)
	semanticToolResultCache.Unlock()
}

func trustedResultLanguageForTool(name string, args map[string]any) string {
	if classifyTool(name) != ToolTypeFileRead {
		return ""
	}
	for _, key := range []string{"path", "file_path", "filename", "file"} {
		if path, ok := args[key].(string); ok {
			return trustedResultLanguageFromPath(path)
		}
	}
	return ""
}

func trustedResultLanguageFromPath(path string) string {
	extension := strings.ToLower(filepath.Ext(strings.TrimSpace(path)))
	return trustedResultLanguagesByExtension[extension]
}

func normalizeTrustedResultLanguage(language string) string {
	language = strings.ToLower(strings.TrimSpace(language))
	if _, ok := trustedResultLanguages[language]; !ok {
		return ""
	}
	return language
}
