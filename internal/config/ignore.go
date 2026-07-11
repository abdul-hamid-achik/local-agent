package config

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/abdul-hamid-achik/local-agent/internal/safeio"
)

const maxAgentIgnoreBytes int64 = 256 << 10

var agentIgnoreReader = safeio.NewReader()

// IgnorePatterns holds parsed .agentignore patterns.
type IgnorePatterns struct {
	patterns []string
	raw      string // original file content for injection into system prompt
}

// LoadIgnoreFile reads and parses an .agentignore file from the given directory.
// Returns nil if no .agentignore file exists (not an error).
func LoadIgnoreFile(dir string) *IgnorePatterns {
	patterns, _ := LoadIgnoreFileWithError(dir)
	return patterns
}

// LoadIgnoreFileWithError is the diagnostic form used at startup. Missing
// files are not errors; unsafe, oversized, or timed-out inputs are rejected.
func LoadIgnoreFileWithError(dir string) (*IgnorePatterns, error) {
	path := filepath.Join(dir, ".agentignore")
	data, err := agentIgnoreReader.ReadRegularFileNoFollow(path, maxAgentIgnoreBytes, safeio.StartupReadTimeout)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read .agentignore: %w", err)
	}

	var patterns []string
	var rawLines []string

	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), int(maxAgentIgnoreBytes)+1)
	for scanner.Scan() {
		line := scanner.Text()
		rawLines = append(rawLines, line)

		trimmed := strings.TrimSpace(line)
		// Skip empty lines and comments.
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		patterns = append(patterns, trimmed)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("parse .agentignore: %w", err)
	}

	return &IgnorePatterns{
		patterns: patterns,
		raw:      strings.Join(rawLines, "\n"),
	}, nil
}

// Match returns true if the given path should be ignored.
// Returns false if the receiver is nil.
func (ip *IgnorePatterns) Match(path string) bool {
	if ip == nil || len(ip.patterns) == 0 {
		return false
	}

	// Normalise the path separators and remove trailing slashes for comparison.
	path = filepath.ToSlash(path)
	cleanPath := strings.TrimSuffix(path, "/")

	for _, pattern := range ip.patterns {
		pat := strings.TrimSuffix(pattern, "/")

		// Check each component of the path against the pattern.
		// e.g. "node_modules" should match "node_modules", "node_modules/foo",
		// and "src/node_modules/bar".
		parts := strings.Split(cleanPath, "/")
		for _, part := range parts {
			if matched, _ := filepath.Match(pat, part); matched {
				return true
			}
		}

		// Also try matching the full path with the pattern (for glob patterns
		// that include path separators like "build/output").
		if matched, _ := filepath.Match(pat, cleanPath); matched {
			return true
		}

		// Prefix match: path starts with the pattern directory.
		if strings.HasPrefix(cleanPath, pat+"/") || cleanPath == pat {
			return true
		}
	}

	return false
}

// Raw returns the raw file content for system prompt injection.
// Returns an empty string if the receiver is nil.
func (ip *IgnorePatterns) Raw() string {
	if ip == nil {
		return ""
	}
	return ip.raw
}

// Patterns returns the list of patterns.
// Returns nil if the receiver is nil.
func (ip *IgnorePatterns) Patterns() []string {
	if ip == nil {
		return nil
	}
	return ip.patterns
}
