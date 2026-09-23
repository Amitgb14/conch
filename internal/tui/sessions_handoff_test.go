package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/proto"
)

// handoffModel is a2SessionsModel with this computer online through a fake
// server, and a second machine, busybox, holding project r5 with a
// worktree for branch feat.
func handoffModel(t *testing.T) (m *Model, sv *sessionsView, src, dst *a1Peer, remote *machine) {
	t.Helper()
	m, sv = a2SessionsModel()
	c, src := a1FakeClient(t, "session.v1", "session.share.v1", proto.CapSessionHandoff)
	m.machines[0].c = c
	m.machines[0].agentList = []proto.AgentAvailability{{Name: "claude", Installed: true}}
	m.machines[0].panes = nil // no agent running here to send to

	remote = newMachine("m2", "busybox", "aghadge@busybox")
	remote.state = stateOnline
	rc, dst := a1FakeClient(t, "session.v1", "session.share.v1", proto.CapSessionHandoff)
	remote.c = rc
	remote.projects = []proto.ProjectInfo{{ID: "r5", Name: "api", Path: "/srv/api", Git: true,
		Worktrees: []proto.WorktreeInfo{{Path: "/srv/api", Branch: "main", Main: true}, {Path: "/srv/api-feat", Branch: "feat"}}}}
	remote.agentList = []proto.AgentAvailability{{Name: "codex", Label: "Codex", Installed: true}, {Name: "gemini", Installed: false}}
	remote.panes = []proto.PaneInfo{
		{ID: "q1", Name: "claude", State: proto.PaneRunning, ProjectID: "r5", Agent: &proto.AgentStatus{Name: "claude"}},
		{ID: "q2", Name: "zsh", State: proto.PaneRunning, ProjectID: "r5"},
	}
	m.machines = append(m.machines, remote)
	return m, sv, src, dst, remote
}

func menuLabels(mu *menu) string {
	var labels []string
	for _, it := range mu.items {
		labels = append(labels, it.key+" "+a2Strip(it.label))
	}
	return strings.Join(labels, " | ")
}

// runItem picks item i of the open menu, which must be there.
func runItem(t *testing.T, m *Model, i int) tea.Cmd {
	t.Helper()
	mu, ok := m.overlay.(*menu)
	if !ok {
		t.Fatalf("no menu open; flash %q", m.flash)
	}
	return mu.run(m, i)
}

func TestShareSessionOnAnotherMachine(t *testing.T) {
	m, sv, src, dst, _ := handoffModel(t)
	sv.sel = 1 // s2, codex on branch feat
	sv.key(m, a2Key("s"))
	mu := m.overlay.(*menu)
	if got := menuLabels(mu); got != "1 Start Claude Code with it | o On another machine…" {
		t.Fatalf("items: %s", got)
	}
	// o is the key; it opens the projects of the other machines.
	if handled, cmd := mu.update(m, a2Key("o")); !handled || cmd != nil {
		t.Fatal("o didn't open the next menu")
	}
	mu = m.overlay.(*menu)
	if got := menuLabels(mu); got != "1 busybox · api" || !strings.Contains(mu.title, "which machine") {
		t.Fatalf("projects: %s (%q)", got, mu.title)
	}
	// Then the agents there: installed ones, and the agents (not shells)
	// running in that project.
	runItem(t, m, 0)
	mu = m.overlay.(*menu)
	if got := menuLabels(mu); got != "1 Start Codex with it |  Send to claude" || mu.title != "Share on busybox · api" {
		t.Fatalf("agents: %s (%q)", got, mu.title)
	}

	msgs := a2Run(runItem(t, m, 0))
	exp := src.waitMethod(t, proto.MethodSessionExport, `"id":"s2"`)
	if !strings.Contains(string(exp.Params), `"dir":"/src/api-feat"`) || !strings.Contains(string(exp.Params), `"agent":"codex"`) {
		t.Fatalf("export params: %s", exp.Params)
	}
	// The fake source answered with no document: the share still names
	// the session, goes into the checkout of the session's branch, and
	// says where it came from.
	share := dst.waitMethod(t, proto.MethodSessionShare, `"to":"codex"`)
	for _, want := range []string{`"dir":"/srv/api-feat"`, `"from":"local"`, `"id":"s2"`, `"agent":"codex"`} {
		if !strings.Contains(string(share.Params), want) {
			t.Errorf("share params lack %s: %s", want, share.Params)
		}
	}
	for _, method := range src.methods() {
		if method == proto.MethodSessionShare {
			t.Fatal("shared on the source machine")
		}
	}
	var created *createdMsg
	stale := false
	for _, msg := range msgs {
		switch v := msg.(type) {
		case createdMsg:
			created = &v
		case sessionsStaleMsg:
			stale = v.key == sessionsKey("m2", "r5")
		}
	}
	if created == nil || created.machine != "m2" || created.note != "shared with Codex on busybox" || !stale {
		t.Fatalf("done: %#v", msgs)
	}
}

