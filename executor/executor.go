package executor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
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
	env := append(os.Environ(), "COMPOSER_NO_INTERACTION=1", "GIT_TERMINAL_PROMPT=0", "DDEV_NONINTERACTIVE=true")

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
		out, err := exec.Command("launchctl", "getenv", "SSH_AUTH_SOCK").Output()
		if err == nil && len(out) > 0 {
			env = append(env, "SSH_AUTH_SOCK="+strings.TrimSpace(string(out)))
		}
	}

	return env
}

const stashMsg = "drupal-updater-autostash"

var safeBranch = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)

var nonSlug = regexp.MustCompile(`[^a-z0-9-]+`)

// BranchSlug lowercases s and turns every run of other characters into one dash.
func BranchSlug(s string) string {
	return strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

// TempBranch is the branch a project's update runs on before it is renamed or cleaned up.
func TempBranch(project string) string { return "drupal-updater/temp-" + BranchSlug(project) }

// git runs git directly (no shell) so branch names can't inject commands.
func git(dir string, args ...string) (string, error) {
	// hooks off: .git is writable from the ddev containers, so a planted hook would run on the host
	cmd := exec.Command("git", append([]string{"-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false"}, args...)...)
	cmd.Dir = dir
	cmd.Env = EnrichedEnv()
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// ResolveStartBranch returns configured if it exists locally, else the remote's
// default branch (origin/HEAD), else main/master, else configured unchanged.
// Only names matching safeBranch are ever returned from repo-derived data.
func ResolveStartBranch(projectPath, configured string) string {
	exists := func(b string) bool {
		if !safeBranch.MatchString(b) {
			return false
		}
		_, err := git(projectPath, "rev-parse", "--verify", "--quiet", "refs/heads/"+b)
		return err == nil
	}
	if exists(configured) {
		return configured
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
	return configured
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
	Status      string // running, success, failed, cancelled
	Command     string
	Output      string
	Screen      string // latest snapshot of the Claude session's terminal; replaces the previous one, not part of the log
}

// Run hands a project's update to a Claude session (see runClaude) and reports progress on outChan.
func Run(ctx context.Context, projectName, projectPath, startBranch, tempBranch string, outChan chan<- StatusUpdate) {
	runClaude(ctx, projectName, projectPath, startBranch, tempBranch, outChan)
}
