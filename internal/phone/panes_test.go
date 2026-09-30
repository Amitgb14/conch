package phone

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/proto"
)

// Every pane, terminals too: listed, started, renamed and closed from the
// phone, and followed over the socket as it happens.
func TestPanesListStartRenameClose(t *testing.T) {
	f := newFixture(t)
	// An agent started from the phone runs the login shell's command: a
	// fake shell that stays up and launches nothing.
	sh := filepath.Join(f.dir, "fake-shell")
	os.WriteFile(sh, []byte("#!/bin/sh\nexec sleep 30\n"), 0o755)
	repo := filepath.Join(f.dir, "api")
	os.MkdirAll(repo, 0o755)
	var proj proto.ProjectInfo
	f.call(proto.MethodProjectAdd, proto.ProjectAddParams{Path: repo}, &proj)

	view, full := f.pair(PermView), f.pair(PermFull)
	agent := f.pane("claude", "stty -echo; exec cat")
	term := f.pane("", "stty -echo; exec cat")
	s := view.socket()
	s.send(ClientMessage{Type: MsgPanesWatch, ID: "w"})
	first := s.next("the panes", func(m ServerMessage) bool { return m.Type == MsgPanes })
	if first.ID != "w" || first.Panes == nil || len(*first.Panes) != 2 {
		t.Fatalf("panes %+v", first)
	}
	ps := *first.Panes
	if ps[0].Pane != agent || ps[0].Kind != KindAgent || ps[0].Agent.Agent != "claude" ||
		ps[1].Pane != term || ps[1].Kind != KindTerminal || ps[1].Agent.Agent != "" || ps[1].State != StateIdle || ps[1].Cwd != f.dir || ps[1].Since.IsZero() {
		t.Fatalf("panes %+v", ps)
	}
	var list PaneList
	if status := view.get("/api/panes", &list); status != 200 || len(list.Panes) != 2 || list.Panes[1].Pane != term {
		t.Fatalf("GET /api/panes: %d %+v", status, list)
	}

	// Refused before anything starts.
	for _, c := range []struct {
		req    NewPaneRequest
		status int
	}{
		{NewPaneRequest{}, 400},
		{NewPaneRequest{Kind: "shell"}, 400},
		{NewPaneRequest{Kind: KindAgent}, 400},
		{NewPaneRequest{Kind: KindTerminal, Agent: "claude"}, 400},
		{NewPaneRequest{Kind: KindTerminal, Prompt: "hi"}, 400},
		{NewPaneRequest{Kind: KindTerminal, Project: "rnope"}, 404},
		{NewPaneRequest{Kind: KindTerminal, Name: "p5"}, 400}, // a name that reads as a pane ID
		{NewPaneRequest{Kind: KindAgent, Agent: "nonesuch"}, 400},
	} {
		if status, e := full.post("/api/panes", c.req, nil); status != c.status {
			t.Errorf("%+v: %d %+v, want %d", c.req, status, e, c.status)
		}
	}
	if status, e := view.post("/api/panes", NewPaneRequest{Kind: KindTerminal}, nil); status != 403 || e.Code != CodeForbidden {
		t.Fatalf("view starting a terminal: %d %+v", status, e)
	}

	// A terminal in the project's folder: announced to the socket.
	var res NewPaneResponse
	if status, e := full.post("/api/panes", NewPaneRequest{Kind: KindTerminal, Project: proj.ID, Name: "build"}, &res); status != 200 {
		t.Fatalf("new terminal: %d %+v", status, e)
	}
	got := s.next("the new terminal", func(m ServerMessage) bool { return m.Type == MsgPaneChanged && m.Info.Pane == res.Pane })
	if got.Info.Kind != KindTerminal || got.Info.Name != "build" || got.Info.Cwd != repo || got.Info.Project == nil || got.Info.Project.ID != proj.ID {
		t.Fatalf("new terminal %+v", got.Info)
	}
	// One in no project starts in the home folder.
	var home NewPaneResponse
	full.post("/api/panes", NewPaneRequest{Kind: KindTerminal}, &home)
	hp := s.next("the home terminal", func(m ServerMessage) bool { return m.Type == MsgPaneChanged && m.Info.Pane == home.Pane })
	if h, _ := os.UserHomeDir(); hp.Info.Cwd != h || hp.Info.Project != nil {
		t.Fatalf("home terminal %+v", hp.Info)
	}
	// An agent, with its first message, as conch launches it.
	t.Setenv("SHELL", sh)
	var ag NewPaneResponse
	if status, e := full.post("/api/panes", NewPaneRequest{Kind: KindAgent, Agent: "claude", Project: proj.ID, Prompt: "review it"}, &ag); status != 200 {
		t.Fatalf("new agent: %d %+v", status, e)
	}
	var panes proto.PaneList
	f.call(proto.MethodPaneList, nil, &panes)
	for _, p := range panes.Panes {
		if p.ID == ag.Pane && (!strings.Contains(strings.Join(p.Command, " "), "claude") || !strings.Contains(strings.Join(p.Command, " "), "review it") || p.Cwd != repo) {
			t.Fatalf("agent pane %+v", p)
		}
	}

	// Rename and close: full only.
	if status, _ := view.post("/api/rename", RenameRequest{Pane: res.Pane, Name: "x"}, nil); status != 403 {
		t.Fatalf("view renaming: %d", status)
	}
	var rn RenameRequest
	if status, e := full.post("/api/rename", RenameRequest{Pane: res.Pane, Name: "  tests  "}, &rn); status != 200 || rn != (RenameRequest{Pane: res.Pane, Name: "tests"}) {
		t.Fatalf("rename: %d %+v %+v", status, e, rn)
	}
	s.next("the rename", func(m ServerMessage) bool {
		return m.Type == MsgPaneChanged && m.Info.Pane == res.Pane && m.Info.Name == "tests"
	})
	for _, bad := range []RenameRequest{{Name: "x"}, {Pane: "tests", Name: "x"}, {Pane: "p999", Name: "x"}, {Pane: res.Pane, Name: "p1"}} {
		if status, _ := full.post("/api/rename", bad, nil); status < 400 {
			t.Errorf("rename %+v: %d", bad, status)
		}
	}
	if status, _ := view.post("/api/close", CloseRequest{Pane: res.Pane}, nil); status != 403 {
		t.Fatalf("view closing: %d", status)
	}
	var cl CloseResponse
	if status, e := full.post("/api/close", CloseRequest{Pane: res.Pane}, &cl); status != 200 || cl != (CloseResponse{Pane: res.Pane, Closed: true}) {
		t.Fatalf("close: %d %+v %+v", status, e, cl)
	}
	s.next("the pane gone", func(m ServerMessage) bool { return m.Type == MsgPaneGone && m.Pane == res.Pane })
	for _, bad := range []CloseRequest{{}, {Pane: "build"}, {Pane: "p999"}} {
		if status, _ := full.post("/api/close", bad, nil); status < 400 {
			t.Errorf("close %+v: %d", bad, status)
		}
	}
}

