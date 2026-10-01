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
	"github.com/Amitgb14/conch/internal/sandbox"
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
	// Each machine is a row on the tab; its agents are a page of its own.
	items := s.items(m)
	a2Item(t, items, "old")
	s.openPage("agents:old")
	a2Item(t, s.items(m), "  agents unknown: the server there predates agent checks; upgrade it")

	s.openPage("agents:" + localMachine)
	items = s.items(m)
	a2Item(t, items, "Claude Code").run(m)
	if m.flash != "Claude Code 2.0 on local at /bin/claude" {
		t.Fatalf("installed agent: %q", m.flash)
	}
	m.overlay = s
	if msg := a2ErrText(a2Run(a2Item(t, items, "Codex").run(m))); msg != "local is online" || m.overlay != nil {
		t.Fatalf("install offline: %q", msg)
	}
	if a2Item(t, items, "Check again").run(m); m.flash != "checking agents on local…" {
		t.Fatalf("check again: %q", m.flash)
	}
	s.openPage("")
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
	}{{"tab", 1}, {"right", 2}, {"l", 3}, {"tab", 4}, {"tab", 5}, {"tab", 0}, {"shift+tab", 5}, {"left", 4}, {"h", 3}, {"4", 3}, {"5", 4}, {"6", 5}, {"1", 0}} {
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
	// A page back, with the selection on screen: how far it scrolled
	// depends on how tall the list is in this window.
	if s.sel != 2 || s.scroll > s.sel || s.sel >= s.scroll+m.settingsListHeight() {
		t.Fatalf("pgup: sel %d scroll %d of %d rows", s.sel, s.scroll, m.settingsListHeight())
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
	s := &settings{shellErr: "no server"} // Theme tab: header, themes, blank, header, message
	items := s.themeItems(m)
	s.sel = len(themes) - 4
	s.move(items, 10) // pgdown
	// Past the themes are the tree toggle and the shell prompt block; the
	// cursor lands on something selectable, never a header or a blank.
	if s.sel < len(themes) || items[s.sel].run == nil {
		t.Errorf("pgdown near the end landed on %d: %+v", s.sel, items[s.sel])
	}
	last := s.sel
	s.move(items, 10)
	if s.sel < last || items[s.sel].run == nil {
		t.Errorf("pgdown at the end landed on %d: %+v", s.sel, items[s.sel])
	}
	s.sel = 2
	s.move(items, -3) // wheel up
	if s.sel != 1 {
		t.Errorf("wheel up near the top should reach the first theme, sel %d", s.sel)
	}
}

func TestSettingsSilenceSteps(t *testing.T) {
	a2Isolate(t)
	m := a2Model()
	s := &settings{tab: 1}
	it := a2Item(t, s.items(m), "A watched pane goes quiet after")
	if !strings.HasPrefix(it.detail, "30s") || !strings.Contains(it.detail, "ctrl+b M") {
		t.Fatalf("detail %q", it.detail)
	}
	var seen []int
	for i := 0; i < len(silencePresets); i++ {
		a2Item(t, s.items(m), "A watched pane goes quiet after").run(m)
		seen = append(seen, m.cfg.Notify.Silence)
	}
	if fmt.Sprint(seen) != "[60 120 300 10 30]" {
		t.Fatalf("steps %v", seen)
	}
	// A value set by hand between presets moves to the next one up.
	for cur, want := range map[int]int{45: 60, 0: 10, -1: 10, 301: 10, 1000: 10} {
		if got := nextSilence(cur); got != want {
			t.Fatalf("nextSilence(%d) = %d, want %d", cur, got, want)
		}
	}
}

func TestSettingsFileIcons(t *testing.T) {
	a2Isolate(t)
	m := a2Model()
	s := &settings{shellErr: "no server"}
	if !a2Item(t, s.themeItems(m), "Letters, in any font").mark {
		t.Fatal("letters are the default")
	}
	a2Item(t, s.themeItems(m), "Nerd Font glyphs").run(m)
	if m.cfg.UI.Icons != iconsNerd || !a2Item(t, s.themeItems(m), "Nerd Font glyphs").mark {
		t.Fatalf("icons %q", m.cfg.UI.Icons)
	}
	a2Item(t, s.themeItems(m), "None").run(m)
	if m.cfg.UI.Icons != iconsOff {
		t.Fatalf("icons %q", m.cfg.UI.Icons)
	}
	if got := ansi.Strip(iconSample(iconsText)); got != "go ts tx rd" {
		t.Fatalf("sample %q", got)
	}
	if ansi.Strip(iconSample(iconsOff)) != "names only" {
		t.Fatal("off sample")
	}
}

// The Sandboxes tab: what a new sandbox is made from, and what of your
// environment goes into it. The key itself is never among the settings —
// it is read from the environment every time.
func TestSettingsSandboxTab(t *testing.T) {
	m, _ := sandboxModel(t)
	s := &settings{}
	s.setTab(sandboxTab)
	if settingsTabs[s.tab] != "Sandboxes" {
		t.Fatalf("tabs %v", settingsTabs)
	}
	plain := func() string {
		var b strings.Builder
		for _, it := range s.items(m) {
			b.WriteString(ansi.Strip(it.label) + "|" + ansi.Strip(it.detail) + "\n")
		}
		return b.String()
	}
	// The tab lists the providers rather than every provider's settings at
	// once, each saying whether it is ready to be used.
	list := plain()
	for _, want := range []string{"Daytona|no key · $DAYTONA_API_KEY is not set", "boat.dev|no key · $BOAT_API_KEY is not set",
		"Enter opens a provider"} {
		if !strings.Contains(list, want) {
			t.Fatalf("the list lacks %q in\n%s", want, list)
		}
	}
	if strings.Contains(list, "API key variable") {
		t.Fatalf("a provider's settings are on the list:\n%s", list)
	}
	// Opening one shows its own page, with the way back at the top.
	item := func(label string) settingItem {
		t.Helper()
		for _, it := range s.items(m) {
			if ansi.Strip(it.label) == label {
				return it
			}
		}
		t.Fatalf("no item %q in\n%s", label, plain())
		return settingItem{}
	}
	// A click opens one too: the row is the target, not just enter.
	m.width, m.height = 100, 40
	b := s.render(*m)
	// A row that opens a page is drawn as one, not as something to choose,
	// and the hint says what esc does there.
	page := ansi.Strip(strings.Join(b.lines, "\n"))
	if !strings.Contains(page, "› Daytona") || !strings.Contains(page, "esc close") {
		t.Fatalf("the list:\n%s", page)
	}
	rowY := func(i int) int { return b.y + 3 + i - s.scroll }
	s.mouse(m, tea.MouseMsg{X: b.x + 3, Y: rowY(1), Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}, b)
	if s.provider != "daytona" {
		t.Fatalf("clicking Daytona opened %q", s.provider)
	}
	b = s.render(*m)
	if got := ansi.Strip(strings.Join(b.lines, "\n")); !strings.Contains(got, "esc back to the providers") {
		t.Fatalf("a provider page's hint:\n%s", got)
	}
	s.mouse(m, tea.MouseMsg{X: b.x + 3, Y: rowY(1), Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}, b)
	if s.provider != "" {
		t.Fatalf("clicking ‹ Sandboxes left %q open", s.provider)
	}
	item("Daytona").run(m)
	if s.provider != "daytona" {
		t.Fatalf("Daytona opened %q", s.provider)
	}
	// With no key, the header says so rather than pretending it is ready.
	out := plain()
	for _, want := range []string{"Daytona|no key · $DAYTONA_API_KEY is not set", "API key variable|DAYTONA_API_KEY",
		"Snapshot|Daytona's default", "Region|the account's default",
		"Stop when idle|after 30m with no agent working and nothing printing", "Pass in|nothing"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in\n%s", want, out)
		}
	}
	// The key can be kept here as well as in the environment; until it is,
	// the row says which variable is used instead.
	if !strings.Contains(out, "API key|not set · $DAYTONA_API_KEY is used") {
		t.Fatalf("the key row:\n%s", out)
	}
	// Only the provider that is open, not every provider at once.
	if strings.Contains(out, "BOAT_API_KEY") {
		t.Fatalf("boat's settings are on Daytona's page:\n%s", out)
	}
	// esc goes back to the list, and the ‹ row does the same for the mouse.
	if closed, _ := s.update(m, a2Key("esc")); closed || s.provider != "" {
		t.Fatalf("esc in a provider page: closed %v provider %q", closed, s.provider)
	}
	item("boat.dev").run(m)
	boat := plain()
	for _, want := range []string{"API key variable|BOAT_API_KEY", "Snapshot|boat.dev's default",
		"‹ Sandboxes|esc", "Life|2h 00m · the provider's own"} {
		if !strings.Contains(boat, want) {
			t.Fatalf("boat's page lacks %q in\n%s", want, boat)
		}
	}
	// The life sits beside the idle stop, which is conch's own doing.
	if idle, life := strings.Index(boat, "Stop when idle|"), strings.Index(boat, "Life|"); idle < 0 || life < idle {
		t.Fatalf("Life should follow Stop when idle:\n%s", boat)
	}
	// It is set in minutes, and empty gives the provider's own length back.
	lifeItem := item("Life")
	lifeItem.run(m)
	d, ok := m.overlay.(*dialog)
	if !ok || !strings.Contains(strings.Join(d.text, " "), "boat.dev's default of 2h 00m") {
		t.Fatalf("the life dialog: %#v", m.overlay)
	}
	d.submit(m, []string{"720"})
	if got := m.cfg.Sandbox.Of("boat").AutoStop; got != 720 {
		t.Fatalf("auto_stop is %d", got)
	}
	if got := ansi.Strip(item("Life").detail); got != "12h 00m from when it is made" {
		t.Fatalf("the life row says %q", got)
	}
	if err := d.submit(m, []string{"soon"}); err == nil {
		t.Fatal("a life that isn't a number was taken")
	}
	d.submit(m, []string{""})
	if got := m.cfg.Sandbox.Of("boat").AutoStop; got != 0 {
		t.Fatalf("emptying it left %d", got)
	}
	m.overlay = nil
	// Daytona is not asked how long a sandbox lives: conch leaves its own
	// idle timer off, so auto_stop would mean something else there.
	if strings.Contains(out, "Life|") {
		t.Fatalf("Daytona was asked for a life:\n%s", out)
	}
	item("‹ Sandboxes").run(m)
	if s.provider != "" {
		t.Fatalf("the back row went to %q", s.provider)
	}
	// Switching tab and coming back starts at the list again.
	s.open("boat")
	s.setTab(0)
	s.setTab(sandboxTab)
	if s.provider != "" {
		t.Fatalf("a tab switch kept %q open", s.provider)
	}
	item("Daytona").run(m)
	out = plain()
	useSandboxProvider(t, &sbProvider{})
	if out := plain(); !strings.Contains(out, "✓ $DAYTONA_API_KEY is set") {
		t.Fatalf("configured:\n%s", out)
	}

	// Stopping when idle cycles through the choices and round again.
	seen := []int{}
	for i := 0; i < len(idleStopChoices)+1; i++ {
		item("Stop when idle").run(m)
		seen = append(seen, m.cfg.Sandbox.Of("daytona").IdleMinutes())
	}
	if fmt.Sprint(seen) != "[60 120 0 15 30 60]" {
		t.Fatalf("idle stop cycled %v", seen)
	}
	sixty := 60
	m.cfg.Sandbox.Set("daytona", config.ProviderCfg{IdleStop: &sixty})
	if out := plain(); !strings.Contains(out, "Stop when idle|after 60m with no agent working") {
		t.Fatalf("idle stop text:\n%s", out)
	}

	// A field opened from the settings screen comes back to it, whether it
	// is saved or dropped, so several can be changed in a row.
	item("Snapshot").run(m)
	if d, ok := m.overlay.(*dialog); !ok || d.back != overlay(s) {
		t.Fatalf("the field forgot where it came from: %#v", m.overlay)
	}
	if _, _ = m.overlay.(*dialog).update(m, a2Key("esc")); m.overlay != overlay(s) {
		t.Fatalf("esc went to %#v", m.overlay)
	}

	// The text settings open a dialog that saves into the configuration.
	for _, c := range []struct{ label, typed, want string }{
		{"Snapshot", " my-snapshot ", "my-snapshot"},
		{"Region", "eu", "eu"},
		{"API key variable", "MY_DAYTONA_KEY", "MY_DAYTONA_KEY"},
	} {
		item(c.label).run(m)
		d, ok := m.overlay.(*dialog)
		if !ok || !strings.Contains(d.title, c.label) {
			t.Fatalf("%s: %#v", c.label, m.overlay)
		}
		d.fields[0].in.SetValue(c.typed)
		if _, cmd := d.update(m, a2Key("enter")); a2ErrText(a2Run(cmd)) != "" {
			t.Fatalf("%s: %v", c.label, a2ErrText(a2Run(cmd)))
		}
		if m.overlay != overlay(s) {
			t.Fatalf("%s did not come back to the settings: %#v", c.label, m.overlay)
		}
	}
	got := m.cfg.Sandbox.Of("daytona")
	if got.Snapshot != "my-snapshot" || got.Target != "eu" || got.APIKeyEnv != "MY_DAYTONA_KEY" {
		t.Fatalf("saved %+v", got)
	}
	if out := plain(); !strings.Contains(out, "API key variable|MY_DAYTONA_KEY") || !strings.Contains(out, "$MY_DAYTONA_KEY") {
		t.Fatalf("the named variable is not shown:\n%s", out)
	}

	// Pass in takes names, separated however, and refuses a value.
	item("Pass in").run(m)
	d = m.overlay.(*dialog)
	if msgs := a2Run(d.submit(m, []string{"A_TOKEN=secret"})); !strings.Contains(a2ErrText(msgs), "give the name of a variable") {
		t.Fatalf("a value: %v", msgs)
	}
	if len(m.cfg.Sandbox.Of("daytona").Env) != 0 {
		t.Fatalf("a refused value was saved: %v", m.cfg.Sandbox.Of("daytona").Env)
	}
	a2Run(d.submit(m, []string{"A_TOKEN, B_TOKEN C_TOKEN"}))
	if fmt.Sprint(m.cfg.Sandbox.Of("daytona").Env) != "[A_TOKEN B_TOKEN C_TOKEN]" {
		t.Fatalf("env %v", m.cfg.Sandbox.Of("daytona").Env)
	}
	if out := plain(); !strings.Contains(out, "Pass in|A_TOKEN B_TOKEN C_TOKEN") {
		t.Fatalf("env shown as:\n%s", out)
	}
	m.overlay = nil

	// A build with no providers says so; one conch doesn't configure here
	// is sent to config.toml rather than left blank.
	providers := sandbox.Providers
	t.Cleanup(func() { sandbox.Providers = providers })
	// A provider conch grows gets the same settings, with no code of its
	// own here, and saves under its own name.
	sandbox.Providers = []string{"daytona", "fly"}
	s.open("")
	if got := plain(); !strings.Contains(got, "Fly|") {
		t.Fatalf("a provider conch grows is not listed:\n%s", got)
	}
	s.open("fly")
	out = plain()
	for _, want := range []string{"API key variable|FLY_API_KEY", "Snapshot|Fly's default", "Pass in|nothing"} {
		if !strings.Contains(out, want) {
			t.Fatalf("another provider lacks %q:\n%s", want, out)
		}
	}
	// Each provider's settings are its own, saved under its own name.
	item("Stop when idle").run(m)
	if fly := m.cfg.Sandbox.Of("fly"); fly.IdleStop == nil || *fly.IdleStop == config.IdleStopDefault {
		t.Fatalf("Fly's idle stop was not saved: %+v", m.cfg.Sandbox)
	}
	if got := m.cfg.Sandbox.Of("daytona"); got.Snapshot != "my-snapshot" || got.Target != "eu" {
		t.Fatalf("Daytona's settings changed with Fly's: %+v", got)
	}
	// A provider that leaves the registry takes its page with it.
	sandbox.Providers = []string{"daytona"}
	if got := plain(); strings.Contains(got, "FLY_API_KEY") {
		t.Fatalf("a provider this build no longer knows kept its page:\n%s", got)
	}
	sandbox.Providers = nil
	if out := plain(); !strings.Contains(out, "knows no sandbox providers") {
		t.Fatalf("no providers:\n%s", out)
	}
}

