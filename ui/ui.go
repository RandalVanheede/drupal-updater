package ui

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"drupal-updater/config"
	"drupal-updater/discovery"
	"drupal-updater/executor"
	"drupal-updater/logging"

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
)

type model struct {
	cfg      *config.Config
	projects []discovery.Project
	selected map[string]bool
	status   map[string]string
	logs     map[string]string

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
}

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

// sanitizeBranchName lowercases and replaces non-alphanumeric (except -) with dashes.
func sanitizeBranchName(s string) string {
	s = strings.ToLower(s)
	re := regexp.MustCompile(`[^a-z0-9-]+`)
	s = re.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	return s
}

func New(cfg *config.Config, projects []discovery.Project) *model {
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
		state:          StateList,
		branchInput:    bi,
		msgChan:        make(chan executor.StatusUpdate, 100),
		width:          80,
		height:         24,
		branchNames:    make(map[string]string),
		tempBranches:   make(map[string]string),
		confirmResults: make(map[string]string),
	}

	for _, p := range projects {
		m.status[p.Name] = "idle"
		m.selected[p.Name] = false
	}

	return m
}

func (m *model) Init() tea.Cmd {
	return textinput.Blink
}

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
				m.detailVP.SetContent(m.logs[m.detailProject])
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
		m.status[msg.ProjectName] = msg.Status
		if msg.Output != "" {
			m.logs[msg.ProjectName] += msg.Command + "\n" + msg.Output + "\n"
		} else {
			m.logs[msg.ProjectName] += "$ " + msg.Command + "\n"
		}
		if msg.Status == "success" || msg.Status == "failed" || msg.Status == "cancelled" {
			m.doneJobs++
		}
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
		m.status[msg.ProjectName] = msg.Status
		if msg.Output != "" {
			m.logs[msg.ProjectName] += msg.Command + "\n" + msg.Output + "\n"
		} else {
			m.logs[msg.ProjectName] += "$ " + msg.Command + "\n"
		}
		if msg.Status == "success" || msg.Status == "failed" || msg.Status == "cancelled" {
			m.doneJobs++
		}
		if msg.ProjectName == m.detailProject {
			m.detailVP.SetContent(m.logs[m.detailProject])
			m.detailVP.GotoBottom()
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
			if !m.confirmDone {
				// Cleanup failed/cancelled projects before quitting
				m.runConfirmation(false)
			}
			return m, []tea.Cmd{tea.Quit}, nil
		case "up", "k":
			if m.branchCursor > 0 {
				m.branchCursor--
			}
		case "down", "j":
			if m.branchCursor < len(m.selectedProjects)-1 {
				m.branchCursor++
			}
		case "d", "enter":
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
					m.detailVP.SetContent(m.logs[pName])
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

		p := m.projectByName(pName)
		if p == nil {
			continue
		}

		pCfg := m.cfg.Projects[pName]
		pCfg.MergeWithDefaults(m.cfg)

		tempBranch := m.tempBranches[pName]
		startBranch := pCfg.GitStartBranch

		commands := executor.BuildCommands(pCfg.Commands, startBranch, tempBranch)

		ctx, cancel := context.WithCancel(parentCtx)
		m.cancels[pName] = cancel
		pName := pName
		cmds := commands
		path := p.Path
		go func() {
			sem <- struct{}{}
			executor.Run(ctx, pName, path, startBranch, tempBranch, cmds, m.msgChan)
			<-sem
		}()
	}

	return waitForActivity(m.msgChan)
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
		pCfg := m.cfg.Projects[pName]
		pCfg.MergeWithDefaults(m.cfg)

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
		commands    []string
	}

	var jobs []job
	for _, p := range m.projects {
		if !m.selected[p.Name] {
			continue
		}
		m.totalJobs++

		pCfg := m.cfg.Projects[p.Name]
		pCfg.MergeWithDefaults(m.cfg)

		// Build temp branch name
		sanitized := sanitizeBranchName(p.Name)
		tempBranch := fmt.Sprintf("drupal-updater/temp-%s", sanitized)
		m.tempBranches[p.Name] = tempBranch

		// Pre-fill final branch name from pattern (replace * with sanitized project name)
		finalBranch := strings.Replace(pCfg.GitNewBranchPattern, "*", sanitized, 1)
		m.branchNames[p.Name] = finalBranch

		logging.Info("[%s] temp branch: %s, final branch: %s", p.Name, tempBranch, finalBranch)

		commands := executor.BuildCommands(pCfg.Commands, pCfg.GitStartBranch, tempBranch)

		jobs = append(jobs, job{name: p.Name, path: p.Path, startBranch: pCfg.GitStartBranch, tempBranch: tempBranch, commands: commands})
	}

	sem := make(chan struct{}, m.cfg.MaxConcurrent)
	for _, j := range jobs {
		j := j
		ctx, cancel := context.WithCancel(parentCtx)
		m.cancels[j.name] = cancel
		go func() {
			sem <- struct{}{}
			executor.Run(ctx, j.name, j.path, j.startBranch, j.tempBranch, j.commands, m.msgChan)
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
	s.WriteString(titleStyle.Render("  ◆ Drupal Updater"))
	s.WriteString("\n\n")

	switch m.state {
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
		s.WriteString(progressStyle.Render(fmt.Sprintf("  %d/%d selected", count, len(m.projects))))
		s.WriteString("\n\n")
		s.WriteString("  " + helpKey("u", "update") + "  " + helpKey("a", "toggle all") + "  " + helpKey("space", "toggle") + "  " + helpKey("q", "quit"))
		s.WriteString("\n  " + helpKey("type", "search") + "  " + helpKey("esc", "clear search") + "  " + helpKey("hint:", "") + progressStyle.Render("--config to manage settings"))
		s.WriteString("\n")

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
			s.WriteString("  " + helpKey("q", "quit"))
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
