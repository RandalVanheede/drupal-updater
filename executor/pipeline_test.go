package executor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"drupal-updater/config"
)

// fakeDdev answers the ddev commands the pipeline runs; its answers come from environment variables the test sets.
const fakeDdev = `#!/bin/sh
case "$*" in
  "describe -j") echo "{\"raw\":{\"status\":\"${FAKE_DDEV_STATUS:-running}\",\"httpurl\":\"$FAKE_URL\"}}" ;;
  "composer install --no-interaction") [ -n "$FAKE_FAIL_INSTALL" ] && { echo "Your lock file is out of sync"; exit 1; }; echo ok ;;
  "composer outdated"*) echo "$FAKE_OUTDATED" ;;
  "composer audit"*) echo "$FAKE_AUDIT"; [ "$FAKE_AUDIT" = '{"advisories":[]}' ] || exit 1 ;;
  "drush status --field=bootstrap") echo "Successful" ;;
  "drush php:eval"*) echo '{"page":1}' ;;
  "drush uli --no-browser") echo "$FAKE_URL/user/reset/1/abc/def/login" ;;
  "drush watchdog:show"*) echo '[]' ;;
  "drush updatedb:status --format=json") echo ' [success] No database updates required.' >&2 ;;
  "drush config:status --format=json") echo ' [notice] No differences between DB and sync directory.' >&2 ;;
  *) echo "ok: $*" ;;
esac
`

type harness struct {
	t        *testing.T
	repo     string
	broken   atomic.Bool // the site answers 500 on /node/1
	prompts  []string
	sessions []string
	onUpdate func() // what the stand-in for Claude does in the update session
	onRepair func()
}

func newHarness(t *testing.T, mode string) *harness {
	for _, kv := range [][2]string{{"GIT_AUTHOR_NAME", "t"}, {"GIT_AUTHOR_EMAIL", "t@e.x"}, {"GIT_COMMITTER_NAME", "t"}, {"GIT_COMMITTER_EMAIL", "t@e.x"}} {
		t.Setenv(kv[0], kv[1])
	}
	t.Setenv("HOME", t.TempDir()) // run files go to ~/.config/drupal-updater/runs
	h := &harness{t: t}

	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/" || r.URL.Path == "/user/login" || strings.HasPrefix(r.URL.Path, "/admin") || strings.HasPrefix(r.URL.Path, "/node/add"):
			w.Write([]byte("<html>fine</html>"))
		case strings.HasPrefix(r.URL.Path, "/user/reset/"):
			http.SetCookie(w, &http.Cookie{Name: "SESS", Value: "1", Path: "/"})
			w.Write([]byte("logged in"))
		case r.URL.Path == "/node/1":
			if h.broken.Load() {
				w.WriteHeader(500)
				w.Write([]byte("The website encountered an unexpected error"))
				return
			}
			w.Write([]byte("<html>node</html>"))
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(site.Close)
	t.Setenv("FAKE_URL", site.URL)

	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "ddev"), []byte(fakeDdev), 0o755)
	oldLook, oldSession := lookTool, session
	lookTool = func(name string) string {
		if name == "ddev" {
			return filepath.Join(bin, "ddev")
		}
		return oldLook(name)
	}
	session = func(ctx context.Context, name, path, prompt, label string, out chan<- StatusUpdate) (claudeResult, error) {
		h.prompts = append(h.prompts, prompt)
		h.sessions = append(h.sessions, label)
		if ctx.Err() != nil {
			return claudeResult{}, ctx.Err()
		}
		if label == "update" && h.onUpdate != nil {
			h.onUpdate()
		}
		if strings.HasPrefix(label, "repair") && h.onRepair != nil {
			h.onRepair()
		}
		return claudeResult{Status: "success", Summary: label + " done"}, nil
	}
	t.Cleanup(func() { lookTool, session = oldLook, oldSession })

	root := t.TempDir()
	origin := filepath.Join(root, "o.git")
	h.repo = filepath.Join(root, "site")
	run(t, root, "init", "--bare", "-q", "-b", "main", origin)
	run(t, root, "clone", "-q", origin, h.repo)
	os.WriteFile(filepath.Join(h.repo, "composer.lock"), []byte(`{"packages":[]}`), 0o644)
	run(t, h.repo, "add", ".")
	run(t, h.repo, "commit", "-qm", "init")
	run(t, h.repo, "push", "-q", "origin", "HEAD:main")
	run(t, h.repo, "branch", "-u", "origin/main")

	Configure(&config.Config{UpdateMode: mode})
	return h
}

