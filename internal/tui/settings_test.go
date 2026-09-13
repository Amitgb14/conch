package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/amitghadge/conch/internal/config"
	"github.com/amitghadge/conch/internal/proto"
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
