package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

// paletteOpen opens the palette with the prefix and /, as ⌘K does in
// conch's own window, and answers it.
func paletteOpen(t *testing.T, m *Model) *cmdPalette {
	t.Helper()
	a1Prefixed(t, m, runes("/"))
	p, ok := m.overlay.(*cmdPalette)
	if !ok {
		t.Fatalf("ctrl+b / opened %T", m.overlay)
	}
	return p
}

// paletteType types s into the open palette.
func paletteType(t *testing.T, m *Model, s string) {
	t.Helper()
	for _, r := range s {
		k := runes(string(r))
		if r == ' ' {
			k = tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
		}
		a1Key(t, m, k)
	}
}

// paletteLines is what the palette lists now, plain.
func paletteLines(p *cmdPalette) []string {
	var out []string
	for _, i := range p.shown {
		out = append(out, ansi.Strip(p.items[i].label))
	}
	return out
}

func TestPaletteListsWhereYouCanGoAndWhatYouCanDo(t *testing.T) {
	m, _ := a1Fixture(t, false)
	p := paletteOpen(t, m)
	all := strings.Join(paletteLines(p), "\n")
	for _, want := range []string{"claude", "› zsh", "› bash", "codex", "◆ api", "◇ feat", "Split right", "New task", "Settings"} {
		if !strings.Contains(all, want) {
			t.Errorf("palette has no %q:\n%s", want, all)
		}
	}
	// The main checkout is the project itself, not a task branch.
	if strings.Contains(all, "◇ main") {
		t.Errorf("the main checkout is listed as a branch:\n%s", all)
	}
	// Panes before places before commands.
	if first := paletteLines(p)[0]; !strings.Contains(first, "claude") {
		t.Errorf("first is %q, want a pane", first)
	}
}

func TestPaletteListsAWaitingAgentFirst(t *testing.T) {
	m, _ := a1Fixture(t, false)
	m.machines[0].panes[3].Agent.State = proto.AgentBlocked // codex
	p := paletteOpen(t, m)
	if first := paletteLines(p)[0]; !strings.HasPrefix(first, "! ") || !strings.Contains(first, "codex") {
		t.Fatalf("first is %q, want the waiting codex", first)
	}
}

func TestPaletteGoesToAPane(t *testing.T) {
	m, _ := a1Fixture(t, false)
	paletteOpen(t, m)
	paletteType(t, m, "codex")
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.overlay != nil {
		t.Fatalf("palette still open: %T", m.overlay)
	}
	if got := m.tab().focused().view.PaneID; got != "p4" {
		t.Fatalf("showing %q, want p4", got)
	}
	if m.focus != focusMain {
		t.Fatalf("focus %v: typing should reach the agent", m.focus)
	}
}

func TestPaletteGoesToABranch(t *testing.T) {
	m, _ := a1Fixture(t, false)
	paletteOpen(t, m)
	paletteType(t, m, "feat branch")
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if want := branchNodeID(localMachine, "r1", "feat"); m.cursor != want {
		t.Fatalf("cursor %q, want %q", m.cursor, want)
	}
	if v := m.tab().focused().view; v.Kind != kindBranch || v.Branch != "feat" {
		t.Fatalf("showing %+v, want the branch's changes", v)
	}
}

func TestPaletteGoesToAProject(t *testing.T) {
	m, _ := a1Fixture(t, false)
	m.cursor = paneNodeID(localMachine, "p3")
	paletteOpen(t, m)
	paletteType(t, m, "api project")
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if want := projectNodeID(localMachine, "r1"); m.cursor != want {
		t.Fatalf("cursor %q, want %q", m.cursor, want)
	}
}

// A command is its key: zoom is ctrl+b z, help is ctrl+b ?.
func TestPaletteRunsACommandAsItsKey(t *testing.T) {
	m, _ := a1Fixture(t, false)
	a1Open(t, m, paneNodeID(localMachine, "p2"))
	paletteOpen(t, m)
	paletteType(t, m, "zoom")
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if !m.zoom {
		t.Fatal("Zoom split did not zoom")
	}
	paletteOpen(t, m)
	paletteType(t, m, "help")
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.overlay == nil {
		t.Fatal("Help opened nothing")
	}
	if _, still := m.overlay.(*cmdPalette); still {
		t.Fatal("Help left the palette open")
	}
	// A tree key acts from the tree, wherever the palette was opened.
	m.overlay = nil
	m.focus = focusMain
	paletteOpen(t, m)
	paletteType(t, m, "review queue")
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.tab().focused().view.Kind != kindReviewQueue {
		t.Fatalf("Review queue shows %+v", m.tab().focused().view)
	}
}

