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

// intakeGH puts a gh on CONCH_GH whose issue list is whatever the test last
// wrote with setIssues (newest first, as gh lists them), and which logs
// every call. A list file holding FAIL makes the list fail as gh does.
func intakeGH(t *testing.T) (setIssues func(...int), log func() string) {
	t.Helper()
	dir := t.TempDir()
	list, calls := filepath.Join(dir, "issues.json"), filepath.Join(dir, "calls")
	gh := filepath.Join(dir, "gh")
	os.WriteFile(gh, []byte(`#!/bin/sh
echo "$*" >> `+calls+`
case "$1 $2" in
"issue list") if grep -q FAIL `+list+`; then echo 'gh: To get started with GitHub CLI, please run:  gh auth login' >&2; exit 4; fi; cat `+list+` ;;
"issue comment") ;;
"pr list") echo '[]' ;;
"api graphql") echo '{}' ;;
*) exit 7 ;;
esac
`), 0o755)
	t.Setenv("CONCH_GH", gh)
	setIssues = func(numbers ...int) {
		var out []map[string]any
		for _, n := range numbers {
			out = append(out, map[string]any{"number": n, "title": "thing " + strconv.Itoa(n), "url": "https://x/" + strconv.Itoa(n),
				"labels": []map[string]string{{"name": "ready"}}})
		}
		raw, _ := json.Marshal(out)
		if out == nil {
			raw = []byte("[]")
		}
		os.WriteFile(list, raw, 0o644)
	}
	setIssues()
	log = func() string { b, _ := os.ReadFile(calls); return string(b) }
	return setIssues, log
}

// queuedIssues is the issues on the schedule, in the order queued.
func queuedIssues(s *Server) []int {
	var out []int
	for _, it := range s.schedules.list().Items {
		if it.Task != nil && it.Task.Issue > 0 {
			out = append(out, it.Task.Issue)
		}
	}
	return out
}

func sameInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func intakeOn(s *Server, projectID string, wip int) {
	s.autopilot.set(proto.AutopilotPolicy{Intake: map[string]string{projectID: "ready"}, IntakeWIP: wip})
}

// startQueued stands in for the schedule starting the queued tasks: they
// leave the schedule, as a task that started does.
func startQueued(t *testing.T, s *Server) {
	t.Helper()
	for _, it := range s.schedules.list().Items {
		if perr := s.scheduleRemove(it.ID); perr != nil {
			t.Fatal(perr)
		}
	}
}

func TestIntakeTakesLabelledIssuesOldestFirstUpToTheLimit(t *testing.T) {
	s, p := afterFixture(t)
	quietPRs(t, p)
	setIssues, ghLog := intakeGH(t)
	setIssues(14, 12, 11) // newest first, as gh lists them
	intakeOn(s, p.id, 2)

	s.intakeOnce()
	if got := queuedIssues(s); !sameInts(got, []int{11, 12}) {
		t.Fatalf("queued %v, want the two oldest [11 12]", got)
	}
	for _, it := range s.schedules.list().Items {
		if !it.Queue || it.WIP != 2 || it.Task.ProjectID != p.id || !strings.Contains(it.Note, "intake: #") {
			t.Fatalf("queued as %+v", it)
		}
	}
	calls := ghLog()
	if !strings.Contains(calls, "issue list --state open --label ready") {
		t.Fatalf("not asked by label:\n%s", calls)
	}
	if strings.Count(calls, "issue comment") != 2 || !strings.Contains(calls, "issue comment 11 --body conch took this from the `ready` label") {
		t.Fatalf("each issue taken says so on GitHub:\n%s", calls)
	}

	// The queue is full: the next look takes nothing more.
	s.intakeOnce()
	if got := queuedIssues(s); !sameInts(got, []int{11, 12}) {
		t.Fatalf("after a second look: %v", got)
	}
	// They start, and the next issue is taken — never the first two again.
	startQueued(t, s)
	s.intakeOnce()
	if got := queuedIssues(s); !sameInts(got, []int{14}) {
		t.Fatalf("after they started: %v, want [14]", got)
	}
	if strings.Count(ghLog(), "issue comment") != 3 {
		t.Fatalf("commented again on an issue already taken:\n%s", ghLog())
	}

	// A reload or a restart remembers what was taken.
	startQueued(t, s)
	s.intake = loadIntake(s.configDir)
	s.intakeOnce()
	if got := queuedIssues(s); len(got) != 0 {
		t.Fatalf("a fresh server took %v again", got)
	}
}

