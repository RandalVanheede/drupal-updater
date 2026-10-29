package discovery

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverProjects(t *testing.T) {
	base := t.TempDir()
	os.MkdirAll(filepath.Join(base, "ddev-site", ".ddev"), 0o755)
	os.MkdirAll(filepath.Join(base, "composer-site"), 0o755)
	os.WriteFile(filepath.Join(base, "composer-site", "composer.json"), []byte("{}"), 0o644)
	os.MkdirAll(filepath.Join(base, "old-site", ".ddev"), 0o755)
	os.MkdirAll(filepath.Join(base, "not-a-project"), 0o755)
	os.WriteFile(filepath.Join(base, "file.txt"), nil, 0o644)

	projects, err := DiscoverProjects([]string{base}, nil, []string{"old-*"})
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 2 {
		t.Fatalf("want 2 projects, got %v", projects)
	}
	if p, ok := Find(projects, "ddev-site"); !ok || p.Path != filepath.Join(base, "ddev-site") {
		t.Fatalf("ddev-site not found: %v", projects)
	}
	if _, ok := Find(projects, "old-site"); ok {
		t.Fatal("excluded project discovered")
	}
}

func TestDiscoverProjectsMissingBase(t *testing.T) {
	projects, err := DiscoverProjects([]string{filepath.Join(t.TempDir(), "nope")}, []string{filepath.Join(t.TempDir(), "gone")}, nil)
	if err != nil || len(projects) != 0 {
		t.Fatalf("got %v, %v", projects, err)
	}
}

func TestDiscoverProjectsMultipleFolders(t *testing.T) {
	a, b, extra := t.TempDir(), t.TempDir(), t.TempDir()
	for _, dir := range []string{filepath.Join(a, "site", ".ddev"), filepath.Join(b, "site", ".ddev"), filepath.Join(b, "other", ".ddev")} {
		os.MkdirAll(dir, 0o755)
	}
	// extra has no project markers; b/other is listed twice (base folder and explicit path)
	projects, err := DiscoverProjects([]string{a, b}, []string{extra, filepath.Join(b, "other")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 4 {
		t.Fatalf("want 4 projects, got %v", projects)
	}
	for name, path := range map[string]string{
		filepath.Base(a) + "/site": filepath.Join(a, "site"),
		filepath.Base(b) + "/site": filepath.Join(b, "site"),
		"other":                    filepath.Join(b, "other"),
		filepath.Base(extra):       extra,
	} {
		if p, ok := Find(projects, name); !ok || p.Path != path {
			t.Errorf("%s: got %v", name, projects)
		}
	}
}

func TestDiscoverProjectsSameParentName(t *testing.T) {
	x, y := filepath.Join(t.TempDir(), "a"), filepath.Join(t.TempDir(), "a")
	os.MkdirAll(filepath.Join(x, "site", ".ddev"), 0o755)
	os.MkdirAll(filepath.Join(y, "site", ".ddev"), 0o755)
	projects, err := DiscoverProjects([]string{x, y}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(x, "site"), filepath.Join(y, "site")} {
		if p, ok := Find(projects, path); !ok || p.Path != path {
			t.Errorf("%s not named by full path: %v", path, projects)
		}
	}
}
