package ui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"drupal-updater/config"
	"drupal-updater/discovery"
	"drupal-updater/executor"
	"drupal-updater/logging"
	"drupal-updater/updater"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// UI States
const (
	StateList = iota
	StateRunning
	StateDetail
	StateConfirmBranch
	StateSettings
)

type model struct {
	cfg      *config.Config
	projects []discovery.Project
	selected map[string]bool
	status   map[string]string
	logs     map[string]string
	screens  map[string]string // live Claude terminal per project

	cursor    int
	state     int
	prevState int

	msgChan   chan executor.StatusUpdate
	totalJobs int
	doneJobs  int

	width  int
	height int

	scrollOffset int

	detailProject string
	detailVP      viewport.Model

	runCursor        int
	selectedProjects []string

	// Cancellation
	cancels   map[string]context.CancelFunc // per-project cancel
	cancelAll context.CancelFunc            // cancel everything

	// Search/filter
	searchQuery string

	// Branch confirmation state
	branchNames    map[string]string // project -> final branch name
	tempBranches   map[string]string // project -> temp branch name
	branchInput    textinput.Model
	branchCursor   int
	editingBranch  bool
	confirmDone    bool
	confirmResults map[string]string

	// Merge requests after the rename (see executor.MergeRequest)
	summaries map[string]string // last successful run's summary, used as MR description
	mrStatus  map[string]string // project -> progress, URL or error
	mergeChan chan tea.Msg      // mergeMsg and Claude screens of conflict sessions
	merging   int               // merge requests still running
	mrCancels map[string]context.CancelFunc

	configPath string
	version    string
	repoFile   map[string]bool  // project has a committed drupal-updater.yaml
	update     *updater.Release // newer release, from the start-up check
	updateMsg  string           // install progress or error
	restart    *exec.Cmd        // set after an update: main replaces the process with it
	settings   *configModel     // open settings screen (StateSettings)
}

type updateMsg struct {
	r   *updater.Release
	cmd *exec.Cmd // set when an install finished
	err error
}

// mergeMsg is progress (done=false) or the result of one project's merge request.
type mergeMsg struct {
	project, text string
	done          bool
}

func waitForMerge(c chan tea.Msg) tea.Cmd { return func() tea.Msg { return <-c } }

// Color palette
var (
	purple   = lipgloss.Color("99")
	pink     = lipgloss.Color("205")
	cyan     = lipgloss.Color("86")
	yellow   = lipgloss.Color("214")
	red      = lipgloss.Color("196")
	green    = lipgloss.Color("78")
	dimWhite = lipgloss.Color("252")
	dim      = lipgloss.Color("241")
	darkGray = lipgloss.Color("236")
)

// Styles
var (
	titleStyle = lipgloss.NewStyle().
			Foreground(pink).
			Bold(true).
			MarginBottom(1)

	subtitleStyle = lipgloss.NewStyle().
			Foreground(dim).
			Italic(true)

	boxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(purple).
			Padding(0, 1)

	itemStyle = lipgloss.NewStyle().
			PaddingLeft(2)

	cursorStyle = lipgloss.NewStyle().
			Foreground(cyan).
			Bold(true)

	selectedItemStyle = lipgloss.NewStyle().
				PaddingLeft(2).
				Foreground(cyan)

	checkboxOn = lipgloss.NewStyle().
			Foreground(green).
			Bold(true)

	checkboxOff = lipgloss.NewStyle().
			Foreground(dim)

	statusRunningStyle = lipgloss.NewStyle().
				Foreground(yellow).
				Bold(true)

	statusSuccessStyle = lipgloss.NewStyle().
				Foreground(green).
				Bold(true)

	statusFailedStyle = lipgloss.NewStyle().
				Foreground(red).
				Bold(true)

	statusIdleStyle = lipgloss.NewStyle().
			Foreground(dim)

	logLineStyle = lipgloss.NewStyle().
			Foreground(dim).
			PaddingLeft(6)

	helpStyle = lipgloss.NewStyle().
			Foreground(dim)

	helpKeyStyle = lipgloss.NewStyle().
			Foreground(dimWhite).
			Bold(true)

	moreStyle = lipgloss.NewStyle().
			Foreground(dim).
			Italic(true).
			PaddingLeft(4)

	detailHeaderStyle = lipgloss.NewStyle().
				Foreground(pink).
				Bold(true).
				MarginBottom(1)

	completedStyle = lipgloss.NewStyle().
			Foreground(green).
			Bold(true).
			MarginTop(1)

	progressStyle = lipgloss.NewStyle().
			Foreground(dim).
			Italic(true)
)

