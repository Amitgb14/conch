package tui

import (
	"encoding/json"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
)

// a4Actions is a model with three actions configured: one everywhere, one
// on branches only, and one that closes its terminal when it is done.
func a4Actions(t *testing.T, withClient bool) (*Model, *a1Peer) {
	t.Helper()
	a2Isolate(t) // a machine row's action runs in the home directory: never the real one
	m, peer := a1Fixture(t, withClient)
	keep := false
	m.cfg.Actions = []config.Action{
		{Name: "Run tests", Run: "go test ./...", On: []string{"branch", "project"}},
		{Name: "Open the folder", Run: "open .", Keep: &keep},
		{Name: "No command", Run: ""},
	}
	return m, peer
}

// a4Created is the pane.create a peer was asked for, decoded.
func a4Created(t *testing.T, peer *a1Peer) proto.PaneCreateParams {
	t.Helper()
	msg := peer.waitMethod(t, proto.MethodPaneCreate, "")
	var params proto.PaneCreateParams
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		t.Fatalf("decode pane.create: %v", err)
	}
	return params
}

// envOf reads one variable out of a pane's environment.
func envOf(env []string, name string) (string, bool) {
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == name {
			return v, true
		}
	}
	return "", false
}

// An action belongs to the kinds of row that stand for somewhere to run a
// command. A section header is not one of them.
func TestActionsOfferedByRowKind(t *testing.T) {
	m, _ := a4Actions(t, false)
	for _, c := range []struct {
		row  string
		want []string
	}{
		{"b:r1:feat", []string{"Run tests", "Open the folder"}},
		{"p:r1", []string{"Run tests", "Open the folder"}},
		{"pane:p1", []string{"Open the folder"}},
		{"m:local", []string{"Open the folder"}},
	} {
		i := indexOfRow(m.rows, c.row)
		if i < 0 {
			t.Fatalf("no row %q in\n%s", c.row, render(m.rows))
		}
		var got []string
		for _, a := range m.actionsFor(m.rows[i]) {
			got = append(got, a.Name)
		}
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%s offers %v, wanted %v", c.row, got, c.want)
		}
	}
	// A section row has no place of its own to run anything.
	for _, kind := range []nodeKind{kindBranches, kindAgents, kindTerminals, kindSessions, kindWorkspace, kindCLI} {
		if got := m.actionsFor(row{kind: kind, machine: localMachine}); got != nil {
			t.Errorf("a %v row offered %d actions", kind, len(got))
		}
	}
	// Nothing configured: no menu item at all, so the menus are as they were.
	m.cfg.Actions = nil
	if got := m.actionsFor(row{kind: kindBranch, machine: localMachine}); got != nil {
		t.Errorf("no actions configured, yet %d offered", len(got))
	}
}

// The row menu's way in: one item, above whatever closes or removes
// something, opening a menu of the commands with digits for them.
func TestRowMenuOffersActions(t *testing.T) {
	m, _ := a4Actions(t, false)
	i := indexOfRow(m.rows, "b:r1:feat")
	if i < 0 {
		t.Fatal("no branch row")
	}
	r := m.rows[i]
	mu := newRowMenu(*m, r, 2, 2)
	at, closeAt := -1, -1
	for j, it := range mu.items {
		if it.label == "Actions…" {
			at = j
		}
		if it.key == "x" && closeAt < 0 {
			closeAt = j
		}
	}
	if at < 0 {
		t.Fatalf("no way into the actions: %v", labelsOf(mu.items))
	}
	if closeAt >= 0 && at > closeAt {
		t.Errorf("the actions are below the item that removes something: %v", labelsOf(mu.items))
	}

	// It opens a menu of the actions, which esc leaves for the row menu.
	m.overlay = mu
	mu.items[at].run(m)
	sub, ok := m.overlay.(*menu)
	if !ok || sub == mu {
		t.Fatalf("the item opened %#v", m.overlay)
	}
	if got := labelsOf(sub.items); strings.Join(got, ",") != "Run tests,Open the folder" {
		t.Errorf("the actions menu lists %v", got)
	}
	if sub.items[0].key != "1" || sub.items[1].key != "2" {
		t.Errorf("the actions are not numbered: %q %q", sub.items[0].key, sub.items[1].key)
	}
	sub.update(m, a2Key("esc"))
	if m.overlay != mu {
		t.Errorf("esc did not go back to the row menu: %#v", m.overlay)
	}

	// With nothing configured the menu is exactly as it was before.
	m.cfg.Actions = nil
	for _, it := range newRowMenu(*m, r, 2, 2).items {
		if it.label == "Actions…" {
			t.Error("an actions item with no actions")
		}
	}
}

