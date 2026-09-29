package tui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// plainTaskModel is an online local machine with a git project, a folder
// that isn't one, and the agents installed.
func plainTaskModel(t *testing.T) (*Model, *a1Peer) {
	t.Helper()
	m, peer := harvestModel(t, harvestCapability)
	mach := m.machines[0]
	mach.projects = append(mach.projects, proto.ProjectInfo{ID: "n1", Name: "notes", Path: "/src/notes"})
	mach.available = map[string]proto.AgentAvailability{
		"claude": {Name: "claude", Installed: true}, "codex": {Name: "codex", Installed: true}}
	m.rebuild()
	return m, peer
}

func paneCreates(t *testing.T, peer *a1Peer) []proto.PaneCreateParams {
	t.Helper()
	var out []proto.PaneCreateParams
	for _, msg := range peer.snapshot() {
		if msg.Method == proto.MethodPaneCreate {
			var p proto.PaneCreateParams
			if err := json.Unmarshal(msg.Params, &p); err != nil {
				t.Fatal(err)
			}
			out = append(out, p)
		}
		if msg.Method == proto.MethodTaskCreate || msg.Method == proto.MethodWorktreeAdd {
			t.Fatalf("a task with no repository asked for %s", msg.Method)
		}
	}
	return out
}

// t on a machine starts an agent in its home with the prompt: no project,
// no repository.
func TestPlainTaskOnMachine(t *testing.T) {
	m, peer := plainTaskModel(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	m.cursor = machineID(localMachine)
	a1Key(t, m, runes("t"))
	d, ok := m.overlay.(*dialog)
	if !ok {
		t.Fatalf("t on a machine: %T %q", m.overlay, m.flash)
	}
	if len(d.fields) != 3 || !strings.Contains(d.title, "New task · local") ||
		!strings.Contains(strings.Join(d.text, " "), "Starts the agent in the home directory of local with the prompt") {
		t.Fatalf("dialog: %q %q (%d fields)", d.title, d.text, len(d.fields))
	}

	peer.setResult(proto.MethodPaneCreate, proto.PaneInfo{ID: "np", Cwd: home})
	a2Type(m, d, "Plan a CLI for expenses")
	msgs := submitDialog(t, m)
	if _, ok := msgs[0].(createdMsg); !ok {
		t.Fatalf("submit: %#v", msgs)
	}
	got := paneCreates(t, peer)
	if len(got) != 1 {
		t.Fatalf("%d panes asked for", len(got))
	}
	if p := got[0]; p.Agent != "claude" || p.Prompt != "Plan a CLI for expenses" || p.Cwd != home || !p.NoProject || p.Cols == 0 {
		t.Fatalf("params %+v", p)
	}
}

// t on a folder that isn't a git repository starts the agent there, in
// that project; several attempts share it and say so.
func TestPlainTaskInAFolder(t *testing.T) {
	m, peer := plainTaskModel(t)
	m.cursor = projectNodeID(localMachine, "n1")
	a1Key(t, m, runes("t"))
	d, ok := m.overlay.(*dialog)
	if !ok {
		t.Fatalf("t on a plain folder: %T %q", m.overlay, m.flash)
	}
	text := func() string { return strings.Join(d.text, " ") }
	if !strings.Contains(d.title, "New task · notes") || !strings.Contains(text(), "Starts the agent in /src/notes") ||
		!strings.Contains(text(), "not a git repository") {
		t.Fatalf("dialog: %q %q", d.title, text())
	}
	a2Type(m, d, "Outline the talk")
	if strings.Contains(text(), "same folder") {
		t.Fatalf("one attempt warned: %q", text())
	}
	d.setFocus(1)
	a2Type(m, d, "claude,codex")
	if !strings.Contains(text(), "2 attempts of the same prompt, all in the same folder") {
		t.Fatalf("two agents: %q", text())
	}
	d.setFocus(2)
	a2Type(m, d, "0")
	if !strings.Contains(text(), "fewer than one") {
		t.Fatalf("bad count: %q", text())
	}
	if msg := a2ErrText(a2Run(d.submit(m, []string{"p", "", "0"}))); !strings.Contains(msg, "fewer than one") {
		t.Fatalf("submit with a bad count: %q", msg)
	}
	if msg := a2ErrText(a2Run(d.submit(m, []string{"  ", "", ""}))); msg != "a task needs a prompt" {
		t.Fatalf("no prompt: %q", msg)
	}
	if got := paneCreates(t, peer); len(got) != 0 {
		t.Fatalf("refused submits still asked: %+v", got)
	}

	peer.setResult(proto.MethodPaneCreate, proto.PaneInfo{ID: "np", Cwd: "/src/notes"})
	msgs := a2Run(d.submit(m, []string{"Outline the talk", "claude,codex", "3"}))
	done, ok := msgs[0].(attemptsDoneMsg)
	if !ok || len(done.panes) != 3 || !done.shared {
		t.Fatalf("attempts: %#v", msgs)
	}
	var agents []string
	for _, p := range paneCreates(t, peer) {
		if p.Cwd != "/src/notes" || p.NoProject || p.Prompt != "Outline the talk" {
			t.Fatalf("params %+v", p)
		}
		agents = append(agents, p.Agent)
	}
	if strings.Join(agents, " ") != "claude codex claude" {
		t.Fatalf("agents %v", agents)
	}
	m.receiveAttempts(done)
	if m.flashIsErr || m.flash != "started 3 attempts in the same folder" {
		t.Fatalf("flash %q", m.flash)
	}
}

func TestPlainTaskRefusals(t *testing.T) {
	m, peer := plainTaskModel(t)
	// An agent that isn't installed is offered for install, not started.
	m.machines[0].available["gemini"] = proto.AgentAvailability{Name: "gemini"}
	d := newPlainTaskDialog(*m, localMachine, "local", "the home directory of local", "", true)
	msgs := a2Run(d.submit(m, []string{"p", "gemini", ""}))
	if ask, ok := msgs[0].(askInstallMsg); !ok || ask.agent != "gemini" {
		t.Fatalf("missing agent: %#v", msgs)
	}
	// One attempt failing among several leaves the others; all failing
	// says why.
	peer.setError(proto.MethodPaneCreate, "unknown agent")
	done := a2Run(m.startPlainAttempts(localMachine, []proto.PaneCreateParams{{Agent: "claude"}, {Agent: "codex"}}))[0].(attemptsDoneMsg)
	if len(done.panes) != 0 || len(done.errs) != 2 || !strings.HasPrefix(done.errs[1], "codex: ") {
		t.Fatalf("failed: %+v", done)
	}
	m.receiveAttempts(done)
	if !m.flashIsErr || !strings.Contains(m.flash, "unknown agent") {
		t.Fatalf("flash %q", m.flash)
	}
	// Offline: nothing starts.
	offline, _ := a1Fixture(t, false)
	if msg := a2ErrText(a2Run(offline.startPlainAttempts(localMachine, []proto.PaneCreateParams{{Agent: "claude"}}))); !strings.Contains(msg, "local is") {
		t.Fatalf("offline: %q", msg)
	}
	// A git project still gets branches.
	m.cursor = projectNodeID(localMachine, "r1")
	m.overlay = nil
	a1Key(t, m, runes("t"))
	if d, ok := m.overlay.(*dialog); !ok || len(d.fields) != 5 {
		t.Fatalf("git project: %T", m.overlay)
	}
}

// The menus say what t does where it is pressed.
func TestPlainTaskMenus(t *testing.T) {
	m, _ := plainTaskModel(t)
	has := func(mu *menu, label string) bool {
		for _, it := range mu.items {
			if it.key == "t" && it.label == label {
				return true
			}
		}
		return false
	}
	if !has(newRowMenu(*m, row{kind: kindMachine, machine: localMachine}, 0, 0), "New task in the home directory…") {
		t.Fatal("machine menu has no task")
	}
	if !has(newRowMenu(*m, row{kind: kindProject, machine: localMachine, projectID: "n1"}, 0, 0), "New task (an agent with a prompt, here)") {
		t.Fatal("plain folder's menu")
	}
	if !has(newRowMenu(*m, row{kind: kindProject, machine: localMachine, projectID: "r1"}, 0, 0), "New task (branch + worktree + agent)") {
		t.Fatal("git project's menu")
	}
}

// t on a sandbox's row starts in that machine's home, which its server
// reported — not this computer's.
func TestPlainTaskOnASandbox(t *testing.T) {
	m, _ := plainTaskModel(t)
	c, peer := a1FakeClient(t)
	box := newMachine("box", "dt", "daytona:sb1")
	box.c, box.server = c, c.Server
	box.server.Home = "/home/daytona"
	box.state = stateOnline
	box.available = map[string]proto.AgentAvailability{"claude": {Name: "claude", Installed: true}}
	m.machines = append(m.machines, box)
	m.rebuild()

	m.cursor = machineID("box")
	a1Key(t, m, runes("t"))
	d, ok := m.overlay.(*dialog)
	if !ok {
		t.Fatalf("t on a sandbox: %T %q", m.overlay, m.flash)
	}
	if !strings.Contains(d.title, "New task · dt") || !strings.Contains(strings.Join(d.text, " "), "the home directory of dt") {
		t.Fatalf("dialog: %q %q", d.title, d.text)
	}
	peer.setResult(proto.MethodPaneCreate, proto.PaneInfo{ID: "r1", Cwd: "/home/daytona"})
	a2Type(m, d, "Plan it")
	msgs := submitDialog(t, m)
	if created, ok := msgs[0].(createdMsg); !ok || created.machine != "box" {
		t.Fatalf("submit: %#v", msgs)
	}
	got := paneCreates(t, peer)
	if len(got) != 1 || got[0].Cwd != "/home/daytona" || !got[0].NoProject || got[0].Prompt != "Plan it" {
		t.Fatalf("params %+v", got)
	}
	// An agent the sandbox lacks is offered for install there.
	box.available["codex"] = proto.AgentAvailability{Name: "codex"}
	if ask, ok := a2Run(d.submit(m, []string{"p", "codex", ""}))[0].(askInstallMsg); !ok || ask.machine != "box" {
		t.Fatal("missing agent on the sandbox was not offered for install")
	}
}

// From a pane, t works where the pane is: a machine-level terminal
// elsewhere than home names its folder, and a pane inside a folder with no
// repository keeps that folder's project.
func TestPlainTaskFromAPane(t *testing.T) {
	m, peer := plainTaskModel(t)
	t.Setenv("HOME", t.TempDir())
	mach := m.machines[0]
	mach.panes = append(mach.panes, proto.PaneInfo{ID: "p8", Name: "zsh", State: proto.PaneRunning, ProjectID: "n1", Cwd: "/src/notes/talk"})
	m.rebuild()

	// p3 is a machine-level shell in /tmp/a1.
	m.cursor = paneNodeID(localMachine, "p3")
	a1Key(t, m, runes("t"))
	d, ok := m.overlay.(*dialog)
	if !ok || !strings.Contains(strings.Join(d.text, " "), "Starts the agent in /tmp/a1 ") || !strings.Contains(d.title, "New task · local") {
		t.Fatalf("loose pane: %T %q", m.overlay, m.flash)
	}
	peer.setResult(proto.MethodPaneCreate, proto.PaneInfo{ID: "np"})
	a2Run(d.submit(m, []string{"p", "", ""}))
	if got := paneCreates(t, peer); len(got) != 1 || got[0].Cwd != "/tmp/a1" || !got[0].NoProject {
		t.Fatalf("loose pane params %+v", got)
	}

	m.overlay = nil
	m.cursor = paneNodeID(localMachine, "p8")
	a1Key(t, m, runes("t"))
	d, ok = m.overlay.(*dialog)
	if !ok || !strings.Contains(d.title, "New task · notes") || !strings.Contains(strings.Join(d.text, " "), "/src/notes/talk") {
		t.Fatalf("pane in a plain folder: %T %q", m.overlay, m.flash)
	}
	a2Run(d.submit(m, []string{"p", "", ""}))
	if got := paneCreates(t, peer); len(got) != 2 || got[1].Cwd != "/src/notes/talk" || got[1].NoProject {
		t.Fatalf("plain folder pane params %+v", got)
	}
}

// Each agent's plan limit is named once, and the dialog fits any screen.
func TestPlainTaskDialogWarningsAndSizes(t *testing.T) {
	m, _ := plainTaskModel(t)
	a5Limits(m, "claude", 97, 25*time.Minute)
	d := newPlainTaskDialog(*m, localMachine, "a-machine-with-a-long-label", "/a/rather/long/path/that/will/not/fit/on/a/small/screen", "/a/rather/long/path", false)
	m.overlay = d
	d.setFocus(1)
	a2Type(m, d, "claude,claude")
	text := strings.Join(d.text, " ")
	if n := strings.Count(text, "Claude's 5-hour limit"); n != 1 {
		t.Fatalf("%d limit warnings in %q", n, text)
	}
	for _, size := range [][2]int{{1, 1}, {20, 5}, {39, 12}, {200, 60}} {
		m.width, m.height = size[0], size[1]
		for _, line := range strings.Split(m.View(), "\n") {
			if a2Width(line) > size[0] {
				t.Fatalf("%v: line wider than the screen: %q", size, line)
			}
		}
	}
}
