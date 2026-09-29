package main

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/server"
)

// a4Turn is a pane whose agent is in state at turn.
func a4Turn(id, state string, turn int) proto.PaneInfo {
	p := a4Pane(id, state)
	p.Agent.Turn = turn
	return p
}

// a4PromptServer answers agent.prompt with res (or perr), then pushes
// events on the same connection; it records the params it was sent.
func a4PromptServer(t *testing.T, res proto.AgentPromptResult, perr *proto.Error, events ...proto.Message) *proto.AgentPromptParams {
	t.Helper()
	srv := startA4Server(t, config.SocketPath())
	got := &proto.AgentPromptParams{}
	srv.setHandle(func(msg proto.Message, conn *proto.Conn) (any, *proto.Error) {
		if msg.Method != proto.MethodAgentPrompt {
			return nil, nil
		}
		json.Unmarshal(msg.Params, got)
		if perr != nil {
			return nil, perr
		}
		go func() {
			for _, e := range events {
				conn.Write(e)
			}
		}()
		return res, nil
	})
	return got
}

func TestA4AgentPromptSends(t *testing.T) {
	a4Env(t)
	got := a4PromptServer(t, proto.AgentPromptResult{ID: "p1", Agent: "claude", Turn: 1}, nil)
	var err error
	out, _ := a4Capture(t, "", func() { err = runAgent([]string{"prompt", "p1", "review", "the", "diff"}) })
	if err != nil || out != "p1 claude sent\n" {
		t.Fatalf("sent: %q %v", out, err)
	}
	if got.ID != "p1" || got.Text != "review the diff" {
		t.Fatalf("params %+v", got)
	}
}

func TestA4AgentPromptBlockedExits3(t *testing.T) {
	a4Env(t)
	a4PromptServer(t, proto.AgentPromptResult{}, proto.Errorf(proto.ErrAgentBlocked,
		"claude in p1 is waiting for an answer: Allow Bash?; nothing was typed"))
	code, out, errOut := a4RunMain(t, "", "agent", "prompt", "-wait", "p1", "go on")
	if code != 3 || out != "" || !strings.Contains(errOut, "waiting for an answer: Allow Bash?; nothing was typed; read it with `conch read p1`") {
		t.Fatalf("blocked: code %d %q %q", code, out, errOut)
	}
}

// What the turn is for: states from before the message — the done that
// became idle when it was typed, another pane's — don't answer it.
func TestA4AgentPromptWaitsForItsTurn(t *testing.T) {
	a4Env(t)
	a4PromptServer(t, proto.AgentPromptResult{ID: "p1", Agent: "claude", Turn: 2}, nil,
		a4Updated(a4Turn("p1", "idle", 1)),
		a4Updated(a4Turn("p9", "done", 2)),
		proto.Message{Event: proto.EventPaneFrame, Data: proto.Marshal(a4Turn("p1", "done", 2))},
		a4Updated(a4Turn("p1", "working", 2)),
		a4Updated(a4Turn("p1", "done", 2)))
	var err error
	out, _ := a4Capture(t, "", func() { err = runAgent([]string{"prompt", "-wait", "p1", "and the docs"}) })
	if err != nil || out != "p1 claude done\n" {
		t.Fatalf("wait: %q %v", out, err)
	}
}

// A question asked before the turn is seen to start still ends the wait:
// an agent read only from its screen can go from idle to asking directly.
func TestA4AgentPromptWaitEndsOnAQuestion(t *testing.T) {
	a4Env(t)
	a4PromptServer(t, proto.AgentPromptResult{ID: "p1", Agent: "claude", Turn: 2}, nil,
		a4Updated(a4Turn("p1", "blocked", 1)))
	var err error
	out, _ := a4Capture(t, "", func() { err = runAgent([]string{"prompt", "-wait", "p1", "go"}) })
	if err != nil || out != "p1 claude blocked\n" {
		t.Fatalf("question: %q %v", out, err)
	}

	// Unless the question is not among the states asked for.
	a4Env(t)
	a4PromptServer(t, proto.AgentPromptResult{ID: "p1", Agent: "claude", Turn: 2}, nil,
		a4Updated(a4Turn("p1", "blocked", 2)), a4Updated(a4Turn("p1", "done", 2)))
	out, _ = a4Capture(t, "", func() { err = runAgent([]string{"prompt", "-wait", "-until", "done", "p1", "go"}) })
	if err != nil || out != "p1 claude done\n" {
		t.Fatalf("until done: %q %v", out, err)
	}
}

