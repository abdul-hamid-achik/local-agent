package tools

import (
	"testing"
)

func TestGrepToolDef(t *testing.T) {
	tool := GrepToolDef()

	if tool.Name != "grep" {
		t.Errorf("Name = %q, want %q", tool.Name, "grep")
	}
	if tool.Description == "" {
		t.Error("Description should not be empty")
	}
	if tool.Parameters == nil {
		t.Error("Parameters should not be nil")
	}
}

func TestReadToolDef(t *testing.T) {
	tool := ReadToolDef()

	if tool.Name != "read" {
		t.Errorf("Name = %q, want %q", tool.Name, "read")
	}
	props := tool.Parameters["properties"].(map[string]any)
	if _, ok := props["path"]; !ok {
		t.Error("should have path property")
	}
}

func TestWriteToolDef(t *testing.T) {
	tool := WriteToolDef()

	if tool.Name != "write" {
		t.Errorf("Name = %q, want %q", tool.Name, "write")
	}
	props := tool.Parameters["properties"].(map[string]any)
	if _, ok := props["path"]; !ok {
		t.Error("should have path property")
	}
	if _, ok := props["content"]; !ok {
		t.Error("should have content property")
	}
}

func TestGlobToolDef(t *testing.T) {
	tool := GlobToolDef()

	if tool.Name != "glob" {
		t.Errorf("Name = %q, want %q", tool.Name, "glob")
	}
}

func TestBashToolDef(t *testing.T) {
	tool := BashToolDef()

	if tool.Name != "bash" {
		t.Errorf("Name = %q, want %q", tool.Name, "bash")
	}
	props := tool.Parameters["properties"].(map[string]any)
	if _, ok := props["command"]; !ok {
		t.Error("should have command property")
	}
}

func TestLsToolDef(t *testing.T) {
	tool := LsToolDef()

	if tool.Name != "ls" {
		t.Errorf("Name = %q, want %q", tool.Name, "ls")
	}
}

func TestFindToolDef(t *testing.T) {
	tool := FindToolDef()

	if tool.Name != "find" {
		t.Errorf("Name = %q, want %q", tool.Name, "find")
	}
	props := tool.Parameters["properties"].(map[string]any)
	if _, ok := props["name"]; !ok {
		t.Error("should have name property")
	}
}

func TestDiffToolDef(t *testing.T) {
	tool := DiffToolDef()

	if tool.Name != "diff" {
		t.Errorf("Name = %q, want %q", tool.Name, "diff")
	}
}

func TestEditToolDef(t *testing.T) {
	tool := EditToolDef()

	if tool.Name != "edit" {
		t.Errorf("Name = %q, want %q", tool.Name, "edit")
	}
}

func TestMkdirToolDef(t *testing.T) {
	tool := MkdirToolDef()

	if tool.Name != "mkdir" {
		t.Errorf("Name = %q, want %q", tool.Name, "mkdir")
	}
}

func TestRemoveToolDef(t *testing.T) {
	tool := RemoveToolDef()

	if tool.Name != "remove" {
		t.Errorf("Name = %q, want %q", tool.Name, "remove")
	}
	props := tool.Parameters["properties"].(map[string]any)
	if _, ok := props["recursive"]; !ok {
		t.Error("should have recursive property")
	}
	if _, ok := props["force"]; !ok {
		t.Error("should have force property")
	}
}

func TestCopyToolDef(t *testing.T) {
	tool := CopyToolDef()

	if tool.Name != "copy" {
		t.Errorf("Name = %q, want %q", tool.Name, "copy")
	}
	props := tool.Parameters["properties"].(map[string]any)
	if _, ok := props["source"]; !ok {
		t.Error("should have source property")
	}
	if _, ok := props["destination"]; !ok {
		t.Error("should have destination property")
	}
}

func TestMoveToolDef(t *testing.T) {
	tool := MoveToolDef()

	if tool.Name != "move" {
		t.Errorf("Name = %q, want %q", tool.Name, "move")
	}
}

func TestExistsToolDef(t *testing.T) {
	tool := ExistsToolDef()

	if tool.Name != "exists" {
		t.Errorf("Name = %q, want %q", tool.Name, "exists")
	}
}
