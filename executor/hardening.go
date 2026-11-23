package executor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// hardenedCoreFiles must be in composer.json's extra.drupal-core-vendor-hardening["drupal/core"].
var hardenedCoreFiles = []string{"install.php", "CHANGELOG.txt", "COPYRIGHT.txt", "INSTALL.mysql.txt", "INSTALL.pgsql.txt",
	"INSTALL.sqlite.txt", "INSTALL.txt", "LICENSE.txt", "MAINTAINERS.txt", "UPDATE.txt", "USAGE.txt"}

// vendorHardeningIssue says what is missing for drupal/core-vendor-hardening in the project's composer.json, so a
// project that is otherwise up to date still gets a session to add it. "" = fine, or composer.json unreadable.
func vendorHardeningIssue(path string) string {
	b, err := os.ReadFile(filepath.Join(path, "composer.json"))
	if err != nil {
		return ""
	}
	var c struct {
		Require map[string]any `json:"require"`
		Config  struct {
			AllowPlugins any `json:"allow-plugins"`
		} `json:"config"`
		Extra struct {
			Hardening map[string][]string `json:"drupal-core-vendor-hardening"`
		} `json:"extra"`
	}
	if json.Unmarshal(b, &c) != nil {
		return ""
	}
	var issues []string
	if _, ok := c.Require["drupal/core-vendor-hardening"]; !ok {
		issues = append(issues, "not in require")
	}
	allowed := c.Config.AllowPlugins == true
	if m, ok := c.Config.AllowPlugins.(map[string]any); ok {
		allowed = m["drupal/core-vendor-hardening"] == true
	}
	if !allowed {
		issues = append(issues, "not allowed in config.allow-plugins")
	}
	have := map[string]bool{}
	for _, f := range c.Extra.Hardening["drupal/core"] {
		have[f] = true
	}
	var missing []string
	for _, f := range hardenedCoreFiles {
		if !have[f] {
			missing = append(missing, f)
		}
	}
	if len(missing) > 0 {
		issues = append(issues, `extra.drupal-core-vendor-hardening["drupal/core"] lacks `+strings.Join(missing, ", "))
	}
	return strings.Join(issues, "; ")
}
