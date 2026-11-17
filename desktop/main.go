package main

import (
	"context"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"drupal-updater/api"
	"drupal-updater/config"
	"drupal-updater/coreupdates"
	"drupal-updater/discovery"
	"drupal-updater/executor"
	"drupal-updater/logging"
	"drupal-updater/updater"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed all:frontend/dist
var assets embed.FS

var version = "dev" // set by the build scripts: -ldflags "-X main.version=v1.2.3"

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
	summaries  map[string]string     // last successful run's summary per project, used as merge request description
	update     *updater.Release      // newer release found by the daily check
	releases   []coreupdates.Release // Drupal core release history, cached
	relFetched time.Time
}

func NewApp() *App {
	home, _ := os.UserHomeDir()
	configPath := filepath.Join(home, ".config", "drupal-updater", "config.yaml")
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		cfg = &config.Config{
			BaseFolders:   []string{"~/projects"},
			MaxConcurrent: 3,
			Projects:      make(map[string]config.ProjectConfig),
		}
	}
	executor.Configure(cfg)
	return &App{cfg: cfg, configPath: configPath}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	go func() { // daily update check; the frontend shows a banner on "update-available"
		for {
			if r, err := updater.Check(version, false); err != nil {
				logging.Error("update check: %v", err)
			} else if r != nil {
				a.mu.Lock()
				a.update = r
				a.mu.Unlock()
				runtime.EventsEmit(ctx, "update-available", r)
			}
			time.Sleep(24 * time.Hour)
		}
	}()
}

func (a *App) GetVersion() string { return version }

// GetUpdate returns the newer release found so far, or nil.
func (a *App) GetUpdate() *updater.Release {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.update
}

// InstallUpdate replaces the app with the release found by the update check and restarts it.
// Refused while a run or merge request is busy, since quitting would kill its Claude session's supervisor.
func (a *App) InstallUpdate() error {
	a.mu.Lock()
	r, busy := a.update, len(a.cancels) > 0 || a.pending > 0
	a.mu.Unlock()
	if busy {
		return fmt.Errorf("wait until all runs and merge requests are finished")
	}
	cmd, err := updater.Install(r)
	if err != nil {
		logging.Error("update install: %v", err)
		return err
	}
	logging.Info("updated to %s, restarting", r.Version)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("installed %s, but restart failed (start it yourself): %v", r.Version, err)
	}
	runtime.Quit(a.ctx)
	return nil
}

// --- Bound methods ---

type ProjectInfo struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	RepoFile bool   `json:"repoFile"` // has a committed drupal-updater.yaml
	Core     string `json:"core"`     // installed Drupal core version from composer.lock, "" if unknown
}

func (a *App) DiscoverProjects() ([]ProjectInfo, error) {
	projects, err := discovery.DiscoverProjects(a.cfg.BaseFolders, a.cfg.ProjectPaths, a.cfg.ExcludedProjects, a.cfg.ScanDepth)
	if err != nil {
		return nil, err
	}
	result := make([]ProjectInfo, len(projects))
	for i, p := range projects {
		_, err := os.Stat(filepath.Join(p.Path, config.ProjectFile))
		result[i] = ProjectInfo{Name: p.Name, Path: p.Path, RepoFile: err == nil, Core: discovery.CoreVersion(p.Path)}
	}
	return result, nil
}

// CoreStatuses compares installed core versions with Drupal's published releases. The release history
// is cached for an hour; when it cannot be fetched (offline) the map is empty and the UI shows no dots.
func (a *App) CoreStatuses(versions []string) map[string]coreupdates.Result {
	a.mu.Lock()
	stale := a.releases == nil || time.Since(a.relFetched) > time.Hour
	a.mu.Unlock()
	if stale {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		rels, err := coreupdates.Fetch(ctx)
		if err != nil {
			logging.Info("core release history: %v", err)
		} else {
			a.mu.Lock()
			a.releases, a.relFetched = rels, time.Now()
			a.mu.Unlock()
		}
	}
	a.mu.Lock()
	rels := a.releases
	a.mu.Unlock()
	out := map[string]coreupdates.Result{}
	for _, v := range versions {
		if r, ok := coreupdates.Check(v, rels); ok {
			out[v] = r
		}
	}
	return out
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
	executor.Configure(cfg) // apply Claude settings without a restart
	return config.SaveConfig(a.configPath, cfg)
}

