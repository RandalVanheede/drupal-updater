package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"drupal-updater/config"
	"drupal-updater/logging"
)

// maxRepairs is how many extra Claude sessions may fix what the checks after an update found.
const maxRepairs = 2

// session runs a Claude session and lookTool finds ddev and friends; variables so tests can stand in for them.
var (
	session  = claudeSession
	lookTool = lookEnriched
)

// pipe is one project's update: the fixed steps run here in Go, Claude does the update itself and any repair.
type pipe struct {
	ctx         context.Context
	name, path  string
	start, temp string
	out         chan<- StatusUpdate
	ddevStop    bool     // ddev was known to be stopped before the run, so it is stopped again afterwards
	notes       []string // what the app did or found, added to the summary
	sites       []site   // the sites drush and the checks run for; see findSites
	multi       bool     // multisite on: problems and notes name their site
}

func (p *pipe) log(command, output string) {
	p.out <- StatusUpdate{ProjectName: p.name, Status: "running", Command: command, Output: output}
}

// cmd runs a fixed command in the project. git runs with hooks and fsmonitor off, like everywhere else.
func (p *pipe) cmd(ctx context.Context, timeout time.Duration, args ...string) (string, error) {
	if args[0] == "git" {
		ctx = context.WithoutCancel(ctx) // a cancelled run must not kill git halfway (index lock, half a merge); the timeout still holds
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var c *exec.Cmd
	if args[0] == "git" {
		c = gitCmd(ctx, p.path, EnrichedEnv(), args[1:]...)
	} else {
		c = exec.CommandContext(ctx, lookTool(args[0]), args[1:]...)
		c.Dir, c.Env = p.path, EnrichedEnv()
	}
	var buf tailBuf
	c.Stdout, c.Stderr = &buf, &buf
	err := c.Run()
	if ctx.Err() == context.DeadlineExceeded {
		err = fmt.Errorf("timed out after %s", timeout)
	}
	return buf.String(), err
}

// tailBuf keeps the last maxOutput bytes a command writes, so a runaway command cannot fill memory; the end holds
// both the JSON and the error.
type tailBuf struct{ b []byte }

const maxOutput = 1 << 20

func (t *tailBuf) Write(p []byte) (int, error) {
	t.b = append(t.b, p...)
	if len(t.b) > 2*maxOutput {
		t.b = append([]byte(nil), t.b[len(t.b)-maxOutput:]...)
	}
	return len(p), nil
}

func (t *tailBuf) String() string {
	if len(t.b) > maxOutput {
		return string(t.b[len(t.b)-maxOutput:])
	}
	return string(t.b)
}

// step runs a fixed command and logs it; on failure it returns a description Claude can act on.
func (p *pipe) step(timeout time.Duration, args ...string) (string, error) {
	line := strings.Join(args, " ")
	p.log("$ "+line, "")
	out, err := p.cmd(p.ctx, timeout, args...)
	if err != nil {
		p.log(line+" failed", tail(out, 3000))
		return out, fmt.Errorf("`%s` failed (%v):\n%s", line, err, tail(out, 3000))
	}
	return out, nil
}

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return "…" + s[len(s)-n:]
	}
	return s
}