// What a phone's keyboard and paste produce, typed as it is: accents,
// emoji, and several lines at once.
func TestSocketText(t *testing.T) {
	f := newFixture(t)
	pane := f.pane("", "stty -echo; printf 'ready>\\n'; exec cat")
	waitFor(t, "the pane's prompt", func() bool { return strings.Contains(f.screen(pane), "ready>") })
	s := f.pair(PermFull).socket()
	s.send(ClientMessage{Type: MsgText, ID: "t1", Pane: pane, Text: "héllo wörld 😀\r"})
	waitFor(t, "the typed line", func() bool { return strings.Contains(f.screen(pane), "héllo wörld 😀") })
	s.send(ClientMessage{Type: MsgText, ID: "t2", Pane: pane, Text: "line one\nline two\n"})
	waitFor(t, "the pasted lines", func() bool {
		sc := f.screen(pane)
		return strings.Contains(sc, "line one") && strings.Contains(sc, "line two")
	})
	for _, c := range []ClientMessage{
		{Type: MsgText, ID: "e1", Pane: pane},
		{Type: MsgText, ID: "e2", Pane: "zsh", Text: "x"},
		{Type: MsgText, ID: "e3", Pane: "p999", Text: "x"},
	} {
		s.send(c)
		got := s.next("the refusal of "+c.ID, func(m ServerMessage) bool { return m.Type == MsgError && m.ID == c.ID })
		if got.Error.Code != CodeBadRequest && got.Error.Code != CodeNotFound {
			t.Errorf("%s: %+v", c.ID, got.Error)
		}
	}
}
