package skill

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Manager handles skill discovery, loading, and activation.
type Manager struct {
	skills []*Skill
	dirs   []string
}

// NewManager creates a skill manager. If dir is empty, uses the default
// skills directory (~/.config/local-agent/skills/).
func NewManager(dir string) *Manager {
	dirs := []string{}
	if dir != "" {
		dirs = append(dirs, dir)
	} else {
		if home, err := os.UserHomeDir(); err == nil {
			dirs = append(dirs, filepath.Join(home, ".config", "local-agent", "skills"))
		}
	}
	return &Manager{dirs: dirs}
}

// AddSearchPath adds a directory to search for skills.
func (m *Manager) AddSearchPath(dir string) {
	for _, d := range m.dirs {
		if d == dir {
			return
		}
	}
	m.dirs = append(m.dirs, dir)
}

// Names returns all skill names.
func (m *Manager) Names() []string {
	var names []string
	for _, s := range m.skills {
		names = append(names, s.Name)
	}
	return names
}

// LoadAll discovers and loads all .md skill files from the skills directories.
func (m *Manager) LoadAll() error {
	for _, dir := range m.dirs {
		if err := m.loadFromDir(dir); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) loadFromDir(dir string) error {
	if dir == "" {
		return nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read skills dir: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		skill, err := parseFrontmatter(string(data))
		if err != nil {
			continue
		}

		skill.Path = path
		if skill.Name == "" {
			skill.Name = strings.TrimSuffix(entry.Name(), ".md")
		}

		m.skills = append(m.skills, skill)
	}

	return nil
}

// All returns all discovered skills.
func (m *Manager) All() []*Skill {
	return m.skills
}

// Activate enables a skill by name.
func (m *Manager) Activate(name string) error {
	for _, s := range m.skills {
		if s.Name == name {
			s.Active = true
			return nil
		}
	}
	return fmt.Errorf("skill not found: %s", name)
}

// Deactivate disables a skill by name.
func (m *Manager) Deactivate(name string) error {
	for _, s := range m.skills {
		if s.Name == name {
			s.Active = false
			return nil
		}
	}
	return fmt.Errorf("skill not found: %s", name)
}

// ActiveContent returns the combined markdown content of all active skills.
func (m *Manager) ActiveContent() string {
	var parts []string
	for _, s := range m.skills {
		if s.Active && s.Content != "" {
			parts = append(parts, fmt.Sprintf("### %s\n%s", s.Name, s.Content))
		}
	}
	return strings.Join(parts, "\n\n")
}