// Run updates one project: the fixed steps in Go (checkout, ddev start, composer install, drush deploy), a look at
// what is outdated (nothing to do = skipped, no Claude at all), a baseline smoke test, Claude for the update itself,
// then drush updb/cex and checks in Go; problems go back to Claude in a repair session. A fixed step that fails is
// handed to Claude as well, with its output, instead of failing the run.
func Run(ctx context.Context, projectName, projectPath, startBranch, tempBranch string, out chan<- StatusUpdate) {
	p := &pipe{ctx: ctx, name: projectName, path: projectPath, start: ResolveStartBranch(projectPath, startBranch), temp: tempBranch, out: out}
	proj := config.ForProject(claudeCfg.projects.Project(projectName), projectPath)
	runDir, _, resultFile, err := runPaths(projectName)
	if err != nil {
		p.finish("failed", err.Error())
		return
	}
	os.MkdirAll(runDir, 0o700)
	os.Remove(filepath.Join(runDir, "summary.md")) // it describes the latest run only, never an older one

	// --- before: the fixed steps; the first one that fails goes to Claude ---
	var setupFailed error
	setupDone := []string{}
	do := func(desc string, timeout time.Duration, args ...string) bool {
		if setupFailed != nil || ctx.Err() != nil {
			return false
		}
		if _, err := p.step(timeout, args...); err != nil {
			setupFailed = err
			return false
		}
		setupDone = append(setupDone, desc)
		return true
	}
	running, known := ddevRunning(ctx, p) // before git touches anything, so a failed checkout does not change the answer
	p.ddevStop = known && !running
	// an earlier run's temp branch with commits is kept (renamed), not reset by checkout -B below
	if kept, err := CleanupKeepWork(p.path, p.start, p.temp); err != nil { // on the start branch, a dirty tree stashed
		setupFailed = err
	} else {
		setupDone = append(setupDone, "checked out "+p.start)
		if kept != "" {
			p.notes = append(p.notes, "an earlier run's work was kept on branch "+kept)
		}
	}
	// the fetch ignores the project's own git config (see FetchOrigin); the merge only fast-forwards, with local
	// changes (often .ddev tweaks the site needs) put back afterwards
	if setupFailed == nil && ctx.Err() == nil {
		p.log("$ git fetch origin", "")
		if err := FetchOrigin(ctx, p.path); err != nil {
			setupFailed = fmt.Errorf("`git fetch origin` failed:\n%v", err)
		} else {
			setupDone = append(setupDone, "fetched origin")
		}
	}
	do("fast-forwarded "+p.start+" to origin/"+p.start, 5*time.Minute,
		"git", "-c", "submodule.recurse=false", "merge", "--ff-only", "--autostash", "refs/remotes/origin/"+p.start)
	do("created "+p.temp, time.Minute, "git", "checkout", "-B", p.temp)
	if running {
		setupDone = append(setupDone, "ddev was already running")
	} else {
		do("started ddev", 15*time.Minute, "ddev", "start")
	}
	do("ran ddev composer install", 20*time.Minute, "ddev", "composer", "install", "--no-interaction")
	multisite := proj.Multisite == "on"
	found, skippedSites := findSites(ctx, p.cmd, p.path, multisite)
	sitesInfo := sitesText(found, skippedSites, multisite)
	p.sites, p.multi = found, multisite
	if len(p.sites) == 0 {
		p.sites = []site{{Dir: "default"}}
	}
	for _, s := range skippedSites {
		p.notes = append(p.notes, "site skipped, pre-existing: "+s)
	}
	for _, s := range p.sites {
		do("ran ddev drush deploy -y"+strings.TrimSuffix(" on "+s.label(p.multi), " "), 15*time.Minute, s.drush("deploy", "-y")...)
	}
	if ctx.Err() != nil {
		p.finish("cancelled", "Cancelled")
		return
	}

	// --- what is there to do? nothing = skipped, without starting Claude ---
	list, known, hardening := "", false, ""
	if issue := vendorHardeningIssue(p.path); issue != "" { // not an update, but it keeps the run from being skipped
		hardening = "\n- drupal/core-vendor-hardening: " + issue
	}
	if setupFailed == nil {
		var n int
		list, n, known = p.outdated(proj.IgnoredPackages)
		if known && n == 0 && hardening == "" {
			p.cleanupDdev()
			if err := Cleanup(p.path, p.start, p.temp); err != nil {
				logging.Error("[%s] cleanup after skip: %v", p.name, err)
			}
			msg := "Nothing to update: every package is on its newest minor or patch release."
			if claudeCfg.disaster {
				msg = "Nothing to update: composer audit reports no security advisories."
			}
			p.finish("skipped", msg)
			return
		}
	}
	if !known {
		list = "(the app could not list them; find them yourself as described below)"
	}
	list = strings.TrimPrefix(list+hardening, "\n")

	// --- baseline smoke test, while the site is as it was ---
	baseline := map[string]smokeReport{}
	if setupFailed == nil {
		for _, s := range p.sites {
			p.log("smoke test: baseline "+s.label(p.multi), "")
			baseline[s.Dir] = smoke(ctx, p.cmd, s, proj.SmokePaths)
		}
		writeJSON(filepath.Join(runDir, "baseline.json"), baseline)
	}

	// --- Claude: the update itself ---
	setup := strings.Join(setupDone, "; ") + "."
	if setupFailed != nil {
		setup += "\nThis step failed, so finish it (and the ones after it in this list: create the temp branch, ddev start, ddev composer install, ddev drush deploy -y) yourself before updating:\n" + setupFailed.Error()
	}
	prev := ""
	if b, err := os.ReadFile(filepath.Join(runDir, "last-failure.txt")); err == nil && len(b) > 0 {
		prev = "\nThe previous attempt on this project failed with:\n" + tail(string(b), 2000) + "\nAvoid repeating what went wrong.\n"
	}
	prompt := fillPrompt(claudeCfg.prompt, p.name, p.path, p.start, p.temp, resultFile,
		"{{setup}}", setup, "{{update_list}}", list, "{{previous_failure}}", prev, "{{sites}}", sitesInfo)
	r, err := session(ctx, p.name, p.path, prompt, "update", out)
	if done := p.sessionEnded(r, err); done {
		return
	}
	summary := "Update session:\n" + r.Summary

	// --- after: updb, cex, checks; problems go back to Claude ---
	for round := 0; ; round++ {
		problems := p.verify(baseline, setupFailed == nil)
		if ctx.Err() != nil {
			p.finish("cancelled", "Cancelled")
			return
		}
		if len(problems) == 0 {
			break
		}
		if round == maxRepairs {
			p.finish("failed", fmt.Sprintf("The checks after the update still found %d problem(s), and %d repair session(s) could not fix them:\n- ", len(problems), maxRepairs)+strings.Join(problems, "\n- ")+"\n\n"+summary)
			return
		}
		p.log(fmt.Sprintf("checks found %d problem(s), starting repair session %d", len(problems), round+1), "- "+strings.Join(problems, "\n- "))
		prompt := fillPrompt(repairPrompt, p.name, p.path, p.start, p.temp, resultFile,
			"{{previous_summary}}", summary, "{{problems}}", "- "+strings.Join(problems, "\n- "), "{{sites}}", sitesInfo)
		r, err := session(ctx, p.name, p.path, prompt, fmt.Sprintf("repair %d", round+1), out)
		if done := p.sessionEnded(r, err); done {
			return
		}
		summary += fmt.Sprintf("\n\nRepair session %d:\n%s", round+1, r.Summary)
	}

	// --- result ---
	if _, err := p.cmd(context.Background(), time.Minute, "git", "diff", "--quiet", "HEAD", "--", "composer.lock"); err == nil {
		p.cleanupDdev()
		Cleanup(p.path, p.start, p.temp)
		p.finish("skipped", "No changes were made.\n"+summary)
		return
	}
	if claudeCfg.disaster {
		if left := p.advisories(); len(left) > 0 {
			p.notes = append(p.notes, "Advisories still open (skipped on purpose, see above): "+strings.Join(left, ", "))
		}
	}
	p.cleanupDdev()
	text := summary
	if len(p.notes) > 0 {
		text += "\n\nChecked by the app:\n- " + strings.Join(p.notes, "\n- ")
	}
	p.finish("success", text+LockDiff(p.path))
}

