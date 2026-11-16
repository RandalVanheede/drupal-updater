package ui

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"drupal-updater/config"
	"drupal-updater/executor"

	tea "github.com/charmbracelet/bubbletea"
)

func TestSplitList(t *testing.T) {
	if got := splitList(" /a, admin:/b,, "); !reflect.DeepEqual(got, []string{"/a", "admin:/b"}) {
		t.Fatalf("got %q", got)
	}
	if got := splitList(""); got != nil {
		t.Fatalf("empty input: got %q, want nil", got)
	}
}

func fieldByLabel(t *testing.T, fs []field, label string) field {
	t.Helper()
	for _, f := range fs {
		if f.label == label {
			return f
		}
	}
	t.Fatalf("no field %q", label)
	return field{}
}

func TestConfigFields(t *testing.T) {
	base := t.TempDir()
	os.Mkdir(filepath.Join(base, "site"), 0o755)
	os.WriteFile(filepath.Join(base, "site", "composer.json"), []byte("{}"), 0o644)
	cfg := &config.Config{BaseFolders: []string{base}}
	cm := NewConfigModel(cfg, filepath.Join(t.TempDir(), "config.yaml"))

	fieldByLabel(t, cm.globalFields(), "Start branch").set("develop")
	if cfg.Defaults.GitStartBranch != "develop" {
		t.Fatalf("branch default not set: %+v", cfg.Defaults)
	}
	_, tools := executor.ClaudeDefaults()
	fieldByLabel(t, cm.globalFields(), "Allowed tools").set(strings.Join(tools, "\n"))
	if cfg.ClaudeAllowedTools != nil {
		t.Fatalf("default tool list should be stored as empty, got %v", cfg.ClaudeAllowedTools)
	}
	cm.startEdit(fieldByLabel(t, cm.globalFields(), "Update mode"), ConfigMenu)
	if cfg.UpdateMode != "disaster" {
		t.Fatalf("update mode did not toggle: %q", cfg.UpdateMode)
	}

	cfg.Projects["site"] = config.ProjectConfig{}
	fieldByLabel(t, cm.projectFields("site"), "Ignored packages").set("drupal/a, drupal/b")
	if msg := cm.saveToRepo("site"); !strings.HasPrefix(msg, "✓") {
		t.Fatal(msg)
	}
	if pc := config.ForProject(cfg.Project("site"), filepath.Join(base, "site")); pc.GitStartBranch != "develop" || len(pc.IgnoredPackages) != 2 {
		t.Fatalf("repo file / defaults not applied: %+v", pc)
	}
}

func TestEmbeddedSettingsReload(t *testing.T) {
	base, cfgPath := t.TempDir(), filepath.Join(t.TempDir(), "config.yaml")
	os.Mkdir(filepath.Join(base, "site"), 0o755)
	os.WriteFile(filepath.Join(base, "site", "composer.json"), []byte("{}"), 0o644)
	cfg := &config.Config{BaseFolders: []string{base}, MaxConcurrent: 3}
	config.SaveConfig(cfgPath, cfg)
	m := New(cfg, nil, cfgPath, "dev")

	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("S")})
	if m.state != StateSettings {
		t.Fatal("S did not open settings")
	}
	cfg.Defaults.GitStartBranch = "unsaved" // an edit, then quit without saving
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m.Update(cmd())
	if m.state != StateList || cfg.Defaults.GitStartBranch != "" {
		t.Fatalf("state %d, start branch %q: unsaved edit should be dropped", m.state, cfg.Defaults.GitStartBranch)
	}
	if len(m.projects) != 1 || m.projects[0].Name != "site" {
		t.Fatalf("projects not rescanned: %+v", m.projects)
	}
}
