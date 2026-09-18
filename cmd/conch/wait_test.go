package main

import (
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
)

// a4Pane is a pane running an agent in the given state; "" means no agent
// has been detected in it yet.
func a4Pane(id, state string) proto.PaneInfo {
	p := proto.PaneInfo{ID: id, Name: "claude", State: "running"}
	if state != "" {
		p.Agent = &proto.AgentStatus{Name: "claude", State: state}
	}
	return p
}

// a4WaitServer answers pane.list with panes, then writes events on the same
// connection. The events go out without waiting: the client queues them, so
// they arrive whether or not the wait loop is reading yet.
func a4WaitServer(t *testing.T, panes []proto.PaneInfo, events ...proto.Message) *a4Server {
	t.Helper()
	srv := startA4Server(t, config.SocketPath())
	srv.setHandle(func(msg proto.Message, conn *proto.Conn) (any, *proto.Error) {
		if msg.Method != proto.MethodPaneList {
			return nil, nil
		}
		if len(events) > 0 {
			go func() {
				for _, e := range events {
					conn.Write(e)
				}
			}()
		}
		return proto.PaneList{Panes: panes}, nil
	})
	return srv
}

func a4Updated(p proto.PaneInfo) proto.Message {
	return proto.Message{Event: proto.EventPaneUpdated, Data: proto.Marshal(p)}
}

func TestA4WaitAlreadyInState(t *testing.T) {
	a4Env(t)
	a4WaitServer(t, []proto.PaneInfo{a4Pane("p1", "working"), a4Pane("p2", "done")})

	var err error
	out, _ := a4Capture(t, "", func() { err = runWait([]string{"p2"}) })
	if err != nil || out != "p2 claude done\n" {
		t.Fatalf("done already: %q %v", out, err)
	}
	// An agent that is merely idle is not done.
	out, _ = a4Capture(t, "", func() { err = runWait([]string{"-state", "idle", "-timeout", "150ms", "p2"}) })
	if _, ok := err.(waitTimeout); !ok || out != "" {
		t.Fatalf("idle: %q %v", out, err)
	}
}

func TestA4WaitForEvent(t *testing.T) {
	a4Env(t)
	a4WaitServer(t, []proto.PaneInfo{a4Pane("p1", "working")},
		a4Updated(a4Pane("p9", "done")),    // another pane finishing is ignored
		a4Updated(a4Pane("p1", "working")), // same state again, keep waiting
		proto.Message{Event: proto.EventPaneFrame, Data: proto.Marshal(a4Pane("p1", "done"))}, // not a state change
		a4Updated(a4Pane("p1", "done")))

	var err error
	out, _ := a4Capture(t, "", func() { err = runWait([]string{"p1"}) })
	if err != nil || out != "p1 claude done\n" {
		t.Fatalf("wait: %q %v", out, err)
	}
}

func TestA4WaitStateNames(t *testing.T) {
	a4Env(t)
	// "waiting" is the word the TUI uses for a blocked agent.
	a4WaitServer(t, []proto.PaneInfo{a4Pane("p1", "working")}, a4Updated(a4Pane("p1", "blocked")))
	var err error
	out, _ := a4Capture(t, "", func() { err = runWait([]string{"-state", "waiting", "p1"}) })
	if err != nil || out != "p1 claude blocked\n" {
		t.Fatalf("waiting: %q %v", out, err)
	}

	// Several states stop on whichever comes first, and blank entries are
	// ignored.
	for _, spec := range []string{"done,waiting", " done , , waiting ", "blocked,done"} {
		want, err := wantedStates(spec)
		if err != nil || !want["done"] || !want["blocked"] || len(want) != 2 {
			t.Fatalf("%q: %v %v", spec, want, err)
		}
	}
	if _, err := wantedStates("finished"); err == nil || !strings.Contains(err.Error(), `unknown state "finished"; use blocked, done, idle, waiting, working`) {
		t.Fatalf("unknown state: %v", err)
	}
	if _, err := wantedStates(" , "); err == nil || !strings.Contains(err.Error(), "at least one state") {
		t.Fatalf("empty: %v", err)
	}
}

func TestA4WaitNoAgentYet(t *testing.T) {
	a4Env(t)
	a4WaitServer(t, []proto.PaneInfo{a4Pane("p1", "")}, a4Updated(a4Pane("p1", "done")))

	var err error
	out, errOut := a4Capture(t, "", func() { err = runWait([]string{"p1"}) })
	if err != nil || out != "p1 claude done\n" {
		t.Fatalf("no agent yet: %q %v", out, err)
	}
	if !strings.Contains(errOut, "waiting for an agent to start in p1") {
		t.Fatalf("note: %q", errOut)
	}
}

