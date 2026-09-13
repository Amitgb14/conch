package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/brain"
	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
)

// settings is the settings overlay: colour and prompt themes,
// notifications, and the agents installed on each machine. Changes apply
// at once and are saved to config.toml.
type settings struct {
	tab    int
	sel    int
	scroll int

	shell    *proto.ShellThemes // this computer's prompt themes
	shellErr string
}

var settingsTabs = []string{"Theme", "Notifications", "Agents", "Brain"}

type shellThemesMsg struct {
	themes proto.ShellThemes
	err    error
}

// settingItem is one line of a settings tab.
type settingItem struct {
	header bool
	label  string
	detail string // right-aligned
	on     *bool  // a toggle
	mark   bool   // the current choice in a list
	run    func(m *Model) tea.Cmd
}

func newSettings(m *Model) (*settings, tea.Cmd) {
	s := &settings{}
	c := m.clientOf(localMachine)
	if c == nil || len(c.MissingCapabilities([]string{"shell.omz.v1"})) > 0 {
		s.shellErr = "prompt themes need the local server to be restarted on this build"
		return s, nil
	}
	return s, func() tea.Msg {
		var th proto.ShellThemes
		err := callCtx(c, proto.MethodShellThemes, nil, &th)
		return shellThemesMsg{themes: th, err: err}
	}
}

// save writes the configuration in the background.
func saveConfig(cfg config.Config) tea.Cmd {
	return func() tea.Msg {
		if err := config.Save(cfg); err != nil {
			return errMsg{fmt.Errorf("saving settings: %w", err)}
		}
		return nil
	}
}

func (s *settings) items(m *Model) []settingItem {
	switch s.tab {
	case 0:
		return s.themeItems(m)
	case 1:
		return s.notifyItems(m)
	case 3:
		return s.brainItems(m)
	}
	return s.agentItems(m)
}

func (s *settings) themeItems(m *Model) []settingItem {
	items := []settingItem{{header: true, label: "Colours"}}
	current := themeByName(m.cfg.UI.Theme).name
	for _, t := range themes {
		t := t
		items = append(items, settingItem{label: t.label, detail: t.swatch(), mark: t.name == current,
			run: func(m *Model) tea.Cmd {
				m.cfg.UI.Theme = t.name
				applyTheme(m.cfg.UI.Theme, m.cfg.UI.Accent)
				return saveConfig(m.cfg)
			}})
	}
	if m.cfg.UI.Accent != "" {
		items = append(items, settingItem{label: "Use the theme's accent (now " + m.cfg.UI.Accent + ")",
			run: func(m *Model) tea.Cmd {
				m.cfg.UI.Accent = ""
				applyTheme(m.cfg.UI.Theme, "")
				return saveConfig(m.cfg)
			}})
	}

	items = append(items, settingItem{}, settingItem{header: true, label: "Shell prompt · Oh My Zsh theme for new zsh terminals"})
	switch {
	case s.shellErr != "":
		items = append(items, settingItem{label: s.shellErr})
	case s.shell == nil:
		items = append(items, settingItem{label: "loading…"})
	case !s.shell.OMZ:
		items = append(items, settingItem{label: "Oh My Zsh isn't installed on this computer (ohmyz.sh)"})
	default:
		own := "Keep my .zshrc theme"
		if s.shell.Current != "" {
			own += " (" + s.shell.Current + ")"
		}
		items = append(items, settingItem{label: own, mark: m.cfg.Shell.OMZTheme == "",
			run: func(m *Model) tea.Cmd { m.cfg.Shell.OMZTheme = ""; return saveConfig(m.cfg) }})
		for _, name := range s.shell.Themes {
			name := name
			items = append(items, settingItem{label: name, mark: m.cfg.Shell.OMZTheme == name,
				run: func(m *Model) tea.Cmd {
					m.cfg.Shell.OMZTheme = name
					m.setFlash("new zsh terminals use the "+name+" prompt", false)
					return saveConfig(m.cfg)
				}})
		}
	}
	return items
}