func TestA4AgentPromptAgentGone(t *testing.T) {
	codex := a4Turn("p1", "done", 2)
	codex.Agent.Name = "codex"
	shell := a4Pane("p1", "")
	exited := a4Turn("p1", "working", 2)
	exited.ExitCode = 1
	for name, c := range map[string]struct {
		event proto.Message
		want  string
	}{
		"no agent":      {a4Updated(shell), "the claude agent in p1 is gone before it was done,idle,waiting"},
		"another agent": {a4Updated(codex), "the claude agent in p1 is gone"},
		"pane exited":   {proto.Message{Event: proto.EventPaneExited, Data: proto.Marshal(exited)}, "pane p1 ended (exit 1)"},
		"pane closed":   {proto.Message{Event: proto.EventPaneClosed, Data: proto.Marshal(exited)}, "pane p1 ended (exit 1)"},
	} {
		t.Run(name, func(t *testing.T) {
			a4Env(t)
			a4PromptServer(t, proto.AgentPromptResult{ID: "p1", Agent: "claude", Turn: 2}, nil, c.event)
			err := runAgent([]string{"prompt", "-wait", "p1", "go"})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("%v, want %q", err, c.want)
			}
		})
	}
}

func TestA4AgentPromptTimeoutExits124(t *testing.T) {
	a4Env(t)
	a4PromptServer(t, proto.AgentPromptResult{ID: "p1", Agent: "claude", Turn: 2}, nil,
		a4Updated(a4Turn("p1", "working", 2)))
	code, out, errOut := a4RunMain(t, "", "agent", "prompt", "-wait", "-timeout", "200ms", "p1", "go")
	if code != 124 || out != "" || !strings.Contains(errOut, "timed out after 200ms") {
		t.Fatalf("timeout: code %d %q %q", code, out, errOut)
	}
}

