package skill

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/abdul-hamid-achik/local-agent/internal/safeio"
)

const maxSkillFileBytes int64 = 1 << 20

var skillFileReader = safeio.NewReader()

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
		data, err := skillFileReader.ReadRegularFileNoFollow(path, maxSkillFileBytes, safeio.StartupReadTimeout)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return fmt.Errorf("read skill %s: %w", path, err)
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

// Has reports whether a skill name is available without changing activation.
func (m *Manager) Has(name string) bool {
	for _, skill := range m.skills {
		if skill.Name == name {
			return true
		}
	}
	return false
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

// UpdateActive atomically validates and then applies a profile skill change.
// Skills outside remove/add are preserved, so manually activated skills are
// not disturbed when a profile changes.
func (m *Manager) UpdateActive(remove, add []string) error {
	for _, name := range append(append([]string(nil), remove...), add...) {
		if !m.Has(name) {
			return fmt.Errorf("skill not found: %s", name)
		}
	}
	removeSet := make(map[string]struct{}, len(remove))
	addSet := make(map[string]struct{}, len(add))
	for _, name := range remove {
		removeSet[name] = struct{}{}
	}
	for _, name := range add {
		addSet[name] = struct{}{}
	}
	for _, s := range m.skills {
		if _, removeSkill := removeSet[s.Name]; removeSkill {
			s.Active = false
		}
		if _, addSkill := addSet[s.Name]; addSkill {
			s.Active = true
		}
	}
	return nil
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
