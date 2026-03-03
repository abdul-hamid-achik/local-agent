package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/charmbracelet/log"
)

// NewSessionLogger creates a timestamped log file at ~/.config/local-agent/logs/
// and returns a logger that writes to it. The caller should defer closing the file.
func NewSessionLogger() (*log.Logger, *os.File, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, nil, fmt.Errorf("home dir: %w", err)
	}

	logDir := filepath.Join(home, ".config", "local-agent", "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil, nil, fmt.Errorf("create log dir: %w", err)
	}

	filename := time.Now().Format("2006-01-02_15-04-05") + ".log"
	f, err := os.Create(filepath.Join(logDir, filename))
	if err != nil {
		return nil, nil, fmt.Errorf("create log file: %w", err)
	}

	logger := log.NewWithOptions(f, log.Options{
		ReportTimestamp: true,
		TimeFormat:      time.RFC3339,
		Prefix:          "local-agent",
		Level:           log.DebugLevel,
	})

	return logger, f, nil
}
