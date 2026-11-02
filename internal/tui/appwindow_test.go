package tui

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/Amitgb14/conch/internal/proto"
)

func TestWindowTitleSaysWhatIsWaiting(t *testing.T) {
	m, _ := a1Fixture(t, false)
	if got := m.windowTitle(); got != "conch · 1 agent working" {
		t.Fatalf("title %q", got)
	}
	m.machines[0].panes[0].Agent.State = proto.AgentBlocked
	if got := m.windowTitle(); got != "conch · ⚑ claude on feat waiting" {
		t.Fatalf("title %q", got)
	}
	for _, p := range m.machines[0].panes {
		if p.Agent != nil {
			p.Agent.State = proto.AgentIdle
		}
	}
	if got := m.windowTitle(); got != "conch" {
		t.Fatalf("idle: %q", got)
	}
}

// A name is somebody's, and a newline in it would end the title early.
func TestWindowTitleIsOneLine(t *testing.T) {
	m, _ := a1Fixture(t, false)
	m.machines[0].panes[0].Name = "evil\x1b]2;x\a\nname"
	m.machines[0].panes[0].Agent.State = proto.AgentBlocked
	if got := m.windowTitle(); strings.ContainsAny(got, "\x1b\a\n") {
		t.Fatalf("title %q", got)
	}
}

func TestWindowTitleOnlyInTheAppWindowAndOnlyOnChange(t *testing.T) {
	m, _ := a1Fixture(t, false)
	windowTitles = nil
	m.titleWindow()
	if len(windowTitles) != 0 {
		t.Fatalf("an ordinary terminal was retitled: %q", windowTitles)
	}
	m.appWindow = true
	m.titleWindow()
	m.titleWindow()
	if len(windowTitles) != 1 {
		t.Fatalf("titles %q, want one", windowTitles)
	}
	m.machines[0].panes[0].Agent.State = proto.AgentBlocked
	m.titleWindow()
	if len(windowTitles) != 2 || !strings.Contains(windowTitles[1], "waiting") {
		t.Fatalf("titles %q", windowTitles)
	}
}

// Every ⌘ shortcut is a key conch takes after the prefix: one that sent a
// key nothing answers would be a shortcut that silently does nothing (or,
// in the tree, goes back to it).
func TestEveryAppShortcutIsAPrefixKey(t *testing.T) {
	asKey := map[string]tea.KeyMsg{
		"\x1b[A": {Type: tea.KeyUp}, "\x1b[B": {Type: tea.KeyDown},
		"\x1b[C": {Type: tea.KeyRight}, "\x1b[D": {Type: tea.KeyLeft},
	}
	seen := map[string]bool{}
	for _, sc := range AppShortcuts {
		id := sc.Mods + "+" + sc.Key
		if seen[id] {
			t.Errorf("⌘%s twice", id)
		}
		seen[id] = true
		k, ok := asKey[sc.Send]
		if !ok {
			k = runes(sc.Send)
		}
		m, _ := a1Fixture(t, false)
		a1Open(t, m, paneNodeID(localMachine, "p2"))
		m.focus = focusMain
		// zoom and ! are taken before layoutKey; everything else by it.
		switch sc.Send {
		case "z":
			a1Prefixed(t, m, k)
			if !m.zoom {
				t.Errorf("⌘%s: no zoom", id)
			}
			continue
		case "!":
			a1Prefixed(t, m, k)
			if m.flash == "" {
				t.Errorf("⌘%s: nothing happened", id)
			}
			continue
		}
		if _, handled := m.layoutKey(k.String()); !handled {
			t.Errorf("⌘%s sends %q, which conch does not take after the prefix", id, sc.Send)
		}
	}
}

func TestAppShortcutNames(t *testing.T) {
	for send, want := range map[string]string{"c": "⌘T", "&": "⇧⌘W", "z": "⇧⌘⏎", "\x1b[A": "⌥⌘↑", "/": "⌘K", "1": "⌘1", "q": ""} {
		if got := appShortcutFor(send); got != want {
			t.Errorf("appShortcutFor(%q) = %q, want %q", send, got, want)
		}
	}
}

// The window conch opens is given each theme's own background and text.
func TestTerminalColorsForEveryTheme(t *testing.T) {
	hex := regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
	for _, th := range themes {
		bg, fg, cur, sel := TerminalColors(th.name, "")
		for _, c := range []string{bg, fg, cur, sel} {
			if !hex.MatchString(c) {
				t.Errorf("%s: colour %q", th.name, c)
			}
		}
		if bg == fg {
			t.Errorf("%s: text the colour of the background", th.name)
		}
	}
	if _, _, cur, _ := TerminalColors("nord", "orange"); cur != string(accentColors["orange"]) {
		t.Errorf("accent name: cursor %s", cur)
	}
	if _, _, cur, _ := TerminalColors("nord", "#123456"); cur != "#123456" {
		t.Errorf("accent hex: cursor %s", cur)
	}
	if bg, _, _, _ := TerminalColors("no-such-theme", ""); bg != string(themes[0].bg) {
		t.Errorf("unknown theme: %s", bg)
	}
}

