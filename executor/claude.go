package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"drupal-updater/config"
	"drupal-updater/logging"
)

const (
	defaultClaudeModel   = "sonnet"
	defaultClaudePerm    = "dontAsk" // unlisted tools are denied instead of prompting, so nothing can stall on approval
	defaultClaudeTimeout = 60 * time.Minute
	claudePoll           = 2 * time.Second
)

// DefaultClaudePrompt is used when claude_prompt is not set in the config.
// Placeholders: {{project}} {{path}} {{start_branch}} {{temp_branch}} {{ignored_packages}}
// {{project_instructions}} {{import_db}} {{result_file}} and {{commands}} (the classic command list).
const DefaultClaudePrompt = `You are updating the Drupal project "{{project}}" located at {{path}} (your working directory). Work fully autonomously: nobody is watching, so never ask questions.

Branches:
- Start branch: {{start_branch}}. If it does not exist locally, use the repository's real default branch (origin/HEAD, else main/master).
- Temp branch: {{temp_branch}}. All work happens on this branch.

Steps, in order. Run one command per tool call (do not chain commands with ; or &&):
1. git checkout {{start_branch}}, then git pull --rebase --autostash
2. git checkout -B {{temp_branch}} (this also resets the branch if it already exists)
3. Make sure ddev is running: ddev describe, and ddev start if it is not
4. ddev composer install
5. ddev drush deploy -y
6. ddev composer update 'drupal/core-*' 'symfony/*' 'twig/*' --with-all-dependencies
7. ddev drush updb -y
8. ddev drush cex -y
9. ddev stop

Packages that must NOT be updated: {{ignored_packages}}
{{project_instructions}}
Solve simple problems yourself and retry, for example:
- local changes blocking checkout/pull: git stash push -u -m drupal-updater-autostash, then retry
- branch has no upstream: set it with --set-upstream-to
- temp branch already exists, wrong default branch name, ddev router/network race, transient network errors: retry (up to 3 times)
- autostash pop conflict on a local-only config file (e.g. .ddev/*): keep the upstream version (git checkout -- <file>), then git stash drop that stash. If the stash holds anything else, leave it and mention it in the summary.
- ddev refuses to start because upstream renamed the project (name in .ddev/config.yaml differs from the one registered for this directory): ddev stop --unlist <old-name>, then ddev start. Then check the site boots (ddev drush status). If the database is missing or empty afterwards, try to restore one yourself: ddev snapshot restore, ddev pull if configured, or a recent dump in the project root or ./db via {{import_db}} <file> (the only permitted way to import; it rejects other locations and dumps older than 30 days). Report failure only if none works.
Be persistent. Diagnose every problem (read the error, inspect state, check logs) and fix it yourself, including composer conflicts, merge/stash conflicts, ddev/docker issues, config import errors and failing update hooks. Try several different approaches before giving up. Only report failure when the problem truly needs a human: missing credentials or access, a decision only the owner can make, or an action you are not permitted to take (see Rules). When you do fail, say exactly what you tried and what is needed.

Rules:
- Do NOT commit. Leave the changes uncommitted on the temp branch.
- NEVER run git push, git reset --hard, git clean, or force flags. Never delete any branch except {{temp_branch}}.
- Do not edit files other than what the steps above change (composer.json/lock, config sync files).

Finish by writing the file {{result_file}} containing JSON only:
{"status": "success" | "failed", "summary": "<one or two sentences: what was updated, or what failed and why>"}
On success you must be on branch {{temp_branch}} when you write it. On failure, first return to the start branch (stash a dirty tree if needed) and delete {{temp_branch}}, then write the file.`

var defaultClaudeAllowed = []string{
	"Read", "Grep", "Glob", "Edit", "Write",
	"Bash(git *)", "Bash(ddev *)", "Bash(composer *)", "Bash(drush *)", "Bash(sleep *)",
}

// ClaudeDefaults exposes the built-in prompt and allowlist for settings screens.
func ClaudeDefaults() (prompt string, allowedTools []string) {
	return DefaultClaudePrompt, append([]string(nil), defaultClaudeAllowed...)
}

