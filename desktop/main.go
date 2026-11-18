package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"drupal-updater/api"
	"drupal-updater/config"
	"drupal-updater/coreupdates"
	"drupal-updater/dashboard"
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
	summaries  map[string]string   // last successful run's summary per project, used as merge request description
	update     *updater.Release    // newer release found by the daily check
	configErr  string              // set when config.yaml was unreadable at start
	core       coreupdates.Tracker // release history + per-project core versions
	dash       *dashboard.Client   // applications dashboard; nil until dashboard_url is configured
	dashURL    string
	dashProj   []dashboard.Project // cached for an hour
	dashAt     time.Time
	paths      map[string]string // project name -> path, from the last discovery
}

func NewApp() *App {
	home, _ := os.UserHomeDir()
	configPath := filepath.Join(home, ".config", "drupal-updater", "config.yaml")
	cfg, err := config.LoadConfig(configPath)
	configErr := ""
	if err != nil {
		// keep the unreadable file: running on defaults and saving must not overwrite it
		if bad, berr := config.BackupBadConfig(configPath); berr == nil {
			configErr = fmt.Sprintf("Your config could not be read (%v). It was kept as %s and defaults are in use.", err, bad)
			logging.Error("%s", configErr)
		} else {
			configErr = fmt.Sprintf("Your config could not be read: %v", err)
		}
		cfg = &config.Config{
			BaseFolders:   []string{"~/projects"},
			MaxConcurrent: 3,
			Projects:      make(map[string]config.ProjectConfig),
		}
	}
	executor.Configure(cfg)
	return &App{cfg: cfg, configPath: configPath, configErr: configErr}
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
	cfg := a.config()
	projects, err := discovery.DiscoverProjects(cfg.BaseFolders, cfg.ProjectPaths, cfg.ExcludedProjects, cfg.ScanDepth)
	if err != nil {
		return nil, err
	}
	result := make([]ProjectInfo, len(projects))
	paths := make(map[string]string, len(projects))
	for i, p := range projects {
		_, err := os.Stat(filepath.Join(p.Path, config.ProjectFile))
		paths[p.Name] = p.Path
		result[i] = ProjectInfo{Name: p.Name, Path: p.Path, RepoFile: err == nil}
	}
	var wg sync.WaitGroup // composer.lock files can be several MB: read them side by side
	gate := make(chan struct{}, 8)
	for i := range result {
		wg.Add(1)
		gate <- struct{}{}
		go func(i int) {
			defer func() { <-gate; wg.Done() }()
			result[i].Core = discovery.CoreVersion(result[i].Path)
		}(i)
	}
	wg.Wait()
	a.mu.Lock()
	a.paths = paths
	a.mu.Unlock()
	return result, nil
}

// RefreshReleases drops the cached Drupal release history so the next CoreStatuses call downloads it again.
func (a *App) RefreshReleases() { a.core.ResetReleases() }

// RefreshCore returns the Drupal core version committed on origin's start branch for a discovered project
// (see coreupdates.Tracker.Refresh); a project that is being updated right now is never fetched.
func (a *App) RefreshCore(name string, force bool) coreupdates.Refresh {
	a.mu.Lock()
	path := a.paths[name]
	_, running := a.cancels[name]
	a.mu.Unlock()
	if path == "" {
		return coreupdates.Refresh{}
	}
	pCfg := config.ForProject(a.config().Project(name), path)
	return a.core.Refresh(path, pCfg.GitStartBranch, force, running)
}

// CoreStatuses compares installed core versions with Drupal's published releases (cached for an hour);
// offline the map is empty and the UI shows no dots.
func (a *App) CoreStatuses(versions []string) map[string]coreupdates.Result {
	return a.core.Statuses(versions)
}

// PickFolder opens the native folder dialog and returns the chosen folder ("" when cancelled), written
// as ~/... when it is inside the home folder, like the config does.
func (a *App) PickFolder() string {
	dir, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: "Choose a folder"})
	if err != nil || dir == "" {
		return ""
	}
	if home, err := os.UserHomeDir(); err == nil {
		if rel, err := filepath.Rel(home, dir); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			return "~/" + filepath.ToSlash(rel)
		}
	}
	return dir
}

