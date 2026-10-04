package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

func subIDs(sa subagents) string {
	var ids []string
	for _, a := range sa.list {
		ids = append(ids, a.ID)
	}
	return strings.Join(ids, ",")
}

func TestSubagentsStep(t *testing.T) {
	now := time.Unix(1000, 0)
	start := func(id string) proto.AgentReportParams {
		return proto.AgentReportParams{Event: "SubagentStart", SessionID: "s1", AgentID: id, AgentType: "Explore"}
	}
	stop := func(id string) proto.AgentReportParams {
		return proto.AgentReportParams{Event: "SubagentStop", SessionID: "s1", AgentID: id}
	}
	var sa subagents

	sa.step(proto.AgentReportParams{Event: "SubagentStart", SessionID: "s1"}, now) // names no subagent
	sa.step(stop("never-started"), now)
	if sa.list != nil {
		t.Fatalf("nothing started: %+v", sa.list)
	}

	sa.step(start("a"), now)
	sa.step(start("b"), now)
	sa.step(start("a"), now.Add(time.Minute)) // a repeated start is the same one
	if got := subIDs(sa); got != "a,b" {
		t.Fatalf("started: %s", got)
	}
	if a := sa.list[0]; a.Type != "Explore" || !a.Since.Equal(now) {
		t.Fatalf("first: %+v", a)
	}

	// Other events, and tool use inside a subagent, change nothing.
	for _, ev := range []string{"PreToolUse", "PostToolUse", "Stop", "Notification", "UserPromptSubmit"} {
		sa.step(proto.AgentReportParams{Event: ev, SessionID: "s1", AgentID: "a"}, now)
	}
	if got := subIDs(sa); got != "a,b" {
		t.Fatalf("after other events: %s", got)
	}

	sa.step(stop("a"), now)
	if got := subIDs(sa); got != "b" {
		t.Fatalf("after a stops: %s", got)
	}
	sa.step(stop("b"), now)
	if sa.list != nil {
		t.Fatalf("the last one stopping leaves none, not an empty list: %#v", sa.list)
	}

	// Compaction starts the same session again: the subagents still run.
	sa.step(start("c"), now)
	sa.step(proto.AgentReportParams{Event: "SessionStart", SessionID: "s1"}, now)
	if got := subIDs(sa); got != "c" {
		t.Fatalf("after compaction: %s", got)
	}
	// /clear or a resume is another session, with none of them.
	sa.step(proto.AgentReportParams{Event: "SessionStart", SessionID: "s2"}, now)
	if sa.list != nil || sa.session != "s2" {
		t.Fatalf("a new session keeps %+v (%s)", sa.list, sa.session)
	}
	// A start from another session than the one kept starts afresh too.
	sa.step(start("d"), now)
	if got := subIDs(sa); got != "d" || sa.session != "s1" {
		t.Fatalf("start in another session: %s (%s)", got, sa.session)
	}
	sa.step(proto.AgentReportParams{Event: "SessionEnd", SessionID: "s1"}, now)
	if sa.list != nil {
		t.Fatalf("session end: %+v", sa.list)
	}

	// A start with no session (an older Claude) still counts.
	sa = subagents{}
	sa.step(proto.AgentReportParams{Event: "SubagentStart", AgentID: "x"}, now)
	if got := subIDs(sa); got != "x" {
		t.Fatalf("no session: %s", got)
	}

	// The task, when the hook says it.
	sa = subagents{}
	sa.step(proto.AgentReportParams{Event: "SubagentStart", AgentID: "t", TaskDescription: "  Write tests \n"}, now)
	if sa.list[0].Description != "Write tests" || len(sa.undescribed()) != 0 {
		t.Fatalf("task: %+v", sa.list)
	}
}

func TestSubagentsCapped(t *testing.T) {
	var sa subagents
	for i := 0; i < maxSubagents+3; i++ {
		sa.step(proto.AgentReportParams{Event: "SubagentStart", SessionID: "s", AgentID: "a" + strconv.Itoa(i)}, time.Now())
	}
	if len(sa.list) != maxSubagents {
		t.Fatalf("kept %d, want %d", len(sa.list), maxSubagents)
	}
	if sa.list[0].ID != "a3" || sa.list[maxSubagents-1].ID != "a"+strconv.Itoa(maxSubagents+2) {
		t.Fatalf("the oldest go first: %s … %s", sa.list[0].ID, sa.list[maxSubagents-1].ID)
	}
	// Exactly at the cap nothing is dropped.
	sa = subagents{}
	for i := 0; i < maxSubagents; i++ {
		sa.step(proto.AgentReportParams{Event: "SubagentStart", SessionID: "s", AgentID: "b" + strconv.Itoa(i)}, time.Now())
	}
	if len(sa.list) != maxSubagents || sa.list[0].ID != "b0" {
		t.Fatalf("at the cap: %d from %s", len(sa.list), sa.list[0].ID)
	}
}