// SetUpdateMode switches between "disaster" (security fixes only) and "standard" (all minor/patch updates)
// and saves it; runs that already started keep their mode.
func (a *App) SetUpdateMode(mode string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cfg.UpdateMode = mode
	executor.Configure(a.cfg)
	return config.SaveConfig(a.configPath, a.cfg)
}

type UpdateRequest struct {
	Projects []string `json:"projects"`
}

// projectPath finds a discovered project's folder by name.
func (a *App) projectPath(name string) (string, error) {
	projects, err := discovery.DiscoverProjects(a.cfg.BaseFolders, a.cfg.ProjectPaths, a.cfg.ExcludedProjects, a.cfg.ScanDepth)
	if err != nil {
		return "", err
	}
	p, ok := discovery.Find(projects, name)
	if !ok {
		return "", fmt.Errorf("project %s not found", name)
	}
	return p.Path, nil
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
		ctx         context.Context
	}

	projects, err := discovery.DiscoverProjects(a.cfg.BaseFolders, a.cfg.ProjectPaths, a.cfg.ExcludedProjects, a.cfg.ScanDepth)
	var jobs []job
	for _, projectName := range req.Projects {
		a.mu.Lock()
		_, busy := a.cancels[projectName]
		if !busy {
			a.cancels[projectName] = func() {} // claim it now so a merge request can't start in between
			delete(a.summaries, projectName)   // a new run replaces the old merge request description
		}
		a.mu.Unlock()
		if busy {
			continue // already running: never start a second session for the same project
		}
		p, ok := discovery.Find(projects, projectName)
		if !ok {
			msg := fmt.Sprintf("project %s not found in the project folders", projectName)
			if err != nil {
				msg = err.Error()
			}
			runtime.EventsEmit(a.ctx, "status-update", map[string]string{"project": projectName, "status": "failed", "command": "discover", "output": msg})
			a.mu.Lock()
			delete(a.cancels, projectName)
			a.mu.Unlock()
			continue
		}
		projectPath := p.Path

		pCfg := config.ForProject(a.cfg.Project(projectName), projectPath)

		tempBranch := executor.TempBranch(projectName)
		startBranch := pCfg.GitStartBranch

		ctx, cancel := context.WithCancel(parentCtx)
		a.mu.Lock()
		a.cancels[projectName] = cancel
		a.mu.Unlock()
		jobs = append(jobs, job{name: projectName, path: projectPath, startBranch: startBranch, tempBranch: tempBranch, ctx: ctx})
	}
	if len(jobs) == 0 {
		a.mu.Lock()
		idle := a.pending == 0
		a.mu.Unlock()
		if idle { // nothing could start (e.g. discovery failed): still let the UI leave the running view
			runtime.EventsEmit(a.ctx, "all-done", nil)
		}
		return
	}

	a.mu.Lock()
	a.pending += len(jobs)
	a.mu.Unlock()

	statusChan := make(chan executor.StatusUpdate, 100)

	// Emit updates to frontend
	go func() {
		for update := range statusChan {
			if update.Status == "success" {
				a.mu.Lock()
				if a.summaries == nil {
					a.summaries = make(map[string]string)
				}
				a.summaries[update.ProjectName] = update.Output
				a.mu.Unlock()
			}
			runtime.EventsEmit(a.ctx, "status-update", map[string]string{
				"project": update.ProjectName,
				"status":  update.Status,
				"command": update.Command,
				"output":  update.Output,
				"screen":  update.Screen,
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
				executor.Run(j.ctx, j.name, j.path, j.startBranch, j.tempBranch, statusChan)
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

// FinalBranch is the suggested name for a project's successful temp branch, from its branch pattern.
func (a *App) FinalBranch(projectName string) (string, error) {
	projectPath, err := a.projectPath(projectName)
	if err != nil {
		return "", err
	}
	pCfg := config.ForProject(a.cfg.Project(projectName), projectPath)
	return executor.FinalBranch(projectPath, pCfg.GitNewBranchPattern, projectName), nil
}

func (a *App) RenameBranch(projectName, finalBranch string) error {
	projectPath, err := a.projectPath(projectName)
	if err != nil {
		return err
	}

	tempBranch := executor.TempBranch(projectName)
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
	projectPath, err := a.projectPath(projectName)
	if err != nil {
		return err
	}

	pCfg := config.ForProject(a.cfg.Project(projectName), projectPath)

	tempBranch := executor.TempBranch(projectName)
	if err := executor.Cleanup(projectPath, pCfg.GitStartBranch, tempBranch); err != nil {
		return err
	}

	logging.Info("[%s] Cleaned up temp branch %s", projectName, tempBranch)
	return nil
}

// SaveProjectFile writes a project's settings to drupal-updater.yaml in its repo, for the team to commit.
func (a *App) SaveProjectFile(name string, pc config.ProjectConfig) (string, error) {
	path, err := a.projectPath(name)
	if err != nil {
		return "", err
	}
	return filepath.Join(path, config.ProjectFile), config.SaveProjectFile(path, pc)
}

// MergeRequest commits, pushes and opens a GitLab merge request for a project's final branch in the
// background. Progress arrives as "merge-update" events {project, state: working|done|failed, message, url};
// a conflict-resolving Claude session streams its screen as "status-update" like a run. Cancel with CancelProject.
func (a *App) MergeRequest(name, branch string) {
	emit := func(state, msg, url string) {
		runtime.EventsEmit(a.ctx, "merge-update", map[string]string{"project": name, "state": state, "message": msg, "url": url})
	}
	path, err := a.projectPath(name)
	if err != nil {
		emit("failed", err.Error(), "")
		return
	}
	a.mu.Lock()
	if a.cancels == nil {
		a.cancels = make(map[string]context.CancelFunc)
	}
	if _, busy := a.cancels[name]; busy {
		a.mu.Unlock()
		emit("failed", "project is busy", "")
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.cancels[name] = cancel
	summary := a.summaries[name]
	pc := config.ForProject(a.cfg.Project(name), path)
	a.mu.Unlock()

	go func() {
		defer func() {
			a.mu.Lock()
			delete(a.cancels, name)
			a.mu.Unlock()
			cancel()
		}()
		screens := make(chan executor.StatusUpdate, 10)
		go func() {
			for u := range screens {
				runtime.EventsEmit(a.ctx, "status-update", map[string]string{"project": name, "screen": u.Screen})
			}
		}()
		defer close(screens)
		if summary == "" {
			summary = "Drupal dependency updates by drupal-updater."
		}
		url, err := executor.MergeRequest(ctx, name, path, branch, pc, summary, func(m string) {
			logging.Info("[%s] merge: %s", name, m)
			emit("working", m, "")
		}, screens)
		if err != nil {
			logging.Error("[%s] merge request failed: %v", name, err)
			emit("failed", err.Error(), "")
			return
		}
		emit("done", "Merge request opened", url)
	}()
}

func main() {
	// git/ddev guard behind the shims of a Claude run (see executor/guard.go)
	if len(os.Args) > 1 && os.Args[1] == "guard" {
		os.Exit(executor.Guard(os.Args[2:]))
	}

	// If invoked with "api" subcommand, handle it (for CLI compatibility)
	if len(os.Args) > 1 && os.Args[1] == "api" {
		api.Run(os.Args[2:])
		return
	}

	logging.Init()
	defer logging.Close()

	app := NewApp()

	w, h := loadWindowSize()
	err := wails.Run(&options.App{
		Title:         "Drupal Updater",
		Width:         w,
		Height:        h,
		OnBeforeClose: saveWindowSize,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		// macOS: content extends under a transparent title bar; traffic lights stay. Linux keeps its native frame.
		Mac: &mac.Options{
			TitleBar: mac.TitleBarHiddenInset(),
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
