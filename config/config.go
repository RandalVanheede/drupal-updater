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
	Commands            []string `yaml:"commands" json:"Commands"`                                // classic mode only
	ClaudeInstructions  string   `yaml:"claude_instructions,omitempty" json:"ClaudeInstructions"` // extra per-project prompt text, claude mode
}

type Config struct {
	BaseFolder            string                   `yaml:"base_folder" json:"BaseFolder"`
	MaxConcurrent         int                      `yaml:"max_concurrent" json:"MaxConcurrent"`
	Mode                  string                   `yaml:"mode,omitempty" json:"Mode"`                  // "claude" (default) or "classic"
	UpdateMode            string                   `yaml:"update_mode,omitempty" json:"UpdateMode"`     // "standard" (default, all minor/patch updates) or "disaster" (security fixes only), claude mode
	ClaudeModel           string                   `yaml:"claude_model,omitempty" json:"ClaudeModel"`   // default sonnet
	ClaudePrompt          string                   `yaml:"claude_prompt,omitempty" json:"ClaudePrompt"` // default: executor.DefaultClaudePrompt
	ClaudeAllowedTools    []string                 `yaml:"claude_allowed_tools,omitempty" json:"ClaudeAllowedTools"`
	ClaudePermissionMode  string                   `yaml:"claude_permission_mode,omitempty" json:"ClaudePermissionMode"`   // default dontAsk
	ClaudeTimeoutMinutes  int                      `yaml:"claude_timeout_minutes,omitempty" json:"ClaudeTimeoutMinutes"`   // default 60
	MaxAttempts           int                      `yaml:"max_attempts,omitempty" json:"MaxAttempts"`                      // per command, default 3
	CommandTimeoutMinutes int                      `yaml:"command_timeout_minutes,omitempty" json:"CommandTimeoutMinutes"` // default 30
	DefaultCommands       []string                 `yaml:"default_commands" json:"DefaultCommands"`
	ExcludedProjects      []string                 `yaml:"excluded_projects" json:"ExcludedProjects"`
	Projects              map[string]ProjectConfig `yaml:"projects" json:"Projects"`
}

func DefaultProjectConfig() ProjectConfig {
	return ProjectConfig{
		GitStartBranch:      "master",
		GitNewBranchPattern: "hotfix/*",
		Commands: []string{
			"git pull --rebase --autostash",
			"ddev describe >/dev/null 2>&1 || ddev start",
			"ddev composer install",
			"ddev drush deploy -y",
			"ddev composer update 'drupal/core-*' 'symfony/*' 'twig/*' --with-all-dependencies",
			"ddev drush updb -y",
			"ddev drush cex -y",
			"ddev stop",
		},
	}
}

// MergeWithDefaults fills in missing fields with default values
func (pc *ProjectConfig) MergeWithDefaults(globalDefaults *Config) {
	defaults := DefaultProjectConfig()
	if pc.GitStartBranch == "" {
		pc.GitStartBranch = defaults.GitStartBranch
	}
	if pc.GitNewBranchPattern == "" {
		pc.GitNewBranchPattern = defaults.GitNewBranchPattern
	}
	if len(pc.Commands) == 0 {
		if len(globalDefaults.DefaultCommands) > 0 {
			pc.Commands = globalDefaults.DefaultCommands
		} else {
			pc.Commands = defaults.Commands
		}
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
				BaseFolder:    "~/projects",
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

	if cfg.BaseFolder == "" {
		cfg.BaseFolder = "~/projects"
	}

	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = 3
	}

	if len(cfg.DefaultCommands) == 0 {
		cfg.DefaultCommands = DefaultProjectConfig().Commands
	}

	return &cfg, nil
}
