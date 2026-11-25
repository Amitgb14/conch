package server

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/pane"
	"github.com/Amitgb14/conch/internal/proto"
)

// A reload between a switch being asked for and its moment keeps it, and
// a switched pane still says where its conversation came from; state from
// an older build, which says neither, loads with neither.
func TestAdoptKeepsASwitch(t *testing.T) {
	a5IsolateEnv(t)
	s, dir := a5Server(t)
	fd1, pid1 := a5PtyProgram(t, "exec sleep 30")
	fd2, pid2 := a5PtyProgram(t, "exec sleep 30")
	at := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	st := &reloadState{NextID: 3, Panes: []reloadPane{
		{Snapshot: pane.Snapshot{ID: "p1", Command: []string{"sleep"}, Cwd: dir, PID: pid1, Cols: 40, Rows: 6}, FD: fd1, Dir: dir, Loose: true,
			SwitchedFrom: &proto.SwitchOrigin{Agent: "claude", Session: "s1", At: at},
			Switch:       &proto.AgentSwitchParams{ID: "p1", To: "claude", NoContinue: true}},
		{Snapshot: pane.Snapshot{ID: "p2", Command: []string{"sleep"}, Cwd: dir, PID: pid2, Cols: 40, Rows: 6}, FD: fd2, Dir: dir, Loose: true},
	}}
	// Through JSON, as a reload carries it.
	b, _ := json.Marshal(st)
	var back reloadState
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	s.adopt(&back)
	list := s.list()
	if len(list) != 2 || list[0].SwitchedFrom == nil || list[0].SwitchedFrom.Session != "s1" || !list[0].SwitchedFrom.At.Equal(at) {
		t.Fatalf("after the reload: %+v", list)
	}
	if list[1].SwitchedFrom != nil {
		t.Fatalf("an older pane gained an origin: %+v", list[1].SwitchedFrom)
	}
	sw, ok := s.switcher.get("p1")
	if !ok || sw.params.To != "claude" || !sw.params.NoContinue {
		t.Fatalf("the waiting switch: %+v %v", sw, ok)
	}
	if _, ok := s.switcher.get("p2"); ok {
		t.Fatal("a switch nobody asked for")
	}
	// The waiting one goes no further than a pane running no agent: it is
	// dropped, and said to have failed.
	s.lookForSwitches(time.Now())
	if _, ok := s.switcher.get("p1"); ok {
		t.Fatal("a switch for a pane with no agent is still waiting")
	}
}

// What a reload writes says it too.
func TestReloadStateNamesTheSwitch(t *testing.T) {
	rp := reloadPane{SwitchedFrom: &proto.SwitchOrigin{Agent: "codex"}, Switch: &proto.AgentSwitchParams{ID: "p4", To: "claude"}}
	b, _ := json.Marshal(rp)
	var m map[string]json.RawMessage
	json.Unmarshal(b, &m)
	if _, ok := m["switched_from"]; !ok {
		t.Fatalf("%s", b)
	}
	if _, ok := m["switch"]; !ok {
		t.Fatalf("%s", b)
	}
	b, _ = json.Marshal(reloadPane{})
	m = nil
	json.Unmarshal(b, &m)
	if _, ok := m["switch"]; ok {
		t.Fatalf("an empty pane writes a switch: %s", b)
	}
}
