// Package api provides hidden CLI commands for the desktop app to communicate
// with the drupal-updater backend over JSON/stdin/stdout.
//
// Usage: drupal-updater api <command> [flags]
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"

	"drupal-updater/config"
	"drupal-updater/discovery"
	"drupal-updater/executor"
	"drupal-updater/logging"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func configPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "drupal-updater", "config.yaml")
}

func outputJSON(v interface{}) {
	data, _ := json.Marshal(v)
	fmt.Println(string(data))
}

func fatal(msg string) {
	outputJSON(map[string]interface{}{"ok": false, "error": msg})
	os.Exit(1)
}

// loadConfigOrFatal loads config, calling fatal on error.
func loadConfigOrFatal() *config.Config {
	cfg, err := config.LoadConfig(configPath())
	if err != nil {
		fatal(fmt.Sprintf("failed to load config: %v", err))
	}
	executor.Configure(cfg)
	return cfg
}

// discoverOrFatal discovers projects, calling fatal on error.
func discoverOrFatal(cfg *config.Config) []discovery.Project {
	projects, err := discovery.DiscoverProjects(cfg.BaseFolder, cfg.ExcludedProjects)
	if err != nil {
		fatal(fmt.Sprintf("failed to discover projects: %v", err))
	}
	return projects
}

// flagValue parses --flag=value or --flag value from args.
func flagValue(args []string, flag string) string {
	prefix := "--" + flag + "="
	for i, a := range args {
		if strings.HasPrefix(a, prefix) {
			return strings.TrimPrefix(a, prefix)
		}
		if a == "--"+flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// runShell executes cmdStr via a login shell in the given directory and returns
// combined stdout+stderr output along with any error.
func runShell(cmdStr, dir string) (string, error) {
	var buf bytes.Buffer
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	c := exec.Command(shell, "-l", "-c", cmdStr)
	c.Dir = dir
	c.Stdout = &buf
	c.Stderr = &buf
	err := c.Run()
	return buf.String(), err
}

// ---------------------------------------------------------------------------
// Run — entry point
// ---------------------------------------------------------------------------

// Run parses the subcommand from args and dispatches to the appropriate handler.
// args should be the arguments after "api", e.g. ["list-projects"].
func Run(args []string) {
	if len(args) == 0 {
		fatal("no command specified; available: list-projects, get-config, save-config, start-update, rename-branch, cleanup-branch")
	}

	cmd := args[0]
	rest := args[1:]

	switch cmd {
	case "list-projects":
		cmdListProjects(rest)
	case "get-config":
		cmdGetConfig(rest)
	case "save-config":
		cmdSaveConfig(rest)
	case "start-update":
		cmdStartUpdate(rest)
	case "rename-branch":
		cmdRenameBranch(rest)
	case "cleanup-branch":
		cmdCleanupBranch(rest)
	default:
		fatal(fmt.Sprintf("unknown command: %s", cmd))
	}
}

// ---------------------------------------------------------------------------
// list-projects
// ---------------------------------------------------------------------------

func cmdListProjects(_ []string) {
	cfg := loadConfigOrFatal()
	projects := discoverOrFatal(cfg)

	type projectItem struct {
		Name string `json:"name"`
		Path string `json:"path"`
	}

	result := make([]projectItem, 0, len(projects))
	for _, p := range projects {
		result = append(result, projectItem{Name: p.Name, Path: p.Path})
	}
	outputJSON(result)
}

// ---------------------------------------------------------------------------
// get-config
// ---------------------------------------------------------------------------

func cmdGetConfig(_ []string) {
	cfg := loadConfigOrFatal()
	outputJSON(cfg)
}

// ---------------------------------------------------------------------------
// save-config
// ---------------------------------------------------------------------------

func cmdSaveConfig(_ []string) {
	var cfg config.Config
	if err := json.NewDecoder(os.Stdin).Decode(&cfg); err != nil {
		fatal(fmt.Sprintf("failed to decode config from stdin: %v", err))
	}

	if err := config.SaveConfig(configPath(), &cfg); err != nil {
		fatal(fmt.Sprintf("failed to save config: %v", err))
	}

	outputJSON(map[string]interface{}{"ok": true})
}

// ---------------------------------------------------------------------------
// start-update
// ---------------------------------------------------------------------------

// statusLine is the JSON shape streamed to stdout for each executor update.
type statusLine struct {
	Project string `json:"project"`
	Status  string `json:"status"`
	Command string `json:"command"`
	Output  string `json:"output"`
}

func cmdStartUpdate(args []string) {
	projectsFlag := flagValue(args, "projects")

	cfg := loadConfigOrFatal()
	allProjects := discoverOrFatal(cfg)

	// Determine which projects to run.
	var selectedProjects []discovery.Project
	if projectsFlag == "" {
		selectedProjects = allProjects
	} else {
		for _, name := range strings.Split(projectsFlag, ",") {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			p, ok := discovery.Find(allProjects, name)
			if !ok {
				fatal(fmt.Sprintf("project not found: %s", name))
			}
			selectedProjects = append(selectedProjects, p)
		}
	}

	if len(selectedProjects) == 0 {
		enc := json.NewEncoder(os.Stdout)
		enc.Encode(map[string]string{"event": "all-done"}) //nolint:errcheck
		return
	}

	// Respect SIGINT so the desktop app can cancel by killing the subprocess.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	type job struct {
		name        string
		path        string
		startBranch string
		tempBranch  string
		commands    []string
	}

	var jobs []job
	for _, p := range selectedProjects {
		pCfg := cfg.Projects[p.Name]
		pCfg.MergeWithDefaults(cfg)

		tempBranch := executor.TempBranch(p.Name)
		commands := executor.BuildCommands(pCfg.Commands, pCfg.GitStartBranch, tempBranch)

		logging.Info("[api] Queuing project %s, temp branch: %s", p.Name, tempBranch)
		jobs = append(jobs, job{
			name:        p.Name,
			path:        p.Path,
			startBranch: pCfg.GitStartBranch,
			tempBranch:  tempBranch,
			commands:    commands,
		})
	}

	// Shared fan-in channel — all executor goroutines write here.
	outChan := make(chan executor.StatusUpdate, len(jobs)*64)

	maxConcurrent := cfg.MaxConcurrent
	if maxConcurrent <= 0 {
		maxConcurrent = 3
	}
	sem := make(chan struct{}, maxConcurrent)

	var wg sync.WaitGroup
	for _, j := range jobs {
		j := j
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			executor.Run(ctx, j.name, j.path, j.startBranch, j.tempBranch, j.commands, outChan)
			<-sem
		}()
	}

	// Close the channel once all executors finish.
	go func() {
		wg.Wait()
		close(outChan)
	}()

	// Stream updates to stdout as newline-delimited JSON.
	enc := json.NewEncoder(os.Stdout)
	for update := range outChan {
		line := statusLine{
			Project: update.ProjectName,
			Status:  update.Status,
			Command: update.Command,
			Output:  update.Output,
		}
		enc.Encode(line) //nolint:errcheck
	}

	// Final sentinel so the caller knows all work is done.
	enc.Encode(map[string]string{"event": "all-done"}) //nolint:errcheck
}

