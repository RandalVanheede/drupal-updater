package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"drupal-updater/logging"

	"gopkg.in/yaml.v3"
)

// ProjectFile is the per-project config committed in the project's repo, shared by the whole team.
// YAML is a superset of JSON, so a JSON body works too.
const ProjectFile = "drupal-updater.yaml"

type ProjectConfig struct {
	GitStartBranch      string   `yaml:"git_start_branch,omitempty" json:"GitStartBranch"`
	GitNewBranchPattern string   `yaml:"git_new_branch_pattern,omitempty" json:"GitNewBranchPattern"`
	IgnoredPackages     []string `yaml:"ignored_packages,omitempty" json:"IgnoredPackages"`
	SmokePaths          []string `yaml:"smoke_paths,omitempty" json:"SmokePaths"`                 // extra paths the agent checks before/after updating; "admin:" prefix = logged in
	ClaudeInstructions  string   `yaml:"claude_instructions,omitempty" json:"ClaudeInstructions"` // extra per-project prompt text
	GitMergeTarget      string   `yaml:"git_merge_target,omitempty" json:"GitMergeTarget"`        // merge request target; default: the start branch
	GitCommitMessage    string   `yaml:"git_commit_message,omitempty" json:"GitCommitMessage"`    // commit made before the merge request; {date} = YYYY-MM-DD
}

type Config struct {
	BaseFolders          []string                 `yaml:"base_folders" json:"BaseFolders"`             // scanned ScanDepth levels deep for projects
	ScanDepth            int                      `yaml:"scan_depth,omitempty" json:"ScanDepth"`       // folder levels below a base folder to search; default 1
	ProjectPaths         []string                 `yaml:"project_paths,omitempty" json:"ProjectPaths"` // single projects outside the base folders
	LegacyBaseFolder     string                   `yaml:"base_folder,omitempty" json:"-"`              // old single-folder key, moved into BaseFolders on load
	MaxConcurrent        int                      `yaml:"max_concurrent" json:"MaxConcurrent"`
	UpdateMode           string                   `yaml:"update_mode,omitempty" json:"UpdateMode"`     // "standard" (default, all minor/patch updates) or "disaster" (security fixes only)
	ClaudeModel          string                   `yaml:"claude_model,omitempty" json:"ClaudeModel"`   // default sonnet
	ClaudePrompt         string                   `yaml:"claude_prompt,omitempty" json:"ClaudePrompt"` // default: executor.DefaultClaudePrompt
	ClaudeAllowedTools   []string                 `yaml:"claude_allowed_tools,omitempty" json:"ClaudeAllowedTools"`
	ClaudePermissionMode string                   `yaml:"claude_permission_mode,omitempty" json:"ClaudePermissionMode"` // default dontAsk
	ClaudeTimeoutMinutes int                      `yaml:"claude_timeout_minutes,omitempty" json:"ClaudeTimeoutMinutes"` // default 60
	ExcludedProjects     []string                 `yaml:"excluded_projects" json:"ExcludedProjects"`
	Defaults             ProjectConfig            `yaml:"defaults,omitempty" json:"Defaults"` // for every project, under its own projects: entry
	Projects             map[string]ProjectConfig `yaml:"projects" json:"Projects"`
}

// Project is a project's local settings: its projects: entry over the global defaults.
func (c *Config) Project(name string) ProjectConfig {
	pc := c.Defaults
	overlay(&pc, c.Projects[name])
	return pc
}

// DefaultCommitMessage is the commit made from a run's changes before its merge request.
const DefaultCommitMessage = "Drupal updates {date}"

// DefaultBranchPattern names the branch a successful run is renamed to: {date} = YYYY-MM-DD, * = project slug.
const DefaultBranchPattern = "hotfix/{date}-drupal-updates"

func DefaultProjectConfig() ProjectConfig {
	return ProjectConfig{
		GitStartBranch:      "master|main",
		GitNewBranchPattern: DefaultBranchPattern,
	}
}

