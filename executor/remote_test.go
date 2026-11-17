package executor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

func TestCleanupKeepWork(t *testing.T) {
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@example.com")
	repo := t.TempDir()
	run(t, repo, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(repo, "a"), []byte("1"), 0o644)
	run(t, repo, "add", ".")
	run(t, repo, "commit", "-qm", "base")

	// temp branch with a commit: renamed, not deleted
	run(t, repo, "checkout", "-qb", "drupal-updater/temp-site")
	os.WriteFile(filepath.Join(repo, "a"), []byte("2"), 0o644)
	run(t, repo, "commit", "-qam", "work")
	kept, err := CleanupKeepWork(repo, "master|main", "drupal-updater/temp-site")
	if err != nil || !strings.HasPrefix(kept, "drupal-updater/failed-site-") {
		t.Fatalf("kept %q err %v", kept, err)
	}
	run(t, repo, "rev-parse", "--verify", "refs/heads/"+kept)
	if out, _ := git(repo, "rev-parse", "--verify", "--quiet", "refs/heads/drupal-updater/temp-site"); out != "" {
		t.Fatal("temp branch should be gone")
	}
	if cur, _ := git(repo, "branch", "--show-current"); strings.TrimSpace(cur) != "main" {
		t.Fatalf("expected to be back on main, on %q", cur)
	}

	// temp branch without new commits: deleted as before
	run(t, repo, "checkout", "-qb", "drupal-updater/temp-empty")
	kept, err = CleanupKeepWork(repo, "main", "drupal-updater/temp-empty")
	if err != nil || kept != "" {
		t.Fatalf("empty branch: kept %q err %v", kept, err)
	}
	if out, _ := git(repo, "rev-parse", "--verify", "--quiet", "refs/heads/drupal-updater/temp-empty"); out != "" {
		t.Fatal("empty temp branch should be deleted")
	}
}

func TestCleanupKeepWorkSurvivesAnAmbiguousBranchName(t *testing.T) {
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@example.com")
	repo := t.TempDir()
	run(t, repo, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(repo, "a"), []byte("1"), 0o644)
	run(t, repo, "add", ".")
	run(t, repo, "commit", "-qm", "base")
	run(t, repo, "tag", "main") // a tag named like the start branch: git warns "refname 'main' is ambiguous"
	run(t, repo, "checkout", "-qb", "drupal-updater/temp-x")
	os.WriteFile(filepath.Join(repo, "a"), []byte("2"), 0o644)
	run(t, repo, "commit", "-qam", "work")
	kept, err := CleanupKeepWork(repo, "main", "drupal-updater/temp-x")
	if err != nil || kept == "" {
		t.Fatalf("the finished work must be kept, not deleted: %q %v", kept, err)
	}
	run(t, repo, "rev-parse", "--verify", "refs/heads/"+kept)
}
