package executor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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

// DefaultClaudePrompt is used when claude_prompt is not set in the config. The app itself does the fixed steps
// around it (checkout, ddev start, composer install, drush deploy before; drush updb and cex, checks and ddev stop
// after) and fills in what it found. Placeholders: {{project}} {{path}} {{start_branch}} {{temp_branch}}
// {{setup}} {{update_list}} {{previous_failure}} {{ignored_packages}} {{project_instructions}} {{result_file}}
// and {{updates}} (the update_mode rules).
const DefaultClaudePrompt = `You are updating the Drupal project "{{project}}" located at {{path}} (your working directory). Work fully autonomously: nobody is watching, so never ask questions.

Branches: start branch {{start_branch}}, temp branch {{temp_branch}}. All work happens on the temp branch.

What the app already did: {{setup}}

Project notes: if the repository has an AGENTS.md (or else a CLAUDE.md), read it before starting and follow it where it doesn't conflict with this task. Flows in its testing / smoke test section are not covered by the app's checks: carry them out (with curl and drush) after updating and fix what the update broke.
{{previous_failure}}
Your task: apply the updates, then stop. Afterwards the app runs ddev drush updb and cex, checks that no database updates or config changes are pending, compares the site with the baseline it took before you started (anonymous and admin pages, recent errors in the log) and, if something regressed, starts another session to fix it. You don't need to do those steps yourself, but check that the site still boots (ddev drush status) when you change a lot.

Updates found by the app before you started:
{{update_list}}

{{updates}}
Packages that must NOT be updated: {{ignored_packages}}
{{project_instructions}}
Solve simple problems yourself and retry, for example:
- local changes blocking checkout/pull: git stash push -u -m drupal-updater-autostash, then retry
- branch has no upstream: set it with --set-upstream-to
- temp branch already exists, wrong default branch name, ddev router/network race, transient network errors: retry (up to 3 times)
- autostash pop conflict on a local-only config file (e.g. .ddev/*): keep the upstream version (git checkout -- <file>) and continue, but never drop that stash: it holds the user's own local edits. Mention the stash in the summary.
- ddev refuses to start because the existing database type/version differs from .ddev/config.yaml (e.g. the volume holds mysql:8.0, config says mariadb:10.11): convert the data to the configured type. ddev utility migrate-database needs the config to match the current volume first, so: ddev config --database=<volume type:version>, then ddev utility migrate-database <configured type:version> (it keeps the data and writes the configured type back). Afterwards make sure .ddev/config.yaml equals the upstream version again (git diff .ddev/config.yaml). Never reset or delete the database.
- ddev refuses to start because upstream renamed the project (name in .ddev/config.yaml differs from the one registered for this directory): ddev stop --unlist <old-name>, then ddev start. Then check the site boots (ddev drush status). If the database is missing or empty afterwards, try to restore one yourself: ddev snapshot restore --latest, ddev pull if configured, or a recent dump in the project via ddev import-db --file=<file>. Report failure only if none works.
Skipped updates and advisories are not a failure: report them in the summary and still finish with status success.
Be persistent. Diagnose every problem (read the error, inspect state, check logs) and fix it yourself, including composer conflicts, merge/stash conflicts, ddev/docker issues, config import errors and failing update hooks. Try several different approaches before giving up. Only report failure when the problem truly needs a human: missing credentials or access, a decision only the owner can make, or an action you are not permitted to take (see Rules). When you do fail, say exactly what you tried and what is needed.

Rules (enforced; refused commands explain what to do instead):
- Do NOT commit, merge or push. Leave the changes uncommitted on the temp branch.
- Never throw away local work or data: no git reset --hard, git clean, git stash drop/clear, ddev delete/clean, --reset-database, --remove-data or snapshot cleanup. Never delete any branch except {{temp_branch}}.
- Keep the changes to what the update needs; local environment fixes (.ddev, database) are fine.

Finish by writing the file {{result_file}} containing JSON only:
{"status": "success" | "failed", "summary": "<see below>"}
The summary is read by a person: short lines separated by \n, each starting with "- ", one fact per line, no paragraphs. On success: the core version change; each updated package as "name old -> new" (related packages on one line, e.g. "drupal/core* 11.4.8 -> 11.4.10"); each skipped update or advisory with its reason; what you fixed along the way; what a person must still do. On failure the first line says what failed, then what you tried and what is needed.
Stay on branch {{temp_branch}} when you write it, also on failure: the app cleans up.`

