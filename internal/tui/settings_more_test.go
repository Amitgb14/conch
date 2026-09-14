package tui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
)

func a2Item(t *testing.T, items []settingItem, label string) settingItem {
	t.Helper()
	for _, it := range items {
		if ansi.Strip(it.label) == label {
			return it
		}
	}
	var labels []string
	for _, it := range items {
		labels = append(labels, ansi.Strip(it.label))
	}
	t.Fatalf("no %q among %q", label, labels)
	return settingItem{}
}

func TestA2NewSettingsAndSave(t *testing.T) {
	a2Isolate(t)
	m := a2Model()
	s, cmd := newSettings(m)
	if cmd != nil || !strings.Contains(s.shellErr, "restarted on this build") {
		t.Fatalf("no local server: %q", s.shellErr)
	}
	m.machines[0].c = a2Client("shell.omz.v1")
	// The command asks the server, so it is only built.
	if s, cmd := newSettings(m); cmd == nil || s.shellErr != "" {
		t.Fatal("prompt themes load from a capable server")
	}
	s.update(m, shellThemesMsg{err: errors.New("zsh missing")})
	if s.shellErr != "zsh missing" {
		t.Fatalf("themes error %q", s.shellErr)
	}
	s.update(m, shellThemesMsg{themes: proto.ShellThemes{OMZ: true}})
	if s.shell == nil || !s.shell.OMZ {
		t.Fatal("themes reply")
	}

	cfg := config.Default()
	cfg.UI.Theme = "nord"
	if msg := saveConfig(cfg)(); msg != nil {
		t.Fatalf("save: %#v", msg)
	}
	if _, err := os.Stat(filepath.Join(config.Dir(), "config.toml")); err != nil || !strings.HasPrefix(config.Dir(), os.Getenv("CONCH_HOME")) {
		t.Fatalf("saved to %s: %v", config.Dir(), err)
	}
	// An unwritable directory reports the failure.
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONCH_HOME", filepath.Join(blocker, "sub"))
	if msg, ok := saveConfig(cfg)().(errMsg); !ok || !strings.Contains(msg.err.Error(), "saving settings") {
		t.Fatalf("save error: %#v", msg)
	}
}

func TestA2SettingsThemeShellStates(t *testing.T) {
	a2Isolate(t)
	defer applyTheme("conch", "")
	m := a2Model()
	s := &settings{shellErr: "no server"}
	a2Item(t, s.themeItems(m), "no server")
	s.shellErr = ""
	a2Item(t, s.themeItems(m), "loading…")
	s.shell = &proto.ShellThemes{}
	a2Item(t, s.themeItems(m), "Oh My Zsh isn't installed on this computer (ohmyz.sh)")
	s.shell = &proto.ShellThemes{OMZ: true, Themes: []string{"ys"}}
	m.cfg.Shell.OMZTheme = "ys"
	a2Item(t, s.themeItems(m), "ys").run(m)
	if m.flash != "new zsh terminals use the ys prompt" {
		t.Fatalf("flash %q", m.flash)
	}
	keep := a2Item(t, s.themeItems(m), "Keep my .zshrc theme")
	if keep.mark {
		t.Fatal("keep is not the choice while a theme is set")
	}
	keep.run(m)
	if m.cfg.Shell.OMZTheme != "" {
		t.Fatal("keep my theme")
	}

	m.cfg.UI.Accent = "#ff0000"
	a2Item(t, s.themeItems(m), "Use the theme's accent (now #ff0000)").run(m)
	if m.cfg.UI.Accent != "" {
		t.Fatal("accent reset")
	}
}

func TestA2SettingsNotifications(t *testing.T) {
	a2Isolate(t)
	m := a2Model()
	s := &settings{tab: 1}
	items := s.items(m)
	a2Item(t, items, "Off")
	if !a2Item(t, items, "Off").mark {
		t.Fatal("quiet hours off by default")
	}
	a2Item(t, items, "22:00 – 08:00").run(m)
	if m.cfg.Notify.QuietStart != "22:00" || m.cfg.Notify.QuietEnd != "08:00" {
		t.Fatal("quiet preset")
	}
	m.cfg.Notify.QuietStart, m.cfg.Notify.QuietEnd = "01:00", "02:00"
	if it := a2Item(t, s.items(m), "01:00 – 02:00 (config.toml)"); !it.mark || it.run != nil {
		t.Fatal("custom quiet hours are shown, not chosen")
	}
	if quietPreset("01:00", "02:00") || !quietPreset("", "") {
		t.Fatal("quietPreset")
	}

	a2Item(t, s.items(m), "Snooze alerts for 1 hour").run(m)
	if time.Until(m.snoozeUntil) < 59*time.Minute || !strings.HasPrefix(m.flash, "alerts snoozed until") {
		t.Fatalf("snooze: %q", m.flash)
	}
	resume := a2Item(t, s.items(m), "Resume alerts")
	if !strings.HasPrefix(resume.detail, "snoozed until") {
		t.Fatalf("resume detail %q", resume.detail)
	}
	resume.run(m)
	if !m.snoozeUntil.IsZero() {
		t.Fatal("resume")
	}

	m.cfg.Notify.Enabled = false
	test := a2Item(t, s.items(m), "Send a test notification")
	if test.run(m) != nil || m.flash != "notifications are off" {
		t.Fatalf("test while off: %q", m.flash)
	}
	m.cfg.Notify.Enabled, m.cfg.Notify.Bell = true, true
	if test.run(m) == nil { // would ring; not run
		t.Fatal("test notification")
	}
	// Toggles flip their setting; turning a sound on also plays it (not run).
	desktop := a2Item(t, s.items(m), "Desktop notification")
	was := m.cfg.Notify.Desktop
	desktop.run(m)
	if m.cfg.Notify.Desktop == was {
		t.Fatal("desktop toggle")
	}
	m.cfg.Notify.Sound = false
	if a2Item(t, s.items(m), "Sound").run(m) == nil || !m.cfg.Notify.Sound {
		t.Fatal("sound toggle")
	}
}

