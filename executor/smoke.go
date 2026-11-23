package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"sort"
	"strings"
	"time"
)

// smokeCheck is one smoke test result: a page, a drush command or the error log.
type smokeCheck struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// smokeReport is what the checks found, plus the newest Error-level log entry at that moment, so the run after the
// update can tell new errors from old ones.
type smokeReport struct {
	Checks  []smokeCheck `json:"checks"`
	LastErr int          `json:"last_error_wid"`
	Skipped string       `json:"skipped,omitempty"` // why the site could not be checked at all
}

// errorMarkers in a page's HTML mean Drupal failed even when it still answered 200.
var errorMarkers = []string{"The website encountered an unexpected error", "PHP Fatal error", "Fatal error:", "Uncaught "}

// runner runs a fixed command in the project and returns its combined output; pipe.cmd in production.
type runner func(ctx context.Context, timeout time.Duration, args ...string) (string, error)

// localClient talks to the project's ddev site over its plain http URL (the local router), so no certificate is
// involved and verification never needs to be switched off.
// Redirects are followed only within the site itself.
func localClient(jar http.CookieJar) *http.Client {
	return &http.Client{Timeout: 45 * time.Second, Jar: jar, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("stopped after 10 redirects")
		}
		if req.URL.Host != via[0].URL.Host {
			return http.ErrUseLastResponse // the page answered with a redirect elsewhere: judged by that answer
		}
		return nil
	}}
}

// smoke checks the site the way a person would before and after an update: drush boots, the main anonymous and
// admin pages answer without errors, an unknown path gives 404, and the newest Error-level log entry.
func smoke(ctx context.Context, run runner, s site, extra []string) smokeReport {
	var rep smokeReport
	add := func(name string, ok bool, detail string) {
		rep.Checks = append(rep.Checks, smokeCheck{name, ok, detail})
	}

	base := s.URL
	if base == "" {
		info, _ := describe(ctx, run)
		base = info.HTTP
		if base == "" {
			base = info.Primary
		}
	}
	u, perr := url.Parse(base)
	if base == "" || perr != nil || u.Host == "" {
		rep.Skipped = "the site URL could not be read from ddev describe"
		return rep
	}

	out, err := run(ctx, 2*time.Minute, s.drush("status", "--field=bootstrap")...)
	add("drush status", err == nil && strings.Contains(out, "Successful"), firstLine(out))
	_, err = run(ctx, 5*time.Minute, s.drush("cr")...)
	add("drush cr", err == nil, "")

	anon := localClient(nil)
	page := func(c *http.Client, label, path string, want int) {
		ok, detail := fetch(ctx, c, base+path, want)
		add(label+" "+path, ok, detail)
	}
	page(anon, "anonymous", "/", 200)
	page(anon, "anonymous", "/user/login", 200)
	page(anon, "anonymous", "/drupal-updater-missing-page", 404)

	// newest published node of each content type, so every node template renders once
	types := map[string]int{}
	if out, err := run(ctx, 2*time.Minute, s.drush("php:eval",
		`$o=[];if(\Drupal::moduleHandler()->moduleExists('node')){foreach(\Drupal::entityTypeManager()->getStorage('node_type')->loadMultiple() as $t){$i=\Drupal::entityQuery('node')->accessCheck(FALSE)->condition('type',$t->id())->condition('status',1)->sort('nid','DESC')->range(0,1)->execute();$o[$t->id()]=$i?(int)reset($i):0;}}echo json_encode($o);`)...); err == nil {
		json.Unmarshal([]byte(lastJSON(out)), &types)
	}
	names := make([]string, 0, len(types))
	for t := range types {
		names = append(names, t)
	}
	sort.Strings(names)
	if len(names) > 50 {
		names = names[:50] // shortcut: a site with more content types gets only the first 50 checked
	}
	for _, t := range names {
		if nid := types[t]; nid > 0 {
			page(anon, "anonymous", fmt.Sprintf("/node/%d", nid), 200)
		}
	}

	// admin pages through a one-time login link
	jar, _ := cookiejar.New(nil)
	admin := localClient(jar)
	loggedIn := false
	if out, err := run(ctx, 2*time.Minute, s.drush("uli", "--no-browser")...); err == nil {
		if link := strings.TrimSpace(lastLine(out)); strings.HasPrefix(link, "http") {
			if l, err := url.Parse(link); err == nil {
				l.Scheme, l.Host = u.Scheme, u.Host // drush may not know the site's own URL
				ok, _ := fetch(ctx, admin, l.String(), 200)
				loggedIn = ok
			}
		}
	}
	add("admin login (drush uli)", loggedIn, "")
	if loggedIn {
		for _, p := range []string{"/admin/content", "/admin/structure", "/admin/config", "/admin/reports/status"} {
			page(admin, "admin", p, 200)
		}
		for _, t := range names {
			page(admin, "admin", "/node/add/"+t, 200)
		}
	}
	for _, p := range extra {
		p = strings.TrimSpace(p)
		if rest, ok := strings.CutPrefix(p, "admin:"); ok {
			if loggedIn {
				page(admin, "admin", "/"+strings.TrimLeft(rest, "/"), 200)
			}
		} else if p != "" {
			page(anon, "anonymous", "/"+strings.TrimLeft(p, "/"), 200)
		}
	}

	if out, err := run(ctx, 2*time.Minute, s.drush("watchdog:show", "--severity=Error", "--count=1", "--format=json")...); err == nil {
		var rows []struct {
			Wid json.Number `json:"wid"`
		}
		if json.Unmarshal([]byte(lastJSON(out)), &rows) == nil && len(rows) > 0 {
			n, _ := rows[0].Wid.Int64()
			rep.LastErr = int(n)
		}
	}
	return rep
}

