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
// Placeholders: {{project}} {{path}} {{start_branch}} {{temp_branch}} {{ignored_packages}} {{smoke_paths}}
// {{project_instructions}} {{result_file}} {{updates}} (the update_mode rules for step 8) and {{commands}} (the classic command list).
const DefaultClaudePrompt = `You are updating the Drupal project "{{project}}" located at {{path}} (your working directory). Work fully autonomously: nobody is watching, so never ask questions.

Branches:
- Start branch: {{start_branch}}. If it does not exist locally, use the repository's real default branch (origin/HEAD, else main/master).
- Temp branch: {{temp_branch}}. All work happens on this branch.

Project notes: if the repository has an AGENTS.md (or else a CLAUDE.md), read it before starting and follow it where it doesn't conflict with this task. Flows listed in its testing / smoke test section are extra checks for steps 6 and 11.

Steps, in order:
1. git checkout {{start_branch}}, then git pull --rebase --autostash
2. git checkout -B {{temp_branch}} (this also resets the branch if it already exists)
3. Make sure ddev is running: ddev describe, and ddev start if it is not
4. ddev composer install
5. ddev drush deploy -y
6. Smoke tests, baseline (see below): the site as it is before any update
7. ddev composer update 'drupal/core-*' 'symfony/*' 'twig/*' --with-all-dependencies
8. Updates, see below
9. ddev drush updb -y
10. ddev drush cex -y
11. Smoke tests again, after the update, and compare with the baseline (see below)
12. ddev stop

{{updates}}
Smoke tests (steps 6 and 11). Run the same checks both times and keep the baseline results. Get the site URL from ddev describe; for admin pages log in with ddev drush uli and follow that link with curl, keeping the session in a cookie jar.
- ddev drush status bootstraps Drupal; ddev drush cr succeeds.
- Anonymous, each must return 200 without PHP errors, exceptions or "The website encountered an unexpected error" in the HTML: /, /user/login, the newest published node of each content type, /search if the search module is enabled. A made-up path must return 404, not 500.
- As admin, each must return 200 without errors: /admin/content, /admin/structure, /admin/config, /node/add/<type> for each content type, and /admin/reports/status without Error-level requirements.
- No new Error-level log entries since the baseline (ddev drush ws --severity=Error).
- After step 10: ddev drush cst shows nothing pending.
- Extra paths for this project (prefix admin: means check while logged in): {{smoke_paths}}
- The flows from the project's AGENTS.md / CLAUDE.md testing section, if any, carried out with curl and drush.
A regression is a check that passed at the baseline and fails after the update. A check that already failed at the baseline is pre-existing: report it, don't try to fix it.
For each regression: find the cause (page output, logs) and fix it when the update caused it (cache, config import, update hook, patch, constraint). If you can't, roll back only the package that causes it, as with a skipped security fix, and run the checks again. If a regression remains, finish with status failed and name the broken check.

Packages that must NOT be updated: {{ignored_packages}}
{{project_instructions}}
Solve simple problems yourself and retry, for example:
- local changes blocking checkout/pull: git stash push -u -m drupal-updater-autostash, then retry
- branch has no upstream: set it with --set-upstream-to
- temp branch already exists, wrong default branch name, ddev router/network race, transient network errors: retry (up to 3 times)
- autostash pop conflict on a local-only config file (e.g. .ddev/*): keep the upstream version (git checkout -- <file>) and continue, but never drop that stash: it holds the user's own local edits (the app prunes old drupal-updater stashes itself). Mention the stash in the summary.
- ddev refuses to start because the existing database type/version differs from .ddev/config.yaml (e.g. the volume holds mysql:8.0, config says mariadb:10.11): convert the data to the configured type. ddev utility migrate-database needs the config to match the current volume first, so: ddev config --database=<volume type:version>, then ddev utility migrate-database <configured type:version> (it keeps the data and writes the configured type back). Afterwards make sure .ddev/config.yaml equals the upstream version again (git diff .ddev/config.yaml). Never reset or delete the database.
- ddev refuses to start because upstream renamed the project (name in .ddev/config.yaml differs from the one registered for this directory): ddev stop --unlist <old-name>, then ddev start. Then check the site boots (ddev drush status). If the database is missing or empty afterwards, try to restore one yourself: ddev snapshot restore --latest, ddev pull if configured, or a recent dump in the project via ddev import-db --file=<file>. Report failure only if none works.
Skipped updates and advisories are not a failure: report them in the summary and still finish with status success.
Be persistent. Diagnose every problem (read the error, inspect state, check logs) and fix it yourself, including composer conflicts, merge/stash conflicts, ddev/docker issues, config import errors and failing update hooks. Try several different approaches before giving up. Only report failure when the problem truly needs a human: missing credentials or access, a decision only the owner can make, or an action you are not permitted to take (see Rules). When you do fail, say exactly what you tried and what is needed.

Rules (enforced; refused commands explain what to do instead):
- Do NOT commit, merge or push. Leave the changes uncommitted on the temp branch.
- Never throw away local work or data: no git reset --hard, git clean, git stash drop/clear, ddev delete/clean, --reset-database, --remove-data or snapshot cleanup. Never delete any branch except {{temp_branch}}.
- Keep the changes to what the update needs; local environment fixes (.ddev, database) are fine.

Finish by writing the file {{result_file}} containing JSON only:
{"status": "success" | "failed", "summary": "<what was updated (core version change, each updated package with old -> new version), each skipped update or advisory with its reason, smoke tests: checks run, regressions fixed, packages rolled back, pre-existing failures; or what failed and why>"}
On success you must be on branch {{temp_branch}} when you write it. On failure, first return to the start branch (stash a dirty tree if needed) and delete {{temp_branch}}, then write the file.`