func TestA2SettingsAgentsAndBrain(t *testing.T) {
	a2Isolate(t)
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	m := a2Model()
	claude := proto.AgentAvailability{Name: "claude", Label: "Claude Code", Installed: true, Version: "2.0", Path: "/bin/claude"}
	codex := proto.AgentAvailability{Name: "codex"}
	m.machines[0].agentList = []proto.AgentAvailability{claude, codex}
	m.machines[0].available = map[string]proto.AgentAvailability{"claude": claude, "codex": codex}
	m.machines = append(m.machines, &machine{id: "old", label: "old", state: stateOnline})
	s := &settings{tab: 2}
	items := s.items(m)
	a2Item(t, items, "  agents unknown: the server there predates agent checks; upgrade it")
	a2Item(t, items, "  Claude Code").run(m)
	if m.flash != "Claude Code 2.0 on local at /bin/claude" {
		t.Fatalf("installed agent: %q", m.flash)
	}
	m.overlay = s
	if msg := a2ErrText(a2Run(a2Item(t, items, "  Codex").run(m))); msg != "local is online" || m.overlay != nil {
		t.Fatalf("install offline: %q", msg)
	}
	if a2Item(t, items, "Check again").run(m); m.flash != "checking agents on every machine…" {
		t.Fatalf("check again: %q", m.flash)
	}
	m.machines[0].agentList = nil
	if got := strings.Join(knownAgents(m), ","); got != "claude,codex,gemini,opencode" {
		t.Fatalf("known agents fallback %q", got)
	}
	if firstNonEmpty() != "" || firstNonEmpty("", "b") != "b" {
		t.Fatal("firstNonEmpty")
	}

	// Brain: providers, models per provider, summaries.
	s.setTab(3)
	items = s.items(m)
	anthropic := a2Item(t, items, "Anthropic API (ANTHROPIC_API_KEY)")
	if !strings.Contains(ansi.Strip(anthropic.detail), "set $ANTHROPIC_API_KEY") {
		t.Fatalf("anthropic detail %q", ansi.Strip(anthropic.detail))
	}
	a2Item(t, items, "Provider default (sonnet)")
	a2Item(t, items, "opus").run(m)
	if m.cfg.Brain.Model != "opus" {
		t.Fatal("claude model")
	}
	a2Item(t, s.items(m), "Provider default (sonnet)").run(m)
	if m.cfg.Brain.Model != "" {
		t.Fatal("provider default model")
	}
	m.cfg.Brain.Model = "opus"
	anthropic.run(m)
	if m.cfg.Brain.Provider != "anthropic" || m.cfg.Brain.Model != "" {
		t.Fatalf("switching provider resets the model: %+v", m.cfg.Brain)
	}
	m.cfg.Brain.Model = "claude-opus-5"
	a2Item(t, s.items(m), "Anthropic API (ANTHROPIC_API_KEY)").run(m)
	if m.cfg.Brain.Model != "claude-opus-5" {
		t.Fatal("choosing the same provider keeps the model")
	}
	a2Item(t, s.items(m), "claude-haiku-4-5")
	m.cfg.Brain.Provider = "openai"
	m.cfg.Brain.Model = ""
	a2Item(t, s.items(m), "  not set · set [brain] model and base_url in config.toml")
	a2Item(t, s.items(m), "Summarise agents when they finish or need you").run(m)
	if !m.cfg.Brain.Summaries {
		t.Fatal("summaries toggle")
	}
	m.cfg.Brain.Provider = "bogus"
	if detail := ansi.Strip(a2Item(t, s.items(m), "Claude Code CLI (your Claude login)").detail); detail == "" {
		t.Fatal("provider rows always explain their state")
	}
}