// repairPrompt is used when the app's checks after an update found problems.
const repairPrompt = `You are fixing an update of the Drupal project "{{project}}" at {{path}} (your working directory), on the branch {{temp_branch}} (start branch {{start_branch}}). Work fully autonomously: nobody is watching, so never ask questions.

An earlier session updated the dependencies and reported:
{{previous_summary}}

After that the app ran ddev drush updb -y and ddev drush cex -y and checked the site against a baseline taken before the update. These problems remain:
{{problems}}

For each problem: find the cause (page output, ddev drush ws, logs) and fix it when the update caused it (cache, config import, update hook, patch, constraint). If you can't, roll back only the package that causes it (ddev composer require '<package>:<old version>' or revert its lines in composer.json/composer.lock and run ddev composer install), as with a skipped security fix. You may run ddev drush updb, cex and cr yourself while fixing; the app checks everything again afterwards.

Packages that must NOT be updated: {{ignored_packages}}
{{project_instructions}}
Rules (enforced): do NOT commit, merge or push; never throw away local work or data (no git reset --hard, git clean, git stash drop/clear, ddev delete/clean, database resets); never delete any branch.

Finish by writing the file {{result_file}} containing JSON only:
{"status": "success" | "failed", "summary": "<see below>"}
The summary is read by a person: short lines separated by \n, each starting with "- ", one fact per line, no paragraphs: per problem what caused it and what you changed (or that it was not a real problem), packages rolled back with their versions, or what still fails and why.
Stay on branch {{temp_branch}} when you write it.`