// PreviewProjects counts the projects these (possibly unsaved) settings would find, for the live hint in Settings.
func (a *App) PreviewProjects(baseFolders, projectPaths, excluded []string, depth int) int {
	projects, err := discovery.DiscoverProjects(baseFolders, projectPaths, excluded, depth)
	if err != nil {
		return 0
	}
	return len(projects)
}

// --- Applications dashboard (MCP server): read-only extra context per project ---

// DashboardState is what Settings shows about the dashboard connection.
type DashboardState struct {
	Configured bool   `json:"configured"`
	Error      string `json:"error,omitempty"` // why the address cannot be used
	SignedIn   bool   `json:"signedIn"`
	Server     string `json:"server"`
}

// DashInfo is a dashboard project matched to a local project.
type DashInfo struct {
	Name            string   `json:"name"`
	URL             string   `json:"url"`
	SLA             string   `json:"sla"`
	Customer        string   `json:"customer"`
	AccountManager  string   `json:"accountManager"`
	CoreVersion     string   `json:"coreVersion"`
	PHP             string   `json:"php"`
	EOLRuntimes     []string `json:"eolRuntimes"`
	SecurityUpdates []string `json:"securityUpdates"`
	SecurityKnown   bool     `json:"securityKnown"` // the pending-security list was looked up (empty alone does not mean none)
	MatchReason     string   `json:"matchReason"`   // how the local project was linked to this dashboard project
	AutoMatched     bool     `json:"autoMatched"`   // found by detection rather than by name or configuration
	Ref             string   `json:"ref"`           // what to store as dashboard_project to pin this match
	Stale           bool     `json:"stale"`
	LastScan        string   `json:"lastScan"`
}

// dashClient returns the client for the configured dashboard, or an error when there is none.
func (a *App) dashClient() (*dashboard.Client, error) {
	url := strings.TrimSpace(a.config().DashboardURL)
	if url == "" {
		url = config.DefaultDashboardURL
	}
	server, err := dashboard.ServerFrom(url)
	if err != nil {
		return nil, err
	}
	url = server
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.dash == nil || a.dashURL != url {
		home, _ := os.UserHomeDir()
		c, err := dashboard.New(url, dashboard.FileStore{Path: filepath.Join(home, ".config", "drupal-updater", "dashboard.json")}, nil)
		if err != nil {
			return nil, err
		}
		a.dash, a.dashURL, a.dashProj = c, url, nil
	}
	return a.dash, nil
}

func (a *App) DashboardState() DashboardState {
	c, err := a.dashClient()
	if err != nil {
		return DashboardState{Server: strings.TrimSpace(a.config().DashboardURL), Error: err.Error()}
	}
	a.mu.Lock()
	server := a.dashURL
	a.mu.Unlock()
	return DashboardState{Configured: true, SignedIn: c.SignedIn(), Server: server}
}

// DashboardSignIn opens the browser for the dashboard login and returns when it is done (or after 5 minutes).
func (a *App) DashboardSignIn() error {
	c, err := a.dashClient()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	return c.SignIn(ctx, func(u string) error { runtime.BrowserOpenURL(a.ctx, u); return nil })
}

func (a *App) DashboardSignOut() error {
	c, err := a.dashClient()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	a.mu.Lock()
	a.dashProj = nil
	a.mu.Unlock()
	return c.SignOut(ctx)
}

// dashProjects returns the dashboard's Drupal projects (cached for an hour; force = reload) and the local project
// names. "signed-out" as the error means the UI should offer the sign-in.
func (a *App) dashProjects(force bool) ([]dashboard.Project, []string, error) {
	c, err := a.dashClient()
	if err != nil {
		return nil, nil, err
	}
	a.mu.Lock()
	cached, at := a.dashProj, a.dashAt
	local := make([]string, 0, len(a.paths))
	for name := range a.paths {
		local = append(local, name)
	}
	a.mu.Unlock()
	sort.Strings(local)
	if force || cached == nil || time.Since(at) > time.Hour {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		cached, err = c.DrupalProjects(ctx)
		if errors.Is(err, dashboard.ErrSignedOut) {
			return nil, nil, errors.New("signed-out")
		}
		if err != nil {
			logging.Info("dashboard: %v", err)
			return nil, nil, err
		}
		a.mu.Lock()
		a.dashProj, a.dashAt = cached, time.Now()
		a.mu.Unlock()
	}
	return cached, local, nil
}