// New builds the TUI. configPath is where mode changes are saved; version ("dev" for local builds) drives the update check.
func New(cfg *config.Config, projects []discovery.Project, configPath, version string) *model {
	bi := textinput.New()
	bi.CharLimit = 200
	bi.Width = 40
	bi.PromptStyle = lipgloss.NewStyle().Foreground(cyan)
	bi.TextStyle = lipgloss.NewStyle().Foreground(dimWhite)

	m := &model{
		cfg:            cfg,
		projects:       projects,
		selected:       make(map[string]bool),
		status:         make(map[string]string),
		logs:           make(map[string]string),
		screens:        make(map[string]string),
		state:          StateList,
		branchInput:    bi,
		msgChan:        make(chan executor.StatusUpdate, 100),
		summaries:      make(map[string]string),
		mrStatus:       make(map[string]string),
		mrCancels:      make(map[string]context.CancelFunc),
		mergeChan:      make(chan tea.Msg, 100),
		width:          80,
		height:         24,
		branchNames:    make(map[string]string),
		tempBranches:   make(map[string]string),
		confirmResults: make(map[string]string),
		configPath:     configPath,
		version:        version,
		repoFile:       make(map[string]bool),
	}

	for _, p := range projects {
		m.status[p.Name] = "idle"
		m.selected[p.Name] = false
		_, err := os.Stat(filepath.Join(p.Path, config.ProjectFile))
		m.repoFile[p.Name] = err == nil
	}

	return m
}

func (m *model) Init() tea.Cmd {
	v := m.version
	return tea.Batch(textinput.Blink, func() tea.Msg {
		r, err := updater.Check(v, true)
		if err != nil {
			logging.Error("update check: %v", err)
		}
		return updateMsg{r: r}
	})
}

// closeSettings reloads the config from disk (so unsaved edits are dropped), applies it and
// rescans the project folders, then returns to the project list.
func (m *model) closeSettings() {
	m.settings, m.state = nil, StateList
	cfg, err := config.LoadConfig(m.configPath)
	if err != nil {
		m.updateMsg = "✗ reloading settings: " + err.Error()
		return
	}
	*m.cfg = *cfg
	executor.Configure(m.cfg)
	projects, err := discovery.DiscoverProjects(m.cfg.BaseFolders, m.cfg.ProjectPaths, m.cfg.ExcludedProjects)
	if err != nil {
		m.updateMsg = "✗ scanning project folders: " + err.Error()
		return
	}
	m.projects = projects
	for _, p := range projects {
		if _, ok := m.status[p.Name]; !ok {
			m.status[p.Name] = "idle"
		}
		_, err := os.Stat(filepath.Join(p.Path, config.ProjectFile))
		m.repoFile[p.Name] = err == nil
	}
	m.cursor, m.scrollOffset = 0, 0
}

// Restart is the command to replace this process with after an update, or nil.
func (m *model) Restart() *exec.Cmd { return m.restart }

