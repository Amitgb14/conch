package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/remote"
	"github.com/Amitgb14/conch/internal/sandbox"
)

var moveCaps = []string{"session.v1", "session.share.v1", proto.CapSessionHandoff, proto.CapWorktreeMove, harvestCapability}

// moveModel is a2Model (project api with worktree feat at /src/api-feat)
// online through a fake server, with agents in feat — Claude working with a
// saved conversation, Codex in a subfolder with none — a terminal there, and
// busybox holding a clone of api and an unrelated project.
func moveModel(t *testing.T) (m *Model, src, dst *a1Peer, remote *machine) {
	t.Helper()
	a2Isolate(t)
	moveSandboxes(t, nil) // a provider key in the developer's environment adds no menu item
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

// moveSandboxes makes p the Daytona provider and leaves every other one
// unconfigured; nil leaves them all so.
func moveSandboxes(t *testing.T, p sandbox.Provider) {
	t.Helper()
	old := openSandboxProvider
	openSandboxProvider = func(name string) (sandbox.Provider, error) {
		if name == "daytona" && p != nil {
			return p, nil
		}
		return nil, errors.New("not here")
	}
	t.Cleanup(func() { openSandboxProvider = old })
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
				cmd = m.receiveMoveCloned(v)
			case moveSandboxFailedMsg:
				m.receiveMoveSandboxFailed(v)
			case projectDescribedMsg:
				cmd = m.receiveProjectDescribed(v)
			case projectMainDoneMsg:
				m.receiveProjectMainDone(v)
			case moveStepMsg:
				cmd = m.receiveMoveStep(v)
			case moveDoneMsg:
				if v.mv.job != nil { // the next worktree of a project
					cmd = m.receiveMoveDone(v)
				} else {
					m.receiveMoveDone(v)
				}
			}
		}
	}
	return last
}

