package config

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestMergeWithDefaults(t *testing.T) {
	global := &Config{DefaultCommands: []string{"global"}}

	pc := ProjectConfig{}
	pc.MergeWithDefaults(global)
	if pc.GitStartBranch != "master" || pc.GitNewBranchPattern != "hotfix/*" || !reflect.DeepEqual(pc.Commands, []string{"global"}) {
		t.Fatalf("defaults not applied: %+v", pc)
	}

	pc = ProjectConfig{GitStartBranch: "main", Commands: []string{"own"}}
	pc.MergeWithDefaults(global)
	if pc.GitStartBranch != "main" || !reflect.DeepEqual(pc.Commands, []string{"own"}) {
		t.Fatalf("overrides lost: %+v", pc)
	}

	pc = ProjectConfig{}
	pc.MergeWithDefaults(&Config{})
	if !reflect.DeepEqual(pc.Commands, DefaultProjectConfig().Commands) {
		t.Fatalf("built-in commands not used: %+v", pc.Commands)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.yaml")
	in := &Config{BaseFolder: "/srv", MaxConcurrent: 2, Projects: map[string]ProjectConfig{"a": {GitStartBranch: "main"}}}
	if err := SaveConfig(path, in); err != nil {
		t.Fatal(err)
	}
	out, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if out.BaseFolder != "/srv" || out.MaxConcurrent != 2 || out.Projects["a"].GitStartBranch != "main" || len(out.DefaultCommands) == 0 {
		t.Fatalf("round trip mismatch: %+v", out)
	}
}

func TestLoadMissingConfig(t *testing.T) {
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "none.yaml"))
	if err != nil || cfg.BaseFolder != "~/projects" || cfg.MaxConcurrent != 3 {
		t.Fatalf("got %+v, %v", cfg, err)
	}
}