func waitForActivity(sub chan executor.StatusUpdate) tea.Cmd {
	return func() tea.Msg {
		return <-sub
	}
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.detailVP.Width = m.width - 4
		m.detailVP.Height = m.height - 6
		return m, nil
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
	case settingsClosedMsg:
		m.closeSettings()
		return m, nil
	case updateMsg:
		switch {
		case msg.err != nil:
			m.updateMsg = "✗ update failed: " + msg.err.Error()
		case msg.cmd != nil:
			m.restart = msg.cmd
			return m, tea.Quit
		default:
			m.update = msg.r
		}
		return m, nil
	case mergeMsg: // handled in any view, so progress keeps flowing while logs are open
		m.mrStatus[msg.project] = msg.text
		m.logs[msg.project] += "merge request: " + msg.text + "\n"
		if msg.done {
			m.merging--
			delete(m.mrCancels, msg.project)
		}
		if m.state == StateDetail && msg.project == m.detailProject {
			m.detailVP.SetContent(m.detailText(m.detailProject))
		}
		if m.merging > 0 {
			return m, waitForMerge(m.mergeChan)
		}
		return m, nil
	case executor.StatusUpdate:
		if m.merging > 0 && msg.Screen != "" && m.confirmDone { // conflict session screen
			m.screens[msg.ProjectName] = msg.Screen
			if m.state == StateDetail && msg.ProjectName == m.detailProject {
				m.detailVP.SetContent(m.detailText(m.detailProject))
			}
			return m, waitForMerge(m.mergeChan)
		}
	}

	switch m.state {
	case StateList:
		m, cmds = m.updateList(msg)
	case StateRunning:
		m, cmds = m.updateRunning(msg)
	case StateDetail:
		m, cmds = m.updateDetail(msg)
	case StateConfirmBranch:
		m, cmds, cmd = m.updateConfirmBranch(msg)
		cmds = append(cmds, cmd)
	case StateSettings:
		_, cmd = m.settings.Update(msg)
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

func (m *model) jumpToMatch() {
	query := strings.ToLower(m.searchQuery)
	for i, p := range m.projects {
		if strings.Contains(strings.ToLower(p.Name), query) {
			m.cursor = i
			m.ensureVisible()
			return
		}
	}
}

func (m *model) updateList(msg tea.Msg) (*model, []tea.Cmd) {
	switch msg := msg.(type) {
	case tea.MouseMsg:
		switch msg.Type {
		case tea.MouseWheelUp:
			if m.cursor > 0 {
				m.cursor--
				m.ensureVisible()
			}
		case tea.MouseWheelDown:
			if m.cursor < len(m.projects)-1 {
				m.cursor++
				m.ensureVisible()
			}
		}
	case tea.KeyMsg:
		key := msg.String()

		// Always handle navigation and search-control keys
		switch key {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
				m.ensureVisible()
			}
			return m, nil
		case "down", "j":
			if m.cursor < len(m.projects)-1 {
				m.cursor++
				m.ensureVisible()
			}
			return m, nil
		case " ", "enter":
			if len(m.projects) == 0 { // possible after the project folders changed in settings
				return m, nil
			}
			pName := m.projects[m.cursor].Name
			m.selected[pName] = !m.selected[pName]
			return m, nil
		case "esc":
			m.searchQuery = ""
			return m, nil
		case "backspace":
			if len(m.searchQuery) > 0 {
				m.searchQuery = m.searchQuery[:len(m.searchQuery)-1]
				if m.searchQuery != "" {
					m.jumpToMatch()
				}
			}
			return m, nil
		}

		// When search is empty, handle action keys
		if m.searchQuery == "" {
			switch key {
			case "q":
				return m, []tea.Cmd{tea.Quit}
			case "a":
				allSelected := true
				for _, p := range m.projects {
					if !m.selected[p.Name] {
						allSelected = false
						break
					}
				}
				for _, p := range m.projects {
					m.selected[p.Name] = !allSelected
				}
				return m, nil
			case "m": // saved, like the desktop toggle
				if m.cfg.UpdateMode == "disaster" {
					m.cfg.UpdateMode = "standard"
				} else {
					m.cfg.UpdateMode = "disaster"
				}
				executor.Configure(m.cfg)
				if err := config.SaveConfig(m.configPath, m.cfg); err != nil {
					logging.Error("saving update mode: %v", err)
				}
				return m, nil
			case "S":
				m.settings = NewConfigModel(m.cfg, m.configPath)
				m.settings.embedded = true
				m.state = StateSettings
				return m, nil
			case "U":
				if m.update == nil || !m.update.CanInstall || strings.HasPrefix(m.updateMsg, "Downloading") {
					return m, nil
				}
				m.updateMsg = "Downloading " + m.update.Version + "…"
				r := m.update
				return m, []tea.Cmd{func() tea.Msg {
					cmd, err := updater.Install(r)
					return updateMsg{cmd: cmd, err: err}
				}}
			case "u":
				logging.Info("Starting execution from StateList")
				m.state = StateRunning
				m.prepareRunningView()
				m.startExecution()
				return m, []tea.Cmd{waitForActivity(m.msgChan)}
			}
		}

		// Type-to-search: append printable characters
		if len(key) == 1 && key[0] >= 32 && key[0] < 127 {
			m.searchQuery += key
			m.jumpToMatch()
		}
	}
	return m, nil
}

func (m *model) updateRunning(msg tea.Msg) (*model, []tea.Cmd) {
	var cmds []tea.Cmd
	switch msg := msg.(type) {
	case tea.MouseMsg:
		switch msg.Type {
		case tea.MouseWheelUp:
			if m.runCursor > 0 {
				m.runCursor--
				m.ensureRunVisible()
			}
		case tea.MouseWheelDown:
			if m.runCursor < len(m.selectedProjects)-1 {
				m.runCursor++
				m.ensureRunVisible()
			}
		}
	case tea.KeyMsg:
		switch msg.String() {
		case "q":
			return m, []tea.Cmd{tea.Quit}
		case "up", "k":
			if m.runCursor > 0 {
				m.runCursor--
				m.ensureRunVisible()
			}
		case "down", "j":
			if m.runCursor < len(m.selectedProjects)-1 {
				m.runCursor++
				m.ensureRunVisible()
			}
		case "enter":
			if len(m.selectedProjects) > 0 {
				m.detailProject = m.selectedProjects[m.runCursor]
				m.prevState = StateRunning
				m.state = StateDetail
				m.detailVP = viewport.New(m.width-4, m.height-6)
				m.detailVP.SetContent(m.detailText(m.detailProject))
				m.detailVP.GotoBottom()
				logging.Info("Entering detail view for project: %s", m.detailProject)
			}
		case "x":
			// Cancel current (selected) project
			if len(m.selectedProjects) > 0 {
				name := m.selectedProjects[m.runCursor]
				if cancel, ok := m.cancels[name]; ok && m.status[name] == "running" {
					cancel()
				}
			}
		case "X":
			// Cancel all
			if m.cancelAll != nil {
				m.cancelAll()
			}
		}
	case executor.StatusUpdate:
		m.applyUpdate(msg)
		if m.doneJobs < m.totalJobs {
			cmds = append(cmds, waitForActivity(m.msgChan))
		} else if m.doneJobs >= m.totalJobs && m.totalJobs > 0 {
			// All jobs done — transition to branch confirmation
			logging.Info("All jobs complete (%d/%d), transitioning to StateConfirmBranch", m.doneJobs, m.totalJobs)
			m.state = StateConfirmBranch
			m.branchCursor = 0
			m.editingBranch = false
		}
	}
	return m, cmds
}

