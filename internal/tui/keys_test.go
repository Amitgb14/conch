package tui

import (
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/proto"
)

// a1EndsInQuit reports whether cmd is a tea.Sequence whose last command
// quits. The commands before it (saving UI state) are not run.
func a1EndsInQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	msg := cmd()
	v := reflect.ValueOf(msg)
	if v.Kind() != reflect.Slice || v.Len() == 0 || !strings.Contains(reflect.TypeOf(msg).String(), "sequenceMsg") {
		return false
	}
	last, ok := v.Index(v.Len() - 1).Interface().(tea.Cmd)
	if !ok || last == nil {
		return false
	}
	_, quit := last().(tea.QuitMsg)
	return quit
}

func TestA1QuitAndDetachKeys(t *testing.T) {
	m, _ := a1Fixture(t, false)
	if !a1EndsInQuit(a1Key(t, m, runes("q"))) {
		t.Fatal("q should save then quit")
	}
	if !a1EndsInQuit(a1Key(t, m, tea.KeyMsg{Type: tea.KeyCtrlC})) {
		t.Fatal("ctrl+c should save then quit")
	}
	if !a1EndsInQuit(a1Prefixed(t, m, runes("d"))) {
		t.Fatal("ctrl+b d should save then quit")
	}
	if m.prefixArmed {
		t.Fatal("prefix stayed armed")
	}
	// From the main area too.
	a1Open(t, m, paneNodeID(localMachine, "p1"))
	m.focus = focusMain
	if !a1EndsInQuit(a1Prefixed(t, m, runes("d"))) {
		t.Fatal("ctrl+b d in a pane should quit")
	}
}

func TestA1TreeCursorKeys(t *testing.T) {
	m, _ := a1Fixture(t, false)
	ids := func() string { return m.cursor }
	a1Key(t, m, runes("j"))
	if ids() != workspaceID(localMachine) {
		t.Fatalf("j: %s", ids())
	}
	a1Key(t, m, runes("j"))
	if ids() != projectNodeID(localMachine, "r1") {
		t.Fatalf("j: %s", ids())
	}
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyDown})
	a1Key(t, m, runes("k"))
	if ids() != projectNodeID(localMachine, "r1") {
		t.Fatalf("down, k: %s", ids())
	}
	a1Key(t, m, runes("G"))
	if ids() != m.rows[len(m.rows)-1].id {
		t.Fatalf("G: %s", ids())
	}
	a1Key(t, m, runes("g"))
	if ids() != m.rows[0].id {
		t.Fatalf("g: %s", ids())
	}
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyEnd})
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyHome})
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyPgDown})
	if ids() != m.rows[len(m.rows)-1].id { // fewer rows than a page
		t.Fatalf("pgdown: %s", ids())
	}
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyPgUp})
	if ids() != m.rows[0].id {
		t.Fatalf("pgup: %s", ids())
	}
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyUp}) // at the top: stays
	if ids() != m.rows[0].id {
		t.Fatalf("up at top: %s", ids())
	}

	// left on an open row folds it; right opens it again; left on a leaf
	// goes to its parent; l on an open row moves down.
	proj := projectNodeID(localMachine, "r1")
	m.cursor = proj
	a1Key(t, m, runes("h"))
	if m.expanded[proj] || indexOfRow(m.rows, sectionID(localMachine, "r1", "agents")) >= 0 {
		t.Fatal("h did not fold the project")
	}
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyRight})
	if !m.expanded[proj] || indexOfRow(m.rows, sectionID(localMachine, "r1", "agents")) < 0 {
		t.Fatal("right did not open the project")
	}
	a1Key(t, m, runes("l"))
	if ids() != sectionID(localMachine, "r1", "branches") {
		t.Fatalf("l on an open row: %s", ids())
	}
	m.cursor = paneNodeID(localMachine, "p2")
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyLeft})
	if ids() != sectionID(localMachine, "r1", "terminals") {
		t.Fatalf("left on a leaf: %s", ids())
	}
	// space toggles.
	a1Key(t, m, tea.KeyMsg{Type: tea.KeySpace})
	if m.expanded[sectionID(localMachine, "r1", "terminals")] {
		t.Fatal("space did not fold")
	}
	// left on the top machine row with nothing above does nothing.
	m.cursor = machineID(localMachine)
	m.expanded[machineID(localMachine)] = false
	m.rebuild()
	a1Key(t, m, runes("h"))
	if ids() != machineID(localMachine) {
		t.Fatalf("h on a folded root: %s", ids())
	}
}

