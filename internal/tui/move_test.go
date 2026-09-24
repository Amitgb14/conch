package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/proto"
)

var moveCaps = []string{"session.v1", "session.share.v1", proto.CapSessionHandoff, proto.CapWorktreeMove, harvestCapability}

// moveModel is a2Model (project api with worktree feat at /src/api-feat)
// online through a fake server, with agents in feat — Claude working with a
// saved conversation, Codex in a subfolder with none — a terminal there, and
// busybox holding a clone of api and an unrelated project.
func moveModel(t *testing.T) (m *Model, src, dst *a1Peer, remote *machine) {
	t.Helper()
	m = a2Model()
	m.subscribed = map[string]bool{} // revealing a new pane subscribes to it
	local := m.machines[0]
	c, src := a1FakeClient(t, moveCaps...)
	local.c = c
	local.projects[0].Remote = "git@github.com:acme/api.git"
	local.panes = append(local.panes,
		proto.PaneInfo{ID: "p5", Name: "claude", State: proto.PaneRunning, ProjectID: "r1", Cwd: "/src/api-feat", Branch: "feat",
			Agent: &proto.AgentStatus{Name: "claude", State: proto.AgentWorking}},
		proto.PaneInfo{ID: "p6", Name: "codex", State: proto.PaneRunning, ProjectID: "r1", Cwd: "/src/api-feat/sub",
			Agent: &proto.AgentStatus{Name: "codex"}},
		proto.PaneInfo{ID: "p7", Name: "zsh", State: proto.PaneRunning, ProjectID: "r1", Cwd: "/src/api-feat"},
	)
	src.setResult(proto.MethodWorktreeDescribe, proto.WorktreeMoveInfo{Branch: "feat", Head: "h2", Base: "main",
		Remote: "git@github.com:acme/api.git", History: []string{"h2", "h1", "h0"}, Staged: 1, Unstaged: 2, Other: 3, Local: []string{".env"}})
	src.setResult(proto.MethodWorktreePack, proto.WorktreePack{ID: "pk", Name: "feat.tar.gz", Size: 3})
	src.setResult(proto.MethodWorktreePackRead, proto.WorktreePackChunk{Data: []byte("abc"), EOF: true})
	src.setResult(proto.MethodSessionList, proto.SessionList{Sessions: []proto.SessionInfo{
		{Agent: "claude", ID: "c1", Dir: "/src/api-feat", PaneID: "p5"},
		{Agent: "claude", ID: "c0", Dir: "/src/api-feat"}, // not open anywhere
	}})
	src.setResult(proto.MethodSessionExport, proto.SessionExport{Name: "claude-c1.md", Doc: "# Handoff\n"})

	remote = newMachine("m2", "busybox", "busybox")
	remote.state = stateOnline
	rc, dst := a1FakeClient(t, moveCaps...)
	remote.c = rc
	remote.projects = []proto.ProjectInfo{
		{ID: "r8", Name: "web", Path: "/srv/web", Git: true, Remote: "https://github.com/acme/web"},
		{ID: "r9", Name: "notes", Path: "/srv/notes"}, // not git: never offered
		{ID: "r5", Name: "api", Path: "/srv/api", Git: true, Remote: "https://github.com/Acme/api/"},
	}
	m.machines = append(m.machines, remote)
	dst.setResult(proto.MethodWorktreeHave, proto.WorktreeHaveResult{Have: []string{"h1", "h0"}})
	dst.setResult(proto.MethodFSUpload, proto.FSUploadResult{Upload: "u1", Path: "/up/feat.tar.gz"})
	dst.setResult(proto.MethodWorktreeUnpack, proto.WorktreeUnpackResult{Path: "/srv/api.worktrees/feat", Branch: "feat"})
	dst.setResult(proto.MethodSessionShare, proto.SessionShareResult{Pane: proto.PaneInfo{ID: "q1", Name: "claude", State: proto.PaneRunning}})
	dst.setResult(proto.MethodPaneCreate, proto.PaneInfo{ID: "q2", Name: "codex", State: proto.PaneRunning})
	return m, src, dst, remote
}

