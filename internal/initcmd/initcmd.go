package initcmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// projectMarker maps a marker file name to its detected project type.
var projectMarkers = map[string]string{
	"go.mod":           "Go",
	"go.sum":           "Go",
	"package.json":     "Node.js",
	"Cargo.toml":       "Rust",
	"pyproject.toml":   "Python",
	"requirements.txt": "Python",
	"setup.py":         "Python",
	"Pipfile":          "Python",
	"Gemfile":          "Ruby",
	"pom.xml":          "Java (Maven)",
	"build.gradle":     "Java (Gradle)",
	"build.gradle.kts": "Kotlin (Gradle)",
	"CMakeLists.txt":   "C/C++ (CMake)",
	"Makefile":         "Make",
	"Taskfile.yml":     "Taskfile",
	"Taskfile.yaml":    "Taskfile",
	"docker-compose.yml":  "Docker Compose",
	"docker-compose.yaml": "Docker Compose",
	"Dockerfile":          "Docker",
	".gitignore":          "Git",
}

// Options configures the behaviour of Run.
type Options struct {
	// Force overwrites an existing AGENT.md.
	Force bool
}

// Run scans dir for project markers and generates an AGENT.md file.
// It returns an error if AGENT.md already exists unless opts.Force is true.
func Run(dir string, opts Options) error {
	agentPath := filepath.Join(dir, "AGENT.md")

	if !opts.Force {
		if _, err := os.Stat(agentPath); err == nil {
			return fmt.Errorf("AGENT.md already exists in %s (use --force to overwrite)", dir)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("reading directory: %w", err)
	}

	// Detect project types from marker files.
	detectedTypes := detectProjectTypes(entries)

	// Build directory listing.
	listing := buildDirectoryListing(dir, entries)

	// Generate AGENT.md content.
	content := generateAgentMD(detectedTypes, listing)

	if err := os.WriteFile(agentPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("writing AGENT.md: %w", err)
	}

	return nil
}

// detectProjectTypes returns a deduplicated, sorted list of project types
// found based on marker files in the directory entries.
func detectProjectTypes(entries []os.DirEntry) []string {
	seen := make(map[string]bool)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if pt, ok := projectMarkers[e.Name()]; ok {
			seen[pt] = true
		}
	}

	types := make([]string, 0, len(seen))
	for t := range seen {
		types = append(types, t)
	}
	sort.Strings(types)
	return types
}

// buildDirectoryListing returns a formatted string of top-level files and
// first-level subdirectory contents.
func buildDirectoryListing(dir string, entries []os.DirEntry) string {
	var b strings.Builder

	var files []string
	var dirs []string

	for _, e := range entries {
		name := e.Name()
		// Skip hidden files/dirs except well-known ones.
		if strings.HasPrefix(name, ".") && name != ".gitignore" {
			continue
		}
		if e.IsDir() {
			dirs = append(dirs, name)
		} else {
			files = append(files, name)
		}
	}

	sort.Strings(files)
	sort.Strings(dirs)

	for _, f := range files {
		b.WriteString(f)
		b.WriteByte('\n')
	}

	for _, d := range dirs {
		b.WriteString(d + "/\n")
		subEntries, err := os.ReadDir(filepath.Join(dir, d))
		if err != nil {
			continue
		}
		var subNames []string
		for _, se := range subEntries {
			n := se.Name()
			if strings.HasPrefix(n, ".") {
				continue
			}
			if se.IsDir() {
				subNames = append(subNames, n+"/")
			} else {
				subNames = append(subNames, n)
			}
		}
		sort.Strings(subNames)
		for _, sn := range subNames {
			b.WriteString("  " + sn + "\n")
		}
	}

	return b.String()
}

// generateAgentMD produces the Markdown content for the AGENT.md file.
func generateAgentMD(projectTypes []string, listing string) string {
	var b strings.Builder

	b.WriteString("# AGENT.md\n\n")

	// Project type section.
	b.WriteString("## Project Type\n\n")
	if len(projectTypes) == 0 {
		b.WriteString("Unknown\n")
	} else {
		b.WriteString(strings.Join(projectTypes, ", ") + "\n")
	}

	// Directory structure.
	b.WriteString("\n## Directory Structure\n\n")
	b.WriteString("```\n")
	b.WriteString(listing)
	b.WriteString("```\n")

	// Placeholder sections.
	b.WriteString("\n## Build Commands\n\n")
	b.WriteString("<!-- Add build, test, and run commands here -->\n")

	b.WriteString("\n## Architecture\n\n")
	b.WriteString("<!-- Describe the high-level architecture here -->\n")

	b.WriteString("\n## Key Files\n\n")
	b.WriteString("<!-- List important files and their purposes here -->\n")

	b.WriteString("\n## Notes\n\n")
	b.WriteString("<!-- Any additional notes for the agent -->\n")

	return b.String()
}
