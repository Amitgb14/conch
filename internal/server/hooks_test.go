package server

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
)

// hookBench is a hooks runner over a scratch directory whose actions and
// notes the test holds.
type hookBench struct {
	h     *hooks
	dir   string
	mu    sync.Mutex
	cfg   config.Config
	notes []proto.AutopilotNote
	runs  []hookRun
}

func newHookBench(t *testing.T, acts ...config.Action) *hookBench {
	t.Helper()
	dir, err := os.MkdirTemp("", "hk")
	if err != nil {
		t.Fatal(err)
	}
	dir, _ = filepath.EvalSymlinks(dir)
	t.Cleanup(func() { os.RemoveAll(dir) })
	b := &hookBench{dir: dir, cfg: config.Config{Actions: acts}}
	b.h = loadHooks(dir, filepath.Join(dir, "s.sock"))
	b.wire()
	t.Cleanup(func() { b.h.stopAll(); b.h.wait() })
	return b
}

func (b *hookBench) wire() {
	b.h.load = func() (config.Config, error) {
		b.mu.Lock()
		defer b.mu.Unlock()
		return b.cfg, nil
	}
	b.h.note = func(n proto.AutopilotNote) {
		b.mu.Lock()
		defer b.mu.Unlock()
		b.notes = append(b.notes, n)
	}
	b.h.ran = func(r hookRun) {
		b.mu.Lock()
		defer b.mu.Unlock()
		b.runs = append(b.runs, r)
	}
}

func (b *hookBench) setActions(acts ...config.Action) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.cfg = config.Config{Actions: acts}
}

func (b *hookBench) ran() []hookRun {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]hookRun(nil), b.runs...)
}

func (b *hookBench) noted() []proto.AutopilotNote {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]proto.AutopilotNote(nil), b.notes...)
}

func act(name, run string, on ...string) config.Action {
	return config.Action{Name: name, Run: run, On: on}
}

