package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
)

func TestSettingsTabs(t *testing.T) {
	t.Setenv("CONCH_HOME", t.TempDir())
	defer applyTheme("conch", "")
	m := &Model{cfg: config.Default(), width: 100, height: 40}
	claude := proto.AgentAvailability{Name: "claude", Label: "Claude Code", Installed: true, Version: "2.1.270"}
	codex := proto.AgentAvailability{Name: "codex", Label: "Codex"}
	m.machines = []*machine{
		{id: localMachine, label: "local", state: stateOnline, agentList: []proto.AgentAvailability{claude, codex},
			available: map[string]proto.AgentAvailability{"claude": claude, "codex": codex}},
		{id: "box", label: "box", state: stateOnline, agentList: []proto.AgentAvailability{}, available: map[string]proto.AgentAvailability{}},
		{id: "gpu", label: "gpu", state: stateOffline},
	}
	s := &settings{shell: &proto.ShellThemes{OMZ: true, Current: "robbyrussell", Themes: []string{"agnoster", "robbyrussell"}}}

	// Theme tab: choosing Nord applies it and marks it.
	var nord *settingItem
	for _, it := range s.themeItems(m) {
		if it.label == "Nord" {
			nord = &it
		}
	}
	nord.run(m)
	if m.cfg.UI.Theme != "nord" || colorAccent != themeByName("nord").accent {
		t.Fatalf("theme not applied: %q %v", m.cfg.UI.Theme, colorAccent)
	}
	for _, it := range s.themeItems(m) {
		if it.label == "agnoster" {
			it.run(m)
		}
	}
	if m.cfg.Shell.OMZTheme != "agnoster" {
		t.Fatalf("omz theme: %q", m.cfg.Shell.OMZTheme)
	}

	// Notifications tab: the master switch toggles.
	s.setTab(1)
	s.render(*m) // settles the selection on the first actionable row
	s.update(m, tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")})
	if m.cfg.Notify.Enabled {
		t.Fatal("notifications still enabled")
	}

	// Agents tab: default choice, installed, missing (installable) and offline rows.
	details, labels := map[string]bool{}, map[string]bool{}
	for _, it := range s.agentItems(m) {
		details[it.detail], labels[it.label] = true, true
		if it.label == "Codex" && it.run != nil {
			it.run(m)
		}
	}
	if !details[styleOK.Render("✓ 2.1.270")] || !details[styleWarn.Render("not installed · enter installs")] ||
		!labels[styleMuted.Render("  agents unknown while offline")] {
		t.Fatalf("agent rows: %v %v", details, labels)
	}
	if m.cfg.Agents.Default != "codex" || m.defaultAgent() != "codex" {
		t.Fatalf("default agent: %q", m.cfg.Agents.Default)
	}
	// What each agent loads is not settings, but this is where people look
	// for it, so the tab says where it is instead of pretending it is here.
	flat := func() string {
		var b strings.Builder
		for _, it := range s.agentItems(m) {
			b.WriteString(ansi.Strip(it.label) + "|" + ansi.Strip(it.detail) + "\n")
		}
		return b.String()
	}
	out := flat()
	for _, want := range []string{"What each agent loads|your own setup, and each checkout's",
		"i on a project, branch or pane", "Your own setup…|~/.claude and the rest, given to the other agents"} {
		if !strings.Contains(out, want) {
			t.Fatalf("the agent setup rows lack %q:\n%s", want, out)
		}
	}
	// One row, not one per agent: the tab is long enough in a small window.
	if strings.Contains(out, "Give the others") {
		t.Fatalf("the tab lists an action per agent:\n%s", out)
	}
	// The line about a checkout points and does nothing.
	for _, it := range s.agentItems(m) {
		if strings.Contains(ansi.Strip(it.label), "i on a project") && it.run != nil {
			t.Fatal("the pointer row does something")
		}
	}

	// Opening it is a page of its own: an agent to take the setup from,
	// the way back, and the undo — and nothing else from the tab.
	item := func(label string) settingItem {
		t.Helper()
		for _, it := range s.agentItems(m) {
			if strings.HasPrefix(ansi.Strip(it.label), label) {
				return it
			}
		}
		t.Fatalf("no row %q in\n%s", label, flat())
		return settingItem{}
	}
	item("Your own setup…").run(m)
	if s.page != "usersync" {
		t.Fatalf("it opened %q", s.page)
	}
	page := flat()
	for _, want := range []string{"Your own setup|what follows you, not a checkout's", "‹ Agents|esc",
		"Give the others Claude Code's setup…", "Put the last one back…|undoes the last one",
		"~/.claude/CLAUDE.md and its skills"} {
		if !strings.Contains(page, want) {
			t.Fatalf("the page lacks %q:\n%s", want, page)
		}
	}
	if strings.Contains(page, "Default agent") || strings.Contains(page, "Remote machines") {
		t.Fatalf("the page kept the tab's own rows:\n%s", page)
	}
	// esc goes back to the tab, and so does the ‹ row.
	if closed, _ := s.update(m, a2Key("esc")); closed || s.page != "" {
		t.Fatalf("esc in a page: closed %v page %q", closed, s.page)
	}
	item("Your own setup…").run(m)
	item("‹ Agents").run(m)
	if s.page != "" {
		t.Fatalf("the back row left %q open", s.page)
	}
	// Switching tab and coming back starts at the tab, not the page.
	s.openPage("usersync")
	s.setTab(0)
	s.setTab(2)
	if s.page != "" {
		t.Fatalf("a tab switch kept %q open", s.page)
	}

	// In a small window it still fits: the page is what it is for.
	for _, size := range [][2]int{{80, 20}, {60, 16}, {40, 12}} {
		m.width, m.height = size[0], size[1]
		s.openPage("usersync")
		b := s.render(*m)
		a2CheckBox(t, b, *m)
		if got := strings.Count(a2Plain(b.lines), "\n") + 1; got > size[1] {
			t.Fatalf("%dx%d: the page is %d lines", size[0], size[1], got)
		}
		s.openPage("")
		a2CheckBox(t, s.render(*m), *m)
	}
}

func TestStatusBarClicks(t *testing.T) {
	t.Setenv("CONCH_HOME", t.TempDir())
	m := Model{cfg: config.Default(), width: 140, height: 40, expanded: map[string]bool{}, showAll: map[string]bool{}}
	m.machines = []*machine{{id: localMachine, label: "local", state: stateOnline, agents: map[string]bool{}, sizes: map[string][2]int{}}}
	m.rebuild()

	line, hits := m.layoutStatus()
	plain := ansi.Strip(line)
	if ansi.StringWidth(line) != m.width || !strings.Contains(plain, "⚙ Settings") {
		t.Fatalf("bar %q (width %d)", plain, ansi.StringWidth(line))
	}
	// Every hit covers the text it acts for.
	find := func(label string) statusHit {
		i := strings.Index(plain, label)
		if i < 0 {
			t.Fatalf("%q not on the bar: %q", label, plain)
		}
		col := ansi.StringWidth(plain[:i])
		for _, h := range hits {
			if col >= h.x0 && col < h.x1 {
				return h
			}
		}
		t.Fatalf("%q at column %d is not clickable", label, col)
		return statusHit{}
	}
	settingsHit := find("⚙ Settings")
	settingsHit.act(&m)
	if _, ok := m.overlay.(*settings); !ok {
		t.Fatalf("Settings button opened %T", m.overlay)
	}
	m.overlay = nil
	find("keys").act(&m)
	if _, ok := m.overlay.(help); !ok {
		t.Fatalf("keys hint opened %T", m.overlay)
	}

	// Narrow terminals keep the button, drop hints that don't fit, and stay exactly as wide.
	m.overlay, m.width = nil, 60
	line, _ = m.layoutStatus()
	if ansi.StringWidth(line) != 60 || !strings.Contains(ansi.Strip(line), "⚙") {
		t.Fatalf("narrow bar %q", ansi.Strip(line))
	}
}

func TestAgentPicker(t *testing.T) {
	m := &Model{cfg: config.Default(), width: 100, height: 40}
	m.cfg.Agents.Default = "opencode"
	list := []proto.AgentAvailability{{Name: "claude", Installed: true}, {Name: "codex"}, {Name: "opencode", Installed: true, Version: "1.18"}}
	m.machines = []*machine{{id: "devbox", label: "devbox", state: stateOnline, agentList: list}}
	m.rows = []row{{id: "machine:devbox", kind: kindMachine, machine: "devbox"}}
	m.cursor = "machine:devbox"
	mu := newAgentMenu(*m, m.machines[0])
	var labels []string
	for _, it := range mu.items {
		labels = append(labels, it.key+" "+ansi.Strip(it.label))
	}
	got := strings.Join(labels, " | ")
	want := "1 Start Claude Code | 2 Install Codex  not installed | 3 Start OpenCode  1.18  default | n Open a terminal instead"
	if got != want || mu.sel != 2 {
		t.Fatalf("picker:\n got %q (sel %d)\nwant %q", got, mu.sel, want)
	}
	if !strings.Contains(mu.title, "devbox") {
		t.Fatalf("title %q should name the machine", mu.title)
	}
}
