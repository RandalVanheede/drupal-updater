package dashboard

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// Hint is a name found inside a local project that may also be the dashboard's name for it.
type Hint struct {
	Kind  string // what it was read from: "Drupal site name", "repository name", "DDEV project"
	Value string
}

// Hints are what a local project reveals about itself besides its folder name.
type Hints struct {
	Names  []Hint
	Groups []string // the git remote's namespace segments ("agora" in gitlab.example/agora/agora-website): often the customer
}

// ReadHints reads the hints from files in the project folder, without running anything: the DDEV project name, the
// origin remote in .git/config, and the Drupal site name in the exported configuration.
func ReadHints(dir string) Hints {
	var h Hints
	if v := yamlName(filepath.Join(dir, ".ddev", "config.yaml")); v != "" {
		h.Names = append(h.Names, Hint{"DDEV project", v})
	}
	if repo, groups := originRepo(dir); repo != "" {
		h.Names = append(h.Names, Hint{"repository name", repo})
		h.Groups = groups
	}
	site := []string{filepath.Join(dir, "config", "sync", "system.site.yml"), filepath.Join(dir, "config", "default", "system.site.yml")}
	if more, _ := filepath.Glob(filepath.Join(dir, "config", "*", "system.site.yml")); more != nil {
		site = append(site, more...)
	}
	for _, f := range site {
		if v := yamlName(f); v != "" {
			h.Names = append(h.Names, Hint{"Drupal site name", v})
			break
		}
	}
	return h
}

// yamlName returns the top-level "name:" value of a small YAML file, or "".
func yamlName(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for n := 0; sc.Scan() && n < 400; n++ {
		line := sc.Text()
		if strings.HasPrefix(line, "name:") {
			return strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "name:")), `'"`)
		}
	}
	return ""
}

// originRepo reads the origin remote from .git/config and splits its path into the repository name and the
// namespace segments before it.
func originRepo(dir string) (repo string, groups []string) {
	f, err := os.Open(filepath.Join(dir, ".git", "config"))
	if err != nil {
		return "", nil
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	inOrigin := false
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(line, "["):
			inOrigin = line == `[remote "origin"]`
		case inOrigin && strings.HasPrefix(line, "url"):
			if _, v, ok := strings.Cut(line, "="); ok {
				return splitRemote(strings.TrimSpace(v))
			}
		}
	}
	return "", nil
}

// splitRemote handles git@host:group/sub/repo.git, ssh://git@host/group/repo.git and https://host/group/repo.git.
func splitRemote(url string) (repo string, groups []string) {
	path := url
	if i := strings.Index(url, "://"); i >= 0 {
		path = url[i+3:]
		if j := strings.Index(path, "/"); j >= 0 {
			path = path[j+1:]
		}
	} else if i := strings.Index(url, ":"); i >= 0 {
		path = url[i+1:]
	}
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	segs := strings.Split(path, "/")
	if len(segs) == 0 || segs[len(segs)-1] == "" {
		return "", nil
	}
	return segs[len(segs)-1], segs[:len(segs)-1]
}
