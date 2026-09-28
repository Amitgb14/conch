package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
)

func a4Named(id, name, state string) proto.PaneInfo {
	return proto.PaneInfo{ID: id, Name: name, State: state}
}

func TestPaneNamed(t *testing.T) {
	panes := []proto.PaneInfo{
		a4Named("p1", "reviewer", proto.PaneRunning),
		a4Named("p2", "claude", proto.PaneRunning),
		a4Named("p3", "claude", proto.PaneRunning),
		a4Named("p4", "old", proto.PaneExited),
		a4Named("p5", "reborn", proto.PaneExited),
		a4Named("p6", "reborn", proto.PaneRunning),
		a4Named("p7", "gone", proto.PaneExited),
		a4Named("p8", "gone", proto.PaneExited),
	}
	for _, c := range []struct{ name, id, err string }{
		{"reviewer", "p1", ""},
		{"old", "p4", ""},    // ended, and nothing else has the name
		{"reborn", "p6", ""}, // the running one, not the one that ended
		{"claude", "", `2 panes are named "claude" (p2, p3); give an ID`},
		{"gone", "", `2 panes are named "gone" (p7, p8)`},
		{"Reviewer", "", `no pane "Reviewer"`}, // names are exact
		{"review", "", `no pane "review"`},
		{"nobody", "", `no pane "nobody"`},
	} {
		id, err := paneNamed(panes, c.name)
		if id != c.id || (c.err == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), c.err)) {
			t.Errorf("%q: %q %v, want %q %q", c.name, id, err, c.id, c.err)
		}
	}
	if _, err := paneNamed(nil, "x"); err == nil {
		t.Error("no panes at all")
	}
}

func TestIsPaneIDShapes(t *testing.T) {
	for ref, want := range map[string]bool{"p1": true, "p42": true, "p": false, "p1a": false, "P1": false, "1": false, "": false, "pp1": false, "reviewer": false, "p-1": false} {
		if proto.IsPaneID(ref) != want {
			t.Errorf("IsPaneID(%q) = %v", ref, !want)
		}
	}
}

// a4NamesServer lists panes and answers every other call with a pane or
// a screen, recording what it was asked.
func a4NamesServer(t *testing.T, panes ...proto.PaneInfo) *a4Server {
	t.Helper()
	srv := startA4Server(t, config.SocketPath())
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		switch msg.Method {
		case proto.MethodPaneList:
			return proto.PaneList{Panes: panes}, nil
		case proto.MethodPaneRead:
			return proto.PaneReadResult{Lines: []string{"screen"}}, nil
		case proto.MethodAgentPrompt:
			return proto.AgentPromptResult{ID: "p1", Agent: "claude", Turn: 1}, nil
		case proto.MethodPaneRename:
			var rp proto.PaneRenameParams
			_ = json.Unmarshal(msg.Params, &rp)
			name := rp.Name
			if name == "" {
				name = "claude"
			}
			return proto.PaneInfo{ID: rp.ID, Name: name}, nil
		}
		return nil, nil
	})
	return srv
}

// Every command that takes a pane takes its name, and reaches the pane by
// its ID.
func TestA4CommandsTakeNames(t *testing.T) {
	done := a4Pane("p1", "done")
	done.Name = "reviewer"
	panes := []proto.PaneInfo{done, a4Named("p2", "claude", proto.PaneRunning), a4Named("p3", "claude", proto.PaneRunning)}

	for _, c := range []struct {
		args   []string
		method string
	}{
		{[]string{"send", "reviewer", "hi"}, proto.MethodPaneSendText},
		{[]string{"send", "-keys", "reviewer", "enter"}, proto.MethodPaneSendKeys},
		{[]string{"read", "reviewer"}, proto.MethodPaneRead},
		{[]string{"close", "reviewer"}, proto.MethodPaneClose},
		{[]string{"redraw", "reviewer"}, proto.MethodPaneRedraw},
		{[]string{"agent", "explain", "reviewer"}, proto.MethodAgentExplain},
		{[]string{"agent", "prompt", "reviewer", "go"}, proto.MethodAgentPrompt},
	} {
		t.Run(strings.Join(c.args, " "), func(t *testing.T) {
			a4Env(t)
			srv := a4NamesServer(t, panes...)
			if code, _, errOut := a4RunMain(t, "", c.args...); code != 0 {
				t.Fatalf("code %d: %s", code, errOut)
			}
			var ref struct{ ID string }
			if !srv.params(t, c.method, &ref) || ref.ID != "p1" {
				t.Fatalf("%s reached %q", c.method, ref.ID)
			}
		})
	}

	// wait answers from the list it reads, by ID.
	a4Env(t)
	a4NamesServer(t, panes...)
	var err error
	out, _ := a4Capture(t, "", func() { err = runWait([]string{"reviewer"}) })
	if err != nil || out != "p1 claude done\n" {
		t.Fatalf("wait: %q %v", out, err)
	}
}