func TestIntakeLeavesAnIssueSomebodyIsOn(t *testing.T) {
	s, p := afterFixture(t)
	quietPRs(t, p)
	setIssues, ghLog := intakeGH(t)
	setIssues(9, 8, 7)
	// #7 has a branch, as conch task -issue 7 names one; #8 is already
	// queued by hand.
	apGit(t, p.root, "branch", "conch/issue-7-thing")
	s.projects.refresh(p, "")
	a5WaitFor(t, "the branch seen", func() bool {
		s.projects.refresh(p, "")
		for _, b := range p.snapshot().Branches {
			if b.Name == "conch/issue-7-thing" {
				return true
			}
		}
		return false
	})
	if _, perr := s.scheduleAdd(proto.ScheduledItem{At: time.Now(), Queue: true, Task: &proto.TaskCreateParams{ProjectID: p.id, Issue: 8}}); perr != nil {
		t.Fatal(perr)
	}
	intakeOn(s, p.id, 5)

	s.intakeOnce()
	if got := queuedIssues(s); !sameInts(got, []int{8, 9}) {
		t.Fatalf("queued %v, want #8 (by hand) and #9 only", got)
	}
	if c := ghLog(); strings.Contains(c, "issue comment 7 ") || strings.Contains(c, "issue comment 8 ") {
		t.Fatalf("commented on work already in hand:\n%s", c)
	}
}

func TestIntakeTakesAnIssueAgainOnceRelabelled(t *testing.T) {
	s, p := afterFixture(t)
	quietPRs(t, p)
	setIssues, _ := intakeGH(t)
	setIssues(5)
	intakeOn(s, p.id, 2)
	s.intakeOnce()
	startQueued(t, s)

	// Still labelled: the same request, not a new one.
	s.intakeOnce()
	if got := queuedIssues(s); len(got) != 0 {
		t.Fatalf("taken twice: %v", got)
	}
	// The label taken off (or the issue closed), then put back.
	setIssues()
	s.intakeOnce()
	setIssues(5)
	s.intakeOnce()
	if got := queuedIssues(s); !sameInts(got, []int{5}) {
		t.Fatalf("relabelled, queued %v, want [5]", got)
	}
}

func TestIntakeSaysAFailingGHOnce(t *testing.T) {
	s, p := afterFixture(t)
	quietPRs(t, p)
	_, ghLog := intakeGH(t)
	os.WriteFile(filepath.Join(filepath.Dir(os.Getenv("CONCH_GH")), "issues.json"), []byte("FAIL"), 0o644)
	intakeOn(s, p.id, 2)
	s.intakeOnce()
	s.intakeOnce()
	if got := queuedIssues(s); len(got) != 0 {
		t.Fatalf("queued %v from a failed list", got)
	}
	s.intake.mu.Lock()
	said := s.intake.said[p.id]
	s.intake.mu.Unlock()
	if !strings.Contains(said, "gh auth login") {
		t.Fatalf("the failure was not kept to say once: %q", said)
	}
	if strings.Count(ghLog(), "issue list") != 2 || strings.Contains(ghLog(), "issue comment") {
		t.Fatalf("calls:\n%s", ghLog())
	}
}

func TestIntakeIsOffWithoutALabel(t *testing.T) {
	s, p := afterFixture(t)
	quietPRs(t, p)
	setIssues, ghLog := intakeGH(t)
	setIssues(1)
	s.autopilot.set(proto.AutopilotPolicy{AutoPR: true, Intake: map[string]string{p.id: "  "}})
	s.intakeOnce()
	if strings.Contains(ghLog(), "issue") || len(queuedIssues(s)) != 0 {
		t.Fatalf("read issues with no label named:\n%s", ghLog())
	}
	// A project that is gone is said, not a crash.
	intakeOn(s, "nope", 2)
	s.intakeOnce()
	if strings.Contains(ghLog(), "issue") {
		t.Fatal("read issues for a project that is not there")
	}
}

