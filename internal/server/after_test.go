package server

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/adapter"
	"github.com/Amitgb14/conch/internal/gitx"
	"github.com/Amitgb14/conch/internal/proto"
)

// afterFixture is a server with a git project whose tasks start a stand-in
// agent: a login shell that runs sleep, which conch takes for Claude.
func afterFixture(t *testing.T) (*Server, *project) {
	t.Helper()
	a5IsolateEnv(t)
	sh := filepath.Join(t.TempDir(), "fake-shell")
	os.WriteFile(sh, []byte("#!/bin/sh\nexec sleep 30\n"), 0o755)
	t.Setenv("SHELL", sh)
	s, dir := a5Server(t)
	// Nothing here needs worktrees watched. A stopped FSEvents stream leaves
	// its pump waiting for good, and enough of them left behind by tests
	// that run first make the reload test's exec hang on macOS (AGENTS.md).
	s.watcher.stop()
	s.watcher = nil
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
	t.Cleanup(func() {
		for _, info := range s.list() {
			s.close(info.ID)
		}
	})
	return s, p
}

// branchPane is the entry of the agent working on branch, once conch has
// seen the agent in it.
func branchPane(t *testing.T, s *Server, p *project, branch string) *entry {
	t.Helper()
	var e *entry
	a5WaitFor(t, "the agent on "+branch, func() bool {
		s.projects.refresh(p, "")
		for _, info := range s.list() {
			if info.Branch == branch && info.State == proto.PaneRunning {
				e = a5Entry(t, s, info.ID)
				s.observe(e)
				return e.info().Agent != nil
			}
		}
		return false
	})
	return e
}

// commitOn makes a commit in branch's worktree, as an agent would.
func commitOn(t *testing.T, p *project, branch, file string) string {
	t.Helper()
	wt, ok := p.branchWorktree(branch)
	if !ok {
		t.Fatalf("no worktree for %s", branch)
	}
	os.WriteFile(filepath.Join(wt.Path, file), []byte(file+"\n"), 0o644)
	gitIn(t, wt.Path, "add", file)
	gitIn(t, wt.Path, "commit", "-q", "-m", "add "+file)
	head, err := gitx.BranchHead(context.Background(), p.root, branch)
	if err != nil {
		t.Fatal(err)
	}
	return head
}

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func onlyItem(t *testing.T, s *Server) proto.ScheduledItem {
	t.Helper()
	items := s.schedules.list().Items
	if len(items) != 1 {
		t.Fatalf("scheduled: %+v", items)
	}
	return items[0]
}

func isAncestor(t *testing.T, p *project, a, b string) bool {
	t.Helper()
	return gitx.IsAncestor(context.Background(), p.root, a, b)
}

// A task after a branch waits while that branch's agent works, and then
// starts on top of what it committed.
func TestAfterBranchStacksOnIt(t *testing.T) {
	s, p := afterFixture(t)
	if _, perr := s.createTask(proto.TaskCreateParams{ProjectID: p.id, Prompt: "add the model", Branch: "api/model"}); perr != nil {
		t.Fatal(perr)
	}
	first := branchPane(t, s, p, "api/model")
	hook(s, first, "UserPromptSubmit")

	now := time.Now()
	it, perr := s.scheduleAdd(proto.ScheduledItem{At: now, After: "api/model",
		Task: &proto.TaskCreateParams{ProjectID: p.id, Prompt: "add the endpoint", Branch: "api/endpoint"}})
	if perr != nil {
		t.Fatal(perr)
	}
	if it.AfterBranch != "api/model" || it.AfterHead == "" || !it.AfterSeen {
		t.Fatalf("added %+v", it)
	}
	s.fireDue(now)
	if w := onlyItem(t, s).Waiting; !strings.Contains(w, "still at work") {
		t.Fatalf("while the agent works: %q", w)
	}
	// Blocked on a question is not finished either.
	hook(s, first, "PermissionRequest")
	s.fireDue(now)
	if w := onlyItem(t, s).Waiting; !strings.Contains(w, "still at work") {
		t.Fatalf("while the agent asks: %q", w)
	}

	head := commitOn(t, p, "api/model", "model.go")
	hook(s, first, "Stop")
	s.projects.refresh(p, "")
	s.fireDue(now.Add(time.Minute))
	if items := s.schedules.list().Items; len(items) != 0 {
		t.Fatalf("still waiting: %+v", items)
	}
	s.projects.refresh(p, "")
	if _, ok := p.branchWorktree("api/endpoint"); !ok {
		t.Fatal("the second task did not start")
	}
	if !isAncestor(t, p, head, "api/endpoint") {
		t.Fatal("the second task did not start from the first one's commit")
	}
}