func TestA4CommandsRefuseUnclearNames(t *testing.T) {
	panes := []proto.PaneInfo{a4Named("p2", "claude", proto.PaneRunning), a4Named("p3", "claude", proto.PaneRunning)}
	for _, args := range [][]string{
		{"send", "claude", "hi"}, {"read", "claude"}, {"close", "claude"}, {"redraw", "claude"},
		{"wait", "claude"}, {"agent", "explain", "claude"}, {"agent", "prompt", "claude", "go"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			a4Env(t)
			srv := a4NamesServer(t, panes...)
			code, _, errOut := a4RunMain(t, "", args...)
			if code != 1 || !strings.Contains(errOut, `2 panes are named "claude" (p2, p3); give an ID`) {
				t.Fatalf("ambiguous: code %d %q", code, errOut)
			}
			for _, m := range srv.methods() {
				if m != proto.MethodPaneList {
					t.Fatalf("still called %s", m)
				}
			}
		})
	}
	a4Env(t)
	a4NamesServer(t, panes...)
	if code, _, errOut := a4RunMain(t, "", "read", "nobody"); code != 1 || !strings.Contains(errOut, `no pane "nobody"`) {
		t.Fatalf("missing: %d %q", code, errOut)
	}
}

// An ID means what it always did: no list is read to find it, so a pane
// named like nothing else is never consulted.
func TestA4IDsGoUnlisted(t *testing.T) {
	a4Env(t)
	srv := a4NamesServer(t, a4Named("p9", "p3", proto.PaneRunning)) // a name an old TUI may have set
	if code, _, errOut := a4RunMain(t, "", "send", "p3", "hi"); code != 0 {
		t.Fatalf("send: %s", errOut)
	}
	var ref proto.PaneRef
	srv.params(t, proto.MethodPaneSendText, &ref)
	if ref.ID != "p3" || strings.Join(srv.methods(), ",") != proto.MethodPaneSendText {
		t.Fatalf("calls %v, to %q", srv.methods(), ref.ID)
	}
}

func TestA4Rename(t *testing.T) {
	a4Env(t)
	srv := a4NamesServer(t, a4Named("p1", "claude", proto.PaneRunning), a4Named("p2", "reviewer", proto.PaneRunning),
		a4Named("p3", "coder", proto.PaneExited))
	var err error
	out, _ := a4Capture(t, "", func() { err = runRename([]string{"p1", " writer "}) })
	var rp proto.PaneRenameParams
	srv.params(t, proto.MethodPaneRename, &rp)
	if err != nil || out != "p1 writer\n" || rp != (proto.PaneRenameParams{ID: "p1", Name: "writer"}) {
		t.Fatalf("rename: %q %v %+v", out, err, rp)
	}
	// By name; and with no new name the pane gets its own back.
	out, _ = a4Capture(t, "", func() { err = runRename([]string{"reviewer"}) })
	srv.params(t, proto.MethodPaneRename, &rp)
	if err != nil || out != "p2 claude\n" || rp != (proto.PaneRenameParams{ID: "p2"}) {
		t.Fatalf("restore: %q %v %+v", out, err, rp)
	}
	// A pane may keep its own name; an ended pane's name is free.
	for _, args := range [][]string{{"reviewer", "reviewer"}, {"p1", "coder"}} {
		if err := runRename(args); err != nil {
			t.Errorf("%q: %v", args, err)
		}
	}

	calls := len(srv.methods())
	for _, c := range []struct {
		args []string
		want string
	}{
		{nil, "usage: conch rename"},
		{[]string{"p1", "a", "b"}, "usage: conch rename"},
		{[]string{"p1", "  "}, "the new name is empty"},
		{[]string{"p1", "p7"}, `"p7" is shaped like a pane ID`},
	} {
		if err := runRename(c.args); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: %v, want %q", c.args, err, c.want)
		}
	}
	if len(srv.methods()) != calls {
		t.Fatal("a refused rename reached the server")
	}
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"p1", "reviewer"}, `pane p2 is already named "reviewer"`},
		{[]string{"nobody", "x"}, `no pane "nobody"`},
	} {
		if err := runRename(c.args); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: %v, want %q", c.args, err, c.want)
		}
	}
	for _, m := range srv.methods()[calls:] {
		if m == proto.MethodPaneRename {
			t.Fatal("renamed anyway")
		}
	}
}