// changeLock is what an update does to the repository.
func (h *harness) changeLock() {
	os.WriteFile(filepath.Join(h.repo, "composer.lock"), []byte(`{"packages":[{"name":"drupal/token","version":"1.1.0","type":"drupal-module"}]}`), 0o644)
}

func (h *harness) run(ctx context.Context) StatusUpdate {
	out := make(chan StatusUpdate, 1000)
	go func() { Run(ctx, "site", h.repo, "main", "drupal-updater/temp-site", out); close(out) }()
	var last StatusUpdate
	for u := range out {
		if u.Status != "running" {
			last = u
		}
	}
	return last
}

func (h *harness) branch() string {
	b, _ := git(h.repo, "branch", "--show-current")
	return strings.TrimSpace(b)
}

func TestNothingOutdatedIsSkippedWithoutClaude(t *testing.T) {
	h := newHarness(t, "standard")
	t.Setenv("FAKE_OUTDATED", `{"installed":[]}`)
	got := h.run(context.Background())
	if got.Status != "skipped" || len(h.sessions) != 0 {
		t.Fatalf("got %s (%q), sessions %v", got.Status, got.Output, h.sessions)
	}
	if h.branch() != "main" {
		t.Fatalf("a skipped project goes back to its start branch, on %q", h.branch())
	}
}

func TestDisasterModeSkipsWithoutAdvisories(t *testing.T) {
	h := newHarness(t, "disaster")
	t.Setenv("FAKE_AUDIT", `{"advisories":[]}`)
	if got := h.run(context.Background()); got.Status != "skipped" || len(h.sessions) != 0 {
		t.Fatalf("got %s, sessions %v", got.Status, h.sessions)
	}
}

func TestUpdateGetsTheListAndIsVerified(t *testing.T) {
	h := newHarness(t, "disaster")
	t.Setenv("FAKE_AUDIT", `{"advisories":{"drupal/token":[{"title":"Access bypass","cve":"CVE-1"}]}}`)
	h.onUpdate = h.changeLock
	got := h.run(context.Background())
	if got.Status != "success" {
		t.Fatalf("got %s: %s", got.Status, got.Output)
	}
	if !strings.Contains(h.prompts[0], "drupal/token: Access bypass (CVE-1)") || strings.Contains(h.prompts[0], "{{") {
		t.Fatalf("the prompt should carry the advisory list and no placeholders:\n%s", h.prompts[0])
	}
	if !strings.Contains(h.prompts[0], "do not update anything that has no advisory") && !strings.Contains(h.prompts[0], "Do not update anything that has no advisory") {
		t.Fatal("disaster mode must forbid non-security updates, core included")
	}
	if !strings.Contains(got.Output, "Checked by the app") || !strings.Contains(got.Output, "no database updates or config changes pending") {
		t.Fatalf("summary should report the checks: %s", got.Output)
	}
	if h.branch() != "drupal-updater/temp-site" {
		t.Fatalf("a successful update stays on the temp branch, on %q", h.branch())
	}
	if LastSummary("site") == "" {
		t.Fatal("the summary should be kept for merge requests after a restart")
	}
}

func TestRegressionGoesToARepairSession(t *testing.T) {
	h := newHarness(t, "standard")
	t.Setenv("FAKE_OUTDATED", `{"installed":[{"name":"drupal/token","version":"1.0.0","latest":"1.1.0"}]}`)
	h.onUpdate = func() { h.changeLock(); h.broken.Store(true) }
	h.onRepair = func() { h.broken.Store(false) }
	got := h.run(context.Background())
	if got.Status != "success" || len(h.sessions) != 2 || h.sessions[1] != "repair 1" {
		t.Fatalf("got %s, sessions %v: %s", got.Status, h.sessions, got.Output)
	}
	if !strings.Contains(h.prompts[1], "/node/1") {
		t.Fatalf("the repair prompt should name the broken page:\n%s", h.prompts[1])
	}
}

