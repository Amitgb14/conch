package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/keymap"
	"github.com/Amitgb14/conch/internal/proto"
)

// A rebound key does the thing, its old key stops doing it, and an
// unbound one does nothing — through the prefix, as somebody presses it.
func TestA1ReboundPrefixKeys(t *testing.T) {
	m, _ := a1Fixture(t, true)
	m.cfg.Keys.Bind = map[string]string{
		"split_right": `\`,
		"new_tab":     "C-n",
		"close_tab":   "", // unbound
	}
	var problems []string
	m.binds, problems = keymap.Resolve(m.cfg.Keys.Bind)
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	a1Open(t, m, paneNodeID(localMachine, "p1"))

	// The new key splits; the default one no longer does.
	a1Prefixed(t, m, runes(`\`))
	if n := len(m.tab().root.leaves()); n != 2 {
		t.Fatalf(`\: %d leaves`, n)
	}
	a1Prefixed(t, m, runes("v"))
	if n := len(m.tab().root.leaves()); n != 2 {
		t.Fatalf("v still splits: %d leaves", n)
	}

	// A key with a modifier on it works the same way.
	was := len(m.tabs)
	a1Prefixed(t, m, tea.KeyMsg{Type: tea.KeyCtrlN})
	if len(m.tabs) != was+1 {
		t.Fatalf("ctrl+n: %d tabs, want %d", len(m.tabs), was+1)
	}
	a1Prefixed(t, m, runes("c"))
	if len(m.tabs) != was+1 {
		t.Fatalf("c still makes a tab: %d", len(m.tabs))
	}

	// An unbound action cannot be reached by its old key, and the key
	// falls through to nothing rather than to another action.
	tabs := len(m.tabs)
	a1Prefixed(t, m, runes("&"))
	if len(m.tabs) != tabs {
		t.Fatalf("& closed a tab although close_tab is unbound: %d", len(m.tabs))
	}
	if cmd, ok := m.layoutKey("&"); ok || cmd != nil {
		t.Fatal("& is still handled")
	}

	// What was not named keeps its default, including the keys that are
	// not in the table at all.
	a1Prefixed(t, m, runes("z"))
	if !m.zoom {
		t.Fatal("zoom did not keep z")
	}
	a1Prefixed(t, m, runes("1"))
	if m.activeTab != 0 {
		t.Fatalf("1 is not the first tab: %d", m.activeTab)
	}
	a1Prefixed(t, m, tea.KeyMsg{Type: tea.KeyCtrlLeft})
	if m.repeatUntil.Before(time.Now()) {
		t.Fatal("ctrl+left is not a resize any more")
	}
}

// The keys inside a pane follow the table too — those are the ones the
// status bar and the menus advertise.
func TestA1ReboundPaneKeys(t *testing.T) {
	m, _ := a1Fixture(t, true)
	m.binds, _ = keymap.Resolve(map[string]string{"scroll_mode": "u", "open_links": ""})
	a1Open(t, m, paneNodeID(localMachine, "p1"))
	// Scroll mode needs a screen to put a cursor on.
	cols, rows := m.paneArea()
	lines := make([]string, rows)
	for i := range lines {
		lines[i] = strings.Repeat("a", cols)
	}
	m.frames[paneKey(localMachine, "p1")] = &proto.Frame{ID: "p1", Lines: lines, History: 1000}
	m.syncView()
	m.focus = focusMain

	a1Prefixed(t, m, runes("u"))
	if !m.scrollMode {
		t.Fatal("u does not scroll")
	}
	m.scrollMode = false
	a1Prefixed(t, m, runes("["))
	if m.scrollMode {
		t.Fatal("[ still scrolls")
	}
	// An unbound key in a pane goes back to the tree, as any other key
	// after the prefix does, rather than doing what it used to.
	m.focus = focusMain
	a1Prefixed(t, m, runes("u"))
	m.scrollMode = false
	m.focus = focusMain
	a1Prefixed(t, m, runes("K"))
	if m.focus != focusSidebar {
		t.Fatal("an unknown key after the prefix did not go back to the tree")
	}
}

// The prefix twice still types the prefix into the program, whatever the
// table says.
func TestA1PrefixTwiceStillTypesIt(t *testing.T) {
	m, peer := a1Fixture(t, true)
	m.binds, _ = keymap.Resolve(map[string]string{"zoom": "ctrl+b"})
	a1Open(t, m, paneNodeID(localMachine, "p1"))
	m.focus = focusMain
	a1Prefixed(t, m, tea.KeyMsg{Type: tea.KeyCtrlB})
	if m.zoom {
		t.Fatal("the prefix twice zoomed instead of being typed")
	}
	peer.waitMethod(t, proto.MethodPaneSendKeys, "ctrl+b")
}

// A binding on the prefix key is refused and said: the prefix twice is how
// a program below conch gets its own ctrl+b, so an action there could
// never be pressed.
func TestResolveBindsKeepsThePrefix(t *testing.T) {
	cfg := config.Default()
	cfg.Keys.Prefix = "ctrl+b"
	cfg.Keys.Bind = map[string]string{"zoom": "C-b", "new_tab": "C-t"}
	binds, warn := resolveBinds(cfg)
	if len(warn) != 1 || !strings.Contains(warn[0], "zoom is on ctrl+b, which is the prefix itself") {
		t.Fatalf("warnings: %v", warn)
	}
	if got := binds.Key(keymap.Zoom); got != "z" {
		t.Errorf("zoom is on %q, want its default back", got)
	}
	if got := binds.Key(keymap.NewTab); got != "ctrl+t" {
		t.Errorf("new_tab is on %q", got)
	}
	// An empty key is an unbind, not a claim on the prefix.
	cfg.Keys.Bind = map[string]string{"zoom": ""}
	binds, warn = resolveBinds(cfg)
	if len(warn) != 0 || binds.Bound(keymap.Zoom) {
		t.Fatalf("unbinding zoom: %v, on %q", warn, binds.Keys(keymap.Zoom))
	}
}

// What was wrong with the table is on screen as soon as the TUI starts,
// beside whatever else could not be read — and the keys it could make
// sense of are in force, since a mistake in one line is not a reason to
// hand somebody a conch with no prefix table.
func TestBindProblemsAreSaidOnStart(t *testing.T) {
	c, _ := a1FakeClient(t)
	cfg := config.Default()
	cfg.Keys.Bind = map[string]string{"nosuchthing": "k", "zoom": "f"}
	m := New(c, cfg)
	if !strings.Contains(m.warning, "nosuchthing is not a key conch binds") {
		t.Fatalf("warning %q", m.warning)
	}
	if got := m.binds.Key(keymap.Zoom); got != "f" {
		t.Errorf("zoom is on %q, so a bad line cost a good one", got)
	}
	if got := m.Warn("settings could not be read").warning; !strings.HasPrefix(got, "settings could not be read · ") ||
		!strings.Contains(got, "nosuchthing") {
		t.Fatalf("both warnings: %q", got)
	}
	// Nothing wrong, nothing said.
	c2, _ := a1FakeClient(t)
	if got := New(c2, config.Default()).Warn("").warning; got != "" {
		t.Fatalf("a clean start says %q", got)
	}
}

// The status bar's PREFIX row lists the keys *after* the prefix, so it is
// the one row that reads them bare. With [keys.bind] those letters are the
// person's: the row shows the key each action is on now, and leaves out an
// action that has no key rather than offering one that does nothing.
func TestPrefixBarFollowsTheBindings(t *testing.T) {
	m, _ := a1Fixture(t, false)
	m.prefixArmed = true
	was := a1HintText(m)
	for _, want := range []string{"/ palette", "v split", "z zoom", "[ scroll"} {
		if !strings.Contains(was, want) {
			t.Fatalf("default prefix bar lacks %q:\n%s", want, was)
		}
	}

	m.cfg.Keys.Bind = map[string]string{
		"palette":     "p",
		"split_right": `\`,
		"zoom":        "f",
		"scroll_mode": "", // unbound
	}
	var problems []string
	m.binds, problems = keymap.Resolve(m.cfg.Keys.Bind)
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	got := a1HintText(m)
	for _, want := range []string{"p palette", `\ split`, "f zoom"} {
		if !strings.Contains(got, want) {
			t.Errorf("prefix bar lacks %q:\n%s", want, got)
		}
	}
	// The keys those actions used to be on are nobody's now.
	for _, gone := range []string{"/ palette", "v split", "z zoom"} {
		if strings.Contains(got, gone) {
			t.Errorf("prefix bar still offers %q:\n%s", gone, got)
		}
	}
	// An unbound action is not in the row at all.
	if strings.Contains(got, "scroll") {
		t.Errorf("unbound scroll_mode is still offered:\n%s", got)
	}
}