// toConfirm picks busybox for feat, which goes to api there by itself.
func toConfirm(t *testing.T, m *Model) *dialog {
	t.Helper()
	m.openMoveWorktree(featTarget)
	deliver(t, m, runItem(t, m, 0)) // busybox
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
	// One choice: the project there is the clone of the same repository,
	// however its URL is written, with no menu of projects.
	deliver(t, m, runItem(t, m, 0))
	src.waitMethod(t, proto.MethodWorktreeDescribe, `"path":"/src/api-feat"`)
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
	// No clone there: one goes where it is here, under the home folder,
	// said in the one confirmation, and the move follows it.
	m, _, dst, remote := moveModel(t)
	remote.projects = nil
	m.openMoveWorktree(featTarget)
	deliver(t, m, runItem(t, m, 0))
	d, ok := m.overlay.(*dialog)
	if !ok {
		t.Fatalf("no confirmation: %v %q", m.overlay, m.flash)
	}
	text := strings.Join(d.text, "\n")
	// /src/api isn't under this home, so it goes under the home there.
	if !strings.Contains(text, "busybox has no clone of acme/api, so it is cloned into ~/api there first") {
		t.Fatalf("text:\n%s", text)
	}
	for _, msg := range dst.snapshot() {
		if msg.Method == proto.MethodProjectClone {
			t.Fatal("cloned before the move was confirmed")
		}
	}
	dst.setResult(proto.MethodProjectClone, proto.ProjectInfo{ID: "r7", Name: "api", Path: "/home/b/api", Git: true})
	if _, ok := deliver(t, m, d.submit(m, nil)).(moveDoneMsg); !ok {
		t.Fatalf("didn't finish; flash %q", m.flash)
	}
	dst.waitMethod(t, proto.MethodProjectClone, `"path":"~/api"`)
	dst.waitMethod(t, proto.MethodProjectClone, `"url":"git@github.com:acme/api.git"`)
	dst.waitMethod(t, proto.MethodWorktreeUnpack, `"project_id":"r7"`)
	if m.project("m2", "r7") == nil {
		t.Fatal("the clone isn't listed")
	}

	// That folder already holds another project: conch asks rather than
	// clone over it, and the folder can be changed.
	m, _, dst, remote = moveModel(t)
	remote.server.Home = "/home/b"
	remote.projects = []proto.ProjectInfo{{ID: "r6", Name: "api", Path: "/home/b/api", Git: true, Remote: "https://example.com/other/api"}}
	m.openMoveWorktree(featTarget)
	deliver(t, m, runItem(t, m, 0))
	mu, ok := m.overlay.(*menu)
	if !ok || menuLabels(mu) != "1 api  ~/api | c Clone acme/api there…" || !strings.Contains(mu.title, "into which project on busybox") {
		t.Fatalf("menu: %v %q", m.overlay, m.flash)
	}
	runItem(t, m, 1)
	cd := m.overlay.(*dialog)
	if v := cd.fields[0].in.Value(); v != "~/api" {
		t.Fatalf("default folder %q", v)
	}
	if cmd := cd.submit(m, []string{"  "}); cmd != nil || m.flash != "a folder is needed" {
		t.Fatalf("empty folder: %q", m.flash)
	}
	cd.submit(m, []string{" ~/code/api "})
	if c, ok := m.overlay.(*dialog); !ok || !strings.Contains(strings.Join(c.text, "\n"), "cloned into ~/code/api there first") {
		t.Fatalf("no confirmation for the folder: %v", m.overlay)
	}
	dst.setResult(proto.MethodProjectClone, proto.ProjectInfo{ID: "r7", Name: "api", Path: "/home/b/code/api", Git: true})
	deliver(t, m, m.overlay.(*dialog).submit(m, nil))
	dst.waitMethod(t, proto.MethodProjectClone, `"path":"~/code/api"`)
	dst.waitMethod(t, proto.MethodWorktreeUnpack, `"project_id":"r7"`)

	// No origin: nothing to match or clone, so the git projects there.
	m, src, _, _ := moveModel(t)
	src.setResult(proto.MethodWorktreeDescribe, proto.WorktreeMoveInfo{Branch: "feat", History: []string{"h"}})
	m.openMoveWorktree(featTarget)
	deliver(t, m, runItem(t, m, 0))
	if mu, ok := m.overlay.(*menu); !ok || menuLabels(mu) != "1 web  /srv/web | 2 api  /srv/api" {
		t.Fatalf("no origin: %v", m.overlay)
	}

	// A clone that fails.
	m, _, dst, remote = moveModel(t)
	remote.projects = nil
	dst.setError(proto.MethodProjectClone, "Permission denied (publickey)")
	deliver(t, m, toConfirm(t, m).submit(m, nil))
	if m.flash != "clone on busybox failed: Permission denied (publickey)" || !m.flashIsErr {
		t.Fatalf("flash %q", m.flash)
	}
	if n, ok := m.overlay.(*dialog); !ok || !n.notice || n.text[1] != "Permission denied (publickey)" || strings.Contains(strings.Join(n.text, " "), "costs") {
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

// moveSandboxModel is moveModel with Daytona set up and no other machine,
// and a sandbox that sets up without a network.
func moveSandboxModel(t *testing.T) (*Model, *sbProvider, *a1Peer) {
	t.Helper()
	m, src, _, _ := moveModel(t)
	m.machines = m.machines[:1]
	p := &sbProvider{created: sandbox.Sandbox{ID: "sb42"}}
	moveSandboxes(t, p)
	stubSetUp(t)
	return m, p, src
}

// stubSetUp sets a new sandbox up without a network.
func stubSetUp(t *testing.T) {
	t.Helper()
	old := setUpSandboxFn
	setUpSandboxFn = func(context.Context, remote.Machine, func(string)) (*client.Client, error) {
		c, _ := a1FakeClient(t)
		return c, nil
	}
	t.Cleanup(func() { setUpSandboxFn = old })
}

func TestMoveWorktreeToNewSandbox(t *testing.T) {
	m, p, _ := moveSandboxModel(t)
	t.Setenv("SB_TOKEN", "tok")
	m.cfg.Sandbox = config.SandboxCfg{"daytona": {Snapshot: "big", Env: []string{"SB_TOKEN"}, AutoStop: 15}}
	m.openMoveWorktree(featTarget)
	mu := m.overlay.(*menu)
	if got := menuLabels(mu); got != " New Daytona sandbox" {
		t.Fatalf("items: %s", got)
	}
	deliver(t, m, runItem(t, m, 0))
	d, ok := m.overlay.(*dialog)
	if !ok {
		t.Fatalf("no confirmation: %v %q", m.overlay, m.flash)
	}
	text := strings.Join(d.text, "\n")
	for _, want := range []string{"Move feat to a new Daytona sandbox", "clones acme/api into ~/api first", "costs, until you stop it",
		"then closed here: claude (working), codex."} {
		if !strings.Contains(text, want) {
			t.Errorf("confirmation lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Nothing is passed in") {
		t.Errorf("says nothing is passed in, with env set:\n%s", text)
	}
	if p.called() != "" {
		t.Fatalf("made a sandbox before it was confirmed: %s", p.called())
	}

	// Confirmed: the sandbox is made from config.toml and added.
	var added machineAddedMsg
	for _, msg := range a2Run(d.submit(m, nil)) {
		if a, ok := msg.(machineAddedMsg); ok {
			added = a
		}
	}
	if added.move == nil || added.m.Target != "daytona:sb42" {
		t.Fatalf("added %+v", added)
	}
	if p.spec.Snapshot != "big" || p.spec.Env["SB_TOKEN"] != "tok" || p.spec.AutoStop != 15 {
		t.Fatalf("spec %+v", p.spec)
	}
	next, _ := m.Update(added) // its connect is left unrun: the test connects it
	*m = next.(Model)
	mach := m.machine(added.m.ID)
	if mach == nil || m.pendingMoves[mach.id] == nil || !strings.Contains(m.flash, "connecting to move feat there") {
		t.Fatalf("not held for the sandbox: %q", m.flash)
	}

	// Connected, it clones and the move goes on to the end.
	rc, dst := a1FakeClient(t, moveCaps...)
	mach.c, mach.state = rc, stateOnline
	dst.setResult(proto.MethodProjectClone, proto.ProjectInfo{ID: "r7", Name: "api", Path: "/home/daytona/api", Git: true})
	dst.setResult(proto.MethodWorktreeHave, proto.WorktreeHaveResult{})
	dst.setResult(proto.MethodFSUpload, proto.FSUploadResult{Upload: "u1", Path: "/up/feat.tar.gz"})
	dst.setResult(proto.MethodWorktreeUnpack, proto.WorktreeUnpackResult{Path: "/home/daytona/api.worktrees/feat", Branch: "feat"})
	dst.setResult(proto.MethodSessionShare, proto.SessionShareResult{Pane: proto.PaneInfo{ID: "q1", Name: "claude", State: proto.PaneRunning}})
	dst.setResult(proto.MethodPaneCreate, proto.PaneInfo{ID: "q2", Name: "codex", State: proto.PaneRunning})
	next, cmd := m.Update(projectsMsg{machine: mach.id, gen: mach.gen})
	*m = next.(Model)
	if m.pendingMoves[mach.id] != nil {
		t.Fatal("still held once connected")
	}
	if _, ok := deliver(t, m, cmd).(moveDoneMsg); !ok {
		t.Fatalf("didn't finish; flash %q", m.flash)
	}
	dst.waitMethod(t, proto.MethodProjectClone, `"path":"~/api"`)
	dst.waitMethod(t, proto.MethodWorktreeUnpack, `"project_id":"r7"`)
	if !strings.HasPrefix(m.flash, "moved feat to "+mach.label+" · 2 agents continue there") {
		t.Fatalf("flash %q", m.flash)
	}
	// Projects again later carry on nothing.
	if _, cmd := m.Update(projectsMsg{machine: mach.id, gen: mach.gen}); len(a2Run(cmd)) > 1 {
		t.Fatal("a second projects list moved again")
	}
}

func TestMoveWorktreeToNewSandboxFailures(t *testing.T) {
	// A provider without a key isn't offered.
	m, _, _ := moveSandboxModel(t)
	moveSandboxes(t, &sbProvider{checkErr: sandbox.ErrNotConfigured})
	if m.openMoveWorktree(featTarget); m.overlay != nil || !strings.Contains(m.flash, "add one, or a sandbox, with M") {
		t.Fatalf("unconfigured: %v %q", m.overlay, m.flash)
	}

	// Nothing to clone in a new sandbox: refused before anything is made.
	m, p, src := moveSandboxModel(t)
	src.setResult(proto.MethodWorktreeDescribe, proto.WorktreeMoveInfo{Branch: "feat", History: []string{"h"}})
	m.openMoveWorktree(featTarget)
	deliver(t, m, runItem(t, m, 0))
	if n, ok := m.overlay.(*dialog); !ok || !n.notice || !strings.Contains(strings.Join(n.text, " "), "no origin") || p.called() != "" {
		t.Fatalf("no origin: %#v %s", m.overlay, p.called())
	}

	// No env named: the confirmation says agents there log in afresh.
	m, _, _ = moveSandboxModel(t)
	m.openMoveWorktree(featTarget)
	deliver(t, m, runItem(t, m, 0))
	if !strings.Contains(strings.Join(m.overlay.(*dialog).text, " "), "Nothing is passed in") {
		t.Fatalf("text %q", m.overlay.(*dialog).text)
	}

	// An env named in config.toml that isn't set here: nothing is made.
	m, p, _ = moveSandboxModel(t)
	t.Setenv("SB_UNSET", "")
	m.cfg.Sandbox = config.SandboxCfg{"daytona": {Env: []string{"SB_UNSET"}}}
	m.openMoveWorktree(featTarget)
	deliver(t, m, runItem(t, m, 0))
	if cmd := m.overlay.(*dialog).submit(m, nil); cmd != nil || !strings.Contains(m.flash, "SB_UNSET") || p.called() != "" {
		t.Fatalf("unset env: %q %s", m.flash, p.called())
	}

	// Making it fails: said in full, nothing here changes.
	m, p, src = moveSandboxModel(t)
	p.created, p.createErr = sandbox.Sandbox{}, errors.New("create sandbox: quota exceeded")
	m.openMoveWorktree(featTarget)
	deliver(t, m, runItem(t, m, 0))
	deliver(t, m, m.overlay.(*dialog).submit(m, nil))
	if !strings.Contains(m.flash, "making a new Daytona sandbox for feat failed: create sandbox: quota exceeded") || !m.flashIsErr {
		t.Fatalf("flash %q", m.flash)
	}
	for _, method := range src.methods() {
		if method == proto.MethodPaneClose || method == proto.MethodWorktreePack {
			t.Fatalf("%s after the sandbox failed", method)
		}
	}

	// The clone in it fails: the notice says the sandbox is still running.
	m, _, _ = moveSandboxModel(t)
	m.openMoveWorktree(featTarget)
	deliver(t, m, runItem(t, m, 0))
	var added machineAddedMsg
	for _, msg := range a2Run(m.overlay.(*dialog).submit(m, nil)) {
		if a, ok := msg.(machineAddedMsg); ok {
			added = a
		}
	}
	next, _ := m.Update(added)
	*m = next.(Model)
	mach := m.machine(added.m.ID)
	rc, dst := a1FakeClient(t, moveCaps...)
	mach.c, mach.state = rc, stateOnline
	dst.setError(proto.MethodProjectClone, "Repository not found")
	next, cmd := m.Update(projectsMsg{machine: mach.id, gen: mach.gen})
	*m = next.(Model)
	deliver(t, m, cmd)
	if n, ok := m.overlay.(*dialog); !ok || !strings.Contains(strings.Join(n.text, " "), "is still running, and costs until stopped") {
		t.Fatalf("notice: %#v %q", m.overlay, m.flash)
	}

	// The conch there is too old: refused when it connects.
	m, _, _ = moveSandboxModel(t)
	m.openMoveWorktree(featTarget)
	deliver(t, m, runItem(t, m, 0))
	for _, msg := range a2Run(m.overlay.(*dialog).submit(m, nil)) {
		if a, ok := msg.(machineAddedMsg); ok {
			added = a
		}
	}
	next, _ = m.Update(added)
	*m = next.(Model)
	mach = m.machine(added.m.ID)
	mach.c, mach.state = a2Client("session.v1"), stateOnline
	if cmd := m.resumeMove(mach.id); cmd != nil || !strings.Contains(m.flash, "predates moving worktrees") {
		t.Fatalf("old sandbox: %q", m.flash)
	}
	// Nothing held, or offline: nothing happens.
	if m.resumeMove(mach.id) != nil || m.resumeMove("nope") != nil {
		t.Fatal("resumed a move nobody held")
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
	if !strings.Contains(labels(feat), "T Move to another machine or a sandbox…") {
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
	m.rows = append(m.rows, row{id: "w:x", kind: kindWorkspace})
	m.cursor = "w:x"
	a1Key(t, m, runes("T"))
	if m.overlay != nil {
		t.Fatal("T on a workspace row opened something")
	}
	m.cursor = "p:r1"
	a1Key(t, m, runes("T"))
	if mu, ok := m.overlay.(*menu); !ok || mu.title != "Move api to which machine?" {
		t.Fatalf("project T: %v %q", m.overlay, m.flash)
	}
	m.overlay = nil
	m.cursor = "b:feat"
	a1Key(t, m, runes("T"))
	if mu, ok := m.overlay.(*menu); !ok || mu.title != "Move feat to which machine?" {
		t.Fatalf("tree T: %v %q", m.overlay, m.flash)
	}

	// An agent's row moves the worktree it works in; not a terminal, and
	// not an agent on the base branch.
	m, _, _, _ = moveModel(t)
	m.machines[0].panes = append(m.machines[0].panes, proto.PaneInfo{ID: "p8", Name: "claude", State: proto.PaneRunning,
		ProjectID: "r1", Cwd: "/src/api", Agent: &proto.AgentStatus{Name: "claude"}})
	agent := row{kind: kindPane, machine: localMachine, paneID: "p6"} // codex in /src/api-feat/sub, no branch said
	if !strings.Contains(labels(agent), "T Move to another machine or a sandbox, with its worktree…") {
		t.Fatalf("agent menu: %s", labels(agent))
	}
	for _, id := range []string{"p7", "p8"} {
		if strings.Contains(labels(row{kind: kindPane, machine: localMachine, paneID: id}), "Move to another") {
			t.Errorf("%s offers a move", id)
		}
	}
	m.focus = focusSidebar
	m.rows = []row{{id: "a:p6", kind: kindPane, machine: localMachine, paneID: "p6"},
		{id: "a:p7", kind: kindPane, machine: localMachine, paneID: "p7"}}
	m.cursor = "a:p7"
	a1Key(t, m, runes("T"))
	if m.overlay != nil {
		t.Fatal("T on a terminal opened something")
	}
	m.cursor = "a:p6"
	a1Key(t, m, runes("T"))
	if mu, ok := m.overlay.(*menu); !ok || mu.title != "Move feat to which machine?" {
		t.Fatalf("agent T: %v %q", m.overlay, m.flash)
	}
	m.overlay = nil
	if m.openMoveAgent(localMachine, "p8"); m.overlay != nil || !strings.Contains(m.flash, "main is the base branch") {
		t.Fatalf("base branch agent: %q", m.flash)
	}
	if m.openMoveAgent(localMachine, "nope"); m.overlay != nil || !strings.Contains(m.flash, "isn't in a worktree") {
		t.Fatalf("no pane: %q", m.flash)
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

// Several clones of the same repository there: the one in the same place
// under the home folder as here, else the first.
func TestMoveWorktreePicksTheSamePlace(t *testing.T) {
	clones := []proto.ProjectInfo{
		{ID: "r5", Name: "api", Path: "/srv/api", Git: true, Remote: "https://github.com/acme/api"},
		{ID: "r6", Name: "api", Path: "/home/b/api", Git: true, Remote: "git@github.com:acme/api"},
	}
	for want, projects := range map[string][]proto.ProjectInfo{"r6": clones, "r5": clones[:1]} {
		m, _, dst, remote := moveModel(t)
		remote.server.Home = "/home/b"
		remote.projects = projects
		deliver(t, m, toConfirm(t, m).submit(m, nil))
		dst.waitMethod(t, proto.MethodWorktreeUnpack, `"project_id":"`+want+`"`)
	}
}