func TestA1TreeEnterTabAndSplitKeys(t *testing.T) {
	m, _ := a1Fixture(t, false)
	// enter on a project folds it (not a pane)
	m.cursor = projectNodeID(localMachine, "r1")
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.focus != focusSidebar || m.expanded[projectNodeID(localMachine, "r1")] {
		t.Fatal("enter on a project should fold it")
	}
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	// tab on a project does nothing
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyTab})
	if m.focus != focusSidebar {
		t.Fatal("tab on a project focused the main area")
	}
	// enter on a pane shows it and focuses the main area.
	m.cursor = paneNodeID(localMachine, "p1")
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.focus != focusMain || m.tab().focused().view.PaneID != "p1" {
		t.Fatalf("enter on a pane: focus %v", m.focus)
	}
	m.focus = focusSidebar
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyTab})
	if m.focus != focusMain {
		t.Fatal("tab on a pane should focus the main area")
	}
	m.focus = focusSidebar
	// v on p2 joins it beside p1; s on p3 stacks it; O on p3 breaks it out.
	m.cursor = paneNodeID(localMachine, "p2")
	a1Key(t, m, runes("v"))
	m.cursor = paneNodeID(localMachine, "p3")
	a1Key(t, m, runes("s"))
	if n := len(m.tab().root.leaves()); n != 3 || m.tab().root.dir != splitRight {
		t.Fatalf("v then s: %d leaves", n)
	}
	a1Key(t, m, runes("O"))
	if len(m.tabs) != 2 || len(m.tabs[0].root.leaves()) != 2 || m.tab().focused().view.PaneID != "p3" {
		t.Fatalf("O: %d tabs", len(m.tabs))
	}
	// Without a selected row v/s/O do nothing.
	m.cursor = "nope"
	before := len(m.tabs)
	a1Key(t, m, runes("O"))
	if len(m.tabs) != before {
		t.Fatal("O without a row opened a tab")
	}
	// enter on "… more" lists every branch.
	m.cursor = paneNodeID(localMachine, "p1")
	m.activate(row{id: "more:r1", kind: kindMore, machine: localMachine, projectID: "r1"})
	if !m.showAll["r1"] {
		t.Fatal("more did not show all")
	}
}

func TestA1FilterKeys(t *testing.T) {
	m, _ := a1Fixture(t, false)
	a1Key(t, m, runes("/"))
	if !m.filtering {
		t.Fatal("/ did not start filtering")
	}
	a1Key(t, m, runes("co"))
	a1Key(t, m, tea.KeyMsg{Type: tea.KeySpace})
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyBackspace})
	a1Key(t, m, runes("d"))
	if m.filter != "cod" {
		t.Fatalf("filter %q", m.filter)
	}
	if indexOfRow(m.rows, paneNodeID(localMachine, "p4")) < 0 || indexOfRow(m.rows, paneNodeID(localMachine, "p2")) >= 0 {
		t.Fatalf("filtered rows:\n%s", render(m.rows))
	}
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyCtrlA}) // ignored
	if m.filter != "cod" || !m.filtering {
		t.Fatal("ctrl+a changed the filter")
	}
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.filtering || m.filter != "cod" {
		t.Fatal("enter keeps the filter and stops typing")
	}
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.filter != "" {
		t.Fatal("esc in the tree clears the filter")
	}
	// backspace on an empty filter, esc while typing, up leaves typing.
	a1Key(t, m, runes("/"))
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyBackspace})
	a1Key(t, m, runes("x"))
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.filtering || m.filter != "" {
		t.Fatal("esc while typing clears and stops")
	}
	a1Key(t, m, runes("/"))
	m.cursor = m.rows[1].id
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if m.filtering || m.cursor != m.rows[0].id {
		t.Fatalf("up while typing: filtering %v cursor %s", m.filtering, m.cursor)
	}
}

