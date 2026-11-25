package server_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/server"
)

// switchHome is a home with stand-in agents on the PATH agents get: each
// writes its name and arguments, then waits. Codex has no app-server, as
// one too old to import a Claude Code session itself.
func switchHome(t *testing.T) (home, log string) {
	t.Helper()
	home = a5Env(t)
	bin := filepath.Join(home, ".local", "bin")
	os.MkdirAll(bin, 0o755)
	log = filepath.Join(home, "launches")
	for _, name := range []string{"claude", "codex", "gemini"} {
		script := "#!/bin/sh\n[ \"$1\" = app-server ] && exit 1\nprintf '" + name + "|%s\\n' \"$*\" >> " + log + "\nexec cat\n"
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return home, log
}

// claudeSession is a Claude Code conversation in home: a request, a
// command and its output, and an answer that says what was decided.
func claudeSession(t *testing.T, home, dir string) (id, path string) {
	t.Helper()
	id = "1b2c3d4e-0000-4000-8000-0000000000aa"
	path = filepath.Join(home, ".claude", "projects", "-src", id+".jsonl")
	os.MkdirAll(filepath.Dir(path), 0o700)
	body := strings.Join([]string{
		`{"type":"user","cwd":"` + dir + `","message":{"role":"user","content":"Pick a queue for the uploads"}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Checking what we have."},{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls queue"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"redis.go\nsqs.go"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Decided: SQS, since redis is not deployed."}]}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return id, path
}

func report(t *testing.T, c *client.Client, p proto.AgentReportParams) {
	t.Helper()
	if err := c.Call(t.Context(), proto.MethodAgentReport, p, nil); err != nil {
		t.Fatal(err)
	}
}

// TestSwitchClaudeToCodexAndBack: a Claude Code conversation is written as
// Codex's own session and resumed by Codex in the same pane — same id —
// which says where it came from; switching back resumes it as Claude
// Code's. A preview changes nothing; undo removes what was written.
func TestSwitchClaudeToCodexAndBack(t *testing.T) {
	home, log := switchHome(t)
	c, dir := startServer(t)
	if !hasCap(c, proto.CapAgentSwitch) {
		t.Fatal("agent.switch.v1 not announced")
	}
	ctx := t.Context()
	var p proto.PaneInfo
	if err := c.Call(ctx, proto.MethodPaneCreate, proto.PaneCreateParams{Agent: "claude", Cwd: dir, Cols: 80, Rows: 20, Name: "uploader"}, &p); err != nil {
		t.Fatal(err)
	}
	launches(t, log, 1)

	// Refused before its conversation is known, to itself, to nothing.
	badRequest(t, c.Call(ctx, proto.MethodAgentSwitch, proto.AgentSwitchParams{ID: p.ID, To: "codex"}, nil), "no conversation yet")
	sid, conv := claudeSession(t, home, dir)
	report(t, c, proto.AgentReportParams{ID: p.ID, Agent: "claude", Event: "Stop", TranscriptPath: conv})
	badRequest(t, c.Call(ctx, proto.MethodAgentSwitch, proto.AgentSwitchParams{ID: p.ID, To: "claude"}, nil), "to itself")
	badRequest(t, c.Call(ctx, proto.MethodAgentSwitch, proto.AgentSwitchParams{ID: p.ID}, nil), "to no agent")
	badRequest(t, c.Call(ctx, proto.MethodAgentSwitch, proto.AgentSwitchParams{ID: p.ID, To: "nope"}, nil), "to an unknown agent")

	var pre proto.AgentSwitchResult
	if err := c.Call(ctx, proto.MethodAgentSwitch, proto.AgentSwitchParams{ID: p.ID, To: "codex", Preview: true}, &pre); err != nil {
		t.Fatal(err)
	}
	if !pre.Preview || pre.Crossed != proto.SwitchNative || pre.Crossing == nil || pre.Crossing.Messages != 3 || pre.Crossing.Commands != 1 ||
		!pre.Crossing.Fits || pre.FromSession != sid {
		t.Fatalf("preview: %+v %+v", pre, pre.Crossing)
	}
	if got := launches(t, log, 1); len(got) != 1 {
		t.Fatalf("a preview started something: %q", got)
	}

	var res proto.AgentSwitchResult
	if err := c.Call(ctx, proto.MethodAgentSwitch, proto.AgentSwitchParams{ID: p.ID, To: "codex", Now: true, NoContinue: true}, &res); err != nil {
		t.Fatal(err)
	}
	if res.Pane.ID != p.ID || res.Crossed != proto.SwitchNative || res.Via != "conch" || res.Session == "" || res.Record == "" {
		t.Fatalf("switched: %+v", res)
	}
	if sf := res.Pane.SwitchedFrom; sf == nil || sf.Agent != "claude" || sf.Session != sid {
		t.Fatalf("switched from: %+v", res.Pane.SwitchedFrom)
	}
	second := launches(t, log, 2)[1]
	if !strings.HasPrefix(second, "codex|") || !strings.HasSuffix(second, " resume "+res.Session) {
		t.Fatalf("Codex started as %q", second)
	}
	rollouts, _ := filepath.Glob(filepath.Join(home, ".codex", "sessions", "*", "*", "*", "rollout-*-"+res.Session+".jsonl"))
	if len(rollouts) != 1 {
		t.Fatalf("rollouts: %v", rollouts)
	}
	if b, _ := os.ReadFile(rollouts[0]); !strings.Contains(string(b), "Decided: SQS, since redis is not deployed.") || !strings.Contains(string(b), `"exec_command"`) {
		t.Fatalf("the rollout:\n%s", b)
	}
	now := panesNow(t, c)
	if len(now) != 1 || now[p.ID].Name != "uploader" || now[p.ID].SwitchedFrom == nil {
		t.Fatalf("panes after: %+v", now)
	}
	// The source session is untouched.
	if b, _ := os.ReadFile(conv); !strings.HasSuffix(string(b), "not deployed.\"}]}}\n") {
		t.Fatal("the Claude session changed")
	}
	waitEvent(t, c, func(m proto.Message) bool {
		var r proto.AgentSwitchResult
		return m.Event == proto.EventAgentSwitched && json.Unmarshal(m.Data, &r) == nil && r.Pane.ID == p.ID && r.To == "codex"
	})

	// And back: Codex's session, now the pane's, written as Claude's own.
	report(t, c, proto.AgentReportParams{ID: p.ID, Agent: "codex", Event: "Stop", SessionID: res.Session})
	var back proto.AgentSwitchResult
	if err := c.Call(ctx, proto.MethodAgentSwitch, proto.AgentSwitchParams{ID: p.ID, To: "claude", Now: true, NoContinue: true, Reason: "back"}, &back); err != nil {
		t.Fatal(err)
	}
	if back.Pane.ID != p.ID || back.Crossed != proto.SwitchNative || back.Pane.SwitchedFrom.Agent != "codex" || back.FromSession != res.Session {
		t.Fatalf("back: %+v", back)
	}
	third := launches(t, log, 3)[2]
	if !strings.HasPrefix(third, "claude|") || !strings.Contains(third, back.Session) {
		t.Fatalf("Claude started as %q", third)
	}
	written, _ := filepath.Glob(filepath.Join(home, ".claude", "projects", "*", back.Session+".jsonl"))
	if len(written) != 1 {
		t.Fatalf("Claude's session: %v", written)
	}

	// Undo removes what the first switch wrote, and only that.
	var undo proto.AgentSwitchUndoResult
	if err := c.Call(ctx, proto.MethodAgentSwitchUndo, proto.AgentSwitchUndoParams{Record: res.Record}, &undo); err != nil {
		t.Fatal(err)
	}
	if len(undo.Removed) != 1 || undo.Removed[0] != rollouts[0] {
		t.Fatalf("undo: %+v", undo)
	}
	if _, err := os.Stat(conv); err != nil {
		t.Fatal("undo removed the source")
	}
	var perr *proto.Error
	if err := c.Call(ctx, proto.MethodAgentSwitchUndo, proto.AgentSwitchUndoParams{Record: res.Record}, nil); err == nil {
		t.Fatal("undone twice")
	} else if !errors.As(err, &perr) || perr.Code != proto.ErrNotFound {
		t.Fatalf("undone twice: %v", err)
	}
}

// TestSwitchWaitsForTheTurn: never mid-turn. With -now it is refused;
// without, it waits and is made when the turn ends — or called off.
func TestSwitchWaitsForTheTurn(t *testing.T) {
	t.Cleanup(server.SetMoveTiming(100*time.Millisecond, time.Second))
	home, log := switchHome(t)
	c, dir := startServer(t)
	ctx := t.Context()
	var p proto.PaneInfo
	if err := c.Call(ctx, proto.MethodPaneCreate, proto.PaneCreateParams{Agent: "claude", Cwd: dir, Cols: 80, Rows: 20}, &p); err != nil {
		t.Fatal(err)
	}
	launches(t, log, 1)
	_, conv := claudeSession(t, home, dir)
	report(t, c, proto.AgentReportParams{ID: p.ID, Agent: "claude", Event: "UserPromptSubmit", TranscriptPath: conv})

	badRequest(t, c.Call(ctx, proto.MethodAgentSwitch, proto.AgentSwitchParams{ID: p.ID, To: "codex", Now: true}, nil), "mid-turn with -now")

	var res proto.AgentSwitchResult
	if err := c.Call(ctx, proto.MethodAgentSwitch, proto.AgentSwitchParams{ID: p.ID, To: "codex", NoContinue: true}, &res); err != nil {
		t.Fatal(err)
	}
	if !res.Pending || len(res.Waits) != 1 || res.Waits[0] != "the turn to end" {
		t.Fatalf("not waiting: %+v", res)
	}
	// Called off, nothing happens when the turn ends.
	if err := c.Call(ctx, proto.MethodAgentSwitchCancel, proto.AgentSwitchCancelParams{ID: p.ID}, nil); err != nil {
		t.Fatal(err)
	}
	badRequest(t, c.Call(ctx, proto.MethodAgentSwitchCancel, proto.AgentSwitchCancelParams{ID: p.ID}, nil), "nothing to call off")
	report(t, c, proto.AgentReportParams{ID: p.ID, Agent: "claude", Event: "Stop", TranscriptPath: conv})
	time.Sleep(500 * time.Millisecond) // several ticks of the switcher
	if got := launches(t, log, 1); len(got) != 1 {
		t.Fatalf("a switch called off was made: %q", got)
	}

	// Asked again mid-turn, it is made once the turn ends.
	report(t, c, proto.AgentReportParams{ID: p.ID, Agent: "claude", Event: "UserPromptSubmit", TranscriptPath: conv})
	if err := c.Call(ctx, proto.MethodAgentSwitch, proto.AgentSwitchParams{ID: p.ID, To: "codex", NoContinue: true}, &res); err != nil || !res.Pending {
		t.Fatalf("%v %+v", err, res)
	}
	report(t, c, proto.AgentReportParams{ID: p.ID, Agent: "claude", Event: "Stop", TranscriptPath: conv})
	if second := launches(t, log, 2)[1]; !strings.HasPrefix(second, "codex|") || !strings.Contains(second, " resume ") {
		t.Fatalf("Codex started as %q", second)
	}
	if now := panesNow(t, c); len(now) != 1 || now[p.ID].SwitchedFrom == nil {
		t.Fatalf("after: %+v", now)
	}
}

// TestSwitchHandsOverToAnotherAgent: an agent conch writes no sessions for
// is started in the same pane with the conversation as a document for its
// first message, and the result says so and why.
func TestSwitchHandsOverToAnotherAgent(t *testing.T) {
	home, log := switchHome(t)
	c, dir := startServer(t)
	ctx := t.Context()
	var p proto.PaneInfo
	if err := c.Call(ctx, proto.MethodPaneCreate, proto.PaneCreateParams{Agent: "claude", Cwd: dir, Cols: 80, Rows: 20}, &p); err != nil {
		t.Fatal(err)
	}
	launches(t, log, 1)
	_, conv := claudeSession(t, home, dir)
	report(t, c, proto.AgentReportParams{ID: p.ID, Agent: "claude", Event: "Stop", TranscriptPath: conv})

	// A handoff asked for, to an agent conch could write for.
	var pre proto.AgentSwitchResult
	if err := c.Call(ctx, proto.MethodAgentSwitch, proto.AgentSwitchParams{ID: p.ID, To: "codex", Preview: true, Handoff: true}, &pre); err != nil {
		t.Fatal(err)
	}
	if pre.Crossed != proto.SwitchHandoff || pre.Why != "a handoff was asked for" {
		t.Fatalf("%+v", pre)
	}

	var res proto.AgentSwitchResult
	if err := c.Call(ctx, proto.MethodAgentSwitch, proto.AgentSwitchParams{ID: p.ID, To: "gemini", Now: true}, &res); err != nil {
		t.Fatal(err)
	}
	if res.Crossed != proto.SwitchHandoff || !strings.Contains(res.Why, "does not write Gemini CLI's sessions") || res.Document == "" || res.Pane.ID != p.ID {
		t.Fatalf("%+v", res)
	}
	if b, err := os.ReadFile(res.Document); err != nil || !strings.Contains(string(b), "Decided: SQS") {
		t.Fatalf("document: %v", err)
	}
	if second := launches(t, log, 2)[1]; !strings.HasPrefix(second, "gemini|") || !strings.Contains(second, ".conch/handoff/") {
		t.Fatalf("Gemini started as %q", second)
	}
}

// A pane with no agent, or one that has ended, is not switched.
func TestSwitchRefusesAShell(t *testing.T) {
	switchHome(t)
	c, dir := startServer(t)
	var p proto.PaneInfo
	if err := c.Call(t.Context(), proto.MethodPaneCreate, proto.PaneCreateParams{Cwd: dir, Cols: 80, Rows: 20}, &p); err != nil {
		t.Fatal(err)
	}
	badRequest(t, c.Call(t.Context(), proto.MethodAgentSwitch, proto.AgentSwitchParams{ID: p.ID, To: "codex"}, nil), "a shell")
	var perr *proto.Error
	if err := c.Call(t.Context(), proto.MethodAgentSwitch, proto.AgentSwitchParams{ID: "p99", To: "codex"}, nil); !errors.As(err, &perr) || perr.Code != proto.ErrNotFound {
		t.Fatalf("no such pane: %v", err)
	}
}

// A client watching the pane goes on seeing it: the new program's screen
// comes to the same subscription, under the same id.
func TestSwitchKeepsTheViewers(t *testing.T) {
	home, log := switchHome(t)
	c, dir := startServer(t)
	ctx := t.Context()
	var p proto.PaneInfo
	if err := c.Call(ctx, proto.MethodPaneCreate, proto.PaneCreateParams{Agent: "claude", Cwd: dir, Cols: 80, Rows: 20}, &p); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(ctx, proto.MethodPaneSubscribe, proto.PaneRef{ID: p.ID}, nil); err != nil {
		t.Fatal(err)
	}
	launches(t, log, 1)
	_, conv := claudeSession(t, home, dir)
	report(t, c, proto.AgentReportParams{ID: p.ID, Agent: "claude", Event: "Stop", TranscriptPath: conv})
	if err := c.Call(ctx, proto.MethodAgentSwitch, proto.AgentSwitchParams{ID: p.ID, To: "codex", Now: true, NoContinue: true}, nil); err != nil {
		t.Fatal(err)
	}
	launches(t, log, 2)
	// Typed in pieces, so the marker is only whole on the screen once the
	// new program has echoed it.
	if err := c.Call(ctx, proto.MethodPaneSendText, proto.PaneSendTextParams{ID: p.ID, Text: "after-" + "the-switch\r"}, nil); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		f := nextFrame(t, c, p.ID, 10*time.Second)
		if strings.Contains(strings.Join(f.Lines, "\n"), "after-the-switch") {
			return
		}
	}
	t.Fatal("no frame of the new program reached the client watching the pane")
}