// The key can be kept in the settings as well as in the environment: the
// row never shows it, a key kept here wins over the variable, and emptying
// the field gives the variable back.
func TestSettingsSandboxKey(t *testing.T) {
	m, _ := sandboxModel(t)
	s := &settings{}
	s.setTab(sandboxTab)
	s.open("daytona")
	item := func(label string) settingItem {
		t.Helper()
		for _, it := range s.items(m) {
			if ansi.Strip(it.label) == label {
				return it
			}
		}
		t.Fatalf("no item %q", label)
		return settingItem{}
	}
	row := func(label string) string { return ansi.Strip(item(label).detail) }
	head := func() string {
		for _, it := range s.items(m) {
			if it.header && ansi.Strip(it.label) == "Daytona" {
				return ansi.Strip(it.detail)
			}
		}
		return ""
	}

	item("API key").run(m)
	d, ok := m.overlay.(*dialog)
	if !ok || !strings.Contains(strings.Join(d.text, " "), "kept in config.toml") {
		t.Fatalf("the dialog does not say where it goes: %#v", m.overlay)
	}
	a2Run(d.submit(m, []string{"  dtn_secret_value_9f3a  "}))
	if got := m.cfg.Sandbox.Of("daytona").APIKey; got != "dtn_secret_value_9f3a" {
		t.Fatalf("saved %q", got)
	}
	// Shown by its last few characters only, never whole.
	if got := row("API key"); got != "kept in config.toml …9f3a" {
		t.Fatalf("the row reads %q", got)
	}
	for _, it := range s.items(m) {
		if strings.Contains(ansi.Strip(it.label)+ansi.Strip(it.detail), "dtn_secret_value") {
			t.Fatalf("the key is on screen: %q %q", it.label, it.detail)
		}
	}
	// A short key gives nothing away at all.
	m.cfg.Sandbox.Set("daytona", config.ProviderCfg{APIKey: "abcd"})
	if got := row("API key"); got != "kept in config.toml …" {
		t.Fatalf("a short key reads %q", got)
	}

	// The provider takes it, with no variable set anywhere.
	t.Setenv("DAYTONA_API_KEY", "")
	m.cfg.Sandbox.Set("daytona", config.ProviderCfg{APIKey: "dtn_from_settings"})
	p, err := sandbox.Open("daytona", m.cfg.Sandbox)
	if err != nil || p.Check() != nil {
		t.Fatalf("a key in the settings: %v %v", err, p.Check())
	}
	// And it wins over a variable that is set.
	t.Setenv("DAYTONA_API_KEY", "dtn_from_env")
	if head() != "✓ key kept in the settings" {
		t.Fatalf("heading %q", head())
	}
	// Emptied, the variable is used again.
	item("API key").run(m)
	a2Run(m.overlay.(*dialog).submit(m, []string{"   "}))
	if got := m.cfg.Sandbox.Of("daytona").APIKey; got != "" {
		t.Fatalf("clearing left %q", got)
	}
	if got := row("API key"); got != "not set · $DAYTONA_API_KEY is used" {
		t.Fatalf("after clearing: %q", got)
	}
	if head() != "✓ $DAYTONA_API_KEY is set" {
		t.Fatalf("heading after clearing: %q", head())
	}
	// A named variable is what the rows and the check talk about.
	m.cfg.Sandbox.Set("daytona", config.ProviderCfg{APIKeyEnv: "MY_KEY"})
	t.Setenv("DAYTONA_API_KEY", "")
	if got, want := row("API key"), "not set · $MY_KEY is used"; got != want {
		t.Fatalf("named variable: %q", got)
	}
	if !strings.Contains(head(), "$MY_KEY is not set") {
		t.Fatalf("heading names the wrong variable: %q", head())
	}
}

