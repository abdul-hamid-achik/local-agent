package tools

import "github.com/abdul-hamid-achik/local-agent/internal/llm"

func GrepToolDef() llm.ToolDef {
	return llm.ToolDef{
		Name:        "grep",
		Description: "Search for a pattern in files. Use this to find code, text, or values across multiple files.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{
					"type":        "string",
					"description": "The regex pattern to search for.",
				},
				"path": map[string]any{
					"type":        "string",
					"description": "Directory path to search in (defaults to current directory).",
				},
				"include": map[string]any{
					"type":        "string",
					"description": "File pattern to include (e.g., '*.go', '*.ts').",
				},
				"context": map[string]any{
					"type":        "integer",
					"description": "Number of lines of context to show around matches (default: 3).",
				},
			},
			"required": []string{"pattern"},
		},
	}
}

func ReadToolDef() llm.ToolDef {
	return llm.ToolDef{
		Name:        "read",
		Description: "Read the contents of a file. Use this to view source code, configuration files, or any text file.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Path to the file to read.",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum number of lines to read (optional).",
				},
				"offset": map[string]any{
					"type":        "integer",
					"description": "Line number to start reading from (optional, 1-indexed).",
				},
			},
			"required": []string{"path"},
		},
	}
}

func WriteToolDef() llm.ToolDef {
	return llm.ToolDef{
		Name:        "write",
		Description: "Write content to a file. Use this to create new files or overwrite existing ones. Creates parent directories if needed.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Path to the file to write.",
				},
				"content": map[string]any{
					"type":        "string",
					"description": "Content to write to the file.",
				},
			},
			"required": []string{"path", "content"},
		},
	}
}

func GlobToolDef() llm.ToolDef {
	return llm.ToolDef{
		Name:        "glob",
		Description: "Find files matching a pattern. Use this to discover files by name patterns like '*.go', '**/*.ts', etc.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{
					"type":        "string",
					"description": "Glob pattern to match (e.g., '**/*.go', 'src/**/*.ts').",
				},
				"path": map[string]any{
					"type":        "string",
					"description": "Directory to search in (defaults to current directory).",
				},
			},
			"required": []string{"pattern"},
		},
	}
}

func BashToolDef() llm.ToolDef {
	return llm.ToolDef{
		Name:        "bash",
		Description: "Execute a shell command. Use this to run git, npm, go, or other command-line tools. Output is returned after completion.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "The shell command to execute.",
				},
				"timeout": map[string]any{
					"type":        "integer",
					"description": "Timeout in seconds (default: 30, max: 120).",
				},
			},
			"required": []string{"command"},
		},
	}
}

func LsToolDef() llm.ToolDef {
	return llm.ToolDef{
		Name:        "ls",
		Description: "List files and directories. Use this to see what's in a directory.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Directory path to list (defaults to current directory).",
				},
			},
		},
	}
}

func FindToolDef() llm.ToolDef {
	return llm.ToolDef{
		Name:        "find",
		Description: "Find files or directories by name. Use this to locate specific files when you know all or part of the filename.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "Name or pattern to search for (supports * and ? wildcards).",
				},
				"path": map[string]any{
					"type":        "string",
					"description": "Directory to search in (defaults to current directory).",
				},
				"type": map[string]any{
					"type":        "string",
					"description": "Type to find: 'f' for files, 'd' for directories (default: both).",
				},
			},
			"required": []string{"name"},
		},
	}
}

func DiffToolDef() llm.ToolDef {
	return llm.ToolDef{
		Name:        "diff",
		Description: "Show the differences between the current file content and new content. Use this to preview changes before writing.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Path to the file to diff.",
				},
				"new_content": map[string]any{
					"type":        "string",
					"description": "The new content to compare against the current file.",
				},
			},
			"required": []string{"path", "new_content"},
		},
	}
}

func EditToolDef() llm.ToolDef {
	return llm.ToolDef{
		Name:        "edit",
		Description: "Apply a patch to a file. Use this to make targeted edits to specific lines without overwriting the entire file. The patch format is: @@ -start,count +new_start,new_count @@\nfollowed by lines starting with - (remove), + (add), or (context).",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Path to the file to edit.",
				},
				"patch": map[string]any{
					"type":        "string",
					"description": "Unified diff patch to apply. Format: @@ -start,count +new_start,new_count @@ followed by -line (remove), +line (add), or context line.",
				},
			},
			"required": []string{"path", "patch"},
		},
	}
}

func MkdirToolDef() llm.ToolDef {
	return llm.ToolDef{
		Name:        "mkdir",
		Description: "Create one or more directories. Creates parent directories as needed.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Path to the directory to create.",
				},
			},
			"required": []string{"path"},
		},
	}
}

func RemoveToolDef() llm.ToolDef {
	return llm.ToolDef{
		Name:        "remove",
		Description: "Remove files or directories. Use with caution - this permanently deletes files.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Path to remove (file or directory).",
				},
				"recursive": map[string]any{
					"type":        "boolean",
					"description": "Remove directories recursively (default: false).",
				},
				"force": map[string]any{
					"type":        "boolean",
					"description": "Ignore nonexistent files (default: false).",
				},
			},
			"required": []string{"path"},
		},
	}
}

func CopyToolDef() llm.ToolDef {
	return llm.ToolDef{
		Name:        "copy",
		Description: "Copy a file from source to destination.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"source": map[string]any{
					"type":        "string",
					"description": "Source path to copy from.",
				},
				"destination": map[string]any{
					"type":        "string",
					"description": "Destination path to copy to.",
				},
			},
			"required": []string{"source", "destination"},
		},
	}
}

func MoveToolDef() llm.ToolDef {
	return llm.ToolDef{
		Name:        "move",
		Description: "Move or rename a file or directory.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"source": map[string]any{
					"type":        "string",
					"description": "Source path to move from.",
				},
				"destination": map[string]any{
					"type":        "string",
					"description": "Destination path to move to.",
				},
			},
			"required": []string{"source", "destination"},
		},
	}
}

func ExistsToolDef() llm.ToolDef {
	return llm.ToolDef{
		Name:        "exists",
		Description: "Check if a file or directory exists and get information about it.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Path to check.",
				},
			},
			"required": []string{"path"},
		},
	}
}
