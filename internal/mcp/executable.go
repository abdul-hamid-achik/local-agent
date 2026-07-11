package mcp

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// resolveExecutable keeps stdio MCP startup reliable when local-agent is
// launched from Finder or another GUI with a minimal PATH.
func resolveExecutable(command string) (string, error) {
	if command == "" {
		return "", fmt.Errorf("empty command")
	}
	if strings.ContainsRune(command, os.PathSeparator) {
		path, err := exec.LookPath(command)
		if err != nil {
			return "", err
		}
		return path, nil
	}
	if path, err := exec.LookPath(command); err == nil {
		return path, nil
	}
	for _, dir := range standardExecutableDirs() {
		candidate := filepath.Join(dir, command)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("executable %q not found in PATH or standard local tool directories", command)
}

func standardExecutableDirs() []string {
	dirs := []string{"/opt/homebrew/bin", "/usr/local/bin"}
	if executable, err := os.Executable(); err == nil {
		dirs = append([]string{filepath.Dir(executable)}, dirs...)
	}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs,
			filepath.Join(home, ".local", "bin"),
			filepath.Join(home, "go", "bin"),
			filepath.Join(home, ".bun", "bin"),
		)
	}
	return uniqueStrings(dirs)
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
