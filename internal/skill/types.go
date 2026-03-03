package skill

import (
	"bufio"
	"strings"

	"gopkg.in/yaml.v3"
)

// Skill represents a loadable skill definition.
type Skill struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Active      bool   `yaml:"-"`
	Content     string `yaml:"-"` // markdown body after frontmatter
	Path        string `yaml:"-"` // file path
}

// parseFrontmatter extracts YAML frontmatter and markdown body from a skill file.
// Frontmatter is delimited by "---" on the first and closing lines.
func parseFrontmatter(data string) (*Skill, error) {
	scanner := bufio.NewScanner(strings.NewReader(data))

	// Check for opening "---".
	if !scanner.Scan() || strings.TrimSpace(scanner.Text()) != "---" {
		// No frontmatter — treat entire content as body.
		return &Skill{Content: data}, nil
	}

	// Read YAML lines until closing "---".
	var yamlBuf strings.Builder
	foundEnd := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "---" {
			foundEnd = true
			break
		}
		yamlBuf.WriteString(line)
		yamlBuf.WriteString("\n")
	}

	if !foundEnd {
		// No closing delimiter — treat as body only.
		return &Skill{Content: data}, nil
	}

	// Parse YAML frontmatter.
	s := &Skill{}
	if err := yaml.Unmarshal([]byte(yamlBuf.String()), s); err != nil {
		return nil, err
	}

	// Remaining content is the markdown body.
	var bodyBuf strings.Builder
	for scanner.Scan() {
		if bodyBuf.Len() > 0 {
			bodyBuf.WriteString("\n")
		}
		bodyBuf.WriteString(scanner.Text())
	}
	s.Content = strings.TrimSpace(bodyBuf.String())

	return s, nil
}