func (s *settings) notifyItems(m *Model) []settingItem {
	n := &m.cfg.Notify
	toggle := func(label, detail string, v *bool, after func(m *Model) tea.Cmd) settingItem {
		return settingItem{label: label, detail: detail, on: v, run: func(m *Model) tea.Cmd {
			*v = !*v
			cmd := saveConfig(m.cfg)
			if after != nil && *v {
				cmd = tea.Batch(cmd, after(m))
			}
			return cmd
		}}
	}
	return []settingItem{
		toggle("Notifications", "master switch", &n.Enabled, nil),
		{},
		{header: true, label: "How"},
		toggle("Desktop notification", "macOS / notify-send", &n.Desktop, nil),
		toggle("Sound", "a system sound", &n.Sound, func(*Model) tea.Cmd { return func() tea.Msg { playSound(); return nil } }),
		toggle("Terminal beep", "the bell character", &n.Bell, func(*Model) tea.Cmd {
			return func() tea.Msg { _, _ = fmt.Print("\a"); return nil }
		}),
		{},
		{header: true, label: "When"},
		toggle("An agent is waiting for you", "permissions, questions", &n.Waiting, nil),
		toggle("An agent finishes", "while you look elsewhere", &n.Done, nil),
		{},
		{label: "Send a test notification", run: func(m *Model) tea.Cmd {
			cfg := m.cfg.Notify
			if !cfg.Enabled {
				m.setFlash("notifications are off", true)
				return nil
			}
			return notify(cfg, "conch", "Notifications work")
		}},
	}
}

func (s *settings) agentItems(m *Model) []settingItem {
	items := []settingItem{{header: true, label: "Default agent", detail: "what c starts"}}
	for _, name := range knownAgents(m) {
		name := name
		items = append(items, settingItem{label: agentLabel(name), mark: m.defaultAgent() == name,
			run: func(m *Model) tea.Cmd {
				m.cfg.Agents.Default = name
				return saveConfig(m.cfg)
			}})
	}
	for _, mach := range m.machines {
		mach := mach
		state := ""
		if mach.state != stateOnline {
			state = mach.state.String()
		}
		items = append(items, settingItem{}, settingItem{header: true, label: mach.label, detail: state})
		switch {
		case mach.state != stateOnline:
			items = append(items, settingItem{label: styleMuted.Render("  agents unknown while " + mach.state.String())})
			continue
		case mach.available == nil:
			items = append(items, settingItem{label: styleMuted.Render("  agents unknown: the server there predates agent checks; upgrade it")})
			continue
		}
		for _, a := range mach.agentList {
			a := a
			item := settingItem{label: "  " + firstNonEmpty(a.Label, agentLabel(a.Name))}
			if a.Installed {
				item.detail = styleOK.Render("✓ " + a.Version)
				item.run = func(m *Model) tea.Cmd {
					m.setFlash(fmt.Sprintf("%s %s on %s at %s", item.label[2:], a.Version, mach.label, a.Path), false)
					return nil
				}
			} else {
				item.detail = styleWarn.Render("not installed · enter installs")
				item.run = func(m *Model) tea.Cmd {
					m.overlay = nil
					return m.installAgent(mach.id, a.Name)
				}
			}
			items = append(items, item)
		}
	}
	items = append(items, settingItem{},
		settingItem{label: "Check again", run: func(m *Model) tea.Cmd {
			var cmds []tea.Cmd
			for _, mach := range m.machines {
				cmds = append(cmds, mach.checkAgents())
			}
			m.setFlash("checking agents on every machine…", false)
			return tea.Batch(cmds...)
		}},
	)
	return items
}