// applyUpdate records a status update: a screen snapshot replaces the live view, anything else is logged.
func (m *model) applyUpdate(msg executor.StatusUpdate) {
	if msg.Screen != "" {
		m.screens[msg.ProjectName] = msg.Screen
		return
	}
	m.status[msg.ProjectName] = msg.Status
	if msg.Status == "success" {
		m.summaries[msg.ProjectName] = msg.Output
	}
	if msg.Output != "" {
		m.logs[msg.ProjectName] += msg.Command + "\n" + msg.Output + "\n"
	} else {
		m.logs[msg.ProjectName] += "$ " + msg.Command + "\n"
	}
	if msg.Status == "success" || msg.Status == "failed" || msg.Status == "cancelled" {
		m.doneJobs++
	}
}

// detailText is a project's log followed by the latest Claude screen, if any.
func (m *model) detailText(name string) string {
	if m.screens[name] == "" {
		return m.logs[name]
	}
	return m.logs[name] + "\n── Claude session (live) ──\n" + strings.TrimRight(m.screens[name], "\n")
}

func (m *model) updateDetail(msg tea.Msg) (*model, []tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc":
			m.state = m.prevState
			logging.Info("Leaving detail view, returning to state %d", m.prevState)
			if m.doneJobs < m.totalJobs {
				return m, []tea.Cmd{waitForActivity(m.msgChan)}
			}
			return m, nil
		}
	case executor.StatusUpdate:
		m.applyUpdate(msg)
		if msg.ProjectName == m.detailProject {
			follow := m.detailVP.AtBottom() // don't yank the view away while the user scrolls back
			m.detailVP.SetContent(m.detailText(m.detailProject))
			if follow {
				m.detailVP.GotoBottom()
			}
		}
		if m.doneJobs < m.totalJobs {
			return m, []tea.Cmd{waitForActivity(m.msgChan)}
		}
		// All done while in detail — go to confirm
		logging.Info("All jobs complete while in detail view, transitioning to StateConfirmBranch")
		m.state = StateConfirmBranch
		m.branchCursor = 0
		m.editingBranch = false
		return m, nil
	}
	var cmd tea.Cmd
	m.detailVP, cmd = m.detailVP.Update(msg)
	return m, []tea.Cmd{cmd}
}

func (m *model) updateConfirmBranch(msg tea.Msg) (*model, []tea.Cmd, tea.Cmd) {
	var cmds []tea.Cmd
	var cmd tea.Cmd

	// If editing a branch name, delegate keys to the text input
	if m.editingBranch {
		switch msg := msg.(type) {
		case tea.KeyMsg:
			switch msg.String() {
			case "enter", "esc":
				// Save value and stop editing
				m.branchNames[m.selectedProjects[m.branchCursor]] = m.branchInput.Value()
				m.editingBranch = false
				m.branchInput.Blur()
				return m, cmds, nil
			}
		}
		m.branchInput, cmd = m.branchInput.Update(msg)
		return m, cmds, cmd
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q":
			if m.merging > 0 {
				return m, cmds, nil // quitting would leave a merge (and maybe its Claude session) half done
			}
			if !m.confirmDone {
				// Cleanup failed/cancelled projects before quitting
				m.runConfirmation(false)
			}
			return m, []tea.Cmd{tea.Quit}, nil
		case "m", "M":
			if !m.confirmDone || len(m.selectedProjects) == 0 {
				break
			}
			names := []string{m.selectedProjects[m.branchCursor]}
			if msg.String() == "M" {
				names = m.selectedProjects
			}
			if cmd := m.startMerges(names); cmd != nil {
				return m, []tea.Cmd{cmd}, nil
			}
		case "x":
			if m.confirmDone && len(m.selectedProjects) > 0 {
				if cancel, ok := m.mrCancels[m.selectedProjects[m.branchCursor]]; ok {
					cancel()
				}
			}
		case "up", "k":
			if m.branchCursor > 0 {
				m.branchCursor--
			}
		case "down", "j":
			if m.branchCursor < len(m.selectedProjects)-1 {
				m.branchCursor++
			}
		case "d", "enter":
			if m.confirmDone && msg.String() == "d" && len(m.selectedProjects) > 0 {
				pName := m.selectedProjects[m.branchCursor]
				m.detailProject = pName
				m.prevState = StateConfirmBranch
				m.state = StateDetail
				m.detailVP = viewport.New(m.width-4, m.height-6)
				m.detailVP.SetContent(m.detailText(pName))
				m.detailVP.GotoBottom()
				return m, cmds, nil
			}
			if !m.confirmDone && len(m.selectedProjects) > 0 {
				pName := m.selectedProjects[m.branchCursor]
				if msg.String() == "enter" && m.status[pName] == "success" {
					// Start editing this project's branch name
					m.branchInput.SetValue(m.branchNames[pName])
					m.branchInput.Focus()
					m.editingBranch = true
					return m, cmds, textinput.Blink
				}
				if msg.String() == "d" {
					// View detail logs
					m.detailProject = pName
					m.prevState = StateConfirmBranch
					m.state = StateDetail
					m.detailVP = viewport.New(m.width-4, m.height-6)
					m.detailVP.SetContent(m.detailText(pName))
					m.detailVP.GotoBottom()
					return m, cmds, nil
				}
			}
		case "c":
			if !m.confirmDone {
				m.runConfirmation(true)
			}
		case "r":
			if !m.confirmDone {
				cmd := m.retryFailed()
				if cmd != nil {
					return m, []tea.Cmd{cmd}, nil
				}
			}
		}
	}
	return m, cmds, nil
}