func readLog(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// A hook runs once per event, in the event's worktree, with the event in
// its environment, and its output is kept in a log; a failure-free run
// says nothing in the activity.
func TestHookRunsWithTheEvent(t *testing.T) {
	t.Setenv("CONCH_PANE_ID", "p-server")
	wt := t.TempDir()
	b := newHookBench(t,
		act("Show", `pwd; echo "ev=$CONCH_EVENT br=$CONCH_BRANCH pane=$CONCH_PANE pid=[$CONCH_PANE_ID] pr=$CONCH_PR act=$CONCH_ACTION sock=$CONCH_SOCKET"`, "agent.done"),
		act("Other", "echo never", "pr.merged"),
		act("Menu", "echo menu"))
	b.h.fire(hookEvent{Name: config.EventAgentDone, Branch: "feat", Worktree: wt, Pane: "p7", PR: 12})
	b.h.wait()
	runs := b.ran()
	if len(runs) != 1 || runs[0].Action != "Show" || runs[0].Err != nil {
		t.Fatalf("runs: %+v", runs)
	}
	got := readLog(t, runs[0].Log)
	real, _ := filepath.EvalSymlinks(wt)
	for _, want := range []string{real, "ev=agent.done br=feat pane=p7 pid=[] pr=12 act=Show", "sock=" + filepath.Join(b.dir, "s.sock"), "conch: finished"} {
		if !strings.Contains(got, want) {
			t.Errorf("log lacks %q:\n%s", want, got)
		}
	}
	if n := b.noted(); len(n) != 0 {
		t.Errorf("a hook that worked was noted: %+v", n)
	}
	// An event nothing runs on runs nothing.
	b.h.fire(hookEvent{Name: config.EventTaskFailed})
	b.h.wait()
	if len(b.ran()) != 1 {
		t.Errorf("task.failed ran %+v", b.ran())
	}
}

func TestHookFailureIsNoted(t *testing.T) {
	b := newHookBench(t, act("Broken", "echo oops >&2; exit 3", "task.failed"))
	b.h.fire(hookEvent{Name: config.EventTaskFailed, ProjectID: "proj", Branch: "b", Error: "no agent"})
	b.h.wait()
	runs := b.ran()
	if len(runs) != 1 || runs[0].Exit != 3 {
		t.Fatalf("runs: %+v", runs)
	}
	if !strings.Contains(readLog(t, runs[0].Log), "oops") {
		t.Error("stderr not in the log")
	}
	n := b.noted()
	if len(n) != 1 || !n[0].Error || n[0].Kind != proto.ActivityHook || n[0].ProjectID != "proj" || n[0].Branch != "b" ||
		!strings.Contains(n[0].Text, "Broken on task.failed failed (exit 3)") || !strings.Contains(n[0].Text, runs[0].Log) {
		t.Errorf("notes: %+v", n)
	}
}

// A hook that hangs is stopped at its timeout — with what it started, so
// a background sleep holding the output open does not keep it — and the
// stop is said.
func TestHookTimeout(t *testing.T) {
	a := act("Hang", "sleep 60 & sleep 60; echo never-reached", "agent.waiting")
	a.Timeout = 1
	b := newHookBench(t, a)
	start := time.Now()
	b.h.fire(hookEvent{Name: config.EventAgentWaiting})
	b.h.wait()
	if took := time.Since(start); took > 10*time.Second {
		t.Fatalf("a 1s timeout took %s", took)
	}
	runs := b.ran()
	if len(runs) != 1 || !runs[0].TimedOut {
		t.Fatalf("runs: %+v", runs)
	}
	if got := readLog(t, runs[0].Log); !strings.Contains(got, "its timeout") || strings.Contains(got, "never-reached") {
		t.Errorf("log: %s", got)
	}
	if n := b.noted(); len(n) != 1 || !strings.Contains(n[0].Text, "took longer than 1s") {
		t.Errorf("notes: %+v", n)
	}
}

// Megabytes of output keep a megabyte, and say how much was cut.
func TestHookOutputIsCapped(t *testing.T) {
	b := newHookBench(t, act("Loud", "head -c 5000000 /dev/zero | tr '\\0' x; echo; echo tail-end", "pr.opened"))
	b.h.fire(hookEvent{Name: config.EventPROpened})
	b.h.wait()
	runs := b.ran()
	if len(runs) != 1 || runs[0].Err != nil || runs[0].Cut == 0 {
		t.Fatalf("runs: %+v", runs)
	}
	st, err := os.Stat(runs[0].Log)
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() > hookLogMax+4096 {
		t.Errorf("log is %d bytes", st.Size())
	}
	if got := readLog(t, runs[0].Log); !strings.Contains(got, "output cut at") || strings.Contains(got, "tail-end") {
		t.Errorf("log end: %q", got[len(got)-200:])
	}
}

// Two events at once run both hooks, side by side, each with its own log.
func TestHookTwoEventsAtOnce(t *testing.T) {
	b := newHookBench(t, act("Slow", `sleep 1; echo "$CONCH_BRANCH"`, "agent.done"))
	start := time.Now()
	var wg sync.WaitGroup
	for _, br := range []string{"one", "two"} {
		wg.Add(1)
		go func() { defer wg.Done(); b.h.fire(hookEvent{Name: config.EventAgentDone, Branch: br}) }()
	}
	wg.Wait()
	b.h.wait()
	runs := b.ran()
	if len(runs) != 2 || runs[0].Log == runs[1].Log {
		t.Fatalf("runs: %+v", runs)
	}
	if took := time.Since(start); took > 1900*time.Millisecond {
		t.Errorf("two 1s hooks took %s: one waited for the other", took)
	}
	both := readLog(t, runs[0].Log) + readLog(t, runs[1].Log)
	if !strings.Contains(both, "one") || !strings.Contains(both, "two") {
		t.Errorf("logs: %s", both)
	}
}

// A hook taken out of config.toml while it runs finishes as it was, and
// does not run on the next event.
func TestHookRemovedWhileRunning(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "started")
	b := newHookBench(t, act("Long", "touch "+marker+"; sleep 1; echo done-ok", "worktree.created"))
	b.h.fire(hookEvent{Name: config.EventWorktreeCreated})
	a5WaitFor(t, "the hook to start", func() bool { _, err := os.Stat(marker); return err == nil })
	b.setActions()
	b.h.wait()
	runs := b.ran()
	if len(runs) != 1 || runs[0].Err != nil || !strings.Contains(readLog(t, runs[0].Log), "done-ok") {
		t.Fatalf("runs: %+v", runs)
	}
	b.h.fire(hookEvent{Name: config.EventWorktreeCreated})
	b.h.wait()
	if len(b.ran()) != 1 {
		t.Errorf("a removed hook ran again: %+v", b.ran())
	}
}