func (s *settings) brainItems(m *Model) []settingItem {
	b := &m.cfg.Brain
	items := []settingItem{{header: true, label: "Provider", detail: "the model behind ✦ Ask and summaries"}}
	current := firstNonEmpty(b.Provider, "claude")
	for _, name := range brain.Providers {
		name := name
		cfg := *b
		cfg.Provider = name
		detail := styleOK.Render("✓ ready")
		if p, err := brain.New(cfg); err != nil {
			detail = styleErr.Render(err.Error())
		} else if err := p.Check(); err != nil {
			detail = styleWarn.Render(strings.TrimPrefix(err.Error(), brain.ErrNotConfigured.Error()+": "))
		}
		items = append(items, settingItem{label: brain.ProviderLabel(name), detail: ansi.Truncate(detail, 44, "…"), mark: current == name,
			run: func(m *Model) tea.Cmd {
				if m.cfg.Brain.Provider != name {
					m.cfg.Brain.Provider, m.cfg.Brain.Model, m.cfg.Brain.SummaryModel = name, "", ""
				}
				return saveConfig(m.cfg)
			}})
	}

	var models []string
	switch current {
	case "claude":
		models = []string{"sonnet", "opus", "haiku"}
	case "anthropic":
		models = []string{"claude-sonnet-5", "claude-opus-5", "claude-haiku-4-5"}
	}
	items = append(items, settingItem{}, settingItem{header: true, label: "Model", detail: "for planning"})
	if len(models) == 0 {
		model := firstNonEmpty(b.Model, "not set")
		items = append(items, settingItem{label: "  " + model + styleMuted.Render(" · set [brain] model and base_url in config.toml")})
	} else {
		items = append(items, settingItem{label: "Provider default (" + models[0] + ")", mark: b.Model == "",
			run: func(m *Model) tea.Cmd { m.cfg.Brain.Model = ""; return saveConfig(m.cfg) }})
		for _, name := range models {
			name := name
			items = append(items, settingItem{label: name, mark: b.Model == name,
				run: func(m *Model) tea.Cmd { m.cfg.Brain.Model = name; return saveConfig(m.cfg) }})
		}
	}

	items = append(items, settingItem{}, settingItem{header: true, label: "Summaries"},
		settingItem{label: "Summarise agents when they finish or need you", detail: "a small model request each", on: &b.Summaries,
			run: func(m *Model) tea.Cmd {
				m.cfg.Brain.Summaries = !m.cfg.Brain.Summaries
				return saveConfig(m.cfg)
			}},
		settingItem{label: styleMuted.Render("  S summarises the selected agent on demand · : opens ✦ Ask")},
		settingItem{label: styleMuted.Render("  Summaries send the agent's visible screen to the provider.")},
	)
	return items
}