func TestA1TreeActionKeysOffline(t *testing.T) {
	m, _ := a1Fixture(t, false)
	m.machines[0].state = stateOffline
	// c and a need the machine.
	a1Key(t, m, runes("c"))
	if !m.flashIsErr || !strings.Contains(m.flash, "local is") || m.overlay != nil {
		t.Fatalf("c offline: %q", m.flash)
	}
	a1Key(t, m, runes("a"))
	if !m.flashIsErr || m.overlay != nil {
		t.Fatalf("a offline: %q", m.flash)
	}
	// n returns a command that reports the machine offline.
	cmd := a1Key(t, m, runes("n"))
	if e, ok := cmd().(errMsg); !ok || !strings.Contains(e.err.Error(), "offline") {
		t.Fatalf("n offline: %#v", cmd())
	}
	// t needs a git project.
	a1Key(t, m, runes("t"))
	if m.flash != "select a git project to start a task" {
		t.Fatalf("t on machine: %q", m.flash)
	}
	m.cursor = projectNodeID(localMachine, "r1")
	a1Key(t, m, runes("t"))
	if _, ok := m.overlay.(*dialog); !ok {
		t.Fatalf("t on a git project: %T", m.overlay)
	}
	m.overlay = nil
	// F: not a git project, then no server.
	m.cursor = machineID(localMachine)
	a1Key(t, m, runes("F"))
	if !strings.Contains(m.flash, "select a git project") {
		t.Fatalf("F on machine: %q", m.flash)
	}
	m.cursor = projectNodeID(localMachine, "r1")
	a1Key(t, m, runes("F"))
	if !strings.Contains(m.flash, "restart the server") {
		t.Fatalf("F offline: %q", m.flash)
	}
	// o on a branch with a PR opens it (not run); without one it says so.
	m.cursor = branchNodeID(localMachine, "r1", "feat")
	if a1Key(t, m, runes("o")) == nil {
		t.Fatal("o on a PR branch returned nothing")
	}
	m.cursor = branchNodeID(localMachine, "r1", "main")
	a1Key(t, m, runes("o"))
	if m.flash != "no pull request for main" {
		t.Fatalf("o without PR: %q", m.flash)
	}
	// S on a non-agent row.
	a1Key(t, m, runes("S"))
	if m.flash != "select an agent to summarise" {
		t.Fatalf("S: %q", m.flash)
	}
	// ! with nobody waiting.
	a1Key(t, m, runes("!"))
	if m.flash != "no agents need you" {
		t.Fatalf("!: %q", m.flash)
	}
	// Every key clears a previous flash first.
	a1Key(t, m, runes("z"))
	if m.flash != "" || !m.zoom {
		t.Fatalf("z: flash %q zoom %v", m.flash, m.zoom)
	}
	a1Key(t, m, runes("z"))
	// Overlays: ?, :, m, M.
	for _, k := range []string{"?", ":", "m", "M"} {
		a1Key(t, m, runes(k))
		if m.overlay == nil {
			t.Fatalf("%s opened no overlay", k)
		}
		m.overlay = nil
	}
	// With an overlay open keys go to it.
	a1Key(t, m, runes("?"))
	a1Key(t, m, runes("j"))
	if m.cursor != branchNodeID(localMachine, "r1", "main") {
		t.Fatal("a key reached the tree under an overlay")
	}
	m.overlay = nil
	// Prefix then an unknown key does nothing; prefix z zooms.
	a1Prefixed(t, m, runes("Q"))
	if m.prefixArmed || m.zoom {
		t.Fatal("unknown prefixed key")
	}
	a1Prefixed(t, m, runes("z"))
	if !m.zoom {
		t.Fatal("prefix z")
	}
}

func TestA1JumpToAttention(t *testing.T) {
	m, _ := a1Fixture(t, false)
	m.machines[0].panes[3].Agent.State = proto.AgentBlocked
	m.filter = "zzz"
	m.rebuild()
	a1Key(t, m, runes("!"))
	if m.filter != "" || m.cursor != paneNodeID(localMachine, "p4") || m.tab().focused().view.PaneID != "p4" {
		t.Fatalf("! went to %q filter %q", m.cursor, m.filter)
	}
}

func TestA1TreeActionKeysOnline(t *testing.T) {
	m, peer := a1Fixture(t, true)
	a1Key(t, m, runes("c"))
	if _, ok := m.overlay.(*menu); !ok {
		t.Fatalf("c online: %T", m.overlay)
	}
	m.overlay = nil
	// a on a server without fs.v1 asks for a path.
	a1Key(t, m, runes("a"))
	if _, ok := m.overlay.(*dialog); !ok {
		t.Fatalf("a on an old server: %T", m.overlay)
	}
	m.overlay = nil
	// R on a project refreshes it.
	m.cursor = projectNodeID(localMachine, "r1")
	a1Key(t, m, runes("R"))
	if m.flash != "refreshing" {
		t.Fatalf("R project: %q", m.flash)
	}
	peer.waitMethod(t, proto.MethodProjectRefresh, `"r1"`)
	// R on a machine reconnects: the connection is dropped and redialled
	// (the redial command is not run).
	m.cursor = machineID(localMachine)
	m.machines[0].failures = 3
	if a1Key(t, m, runes("R")) == nil {
		t.Fatal("R machine returned no command")
	}
	if m.machines[0].c != nil || m.machines[0].failures != 0 || m.machines[0].state != stateConnecting || m.flash != "connecting to local…" {
		t.Fatalf("reconnect: c %v failures %d flash %q", m.machines[0].c, m.machines[0].failures, m.flash)
	}
	if m.reconnect("nope", false) != nil {
		t.Fatal("reconnect unknown machine")
	}
}