// A misspelt event in on runs nothing (and config's ActionProblems says
// so), while the rest of the action's events still run it.
func TestHookUnknownEventRunsNothing(t *testing.T) {
	b := newHookBench(t, act("Typo", "echo x", "agent.finished", "pr.merged"))
	b.h.fire(hookEvent{Name: "agent.finished"})
	b.h.wait()
	if len(b.ran()) != 0 {
		t.Fatalf("ran on an event that does not exist: %+v", b.ran())
	}
	b.h.fire(hookEvent{Name: config.EventPRMerged})
	b.h.wait()
	if len(b.ran()) != 1 {
		t.Fatalf("runs: %+v", b.ran())
	}
}

// Stopping — the server ending or reloading — stops runs under way
// without calling them failures, and starts no more.
func TestHookStopAll(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "started")
	b := newHookBench(t, act("Long", "touch "+marker+"; sleep 60", "agent.done"))
	b.h.fire(hookEvent{Name: config.EventAgentDone})
	a5WaitFor(t, "the hook to start", func() bool { _, err := os.Stat(marker); return err == nil })
	b.h.stopAll()
	b.h.wait()
	runs := b.ran()
	if len(runs) != 1 || !runs[0].Stopped {
		t.Fatalf("runs: %+v", runs)
	}
	if !strings.Contains(readLog(t, runs[0].Log), "the server stopped or reloaded") {
		t.Error("log does not say why it stopped")
	}
	if n := b.noted(); len(n) != 0 {
		t.Errorf("a stop was noted as a failure: %+v", n)
	}
	b.h.fire(hookEvent{Name: config.EventAgentDone})
	b.h.wait()
	if len(b.ran()) != 1 {
		t.Errorf("ran after stopping: %+v", b.ran())
	}
}

// A hook whose directory and command fail to start is said, not lost.
func TestHookCannotStart(t *testing.T) {
	b := newHookBench(t, act("X", "true", "agent.done"))
	// Logs cannot be written where a file stands in for their folder.
	os.MkdirAll(filepath.Join(b.dir, "hooks"), 0o700)
	os.WriteFile(filepath.Join(b.dir, hookLogDir), []byte("x"), 0o600)
	b.h.fire(hookEvent{Name: config.EventAgentDone})
	b.h.wait()
	if n := b.noted(); len(n) != 1 || !strings.Contains(n[0].Text, "X on agent.done failed") {
		t.Errorf("notes: %+v", n)
	}
}

func TestHookLogsArePruned(t *testing.T) {
	b := newHookBench(t)
	dir := filepath.Join(b.dir, hookLogDir)
	os.MkdirAll(dir, 0o700)
	for i := 0; i < hookLogsKept+5; i++ {
		os.WriteFile(filepath.Join(dir, fmt.Sprintf("20260101-000000-%04d-x.log", i)), nil, 0o600)
	}
	b.h.prune(dir)
	ents, _ := os.ReadDir(dir)
	if len(ents) != hookLogsKept {
		t.Fatalf("%d logs kept", len(ents))
	}
	if ents[0].Name() != "20260101-000000-0005-x.log" {
		t.Errorf("oldest kept: %s", ents[0].Name())
	}
}