// Only the first nine get a digit; the rest are reached with the cursor.
func TestActionsMenuNumbersTheFirstNine(t *testing.T) {
	m, _ := a4Actions(t, false)
	m.cfg.Actions = nil
	for i := 0; i < 12; i++ {
		m.cfg.Actions = append(m.cfg.Actions, config.Action{Name: "act" + string(rune('a'+i)), Run: "true"})
	}
	sub := newActionsMenu(*m, row{kind: kindBranch, machine: localMachine, branch: "feat"}, 0, 0, nil)
	if len(sub.items) != 12 {
		t.Fatalf("listed %d of twelve", len(sub.items))
	}
	for i, it := range sub.items {
		switch {
		case i < 9 && it.key == "":
			t.Errorf("item %d has no digit", i)
		case i >= 9 && it.key != "":
			t.Errorf("item %d got the key %q", i, it.key)
		}
	}
	// It still draws inside a small terminal.
	m.width, m.height = 40, 12
	b := sub.render(*m)
	a2CheckBox(t, b, *m)
}

// An action runs in a terminal of its own, on the machine the row belongs
// to, in that row's checkout, with the context in its environment.
func TestRunActionStartsItsOwnTerminal(t *testing.T) {
	m, peer := a4Actions(t, true)
	peer.setResult(proto.MethodPaneCreate, proto.PaneInfo{ID: "a1", Name: "Run tests · feat", State: proto.PaneRunning})
	i := indexOfRow(m.rows, "b:r1:feat")
	msgs := a2Run(m.runAction(m.cfg.Actions[0], m.rows[i]))
	if err := a2ErrText(msgs); err != "" {
		t.Fatalf("running it: %s", err)
	}
	params := a4Created(t, peer)
	if got := params.Command; len(got) != 3 || got[0] != "/bin/sh" || got[1] != "-lc" || got[2] != "go test ./..." {
		t.Errorf("command %q", got)
	}
	if params.Cwd != "/src/api-feat" {
		t.Errorf("it ran in %q, not the branch's worktree", params.Cwd)
	}
	if params.Name != "Run tests · feat" {
		t.Errorf("terminal named %q", params.Name)
	}
	if params.NoProject {
		t.Error("a branch's action was started outside its project")
	}
	for _, want := range [][2]string{
		{"CONCH_MACHINE", "local"}, {"CONCH_PROJECT", "api"}, {"CONCH_BRANCH", "feat"},
		{"CONCH_WORKTREE", "/src/api-feat"}, {"CONCH_PANE", ""},
	} {
		got, ok := envOf(params.Env, want[0])
		if !ok {
			t.Errorf("%s was not set at all", want[0])
			continue
		}
		if got != want[1] {
			t.Errorf("%s=%q, wanted %q", want[0], got, want[1])
		}
	}
	// The pane it started is the one that opens, and it is remembered as
	// an action's terminal so its output is kept when it exits.
	var started bool
	for _, msg := range msgs {
		if a, ok := msg.(actionStartedMsg); ok {
			started = true
			a2Run(m.receiveActionStarted(a))
			if m.actionPanes[paneKey(localMachine, "a1")] != "Run tests" {
				t.Errorf("not remembered: %v", m.actionPanes)
			}
		}
	}
	if !started {
		t.Fatalf("no actionStartedMsg: %#v", msgs)
	}
}

// A project's row is not about a branch, but the directory it hands the
// command is on one: that is what the variable says, rather than nothing.
func TestRunActionNamesTheProjectsBranch(t *testing.T) {
	m, peer := a4Actions(t, true)
	peer.setResult(proto.MethodPaneCreate, proto.PaneInfo{ID: "a5", State: proto.PaneRunning})
	i := indexOfRow(m.rows, "p:r1")
	if i < 0 {
		t.Fatal("no project row")
	}
	a2Run(m.runAction(m.cfg.Actions[0], m.rows[i]))
	params := a4Created(t, peer)
	if params.Cwd != "/src/api" {
		t.Fatalf("a project's action ran in %q", params.Cwd)
	}
	if got, _ := envOf(params.Env, "CONCH_BRANCH"); got != "main" {
		t.Errorf("CONCH_BRANCH=%q, wanted the branch that directory is on", got)
	}
	// A directory that is no worktree of the project leaves it empty —
	// set, so a `set -u` script survives it, but saying nothing.
	m.machines[0].projects[0].Worktrees = nil
	m.rebuild()
	r := m.rows[indexOfRow(m.rows, "p:r1")]
	got, ok := envOf(m.actionEnv(r, m.placeOf(r)), "CONCH_BRANCH")
	if !ok || got != "" {
		t.Errorf("with no worktree there: CONCH_BRANCH=%q (set: %v)", got, ok)
	}
}

