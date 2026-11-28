package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
)

// TestAgentSwitchCommand: the switch is sent as asked and said — made,
// waiting, previewed, handed over — and its cancel and undo go to their
// own methods.
func TestAgentSwitchCommand(t *testing.T) {
	srv, seen := accountServer(t)
	reply := proto.AgentSwitchResult{Pane: proto.PaneInfo{ID: "p3"}, From: "claude", To: "codex", Crossed: proto.SwitchNative,
		Session: "t-9", Record: "20261124T1", Via: "conch",
		Crossing: &proto.SwitchCrossing{Messages: 12, Commands: 4, Notes: 7, Plan: true, Fits: true, Left: []string{"Claude Code's thinking and reasoning"}}}
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		m := seen.Get()
		m[msg.Method] = msg.Params
		seen.Set(m)
		switch msg.Method {
		case proto.MethodAgentSwitch:
			return reply, nil
		case proto.MethodAgentSwitchUndo:
			return proto.AgentSwitchUndoResult{Removed: []string{"/h/.codex/r.jsonl"}, Kept: []string{"/h/k.jsonl"}}, nil
		}
		return nil, nil
	})
	out, _, err := a4Out(t, func() error { return runAgent([]string{"switch", "-now", "p3", "codex"}) })
	if err != nil {
		t.Fatal(err)
	}
	if p := sent[proto.AgentSwitchParams](t, seen, proto.MethodAgentSwitch); p != (proto.AgentSwitchParams{ID: "p3", To: "codex", Now: true}) {
		t.Errorf("sent %+v", p)
	}
	for _, want := range []string{"p3: switched from claude to codex, which resumed session t-9", "conch agent switch -undo 20261124T1",
		"12 messages, 4 commands, 7 notes, the plan", "left behind: Claude Code's thinking"} {
		if !strings.Contains(out, want) {
			t.Errorf("said %q, want %q", out, want)
		}
	}

	reply = proto.AgentSwitchResult{From: "claude", To: "codex", Pending: true, Waits: []string{"the turn to end"}}
	out, _, _ = a4Out(t, func() error { return runAgent([]string{"switch", "p3", "codex", "-handoff", "-no-continue"}) })
	if p := sent[proto.AgentSwitchParams](t, seen, proto.MethodAgentSwitch); !p.Handoff || !p.NoContinue || p.Now {
		t.Errorf("sent %+v", p)
	}
	if !strings.Contains(out, "will switch from claude to codex once it has the turn to end") || !strings.Contains(out, "-cancel p3") {
		t.Errorf("pending said %q", out)
	}

	reply = proto.AgentSwitchResult{From: "codex", To: "gemini", Preview: true, Crossed: proto.SwitchHandoff, Why: "conch does not write Gemini CLI's sessions"}
	out, _, _ = a4Out(t, func() error { return runAgent([]string{"switch", "-preview", "p3", "gemini"}) })
	if !strings.Contains(out, "would hand the conversation over as a document (conch does not write Gemini CLI's sessions)") {
		t.Errorf("preview said %q", out)
	}

	if _, _, err := a4Out(t, func() error { return runAgent([]string{"switch", "-cancel", "p3"}) }); err != nil {
		t.Fatal(err)
	}
	if p := sent[proto.AgentSwitchCancelParams](t, seen, proto.MethodAgentSwitchCancel); p.ID != "p3" {
		t.Errorf("cancel sent %+v", p)
	}
	out, _, err = a4Out(t, func() error { return runAgent([]string{"switch", "-undo", "20261124T1"}) })
	if err != nil || !strings.Contains(out, "removed /h/.codex/r.jsonl") || !strings.Contains(out, "kept (written to since): /h/k.jsonl") {
		t.Errorf("undo: %v %q", err, out)
	}
	for _, args := range [][]string{{"switch"}, {"switch", "p3"}, {"switch", "p3", "codex", "more"}, {"switch", "-cancel"}, {"switch", "-undo", "x", "p3"}} {
		if _, _, err := a4Out(t, func() error { return runAgent(args) }); err == nil {
			t.Errorf("%v: no error", args)
		}
	}
}

// A switch takes as long as reading and writing the conversation takes:
// the command and the MCP tool wait for it past the usual call's wait,
// rather than saying it failed while the server goes on switching.
func TestSwitchWaitsPastAnOrdinaryCall(t *testing.T) {
	old := callWait
	callWait = 50 * time.Millisecond
	t.Cleanup(func() { callWait = old })
	srv, _ := accountServer(t)
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		if msg.Method == proto.MethodAgentSwitch {
			time.Sleep(300 * time.Millisecond)
			return proto.AgentSwitchResult{Pane: proto.PaneInfo{ID: "p3"}, From: "claude", To: "codex", Crossed: proto.SwitchNative, Session: "t-9"}, nil
		}
		return nil, nil
	})
	out, _, err := a4Out(t, func() error { return runAgent([]string{"switch", "-now", "p3", "codex"}) })
	if err != nil || !strings.Contains(out, "switched from claude to codex") {
		t.Fatalf("a slow switch: %v %q", err, out)
	}
	if switchWait < 2*time.Minute+20*time.Second {
		t.Errorf("switchWait %v is shorter than the server may take to read and write a conversation", switchWait)
	}
}

// An older server is not asked, from the command or the tool.
func TestSwitchOnAnOlderServer(t *testing.T) {
	srv, seen := accountServer(t)
	srv.setHello(func(n int) proto.HelloResult {
		h := currentHello(n)
		var caps []string
		for _, c := range h.Capabilities {
			if c != proto.CapAgentSwitch {
				caps = append(caps, c)
			}
		}
		h.Capabilities = caps
		return h
	})
	if _, _, err := a4Out(t, func() error { return runAgent([]string{"switch", "p3", "codex"}) }); err == nil || !strings.Contains(err.Error(), "server reload") {
		t.Errorf("older server: %v", err)
	}
	if r := a4MCPOne(t, "switch", map[string]any{"pane": "p3", "to": "codex"}); !r.Result.IsError || !strings.Contains(r.text(), "server reload") {
		t.Errorf("tool on an older server: %q", r.text())
	}
	if _, ok := seen.Get()[proto.MethodAgentSwitch]; ok {
		t.Error("agent.switch sent to a server without agent.switch.v1")
	}
}

// The tool sends what the command sends, and says the same.
func TestMCPSwitchTool(t *testing.T) {
	a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	var got proto.AgentSwitchParams
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		if msg.Method == proto.MethodAgentSwitch {
			_ = json.Unmarshal(msg.Params, &got)
			return proto.AgentSwitchResult{From: "claude", To: "codex", Preview: true, Crossed: proto.SwitchNative,
				Crossing: &proto.SwitchCrossing{Messages: 2}}, nil
		}
		return nil, nil
	})
	r := a4MCPOne(t, "switch", map[string]any{"pane": "p3", "to": "codex", "preview": true})
	if r.Result.IsError || got != (proto.AgentSwitchParams{ID: "p3", To: "codex", Preview: true}) {
		t.Fatalf("%q sent %+v", r.text(), got)
	}
	if !strings.Contains(r.text(), "would write codex's own session") {
		t.Errorf("said %q", r.text())
	}
	if r := a4MCPOne(t, "switch", map[string]any{"pane": "p3"}); !r.Result.IsError {
		t.Error("no agent named, not refused")
	}
	if r := a4MCPOne(t, "switch", map[string]any{"pane": "p3", "to": "codex", "force": true}); !r.Result.IsError {
		t.Error("an unknown argument, not refused")
	}
}