// A task after one still on the schedule — named by its branch or its ID —
// waits for it to start, then for it to finish, and then stacks on it.
func TestAfterScheduledTask(t *testing.T) {
	s, p := afterFixture(t)
	now := time.Now()
	first, perr := s.scheduleAdd(proto.ScheduledItem{At: now.Add(time.Hour),
		Task: &proto.TaskCreateParams{ProjectID: p.id, Prompt: "add the model", Branch: "api/model"}})
	if perr != nil {
		t.Fatal(perr)
	}
	byBranch, perr := s.scheduleAdd(proto.ScheduledItem{At: now, After: "api/model",
		Task: &proto.TaskCreateParams{ProjectID: p.id, Prompt: "add the endpoint", Branch: "api/endpoint"}})
	if perr != nil {
		t.Fatal(perr)
	}
	if byBranch.After != first.ID || byBranch.AfterBranch != "" {
		t.Fatalf("the branch of a scheduled task resolves to the task: %+v", byBranch)
	}
	byID, perr := s.scheduleAdd(proto.ScheduledItem{At: now, After: first.ID,
		Task: &proto.TaskCreateParams{ProjectID: p.id, Prompt: "document it", Branch: "api/docs", Base: "main"}})
	if perr != nil {
		t.Fatal(perr)
	}
	// A chain: after the second, which is after the first.
	chained, perr := s.scheduleAdd(proto.ScheduledItem{At: now, After: byBranch.ID,
		Task: &proto.TaskCreateParams{ProjectID: p.id, Prompt: "add a client", Branch: "api/client"}})
	if perr != nil {
		t.Fatal(perr)
	}

	s.fireDue(now)
	for _, it := range s.schedules.list().Items {
		if it.ID != first.ID && !strings.Contains(it.Waiting, "to start, then finish") {
			t.Fatalf("%s before the first started: %q", it.Task.Prompt, it.Waiting)
		}
	}

	// The first starts; in the same tick the others learn its branch, and
	// wait for its agent rather than starting beside it.
	s.fireDue(now.Add(2 * time.Hour))
	byIDNow, byBranchNow := itemByID(s, byID.ID), itemByID(s, byBranch.ID)
	if byIDNow == nil || byBranchNow == nil {
		t.Fatalf("started too soon: %+v", s.schedules.list().Items)
	}
	if byIDNow.AfterBranch != "api/model" || byBranchNow.AfterBranch != "api/model" {
		t.Fatalf("not told the branch: %+v %+v", byIDNow, byBranchNow)
	}
	if !strings.Contains(byBranchNow.Waiting, "api/model") {
		t.Fatalf("waiting: %q", byBranchNow.Waiting)
	}
	if c := itemByID(s, chained.ID); c == nil || c.AfterBranch != "" {
		t.Fatalf("the end of the chain: %+v", c)
	}

	e := branchPane(t, s, p, "api/model")
	hook(s, e, "UserPromptSubmit")
	s.fireDue(now.Add(2 * time.Hour))
	if it := itemByID(s, byBranch.ID); it == nil || !it.AfterSeen {
		t.Fatalf("the agent at work was not noted: %+v", it)
	}
	head := commitOn(t, p, "api/model", "model.go")
	hook(s, e, "Stop")
	s.projects.refresh(p, "")
	s.fireDue(now.Add(3 * time.Hour))
	s.projects.refresh(p, "")
	if !isAncestor(t, p, head, "api/endpoint") {
		t.Fatal("the endpoint is not stacked on the model")
	}
	// The one with a Base of its own waited, but starts from that base.
	if isAncestor(t, p, head, "api/docs") {
		t.Fatal("a task with -base was stacked anyway")
	}
	if c := itemByID(s, chained.ID); c == nil || c.AfterBranch != "api/endpoint" {
		t.Fatalf("the end of the chain now waits for the endpoint: %+v", c)
	}
}

