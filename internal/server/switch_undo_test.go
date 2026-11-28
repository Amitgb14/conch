package server_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Amitgb14/conch/internal/proto"
)

// TestSwitchUndoLeavesARunningSession: a switch made with -no-continue
// leaves the new agent resumed on the written session and not yet
// writing to it, so the file still hashes as written. Undo must not
// delete it from under that agent; once the pane has moved on, it may.
func TestSwitchUndoLeavesARunningSession(t *testing.T) {
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
	var res proto.AgentSwitchResult
	if err := c.Call(ctx, proto.MethodAgentSwitch, proto.AgentSwitchParams{ID: p.ID, To: "codex", Now: true, NoContinue: true}, &res); err != nil {
		t.Fatal(err)
	}
	launches(t, log, 2)
	rollouts, _ := filepath.Glob(filepath.Join(home, ".codex", "sessions", "*", "*", "*", "rollout-*-"+res.Session+".jsonl"))
	if len(rollouts) != 1 || res.Record == "" {
		t.Fatalf("rollouts %v, record %q", rollouts, res.Record)
	}
	badRequest(t, c.Call(ctx, proto.MethodAgentSwitchUndo, proto.AgentSwitchUndoParams{Record: res.Record}, nil), "undo under a running agent")
	if _, err := os.Stat(rollouts[0]); err != nil {
		t.Fatal("undo deleted the session the agent is running on")
	}
	// Reported by its own id, it is still the one in use.
	report(t, c, proto.AgentReportParams{ID: p.ID, Agent: "codex", Event: "Stop", SessionID: res.Session})
	badRequest(t, c.Call(ctx, proto.MethodAgentSwitchUndo, proto.AgentSwitchUndoParams{Record: res.Record}, nil), "undo of a reported session")
	if _, err := os.Stat(rollouts[0]); err != nil {
		t.Fatal("undo deleted the session the agent reported")
	}

	// Once the pane is on another agent, the session is nobody's.
	var back proto.AgentSwitchResult
	if err := c.Call(ctx, proto.MethodAgentSwitch, proto.AgentSwitchParams{ID: p.ID, To: "claude", Now: true, NoContinue: true}, &back); err != nil {
		t.Fatal(err)
	}
	var undo proto.AgentSwitchUndoResult
	if err := c.Call(ctx, proto.MethodAgentSwitchUndo, proto.AgentSwitchUndoParams{Record: res.Record}, &undo); err != nil {
		t.Fatal(err)
	}
	if len(undo.Removed) != 1 || undo.Removed[0] != rollouts[0] {
		t.Fatalf("undo: %+v", undo)
	}
}

// TestSwitchUndoRemovesOnlyItsRecord: the record is found by the stamp
// made safe, and removed by it too — never by a path the stamp spells.
func TestSwitchUndoRemovesOnlyItsRecord(t *testing.T) {
	switchHome(t)
	c, dir := startServer(t)
	recs := filepath.Join(dir, "switches")
	if err := os.MkdirAll(recs, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(recs, "___outside.json"), []byte(`{"stamp":"../outside","files":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(dir, "outside.json")
	if err := os.WriteFile(outside, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(t.Context(), proto.MethodAgentSwitchUndo, proto.AgentSwitchUndoParams{Record: "../outside"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatal("undo removed a file outside the records")
	}
	if _, err := os.Stat(filepath.Join(recs, "___outside.json")); !os.IsNotExist(err) {
		t.Fatalf("the record itself stayed: %v", err)
	}
}
