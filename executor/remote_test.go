package executor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestRemoteStartFile(t *testing.T) {
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@example.com")
	root := t.TempDir()
	origin, a, b := filepath.Join(root, "origin.git"), filepath.Join(root, "a"), filepath.Join(root, "b")
	run(t, root, "init", "--bare", "-b", "main", origin)
	run(t, root, "clone", "-q", origin, a)
	os.WriteFile(filepath.Join(a, "composer.lock"), []byte("old"), 0o644)
	run(t, a, "add", ".")
	run(t, a, "commit", "-qm", "one")
	run(t, a, "push", "-q", "origin", "HEAD:main")
	run(t, root, "clone", "-q", origin, b) // b is now a stale copy once a pushes again
	os.WriteFile(filepath.Join(a, "composer.lock"), []byte("new"), 0o644)
	run(t, a, "commit", "-qam", "two")
	run(t, a, "push", "-q", "origin", "HEAD:main")

	ctx := context.Background()
	got, err := RemoteStartFile(ctx, b, "master|main", "composer.lock")
	if err != nil || string(got) != "old" {
		t.Fatalf("before fetch: %q %v", got, err)
	}
	if FetchedWithin(b, time.Hour) {
		t.Fatal("a fresh clone has no FETCH_HEAD")
	}
	if err := FetchOrigin(ctx, b); err != nil {
		t.Fatal(err)
	}
	if !FetchedWithin(b, time.Hour) || FetchedWithin(b, -time.Hour) {
		t.Fatal("FETCH_HEAD age not honoured")
	}
	if got, err = RemoteStartFile(ctx, b, "master|main", "composer.lock"); err != nil || string(got) != "new" {
		t.Fatalf("after fetch: %q %v", got, err)
	}
	if string(mustRead(t, filepath.Join(b, "composer.lock"))) != "old" {
		t.Fatal("working tree must stay untouched")
	}
	if _, err := RemoteStartFile(ctx, b, "nope", "composer.lock"); err != nil {
		t.Fatalf("origin/HEAD fallback: %v", err)
	}
	if _, err := RemoteStartFile(ctx, a+"/..", "main", "composer.lock"); err == nil {
		t.Fatal("not a repo must error")
	}
}

func mustRead(t *testing.T, p string) []byte {
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
