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

func DiscoverProjects(baseFolder string, excludedProjects []string) ([]Project, error) {
	isExcluded := func(name string) bool {
		for _, pattern := range excludedProjects {
			if matched, _ := filepath.Match(pattern, name); matched {
				return true
			}
		}
		return false
	}
	// Expand ~ to user home dir
	if strings.HasPrefix(baseFolder, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			baseFolder = filepath.Join(home, baseFolder[2:])
		}
	}

	var projects []Project

	entries, err := os.ReadDir(baseFolder)
	if err != nil {
		if os.IsNotExist(err) {
			// Base folder doesn't exist yet, return empty list
			return projects, nil
		}
		return nil, err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		projectPath := filepath.Join(baseFolder, entry.Name())

		// A naive check: consider it a project if it has a .ddev folder or composer.json
		hasDdev := false
		if _, err := os.Stat(filepath.Join(projectPath, ".ddev")); err == nil {
			hasDdev = true
		}

		hasComposer := false
		if _, err := os.Stat(filepath.Join(projectPath, "composer.json")); err == nil {
			hasComposer = true
		}

		if hasDdev || hasComposer {
			if isExcluded(entry.Name()) {
				continue
			}
			projects = append(projects, Project{
				Name: entry.Name(),
				Path: projectPath,
			})
		}
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
