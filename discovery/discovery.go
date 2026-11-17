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

// DiscoverProjects scans each base folder up to depth levels deep (min 1; a project's own subfolders are
// not searched) and adds every explicit project path.
// A project is named after its folder; when two share a name, both become "<parent>/<folder>",
// and if that still clashes, the full path (names key the config, tmux sessions and run dirs).
func DiscoverProjects(baseFolders, projectPaths, excludedProjects []string, depth int) ([]Project, error) {
	isExcluded := func(name string) bool {
		for _, pattern := range excludedProjects {
			if matched, _ := filepath.Match(pattern, name); matched {
				return true
			}
		}
		return false
	}
	// A Drupal project requires drupal/core (or a distribution/CMS that pulls it in) in its composer.json.
	isProject := func(dir string) bool {
		data, err := os.ReadFile(filepath.Join(dir, "composer.json"))
		return err == nil && (strings.Contains(string(data), `"drupal/core`) || strings.Contains(string(data), `"drupal/cms"`))
	}

	var paths []string
	seen := map[string]bool{}
	add := func(p string) {
		if !seen[p] {
			seen[p] = true
			paths = append(paths, p)
		}
	}

	var scan func(dir string, left int) error
	scan = func(dir string, left int) error {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			p := filepath.Join(dir, entry.Name())
			if !entry.IsDir() || isExcluded(entry.Name()) {
				continue
			}
			if isProject(p) {
				add(p)
			} else if left > 1 {
				if err := scan(p, left-1); err != nil && !os.IsPermission(err) {
					return err
				}
			}
		}
		return nil
	}

	for _, base := range baseFolders {
		base = expandHome(base)
		if err := scan(base, max(depth, 1)); err != nil {
			if os.IsNotExist(err) {
				continue // base folder doesn't exist (yet)
			}
			return nil, err
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
