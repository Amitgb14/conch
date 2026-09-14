package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/proto"
)

func typeQuery(t *testing.T, m *Model, sv *sessionsView, s string) tea.Cmd {
	t.Helper()
	var cmd tea.Cmd
	for _, r := range s {
		k := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
		if r == ' ' {
			k = tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
		}
		if back, c := sv.key(m, k); back {
			t.Fatalf("typing %q left the view", r)
		} else {
			cmd = c
		}
	}
	return cmd
}

func visibleIDs(m *Model, sv *sessionsView) string {
	var ids []string
	for _, s := range sv.visible(*m) {
		ids = append(ids, s.Agent+":"+s.ID)
	}
	return strings.Join(ids, ",")
}

func TestSessionsSearchTyping(t *testing.T) {
	m, sv := a2SessionsModel()

	// "/" starts typing; letters that are keys elsewhere (q, h, s, d, a) are text.
	sv.key(m, a2Key("/"))
	if !sv.typing {
		t.Fatal("/ didn't start a search")
	}
	if cmd := typeQuery(t, m, sv, "qhsda"); cmd != nil {
		t.Fatal("no search capability: nothing should be scheduled")
	}
	if sv.query != "qhsda" || m.overlay != nil || visibleIDs(m, sv) != "" {
		t.Fatalf("query %q overlay %v visible %q", sv.query, m.overlay, visibleIDs(m, sv))
	}
	// Backspace edits; ctrl+u empties; backspace on an empty query stops typing.
	sv.key(m, tea.KeyMsg{Type: tea.KeyCtrlU})
	if sv.query != "" || !sv.typing {
		t.Fatalf("ctrl+u: %q typing %v", sv.query, sv.typing)
	}
	typeQuery(t, m, sv, "auth")
	sv.key(m, tea.KeyMsg{Type: tea.KeyBackspace})
	if sv.query != "aut" {
		t.Fatalf("backspace: %q", sv.query)
	}
	for range "aut" {
		sv.key(m, tea.KeyMsg{Type: tea.KeyBackspace})
	}
	sv.key(m, tea.KeyMsg{Type: tea.KeyBackspace})
	if sv.typing {
		t.Fatal("backspace on an empty query should stop typing")
	}

	// Titles, agents (name or label), branches and IDs match; every word must.
	for query, want := range map[string]string{
		"refactor":      "claude:s1",
		"REFACTOR AUTH": "claude:s1",
		"codex":         "codex:s2",
		"claude code":   "claude:s1,claude:s3",
		"feat":          "codex:s2",
		"s3":            "claude:s3",
		"refactor feat": "",
		"lost":          "opencode:", // an interrupted run matches by title
	} {
		sv.setQuery("")
		sv.typing = true
		typeQuery(t, m, sv, query)
		if got := visibleIDs(m, sv); got != want {
			t.Errorf("%q: %q, want %q", query, got, want)
		}
	}

	// The agent filter still applies.
	sv.setQuery("")
	sv.agent = "claude"
	typeQuery(t, m, sv, "open")
	if got := visibleIDs(m, sv); got != "claude:s3" {
		t.Fatalf("with agent filter: %q", got)
	}
	sv.agent = ""

	// enter keeps the results and gives the keys back; esc then clears the
	// search before a second esc leaves the view.
	sv.setQuery("")
	typeQuery(t, m, sv, "add")
	sv.sel = 3
	sv.key(m, a2Key("enter"))
	if sv.typing || sv.query != "add" {
		t.Fatalf("enter: typing %v query %q", sv.typing, sv.query)
	}
	if back, _ := sv.key(m, a2Key("esc")); back || sv.query != "" {
		t.Fatalf("first esc: back %v query %q", back, sv.query)
	}
	if back, _ := sv.key(m, a2Key("esc")); !back {
		t.Fatal("second esc should go back")
	}

	// esc while typing clears and stops.
	sv.key(m, a2Key("/"))
	typeQuery(t, m, sv, "x")
	sv.key(m, a2Key("esc"))
	if sv.typing || sv.query != "" {
		t.Fatalf("esc while typing: %v %q", sv.typing, sv.query)
	}

	// Arrows end typing and move.
	sv.key(m, a2Key("/"))
	typeQuery(t, m, sv, "a")
	sv.sel = 0
	if back, _ := sv.key(m, a2Key("down")); back || sv.typing || sv.sel != 1 {
		t.Fatalf("down: typing %v sel %d", sv.typing, sv.sel)
	}
	// Other special keys while typing do nothing.
	sv.typing = true
	before := sv.query
	sv.key(m, tea.KeyMsg{Type: tea.KeyTab})
	if !sv.typing || sv.query != before {
		t.Fatal("tab while typing changed the search")
	}
}