func TestA1RenameAndRemove(t *testing.T) {
	t.Setenv("CONCH_HOME", t.TempDir()) // removeMachine edits the machine catalog
	m, _ := a1Fixture(t, false)
	a1Key(t, m, runes("r"))
	if m.flash != "this computer is always called local" || m.overlay != nil {
		t.Fatalf("r on the local machine: %q", m.flash)
	}
	m.cursor = paneNodeID(localMachine, "p2")
	a1Key(t, m, runes("r"))
	if d, ok := m.overlay.(*dialog); !ok || d.title != " Rename " {
		t.Fatalf("r on pane: %#v", m.overlay)
	}
	m.overlay = nil

	confirm := func(want string) *dialog {
		t.Helper()
		d, ok := m.overlay.(*dialog)
		if !ok || !d.confirm || !strings.Contains(d.text[0], want) {
			t.Fatalf("want confirm %q, got %#v", want, m.overlay)
		}
		m.overlay = nil
		return d
	}
	a1Key(t, m, runes("x"))
	confirm("Close zsh?")
	m.cursor = projectNodeID(localMachine, "r1")
	a1Key(t, m, runes("x"))
	confirm("Remove api from the sidebar?")
	m.cursor = branchNodeID(localMachine, "r1", "feat")
	a1Key(t, m, runes("x"))
	confirm("Remove worktree /src/api-feat?")
	m.cursor = branchNodeID(localMachine, "r1", "main")
	a1Key(t, m, runes("x"))
	if m.overlay != nil || m.flash != "only linked worktrees can be removed" {
		t.Fatalf("x on main worktree: %q", m.flash)
	}
	m.cursor = machineID(localMachine)
	a1Key(t, m, runes("x"))
	if m.overlay != nil || m.flash != "this computer can't be removed" {
		t.Fatalf("x on local machine: %q", m.flash)
	}
	// Rows whose data is gone do nothing.
	m.flash = ""
	for _, r := range []row{
		{id: "pane:gone", kind: kindPane, machine: localMachine, paneID: "gone"},
		{id: "b:gone:x", kind: kindBranch, machine: localMachine, projectID: "gone", branch: "x"},
		{id: "p:gone", kind: kindProject, machine: localMachine, projectID: "gone"},
	} {
		m.rows = append(m.rows, r)
		m.cursor = r.id
		m.openRemove()
		if m.overlay != nil || m.flash != "" {
			t.Fatalf("x on missing %s: %T %q", r.id, m.overlay, m.flash)
		}
	}

	// A remote machine: confirm removes it from the TUI.
	box := newMachine("box", "buildbox", "me@box")
	m.machines = append(m.machines, box)
	m.rows = append(m.rows, row{id: machineID("box"), kind: kindMachine, machine: "box"})
	m.cursor = machineID("box")
	a1Key(t, m, runes("x"))
	d := confirm("Remove buildbox from conch?")
	d.submit(m, nil)
	if len(m.machines) != 1 || m.machine("box") != nil {
		t.Fatalf("machine not removed: %d", len(m.machines))
	}
	if !m.flashIsErr || !strings.Contains(m.flash, "no machine") {
		t.Fatalf("catalog without the machine should report it: %q", m.flash)
	}
}

