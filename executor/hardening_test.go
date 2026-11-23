package executor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVendorHardeningIssue(t *testing.T) {
	dir := t.TempDir()
	if got := vendorHardeningIssue(dir); got != "" {
		t.Fatalf("no composer.json must not count as an issue, got %q", got)
	}
	write := func(s string) { os.WriteFile(filepath.Join(dir, "composer.json"), []byte(s), 0o600) }
	write(`{"require":{"drupal/core-recommended":"^11"},"config":{"allow-plugins":{"drupal/core-vendor-hardening":false}},"extra":{"drupal-core-vendor-hardening":{"drupal/core":["install.php"]}}}`)
	got := vendorHardeningIssue(dir)
	for _, want := range []string{"not in require", "allow-plugins", "CHANGELOG.txt"} {
		if !strings.Contains(got, want) {
			t.Errorf("issue %q lacks %q", got, want)
		}
	}
	if strings.Contains(got, "install.php") {
		t.Errorf("install.php is present, got %q", got)
	}
	write(`{"require":{"drupal/core-vendor-hardening":"^11"},"config":{"allow-plugins":true},"extra":{"drupal-core-vendor-hardening":{"drupal/core":["install.php","CHANGELOG.txt","COPYRIGHT.txt","INSTALL.mysql.txt","INSTALL.pgsql.txt","INSTALL.sqlite.txt","INSTALL.txt","LICENSE.txt","MAINTAINERS.txt","UPDATE.txt","USAGE.txt","extra.txt"]}}}`)
	if got := vendorHardeningIssue(dir); got != "" {
		t.Errorf("complete setup reported %q", got)
	}
}
