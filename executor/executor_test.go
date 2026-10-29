package executor

import (
	"context"
	"os"
	"os/exec"
	"testing"
)

// shellCmd runs a shell snippet for test setup.
func shellCmd(_ context.Context, c, dir string) (string, error) {
	cmd := exec.Command("sh", "-c", c)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
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