func TestA1CopyRow(t *testing.T) {
	m, _ := a1Fixture(t, false)
	// The commands would write to the clipboard, so they are not run.
	for _, r := range []row{
		{kind: kindBranch, branch: "feat"},
		{kind: kindPane, machine: localMachine, paneID: "p1"},
		{kind: kindProject, machine: localMachine, projectID: "r1"},
		{kind: kindAgents, machine: localMachine, projectID: "r1"},
	} {
		if m.copyRow(r) == nil {
			t.Errorf("copyRow(%v) returned nothing", r.kind)
		}
	}
	for _, r := range []row{
		{kind: kindMachine, machine: localMachine},
		{kind: kindPane, machine: localMachine, paneID: "gone"},
		{kind: kindProject, machine: localMachine, projectID: "gone"},
	} {
		if m.copyRow(r) != nil {
			t.Errorf("copyRow(%v) returned a command", r.kind)
		}
	}
	// Copying nothing is a no-op with no side effects.
	if copyText("")() != nil {
		t.Fatal("copyText of empty text")
	}
	if a1Key(t, m, runes("y")) != nil {
		t.Fatal("y on the machine row copies nothing")
	}
}

func TestA1ServerHelpers(t *testing.T) {
	m, _ := a1Fixture(t, false)
	if m.restartServer(localMachine) != nil || m.reloadServer(localMachine) != nil || m.canReload(localMachine) || m.canReload("nope") {
		t.Fatal("server helpers without a connection")
	}
	c, peer := a1FakeClient(t, "server.reload.v1")
	m.machines[0].c = c
	if !m.canReload(localMachine) {
		t.Fatal("canReload with the capability")
	}
	if msg := m.restartServer(localMachine)(); msg != flashMsg("restarting the server on local") {
		t.Fatalf("restart: %#v", msg)
	}
	peer.waitMethod(t, proto.MethodServerStop, "")
	peer.setError(proto.MethodServerStop, "nope")
	if _, ok := m.restartServer(localMachine)().(errMsg); !ok {
		t.Fatal("restart error")
	}
	peer.setError(proto.MethodServerReload, "no reload")
	cmd := m.reloadServerInto(localMachine, "/bin/new")
	if m.flash != "reloading the server on local…" {
		t.Fatalf("reload flash %q", m.flash)
	}
	if e, ok := cmd().(errMsg); !ok || !strings.Contains(e.err.Error(), "no reload") {
		t.Fatalf("reload error: %#v", e)
	}
	peer.waitMethod(t, proto.MethodServerReload, `/bin/new`)
	c2, _ := a1FakeClient(t)
	m.machines[0].c = c2
	if m.canReload(localMachine) {
		t.Fatal("canReload without the capability")
	}
	if cwdOrHome() == "" {
		t.Fatal("cwdOrHome")
	}
}

func TestA1MainKeysForwardToPane(t *testing.T) {
	m, peer := a1Fixture(t, true)
	c := m.machines[0].c
	a1Open(t, m, paneNodeID(localMachine, "p1"))
	m.focus = focusMain
	if m.viewing != "p1" {
		t.Fatalf("viewing %q", m.viewing)
	}
	m.sel = &selection{paneID: "p1"}
	a1Key(t, m, runes("hi"))
	peer.waitMethod(t, proto.MethodPaneSendText, `"text":"hi"`)
	if m.sel != nil {
		t.Fatal("typing keeps the selection")
	}
	// Typing while scrolled back returns to the live screen.
	m.frame = &proto.Frame{ID: "p1", History: 100}
	m.offset = 10
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	peer.waitMethod(t, proto.MethodPaneScroll, `"offset":0`)
	peer.waitMethod(t, proto.MethodPaneSendKeys, `"enter"`)
	if m.offset != 0 {
		t.Fatalf("offset %d", m.offset)
	}
	// Prefix twice sends the prefix itself.
	a1Prefixed(t, m, tea.KeyMsg{Type: tea.KeyCtrlB})
	peer.waitMethod(t, proto.MethodPaneSendKeys, `"ctrl+b"`)
	if m.focus != focusMain {
		t.Fatal("prefix prefix left the pane")
	}
	// Prefix and an unhandled key goes back to the tree.
	a1Prefixed(t, m, runes("Q"))
	if m.focus != focusSidebar {
		t.Fatal("prefix Q should return to the tree")
	}
	m.focus = focusMain
	a1Prefixed(t, m, runes("z"))
	if !m.zoom {
		t.Fatal("prefix z in a pane")
	}
	a1Prefixed(t, m, runes("z"))
	a1Prefixed(t, m, runes("!"))
	if m.flash != "no agents need you" {
		t.Fatalf("prefix !: %q", m.flash)
	}
	// Scroll mode needs a frame; pgup also pages back.
	m.frame = nil
	a1Prefixed(t, m, runes("["))
	if m.scrollMode {
		t.Fatal("scroll mode without a frame")
	}
	m.frame = &proto.Frame{ID: "p1", History: 100}
	a1Prefixed(t, m, tea.KeyMsg{Type: tea.KeyPgUp})
	if !m.scrollMode || m.offset == 0 {
		t.Fatalf("prefix pgup: scroll %v offset %d", m.scrollMode, m.offset)
	}
	// An exited pane can't take keys.
	m.scrollMode = false
	m.machines[0].panes[0].State = proto.PaneExited
	a1Key(t, m, runes("x"))
	if m.focus != focusSidebar {
		t.Fatal("keys to an exited pane should return to the tree")
	}
	if n := peer.count(t, c, proto.MethodPaneSendText, `"text":"x"`); n != 0 {
		t.Fatal("key sent to an exited pane")
	}
}

