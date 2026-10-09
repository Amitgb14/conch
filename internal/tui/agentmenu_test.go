package tui

import (
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

// a5Agents is a1Fixture with an agent list: the five supported, the
// manifests conch ships, and one of somebody's own — installed, and not,
// and one conch has no installer for.
func a5Agents(t *testing.T, withClient bool) (*Model, *a1Peer) {
	t.Helper()
	a2Isolate(t)
	m, peer := a1Fixture(t, false)
	if withClient {
		// Its own client, because asking which agents are installed is
		// behind a capability an older server does not have.
		var c *client.Client
		c, peer = a1FakeClient(t, "agent.install.v1")
		m.machines[0].c, m.machines[0].server = c, c.Server
	}
	list := []proto.AgentAvailability{
		{Name: "claude", Label: "Claude Code", Installed: true, Version: "2.1.293", Tier: proto.TierSupported},
		{Name: "codex", Label: "Codex", Installed: false, Tier: proto.TierSupported},
		{Name: "kilo", Label: "Kilo Code", Installed: true, Version: "7.8.8", Tier: proto.TierRunsHere},
		{Name: "aider", Label: "Aider", Installed: false, Tier: proto.TierRunsHere},
		{Name: "homegrown", Label: "Home Grown", Installed: false, Tier: proto.TierRunsHere, NoInstaller: true},
	}
	mach := m.machines[0]
	mach.agentList = list
	mach.available = map[string]proto.AgentAvailability{}
	for _, a := range list {
		mach.available[a.Name] = a
	}
	return m, peer
}

// Every agent gets a mark, including the ones conch only runs and the
// ones it has never heard of. Reported from use: "there are icon missing
// for newly added agents".
func TestEveryAgentHasAMark(t *testing.T) {
	m, _ := a5Agents(t, false)
	pane := func(agent string) proto.PaneInfo {
		return proto.PaneInfo{ID: "p9", Agent: &proto.AgentStatus{Name: agent}}
	}
	for _, agent := range []string{"claude", "codex", "gemini", "opencode", "devin",
		"aider", "amp", "cursor", "grok", "kilo", "pi", "somebodys-own", "", "robo2"} {
		for _, icons := range []string{"text", "nerd"} {
			m.cfg.UI.Icons = icons
			mark, _ := m.agentMark(pane(agent))
			if strings.TrimSpace(mark) == "" {
				t.Errorf("%s has no mark with icons = %s", agent, icons)
			}
		}
	}
	// Each of the ten conch knows is its own mark, so two agents never
	// read alike.
	for _, icons := range []string{"text", "nerd"} {
		m.cfg.UI.Icons = icons
		seen := map[string]string{}
		for _, agent := range []string{"claude", "codex", "gemini", "opencode", "devin", "aider", "amp", "cursor", "grok", "kilo", "pi"} {
			mark, _ := m.agentMark(pane(agent))
			if was, dup := seen[mark]; dup {
				t.Errorf("%s and %s share a mark with icons = %s", was, agent, icons)
			}
			seen[mark] = agent
		}
	}
	// Icons off leaves them all out, as before.
	m.cfg.UI.Icons = "off"
	for _, agent := range []string{"claude", "kilo", "somebodys-own"} {
		if mark, _ := m.agentMark(pane(agent)); mark != "" {
			t.Errorf("icons are off, yet %s has %q", agent, mark)
		}
	}
	// A pane that is no agent still has none.
	if mark, _ := m.agentMark(proto.PaneInfo{ID: "p9"}); mark != "" {
		t.Errorf("a terminal has an agent mark: %q", mark)
	}
	// The default says what the thing is — an agent of a kind conch was
	// not told about — rather than being a blank square (AGENTS.md,
	// Adding an agent · Marks).
	m.cfg.UI.Icons = "text"
	if mark, _ := m.agentMark(pane("somebodys-own")); strings.TrimSpace(mark) != "🤖" {
		t.Errorf("the default mark is %q, not a bot", strings.TrimSpace(mark))
	}
}

// A mark is one or two cells and the tree measures every line, so a mark
// nobody chose must not be wider than the ones that were.
func TestTheFallbackMarkIsAsWideAsTheRest(t *testing.T) {
	m, _ := a5Agents(t, false)
	for _, icons := range []string{"text", "nerd"} {
		m.cfg.UI.Icons = icons
		known, _ := m.agentMark(proto.PaneInfo{Agent: &proto.AgentStatus{Name: "claude"}})
		other, _ := m.agentMark(proto.PaneInfo{Agent: &proto.AgentStatus{Name: "nobody-knows"}})
		if w1, w2 := ansi.StringWidth(known), ansi.StringWidth(other); w1 != w2 {
			t.Errorf("icons = %s: a known mark is %d cells, an unknown one %d", icons, w1, w2)
		}
	}
}

// The menu says what it can do: an agent conch has no installer for is
// not offered as an install, because running an empty script and saying
// "installed" is what that used to do.
func TestAgentMenuDoesNotOfferAnInstallItCannotDo(t *testing.T) {
	m, _ := a5Agents(t, false)
	mu := newAgentMenu(*m, m.machines[0])
	plainLabels := a2Plain(labelsOf(mu.items))
	for _, want := range []string{"Start Claude Code", "Install Codex", "Start Kilo Code", "Install Aider"} {
		if !strings.Contains(plainLabels, want) {
			t.Errorf("the menu does not offer %q:\n%s", want, plainLabels)
		}
	}
	if strings.Contains(plainLabels, "Install Home Grown") {
		t.Errorf("it offered an install it cannot do:\n%s", plainLabels)
	}
	if !strings.Contains(plainLabels, "Home Grown") || !strings.Contains(plainLabels, "no installer") {
		t.Errorf("it does not say why Home Grown cannot be installed:\n%s", plainLabels)
	}
	// Choosing that row says what to do instead, and starts nothing.
	var at int
	for i, it := range mu.items {
		if strings.Contains(a2Plain([]string{it.label}), "Home Grown") {
			at = i
		}
	}
	if cmd := mu.items[at].run(m); cmd != nil {
		t.Errorf("it did something: %#v", a2Run(cmd))
	}
	if !strings.Contains(m.flash, "PATH") {
		t.Errorf("the flash was %q", m.flash)
	}
	// It is not given a digit either: a digit is for something that acts,
	// and a row that keeps one leaves a hole in the count.
	if mu.items[at].key != "" {
		t.Errorf("the row that cannot act has the key %q", mu.items[at].key)
	}
	var digits []string
	for _, it := range mu.items {
		if it.key != "" && it.key != "n" {
			digits = append(digits, it.key)
		}
	}
	if strings.Join(digits, "") != "1234" {
		t.Errorf("the digits are %v, not 1 2 3 4 in order", digits)
	}
}

// An agent installed while conch was running: the list is from when the
// machine connected, so the menu said "Install" and starting it asked to
// install it again. Reported from use: "agent is installed but it not
// opened in first time".
func TestAgentMenuAsksForAFreshList(t *testing.T) {
	m, peer := a5Agents(t, true)
	// Opening the menu asks the machine again.
	cmd := a1Key(t, m, a2Key("c"))
	if _, ok := m.overlay.(*menu); !ok {
		t.Fatalf("c did not open the agent menu: %#v", m.overlay)
	}
	a2Run(cmd)
	peer.waitMethod(t, proto.MethodAgentStatus, "")

	// Aider was installed in the meantime; the answer arrives while the
	// menu is open, and the menu is redrawn rather than left stale.
	mu := m.overlay.(*menu)
	if !strings.Contains(a2Plain(labelsOf(mu.items)), "Install Aider") {
		t.Fatalf("the menu did not start out offering an install:\n%s", a2Plain(labelsOf(mu.items)))
	}
	fresh := append([]proto.AgentAvailability(nil), m.machines[0].agentList...)
	for i := range fresh {
		if fresh[i].Name == "aider" {
			fresh[i].Installed, fresh[i].Version = true, "0.86.2"
		}
	}
	next, _ := m.Update(agentStatusMsg{machine: localMachine, gen: m.machines[0].gen, agents: fresh})
	*m = next.(Model)
	after, ok := m.overlay.(*menu)
	if !ok {
		t.Fatalf("the menu was closed by a fresh list: %#v", m.overlay)
	}
	plain := a2Plain(labelsOf(after.items))
	if !strings.Contains(plain, "Start Aider") || strings.Contains(plain, "Install Aider") {
		t.Errorf("the menu was not redrawn for the fresh list:\n%s", plain)
	}
	// Another machine's list does not disturb it.
	m.overlay = after
	next, _ = m.Update(agentStatusMsg{machine: "elsewhere", agents: fresh})
	*m = next.(Model)
	if m.overlay != after {
		t.Errorf("another machine's list rebuilt this machine's menu")
	}
}

// Starting an agent conch cannot install says so instead of offering an
// installer that would do nothing.
func TestAskInstallSaysWhenThereIsNoInstaller(t *testing.T) {
	m, _ := a5Agents(t, false)
	next, _ := m.Update(askInstallMsg{machine: localMachine, agent: "homegrown"})
	*m = next.(Model)
	if m.overlay != nil {
		t.Errorf("it offered something: %#v", m.overlay)
	}
	if !strings.Contains(m.flash, "Home Grown") || !strings.Contains(m.flash, "no installer") {
		t.Errorf("the flash was %q", m.flash)
	}
	// One conch can install is still offered, by its own label.
	next, _ = m.Update(askInstallMsg{machine: localMachine, agent: "aider"})
	*m = next.(Model)
	c, ok := m.overlay.(*dialog)
	if !ok {
		t.Fatalf("no question for an agent conch can install: %#v", m.overlay)
	}
	if !strings.Contains(a2Plain(c.render(*m).lines), "Aider") {
		t.Errorf("the question does not name the agent:\n%s", a2Plain(c.render(*m).lines))
	}
}

// Every agent conch ships has all three of dot, glyph and colour, and no
// two share a mark (AGENTS.md, Adding an agent · Marks). A mark missing
// from one of the maps is a blank in the tree, or a glyph in nobody's
// colour, for exactly one agent — which is the kind of thing nobody
// notices until they use that agent.
func TestEveryShippedAgentIsInEveryMarkMap(t *testing.T) {
	shipped := []string{"claude", "codex", "gemini", "opencode", "devin", "aider", "amp", "cursor", "grok", "kilo", "pi"}
	for _, name := range shipped {
		if agentDots[name] == "" {
			t.Errorf("%s has no dot", name)
		}
		if agentGlyphs[name] == "" {
			t.Errorf("%s has no glyph", name)
		}
		if agentColors[name] == "" {
			t.Errorf("%s has no colour", name)
		}
		if agentLabels[name] == "" {
			t.Errorf("%s has no label", name)
		}
	}
	// Nothing in the maps for an agent that is not shipped: a leftover
	// would be a mark nobody can see.
	for _, m := range []map[string]string{agentDots, agentGlyphs, agentLabels} {
		for name := range m {
			if !slices.Contains(shipped, name) {
				t.Errorf("a mark for %q, which conch does not ship", name)
			}
		}
	}
}

// A glyph above the Font Awesome 4.7 range is not in a Nerd Fonts v2
// font, which draws nothing for it — worse than a plain character
// (AGENTS.md, and the same reason fileicons.go gives).
func TestAgentGlyphsAreDrawableByAnOlderNerdFont(t *testing.T) {
	check := func(name, glyph string) {
		t.Helper()
		r := []rune(glyph)
		if len(r) != 1 {
			t.Errorf("%s's glyph is %d runes, so it is not one cell", name, len(r))
			return
		}
		switch c := r[0]; {
		case c < 0xE000: // an ordinary character every font has
		case c <= 0xF2FF: // Nerd Fonts' Font Awesome 4.7 range
		default:
			t.Errorf("%s's glyph is U+%04X, past the range a v2 font draws", name, c)
		}
	}
	for name, glyph := range agentGlyphs {
		check(name, glyph)
	}
	check("the default", agentGlyphOther)
}

// An agent that puts its own mark at the start of its window title gets
// it drawn twice, since conch draws one of its own in front. Reported
// the moment Pi was first started: "π π - conch".
func TestATitleDoesNotRepeatTheMark(t *testing.T) {
	for _, c := range []struct{ name, label, mark, want string }{
		{"pi's own title", "π - conch", "π ", "conch"},
		{"no separator", "π conch", "π ", "conch"},
		{"a colon", "π: building", "π ", "building"},
		{"an em dash", "π — conch", "π ", "conch"},
		{"the title is only the mark", "π", "π ", "π"},
		{"a different mark", "π - conch", " ", "π - conch"},
		{"no mark at all (icons off)", "π - conch", "", "π - conch"},
		{"a name that merely starts with a letter", "pi - conch", "π ", "pi - conch"},
		{"an emoji mark in the title", "🟥 pi", "🟥 ", "pi"},
		{"nothing to go on", "", "π ", ""},
		{"a mark in the middle is left alone", "conch π", "π ", "conch π"},
		{"a dash the title means", "π --no-tools", "π ", "-no-tools"},
	} {
		if got := undoubleMark(c.label, c.mark); got != c.want {
			t.Errorf("%s: %q with mark %q → %q, wanted %q", c.name, c.label, c.mark, got, c.want)
		}
	}
}

// And the row really draws it once, through the renderer rather than the
// helper alone.
func TestPiRowShowsOneMark(t *testing.T) {
	a2Isolate(t)
	defer applyTheme("conch", "")
	m, _ := a1Fixture(t, false)
	m.cfg.UI.Icons = "nerd"
	m.machines[0].panes[0] = proto.PaneInfo{ID: "p1", Name: "pi", Title: "π - conch", State: proto.PaneRunning,
		ProjectID: "r1", Branch: "feat", Cwd: "/src/api-feat",
		Agent: &proto.AgentStatus{Name: "pi", State: proto.AgentIdle}}
	m.rebuild()
	var row string
	for _, l := range strings.Split(ansi.Strip(m.View()), "\n") {
		if strings.Contains(l, "conch") && strings.Contains(l, "π") {
			row = l
		}
	}
	if row == "" {
		t.Fatalf("no pi row on screen:\n%s", ansi.Strip(m.View()))
	}
	if n := strings.Count(row, "π"); n != 1 {
		t.Errorf("the mark is drawn %d times: %q", n, row)
	}
}

// With a dozen agents the numbers are worth more on the ones somebody
// is choosing between, so what can be started comes first and the
// installs follow. Reported from use: "when press c it shows only 1-9,
// kilo and pi don't have number".
func TestAgentMenuNumbersWhatCanBeStarted(t *testing.T) {
	m, _ := a5Agents(t, false)
	var list []proto.AgentAvailability
	// Three that need installing, then eight that are ready: the order a
	// machine might well give them.
	for _, n := range []string{"amp", "cursor", "grok"} {
		list = append(list, proto.AgentAvailability{Name: n, Label: n, Tier: proto.TierRunsHere})
	}
	for _, n := range []string{"claude", "codex", "gemini", "opencode", "devin", "aider", "kilo", "pi"} {
		list = append(list, proto.AgentAvailability{Name: n, Label: n, Installed: true})
	}
	m.machines[0].agentList = list
	mu := newAgentMenu(*m, m.machines[0])

	var order, keys []string
	for _, it := range mu.items {
		plain := a2Plain([]string{it.label})
		if strings.HasPrefix(plain, "Start ") || strings.HasPrefix(plain, "Install ") {
			order = append(order, plain)
			keys = append(keys, it.key)
		}
	}
	// Everything startable first, in the machine's own order.
	for i, want := range []string{"claude", "codex", "gemini", "opencode", "devin", "aider", "kilo", "pi"} {
		if !strings.HasPrefix(order[i], "Start "+want) {
			t.Fatalf("row %d is %q, wanted Start %s", i, order[i], want)
		}
	}
	// Every one of the eight has a digit, and so do two of the installs:
	// ten, with 0 for the tenth, as the tab keys do.
	if strings.Join(keys[:10], "") != "1234567890" {
		t.Errorf("the first ten keys are %v", keys[:10])
	}
	if keys[10] != "" {
		t.Errorf("an eleventh row took the key %q", keys[10])
	}
	// The digit really starts the agent it is beside.
	m.overlay = mu
	for i, it := range mu.items {
		if it.key == "0" {
			if !strings.HasPrefix(a2Plain([]string{mu.items[i].label}), "Install ") {
				t.Errorf("0 is on %q", a2Plain([]string{it.label}))
			}
		}
	}
}

// A pane is there to type into, as its tab in the bar is: clicking one
// in the tree hands it the keyboard. Reported from use: "if i press any
// key it think its command". A branch or a project keeps the tree, since
// its keys are the point of it.
func TestClickingAPaneTypesIntoIt(t *testing.T) {
	a2Isolate(t)
	m, _ := a1Fixture(t, false)
	m.width, m.height, m.sidebarW = 120, 30, 30
	m.rebuild()
	click := func(id string) {
		t.Helper()
		m.focus = focusSidebar
		// Each one a single click: two on the same row in a row would be
		// a double click, which has always opened and focused the pane.
		m.lastClickID, m.lastClickAt = "", time.Time{}
		i := indexOfRow(m.rows, id)
		if i < 0 {
			t.Fatalf("no row %q in\n%s", id, render(m.rows))
		}
		next, _ := m.sidebarMouse(tea.MouseMsg{X: 5, Y: 2 + i - m.scroll,
			Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}, true, true, false)
		*m = next.(Model)
	}
	click("pane:p1") // an agent
	if m.focus != focusMain {
		t.Errorf("clicking an agent left the keyboard in the tree")
	}
	click("b:r1:feat") // a branch: b, c, n, t are what it is for
	if m.focus != focusSidebar {
		t.Errorf("clicking a branch took the keyboard away from the tree")
	}
	click("pane:p2") // a terminal
	if m.focus != focusMain {
		t.Errorf("clicking a terminal left the keyboard in the tree")
	}
	// A pane that has exited has nothing to type into.
	m.machines[0].panes[1].State = proto.PaneExited
	m.rebuild()
	click("pane:p2")
	if m.focus != focusSidebar {
		t.Errorf("clicking an exited pane gave it the keyboard")
	}
}