func TestIntakeFileLoads(t *testing.T) {
	dir := t.TempDir()
	if b := loadIntake(dir); len(b.saved.Taken) != 0 {
		t.Fatal("a missing file is an empty book")
	}
	os.WriteFile(filepath.Join(dir, "intake.json"), []byte("{not json"), 0o600)
	if b := loadIntake(dir); b.saved.Taken == nil {
		t.Fatal("a broken file still gives a book to write to")
	}
	os.WriteFile(filepath.Join(dir, "intake.json"), []byte(`{"taken":{"r1#3":{"project":"r1","issue":3,"taken":"2026-11-14T00:00:00Z","gone":true}}}`), 0o600)
	if r := loadIntake(dir).saved.Taken["r1#3"]; r.Issue != 3 || !r.Gone {
		t.Fatalf("read back %+v", r)
	}
}

func TestCleanPolicyTriage(t *testing.T) {
	p := cleanPolicy(proto.AutopilotPolicy{Triage: map[string]string{"a": " new ", "b": ""}, TriageAgent: " codex "})
	if len(p.Triage) != 1 || p.Triage["a"] != "new" || p.TriageAgent != "codex" {
		t.Fatalf("%+v", p)
	}
	if p := cleanPolicy(proto.AutopilotPolicy{Triage: map[string]string{"b": " "}}); p.Triage != nil {
		t.Fatalf("%+v", p)
	}
}

func TestCleanPolicyIntake(t *testing.T) {
	p := cleanPolicy(proto.AutopilotPolicy{Intake: map[string]string{"a": " ready ", "b": "", "": "x"}, IntakeWIP: 99})
	if len(p.Intake) != 1 || p.Intake["a"] != "ready" || p.IntakeWIP != maxWIP {
		t.Fatalf("%+v", p)
	}
	if p := cleanPolicy(proto.AutopilotPolicy{Intake: map[string]string{"b": " "}, IntakeWIP: -3}); p.Intake != nil || p.IntakeWIP != 0 {
		t.Fatalf("%+v", p)
	}
}

// The queue waits on agents that are busy, not on every agent left open:
// a project with idle agents in it once kept its queue waiting for good.
func TestHoldsQueue(t *testing.T) {
	now := time.Now()
	old := now.Add(-time.Hour)
	agent := func(state string, created time.Time) proto.PaneInfo {
		return proto.PaneInfo{State: proto.PaneRunning, Created: created, Agent: &proto.AgentStatus{Name: "claude", State: state}}
	}
	cases := map[string]struct {
		info     proto.PaneInfo
		launched bool
		want     bool
	}{
		"working":                {agent(proto.AgentWorking, old), false, true},
		"waiting on the person":  {agent(proto.AgentBlocked, old), false, true},
		"state not known":        {agent("", old), false, true},
		"idle":                   {agent(proto.AgentIdle, old), false, false},
		"done":                   {agent(proto.AgentDone, old), false, false},
		"idle but just started":  {agent(proto.AgentIdle, now.Add(-time.Second)), false, true},
		"launched, not detected": {proto.PaneInfo{State: proto.PaneRunning, Created: old}, true, true},
		"a shell":                {proto.PaneInfo{State: proto.PaneRunning, Created: now}, false, false},
		"exited":                 {proto.PaneInfo{State: proto.PaneExited, Created: old, Agent: &proto.AgentStatus{State: proto.AgentWorking}}, true, false},
	}
	for name, c := range cases {
		if got := holdsQueue(c.info, c.launched, now); got != c.want {
			t.Errorf("%s: holds the queue %v, want %v", name, got, c.want)
		}
	}
	rev := agent(proto.AgentWorking, old)
	rev.Name = proto.ReviewerPrefix + "api"
	if holdsQueue(rev, false, now) {
		t.Error("a reviewer holds the queue")
	}
}
