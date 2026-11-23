package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"drupal-updater/config"
	"drupal-updater/discovery"
	"drupal-updater/executor"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Config sub-states
const (
	ConfigMenu = iota
	ConfigProjectList
	ConfigProjectEdit
	ConfigEditField
	ConfigNewProject
)

// field is one editable setting: the same settings, in the same groups, as the desktop Settings screen.
type field struct {
	section, label string
	get            func() string // value as edited ("" = inherit / default)
	set            func(string)
	show           func() string // value as displayed; nil = get
	options        []string      // enter cycles through these instead of editing
	multi          bool          // edited in a textarea, one item per line
	placeholder    string        // what an empty value means
}

type configModel struct {
	cfg        *config.Config
	configPath string

	state   int
	cursor  int
	message string

	input textinput.Model
	area  textarea.Model
	edit  *field // field being edited
	back  int    // state to return to after editing

	embedded bool // inside the main TUI: closing returns there instead of quitting

	// Project editing
	projectNames   []string
	projectCursor  int
	editingProject string
	fieldCursor    int
}

func NewConfigModel(cfg *config.Config, configPath string) *configModel {
	ti := textinput.New()
	ti.CharLimit = 512
	ti.Width = 60
	ti.PromptStyle = lipgloss.NewStyle().Foreground(cyan)
	ti.TextStyle = lipgloss.NewStyle().Foreground(dimWhite)
	ta := textarea.New()
	ta.SetWidth(90)
	ta.SetHeight(14)
	ta.CharLimit = 0
	ta.ShowLineNumbers = false

	if cfg.Projects == nil {
		cfg.Projects = make(map[string]config.ProjectConfig)
	}
	cm := &configModel{cfg: cfg, configPath: configPath, state: ConfigMenu, input: ti, area: ta}
	cm.refreshProjectNames()
	return cm
}

func (cm *configModel) refreshProjectNames() {
	cm.projectNames = nil
	for name := range cm.cfg.Projects {
		cm.projectNames = append(cm.projectNames, name)
	}
	slices.Sort(cm.projectNames)
}

func list(s string) []string { return splitList(strings.ReplaceAll(s, "\n", ",")) }

func orText(v, empty string) string {
	if v == "" {
		return empty
	}
	return v
}