// fetch requests a page and reports whether it answered want without Drupal error output.
func fetch(ctx context.Context, c *http.Client, u string, want int) (bool, string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return false, err.Error()
	}
	resp, err := c.Do(req)
	if err != nil {
		return false, err.Error()
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != want {
		return false, fmt.Sprintf("HTTP %d, expected %d", resp.StatusCode, want)
	}
	for _, m := range errorMarkers {
		if strings.Contains(string(body), m) {
			return false, fmt.Sprintf("HTTP %d but the page shows %q", resp.StatusCode, m)
		}
	}
	return true, ""
}

// regressions lists the checks that passed in before and fail in after; checks that already failed before are
// pre-existing and returned separately.
func regressions(before, after smokeReport) (broken, preexisting []string) {
	was := map[string]bool{}
	for _, c := range before.Checks {
		was[c.Name] = c.OK
	}
	for _, c := range after.Checks {
		ok, known := was[c.Name]
		switch {
		case c.OK:
		case known && !ok:
			preexisting = append(preexisting, c.Name)
		default: // passed before, or new and failing
			broken = append(broken, strings.TrimSpace(c.Name+": "+c.Detail))
		}
	}
	return broken, preexisting
}

// newErrors lists Error-level log entries written after the baseline.
func newErrors(ctx context.Context, run runner, s site, since int) []string {
	out, err := run(ctx, 2*time.Minute, s.drush("watchdog:show", "--severity=Error", "--count=20", "--format=json")...)
	if err != nil {
		return nil
	}
	var rows []struct {
		Wid     json.Number `json:"wid"`
		Message string      `json:"message"`
	}
	if json.Unmarshal([]byte(lastJSON(out)), &rows) != nil {
		return nil
	}
	var msgs []string
	for _, r := range rows {
		if n, _ := r.Wid.Int64(); int(n) > since {
			msgs = append(msgs, truncate(r.Message, 300))
		}
	}
	return msgs
}

// lastJSON returns the JSON at the end of a command's output, skipping any lines ddev or drush print before it.
// Only line starts are tried, and at most 50 of them, so a long log before the JSON stays cheap.
func lastJSON(out string) string {
	out = strings.TrimSpace(out)
	for i, tries := 0, 0; i < len(out) && tries < 50; {
		if out[i] == '{' || out[i] == '[' {
			tries++
			if json.Valid([]byte(out[i:])) {
				return out[i:]
			}
		}
		next := strings.IndexByte(out[i:], '\n')
		if next < 0 {
			break
		}
		i += next + 1
	}
	return out
}

func firstLine(s string) string { l, _, _ := strings.Cut(strings.TrimSpace(s), "\n"); return l }

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
