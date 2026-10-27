package executor

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"drupal-updater/config"
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

// EnrichedEnv returns the current environment with common macOS binary paths
// added to PATH and ensures SSH_AUTH_SOCK is available. This is needed because
// GUI apps on macOS don't inherit the shell's PATH or SSH agent.
func EnrichedEnv() []string {
	extraPaths := []string{
		"/opt/homebrew/bin",
		"/opt/homebrew/sbin",
		"/usr/local/bin",
		"/usr/local/sbin",
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

	// If SSH_AUTH_SOCK is missing (common in macOS GUI apps), try to find it
	if !hasSSHAuth {
		// macOS launchd-managed agent socket
		sock := "/tmp/com.apple.launchd." // common prefix, but varies
		// Try the standard macOS SSH agent socket via launchctl
		out, err := exec.Command("launchctl", "getenv", "SSH_AUTH_SOCK").Output()
		if err == nil && len(out) > 0 {
			sock = strings.TrimSpace(string(out))
			env = append(env, "SSH_AUTH_SOCK="+sock)
		}
	}

	return env
}

const (
	retryBackoff   = 5 * time.Second
	stashMsg       = "drupal-updater-autostash"
	stashCmd       = "git stash push -u -m " + stashMsg
	stashMaxAge    = 14 * 24 * time.Hour
	upstreamFixCmd = "git branch --set-upstream-to=\"origin/$(git branch --show-current)\""
)

// Tunables, set once at startup via Configure.
var (
	maxAttempts = 3
	cmdTimeout  = 30 * time.Minute
)

// Configure applies retry/timeout/claude settings from the config; unset values keep defaults.
func Configure(cfg *config.Config) {
	if cfg.MaxAttempts > 0 {
		maxAttempts = cfg.MaxAttempts
	}
	if cfg.CommandTimeoutMinutes > 0 {
		cmdTimeout = time.Duration(cfg.CommandTimeoutMinutes) * time.Minute
	}
	configureClaude(cfg)
}

var safeBranch = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)

// git runs git directly (no shell) so branch names can't inject commands.
func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
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

// PruneStashes drops auto-heal stashes older than stashMaxAge.
func PruneStashes(projectPath string) {
	ctx := context.Background()
	out, err := shellCmd(ctx, "git stash list --format='%gd|%ct|%s'", projectPath)
	if err != nil {
		return
	}
	lines := strings.Split(strings.TrimSpace(out), "\n") // newest first, so dropping from the end keeps indexes valid
	for i := len(lines) - 1; i >= 0; i-- {
		parts := strings.SplitN(lines[i], "|", 3)
		if len(parts) < 3 || !strings.Contains(parts[2], stashMsg) {
			continue
		}
		if ts, err := strconv.ParseInt(parts[1], 10, 64); err == nil && time.Since(time.Unix(ts, 0)) > stashMaxAge {
			shellCmd(ctx, "git stash drop --quiet "+parts[0], projectPath)
		}
	}
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
	cmd := exec.Command("git", "branch", "-m", "--", oldBranch, newBranch)
	cmd.Dir = projectPath
	cmd.Env = EnrichedEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
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

// BuildCommands expands every "git pull*" command into: checkout start branch,
// the pull itself, then (re)create the temp branch. `checkout -B` resets an
// existing temp branch, so a leftover one from an earlier run is not an error.
func BuildCommands(commands []string, startBranch, tempBranch string) []string {
	var out []string
	for _, c := range commands {
		if strings.HasPrefix(c, "git pull") {
			out = append(out, "git checkout "+startBranch, c, "git checkout -B "+tempBranch)
		} else {
			out = append(out, c)
		}
	}
	return out
}

// heal inspects a failed command's output. fix is an optional command to run
// before the retry; retry reports whether another attempt is worthwhile.
// shortcut: substring matching on tool output, replace with structured checks if it gets flaky.
func heal(output string) (fix string, retry bool) {
	o := strings.ToLower(output)
	has := func(subs ...string) bool {
		for _, s := range subs {
			if strings.Contains(o, s) {
				return true
			}
		}
		return false
	}
	switch {
	case has("would be overwritten", "local changes", "unstaged changes", "uncommitted changes", "unmerged files", "commit or stash"):
		return stashCmd, true
	case has("no tracking information"):
		return upstreamFixCmd, true
	case has("router network aliases", "is not connected to network"): // ddev router race between parallel starts
		return "", true
	case has("index.lock", "could not resolve host", "timed out", "connection reset",
		"temporary failure", "could not read from remote", "curl error"):
		return "", true
	}
	return "", false
}

// ddevStartMu serializes `ddev start`/`stop`: parallel calls race on the shared ddev router.
var ddevStartMu sync.Mutex

func shellCmd(ctx context.Context, cmdStr, dir string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, cmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, loginShell(), "-l", "-c", cmdStr)
	cmd.Dir = dir
	cmd.Env = EnrichedEnv()
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}

