package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestNewAppKeepsUnreadableConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".config", "drupal-updater")
	os.MkdirAll(dir, 0o755)
	cfgPath := filepath.Join(dir, "config.yaml")
	os.WriteFile(cfgPath, []byte("base_folders: [unclosed"), 0o644)

	a := NewApp()
	if a.GetConfigError() == "" {
		t.Fatal("an unreadable config should be reported")
	}
	kept, _ := filepath.Glob(cfgPath + ".bad-*")
	if len(kept) != 1 {
		t.Fatalf("the original must be kept as config.yaml.bad-<time>: %v", kept)
	}
	if b, err := os.ReadFile(kept[0]); err != nil || string(b) != "base_folders: [unclosed" {
		t.Fatalf("backup content: %v", err)
	}
	if _, err := os.Stat(cfgPath); !os.IsNotExist(err) {
		t.Fatal("saving defaults later must not overwrite the user's file")
	}
}

func TestSetUpdateModeReplacesTheConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	a := NewApp()
	old := a.config()
	if err := a.SetUpdateMode("disaster"); err != nil {
		t.Fatal(err)
	}
	if old.UpdateMode == "disaster" {
		t.Fatal("readers holding the old snapshot must not see an in-place edit")
	}
	if a.config().UpdateMode != "disaster" {
		t.Fatal("new mode not applied")
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "drupal-updater", "config.yaml")); err != nil {
		t.Fatalf("mode not saved: %v", err)
	}
}

func TestCancelAllAlsoCancelsMergeRequests(t *testing.T) {
	a := &App{}
	ctx, cancel := context.WithCancel(context.Background())
	a.cancels = map[string]context.CancelFunc{"site": cancel} // a merge request registers itself here
	a.CancelAll()
	if ctx.Err() == nil {
		t.Fatal("Cancel All left a merge request running")
	}
}

func TestProjectPathUsesTheDiscoveryCache(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	base := t.TempDir()
	proj := filepath.Join(base, "site")
	os.MkdirAll(proj, 0o755)
	os.WriteFile(filepath.Join(proj, "composer.json"), []byte(`{"require":{"drupal/core-recommended":"^11"}}`), 0o644)

	a := NewApp()
	a.cfg.BaseFolders = []string{base}
	got, err := a.projectPath("site") // not cached yet: scans
	if err != nil || got != proj {
		t.Fatalf("scan: %q %v", got, err)
	}
	a.paths = map[string]string{"site": proj}
	a.cfg.BaseFolders = nil // a rescan would no longer find it, so this must come from the cache
	if got, err = a.projectPath("site"); err != nil || got != proj {
		t.Fatalf("cache: %q %v", got, err)
	}
	a.paths["site"] = filepath.Join(base, "moved") // cached path vanished: falls back to a scan, which now finds nothing
	if _, err = a.projectPath("site"); err == nil {
		t.Fatal("a vanished project must not be returned from the cache")
	}
	if p := a.RefreshCore("nope", false); p.Version != "" || p.FetchFailed {
		t.Fatalf("unknown project: %+v", p)
	}
}

func TestPreviewProjectsCountsUnsavedSettings(t *testing.T) {
	base := t.TempDir()
	for _, name := range []string{"a", "b", "skipme"} {
		os.MkdirAll(filepath.Join(base, name), 0o755)
		os.WriteFile(filepath.Join(base, name, "composer.json"), []byte(`{"require":{"drupal/core":"^11"}}`), 0o644)
	}
	a := &App{}
	if n := a.PreviewProjects([]string{base}, nil, nil, 1); n != 3 {
		t.Fatalf("all: %d", n)
	}
	if n := a.PreviewProjects([]string{base}, nil, []string{"skip*"}, 1); n != 2 {
		t.Fatalf("excluded: %d", n)
	}
	if n := a.PreviewProjects([]string{filepath.Join(base, "missing")}, nil, nil, 1); n != 0 {
		t.Fatalf("missing folder: %d", n)
	}
}

func TestDashboardAddressHandling(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a := NewApp()
	if st := a.DashboardState(); !st.Configured || st.SignedIn || st.Server != "https://applications.dyna-ops.be" {
		t.Fatalf("empty address uses the default: %+v", st)
	}
	a.cfg.DashboardURL = "http://not-https.example.com"
	if st := a.DashboardState(); st.Configured || st.Error == "" {
		t.Fatalf("a non-https address must be refused with a reason: %+v", st)
	}
	a.cfg.DashboardURL = "https://applications.example.com/api/mcp" // the full MCP address is fine
	if st := a.DashboardState(); !st.Configured || st.Server != "https://applications.example.com" {
		t.Fatalf("full MCP address: %+v", st)
	}
}

func TestSafeDashURL(t *testing.T) {
	server := "https://applications.example.com"
	for raw, want := range map[string]string{
		"https://applications.example.com/projects/1": "https://applications.example.com/projects/1",
		"smb://evil.example/share":                    "",
		"vscode://file/etc/passwd":                    "",
		"http://applications.example.com/projects/1":  "",
		"https://evil.example/projects/1":             "",
		"https://applications.example.com.evil.net/":  "",
		"": "",
	} {
		if got := safeDashURL(raw, server); got != want {
			t.Errorf("%q: got %q want %q", raw, got, want)
		}
	}
}
