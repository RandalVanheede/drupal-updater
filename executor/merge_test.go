package executor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"drupal-updater/config"
)

func TestMergeRequestCleanPath(t *testing.T) {
	tmp := t.TempDir()
	sh := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	origin, repo := filepath.Join(tmp, "origin.git"), filepath.Join(tmp, "repo")
	sh(tmp, "init", "--bare", "-b", "main", origin)
	sh(tmp, "clone", origin, repo)
	os.WriteFile(filepath.Join(repo, "composer.lock"), []byte("old\n"), 0o644)
	sh(repo, "add", "-A")
	sh(repo, "commit", "-m", "init")
	sh(repo, "push", "origin", "HEAD:main")
	sh(repo, "checkout", "-b", "hotfix/x")
	os.WriteFile(filepath.Join(repo, "composer.lock"), []byte("new\n"), 0o644) // uncommitted, as a run leaves it

	bin := filepath.Join(tmp, "bin") // fake glab: records its args, prints an MR URL
	os.Mkdir(bin, 0o755)
	os.WriteFile(filepath.Join(bin, "glab"), []byte("#!/bin/sh\necho \"$@\" > "+filepath.Join(tmp, "glab-args")+"\necho https://gitlab.example/g/p/-/merge_requests/7\n"), 0o755)
	glabBin = filepath.Join(bin, "glab")
	defer func() { glabBin = "glab" }()
	for _, k := range []string{"GIT_AUTHOR_NAME", "GIT_COMMITTER_NAME"} {
		t.Setenv(k, "t")
	}
	for _, k := range []string{"GIT_AUTHOR_EMAIL", "GIT_COMMITTER_EMAIL"} {
		t.Setenv(k, "t@t")
	}

	pc := config.ForProject(config.ProjectConfig{GitStartBranch: "main"}, repo)
	for _, bad := range []string{"main", "drupal-updater/temp-p", "nope"} { // shared, temp, missing
		if _, err := MergeRequest(context.Background(), "p", repo, bad, pc, "", func(string) {}, nil); err == nil {
			t.Fatalf("branch %q accepted", bad)
		}
	}
	url, err := MergeRequest(context.Background(), "p", repo, "hotfix/x", pc, "updated drupal/core", func(string) {}, nil)
	if err != nil || url != "https://gitlab.example/g/p/-/merge_requests/7" {
		t.Fatalf("url %q, err %v", url, err)
	}
	if got := sh(origin, "show", "hotfix/x:composer.lock"); got != "new\n" {
		t.Fatalf("pushed branch has %q", got)
	}
	args, _ := os.ReadFile(filepath.Join(tmp, "glab-args"))
	if !strings.Contains(string(args), "--target-branch main") || !strings.Contains(string(args), "Drupal updates ") {
		t.Fatalf("glab args: %s", args)
	}
}