// appSized is m in conch's own window, sized as the terminal says.
func appSized(t *testing.T, m *Model, w, h int) {
	t.Helper()
	m.appWindow = true
	next, _ := m.update(tea.WindowSizeMsg{Width: w, Height: h})
	*m = next.(Model)
}

func TestAppHeaderTakesTheTopRow(t *testing.T) {
	m, _ := a1Fixture(t, false)
	appSized(t, m, 150, 40)
	if m.headerRows != 2 || m.height != 38 {
		t.Fatalf("header %d, height %d", m.headerRows, m.height)
	}
	lines := strings.Split(m.View(), "\n")
	if len(lines) != 40 {
		t.Fatalf("%d lines for a 40-row window", len(lines))
	}
	top := ansi.Strip(lines[0])
	if !strings.Contains(top, "⌕ Search agents, branches, commands") || !strings.Contains(top, "⌘K") {
		t.Fatalf("header %q", top)
	}
	// conch's mark and the workspaces are at its left, and so no longer
	// at the top of the sidebar under it.
	if !strings.HasPrefix(top, "  ◆ conch  + ") {
		t.Fatalf("header %q", top)
	}
	// A row of space between the band and the tab bar.
	if strings.TrimSpace(ansi.Strip(lines[1])) != "" {
		t.Fatalf("row under the band %q", ansi.Strip(lines[1]))
	}
	// The sidebar keeps its box, with no title of its own: the
	// workspaces are in the band.
	if side := ansi.Cut(ansi.Strip(lines[2]), 0, m.sidebarW); strings.Trim(side, "╭─╮") != "" || !strings.HasPrefix(side, "╭") {
		t.Fatalf("sidebar's top row %q", side)
	}

	// With an agent waiting, the header says so on the right.
	m.machines[0].panes[0].Agent.State = proto.AgentBlocked
	if top := ansi.Strip(strings.Split(m.View(), "\n")[0]); !strings.HasSuffix(strings.TrimRight(top, " "), "⚑ 1 waiting") {
		t.Fatalf("header %q", top)
	}
}

func TestAppHeaderOnlyInTheAppWindowAndTallEnough(t *testing.T) {
	m, _ := a1Fixture(t, false)
	next, _ := m.update(tea.WindowSizeMsg{Width: 150, Height: 40})
	*m = next.(Model)
	if m.headerRows != 0 || strings.Contains(m.View(), "⌘K") {
		t.Fatal("a header in an ordinary terminal")
	}
	appSized(t, m, 150, appHeaderMinHeight-1)
	if m.headerRows != 0 || m.height != appHeaderMinHeight-1 {
		t.Fatalf("short window: header %d height %d", m.headerRows, m.height)
	}
	// Growing again brings it back, and shrinking takes it away: the
	// height is the terminal's each time, not the last one less a row.
	appSized(t, m, 150, 30)
	appSized(t, m, 150, 30)
	if m.headerRows != 2 || m.height != 28 {
		t.Fatalf("resized twice: header %d height %d", m.headerRows, m.height)
	}
}

func TestAppHeaderFitsEveryWidth(t *testing.T) {
	for _, w := range []int{1, 5, 20, 39, 80, 300} {
		m, _ := a1Fixture(t, false)
		m.machines[0].panes[0].Agent.State = proto.AgentBlocked
		appSized(t, m, w, 30)
		for i, l := range strings.Split(m.View(), "\n") {
			if got := ansi.StringWidth(l); got > w {
				t.Errorf("width %d: line %d is %d wide", w, i, got)
			}
		}
	}
}