// globalFields are the settings menu entries, in desktop order.
func (cm *configModel) globalFields() []field {
	c := cm.cfg
	d := &c.Defaults
	prompt, tools := executor.ClaudeDefaults()
	str := func(p *string) (func() string, func(string)) {
		return func() string { return *p }, func(v string) { *p = strings.TrimSpace(v) }
	}
	sl := func(p *[]string) (func() string, func(string)) {
		return func() string { return strings.Join(*p, ", ") }, func(v string) { *p = list(v) }
	}
	num := func(p *int, def int) (func() string, func(string)) {
		return func() string { return strconv.Itoa(orInt(*p, def)) }, func(v string) {
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
				*p = n
			}
		}
	}
	var fs []field
	add := func(section, label string, get func() string, set func(string), f field) {
		f.section, f.label, f.get, f.set = section, label, get, set
		fs = append(fs, f)
	}
	g, s := sl(&c.BaseFolders)
	add("Projects", "Base folders", g, s, field{placeholder: "scanned for projects"})
	g, s = num(&c.ScanDepth, 1)
	add("Projects", "Scan depth", g, s, field{placeholder: "levels below a base folder"})
	g, s = sl(&c.ProjectPaths)
	add("Projects", "Extra project paths", g, s, field{placeholder: "single projects"})
	g, s = sl(&c.ExcludedProjects)
	add("Projects", "Excluded projects", g, s, field{placeholder: "none; * wildcards"})
	g, s = num(&c.MaxConcurrent, 3)
	add("Projects", "Max concurrent runs", g, s, field{})
	g, s = str(&c.UpdateMode)
	add("Projects", "Update mode", func() string { return orText(c.UpdateMode, "standard") }, s, field{options: []string{"standard", "disaster"}})
	add("Projects", "Multisite", func() string { return orText(d.Multisite, "off") },
		func(v string) { d.Multisite = strings.TrimSuffix(v, "off") }, field{options: []string{"off", "on"}})

	g, s = str(&d.GitStartBranch)
	add("Branch defaults", "Start branch", g, s, field{placeholder: "master|main"})
	g, s = str(&d.GitNewBranchPattern)
	add("Branch defaults", "Final branch pattern", g, s, field{placeholder: config.DefaultBranchPattern})
	g, s = str(&d.GitMergeTarget)
	add("Branch defaults", "Merge request target", g, s, field{placeholder: "the start branch"})
	g, s = str(&d.GitCommitMessage)
	add("Branch defaults", "Commit message", g, s, field{placeholder: config.DefaultCommitMessage})

	g, s = str(&c.ClaudeModel)
	add("Claude", "Model", g, s, field{placeholder: "sonnet"})
	add("Claude", "Permission mode", func() string { return orText(c.ClaudePermissionMode, "dontAsk") },
		func(v string) { c.ClaudePermissionMode = v }, field{options: []string{"dontAsk", "acceptEdits", "plan", "manual"}})
	g, s = num(&c.ClaudeTimeoutMinutes, 60)
	add("Claude", "Timeout (minutes)", g, s, field{})
	add("Claude", "Allowed tools",
		func() string { return strings.Join(orList(c.ClaudeAllowedTools, tools), "\n") },
		func(v string) { // store the default as empty so built-in improvements still apply, as the desktop does
			if c.ClaudeAllowedTools = list(v); slices.Equal(c.ClaudeAllowedTools, tools) {
				c.ClaudeAllowedTools = nil
			}
		},
		field{multi: true, show: func() string { return strings.Join(orList(c.ClaudeAllowedTools, tools), ", ") }})
	add("Claude", "Prompt",
		func() string { return orText(c.ClaudePrompt, prompt) },
		func(v string) {
			if c.ClaudePrompt = v; strings.TrimSpace(v) == strings.TrimSpace(prompt) || strings.TrimSpace(v) == "" {
				c.ClaudePrompt = ""
			}
			c.ClaudePromptBase = ""
			if c.ClaudePrompt != "" {
				c.ClaudePromptBase = executor.PromptBase() // written against the built-in prompt shown here
			}
		},
		field{multi: true, show: func() string {
			if c.ClaudePrompt != "" {
				return "custom"
			}
			return "default"
		}})
	return fs
}

func orInt(v, d int) int {
	if v > 0 {
		return v
	}
	return d
}

func orList(v, d []string) []string {
	if len(v) > 0 {
		return v
	}
	return d
}

// projectFields edit the local projects: entry; display shows what a run uses.
func (cm *configModel) projectFields(name string) []field {
	pc := cm.cfg.Projects[name]
	save := func() { cm.cfg.Projects[name] = pc }
	str := func(label string, p *string, inherited string) field {
		return field{label: label, get: func() string { return *p }, set: func(v string) { *p = strings.TrimSpace(v); save() }, placeholder: inherited}
	}
	sl := func(label string, p *[]string) field {
		return field{label: label, get: func() string { return strings.Join(*p, ", ") }, set: func(v string) { *p = list(v); save() }, placeholder: "none"}
	}
	inh := cm.cfg.Defaults
	inh.MergeWithDefaults()
	return []field{
		str("Start branch", &pc.GitStartBranch, inh.GitStartBranch),
		str("Final branch pattern", &pc.GitNewBranchPattern, inh.GitNewBranchPattern),
		str("Merge request target", &pc.GitMergeTarget, orText(inh.GitMergeTarget, "the start branch")),
		str("Commit message", &pc.GitCommitMessage, inh.GitCommitMessage),
		sl("Ignored packages", &pc.IgnoredPackages),
		sl("Smoke-test paths", &pc.SmokePaths),
		{label: "Multisite", options: []string{"off", "on"}, // shows the defaults until set; stored only when it differs
			get: func() string { return orText(pc.Multisite, orText(inh.Multisite, "off")) },
			set: func(v string) {
				if pc.Multisite = v; v == orText(inh.Multisite, "off") {
					pc.Multisite = ""
				}
				save()
			}},
		{label: "Claude instructions", multi: true, placeholder: "none",
			get: func() string { return pc.ClaudeInstructions },
			set: func(v string) { pc.ClaudeInstructions = strings.TrimSpace(v); save() }},
	}
}