// knownAgents lists agent names any connected machine reported, in order,
// falling back to the ones conch ships adapters for.
func knownAgents(m *Model) []string {
	seen := map[string]bool{}
	var names []string
	for _, mach := range m.machines {
		for _, a := range mach.agentList {
			if !seen[a.Name] {
				seen[a.Name] = true
				names = append(names, a.Name)
			}
		}
	}
	if len(names) == 0 {
		names = []string{"claude", "codex", "gemini", "opencode"}
	}
	return names
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func (s *settings) update(m *Model, msg tea.Msg) (bool, tea.Cmd) {
	switch msg := msg.(type) {
	case shellThemesMsg:
		if msg.err != nil {
			s.shellErr = msg.err.Error()
		} else {
			s.shell = &msg.themes
		}
		return false, nil
	case tea.KeyMsg:
		items := s.items(m)
		switch msg.String() {
		case "esc", "q", ",":
			m.overlay = nil
			return true, nil
		case "tab", "right", "l":
			s.setTab((s.tab + 1) % len(settingsTabs))
		case "shift+tab", "left", "h":
			s.setTab((s.tab + len(settingsTabs) - 1) % len(settingsTabs))
		case "1", "2", "3", "4":
			s.setTab(int(msg.String()[0] - '1'))
		case "up", "k":
			s.move(items, -1)
		case "down", "j":
			s.move(items, 1)
		case "pgup":
			s.move(items, -10)
		case "pgdown":
			s.move(items, 10)
		case "enter", " ":
			if s.sel < len(items) && items[s.sel].run != nil {
				return false, items[s.sel].run(m)
			}
		}
	}
	return false, nil
}

func (s *settings) setTab(t int) {
	s.tab, s.sel, s.scroll = t, 0, 0
}

// move steps the selection by delta, skipping lines that do nothing.
func (s *settings) move(items []settingItem, delta int) {
	if len(items) == 0 {
		return
	}
	step := 1
	if delta < 0 {
		step = -1
	}
	next := clamp(s.sel+delta, 0, len(items)-1)
	for next >= 0 && next < len(items) && items[next].run == nil {
		next += step
	}
	if next >= 0 && next < len(items) {
		s.sel = next
	}
}

func (m Model) settingsListHeight() int { return clamp(m.height-10, 6, 24) }

func (s *settings) render(m Model) box {
	w := clamp(78, 40, max(m.width-4, 40))
	items := s.items(&m)
	if s.sel >= len(items) || items[s.sel].run == nil {
		s.move(items, 0)
		if s.sel < len(items) && items[s.sel].run == nil {
			s.move(items, 1)
		}
	}
	listH := m.settingsListHeight()
	if s.sel < s.scroll {
		s.scroll = s.sel
	}
	if s.sel >= s.scroll+listH {
		s.scroll = s.sel - listH + 1
	}

	var tabs strings.Builder
	for i, name := range settingsTabs {
		label := fmt.Sprintf(" %d %s ", i+1, name)
		if i == s.tab {
			tabs.WriteString(styleSel.Render(label))
		} else {
			tabs.WriteString(styleMuted.Render(label))
		}
		tabs.WriteString(" ")
	}
	lines := []string{tabs.String(), ""}
	for i := s.scroll; i < s.scroll+listH; i++ {
		if i >= len(items) {
			lines = append(lines, "")
			continue
		}
		lines = append(lines, s.itemLine(items[i], i == s.sel, w))
	}
	if more := len(items) - (s.scroll + listH); more > 0 {
		lines[len(lines)-1] = styleMuted.Render(fmt.Sprintf("  … %d more", more))
	}
	lines = append(lines, "", styleMuted.Render(" tab switch · ↑↓ move · enter choose/toggle · esc close"),
		styleMuted.Render(" saved to "+ansi.Truncate(config.Dir()+"/config.toml", w-10, "…")))
	b := box{lines: frameLines(" ⚙ Settings ", lines, w, colorAccent)}
	b.x = max((m.width-b.width())/2, 0)
	b.y = max((m.height-len(b.lines))/4, 0)
	return b
}

func (s *settings) itemLine(it settingItem, selected bool, w int) string {
	if it.header {
		return spread(" "+styleBold.Render(it.label), styleMuted.Render(it.detail), w)
	}
	prefix := "   "
	switch {
	case it.on != nil && *it.on:
		prefix = " " + styleOK.Render("■") + " "
	case it.on != nil:
		prefix = " " + styleMuted.Render("□") + " "
	case it.mark:
		prefix = " " + styleAccent.Render("●") + " "
	case it.run != nil:
		prefix = " " + styleMuted.Render("○") + " "
	}
	if selected {
		plain := ansi.Strip(prefix) + it.label
		return styleSel.Render(spread(plain, ansi.Strip(it.detail), w))
	}
	return spread(prefix+it.label, it.detail, w)
}

func (s *settings) mouse(m *Model, msg tea.MouseMsg, b box) tea.Cmd {
	if !b.contains(msg.X, msg.Y) {
		if msg.Action == tea.MouseActionPress {
			m.overlay = nil
		}
		return nil
	}
	items := s.items(m)
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		s.move(items, -3)
		return nil
	case tea.MouseButtonWheelDown:
		s.move(items, 3)
		return nil
	}
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return nil
	}
	row := msg.Y - b.y - 1
	if row == 0 { // the tab bar
		x := msg.X - b.x - 1
		for i, name := range settingsTabs {
			width := len(fmt.Sprintf(" %d %s ", i+1, name)) + 1
			if x < width {
				s.setTab(i)
				return nil
			}
			x -= width
		}
		return nil
	}
	i := s.scroll + row - 2
	if row >= 2 && i < len(items) && items[i].run != nil {
		s.sel = i
		return items[i].run(m)
	}
	return nil
}
