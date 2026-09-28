package server

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
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
		if kind == scopeServer {
			if in == nil {
				t.Errorf("%s: the server is never an agent's", method)
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
