package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestMergeWithDefaults(t *testing.T) {
	pc := ProjectConfig{}
	pc.MergeWithDefaults()
	if pc.GitStartBranch != "master" || pc.GitNewBranchPattern != DefaultBranchPattern {
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

	if err := SaveProjectFile(dir, ProjectConfig{GitStartBranch: "saved"}); err != nil {
		t.Fatal(err)
	}
	if pc = ForProject(ProjectConfig{}, dir); pc.GitStartBranch != "saved" {
		t.Fatalf("saved file not read back: %+v", pc)
	}
}
