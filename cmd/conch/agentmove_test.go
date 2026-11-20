package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// TestAgentMoveCommand: the move is sent as asked and said; no account
// leaves the choice to conch; an older server is not asked.
func TestAgentMoveCommand(t *testing.T) {
	srv, seen := accountServer(t)
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		m := seen.Get()
		m[msg.Method] = msg.Params
		seen.Set(m)
		if msg.Method == proto.MethodAgentMove {
			return proto.PaneInfo{ID: "p9", Account: "work", MovedFrom: "default", Replaces: "p3"}, nil
		}
		return nil, nil
	})
	out, _, err := a4Out(t, func() error { return runAgent([]string{"move", "p3", "-account", "work", "-continue"}) })
	if err != nil {
		t.Fatal(err)
	}
	if p := sent[proto.AgentMoveParams](t, seen, proto.MethodAgentMove); p != (proto.AgentMoveParams{ID: "p3", Account: "work", Continue: true}) {
		t.Errorf("sent %+v", p)
	}
	if !strings.Contains(out, "p3: conversation carried to work and resumed there as p9") {
		t.Errorf("said %q", out)
	}
	if _, _, err := a4Out(t, func() error { return runAgent([]string{"move", "p3"}) }); err != nil {
		t.Fatal(err)
	}
	if p := sent[proto.AgentMoveParams](t, seen, proto.MethodAgentMove); p.Account != "" {
		t.Errorf("no account asked for %q", p.Account)
	}
	for _, args := range [][]string{{"move"}, {"move", "p3", "p4"}} {
		if _, _, err := a4Out(t, func() error { return runAgent(args) }); err == nil {
			t.Errorf("%v: no error", args)
		}
	}
}

func TestAccountSuggestCommand(t *testing.T) {
	srv, seen := accountServer(t)
	yes := true
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		m := seen.Get()
		m[msg.Method] = msg.Params
		seen.Set(m)
		if msg.Method == proto.MethodAccountSuggest {
			return proto.AccountSuggestion{Agent: "claude", Accounts: []proto.AccountRank{
				{Account: "backup2", Room: 96, Reported: true, SignedIn: &yes, Agents: 3, Usable: true, Why: "96% room"},
				{Account: "", Room: 0, Reported: true, Usable: false, Why: "full · resets in 2h 10m", Agents: 17},
			}}, nil
		}
		return nil, nil
	})
	out, _, err := a4Out(t, func() error { return runAccount([]string{"suggest"}) })
	if err != nil {
		t.Fatal(err)
	}
	if p := sent[proto.AccountSuggestParams](t, seen, proto.MethodAccountSuggest); p.Agent != "claude" {
		t.Errorf("asked for %+v", p)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], "backup2") || !strings.Contains(lines[0], "conch's pick") ||
		!strings.Contains(lines[1], "default") || strings.Contains(lines[1], "pick") || !strings.Contains(lines[1], "17 agents") {
		t.Errorf("printed:\n%s", out)
	}
	// Nothing usable: said so, nothing picked.
	got, _, _ := a4Out(t, func() error {
		printSuggestion(proto.AccountSuggestion{Agent: "codex", Accounts: []proto.AccountRank{{Account: "", Why: "full"}}}, time.Now())
		return nil
	})
	if !strings.Contains(got, "No codex account has room") || strings.Contains(got, "pick") {
		t.Errorf("none usable:\n%s", got)
	}
}

func TestMoveOnAnOlderServer(t *testing.T) {
	srv, seen := accountServer(t)
	srv.setHello(func(n int) proto.HelloResult {
		h := currentHello(n)
		var caps []string
		for _, c := range h.Capabilities {
			if c != proto.CapAgentMove {
				caps = append(caps, c)
			}
		}
		h.Capabilities = caps
		return h
	})
	for _, run := range []func() error{
		func() error { return runAgent([]string{"move", "p3"}) },
		func() error { return runAccount([]string{"suggest"}) },
	} {
		if _, _, err := a4Out(t, run); err == nil || !strings.Contains(err.Error(), "server reload") {
			t.Errorf("older server: %v", err)
		}
	}
	for _, m := range []string{proto.MethodAgentMove, proto.MethodAccountSuggest} {
		if _, ok := seen.Get()[m]; ok {
			t.Errorf("%s sent to a server without agent.move.v1", m)
		}
	}
}

// TestAccountBack: every running agent moved off the account goes back,
// others are left; an account with no room refuses them all, moving none.
func TestAccountBack(t *testing.T) {
	srv, seen := accountServer(t)
	var moves []string
	room := true
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		m := seen.Get()
		m[msg.Method] = msg.Params
		seen.Set(m)
		switch msg.Method {
		case proto.MethodPaneList:
			return proto.PaneList{Panes: []proto.PaneInfo{
				{ID: "p12", State: proto.PaneRunning, Account: "backup2", MovedFrom: "default", Agent: &proto.AgentStatus{Name: "claude"}},
				{ID: "p13", State: proto.PaneRunning, Account: "backup2"}, // started there
				{ID: "p14", State: proto.PaneExited, Account: "backup2", MovedFrom: "default"},
				{ID: "p15", State: proto.PaneRunning, Account: "backup2", MovedFrom: "default", Agent: &proto.AgentStatus{Name: "claude"}},
			}}, nil
		case proto.MethodAccountSuggest:
			return proto.AccountSuggestion{Agent: "claude", Accounts: []proto.AccountRank{{Account: "", Usable: room, Why: map[bool]string{true: "80% room", false: "full · resets in 2h 10m"}[room]}}}, nil
		case proto.MethodAgentMove:
			var p proto.AgentMoveParams
			json.Unmarshal(msg.Params, &p)
			moves = append(moves, p.ID+"→"+p.Account+"/"+p.Reason)
			return proto.PaneInfo{ID: "n" + p.ID}, nil
		}
		return nil, nil
	})
	out, _, err := a4Out(t, func() error { return runAccount([]string{"back"}) })
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(moves, ",") != "p12→default/back,p15→default/back" {
		t.Errorf("moved %v", moves)
	}
	if !strings.Contains(out, "p12: back on default as np12") {
		t.Errorf("said %q", out)
	}
	moves, room = nil, false
	if _, _, err := a4Out(t, func() error { return runAccount([]string{"back", "default"}) }); err == nil || !strings.Contains(err.Error(), "no room yet") {
		t.Errorf("no room: %v", err)
	}
	if len(moves) != 0 {
		t.Errorf("moved with no room: %v", moves)
	}
	// Nobody moved off another account: said, nothing done.
	out, _, err = a4Out(t, func() error { return runAccount([]string{"back", "work"}) })
	if err != nil || !strings.Contains(out, "No agent was moved off work") {
		t.Errorf("none: %q %v", out, err)
	}
}