func TestSubagentsDescribeAndKey(t *testing.T) {
	sa := subagents{list: []proto.Subagent{{ID: "a"}, {ID: "b", Description: "kept"}, {ID: "c"}}}
	if got := strings.Join(sa.undescribed(), ","); got != "a,c" {
		t.Fatalf("undescribed: %s", got)
	}
	before := sa.key()
	sa.describe(map[string]string{"a": "found", "b": "not over a known one", "zz": "no such"})
	if sa.list[0].Description != "found" || sa.list[1].Description != "kept" || sa.list[2].Description != "" {
		t.Fatalf("described: %+v", sa.list)
	}
	if sa.key() == before {
		t.Fatal("a new description is a change clients are told about")
	}
	sa.describe(nil)
	var empty subagents
	if empty.key() != "" || len(empty.undescribed()) != 0 {
		t.Fatal("no subagents")
	}
}

func TestSubagentTask(t *testing.T) {
	dir := t.TempDir()
	transcript := filepath.Join(dir, "sess.jsonl")
	sub := filepath.Join(dir, "sess", "subagents")
	os.MkdirAll(sub, 0o755)
	os.WriteFile(filepath.Join(sub, "agent-a1.meta.json"),
		[]byte(`{"agentType":"Explore","description":" Study the API ","toolUseId":"toolu_1"}`), 0o600)
	os.WriteFile(filepath.Join(sub, "agent-bad.meta.json"), []byte(`{"description":`), 0o600)
	os.WriteFile(filepath.Join(sub, "agent-none.meta.json"), []byte(`{"agentType":"Explore"}`), 0o600)
	// Larger than is read: given up on, not read whole.
	os.WriteFile(filepath.Join(sub, "agent-huge.meta.json"),
		[]byte(`{"description":"`+strings.Repeat("x", 100<<10)+`"}`), 0o600)
	os.WriteFile(filepath.Join(dir, "agent-esc.meta.json"), []byte(`{"description":"outside"}`), 0o600)

	cases := []struct{ transcript, id, want string }{
		{transcript, "a1", "Study the API"},
		{transcript, "missing", ""},
		{transcript, "bad", ""},
		{transcript, "none", ""},
		{transcript, "huge", ""},
		{transcript, "", ""},
		{"", "a1", ""},
		{filepath.Join(dir, "sess"), "a1", ""}, // not a transcript
		{transcript, "../../agent-esc", ""},    // not a way out of the folder
		{transcript, `..\x`, ""},
		{filepath.Join(dir, "other.jsonl"), "a1", ""}, // another session's
	}
	for _, c := range cases {
		if got := subagentTask(c.transcript, c.id); got != c.want {
			t.Errorf("subagentTask(%q, %q) = %q, want %q", c.transcript, c.id, got, c.want)
		}
	}
}