var featTarget = harvestTarget{machine: localMachine, projectID: "r1", branch: "feat"}

// deliver runs cmd and hands each message to its move handler, following
// the move to its end; it returns the last message.
func deliver(t *testing.T, m *Model, cmd tea.Cmd) tea.Msg {
	t.Helper()
	var last tea.Msg
	for i := 0; cmd != nil && i < 50; i++ {
		msgs := a2Run(cmd)
		cmd = nil
		for _, msg := range msgs {
			last = msg
			switch v := msg.(type) {
			case moveDescribedMsg:
				cmd = m.receiveMoveDescribed(v)
			case moveClonedMsg:
				m.receiveMoveCloned(v)
			case moveStepMsg:
				cmd = m.receiveMoveStep(v)
			case moveDoneMsg:
				m.receiveMoveDone(v)
			}
		}
	}
	return last
}

// toConfirm walks the menus to the confirmation for moving feat into api on
// busybox.
func toConfirm(t *testing.T, m *Model) *dialog {
	t.Helper()
	m.openMoveWorktree(featTarget)
	deliver(t, m, runItem(t, m, 0)) // busybox
	runItem(t, m, 0)                // api, the same origin
	d, ok := m.overlay.(*dialog)
	if !ok {
		t.Fatalf("no confirmation; flash %q", m.flash)
	}
	return d
}

