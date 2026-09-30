package server

import (
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/proto"
)

// A pane's program and anything it starts, however deep, are in the pane;
// this process, and processes that aren't there, are in none.
func TestCallerPane(t *testing.T) {
	s, _, work := shareFixture(t)
	e := agentPane(t, s, "p1", "", work, `sh -c 'sleep 30 & echo "child:$!"; wait' & wait`)
	var child int
	re := regexp.MustCompile(`child:(\d+)`)
	a5WaitFor(t, "the grandchild's pid", func() bool {
		m := re.FindStringSubmatch(strings.Join(e.p.PlainLines(), "\n"))
		if m != nil {
			child, _ = strconv.Atoi(m[1])
		}
		return child > 0
	})
	for pid, want := range map[int]string{
		e.p.Info().PID: "p1", // the pane's own program
		child:          "p1", // two levels down
		os.Getpid():    "",   // the test, outside every pane
		os.Getppid():   "",
		1:              "",
		0:              "",
		-5:             "",
		1 << 30:        "", // no such process
	} {
		if got := s.callerPane(pid); got != want {
			t.Errorf("callerPane(%d) = %q, want %q", pid, got, want)
		}
	}
	// A pane that has ended holds nobody, even if its pid comes round again.
	e.p.Close()
	a5WaitFor(t, "p1 exits", func() bool { return e.info().State == proto.PaneExited })
	if got := s.callerPane(e.p.Info().PID); got != "" {
		t.Fatalf("an ended pane still claims its pid: %q", got)
	}
}

func TestPeerPID(t *testing.T) {
	sock := filepath.Join(shortTempDir(t), "s.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		if c, err := net.Dial("unix", sock); err == nil {
			defer c.Close()
			buf := make([]byte, 1)
			c.Read(buf)
		}
	}()
	c, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if got := peerPID(c); got != os.Getpid() {
		t.Fatalf("peer %d, want this process %d", got, os.Getpid())
	}
	// Not a unix socket: nobody to ask.
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	if got := peerPID(a); got != 0 {
		t.Fatalf("pipe peer %d", got)
	}
}

func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "sc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// lineageServer has four agent panes in no project: p1 started p2, which
// started p3; and p4 on its own.
func lineageServer(t *testing.T) *Server {
	t.Helper()
	s, _, work := shareFixture(t)
	for _, id := range []string{"p1", "p2", "p3", "p4"} {
		agentPane(t, s, id, "claude", work, "stty -echo; exec cat")
	}
	s.madeBy(&client{pane: "p1"}, "p2")
	s.madeBy(&client{pane: "p2"}, "p3")
	return s
}

func TestMadeBy(t *testing.T) {
	s := lineageServer(t)
	lineage := func(id string) string {
		e, _ := s.get(id)
		return strings.Join(e.creators(), ",")
	}
	if lineage("p2") != "p1" || lineage("p3") != "p2,p1" || lineage("p1") != "" || lineage("p4") != "" {
		t.Fatalf("lineage: p2=%q p3=%q", lineage("p2"), lineage("p3"))
	}
	if e, _ := s.get("p3"); e.info().CreatedBy != "p2" {
		t.Fatalf("CreatedBy %q", e.info().CreatedBy)
	}
	// A creator is set once: a later call can't take the pane over.
	s.madeBy(&client{pane: "p4"}, "p3")
	// Nor are these creators: nobody, the pane itself, a pane not there.
	s.madeBy(&client{}, "p4")
	s.madeBy(&client{pane: "p4"}, "p4")
	s.madeBy(&client{pane: "p4"}, "p404")
	if lineage("p3") != "p2,p1" || lineage("p4") != "" {
		t.Fatalf("overwritten: p3=%q p4=%q", lineage("p3"), lineage("p4"))
	}
}