func itemByID(s *Server, id string) *proto.ScheduledItem {
	for _, it := range s.schedules.list().Items {
		if it.ID == id {
			return &it
		}
	}
	return nil
}

// An agent seen idle before it has ever worked has not finished: it has
// not started.
func TestAfterAnAgentNotYetStarted(t *testing.T) {
	s, p := afterFixture(t)
	if _, perr := s.createTask(proto.TaskCreateParams{ProjectID: p.id, Prompt: "add the model", Branch: "api/model"}); perr != nil {
		t.Fatal(perr)
	}
	e := branchPane(t, s, p, "api/model")
	hook(s, e, "SessionStart")
	it := proto.ScheduledItem{At: time.Now(), After: "api/model", Task: &proto.TaskCreateParams{ProjectID: p.id, Prompt: "x"}}
	if perr := s.resolveAfter(&it); perr != nil {
		t.Fatal(perr)
	}
	// A branch that exists already, but whose agent has only just started:
	// idle, with nothing done yet, is not finished.
	if _, w := s.afterReady(&it); !strings.Contains(w, "not started working yet") {
		t.Fatalf("a new agent: waiting %q", w)
	}
	// Past the grace, an agent with no turns is one from before a reload,
	// idle because it is finished.
	if w := s.branchAgents(p.id, "api/model", "", time.Now().Add(afterStartGrace+time.Second)); w.starting || w.agents != 1 {
		t.Fatalf("an old agent: %+v", w)
	}
	// One it learned of from a task it waited for starting must be seen at
	// work, however old.
	it.AfterSeen = false
	if w := s.branchAgents(p.id, "api/model", "", time.Now().Add(afterStartGrace+time.Second)); w.busy || w.done {
		t.Fatalf("idle: %+v", w)
	}
	if _, w := s.afterReady(&it); !strings.Contains(w, "not started working yet") {
		t.Fatalf("waiting %q", w)
	}
	hook(s, e, "UserPromptSubmit")
	if _, w := s.afterReady(&it); !strings.Contains(w, "still at work") || !it.AfterSeen {
		t.Fatalf("waiting %q, seen %v", w, it.AfterSeen)
	}
	hook(s, e, "Stop")
	if base, w := s.afterReady(&it); w != "" || base != "api/model" {
		t.Fatalf("finished: base %q, waiting %q", base, w)
	}
}

// Uncommitted work would not be in a branch made from it, so the task waits
// for it to be committed — unless it named a base of its own.
func TestAfterUncommittedWork(t *testing.T) {
	s, p := afterFixture(t)
	if _, perr := s.createTask(proto.TaskCreateParams{ProjectID: p.id, Prompt: "add the model", Branch: "api/model"}); perr != nil {
		t.Fatal(perr)
	}
	e := branchPane(t, s, p, "api/model")
	hook(s, e, "UserPromptSubmit")
	hook(s, e, "Stop")
	wt, _ := p.branchWorktree("api/model")
	os.WriteFile(filepath.Join(wt.Path, "model.go"), []byte("package api\n"), 0o644)
	s.projects.refresh(p, "")
	it := proto.ScheduledItem{After: "api/model", Task: &proto.TaskCreateParams{ProjectID: p.id, Prompt: "x"}}
	if perr := s.resolveAfter(&it); perr != nil {
		t.Fatal(perr)
	}
	if _, w := s.afterReady(&it); !strings.Contains(w, "uncommitted changes") {
		t.Fatalf("waiting %q", w)
	}
	it.Task.Base = "main"
	if base, w := s.afterReady(&it); w != "" || base != "" {
		t.Fatalf("with a base of its own: base %q, waiting %q", base, w)
	}
}

