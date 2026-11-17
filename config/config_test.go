package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestMergeWithDefaults(t *testing.T) {
	pc := ProjectConfig{}
	pc.MergeWithDefaults()
	if pc.GitStartBranch != "master|main" || pc.GitNewBranchPattern != DefaultBranchPattern {
		t.Fatalf("defaults not applied: %+v", pc)
	}

	pc = ProjectConfig{GitStartBranch: "main", GitNewBranchPattern: "feature/*"}
	pc.MergeWithDefaults()
	if pc.GitStartBranch != "main" || pc.GitNewBranchPattern != "feature/*" {
		t.Fatalf("overrides lost: %+v", pc)
	}

	pc = ProjectConfig{GitNewBranchPattern: "hotfix/*"}
	pc.MergeWithDefaults()
	if pc.GitNewBranchPattern != DefaultBranchPattern {
		t.Fatalf("old default not migrated: %+v", pc)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.yaml")
	in := &Config{BaseFolders: []string{"/srv", "/www"}, ProjectPaths: []string{"/opt/site"}, MaxConcurrent: 2, Projects: map[string]ProjectConfig{"a": {GitStartBranch: "main"}}}
	if err := SaveConfig(path, in); err != nil {
		t.Fatal(err)
	}
	out, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(out.BaseFolders, in.BaseFolders) || !reflect.DeepEqual(out.ProjectPaths, in.ProjectPaths) || out.MaxConcurrent != 2 || out.Projects["a"].GitStartBranch != "main" {
		t.Fatalf("round trip mismatch: %+v", out)
	}
}

func TestLoadLegacyBaseFolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(path, []byte("base_folder: /srv\nmode: classic\ndefault_commands: [ls]\n"), 0o644)
	cfg, err := LoadConfig(path)
	if err != nil || !reflect.DeepEqual(cfg.BaseFolders, []string{"/srv"}) || cfg.LegacyBaseFolder != "" {
		t.Fatalf("got %+v, %v", cfg, err)
	}
}

func TestLoadMissingConfig(t *testing.T) {
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "none.yaml"))
	if err != nil || !reflect.DeepEqual(cfg.BaseFolders, []string{"~/projects"}) || cfg.MaxConcurrent != 3 {
		t.Fatalf("got %+v, %v", cfg, err)
	}
}

func TestForProject(t *testing.T) {
	dir := t.TempDir()
	local := ProjectConfig{GitStartBranch: "develop", IgnoredPackages: []string{"drupal/local"}, ClaudeInstructions: "mine"}

	pc := ForProject(local, dir) // no repo file: local + defaults
	if pc.GitStartBranch != "develop" || pc.GitNewBranchPattern != DefaultBranchPattern || pc.GitCommitMessage != DefaultCommitMessage {
		t.Fatalf("without file: %+v", pc)
	}

	os.WriteFile(filepath.Join(dir, ProjectFile), []byte("git_start_branch: main\nignored_packages: [drupal/repo]\ngit_merge_target: staging\n"), 0o644)
	pc = ForProject(local, dir)
	if pc.GitStartBranch != "main" || !reflect.DeepEqual(pc.IgnoredPackages, []string{"drupal/repo"}) || pc.GitMergeTarget != "staging" || pc.ClaudeInstructions != "mine" {
		t.Fatalf("repo should win, local fill gaps: %+v", pc)
	}

	os.WriteFile(filepath.Join(dir, ProjectFile), []byte(`{"git_start_branch": "json"}`), 0o644)
	if pc = ForProject(local, dir); pc.GitStartBranch != "json" {
		t.Fatalf("JSON body not read: %+v", pc)
	}

	os.WriteFile(filepath.Join(dir, ProjectFile), []byte("git_start_branch: [broken"), 0o644)
	if pc = ForProject(local, dir); pc.GitStartBranch != "develop" {
		t.Fatalf("broken file should fall back to local: %+v", pc)
	}

	os.Remove(filepath.Join(dir, ProjectFile))
	if err := SaveProjectFile(dir, ProjectConfig{GitStartBranch: "saved"}); err != nil {
		t.Fatal(err)
	}
	if pc = ForProject(ProjectConfig{}, dir); pc.GitStartBranch != "saved" {
		t.Fatalf("saved file not read back: %+v", pc)
	}
}

func TestProjectDefaults(t *testing.T) {
	cfg := &Config{Defaults: ProjectConfig{GitStartBranch: "develop", GitMergeTarget: "staging"}, Projects: map[string]ProjectConfig{"a": {GitStartBranch: "main"}}}
	if pc := cfg.Project("a"); pc.GitStartBranch != "main" || pc.GitMergeTarget != "staging" {
		t.Fatalf("project entry should win, defaults fill gaps: %+v", pc)
	}
	if pc := ForProject(cfg.Project("b"), t.TempDir()); pc.GitStartBranch != "develop" || pc.GitNewBranchPattern != DefaultBranchPattern {
		t.Fatalf("project without entry: %+v", pc)
	}
}

func TestSaveProjectFileKeepsTeamSettings(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ProjectFile), []byte("git_merge_target: staging\n"), 0o644)
	if err := SaveProjectFile(dir, ProjectConfig{GitStartBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	if pc := ForProject(ProjectConfig{}, dir); pc.GitMergeTarget != "staging" || pc.GitStartBranch != "main" {
		t.Fatalf("existing setting lost or new one missing: %+v", pc)
	}
	os.WriteFile(filepath.Join(dir, ProjectFile), []byte("git_start_branch: [broken"), 0o644)
	if SaveProjectFile(dir, ProjectConfig{GitStartBranch: "x"}) == nil {
		t.Fatal("overwrote a file that does not parse")
	}
}

func TestWriteFileAtomicAndBackup(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := WriteFileAtomic(p, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(p, []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "two" {
		t.Fatalf("got %q", b)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("temp files left behind: %v", entries)
	}
	bad, err := BackupBadConfig(p)
	if err != nil || !strings.HasPrefix(bad, p+".bad-") {
		t.Fatalf("backup: %q %v", bad, err)
	}
	if b, _ := os.ReadFile(bad); string(b) != "two" {
		t.Fatal("backup lost the content")
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("original should be moved away")
	}
	if _, err := BackupBadConfig(p); err == nil {
		t.Fatal("missing file must error")
	}
}

func TestDashboardProjectComesFromTheRepoFile(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{Projects: map[string]ProjectConfig{"site": {DashboardProject: "Local Name"}}}
	if got := ForProject(cfg.Project("site"), dir).DashboardProject; got != "Local Name" {
		t.Fatalf("local override: %q", got)
	}
	if err := SaveProjectFile(dir, ProjectConfig{DashboardProject: "Shared Name"}); err != nil {
		t.Fatal(err)
	}
	if got := ForProject(cfg.Project("site"), dir).DashboardProject; got != "Shared Name" {
		t.Fatalf("the repo's drupal-updater.yaml must win: %q", got)
	}
	// saving another field later keeps the mapping that is already in the file
	if err := SaveProjectFile(dir, ProjectConfig{GitStartBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	if got := ForProject(ProjectConfig{}, dir); got.DashboardProject != "Shared Name" || got.GitStartBranch != "main" {
		t.Fatalf("merge lost a field: %+v", got)
	}
}
