package tui

import (
	"net"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
)

// a1Peer is an in-memory stand-in for the far end of a client connection:
// it answers the hello handshake and records everything else it receives.
// It is not a conch server; nothing leaves the process.
type a1Peer struct {
	mu     sync.Mutex
	msgs   []proto.Message
	errors map[string]string // method -> error message to answer with
}

// a1FakeClient connects a client.Client to an a1Peer over net.Pipe.
func a1FakeClient(t *testing.T, caps ...string) (*client.Client, *a1Peer) {
	t.Helper()
	cs, ss := net.Pipe()
	peer := &a1Peer{errors: map[string]string{}}
	go func() {
		conn := proto.NewConn(ss)
		defer conn.Close()
		for {
			msg, err := conn.Read()
			if err != nil {
				return
			}
			if msg.Method == proto.MethodHello {
				_ = conn.Write(proto.Message{ID: msg.ID, Result: proto.Marshal(proto.HelloResult{
					Version: proto.Version, Protocol: proto.ProtocolVersion, Capabilities: caps, Home: "/home/a1"})})
				continue
			}
			peer.mu.Lock()
			peer.msgs = append(peer.msgs, msg)
			errText := peer.errors[msg.Method]
			peer.mu.Unlock()
			if msg.ID == "" {
				continue
			}
			reply := proto.Message{ID: msg.ID}
			if errText != "" {
				reply.Error = &proto.Error{Code: "a1", Message: errText}
			}
			_ = conn.Write(reply)
		}
	}()
	c, err := client.New(cs, "a1-test")
	if err != nil {
		t.Fatalf("fake client: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c, peer
}

func (p *a1Peer) setError(method, text string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.errors[method] = text
}

// methods lists the methods received so far.
func (p *a1Peer) methods() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []string
	for _, m := range p.msgs {
		out = append(out, m.Method)
	}
	return out
}

func (p *a1Peer) snapshot() []proto.Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]proto.Message(nil), p.msgs...)
}

// waitFor waits until a received message satisfies ok, failing after a
// generous deadline (messages travel through goroutines).
func (p *a1Peer) waitFor(t *testing.T, what string, ok func(proto.Message) bool) proto.Message {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		for _, m := range p.snapshot() {
			if ok(m) {
				return m
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("never received %s; got %v", what, p.methods())
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// waitMethod waits for a message of method whose params contain want
// (compared as raw JSON substring when want != "").
func (p *a1Peer) waitMethod(t *testing.T, method, want string) proto.Message {
	t.Helper()
	return p.waitFor(t, method+" "+want, func(m proto.Message) bool {
		return m.Method == method && (want == "" || a1Contains(string(m.Params), want))
	})
}

// count reports how many messages of method (with params containing want)
// were received, after letting in-flight notifications settle behind a
// marker call.
func (p *a1Peer) count(t *testing.T, c *client.Client, method, want string) int {
	t.Helper()
	// A synchronous call goes through the same ordered outbox, so every
	// notification sent before it has arrived once it returns.
	_ = callCtx(c, "a1.flush", nil, nil)
	n := 0
	for _, m := range p.snapshot() {
		if m.Method == method && (want == "" || a1Contains(string(m.Params), want)) {
			n++
		}
	}
	return n
}

func a1Contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && a1Index(s, sub) >= 0)
}

func a1Index(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// a1Fixture builds a model with one local machine: project r1 "api" (git,
// with a linked worktree and a PR) holding agent p1 and terminal p2, and
// outside every project agent p4 and terminal p3. With withClient the
// machine is online through an a1Peer.
func a1Fixture(t *testing.T, withClient bool) (*Model, *a1Peer) {
	t.Helper()
	agent := &proto.AgentStatus{Name: "claude", State: proto.AgentIdle}
	panes := []proto.PaneInfo{
		{ID: "p1", Name: "claude", State: proto.PaneRunning, ProjectID: "r1", Branch: "feat", Cwd: "/src/api-feat", Agent: agent},
		{ID: "p2", Name: "zsh", State: proto.PaneRunning, ProjectID: "r1", Cwd: "/src/api"},
		{ID: "p3", Name: "bash", State: proto.PaneRunning, Cwd: "/tmp/a1"},
		{ID: "p4", Name: "codex", State: proto.PaneRunning, Agent: &proto.AgentStatus{Name: "codex", State: proto.AgentWorking}},
	}
	proj := proto.ProjectInfo{ID: "r1", Name: "api", Path: "/src/api", Git: true, Base: "main",
		Branches: []proto.BranchInfo{
			{Name: "main", Committed: time.Now(), Worktree: "/src/api"},
			{Name: "feat", Committed: time.Now(), Worktree: "/src/api-feat", BaseAhead: 2, BaseBehind: 1,
				PR: &proto.PRInfo{Number: 7, State: "OPEN", Checks: "fail", Title: "Feature", URL: "https://example.invalid/pr/7"}},
		},
		Worktrees: []proto.WorktreeInfo{
			{Path: "/src/api", Branch: "main", Main: true},
			{Path: "/src/api-feat", Branch: "feat", Status: &proto.GitStatus{Files: 2, Added: 3, Deleted: 1}},
		}}
	mach := newMachine(localMachine, "local", "")
	mach.panes = panes
	mach.projects = []proto.ProjectInfo{proj}
	mach.state = stateOnline
	var peer *a1Peer
	if withClient {
		var c *client.Client
		c, peer = a1FakeClient(t)
		mach.c, mach.server = c, c.Server
	}
	m := &Model{cfg: config.Default(), width: 160, height: 40, sidebarW: 30,
		expanded: map[string]bool{}, showAll: map[string]bool{},
		frames: map[string]*proto.Frame{}, subscribed: map[string]bool{},
		sessions: map[string]*sessionsData{},
		machines: []*machine{mach}}
	m.rebuild()
	return m, peer
}

// a1At moves the tree cursor to row id as a cursor move does.
func a1At(t *testing.T, m *Model, id string) {
	t.Helper()
	if indexOfRow(m.rows, id) < 0 {
		t.Fatalf("no row %q in %s", id, render(m.rows))
	}
	m.cursor = id
	m.syncView()
}

// a1Open shows row id as enter or a click does.
func a1Open(t *testing.T, m *Model, id string) {
	t.Helper()
	i := indexOfRow(m.rows, id)
	if i < 0 {
		t.Fatalf("no row %q in %s", id, render(m.rows))
	}
	m.cursor = id
	m.show(m.rows[i])
}

// a1Key sends a key through handleKey.
func a1Key(t *testing.T, m *Model, k tea.KeyMsg) tea.Cmd {
	t.Helper()
	next, cmd := m.handleKey(k)
	*m = next.(Model)
	return cmd
}

// a1Prefixed sends the prefix, then k.
func a1Prefixed(t *testing.T, m *Model, k tea.KeyMsg) tea.Cmd {
	t.Helper()
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyCtrlB})
	if !m.prefixArmed {
		t.Fatal("prefix not armed")
	}
	return a1Key(t, m, k)
}

// a1Mouse sends a mouse event through handleMouse.
func a1Mouse(t *testing.T, m *Model, x, y int, b tea.MouseButton, a tea.MouseAction) tea.Cmd {
	t.Helper()
	next, cmd := m.handleMouse(tea.MouseMsg{X: x, Y: y, Button: b, Action: a})
	*m = next.(Model)
	return cmd
}

func a1PaneIDs(m *Model, idx []int) []string {
	var out []string
	for _, i := range idx {
		out = append(out, m.tabs[i].root.leaves()[0].view.PaneID)
	}
	return out
}