// Whether conch keeps a note of what a sandbox was running is the user's
// to decide, per provider.
func TestSettingsSandboxRestore(t *testing.T) {
	m, _ := sandboxModel(t)
	s := &settings{}
	s.setTab(sandboxTab)
	s.open("daytona")
	item := func() settingItem {
		t.Helper()
		for _, it := range s.items(m) {
			if ansi.Strip(it.label) == "Bring back what was running" {
				return it
			}
		}
		t.Fatal("no such setting")
		return settingItem{}
	}
	// On unless said otherwise, and shown as a toggle that is on.
	if got := item(); ansi.Strip(got.detail) != "offers the agents and terminals back, resumed where they left off" ||
		got.on == nil || !*got.on {
		t.Fatalf("by default: %q %v", got.detail, got.on)
	}
	if !m.cfg.Sandbox.Of("daytona").RestoresRunning() {
		t.Fatal("the default should be to offer them back")
	}
	// Turned off, it says nothing is kept, and the configuration says so.
	item().run(m)
	if m.cfg.Sandbox.Of("daytona").RestoresRunning() {
		t.Fatal("it is still on")
	}
	if got := item(); ansi.Strip(got.detail) != "no note is kept of what ran" || got.on == nil || *got.on {
		t.Fatalf("turned off: %q %v", got.detail, got.on)
	}
	// And back on again.
	item().run(m)
	if !m.cfg.Sandbox.Of("daytona").RestoresRunning() {
		t.Fatal("it did not come back on")
	}
	// Each provider decides for itself.
	providers := sandbox.Providers
	t.Cleanup(func() { sandbox.Providers = providers })
	sandbox.Providers = []string{"daytona", "fly"}
	no := false
	m.cfg.Sandbox.Set("fly", config.ProviderCfg{Restore: &no})
	if !m.cfg.Sandbox.Of("daytona").RestoresRunning() || m.cfg.Sandbox.Of("fly").RestoresRunning() {
		t.Fatalf("one provider's answer is not another's: %+v", m.cfg.Sandbox)
	}
}