func TestSlug(t *testing.T) {
	for in, want := range map[string]string{"Run tests": "run-tests", "": "hook", "!!!": "hook", "Post → Slack!": "post-slack",
		strings.Repeat("a", 100): strings.Repeat("a", 40)} {
		if got := slug(in); got != want {
			t.Errorf("slug(%q) = %q, want %q", in, got, want)
		}
	}
}

// Pull requests: none is news on the first read; one opened is, once; one
// merged that was seen open is, once — and a server started again on the
// same file sees nothing new in the same list.
func TestHookPRsOnce(t *testing.T) {
	b := newHookBench(t)
	names := func(evs []hookEvent) string {
		var out []string
		for _, e := range evs {
			out = append(out, e.Name+":"+e.Branch)
		}
		return strings.Join(out, ",")
	}
	first := map[string]proto.PRInfo{"old": {Number: 1, State: "OPEN"}, "gone": {Number: 2, State: "MERGED"}}
	if got := b.h.observePRs("p", first); len(got) != 0 {
		t.Fatalf("first read: %s", names(got))
	}
	if got := b.h.observePRs("p", first); len(got) != 0 {
		t.Fatalf("same read: %s", names(got))
	}
	second := map[string]proto.PRInfo{"old": {Number: 1, State: "MERGED", URL: "u1"}, "new": {Number: 3, State: "OPEN", Title: "T"}}
	got := b.h.observePRs("p", second)
	if names(got) != "pr.opened:new,pr.merged:old" && names(got) != "pr.merged:old,pr.opened:new" {
		t.Fatalf("second read: %s", names(got))
	}
	for _, e := range got {
		if e.Branch == "old" && (e.PR != 1 || e.PRURL != "u1") || e.Branch == "new" && (e.PR != 3 || e.PRTitle != "T") {
			t.Errorf("event: %+v", e)
		}
	}
	// A reload: a new runner from the same file.
	again := loadHooks(b.dir, "")
	if got := again.observePRs("p", second); len(got) != 0 {
		t.Fatalf("after a reload the same list fired %s", names(got))
	}
	// Merged without having been seen open (opened and merged between
	// two reads, or a branch new here): not said, since it cannot be told
	// from a merge long ago.
	third := map[string]proto.PRInfo{"new": {Number: 3, State: "OPEN"}, "quick": {Number: 9, State: "MERGED"}}
	if got := again.observePRs("p", third); len(got) != 0 {
		t.Fatalf("third: %s", names(got))
	}
	// A new pull request on a branch whose last one closed is opened.
	fourth := map[string]proto.PRInfo{"new": {Number: 4, State: "OPEN"}}
	if got := again.observePRs("p", fourth); names(got) != "pr.opened:new" {
		t.Fatalf("fourth: %s", names(got))
	}
	// Another project is read on its own.
	if got := again.observePRs("q", fourth); len(got) != 0 {
		t.Fatalf("another project's first read: %s", names(got))
	}
}

func TestHookStateFileDamaged(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, hookStateFile), []byte("{not json"), 0o600)
	h := loadHooks(dir, "")
	if got := h.observePRs("p", map[string]proto.PRInfo{"a": {Number: 1, State: "OPEN"}}); len(got) != 0 {
		t.Errorf("a damaged file made a first read fire: %+v", got)
	}
}

// The server's side: an agent's state change fires agent.done once, or
// agent.waiting, and a pane adopted after a reload (no state before) none.
func TestHookAgentStates(t *testing.T) {
	a5IsolateEnv(t)
	s, _ := a5Server(t)
	b := &hookBench{dir: s.configDir}
	b.h = s.hooks
	b.wire()
	b.setActions(act("Done", "true", "agent.done"), act("Wait", "true", "agent.waiting"))
	pane := func(state string) proto.PaneInfo {
		return proto.PaneInfo{ID: "p1", Branch: "b", Agent: &proto.AgentStatus{Name: "claude", State: state}}
	}
	s.hookAgent(pane(proto.AgentDone), "")
	s.hookAgent(pane(proto.AgentDone), proto.AgentDone)
	s.hooks.wait()
	if len(b.ran()) != 0 {
		t.Fatalf("no change ran %+v", b.ran())
	}
	s.hookAgent(pane(proto.AgentDone), proto.AgentWorking)
	s.hooks.wait()
	s.hookAgent(pane(proto.AgentBlocked), proto.AgentWorking)
	s.hooks.wait()
	s.hookAgent(pane(proto.AgentWorking), proto.AgentBlocked)
	s.hooks.wait()
	runs := b.ran()
	if len(runs) != 2 || runs[0].Action != "Done" || runs[1].Action != "Wait" {
		t.Fatalf("runs: %+v", runs)
	}
}

