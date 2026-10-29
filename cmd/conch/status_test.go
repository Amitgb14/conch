package main

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
)

var statusPanes = []proto.PaneInfo{
	{ID: "p1", Name: "claude", State: proto.PaneRunning, Cols: 116, Rows: 44, Cwd: "/Users/you/workspace/conch",
		Command: []string{"/bin/zsh", "-lc", `PATH="$HOME/.local/bin:$PATH"; exec claude --settings x`},
		Agent:   &proto.AgentStatus{Name: "claude", State: proto.AgentWorking}},
	{ID: "p2", Name: "reviewer", State: proto.PaneRunning, Cols: 116, Rows: 44, Cwd: "/Users/you/workspace/conch.worktrees/conch-how-make-better-ai-agents",
		Agent: &proto.AgentStatus{Name: "codex", State: proto.AgentBlocked}},
	{ID: "p3", Name: "check · master", State: proto.PaneExited, ExitCode: 0, Cols: 120, Rows: 40, Cwd: "/Users/you/workspace/conch",
		Command: []string{"/bin/sh", "-lc", "go test -timeout 30m ./..."}},
	{ID: "p4", Name: "zsh", State: proto.PaneRunning, Cols: 80, Rows: 24, Cwd: "/tmp", Command: []string{"/bin/zsh", "-l"}},
}

// TestStatusFitsTheTerminal: no line wider than the window, at any width,
// and what tells panes apart is the last to go.
func TestStatusFitsTheTerminal(t *testing.T) {
	for _, width := range []int{1, 10, 20, 40, 60, 80, 120, 200} {
		out := statusFitted(statusPanes, "/Users/you", width)
		for _, l := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
			if n := utf8.RuneCountInString(l); n > width {
				t.Errorf("width %d: a line of %d: %q", width, n, l)
			}
		}
	}

	wide := statusFitted(statusPanes, "/Users/you", 200)
	lines := strings.Split(wide, "\n")
	if lines[0] != "2 agents: 1 working, 1 waiting" {
		t.Errorf("summary %q", lines[0])
	}
	if f := strings.Fields(lines[1]); strings.Join(f, " ") != "ID NAME AGENT STATE FOLDER COMMAND" {
		t.Errorf("header %q (no pane is on ssh, so no SSH column)", lines[1])
	}
	if f := strings.Fields(lines[2]); strings.Join(f, " ") != "p1 claude claude working ~/workspace/conch" {
		t.Errorf("agent row %q", lines[2])
	}
	if f := strings.Fields(lines[3]); strings.Join(f, " ") != "p2 reviewer codex waiting ~/workspace/conch.worktrees/conch-how-make-better-ai-agents" {
		t.Errorf("waiting row %q", lines[3])
	}
	for _, want := range []string{"exited(0)", "go test -timeout 30m ./...", "/tmp", "/bin/zsh -l"} {
		if !strings.Contains(wide, want) {
			t.Errorf("wide: no %q:\n%s", want, wide)
		}
	}
	// An agent's command is the agent: not repeated as a wall of shell.
	if strings.Contains(wide, "exec claude") {
		t.Errorf("an agent's command is shown:\n%s", wide)
	}

	// At 80 the folder gives up its start, never its end, and a command
	// with no room to say anything is left out rather than cut to
	// "/bin/sh -lc…".
	narrow := statusFitted(statusPanes, "/Users/you", 80)
	if strings.Contains(narrow, "COMMAND") || strings.Contains(narrow, "/bin/sh") {
		t.Errorf("80: a command squeezed in:\n%s", narrow)
	}
	if !strings.Contains(narrow, "…") || !strings.Contains(narrow, "waiting") {
		t.Errorf("80:\n%s", narrow)
	}
	for _, l := range strings.Split(narrow, "\n") {
		if strings.HasPrefix(l, "p2") && !strings.Contains(l, "agents") {
			t.Errorf("80: the folder lost its end: %q", l)
		}
	}
}

func TestStatusSSHColumn(t *testing.T) {
	panes := append([]proto.PaneInfo{}, statusPanes...)
	panes[3].SSH = "me@box"
	out := statusFitted(panes, "", 200)
	if !strings.Contains(strings.Split(out, "\n")[1], "SSH") || !strings.Contains(out, "me@box") {
		t.Errorf("ssh:\n%s", out)
	}
}