// From a pane row the pane is in the environment, and from a machine row
// the action runs in the home directory, outside every project.
func TestRunActionFromPaneAndMachine(t *testing.T) {
	m, peer := a4Actions(t, true)
	peer.setResult(proto.MethodPaneCreate, proto.PaneInfo{ID: "a2", State: proto.PaneRunning})
	i := indexOfRow(m.rows, "pane:p1")
	if i < 0 {
		t.Fatal("no pane row")
	}
	a2Run(m.runAction(m.cfg.Actions[1], m.rows[i]))
	params := a4Created(t, peer)
	if got, _ := envOf(params.Env, "CONCH_PANE"); got != "p1" {
		t.Errorf("CONCH_PANE=%q", got)
	}
	if got, _ := envOf(params.Env, "CONCH_BRANCH"); got != "feat" {
		t.Errorf("a pane on a branch: CONCH_BRANCH=%q", got)
	}

	m2, peer2 := a4Actions(t, true)
	m2.machines[0].server.Home = "/home/a1"
	peer2.setResult(proto.MethodPaneCreate, proto.PaneInfo{ID: "a3", State: proto.PaneRunning})
	j := indexOfRow(m2.rows, "m:local")
	a2Run(m2.runAction(m2.cfg.Actions[1], m2.rows[j]))
	machineParams := a4Created(t, peer2)
	if !machineParams.NoProject {
		t.Error("a machine's action was put in a project")
	}
	if machineParams.Cwd == "" {
		t.Error("a machine's action has nowhere to run")
	}
	for _, name := range []string{"CONCH_PROJECT", "CONCH_BRANCH", "CONCH_PANE"} {
		got, ok := envOf(machineParams.Env, name)
		if !ok {
			t.Errorf("%s is missing rather than empty, which a `set -u` script trips over", name)
		}
		if got != "" {
			t.Errorf("%s=%q on a machine row", name, got)
		}
	}
}

// What cannot be run says why, and asks for nothing: a branch nobody has
// checked out has no directory, and a machine that is away has no server.
func TestRunActionSaysWhyItCannot(t *testing.T) {
	m, peer := a4Actions(t, true)
	// A branch with no worktree: the fixture's project has none for "old".
	m.machines[0].projects[0].Branches = append(m.machines[0].projects[0].Branches, proto.BranchInfo{Name: "old"})
	m.showAll[scoped(localMachine, "r1")] = true
	m.rebuild()
	i := indexOfRow(m.rows, "b:r1:old")
	if i < 0 {
		t.Fatalf("no row for the unchecked-out branch:\n%s", render(m.rows))
	}
	if cmd := m.runAction(m.cfg.Actions[0], m.rows[i]); cmd != nil {
		t.Errorf("it tried to run anyway: %#v", a2Run(cmd))
	}
	if !strings.Contains(m.flash, "no worktree") {
		t.Errorf("flash was %q", m.flash)
	}
	if n := peer.count(t, m.machines[0].c, proto.MethodPaneCreate, ""); n != 0 {
		t.Errorf("%d panes were created", n)
	}

	// A machine that is away.
	off, _ := a4Actions(t, false)
	off.machines[0].state = stateOffline
	off.machines[0].c = nil
	j := indexOfRow(off.rows, "b:r1:feat")
	if cmd := off.runAction(off.cfg.Actions[0], off.rows[j]); cmd != nil {
		t.Errorf("offline, yet it ran: %#v", a2Run(cmd))
	}
	if off.flash == "" {
		t.Error("offline said nothing")
	}
}