func (m *model) retryFailed() tea.Cmd {
	var retryProjects []string
	for _, pName := range m.selectedProjects {
		if m.status[pName] == "failed" || m.status[pName] == "cancelled" {
			retryProjects = append(retryProjects, pName)
		}
	}
	if len(retryProjects) == 0 {
		return nil
	}

	// Reset state for retry
	m.state = StateRunning
	m.totalJobs = len(retryProjects)
	m.doneJobs = 0
	m.selectedProjects = retryProjects
	m.runCursor = 0
	m.scrollOffset = 0
	m.msgChan = make(chan executor.StatusUpdate, 100)

	parentCtx, cancelAll := context.WithCancel(context.Background())
	m.cancelAll = cancelAll
	m.cancels = make(map[string]context.CancelFunc)

	sem := make(chan struct{}, m.cfg.MaxConcurrent)
	for _, pName := range retryProjects {
		m.status[pName] = "idle"
		m.logs[pName] = ""
		delete(m.screens, pName)

		p := m.projectByName(pName)
		if p == nil {
			continue
		}

		pCfg := config.ForProject(m.cfg.Project(pName), p.Path)

		tempBranch := m.tempBranches[pName]
		startBranch := pCfg.GitStartBranch

		ctx, cancel := context.WithCancel(parentCtx)
		m.cancels[pName] = cancel
		pName := pName
		path := p.Path
		go func() {
			sem <- struct{}{}
			executor.Run(ctx, pName, path, startBranch, tempBranch, m.msgChan)
			<-sem
		}()
	}

	return waitForActivity(m.msgChan)
}

// renamed reports whether a project's temp branch was renamed in the confirmation step.
func (m *model) renamed(name string) bool {
	return strings.HasPrefix(m.confirmResults[name], "renamed to ")
}

// startMerges opens merge requests for the renamed projects among names that have none running or done.
func (m *model) startMerges(names []string) tea.Cmd {
	started := false
	for _, name := range names {
		p := m.projectByName(name)
		if p == nil || !m.renamed(name) {
			continue
		}
		if _, busy := m.mrCancels[name]; busy || strings.HasPrefix(m.mrStatus[name], "http") {
			continue
		}
		ctx, cancel := context.WithCancel(context.Background())
		m.mrCancels[name] = cancel
		m.merging++
		started = true
		m.mrStatus[name] = "starting…"
		pc := config.ForProject(m.cfg.Project(name), p.Path)
		branch, path, summary, c := m.branchNames[name], p.Path, m.summaries[name], m.mergeChan
		if summary == "" {
			summary = "Drupal dependency updates by drupal-updater."
		}
		go func() {
			screens := make(chan executor.StatusUpdate, 10)
			go func() {
				for u := range screens {
					c <- u
				}
			}()
			url, err := executor.MergeRequest(ctx, name, path, branch, pc, summary, func(t string) {
				c <- mergeMsg{project: name, text: t}
			}, screens)
			close(screens)
			cancel()
			if err != nil {
				logging.Error("[%s] merge request failed: %v", name, err)
				c <- mergeMsg{project: name, text: "✗ " + err.Error(), done: true}
				return
			}
			c <- mergeMsg{project: name, text: url, done: true}
		}()
	}
	if !started {
		return nil
	}
	return waitForMerge(m.mergeChan)
}

