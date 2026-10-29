package config

import (
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type ProjectConfig struct {
	GitStartBranch      string   `yaml:"git_start_branch" json:"GitStartBranch"`
	GitNewBranchPattern string   `yaml:"git_new_branch_pattern" json:"GitNewBranchPattern"`
	IgnoredPackages     []string `yaml:"ignored_packages" json:"IgnoredPackages"`
	SmokePaths          []string `yaml:"smoke_paths,omitempty" json:"SmokePaths"`                 // extra paths the agent checks before/after updating; "admin:" prefix = logged in
	ClaudeInstructions  string   `yaml:"claude_instructions,omitempty" json:"ClaudeInstructions"` // extra per-project prompt text
}

type Config struct {
	BaseFolders          []string                 `yaml:"base_folders" json:"BaseFolders"`             // scanned one level deep for projects
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
	Projects             map[string]ProjectConfig `yaml:"projects" json:"Projects"`
}

// DefaultBranchPattern names the branch a successful run is renamed to: {date} = YYYY-MM-DD, * = project slug.
const DefaultBranchPattern = "hotfix/{date}-drupal-updates"

func DefaultProjectConfig() ProjectConfig {
	return ProjectConfig{
		GitStartBranch:      "master",
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
