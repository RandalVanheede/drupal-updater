package main

import (
	"context"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"drupal-updater/api"
	"drupal-updater/config"
	"drupal-updater/discovery"
	"drupal-updater/executor"
	"drupal-updater/logging"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed all:frontend/dist
var assets embed.FS

type App struct {
	ctx        context.Context
	cfg        *config.Config
	configPath string
	mu         sync.Mutex
	cancelAll  context.CancelFunc
	parentCtx  context.Context
	cancels    map[string]context.CancelFunc // running projects
	pending    int                           // projects started and not yet finished
	sem        chan struct{}
}

func NewApp() *App {
	home, _ := os.UserHomeDir()
	configPath := filepath.Join(home, ".config", "drupal-updater", "config.yaml")
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		cfg = &config.Config{
			BaseFolder:    "~/projects",
			MaxConcurrent: 3,
			Projects:      make(map[string]config.ProjectConfig),
		}
	}
	executor.Configure(cfg)
	return &App{cfg: cfg, configPath: configPath}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

// --- Bound methods ---

type ProjectInfo struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

func (a *App) DiscoverProjects() ([]ProjectInfo, error) {
	projects, err := discovery.DiscoverProjects(a.cfg.BaseFolder, a.cfg.ExcludedProjects)
	if err != nil {
		return nil, err
	}
	result := make([]ProjectInfo, len(projects))
	for i, p := range projects {
		result[i] = ProjectInfo{Name: p.Name, Path: p.Path}
	}
	return result, nil
}

func (a *App) GetConfig() *config.Config {
	return a.cfg
}

// GetClaudeDefaults returns the built-in Claude prompt and allowed tools for the settings screen.
func (a *App) GetClaudeDefaults() map[string]any {
	prompt, tools := executor.ClaudeDefaults()
	return map[string]any{"prompt": prompt, "allowedTools": tools}
}

func (a *App) SaveConfig(cfg *config.Config) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cfg = cfg
	if a.pending == 0 {
		a.sem = nil // rebuilt with the new max_concurrent on next start
	}
	executor.Configure(cfg) // apply Claude/retry settings without a restart
	return config.SaveConfig(a.configPath, cfg)
}

type UpdateRequest struct {
	Projects []string `json:"projects"`
}

func sanitize(name string) string {
	s := strings.ToLower(name)
	s = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		return '-'
	}, s)
	return strings.Trim(s, "-")
}