// sessionEnded handles a Claude session that ended without success; it reports whether the run is over.
func (p *pipe) sessionEnded(r claudeResult, err error) bool {
	switch {
	case p.ctx.Err() != nil:
		p.finish("cancelled", "Cancelled")
	case err != nil:
		p.finish("failed", err.Error())
	case r.Status != "success":
		p.finish("failed", "Claude reported failure: "+r.Summary)
	default:
		if cur, _ := git(p.path, "branch", "--show-current"); strings.TrimSpace(cur) != p.temp {
			p.finish("failed", fmt.Sprintf("Claude reported success but the repo is on %q, expected %q", strings.TrimSpace(cur), p.temp))
			return true
		}
		return false
	}
	return true
}

// verify runs drush updb and cex per site and checks the result; it returns what is wrong.
func (p *pipe) verify(baseline map[string]smokeReport, compare bool) []string {
	var problems []string
	for _, s := range p.sites {
		problems = append(problems, p.verifySite(s, baseline[s.Dir], compare)...)
	}
	if len(problems) == 0 {
		p.notes = append(p.notes, "drush updb and cex ran; no database updates or config changes pending")
	}
	return problems
}

func (p *pipe) verifySite(s site, baseline smokeReport, compare bool) []string {
	var problems []string
	at := s.label(p.multi)
	add := func(msg string) { problems = append(problems, at+msg) }
	if _, err := p.step(15*time.Minute, s.drush("updb", "-y")...); err != nil {
		add(err.Error())
	}
	if _, err := p.step(10*time.Minute, s.drush("cex", "-y")...); err != nil {
		add(err.Error())
	}
	if out, err := p.cmd(p.ctx, 5*time.Minute, s.drush("updatedb:status", "--format=json")...); err == nil && pending(out) {
		add("database updates are still pending after drush updb:\n" + tail(out, 1500))
	}
	if out, err := p.cmd(p.ctx, 5*time.Minute, s.drush("config:status", "--format=json")...); err == nil && pending(out) {
		add("configuration differs from the exported config after drush cex:\n" + tail(out, 1500))
	}
	p.log("smoke test: after the update "+at, "")
	after := smoke(p.ctx, p.cmd, s, config.ForProject(claudeCfg.projects.Project(p.name), p.path).SmokePaths)
	switch {
	case after.Skipped != "":
		p.notes = append(p.notes, at+"smoke test skipped: "+after.Skipped)
	case compare && baseline.Skipped == "":
		broken, old := regressions(baseline, after)
		for _, b := range broken {
			add("smoke test regression: " + b)
		}
		for _, e := range newErrors(p.ctx, p.cmd, s, baseline.LastErr) {
			add("new error in the Drupal log: " + e)
		}
		if len(broken) == 0 {
			p.notes = append(p.notes, fmt.Sprintf("%ssmoke test: %d checks pass as before", at, len(after.Checks)-len(old)))
		}
		if len(old) > 0 {
			p.notes = append(p.notes, at+"already failing before the update (not touched): "+strings.Join(old, ", "))
		}
	default: // no baseline (a setup step failed): only what fails now
		for _, c := range after.Checks {
			if !c.OK {
				add("smoke test fails: " + strings.TrimSpace(c.Name+": "+c.Detail))
			}
		}
	}
	return problems
}

