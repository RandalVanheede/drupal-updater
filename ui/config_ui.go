package ui

import (
	"fmt"
	"strconv"
	"strings"

	"drupal-updater/config"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Config sub-states
const (
	ConfigMenu = iota
	ConfigEditBaseFolder
	ConfigEditMaxConcurrent
	ConfigProjectList
	ConfigProjectEdit
	ConfigProjectEditField
	ConfigEditDefaultCommands
)

type configModel struct {
	cfg        *config.Config
	configPath string

	state   int
	cursor  int
	message string

	input   textinput.Model
	ta      textarea.Model
	usingTA bool // true when textarea is active instead of textinput

	// Project editing
	projectNames   []string
	projectCursor  int
	editingProject string
	fieldCursor    int
	editingField   string
}

func NewConfigModel(cfg *config.Config, configPath string) *configModel {
	ti := textinput.New()
	ti.Focus()
	ti.CharLimit = 256
	ti.Width = 50
	ti.PromptStyle = lipgloss.NewStyle().Foreground(cyan)
	ti.TextStyle = lipgloss.NewStyle().Foreground(dimWhite)

	ta := textarea.New()
	ta.Placeholder = "One command per line..."
	ta.ShowLineNumbers = false
	ta.SetWidth(60)
	ta.SetHeight(10)
	ta.CharLimit = 4096

	cm := &configModel{
		cfg:        cfg,
		configPath: configPath,
		state:      ConfigMenu,
		input:      ti,
		ta:         ta,
	}
	cm.refreshProjectNames()
	return cm
}

func (cm *configModel) refreshProjectNames() {
	cm.projectNames = nil
	for name := range cm.cfg.Projects {
		cm.projectNames = append(cm.projectNames, name)
	}
}

func (cm *configModel) Init() tea.Cmd {
	return textinput.Blink
}

func (cm *configModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return cm, tea.Quit
		}
	}

	switch cm.state {
	case ConfigMenu:
		return cm.updateMenu(msg)
	case ConfigEditBaseFolder:
		return cm.updateEditField(msg, func(val string) {
			cm.cfg.BaseFolder = val
		})
	case ConfigEditMaxConcurrent:
		return cm.updateEditField(msg, func(val string) {
			if n, err := strconv.Atoi(val); err == nil && n > 0 {
				cm.cfg.MaxConcurrent = n
			}
		})
	case ConfigEditDefaultCommands:
		return cm.updateTextarea(msg, ConfigMenu, func(val string) {
			if val == "" {
				cm.cfg.DefaultCommands = nil
			} else {
				parts := strings.Split(val, "\n")
				cm.cfg.DefaultCommands = nil
				for _, p := range parts {
					p = strings.TrimSpace(p)
					if p != "" {
						cm.cfg.DefaultCommands = append(cm.cfg.DefaultCommands, p)
					}
				}
			}
		})
	case ConfigProjectList:
		return cm.updateProjectList(msg)
	case ConfigProjectEdit:
		return cm.updateProjectEdit(msg)
	case ConfigProjectEditField:
		if cm.usingTA {
			return cm.updateTextarea(msg, ConfigProjectEdit, func(val string) {
				pc := cm.cfg.Projects[cm.editingProject]
				if val == "" {
					pc.Commands = nil
				} else {
					parts := strings.Split(val, "\n")
					pc.Commands = nil
					for _, p := range parts {
						p = strings.TrimSpace(p)
						if p != "" {
							pc.Commands = append(pc.Commands, p)
						}
					}
				}
				cm.cfg.Projects[cm.editingProject] = pc
			})
		}
		cm.input, cmd = cm.input.Update(msg)
		switch msg := msg.(type) {
		case tea.KeyMsg:
			switch msg.String() {
			case "enter":
				cm.saveProjectField()
				cm.state = ConfigProjectEdit
			case "esc":
				cm.state = ConfigProjectEdit
			}
		}
		return cm, cmd
	}

	return cm, nil
}