// An action's terminal is its output: conch keeps it when the command
// exits, as it does a check's, and says how it went. Unless the action
// said `keep = false`, when it closes like any other terminal.
func TestActionTerminalIsKeptOrClosed(t *testing.T) {
	for _, c := range []struct {
		name       string
		keep       bool
		exit       int
		wantClosed int
		wantFlash  string
	}{
		{"kept after passing", true, 0, 0, "Run tests finished"},
		{"kept after failing", true, 2, 0, "Run tests failed (exit 2)"},
		{"closed when told to", false, 0, 1, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			m, peer := a4Actions(t, true)
			a2Run(m.receiveActionStarted(actionStartedMsg{machine: localMachine, name: "Run tests",
				keep: c.keep, info: proto.PaneInfo{ID: "a9", Name: "Run tests · feat", State: proto.PaneRunning}}))
			exit := proto.PaneInfo{ID: "a9", Name: "Run tests · feat", State: proto.PaneExited, ExitCode: c.exit}
			m.machines[0].panes = append(m.machines[0].panes, exit)
			a2Run(m.handleEvent(m.machines[0], eventMsg(t, proto.EventPaneExited, exit)))
			if n := peer.count(t, m.machines[0].c, proto.MethodPaneClose, "a9"); n != c.wantClosed {
				t.Errorf("closed %d times, wanted %d", n, c.wantClosed)
			}
			if c.wantFlash != "" && !strings.Contains(m.flash, c.wantFlash) {
				t.Errorf("flash was %q, wanted %q", m.flash, c.wantFlash)
			}
			if c.keep && m.actionPanes[paneKey(localMachine, "a9")] != "" {
				t.Error("an exited action is still listed as running")
			}
			// Closing it by hand forgets it, whichever way it went.
			a2Run(m.handleEvent(m.machines[0], eventMsg(t, proto.EventPaneClosed, proto.PaneRef{ID: "a9"})))
			if _, still := m.actionPanes[paneKey(localMachine, "a9")]; still {
				t.Error("a closed terminal is still remembered")
			}
		})
	}
}

// Whatever happens, an action's own pane is never mistaken for an agent's
// or a check's: a pane conch did not start as an action closes as before.
func TestOrdinaryPaneStillClosesOnExit(t *testing.T) {
	m, peer := a4Actions(t, true)
	exit := proto.PaneInfo{ID: "p2", Name: "zsh", State: proto.PaneExited, ExitCode: 0}
	m.machines[0].panes[1] = exit
	a2Run(m.handleEvent(m.machines[0], eventMsg(t, proto.EventPaneExited, exit)))
	if n := peer.count(t, m.machines[0].c, proto.MethodPaneClose, "p2"); n != 1 {
		t.Errorf("an ordinary terminal that exited was closed %d times", n)
	}
}

// The settings screen is where an action that never shows up says why.
func TestSettingsActionsTab(t *testing.T) {
	a2Isolate(t)
	defer applyTheme("conch", "")
	m, _ := a4Actions(t, false)
	m.cfg.Actions = append(m.cfg.Actions, config.Action{Name: "Odd", Run: "true", On: []string{"worktree"}})
	s := &settings{tab: actionsTab}
	out := a2Plain(s.render(*m).lines)
	for _, want := range []string{"Run tests", "go test ./...", "branch, project", "closes when done",
		"No command has no command to run", "Odd is offered on worktree"} {
		if !strings.Contains(out, want) {
			t.Errorf("the Actions tab does not say %q:\n%s", want, out)
		}
	}
	// With none configured it says how to write one.
	m.cfg.Actions = nil
	empty := a2Plain(s.render(*m).lines)
	for _, want := range []string{"[[actions]]", "config.toml", "CONCH_BRANCH"} {
		if !strings.Contains(empty, want) {
			t.Errorf("the empty Actions tab does not say %q:\n%s", want, empty)
		}
	}
	// It draws inside a small terminal, and the tab bar fits where there
	// is room for it.
	for _, size := range [][2]int{{200, 40}, {120, 24}, {90, 20}, {60, 12}} {
		m.width, m.height = size[0], size[1]
		b := s.render(*m)
		a2CheckBox(t, b, *m)
		if size[0] >= 90 && !strings.Contains(a2Plain(b.lines), "7 Actions") {
			t.Errorf("at %d columns the tab bar cut off the last tab:\n%s", size[0], a2Plain(b.lines[:2]))
		}
	}
}

// Picking an action with its digit runs it, which is the whole point of
// the numbers.
func TestActionsMenuDigitRuns(t *testing.T) {
	m, peer := a4Actions(t, true)
	peer.setResult(proto.MethodPaneCreate, proto.PaneInfo{ID: "a7", State: proto.PaneRunning})
	i := indexOfRow(m.rows, "b:r1:feat")
	sub := newActionsMenu(*m, m.rows[i], 0, 0, nil)
	m.overlay = sub
	handled, cmd := sub.update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("1")})
	if !handled {
		t.Fatal("the digit was not taken")
	}
	a2Run(cmd)
	params := a4Created(t, peer)
	if params.Command[2] != "go test ./..." {
		t.Errorf("1 ran %q", params.Command)
	}
}

func labelsOf(items []menuItem) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.label)
	}
	return out
}