// Step 8 of DefaultClaudePrompt per update_mode, filled in as {{updates}}.
const (
	disasterUpdates = `Updates (step 8), disaster mode: security fixes only. Goal: fix every security advisory that can be fixed without a major upgrade.
- Find advisories with ddev composer audit --format=json (it covers drupal/* and all other Composer packages). Cross-check Drupal ones with ddev drush pm:security if that command exists.
- "Major" is the first version number as Composer sees it. Drupal contrib "8.x-1.5" is 1.5.0 to Composer, so 8.x-1.x and 1.x are the same major; 1.x to 2.x is a major upgrade.
- Drupal core first, because contrib fixes may need a newer core minor: move drupal/core and its companions (drupal/core-recommended, drupal/core-composer-scaffold, drupal/core-project-message, and drupal/core-dev if required) together to the lowest fixed release within the installed major. A minor bump (e.g. 10.3 to 10.5) is fine; a new major (10 to 11) never.
- Then every other affected package (contrib modules, themes, libraries such as guzzlehttp/*): the lowest fixed version within the installed major. Use ddev composer update <package> --with-all-dependencies when composer.json allows it; otherwise widen the constraint within the same major with ddev composer require '<package>:^<x.y>' --with-all-dependencies.
- Skip an advisory, and keep going with the rest, when the fix needs: a new major of the package, a new major of Drupal core (directly or through its dependencies), a newer PHP than the project runs, or an update of a package listed below as not to be updated. Never use --ignore-platform-reqs. Never move core to a new major.
- If one package's fix conflicts, revert just that change (git checkout -- composer.json composer.lock, then redo the others) rather than giving up on all of them.
- Finish with ddev composer audit again and compare: every advisory left must be one you deliberately skipped.`
	standardUpdates = `Updates (step 8), standard mode: every minor and patch update, never a major.
- "Major" is the first version number as Composer sees it. Drupal contrib "8.x-1.5" is 1.5.0 to Composer, so 8.x-1.x and 1.x are the same major; 1.x to 2.x is a major upgrade.
- Run ddev composer update --with-all-dependencies. If packages are listed below as not to be updated, name every other direct dependency instead (ddev composer show --direct --name-only).
- Then check git diff composer.lock: any package that moved to a new major (possible with loose constraints such as * or >=) goes back to its old major with ddev composer require '<package>:^<old x.y>' --with-all-dependencies. Never move core to a new major. Never use --ignore-platform-reqs.`
)