func TestAppHeaderClicks(t *testing.T) {
	m, _ := a1Fixture(t, false)
	m.machines[0].panes[3].Agent.State = proto.AgentBlocked // codex
	appSized(t, m, 150, 40)
	click := func(x, y int) {
		next, _ := m.update(tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
		*m = next.(Model)
	}
	x0, _ := m.appHeaderBox()
	click(x0+2, 0)
	if _, ok := m.overlay.(*cmdPalette); !ok {
		t.Fatalf("a click on the box opened %T", m.overlay)
	}
	click(1, 0) // outside it, on the header: closes it as anywhere would
	if m.overlay != nil {
		t.Fatalf("still open: %T", m.overlay)
	}
	// The row of space under the band does nothing, bar closing a menu.
	cursor := m.cursor
	m.openPalette()
	click(5, 1)
	if m.overlay != nil || m.cursor != cursor {
		t.Fatalf("a click on the space: overlay %T, cursor %q", m.overlay, m.cursor)
	}
	_, cx := m.appHeaderCounts()
	click(cx+1, 0)
	if want := paneNodeID(localMachine, "p4"); m.cursor != want {
		t.Fatalf("a click on the counts went to %q, want %q", m.cursor, want)
	}

	// Below the header a click lands where it would without one: the
	// same tree row, one screen row lower.
	plain, _ := a1Fixture(t, false)
	next, _ := plain.update(tea.WindowSizeMsg{Width: 150, Height: 40 - appHeaderRows})
	*plain = next.(Model)
	row := 1 + indexOfRow(plain.rows, paneNodeID(localMachine, "p2"))
	next, _ = plain.update(tea.MouseMsg{X: 5, Y: row, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	*plain = next.(Model)
	m.overlay = nil
	click(5, row+appHeaderRows)
	if plain.cursor == "" || plain.cursor == paneNodeID(localMachine, "p4") || m.cursor != plain.cursor {
		t.Fatalf("without header %q, with %q", plain.cursor, m.cursor)
	}
}

// The workspace buttons work from the header, and the sidebar's top row
// no longer answers for them.
func TestAppHeaderWorkspaces(t *testing.T) {
	m, _ := a1Fixture(t, false)
	appSized(t, m, 150, 40)
	click := func(x, y int) {
		next, _ := m.update(tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
		*m = next.(Model)
	}
	plusAt := func() int {
		_, hits := m.appHeaderSpaces()
		for _, h := range hits {
			if h.space == spaceHitPlus {
				return h.x0
			}
		}
		t.Fatalf("no + in %q", ansi.Strip(m.appHeader()))
		return -1
	}
	click(plusAt(), 0)
	if len(m.spaces) != 2 || m.activeSpace != 1 {
		t.Fatalf("+ in the header: %d spaces, on %d", len(m.spaces), m.activeSpace)
	}
	if top := ansi.Strip(m.appHeader()); !strings.Contains(top, " 1  2 × ") {
		t.Fatalf("header %q", top)
	}
	_, hits := m.appHeaderSpaces()
	for _, h := range hits {
		if h.space == 0 {
			click(h.x0, 0)
		}
	}
	if m.activeSpace != 0 {
		t.Fatalf("clicking 1 left workspace %d on screen", m.activeSpace)
	}
	if _, ok := m.spaceAt(4); ok {
		t.Fatal("the sidebar's top row still has buttons in the app window")
	}
	// A header too narrow for them leaves them out rather than cutting one.
	appSized(t, m, 30, 40)
	if l, hits := m.appHeaderSpaces(); l != "" || hits != nil {
		t.Fatalf("narrow: %q %v", ansi.Strip(l), hits)
	}
}

// The header is a band of the title bar's shade across the whole width —
// under its gaps and after every styled part, each of which ends in a
// reset — with the search field set into it in the window's background.
func TestAppHeaderIsABand(t *testing.T) {
	old := lipgloss.ColorProfile()
	t.Cleanup(func() { lipgloss.SetColorProfile(old); applyTheme("conch", "") })
	lipgloss.SetColorProfile(termenv.TrueColor)
	applyTheme("conch", "")
	m, _ := a1Fixture(t, false)
	m.machines[0].panes[0].Agent.State = proto.AgentBlocked
	appSized(t, m, 150, 40)
	line := m.appHeader()
	band := "\x1b[" + termenv.TrueColor.Color(string(colorChrome)).Sequence(true) + "m"
	if !strings.HasPrefix(line, band) {
		t.Fatalf("no band at the start: %q", line)
	}
	body := strings.TrimSuffix(line, "\x1b[0m")
	if n, m := strings.Count(body, "\x1b[0m"), strings.Count(body, "\x1b[0m"+band); n == 0 || n != m {
		t.Fatalf("%d resets, %d followed by the band", n, m)
	}
	if w := ansi.StringWidth(line); w != 150 {
		t.Fatalf("band is %d wide in 150", w)
	}
	if well := termenv.TrueColor.Color(string(colorWell)).Sequence(true); !strings.Contains(line, well) {
		t.Fatal("the search field is not set into the band")
	}
	// No colours, no escapes.
	lipgloss.SetColorProfile(termenv.Ascii)
	if got := onBand("x", colorChrome); got != "x" {
		t.Fatalf("ascii: %q", got)
	}
}

func TestLighten(t *testing.T) {
	// The title bar measured over conch's background, #20242A, to within
	// a step a channel.
	got := string(lighten("#15191E", 0.05))
	for i, want := range []int{0x20, 0x24, 0x2A} {
		var c int
		fmt.Sscanf(got[1+2*i:3+2*i], "%02x", &c)
		if c < want-1 || c > want+1 {
			t.Fatalf("lighten conch bg = %s, want about #20242A", got)
		}
	}
	if got := lighten("#000000", 0); got != "#000000" {
		t.Fatalf("by nothing = %s", got)
	}
	if got := lighten("#FFFFFF", 0.5); got != "#FFFFFF" {
		t.Fatalf("white = %s", got)
	}
	for _, bad := range []lipgloss.Color{"", "teal", "#12", "#GGGGGG"} {
		if got := lighten(bad, 0.05); got != bad {
			t.Errorf("%q came back %q", bad, got)
		}
	}
}
