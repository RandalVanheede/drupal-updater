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

	projects, err := DiscoverProjects(base, []string{"old-*"})
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
	projects, err := DiscoverProjects(filepath.Join(t.TempDir(), "nope"), nil)
	if err != nil || len(projects) != 0 {
		t.Fatalf("got %v, %v", projects, err)
	}
}