func TestA4WaitPaneEnds(t *testing.T) {
	for _, event := range []string{proto.EventPaneExited, proto.EventPaneClosed} {
		t.Run(event, func(t *testing.T) {
			a4Env(t)
			p := a4Pane("p1", "working")
			p.ExitCode = 3
			a4WaitServer(t, []proto.PaneInfo{a4Pane("p1", "working")}, proto.Message{Event: event, Data: proto.Marshal(p)})
			err := runWait([]string{"p1"})
			if err == nil || !strings.Contains(err.Error(), "pane p1 ended (exit 3) before it was done") {
				t.Fatalf("%v", err)
			}
		})
	}
}

// A pane can end before the wait starts; then no event is coming and only
// the pane's own state says so.
func TestA4WaitPaneAlreadyEnded(t *testing.T) {
	a4Env(t)
	gone := a4Pane("p1", "working")
	gone.State, gone.ExitCode = proto.PaneExited, 2
	done := a4Pane("p2", "done")
	done.State, done.ExitCode = proto.PaneExited, 0
	a4WaitServer(t, []proto.PaneInfo{gone, done})

	err := runWait([]string{"-timeout", "5s", "p1"})
	if err == nil || !strings.Contains(err.Error(), "pane p1 ended (exit 2) before it was done") {
		t.Fatalf("ended: %v", err)
	}
	// An agent that finished before the pane exited still counts.
	var out string
	out, _ = a4Capture(t, "", func() { err = runWait([]string{"p2"}) })
	if err != nil || out != "p2 claude done\n" {
		t.Fatalf("done then exited: %q %v", out, err)
	}
}

func TestA4WaitServerHangsUp(t *testing.T) {
	a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	srv.setHandle(func(msg proto.Message, conn *proto.Conn) (any, *proto.Error) {
		if msg.Method == proto.MethodPaneList {
			go conn.Close() // the server goes away mid-wait
			return proto.PaneList{Panes: []proto.PaneInfo{a4Pane("p1", "working")}}, nil
		}
		return nil, nil
	})
	if err := runWait([]string{"p1"}); err == nil {
		t.Fatal("a closed connection should end the wait")
	}
}

func TestA4WaitArgumentsAndMissingPane(t *testing.T) {
	a4Env(t)
	a4WaitServer(t, []proto.PaneInfo{a4Pane("p1", "working")})

	if err := runWait(nil); err == nil || !strings.Contains(err.Error(), "usage: conch wait") {
		t.Fatalf("no pane id: %v", err)
	}
	if err := runWait([]string{"p1", "p2"}); err == nil || !strings.Contains(err.Error(), "usage: conch wait") {
		t.Fatalf("two ids: %v", err)
	}
	if err := runWait([]string{"-state", "gone", "p1"}); err == nil || !strings.Contains(err.Error(), "unknown state") {
		t.Fatalf("bad state: %v", err)
	}
	if err := runWait([]string{"nope"}); err == nil || !strings.Contains(err.Error(), `no pane "nope"`) {
		t.Fatalf("missing pane: %v", err)
	}
}

func TestA4WaitNoServer(t *testing.T) {
	a4Env(t) // nothing listens on the socket
	if err := runWait([]string{"p1"}); err == nil || !strings.Contains(err.Error(), "server is not running") {
		t.Fatalf("no server: %v", err)
	}
}

// The exit code matters most: a script chaining tasks tells a timeout from a
// real failure by it.
func TestA4WaitTimeoutExits124(t *testing.T) {
	a4Env(t)
	a4WaitServer(t, []proto.PaneInfo{a4Pane("p1", "working")})

	start := time.Now()
	code, out, errOut := a4RunMain(t, "", "wait", "-timeout", "200ms", "p1")
	if code != 124 || out != "" || !strings.Contains(errOut, "timed out after 200ms") {
		t.Fatalf("timeout: code %d %q %q", code, out, errOut)
	}
	if took := time.Since(start); took < 200*time.Millisecond {
		t.Fatalf("returned after %s, before the timeout", took)
	}
	// Without -timeout it would wait forever, so only the reached-state path
	// is exercised here.
	code, out, _ = a4RunMain(t, "", "wait", "-state", "working", "p1")
	if code != 0 || out != "p1 claude working\n" {
		t.Fatalf("working: code %d %q", code, out)
	}
}