// TestSubagentsReported drives the hooks through the server: the pane's
// agent lists its subagents, a task written after the start is picked up by
// a later report, clients are told, and a stop takes one away.
func TestSubagentsReported(t *testing.T) {
	home := a5IsolateEnv(t)
	s, dir := a5Server(t)
	ev := a5Listen(t, s)
	info, perr := s.create(proto.PaneCreateParams{Agent: "claude", Command: []string{"/bin/sleep", "30"}, Cwd: dir})
	if perr != nil {
		t.Fatal(perr)
	}
	e := a5Entry(t, s, info.ID)
	transcript := filepath.Join(home, "sess.jsonl")
	os.WriteFile(transcript, nil, 0o600)
	report := func(rp proto.AgentReportParams) {
		rp.ID, rp.Agent, rp.SessionID, rp.TranscriptPath = info.ID, "claude", "s1", transcript
		s.report(e, rp)
	}

	report(proto.AgentReportParams{Event: "UserPromptSubmit"})
	if pi := e.info(); pi.Agent == nil || pi.Agent.Subagents != nil {
		t.Fatalf("no subagents yet: %+v", pi.Agent)
	}
	updates := ev.count(proto.EventPaneUpdated)

	report(proto.AgentReportParams{Event: "SubagentStart", AgentID: "a1", AgentType: "general-purpose"})
	subs := e.info().Agent.Subagents
	if len(subs) != 1 || subs[0].ID != "a1" || subs[0].Type != "general-purpose" || subs[0].Description != "" {
		t.Fatalf("started: %+v", subs)
	}
	a5WaitFor(t, "update for the start", func() bool { return ev.count(proto.EventPaneUpdated) > updates })

	// Claude writes the task beside the transcript; the next report finds it.
	meta := filepath.Join(home, "sess", "subagents")
	os.MkdirAll(meta, 0o755)
	os.WriteFile(filepath.Join(meta, "agent-a1.meta.json"), []byte(`{"description":"Write gateway tests"}`), 0o600)
	updates = ev.count(proto.EventPaneUpdated)
	report(proto.AgentReportParams{Event: "PreToolUse"})
	if subs := e.info().Agent.Subagents; len(subs) != 1 || subs[0].Description != "Write gateway tests" {
		t.Fatalf("described: %+v", subs)
	}
	a5WaitFor(t, "update for the description", func() bool { return ev.count(proto.EventPaneUpdated) > updates })

	// One started with its task in the hook needs no file.
	report(proto.AgentReportParams{Event: "SubagentStart", AgentID: "a2", AgentType: "Explore", TaskDescription: "Run race tests"})
	if subs := e.info().Agent.Subagents; len(subs) != 2 || subs[1].Description != "Run race tests" {
		t.Fatalf("second: %+v", subs)
	}

	// The info handed out is a copy: changing it changes nothing here.
	e.info().Agent.Subagents[0].Description = "scribbled"
	if e.info().Agent.Subagents[0].Description != "Write gateway tests" {
		t.Fatal("info shares the pane's list")
	}

	// The main agent finishing its turn leaves background subagents running.
	report(proto.AgentReportParams{Event: "Stop"})
	if n := len(e.info().Agent.Subagents); n != 2 {
		t.Fatalf("after Stop: %d", n)
	}

	updates = ev.count(proto.EventPaneUpdated)
	report(proto.AgentReportParams{Event: "SubagentStop", AgentID: "a1", AgentType: "general-purpose"})
	if subs := e.info().Agent.Subagents; len(subs) != 1 || subs[0].ID != "a2" {
		t.Fatalf("after a1 stops: %+v", subs)
	}
	a5WaitFor(t, "update for the stop", func() bool { return ev.count(proto.EventPaneUpdated) > updates })

	report(proto.AgentReportParams{Event: "SessionEnd"})
	if subs := e.info().Agent.Subagents; subs != nil {
		t.Fatalf("after the session ends: %+v", subs)
	}
}

// A report with no transcript known yet (no Claude hook named one) still
// lists the subagent, by its type.
func TestSubagentsWithoutTranscript(t *testing.T) {
	a5IsolateEnv(t)
	s, dir := a5Server(t)
	info, perr := s.create(proto.PaneCreateParams{Agent: "claude", Command: []string{"/bin/sleep", "30"}, Cwd: dir})
	if perr != nil {
		t.Fatal(perr)
	}
	e := a5Entry(t, s, info.ID)
	s.report(e, proto.AgentReportParams{ID: info.ID, Agent: "claude", Event: "SubagentStart", AgentID: "a1", AgentType: "Explore"})
	if subs := e.info().Agent.Subagents; len(subs) != 1 || subs[0].Type != "Explore" {
		t.Fatalf("subagents: %+v", subs)
	}
}

// Reload state carries the subagents, and a state file from before them
// still loads, with none.
func TestSubagentsReloadState(t *testing.T) {
	rp := reloadPane{Subagents: []proto.Subagent{{ID: "a1", Type: "Explore", Description: "d", Since: time.Unix(5, 0).UTC()}},
		SubagentSession: "s1"}
	b, err := json.Marshal(rp)
	if err != nil {
		t.Fatal(err)
	}
	var back reloadPane
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if len(back.Subagents) != 1 || back.Subagents[0] != rp.Subagents[0] || back.SubagentSession != "s1" {
		t.Fatalf("round trip: %+v", back)
	}
	var old reloadPane
	if err := json.Unmarshal([]byte(`{"snapshot":{"id":"p1"},"fd":3,"dir":"/x","created_by":["p0"]}`), &old); err != nil {
		t.Fatal(err)
	}
	if old.Subagents != nil || old.SubagentSession != "" {
		t.Fatalf("older state: %+v", old)
	}
	if b, _ := json.Marshal(reloadPane{}); strings.Contains(string(b), "subagent") {
		t.Fatalf("none are written when there are none: %s", b)
	}
}