func TestStatusSummary(t *testing.T) {
	for _, tc := range []struct {
		panes []proto.PaneInfo
		want  string
	}{
		{nil, ""},
		{statusPanes[3:], ""}, // no agents
		{statusPanes[:1], "1 agent: 1 working"},
		// An exited agent is not one of them; a state a newer server
		// knows is still counted.
		{[]proto.PaneInfo{
			{State: proto.PaneExited, Agent: &proto.AgentStatus{State: proto.AgentWorking}},
			{State: proto.PaneRunning, Agent: &proto.AgentStatus{State: proto.AgentDone}},
			{State: proto.PaneRunning, Agent: &proto.AgentStatus{State: "thinking"}},
		}, "2 agents: 1 done, 1 other"},
	} {
		if got := statusSummary(tc.panes); got != tc.want {
			t.Errorf("%q, want %q", got, tc.want)
		}
	}
}

func TestTildeAndCuts(t *testing.T) {
	for _, tc := range []struct{ path, home, want string }{
		{"/Users/you", "/Users/you", "~"},
		{"/Users/you/src", "/Users/you", "~/src"},
		{"/Users/youth", "/Users/you", "/Users/youth"}, // not under it
		{"/src", "", "/src"},
		{"/src", "/", "/src"},
	} {
		if got := tildePath(tc.path, tc.home); got != tc.want {
			t.Errorf("%q %q: %q", tc.path, tc.home, got)
		}
	}
	if cutStart("abcdef", 4) != "…def" || cutEnd("abcdef", 4) != "abc…" || cutEnd("ab", 4) != "ab" ||
		cutEnd("abc", 1) != "a" || cutStart("abc", 0) != "" || cutEnd("ü日本語", 3) != "ü日…" {
		t.Error("cuts")
	}
}

// TestStatusInATerminal: through the command, with a terminal of 80
// columns; piped, or with -wide, the table scripts read is what it was.
func TestStatusInATerminal(t *testing.T) {
	a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	srv.setHandle(func(proto.Message, *proto.Conn) (any, *proto.Error) {
		return proto.PaneList{Panes: statusPanes}, nil
	})
	old := terminalWidth
	t.Cleanup(func() { terminalWidth = old })

	terminalWidth = func() int { return 80 }
	var err error
	out, _ := a4Capture(t, "", func() { err = runStatus(nil) })
	if err != nil || !strings.Contains(out, "2 agents: 1 working, 1 waiting") || strings.Contains(out, "SIZE") {
		t.Fatalf("terminal: %v\n%s", err, out)
	}
	for _, l := range strings.Split(strings.TrimSuffix(out, "\n"), "\n")[1:] {
		if n := utf8.RuneCountInString(l); n > 80 {
			t.Errorf("a line of %d: %q", n, l)
		}
	}
	out, _ = a4Capture(t, "", func() { err = runStatus([]string{"-wide"}) })
	if err != nil || !strings.Contains(out, "ID  NAME") || !strings.Contains(out, "SIZE") || !strings.Contains(out, "claude:working") {
		t.Fatalf("-wide: %v\n%s", err, out)
	}
	terminalWidth = func() int { return 0 } // a pipe
	piped, _ := a4Capture(t, "", func() { err = runStatus(nil) })
	if piped != out {
		t.Fatalf("piped is not the wide table:\n%s\nwant\n%s", piped, out)
	}

	// -json: the server and every pane, for a script that would rather.
	out, _ = a4Capture(t, "", func() { err = runStatus([]string{"-json"}) })
	var got struct {
		Server *proto.HelloResult `json:"server"`
		Panes  []proto.PaneInfo   `json:"panes"`
	}
	if err != nil || json.Unmarshal([]byte(out), &got) != nil || got.Server == nil || len(got.Panes) != 4 || got.Panes[1].Agent.State != proto.AgentBlocked {
		t.Fatalf("-json: %v\n%s", err, out)
	}

	for _, args := range [][]string{{"-wide", "-json"}, {"extra"}} {
		if _, _ = a4Capture(t, "", func() { err = runStatus(args) }); err == nil || !strings.Contains(err.Error(), statusUsage) {
			t.Errorf("%v: %v", args, err)
		}
	}
}

// With no server, -json is still JSON: a script should not have to parse
// a sentence to learn there is nothing running.
func TestStatusJSONWithoutServer(t *testing.T) {
	a4Env(t)
	var err error
	out, _ := a4Capture(t, "", func() { err = runStatus([]string{"-json"}) })
	if err != nil || strings.TrimSpace(out) != `{"panes":[],"server":null}` {
		t.Fatalf("%v %q", err, out)
	}
}
