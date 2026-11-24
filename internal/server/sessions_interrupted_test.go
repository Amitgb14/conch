package server

import (
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// TestInterruptedRunOpenAgainListedOnce is #346: an interrupted run whose
// session has since been resumed and is open in a pane was appended as a
// second entry with the same agent, id and folder, which Conch.app drew as
// a blank gap.
func TestInterruptedRunOpenAgainListedOnce(t *testing.T) {
	now := time.Now()
	out := []proto.SessionInfo{
		{Agent: "claude", ID: "s1", Dir: "/r", PaneID: "p9", Updated: now},
		{Agent: "claude", ID: "s2", Dir: "/r", Updated: now.Add(-time.Hour)},
	}
	runs := []agentRun{
		{Agent: "claude", Session: "s1", Dir: "/r", Started: now.Add(-3 * time.Hour), Seen: now.Add(-2 * time.Hour)},
		{Agent: "claude", Session: "s2", Dir: "/r", Started: now.Add(-3 * time.Hour), Seen: now.Add(-2 * time.Hour)},
	}
	got := dedupeSessions(markInterrupted(out, runs))
	if len(got) != 2 {
		t.Fatalf("want each session once, got %d: %+v", len(got), got)
	}
	if got[0].ID != "s1" || got[0].PaneID != "p9" || got[0].Interrupted {
		t.Errorf("open session should be listed once, not interrupted: %+v", got[0])
	}
	if got[1].ID != "s2" || !got[1].Interrupted {
		t.Errorf("closed session should be marked interrupted: %+v", got[1])
	}
}

// A run on another account is not the open session of the same id on the
// default one; it is listed under its own account.
func TestInterruptedRunKeepsItsAccount(t *testing.T) {
	now := time.Now()
	out := []proto.SessionInfo{{Agent: "claude", ID: "s1", Dir: "/r", PaneID: "p9", Updated: now}}
	runs := []agentRun{{Agent: "claude", Account: "work", Session: "s1", Dir: "/r", Started: now.Add(-time.Hour), Seen: now}}
	got := dedupeSessions(markInterrupted(out, runs))
	if len(got) != 2 || got[1].Account != "work" || !got[1].Interrupted {
		t.Fatalf("want the work account's run beside the open session: %+v", got)
	}
}

// A run with no session id still matches the newest closed session in its
// window, and is listed by itself when there is none.
func TestInterruptedRunWithoutID(t *testing.T) {
	now := time.Now()
	run := agentRun{Agent: "codex", Dir: "/r", Started: now.Add(-time.Hour), Seen: now}
	got := markInterrupted([]proto.SessionInfo{{Agent: "codex", ID: "a", Dir: "/r", Updated: now.Add(-30 * time.Minute)}}, []agentRun{run})
	if len(got) != 1 || !got[0].Interrupted {
		t.Fatalf("want the session in the run's window marked: %+v", got)
	}
	got = markInterrupted([]proto.SessionInfo{{Agent: "codex", ID: "a", Dir: "/r", PaneID: "p1", Updated: now}}, []agentRun{run})
	if len(got) != 2 || got[1].ID != "" || !got[1].Interrupted {
		t.Fatalf("an open session is not the run's; the run is listed alone: %+v", got)
	}
	if got := markInterrupted(nil, nil); len(got) != 0 {
		t.Fatalf("nothing in, nothing out: %+v", got)
	}
}

func TestDedupeSessions(t *testing.T) {
	in := []proto.SessionInfo{
		{Agent: "claude", ID: "x", Interrupted: true},
		{Agent: "claude", ID: "x", PaneID: "p1"},
		{Agent: "claude", Account: "work", ID: "x"},
		{Agent: "codex", ID: "x"},
		{Agent: "claude", ID: "", Interrupted: true},
		{Agent: "claude", ID: "", Interrupted: true},
		{Agent: "codex", ID: "x", Interrupted: true},
	}
	got := dedupeSessions(in)
	if len(got) != 5 {
		t.Fatalf("want 5 entries, got %d: %+v", len(got), got)
	}
	if got[0].PaneID != "p1" || got[0].Interrupted {
		t.Errorf("the open entry wins and is not interrupted: %+v", got[0])
	}
	if got[2].Agent != "codex" || !got[2].Interrupted {
		t.Errorf("the interrupted mark carries over: %+v", got[2])
	}
	if got := dedupeSessions(nil); len(got) != 0 {
		t.Fatalf("nil: %+v", got)
	}
}
