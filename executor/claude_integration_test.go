//go:build integration

package executor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"drupal-updater/config"
)

// testPrompt runs a fake step (%s) instead of the default ddev/composer steps.
const testPrompt = `Autonomous run in {{path}} (project {{project}}), never ask questions. Start branch {{start_branch}}, temp branch {{temp_branch}}.
Run these steps in order, one command per tool call:
1. git checkout {{start_branch}}
2. git pull --rebase --autostash
3. git checkout -B {{temp_branch}}
4. %s
Do NOT commit. NEVER git push/reset --hard/clean. If a step fails and cannot be fixed simply, stop.
Finish by writing {{result_file}} with JSON only: {"status": "success"|"failed", "summary": "<short>"}.
On success be on branch {{temp_branch}}. On failure first return to {{start_branch}} and delete {{temp_branch}}, then write the file.`

// Real Claude run on a throwaway repo. Needs claude+tmux; set DU_E2E_DIR to a
// directory inside a folder Claude already trusts. Run: go test -tags integration -run Claude ./executor
func TestClaudeEndToEnd(t *testing.T) {
	base := os.Getenv("DU_E2E_DIR")
	if base == "" {
		t.Skip("DU_E2E_DIR not set")
	}
	os.RemoveAll(base)
	os.MkdirAll(base, 0o755)
	sh := func(dir, c string) {
		cmd := exec.Command("sh", "-c", c)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v %s", c, err, out)
		}
	}
	sh(base, "git init -q --bare -b main remote.git && git clone -q remote.git proj && cd proj && git config user.email a@b.c && git config user.name t && echo a > f && git add f && git commit -qm init && git push -q origin main")
	proj := base + "/proj"

	Configure(&config.Config{ClaudeTimeoutMinutes: 10, ClaudePrompt: fmt.Sprintf(testPrompt, "echo updated > UPDATED.txt")})
	out := make(chan StatusUpdate, 100)
	ctx, cancel := context.WithTimeout(context.Background(), 11*time.Minute)
	defer cancel()
	go func() { Run(ctx, "e2e", proj, "main", "drupal-updater/temp-e2e", out) }()
	var last StatusUpdate
	for u := range out {
		t.Logf("%s | %s | %s", u.Status, u.Command, strings.TrimSpace(u.Output))
		last = u
		if u.Status != "running" {
			break
		}
	}
	if last.Status != "success" {
		t.Fatalf("final status %q", last.Status)
	}
	if b, _ := git(proj, "branch", "--show-current"); strings.TrimSpace(b) != "drupal-updater/temp-e2e" {
		t.Errorf("on branch %q", b)
	}
	if _, err := os.Stat(proj + "/UPDATED.txt"); err != nil {
		t.Error("UPDATED.txt missing")
	}
}

func TestClaudeFailurePath(t *testing.T) {
	base := os.Getenv("DU_E2E_DIR")
	if base == "" {
		t.Skip("DU_E2E_DIR not set")
	}
	proj := base + "/proj"
	exec.Command("git", "-C", proj, "checkout", "-q", "main").Run()
	exec.Command("git", "-C", proj, "branch", "-D", "drupal-updater/temp-fail").Run()
	Configure(&config.Config{ClaudeTimeoutMinutes: 10, ClaudePrompt: fmt.Sprintf(testPrompt, "composer-does-not-exist install --no-interaction")})
	out := make(chan StatusUpdate, 100)
	ctx, cancel := context.WithTimeout(context.Background(), 11*time.Minute)
	defer cancel()
	go func() { Run(ctx, "fail", proj, "main", "drupal-updater/temp-fail", out) }()
	var last StatusUpdate
	for u := range out {
		t.Logf("%s | %s | %s", u.Status, u.Command, strings.TrimSpace(u.Output))
		last = u
		if u.Status != "running" {
			break
		}
	}
	if last.Status != "failed" {
		t.Fatalf("final status %q", last.Status)
	}
	if b, _ := git(proj, "branch", "--show-current"); strings.TrimSpace(b) != "main" {
		t.Errorf("on branch %q, want main", b)
	}
	if b, _ := git(proj, "branch", "--list", "drupal-updater/temp-fail"); strings.TrimSpace(b) != "" {
		t.Errorf("temp branch left: %q", b)
	}
}
