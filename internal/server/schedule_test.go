package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/adapter"
	"github.com/Amitgb14/conch/internal/proto"
)

func TestCheckItem(t *testing.T) {
	at := time.Now().Add(time.Hour)
	for name, tc := range map[string]struct {
		it   proto.ScheduledItem
		want string
	}{
		"no time":          {proto.ScheduledItem{Pane: "p1", Text: "go"}, "needs a time"},
		"nothing":          {proto.ScheduledItem{At: at}, "a message"},
		"no text":          {proto.ScheduledItem{At: at, Pane: "p1", Text: "  "}, "a message"},
		"both":             {proto.ScheduledItem{At: at, Pane: "p1", Text: "go", Task: &proto.TaskCreateParams{Prompt: "x"}}, "not both"},
		"empty task":       {proto.ScheduledItem{At: at, Task: &proto.TaskCreateParams{Prompt: " "}}, "needs a prompt"},
		"too often":        {proto.ScheduledItem{At: at, Pane: "p1", Text: "go", EveryMinutes: 1}, "every 5 minutes"},
		"negative":         {proto.ScheduledItem{At: at, Pane: "p1", Text: "go", EveryMinutes: -5}, "every 5 minutes"},
		"a message":        {proto.ScheduledItem{At: at, Pane: "p1", Text: " go "}, ""},
		"a queued message": {proto.ScheduledItem{At: at, Pane: "p1", Text: "go", Queue: true}, "only a task"},
		"a queued repeat":  {proto.ScheduledItem{At: at, Task: &proto.TaskCreateParams{Prompt: "x"}, Queue: true, EveryMinutes: 60}, "runs once"},
		"too much wip":     {proto.ScheduledItem{At: at, Task: &proto.TaskCreateParams{Prompt: "x"}, Queue: true, WIP: 21}, "1 to 20"},
		"negative wip":     {proto.ScheduledItem{At: at, Task: &proto.TaskCreateParams{Prompt: "x"}, Queue: true, WIP: -1}, "1 to 20"},
		"a queued task":    {proto.ScheduledItem{At: at, Task: &proto.TaskCreateParams{Prompt: "x"}, Queue: true}, ""},
		"a daily task":     {proto.ScheduledItem{At: at, Task: &proto.TaskCreateParams{Prompt: "bump deps"}, EveryMinutes: 1440}, ""},
	} {
		perr := checkItem(&tc.it)
		switch {
		case tc.want == "" && perr != nil:
			t.Errorf("%s: refused: %v", name, perr)
		case tc.want != "" && (perr == nil || !strings.Contains(perr.Message, tc.want)):
			t.Errorf("%s: %v, want %q", name, perr, tc.want)
		}
	}
}

func TestRepeat(t *testing.T) {
	now := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	if _, keep := repeat(proto.ScheduledItem{At: now}, now); keep {
		t.Fatal("a one-off was kept")
	}
	// Missed runs (the machine was asleep) are skipped, not run in a burst.
	it, keep := repeat(proto.ScheduledItem{At: now.Add(-50 * time.Hour), EveryMinutes: 1440}, now)
	if !keep || !it.At.After(now) || it.At.Sub(now) > 24*time.Hour {
		t.Fatalf("next run at %v", it.At)
	}
}

func TestScheduleBookLoad(t *testing.T) {
	dir := t.TempDir()
	at := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	os.WriteFile(filepath.Join(dir, "schedule.json"), []byte(`{"items": [
		{"id": "s1", "at": "`+at+`", "pane": "p1", "text": "continue"},
		{"id": "s2", "at": "`+at+`"},
		{"at": "`+at+`", "pane": "p1", "text": "no id"},
		{"id": "s4", "at": "`+at+`", "task": {"project_id": "r1", "prompt": "bump"}, "every_minutes": 1440}
	]}`), 0o600)
	b := loadSchedule(dir)
	var ids []string
	for _, it := range b.list().Items {
		ids = append(ids, it.ID)
	}
	if strings.Join(ids, ",") != "s1,s4" {
		t.Fatalf("kept %v", ids)
	}
	for _, bad := range []string{"", "not json", `{"items": [{"id": "s1",`} {
		os.WriteFile(filepath.Join(dir, "schedule.json"), []byte(bad), 0o600)
		if got := loadSchedule(dir).list().Items; len(got) != 0 {
			t.Fatalf("%q loaded %v", bad, got)
		}
	}
	if got := loadSchedule(t.TempDir()).list().Items; len(got) != 0 {
		t.Fatal("no file loaded something")
	}
}

// scheduleFixture is a server with an agent pane that echoes what it is
// typed, so a delivered message can be seen on its screen.
func scheduleFixture(t *testing.T) (*Server, *entry, string) {
	t.Helper()
	s, _, work := shareFixture(t)
	e := agentPane(t, s, "p1", "claude", work, "stty -echo; exec cat")
	t.Cleanup(func() { e.p.Close() })
	return s, e, work
}