// A branch that is gone: merged and cleaned away starts the task from the
// project's base; thrown away leaves it asking.
func TestAfterABranchThatIsGone(t *testing.T) {
	s, p := afterFixture(t)
	for _, b := range []string{"merged", "discarded"} {
		gitIn(t, p.root, "branch", b)
		gitIn(t, p.root, "switch", "-q", b)
		os.WriteFile(filepath.Join(p.root, b+".go"), []byte(b), 0o644)
		gitIn(t, p.root, "add", b+".go")
		gitIn(t, p.root, "commit", "-q", "-m", b)
		gitIn(t, p.root, "switch", "-q", "main")
	}
	s.projects.refresh(p, "")
	add := func(after string) proto.ScheduledItem {
		t.Helper()
		it, perr := s.scheduleAdd(proto.ScheduledItem{At: time.Now(), After: after,
			Task: &proto.TaskCreateParams{ProjectID: p.id, Prompt: "after " + after, Branch: "next-" + after}})
		if perr != nil {
			t.Fatal(perr)
		}
		return it
	}
	merged, discarded := add("merged"), add("discarded")
	gitIn(t, p.root, "merge", "-q", "--no-edit", "merged")
	gitIn(t, p.root, "branch", "-q", "-D", "merged", "discarded")
	s.projects.refresh(p, "")

	s.fireDue(time.Now())
	if it := itemByID(s, merged.ID); it != nil {
		t.Fatalf("after a merged branch, still waiting: %q", it.Waiting)
	}
	if it := itemByID(s, discarded.ID); it == nil || !strings.Contains(it.Waiting, "was it discarded") {
		t.Fatalf("after a discarded branch: %+v", it)
	}
	s.projects.refresh(p, "")
	if _, ok := p.branchWorktree("next-merged"); !ok {
		t.Fatal("the task after the merged branch did not start")
	}
	head, _ := gitx.BranchHead(context.Background(), p.root, "main")
	if !isAncestor(t, p, head, "next-merged") {
		t.Fatal("it did not start from main")
	}
}

// What it waits for leaving the schedule without starting.
func TestAfterATaskThatNeverStarts(t *testing.T) {
	s, p := afterFixture(t)
	now := time.Now()
	removed, _ := s.scheduleAdd(proto.ScheduledItem{At: now.Add(time.Hour), Task: &proto.TaskCreateParams{ProjectID: p.id, Prompt: "one"}})
	waits, perr := s.scheduleAdd(proto.ScheduledItem{At: now, After: removed.ID, Task: &proto.TaskCreateParams{ProjectID: p.id, Prompt: "two"}})
	if perr != nil {
		t.Fatal(perr)
	}
	s.scheduleRemove(removed.ID)
	s.fireDue(now)
	if it := itemByID(s, waits.ID); it == nil || !strings.Contains(it.Waiting, "no longer on the schedule") {
		t.Fatalf("after a removed task: %+v", it)
	}
	s.scheduleRemove(waits.ID)

	// One that fails to start tells those waiting for it why.
	failing, _ := s.scheduleAdd(proto.ScheduledItem{At: now, Task: &proto.TaskCreateParams{ProjectID: p.id, Prompt: "x", Branch: "bad..name"}})
	waits, _ = s.scheduleAdd(proto.ScheduledItem{At: now, After: failing.ID, Task: &proto.TaskCreateParams{ProjectID: p.id, Prompt: "y"}})
	s.fireDue(now)
	if it := itemByID(s, waits.ID); it == nil || !strings.Contains(it.LastError, "did not start") || !strings.Contains(it.Waiting, "no longer on the schedule") {
		t.Fatalf("after a failed task: %+v", it)
	}
}

