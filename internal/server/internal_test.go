package server

import (
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"

	"github.com/Amitgb14/conch/internal/buildinfo"
	"github.com/Amitgb14/conch/internal/detect"
	"github.com/Amitgb14/conch/internal/pane"
	"github.com/Amitgb14/conch/internal/proto"
)

// a5IsolateEnv keeps the test away from the user's home, config and any conch
// server the test itself may run inside.
func a5IsolateEnv(t *testing.T) string {
	t.Helper()
	home, _ := filepath.EvalSymlinks(t.TempDir())
	t.Setenv("HOME", home)
	short, err := os.MkdirTemp("", "a5h")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(short) })
	t.Setenv("CONCH_HOME", short)
	for _, k := range []string{"CONCH_SOCKET", "CONCH_PANE_ID", "CLAUDE_CONFIG_DIR", "CODEX_HOME", "XDG_DATA_HOME", "XDG_CONFIG_HOME", "ZDOTDIR", reloadStateEnv} {
		t.Setenv(k, "")
	}
	t.Setenv("SHELL", "/bin/sh")
	return home
}

// a5Server is a server that is not listening: requests are made by calling
// its methods directly.
func a5Server(t *testing.T) (*Server, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "a5")
	if err != nil {
		t.Fatal(err)
	}
	dir, _ = filepath.EvalSymlinks(dir)
	s := New(filepath.Join(dir, "s.sock"), dir)
	s.manifests, _ = detect.LoadManifests("")
	t.Cleanup(func() {
		s.Stop()
		s.mu.Lock()
		entries := make([]*entry, 0, len(s.panes))
		for _, e := range s.panes {
			entries = append(entries, e)
		}
		s.mu.Unlock()
		for _, e := range entries {
			e.p.Close()
		}
		os.RemoveAll(dir)
	})
	return s, dir
}

// a5Events registers a client that records every event broadcast to it.
type a5Events struct {
	mu   sync.Mutex
	msgs []proto.Message
}

func a5Listen(t *testing.T, s *Server) *a5Events {
	t.Helper()
	a, b := net.Pipe()
	c := &client{conn: proto.NewConn(a), subs: map[string]*subscription{}}
	s.mu.Lock()
	s.clients[c] = struct{}{}
	s.mu.Unlock()
	ev := &a5Events{}
	go func() {
		rc := proto.NewConn(b)
		for {
			m, err := rc.Read()
			if err != nil {
				return
			}
			ev.mu.Lock()
			ev.msgs = append(ev.msgs, m)
			ev.mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		s.mu.Lock()
		delete(s.clients, c)
		s.mu.Unlock()
		a.Close()
		b.Close()
	})
	return ev
}

func (ev *a5Events) count(event string) int {
	ev.mu.Lock()
	defer ev.mu.Unlock()
	n := 0
	for _, m := range ev.msgs {
		if m.Event == event {
			n++
		}
	}
	return n
}