// worktree.created fires once for a worktree the server makes, with its
// path and branch; and a task that cannot start fires task.failed.
func TestHookWorktreeAndTaskThroughServer(t *testing.T) {
	a5IsolateEnv(t)
	s, dir := a5Server(t)
	repo := filesRepo(t, s, dir)
	out := filepath.Join(dir, "seen")
	b := &hookBench{dir: s.configDir}
	b.h = s.hooks
	b.wire()
	b.setActions(
		act("Setup", `echo "$CONCH_EVENT $CONCH_BRANCH $CONCH_WORKTREE $CONCH_ROOT" >> `+out, "worktree.created"),
		act("Failed", `echo "$CONCH_EVENT [$CONCH_ERROR]" >> `+out, "task.failed"))
	id := s.projectOfCheckout(repo)
	raw, perr := s.dispatch(&client{}, proto.Message{Method: proto.MethodWorktreeAdd, Params: proto.Marshal(proto.WorktreeAddParams{ProjectID: id, Branch: "feat"})})
	if perr != nil {
		t.Fatal(perr)
	}
	wt := raw.(proto.WorktreeResult).Path
	s.hooks.wait()
	_, _, perr = s.startTask(proto.TaskCreateParams{ProjectID: id})
	if perr == nil {
		t.Fatal("a task with no prompt started")
	}
	s.hooks.wait()
	got, _ := os.ReadFile(out)
	want := "worktree.created feat " + wt + " " + repo + "\ntask.failed [a task needs a prompt]\n"
	if string(got) != want {
		t.Errorf("hooks saw:\n%s\nwant:\n%s", got, want)
	}
}

// What the server runs is config.toml's actions and the hook files
// installed from a registry, after them.
func TestHookActionsIncludeInstalledFiles(t *testing.T) {
	a5IsolateEnv(t)
	home := os.Getenv("CONCH_HOME")
	os.WriteFile(filepath.Join(home, "config.toml"), []byte("[[actions]]\nname = \"Mine\"\nrun = \"true\"\non = [\"agent.done\"]\n"), 0o600)
	os.MkdirAll(filepath.Join(home, "integrations"), 0o700)
	os.WriteFile(filepath.Join(home, "integrations", "shared.toml"), []byte(`format = 3
kind = "hook"
name = "shared"
label = "Shared"
[hook]
run = "echo shared"
on = ["agent.done"]
`), 0o600)
	cfg, err := hookActions()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, a := range cfg.ActionsFor(config.EventAgentDone) {
		got = append(got, a.Name)
	}
	if strings.Join(got, ",") != "Mine,Shared" {
		t.Errorf("agent.done runs %v", got)
	}
}

// A read that changes nothing writes nothing: pull requests are read
// every few seconds.
func TestHookPRsWriteOnlyOnChange(t *testing.T) {
	b := newHookBench(t)
	prs := map[string]proto.PRInfo{"a": {Number: 1, State: "OPEN"}}
	b.h.observePRs("p", prs)
	path := filepath.Join(b.dir, hookStateFile)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	b.h.observePRs("p", prs)
	if _, err := os.Stat(path); err == nil {
		t.Error("the same read wrote hooks.json again")
	}
	b.h.observePRs("p", map[string]proto.PRInfo{"a": {Number: 1, State: "MERGED"}})
	if _, err := os.Stat(path); err != nil {
		t.Errorf("a change was not written: %v", err)
	}
}
