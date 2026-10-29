package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"drupal-updater/api"
	"drupal-updater/config"
	"drupal-updater/discovery"
	"drupal-updater/executor"
	"drupal-updater/logging"
	"drupal-updater/ui"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	// git/ddev guard behind the shims of a Claude run (see executor/guard.go)
	if len(os.Args) > 1 && os.Args[1] == "guard" {
		os.Exit(executor.Guard(os.Args[2:]))
	}

	logging.Init()
	defer logging.Close()

	logging.Info("drupal-updater starting")

	home, err := os.UserHomeDir()
	if err != nil {
		logging.Error("Error getting home directory: %v", err)
		fmt.Fprintf(os.Stderr, "Error getting home directory: %v\n", err)
		os.Exit(1)
	}

	configPath := filepath.Join(home, ".config", "drupal-updater", "config.yaml")

	// Hidden API subcommand for desktop app
	if len(os.Args) > 1 && os.Args[1] == "api" {
		api.Run(os.Args[2:])
		return
	}

	// Check for --config flag
	if len(os.Args) > 1 && (os.Args[1] == "--config" || os.Args[1] == "-c") {
		cfg, err := config.LoadConfig(configPath)
		if err != nil {
			logging.Error("Error loading config: %v", err)
			fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
			os.Exit(1)
		}

		logging.Info("Launching config UI")
		m := ui.NewConfigModel(cfg, configPath)
		p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
		if _, err := p.Run(); err != nil {
			logging.Error("Config UI error: %v", err)
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		logging.Error("Error loading config: %v", err)
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	executor.Configure(cfg)

	projects, err := discovery.DiscoverProjects(cfg.BaseFolders, cfg.ProjectPaths, cfg.ExcludedProjects)
	if err != nil {
		logging.Error("Error discovering projects: %v", err)
		fmt.Fprintf(os.Stderr, "Error discovering projects: %v\n", err)
		os.Exit(1)
	}

	if len(projects) == 0 {
		logging.Info("No projects found in %v %v", cfg.BaseFolders, cfg.ProjectPaths)
		fmt.Printf("No Drupal/DDEV projects found in %s\n", strings.Join(append(cfg.BaseFolders, cfg.ProjectPaths...), ", "))
		fmt.Println("Run with --config to configure your project folders.")
		os.Exit(0)
	}

	logging.Info("Found %d projects", len(projects))

	m := ui.New(cfg, projects)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		logging.Error("UI error: %v", err)
		fmt.Fprintf(os.Stderr, "Error running program: %v\n", err)
		os.Exit(1)
	}

	logging.Info("drupal-updater exiting normally")
}
