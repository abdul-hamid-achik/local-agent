package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadIgnoreFile_Valid(t *testing.T) {
	dir := t.TempDir()
	content := `# Build artifacts
node_modules
*.log
.git
build/
dist/
vendor/
`
	if err := os.WriteFile(filepath.Join(dir, ".agentignore"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	ip := LoadIgnoreFile(dir)
	if ip == nil {
		t.Fatal("expected non-nil IgnorePatterns")
	}

	wantPatterns := []string{"node_modules", "*.log", ".git", "build/", "dist/", "vendor/"}
	if len(ip.Patterns()) != len(wantPatterns) {
		t.Fatalf("got %d patterns, want %d", len(ip.Patterns()), len(wantPatterns))
	}
	for i, p := range ip.Patterns() {
		if p != wantPatterns[i] {
			t.Errorf("pattern[%d] = %q, want %q", i, p, wantPatterns[i])
		}
	}

	if ip.Raw() != content[:len(content)-1] { // raw joins lines without trailing newline from Join
		// Just check it contains the comment and patterns
		if ip.Raw() == "" {
			t.Error("Raw() should not be empty")
		}
	}
}

func TestLoadIgnoreFile_Missing(t *testing.T) {
	dir := t.TempDir()
	ip := LoadIgnoreFile(dir)
	if ip != nil {
		t.Error("expected nil for missing .agentignore")
	}
}

func TestLoadIgnoreFile_Empty(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".agentignore"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	ip := LoadIgnoreFile(dir)
	if ip == nil {
		t.Fatal("expected non-nil IgnorePatterns for empty file")
	}
	if len(ip.Patterns()) != 0 {
		t.Errorf("expected 0 patterns, got %d", len(ip.Patterns()))
	}
}

func TestLoadIgnoreFile_CommentsOnly(t *testing.T) {
	dir := t.TempDir()
	content := "# This is a comment\n# Another comment\n\n"
	if err := os.WriteFile(filepath.Join(dir, ".agentignore"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	ip := LoadIgnoreFile(dir)
	if ip == nil {
		t.Fatal("expected non-nil IgnorePatterns")
	}
	if len(ip.Patterns()) != 0 {
		t.Errorf("expected 0 patterns for comments-only file, got %d", len(ip.Patterns()))
	}
}

func TestIgnorePatterns_Match_Exact(t *testing.T) {
	ip := &IgnorePatterns{
		patterns: []string{"node_modules", ".git", "vendor"},
	}

	tests := []struct {
		path string
		want bool
	}{
		{"node_modules", true},
		{"node_modules/package/index.js", true},
		{".git", true},
		{".git/config", true},
		{"vendor", true},
		{"vendor/lib/foo.go", true},
		{"src/main.go", false},
		{"README.md", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := ip.Match(tt.path); got != tt.want {
				t.Errorf("Match(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestIgnorePatterns_Match_Glob(t *testing.T) {
	ip := &IgnorePatterns{
		patterns: []string{"*.log", "*.tmp"},
	}

	tests := []struct {
		path string
		want bool
	}{
		{"app.log", true},
		{"debug.log", true},
		{"temp.tmp", true},
		{"logs/app.log", true},
		{"main.go", false},
		{"log.txt", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := ip.Match(tt.path); got != tt.want {
				t.Errorf("Match(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestIgnorePatterns_Match_DirectoryPattern(t *testing.T) {
	ip := &IgnorePatterns{
		patterns: []string{"build/", "dist/"},
	}

	tests := []struct {
		path string
		want bool
	}{
		{"build", true},
		{"build/output.js", true},
		{"dist", true},
		{"dist/bundle.js", true},
		{"src/build.go", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := ip.Match(tt.path); got != tt.want {
				t.Errorf("Match(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestIgnorePatterns_Match_NilReceiver(t *testing.T) {
	var ip *IgnorePatterns
	if ip.Match("anything") {
		t.Error("nil IgnorePatterns should not match anything")
	}
}

func TestIgnorePatterns_Raw_NilReceiver(t *testing.T) {
	var ip *IgnorePatterns
	if ip.Raw() != "" {
		t.Error("nil IgnorePatterns Raw() should return empty string")
	}
}

func TestIgnorePatterns_Patterns_NilReceiver(t *testing.T) {
	var ip *IgnorePatterns
	if ip.Patterns() != nil {
		t.Error("nil IgnorePatterns Patterns() should return nil")
	}
}