func TestA1MainKeysOtherViews(t *testing.T) {
	m, _ := a1Fixture(t, false)
	// A project page takes no keys.
	a1Open(t, m, projectNodeID(localMachine, "r1"))
	m.focus = focusMain
	a1Key(t, m, runes("j"))
	if m.focus != focusSidebar {
		t.Fatal("project page kept focus")
	}
	// Sessions without their view go back to the tree.
	m.rows = append(m.rows, row{id: sectionID(localMachine, "r1", "sessions"), kind: kindSessions, machine: localMachine, projectID: "r1"})
	m.cursor = sectionID(localMachine, "r1", "sessions")
	m.focus, m.sessionsView = focusMain, nil
	a1Key(t, m, runes("j"))
	if m.focus != focusSidebar {
		t.Fatal("sessions without a view kept focus")
	}
	// A branch's changes: esc goes back, other keys stay.
	a1Open(t, m, branchNodeID(localMachine, "r1", "feat"))
	if m.changes == nil {
		t.Fatal("branch leaf has no changes view")
	}
	m.focus = focusMain
	a1Key(t, m, runes("j"))
	if m.focus != focusMain {
		t.Fatal("j in changes left the view")
	}
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.focus != focusSidebar {
		t.Fatal("esc in changes should return to the tree")
	}
}

func TestA1LayoutKeys(t *testing.T) {
	m, _ := a1Fixture(t, false)
	a1Open(t, m, paneNodeID(localMachine, "p2"))
	m.focus = focusMain
	leaves := func() int { return len(m.tab().root.leaves()) }

	// Split keys: every alias splits once with an empty half to pick for.
	for i, k := range []string{"%", "|", "\"", "_"} {
		m.tab().focus = m.tab().root.leaves()[0].id
		m.machines[0].panes[1].State = proto.PaneExited // no new shell to start
		a1Prefixed(t, m, runes(k))
		if leaves() != i+2 {
			t.Fatalf("%s: %d leaves", k, leaves())
		}
	}
	a1Prefixed(t, m, runes("v"))
	a1Prefixed(t, m, runes("-"))
	if leaves() != 7 {
		t.Fatalf("v, -: %d leaves", leaves())
	}
	// = equalizes every split.
	m.tab().root.ratio = 0.9
	a1Prefixed(t, m, runes("="))
	if m.tab().root.ratio != 0.5 {
		t.Fatalf("= ratio %v", m.tab().root.ratio)
	}
	// o cycles the focus through the splits.
	first := m.tab().focus
	seen := map[int]bool{first: true}
	for i := 0; i < leaves()-1; i++ {
		a1Prefixed(t, m, runes("o"))
		seen[m.tab().focus] = true
	}
	a1Prefixed(t, m, runes("o"))
	if len(seen) != 7 || m.tab().focus != first {
		t.Fatalf("o visited %d leaves, back at %d want %d", len(seen), m.tab().focus, first)
	}
	// x on an empty split closes it at once.
	m.focus = focusMain
	a1Prefixed(t, m, runes("x"))
	if leaves() != 6 {
		t.Fatalf("x: %d leaves", leaves())
	}
	// Arrow and vim focus keys don't panic and stay within the tab.
	for _, k := range []tea.KeyMsg{{Type: tea.KeyUp}, {Type: tea.KeyDown}, {Type: tea.KeyLeft}, {Type: tea.KeyRight}, runes("k"), runes("j"), runes("h")} {
		m.focus = focusMain
		a1Prefixed(t, m, k)
		if m.tab().leaf(m.tab().focus) == nil {
			t.Fatalf("%s lost the focus", k)
		}
	}
	// ? and : open overlays; w and s the tab picker.
	for _, k := range []string{"?", ":", "w", "s"} {
		m.overlay = nil
		a1Prefixed(t, m, runes(k))
		if m.overlay == nil {
			t.Fatalf("prefix %s opened nothing", k)
		}
	}
	m.overlay = nil
	// , renames the tab.
	a1Prefixed(t, m, runes(","))
	d, ok := m.overlay.(*dialog)
	if !ok || d.title != " Rename tab " {
		t.Fatalf(", overlay %#v", m.overlay)
	}
	m.overlay = nil
	d.submit(m, []string{"  work  "})
	if m.tab().name != "work" {
		t.Fatalf("renamed %q", m.tab().name)
	}
	// c opens a tab, n / p step, 1 and 0 go to listed tabs.
	a1Prefixed(t, m, runes("c"))
	if len(m.tabs) != 2 || m.activeTab != 1 {
		t.Fatalf("c: %d tabs active %d", len(m.tabs), m.activeTab)
	}
	a1Prefixed(t, m, runes("p"))
	a1Prefixed(t, m, runes("1"))
	if m.activeTab != 0 {
		t.Fatalf("1: active %d", m.activeTab)
	}
	a1Prefixed(t, m, runes("0"))
	if m.activeTab != 0 {
		t.Fatalf("0 without a tenth tab: active %d", m.activeTab)
	}
	a1Prefixed(t, m, runes("n"))
	if m.activeTab != 1 {
		t.Fatalf("n: active %d", m.activeTab)
	}
	// & on a tab with nothing running closes it.
	a1Prefixed(t, m, runes("&"))
	if len(m.tabs) != 1 {
		t.Fatalf("&: %d tabs", len(m.tabs))
	}
	// & on the preview does nothing.
	m.previewing = true
	if cmd, ok := m.layoutKey("&"); cmd != nil || !ok {
		t.Fatal("& on the preview")
	}
	m.previewing = false
	// S toggles sync (needs two running panes).
	m.machines[0].panes[1].State = proto.PaneRunning
	a1Prefixed(t, m, runes("S"))
	if m.tab().sync || !m.flashIsErr {
		t.Fatal("S with one running pane")
	}
	// ctrl+arrows resize; unknown keys are not handled.
	a1Prefixed(t, m, tea.KeyMsg{Type: tea.KeyCtrlLeft})
	if m.repeatUntil.Before(time.Now()) {
		t.Fatal("ctrl+left did not open the repeat window")
	}
	if _, ok := m.layoutKey("Q"); ok {
		t.Fatal("Q handled as a layout key")
	}
}