func TestPaletteNarrowsAndRanks(t *testing.T) {
	m, _ := a1Fixture(t, false)
	p := paletteOpen(t, m)
	all := len(p.shown)
	paletteType(t, m, "split")
	lines := paletteLines(p)
	if len(lines) == 0 || len(lines) >= all {
		t.Fatalf("split left %d of %d", len(lines), all)
	}
	for _, l := range lines {
		if !strings.Contains(strings.ToLower(l), "split") {
			t.Errorf("%q does not match split", l)
		}
	}
	// A name that starts with the query comes before one that only has
	// it inside: "Split right" before "Close split".
	if !strings.HasPrefix(lines[0], "Split") {
		t.Errorf("first is %q", lines[0])
	}
	// Words match anywhere, in any order.
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyCtrlU})
	paletteType(t, m, "down split")
	if l := paletteLines(p); len(l) != 1 || l[0] != "Split down" {
		t.Fatalf("down split: %q", l)
	}
	// Backspace widens again; nothing matching says so, and enter on it
	// closes without running anything.
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyBackspace})
	if len(paletteLines(p)) == 0 {
		t.Fatal("backspace left nothing")
	}
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyCtrlU})
	paletteType(t, m, "zzqq")
	if len(p.shown) != 0 {
		t.Fatalf("zzqq: %q", paletteLines(p))
	}
	if b := strings.Join(p.render(*m).lines, "\n"); !strings.Contains(ansi.Strip(b), "nothing matches") {
		t.Fatalf("no match is not said:\n%s", ansi.Strip(b))
	}
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.overlay != nil {
		t.Fatal("enter on nothing left the palette open")
	}
}

// Letters are typed, not taken as keys: j and k would move a menu.
func TestPaletteTypesLettersThatAreKeysElsewhere(t *testing.T) {
	m, _ := a1Fixture(t, false)
	p := paletteOpen(t, m)
	paletteType(t, m, "jkq")
	if p.query != "jkq" || m.overlay == nil {
		t.Fatalf("query %q, open %v", p.query, m.overlay != nil)
	}
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.overlay != nil {
		t.Fatal("esc did not close it")
	}
}

func TestPalettePasteIsOneLine(t *testing.T) {
	m, _ := a1Fixture(t, false)
	p := paletteOpen(t, m)
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("split\nright\t"), Paste: true})
	if p.query != "split right" {
		t.Fatalf("query %q", p.query)
	}
}

func TestPaletteMovesAndScrolls(t *testing.T) {
	m, _ := a1Fixture(t, false)
	m.height = 12 // a few rows on screen, far fewer than the list
	p := paletteOpen(t, m)
	n := p.rows(m.height)
	if n >= len(p.shown) {
		t.Fatalf("%d rows for %d items: nothing to scroll", n, len(p.shown))
	}
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyUp}) // already at the top
	if p.sel != 0 {
		t.Fatalf("up from the top: %d", p.sel)
	}
	for range n {
		a1Key(t, m, tea.KeyMsg{Type: tea.KeyDown})
	}
	if p.sel != n || p.top != 1 {
		t.Fatalf("after %d downs: sel %d top %d", n, p.sel, p.top)
	}
	for range len(p.shown) { // more than enough pages: it stops at the end
		a1Key(t, m, tea.KeyMsg{Type: tea.KeyPgDown})
	}
	if p.sel != len(p.shown)-1 || p.sel-p.top != n-1 {
		t.Fatalf("at the end: sel %d top %d of %d", p.sel, p.top, len(p.shown))
	}
	// The selection is always on screen, and drawn.
	b := p.render(*m)
	if !strings.Contains(strings.Join(b.lines, "\n"), ansi.Strip(p.items[p.shown[p.sel]].label)) {
		t.Fatal("the selected item is not drawn")
	}
}

func TestPaletteMouse(t *testing.T) {
	m, _ := a1Fixture(t, false)
	p := paletteOpen(t, m)
	b := p.render(*m)
	// Hovering the second item selects it; the wheel moves; a click runs.
	p.mouse(m, tea.MouseMsg{X: b.x + 4, Y: b.y + 4, Action: tea.MouseActionMotion}, b)
	if p.sel != 1 {
		t.Fatalf("hover: sel %d", p.sel)
	}
	p.mouse(m, tea.MouseMsg{X: b.x + 4, Y: b.y + 4, Button: tea.MouseButtonWheelDown}, b)
	if p.sel != 2 {
		t.Fatalf("wheel: sel %d", p.sel)
	}
	// The query line and the rule are not items.
	p.mouse(m, tea.MouseMsg{X: b.x + 4, Y: b.y + 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}, b)
	if m.overlay == nil {
		t.Fatal("a click on the query ran something")
	}
	target := p.items[p.shown[0]]
	p.mouse(m, tea.MouseMsg{X: b.x + 4, Y: b.y + 3, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}, b)
	if m.overlay != nil {
		t.Fatal("a click on an item left the palette open")
	}
	if !strings.Contains(target.label, "claude") || m.tab().focused().view.PaneID != "p1" {
		t.Fatalf("clicked %q, showing %q", ansi.Strip(target.label), m.tab().focused().view.PaneID)
	}
	// A press outside closes it.
	p = paletteOpen(t, m)
	b = p.render(*m)
	p.mouse(m, tea.MouseMsg{X: 0, Y: m.height - 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}, b)
	if m.overlay != nil {
		t.Fatal("a click outside left it open")
	}
}