func a5WaitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func a5GitRepo(t *testing.T, repo string) {
	t.Helper()
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.name", "t"},
		{"config", "user.email", "t@example.com"},
		{"commit", "-q", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

func a5Entry(t *testing.T, s *Server, id string) *entry {
	t.Helper()
	e, perr := s.get(id)
	if perr != nil {
		t.Fatal(perr)
	}
	return e
}

// ---- run log ----

func TestA5RunLogLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent-runs.json")
	if l := loadRunLog(dir, false); len(l.interruptedRuns()) != 0 || len(l.active) != 0 {
		t.Fatalf("missing file: %+v", l)
	}
	os.WriteFile(path, []byte("{broken"), 0o600)
	if l := loadRunLog(dir, false); len(l.interruptedRuns()) != 0 {
		t.Fatal("corrupt file should give an empty log")
	}
	if b, _ := os.ReadFile(path); string(b) != "{broken" {
		t.Fatal("a corrupt log must not be overwritten")
	}

	now := time.Now()
	write := func() {
		b, _ := json.Marshal(runLogFile{
			Active: []agentRun{{Pane: "p1", Agent: "claude", Dir: "/a", Session: "s1", Seen: now}},
			Interrupted: []agentRun{
				{Pane: "p2", Agent: "codex", Dir: "/b", Seen: now.Add(-8 * 24 * time.Hour)},
				{Pane: "p3", Agent: "codex", Dir: "/c", Seen: now.Add(-time.Hour)},
			},
		})
		os.WriteFile(path, b, 0o600)
	}
	write()
	l := loadRunLog(dir, false)
	runs := l.interruptedRuns()
	if len(runs) != 2 || runs[0].Pane != "p3" || runs[1].Pane != "p1" || len(l.active) != 0 {
		t.Fatalf("active runs become interrupted, stale ones are dropped: %+v", runs)
	}
	var f runLogFile
	b, _ := os.ReadFile(path)
	if json.Unmarshal(b, &f) != nil || len(f.Active) != 0 || len(f.Interrupted) != 2 {
		t.Fatalf("saved: %s", b)
	}

	write()
	l = loadRunLog(dir, true) // a reload keeps its panes running
	if runs := l.interruptedRuns(); len(runs) != 1 || runs[0].Pane != "p3" || l.active["p1"].Session != "s1" {
		t.Fatalf("reload: interrupted %+v active %+v", runs, l.active)
	}
	// The copy is independent of the log.
	runs = l.interruptedRuns()
	runs[0].Pane = "changed"
	if l.interruptedRuns()[0].Pane != "p3" {
		t.Fatal("interruptedRuns shares memory with the log")
	}
}

func TestA5RunLogNoteForgetResolve(t *testing.T) {
	dir := t.TempDir()
	l := loadRunLog(dir, false)
	read := func() runLogFile {
		var f runLogFile
		b, _ := os.ReadFile(l.path)
		json.Unmarshal(b, &f)
		return f
	}
	created := time.Now().Add(-time.Minute)

	// No agent: nothing recorded.
	l.note(proto.PaneInfo{ID: "p1", State: proto.PaneRunning}, "/d", created)
	if len(l.active) != 0 {
		t.Fatal("a pane without an agent was noted")
	}
	info := proto.PaneInfo{ID: "p1", State: proto.PaneRunning, Agent: &proto.AgentStatus{Name: "codex"}}
	l.note(info, "/d", created)
	if f := read(); len(f.Active) != 1 || f.Active[0].Agent != "codex" || f.Active[0].Dir != "/d" || !f.Active[0].Started.Equal(created) {
		t.Fatalf("noted: %+v", f)
	}
	info.Agent = &proto.AgentStatus{Name: "codex", SessionID: "x"}
	l.note(info, "/d", created)
	if l.active["p1"].Session != "x" {
		t.Fatalf("session not recorded: %+v", l.active["p1"])
	}
	info.Agent = &proto.AgentStatus{Name: "codex"}
	l.note(info, "/d", created)
	if l.active["p1"].Session != "x" {
		t.Fatal("a later sample without a session must keep the known one")
	}
	seen := l.active["p1"].Seen
	l.note(info, "/d", created)
	if !l.active["p1"].Seen.Equal(seen) {
		t.Fatal("an unchanged run within a minute should not be rewritten")
	}
	info.Agent = &proto.AgentStatus{Name: "claude"}
	l.note(info, "/d", created)
	if r := l.active["p1"]; r.Agent != "claude" || r.Session != "" {
		t.Fatalf("a different agent must not inherit the session: %+v", r)
	}
	info.State = proto.PaneExited
	l.note(info, "/d", created)
	if len(l.active) != 0 || len(read().Active) != 0 {
		t.Fatal("an exited pane stays active")
	}

	info.State = proto.PaneRunning
	l.note(info, "/d", created)
	l.forget("nope")
	if len(l.active) != 1 {
		t.Fatal("forgetting an unknown pane dropped a run")
	}
	l.forget("p1")
	if len(l.active) != 0 || len(read().Active) != 0 {
		t.Fatal("forget kept the run")
	}

	l.interrupted = []agentRun{
		{Agent: "claude", Dir: "/a", Session: "s1"},
		{Agent: "claude", Dir: "/a", Session: "s2"},
		{Agent: "codex", Dir: "/c"},
		{Agent: "codex", Dir: "/other"},
	}
	l.resolve("claude", "s1", "/a")
	if runs := l.interruptedRuns(); len(runs) != 3 || runs[0].Session != "s2" {
		t.Fatalf("resolve by session: %+v", runs)
	}
	l.resolve("codex", "whatever", "/c") // a run without a session matches any id
	if runs := l.interruptedRuns(); len(runs) != 2 {
		t.Fatalf("resolve sessionless run: %+v", runs)
	}
	l.resolve("claude", "", "/a") // no id: every run of the agent there
	if runs := l.interruptedRuns(); len(runs) != 1 || runs[0].Dir != "/other" {
		t.Fatalf("resolve all: %+v", runs)
	}
	if f := read(); len(f.Interrupted) != 1 {
		t.Fatalf("resolve not saved: %+v", f)
	}

	// Saving where the log can't be written is not fatal.
	bad := &runLog{path: filepath.Join(dir, "missing", "runs.json"), active: map[string]agentRun{}}
	bad.saveLocked()
	if _, err := os.Stat(bad.path); err == nil {
		t.Fatal("wrote into a missing directory")
	}
}

func TestA5SortSessions(t *testing.T) {
	now := time.Now()
	out := []proto.SessionInfo{
		{ID: "old", Updated: now.Add(-3 * time.Hour)},
		{ID: "int-old", Updated: now.Add(-5 * time.Hour), Interrupted: true},
		{ID: "new", Updated: now},
		{ID: "int-new", Updated: now.Add(-time.Hour), Interrupted: true},
	}
	sortSessions(out)
	var ids []string
	for _, s := range out {
		ids = append(ids, s.ID)
	}
	if got := strings.Join(ids, ","); got != "int-new,int-old,new,old" {
		t.Fatalf("order %s", got)
	}
	sortSessions(nil)
	if !withinAny("/a/b", []string{"/x", "/a"}) || withinAny("/ab", []string{"/a"}) || withinAny("/a", nil) {
		t.Fatal("withinAny")
	}
}

func TestA5TrashDir(t *testing.T) {
	home := a5IsolateEnv(t)
	cfg := t.TempDir()
	if got := trashDir(cfg); got != filepath.Join(cfg, "trash") {
		t.Fatalf("without ~/.Trash: %s", got)
	}
	os.MkdirAll(filepath.Join(home, ".Trash"), 0o700)
	want := filepath.Join(cfg, "trash")
	if runtime.GOOS == "darwin" {
		want = filepath.Join(home, ".Trash")
	}
	if got := trashDir(cfg); got != want {
		t.Fatalf("trash %s, want %s", got, want)
	}
}

// ---- limits ----

func TestA5SetLimits(t *testing.T) {
	a5IsolateEnv(t)
	s, _ := a5Server(t)
	ev := a5Listen(t, s)

	s.setLimits(proto.PlanLimits{Agent: "claude"}) // nothing to show
	if len(s.allLimits().Limits) != 0 {
		t.Fatal("empty limits recorded")
	}
	resets := time.Now().Add(time.Hour).Truncate(time.Second)
	s.setLimits(proto.PlanLimits{Agent: "claude", FiveHour: &proto.LimitWindow{UsedPct: 10.2, ResetsAt: resets}})
	l := s.allLimits().Limits
	if len(l) != 1 || l[0].At.IsZero() {
		t.Fatalf("limits: %+v", l)
	}
	a5WaitFor(t, "first limits event", func() bool { return ev.count(proto.EventAgentLimits) == 1 })
	// The same whole percentage and reset: recorded quietly.
	s.setLimits(proto.PlanLimits{Agent: "claude", FiveHour: &proto.LimitWindow{UsedPct: 10.7, ResetsAt: resets}})
	if got := s.allLimits().Limits[0].FiveHour.UsedPct; got != 10.7 {
		t.Fatalf("latest value not kept: %v", got)
	}
	at := time.Now().Add(-time.Minute)
	s.setLimits(proto.PlanLimits{Agent: "claude", At: at, FiveHour: &proto.LimitWindow{UsedPct: 11, ResetsAt: resets}})
	s.setLimits(proto.PlanLimits{Agent: "claude", At: at, FiveHour: &proto.LimitWindow{UsedPct: 11, ResetsAt: resets}, Week: &proto.LimitWindow{UsedPct: 3}})
	s.setLimits(proto.PlanLimits{Agent: "codex", Spend: &proto.LimitWindow{UsedPct: 50}})
	a5WaitFor(t, "limits events", func() bool { return ev.count(proto.EventAgentLimits) == 4 })
	time.Sleep(50 * time.Millisecond)
	if n := ev.count(proto.EventAgentLimits); n != 4 {
		t.Fatalf("%d limits events, want 4", n)
	}
	if got := s.allLimits().Limits; len(got) != 2 {
		t.Fatalf("limits by agent: %+v", got)
	}
	for _, l := range s.allLimits().Limits {
		if l.Agent == "claude" && !l.At.Equal(at) {
			t.Fatalf("given time replaced: %v", l.At)
		}
	}
}

func TestA5SameWindow(t *testing.T) {
	now := time.Now()
	w := func(pct float64, at time.Time) *proto.LimitWindow {
		return &proto.LimitWindow{UsedPct: pct, ResetsAt: at}
	}
	for _, tc := range []struct {
		a, b *proto.LimitWindow
		want bool
	}{
		{nil, nil, true},
		{nil, w(1, now), false},
		{w(1, now), nil, false},
		{w(1.1, now), w(1.9, now), true},
		{w(1, now), w(2, now), false},
		{w(1, now), w(1, now.Add(time.Second)), false},
	} {
		if got := sameWindow(tc.a, tc.b); got != tc.want {
			t.Errorf("sameWindow(%+v, %+v) = %v", tc.a, tc.b, got)
		}
	}
}

// ---- reload ----

func TestA5TakeReloadState(t *testing.T) {
	a5IsolateEnv(t)
	if st, err := takeReloadState(); st != nil || err != nil {
		t.Fatalf("no reload: %v %v", st, err)
	}
	dir := t.TempDir()
	t.Setenv(reloadStateEnv, filepath.Join(dir, "missing.json"))
	if _, err := takeReloadState(); err == nil {
		t.Fatal("missing state file accepted")
	}
	if os.Getenv(reloadStateEnv) != "" {
		t.Fatal("state variable left for panes to inherit")
	}

	bad := filepath.Join(dir, "bad.json")
	os.WriteFile(bad, []byte("not json"), 0o600)
	t.Setenv(reloadStateEnv, bad)
	if _, err := takeReloadState(); err == nil {
		t.Fatal("bad state accepted")
	}

	want := reloadState{FromBuild: "abc", ListenerFD: 7, NextID: 12,
		Panes: []reloadPane{{Snapshot: pane.Snapshot{ID: "p3", Command: []string{"sh"}, Replay: "hi"}, FD: 9, Dir: "/x", Loose: true,
			Tracker: detect.TrackerState{Hint: "claude", HookState: "working"}, Transcript: "/t.jsonl"}},
		Limits:  []proto.PlanLimits{{Agent: "codex", Week: &proto.LimitWindow{UsedPct: 5}}},
		Started: time.Date(2026, 9, 1, 8, 30, 0, 0, time.UTC),
	}
	b, _ := json.Marshal(want)
	good := filepath.Join(dir, "good.json")
	os.WriteFile(good, b, 0o600)
	t.Setenv(reloadStateEnv, good)
	st, err := takeReloadState()
	if err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(st)
	if string(got) != string(b) {
		t.Fatalf("round trip:\n%s\n%s", got, b)
	}
	if _, err := os.Stat(good); !os.IsNotExist(err) {
		t.Fatal("state file not removed")
	}
}

func a5VersionScript(t *testing.T, out string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "conch")
	if err := os.WriteFile(p, []byte("#!/bin/sh\nprintf '%s' '"+out+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestA5ReloadBinary(t *testing.T) {
	a5IsolateEnv(t)
	if _, perr := reloadBinary(filepath.Join(t.TempDir(), "nope")); perr == nil || !strings.Contains(perr.Message, "does not run") {
		t.Fatalf("missing binary: %v", perr)
	}
	if _, perr := reloadBinary(a5VersionScript(t, "garbage")); perr == nil || !strings.Contains(perr.Message, "not a conch binary") {
		t.Fatalf("garbage: %v", perr)
	}
	if b := buildinfo.Build(); b != "" {
		same := a5VersionScript(t, `{"build":"`+b+`","capabilities":["server.reload.v1"]}`)
		if _, perr := reloadBinary(same); perr == nil || !strings.Contains(perr.Message, "already runs build") {
			t.Fatalf("same build: %v", perr)
		}
	}
	old := a5VersionScript(t, `{"build":"000000000000","capabilities":["pane.v1"]}`)
	if _, perr := reloadBinary(old); perr == nil || !strings.Contains(perr.Message, "can't take over") {
		t.Fatalf("no reload capability: %v", perr)
	}
	ok := a5VersionScript(t, `{"build":"000000000000","capabilities":["pane.v1","server.reload.v1"]}`)
	if got, perr := reloadBinary(ok); perr != nil || got != ok {
		t.Fatalf("reloadable: %q %v", got, perr)
	}
}

func TestA5ReloadExecFailureCarriesOn(t *testing.T) {
	a5IsolateEnv(t)
	s, dir := a5Server(t)

	// Without a unix listener there is nothing to hand over.
	s.reload("/nonexistent/conch")

	ln, err := net.Listen("unix", filepath.Join(dir, "r.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	s.mu.Lock()
	s.ln = ln
	s.mu.Unlock()

	// An exited pane is not carried over.
	info, perr := s.create(proto.PaneCreateParams{Command: []string{"/bin/sh", "-c", "exit 0"}, Cwd: dir, NoProject: true})
	if perr != nil {
		t.Fatal(perr)
	}
	e := a5Entry(t, s, info.ID)
	<-e.p.Done()

	s.reload(filepath.Join(dir, "no-such-conch")) // exec fails; the server carries on
	if _, err := os.Stat(filepath.Join(dir, "reload-state.json")); !os.IsNotExist(err) {
		t.Fatalf("state file left behind: %v", err)
	}
	// The listener still works.
	go func() {
		if c, err := ln.Accept(); err == nil {
			c.Close()
		}
	}()
	c, err := net.Dial("unix", filepath.Join(dir, "r.sock"))
	if err != nil {
		t.Fatalf("listener broken after failed reload: %v", err)
	}
	c.Close()

	// Unwritable config dir: the state can't be saved.
	s.configDir = filepath.Join(dir, "missing", "deeper")
	s.reload(filepath.Join(dir, "no-such-conch"))
}

// a5PtyProgram starts a program on a terminal the way a previous server
// process would have left it for adoption.
// The descriptor is a raw one the adopter takes ownership of.
func a5PtyProgram(t *testing.T, script string) (fd, pid int) {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", script)
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: 40, Rows: 6})
	if err != nil {
		t.Fatal(err)
	}
	defer ptmx.Close()
	if fd, err = unix.Dup(int(ptmx.Fd())); err != nil {
		t.Fatal(err)
	}
	return fd, cmd.Process.Pid
}

func TestA5Adopt(t *testing.T) {
	home := a5IsolateEnv(t)
	s, dir := a5Server(t)
	ev := a5Listen(t, s)
	repo := filepath.Join(dir, "repo")
	a5GitRepo(t, repo)

	transcript := filepath.Join(home, "t.jsonl")
	os.WriteFile(transcript, []byte(`{"type":"assistant","message":{"id":"m1","model":"opus","usage":{"input_tokens":5,"output_tokens":7}}}`+"\n"), 0o600)

	fd1, pid1 := a5PtyProgram(t, "printf 'adopt''ed-one\\n'; exec sleep 30")
	fd2, pid2 := a5PtyProgram(t, "exec sleep 30")
	st := &reloadState{
		FromBuild: "old", NextID: 7,
		Limits: []proto.PlanLimits{{Agent: "codex", Week: &proto.LimitWindow{UsedPct: 12}}},
		Panes: []reloadPane{
			{Snapshot: pane.Snapshot{ID: "p3", Name: "one", CustomName: "mine", Command: []string{"sleep"}, Cwd: dir, PID: pid1, Cols: 40, Rows: 6, Replay: "before-reload"},
				FD: fd1, Dir: dir, Loose: true, Transcript: transcript,
				Tracker: detect.TrackerState{Hint: "claude", Status: detect.Status{Agent: "claude", State: "working"}, Seen: true}},
			{Snapshot: pane.Snapshot{ID: "p5", Name: "two", Command: []string{"sleep"}, Cwd: repo, PID: pid2, Cols: 40, Rows: 6},
				FD: fd2, Dir: repo},
		},
	}
	s.adopt(st)

	if s.nextID != 7 {
		t.Fatalf("next id %d", s.nextID)
	}
	if l := s.allLimits().Limits; len(l) != 1 || l[0].Agent != "codex" {
		t.Fatalf("limits: %+v", l)
	}
	a5WaitFor(t, "limits event", func() bool { return ev.count(proto.EventAgentLimits) == 1 })
	list := s.list()
	if len(list) != 2 || list[0].ID != "p3" || list[1].ID != "p5" {
		t.Fatalf("panes: %+v", list)
	}
	if list[0].Name != "mine" || !list[0].CustomName || list[0].ProjectID != "" {
		t.Fatalf("loose pane: %+v", list[0])
	}
	if list[1].ProjectID == "" {
		t.Fatalf("pane in a repository lost its project: %+v", list[1])
	}
	e := a5Entry(t, s, "p3")
	e.mu.Lock()
	tok := e.tokens
	e.mu.Unlock()
	if tok == nil || tok.Output != 7 || tok.Model != "opus" {
		t.Fatalf("tokens from transcript: %+v", tok)
	}
	a5WaitFor(t, "replayed and new output", func() bool {
		screen := strings.Join(e.p.PlainLines(), "\n")
		return strings.Contains(screen, "before-reload") && strings.Contains(screen, "adopted-one")
	})
	// New panes continue the numbering.
	info, perr := s.create(proto.PaneCreateParams{Command: []string{"/bin/sh", "-c", "sleep 30"}, Cwd: dir, NoProject: true})
	if perr != nil || info.ID != "p8" {
		t.Fatalf("next pane %q %v", info.ID, perr)
	}
}

func TestA5RunAdoptsReloadState(t *testing.T) {
	a5IsolateEnv(t)
	dir, err := os.MkdirTemp("", "a5r")
	if err != nil {
		t.Fatal(err)
	}
	dir, _ = filepath.EvalSymlinks(dir)
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "s.sock")

	// The previous program's listener and a pane, as inherited descriptors.
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	ul := ln.(*net.UnixListener)
	ul.SetUnlinkOnClose(false)
	lf, err := ul.File()
	if err != nil {
		t.Fatal(err)
	}
	lfd, err := unix.Dup(int(lf.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	lf.Close()
	ln.Close()
	pfd, pid := a5PtyProgram(t, "exec sleep 30")

	runs, _ := json.Marshal(runLogFile{Active: []agentRun{{Pane: "p2", Agent: "claude", Dir: dir, Seen: time.Now()}}})
	os.WriteFile(filepath.Join(dir, "agent-runs.json"), runs, 0o600)
	st, _ := json.Marshal(reloadState{FromBuild: "old", ListenerFD: lfd, NextID: 2,
		Panes: []reloadPane{{Snapshot: pane.Snapshot{ID: "p2", Command: []string{"sleep"}, Cwd: dir, PID: pid, Cols: 40, Rows: 6}, FD: pfd, Dir: dir, Loose: true}}})
	statePath := filepath.Join(dir, "reload-state.json")
	os.WriteFile(statePath, st, 0o600)
	t.Setenv(reloadStateEnv, statePath)

	s := New(sock, dir)
	if len(s.runs.active) != 1 || len(s.runs.interruptedRuns()) != 0 {
		t.Fatalf("a reload keeps runs active: %+v %+v", s.runs.active, s.runs.interrupted)
	}
	ran := make(chan error, 1)
	go func() { ran <- s.Run() }()

	var conn net.Conn
	a5WaitFor(t, "server on inherited listener", func() bool {
		conn, err = net.Dial("unix", sock)
		return err == nil
	})
	pc := proto.NewConn(conn)
	if err := pc.Write(proto.Message{ID: "1", Method: proto.MethodPaneList}); err != nil {
		t.Fatal(err)
	}
	var list proto.PaneList
	for {
		m, err := pc.Read()
		if err != nil {
			t.Fatal(err)
		}
		if m.ID == "1" {
			if json.Unmarshal(m.Result, &list) != nil || len(list.Panes) != 1 || list.Panes[0].ID != "p2" {
				t.Fatalf("adopted panes: %s", m.Result)
			}
			break
		}
	}
	conn.Close()
	s.Stop()
	select {
	case err := <-ran:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("server did not stop")
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatal("state file not consumed")
	}
	// A server that took over an inherited listener still clears the socket
	// away when it stops, or the next one would find a dead file.
	if _, err := os.Stat(sock); !os.IsNotExist(err) {
		t.Fatalf("socket left behind by a reloaded server: %v", err)
	}
}

// ---- usage ----

// a5Unwatched registers a pane without the watch goroutine, whose fields
// (like entry.usage) the test then owns. With an agent, it waits until the
// agent is detected.
func a5Unwatched(t *testing.T, s *Server, id, agent, dir string) *entry {
	t.Helper()
	p, err := pane.Start(pane.Options{ID: id, Command: []string{"/bin/sleep", "30"}, Cwd: dir})
	if err != nil {
		t.Fatal(err)
	}
	e := newEntry(p, dir, detect.NewTracker(s.manifests, agent), nil)
	s.mu.Lock()
	s.panes[id] = e
	s.order = append(s.order, id)
	s.mu.Unlock()
	if agent != "" {
		a5WaitFor(t, agent+" detected", func() bool {
			s.observe(e)
			a := e.info().Agent
			return a != nil && a.Name == agent
		})
	}
	return e
}

func TestA5PollUsageCodex(t *testing.T) {
	home := a5IsolateEnv(t)
	s, dir := a5Server(t)
	work := filepath.Join(dir, "work")
	os.MkdirAll(work, 0o755)

	rollout := filepath.Join(home, ".codex", "sessions", "2026", "09", "14", "rollout-a5.jsonl")
	os.MkdirAll(filepath.Dir(rollout), 0o755)
	lines := []string{
		`{"type":"session_meta","payload":{"id":"cx1","cwd":"` + work + `","git":{"branch":"HEAD"}}}`,
		`{"type":"event_msg","payload":{"type":"user_message","message":"do things"}}`,
		`{"type":"turn_context","payload":{"model":"gpt-x"}}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":40,"output_tokens":9},"last_token_usage":{"input_tokens":60},"model_context_window":1000},"rate_limits":{"primary":{"used_percent":20,"window_minutes":300,"resets_at":1900000000},"secondary":{"used_percent":3,"window_minutes":10080}}}}`,
	}
	os.WriteFile(rollout, []byte(strings.Join(lines, "\n")+"\n"), 0o644)

	e := a5Unwatched(t, s, "p1", "codex", work)
	info := e.info()
	s.pollUsage(e)
	pi := e.info()
	if tok := pi.Agent.Tokens; tok == nil || tok.Input != 60 || tok.CacheRead != 40 || tok.Output != 9 || tok.ContextSize != 1000 || tok.Model != "gpt-x" {
		t.Fatalf("tokens: %+v", pi.Agent.Tokens)
	}
	lim := s.allLimits().Limits
	if len(lim) != 1 || lim[0].Agent != "codex" || lim[0].FiveHour == nil || lim[0].FiveHour.UsedPct != 20 || lim[0].Week == nil {
		t.Fatalf("limits: %+v", lim)
	}
	// A second poll reuses the found session.
	src := e.usage
	s.pollUsage(e)
	if e.usage != src {
		t.Fatal("session looked up again within a minute")
	}

	// Session listing: the pane's open session is claimed, HEAD is no branch.
	list, perr := s.listSessions(proto.SessionListParams{Dir: work})
	if perr != nil {
		t.Fatal(perr)
	}
	if len(list.Sessions) != 1 || list.Sessions[0].PaneID != info.ID || list.Sessions[0].Branch != "" {
		t.Fatalf("sessions: %+v", list.Sessions)
	}
	if perr := s.deleteSession(proto.SessionRef{Agent: "codex", ID: "cx1", Dir: work}); perr == nil || !strings.Contains(perr.Message, "open in pane") {
		t.Fatalf("deleting an open session: %v", perr)
	}
}

func TestA5PollUsageSkips(t *testing.T) {
	a5IsolateEnv(t)
	s, dir := a5Server(t)
	e := a5Unwatched(t, s, "p1", "", dir)
	s.pollUsage(e) // no agent
	if e.usage != nil {
		t.Fatal("usage for a plain pane")
	}

	e = a5Unwatched(t, s, "p2", "opencode", dir)
	s.pollUsage(e) // no session saved: nothing found
	if e.usage != nil || e.info().Agent.Tokens != nil {
		t.Fatal("usage without a session")
	}
}

func TestA5ReportTranscriptAndStatusLine(t *testing.T) {
	home := a5IsolateEnv(t)
	s, dir := a5Server(t)
	ev := a5Listen(t, s)
	repo := filepath.Join(dir, "repo")
	a5GitRepo(t, repo)

	info, perr := s.create(proto.PaneCreateParams{Agent: "claude", Command: []string{"/bin/sleep", "30"}, Cwd: repo})
	if perr != nil {
		t.Fatal(perr)
	}
	e := a5Entry(t, s, info.ID)
	if e.project == nil {
		t.Fatal("pane in a repository has no project")
	}
	loaded := e.project.load()
	e.project.mu.Lock()
	e.project.info.Worktrees = loaded.Worktrees
	e.project.mu.Unlock()
	t1 := filepath.Join(home, "one.jsonl")
	os.WriteFile(t1, []byte(`{"type":"assistant","message":{"id":"a","usage":{"input_tokens":3,"output_tokens":4}}}`+"\n"), 0o600)

	s.statusLine(e, proto.AgentReportParams{ID: info.ID, Agent: "claude", Event: "StatusLine", ContextUsed: 10, ContextSize: 200})
	s.report(e, proto.AgentReportParams{ID: info.ID, Agent: "claude", Event: "PostToolUse", SessionID: "sess-1", TranscriptPath: t1})
	e.project.mu.Lock()
	pending := e.project.pending
	e.project.mu.Unlock()
	if !pending {
		t.Fatal("a tool event should request a project refresh")
	}
	pi := e.info()
	if pi.Agent == nil || pi.Agent.SessionID != "sess-1" || pi.Agent.State != detect.StateWorking {
		t.Fatalf("agent: %+v", pi.Agent)
	}
	if tok := pi.Agent.Tokens; tok == nil || tok.Output != 4 || tok.ContextSize != 200 {
		t.Fatalf("tokens keep the status line's context size: %+v", tok)
	}
	a5WaitFor(t, "pane.updated", func() bool { return ev.count(proto.EventPaneUpdated) > 0 })

	// A missing transcript leaves the tokens; a new one replaces them.
	s.report(e, proto.AgentReportParams{ID: info.ID, Agent: "claude", Event: "Notification", TranscriptPath: filepath.Join(home, "gone.jsonl")})
	if tok := e.info().Agent.Tokens; tok == nil || tok.Output != 4 {
		t.Fatalf("missing transcript dropped tokens: %+v", tok)
	}
	t2 := filepath.Join(home, "two.jsonl")
	os.WriteFile(t2, []byte(`{"type":"assistant","message":{"id":"b","usage":{"output_tokens":50}}}`+"\n"), 0o600)
	s.report(e, proto.AgentReportParams{ID: info.ID, Agent: "claude", Event: "Stop", TranscriptPath: t2})
	if tok := e.info().Agent.Tokens; tok == nil || tok.Output != 50 {
		t.Fatalf("new transcript: %+v", tok)
	}

	s.userInput(e)
	s.markSeen(e)
	s.setWatchers(e, +1)
	s.setWatchers(e, -1)
	if ex := e.explain(); ex.Hint != "claude" || ex.Status.Agent != "claude" {
		t.Fatalf("explain: %+v", ex)
	}
	s.rename(e, "renamed")
	if e.info().Name != "renamed" {
		t.Fatal("rename")
	}
	if !s.projectHasPanes(e.project.id) || s.projectHasPanes("nope") || !s.panesIn(repo) || s.panesIn(filepath.Join(dir, "elsewhere")) {
		t.Fatal("project pane lookups")
	}

	// Sessions: an interrupted run with nothing saved is listed on its own;
	// a claude session reported by the pane is claimed by it.
	enc := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, repo)
	sessFile := filepath.Join(home, ".claude", "projects", enc, "sess-1.jsonl")
	os.MkdirAll(filepath.Dir(sessFile), 0o755)
	line, _ := json.Marshal(map[string]any{"type": "user", "cwd": repo, "gitBranch": "HEAD", "message": map[string]any{"role": "user", "content": "Plan it"}})
	os.WriteFile(sessFile, append(line, '\n'), 0o644)
	s.runs.mu.Lock()
	s.runs.interrupted = []agentRun{
		{Agent: "gemini", Dir: repo, Started: time.Now().Add(-time.Hour), Seen: time.Now().Add(-time.Minute)},
		{Agent: "codex", Dir: "/elsewhere", Seen: time.Now()},
	}
	s.runs.mu.Unlock()
	list, perr := s.listSessions(proto.SessionListParams{ProjectID: e.project.id})
	if perr != nil {
		t.Fatal(perr)
	}
	if len(list.Sessions) != 2 {
		t.Fatalf("sessions: %+v", list.Sessions)
	}
	if first := list.Sessions[0]; !first.Interrupted || first.Agent != "gemini" || first.ID != "" {
		t.Fatalf("interrupted run first: %+v", first)
	}
	if second := list.Sessions[1]; second.ID != "sess-1" || second.PaneID != info.ID || second.Branch != "main" {
		t.Fatalf("claimed session: %+v", second)
	}
	if _, perr := s.listSessions(proto.SessionListParams{}); perr == nil {
		t.Fatal("listing without a project or directory")
	}
	if _, perr := s.listSessions(proto.SessionListParams{ProjectID: "nope"}); perr == nil || perr.Code != proto.ErrNotFound {
		t.Fatalf("unknown project: %v", perr)
	}
	if perr := s.deleteSession(proto.SessionRef{Agent: "gemini", Dir: repo}); perr == nil || !strings.Contains(perr.Message, "dismiss") {
		t.Fatalf("deleting a run without a conversation: %v", perr)
	}
}

// ---- local files, shell, fs ----

func TestA5CopyFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	os.WriteFile(src, []byte("data"), 0o640)

	if why := copyFile(filepath.Join(dir, "missing"), filepath.Join(dir, "x"), false); why == "" {
		t.Fatal("copied a missing file")
	}
	dst := filepath.Join(dir, "out", "nested", "dst")
	if why := copyFile(src, dst, false); why != "" {
		t.Fatal(why)
	}
	if st, _ := os.Stat(dst); st.Mode().Perm() != 0o640 {
		t.Fatalf("mode %v", st.Mode())
	}
	if why := copyFile(src, dst, false); why != "exists" {
		t.Fatalf("existing: %q", why)
	}
	os.WriteFile(src, []byte("newer"), 0o640)
	if why := copyFile(src, dst, true); why != "" {
		t.Fatal(why)
	}
	if b, _ := os.ReadFile(dst); string(b) != "newer" {
		t.Fatalf("overwrite: %q", b)
	}
	if _, err := os.Stat(dst + ".conch-tmp"); !os.IsNotExist(err) {
		t.Fatal("temp file left")
	}

	link := filepath.Join(dir, "link")
	os.Symlink("src", link)
	linkDst := filepath.Join(dir, "out", "link")
	os.WriteFile(linkDst, []byte("in the way"), 0o600)
	if why := copyFile(link, linkDst, true); why != "" {
		t.Fatal(why)
	}
	if target, err := os.Readlink(linkDst); err != nil || target != "src" {
		t.Fatalf("symlink copy: %q %v", target, err)
	}

	if why := copyFile(filepath.Join(dir, "out"), filepath.Join(dir, "dircopy"), false); why != "not a regular file" {
		t.Fatalf("directory: %q", why)
	}
	big := filepath.Join(dir, "big")
	f, _ := os.Create(big)
	f.Truncate(maxLocalFile + 1)
	f.Close()
	if why := copyFile(big, filepath.Join(dir, "bigcopy"), false); why != "larger than 10 MB" {
		t.Fatalf("big: %q", why)
	}
	// The destination's parent is a file.
	if why := copyFile(src, filepath.Join(dir, "src", "under-a-file"), false); why == "" {
		t.Fatal("copied under a file")
	}
	// An unreadable source.
	secret := filepath.Join(dir, "secret")
	os.WriteFile(secret, []byte("x"), 0o000)
	if os.Getuid() != 0 {
		if why := copyFile(secret, filepath.Join(dir, "secretcopy"), false); why == "" {
			t.Fatal("copied an unreadable file")
		}
	}
	// An unwritable destination directory.
	ro := filepath.Join(dir, "ro")
	os.MkdirAll(ro, 0o500)
	defer os.Chmod(ro, 0o700)
	if os.Getuid() != 0 {
		if why := copyFile(src, filepath.Join(ro, "x"), false); why == "" {
			t.Fatal("copied into a read-only directory")
		}
		if why := copyFile(link, filepath.Join(ro, "l"), false); why == "" {
			t.Fatal("linked into a read-only directory")
		}
	}
}

func TestA5FileState(t *testing.T) {
	dir := t.TempDir()
	p := func(name string) string { return filepath.Join(dir, name) }
	os.WriteFile(p("a"), []byte("same"), 0o600)
	os.WriteFile(p("b"), []byte("same"), 0o600)
	os.WriteFile(p("c"), []byte("diff"), 0o600)
	os.WriteFile(p("d"), []byte("longer"), 0o600)
	os.Symlink("a", p("la"))
	os.Symlink("a", p("la2"))
	os.Symlink("b", p("lb"))
	for _, tc := range []struct{ src, dst, want string }{
		{"missing", "a", proto.FileMissing},
		{"a", "missing", proto.FileMissing},
		{"a", "b", proto.FileSame},
		{"a", "c", proto.FileDiffers},
		{"a", "d", proto.FileDiffers},
		{"la", "la2", proto.FileSame},
		{"la", "lb", proto.FileDiffers},
		{"la", "a", proto.FileDiffers},
	} {
		if got := fileState(p(tc.src), p(tc.dst)); got != tc.want {
			t.Errorf("fileState(%s, %s) = %s, want %s", tc.src, tc.dst, got, tc.want)
		}
	}
	for _, name := range []string{"big1", "big2"} {
		f, _ := os.Create(p(name))
		f.Truncate(maxLocalFile + 1)
		f.Close()
	}
	if got := fileState(p("big1"), p("big2")); got != proto.FileSame {
		t.Fatalf("big files of the same size: %s", got)
	}
}

func TestA5ShellHelpers(t *testing.T) {
	home := a5IsolateEnv(t)
	s, dir := a5Server(t)

	if got := omzDir(home); got != filepath.Join(home, ".oh-my-zsh") {
		t.Fatalf("default omz dir %s", got)
	}
	if zshrcVar(home, "ZSH") != "" {
		t.Fatal("no zshrc")
	}
	for rc, want := range map[string]string{
		"export ZSH=\"$HOME/omz\"\n":           filepath.Join(home, "omz"),
		"ZSH='${HOME}/braced'\n":               filepath.Join(home, "braced"),
		"  export ZSH=~/tilde # comment\n":     filepath.Join(home, "tilde"),
		"OTHER=1\nZSH=/abs/omz\nZSH=/second\n": "/abs/omz",
	} {
		os.WriteFile(filepath.Join(home, ".zshrc"), []byte(rc), 0o600)
		if got := omzDir(home); got != want {
			t.Errorf("zshrc %q: omz dir %s, want %s", rc, got, want)
		}
	}
	if th := s.shellThemes(); th.OMZ || th.Shell != "sh" {
		t.Fatalf("themes without omz: %+v", th)
	}

	if env := s.shellThemeEnv(""); env != nil {
		t.Fatal("no theme")
	}
	t.Setenv("SHELL", "/bin/zsh")
	for _, bad := range []string{"a/b", "a b", "a\\b", "tab\t"} {
		if env := s.shellThemeEnv(bad); env != nil {
			t.Fatalf("theme %q accepted", bad)
		}
	}
	env := s.shellThemeEnv("agnoster")
	want := []string{"ZDOTDIR=" + filepath.Join(dir, "zsh"), "CONCH_USER_ZDOTDIR=" + home, "CONCH_OMZ_THEME=agnoster"}
	if strings.Join(env, "|") != strings.Join(want, "|") {
		t.Fatalf("env %v", env)
	}
	for name := range zshWrapper {
		if _, err := os.Stat(filepath.Join(dir, "zsh", name)); err != nil {
			t.Fatalf("wrapper %s: %v", name, err)
		}
	}
	t.Setenv("ZDOTDIR", "/custom/zdot")
	if env := s.shellThemeEnv("x"); len(env) != 3 || env[1] != "CONCH_USER_ZDOTDIR=/custom/zdot" {
		t.Fatalf("user ZDOTDIR: %v", env)
	}
	// Wrapper files can't be written.
	os.WriteFile(filepath.Join(dir, "file"), nil, 0o600)
	s.configDir = filepath.Join(dir, "file")
	if env := s.shellThemeEnv("x"); env != nil {
		t.Fatalf("unwritable config dir: %v", env)
	}
	t.Setenv("SHELL", "/bin/bash")
	s.configDir = dir
	if env := s.shellThemeEnv("x"); env != nil {
		t.Fatal("theme env for bash")
	}
}

func TestA5ResolvePathAndFS(t *testing.T) {
	home := a5IsolateEnv(t)
	s, dir := a5Server(t)
	for in, want := range map[string]string{
		"":          home,
		"~":         home,
		"~/a/b":     filepath.Join(home, "a", "b"),
		"rel/x":     filepath.Join(home, "rel", "x"),
		"/abs/../y": "/y",
	} {
		if got, err := resolvePath(in); err != nil || got != want {
			t.Errorf("resolvePath(%q) = %q %v, want %q", in, got, err, want)
		}
	}
	if _, perr := s.listDir(proto.FSListParams{Path: filepath.Join(dir, "missing")}); perr == nil {
		t.Fatal("listing a missing folder")
	}
	os.MkdirAll(filepath.Join(dir, "real"), 0o755)
	os.Symlink(filepath.Join(dir, "real"), filepath.Join(dir, "linked"))
	os.Symlink(filepath.Join(dir, "nowhere"), filepath.Join(dir, "dangling"))
	list, perr := s.listDir(proto.FSListParams{Path: dir})
	if perr != nil {
		t.Fatal(perr)
	}
	names := []string{}
	for _, e := range list.Entries {
		names = append(names, e.Name)
	}
	if got := strings.Join(names, ","); got != "linked,real" {
		t.Fatalf("entries %s", got)
	}
	if root, perr := s.listDir(proto.FSListParams{Path: "/"}); perr != nil || root.Parent != "" {
		t.Fatalf("root listing parent %q %v", root.Parent, perr)
	}
	if _, perr := s.mkdir(proto.FSMkdirParams{Path: filepath.Join(dir, "no", "parent")}); perr == nil || strings.Contains(perr.Message, "already exists") {
		t.Fatalf("mkdir without a parent: %v", perr)
	}
	if _, perr := s.createProject(proto.ProjectCreateParams{Path: filepath.Join(dir, "no", "parent")}); perr == nil || strings.Contains(perr.Message, "already exists") {
		t.Fatalf("create without a parent: %v", perr)
	}
	p, perr := s.createProject(proto.ProjectCreateParams{Path: filepath.Join(dir, "plain")})
	if perr != nil || p.Git {
		t.Fatalf("plain project: %+v %v", p, perr)
	}
}

// ---- projects ----

func TestA5ProjectCatalog(t *testing.T) {
	home := a5IsolateEnv(t)
	s, dir := a5Server(t)
	ev := a5Listen(t, s)
	pm := s.projects

	pm.load() // no catalog yet
	os.WriteFile(pm.file, []byte("{nope"), 0o600)
	pm.load()
	if len(pm.list()) != 0 {
		t.Fatal("corrupt catalog loaded projects")
	}

	repo := filepath.Join(dir, "repo")
	a5GitRepo(t, repo)
	plain := filepath.Join(home, "plain")
	os.MkdirAll(plain, 0o755)
	files := []string{"*.secret"}
	b, _ := json.Marshal(catalog{Projects: []catalogEntry{
		{Path: repo, LocalFiles: &files},
		{Path: filepath.Join(dir, "gone")},
		{Path: "~/plain"},
	}})
	os.WriteFile(pm.file, b, 0o600)
	pm.load()
	list := pm.list()
	if len(list) != 2 || list[0].Path != repo || list[1].Path != plain {
		t.Fatalf("loaded: %+v", list)
	}
	if list[0].LocalFilesDefault || strings.Join(list[0].LocalFiles, ",") != "*.secret" || !list[0].Git || list[1].Git {
		t.Fatalf("catalog details: %+v", list)
	}
	// Adding again returns the same project.
	again, err := pm.add(filepath.Join(repo, "."), true)
	if err != nil || again.id != list[0].ID {
		t.Fatalf("re-add: %v %v", again, err)
	}
	if _, err := pm.add(filepath.Join(dir, "missing"), true); err == nil {
		t.Fatal("added a missing folder")
	}
	os.WriteFile(filepath.Join(dir, "afile"), nil, 0o600)
	if _, err := pm.add(filepath.Join(dir, "afile"), true); err == nil {
		t.Fatal("added a file")
	}
	if pm.ensure(filepath.Join(home)) != nil {
		t.Fatal("a plain folder became a project implicitly")
	}
	if p := pm.ensure(filepath.Join(plain)); p == nil || p.id != list[1].ID {
		t.Fatal("ensure finds a known project")
	}

	if perr := pm.remove("nope"); perr == nil || perr.Code != proto.ErrNotFound {
		t.Fatalf("remove unknown: %v", perr)
	}
	if perr := pm.remove(list[1].ID); perr != nil {
		t.Fatal(perr)
	}
	a5WaitFor(t, "project.removed", func() bool { return ev.count(proto.EventProjectRemoved) == 1 })
	saved, _ := os.ReadFile(pm.file)
	if strings.Contains(string(saved), plain) || !strings.Contains(string(saved), `"*.secret"`) {
		t.Fatalf("catalog after remove: %s", saved)
	}
	pm.requestPRs(again)
	again.mu.Lock()
	pending := again.prPending
	again.mu.Unlock()
	if !pending {
		t.Fatal("requestPRs")
	}

	// Saving into a missing directory only logs.
	pm.file = filepath.Join(dir, "missing", "projects.json")
	pm.save()
}

func TestA5WorktreeErrors(t *testing.T) {
	a5IsolateEnv(t)
	s, dir := a5Server(t)
	pm := s.projects
	repo := filepath.Join(dir, "repo")
	a5GitRepo(t, repo)
	p, err := pm.add(repo, true)
	if err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	p.info = p.load()
	p.info.LocalFiles, p.info.LocalFilesDefault = DefaultLocalFiles, true
	p.mu.Unlock()

	plainDir := filepath.Join(dir, "plain")
	os.MkdirAll(plainDir, 0o755)
	plain, _ := pm.add(plainDir, true)
	if _, _, perr := pm.addWorktree(plain, "x", ""); perr == nil || !strings.Contains(perr.Message, "not a git repository") {
		t.Fatalf("plain folder: %v", perr)
	}
	if _, _, perr := pm.addWorktree(p, "main", ""); perr == nil || !strings.Contains(perr.Message, "already checked out") {
		t.Fatalf("checked-out branch: %v", perr)
	}
	if _, _, perr := pm.addWorktree(p, "bad..name", ""); perr == nil || !strings.Contains(perr.Message, "invalid branch") {
		t.Fatalf("bad name: %v", perr)
	}
	if _, _, perr := pm.addWorktree(p, "feat", "no-such-base"); perr == nil {
		t.Fatal("missing base accepted")
	}
	// A taken directory gets a suffix.
	taken := filepath.Join(dir, "repo.worktrees", "topic")
	os.MkdirAll(taken, 0o755)
	path, _, perr := pm.addWorktree(p, "topic", "")
	if perr != nil || path != taken+"-2" {
		t.Fatalf("suffixed worktree %q %v", path, perr)
	}

	if perr := pm.removeWorktree(proto.WorktreeRemoveParams{ProjectID: "nope", Path: path}); perr == nil || perr.Code != proto.ErrNotFound {
		t.Fatalf("unknown project: %v", perr)
	}
	if perr := pm.removeWorktree(proto.WorktreeRemoveParams{ProjectID: p.id, Path: filepath.Join(dir, "elsewhere")}); perr == nil || perr.Code != proto.ErrNotFound {
		t.Fatalf("unknown worktree: %v", perr)
	}
	info, perr := s.create(proto.PaneCreateParams{Command: []string{"/bin/sh", "-c", "sleep 30"}, Cwd: path})
	if perr != nil {
		t.Fatal(perr)
	}
	if perr := pm.removeWorktree(proto.WorktreeRemoveParams{ProjectID: p.id, Path: path}); perr == nil || !strings.Contains(perr.Message, "panes are still running") {
		t.Fatalf("worktree with panes: %v", perr)
	}
	if perr := s.close(info.ID); perr != nil {
		t.Fatal(perr)
	}
	if perr := s.close(info.ID); perr == nil {
		t.Fatal("closed a pane twice")
	}
	if perr := pm.removeWorktree(proto.WorktreeRemoveParams{ProjectID: p.id, Path: path}); perr != nil {
		t.Fatalf("remove: %v", perr)
	}

	if _, perr := pm.changes(proto.ChangesParams{ProjectID: "nope"}); perr == nil {
		t.Fatal("changes of unknown project")
	}
	if ch, perr := pm.changes(proto.ChangesParams{ProjectID: p.id, Branch: "no-such-branch"}); perr != nil || len(ch.Files) != 0 || ch.Worktree != "" {
		t.Fatalf("a missing branch has no changes: %+v %v", ch, perr)
	}
	if _, perr := pm.diff(proto.DiffParams{ProjectID: "nope"}); perr == nil {
		t.Fatal("diff of unknown project")
	}
	if _, perr := pm.diff(proto.DiffParams{ProjectID: p.id, Branch: "no-such-branch", File: "x"}); perr == nil {
		t.Fatal("diff of a missing branch")
	}
	if perr := p.linkedWorktree(filepath.Join(dir, "elsewhere")); perr == nil || perr.Code != proto.ErrNotFound {
		t.Fatalf("linkedWorktree: %v", perr)
	}
	if _, perr := pm.copyFiles(proto.WorktreeFilesParams{ProjectID: "nope"}); perr == nil {
		t.Fatal("copyFiles unknown project")
	}
	if _, perr := pm.setLocalFiles(proto.ProjectFilesParams{ProjectID: "nope"}); perr == nil {
		t.Fatal("setLocalFiles unknown project")
	}
	if _, perr := pm.setLocalFiles(proto.ProjectFilesParams{ProjectID: p.id, Patterns: []string{"/abs"}}); perr == nil {
		t.Fatal("absolute pattern accepted")
	}
	got, perr := pm.setLocalFiles(proto.ProjectFilesParams{ProjectID: p.id, Patterns: []string{" a ", "", "  "}})
	if perr != nil || strings.Join(got.LocalFiles, ",") != "a" {
		t.Fatalf("patterns trimmed: %+v %v", got.LocalFiles, perr)
	}
	if files, err := plain.localFiles(t.Context(), true); err != nil || files != nil {
		t.Fatalf("plain folder local files: %v %v", files, err)
	}
}

func TestA5AgentSetupErrors(t *testing.T) {
	home := a5IsolateEnv(t)
	s, dir := a5Server(t)
	if _, perr := s.agentSetup(proto.AgentSetupParams{Dir: filepath.Join(dir, "missing")}); perr == nil {
		t.Fatal("missing dir")
	}
	os.WriteFile(filepath.Join(dir, "f"), nil, 0o600)
	if _, perr := s.agentSetup(proto.AgentSetupParams{Dir: filepath.Join(dir, "f")}); perr == nil || !strings.Contains(perr.Message, "not a directory") {
		t.Fatalf("file: %v", perr)
	}
	if _, perr := s.agentSetup(proto.AgentSetupParams{Dir: "~", Agent: "nope"}); perr == nil || !strings.Contains(perr.Message, "unknown agent") {
		t.Fatalf("unknown agent: %v", perr)
	}
	res, perr := s.agentSetup(proto.AgentSetupParams{Dir: "~"})
	if perr != nil || res.Dir != home || len(res.Agents) == 0 {
		t.Fatalf("home setup: %+v %v", res, perr)
	}
}

func TestA5CreateErrors(t *testing.T) {
	a5IsolateEnv(t)
	s, dir := a5Server(t)
	if _, perr := s.create(proto.PaneCreateParams{Agent: "nope"}); perr == nil || !strings.Contains(perr.Message, "unknown agent") {
		t.Fatalf("unknown agent: %v", perr)
	}
	// A manifest-only agent can be detected but not launched.
	s.manifests["watchonly"] = &detect.Manifest{Agent: "watchonly"}
	if _, perr := s.create(proto.PaneCreateParams{Agent: "watchonly"}); perr == nil || !strings.Contains(perr.Message, "cannot be launched") {
		t.Fatalf("unlaunchable agent: %v", perr)
	}
	if _, perr := s.create(proto.PaneCreateParams{Command: []string{"/bin/sh"}, Cwd: filepath.Join(dir, "missing")}); perr == nil {
		t.Fatal("missing cwd")
	}
	if _, perr := s.create(proto.PaneCreateParams{Command: []string{filepath.Join(dir, "no-binary")}, Cwd: dir}); perr == nil || !strings.Contains(perr.Message, "start") {
		t.Fatalf("missing binary: %v", perr)
	}
	// No command and no cwd: the login shell in the home directory.
	info, perr := s.create(proto.PaneCreateParams{NoProject: true})
	if perr != nil {
		t.Fatal(perr)
	}
	home, _ := os.UserHomeDir()
	if info.Cwd != home || strings.Join(info.Command, " ") != "/bin/sh -l" {
		t.Fatalf("default pane: %+v", info)
	}
}
