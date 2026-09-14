package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

func a2SessionsModel() (*Model, *sessionsView) {
	m := a2Model()
	now := time.Now()
	m.sessions = map[string]*sessionsData{sessionsKey(localMachine, "r1"): {at: now, list: []proto.SessionInfo{
		{Agent: "claude", ID: "s1", Dir: "/src/api", Title: "Refactor auth", Updated: now.Add(-2 * time.Hour)},
		{Agent: "codex", ID: "s2", Dir: "/src/api-feat", Branch: "feat", Title: "Add feature", Updated: now.Add(-3 * time.Minute), Interrupted: true},
		{Agent: "claude", ID: "s3", Dir: "/elsewhere", Title: "Open one", PaneID: "p1", Updated: now},
		{Agent: "opencode", Dir: "/src/api", Title: "Lost run", Interrupted: true, Updated: now.Add(-40 * 24 * time.Hour)},
	}}}
	return m, &sessionsView{machine: localMachine, projectID: "r1"}
}

func TestA2SessionsFilterAndMove(t *testing.T) {
	m, sv := a2SessionsModel()
	if got := sv.agents(*m); strings.Join(got, ",") != "claude,codex,opencode" {
		t.Fatalf("agents: %v", got)
	}
	// a cycles through the agents present, then back to all.
	for _, want := range []string{"claude", "codex", "opencode", ""} {
		sv.sel = 1
		sv.key(m, a2Key("a"))
		if sv.agent != want || sv.sel != 0 {
			t.Fatalf("filter %q (sel %d), want %q", sv.agent, sv.sel, want)
		}
	}
	sv.agent = "claude"
	if list := sv.visible(*m); len(list) != 2 || list[1].ID != "s3" {
		t.Fatalf("claude sessions: %+v", list)
	}
	sv.agent = ""

	for _, step := range []struct {
		key  string
		want int
	}{{"down", 1}, {"j", 2}, {"down", 3}, {"down", 3}, {"up", 2}, {"k", 1}, {"g", 0}, {"G", 3}, {"home", 0}, {"end", 3}, {"pgup", 0}, {"pgdown", 3}} {
		if back, _ := sv.key(m, a2Key(step.key)); back || sv.sel != step.want {
			t.Fatalf("after %s sel %d, want %d", step.key, sv.sel, step.want)
		}
	}
	for _, k := range []string{"esc", "q", "left", "h", "tab"} {
		if back, _ := sv.key(m, a2Key(k)); !back {
			t.Fatalf("%s should go back to the tree", k)
		}
	}

	// Mouse: the wheel moves, a click selects, a click on the selection resumes.
	sv.sel = 0
	sv.mouse(m, tea.MouseMsg{Button: tea.MouseButtonWheelDown}, 0, 0)
	sv.mouse(m, tea.MouseMsg{Button: tea.MouseButtonWheelDown}, 0, 0)
	sv.mouse(m, tea.MouseMsg{Button: tea.MouseButtonWheelUp}, 0, 0)
	if sv.sel != 1 {
		t.Fatalf("wheel sel %d", sv.sel)
	}
	if cmd := sv.mouse(m, tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}, 5, sessionsListTop+2); cmd != nil || sv.sel != 2 {
		t.Fatalf("click selects: sel %d", sv.sel)
	}
	if sv.mouse(m, tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}, 5, 0) != nil || sv.sel != 2 {
		t.Fatal("a click on the header does nothing")
	}
	if sv.mouse(m, tea.MouseMsg{Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft}, 5, sessionsListTop) != nil || sv.sel != 2 {
		t.Fatal("a release does nothing")
	}
	msgs := a2Run(sv.mouse(m, tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}, 5, sessionsListTop+2))
	if c, ok := msgs[0].(createdMsg); !ok || c.info.ID != "p1" {
		t.Fatalf("clicking an open session shows its pane: %#v", msgs)
	}
}

func TestA2SessionsResume(t *testing.T) {
	m, sv := a2SessionsModel()
	// Enter on an open session shows its pane.
	sv.sel = 2
	_, cmd := sv.key(m, a2Key("enter"))
	if c, ok := a2Run(cmd)[0].(createdMsg); !ok || c.info.ID != "p1" || c.machine != localMachine {
		t.Fatalf("resume an open session: %#v", c)
	}
	// A session whose agent isn't installed asks to install it.
	m.machines[0].available = map[string]proto.AgentAvailability{"claude": {Name: "claude", Installed: true}}
	sv.sel = 1
	_, cmd = sv.key(m, a2Key("l"))
	if ask, ok := a2Run(cmd)[0].(askInstallMsg); !ok || ask.agent != "codex" {
		t.Fatalf("resume with codex missing: %#v", ask)
	}
	// Otherwise it asks the server, which isn't connected here.
	sv.sel = 0
	_, cmd = sv.key(m, a2Key("right"))
	if msg := a2ErrText(a2Run(cmd)); msg != "local is online" {
		t.Fatalf("resume offline: %q", msg)
	}
	// An open session whose pane is gone is resumed again.
	stale := proto.SessionInfo{Agent: "claude", ID: "s9", PaneID: "gone"}
	if msg := a2ErrText(a2Run(m.resumeSession(localMachine, "r1", stale))); msg != "local is online" {
		t.Fatalf("resume a session whose pane closed: %q", msg)
	}

	// I resumes every interrupted session not open in a pane.
	m.machines[0].available = nil
	_, cmd = sv.key(m, a2Key("I"))
	if cmd == nil {
		t.Fatal("I with interrupted sessions")
	}
	sv.agent = "claude"
	sv.key(m, a2Key("I"))
	if m.flash != "no interrupted sessions" {
		t.Fatalf("I without interrupted sessions: flash %q", m.flash)
	}
}