// StatusUpdate represents a status change for a project
type StatusUpdate struct {
	ProjectName string
	Status      string // running, success, failed, cancelled
	Command     string
	Output      string
}

// Run executes commands sequentially for a project, sending updates to the channel.
// It respects context cancellation between commands and kills running processes.
// startBranch is used for cleanup if a failure occurs after a branch checkout.
func Run(ctx context.Context, projectName string, projectPath string, startBranch string, tempBranch string, commands []string, outChan chan<- StatusUpdate) {
	if claudeCfg.enabled {
		runClaude(ctx, projectName, projectPath, startBranch, tempBranch, commands, outChan)
		return
	}
	logging.Info("[%s] Starting execution (%d commands)", projectName, len(commands))

	outChan <- StatusUpdate{
		ProjectName: projectName,
		Status:      "running",
		Command:     "Starting...",
	}

	onTempBranch := false

	// Config says e.g. master but the repo uses main: fix up front instead of failing.
	if resolved := ResolveStartBranch(projectPath, startBranch); resolved != startBranch {
		logging.Info("[%s] Start branch %s not found, using %s", projectName, startBranch, resolved)
		for i, c := range commands {
			if c == "git checkout "+startBranch {
				commands[i] = "git checkout " + resolved
			}
		}
		startBranch = resolved
	}
	PruneStashes(projectPath)

	for _, cmdStr := range commands {
		// Check cancellation before starting next command
		select {
		case <-ctx.Done():
			logging.Info("[%s] Cancelled before command: %s", projectName, cmdStr)
			outChan <- StatusUpdate{
				ProjectName: projectName,
				Status:      "cancelled",
				Command:     cmdStr,
				Output:      "Cancelled",
			}
			return
		default:
		}

		logging.Info("[%s] Running: %s", projectName, cmdStr)

		outChan <- StatusUpdate{
			ProjectName: projectName,
			Status:      "running",
			Command:     cmdStr,
		}

		var output string
		var err error
		for attempt := 1; ; attempt++ {
			if strings.Contains(cmdStr, "ddev start") || strings.Contains(cmdStr, "ddev stop") {
				ddevStartMu.Lock()
				output, err = shellCmd(ctx, cmdStr, projectPath)
				ddevStartMu.Unlock()
			} else {
				output, err = shellCmd(ctx, cmdStr, projectPath)
			}
			if err == nil || ctx.Err() != nil || attempt >= maxAttempts {
				break
			}
			fix, retry := heal(output)
			if !retry {
				break
			}
			note := fmt.Sprintf("Attempt %d/%d failed, retrying", attempt, maxAttempts)
			if fix != "" {
				note += " after: " + fix
			}
			logging.Info("[%s] %s: %s\nOutput: %s", projectName, note, cmdStr, output)
			outChan <- StatusUpdate{ProjectName: projectName, Status: "running", Command: cmdStr, Output: note + "\n"}
			if fix != "" {
				if fixOut, fixErr := shellCmd(ctx, fix, projectPath); fixErr != nil {
					logging.Error("[%s] Auto-fix failed: %s\n%s", projectName, fix, fixOut)
				}
			}
			select {
			case <-ctx.Done():
			case <-time.After(retryBackoff * time.Duration(attempt)):
			}
		}

		if err == nil && strings.Contains(cmdStr, "git checkout -B ") {
			onTempBranch = true
		}

		if err != nil {
			if ctx.Err() != nil {
				logging.Info("[%s] Cancelled during command: %s", projectName, cmdStr)
				outChan <- StatusUpdate{
					ProjectName: projectName,
					Status:      "cancelled",
					Command:     cmdStr,
					Output:      "Cancelled",
				}
			} else {
				output = output + "\n" + err.Error()
				logging.Error("[%s] Failed command: %s\nOutput: %s", projectName, cmdStr, output)
				outChan <- StatusUpdate{
					ProjectName: projectName,
					Status:      "failed",
					Command:     cmdStr,
					Output:      output,
				}
				// Best-effort cleanup: go back to start branch if we branched off
				if onTempBranch && startBranch != "" {
					logging.Info("[%s] Attempting cleanup: git checkout %s", projectName, startBranch)
					Cleanup(projectPath, startBranch, "")
				}
			}
			return
		}

		logging.Info("[%s] Command succeeded: %s", projectName, cmdStr)
		if output != "" {
			outChan <- StatusUpdate{
				ProjectName: projectName,
				Status:      "running",
				Command:     cmdStr,
				Output:      output,
			}
		}
	}

	logging.Info("[%s] All commands completed successfully", projectName)
	outChan <- StatusUpdate{
		ProjectName: projectName,
		Status:      "success",
		Command:     "All commands completed successfully",
	}
}