// Down the lineage, and only down it: p1 reaches what p2 started even
// after p2 is gone; p3 can't reach up to p1; p4 reaches nobody's.
func TestScopeLineage(t *testing.T) {
	s := lineageServer(t)
	if err := s.close("p2"); err != nil {
		t.Fatal(err)
	}
	closeFrom := func(from, to string) *proto.Error {
		return s.inScope(&client{pane: from}, proto.Message{Method: proto.MethodPaneClose, Params: proto.Marshal(proto.PaneRef{ID: to})})
	}
	for _, c := range []struct {
		from, to string
		ok       bool
	}{
		{"p1", "p3", true},  // its grandchild, its child gone
		{"p3", "p3", true},  // itself
		{"p3", "p1", false}, // its creator's creator is not its own
		{"p4", "p3", false},
		{"p4", "p1", false},
		{"p1", "p404", true}, // nothing there: the method says so
		{"", "p1", true},     // from outside the panes
		{"p404", "p1", true}, // from a pane that has gone: nobody to scope
	} {
		perr := closeFrom(c.from, c.to)
		if (perr == nil) != c.ok || (perr != nil && perr.Code != proto.ErrOutOfScope) {
			t.Errorf("%s closes %s: %v, want ok=%v", c.from, c.to, perr, c.ok)
		}
	}
	perr := closeFrom("p4", "p3")
	if perr == nil || perr.Message != "the claude agent in p4 may not close p3: it did not start it, and it is in another project; do it from the TUI or a terminal pane" {
		t.Fatalf("message: %v", perr)
	}
}

func TestScopeMethods(t *testing.T) {
	s := lineageServer(t)
	p4 := &client{pane: "p4"}
	msg := func(method string, params any) proto.Message {
		return proto.Message{Method: method, Params: proto.Marshal(params)}
	}
	for _, c := range []struct {
		name string
		msg  proto.Message
		ok   bool
	}{
		{"read another's pane", msg(proto.MethodPaneRead, proto.PaneRef{ID: "p1"}), true},
		{"list", msg(proto.MethodPaneList, nil), true},
		{"search another's pane", msg(proto.MethodPaneSearch, proto.PaneRef{ID: "p1"}), true},
		{"report for another", msg(proto.MethodAgentReport, proto.AgentReportParams{ID: "p1", Event: "Stop"}), false},
		{"report for itself", msg(proto.MethodAgentReport, proto.AgentReportParams{ID: "p4", Event: "Stop"}), true},
		{"prompt another", msg(proto.MethodAgentPrompt, proto.AgentPromptParams{ID: "p1", Text: "x"}), false},
		{"broadcast to itself", msg(proto.MethodAgentBroadcast, proto.AgentBroadcastParams{IDs: []string{"p4"}, Text: "x"}), true},
		{"broadcast with one out", msg(proto.MethodAgentBroadcast, proto.AgentBroadcastParams{IDs: []string{"p4", "p1"}, Text: "x"}), false},
		{"hand a session to another's pane", msg(proto.MethodSessionShare, proto.SessionShareParams{ID: "s", PaneID: "p1"}), false},
		{"hand a session to a new pane", msg(proto.MethodSessionShare, proto.SessionShareParams{ID: "s", To: "codex"}), true},
		{"stop", msg(proto.MethodServerStop, nil), false},
		{"reload", proto.Message{Method: proto.MethodServerReload}, false},
		{"bad params", proto.Message{Method: proto.MethodPaneClose, Params: []byte(`{"id":`)}, true}, // the method's own error
		{"a project that isn't there", msg(proto.MethodBranchDiscard, proto.BranchDiscardParams{ProjectID: "r404"}), true},
	} {
		perr := s.inScope(p4, c.msg)
		if (perr == nil) != c.ok {
			t.Errorf("%s: %v, want ok=%v", c.name, perr, c.ok)
		}
	}

	// A pane whose agent has gone — a shell again — is the person's.
	shell := agentPane(t, s, "p9", "", t.TempDir(), "stty -echo; exec cat")
	if perr := s.inScope(&client{pane: shell.info().ID}, msg(proto.MethodPaneClose, proto.PaneRef{ID: "p1"})); perr != nil {
		t.Fatalf("no agent there: %v", perr)
	}
	// Nor does an ended agent pane scope anything.
	e, _ := s.get("p4")
	e.p.Close()
	a5WaitFor(t, "p4 exits", func() bool { return e.info().State == proto.PaneExited })
	if perr := s.inScope(p4, msg(proto.MethodPaneClose, proto.PaneRef{ID: "p1"})); perr != nil {
		t.Fatalf("ended: %v", perr)
	}
}