func (cm *configModel) updateTextarea(msg tea.Msg, backState int, apply func(string)) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "ctrl+s":
			apply(cm.ta.Value())
			cm.usingTA = false
			cm.message = "Updated"
			cm.state = backState
			return cm, nil
		}
	}
	cm.ta, cmd = cm.ta.Update(msg)
	return cm, cmd
}

func (cm *configModel) updateMenu(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc":
			return cm, tea.Quit
		case "up", "k":
			if cm.cursor > 0 {
				cm.cursor--
			}
		case "down", "j":
			if cm.cursor < 4 {
				cm.cursor++
			}
		case "enter":
			switch cm.cursor {
			case 0: // Edit base folder
				cm.input.SetValue(cm.cfg.BaseFolder)
				cm.input.CursorEnd()
				cm.state = ConfigEditBaseFolder
			case 1: // Edit max concurrent
				cm.input.SetValue(strconv.Itoa(cm.cfg.MaxConcurrent))
				cm.input.CursorEnd()
				cm.state = ConfigEditMaxConcurrent
			case 2: // Edit default commands
				cm.ta.SetValue(strings.Join(cm.cfg.DefaultCommands, "\n"))
				cm.ta.Focus()
				cm.usingTA = true
				cm.state = ConfigEditDefaultCommands
			case 3: // Manage projects
				cm.state = ConfigProjectList
				cm.projectCursor = 0
			case 4: // Save & exit
				err := config.SaveConfig(cm.configPath, cm.cfg)
				if err != nil {
					cm.message = "Error: " + err.Error()
				} else {
					cm.message = "Saved!"
					return cm, tea.Quit
				}
			}
		}
	}
	return cm, nil
}

func (cm *configModel) updateEditField(msg tea.Msg, apply func(string)) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "enter":
			apply(cm.input.Value())
			cm.message = "Updated"
			cm.state = ConfigMenu
			return cm, nil
		case "esc":
			cm.state = ConfigMenu
			return cm, nil
		}
	}
	cm.input, cmd = cm.input.Update(msg)
	return cm, cmd
}

func (cm *configModel) updateProjectList(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			cm.state = ConfigMenu
		case "up", "k":
			if cm.projectCursor > 0 {
				cm.projectCursor--
			}
		case "down", "j":
			if cm.projectCursor < len(cm.projectNames) {
				cm.projectCursor++
			}
		case "enter":
			if cm.projectCursor < len(cm.projectNames) {
				// Edit existing project
				cm.editingProject = cm.projectNames[cm.projectCursor]
				cm.fieldCursor = 0
				cm.state = ConfigProjectEdit
			} else {
				// Add new project (last item)
				cm.input.SetValue("")
				cm.input.Placeholder = "project-name"
				cm.editingField = "new_project"
				cm.state = ConfigProjectEditField
			}
		case "d":
			if cm.projectCursor < len(cm.projectNames) {
				name := cm.projectNames[cm.projectCursor]
				delete(cm.cfg.Projects, name)
				cm.refreshProjectNames()
				if cm.projectCursor >= len(cm.projectNames) && cm.projectCursor > 0 {
					cm.projectCursor--
				}
				cm.message = "Deleted " + name
			}
		}
	}
	return cm, nil
}

