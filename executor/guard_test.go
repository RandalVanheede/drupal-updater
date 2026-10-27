package executor

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	if err := writeGuardState(gitBin, proj, runDir); err != nil {
		t.Fatal(err)
	}

	write(".ddev/db_snapshots/big.gz", "changed, irrelevant")
	if err := checkDdevErr(proj, runDir); err != nil {
		t.Fatalf("unchanged surface refused: %v", err)
	}

	write(".ddev/commands/host/evil", "#!/bin/sh\ncurl x | sh\n")
	if err := checkDdevErr(proj, runDir); err == nil {
		t.Fatal("new host command allowed")
	}

	// a remote ref written directly (as code in a container could) is not trusted...
	run("add", ".")
	run("commit", "-qm", "upstream")
	run("update-ref", "refs/remotes/origin/main", "HEAD")
	if err := checkDdevErr(proj, runDir); err == nil {
		t.Fatal("forged remote ref trusted")
	}
	// ...only what the guard's own fetch/pull brought in
	if err := trustRemoteRefs(gitBin, proj, runDir); err != nil {
		t.Fatal(err)
	}
	if err := checkDdevErr(proj, runDir); err != nil {
		t.Fatalf("fetched upstream state refused: %v", err)
	}

	write(".ddev/config.yaml", "name: x\nhooks:\n  post-start:\n    - exec-host: touch /tmp/pwned\n")
	if err := checkDdevErr(proj, runDir); err == nil {
		t.Fatal("exec-host hook allowed")
	}
	write(".ddev/config.yaml", "name: x\n")

	// host commands swapped for a symlink to a folder outside .ddev
	os.RemoveAll(filepath.Join(proj, ".ddev/commands/host"))
	write("evil/x", "#!/bin/sh\n")
	os.Symlink("../../evil", filepath.Join(proj, ".ddev/commands/host"))
	if err := checkDdevErr(proj, runDir); err == nil {
		t.Fatal("symlinked host commands allowed")
	}
}

func TestGuardGitConfigAndCwd(t *testing.T) {
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	proj, runDir := t.TempDir(), t.TempDir()
	cmd := exec.Command(gitBin, "init", "-q")
	cmd.Dir = proj
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	if err := writeGuardState(gitBin, proj, runDir); err != nil {
		t.Fatal(err)
	}
	if err := checkGitConfig(gitBin, proj, runDir); err != nil {
		t.Fatalf("unchanged config refused: %v", err)
	}
	f, _ := os.OpenFile(filepath.Join(proj, ".git", "config"), os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("[core]\n\tfsmonitor = touch /tmp/pwned\n")
	f.Close()
	if checkGitConfig(gitBin, proj, runDir) == nil {
		t.Fatal("tampered .git/config allowed")
	}

	t.Chdir(filepath.Join(proj, ".git"))
	if err := checkCwd(proj); err != nil {
		t.Fatalf("subdir refused: %v", err)
	}
	t.Chdir(t.TempDir())
	if checkCwd(proj) == nil {
		t.Fatal("cwd outside project allowed")
	}
}

func TestUpdateGitConfigAcceptsOnlyBranchChanges(t *testing.T) {
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	proj, runDir := t.TempDir(), t.TempDir()
	git := func(args ...string) {
		cmd := exec.Command(gitBin, args...)
		cmd.Dir = proj
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	git("init", "-q")
	if err := saveGitConfig(gitBin, proj, runDir); err != nil {
		t.Fatal(err)
	}
	git("config", "branch.main.remote", "origin")
	if err := updateGitConfig(gitBin, proj, runDir); err != nil {
		t.Fatalf("branch.* change refused: %v", err)
	}
	if err := checkGitConfig(gitBin, proj, runDir); err != nil {
		t.Fatalf("snapshot not updated: %v", err)
	}
	git("config", "core.sshCommand", "touch /tmp/pwned")
	if updateGitConfig(gitBin, proj, runDir) == nil {
		t.Fatal("core.sshCommand change accepted")
	}
	if checkGitConfig(gitBin, proj, runDir) == nil {
		t.Fatal("snapshot updated despite unexpected change")
	}
}

func TestCheckDdevMixesLocalAndUpstreamFiles(t *testing.T) {
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
		os.MkdirAll(filepath.Dir(filepath.Join(proj, rel)), 0o755)
		os.WriteFile(filepath.Join(proj, rel), []byte(content), 0o644)
	}
	write(".ddev/config.yaml", "database: mariadb\n")
	run("init", "-q")
	run("add", ".")
	run("commit", "-qm", "init")
	run("update-ref", "refs/remotes/origin/master", "HEAD")
	// run starts with a local edit and a local-only (ignored) config file
	write(".ddev/config.yaml", "database: mysql\n")
	write(".ddev/config.local.yaml", "nodejs_version: 24\n")
	if err := writeGuardState(gitBin, proj, runDir); err != nil {
		t.Fatal(err)
	}
	// stash/pull put the upstream config.yaml back; the local-only file stays
	write(".ddev/config.yaml", "database: mariadb\n")
	if err := checkDdevErr(proj, runDir); err != nil {
		t.Fatalf("upstream config + local-only file refused: %v", err)
	}
	// and the user's own edit coming back (stash pop) is fine too
	write(".ddev/config.yaml", "database: mysql\n")
	if err := checkDdevErr(proj, runDir); err != nil {
		t.Fatalf("restored local edit refused: %v", err)
	}
	write(".ddev/config.local.yaml", "hooks:\n  post-start:\n    - exec-host: touch /tmp/pwned\n")
	if checkDdevErr(proj, runDir) == nil {
		t.Fatal("planted hook in local-only file allowed")
	}
}

func TestTrustGeneratedOnlyRewrittenExistingFiles(t *testing.T) {
	proj, runDir := t.TempDir(), t.TempDir()
	write := func(rel, content string) {
		os.MkdirAll(filepath.Dir(filepath.Join(proj, rel)), 0o755)
		os.WriteFile(filepath.Join(proj, rel), []byte(content), 0o644)
	}
	write(".ddev/providers/acquia.yaml", "#ddev-generated\nv1\n")
	if err := writeReadOnly(filepath.Join(runDir, ddevTrustedFile), []byte("[{}]")); err != nil {
		t.Fatal(err)
	}
	before, _ := workingSurface(proj)                                   // in Guard this is the surface checkDdev verified
	write(".ddev/providers/acquia.yaml", "#ddev-generated\nv2\n")       // ddev upgrade rewrite
	write(".ddev/commands/host/evil", "#ddev-generated\ncurl x | sh\n") // new file during start
	if err := trustGenerated(proj, runDir, before); err != nil {
		t.Fatal(err)
	}
	err := checkDdevErr(proj, runDir)
	if err == nil || !strings.Contains(err.Error(), "commands/host/evil") || strings.Contains(err.Error(), "acquia") {
		t.Fatalf("want only the new host command refused, got %v", err)
	}
}

func checkDdevErr(root, runDir string) error {
	_, err := checkDdev(root, runDir)
	return err
}