// Always denied, regardless of config: these are never part of an update.
var claudeDenied = []string{
	"Bash(git push*)", "Bash(git reset --hard*)", "Bash(git clean*)", "Bash(git * --force*)", "Bash(git * -f *)",
	// arbitrary-code escape hatches the wildcard allows would otherwise grant
	"Bash(git -c*)", "Bash(git * -c *)", "Bash(git config*)", "Bash(git submodule*)",
	"Bash(composer exec*)", "Bash(composer run*)", "Bash(composer global*)", "Bash(composer config*)",
	"Bash(ddev exec*)", "Bash(ddev ssh*)", "Bash(ddev import-db*)", "Bash(ddev config*)",
	"Bash(git bisect*)", "Bash(git difftool*)", "Bash(git mergetool*)", "Bash(git * --exec*)", "Bash(git * -x*)", "Bash(git * --upload-pack*)",
	"Bash(ddev composer exec*)", "Bash(ddev composer run*)", "Bash(ddev mysql*)", "Bash(ddev launch*)",
	"Edit(.git/**)", "Write(.git/**)",
	"Bash(drush php*)", "Bash(drush ev*)", "Bash(drush sql*)", "Bash(ddev drush php*)", "Bash(ddev drush ev*)", "Bash(ddev drush sql*)",
}

// importDBScript is the only way Claude may import a database: ddev import-db stays denied, this wrapper
// accepts only a recent dump (regular file, no symlink) that sits in the project root or ./db.
const importDBScript = `#!/bin/sh
ROOT=%s
DAYS=%d
[ $# -eq 1 ] || { echo "usage: import-db.sh <dump file>" >&2; exit 2; }
f=$1
{ [ -f "$f" ] && [ ! -L "$f" ]; } || { echo "not a regular file: $f" >&2; exit 1; }
d=$(cd "$(dirname "$f")" && pwd -P) || exit 1
case "$d" in "$ROOT"|"$ROOT/db") ;; *) echo "dump must be in the project root or ./db" >&2; exit 1;; esac
case "$f" in *.sql|*.sql.gz|*.sql.zip|*.sql.xz|*.sql.bz2|*.mysql|*.mysql.gz) ;; *) echo "unsupported dump extension" >&2; exit 1;; esac
[ -n "$(find "$f" -mtime -"$DAYS")" ] || { echo "dump is older than $DAYS days" >&2; exit 1; }
cd "$ROOT" && exec ddev import-db --file="$d/$(basename "$f")"
`

const dumpMaxAgeDays = 30

var claudeCfg struct {
	enabled    bool
	model      string
	prompt     string
	allowed    []string
	permission string
	timeout    time.Duration
	projects   map[string]config.ProjectConfig
}

func orDefault(v, d string) string {
	if v != "" {
		return v
	}
	return d
}

// configureClaude copies the claude_* settings; mode "classic" disables the tmux/Claude runner.
func configureClaude(cfg *config.Config) {
	c := &claudeCfg
	c.enabled = cfg.Mode != "classic"
	c.model = orDefault(cfg.ClaudeModel, defaultClaudeModel)
	c.prompt = orDefault(cfg.ClaudePrompt, DefaultClaudePrompt)
	c.allowed = defaultClaudeAllowed
	if len(cfg.ClaudeAllowedTools) > 0 {
		c.allowed = cfg.ClaudeAllowedTools
	}
	c.permission = safePermissionMode(cfg.ClaudePermissionMode)
	c.projects = make(map[string]config.ProjectConfig, len(cfg.Projects))
	for k, v := range cfg.Projects {
		c.projects[k] = v
	}
	c.timeout = defaultClaudeTimeout
	if cfg.ClaudeTimeoutMinutes > 0 {
		c.timeout = time.Duration(cfg.ClaudeTimeoutMinutes) * time.Minute
	}
}

// safePermissionMode rejects modes that skip permission checks; anything else falls back to the default.
func safePermissionMode(m string) string {
	switch m {
	case "dontAsk", "acceptEdits", "plan", "manual":
		return m
	case "":
	default:
		logging.Error("claude_permission_mode %q not allowed, using %s", m, defaultClaudePerm)
	}
	return defaultClaudePerm
}

func sq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func safeName(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, s)
}

// tmuxPath prefers the copy bundled inside the .app, then a tmux on the enriched PATH.
// (exec.LookPath uses the process PATH, which is minimal for GUI apps.)
func tmuxPath() string {
	if exe, err := os.Executable(); err == nil {
		bundled := filepath.Join(filepath.Dir(exe), "..", "Resources", "tmux-bundle", "tmux")
		if _, err := os.Stat(bundled); err == nil {
			return bundled
		}
	}
	for _, e := range EnrichedEnv() {
		if v, ok := strings.CutPrefix(e, "PATH="); ok {
			for _, dir := range filepath.SplitList(v) {
				if p := filepath.Join(dir, "tmux"); isExecutable(p) {
					return p
				}
			}
		}
	}
	return "tmux"
}