// runConfirmation performs the git rename/cleanup operations.
// If doRenames is true, successful projects get their temp branch renamed to final branch.
// Failed/cancelled projects always get cleaned up.
func (m *model) runConfirmation(doRenames bool) {
	logging.Info("Running branch confirmation (doRenames=%v)", doRenames)
	m.confirmDone = true

	for _, pName := range m.selectedProjects {
		p := m.projectByName(pName)
		if p == nil {
			continue
		}
		pCfg := config.ForProject(m.cfg.Project(pName), p.Path)

		tempBranch := m.tempBranches[pName]
		st := m.status[pName]

		if st == "success" && doRenames {
			finalBranch := m.branchNames[pName]
			logging.Info("[%s] Renaming branch %s -> %s", pName, tempBranch, finalBranch)
			err := executor.RenameBranch(p.Path, tempBranch, finalBranch)
			out := ""
			if err != nil {
				logging.Error("[%s] Branch rename failed: %v\n%s", pName, err, out)
				m.confirmResults[pName] = fmt.Sprintf("rename failed: %v", err)
			} else {
				logging.Info("[%s] Branch renamed successfully", pName)
				m.confirmResults[pName] = "renamed to " + finalBranch
			}
		} else if st != "success" {
			// Cleanup: checkout start branch and delete temp branch
			if tempBranch != "" {
				logging.Info("[%s] Cleaning up temp branch %s", pName, tempBranch)
				if err := executor.Cleanup(p.Path, pCfg.GitStartBranch, tempBranch); err != nil {
					logging.Error("[%s] Cleanup failed: %v", pName, err)
					m.confirmResults[pName] = "cleanup failed: " + err.Error()
					continue
				}
				m.confirmResults[pName] = "cleaned up"
			} else {
				m.confirmResults[pName] = "no cleanup needed"
			}
		}
	}
}

func (m *model) projectByName(name string) *discovery.Project {
	for i, p := range m.projects {
		if p.Name == name {
			return &m.projects[i]
		}
	}
	return nil
}

func (m *model) prepareRunningView() {
	m.selectedProjects = nil
	for _, p := range m.projects {
		if m.selected[p.Name] {
			m.selectedProjects = append(m.selectedProjects, p.Name)
		}
	}
	m.runCursor = 0
	m.scrollOffset = 0
}

func (m *model) ensureVisible() {
	visibleLines := m.availableLines()
	if visibleLines <= 0 {
		return
	}
	if m.cursor < m.scrollOffset {
		m.scrollOffset = m.cursor
	}
	if m.cursor >= m.scrollOffset+visibleLines {
		m.scrollOffset = m.cursor - visibleLines + 1
	}
}

func (m *model) ensureRunVisible() {
	visibleLines := m.availableLines()
	if visibleLines <= 0 {
		return
	}
	if m.runCursor < m.scrollOffset {
		m.scrollOffset = m.runCursor
	}
	if m.runCursor >= m.scrollOffset+visibleLines {
		m.scrollOffset = m.runCursor - visibleLines + 1
	}
}

func (m *model) availableLines() int {
	reserved := 8
	available := m.height - reserved
	if available < 1 {
		return 1
	}
	return available
}

func (m *model) startExecution() {
	m.totalJobs = 0
	m.doneJobs = 0
	m.cancels = make(map[string]context.CancelFunc)
	m.branchNames = make(map[string]string)
	m.tempBranches = make(map[string]string)
	m.confirmResults = make(map[string]string)

	parentCtx, cancelAll := context.WithCancel(context.Background())
	m.cancelAll = cancelAll

	type job struct {
		name        string
		path        string
		startBranch string
		tempBranch  string
	}

	var jobs []job
	for _, p := range m.projects {
		if !m.selected[p.Name] {
			continue
		}
		m.totalJobs++

		pCfg := config.ForProject(m.cfg.Project(p.Name), p.Path)

		tempBranch := executor.TempBranch(p.Name)
		m.tempBranches[p.Name] = tempBranch

		// Pre-fill final branch name from the pattern; editable on the confirm screen
		finalBranch := executor.FinalBranch(p.Path, pCfg.GitNewBranchPattern, p.Name)
		m.branchNames[p.Name] = finalBranch

		logging.Info("[%s] temp branch: %s, final branch: %s", p.Name, tempBranch, finalBranch)

		jobs = append(jobs, job{name: p.Name, path: p.Path, startBranch: pCfg.GitStartBranch, tempBranch: tempBranch})
	}

	sem := make(chan struct{}, m.cfg.MaxConcurrent)
	for _, j := range jobs {
		j := j
		ctx, cancel := context.WithCancel(parentCtx)
		m.cancels[j.name] = cancel
		go func() {
			sem <- struct{}{}
			executor.Run(ctx, j.name, j.path, j.startBranch, j.tempBranch, m.msgChan)
			<-sem
		}()
	}
}

// Helper to render help keys
func helpKey(key, desc string) string {
	return helpKeyStyle.Render(key) + helpStyle.Render(" "+desc)
}