func TestA4AgentPromptOldServer(t *testing.T) {
	a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	srv.setHello(func(n int) proto.HelloResult {
		h := currentHello(n)
		h.Capabilities = []string{"pane.v1", "agent.v1"}
		return h
	})
	err := runAgent([]string{"prompt", "p1", "go"})
	if err == nil || !strings.Contains(err.Error(), "predates `conch agent prompt`") {
		t.Fatalf("old server: %v", err)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	for _, c := range srv.calls {
		if c.Method == proto.MethodAgentPrompt {
			t.Fatal("asked an old server anyway")
		}
	}
}

func TestA4AgentPromptArguments(t *testing.T) {
	a4Env(t) // nothing listens on the socket
	for _, args := range [][]string{{"prompt"}, {"prompt", "p1"}, {"prompt", "-wait", "p1"}} {
		if err := runAgent(args); err == nil || !strings.Contains(err.Error(), "usage: conch agent prompt") {
			t.Errorf("%q: %v", args, err)
		}
	}
	if err := runAgent([]string{"prompt", "-until", "finished", "p1", "go"}); err == nil || !strings.Contains(err.Error(), `unknown state "finished"`) {
		t.Fatalf("bad -until: %v", err)
	}
	if err := runAgent([]string{"prompt", "p1", "go"}); err == nil || !strings.Contains(err.Error(), "server is not running") {
		t.Fatalf("no server: %v", err)
	}
}

func TestA4AgentPromptServerHangsUp(t *testing.T) {
	a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	srv.setHandle(func(msg proto.Message, conn *proto.Conn) (any, *proto.Error) {
		if msg.Method == proto.MethodAgentPrompt {
			go conn.Close() // the server goes away mid-wait
			return proto.AgentPromptResult{ID: "p1", Agent: "claude", Turn: 1}, nil
		}
		return nil, nil
	})
	if err := runAgent([]string{"prompt", "-wait", "p1", "go"}); err == nil {
		t.Fatal("a closed connection should end the wait")
	}
}

func TestA4AgentUsageListsPrompt(t *testing.T) {
	a4Env(t)
	if err := runAgent(nil); err == nil || !strings.Contains(err.Error(), "prompt [-wait] ID TEXT") {
		t.Fatalf("agent usage: %v", err)
	}
	if !strings.Contains(usage, "conch agent prompt [-wait]") || !strings.Contains(usage, "(exit 3)") {
		t.Fatal("conch help leaves out agent prompt")
	}
}

// startA4RealServer runs an in-process server on the test's socket, with
// /bin/sh panes only, and returns a client of its own.
func startA4RealServer(t *testing.T, dir string) *client.Client {
	t.Helper()
	srv := server.New(config.SocketPath(), dir)
	go srv.Run()
	t.Cleanup(func() {
		srv.Stop()
		for i := 0; i < 100; i++ {
			if _, err := os.Stat(config.SocketPath()); err != nil {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
	})
	for i := 0; ; i++ {
		if c, err := client.Dial(config.SocketPath(), "test"); err == nil {
			t.Cleanup(func() { c.Close() })
			return c
		}
		if i == 200 {
			t.Fatal("server did not start")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// The race end to end, against a real server: the agent finished unseen
// (done), the message turns that into idle, and the wait sits through it
// until the new work ends.
func TestA4AgentPromptRealServer(t *testing.T) {
	dir := a4Env(t)
	c := startA4RealServer(t, dir)
	var info proto.PaneInfo
	if err := call(c, proto.MethodPaneCreate, proto.PaneCreateParams{Agent: "claude",
		Command: []string{"/bin/sh", "-c", "stty -echo; exec cat"}, Cwd: dir, Cols: 80, Rows: 10}, &info); err != nil {
		t.Fatal(err)
	}
	report := func(event string) {
		if err := call(c, proto.MethodAgentReport, proto.AgentReportParams{ID: info.ID, Agent: "claude", Event: event}, nil); err != nil {
			t.Error(err)
		}
	}
	agent := func() *proto.AgentStatus {
		var list proto.PaneList
		if err := call(c, proto.MethodPaneList, nil, &list); err != nil {
			t.Error(err)
			return nil
		}
		p, _ := paneByID(list.Panes, info.ID)
		return p.Agent
	}
	until := func(what string, ok func() bool) {
		for i := 0; !ok(); i++ {
			if i == 500 {
				t.Errorf("timed out waiting for %s", what)
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	until("the agent", func() bool { return agent() != nil })
	report("UserPromptSubmit")
	report("Stop")
	if a := agent(); a.State != proto.AgentDone || a.Turn != 1 {
		t.Fatalf("before: %+v", a)
	}

	// The agent's side, while the command waits: the message arrives, the
	// done turns idle, and only then does it work and finish.
	var returned atomic.Bool
	go func() {
		until("the message", func() bool {
			var screen proto.PaneReadResult
			return call(c, proto.MethodPaneRead, proto.PaneRef{ID: info.ID}, &screen) == nil &&
				strings.Contains(strings.Join(screen.Lines, "\n"), "and the docs")
		})
		until("idle", func() bool { a := agent(); return a != nil && a.State == proto.AgentIdle })
		time.Sleep(200 * time.Millisecond) // time to (wrongly) take the idle for the answer
		if returned.Load() {
			t.Error("the wait returned on the idle left from before the message")
		}
		report("UserPromptSubmit")
		report("Stop")
	}()
	var err error
	out, _ := a4Capture(t, "", func() {
		err = runAgent([]string{"prompt", "-wait", "-timeout", "20s", info.ID, "and the docs"})
		returned.Store(true)
	})
	if err != nil || out != info.ID+" claude done\n" {
		t.Fatalf("wait: %q %v", out, err)
	}

	// And refused while it asks.
	report("PermissionRequest")
	err = runAgent([]string{"prompt", info.ID, "go on"})
	var blocked agentBlocked
	if !errors.As(err, &blocked) || !strings.Contains(err.Error(), "claude in "+info.ID+" is waiting for an answer; nothing was typed") {
		t.Fatalf("blocked: %v", err)
	}
}