func isExecutable(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir() && st.Mode()&0o111 != 0
}

// tmuxSocket is a private tmux server, so a bundled tmux never talks to (or conflicts in
// version with) the user's own tmux server.
const tmuxSocket = "drupal-updater"

func tmux(args ...string) (string, error) {
	cmd := exec.Command(tmuxPath(), append([]string{"-L", tmuxSocket}, args...)...)
	// A GUI app has no TERM. The macOS bundled ncurses can't see Homebrew's terminfo; Linux keeps its own (Debian: /lib/terminfo).
	cmd.Env = append(EnrichedEnv(), "TERM=xterm-256color")
	if runtime.GOOS == "darwin" {
		cmd.Env = append(cmd.Env, "TERMINFO_DIRS=/usr/share/terminfo")
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// AttachCommand is what a user types to watch or take over a project's session.
func AttachCommand(project string) string {
	return fmt.Sprintf("tmux -L %s attach -t %s", tmuxSocket, SessionName(project))
}

// SessionName is the tmux session used for a project, for `tmux attach -t`.
func SessionName(project string) string { return "du-" + safeName(project) }

type claudeResult struct {
	Status  string `json:"status"`
	Summary string `json:"summary"`
}

// runClaude hands the whole update to a Claude Code session in tmux and waits
// for the result file the session writes. Go only verifies and cleans up.
func runClaude(ctx context.Context, name, path, startBranch, tempBranch string, commands []string, out chan<- StatusUpdate) {
	session := SessionName(name)
	// Terminal statuses are sent only after the session is dead and the repo is settled.
	send := func(status, command, output string) {
		if status != "running" {
			tmux("kill-session", "-t", "="+session)
		}
		out <- StatusUpdate{ProjectName: name, Status: status, Command: command, Output: output}
	}
	fail := func(msg string) {
		logging.Error("[%s] %s", name, msg)
		tmux("kill-session", "-t", "="+session)
		if tempBranch != "" {
			Cleanup(path, startBranch, tempBranch) // best-effort
		}
		send("failed", "Claude session", msg)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		fail("no home dir: " + err.Error())
		return
	}
	cfgDir := filepath.Join(home, ".config") // same place as config.yaml and updater.log
	runDir := filepath.Join(cfgDir, "drupal-updater", "runs", safeName(name))
	resultFile := filepath.Join(runDir, "result.json")
	turnFile := filepath.Join(runDir, "turn-done")
	exitFile := filepath.Join(runDir, "exit")

	if err := os.MkdirAll(runDir, 0o755); err != nil {
		fail(err.Error())
		return
	}
	for _, f := range []string{resultFile, turnFile, exitFile} {
		os.Remove(f)
	}
	tmux("kill-session", "-t", "="+session) // stale session from an earlier run
	if err := trustFolder(path); err != nil {
		logging.Error("[%s] could not pre-trust folder: %v", name, err) // falls back to the trust-prompt detection below
	}

	importDB := filepath.Join(runDir, "import-db.sh")
	numbered := make([]string, len(commands))
	for i, c := range commands {
		numbered[i] = fmt.Sprintf("%d. %s", i+1, c)
	}
	proj := claudeCfg.projects[name]
	ignored := "none"
	if len(proj.IgnoredPackages) > 0 {
		ignored = strings.Join(proj.IgnoredPackages, ", ")
	}
	if proj.ClaudeInstructions != "" {
		proj.ClaudeInstructions = "\nExtra instructions for this project:\n" + proj.ClaudeInstructions + "\n"
	}
	prompt := strings.NewReplacer(
		"{{ignored_packages}}", ignored, "{{project_instructions}}", proj.ClaudeInstructions,
		"{{import_db}}", importDB, "{{project}}", name, "{{path}}", path, "{{start_branch}}", startBranch,
		"{{temp_branch}}", tempBranch, "{{commands}}", strings.Join(numbered, "\n"), "{{result_file}}", resultFile,
	).Replace(claudeCfg.prompt)

	settings := map[string]any{"hooks": map[string]any{"Stop": []any{map[string]any{
		"hooks": []any{map[string]any{"type": "command", "command": "touch " + sq(turnFile)}},
	}}}}
	settingsJSON, _ := json.Marshal(settings)
	root := path
	if r, err := filepath.EvalSymlinks(path); err == nil {
		root = r
	}
	allowed := append(append([]string(nil), claudeCfg.allowed...), "Bash("+importDB+" *)")
	script := fmt.Sprintf("#!/bin/sh\ncd %s || exit 1\nclaude --model %s --strict-mcp-config --permission-mode %s --settings %s --add-dir %s --allowedTools=%s --disallowedTools=%s \"$(cat %s)\"\necho $? > %s\n",
		sq(path), sq(claudeCfg.model), sq(claudeCfg.permission), sq(filepath.Join(runDir, "settings.json")), sq(runDir),
		sq(strings.Join(allowed, ",")), sq(strings.Join(claudeDenied, ",")), sq(filepath.Join(runDir, "prompt.md")), sq(exitFile))
	files := map[string]string{"prompt.md": prompt, "settings.json": string(settingsJSON), "run.sh": script,
		"import-db.sh": fmt.Sprintf(importDBScript, sq(root), dumpMaxAgeDays)}
	for f, content := range files {
		perm := os.FileMode(0o644)
		if f == "import-db.sh" {
			perm = 0o755
		}
		if err := os.WriteFile(filepath.Join(runDir, f), []byte(content), perm); err != nil {
			fail(err.Error())
			return
		}
	}

	if o, err := tmux("new-session", "-d", "-s", session, "-c", path, loginShell(), "-l", filepath.Join(runDir, "run.sh")); err != nil {
		fail("tmux failed: " + err.Error() + "\n" + o)
		return
	}

	send("running", fmt.Sprintf("Claude (%s) in tmux session %s", claudeCfg.model, session),
		fmt.Sprintf("Watch or take over: %s\nPrompt: %s\n", AttachCommand(name), filepath.Join(runDir, "prompt.md")))
	logging.Info("[%s] Claude session started: %s", name, session)

	deadline := time.After(claudeCfg.timeout)
	tick := time.NewTicker(claudePoll)
	defer tick.Stop()
	var notifiedTurn time.Time
	notifiedPrompt := false
	for {
		select {
		case <-ctx.Done():
			send("cancelled", "Claude session", "Cancelled")
			return
		case <-deadline:
			fail(fmt.Sprintf("timed out after %s; session killed", claudeCfg.timeout))
			return
		case <-tick.C:
		}

		if data, err := os.ReadFile(resultFile); err == nil {
			var r claudeResult
			if json.Unmarshal(data, &r) != nil {
				continue // partially written, read again next tick
			}
			if r.Status != "success" {
				fail("Claude reported failure: " + r.Summary)
				return
			}
			if tempBranch != "" {
				if cur, _ := git(path, "branch", "--show-current"); strings.TrimSpace(cur) != tempBranch {
					fail(fmt.Sprintf("Claude reported success but repo is on %q, expected %q", strings.TrimSpace(cur), tempBranch))
					return
				}
			}
			logging.Info("[%s] Claude finished: %s", name, r.Summary)
			send("success", "Claude session", r.Summary)
			return
		}

		if _, err := tmux("has-session", "-t", "="+session); err != nil {
			code, _ := os.ReadFile(exitFile)
			fail(fmt.Sprintf("Claude exited without writing a result (exit %s)", strings.TrimSpace(string(code))))
			return
		}
		pane, _ := tmux("capture-pane", "-p", "-t", "="+session+":")
		if strings.Contains(pane, "trust this folder") {
			fail(fmt.Sprintf("Claude does not trust %s yet. Run `claude` once in that folder, accept the prompt, then retry.", path))
			return
		}
		if !notifiedPrompt && strings.Contains(pane, "requires approval") {
			notifiedPrompt = true
			send("running", "Claude is waiting for approval",
				fmt.Sprintf("A command is not in claude_allowed_tools. Take over: %s\n", AttachCommand(name)))
		}
		if st, err := os.Stat(turnFile); err == nil && st.ModTime().After(notifiedTurn) {
			notifiedTurn = st.ModTime()
			send("running", "Claude stopped without a result",
				fmt.Sprintf("Probably waiting for input. Take over: %s\n", AttachCommand(name)))
		}
	}
}