// pending reports whether a drush --format=json listing has entries. With nothing pending drush prints a message
// instead of JSON ("No database updates required", "No differences between DB and sync directory"); other output
// that is not JSON counts as pending, so an unexpected answer is never taken for a clean site.
func pending(out string) bool {
	j := strings.TrimSpace(lastJSON(out))
	if !json.Valid([]byte(j)) {
		return j != "" && !strings.Contains(j, "No database updates required") && !strings.Contains(j, "No differences")
	}
	return j != "[]" && j != "{}" && j != "null"
}

// outdated lists what this run is meant to update: security advisories (disaster mode) or minor/patch updates.
// known is false when the list could not be produced; then nothing is skipped.
func (p *pipe) outdated(ignored []string) (list string, n int, known bool) {
	skip := map[string]bool{}
	for _, i := range ignored {
		skip[strings.TrimSpace(i)] = true
	}
	if claudeCfg.disaster {
		adv := p.advisories()
		if adv == nil {
			return "", 0, false
		}
		var keep []string
		for _, a := range adv {
			if !skip[strings.SplitN(a, ": ", 2)[0]] {
				keep = append(keep, a)
			}
		}
		return "- " + strings.Join(keep, "\n- "), len(keep), true
	}
	out, err := p.cmd(p.ctx, 5*time.Minute, "ddev", "composer", "outdated", "--minor-only", "--format=json", "--no-interaction")
	if err != nil {
		return "", 0, false
	}
	var o struct {
		Installed []struct {
			Name    string `json:"name"`
			Version string `json:"version"`
			Latest  string `json:"latest"`
		} `json:"installed"`
	}
	if json.Unmarshal([]byte(lastJSON(out)), &o) != nil {
		return "", 0, false
	}
	var lines []string
	for _, i := range o.Installed {
		if !skip[i.Name] && i.Latest != "" && i.Latest != i.Version {
			lines = append(lines, fmt.Sprintf("%s %s -> %s", i.Name, i.Version, i.Latest))
		}
	}
	if len(lines) == 0 {
		return "", 0, true
	}
	return "- " + strings.Join(lines, "\n- "), len(lines), true
}