func (a *App) StartUpdate(req UpdateRequest) {
	a.mu.Lock()
	if a.sem == nil {
		n := a.cfg.MaxConcurrent
		if n <= 0 {
			n = 3
		}
		a.sem = make(chan struct{}, n) // shared by every batch so max_concurrent holds across overlapping starts
	}
	sem := a.sem
	if a.cancels == nil {
		a.cancels = make(map[string]context.CancelFunc)
	}
	if a.parentCtx == nil || a.pending == 0 {
		a.parentCtx, a.cancelAll = context.WithCancel(context.Background())
	}
	parentCtx := a.parentCtx
	a.mu.Unlock()

	type job struct {
		name        string
		path        string
		startBranch string
		tempBranch  string
		commands    []string
		ctx         context.Context
	}

	projects, _ := discovery.DiscoverProjects(a.cfg.BaseFolder, a.cfg.ExcludedProjects)
	var jobs []job
	for _, projectName := range req.Projects {
		a.mu.Lock()
		_, busy := a.cancels[projectName]
		a.mu.Unlock()
		if busy {
			continue // already running: never start a second session for the same project
		}
		var projectPath string
		for _, p := range projects {
			if p.Name == projectName {
				projectPath = p.Path
				break
			}
		}
		if projectPath == "" {
			continue
		}

		pCfg := a.cfg.Projects[projectName]
		pCfg.MergeWithDefaults(a.cfg)

		tempBranch := "drupal-updater/temp-" + sanitize(projectName)
		startBranch := pCfg.GitStartBranch

		commands := executor.BuildCommands(pCfg.Commands, startBranch, tempBranch)

		ctx, cancel := context.WithCancel(parentCtx)
		a.mu.Lock()
		a.cancels[projectName] = cancel
		a.mu.Unlock()
		jobs = append(jobs, job{name: projectName, path: projectPath, startBranch: startBranch, tempBranch: tempBranch, commands: commands, ctx: ctx})
	}
	if len(jobs) == 0 {
		return
	}

	a.mu.Lock()
	a.pending += len(jobs)
	a.mu.Unlock()

	statusChan := make(chan executor.StatusUpdate, 100)

	// Emit updates to frontend
	go func() {
		for update := range statusChan {
			runtime.EventsEmit(a.ctx, "status-update", map[string]string{
				"project": update.ProjectName,
				"status":  update.Status,
				"command": update.Command,
				"output":  update.Output,
			})
		}
	}()

	go func() {
		var wg sync.WaitGroup
		for _, j := range jobs {
			j := j
			wg.Add(1)
			go func() {
				defer wg.Done()
				sem <- struct{}{}
				executor.Run(j.ctx, j.name, j.path, j.startBranch, j.tempBranch, j.commands, statusChan)
				<-sem
				a.mu.Lock()
				delete(a.cancels, j.name)
				a.pending--
				a.mu.Unlock()
			}()
		}
		wg.Wait()
		close(statusChan)
		a.mu.Lock()
		last := a.pending == 0
		a.mu.Unlock()
		if last { // only when no other batch is still running
			runtime.EventsEmit(a.ctx, "all-done", nil)
		}
	}()
}

func (a *App) CancelAll() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cancelAll != nil {
		a.cancelAll()
	}
}

func (a *App) CancelProject(name string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if cancel, ok := a.cancels[name]; ok {
		cancel()
	}
}

func (a *App) RenameBranch(projectName, finalBranch string) error {
	projects, _ := discovery.DiscoverProjects(a.cfg.BaseFolder, a.cfg.ExcludedProjects)
	var projectPath string
	for _, p := range projects {
		if p.Name == projectName {
			projectPath = p.Path
			break
		}
	}
	if projectPath == "" {
		return fmt.Errorf("project %s not found", projectName)
	}

	tempBranch := "drupal-updater/temp-" + sanitize(projectName)
	if err := executor.RenameBranch(projectPath, tempBranch, finalBranch); err != nil {
		return err
	}
	logging.Info("[%s] Renamed branch %s -> %s", projectName, tempBranch, finalBranch)
	return nil
}

func (a *App) RetryProjects(projectNames []string) {
	a.StartUpdate(UpdateRequest{Projects: projectNames})
}

func (a *App) CleanupBranch(projectName string) error {
	projects, _ := discovery.DiscoverProjects(a.cfg.BaseFolder, a.cfg.ExcludedProjects)
	var projectPath string
	for _, p := range projects {
		if p.Name == projectName {
			projectPath = p.Path
			break
		}
	}
	if projectPath == "" {
		return fmt.Errorf("project %s not found", projectName)
	}

	pCfg := a.cfg.Projects[projectName]
	pCfg.MergeWithDefaults(a.cfg)

	tempBranch := "drupal-updater/temp-" + sanitize(projectName)
	if err := executor.Cleanup(projectPath, pCfg.GitStartBranch, tempBranch); err != nil {
		return err
	}

	logging.Info("[%s] Cleaned up temp branch %s", projectName, tempBranch)
	return nil
}

func main() {
	// If invoked with "api" subcommand, handle it (for CLI compatibility)
	if len(os.Args) > 1 && os.Args[1] == "api" {
		api.Run(os.Args[2:])
		return
	}

	logging.Init()
	defer logging.Close()

	app := NewApp()

	err := wails.Run(&options.App{
		Title:  "Drupal Updater",
		Width:  900,
		Height: 600,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		OnStartup: app.startup,
		Bind: []interface{}{
			app,
		},
	})

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
