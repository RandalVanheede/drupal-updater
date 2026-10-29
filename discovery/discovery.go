package discovery

import (
	"os"
	"path/filepath"
	"strings"
)

type Project struct {
	Name string
	Path string
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return filepath.Clean(p)
}

// DiscoverProjects scans each base folder one level deep and adds every explicit project path.
// A project is named after its folder; when two share a name, both become "<parent>/<folder>",
// and if that still clashes, the full path (names key the config, tmux sessions and run dirs).
func DiscoverProjects(baseFolders, projectPaths, excludedProjects []string) ([]Project, error) {
	isExcluded := func(name string) bool {
		for _, pattern := range excludedProjects {
			if matched, _ := filepath.Match(pattern, name); matched {
				return true
			}
		}
		return false
	}
	// A naive check: consider it a project if it has a .ddev folder or composer.json
	isProject := func(dir string) bool {
		for _, marker := range []string{".ddev", "composer.json"} {
			if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
				return true
			}
		}
		return false
	}

	var paths []string
	seen := map[string]bool{}
	add := func(p string) {
		if !seen[p] {
			seen[p] = true
			paths = append(paths, p)
		}
	}

	for _, base := range baseFolders {
		base = expandHome(base)
		entries, err := os.ReadDir(base)
		if err != nil {
			if os.IsNotExist(err) {
				continue // base folder doesn't exist (yet)
			}
			return nil, err
		}
		for _, entry := range entries {
			p := filepath.Join(base, entry.Name())
			if entry.IsDir() && isProject(p) && !isExcluded(entry.Name()) {
				add(p)
			}
		}
	}
	// Explicit paths are added on purpose, so no marker or exclusion check; missing ones are skipped.
	for _, p := range projectPaths {
		p = expandHome(p)
		if info, err := os.Stat(p); err == nil && info.IsDir() {
			add(p)
		}
	}

	// Short names have no "/" and full paths start with one, so each step can only remove clashes.
	names := make([]string, len(paths))
	for i, p := range paths {
		names[i] = filepath.Base(p)
	}
	parent := func(p string) string { return filepath.Base(filepath.Dir(p)) + "/" + filepath.Base(p) }
	full := func(p string) string { return p }
	for _, rename := range []func(string) string{parent, full} {
		count := map[string]int{}
		for _, n := range names {
			count[n]++
		}
		for i, p := range paths {
			if count[names[i]] > 1 {
				names[i] = rename(p)
			}
		}
	}
	projects := make([]Project, len(paths))
	for i, p := range paths {
		projects[i] = Project{Name: names[i], Path: p}
	}
	return projects, nil
}

// Find returns the project with the given name.
func Find(projects []Project, name string) (Project, bool) {
	for _, p := range projects {
		if p.Name == name {
			return p, true
		}
	}
	return Project{}, false
}
