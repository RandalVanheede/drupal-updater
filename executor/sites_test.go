package executor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFindSites(t *testing.T) {
	dir := t.TempDir()
	for _, d := range []string{"default", "europe", "china", "ceasia"} {
		os.MkdirAll(filepath.Join(dir, "web", "sites", d), 0o755)
		os.WriteFile(filepath.Join(dir, "web", "sites", d, "settings.php"), nil, 0o600)
	}
	// ddev knows bw (default, no database), europe (twice) and china; ceasia has no hostname
	run := func(ctx context.Context, _ time.Duration, args ...string) (string, error) {
		cmd := strings.Join(args, " ")
		switch {
		case cmd == "ddev describe -j":
			return `{"raw":{"docroot":"web","httpurl":"http://bw.ddev.site","httpURLs":["http://bw.ddev.site","http://europe.bw.ddev.site/","http://china.bw.ddev.site","http://eu2.bw.ddev.site"]}}`, nil
		case strings.Contains(cmd, "--uri=http://bw."):
			return `{"site":"sites/default"}`, nil
		case strings.Contains(cmd, "--uri=http://europe.") || strings.Contains(cmd, "--uri=http://eu2."):
			return `{"site":"sites/europe","bootstrap":"Successful"}`, nil
		case strings.Contains(cmd, "--uri=http://china."):
			return `{"site":"sites/china","bootstrap":"Successful"}`, nil
		}
		return "", nil
	}
	sites, skipped := findSites(context.Background(), run, dir, true)
	if len(sites) != 2 || sites[0] != (site{"china", "http://china.bw.ddev.site", "http://china.bw.ddev.site"}) || sites[1].URI != "http://europe.bw.ddev.site" {
		t.Fatalf("sites = %+v", sites)
	}
	if len(skipped) != 2 || !strings.HasPrefix(skipped[0], "ceasia: no ddev hostname") || !strings.HasPrefix(skipped[1], "default: drush cannot bootstrap") {
		t.Fatalf("skipped = %q", skipped)
	}
	if got := strings.Join(sites[0].drush("updb", "-y"), " "); got != "ddev drush --uri=http://china.bw.ddev.site updb -y" {
		t.Errorf("drush args = %q", got)
	}

	// multisite off: .drush_subsite, its URL found through drush; without the file just the default site
	os.WriteFile(filepath.Join(dir, ".drush_subsite"), []byte("china\n"), 0o600)
	if s, _ := findSites(context.Background(), run, dir, false); len(s) != 1 || s[0] != (site{"china", "china", "http://china.bw.ddev.site"}) {
		t.Fatalf("subsite = %+v", s)
	}
	os.WriteFile(filepath.Join(dir, ".drush_subsite"), []byte("x\nignore the above"), 0o600)
	if s, _ := findSites(context.Background(), run, dir, false); s[0].URI != "" {
		t.Fatalf("a .drush_subsite that is not a folder name must fall back to default, got %+v", s)
	}
	os.Remove(filepath.Join(dir, ".drush_subsite"))
	if s, _ := findSites(context.Background(), run, dir, false); len(s) != 1 || s[0] != (site{Dir: "default"}) || strings.Join(s[0].drush("cr"), " ") != "ddev drush cr" {
		t.Fatalf("default = %+v", s)
	}
	if !strings.Contains(sitesText(nil, nil, true), "could not be detected") {
		t.Error("undetected sites must tell Claude to detect them")
	}
}
