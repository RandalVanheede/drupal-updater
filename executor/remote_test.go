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

func TestFetchOriginRefusesRemoteHelperURLs(t *testing.T) {
	repo := t.TempDir()
	run(t, repo, "init", "-q", "-b", "main")
	marker := filepath.Join(t.TempDir(), "ran")
	run(t, repo, "remote", "add", "origin", "ext::sh -c 'touch "+marker+"'")
	if err := FetchOrigin(context.Background(), repo); err == nil {
		t.Fatal("a remote helper URL must be refused")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the helper command ran on the host")
	}
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// A repo whose config makes a plain `git fetch origin` run a program must not make FetchOrigin run it.
func TestFetchOriginDoesNotRunProgramsNamedByRepoConfig(t *testing.T) {
	for _, kv := range [][2]string{{"GIT_AUTHOR_NAME", "t"}, {"GIT_AUTHOR_EMAIL", "t@e.x"}, {"GIT_COMMITTER_NAME", "t"}, {"GIT_COMMITTER_EMAIL", "t@e.x"}} {
		t.Setenv(kv[0], kv[1])
	}
	root := t.TempDir()
	origin, work := filepath.Join(root, "o.git"), filepath.Join(root, "w")
	run(t, root, "init", "--bare", "-b", "main", origin)
	run(t, root, "clone", "-q", origin, work)
	os.WriteFile(filepath.Join(work, "f"), []byte("1"), 0o644)
	run(t, work, "add", ".")
	run(t, work, "commit", "-qm", "1")
	run(t, work, "push", "-q", "origin", "HEAD:main")

	cases := map[string]func(repo, marker string){
		"remote.origin.vcs": func(repo, marker string) {
			run(t, repo, "config", "remote.origin.vcs", "x/../payload")
			run(t, repo, "config", "remote.origin.url", "foo")
			os.MkdirAll(filepath.Join(repo, "git-remote-x"), 0o755)
			os.WriteFile(filepath.Join(repo, "payload"), []byte("#!/bin/sh\ntouch "+marker+"\n"), 0o755)
		},
		"core.gitProxy": func(repo, marker string) {
			run(t, repo, "config", "core.gitProxy", marker+"-px")
			os.WriteFile(marker+"-px", []byte("#!/bin/sh\ntouch "+marker+"\n"), 0o755)
			run(t, repo, "config", "remote.origin.url", "git://example.invalid/x")
		},
		"remote.origin.uploadpack": func(repo, marker string) {
			os.WriteFile(marker+"-up", []byte("#!/bin/sh\ntouch "+marker+"\nexec git-upload-pack \"$@\"\n"), 0o755)
			run(t, repo, "config", "remote.origin.uploadpack", marker+"-up")
		},
		"http.cookieFile": func(repo, marker string) { // a file the repo config wants rewritten as a cookie jar
			run(t, repo, "config", "http.cookieFile", marker)
			run(t, repo, "config", "http.saveCookies", "true")
		},
		"core.alternateRefsCommand": func(repo, marker string) {
			run(t, repo, "config", "core.alternateRefsCommand", "touch "+marker+" #")
			os.WriteFile(filepath.Join(repo, ".git", "objects", "info", "alternates"), []byte(filepath.Join(origin, "objects")+"\n"), 0o644)
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			victim := filepath.Join(root, "victim-"+strings.ReplaceAll(name, ".", "-"))
			run(t, root, "clone", "-q", origin, victim)
			marker := filepath.Join(root, "ran-"+strings.ReplaceAll(name, ".", "-"))
			setup(victim, marker)
			// sanity: the setup is dangerous, or this test proves nothing (not every git version honours each setting)
			probe := exec.Command("git", "fetch", "origin")
			probe.Dir = victim
			probe.Run()
			dangerous := exists(marker)
			os.Remove(marker)
			FetchOrigin(context.Background(), victim)
			if exists(marker) {
				t.Fatalf("FetchOrigin ran the program that %s names", name)
			}
			if name == "http.cookieFile" {
				return // only dangerous against a server that sets cookies; the scratch repository ignores it anyway
			}
			if !dangerous {
				t.Logf("note: a plain git fetch did not run it on this git version, so only the guard's absence of effect was shown")
			}
		})
	}
}

func TestOnlyRemoteSortsOfRemotesAreFetched(t *testing.T) {
	t.Setenv("DU_ALLOW_LOCAL_REMOTES", "0")
	for _, ok := range []string{"https://gitlab.example.net/group/repo.git", "ssh://git@host.example/group/repo.git", "git@gitlab.example.net:group/repo.git", "gitlab-work:group/repo.git"} {
		if !remoteAllowed(ok) {
			t.Errorf("%q should be fetched", ok)
		}
	}
	for _, bad := range []string{"/srv/repos/x.git", "file:///srv/repos/x.git", "../x", "git://host.example/x.git", "ext::sh -c id", "ext::true", "fd::3", "foo", "-oProxyCommand=id@x:y", "git@-evil:x", "https://host.example/x y", "http://host.example/x"} {
		if remoteAllowed(bad) {
			t.Errorf("%q must be refused", bad)
		}
	}
	local := t.TempDir()
	run(t, local, "init", "-q", "-b", "main")
	run(t, local, "remote", "add", "origin", t.TempDir()) // a local path: refused outside the tests
	if err := FetchOrigin(context.Background(), local); err == nil {
		t.Fatal("a local path origin must be refused")
	}
}