func TestA2SessionsDeleteDismissCopy(t *testing.T) {
	m, sv := a2SessionsModel()
	for _, c := range []struct {
		sel  int
		want string
	}{
		{2, "that session is open in a pane; close it first"},
		{3, "this interrupted run has no saved conversation; x dismisses it"},
		{0, "the server there predates deleting sessions; reload it"},
	} {
		sv.sel, m.flash = c.sel, ""
		if _, cmd := sv.key(m, a2Key("d")); cmd != nil || m.flash != c.want || m.overlay != nil {
			t.Fatalf("delete %d: flash %q overlay %v", c.sel, m.flash, m.overlay)
		}
	}
	// With a server that can delete, d asks first.
	m.machines[0].c = a2Client("session.delete.v1")
	sv.sel = 0
	sv.key(m, a2Key("delete"))
	d, ok := m.overlay.(*dialog)
	if !ok || !d.confirm || d.text[0] != "Delete the Claude Code session “Refactor auth”? moves its files to the Trash." {
		t.Fatalf("delete confirm: %#v", m.overlay)
	}
	d.update(m, a2Key("n"))
	if m.overlay != nil {
		t.Fatal("no keeps the session")
	}
	m.sessions[sessionsKey(localMachine, "r1")].list[3].ID = "oc1"
	sv.sel = 3
	sv.key(m, a2Key("d"))
	if d := m.overlay.(*dialog); !strings.HasSuffix(d.text[0], "OpenCode deletes it.") {
		t.Fatalf("opencode delete text: %q", d.text[0])
	}
	m.overlay = nil
	// Confirming without a connection reports it.
	sv.key(m, a2Key("d"))
	d = m.overlay.(*dialog)
	m.machines[0].c = nil
	_, cmd := d.update(m, a2Key("y"))
	if msg := a2ErrText(a2Run(cmd)); msg != "local is online" {
		t.Fatalf("delete offline: %q", msg)
	}

	// x dismisses only interrupted sessions.
	sv.sel = 0
	if _, cmd := sv.key(m, a2Key("x")); cmd != nil {
		t.Fatal("x on a normal session")
	}
	sv.sel = 1
	if _, cmd := sv.key(m, a2Key("x")); a2ErrText(a2Run(cmd)) != "local is online" {
		t.Fatal("x on an interrupted session asks the server")
	}
	// y copies a session ID; nothing without one.
	sv.sel = 0
	if _, cmd := sv.key(m, a2Key("y")); cmd == nil {
		t.Fatal("y should copy the session ID")
	}
	m.sessions[sessionsKey(localMachine, "r1")].list[3].ID = ""
	sv.sel = 3
	if _, cmd := sv.key(m, a2Key("y")); cmd != nil {
		t.Fatal("y without a session ID")
	}
	// d with nothing listed.
	sv.agent = "gemini"
	if _, cmd := sv.key(m, a2Key("d")); cmd != nil || m.overlay != nil {
		t.Fatal("d on an empty list")
	}
	// R reloads only from a server that lists sessions.
	if _, cmd := sv.key(m, a2Key("R")); cmd != nil {
		t.Fatal("R without a connection")
	}
}

func TestA2SessionsLoadAndReceive(t *testing.T) {
	m := a2Model()
	if m.loadSessions(localMachine, "r1", true) != nil || m.sessions == nil || m.hasSessions(localMachine) {
		t.Fatal("no connection: nothing to load")
	}
	m.machines[0].c = a2Client()
	if m.loadSessions(localMachine, "r1", true) != nil || m.hasSessions(localMachine) {
		t.Fatal("a server without session.v1")
	}
	m.machines[0].c = a2Client("session.v1")
	if !m.hasSessions(localMachine) || !m.hasCapability(localMachine, "session.v1") || m.hasCapability(localMachine, "x") || m.hasCapability("nope", "session.v1") {
		t.Fatal("capabilities")
	}
	key := sessionsKey(localMachine, "r1")
	if key != "local|r1" {
		t.Fatalf("key %q", key)
	}
	// The command calls the server, so it is not run.
	if m.loadSessions(localMachine, "r1", false) == nil || !m.sessions[key].loading {
		t.Fatal("first load")
	}
	if m.loadSessions(localMachine, "r1", true) != nil {
		t.Fatal("a load in flight is not repeated")
	}
	m.receiveSessions(sessionsMsg{key: key, err: errors.New("boom")})
	d := m.sessions[key]
	if d.loading || d.err != "boom" || d.at.IsZero() {
		t.Fatalf("error reply: %+v", d)
	}
	m.receiveSessions(sessionsMsg{key: key, list: []proto.SessionInfo{{Agent: "claude", Interrupted: true}}})
	if d.err != "" || len(d.list) != 1 || d.interrupted() != 1 {
		t.Fatalf("list reply: %+v", d)
	}
	if m.loadSessions(localMachine, "r1", false) != nil {
		t.Fatal("a fresh list is not reloaded")
	}
	m.receiveSessions(sessionsMsg{key: "other|x"}) // unknown keys are ignored
	if _, ok := m.sessions["other|x"]; ok {
		t.Fatal("reply for an unknown key stored")
	}

	// A stale mark reloads through Update; a new connection marks lists stale.
	m.machines[0].c = nil
	next, cmd := m.Update(sessionsStaleMsg{key: key})
	if cmd != nil || next.(Model).sessions[key] != d {
		t.Fatal("a stale list without a connection")
	}
}