func TestShareSessionOnAnotherMachineCarriesTheDocument(t *testing.T) {
	m, sv, src, dst, _ := handoffModel(t)
	src.setResult(proto.MethodSessionExport, proto.SessionExport{Name: "claude-s1.md", Doc: "# Handoff: auth\n"})
	dst.setResult(proto.MethodSessionShare, proto.SessionShareResult{Pane: proto.PaneInfo{ID: "q9"}, Path: "/srv/api/.conch/handoff/claude-s1.md"})
	sv.sel = 0 // s1: no branch, so the project's own folder
	sv.key(m, a2Key("s"))
	runItem(t, m, 1)
	runItem(t, m, 0)
	msgs := a2Run(runItem(t, m, 1)) // send to the running claude
	share := dst.waitMethod(t, proto.MethodSessionShare, `"pane_id":"q1"`)
	for _, want := range []string{`"doc":"# Handoff: auth\n"`, `"name":"claude-s1.md"`, `"dir":"/srv/api"`} {
		if !strings.Contains(string(share.Params), want) {
			t.Errorf("share params lack %s: %s", want, share.Params)
		}
	}
	for _, msg := range msgs {
		if c, ok := msg.(createdMsg); ok && (c.info.ID != "q9" || c.note != "shared with claude on busybox · claude-s1.md") {
			t.Fatalf("created: %#v", c)
		}
	}
}

func TestShareSessionOnAnotherMachineErrors(t *testing.T) {
	errText := func(msgs []tea.Msg) string {
		for _, msg := range msgs {
			if e, ok := msg.(errMsg); ok {
				return e.err.Error()
			}
		}
		return ""
	}

	// Reading the conversation fails: nothing is sent to the other machine.
	m, sv, src, dst, _ := handoffModel(t)
	src.setError(proto.MethodSessionExport, "no codex session s2")
	sv.sel = 1
	sv.key(m, a2Key("s"))
	runItem(t, m, 1)
	runItem(t, m, 0)
	if got := errText(a2Run(runItem(t, m, 0))); !strings.Contains(got, "read the conversation on local") || !strings.Contains(got, "no codex session s2") {
		t.Fatalf("export error: %q", got)
	}
	for _, method := range dst.methods() {
		if method == proto.MethodSessionShare {
			t.Fatal("shared after the export failed")
		}
	}

	// The other machine refuses.
	m, sv, _, dst, _ = handoffModel(t)
	dst.setError(proto.MethodSessionShare, "/srv/api-feat no longer exists")
	sv.sel = 1
	sv.key(m, a2Key("s"))
	runItem(t, m, 1)
	runItem(t, m, 0)
	if got := errText(a2Run(runItem(t, m, 0))); !strings.Contains(got, "share on busybox") || !strings.Contains(got, "no longer exists") {
		t.Fatalf("share error: %q", got)
	}

	// Either machine drops offline while the menus are open.
	m, sv, _, _, remote := handoffModel(t)
	sv.sel = 1
	sv.key(m, a2Key("s"))
	runItem(t, m, 1)
	runItem(t, m, 0)
	remote.c = nil
	if got := errText(a2Run(runItem(t, m, 0))); !strings.Contains(got, "busybox") {
		t.Fatalf("target offline: %q", got)
	}
	m, sv, _, _, _ = handoffModel(t)
	sv.sel = 1
	sv.key(m, a2Key("s"))
	runItem(t, m, 1)
	runItem(t, m, 0)
	m.machines[0].c = nil
	if got := errText(a2Run(runItem(t, m, 0))); !strings.Contains(got, "local") {
		t.Fatalf("source offline: %q", got)
	}
}