// A project is the agent's when it works in it; a pane in no project has
// none, so every project is another's.
func TestScopeProjects(t *testing.T) {
	s, _, work := shareFixture(t)
	other := filepath.Join(t.TempDir(), "other")
	a5GitRepo(t, other)
	mine, err := s.projects.add(work, false)
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := s.projects.add(other, false)
	if err != nil {
		t.Fatal(err)
	}
	in := agentPane(t, s, "p1", "claude", work, "stty -echo; exec cat")
	in.mu.Lock()
	in.project = mine
	in.mu.Unlock()
	loose := agentPane(t, s, "p2", "claude", work, "stty -echo; exec cat") // in no project

	discard := func(from *entry, proj string) *proto.Error {
		return s.inScope(&client{pane: from.info().ID}, proto.Message{Method: proto.MethodBranchDiscard,
			Params: proto.Marshal(proto.BranchDiscardParams{ProjectID: proj, Branch: "b"})})
	}
	remove := func(from *entry, proj string) *proto.Error {
		return s.inScope(&client{pane: from.info().ID}, proto.Message{Method: proto.MethodProjectRemove,
			Params: proto.Marshal(proto.ProjectRef{ID: proj})})
	}
	if perr := discard(in, mine.id); perr != nil {
		t.Fatalf("its own project: %v", perr)
	}
	if perr := discard(in, theirs.id); perr == nil || !strings.Contains(perr.Message, "discard a branch in project "+theirs.id+": it works in project "+mine.id) {
		t.Fatalf("another project: %v", perr)
	}
	if perr := remove(in, theirs.id); perr == nil {
		t.Fatal("removed another project")
	}
	// The git panel's commands change a worktree as much as a commit does.
	gitIn := func(proj string) *proto.Error {
		return s.inScope(&client{pane: in.info().ID}, proto.Message{Method: proto.MethodBranchGit,
			Params: proto.Marshal(proto.BranchGitParams{ProjectID: proj, Branch: "b", Commands: [][]string{{"reset", "--hard"}}})})
	}
	if perr := gitIn(mine.id); perr != nil {
		t.Fatalf("git in its own project: %v", perr)
	}
	if perr := gitIn(theirs.id); perr == nil || !strings.Contains(perr.Message, "run git in project "+theirs.id) {
		t.Fatalf("git in another project: %v", perr)
	}
	if perr := discard(loose, mine.id); perr == nil || !strings.Contains(perr.Message, "it works in no project") {
		t.Fatalf("from no project: %v", perr)
	}
	// Two panes in one project reach each other, whoever started them.
	peer := agentPane(t, s, "p3", "claude", work, "stty -echo; exec cat")
	peer.mu.Lock()
	peer.project = mine
	peer.mu.Unlock()
	if perr := s.inScope(&client{pane: "p1"}, proto.Message{Method: proto.MethodPaneClose, Params: proto.Marshal(proto.PaneRef{ID: "p3"})}); perr != nil {
		t.Fatalf("same project: %v", perr)
	}
}

func TestVerbs(t *testing.T) {
	for m := range scoped {
		if _, ok := verbs[m]; !ok {
			t.Errorf("%s has no verb for its refusal", m)
		}
	}
	if verb("x.y") != "use x.y on" {
		t.Fatal(verb("x.y"))
	}
}