func TestScheduledMessageIsDelivered(t *testing.T) {
	s, e, _ := scheduleFixture(t)
	now := time.Now()
	it, perr := s.scheduleAdd(proto.ScheduledItem{At: now.Add(time.Hour), Pane: "p1", Text: "continue please"})
	if perr != nil {
		t.Fatal(perr)
	}
	if it.ID == "" || it.Agent != "claude" || it.PaneCreated.IsZero() {
		t.Fatalf("added %+v", it)
	}
	// Not yet due: nothing happens.
	s.fireDue(now)
	if len(s.schedules.list().Items) != 1 {
		t.Fatal("fired early")
	}
	// Due: typed in and gone from the list.
	s.fireDue(now.Add(2 * time.Hour))
	if items := s.schedules.list().Items; len(items) != 0 {
		t.Fatalf("still scheduled: %+v", items)
	}
	a5WaitFor(t, "the message on screen", func() bool { return screenCount(e, "continue please") > 0 })

	// It survives a restart: the file has it.
	s.scheduleAdd(proto.ScheduledItem{At: now.Add(time.Hour), Pane: "p1", Text: "later"})
	if got := loadSchedule(s.configDir).list().Items; len(got) != 1 || got[0].Text != "later" {
		t.Fatalf("reloaded %+v", got)
	}
}

func TestScheduledMessageWaitsForABusyAgent(t *testing.T) {
	s, e, _ := scheduleFixture(t)
	now := time.Now()
	s.scheduleAdd(proto.ScheduledItem{At: now, Pane: "p1", Text: "continue"})
	for event, state := range map[string]string{"UserPromptSubmit": proto.AgentWorking, "PermissionRequest": proto.AgentBlocked} {
		hook(s, e, event)
		if st, _ := agentState(e); st != state {
			t.Fatalf("%s left the agent %s", event, st)
		}
		s.fireDue(now)
		items := s.schedules.list().Items
		if len(items) != 1 || items[0].Waiting == "" || !items[0].At.After(now) {
			t.Fatalf("%s: %+v", state, items)
		}
	}
}

func TestScheduledMessageForAStrangerIsDropped(t *testing.T) {
	s, _, _ := scheduleFixture(t)
	now := time.Now()
	// A pane that has since been replaced by another of the same ID.
	it, _ := s.scheduleAdd(proto.ScheduledItem{At: now, Pane: "p1", Text: "continue"})
	s.schedules.mu.Lock()
	s.schedules.items[0].PaneCreated = it.PaneCreated.Add(-time.Hour)
	s.schedules.mu.Unlock()
	s.fireDue(now)
	if len(s.schedules.list().Items) != 0 {
		t.Fatal("kept a message for a pane that is not the one it was for")
	}
	// Another agent now, and a pane gone.
	s.scheduleAdd(proto.ScheduledItem{At: now, Pane: "p1", Agent: "codex", Text: "continue"})
	s.fireDue(now)
	if len(s.schedules.list().Items) != 0 {
		t.Fatal("kept a message for another agent")
	}
	if _, perr := s.scheduleAdd(proto.ScheduledItem{At: now, Pane: "p9", Text: "x"}); perr == nil {
		t.Fatal("scheduled for a pane that doesn't exist")
	}
}

func TestScheduleAddRefusals(t *testing.T) {
	s, _, work := scheduleFixture(t)
	shell := agentPane(t, s, "p2", "", work, "sleep 30")
	t.Cleanup(func() { shell.p.Close() })
	at := time.Now().Add(time.Hour)
	if _, perr := s.scheduleAdd(proto.ScheduledItem{At: at, Pane: "p2", Text: "x"}); perr == nil || !strings.Contains(perr.Message, "not running an agent") {
		t.Fatalf("a shell: %v", perr)
	}
	if _, perr := s.scheduleAdd(proto.ScheduledItem{At: at, Task: &proto.TaskCreateParams{ProjectID: "nope", Prompt: "x"}}); perr == nil {
		t.Fatal("a task for an unknown project")
	}
	for range scheduleMax {
		s.schedules.items = append(s.schedules.items, proto.ScheduledItem{ID: "x", At: at, Pane: "p1", Text: "x"})
	}
	if _, perr := s.scheduleAdd(proto.ScheduledItem{At: at, Pane: "p1", Text: "x"}); perr == nil || !strings.Contains(perr.Message, "remove some first") {
		t.Fatalf("over the cap: %v", perr)
	}
	if perr := s.scheduleRemove("nope"); perr == nil || perr.Code != proto.ErrNotFound {
		t.Fatalf("removing nothing: %v", perr)
	}
	if perr := s.scheduleRemove("x"); perr != nil {
		t.Fatal(perr)
	}
}

