// Package runtimepref owns small, user-scoped runtime preferences that must
// survive process restarts without becoming part of repository or session
// state.
package runtimepref

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/abdul-hamid-achik/local-agent/internal/safeio"
)

const (
	preferenceVersion      = 1
	maxPreferenceFileBytes = 1024
	maxPreferredModelBytes = 256
	preferenceReadTimeout  = 2 * time.Second
	preferenceLockTimeout  = 2 * time.Second
	defaultPreferencesFile = "runtime-preferences.json"
)

type document struct {
	Version     int    `json:"version"`
	ManualModel string `json:"manual_model,omitempty"`
}

// Store persists one bounded manual model selection in an owner-private file.
// It deliberately contains no workspace or session identity.
type Store struct {
	mu     sync.RWMutex
	path   string
	reader *safeio.Reader
}

// DefaultPath returns the user-scoped preference path used by the CLI.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home dir: %w", err)
	}
	return filepath.Join(home, ".config", "local-agent", defaultPreferencesFile), nil
}

// NewStore creates a preference store at path. The file is opened lazily.
func NewStore(path string) *Store {
	return &Store{path: filepath.Clean(path), reader: safeio.NewReader()}
}

// LoadManualModel reads the saved manual selection. The boolean is false when
// no selection is saved. Unknown versions and malformed documents fail closed.
func (s *Store) LoadManualModel() (string, bool, error) {
	if s == nil || strings.TrimSpace(s.path) == "" || s.reader == nil {
		return "", false, fmt.Errorf("model preference store is not initialized")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	doc, err := s.loadLocked()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	if doc.ManualModel == "" {
		return "", false, nil
	}
	return doc.ManualModel, true, nil
}

// SetManualModel atomically replaces the saved manual selection.
func (s *Store) SetManualModel(model string) error {
	model, err := validateModel(model)
	if err != nil {
		return err
	}
	return s.write(document{Version: preferenceVersion, ManualModel: model})
}

// ClearManualModel durably removes any saved manual selection.
func (s *Store) ClearManualModel() error {
	return s.write(document{Version: preferenceVersion})
}

func (s *Store) write(doc document) error {
	if s == nil || strings.TrimSpace(s.path) == "" {
		return fmt.Errorf("model preference store is not initialized")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	dir := filepath.Dir(s.path)
	if err := safeio.ValidatePublishPath(s.path); err != nil {
		return fmt.Errorf("validate preference path: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create preference dir: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("secure preference dir: %w", err)
	}
	if err := safeio.ValidatePublishPath(s.path); err != nil {
		return fmt.Errorf("revalidate preference path: %w", err)
	}

	return safeio.WithExclusiveFileLock(s.path+".lock", preferenceLockTimeout, func() error {
		return s.persistLocked(doc)
	})
}

func (s *Store) loadLocked() (document, error) {
	var doc document
	data, err := s.reader.ReadPrivateRegularFileNoFollow(s.path, maxPreferenceFileBytes, preferenceReadTimeout)
	if err != nil {
		return doc, fmt.Errorf("read model preference: %w", err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&doc); err != nil {
		return document{}, fmt.Errorf("parse model preference: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = fmt.Errorf("trailing JSON value")
		}
		return document{}, fmt.Errorf("parse model preference: %w", err)
	}
	if doc.Version != preferenceVersion {
		return document{}, fmt.Errorf("unsupported model preference version %d", doc.Version)
	}
	if doc.ManualModel != "" {
		model, err := validateModel(doc.ManualModel)
		if err != nil {
			return document{}, fmt.Errorf("invalid saved model preference: %w", err)
		}
		doc.ManualModel = model
	}
	return doc, nil
}

func (s *Store) persistLocked(doc document) error {
	data, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("marshal model preference: %w", err)
	}
	data = append(data, '\n')
	if len(data) > maxPreferenceFileBytes {
		return fmt.Errorf("serialized model preference exceeds %d bytes", maxPreferenceFileBytes)
	}

	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, ".runtime-preferences-*.tmp")
	if err != nil {
		return fmt.Errorf("create preference temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("secure preference temp file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("write model preference: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync model preference: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close model preference: %w", err)
	}
	if err := safeio.ValidatePublishPath(s.path); err != nil {
		return fmt.Errorf("revalidate preference publish path: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("commit model preference: %w", err)
	}
	if directory, err := os.Open(dir); err == nil {
		_ = directory.Sync()
		_ = directory.Close()
	}
	return nil
}

func validateModel(model string) (string, error) {
	model = strings.TrimSpace(model)
	if model == "" {
		return "", fmt.Errorf("model preference is empty")
	}
	if !utf8.ValidString(model) {
		return "", fmt.Errorf("model preference is not valid UTF-8")
	}
	if len(model) > maxPreferredModelBytes {
		return "", fmt.Errorf("model preference exceeds %d bytes", maxPreferredModelBytes)
	}
	for _, r := range model {
		if unicode.IsControl(r) || unicode.In(r, unicode.Bidi_Control) {
			return "", fmt.Errorf("model preference contains control characters")
		}
	}
	return model, nil
}
