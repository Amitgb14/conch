package server

import (
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// TestTagOwnedAttributes says, for a machine like the one in #388, which
// process is whose: an agent's pane and what it started (its MCP server, a
// conch mcp) carry the agent and its state, a shell's pane carries no
// agent, the server's own children are the server's even though the panes
// are too, a conch outside any pane is a client, and nothing else is
// conch's.
func TestTagOwnedAttributes(t *testing.T) {
	since := time.Date(2026, 11, 28, 8, 0, 0, 0, time.UTC)
	all := []proto.SystemProcess{
		{PID: 100, PPID: 1, Name: "conch"},     // the server
		{PID: 200, PPID: 100, Name: "claude"},  // pane p1's agent
		{PID: 201, PPID: 200, Name: "node"},    // its MCP server
		{PID: 202, PPID: 200, Name: "conch"},   // conch mcp, under the agent
		{PID: 300, PPID: 100, Name: "zsh"},     // pane p2: a shell
		{PID: 301, PPID: 300, Name: "go"},      // a build in it
		{PID: 400, PPID: 100, Name: "git"},     // the server's own
		{PID: 500, PPID: 1, Name: "Conch"},     // the app: its own to mark
		{PID: 501, PPID: 500, Name: "conch"},   // conch attach
		{PID: 600, PPID: 1, Name: "conch.bin"}, // this server's binary name
		{PID: 700, PPID: 1, Name: "Safari"},    // not conch's
		{PID: 800, PPID: 800, Name: "loop"},    // its own parent: no tree loops
	}
	owners := []paneOwner{
		{pid: 200, id: "p1", name: "Fix the docs", branch: "conch/x", agent: "claude", state: proto.AgentIdle, since: since},
		{pid: 300, id: "p2", name: "tests", project: "conch", state: "ignored without an agent", since: since},
		{pid: 999, id: "p3", agent: "codex"}, // its program has ended since
	}
	tagOwned(all, owners, 100, map[string]bool{"conch": true, "conch.bin": true})
	by := map[int]proto.SystemProcess{}
	for _, p := range all {
		by[p.PID] = p
	}
	for _, pid := range []int{200, 201, 202} {
		p := by[pid]
		if p.Pane != "p1" || p.Agent != "claude" || p.AgentState != proto.AgentIdle || !p.AgentSince.Equal(since) || p.Conch != "" {
			t.Errorf("pid %d = %+v, want the agent's pane, idle since %v", pid, p, since)
		}
	}
	for _, pid := range []int{300, 301} {
		p := by[pid]
		if p.Pane != "p2" || p.Project != "conch" || p.Agent != "" || p.AgentState != "" || !p.AgentSince.IsZero() {
			t.Errorf("pid %d = %+v, want the shell's pane and no agent", pid, p)
		}
	}
	for pid, want := range map[int]string{100: "server", 400: "server", 501: "client", 600: "client", 500: "", 700: "", 800: ""} {
		if p := by[pid]; p.Conch != want || p.Pane != "" {
			t.Errorf("pid %d (%s): conch %q pane %q, want conch %q", pid, p.Name, p.Conch, p.Pane, want)
		}
	}

	// No panes and nothing of conch's: nothing is marked.
	quiet := []proto.SystemProcess{{PID: 1, Name: "launchd"}, {PID: 2, PPID: 1, Name: "sh"}}
	tagOwned(quiet, nil, 12345, map[string]bool{"conch": true})
	for _, p := range quiet {
		if p.Pane != "" || p.Conch != "" {
			t.Errorf("%+v marked on a machine without conch", p)
		}
	}
	tagOwned(nil, owners, 100, nil)
}

func TestConchNames(t *testing.T) {
	n := conchNames()
	if !n["conch"] || len(n) < 1 {
		t.Errorf("names = %v", n)
	}
}