func TestA1ScrollModeKeys(t *testing.T) {
	m, peer := a1Fixture(t, true)
	a1Open(t, m, paneNodeID(localMachine, "p1"))
	m.focus = focusMain
	cols, rows := m.paneArea()
	lines := make([]string, rows)
	for i := range lines {
		lines[i] = strings.Repeat(string(rune('a'+i%26)), cols)
	}
	m.frames[paneKey(localMachine, "p1")] = &proto.Frame{ID: "p1", Lines: lines, History: 1000}
	m.syncView()
	m.focus = focusMain
	a1Prefixed(t, m, runes("["))
	if !m.scrollMode || m.curX != 0 || m.curY != rows-1 {
		t.Fatalf("enter scroll mode: %v %d,%d", m.scrollMode, m.curX, m.curY)
	}
	key := func(k tea.KeyMsg) tea.Cmd { return a1Key(t, m, k) }
	key(runes("l"))
	key(tea.KeyMsg{Type: tea.KeyRight})
	key(runes("h"))
	if m.curX != 1 {
		t.Fatalf("curX %d", m.curX)
	}
	key(runes("$"))
	if m.curX != cols-1 {
		t.Fatalf("$: %d", m.curX)
	}
	key(tea.KeyMsg{Type: tea.KeyRight}) // clamps
	if m.curX != cols-1 {
		t.Fatalf("right at the edge: %d", m.curX)
	}
	key(runes("0"))
	key(tea.KeyMsg{Type: tea.KeyLeft}) // clamps
	if m.curX != 0 {
		t.Fatalf("0: %d", m.curX)
	}
	// Down at the bottom scrolls toward live (already live: no change).
	key(runes("j"))
	if m.curY != rows-1 || m.offset != 0 {
		t.Fatalf("down at bottom: y %d offset %d", m.curY, m.offset)
	}
	// v starts a keyboard selection that follows the cursor.
	key(runes("v"))
	if m.sel == nil || !m.sel.keyboard {
		t.Fatal("v did not start a selection")
	}
	key(runes("k"))
	key(runes("l"))
	if m.sel.by != rows-2 || m.sel.bx != 1 || m.sel.ay != rows-1 {
		t.Fatalf("selection %+v", *m.sel)
	}
	// Up past the top scrolls back, and the anchor moves with the text.
	m.curY = 0
	key(runes("k"))
	if m.offset != 1 || m.curY != 0 || m.sel.ay != rows {
		t.Fatalf("up at top: offset %d y %d anchor %d", m.offset, m.curY, m.sel.ay)
	}
	peer.waitMethod(t, proto.MethodPaneScroll, `"offset":1`)
	key(tea.KeyMsg{Type: tea.KeyPgUp})
	page := rows - 2
	if m.offset != 1+page {
		t.Fatalf("pgup: offset %d want %d", m.offset, 1+page)
	}
	key(runes("f"))
	if m.offset != 1 {
		t.Fatalf("f: offset %d", m.offset)
	}
	key(runes("g"))
	if m.offset != 1000 || m.curY != 0 {
		t.Fatalf("g: offset %d y %d", m.offset, m.curY)
	}
	// Down past the bottom scrolls forward.
	m.curY = rows - 1
	key(runes("j"))
	if m.offset != 999 || m.curY != rows-1 {
		t.Fatalf("down at bottom: offset %d y %d", m.offset, m.curY)
	}
	// space toggles the selection off; y without a selection does nothing.
	key(tea.KeyMsg{Type: tea.KeySpace})
	if m.sel != nil {
		t.Fatal("space kept the selection")
	}
	if key(runes("y")) != nil || !m.scrollMode {
		t.Fatal("y without a selection")
	}
	// y copies (not run) and leaves scroll mode at the live screen.
	key(runes("v"))
	key(runes("l"))
	if cmd := key(runes("y")); cmd == nil {
		t.Fatal("y returned no copy command")
	}
	if m.scrollMode || m.sel != nil || m.offset != 0 {
		t.Fatalf("after y: scroll %v offset %d", m.scrollMode, m.offset)
	}
	// Any other key leaves scroll mode.
	a1Prefixed(t, m, runes("["))
	key(tea.KeyMsg{Type: tea.KeyPgUp})
	key(tea.KeyMsg{Type: tea.KeyEsc})
	if m.scrollMode || m.offset != 0 {
		t.Fatalf("esc: scroll %v offset %d", m.scrollMode, m.offset)
	}
	// Mouse selection is dropped by scrolling; enterScrollMode needs a view.
	m.sel = &selection{paneID: "p1"}
	m.scrollPane(5)
	if m.sel != nil {
		t.Fatal("scrolling kept a mouse selection")
	}
	m.viewing = ""
	m.scrollMode = false
	m.enterScrollMode()
	if m.scrollMode {
		t.Fatal("scroll mode without a viewed pane")
	}
}

