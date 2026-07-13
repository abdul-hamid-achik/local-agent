package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestConfigFileCandidatesPrecedenceAndDeduplication(t *testing.T) {
	home := t.TempDir()
	xdg := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)

	want := []string{
		"local-agent.yaml",
		"local-agent.yml",
		filepath.Join(xdg, "local-agent", "config.yaml"),
		filepath.Join(xdg, "local-agent", "config.yml"),
		filepath.Join(home, ".config", "local-agent", "config.yaml"),
		filepath.Join(home, ".config", "local-agent", "config.yml"),
	}
	if got := configFileCandidates(); !reflect.DeepEqual(got, want) {
		t.Fatalf("config candidates = %#v, want %#v", got, want)
	}

	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	want = []string{
		"local-agent.yaml",
		"local-agent.yml",
		filepath.Join(home, ".config", "local-agent", "config.yaml"),
		filepath.Join(home, ".config", "local-agent", "config.yml"),
	}
	if got := configFileCandidates(); !reflect.DeepEqual(got, want) {
		t.Fatalf("deduplicated config candidates = %#v, want %#v", got, want)
	}
}

func TestConfigFileCandidatesIgnoreRelativeXDGHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", "relative-config")

	want := []string{
		"local-agent.yaml",
		"local-agent.yml",
		filepath.Join(home, ".config", "local-agent", "config.yaml"),
		filepath.Join(home, ".config", "local-agent", "config.yml"),
	}
	if got := configFileCandidates(); !reflect.DeepEqual(got, want) {
		t.Fatalf("relative-XDG config candidates = %#v, want %#v", got, want)
	}
}

func TestFindAndReadConfigFilePrecedence(t *testing.T) {
	workspace := t.TempDir()
	home := t.TempDir()
	xdg := t.TempDir()
	oldWorkDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(workspace); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWorkDir) })
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)

	homePath := filepath.Join(home, ".config", "local-agent", "config.yaml")
	xdgPath := filepath.Join(xdg, "local-agent", "config.yml")
	writeConfigFixture(t, homePath, "home")
	writeConfigFixture(t, xdgPath, "xdg")

	path, data, err := findAndReadConfigFile()
	if err != nil {
		t.Fatal(err)
	}
	if path != xdgPath || string(data) != "xdg" {
		t.Fatalf("XDG selection path=%q data=%q, want path=%q data=%q", path, data, xdgPath, "xdg")
	}

	localPath := filepath.Join(workspace, "local-agent.yml")
	writeConfigFixture(t, localPath, "local")
	path, data, err = findAndReadConfigFile()
	if err != nil {
		t.Fatal(err)
	}
	if path != "local-agent.yml" || string(data) != "local" {
		t.Fatalf("local selection path=%q data=%q, want path=%q data=%q", path, data, "local-agent.yml", "local")
	}
}

func TestFindAndReadConfigFileFallsBackToHomeConfig(t *testing.T) {
	workspace := t.TempDir()
	home := t.TempDir()
	oldWorkDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(workspace); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWorkDir) })
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", "")

	homePath := filepath.Join(home, ".config", "local-agent", "config.yml")
	writeConfigFixture(t, homePath, "home")
	path, data, err := findAndReadConfigFile()
	if err != nil {
		t.Fatal(err)
	}
	if path != homePath || string(data) != "home" {
		t.Fatalf("home fallback path=%q data=%q, want path=%q data=%q", path, data, homePath, "home")
	}
}

func TestLoadRecordsAbsoluteSelectedConfigPathWithoutSerializingIt(t *testing.T) {
	workspace := t.TempDir()
	oldWorkDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(workspace); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWorkDir) })
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", "")

	path := filepath.Join(workspace, "local-agent.yaml")
	writeConfigFixture(t, path, "privacy:\n  local_only: false\n")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	wantPath, err := filepath.Abs("local-agent.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SourcePath != wantPath {
		t.Fatalf("SourcePath = %q, want %q", cfg.SourcePath, wantPath)
	}
	encoded, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "sourcepath") || strings.Contains(string(encoded), cfg.SourcePath) {
		t.Fatalf("runtime source path was serialized: %s", encoded)
	}
	encoded, err = json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "SourcePath") || strings.Contains(string(encoded), cfg.SourcePath) {
		t.Fatalf("runtime source path was JSON serialized: %s", encoded)
	}
}

func writeConfigFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
