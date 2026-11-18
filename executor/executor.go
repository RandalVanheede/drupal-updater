package executor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"drupal-updater/logging"
)

// loginShell returns the user's default shell, falling back to /bin/sh.
// Using a login shell ensures .profile/.bash_profile/.zshrc are sourced.
func loginShell() string {
	if shell := os.Getenv("SHELL"); shell != "" {
		return shell
	}
	return "/bin/sh"
}

// EnrichedEnv returns the current environment with common binary paths (Homebrew,
// Linuxbrew, ~/.local/bin) added to PATH and ensures SSH_AUTH_SOCK is available.
// This is needed because GUI apps on macOS don't inherit the shell's PATH or SSH agent.
func EnrichedEnv() []string {
	extraPaths := []string{
		"/opt/homebrew/bin",
		"/opt/homebrew/sbin",
		"/usr/local/bin",
		"/usr/local/sbin",
		"/home/linuxbrew/.linuxbrew/bin",
		os.Getenv("HOME") + "/.local/bin",
	}
	// Never let a tool wait on a prompt nobody can answer.
	// GIT_NO_LAZY_FETCH: a partial-clone "promisor" remote in a project's .git/config (writable from the ddev containers)
	// would otherwise make git run a program, or reach a server, whenever it reads an object that is missing.
	env := append(os.Environ(), "COMPOSER_NO_INTERACTION=1", "GIT_TERMINAL_PROMPT=0", "DDEV_NONINTERACTIVE=true", "GIT_NO_LAZY_FETCH=1")

	hasSSHAuth := false
	for i, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			current := strings.TrimPrefix(e, "PATH=")
			env[i] = "PATH=" + strings.Join(extraPaths, ":") + ":" + current
		}
		if strings.HasPrefix(e, "SSH_AUTH_SOCK=") {
			hasSSHAuth = true
		}
	}

	// If SSH_AUTH_SOCK is missing (common in macOS GUI apps), ask launchd; Linux desktop sessions already export it
	if !hasSSHAuth && runtime.GOOS == "darwin" {
		if sock := launchdSSHAuthSock(); sock != "" {
			env = append(env, "SSH_AUTH_SOCK="+sock)
		}
	}

	return env
}

// launchdSSHAuthSock asks launchd once per process: EnrichedEnv runs several times a second while updates run.
var launchdSSHAuthSock = sync.OnceValue(func() string {
	out, err := exec.Command("launchctl", "getenv", "SSH_AUTH_SOCK").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
})

const stashMsg = "drupal-updater-autostash"

var safeBranch = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)

var nonSlug = regexp.MustCompile(`[^a-z0-9-]+`)