func (cm *configModel) updateProjectEdit(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			cm.state = ConfigProjectList
		case "up", "k":
			if cm.fieldCursor > 0 {
				cm.fieldCursor--
			}
		case "down", "j":
			if cm.fieldCursor < 4 {
				cm.fieldCursor++
			}
		case "enter":
			pc := cm.cfg.Projects[cm.editingProject]
			pc.MergeWithDefaults(cm.cfg)
			switch cm.fieldCursor {
			case 0:
				cm.input.SetValue(pc.GitStartBranch)
				cm.editingField = "git_start_branch"
				cm.input.CursorEnd()
				cm.usingTA = false
				cm.state = ConfigProjectEditField
			case 1:
				cm.input.SetValue(pc.GitNewBranchPattern)
				cm.editingField = "git_new_branch_pattern"
				cm.input.CursorEnd()
				cm.usingTA = false
				cm.state = ConfigProjectEditField
			case 2:
				cm.input.SetValue(strings.Join(pc.IgnoredPackages, ", "))
				cm.editingField = "ignored_packages"
				cm.input.CursorEnd()
				cm.usingTA = false
				cm.state = ConfigProjectEditField
			case 3:
				cm.input.SetValue(strings.Join(pc.SmokePaths, ", "))
				cm.editingField = "smoke_paths"
				cm.input.CursorEnd()
				cm.usingTA = false
				cm.state = ConfigProjectEditField
			case 4:
				cm.ta.SetValue(strings.Join(pc.Commands, "\n"))
				cm.ta.Focus()
				cm.usingTA = true
				cm.editingField = "commands"
				cm.state = ConfigProjectEditField
			}
		}
	}
	return cm, nil
}

