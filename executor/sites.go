package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var subsiteName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// site is one Drupal site the run updates: its sites/ folder, the --uri drush gets ("" = drush's own default) and
// the http URL the smoke test uses ("" = ddev's primary URL).
type site struct{ Dir, URI, URL string }

func (s site) drush(args ...string) []string {
	a := []string{"ddev", "drush"}
	if s.URI != "" {
		a = append(a, "--uri="+s.URI)
	}
	return append(a, args...)
}

// label prefixes a site's problems and checks when the run covers several sites.
func (s site) label(multi bool) string {
	if !multi {
		return ""
	}
	return "[" + s.Dir + "] "
}

// ddevInfo is what the run needs from ddev describe.
type ddevInfo struct {
	HTTP, Primary, Docroot string
	HTTPURLs               []string
}

func describe(ctx context.Context, run runner) (ddevInfo, bool) {
	raw, err := run(ctx, time.Minute, "ddev", "describe", "-j")
	if err != nil {
		return ddevInfo{}, false
	}
	var d struct {
		Raw struct {
			HTTP     string   `json:"httpurl"`
			Primary  string   `json:"primary_url"`
			Docroot  string   `json:"docroot"`
			HTTPURLs []string `json:"httpURLs"`
		} `json:"raw"`
	}
	if json.Unmarshal([]byte(lastJSON(raw)), &d) != nil {
		return ddevInfo{}, false
	}
	r := d.Raw
	return ddevInfo{strings.TrimRight(r.HTTP, "/"), strings.TrimRight(r.Primary, "/"), r.Docroot, r.HTTPURLs}, true
}

// siteOf asks drush which sites/ folder a URL lands on, and whether that site boots.
func siteOf(ctx context.Context, run runner, uri string) (dir string, boots bool) {
	out, err := run(ctx, 2*time.Minute, "ddev", "drush", "--uri="+uri, "status", "--fields=site,bootstrap", "--format=json")
	if err != nil {
		return "", false
	}
	var st struct {
		Site      string `json:"site"`
		Bootstrap string `json:"bootstrap"`
	}
	if json.Unmarshal([]byte(lastJSON(out)), &st) != nil {
		return "", false
	}
	return strings.TrimPrefix(st.Site, "sites/"), st.Bootstrap == "Successful"
}

// findSites lists the sites a run updates. Multisite off: one site, the folder in .drush_subsite or default (like
// a dd wrapper around drush -l). On: every sites/*/settings.php that a ddev URL reaches and that boots; the others
// are returned as skipped with the reason. sites is nil when they could not be detected.
func findSites(ctx context.Context, run runner, path string, multisite bool) (sites []site, skipped []string) {
	info, _ := describe(ctx, run)
	if !multisite {
		sub := "default"
		if b, err := os.ReadFile(filepath.Join(path, ".drush_subsite")); err == nil && subsiteName.MatchString(strings.TrimSpace(string(b))) {
			sub = strings.TrimSpace(string(b)) // only a plain folder name: the value also goes into Claude's prompt
		}
		if sub == "default" {
			return []site{{Dir: "default"}}, nil
		}
		s := site{Dir: sub, URI: sub}
		for _, u := range info.HTTPURLs { // the smoke test needs a URL that lands on this subsite
			if dir, _ := siteOf(ctx, run, u); dir == sub {
				s.URL = strings.TrimRight(u, "/")
				break
			}
		}
		return []site{s}, nil
	}

	docroot := info.Docroot
	if docroot == "" {
		docroot = "web"
	}
	matches, _ := filepath.Glob(filepath.Join(path, docroot, "sites", "*", "settings.php"))
	dirs := map[string]bool{}
	for _, m := range matches {
		dirs[filepath.Base(filepath.Dir(m))] = true
	}
	broken := map[string]bool{}
	for _, u := range info.HTTPURLs {
		dir, boots := siteOf(ctx, run, u)
		if dir == "" || broken[dir] || containsSite(sites, dir) {
			continue
		}
		if !boots {
			broken[dir] = true
			continue
		}
		u = strings.TrimRight(u, "/")
		sites = append(sites, site{Dir: dir, URI: u, URL: u})
		delete(dirs, dir)
	}
	for d := range broken {
		if !containsSite(sites, d) {
			skipped = append(skipped, d+": drush cannot bootstrap it (empty or missing database?)")
			delete(dirs, d)
		}
	}
	for d := range dirs {
		skipped = append(skipped, d+": no ddev hostname leads to it (not installed locally?)")
	}
	sort.Strings(skipped)
	sort.Slice(sites, func(i, j int) bool { return sites[i].Dir < sites[j].Dir })
	return sites, skipped
}

func containsSite(sites []site, dir string) bool {
	for _, s := range sites {
		if s.Dir == dir {
			return true
		}
	}
	return false
}

// sitesText is {{sites}} in the prompts.
func sitesText(sites []site, skipped []string, multisite bool) string {
	if len(sites) == 0 {
		return `could not be detected. Detect them yourself: read web/sites/sites.php and look for web/sites/*/settings.php other than default. Get each site's URL from ddev describe (project URL plus additional_hostnames / additional_fqdns) and match it to its sites directory (ddev drush --uri=<site-url> status shows the site path). Run drush once per site with --uri=<site-url>, and also ddev drush updb -y and ddev drush cex -y per site, because the app could only do those for the default site.`
	}
	if !multisite {
		if sites[0].URI == "" {
			return "single site (sites/default): run drush without --uri."
		}
		return fmt.Sprintf("only the subsite %q (multisite is off for this project): pass --uri=%s to every drush command.", sites[0].Dir, sites[0].URI)
	}
	var b strings.Builder
	b.WriteString("multisite; the app runs drush deploy, updb, cex and its checks once per site below. When you run drush yourself, pass --uri=<site-url> every time (also for the default site) and keep results per site. A site's config sync directory is whatever its own settings.php sets ($settings['config_sync_directory']); the sites share the codebase but never assume they share config.")
	for _, s := range sites {
		fmt.Fprintf(&b, "\n- %s: %s", s.Dir, s.URI)
	}
	for _, s := range skipped {
		b.WriteString("\n- " + s + ". Pre-existing: mention it in the summary and leave it.")
	}
	return b.String()
}