// dashOverrides reads each local project's dashboard_project (its own local entry, or the repo's drupal-updater.yaml,
// which wins) so that names which differ from the dashboard's can be matched.
func (a *App) dashOverrides(local []string) map[string]string {
	cfg := a.config()
	a.mu.Lock()
	paths := make(map[string]string, len(local))
	for _, n := range local {
		paths[n] = a.paths[n]
	}
	a.mu.Unlock()
	out := map[string]string{}
	for _, n := range local {
		if v := config.ForProject(cfg.Project(n), paths[n]).DashboardProject; v != "" {
			out[n] = v
		}
	}
	return out
}

// safeDashURL returns raw only when it is an https address on the dashboard's own host: the URL is opened in the
// user's browser, so one that came from the server must not be able to start another application (smb://, vscode://).
func safeDashURL(raw, server string) string {
	u, err := url.Parse(raw)
	s, serr := url.Parse(server)
	if err != nil || serr != nil || u.Scheme != "https" || u.Host == "" || u.Host != s.Host {
		return ""
	}
	return u.String()
}

// dashMatcher prepares the matching: every local project with what its files reveal (git remote, DDEV name, site name).
func (a *App) dashMatcher(projects []dashboard.Project, local []string) *dashboard.Matcher {
	a.mu.Lock()
	paths := make(map[string]string, len(local))
	for _, n := range local {
		paths[n] = a.paths[n]
	}
	a.mu.Unlock()
	locals := make([]dashboard.Local, len(local))
	for i, n := range local {
		locals[i] = dashboard.Local{Name: n, Hints: dashboard.ReadHints(paths[n])}
	}
	return dashboard.NewMatcher(locals, projects)
}

// DashboardInfo returns the dashboard data for the discovered local projects, keyed by local name.
func (a *App) DashboardInfo(force bool) (map[string]DashInfo, error) {
	projects, local, err := a.dashProjects(force)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	server := a.dashURL
	a.mu.Unlock()
	out := map[string]DashInfo{}
	for name, mt := range a.dashMatcher(projects, local).Resolve(a.dashOverrides(local)) {
		p := mt.Project
		out[name] = DashInfo{MatchReason: mt.Reason, AutoMatched: mt.Auto, Ref: dashboard.Ref(p, projects), Name: p.Name, URL: safeDashURL(p.DashboardURL, server), SLA: p.SLA, Customer: p.Customer, AccountManager: p.AccountManager,
			CoreVersion: p.CoreVersion, PHP: p.PHP, EOLRuntimes: p.EOLRuntimes, SecurityUpdates: p.SecurityUpdates, SecurityKnown: p.SecurityKnown, Stale: p.Stale, LastScan: p.LastScan}
	}
	return out, nil
}

// DashRef is a dashboard project as the picker offers it; Ref is what gets stored as dashboard_project.
type DashRef struct {
	Ref      string `json:"ref"`
	Name     string `json:"name"`
	Customer string `json:"customer"`
}

// UnmatchedProject is a local project without a dashboard match, with the closest dashboard names.
type UnmatchedProject struct {
	Local       string    `json:"local"`
	Suggestions []DashRef `json:"suggestions"`
}

// DashCandidates is everything the picker needs: all dashboard projects, and the local ones still unmatched.
type DashCandidates struct {
	Projects  []DashRef          `json:"projects"`
	Unmatched []UnmatchedProject `json:"unmatched"`
}

