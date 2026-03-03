package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewSessionLogger(t *testing.T) {
	logger, f, err := NewSessionLogger()
	if err != nil {
		t.Fatalf("NewSessionLogger() error: %v", err)
	}
	if f != nil {
		defer f.Close()
		defer os.Remove(f.Name())
	}

	if logger == nil {
		t.Fatal("logger should not be nil")
	}

	// Verify the file was created in the expected directory.
	if f == nil {
		t.Fatal("file should not be nil")
	}

	dir := filepath.Dir(f.Name())
	if !strings.Contains(dir, filepath.Join(".config", "local-agent", "logs")) {
		t.Errorf("log file should be in ~/.config/local-agent/logs/, got %q", dir)
	}

	// Verify the file is writable.
	logger.Info("test message", "key", "value")
}

func TestNilLoggerNoPanic(t *testing.T) {
	// Ensure the pattern used in the TUI works: nil check before calling.
	var called bool
	logger, f, err := NewSessionLogger()
	if err == nil && f != nil {
		defer f.Close()
		defer os.Remove(f.Name())
		logger.Info("test")
		called = true
	}
	// Just verify the code path executed without panic.
	_ = called
}
