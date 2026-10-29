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
	if pc.GitStartBranch != "master" || pc.GitNewBranchPattern != "hotfix/*" {
		t.Fatalf("defaults not applied: %+v", pc)
	}

	pc = ProjectConfig{GitStartBranch: "main", GitNewBranchPattern: "feature/*"}
	pc.MergeWithDefaults()
	if pc.GitStartBranch != "main" || pc.GitNewBranchPattern != "feature/*" {
		t.Fatalf("overrides lost: %+v", pc)
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