func TestMoveWorktreeMenus(t *testing.T) {
	m, src, _, _ := moveModel(t)
	m.openMoveWorktree(featTarget)
	mu := m.overlay.(*menu)
	if got := menuLabels(mu); got != "1 busybox" || mu.title != "Move feat to which machine?" {
		t.Fatalf("machines: %s (%q)", got, mu.title)
	}
	deliver(t, m, runItem(t, m, 0))
	src.waitMethod(t, proto.MethodWorktreeDescribe, `"path":"/src/api-feat"`)
	mu = m.overlay.(*menu)
	// The clone of the same repository first however its URL is written,
	// then other git projects, then a new clone.
	if got := menuLabels(mu); got != "1 api  /srv/api · same origin | 2 web  /srv/web | c Clone acme/api there…" {
		t.Fatalf("projects: %s", got)
	}
	if !strings.Contains(mu.title, "into which project on busybox") {
		t.Fatalf("title %q", mu.title)
	}
	runItem(t, m, 0)
	d := m.overlay.(*dialog)
	text := strings.Join(d.text, "\n")
	for _, want := range []string{
		"Move feat to busybox · api",
		"Uncommitted: 1 staged, 2 changed, 3 untracked file(s).",
		"Local files too: .env.",
		"then closed here: claude (working), codex.",
		"A working agent is stopped mid-task",
		"The worktree here is kept",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("confirmation lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "zsh") {
		t.Error("a terminal is listed as moving")
	}
	// Small screens still fit.
	for _, size := range [][2]int{{30, 10}, {20, 5}, {1, 1}, {200, 60}} {
		m.width, m.height = size[0], size[1]
		for _, line := range strings.Split(m.View(), "\n") {
			if a2Width(line) > size[0] {
				t.Fatalf("%v: line wider than the screen: %q", size, line)
			}
		}
	}
}

func TestMoveWorktreeRuns(t *testing.T) {
	m, src, dst, remote := moveModel(t)
	d := toConfirm(t, m)
	last := deliver(t, m, d.submit(m, nil))
	done, ok := last.(moveDoneMsg)
	if !ok {
		t.Fatalf("ended with %#v; flash %q", last, m.flash)
	}

	// The target says which of the history it has; the pack skips the newest.
	dst.waitMethod(t, proto.MethodWorktreeHave, `"commits":["h2","h1","h0"]`)
	src.waitMethod(t, proto.MethodWorktreePack, `"have":"h1"`)
	dst.waitMethod(t, proto.MethodFSUpload, `"name":"feat.tar.gz"`)
	dst.waitMethod(t, proto.MethodWorktreeUnpack, `"pack":"/up/feat.tar.gz"`)
	dst.waitMethod(t, proto.MethodWorktreeUnpack, `"project_id":"r5"`)

	// Claude's conversation goes along; Codex, with none saved, starts afresh.
	src.waitMethod(t, proto.MethodSessionExport, `"id":"c1"`)
	share := dst.waitMethod(t, proto.MethodSessionShare, `"to":"claude"`)
	for _, want := range []string{`"dir":"/srv/api.worktrees/feat"`, `"from":"local"`, `"doc":"# Handoff\n"`} {
		if !strings.Contains(string(share.Params), want) {
			t.Errorf("share lacks %s: %s", want, share.Params)
		}
	}
	dst.waitMethod(t, proto.MethodPaneCreate, `"agent":"codex","cwd":"/srv/api.worktrees/feat"`)
	src.waitMethod(t, proto.MethodPaneClose, `"id":"p5"`)
	src.waitMethod(t, proto.MethodPaneClose, `"id":"p6"`)
	for _, msg := range src.snapshot() {
		if msg.Method == proto.MethodPaneClose && strings.Contains(string(msg.Params), `"p7"`) {
			t.Fatal("closed the terminal")
		}
	}
	if len(done.started) != 2 || len(done.closed) != 2 || len(done.failed) != 0 {
		t.Fatalf("done: %+v", done)
	}
	if m.flash != "moved feat to busybox · 2 agents continue there" || m.flashIsErr {
		t.Fatalf("flash %q", m.flash)
	}
	if remote.paneIndex("q1") < 0 || remote.paneIndex("q2") < 0 || m.focus != focusMain {
		t.Fatalf("new panes not shown: %+v", remote.panes)
	}
}

func TestMoveWorktreeFailures(t *testing.T) {
	// The worktree can't be rebuilt there: said so, and nothing here closes.
	m, src, dst, _ := moveModel(t)
	dst.setError(proto.MethodWorktreeUnpack, "branch feat is already checked out at /srv/api")
	deliver(t, m, toConfirm(t, m).submit(m, nil))
	// The status bar cuts a long reason short, so it is shown in full.
	if !strings.HasPrefix(m.flash, "moving feat to busybox failed: rebuild the worktree: branch feat is already checked out") || !m.flashIsErr {
		t.Fatalf("flash %q", m.flash)
	}
	n, ok := m.overlay.(*dialog)
	if !ok || !n.notice || strings.Join(n.text, "|") != "moving feat to busybox failed:|rebuild the worktree: branch feat is already checked out at /srv/api|Nothing changed on local." {
		t.Fatalf("notice: %#v", m.overlay)
	}
	// It closes with enter, and takes no other key.
	n.update(m, a2Key("y"))
	if m.overlay == nil {
		t.Fatal("y closed the notice")
	}
	n.update(m, a2Key("enter"))
	if m.overlay != nil {
		t.Fatal("enter didn't close the notice")
	}
	for _, method := range src.methods() {
		if method == proto.MethodPaneClose {
			t.Fatal("closed an agent after a failed move")
		}
	}

	// An agent that can't be continued there stays here, and is named.
	m, src, dst, _ = moveModel(t)
	dst.setError(proto.MethodSessionShare, "claude isn't installed")
	deliver(t, m, toConfirm(t, m).submit(m, nil))
	if !strings.Contains(m.flash, "1 agent continues there") || !strings.Contains(m.flash, "stayed here: claude: claude isn't installed") || !m.flashIsErr {
		t.Fatalf("flash %q", m.flash)
	}
	if n, ok := m.overlay.(*dialog); !ok || !n.notice || !strings.Contains(strings.Join(n.text, "\n"), "these agents stayed here:\n  claude: claude isn't installed") {
		t.Fatalf("notice: %#v", m.overlay)
	}
	for _, msg := range src.snapshot() {
		if msg.Method == proto.MethodPaneClose && strings.Contains(string(msg.Params), `"p5"`) {
			t.Fatal("closed the agent that stayed")
		}
	}

	// Started there but the pane here won't close.
	m, src, _, _ = moveModel(t)
	src.setError(proto.MethodPaneClose, "no pane")
	deliver(t, m, toConfirm(t, m).submit(m, nil))
	if !strings.Contains(m.flash, "started there but didn't close here") {
		t.Fatalf("flash %q", m.flash)
	}

	// Describing fails.
	m, src, _, _ = moveModel(t)
	src.setError(proto.MethodWorktreeDescribe, "has unresolved conflicts")
	m.openMoveWorktree(featTarget)
	deliver(t, m, runItem(t, m, 0))
	if n, ok := m.overlay.(*dialog); m.flash != "can't move feat: has unresolved conflicts" || !ok || !n.notice {
		t.Fatalf("flash %q, overlay %#v", m.flash, m.overlay)
	}

	// A machine goes offline before the move starts.
	m, _, _, remote := moveModel(t)
	d := toConfirm(t, m)
	remote.c = nil
	if cmd := d.submit(m, nil); cmd != nil || !strings.Contains(m.flash, "busybox is") {
		t.Fatalf("offline target: %q", m.flash)
	}
	m, _, _, _ = moveModel(t)
	d = toConfirm(t, m)
	m.machines[0].c = nil
	if cmd := d.submit(m, nil); cmd != nil || !strings.Contains(m.flash, "local is") {
		t.Fatalf("offline source: %q", m.flash)
	}

	// A server there without the session handoff: the agents start afresh.
	m, _, dst, remote = moveModel(t)
	remote.c, dst = a1FakeClient(t, "session.v1", proto.CapWorktreeMove)
	dst.setResult(proto.MethodFSUpload, proto.FSUploadResult{Upload: "u1", Path: "/up/x"})
	dst.setResult(proto.MethodWorktreeUnpack, proto.WorktreeUnpackResult{Path: "/srv/api.worktrees/feat", Branch: "feat"})
	dst.setResult(proto.MethodPaneCreate, proto.PaneInfo{ID: "q3"})
	deliver(t, m, toConfirm(t, m).submit(m, nil))
	dst.waitMethod(t, proto.MethodPaneCreate, `"agent":"claude"`)
	for _, method := range dst.methods() {
		if method == proto.MethodSessionShare {
			t.Fatal("shared with a server that can't take it")
		}
	}
}

func TestMoveWorktreeGuards(t *testing.T) {
	for name, c := range map[string]struct {
		setup func(m *Model, remote *machine)
		t     harvestTarget
		want  string
	}{
		"base branch":    {nil, harvestTarget{localMachine, "r1", "main"}, "main is the base branch"},
		"no worktree":    {nil, harvestTarget{localMachine, "r1", "gone"}, "gone isn't checked out"},
		"no project":     {nil, harvestTarget{localMachine, "r404", "feat"}, "select a branch of a git project"},
		"no branch":      {nil, harvestTarget{localMachine, "r1", ""}, "select a branch of a git project"},
		"old source":     {func(m *Model, _ *machine) { m.machines[0].c = a2Client("session.v1") }, featTarget, "the server on local predates moving worktrees"},
		"source offline": {func(m *Model, _ *machine) { m.machines[0].c = nil }, featTarget, "local is"},
		"none online":    {func(_ *Model, r *machine) { r.state = stateOffline }, featTarget, "no other machine is online"},
		"none connected": {func(_ *Model, r *machine) { r.c = nil }, featTarget, "no other machine is online"},
	} {
		m, _, _, remote := moveModel(t)
		if c.setup != nil {
			c.setup(m, remote)
		}
		m.flash = ""
		if cmd := m.openMoveWorktree(c.t); cmd != nil || m.overlay != nil || !strings.Contains(m.flash, c.want) {
			t.Errorf("%s: flash %q overlay %v", name, m.flash, m.overlay)
		}
	}

	// An old server on the machine picked.
	m, _, _, remote := moveModel(t)
	remote.c = a2Client("session.v1")
	m.openMoveWorktree(featTarget)
	if cmd := runItem(t, m, 0); cmd != nil || !strings.Contains(m.flash, "the server on busybox predates moving worktrees") {
		t.Fatalf("old target: %q", m.flash)
	}

	// Nowhere to put it: no git project there and no origin to clone.
	m, src, _, remote := moveModel(t)
	remote.projects = remote.projects[1:2]
	src.setResult(proto.MethodWorktreeDescribe, proto.WorktreeMoveInfo{Branch: "feat", History: []string{"h"}})
	m.openMoveWorktree(featTarget)
	deliver(t, m, runItem(t, m, 0))
	if m.overlay != nil || !strings.Contains(m.flash, "busybox has no git project, and this one has no origin") {
		t.Fatalf("nowhere: %q", m.flash)
	}
}

func TestMoveWorktreeClone(t *testing.T) {
	m, _, dst, remote := moveModel(t)
	remote.projects = nil
	m.openMoveWorktree(featTarget)
	deliver(t, m, runItem(t, m, 0))
	if got := menuLabels(m.overlay.(*menu)); got != "c Clone acme/api there…" {
		t.Fatalf("items: %s", got)
	}
	runItem(t, m, 0)
	d := m.overlay.(*dialog)
	// /src/api isn't under this home, so it goes under the home there.
	if v := d.fields[0].in.Value(); v != "~/api" {
		t.Fatalf("default folder %q", v)
	}
	if !strings.Contains(strings.Join(d.text, " "), "git@github.com:acme/api.git") {
		t.Fatalf("text %q", d.text)
	}
	d.fields[0].in.SetValue("  ")
	if cmd := d.submit(m, []string{"  "}); cmd != nil || m.flash != "a folder is needed" {
		t.Fatalf("empty folder: %q", m.flash)
	}

	dst.setResult(proto.MethodProjectClone, proto.ProjectInfo{ID: "r7", Name: "api", Path: "/home/b/code/api", Git: true})
	deliver(t, m, d.submit(m, []string{"~/code/api"}))
	dst.waitMethod(t, proto.MethodProjectClone, `"path":"~/code/api"`)
	dst.waitMethod(t, proto.MethodProjectClone, `"url":"git@github.com:acme/api.git"`)
	if m.project("m2", "r7") == nil {
		t.Fatal("the clone isn't listed")
	}
	cd, ok := m.overlay.(*dialog)
	if !ok || !strings.Contains(strings.Join(cd.text, "\n"), "Move feat to busybox · api") {
		t.Fatalf("no confirmation after cloning: %v %q", m.overlay, m.flash)
	}
	deliver(t, m, cd.submit(m, nil))
	dst.waitMethod(t, proto.MethodWorktreeUnpack, `"project_id":"r7"`)

	// A clone that fails.
	m, _, dst, remote = moveModel(t)
	remote.projects = nil
	dst.setError(proto.MethodProjectClone, "Permission denied (publickey)")
	m.openMoveWorktree(featTarget)
	deliver(t, m, runItem(t, m, 0))
	runItem(t, m, 0)
	deliver(t, m, m.overlay.(*dialog).submit(m, []string{"~/api"}))
	if m.flash != "clone on busybox failed: Permission denied (publickey)" || !m.flashIsErr {
		t.Fatalf("flash %q", m.flash)
	}
	if n, ok := m.overlay.(*dialog); !ok || !n.notice || n.text[1] != "Permission denied (publickey)" {
		t.Fatalf("notice: %#v", m.overlay)
	}
	// A notice fits a small screen, and a click does nothing to it.
	for _, size := range [][2]int{{30, 10}, {20, 5}, {1, 1}} {
		m.width, m.height = size[0], size[1]
		for _, line := range strings.Split(m.View(), "\n") {
			if a2Width(line) > size[0] {
				t.Fatalf("%v: %q", size, line)
			}
		}
	}
	m.width, m.height = 120, 40
	n := m.overlay.(*dialog)
	if cmd := n.mouse(m, tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: 60, Y: 15}, n.render(*m)); cmd != nil || m.overlay == nil {
		t.Fatal("a click inside closed or ran something")
	}
}

