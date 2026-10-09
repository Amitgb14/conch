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

	// Agents tab: the default agent to choose, and a row per machine
	// saying how it stands — its agents are a page of their own.
	details, labels := map[string]bool{}, map[string]bool{}
	for _, it := range s.agentItems(m) {
		details[ansi.Strip(it.detail)], labels[ansi.Strip(it.label)] = true, true
		if it.label == "Codex" && it.run != nil {
			it.run(m)
		}
	}
	if !labels["local"] || !details["1 installed · 1 not"] || !details["offline"] {
		t.Fatalf("machine rows: %v %v", details, labels)
	}
	// And that page has the versions and what is missing.
	s.openPage("agents:" + localMachine)
	rows := map[string]string{}
	for _, it := range s.agentItems(m) {
		rows[ansi.Strip(it.label)] = ansi.Strip(it.detail)
	}
	if rows["Claude Code"] != "✓ 2.1.270" || rows["Codex"] != "not installed · enter installs" ||
		rows["‹ Agents"] != "esc" || rows["Check again"] != "on local" {
		t.Fatalf("local's page: %v", rows)
	}
	// An offline machine says so there rather than listing nothing.
	s.openPage("agents:gpu")
	off := map[string]bool{}
	for _, it := range s.agentItems(m) {
		off[ansi.Strip(it.label)] = true
	}
	if !off["  agents unknown while offline"] {
		t.Fatalf("an offline machine's page: %v", off)
	}
	// A machine that has gone while its page was open falls back to the tab.
	s.openPage("agents:nope")
	if s.agentItems(m); s.page != "" {
		t.Fatalf("a machine that is not there left %q open", s.page)
	}
	s.openPage("")
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
	if _, ok := m.overlay.(*help); !ok {
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
	// What can be started comes first, then what would have to be
	// installed, so the numbers land on the agents being chosen between.
	want := "1 Start Claude Code | 2 Start OpenCode  1.18  default | 3 Install Codex  not installed | n Open a terminal instead"
	if got != want || mu.sel != 1 {
		t.Fatalf("picker:\n got %q (sel %d)\nwant %q", got, mu.sel, want)
	}
	if !strings.Contains(mu.title, "devbox") {
		t.Fatalf("title %q should name the machine", mu.title)
	}
}

func TestThemePromptSamples(t *testing.T) {
	t.Setenv("CONCH_HOME", t.TempDir())
	defer applyTheme("conch", "")
	m := &Model{cfg: config.Default(), width: 100, height: 40}
	long := strings.Repeat("prompt ", 20)
	s := &settings{shell: &proto.ShellThemes{OMZ: true, Current: "robbyrussell",
		Themes: []string{"agnoster", "plain", "quiet", "long", "titled"},
		Samples: map[string]string{
			"agnoster": "\x1b[34muser\x1b[0m@host ~",
			"plain":    "plain>",
			"long":     long,
			"titled":   "\x1b]0;a window title\x07ok>",
		}}}
	rows, raw := map[string]string{}, map[string]string{}
	for _, it := range s.themeItems(m) {
		rows[ansi.Strip(it.label)] = ansi.Strip(it.detail)
		raw[ansi.Strip(it.label)] = it.detail
	}
	if rows["plain"] != "plain>" || rows["agnoster"] != "user@host ~" {
		t.Fatalf("samples on the rows: %q %q", rows["plain"], rows["agnoster"])
	}
	// A theme nobody could expand keeps its row and says nothing.
	if rows["quiet"] != "" {
		t.Fatalf("a theme with no sample: %q", rows["quiet"])
	}
	// The colours are kept, and closed off so none reaches the frame.
	if !strings.Contains(raw["agnoster"], "\x1b[34m") || !strings.HasSuffix(raw["agnoster"], "\x1b[0m") {
		t.Fatalf("agnoster's colours: %q", raw["agnoster"])
	}
	// A long prompt is cut, and an escape that would retitle the terminal is gone.
	if w := ansi.StringWidth(rows["long"]); w != promptSampleMax || !strings.HasSuffix(rows["long"], "…") {
		t.Fatalf("a long prompt: %d cells, %q", w, rows["long"])
	}
	if rows["titled"] != "ok>" || strings.Contains(raw["titled"], "\x1b]") {
		t.Fatalf("an OSC in a prompt: %q", raw["titled"])
	}

	// Whatever a theme prints, the box keeps its width — in a tiny window too.
	for _, size := range [][2]int{{100, 40}, {60, 16}, {40, 12}} {
		m.width, m.height = size[0], size[1]
		a2CheckBox(t, s.render(*m), *m)
	}
}

func TestOnlyColour(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"", ""},
		{"plain>", "plain>"},
		{"\x1b[31mred\x1b[0m", "\x1b[31mred\x1b[0m"},
		{"\x1b[2Jclear", " clear"},      // a sequence that isn't a colour
		{"\x1b]0;title\x07ok", " ok"},   // OSC ended by BEL
		{"\x1b]0;title\x1b\\ok", " ok"}, // OSC ended by ST
		{"\x1b(Bok", " ok"},             // a two-byte escape
		{"a\x01b\tc\nd", "a b c d"},     // control characters
		{"\x1b[", " "},                  // a sequence cut short
		{"\x1b]0;never ends", " "},      // an OSC cut short
		{"\x1b", " "},                   // an escape at the very end
		{"\x1b[38;5;41mcolour\x1b[39m", "\x1b[38;5;41mcolour\x1b[39m"},
	} {
		if got := onlyColour(c.in); got != c.want {
			t.Errorf("onlyColour(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := promptSample("  \x1b[31m\x1b[0m  "); got != "" {
		t.Errorf("a prompt that shows nothing: %q", got)
	}
}