// The agent works autonomously with every tool; the hard limits live in claudeDenied and in the
// git/ddev guard shims (guard.go), which also catch forms these globs miss.
var defaultClaudeAllowed = []string{"Read", "Grep", "Glob", "Edit", "Write", "Bash", "WebFetch", "WebSearch"}

// ClaudeDefaults exposes the built-in prompt and allowlist for settings screens.
func ClaudeDefaults() (prompt string, allowedTools []string) {
	return DefaultClaudePrompt, append([]string(nil), defaultClaudeAllowed...)
}

// Always denied, regardless of config: no commits/merges/pushes, nothing that destroys local work or data.
var claudeDenied = []string{
	"Bash(git commit*)", "Bash(git merge*)", "Bash(git push*)", "Bash(git cherry-pick*)", "Bash(git revert*)", "Bash(git am*)",
	"Bash(git reset --hard*)", "Bash(git clean*)", "Bash(git stash drop*)", "Bash(git stash clear*)",
	"Bash(ddev delete*)", "Bash(ddev clean*)", "Bash(ddev * --reset-database*)", "Bash(ddev * --remove-data*)", "Bash(ddev snapshot --cleanup*)",
}

var claudeCfg struct {
	enabled    bool
	model      string
	prompt     string
	updates    string // {{updates}} text for the configured update_mode
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
	c.updates = standardUpdates
	if cfg.UpdateMode == "disaster" {
		c.updates = disasterUpdates
	}
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

// runDirFor is where a project's Claude run keeps its prompt, scripts, shims and guard state.
func runDirFor(project string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "drupal-updater", "runs", safeName(project)), nil // next to config.yaml and updater.log
}

// tmuxPath prefers the copy bundled inside the .app, then a tmux on the enriched PATH.
func tmuxPath() string {
	if exe, err := os.Executable(); err == nil {
		bundled := filepath.Join(filepath.Dir(exe), "..", "Resources", "tmux-bundle", "tmux")
		if _, err := os.Stat(bundled); err == nil {
			return bundled
		}
	}
	return lookEnriched("tmux")
}