func TestFetchOriginUpdatesRefsWithoutTouchingTheWorkingTree(t *testing.T) {
	for _, kv := range [][2]string{{"GIT_AUTHOR_NAME", "t"}, {"GIT_AUTHOR_EMAIL", "t@e.x"}, {"GIT_COMMITTER_NAME", "t"}, {"GIT_COMMITTER_EMAIL", "t@e.x"}} {
		t.Setenv(kv[0], kv[1])
	}
	root := t.TempDir()
	origin, a, b := filepath.Join(root, "o.git"), filepath.Join(root, "a"), filepath.Join(root, "b")
	run(t, root, "init", "--bare", "-b", "main", origin)
	run(t, root, "clone", "-q", origin, a)
	os.WriteFile(filepath.Join(a, "f"), []byte("1"), 0o644)
	run(t, a, "add", ".")
	run(t, a, "commit", "-qm", "1")
	run(t, a, "push", "-q", "origin", "HEAD:main")
	run(t, root, "clone", "-q", origin, b)
	os.WriteFile(filepath.Join(a, "f"), []byte("2"), 0o644)
	run(t, a, "commit", "-qam", "2")
	run(t, a, "push", "-q", "origin", "HEAD:main", "HEAD:refs/heads/feature")
	if err := FetchOrigin(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{"refs/remotes/origin/main", "refs/remotes/origin/feature"} {
		run(t, b, "rev-parse", "--verify", ref)
	}
	if got := string(mustRead(t, filepath.Join(b, "f"))); got != "1" {
		t.Fatalf("the working tree must stay as it was: %q", got)
	}
	if !FetchedWithin(b, time.Hour) {
		t.Fatal("a fetch should refresh the FETCH_HEAD age")
	}
	if left, _ := filepath.Glob(filepath.Join(os.TempDir(), "drupal-updater-fetch-*")); len(left) > 0 {
		t.Logf("scratch repositories left behind: %v", left)
	}
}

// the fetch only accepts https/ssh remotes in production; the tests use local repositories
func TestMain(m *testing.M) {
	os.Setenv("DU_ALLOW_LOCAL_REMOTES", "1")
	os.Exit(m.Run())
}

// A partial-clone "promisor" remote in the project's config must not run anything when git reads a missing object.
func TestMissingObjectsDoNotStartALazyFetch(t *testing.T) {
	repo := t.TempDir()
	run(t, repo, "init", "-q", "-b", "main")
	marker := filepath.Join(t.TempDir(), "ran")
	run(t, repo, "config", "core.repositoryformatversion", "1")
	run(t, repo, "config", "extensions.partialClone", "evil")
	run(t, repo, "config", "remote.evil.promisor", "true")
	script := marker + "-cmd"
	os.WriteFile(script, []byte("#!/bin/sh\ntouch "+marker+"\n"), 0o755)
	run(t, repo, "config", "remote.evil.url", "ext::"+script)
	run(t, repo, "config", "protocol.ext.allow", "always")
	// a branch pointing at an object that does not exist
	os.MkdirAll(filepath.Join(repo, ".git", "refs", "remotes", "origin"), 0o755)
	os.WriteFile(filepath.Join(repo, ".git", "refs", "remotes", "origin", "main"), []byte("1111111111111111111111111111111111111111\n"), 0o644)

	probe := exec.Command("git", "show", "refs/remotes/origin/main:composer.lock") // what git does without the guard
	probe.Dir = repo
	probe.Run()
	dangerous := exists(marker)
	os.Remove(marker)

	RemoteStartFile(context.Background(), repo, "main", "composer.lock")
	if exists(marker) {
		t.Fatal("reading a missing object ran the promisor remote's command")
	}
	if !dangerous {
		t.Fatal("the setup is not dangerous on this git, so this test would prove nothing")
	}
}

func TestFetchKeepsAShallowBoundary(t *testing.T) {
	for _, kv := range [][2]string{{"GIT_AUTHOR_NAME", "t"}, {"GIT_AUTHOR_EMAIL", "t@e.x"}, {"GIT_COMMITTER_NAME", "t"}, {"GIT_COMMITTER_EMAIL", "t@e.x"}} {
		t.Setenv(kv[0], kv[1])
	}
	root := t.TempDir()
	origin, a, b := filepath.Join(root, "o.git"), filepath.Join(root, "a"), filepath.Join(root, "b")
	run(t, root, "init", "--bare", "-b", "main", origin)
	run(t, root, "clone", "-q", origin, a)
	for _, v := range []string{"1", "2", "3"} {
		os.WriteFile(filepath.Join(a, "f"), []byte(v), 0o644)
		run(t, a, "add", ".")
		run(t, a, "commit", "-qm", v)
	}
	run(t, a, "push", "-q", "origin", "HEAD:main")
	run(t, root, "clone", "-q", "--depth", "1", "file://"+origin, b)
	os.WriteFile(filepath.Join(a, "f"), []byte("4"), 0o644)
	run(t, a, "commit", "-qam", "4")
	run(t, a, "push", "-q", "origin", "HEAD:main")
	if err := FetchOrigin(context.Background(), b); err != nil {
		t.Fatalf("a shallow clone must still fetch: %v", err)
	}
}