func TestPaletteFitsEverySize(t *testing.T) {
	for _, sz := range [][2]int{{1, 1}, {20, 5}, {39, 10}, {80, 24}, {300, 90}} {
		m, _ := a1Fixture(t, false)
		m.width, m.height = sz[0], sz[1]
		p := paletteOpen(t, m)
		paletteType(t, m, "s")
		b := p.render(*m)
		if sz[0] >= 3 {
			for i, l := range b.lines {
				if w := ansi.StringWidth(l); w > m.width {
					t.Errorf("%dx%d: line %d is %d wide", sz[0], sz[1], i, w)
				}
			}
		}
		for i, l := range strings.Split(m.View(), "\n") {
			if w := ansi.StringWidth(l); w > m.width {
				t.Errorf("%dx%d: view line %d is %d wide", sz[0], sz[1], i, w)
			}
		}
	}
}

// conch's own pane is never a place to go: showing it shrinks the
// terminal conch is drawing in (tabs.go, ownPane).
func TestPaletteLeavesOutItsOwnPane(t *testing.T) {
	m, _ := a1Fixture(t, false)
	m.machines[0].ownPane = "p4"
	p := paletteOpen(t, m)
	for _, l := range paletteLines(p) {
		if strings.Contains(l, "codex") {
			t.Fatalf("its own pane is listed: %q", l)
		}
	}
}

// In conch's own window a command shows its ⌘ shortcut beside its key;
// anywhere else, only the key, since ⌘ does nothing there.
func TestPaletteNamesShortcutsInTheAppWindow(t *testing.T) {
	hint := func(app bool) string {
		m, _ := a1Fixture(t, false)
		m.appWindow = app
		p := paletteOpen(t, m)
		for _, it := range p.items {
			if it.label == "Split right" {
				return it.hint
			}
		}
		t.Fatal("no Split right")
		return ""
	}
	if got := hint(false); got != "ctrl+b %" {
		t.Errorf("terminal: %q", got)
	}
	if got := hint(true); got != "⌘D  ctrl+b %" {
		t.Errorf("app window: %q", got)
	}
}

// With the prefix pressed, the bar offers the palette, and a click on it
// opens it.
func TestPrefixBarOffersThePalette(t *testing.T) {
	m, _ := a1Fixture(t, false)
	m.prefixArmed = true
	if !strings.Contains(a1HintText(m), "/ palette") {
		t.Fatalf("prefix bar: %s", a1HintText(m))
	}
	_, items := m.statusHints()
	for _, it := range items {
		if strings.Contains(ansi.Strip(it.text), "palette") {
			it.act(m)
			if _, ok := m.overlay.(*cmdPalette); !ok || m.prefixArmed {
				t.Fatalf("click: overlay %T, prefix %v", m.overlay, m.prefixArmed)
			}
			return
		}
	}
	t.Fatal("no palette item")
}

// The palette is a search box: no title in its frame, what to type for
// while empty, how many match beside the query, and the screen behind it
// faded.
func TestPaletteLooksLikeASearchBox(t *testing.T) {
	m, _ := a1Fixture(t, false)
	p := paletteOpen(t, m)
	b := p.render(*m)
	if top := ansi.Strip(b.lines[0]); strings.Trim(top, "╭─╮") != "" {
		t.Fatalf("frame has a title: %q", top)
	}
	if l := ansi.Strip(b.lines[1]); !strings.Contains(l, "agents, branches, projects, commands") {
		t.Fatalf("empty search line %q", l)
	}
	paletteType(t, m, "split")
	l := strings.TrimRight(strings.TrimSuffix(ansi.Strip(p.render(*m).lines[1]), "│"), " ")
	if want := "1/" + itoa(len(p.shown)); !strings.HasSuffix(l, want) {
		t.Fatalf("search line %q, want it to end %s", l, want)
	}
	if !p.dimBackground() {
		t.Fatal("the screen behind is not faded")
	}
	// In conch's own window it hangs from the header's search box.
	appSized(t, m, 150, 40)
	if y := p.render(*m).y; y != 0 {
		t.Fatalf("in the app window it opens at row %d", y)
	}
}