func TestSessionsSearchConversations(t *testing.T) {
	m, sv := a2SessionsModel()
	c, peer := a1FakeClient(t, "session.v1", "session.search.v1")
	m.machines[0].c = c
	leaf := m.newLeaf(viewRef{Row: "sessions", Kind: kindSessions, Machine: localMachine, ProjectID: "r1"})
	leaf.sessions = sv
	m.tabs = []*tab{{root: &layoutNode{leaf: leaf}, focus: leaf.id}}

	sv.key(m, a2Key("/"))
	cmd := typeQuery(t, m, sv, "race")
	if cmd == nil || !sv.searching {
		t.Fatal("typing should schedule a conversation search")
	}
	// A tick for an outdated query does nothing.
	if m.searchSessions(sessionSearchTickMsg{machine: localMachine, projectID: "r1", query: "rac"}) != nil {
		t.Fatal("stale tick searched")
	}
	if m.searchSessions(sessionSearchTickMsg{machine: "box", projectID: "r1", query: "race"}) != nil {
		t.Fatal("tick for another machine searched")
	}
	run := m.searchSessions(sessionSearchTickMsg{machine: localMachine, projectID: "r1", query: "race"})
	if run == nil {
		t.Fatal("current tick didn't search")
	}
	if msg, ok := run().(sessionSearchMsg); !ok || msg.query != "race" || msg.err != nil {
		t.Fatalf("search result: %#v", msg)
	}
	peer.waitMethod(t, proto.MethodSessionSearch, `"query":"race"`)
	peer.waitMethod(t, proto.MethodSessionSearch, `"project_id":"r1"`)

	// Nothing matches locally; the server finds s1 inside its conversation.
	if visibleIDs(m, sv) != "" {
		t.Fatalf("before results: %q", visibleIDs(m, sv))
	}
	next, _ := m.update(sessionSearchMsg{machine: localMachine, projectID: "r1", query: "race",
		list: []proto.SessionInfo{{Agent: "claude", ID: "s1", Snippet: "…found a race in the token refresh…"}, {Agent: "opencode", ID: ""}}})
	*m = next.(Model)
	if got := visibleIDs(m, sv); got != "claude:s1" || sv.searching {
		t.Fatalf("after results: %q searching %v", got, sv.searching)
	}
	out := a2Plain(sv.render(*m, 120, 20))
	if !strings.Contains(out, "“…found a race in the token refresh…”") || !strings.Contains(out, "/ race█") || !strings.Contains(out, "1 found") {
		t.Fatalf("render:\n%s", out)
	}
	// Rows never grow wider than the view, snippet or not.
	for _, w := range []int{30, 60, 120} {
		for i, l := range sv.render(*m, w, 20) {
			if lw := a2Width(l); lw > w {
				t.Fatalf("width %d: line %d is %d wide", w, i, lw)
			}
		}
	}

	// Results for a query that has since changed are ignored.
	typeQuery(t, m, sv, "s")
	m.receiveSessionSearch(sessionSearchMsg{machine: localMachine, projectID: "r1", query: "race", list: []proto.SessionInfo{{Agent: "codex", ID: "s2"}}})
	if sv.hits["codex|s2"] != "" || visibleIDs(m, sv) != "" {
		t.Fatalf("stale results applied: %v", sv.hits)
	}
	// An error is shown.
	m.receiveSessionSearch(sessionSearchMsg{machine: localMachine, projectID: "r1", query: "races", err: errors.New("timed out")})
	if !strings.Contains(a2Plain(sv.render(*m, 120, 20)), "conversations: timed out") {
		t.Fatal("error not shown")
	}
	// Clearing forgets results.
	sv.setQuery("")
	if sv.hits != nil || sv.searchErr != "" || sv.searching {
		t.Fatal("clear kept search state")
	}
	if sv.scheduleSearch(m) != nil {
		t.Fatal("empty query scheduled a search")
	}
	// The status bar switches to search hints while typing.
	m.focus = focusMain
	m.sessionsView = sv
	m.rows = []row{{id: "sessions", kind: kindSessions, machine: localMachine, projectID: "r1"}}
	m.cursor = "sessions"
	sv.typing = true
	if chip, _ := m.statusHints(); !strings.Contains(chip, "SEARCH") {
		t.Fatalf("chip %q", chip)
	}
	sv.typing = false
	if chip, items := m.statusHints(); !strings.Contains(chip, "SESSIONS") || !strings.Contains(a2ItemsText(items), "/ search") {
		t.Fatalf("chip %q", chip)
	}
	// No matches: a hint to clear.
	sv.setQuery("zzz")
	if !strings.Contains(a2Plain(sv.render(*m, 120, 20)), "no sessions match") {
		t.Fatal("empty search result text")
	}
}