func TestUnfixableRegressionFailsAndCleansUp(t *testing.T) {
	h := newHarness(t, "standard")
	t.Setenv("FAKE_OUTDATED", `{"installed":[{"name":"drupal/token","version":"1.0.0","latest":"1.1.0"}]}`)
	h.onUpdate = func() { h.changeLock(); h.broken.Store(true) }
	got := h.run(context.Background())
	if got.Status != "failed" || len(h.sessions) != 1+maxRepairs || !strings.Contains(got.Output, "/node/1") {
		t.Fatalf("got %s, sessions %v: %s", got.Status, h.sessions, got.Output)
	}
	if h.branch() != "main" {
		t.Fatalf("a failed run returns to the start branch, on %q", h.branch())
	}
	runDir, _ := runDirFor("site")
	if b, _ := os.ReadFile(filepath.Join(runDir, "last-failure.txt")); len(b) == 0 {
		t.Fatal("the failure should be kept for the next attempt")
	}
	// the next attempt is told what went wrong
	h.broken.Store(false)
	h.onUpdate = h.changeLock
	h.run(context.Background())
	if !strings.Contains(h.prompts[len(h.prompts)-1], "previous attempt on this project failed") {
		t.Fatal("a retry should carry the previous failure")
	}
}

func TestFailingSetupStepIsHandedToClaude(t *testing.T) {
	h := newHarness(t, "standard")
	t.Setenv("FAKE_FAIL_INSTALL", "1")
	h.onUpdate = h.changeLock
	got := h.run(context.Background())
	if len(h.prompts) == 0 || !strings.Contains(h.prompts[0], "composer install --no-interaction` failed") || !strings.Contains(h.prompts[0], "lock file is out of sync") {
		t.Fatalf("Claude should get the failed step and its output; status %s, prompts %v", got.Status, h.prompts)
	}
}

func TestNoLockChangeIsSkipped(t *testing.T) {
	h := newHarness(t, "standard")
	t.Setenv("FAKE_OUTDATED", `{"installed":[{"name":"drupal/token","version":"1.0.0","latest":"1.1.0"}]}`)
	got := h.run(context.Background()) // the stand-in changes nothing
	if got.Status != "skipped" || h.branch() != "main" {
		t.Fatalf("an update that changed nothing leaves nothing to merge: %s on %q", got.Status, h.branch())
	}
}

func TestCancelReturnsToTheStartBranch(t *testing.T) {
	h := newHarness(t, "standard")
	t.Setenv("FAKE_OUTDATED", `{"installed":[{"name":"drupal/token","version":"1.0.0","latest":"1.1.0"}]}`)
	ctx, cancel := context.WithCancel(context.Background())
	h.onUpdate = func() { h.changeLock(); cancel(); time.Sleep(10 * time.Millisecond) }
	oldSession := session
	session = func(c context.Context, name, path, prompt, label string, out chan<- StatusUpdate) (claudeResult, error) {
		oldSession(c, name, path, prompt, label, out)
		return claudeResult{}, c.Err()
	}
	t.Cleanup(func() { session = oldSession })
	got := h.run(ctx)
	if got.Status != "cancelled" || h.branch() != "main" {
		t.Fatalf("got %s on %q", got.Status, h.branch())
	}
}

func TestLimitHitAndCapFor(t *testing.T) {
	if limitHit("doing work\n⎿ API Error: 429 rate_limit_error\n> ") == "" {
		t.Error("a 429 at the end of the screen is a limit")
	}
	if limitHit(strings.Repeat("x\n", 40)+"all good\n") != "" {
		t.Error("no limit on a normal screen")
	}
	if got := capFor(8, 8*(1<<30)); got != 3 {
		t.Errorf("8 GiB of Docker memory holds 3 projects, got %d", got)
	}
	if got := capFor(2, 32*(1<<30)); got != 2 {
		t.Errorf("a lower setting is kept, got %d", got)
	}
	if got := capFor(4, 1<<30); got != 1 {
		t.Errorf("at least one project always runs, got %d", got)
	}
}