func (m *model) View() string {
	var s strings.Builder

	// Header
	s.WriteString("\n")
	s.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, titleStyle.Render("  ✦ Drupal Updater"), progressStyle.Render("  "+m.version))) // title has a bottom margin
	s.WriteString("\n\n")

	switch m.state {
	case StateSettings:
		return m.settings.View()
	case StateList:
		s.WriteString(subtitleStyle.Render("  Select projects to update:") + "\n")
		if m.searchQuery != "" {
			s.WriteString("  " + lipgloss.NewStyle().Foreground(yellow).Render("🔍 "+m.searchQuery) + "\n")
		}
		s.WriteString("\n")

		visible := m.availableLines()
		end := m.scrollOffset + visible
		if end > len(m.projects) {
			end = len(m.projects)
		}

		if m.scrollOffset > 0 {
			s.WriteString(moreStyle.Render("↑ more") + "\n")
		}

		for i := m.scrollOffset; i < end; i++ {
			p := m.projects[i]

			cursor := "  "
			if m.cursor == i {
				cursor = cursorStyle.Render("▸ ")
			}

			var checkbox string
			if m.selected[p.Name] {
				checkbox = checkboxOn.Render("◉")
			} else {
				checkbox = checkboxOff.Render("○")
			}

			name := p.Name
			if m.cursor == i {
				name = lipgloss.NewStyle().Foreground(cyan).Render(name)
			} else {
				name = lipgloss.NewStyle().Foreground(dimWhite).Render(name)
			}

			if m.repoFile[p.Name] {
				name += progressStyle.Render("  repo config")
			}
			s.WriteString(fmt.Sprintf("  %s%s %s\n", cursor, checkbox, name))
		}

		if end < len(m.projects) {
			s.WriteString(moreStyle.Render("↓ more") + "\n")
		}

		// Count selected
		count := 0
		for _, p := range m.projects {
			if m.selected[p.Name] {
				count++
			}
		}
		s.WriteString("\n")
		mode := "standard (all minor/patch updates)"
		if m.cfg.UpdateMode == "disaster" {
			mode = "disaster (security fixes only)"
		}
		s.WriteString(progressStyle.Render(fmt.Sprintf("  %d/%d selected · mode: %s", count, len(m.projects), mode)))
		s.WriteString("\n\n")
		s.WriteString("  " + helpKey("u", "update") + "  " + helpKey("a", "toggle all") + "  " + helpKey("space", "toggle") + "  " + helpKey("m", "mode") + "  " + helpKey("q", "quit"))
		s.WriteString("\n  " + helpKey("S", "settings") + "  " + helpKey("type", "search") + "  " + helpKey("esc", "clear search"))
		s.WriteString("\n")
		if m.updateMsg != "" {
			s.WriteString("\n  " + progressStyle.Render(m.updateMsg) + "\n")
		} else if m.update != nil && m.update.CanInstall {
			s.WriteString("\n  " + completedStyle.Render(m.update.Version+" available") + "  " + helpKey("U", "update & restart") + "\n")
		} else if m.update != nil {
			s.WriteString("\n  " + completedStyle.Render(m.update.Version+" available: ") + progressStyle.Render(m.update.URL) + "\n")
		}

	case StateRunning:
		progress := fmt.Sprintf("%d/%d done", m.doneJobs, m.totalJobs)
		s.WriteString(subtitleStyle.Render("  Updating projects  ") + progressStyle.Render(progress) + "\n\n")

		visible := m.availableLines()
		end := m.scrollOffset + visible
		if end > len(m.selectedProjects) {
			end = len(m.selectedProjects)
		}

		for i := m.scrollOffset; i < end; i++ {
			pName := m.selectedProjects[i]
			st := m.status[pName]

			cursor := "  "
			if m.runCursor == i {
				cursor = cursorStyle.Render("▸ ")
			}

			var statusStr string
			switch st {
			case "running":
				statusStr = statusRunningStyle.Render("● RUNNING")
			case "success":
				statusStr = statusSuccessStyle.Render("✓ SUCCESS")
			case "failed":
				statusStr = statusFailedStyle.Render("✗ FAILED ")
			case "cancelled":
				statusStr = statusIdleStyle.Render("◌ CANCEL ")
			default:
				statusStr = statusIdleStyle.Render("○ IDLE   ")
			}

			name := lipgloss.NewStyle().Foreground(dimWhite).Render(pName)
			s.WriteString(fmt.Sprintf("  %s%s  %s\n", cursor, statusStr, name))

			if st == "running" || st == "failed" {
				lines := strings.Split(strings.TrimRight(m.logs[pName], "\n"), "\n")
				if len(lines) > 0 {
					last := lines[len(lines)-1]
					maxW := m.width - 10
					if maxW > 0 && len(last) > maxW {
						last = last[:maxW]
					}
					s.WriteString(logLineStyle.Render("└─ "+last) + "\n")
				}
			}
		}

		s.WriteString("\n")
		s.WriteString("  " + helpKey("enter", "detail") + "  " + helpKey("x", "cancel") + "  " + helpKey("X", "cancel all") + "  " + helpKey("j/k", "navigate") + "  " + helpKey("q", "quit"))
		s.WriteString("\n")

	case StateDetail:
		st := m.status[m.detailProject]
		var statusStr string
		switch st {
		case "running":
			statusStr = statusRunningStyle.Render("● RUNNING")
		case "success":
			statusStr = statusSuccessStyle.Render("✓ SUCCESS")
		case "failed":
			statusStr = statusFailedStyle.Render("✗ FAILED")
		case "cancelled":
			statusStr = statusIdleStyle.Render("◌ CANCELLED")
		default:
			statusStr = statusIdleStyle.Render("○ IDLE")
		}

		header := detailHeaderStyle.Render(m.detailProject) + "  " + statusStr
		s.WriteString("  " + header + "\n")
		s.WriteString("  " + lipgloss.NewStyle().Foreground(darkGray).Render(strings.Repeat("─", min(m.width-4, 60))) + "\n")
		s.WriteString(m.detailVP.View() + "\n")
		s.WriteString("  " + helpKey("esc", "back") + "  " + helpKey("↑/↓", "scroll"))
		s.WriteString("\n")

	case StateConfirmBranch:
		s.WriteString(subtitleStyle.Render("  Confirm branch names for successful projects:") + "\n\n")

		for i, pName := range m.selectedProjects {
			st := m.status[pName]

			cursor := "  "
			if m.branchCursor == i {
				cursor = cursorStyle.Render("▸ ")
			}

			var statusStr string
			switch st {
			case "success":
				statusStr = statusSuccessStyle.Render("✓")
			case "failed":
				statusStr = statusFailedStyle.Render("✗")
			case "cancelled":
				statusStr = statusIdleStyle.Render("◌")
			default:
				statusStr = statusIdleStyle.Render("○")
			}

			name := lipgloss.NewStyle().Foreground(dimWhite).Render(pName)

			if m.confirmDone {
				result := m.confirmResults[pName]
				if mr := m.mrStatus[pName]; mr != "" {
					style := progressStyle
					if strings.HasPrefix(mr, "✗") {
						style = statusFailedStyle
					} else if strings.HasPrefix(mr, "http") {
						style = statusSuccessStyle
					}
					if w := m.width - lipgloss.Width(pName) - lipgloss.Width(result) - 14; w > 10 && len([]rune(mr)) > w && !strings.HasPrefix(mr, "http") {
						mr = string([]rune(mr)[:w-1]) + "…" // one line per project; the full text is in the logs (d)
					}
					result += "  " + style.Render("MR: "+mr)
				}
				s.WriteString(fmt.Sprintf("  %s%s  %s  %s\n", cursor, statusStr, name,
					progressStyle.Render(result)))
			} else if st == "success" {
				branchDisplay := m.branchNames[pName]
				if m.editingBranch && m.branchCursor == i {
					s.WriteString(fmt.Sprintf("  %s%s  %s  %s\n", cursor, statusStr, name, m.branchInput.View()))
				} else {
					branchStyle := lipgloss.NewStyle().Foreground(cyan)
					hint := ""
					if m.branchCursor == i {
						hint = progressStyle.Render("  (enter to edit)")
					}
					s.WriteString(fmt.Sprintf("  %s%s  %s  %s%s\n", cursor, statusStr, name,
						branchStyle.Render(branchDisplay), hint))
				}
			} else {
				s.WriteString(fmt.Sprintf("  %s%s  %s  %s\n", cursor, statusStr, name,
					progressStyle.Render("cleanup will be performed")))
			}
		}

		s.WriteString("\n")
		if m.confirmDone {
			s.WriteString(completedStyle.Render("  ✓ Done.") + "\n\n")
			if m.merging > 0 {
				s.WriteString("  " + helpKey("x", "cancel MR") + "  " + helpKey("d", "view logs") + "  " + helpKey("j/k", "navigate") + "  " + progressStyle.Render(fmt.Sprintf("%d merge request(s) running", m.merging)))
			} else {
				s.WriteString("  " + helpKey("m", "create MR") + "  " + helpKey("M", "create all MRs") + "  " + helpKey("d", "view logs") + "  " + helpKey("j/k", "navigate") + "  " + helpKey("q", "quit"))
			}
		} else {
			s.WriteString("  " + helpKey("c", "confirm") + "  " + helpKey("r", "retry failed") + "  " + helpKey("enter", "edit branch") + "  " + helpKey("d", "view logs") + "  " + helpKey("j/k", "navigate") + "  " + helpKey("q", "quit & cleanup"))
		}
		s.WriteString("\n")
	}

	return s.String()
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
