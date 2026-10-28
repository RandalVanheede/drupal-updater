package executor

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// LockDiff summarises the production packages that changed in composer.lock since HEAD as a Markdown
// table, like `composer-lock-diff --no-links --only-prod --md` but without needing PHP on the host.
// Empty when there is no lock file, no change, or HEAD has no lock to compare with.
func LockDiff(projectPath string) string {
	cmd := exec.Command("git", "-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "show", "HEAD:composer.lock")
	cmd.Dir = projectPath
	cmd.Env = EnrichedEnv()
	base, err := cmd.Output() // stdout only: a git warning on stderr must not corrupt the JSON
	if err != nil {
		return ""
	}
	target, err := os.ReadFile(filepath.Join(projectPath, "composer.lock"))
	if err != nil {
		return ""
	}
	return diffLocks(base, target)
}

func diffLocks(base, target []byte) string {
	from, to := lockVersions(base), lockVersions(target)
	if from == nil || to == nil {
		return ""
	}
	var rows [][3]string
	for name, v := range from {
		if w, ok := to[name]; !ok {
			rows = append(rows, [3]string{name, v, "REMOVED"})
		} else if w != v {
			rows = append(rows, [3]string{name, v, w})
		}
	}
	for name, w := range to {
		if _, ok := from[name]; !ok {
			rows = append(rows, [3]string{name, "NEW", w})
		}
	}
	if len(rows) == 0 {
		return ""
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i][0] < rows[j][0] })
	rows = append([][3]string{{"Production Changes", "From", "To"}, {}}, rows...)
	var width [3]int
	for _, r := range rows {
		for i, c := range r {
			width[i] = max(width[i], len(c))
		}
	}
	var b strings.Builder
	b.WriteString("\n\n")
	for n, r := range rows {
		for i, c := range r {
			if n == 1 {
				c = strings.Repeat("-", width[i])
			}
			fmt.Fprintf(&b, "| %-*s ", width[i], c)
		}
		b.WriteString("|\n")
	}
	return b.String()
}

// lockVersions maps each production package in a composer.lock to its version; dev versions
// get the short commit, since their name alone doesn't change. Nil when data isn't a lock file.
func lockVersions(data []byte) map[string]string {
	var lock struct {
		Packages []struct {
			Name    string `json:"name"`
			Version string `json:"version"`
			Source  struct {
				Reference string `json:"reference"`
			} `json:"source"`
		} `json:"packages"`
	}
	if json.Unmarshal(data, &lock) != nil {
		return nil
	}
	m := make(map[string]string, len(lock.Packages))
	for _, p := range lock.Packages {
		v := p.Version
		if ref := p.Source.Reference; strings.HasPrefix(v, "dev-") && len(ref) >= 7 {
			v += " " + ref[:7]
		}
		m[p.Name] = v
	}
	return m
}