// projectPath finds a configured project's folder, if it is discovered.
func (cm *configModel) projectPath(name string) (string, bool) {
	projects, _ := discovery.DiscoverProjects(cm.cfg.BaseFolders, cm.cfg.ProjectPaths, cm.cfg.ExcludedProjects, cm.cfg.ScanDepth)
	p, ok := discovery.Find(projects, name)
	return p.Path, ok
}

// effective returns what a run uses for a project (repo drupal-updater.yaml over the local settings, then
// defaults), the local settings with defaults, and whether a repo file exists.
func (cm *configModel) effective(name string) (eff, local config.ProjectConfig, repoFile bool) {
	local = cm.cfg.Project(name)
	local.MergeWithDefaults()
	eff = local
	if path, ok := cm.projectPath(name); ok {
		eff = config.ForProject(cm.cfg.Project(name), path)
		_, err := os.Stat(filepath.Join(path, config.ProjectFile))
		repoFile = err == nil
	}
	return eff, local, repoFile
}

// settingsClosedMsg tells the main TUI that the embedded settings screen was closed.
type settingsClosedMsg struct{}

func (cm *configModel) close() tea.Cmd {
	if cm.embedded {
		return func() tea.Msg { return settingsClosedMsg{} }
	}
	return tea.Quit
}

func (cm *configModel) Init() tea.Cmd {
	return textinput.Blink
}

func (cm *configModel) startEdit(f field, back int) tea.Cmd {
	cm.message = ""
	if f.options != nil { // cycle
		i := slices.Index(f.options, f.get())
		f.set(f.options[(i+1)%len(f.options)])
		return nil
	}
	cm.edit, cm.back, cm.state = &f, back, ConfigEditField
	if f.multi {
		cm.area.SetValue(f.get())
		cm.area.Placeholder = f.placeholder
		return cm.area.Focus()
	}
	cm.input.SetValue(f.get())
	cm.input.Placeholder = f.placeholder
	cm.input.CursorEnd()
	return cm.input.Focus()
}

func (cm *configModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, isKey := msg.(tea.KeyMsg)
	if isKey && key.String() == "ctrl+c" {
		return cm, tea.Quit
	}

	switch cm.state {
	case ConfigEditField, ConfigNewProject:
		return cm.updateEdit(msg)
	}
	if !isKey {
		return cm, nil
	}

	switch cm.state {
	case ConfigMenu:
		fs := cm.globalFields()
		n := len(fs) + 2 // + manage projects, save & exit
		switch key.String() {
		case "q", "esc":
			return cm, cm.close()
		case "up", "k":
			cm.cursor = max(cm.cursor-1, 0)
		case "down", "j":
			cm.cursor = min(cm.cursor+1, n-1)
		case "enter":
			switch {
			case cm.cursor < len(fs):
				return cm, cm.startEdit(fs[cm.cursor], ConfigMenu)
			case cm.cursor == len(fs):
				cm.state, cm.projectCursor, cm.message = ConfigProjectList, 0, ""
			default:
				if err := config.SaveConfig(cm.configPath, cm.cfg); err != nil {
					cm.message = "Error: " + err.Error()
				} else {
					return cm, cm.close()
				}
			}
		}

	case ConfigProjectList:
		switch key.String() {
		case "esc":
			cm.state = ConfigMenu
		case "up", "k":
			cm.projectCursor = max(cm.projectCursor-1, 0)
		case "down", "j":
			cm.projectCursor = min(cm.projectCursor+1, len(cm.projectNames))
		case "enter":
			if cm.projectCursor < len(cm.projectNames) {
				cm.editingProject, cm.fieldCursor, cm.state, cm.message = cm.projectNames[cm.projectCursor], 0, ConfigProjectEdit, ""
			} else {
				cm.edit, cm.back, cm.state = nil, ConfigProjectList, ConfigNewProject
				cm.input.SetValue("")
				cm.input.Placeholder = "project name (folder name, as in the project list)"
				return cm, cm.input.Focus()
			}
		case "d":
			if cm.projectCursor < len(cm.projectNames) {
				name := cm.projectNames[cm.projectCursor]
				delete(cm.cfg.Projects, name)
				cm.refreshProjectNames()
				cm.projectCursor = min(cm.projectCursor, len(cm.projectNames))
				cm.message = "Deleted " + name
			}
		}

	case ConfigProjectEdit:
		fs := cm.projectFields(cm.editingProject)
		switch key.String() {
		case "esc":
			cm.state, cm.message = ConfigProjectList, ""
		case "up", "k":
			cm.fieldCursor = max(cm.fieldCursor-1, 0)
		case "down", "j":
			cm.fieldCursor = min(cm.fieldCursor+1, len(fs)-1)
		case "enter":
			return cm, cm.startEdit(fs[cm.fieldCursor], ConfigProjectEdit)
		case "w":
			cm.message = cm.saveToRepo(cm.editingProject)
		}
	}
	return cm, nil
}