func TestRunFolderNamesDoNotCollide(t *testing.T) {
	if safeName("my-site") != "my-site" {
		t.Error("plain names stay as they are")
	}
	if a, b := safeName("my.site"), safeName("my-site"); a == b {
		t.Errorf("%q and %q must not share a run folder", a, b)
	}
	if a, b := safeName("client/site"), safeName("client-site"); a == b {
		t.Errorf("%q and %q must not share a run folder", a, b)
	}
}

func TestMergeRequestTextCannotActOnGitLab(t *testing.T) {
	got := mrDescription("Updated core.\n/assign @all\n  /label ~urgent\nmail @here")
	for _, bad := range []string{"\n/assign", "\n  /label", "@all", "@here"} {
		if strings.Contains(got, bad) {
			t.Errorf("%q survived: %q", bad, got)
		}
	}
	if !strings.Contains(got, "Updated core.") {
		t.Error("ordinary text is kept")
	}
}

func TestSummaryBelongsToTheLatestRun(t *testing.T) {
	h := newHarness(t, "standard")
	t.Setenv("FAKE_OUTDATED", `{"installed":[{"name":"drupal/token","version":"1.0.0","latest":"1.1.0"}]}`)
	h.onUpdate = h.changeLock
	if got := h.run(context.Background()); got.Status != "success" || LastSummary("site") == "" {
		t.Fatalf("first run: %s", got.Status)
	}
	git(h.repo, "checkout", "-q", "main")
	t.Setenv("FAKE_OUTDATED", `{"installed":[]}`)
	h.onUpdate = nil
	if got := h.run(context.Background()); got.Status != "skipped" {
		t.Fatalf("second run: %s", got.Status)
	}
	if LastSummary("site") != "" {
		t.Fatal("a later run must not leave an older run's summary for the merge request")
	}
}

func TestDrushNothingPendingMessagesAreClean(t *testing.T) {
	for out, want := range map[string]bool{
		" [success] No database updates required.\n":              false,
		" [notice] No differences between DB and sync directory.": false,
		"[]": false,
		"":   false,
		`[{"module":"system","update_id":"11201"}]`: true,
		"PHP Warning: something unexpected":         true,
	} {
		if got := pending(out); got != want {
			t.Errorf("pending(%q) = %v, want %v", out, got, want)
		}
	}
}

func TestUnreadableAuditIsNeverASkip(t *testing.T) {
	h := newHarness(t, "disaster")
	t.Setenv("FAKE_AUDIT", `{"advisories":"something new"}`)
	h.onUpdate = h.changeLock
	if got := h.run(context.Background()); got.Status == "skipped" || len(h.sessions) == 0 {
		t.Fatalf("an audit that cannot be read must go to Claude, got %s, sessions %v", got.Status, h.sessions)
	}
}

func TestRemoteHost(t *testing.T) {
	for in, want := range map[string]string{
		"git@gitlab.example.com:group/site.git":        "gitlab.example.com",
		"https://gitlab.example.com/group/site.git":    "gitlab.example.com",
		"ssh://git@gitlab.example.com:2222/g/site.git": "gitlab.example.com",
	} {
		if got := remoteHost(in); got != want {
			t.Errorf("remoteHost(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRepoSlug(t *testing.T) {
	for in, want := range map[string]string{
		"git@github.com:assetkiwi/marketing-site.git":     "assetkiwi/marketing-site",
		"https://github.com/assetkiwi/marketing-site.git": "assetkiwi/marketing-site",
		"ssh://git@github.com/assetkiwi/marketing-site":   "assetkiwi/marketing-site",
	} {
		if got := repoSlug(in); got != want {
			t.Errorf("repoSlug(%q) = %q, want %q", in, got, want)
		}
	}
	for _, bad := range []string{repoSlug("https://github.com"), repoSlug("git@github.com:"), repoSlug("git@github.com:evil.example/o/r"), "../x", "-x"} {
		if ghSlug.MatchString(bad) && !strings.HasPrefix(bad, ".") {
			t.Errorf("slug %q should be refused", bad)
		}
	}
	if !mrURL.MatchString("https://github.com/o/r/pull/12") {
		t.Error("a GitHub pull request URL should be recognised")
	}
}
