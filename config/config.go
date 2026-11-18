package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"drupal-updater/logging"

	"gopkg.in/yaml.v3"
)

// ProjectFile is the per-project config committed in the project's repo, shared by the whole team.
// YAML is a superset of JSON, so a JSON body works too.
const ProjectFile = "drupal-updater.yaml"

// DefaultDashboardURL is used when dashboard_url is empty. Nothing is requested from it before the user connects.
const DefaultDashboardURL = "https://applications.dyna-ops.be/api/mcp"

type ProjectConfig struct {
	GitStartBranch      string   `yaml:"git_start_branch,omitempty" json:"GitStartBranch"`
	GitNewBranchPattern string   `yaml:"git_new_branch_pattern,omitempty" json:"GitNewBranchPattern"`
	IgnoredPackages     []string `yaml:"ignored_packages,omitempty" json:"IgnoredPackages"`
	SmokePaths          []string `yaml:"smoke_paths,omitempty" json:"SmokePaths"`                 // extra paths the agent checks before/after updating; "admin:" prefix = logged in
	ClaudeInstructions  string   `yaml:"claude_instructions,omitempty" json:"ClaudeInstructions"` // extra per-project prompt text
	GitMergeTarget      string   `yaml:"git_merge_target,omitempty" json:"GitMergeTarget"`        // merge request target; default: the start branch
	GitCommitMessage    string   `yaml:"git_commit_message,omitempty" json:"GitCommitMessage"`    // commit made before the merge request; {date} = YYYY-MM-DD
	DashboardProject    string   `yaml:"dashboard_project,omitempty" json:"DashboardProject"`     // this project's name (or id) in the applications dashboard, when it differs from the folder name
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
	DashboardURL         string                   `yaml:"dashboard_url,omitempty" json:"DashboardURL"` // applications dashboard (MCP server) to show SLA, customer and pending security modules from; empty = off
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
	pick(&dst.DashboardProject, src.DashboardProject)
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
	return WriteFileAtomic(path, data, 0o644)
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

	return WriteFileAtomic(path, data, 0644)
}

// WriteFileAtomic writes to a temp file next to path and renames it over, so a crash mid-write never
// leaves a truncated file behind.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if fi, err := os.Stat(path); err == nil {
		perm = fi.Mode().Perm() // keep the mode of a file that is already there (a config the user made 0600 stays so)
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// BackupBadConfig moves an unreadable config aside to <path>.bad and returns that path, so that running on
// defaults and saving later cannot destroy the user's file.
func BackupBadConfig(path string) (string, error) {
	if _, err := os.Stat(path); err != nil {
		return "", err
	}
	bad := path + ".bad-" + time.Now().Format("20060102-150405") // never replaces an earlier backup
	return bad, os.Rename(path, bad)
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
	// A custom prompt from before 1.4 describes every step itself, while the app now runs the fixed steps: drop it,
	// so the built-in prompt is used and the next save leaves it out.
	if cfg.ClaudePrompt != "" && !strings.Contains(cfg.ClaudePrompt, "{{update_list}}") {
		backup := path + ".claude_prompt.bak"
		if err := os.WriteFile(backup, []byte(cfg.ClaudePrompt), 0o600); err == nil {
			logging.Info("claude_prompt predates the app running the fixed update steps; using the built-in prompt (the old one is in %s)", backup)
		} else {
			logging.Info("claude_prompt predates the app running the fixed update steps; using the built-in prompt (backup failed: %v)", err)
		}
		cfg.ClaudePrompt = ""
	}

	return &cfg, nil
}
