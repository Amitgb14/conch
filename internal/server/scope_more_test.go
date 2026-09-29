package server

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// fakeLoginShell makes every agent and installer this server launches a
// sleep: nothing real runs, and the pane stays up to be looked at.
func fakeLoginShell(t *testing.T) {
	t.Helper()
	sh := filepath.Join(t.TempDir(), "fake-shell")
	if err := os.WriteFile(sh, []byte("#!/bin/sh\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", sh)
}

// Every method in scoped, from an agent in project "mine": refused on
// another's pane or project, let through on its own. A method added to
// scoped is checked here without a line more.
func TestScopeEveryMethod(t *testing.T) {
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
	me := agentPane(t, s, "p1", "claude", work, "stty -echo; exec cat")
	stranger := agentPane(t, s, "p2", "claude", other, "stty -echo; exec cat")
	for e, p := range map[*entry]*project{me: mine, stranger: theirs} {
		e.mu.Lock()
		e.project = p
		e.mu.Unlock()
	}
	caller := &client{pane: "p1"}
	params := func(kind scopeKind, pane, proj string) json.RawMessage {
		switch kind {
		case scopePane:
			return proto.Marshal(map[string]any{"id": pane, "text": "x"})
		case scopePanes:
			return proto.Marshal(map[string]any{"ids": []string{pane}, "text": "x"})
		case scopeProjID:
			return proto.Marshal(map[string]any{"id": proj})
		case scopeProject:
			return proto.Marshal(map[string]any{"project_id": proj, "branch": "b"})
		}
		return nil
	}
	for method, kind := range scoped {
		out := s.inScope(caller, proto.Message{Method: method, Params: params(kind, "p2", theirs.id)})
		if out == nil || out.Code != proto.ErrOutOfScope {
			t.Errorf("%s on another's: %v", method, out)
		}
		in := s.inScope(caller, proto.Message{Method: method, Params: params(kind, "p1", mine.id)})
		if kind == scopeServer || kind == scopeHome {
			if in == nil {
				t.Errorf("%s: the server and the person's setup are never an agent's", method)
			}
		} else if in != nil {
			t.Errorf("%s on its own: %v", method, in)
		}
	}
	// And the one method whose target depends on its params.
	share := func(pane string) *proto.Error {
		return s.inScope(caller, proto.Message{Method: proto.MethodSessionShare, Params: proto.Marshal(proto.SessionShareParams{ID: "c1", PaneID: pane})})
	}
	if share("p2") == nil || share("p1") != nil || share("") != nil {
		t.Errorf("session.share: to another %v, to itself %v, to a new pane %v", share("p2"), share("p1"), share(""))
	}
	// Methods that only read or start things are never checked.
	for _, m := range []string{proto.MethodPaneList, proto.MethodPaneRead, proto.MethodPaneSubscribe, proto.MethodPaneSearch,
		proto.MethodAgentExplain, proto.MethodPaneCreate, proto.MethodTaskCreate, proto.MethodSessionList, proto.MethodProjectChanges} {
		if perr := s.inScope(caller, proto.Message{Method: m, Params: params(scopePane, "p2", theirs.id)}); perr != nil {
			t.Errorf("%s: %v", m, perr)
		}
	}
}

// Every way a pane gets started records the pane it was started from —
// and none from outside the panes.
func TestCreatorEveryWay(t *testing.T) {
	s, _, work := shareFixture(t)
	fakeLoginShell(t)
	proj, err := s.projects.add(work, false)
	if err != nil {
		t.Fatal(err)
	}
	agentPane(t, s, "p1", "claude", work, "stty -echo; exec cat")
	s.nextID = 1 // agentPane placed p1 by hand

	for _, c := range []struct {
		name   string
		method string
		params func(from string) any
	}{
		{"pane", proto.MethodPaneCreate, func(string) any {
			return proto.PaneCreateParams{Command: []string{"/bin/sleep", "30"}, Cwd: work}
		}},
		{"task", proto.MethodTaskCreate, func(from string) any { // each on a branch of its own
			return proto.TaskCreateParams{ProjectID: proj.id, Prompt: "review it", Branch: "review-" + from + "x"}
		}},
		{"install", proto.MethodAgentInstall, func(string) any { return proto.AgentInstallParams{Agent: "codex"} }},
		{"resume", proto.MethodSessionResume, func(string) any { return proto.SessionRef{Agent: "claude", ID: "c1", Dir: work} }},
		{"share", proto.MethodSessionShare, func(string) any {
			return proto.SessionShareParams{Agent: "claude", ID: "c1", Dir: work, To: "codex"}
		}},
	} {
		for _, from := range []string{"p1", ""} {
			res, perr := s.dispatch(&client{pane: from}, proto.Message{Method: c.method, Params: proto.Marshal(c.params(from))})
			if perr != nil {
				t.Fatalf("%s from %q: %v", c.name, from, perr)
			}
			info, ok := res.(proto.PaneInfo)
			if sr, isShare := res.(proto.SessionShareResult); isShare {
				info, ok = sr.Pane, true
			}
			if !ok || info.ID == "" {
				t.Fatalf("%s: no pane in %#v", c.name, res)
			}
			// The answer says so, and so does the pane from then on.
			e, _ := s.get(info.ID)
			if info.CreatedBy != from || e.info().CreatedBy != from {
				t.Errorf("%s from %q: answered %q, pane says %q", c.name, from, info.CreatedBy, e.info().CreatedBy)
			}
		}
	}
	// Handing a session to a running pane starts nothing, so names no one.
	res, perr := s.dispatch(&client{pane: "p1"}, proto.Message{Method: proto.MethodSessionShare,
		Params: proto.Marshal(proto.SessionShareParams{Agent: "claude", ID: "c1", Dir: work, PaneID: "p1"})})
	if perr != nil {
		t.Fatal(perr)
	}
	if got := res.(proto.SessionShareResult).Pane; got.ID != "p1" || got.CreatedBy != "" {
		t.Fatalf("share into a pane: %+v", got)
	}
}

// A refused server.stop is answered and the server carries on; the same
// call from outside the panes still stops it.
func TestRefusedStopKeepsServer(t *testing.T) {
	s, _, work := shareFixture(t)
	agentPane(t, s, "p1", "claude", work, "stty -echo; exec cat")

	stop := func(from string) proto.Message {
		a, b := net.Pipe()
		defer a.Close()
		defer b.Close()
		got := make(chan proto.Message, 1)
		go func() {
			m, _ := proto.NewConn(b).Read()
			got <- m
		}()
		s.handle(&client{conn: proto.NewConn(a), pane: from}, proto.Message{ID: "1", Method: proto.MethodServerStop})
		select {
		case m := <-got:
			return m
		case <-time.After(5 * time.Second):
			t.Fatal("no answer")
		}
		return proto.Message{}
	}
	stopped := func() bool {
		select {
		case <-s.quit:
			return true
		default:
			return false
		}
	}
	if m := stop("p1"); m.Error == nil || m.Error.Code != proto.ErrOutOfScope {
		t.Fatalf("from the agent: %+v", m)
	}
	if stopped() {
		t.Fatal("a refused stop stopped the server")
	}
	if m := stop(""); m.Error != nil {
		t.Fatalf("from outside: %+v", m.Error)
	}
	if !stopped() {
		t.Fatal("a stop from outside the panes didn't stop it")
	}
}

func TestActFor(t *testing.T) {
	s, _, work := shareFixture(t)
	agentPane(t, s, "p1", "claude", work, "stty -echo; exec cat")
	far := &client{}
	for _, c := range []struct {
		c    *client
		p    proto.ActForParams
		want string
	}{
		{far, proto.ActForParams{}, "needs the caller's id"},
		{&client{pane: "p1"}, proto.ActForParams{ID: "laptop/p4@1"}, "comes from pane p1 here"},
		{far, proto.ActForParams{ID: "laptop/p4@1", Agent: "claude"}, ""},
		{far, proto.ActForParams{ID: "laptop/p4@1", Agent: "claude"}, ""}, // the same again is fine
		{far, proto.ActForParams{ID: "laptop/p9@2"}, "already acts for laptop/p4@1"},
	} {
		perr := s.actFor(c.c, c.p)
		if (c.want == "") != (perr == nil) || (perr != nil && !strings.Contains(perr.Message, c.want)) {
			t.Errorf("%+v: %v, want %q", c.p, perr, c.want)
		}
	}
	if far.actFor.ID != "laptop/p4@1" {
		t.Fatalf("held to %q", far.actFor.ID)
	}
}

func TestCallerInfo(t *testing.T) {
	s, _, work := shareFixture(t)
	agent := agentPane(t, s, "p1", "claude", work, "stty -echo; exec cat")
	agentPane(t, s, "p2", "", work, "stty -echo; exec cat")
	host, _ := os.Hostname()
	if got := s.callerInfo(&client{}); got != (proto.CallerInfo{}) {
		t.Errorf("outside: %+v", got)
	}
	if got := s.callerInfo(&client{pane: "p2"}); got != (proto.CallerInfo{Pane: "p2"}) {
		t.Errorf("terminal: %+v", got)
	}
	want := proto.CallerInfo{Pane: "p1", Agent: "claude", Scoped: true,
		ID: host + "/p1@" + strconv.FormatInt(agent.info().Created.Unix(), 10), Label: "p1 on " + host}
	if got := s.callerInfo(&client{pane: "p1"}); got != want {
		t.Errorf("agent: %+v, want %+v", got, want)
	}
	if got := s.callerInfo(&client{pane: "p404"}); got != (proto.CallerInfo{}) {
		t.Errorf("gone: %+v", got)
	}
}

// A caller from another machine has no pane or project here, only what it
// started: every scoped method is refused on the rest and let through on
// that, and a project is its own once it started a pane there.
func TestScopeRemoteCaller(t *testing.T) {
	s, _, work := shareFixture(t)
	fakeLoginShell(t)
	proj, err := s.projects.add(work, false)
	if err != nil {
		t.Fatal(err)
	}
	local := agentPane(t, s, "p1", "claude", work, "stty -echo; exec cat") // someone else's, in the project
	local.mu.Lock()
	local.project = proj
	local.mu.Unlock()
	s.nextID = 1
	far := &client{}
	if perr := s.actFor(far, proto.ActForParams{ID: "laptop/p4@1", Label: "p4 on laptop", Agent: "codex"}); perr != nil {
		t.Fatal(perr)
	}
	msg := func(method string, params any) proto.Message {
		return proto.Message{Method: method, Params: proto.Marshal(params)}
	}
	// Before it starts anything, the project isn't its own either.
	perr := s.inScope(far, msg(proto.MethodBranchDiscard, proto.BranchDiscardParams{ProjectID: proj.id, Branch: "b"}))
	if perr == nil || perr.Message != "the codex agent in p4 on laptop may not discard a branch in project "+proj.id+": it started nothing there; do it from the TUI or a terminal pane" {
		t.Fatalf("project before: %v", perr)
	}
	res, perr := s.dispatch(far, msg(proto.MethodPaneCreate, proto.PaneCreateParams{Command: []string{"/bin/sleep", "30"}, Cwd: work}))
	if perr != nil {
		t.Fatal(perr)
	}
	mine := res.(proto.PaneInfo)
	if mine.CreatedBy != "laptop/p4@1" {
		t.Fatalf("created by %q", mine.CreatedBy)
	}
	// What its pane starts is its too.
	agentPane(t, s, "p7", "", work, "exec sleep 30")
	s.madeBy(&client{pane: mine.ID}, "p7")

	for method, kind := range scoped {
		target, own := "p1", mine.ID
		var out, in any
		switch kind {
		case scopePane:
			out, in = map[string]any{"id": target, "text": "x"}, map[string]any{"id": own, "text": "x"}
		case scopePanes:
			out, in = map[string]any{"ids": []string{own, target}, "text": "x"}, map[string]any{"ids": []string{own, "p7"}, "text": "x"}
		case scopeProjID, scopeProject:
			continue // below
		case scopeServer, scopeHome:
			out = nil
		}
		if perr := s.inScope(far, msg(method, out)); perr == nil || perr.Code != proto.ErrOutOfScope {
			t.Errorf("%s on another's: %v", method, perr)
		}
		if kind != scopeServer && kind != scopeHome {
			if perr := s.inScope(far, msg(method, in)); perr != nil {
				t.Errorf("%s on its own: %v", method, perr)
			}
		}
	}
	if perr := s.inScope(far, msg(proto.MethodPaneClose, proto.PaneRef{ID: "p7"})); perr != nil {
		t.Errorf("what its pane started: %v", perr)
	}
	// Same project as p1 is no leeway for a caller with no project here.
	if perr := s.inScope(far, msg(proto.MethodPaneClose, proto.PaneRef{ID: "p1"})); perr == nil ||
		!strings.HasSuffix(perr.Message, "may not close p1: it did not start it; do it from the TUI or a terminal pane") {
		t.Errorf("p1: %v", perr)
	}
	// Now it has a pane in the project, the project's branches are its.
	if perr := s.inScope(far, msg(proto.MethodBranchDiscard, proto.BranchDiscardParams{ProjectID: proj.id, Branch: "b"})); perr != nil {
		t.Errorf("project after: %v", perr)
	}
	if perr := s.inScope(far, msg(proto.MethodProjectRemove, proto.ProjectRef{ID: proj.id})); perr != nil {
		t.Errorf("remove its project: %v", perr)
	}
	// A caller that gave no agent name is still named in a refusal.
	anon := &client{}
	s.actFor(anon, proto.ActForParams{ID: "box/p2@3"})
	if perr := s.inScope(anon, msg(proto.MethodPaneClose, proto.PaneRef{ID: "p1"})); perr == nil ||
		!strings.HasPrefix(perr.Message, "the agent in box/p2@3 may not close p1") {
		t.Errorf("anonymous: %v", perr)
	}
}

// Slow methods run beside the connection's reader, so a declaration and
// the checks that read it can meet; -race says whether they do safely.
func TestActForConcurrent(t *testing.T) {
	s, _, work := shareFixture(t)
	agentPane(t, s, "p1", "claude", work, "stty -echo; exec cat")
	far := &client{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			s.inScope(far, proto.Message{Method: proto.MethodPaneClose, Params: proto.Marshal(proto.PaneRef{ID: "p1"})})
			s.madeBy(far, "p404")
		}
	}()
	for i := 0; i < 200; i++ {
		s.actFor(far, proto.ActForParams{ID: "laptop/p4@1"})
	}
	<-done
	if perr := s.inScope(far, proto.Message{Method: proto.MethodPaneClose, Params: proto.Marshal(proto.PaneRef{ID: "p1"})}); perr == nil {
		t.Fatal("declared, yet not held")
	}
}