// BranchSlug lowercases s and turns every run of other characters into one dash.
func BranchSlug(s string) string {
	return strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

// TempBranch is the branch a project's update runs on before it is renamed or cleaned up.
func TempBranch(project string) string { return "drupal-updater/temp-" + BranchSlug(project) }

// FinalBranch fills a branch pattern ({date} = today as YYYY-MM-DD, * = project slug) and appends
// -2, -3, ... while that branch exists locally or on origin, so a second run on one day gets its own name.
func FinalBranch(projectPath, pattern, project string) string {
	name := strings.NewReplacer("{date}", time.Now().Format("2006-01-02"), "*", BranchSlug(project)).Replace(pattern)
	for i, candidate := 2, name; ; i++ {
		_, local := git(projectPath, "rev-parse", "--verify", "--quiet", "refs/heads/"+candidate)
		_, remote := git(projectPath, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+candidate)
		if local != nil && remote != nil {
			return candidate
		}
		candidate = fmt.Sprintf("%s-%d", name, i)
	}
}

// git runs git directly (no shell) so branch names can't inject commands.
func git(dir string, args ...string) (string, error) {
	// hooks off: .git is writable from the ddev containers, so a planted hook would run on the host
	cmd := exec.Command("git", append([]string{"-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false"}, args...)...)
	cmd.Dir = dir
	cmd.Env = EnrichedEnv()
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// ResolveStartBranch takes configured as a "|"-separated list ("master|main") and
// returns the first that exists locally, else the remote's default branch
// (origin/HEAD), else main/master, else the first listed name unchanged.
// Only names matching safeBranch are ever returned from repo-derived data.
func ResolveStartBranch(projectPath, configured string) string {
	exists := func(b string) bool {
		if !safeBranch.MatchString(b) {
			return false
		}
		_, err := git(projectPath, "rev-parse", "--verify", "--quiet", "refs/heads/"+b)
		return err == nil
	}
	names := strings.Split(configured, "|")
	for i, b := range names {
		names[i] = strings.TrimSpace(b)
		if exists(names[i]) {
			return names[i]
		}
	}
	if out, err := git(projectPath, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		if b := strings.TrimPrefix(strings.TrimSpace(out), "origin/"); exists(b) {
			return b
		}
	}
	for _, b := range []string{"main", "master"} {
		if exists(b) {
			return b
		}
	}
	return names[0]
}

// RenameBranch renames oldBranch to newBranch without a shell. newBranch is
// validated with git check-ref-format so user input can't inject options or commands.
func RenameBranch(projectPath, oldBranch, newBranch string) error {
	if strings.HasPrefix(newBranch, "-") {
		return fmt.Errorf("invalid branch name %q", newBranch)
	}
	check := exec.Command("git", "check-ref-format", "--branch", newBranch)
	check.Dir = projectPath
	if err := check.Run(); err != nil {
		return fmt.Errorf("invalid branch name %q", newBranch)
	}
	if out, err := git(projectPath, "branch", "-m", "--", oldBranch, newBranch); err != nil {
		return fmt.Errorf("git branch -m failed: %v\n%s", err, out)
	}
	return nil
}

// CleanupKeepWork is Cleanup for a run that failed: when tempBranch holds commits that startBranch does not,
// it is renamed to drupal-updater/failed-<project>-<time> instead of deleted, and that name is returned
// (empty when nothing was worth keeping), so a timeout or a bad result never throws away finished work.
func CleanupKeepWork(projectPath, startBranch, tempBranch string) (string, error) {
	resolved := ResolveStartBranch(projectPath, startBranch)
	if tempBranch == "" || !safeBranch.MatchString(resolved) || !safeBranch.MatchString(tempBranch) {
		return "", Cleanup(projectPath, startBranch, tempBranch)
	}
	// Fully qualified refs (a tag named like the branch would make a short name ambiguous) and stdout only (git warnings
	// go to stderr). Only a count that is provably zero allows deleting: when in doubt the work is kept.
	// show-ref exits 1 only when the branch provably does not exist; any other failure falls through and keeps the work
	var ee *exec.ExitError
	if err := gitCmd(context.Background(), projectPath, EnrichedEnv(), "show-ref", "--verify", "--quiet", "refs/heads/"+tempBranch).Run(); errors.As(err, &ee) && ee.ExitCode() == 1 {
		return "", Cleanup(projectPath, startBranch, "")
	}
	cmd := gitCmd(context.Background(), projectPath, EnrichedEnv(), "rev-list", "--count", "refs/heads/"+resolved+"..refs/heads/"+tempBranch, "--")
	raw, err := cmd.Output()
	if n, convErr := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil && convErr == nil && n == 0 {
		return "", Cleanup(projectPath, startBranch, tempBranch)
	}
	if err := Cleanup(projectPath, startBranch, ""); err != nil { // back on the start branch, temp branch untouched
		return "", err
	}
	kept := "drupal-updater/failed-" + strings.TrimPrefix(tempBranch, "drupal-updater/temp-") + "-" + time.Now().Format("20060102-150405")
	if out, err := git(projectPath, "branch", "-m", "--", tempBranch, kept); err != nil {
		return "", fmt.Errorf("could not keep %s: %v\n%s", tempBranch, err, out)
	}
	return kept, nil
}

// Cleanup returns the project to startBranch (stashing a dirty tree if needed)
// and deletes tempBranch. A missing temp branch is not an error.
func Cleanup(projectPath, startBranch, tempBranch string) error {
	startBranch = ResolveStartBranch(projectPath, startBranch)
	if !safeBranch.MatchString(startBranch) || (tempBranch != "" && !safeBranch.MatchString(tempBranch)) {
		return fmt.Errorf("refusing unsafe branch name %q / %q", startBranch, tempBranch)
	}
	if _, err := git(projectPath, "checkout", startBranch, "--"); err != nil {
		git(projectPath, "stash", "push", "-u", "-m", stashMsg)
		if out, err := git(projectPath, "checkout", startBranch, "--"); err != nil {
			return fmt.Errorf("git checkout %s failed: %v\n%s", startBranch, err, out)
		}
	}
	if tempBranch != "" {
		git(projectPath, "branch", "-D", "--", tempBranch) // best-effort: may not exist
	}
	return nil
}

// StatusUpdate represents a status change for a project
type StatusUpdate struct {
	ProjectName string
	Status      string // running, success, failed, cancelled, skipped (nothing to update; the project is back on its start branch)
	Command     string
	Output      string
	Screen      string // latest snapshot of the Claude session's terminal; replaces the previous one, not part of the log
}

// gibPerProject is roughly what one project's ddev containers plus composer need while updating.
const gibPerProject = 2.5

// ConcurrencyCap lowers n to what Docker's memory can hold (about 2.5 GiB per project), so a high
// max_concurrent does not make every run crawl or get killed. Without docker info it returns n unchanged.
func ConcurrencyCap(n int) int {
	if n < 1 {
		n = 3 // the config default
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second) // a hung Docker must not hold up a start
	defer cancel()
	out, err := exec.CommandContext(ctx, lookEnriched("docker"), "info", "--format", "{{.MemTotal}}").Output()
	if err != nil {
		return n
	}
	mem, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil || mem <= 0 {
		return n
	}
	return capFor(n, mem)
}

func capFor(n int, memBytes float64) int {
	limit := int(memBytes / (gibPerProject * (1 << 30)))
	if limit < 1 {
		limit = 1
	}
	if n > limit {
		logging.Info("max_concurrent %d lowered to %d: Docker has %.1f GiB of memory", n, limit, memBytes/(1<<30))
		return limit
	}
	return n
}