// advisories runs composer audit and returns one line per advisory ("package: title (CVE)"); nil when the audit
// could not run. composer audit exits non-zero when it finds advisories, so the output decides, not the exit code.
func (p *pipe) advisories() []string {
	out, _ := p.cmd(p.ctx, 5*time.Minute, "ddev", "composer", "audit", "--format=json", "--no-interaction")
	var a struct {
		Advisories json.RawMessage `json:"advisories"`
	}
	if json.Unmarshal([]byte(lastJSON(out)), &a) != nil || a.Advisories == nil {
		return nil
	}
	byPkg := map[string][]struct {
		Title string `json:"title"`
		CVE   string `json:"cve"`
	}{}
	if json.Unmarshal(a.Advisories, &byPkg) != nil && strings.TrimSpace(string(a.Advisories)) != "[]" {
		return nil // an empty audit is [] rather than {}; anything else unreadable must never look like "no advisories"
	}
	lines := []string{}
	for pkg, list := range byPkg {
		for _, adv := range list {
			l := pkg + ": " + adv.Title
			if adv.CVE != "" {
				l += " (" + adv.CVE + ")"
			}
			lines = append(lines, l)
		}
	}
	sort.Strings(lines)
	return lines
}

// ddevRunning reports whether the project's ddev containers are up.
// known is false when ddev could not tell.
func ddevRunning(ctx context.Context, p *pipe) (running, known bool) {
	out, err := p.cmd(ctx, time.Minute, "ddev", "describe", "-j")
	if err != nil {
		return false, false
	}
	var d struct {
		Raw struct {
			Status string `json:"status"`
		} `json:"raw"`
	}
	if json.Unmarshal([]byte(lastJSON(out)), &d) != nil || d.Raw.Status == "" {
		return false, false
	}
	return d.Raw.Status == "running", true
}

// cleanupDdev stops ddev again, unless it was running before the update started.
func (p *pipe) cleanupDdev() {
	if p.ddevStop {
		p.cmd(context.Background(), 5*time.Minute, "ddev", "stop")
	}
}

// finish ends the run: on failure or cancel the work is kept or stashed and the project returns to its start
// branch; the summary or failure is saved so it survives a restart and informs the next attempt.
func (p *pipe) finish(status, output string) {
	runDir, _ := runDirFor(p.name)
	os.MkdirAll(runDir, 0o700) // a run that ends before Claude starts has not made it yet
	switch status {
	case "failed", "cancelled":
		tmux("kill-session", "-t", "="+SessionName(p.name))
		if p.temp != "" {
			if kept, err := CleanupKeepWork(p.path, p.start, p.temp); err != nil {
				logging.Error("[%s] cleanup: %v", p.name, err)
			} else if kept != "" {
				output += "\nThe work done so far is kept on branch " + kept
			}
		}
		p.cleanupDdev()
		if status == "failed" {
			logging.Error("[%s] %s", p.name, output)
			os.WriteFile(filepath.Join(runDir, "last-failure.txt"), []byte(output), 0o600)
		}
	case "success":
		os.Remove(filepath.Join(runDir, "last-failure.txt"))
		os.WriteFile(filepath.Join(runDir, "summary.md"), []byte(output), 0o600)
		logging.Info("[%s] update finished", p.name)
	case "skipped":
		os.Remove(filepath.Join(runDir, "last-failure.txt"))
	}
	p.out <- StatusUpdate{ProjectName: p.name, Status: status, Command: "update", Output: output}
}

// LastSummary returns the summary of a project's last successful run, kept on disk for merge requests made after
// a restart.
func LastSummary(project string) string {
	runDir, err := runDirFor(project)
	if err != nil {
		return ""
	}
	f, err := os.Open(filepath.Join(runDir, "summary.md"))
	if err != nil {
		return ""
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		return ""
	}
	b, _ := io.ReadAll(io.LimitReader(f, 64<<10)) // it becomes a command-line argument for glab
	return string(b)
}

func writeJSON(path string, v any) {
	if b, err := json.MarshalIndent(v, "", "  "); err == nil {
		os.WriteFile(path, b, 0o644)
	}
}