func TestA2SettingsKeysRenderMouse(t *testing.T) {
	a2Isolate(t)
	defer applyTheme("conch", "")
	m := a2Model()
	m.height = 20 // list height 10: the theme tab scrolls
	s := &settings{shell: &proto.ShellThemes{OMZ: true}}
	for i := 0; i < 10; i++ {
		s.shell.Themes = append(s.shell.Themes, fmt.Sprint("theme", i))
	}
	m.overlay = s

	for _, step := range []struct {
		key string
		tab int
	}{{"tab", 1}, {"right", 2}, {"l", 3}, {"tab", 0}, {"shift+tab", 3}, {"left", 2}, {"h", 1}, {"4", 3}, {"1", 0}} {
		s.sel = 3
		s.update(m, a2Key(step.key))
		if s.tab != step.tab || s.sel != 0 {
			t.Fatalf("after %s tab %d sel %d", step.key, s.tab, s.sel)
		}
	}
	items := s.items(m)
	s.render(*m) // settles on the first choice
	if s.sel != 1 || items[s.sel].run == nil {
		t.Fatalf("render settles on an item: %d", s.sel)
	}
	s.update(m, a2Key("up"))
	if s.sel != 1 {
		t.Fatal("up at the first item stays")
	}
	s.update(m, a2Key("down"))
	s.update(m, a2Key("j"))
	if s.sel != 3 {
		t.Fatalf("down: %d", s.sel)
	}
	s.update(m, a2Key("k"))
	s.update(m, a2Key("pgdown"))
	if s.sel != 12 {
		t.Fatalf("pgdown: %d", s.sel)
	}
	b := s.render(*m)
	a2CheckBox(t, b, *m)
	if s.scroll == 0 || !strings.Contains(a2Plain(b.lines), "more") {
		t.Fatalf("scrolled render (scroll %d):\n%s", s.scroll, a2Plain(b.lines))
	}
	s.update(m, a2Key("pgup"))
	s.render(*m)
	if s.sel != 2 || s.scroll != 2 {
		t.Fatalf("pgup: sel %d scroll %d", s.sel, s.scroll)
	}
	// Enter chooses.
	s.update(m, a2Key("enter"))
	if m.cfg.UI.Theme != themes[1].name {
		t.Fatalf("enter chose %q", m.cfg.UI.Theme)
	}
	s.move(nil, 1)

	// Mouse: wheel, tab bar and items.
	b = s.render(*m)
	s.mouse(m, tea.MouseMsg{X: b.x + 2, Y: b.y + 4, Button: tea.MouseButtonWheelDown}, b)
	if s.sel != 5 {
		t.Fatalf("wheel down: %d", s.sel)
	}
	s.mouse(m, tea.MouseMsg{X: b.x + 2, Y: b.y + 4, Button: tea.MouseButtonWheelUp}, b)
	if s.sel != 2 {
		t.Fatalf("wheel up: %d", s.sel)
	}
	s.mouse(m, tea.MouseMsg{X: b.x + 2, Y: b.y + 4, Action: tea.MouseActionMotion}, b)
	x := b.x + 1 + len(" 1 Theme ") + 1 + 1
	s.mouse(m, tea.MouseMsg{X: x, Y: b.y + 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}, b)
	if s.tab != 1 {
		t.Fatalf("tab click: %d", s.tab)
	}
	s.mouse(m, tea.MouseMsg{X: b.x + b.width() - 2, Y: b.y + 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}, b)
	if s.tab != 1 {
		t.Fatal("a click past the tabs")
	}
	s.setTab(0)
	b = s.render(*m)
	s.mouse(m, tea.MouseMsg{X: b.x + 3, Y: b.y + 1 + 2 + 3, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}, b)
	if s.sel != 3 || m.cfg.UI.Theme != themes[2].name {
		t.Fatalf("item click: sel %d theme %q", s.sel, m.cfg.UI.Theme)
	}
	s.mouse(m, tea.MouseMsg{X: b.x + 3, Y: b.y + 1 + 2, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}, b)
	if s.sel != 3 {
		t.Fatal("clicking a header does nothing")
	}
	s.mouse(m, tea.MouseMsg{X: 0, Y: 0, Action: tea.MouseActionMotion}, b)
	if m.overlay == nil {
		t.Fatal("motion outside")
	}
	s.mouse(m, tea.MouseMsg{X: 0, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}, b)
	if m.overlay != nil {
		t.Fatal("a click outside closes")
	}
	for _, k := range []string{"esc", "q", ","} {
		m.overlay = s
		if closed, _ := s.update(m, a2Key(k)); !closed || m.overlay != nil {
			t.Fatalf("%s closes", k)
		}
	}
	if closed, _ := s.update(m, tickMsg{}); closed {
		t.Fatal("tick")
	}
}

func TestA2SettingsMoveReachesListEdges(t *testing.T) {
	m := a2Model()
	s := &settings{shellErr: "no server"} // Theme tab: header, 6 themes, blank, header, message
	items := s.themeItems(m)
	s.sel = 2
	s.move(items, 10) // pgdown
	if s.sel != 6 {
		t.Errorf("pgdown near the end should reach the last theme, sel %d", s.sel)
	}
	s.sel = 2
	s.move(items, -3) // wheel up
	if s.sel != 1 {
		t.Errorf("wheel up near the top should reach the first theme, sel %d", s.sel)
	}
}