// The update rules of DefaultClaudePrompt per update_mode, filled in as {{updates}}.
const (
	disasterUpdates = `Updates, disaster mode: security fixes only. Do not update anything that has no advisory, core included. Goal: fix every security advisory that can be fixed without a major upgrade.
- Find advisories with ddev composer audit --format=json (it covers drupal/* and all other Composer packages). Cross-check Drupal ones with ddev drush pm:security if that command exists.
- "Major" is the first version number as Composer sees it. Drupal contrib "8.x-1.5" is 1.5.0 to Composer, so 8.x-1.x and 1.x are the same major; 1.x to 2.x is a major upgrade.
- Drupal core first, because contrib fixes may need a newer core minor: move drupal/core and its companions (drupal/core-recommended, drupal/core-composer-scaffold, drupal/core-project-message, and drupal/core-dev if required) together to the lowest fixed release within the installed major. A minor bump (e.g. 10.3 to 10.5) is fine; a new major (10 to 11) never.
- Then every other affected package (contrib modules, themes, libraries such as guzzlehttp/*): the lowest fixed version within the installed major. Use ddev composer update <package> --with-all-dependencies when composer.json allows it; otherwise widen the constraint within the same major with ddev composer require '<package>:^<x.y>' --with-all-dependencies.
- Skip an advisory, and keep going with the rest, when the fix needs: a new major of the package, a new major of Drupal core (directly or through its dependencies), a newer PHP than the project runs, or an update of a package listed below as not to be updated. Never use --ignore-platform-reqs. Never move core to a new major.
- If one package's fix conflicts, revert just that change (git checkout -- composer.json composer.lock, then redo the others) rather than giving up on all of them.
- Finish with ddev composer audit again and compare: every advisory left must be one you deliberately skipped.`
	standardUpdates = `Updates, standard mode: every minor and patch update, never a major.
- Start with core and its framework: ddev composer update 'drupal/core-*' 'symfony/*' 'twig/*' --with-all-dependencies.
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
	model      string
	prompt     string
	updates    string // {{updates}} text for the configured update_mode
	disaster   bool   // security fixes only
	allowed    []string
	permission string
	timeout    time.Duration
	projects   *config.Config // only Defaults and Projects, copied so later edits don't race a run
}

func orDefault(v, d string) string {
	if v != "" {
		return v
	}
	return d
}

// Configure copies the claude_* settings from the config; unset values keep defaults.
func Configure(cfg *config.Config) {
	c := &claudeCfg
	c.model = orDefault(cfg.ClaudeModel, defaultClaudeModel)
	c.prompt = orDefault(cfg.ClaudePrompt, DefaultClaudePrompt)
	c.updates = standardUpdates
	c.disaster = cfg.UpdateMode == "disaster"
	if c.disaster {
		c.updates = disasterUpdates
	}
	c.allowed = defaultClaudeAllowed
	if len(cfg.ClaudeAllowedTools) > 0 {
		c.allowed = cfg.ClaudeAllowedTools
	}
	c.permission = safePermissionMode(cfg.ClaudePermissionMode)
	c.projects = &config.Config{Defaults: cfg.Defaults, Projects: make(map[string]config.ProjectConfig, len(cfg.Projects))}
	for k, v := range cfg.Projects {
		c.projects.Projects[k] = v
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

// safeName makes a project name usable as a folder and tmux session name. Names that had to change get a short
// hash of the original, so "my.site" and "my-site" never share a run folder (or a merge request description).
func safeName(s string) string {
	clean := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, s)
	if clean == s {
		return s
	}
	sum := sha256.Sum256([]byte(s))
	return clean + "-" + hex.EncodeToString(sum[:4])
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

// runPaths are the files a project's Claude session uses.
func runPaths(name string) (runDir, outDir, resultFile string, err error) {
	runDir, err = runDirFor(name)
	if err != nil {
		return "", "", "", err
	}
	outDir = filepath.Join(runDir, "out") // added via --add-dir so Claude can write the result file
	return runDir, outDir, filepath.Join(outDir, "result.json"), nil
}

// fillPrompt fills the common placeholders plus the extra old/new pairs.
func fillPrompt(tmpl, name, path, startBranch, tempBranch, resultFile string, extra ...string) string {
	proj := config.ForProject(claudeCfg.projects.Project(name), path)
	ignored, smoke := "none", "none"
	if len(proj.IgnoredPackages) > 0 {
		ignored = strings.Join(proj.IgnoredPackages, ", ")
	}
	if len(proj.SmokePaths) > 0 {
		smoke = strings.Join(proj.SmokePaths, ", ")
	}
	instructions := ""
	if proj.ClaudeInstructions != "" {
		instructions = "\nExtra instructions for this project:\n" + proj.ClaudeInstructions + "\n"
	}
	return strings.NewReplacer(append([]string{
		"{{ignored_packages}}", ignored, "{{smoke_paths}}", smoke, "{{project_instructions}}", instructions, "{{updates}}", claudeCfg.updates,
		"{{project}}", name, "{{path}}", path, "{{start_branch}}", startBranch,
		"{{temp_branch}}", tempBranch, "{{result_file}}", resultFile,
	}, extra...)...).Replace(tmpl)
}

// limitMarkers are what Claude Code prints when the API refuses to continue; seen on screen after the turn
// ended without a result, they end the run at once instead of after the full timeout.
var limitMarkers = []string{"usage limit", "Usage limit", "rate_limit_error", "API Error: 429", "API Error: 529", "overloaded_error", "Overloaded"}

// stallAfter is how long a session may sit idle after ending its turn without a result before the run gives up.
var stallAfter = 10 * time.Minute

// claudeSession runs one Claude Code session in tmux on prompt (whose {{result_file}} must already be filled
// with resultFile from runPaths) and waits for the result file. It returns the result, or an error saying why
// there is none: cancelled, timed out, exited, a usage or rate limit, or stalled. The session is always gone
// when it returns. Screens stream to out; label names the session in the log.
func claudeSession(ctx context.Context, name, path, prompt, label string, out chan<- StatusUpdate) (claudeResult, error) {
	session := SessionName(name)
	defer tmux("kill-session", "-t", "="+session)
	runDir, outDir, resultFile, err := runPaths(name)
	if err != nil {
		return claudeResult{}, err
	}
	turnFile := filepath.Join(runDir, "turn-done")
	exitFile := filepath.Join(runDir, "exit")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return claudeResult{}, err
	}
	for _, f := range []string{resultFile, turnFile, exitFile} {
		os.Remove(f)
	}
	tmux("kill-session", "-t", "="+session) // stale session from an earlier run
	if err := trustFolder(path); err != nil {
		logging.Error("[%s] could not pre-trust folder: %v", name, err) // falls back to the trust-prompt detection below
	}
	if err := trustExact(outDir); err != nil { // newer Claude prompts for untrusted --add-dir folders
		logging.Error("[%s] could not pre-trust %s: %v", name, outDir, err)
	}

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
		return claudeResult{}, errors.New("cannot locate own binary for the git/ddev guard: " + err.Error())
	}
	realGit, realDdev := lookEnriched("git"), lookEnriched("ddev")
	if !filepath.IsAbs(realGit) || !filepath.IsAbs(realDdev) {
		return claudeResult{}, errors.New("git and ddev must be installed (not found on PATH)")
	}
	if err := os.MkdirAll(filepath.Join(runDir, "bin"), 0o755); err != nil {
		return claudeResult{}, err
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
			return claudeResult{}, err
		}
	}

	if o, err := tmux("new-session", "-d", "-x", "200", "-y", "50", "-s", session, "-c", path, loginShell(), "-l", filepath.Join(runDir, "run.sh")); err != nil {
		return claudeResult{}, errors.New("tmux failed: " + err.Error() + "\n" + o)
	}
	out <- StatusUpdate{ProjectName: name, Status: "running", Command: fmt.Sprintf("%s: Claude (%s) in tmux session %s", label, claudeCfg.model, session),
		Output: fmt.Sprintf("Watch or take over: %s\nPrompt: %s\n", AttachCommand(name), filepath.Join(runDir, "prompt.md"))}
	logging.Info("[%s] Claude session started: %s (%s)", name, session, label)

	deadline := time.After(claudeCfg.timeout)
	tick := time.NewTicker(claudePoll)
	defer tick.Stop()
	var turnSeen time.Time   // last time the Stop hook fired
	var lastChange time.Time // last time the screen changed
	notifiedPrompt := false
	var lastPane string
	for {
		select {
		case <-ctx.Done():
			return claudeResult{}, ctx.Err()
		case <-deadline:
			return claudeResult{}, fmt.Errorf("timed out after %s; session killed", claudeCfg.timeout)
		case <-tick.C:
		}

		if data, err := os.ReadFile(resultFile); err == nil {
			var r claudeResult
			if json.Unmarshal(data, &r) != nil {
				continue // partially written, read again next tick
			}
			return r, nil
		}

		// capture-pane doubles as the liveness check: it fails once the session is gone
		pane, err := tmux("capture-pane", "-p", "-t", "="+session+":")
		if err != nil {
			code, _ := os.ReadFile(exitFile)
			return claudeResult{}, fmt.Errorf("Claude exited without writing a result (exit %s)", strings.TrimSpace(string(code)))
		}
		if pane != lastPane {
			lastPane, lastChange = pane, time.Now()
			select { // live view only: drop a frame rather than stall the run when nobody reads
			case out <- StatusUpdate{ProjectName: name, Status: "running", Screen: pane}:
			default:
			}
		}
		if strings.Contains(pane, "trust this folder") || strings.Contains(pane, "Adding a new directory") {
			return claudeResult{}, fmt.Errorf("Claude does not trust %s (or %s) yet. Run `claude` once in that folder, accept the prompt, then retry", path, outDir)
		}
		if !notifiedPrompt && strings.Contains(pane, "requires approval") {
			notifiedPrompt = true
			out <- StatusUpdate{ProjectName: name, Status: "running", Command: "Claude is waiting for approval",
				Output: fmt.Sprintf("A command is not in claude_allowed_tools. Take over: %s\n", AttachCommand(name))}
		}
		if st, err := os.Stat(turnFile); err == nil && st.ModTime().After(turnSeen) {
			turnSeen = st.ModTime()
			if m := limitHit(pane); m != "" {
				return claudeResult{}, errors.New("Claude stopped on a usage or rate limit (" + m + "); retry later")
			}
			out <- StatusUpdate{ProjectName: name, Status: "running", Command: "Claude stopped without a result",
				Output: fmt.Sprintf("Probably waiting for input. Take over: %s\n", AttachCommand(name))}
		}
		// ended its turn without a result and nothing has happened since: nobody is coming back to it
		if !turnSeen.IsZero() && time.Since(lastChange) > stallAfter && time.Since(turnSeen) > stallAfter {
			return claudeResult{}, fmt.Errorf("Claude stopped without a result and stayed idle for %s", stallAfter)
		}
	}
}

// limitHit returns the first usage or rate limit message on the last lines of the screen, if any.
func limitHit(pane string) string {
	lines := strings.Split(strings.TrimRight(pane, "\n "), "\n")
	if len(lines) > 15 {
		lines = lines[len(lines)-15:]
	}
	tail := strings.Join(lines, "\n")
	for _, m := range limitMarkers {
		if strings.Contains(tail, m) {
			return m
		}
	}
	return ""
}

// runClaude runs a session for a task that needs no pipeline around it (resolving merge conflicts) and reports
// it as one success or failure.
func runClaude(ctx context.Context, name, path string, out chan<- StatusUpdate, promptTmpl string, extra ...string) {
	_, _, resultFile, err := runPaths(name)
	if err == nil {
		var r claudeResult
		r, err = claudeSession(ctx, name, path, fillPrompt(promptTmpl, name, path, "", "", resultFile, extra...), "conflicts", out)
		if err == nil {
			status := "success"
			if r.Status != "success" {
				status = "failed"
			}
			out <- StatusUpdate{ProjectName: name, Status: status, Command: "Claude session", Output: r.Summary}
			return
		}
	}
	status := "failed"
	if ctx.Err() != nil {
		status = "cancelled"
	}
	out <- StatusUpdate{ProjectName: name, Status: status, Command: "Claude session", Output: err.Error()}
}