// ---------------------------------------------------------------------------
// rename-branch
// ---------------------------------------------------------------------------

func cmdRenameBranch(args []string) {
	projectName := flagValue(args, "project")
	finalBranch := flagValue(args, "branch")

	if projectName == "" {
		fatal("--project is required")
	}
	if finalBranch == "" {
		fatal("--branch is required")
	}

	cfg := loadConfigOrFatal()
	projects := discoverOrFatal(cfg)

	p, ok := discovery.Find(projects, projectName)
	if !ok {
		fatal(fmt.Sprintf("project not found: %s", projectName))
	}

	tempBranch := executor.TempBranch(projectName)
	logging.Info("[api] rename-branch: %s -> %s in %s", tempBranch, finalBranch, p.Path)

	if err := executor.RenameBranch(p.Path, tempBranch, finalBranch); err != nil {
		fatal(err.Error())
	}

	outputJSON(map[string]interface{}{"ok": true})
}

// ---------------------------------------------------------------------------
// cleanup-branch
// ---------------------------------------------------------------------------

func cmdCleanupBranch(args []string) {
	projectName := flagValue(args, "project")
	if projectName == "" {
		fatal("--project is required")
	}

	cfg := loadConfigOrFatal()
	projects := discoverOrFatal(cfg)

	p, ok := discovery.Find(projects, projectName)
	if !ok {
		fatal(fmt.Sprintf("project not found: %s", projectName))
	}

	pCfg := cfg.Projects[projectName]
	pCfg.MergeWithDefaults(cfg)

	tempBranch := executor.TempBranch(projectName)

	logging.Info("[api] cleanup-branch: %s -> %s in %s", tempBranch, pCfg.GitStartBranch, p.Path)
	if err := executor.Cleanup(p.Path, pCfg.GitStartBranch, tempBranch); err != nil {
		fatal(err.Error())
	}

	outputJSON(map[string]interface{}{"ok": true})
}