// saveToRepo writes the project's local overrides to its drupal-updater.yaml, like the desktop "Save to repo".
func (cm *configModel) saveToRepo(name string) string {
	path, ok := cm.projectPath(name)
	if !ok {
		return "✗ project not found in the project folders"
	}
	if err := config.SaveProjectFile(path, cm.cfg.Projects[name]); err != nil {
		return "✗ " + err.Error()
	}
	return "✓ wrote " + filepath.Join(path, config.ProjectFile) + " — commit it"
}

func (cm *configModel) updateEdit(msg tea.Msg) (tea.Model, tea.Cmd) {
	multi := cm.edit != nil && cm.edit.multi
	if key, ok := msg.(tea.KeyMsg); ok {
		done := key.String() == "enter" && !multi || key.String() == "ctrl+s"
		if key.String() == "esc" {
			cm.state = cm.back
			cm.input.Blur()
			cm.area.Blur()
			return cm, nil
		}
		if done {
			val := cm.input.Value()
			if multi {
				val = cm.area.Value()
			}
			if cm.state == ConfigNewProject {
				if name := strings.TrimSpace(val); name != "" {
					if _, exists := cm.cfg.Projects[name]; !exists {
						cm.cfg.Projects[name] = config.ProjectConfig{} // empty = branch defaults
					}
					cm.refreshProjectNames()
					cm.editingProject, cm.fieldCursor, cm.back = name, 0, ConfigProjectEdit
				}
			} else {
				cm.edit.set(val)
				cm.message = "Updated " + cm.edit.label + " (save & exit to keep)"
			}
			cm.state = cm.back
			cm.input.Blur()
			cm.area.Blur()
			return cm, nil
		}
	}
	var cmd tea.Cmd
	if multi {
		cm.area, cmd = cm.area.Update(msg)
	} else {
		cm.input, cmd = cm.input.Update(msg)
	}
	return cm, cmd
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

func shown(f field) string {
	v := f.get()
	if f.show != nil {
		v = f.show()
	}
	if f.multi && f.show == nil && strings.Contains(v, "\n") {
		v = strings.SplitN(v, "\n", 2)[0] + " …"
	}
	if v == "" {
		return helpStyle.Render(f.placeholder)
	}
	return lipgloss.NewStyle().Foreground(dimWhite).Render(v)
}

func (cm *configModel) View() string {
	var s strings.Builder
	s.WriteString("\n" + titleStyle.Render("  ✦ Drupal Updater — Settings") + "\n\n")

	switch cm.state {
	case ConfigMenu:
		fs := cm.globalFields()
		section := ""
		for i, f := range fs {
			if f.section != section {
				section = f.section
				s.WriteString("\n" + subtitleStyle.Render("  "+section) + "\n")
			}
			writeLine(&s, f.label+": "+shown(f), i == cm.cursor)
		}
		s.WriteString("\n")
		writeLine(&s, fmt.Sprintf("Project overrides (%d) →", len(cm.cfg.Projects)), cm.cursor == len(fs))
		writeLine(&s, "Save & exit", cm.cursor == len(fs)+1)
		cm.footer(&s, helpKey("enter", "edit / toggle")+"  "+helpKey("j/k", "navigate")+"  "+helpKey("esc/q", "quit without saving"))

	case ConfigProjectList:
		s.WriteString(subtitleStyle.Render("  Project overrides") + "  " + helpStyle.Render("local; a committed "+config.ProjectFile+" wins per field") + "\n\n")
		writeList(&s, append(append([]string(nil), cm.projectNames...), "+ Add project"), cm.projectCursor)
		cm.footer(&s, helpKey("enter", "edit")+"  "+helpKey("d", "delete")+"  "+helpKey("esc", "back"))

	case ConfigProjectEdit:
		eff, local, repoFile := cm.effective(cm.editingProject) // show what a run would use
		s.WriteString(subtitleStyle.Render("  Project: "+cm.editingProject) + "\n")
		if repoFile {
			s.WriteString("  " + progressStyle.Render(config.ProjectFile+" in the repo wins per field; you edit the local values.") + "\n")
		}
		s.WriteString("\n")
		effVals := []string{eff.GitStartBranch, eff.GitNewBranchPattern, orText(eff.GitMergeTarget, "the start branch"), eff.GitCommitMessage,
			strings.Join(eff.IgnoredPackages, ", "), strings.Join(eff.SmokePaths, ", "), orText(eff.Multisite, "off"), firstLine(eff.ClaudeInstructions)}
		locVals := []string{local.GitStartBranch, local.GitNewBranchPattern, orText(local.GitMergeTarget, "the start branch"), local.GitCommitMessage,
			strings.Join(local.IgnoredPackages, ", "), strings.Join(local.SmokePaths, ", "), orText(local.Multisite, "off"), firstLine(local.ClaudeInstructions)}
		for i, f := range cm.projectFields(cm.editingProject) {
			v := lipgloss.NewStyle().Foreground(dimWhite).Render(orText(effVals[i], "(none)"))
			switch {
			case effVals[i] != locVals[i]:
				v += progressStyle.Render("  (repo file; local: " + orText(locVals[i], "(none)") + ")")
			case f.get() == "":
				v += helpStyle.Render("  (inherited)")
			}
			writeLine(&s, f.label+": "+v, i == cm.fieldCursor)
		}
		cm.footer(&s, helpKey("enter", "edit")+"  "+helpKey("w", "save to repo")+"  "+helpKey("esc", "back"))

	case ConfigEditField, ConfigNewProject:
		label := "New project"
		if cm.edit != nil {
			label = cm.edit.label
		}
		s.WriteString(subtitleStyle.Render("  "+label+":") + "\n\n")
		if cm.edit != nil && cm.edit.multi {
			s.WriteString(cm.area.View() + "\n\n  " + helpKey("ctrl+s", "save") + "  " + helpKey("esc", "cancel"))
		} else {
			s.WriteString("  " + cm.input.View() + "\n\n  " + helpKey("enter", "save") + "  " + helpKey("esc", "cancel"))
		}
		s.WriteString("\n")
	}
	return s.String()
}

func firstLine(v string) string {
	if before, _, ok := strings.Cut(v, "\n"); ok {
		return before + " …"
	}
	return v
}

func writeLine(s *strings.Builder, item string, selected bool) {
	marker := "  "
	if selected {
		marker = cursorStyle.Render("▸ ")
	}
	s.WriteString("  " + marker + item + "\n")
}

func (cm *configModel) footer(s *strings.Builder, keys string) {
	if cm.message != "" {
		s.WriteString("\n  " + progressStyle.Render(cm.message))
	}
	s.WriteString("\n\n  " + keys + "\n")
}
