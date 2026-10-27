package executor

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestBuildCommands(t *testing.T) {
	got := BuildCommands([]string{"git pull --rebase", "ddev start"}, "main", "tmp")
	want := []string{"git checkout main", "git pull --rebase", "git checkout -B tmp", "ddev start"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}

func TestHeal(t *testing.T) {
	cases := []struct {
		out   string
		fix   string
		retry bool
	}{
		{"error: Your local changes to the following files would be overwritten by checkout", stashCmd, true},
		// messages taken from ~/.config/drupal-updater/updater.log
		{"error: Your local changes to the following files would be overwritten by checkout:\n\t.gitignore\nPlease commit your changes or stash them before you switch branches.", stashCmd, true},
		{"error: cannot pull with rebase: Your index contains uncommitted changes.", stashCmd, true},
		{"There is no tracking information for the current branch.\nPlease specify which branch you want to rebase against.", upstreamFixCmd, true},
		{"Failed to start baru: failed to update router network aliases: failed to disconnect container 29df from network ddev_default: Error response from daemon: container 29df is not connected to network ddev_default", "", true},
		{"unable to start project x because the configured database type does not match", "", false},
		{"- Required package \"a/b\" is not present in the lock file.", "", false},
		{"curl error 6: Could not resolve host: repo.packagist.org", "", true},
		{"fatal: pathspec 'x' did not match", "", false},
	}
	for _, c := range cases {
		if fix, retry := heal(c.out); fix != c.fix || retry != c.retry {
			t.Errorf("%q: got (%q,%v)", c.out, fix, retry)
		}
	}
}

func TestCleanupDirtyTree(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	for _, c := range []string{
		"git init -q -b main && git config user.email a@b.c && git config user.name t",
		"echo a > f && git add f && git commit -qm init",
		"git checkout -qB tmp && echo b > f", // dirty change on tmp, would block checkout? same file differs in tmp vs main only if committed
		"git commit -qam tmpchange && git checkout -q main && git checkout -q tmp && echo dirty > f",
	} {
		if out, err := shellCmd(ctx, c, dir); err != nil {
			t.Fatalf("%s: %v %s", c, err, out)
		}
	}
	if err := Cleanup(dir, "main", "tmp"); err != nil {
		t.Fatal(err)
	}
	if out, _ := shellCmd(ctx, "git branch --show-current; git branch --list tmp", dir); out != "main\n" {
		t.Fatalf("got %q", out)
	}
}

func TestRenameBranchRejectsBadNames(t *testing.T) {
	dir := t.TempDir()
	shellCmd(context.Background(), "git init -q -b main && git config user.email a@b.c && git config user.name t && git commit -q --allow-empty -m i && git branch tmp", dir)
	for _, bad := range []string{"--delete", "a b", "x;touch pwned", "a..b"} {
		if err := RenameBranch(dir, "tmp", bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if err := RenameBranch(dir, "tmp", "hotfix/ok"); err != nil {
		t.Fatal(err)
	}
}

func gitRepo(t *testing.T, setup string) string {
	t.Helper()
	dir := t.TempDir()
	if out, err := shellCmd(context.Background(), "git init -q -b main && git config user.email a@b.c && git config user.name t && git commit -q --allow-empty -m i && "+setup, dir); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	return dir
}

func TestResolveStartBranch(t *testing.T) {
	dir := gitRepo(t, "true")
	if got := ResolveStartBranch(dir, "master"); got != "main" {
		t.Errorf("got %q", got)
	}
	if got := ResolveStartBranch(dir, "main"); got != "main" {
		t.Errorf("got %q", got)
	}
}

func TestPruneStashes(t *testing.T) {
	dir := gitRepo(t, "echo x > f && git add f && git commit -qm f && echo y > f && git stash push -q -m "+stashMsg+" && echo z > f && git stash push -q -m mine")
	PruneStashes(dir) // fresh: nothing dropped
	if out, _ := shellCmd(context.Background(), "git stash list | wc -l", dir); strings.TrimSpace(out) != "2" {
		t.Fatalf("fresh stashes dropped: %q", out)
	}
	old := time.Now().Add(-stashMaxAge - time.Hour).Format(time.RFC3339)
	// backdate every stash entry by rewriting reflog timestamps is brittle; use GIT_COMMITTER_DATE on a new stash instead
	shellCmd(context.Background(), "echo w > f && GIT_COMMITTER_DATE="+old+" git stash push -q -m "+stashMsg, dir)
	PruneStashes(dir)
	out, _ := shellCmd(context.Background(), "git stash list", dir)
	if strings.Count(out, "\n") != 2 || !strings.Contains(out, "mine") {
		t.Fatalf("got %q", out)
	}
}

func TestCleanupAndResolveRejectUnsafeNames(t *testing.T) {
	dir := gitRepo(t, "git branch 'x;touch${IFS}pwned'")
	if got := ResolveStartBranch(dir, "nope"); got != "main" {
		t.Errorf("got %q", got)
	}
	if err := Cleanup(dir, "main", "a;touch${IFS}pwned"); err == nil {
		t.Error("unsafe temp branch accepted")
	}
	if _, err := os.Stat(dir + "/pwned"); err == nil {
		t.Error("injection ran")
	}
}

func TestTempBranch(t *testing.T) {
	for in, want := range map[string]string{
		"My Site":    "drupal-updater/temp-my-site",
		"a..b__c":    "drupal-updater/temp-a-b-c",
		"-x-":        "drupal-updater/temp-x",
		"site-2.org": "drupal-updater/temp-site-2-org",
	} {
		if got := TempBranch(in); got != want {
			t.Errorf("TempBranch(%q) = %q, want %q", in, got, want)
		}
	}
}