// Two tasks each waiting for the other — only a hand-edited file makes one
// — wait and say so, rather than either starting.
func TestAfterALoop(t *testing.T) {
	s, p := afterFixture(t)
	now := time.Now()
	task := func(prompt string) *proto.TaskCreateParams {
		return &proto.TaskCreateParams{ProjectID: p.id, Prompt: prompt}
	}
	s.schedules.mu.Lock()
	s.schedules.items = append(s.schedules.items,
		proto.ScheduledItem{ID: "sa", At: now, After: "sb", Task: task("a")},
		proto.ScheduledItem{ID: "sb", At: now, After: "sa", Task: task("b")})
	s.schedules.mu.Unlock()
	s.fireDue(now)
	items := s.schedules.list().Items
	if len(items) != 2 {
		t.Fatalf("started: %+v", items)
	}
	for _, it := range items {
		if !strings.Contains(it.Waiting, "waits on itself") {
			t.Fatalf("%s: %q", it.ID, it.Waiting)
		}
	}
}

func TestAfterRefusals(t *testing.T) {
	s, p := afterFixture(t)
	other := filepath.Join(filepath.Dir(p.root), "web")
	a5GitRepo(t, other)
	q, err := s.projects.add(other, false)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(time.Hour)
	elsewhere, _ := s.scheduleAdd(proto.ScheduledItem{At: at, Task: &proto.TaskCreateParams{ProjectID: q.id, Prompt: "x"}})
	daily, _ := s.scheduleAdd(proto.ScheduledItem{At: at, EveryMinutes: 1440, Task: &proto.TaskCreateParams{ProjectID: p.id, Prompt: "x"}})
	shell := agentPane(t, s, "p9", "", p.root, "exec sleep 30")
	t.Cleanup(func() { shell.p.Close() })
	for after, want := range map[string]string{
		"nope":           "neither a branch",
		"s0123abcd":      "nothing on the schedule",
		elsewhere.ID:     "another project",
		daily.ID:         "repeats",
		"p9":             "not on a branch",
		"p404":           "no pane",
		"main; rm -rf /": "neither a branch",
	} {
		_, perr := s.scheduleAdd(proto.ScheduledItem{At: at, After: after, Task: &proto.TaskCreateParams{ProjectID: p.id, Prompt: "x"}})
		if perr == nil || !strings.Contains(perr.Message, want) {
			t.Errorf("after %q: %v, want %q", after, perr, want)
		}
	}
	for name, tc := range map[string]struct {
		it   proto.ScheduledItem
		want string
	}{
		"a message":  {proto.ScheduledItem{At: at, Pane: "p1", Text: "go", After: "main"}, "only a task"},
		"a repeat":   {proto.ScheduledItem{At: at, EveryMinutes: 60, After: "main", Task: &proto.TaskCreateParams{Prompt: "x"}}, "runs once"},
		"blank":      {proto.ScheduledItem{At: at, After: "  ", Task: &proto.TaskCreateParams{Prompt: "x"}}, ""},
		"after main": {proto.ScheduledItem{At: at, After: "main", Task: &proto.TaskCreateParams{Prompt: "x"}}, ""},
	} {
		perr := checkItem(&tc.it)
		switch {
		case tc.want == "" && perr != nil:
			t.Errorf("%s: refused: %v", name, perr)
		case tc.want != "" && (perr == nil || !strings.Contains(perr.Message, tc.want)):
			t.Errorf("%s: %v, want %q", name, perr, tc.want)
		}
	}
	// After the project's own base branch is fine: it waits for nothing.
	it, perr := s.scheduleAdd(proto.ScheduledItem{At: at, After: " main ", Task: &proto.TaskCreateParams{ProjectID: p.id, Prompt: "x"}})
	if perr != nil || it.After != "main" || it.AfterBranch != "main" {
		t.Fatalf("after main: %+v %v", it, perr)
	}
}

// What it has learned survives a restart.
func TestAfterSurvivesARestart(t *testing.T) {
	s, p := afterFixture(t)
	it, perr := s.scheduleAdd(proto.ScheduledItem{At: time.Now().Add(time.Hour), After: "main",
		Task: &proto.TaskCreateParams{ProjectID: p.id, Prompt: "x"}})
	if perr != nil {
		t.Fatal(perr)
	}
	got := loadSchedule(s.configDir).list().Items
	if len(got) != 1 || got[0].After != "main" || got[0].AfterBranch != "main" || got[0].AfterHead != it.AfterHead || !got[0].AfterSeen {
		t.Fatalf("reloaded %+v", got)
	}
}