func TestA2SessionsRender(t *testing.T) {
	m, sv := a2SessionsModel()
	w := 90
	// Nothing loaded, loading, error.
	empty := &sessionsView{machine: localMachine, projectID: "r2"}
	if out := a2Plain(empty.render(*m, w, 20)); !strings.Contains(out, "Sessions · r2") || !strings.Contains(out, "loading…") {
		t.Fatalf("unloaded:\n%s", out)
	}
	m.sessions[sessionsKey(localMachine, "r2")] = &sessionsData{err: "no such project"}
	if out := a2Plain(empty.render(*m, w, 20)); !strings.Contains(out, "no such project") {
		t.Fatalf("error:\n%s", out)
	}
	m.sessions[sessionsKey(localMachine, "r2")] = &sessionsData{list: []proto.SessionInfo{}}
	if out := a2Plain(empty.render(*m, w, 20)); !strings.Contains(out, "0 saved · all agents") || !strings.Contains(out, "no saved sessions for this project") {
		t.Fatalf("empty:\n%s", out)
	}

	m.focus = focusMain
	lines := sv.render(*m, w, 20)
	out := a2Plain(lines)
	for _, want := range []string{"Sessions · api", "4 saved · all agents", "⚠ 2 interrupted", "▸ · Claude Code  Refactor auth", "2h",
		"feat · interrupted · 3m", "/elsewhere · open · now", time.Now().Add(-40 * 24 * time.Hour).Format("Jan 2006"), "OpenCode"} {
		if !strings.Contains(out, want) {
			t.Fatalf("render lacks %q:\n%s", want, out)
		}
	}
	for i, l := range lines[3:] {
		if lw := ansi.StringWidth(l); lw != w {
			t.Fatalf("row %d is %d wide:\n%s", i, lw, out)
		}
	}
	sv.agent = "codex"
	if out := a2Plain(sv.render(*m, w, 20)); !strings.Contains(out, "1 saved · Codex") {
		t.Fatalf("filtered:\n%s", out)
	}
	// A short view scrolls to keep the selection visible.
	sv.agent, sv.sel = "", 3
	lines = sv.render(*m, w, sessionsListTop+2)
	if sv.scroll != 2 || len(lines) != sessionsListTop+2 || !strings.Contains(ansi.Strip(lines[len(lines)-1]), "Lost run") {
		t.Fatalf("scroll %d:\n%s", sv.scroll, a2Plain(lines))
	}
	sv.sel = 0
	sv.render(*m, w, sessionsListTop+2)
	if sv.scroll != 0 {
		t.Fatalf("scroll back up: %d", sv.scroll)
	}
	// Very narrow views don't panic.
	sv.render(*m, 10, 3)
}

func TestA2ProjectSessionLines(t *testing.T) {
	m, _ := a2SessionsModel()
	proj := m.machines[0].projects[0]
	if m.projectSessionLines(localMachine, proto.ProjectInfo{ID: "none"}, 80) != nil {
		t.Fatal("no sessions block before sessions load")
	}
	out := a2Plain(m.projectSessionLines(localMachine, proj, 80))
	for _, want := range []string{"Sessions  4", "⚠ 2 interrupted", "Claude Code  Refactor auth", "feat · 3m", "● Claude Code  Open one"} {
		if !strings.Contains(out, want) {
			t.Fatalf("lacks %q:\n%s", want, out)
		}
	}
	d := m.sessions[sessionsKey(localMachine, "r1")]
	for i := 0; i < 4; i++ {
		d.list = append(d.list, proto.SessionInfo{Agent: "gemini", Title: "more", Updated: time.Now()})
	}
	out = a2Plain(m.projectSessionLines(localMachine, proj, 80))
	if !strings.Contains(out, "… 3 more · select Sessions in the tree to resume") || strings.Count(out, "\n") != 7 {
		t.Fatalf("long list:\n%s", out)
	}
	if joinNonEmpty(" · ", "", "a", "", "b") != "a · b" || joinNonEmpty(",") != "" {
		t.Fatal("joinNonEmpty")
	}
}