func TestShareSessionOnAnotherMachineChoices(t *testing.T) {
	// An older server here can't export: say so instead of listing projects.
	m, sv, _, _, _ := handoffModel(t)
	m.machines[0].c, _ = a1FakeClient(t, "session.v1", "session.share.v1")
	sv.sel = 1
	sv.key(m, a2Key("s"))
	runItem(t, m, 1)
	if m.overlay != nil || !strings.Contains(m.flash, "server on local predates handing sessions") {
		t.Fatalf("old source: %q", m.flash)
	}

	// An older server there: listed, but it says why it can't be chosen.
	m, sv, _, _, remote := handoffModel(t)
	remote.c, _ = a1FakeClient(t, "session.v1", "session.share.v1")
	sv.sel = 1
	sv.key(m, a2Key("s"))
	runItem(t, m, 1)
	runItem(t, m, 0)
	if m.overlay != nil || !strings.Contains(m.flash, "server on busybox predates receiving") {
		t.Fatalf("old target: %q", m.flash)
	}

	// Nothing installed or running there.
	m, sv, _, _, remote = handoffModel(t)
	remote.agentList = []proto.AgentAvailability{{Name: "codex", Installed: false}}
	remote.panes = nil
	sv.sel = 1
	sv.key(m, a2Key("s"))
	runItem(t, m, 1)
	runItem(t, m, 0)
	if m.overlay != nil || !strings.Contains(m.flash, "no agent on busybox") {
		t.Fatalf("no agent there: %q", m.flash)
	}

	// Offline machines, and online ones without projects, aren't offered;
	// with none left there is no "another machine" item.
	for _, change := range []func(*machine){
		func(r *machine) { r.state = stateOffline },
		func(r *machine) { r.c = nil },
		func(r *machine) { r.projects = nil },
	} {
		m, sv, _, _, remote = handoffModel(t)
		change(remote)
		sv.sel = 1
		sv.key(m, a2Key("s"))
		if got := menuLabels(m.overlay.(*menu)); got != "1 Start Claude Code with it" {
			t.Fatalf("items: %s", got)
		}
	}

	// Nothing installed here, but another machine can take it: the menu
	// offers only that, instead of saying there is no agent.
	m, sv, _, _, _ = handoffModel(t)
	m.machines[0].agentList = []proto.AgentAvailability{{Name: "claude", Installed: false}}
	sv.sel = 1
	sv.key(m, a2Key("s"))
	if mu, ok := m.overlay.(*menu); !ok || menuLabels(mu) != "o On another machine…" {
		t.Fatalf("only elsewhere: %v %q", m.overlay, m.flash)
	}

	// Several machines and projects, in the tree's order, keyed 1-9.
	m, sv, _, _, remote = handoffModel(t)
	remote.projects = append(remote.projects, proto.ProjectInfo{ID: "r6", Name: "web", Path: "/srv/web"})
	third := newMachine("m3", "gpu", "gpu")
	third.state, third.c = stateOnline, remote.c
	for i := 0; i < 9; i++ {
		third.projects = append(third.projects, proto.ProjectInfo{ID: "g" + string(rune('0'+i)), Name: "p" + string(rune('0'+i))})
	}
	m.machines = append(m.machines, third)
	sv.sel = 1
	sv.key(m, a2Key("s"))
	runItem(t, m, 1)
	mu := m.overlay.(*menu)
	if len(mu.items) != 11 || a2Strip(mu.items[1].label) != "busybox · web" || mu.items[2].label != "gpu · p0" ||
		mu.items[8].key != "9" || mu.items[9].key != "" {
		t.Fatalf("projects: %s", menuLabels(mu))
	}
	// On a narrow or tiny screen, the menu is cut to fit.
	for _, size := range [][2]int{{30, 10}, {20, 5}, {1, 1}} {
		m.width, m.height = size[0], size[1]
		for _, line := range strings.Split(m.View(), "\n") {
			if a2Width(line) > size[0] {
				t.Fatalf("%v: line wider than the screen: %q", size, line)
			}
		}
	}
}

func TestHandoffDir(t *testing.T) {
	p := proto.ProjectInfo{Path: "/srv/api", Worktrees: []proto.WorktreeInfo{{Path: "/srv/api", Branch: "main"}, {Path: "/srv/api-feat", Branch: "feat"}}}
	for branch, want := range map[string]string{"feat": "/srv/api-feat", "main": "/srv/api", "": "/srv/api", "gone": "/srv/api"} {
		if got := handoffDir(p, branch); got != want {
			t.Errorf("%q: %s", branch, got)
		}
	}
	// A project with no worktrees listed (not git, or an old server).
	if got := handoffDir(proto.ProjectInfo{Path: "/x"}, "feat"); got != "/x" {
		t.Errorf("no worktrees: %s", got)
	}
}
