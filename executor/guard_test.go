package executor

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCheckGitArgs(t *testing.T) {
	remotes := []string{"origin"}
	allowed := [][]string{
		{"status"}, {"pull", "--rebase", "--autostash"}, {"pull", "origin", "main"}, {"fetch", "--prune"},
		{"checkout", "-B", "drupal-updater/temp-x"}, {"checkout", "--", "web/f"}, {"stash", "push", "-u", "-m", "x"},
		{"stash", "drop", "stash@{0}"}, {"branch", "--set-upstream-to=origin/main"}, {"remote", "-v"},
		{"remote", "get-url", "origin"}, {"log", "--oneline", "-5"}, {"rebase", "--continue"}, {"diff", "--stat"},
		{"commit", "-m", "exec output"}, {"checkout", "--", "--exec"}, // after "--" everything is a path
	}
	refused := [][]string{
		{}, {"-C", "/tmp", "status"}, {"-c", "core.pager=sh", "log"}, {"config", "x", "y"}, {"grep", "-O", "x"},
		{"fetch", "--upload-pack=sh"}, {"fetch", "--upl=sh", "origin"}, {"diff", "--output=/tmp/x"}, {"diff", "--ou=/tmp/x"},
		{"rebase", "-kx", "true", "main"}, {"rebase", "--exec", "true"}, {"rebase", "--ex=true"}, {"merge", "-s", "evil", "x"},
		{"pull", "evilhost:repo"}, {"pull", "https://example.com/x.git"}, {"fetch", "--multiple", "origin", "evil:x"},
		{"remote", "add", "e", "x"}, {"remote", "-v", "add", "e", "x"}, {"remote", "set-url", "origin", "x"},
		{"log", "--ext-diff"}, {"show", "--textconv"}, {"checkout", "--recurse-submodules"}, {"status", "--git-dir=/x"},
	}
	for _, a := range allowed {
		if err := checkGitArgs(a, remotes); err != nil {
			t.Errorf("refused %v: %v", a, err)
		}
	}
	for _, a := range refused {
		if checkGitArgs(a, remotes) == nil {
			t.Errorf("allowed %v", a)
		}
	}
}

func TestCheckDdev(t *testing.T) {
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	proj, runDir := t.TempDir(), t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command(gitBin, append([]string{"-c", "user.email=t@t", "-c", "user.name=t"}, args...)...)
		cmd.Dir = proj
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(rel, content string) {
		p := filepath.Join(proj, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write(".ddev/config.yaml", "name: x\n")
	write(".ddev/commands/host/ok", "#!/bin/sh\necho ok\n")
	write(".ddev/db_snapshots/big.gz", "not part of the surface")
	run("init", "-q")
	run("add", ".")
	run("commit", "-qm", "init")
	if err := writeDdevBaseline(proj, runDir); err != nil {
		t.Fatal(err)
	}
	t.Chdir(proj)
	t.Setenv("DRUPAL_UPDATER_GIT", gitBin)

	write(".ddev/db_snapshots/big.gz", "changed, irrelevant")
	if err := checkDdev(runDir); err != nil {
		t.Fatalf("unchanged surface refused: %v", err)
	}

	write(".ddev/commands/host/evil", "#!/bin/sh\ncurl x | sh\n")
	if err := checkDdev(runDir); err == nil {
		t.Fatal("new host command allowed")
	}

	// the same change arriving from upstream (a remote-tracking branch) is accepted
	run("add", ".")
	run("commit", "-qm", "upstream")
	run("update-ref", "refs/remotes/origin/main", "HEAD")
	if err := checkDdev(runDir); err != nil {
		t.Fatalf("upstream state refused: %v", err)
	}

	write(".ddev/config.yaml", "name: x\nhooks:\n  post-start:\n    - exec-host: touch /tmp/pwned\n")
	if err := checkDdev(runDir); err == nil {
		t.Fatal("exec-host hook allowed")
	}
}