func TestScheduledTaskStarts(t *testing.T) {
	a5IsolateEnv(t)
	sh := filepath.Join(t.TempDir(), "fake-shell")
	os.WriteFile(sh, []byte("#!/bin/sh\nexec sleep 30\n"), 0o755)
	t.Setenv("SHELL", sh)
	s, dir := a5Server(t)
	var err error
	if s.adapters, err = adapter.New("/usr/local/bin/conch", s.configDir); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(dir, "api")
	a5GitRepo(t, repo)
	p, err := s.projects.add(repo, false)
	if err != nil {
		t.Fatal(err)
	}
	a5WaitFor(t, "the project loaded", func() bool { s.projects.refresh(p, ""); return p.snapshot().Base == "main" })
	now := time.Date(2026, 10, 14, 7, 0, 0, 0, time.Local)
	daily, perr := s.scheduleAdd(proto.ScheduledItem{At: now, EveryMinutes: 1440,
		Task: &proto.TaskCreateParams{ProjectID: p.id, Prompt: "Bump the dependencies", Name: "deps"}})
	if perr != nil {
		t.Fatal(perr)
	}
	s.fireDue(now)
	items := s.schedules.list().Items
	if len(items) != 1 || items[0].ID != daily.ID || items[0].LastError != "" || !items[0].At.Equal(now.Add(24*time.Hour)) {
		t.Fatalf("after the first run: %+v", items)
	}
	// Each run is a branch of its own; the second day's doesn't collide.
	s.fireDue(now.Add(24 * time.Hour))
	if items := s.schedules.list().Items; items[0].LastError != "" {
		t.Fatalf("second run: %s", items[0].LastError)
	}
	for _, day := range []string{"20261014-0700", "20261015-0700"} {
		if _, ok := p.branchWorktree("api/bump-dependencies-" + day); !ok {
			var have []string
			for _, wt := range p.snapshot().Worktrees {
				have = append(have, wt.Branch)
			}
			t.Fatalf("no worktree for day %s among %v", day, have)
		}
	}
	// A one-off that fails says so in the log and is done.
	s.scheduleAdd(proto.ScheduledItem{At: now, Task: &proto.TaskCreateParams{ProjectID: p.id, Prompt: "x", Branch: "bad..name"}})
	s.fireDue(now.Add(48 * time.Hour))
	for _, it := range s.schedules.list().Items {
		if it.Task.Branch == "bad..name" {
			t.Fatalf("a failed one-off was kept: %+v", it)
		}
	}
}

func TestQueuedTasksWaitForRoom(t *testing.T) {
	a5IsolateEnv(t)
	sh := filepath.Join(t.TempDir(), "fake-shell")
	os.WriteFile(sh, []byte("#!/bin/sh\nexec sleep 30\n"), 0o755)
	t.Setenv("SHELL", sh)
	s, dir := a5Server(t)
	var err error
	if s.adapters, err = adapter.New("/usr/local/bin/conch", s.configDir); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(dir, "api")
	a5GitRepo(t, repo)
	p, err := s.projects.add(repo, false)
	if err != nil {
		t.Fatal(err)
	}
	a5WaitFor(t, "the project loaded", func() bool { s.projects.refresh(p, ""); return p.snapshot().Base == "main" })
	now := time.Now()
	for _, prompt := range []string{"first job", "second job", "third job"} {
		if _, perr := s.scheduleAdd(proto.ScheduledItem{At: now, Queue: true, WIP: 2,
			Task: &proto.TaskCreateParams{ProjectID: p.id, Prompt: prompt}}); perr != nil {
			t.Fatal(perr)
		}
	}
	// One tick starts as many as there is room for, in the order queued.
	s.fireDue(now)
	items := s.schedules.list().Items
	if len(items) != 1 || items[0].Task.Prompt != "third job" || !strings.Contains(items[0].Waiting, "2 agents are running") {
		t.Fatalf("after the first tick: %+v", items)
	}
	if n := s.runningAgents(p.id); n != 2 {
		t.Fatalf("%d agents running, want 2", n)
	}
	// Still full: it waits.
	s.fireDue(now.Add(time.Minute))
	if len(s.schedules.list().Items) != 1 {
		t.Fatal("started past the limit")
	}
	// One closes, and the next starts.
	for _, info := range s.list() {
		if info.ProjectID == p.id && info.State == proto.PaneRunning {
			if perr := s.close(info.ID); perr != nil {
				t.Fatal(perr)
			}
			break
		}
	}
	s.fireDue(now.Add(2 * time.Minute))
	if items := s.schedules.list().Items; len(items) != 0 {
		t.Fatalf("still queued: %+v", items)
	}
	if n := s.runningAgents(p.id); n != 2 {
		t.Fatalf("%d agents running, want 2", n)
	}
}