// MergeWithDefaults fills in missing fields with default values
func (pc *ProjectConfig) MergeWithDefaults() {
	defaults := DefaultProjectConfig()
	if pc.GitStartBranch == "" {
		pc.GitStartBranch = defaults.GitStartBranch
	}
	if pc.GitNewBranchPattern == "" || pc.GitNewBranchPattern == "hotfix/*" { // hotfix/* = old default, saved into every project
		pc.GitNewBranchPattern = defaults.GitNewBranchPattern
	}
	if pc.GitCommitMessage == "" {
		pc.GitCommitMessage = DefaultCommitMessage
	}
}

// ForProject returns a project's effective settings: the repo's drupal-updater.yaml wins per field,
// the local settings (Config.Project) fill the gaps, then built-in defaults. A broken repo file is logged and skipped.
func ForProject(local ProjectConfig, projectPath string) ProjectConfig {
	pc := local
	if data, err := os.ReadFile(filepath.Join(projectPath, ProjectFile)); err == nil {
		var repo ProjectConfig
		if err := yaml.Unmarshal(data, &repo); err != nil {
			logging.Error("ignoring %s in %s: %v", ProjectFile, projectPath, err)
		} else {
			overlay(&pc, repo)
		}
	}
	pc.MergeWithDefaults()
	return pc
}

// overlay copies every field src sets onto dst.
func overlay(dst *ProjectConfig, src ProjectConfig) {
	pick := func(d *string, v string) {
		if v != "" {
			*d = v
		}
	}
	pick(&dst.GitStartBranch, src.GitStartBranch)
	pick(&dst.GitNewBranchPattern, src.GitNewBranchPattern)
	pick(&dst.ClaudeInstructions, src.ClaudeInstructions)
	pick(&dst.GitMergeTarget, src.GitMergeTarget)
	pick(&dst.GitCommitMessage, src.GitCommitMessage)
	if src.IgnoredPackages != nil {
		dst.IgnoredPackages = src.IgnoredPackages
	}
	if src.SmokePaths != nil {
		dst.SmokePaths = src.SmokePaths
	}
}

// SaveProjectFile writes pc's settings into the project's drupal-updater.yaml, for the team to commit.
// Settings already in the file that pc leaves empty are kept.
func SaveProjectFile(projectPath string, pc ProjectConfig) error {
	path := filepath.Join(projectPath, ProjectFile)
	var merged ProjectConfig
	if data, err := os.ReadFile(path); err == nil {
		if err := yaml.Unmarshal(data, &merged); err != nil {
			return fmt.Errorf("%s does not parse, fix it first: %v", ProjectFile, err)
		}
	}
	overlay(&merged, pc)
	data, err := yaml.Marshal(merged)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(projectPath, ProjectFile), data, 0o644)
}

func SaveConfig(path string, cfg *Config) error {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			path = home + path[1:]
		}
	}

	// Ensure directory exists
	dir := path[:strings.LastIndex(path, "/")]
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}

func LoadConfig(path string) (*Config, error) {
	// Expand ~ to user home dir if needed
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			path = home + path[1:]
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Return default config if file doesn't exist
			return &Config{
				BaseFolders:   []string{"~/projects"},
				MaxConcurrent: 3,
				Projects:      make(map[string]ProjectConfig),
			}, nil
		}
		return nil, err
	}

	var cfg Config
	err = yaml.Unmarshal(data, &cfg)
	if err != nil {
		return nil, err
	}

	if cfg.LegacyBaseFolder != "" {
		cfg.BaseFolders = append([]string{cfg.LegacyBaseFolder}, cfg.BaseFolders...)
		cfg.LegacyBaseFolder = ""
	}
	if len(cfg.BaseFolders) == 0 && len(cfg.ProjectPaths) == 0 {
		cfg.BaseFolders = []string{"~/projects"}
	}

	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = 3
	}

	return &cfg, nil
}