func TestMoveWorktreeEntryPoints(t *testing.T) {
	m, _, _, _ := moveModel(t)
	labels := func(r row) string {
		var out []string
		for _, it := range newRowMenu(*m, r, 0, 0).items {
			out = append(out, it.key+" "+it.label)
		}
		return strings.Join(out, "|")
	}
	feat := row{kind: kindBranch, machine: localMachine, projectID: "r1", branch: "feat"}
	if !strings.Contains(labels(feat), "T Move to another machine…") {
		t.Fatalf("feat menu: %s", labels(feat))
	}
	// Not for the base branch, a branch without a worktree, or an old server.
	for _, r := range []row{
		{kind: kindBranch, machine: localMachine, projectID: "r1", branch: "main"},
		{kind: kindBranch, machine: localMachine, projectID: "r1", branch: "other"},
	} {
		if strings.Contains(labels(r), "Move to another") {
			t.Errorf("%s offers a move", r.branch)
		}
	}
	m.machines[0].c = a2Client(harvestCapability)
	if strings.Contains(labels(feat), "Move to another") {
		t.Error("an old server offers a move")
	}

	// T on the branch in the tree opens the move; on other rows it does nothing.
	m, _, _, _ = moveModel(t)
	m.focus = focusSidebar
	m.rows = []row{{id: "b:feat", kind: kindBranch, machine: localMachine, projectID: "r1", branch: "feat"},
		{id: "p:r1", kind: kindProject, machine: localMachine, projectID: "r1"}}
	m.cursor = "p:r1"
	a1Key(t, m, runes("T"))
	if m.overlay != nil {
		t.Fatal("T on a project row opened something")
	}
	m.cursor = "b:feat"
	a1Key(t, m, runes("T"))
	if mu, ok := m.overlay.(*menu); !ok || mu.title != "Move feat to which machine?" {
		t.Fatalf("tree T: %v %q", m.overlay, m.flash)
	}

	// T in the changes view opens the move.
	m, _, _, _ = moveModel(t)
	cv := &changesView{machine: localMachine, projectID: "r1", branch: "feat"}
	cv.key(m, a2Key("T"))
	if mu, ok := m.overlay.(*menu); !ok || mu.title != "Move feat to which machine?" {
		t.Fatalf("changes view T: %v %q", m.overlay, m.flash)
	}
}