// splitList turns "a, b,,c" into [a b c]; nil when empty.
func splitList(val string) []string {
	var out []string
	for _, p := range strings.Split(val, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (cm *configModel) saveProjectField() {
	val := cm.input.Value()

	if cm.editingField == "new_project" {
		if val != "" {
			if cm.cfg.Projects == nil {
				cm.cfg.Projects = make(map[string]config.ProjectConfig)
			}
			cm.cfg.Projects[val] = config.DefaultProjectConfig()
			cm.refreshProjectNames()
			cm.editingProject = val
			cm.fieldCursor = 0
			cm.state = ConfigProjectEdit
		}
		return
	}

	pc := cm.cfg.Projects[cm.editingProject]
	switch cm.editingField {
	case "git_start_branch":
		pc.GitStartBranch = val
	case "git_new_branch_pattern":
		pc.GitNewBranchPattern = val
	case "ignored_packages":
		pc.IgnoredPackages = splitList(val)
	case "smoke_paths":
		pc.SmokePaths = splitList(val)
	case "commands":
		if val == "" {
			pc.Commands = nil
		} else {
			parts := strings.Split(val, "\n")
			pc.Commands = nil
			for _, p := range parts {
				p = strings.TrimSpace(p)
				if p != "" {
					pc.Commands = append(pc.Commands, p)
				}
			}
		}
	}
	cm.cfg.Projects[cm.editingProject] = pc
}

// writeList renders items with the cursor marker and highlight on the item at cursor.
func writeList(s *strings.Builder, items []string, cursor int) {
	for i, item := range items {
		marker, color := "  ", dimWhite
		if i == cursor {
			marker, color = cursorStyle.Render("▸ "), cyan
		}
		s.WriteString(fmt.Sprintf("  %s%s\n", marker, lipgloss.NewStyle().Foreground(color).Render(item)))
	}
}

func (cm *configModel) View() string {
	var s strings.Builder

	s.WriteString("\n")
	s.WriteString(titleStyle.Render("  ◆ Drupal Updater — Configuration"))
	s.WriteString("\n\n")

	switch cm.state {
	case ConfigMenu:
		items := []string{
			fmt.Sprintf("Base folder: %s", lipgloss.NewStyle().Foreground(dimWhite).Render(cm.cfg.BaseFolder)),
			fmt.Sprintf("Max concurrent: %s", lipgloss.NewStyle().Foreground(dimWhite).Render(strconv.Itoa(cm.cfg.MaxConcurrent))),
			fmt.Sprintf("Default commands: %s", lipgloss.NewStyle().Foreground(dimWhite).Render(fmt.Sprintf("(%d commands)", len(cm.cfg.DefaultCommands)))),
			fmt.Sprintf("Manage projects (%d configured)", len(cm.cfg.Projects)),
			"Save & exit",
		}
		writeList(&s, items, cm.cursor)
		s.WriteString("\n  " + helpStyle.Render("Claude mode/model/prompt/tools: use the desktop app Settings or edit ~/.config/drupal-updater/config.yaml"))
		if cm.message != "" {
			s.WriteString("\n  " + progressStyle.Render(cm.message))
		}
		s.WriteString("\n\n")
		s.WriteString("  " + helpKey("enter", "edit") + "  " + helpKey("esc/q", "quit without saving"))
		s.WriteString("\n")

	case ConfigProjectList:
		s.WriteString(subtitleStyle.Render("  Projects:") + "\n\n")
		writeList(&s, append(append([]string(nil), cm.projectNames...), "+ Add project"), cm.projectCursor)
		if cm.message != "" {
			s.WriteString("\n  " + progressStyle.Render(cm.message))
		}
		s.WriteString("\n\n  " + helpKey("enter", "edit") + "  " + helpKey("d", "delete") + "  " + helpKey("esc", "back"))
		s.WriteString("\n")

	case ConfigProjectEdit:
		pc := cm.cfg.Projects[cm.editingProject]
		pc.MergeWithDefaults(cm.cfg) // show what a run would use
		value := func(v string) string { return lipgloss.NewStyle().Foreground(dimWhite).Render(v) }
		orNone := func(l []string) string {
			if len(l) == 0 {
				return "(none)"
			}
			return strings.Join(l, ", ")
		}
		// order must match the fieldCursor cases in updateProjectEdit
		items := []string{
			"Start branch: " + value(pc.GitStartBranch),
			"New branch pattern: " + value(pc.GitNewBranchPattern),
			"Ignored packages: " + value(orNone(pc.IgnoredPackages)),
			"Smoke-test paths: " + value(orNone(pc.SmokePaths)),
			"Commands (classic mode): " + value(fmt.Sprintf("(%d commands)", len(pc.Commands))),
		}
		s.WriteString(subtitleStyle.Render("  Project: "+cm.editingProject) + "\n\n")
		writeList(&s, items, cm.fieldCursor)
		s.WriteString("\n  " + helpKey("enter", "edit") + "  " + helpKey("esc", "back"))
		s.WriteString("\n")

	case ConfigEditBaseFolder:
		s.WriteString(subtitleStyle.Render("  Base folder:") + "\n\n")
		s.WriteString("  " + cm.input.View() + "\n\n")
		s.WriteString("  " + helpKey("enter", "save") + "  " + helpKey("esc", "cancel"))
		s.WriteString("\n")

	case ConfigEditMaxConcurrent:
		s.WriteString(subtitleStyle.Render("  Max concurrent jobs:") + "\n\n")
		s.WriteString("  " + cm.input.View() + "\n\n")
		s.WriteString("  " + helpKey("enter", "save") + "  " + helpKey("esc", "cancel"))
		s.WriteString("\n")

	case ConfigEditDefaultCommands:
		s.WriteString(subtitleStyle.Render("  Default commands (one per line):") + "\n\n")
		s.WriteString(cm.ta.View() + "\n\n")
		s.WriteString("  " + helpKey("esc/ctrl+s", "save & back"))
		s.WriteString("\n")

	case ConfigProjectEditField:
		if cm.usingTA {
			s.WriteString(subtitleStyle.Render("  Commands (one per line):") + "\n\n")
			s.WriteString(cm.ta.View() + "\n\n")
			s.WriteString("  " + helpKey("esc/ctrl+s", "save & back"))
			s.WriteString("\n")
		} else {
			label := cm.editingField
			if label == "new_project" {
				label = "New project name"
			}
			s.WriteString(subtitleStyle.Render("  "+label+":") + "\n\n")
			s.WriteString("  " + cm.input.View() + "\n\n")
			s.WriteString("  " + helpKey("enter", "save") + "  " + helpKey("esc", "cancel"))
			s.WriteString("\n")
		}
	}

	return s.String()
}