func (a *App) DashboardCandidates() (DashCandidates, error) {
	projects, local, err := a.dashProjects(false)
	if err != nil {
		return DashCandidates{}, err
	}
	matcher := a.dashMatcher(projects, local)
	matched := matcher.Resolve(a.dashOverrides(local))
	taken := map[string]bool{}
	for _, mt := range matched {
		taken[mt.Project.ID] = true
	}
	ref := func(p dashboard.Project) DashRef {
		return DashRef{Ref: dashboard.Ref(p, projects), Name: p.Name, Customer: p.Customer}
	}
	out := DashCandidates{Projects: []DashRef{}, Unmatched: []UnmatchedProject{}}
	for _, p := range projects {
		out.Projects = append(out.Projects, ref(p))
	}
	sort.Slice(out.Projects, func(i, j int) bool {
		return strings.ToLower(out.Projects[i].Name) < strings.ToLower(out.Projects[j].Name)
	})
	for _, name := range local {
		if _, ok := matched[name]; ok {
			continue
		}
		u := UnmatchedProject{Local: name, Suggestions: []DashRef{}}
		for _, p := range matcher.Suggest(name, taken, 3) {
			u.Suggestions = append(u.Suggestions, ref(p))
		}
		out.Unmatched = append(out.Unmatched, u)
	}
	return out, nil
}

// config returns the current config; SaveConfig/SetUpdateMode replace the pointer, never edit it in place.
func (a *App) config() *config.Config {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg
}

// GetConfigError is non-empty when config.yaml could not be read at start (the file is kept as config.yaml.bad).
func (a *App) GetConfigError() string { return a.configErr }

func (a *App) GetConfig() *config.Config {
	return a.config()
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
	c := *a.cfg
	c.UpdateMode = mode
	a.cfg = &c
	executor.Configure(a.cfg)
	return config.SaveConfig(a.configPath, a.cfg)
}

type UpdateRequest struct {
	Projects []string `json:"projects"`
}

// projectPath finds a discovered project's folder by name.
func (a *App) projectPath(name string) (string, error) {
	a.mu.Lock()
	cached := a.paths[name]
	a.mu.Unlock()
	if fi, err := os.Stat(cached); cached != "" && err == nil && fi.IsDir() {
		return cached, nil // known from the last discovery; no need to walk every base folder again
	}
	cfg := a.config()
	projects, err := discovery.DiscoverProjects(cfg.BaseFolders, cfg.ProjectPaths, cfg.ExcludedProjects, cfg.ScanDepth)
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
	maxConc := a.cfg.MaxConcurrent
	a.mu.Unlock()
	n := executor.ConcurrencyCap(maxConc) // asks Docker: outside the lock, so the UI never waits on it
	a.mu.Lock()
	if a.sem == nil {
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
		projectPath, err := a.projectPath(projectName) // the discovery cache, rescanned only when the folder is gone
		if err != nil {
			msg := err.Error()
			runtime.EventsEmit(a.ctx, "status-update", map[string]string{"project": projectName, "status": "failed", "command": "discover", "output": msg})
			a.mu.Lock()
			delete(a.cancels, projectName)
			a.mu.Unlock()
			continue
		}

		pCfg := config.ForProject(a.config().Project(projectName), projectPath)

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
	for _, cancel := range a.cancels { // also merge requests, which are not children of parentCtx
		cancel()
	}
}

// beforeClose is the window's OnBeforeClose: it saves the window size and, while updates or merge requests
// are running, asks first, because quitting abandons their Claude sessions.
func (a *App) beforeClose(ctx context.Context) bool {
	saveWindowSize(ctx)
	a.mu.Lock()
	busy := len(a.cancels) > 0 || a.pending > 0
	a.mu.Unlock()
	if !busy {
		return false
	}
	choice, err := runtime.MessageDialog(ctx, runtime.MessageDialogOptions{
		Type:          runtime.QuestionDialog,
		Title:         "Updates are still running",
		Message:       "Quitting now cancels the running updates and merge requests. Quit anyway?",
		Buttons:       []string{"Quit", "Keep running"},
		DefaultButton: "Keep running",
		CancelButton:  "Keep running",
	})
	if err != nil || choice != "Quit" {
		return true // prevent the close
	}
	a.CancelAll()
	return false
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
	pCfg := config.ForProject(a.config().Project(projectName), projectPath)
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

	pCfg := config.ForProject(a.config().Project(projectName), projectPath)

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
	if summary == "" {
		summary = executor.LastSummary(name) // the run finished before a restart
	}
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
		MinWidth:      760,
		MinHeight:     480,
		OnBeforeClose: app.beforeClose,
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