// The same pane ID on the same machine, started later, is someone else:
// it can't reach what the first started.
func TestActForReusedPaneID(t *testing.T) {
	s, _, work := shareFixture(t)
	s.nextID = 0
	first, later := &client{}, &client{}
	s.actFor(first, proto.ActForParams{ID: "laptop/p4@100", Agent: "claude"})
	s.actFor(later, proto.ActForParams{ID: "laptop/p4@200", Agent: "claude"})
	res, perr := s.dispatch(first, proto.Message{Method: proto.MethodPaneCreate,
		Params: proto.Marshal(proto.PaneCreateParams{Command: []string{"/bin/sleep", "30"}, Cwd: work})})
	if perr != nil {
		t.Fatal(perr)
	}
	id := res.(proto.PaneInfo).ID
	closeFrom := func(c *client) *proto.Error {
		return s.inScope(c, proto.Message{Method: proto.MethodPaneClose, Params: proto.Marshal(proto.PaneRef{ID: id})})
	}
	if perr := closeFrom(first); perr != nil {
		t.Fatalf("its own: %v", perr)
	}
	if perr := closeFrom(later); perr == nil {
		t.Fatal("a later pane with the same ID reached it")
	}
}

// Every way a pane starts, from a connection acting for an agent on
// another machine, names that agent.
func TestCreatorEveryWayFromAfar(t *testing.T) {
	s, _, work := shareFixture(t)
	fakeLoginShell(t)
	proj, err := s.projects.add(work, false)
	if err != nil {
		t.Fatal(err)
	}
	far := &client{}
	s.actFor(far, proto.ActForParams{ID: "laptop/p4@1", Agent: "claude"})
	for name, m := range map[string]proto.Message{
		"pane":    {Method: proto.MethodPaneCreate, Params: proto.Marshal(proto.PaneCreateParams{Command: []string{"/bin/sleep", "30"}, Cwd: work})},
		"task":    {Method: proto.MethodTaskCreate, Params: proto.Marshal(proto.TaskCreateParams{ProjectID: proj.id, Prompt: "go", Branch: "from-afar"})},
		"install": {Method: proto.MethodAgentInstall, Params: proto.Marshal(proto.AgentInstallParams{Agent: "codex"})},
		"resume":  {Method: proto.MethodSessionResume, Params: proto.Marshal(proto.SessionRef{Agent: "claude", ID: "c1", Dir: work})},
		"share":   {Method: proto.MethodSessionShare, Params: proto.Marshal(proto.SessionShareParams{Agent: "claude", ID: "c1", Dir: work, To: "codex"})},
	} {
		res, perr := s.dispatch(far, m)
		if perr != nil {
			t.Fatalf("%s: %v", name, perr)
		}
		info, ok := res.(proto.PaneInfo)
		if sr, isShare := res.(proto.SessionShareResult); isShare {
			info, ok = sr.Pane, true
		}
		if !ok || info.CreatedBy != "laptop/p4@1" {
			t.Errorf("%s: created by %q", name, info.CreatedBy)
		}
	}
}
