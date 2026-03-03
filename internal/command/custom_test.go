package command

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseCustomCommand(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantOK  bool
		wantCmd CustomCommand
	}{
		{
			name: "valid command",
			content: `---
name: review
description: Code review prompt
---
Review this code: {{input}}`,
			wantOK: true,
			wantCmd: CustomCommand{
				Name:        "review",
				Description: "Code review prompt",
				Template:    "Review this code: {{input}}",
			},
		},
		{
			name: "no description",
			content: `---
name: explain
---
Explain this: {{input}}`,
			wantOK: true,
			wantCmd: CustomCommand{
				Name:     "explain",
				Template: "Explain this: {{input}}",
			},
		},
		{
			name:    "no frontmatter",
			content: "just some text",
			wantOK:  false,
		},
		{
			name: "no name",
			content: `---
description: something
---
body`,
			wantOK: false,
		},
		{
			name: "no body",
			content: `---
name: empty
---`,
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, ok := parseCustomCommand(tt.content)
			if ok != tt.wantOK {
				t.Fatalf("parseCustomCommand() ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if cmd.Name != tt.wantCmd.Name {
				t.Errorf("Name = %q, want %q", cmd.Name, tt.wantCmd.Name)
			}
			if cmd.Description != tt.wantCmd.Description {
				t.Errorf("Description = %q, want %q", cmd.Description, tt.wantCmd.Description)
			}
			if cmd.Template != tt.wantCmd.Template {
				t.Errorf("Template = %q, want %q", cmd.Template, tt.wantCmd.Template)
			}
		})
	}
}

func TestLoadCustomCommands(t *testing.T) {
	dir := t.TempDir()

	// Write a valid command file.
	err := os.WriteFile(filepath.Join(dir, "review.md"), []byte(`---
name: review
description: Review code
---
Review: {{input}}`), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	// Write an invalid file (no frontmatter).
	err = os.WriteFile(filepath.Join(dir, "invalid.md"), []byte("just text"), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	// Write a non-md file (should be ignored).
	err = os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("not a command"), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	cmds := LoadCustomCommands(dir)
	if len(cmds) != 1 {
		t.Fatalf("LoadCustomCommands() returned %d commands, want 1", len(cmds))
	}
	if cmds[0].Name != "review" {
		t.Errorf("Name = %q, want %q", cmds[0].Name, "review")
	}
}

func TestLoadCustomCommands_MissingDir(t *testing.T) {
	cmds := LoadCustomCommands("/nonexistent/path")
	if len(cmds) != 0 {
		t.Errorf("expected empty result for missing dir, got %d", len(cmds))
	}
}

func TestRegisterCustomCommands(t *testing.T) {
	dir := t.TempDir()
	err := os.WriteFile(filepath.Join(dir, "test.md"), []byte(`---
name: testcmd
description: A test command
---
Do this: {{input}}`), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	reg := NewRegistry()
	RegisterCustomCommands(reg, dir)

	result := reg.Execute(&Context{}, "testcmd", []string{"hello", "world"})
	if result.Action != ActionSendPrompt {
		t.Errorf("Action = %v, want ActionSendPrompt", result.Action)
	}
	if result.Data != "Do this: hello world" {
		t.Errorf("Data = %q, want %q", result.Data, "Do this: hello world")
	}
}