func TestA4TaskName(t *testing.T) {
	for _, old := range []bool{false, true} {
		t.Run(map[bool]string{false: "server takes it", true: "older server"}[old], func(t *testing.T) {
			a4Env(t)
			srv := startA4Server(t, config.SocketPath())
			if old {
				srv.setHello(func(n int) proto.HelloResult {
					h := currentHello(n)
					h.Capabilities = []string{"pane.v1", "project.v1"}
					return h
				})
			}
			srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
				switch msg.Method {
				case proto.MethodProjectAdd:
					return proto.ProjectInfo{ID: "proj1", Name: "api"}, nil
				case proto.MethodTaskCreate:
					return proto.PaneInfo{ID: "p5", Name: "claude", Cwd: "/src/wt", Branch: "review"}, nil
				case proto.MethodPaneRename:
					return proto.PaneInfo{ID: "p5", Name: "reviewer", Cwd: "/src/wt", Branch: "review"}, nil
				}
				return nil, nil
			})
			var err error
			out, _ := a4Capture(t, "", func() { err = runTask([]string{"-cwd", "/src", "-name", " reviewer ", "review", "it"}) })
			if err != nil || out != "p5  /src/wt  review\n" {
				t.Fatalf("task: %q %v", out, err)
			}
			var tp proto.TaskCreateParams
			srv.params(t, proto.MethodTaskCreate, &tp)
			var rp proto.PaneRenameParams
			renamed := srv.params(t, proto.MethodPaneRename, &rp)
			switch {
			case !old && (tp.Name != "reviewer" || renamed):
				t.Fatalf("named by the server: %+v renamed=%v", tp, renamed)
			case old && (tp.Name != "" || rp != (proto.PaneRenameParams{ID: "p5", Name: "reviewer"})):
				t.Fatalf("renamed after: %+v %+v", tp, rp)
			}
		})
	}

	a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		if msg.Method == proto.MethodProjectAdd {
			return proto.ProjectInfo{ID: "proj1", Name: "api"}, nil
		}
		return nil, nil
	})
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"-cwd", "/src", "-name", "p3", "go"}, `-name "p3" is shaped like a pane ID`},
		{[]string{"-cwd", "/src", "-name", "r", "-n", "2", "go"}, "-name names one pane, and this starts 2"},
		{[]string{"-cwd", "/src", "-name", "r", "-agent", "claude,codex", "go"}, "-name names one pane, and this starts 2"},
	} {
		if err := runTask(c.args); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: %v, want %q", c.args, err, c.want)
		}
	}
	for _, m := range srv.methods() {
		if m == proto.MethodTaskCreate {
			t.Fatal("a refused name still started a task")
		}
	}
}

// Names against a real server: a pane renamed is reached by its name, the
// name survives nothing but that pane, and giving it back ends the name.
func TestA4NamesRealServer(t *testing.T) {
	dir := a4Env(t)
	c := startA4RealServer(t, dir)
	var info proto.PaneInfo
	if err := call(c, proto.MethodPaneCreate, proto.PaneCreateParams{Agent: "claude",
		Command: []string{"/bin/sh", "-c", "stty -echo; exec cat"}, Cwd: dir, Cols: 80, Rows: 10}, &info); err != nil {
		t.Fatal(err)
	}
	var err error
	out, _ := a4Capture(t, "", func() { err = runRename([]string{info.ID, "reviewer"}) })
	if err != nil || out != info.ID+" reviewer\n" {
		t.Fatalf("rename: %q %v", out, err)
	}
	for i := 0; ; i++ { // the agent is detected on the server's next look
		var list proto.PaneList
		call(c, proto.MethodPaneList, nil, &list)
		if p, _ := paneByID(list.Panes, info.ID); p.Agent != nil {
			break
		}
		if i == 250 {
			t.Fatal("no agent detected")
		}
		time.Sleep(20 * time.Millisecond)
	}
	out, _ = a4Capture(t, "", func() { err = runAgent([]string{"prompt", "reviewer", "named-message"}) })
	if err != nil || out != info.ID+" claude sent\n" {
		t.Fatalf("prompt by name: %q %v", out, err)
	}
	for i := 0; ; i++ {
		out, _ = a4Capture(t, "", func() { err = runRead([]string{"reviewer"}) })
		if err == nil && strings.Contains(out, "named-message") {
			break
		}
		if i == 250 {
			t.Fatalf("read by name: %q %v", out, err)
		}
		time.Sleep(20 * time.Millisecond)
	}

	a4Capture(t, "", func() { err = runRename([]string{"reviewer"}) })
	if err != nil {
		t.Fatal(err)
	}
	if err := runRead([]string{"reviewer"}); err == nil || !strings.Contains(err.Error(), `no pane "reviewer"`) {
		t.Fatalf("after the name was given back: %v", err)
	}
	if err := runClose([]string{info.ID}); err != nil {
		t.Fatal(err)
	}
}