func TestSessionsShareMenu(t *testing.T) {
	m, sv := a2SessionsModel()
	mach := m.machines[0]

	// Without the capability, or for a run with nothing saved: a message.
	mach.c = a2Client("session.v1")
	sv.sel = 0
	sv.key(m, a2Key("s"))
	if m.overlay != nil || !strings.Contains(m.flash, "predates sharing") {
		t.Fatalf("old server: %q", m.flash)
	}
	c, peer := a1FakeClient(t, "session.v1", "session.share.v1")
	mach.c = c
	sv.sel = 3 // "Lost run": no ID
	sv.key(m, a2Key("s"))
	if m.overlay != nil || !strings.Contains(m.flash, "no saved conversation") {
		t.Fatalf("no ID: %q", m.flash)
	}

	// Installed agents first, then agents running in the project (not the
	// session's own pane, not shells, not other projects).
	mach.agentList = []proto.AgentAvailability{{Name: "claude", Label: "Claude Code", Installed: true}, {Name: "codex", Installed: true}, {Name: "gemini", Installed: false}}
	mach.panes = append(mach.panes,
		proto.PaneInfo{ID: "p5", Name: "codex", State: proto.PaneRunning, ProjectID: "r1", Branch: "feat", Agent: &proto.AgentStatus{Name: "codex"}},
		proto.PaneInfo{ID: "p6", Name: "old", State: proto.PaneExited, ProjectID: "r1", Agent: &proto.AgentStatus{Name: "codex"}},
		proto.PaneInfo{ID: "p7", Name: "other", State: proto.PaneRunning, ProjectID: "r9", Agent: &proto.AgentStatus{Name: "claude"}},
	)
	sv.sel = 2 // s3, open in p1
	sv.key(m, a2Key("s"))
	mu, ok := m.overlay.(*menu)
	if !ok {
		t.Fatalf("no menu: %q", m.flash)
	}
	var labels []string
	for _, it := range mu.items {
		labels = append(labels, it.key+" "+a2Strip(it.label))
	}
	if got := strings.Join(labels, " | "); got != "1 Start Claude Code with it | 2 Start Codex with it |  Send to codex  feat" {
		t.Fatalf("items: %s", got)
	}
	if !strings.Contains(mu.title, "Open one") {
		t.Fatalf("title %q", mu.title)
	}

	// Choosing an agent shares; the result shows the pane with a note.
	cmd := mu.run(m, 1)
	msgs := a2Run(cmd)
	peer.waitMethod(t, proto.MethodSessionShare, `"to":"codex"`)
	peer.waitMethod(t, proto.MethodSessionShare, `"id":"s3"`)
	var created *createdMsg
	stale := false
	for _, msg := range msgs {
		switch v := msg.(type) {
		case createdMsg:
			created = &v
		case sessionsStaleMsg:
			stale = v.key == sessionsKey(localMachine, "r1")
		}
	}
	if created == nil || created.note != "shared with Codex" || !stale {
		t.Fatalf("done: %#v", msgs)
	}

	// Sending to a running agent names the pane.
	sv.key(m, a2Key("s"))
	a2Run(m.overlay.(*menu).run(m, 2))
	peer.waitMethod(t, proto.MethodSessionShare, `"pane_id":"p5"`)

	// An older server that lists no agents: the four conch knows.
	mach.agentList = nil
	mach.panes = nil
	sv.key(m, a2Key("s"))
	if mu := m.overlay.(*menu); len(mu.items) != 4 {
		t.Fatalf("fallback items: %d", len(mu.items))
	}
	m.overlay = nil

	// Nothing installed and nothing running: say so.
	mach.agentList = []proto.AgentAvailability{{Name: "claude", Installed: false}}
	sv.key(m, a2Key("s"))
	if m.overlay != nil || !strings.Contains(m.flash, "no agent to share with") {
		t.Fatalf("nothing: %q", m.flash)
	}
	// An empty list: s does nothing.
	sv.setQuery("zzz")
	m.flash = ""
	sv.key(m, a2Key("s"))
	if m.overlay != nil || m.flash != "" {
		t.Fatal("s on an empty list")
	}
}

func a2Width(s string) int { return len([]rune(a2Strip(s))) }

func a2Strip(s string) string { return a2Plain([]string{s}) }

func a2ItemsText(items []statusItem) string {
	var parts []string
	for _, it := range items {
		parts = append(parts, a2Strip(it.text))
	}
	return strings.Join(parts, " · ")
}

var _ = time.Second
