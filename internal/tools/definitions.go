package tools

import "github.com/abdul-hamid-achik/local-agent/internal/llm"

var builtinToolNames = map[string]bool{
	"grep":   true,
	"read":   true,
	"write":  true,
	"glob":   true,
	"bash":   true,
	"ls":     true,
	"find":   true,
	"diff":   true,
	"edit":   true,
	"mkdir":  true,
	"remove": true,
	"copy":   true,
	"move":   true,
	"exists": true,
}

func AllToolDefs() []llm.ToolDef {
	return []llm.ToolDef{
		GrepToolDef(),
		ReadToolDef(),
		WriteToolDef(),
		GlobToolDef(),
		BashToolDef(),
		LsToolDef(),
		FindToolDef(),
		DiffToolDef(),
		EditToolDef(),
		MkdirToolDef(),
		RemoveToolDef(),
		CopyToolDef(),
		MoveToolDef(),
		ExistsToolDef(),
	}
}

func IsBuiltinTool(name string) bool {
	return builtinToolNames[name]
}