// lookEnriched finds name on the enriched PATH, else returns name unchanged.
// (exec.LookPath uses the process PATH, which is minimal for GUI apps.)
func lookEnriched(name string) string {
	for _, e := range EnrichedEnv() {
		if v, ok := strings.CutPrefix(e, "PATH="); ok {
			for _, dir := range filepath.SplitList(v) {
				if p := filepath.Join(dir, name); isExecutable(p) {
					return p
				}
			}
		}
	}
	return name
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

	runDir, err := runDirFor(name)
	if err != nil {
		fail(err.Error())
		return
	}
	outDir := filepath.Join(runDir, "out") // added via --add-dir so Claude can write the result file
	resultFile := filepath.Join(outDir, "result.json")
	turnFile := filepath.Join(runDir, "turn-done")
	exitFile := filepath.Join(runDir, "exit")

	if err := os.MkdirAll(outDir, 0o755); err != nil {
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

	numbered := make([]string, len(commands))
	for i, c := range commands {
		numbered[i] = fmt.Sprintf("%d. %s", i+1, c)
	}
	proj := claudeCfg.projects[name]
	ignored, smoke := "none", "none"
	if len(proj.IgnoredPackages) > 0 {
		ignored = strings.Join(proj.IgnoredPackages, ", ")
	}
	if len(proj.SmokePaths) > 0 {
		smoke = strings.Join(proj.SmokePaths, ", ")
	}
	if proj.ClaudeInstructions != "" {
		proj.ClaudeInstructions = "\nExtra instructions for this project:\n" + proj.ClaudeInstructions + "\n"
	}
	prompt := strings.NewReplacer(
		"{{ignored_packages}}", ignored, "{{smoke_paths}}", smoke, "{{project_instructions}}", proj.ClaudeInstructions, "{{updates}}", claudeCfg.updates,
		"{{project}}", name, "{{path}}", path, "{{start_branch}}", startBranch,
		"{{temp_branch}}", tempBranch, "{{commands}}", strings.Join(numbered, "\n"), "{{result_file}}", resultFile,
	).Replace(claudeCfg.prompt)

	settings := map[string]any{"hooks": map[string]any{"Stop": []any{map[string]any{
		"hooks": []any{map[string]any{"type": "command", "command": "touch " + sq(turnFile)}},
	}}}}
	settingsJSON, _ := json.Marshal(settings)
	// Vanilla session, ~70% fewer input tokens than with the user's setup (measured): no CLAUDE.md files (the prompt
	// reads the project's AGENTS.md itself), no settings files (so no user/repo hooks or plugins), no skills, core tools
	// only; --settings still applies (Stop hook). Static system prompt so concurrent runs share the prompt cache.
	script := fmt.Sprintf("#!/bin/sh\ncd %s || exit 1\nPATH=%s:\"$PATH\"; export PATH # git/ddev resolve to the guard shims\nCLAUDE_CODE_DISABLE_CLAUDE_MDS=1 claude --model %s --strict-mcp-config --setting-sources '' --disable-slash-commands --tools=Bash,Read,Edit,Write,Grep,Glob,WebFetch,WebSearch --exclude-dynamic-system-prompt-sections --permission-mode %s --settings %s --add-dir %s --allowedTools=%s --disallowedTools=%s \"$(cat %s)\"\necho $? > %s\n",
		sq(path), sq(filepath.Join(runDir, "bin")), sq(claudeCfg.model), sq(claudeCfg.permission), sq(filepath.Join(runDir, "settings.json")), sq(outDir),
		sq(strings.Join(claudeCfg.allowed, ",")), sq(strings.Join(claudeDenied, ",")), sq(filepath.Join(runDir, "prompt.md")), sq(exitFile))
	exe, err := os.Executable()
	if err != nil {
		fail("cannot locate own binary for the git/ddev guard: " + err.Error())
		return
	}
	realGit, realDdev := lookEnriched("git"), lookEnriched("ddev")
	if !filepath.IsAbs(realGit) || !filepath.IsAbs(realDdev) {
		fail("git and ddev must be installed (not found on PATH)")
		return
	}
	if err := os.MkdirAll(filepath.Join(runDir, "bin"), 0o755); err != nil {
		fail(err.Error())
		return
	}
	files := map[string]string{"prompt.md": prompt, "settings.json": string(settingsJSON), "run.sh": script,
		"bin/git":  fmt.Sprintf("#!/bin/sh\nexec %s guard git %s \"$@\"\n", sq(exe), sq(realGit)),
		"bin/ddev": fmt.Sprintf("#!/bin/sh\nexec %s guard ddev %s \"$@\"\n", sq(exe), sq(realDdev))}
	for f, content := range files {
		perm := os.FileMode(0o444) // read-only, so a shell redirect can't rewrite them either
		if strings.HasPrefix(f, "bin/") {
			perm = 0o555
		}
		os.Remove(filepath.Join(runDir, f)) // earlier run's copy is read-only
		if err := os.WriteFile(filepath.Join(runDir, f), []byte(content), perm); err != nil {
			fail(err.Error())
			return
		}
	}

	if o, err := tmux("new-session", "-d", "-x", "200", "-y", "50", "-s", session, "-c", path, loginShell(), "-l", filepath.Join(runDir, "run.sh")); err != nil {
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
	var lastPane string
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
			send("success", "Claude session", r.Summary+LockDiff(path))
			return
		}

		if _, err := tmux("has-session", "-t", "="+session); err != nil {
			code, _ := os.ReadFile(exitFile)
			fail(fmt.Sprintf("Claude exited without writing a result (exit %s)", strings.TrimSpace(string(code))))
			return
		}
		pane, _ := tmux("capture-pane", "-p", "-t", "="+session+":")
		if pane != lastPane {
			lastPane = pane
			select { // live view only: drop a frame rather than stall the run when nobody reads
			case out <- StatusUpdate{ProjectName: name, Status: "running", Screen: pane}:
			default:
			}
		}
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