func TestA1ForwardKeyEncodings(t *testing.T) {
	c, peer := a1FakeClient(t)
	cases := []struct {
		k      tea.KeyMsg
		method string
		want   string
	}{
		{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("pasted"), Paste: true}, proto.MethodPaneSendText, `"paste":true`},
		{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x"), Alt: true}, proto.MethodPaneSendKeys, `"alt+x"`},
		{runes("abc"), proto.MethodPaneSendText, `"text":"abc"`},
		{tea.KeyMsg{Type: tea.KeySpace}, proto.MethodPaneSendText, `"text":" "`},
		{tea.KeyMsg{Type: tea.KeySpace, Alt: true}, proto.MethodPaneSendKeys, `"alt+space"`},
		{tea.KeyMsg{Type: tea.KeyCtrlC}, proto.MethodPaneSendKeys, `"ctrl+c"`},
		{tea.KeyMsg{Type: tea.KeyUp}, proto.MethodPaneSendKeys, `"up"`},
	}
	for _, tc := range cases {
		forwardKey(c, "p9", tc.k)
	}
	for _, tc := range cases {
		if n := peer.count(t, c, tc.method, tc.want); n != 1 {
			t.Errorf("%v: %d × %s %s; got %v", tc.k, n, tc.method, tc.want, peer.methods())
		}
	}
	if !decodeInto(proto.Message{Data: []byte(`{"id":"p1"}`)}, &proto.PaneRef{}) || decodeInto(proto.Message{Data: []byte(`{`)}, &proto.PaneRef{}) {
		t.Fatal("decodeInto")
	}
}