func TestSameRemote(t *testing.T) {
	same := [][2]string{
		{"git@github.com:acme/api.git", "https://github.com/acme/api"},
		{"ssh://git@github.com/acme/api.git", "https://GitHub.com/acme/api/"},
		{"https://user@github.com:443/acme/api.git", "git@github.com:acme/api"},
		{"/srv/git/api.git", "/srv/git/api"},
	}
	for _, c := range same {
		if !sameRemote(c[0], c[1]) {
			t.Errorf("%q and %q differ", c[0], c[1])
		}
	}
	for _, c := range [][2]string{
		{"git@github.com:acme/api.git", "git@github.com:acme/web.git"},
		{"git@github.com:acme/api.git", "git@gitlab.com:acme/api.git"},
		{"", ""},
		{"", "git@github.com:acme/api.git"},
	} {
		if sameRemote(c[0], c[1]) {
			t.Errorf("%q and %q match", c[0], c[1])
		}
	}
	for u, want := range map[string]string{"git@github.com:acme/api.git": "acme/api", "https://h/x": "h/x", "api": "api", "": ""} {
		if got := remoteName(u); got != want {
			t.Errorf("remoteName(%q) = %q", u, got)
		}
	}
}

// A menu whose title is its widest line shows the whole title.
func TestMenuShowsItsWholeTitle(t *testing.T) {
	m := a2Model()
	for _, title := range []string{"Move feat to which machine?", "x", ""} {
		mu := &menu{title: title, items: []menuItem{{"1", "busybox", nil}}}
		top := a2Strip(mu.render(*m).lines[0])
		if !strings.Contains(top, " "+title+" ") || strings.Contains(top, "…") {
			t.Errorf("%q: %q", title, top)
		}
	}
	// Too long for the screen: cut to fit, with the frame intact.
	m.width, m.height = 20, 8
	m.overlay = &menu{title: strings.Repeat("long ", 10), items: []menuItem{{"1", "a", nil}}}
	for _, line := range strings.Split(m.View(), "\n") {
		if a2Width(line) > 20 {
			t.Fatalf("wider than the screen: %q", line)
		}
	}
}
