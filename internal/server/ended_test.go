package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/adapter"
	"github.com/Amitgb14/conch/internal/proto"
)

// endedFixture is a server with adapters, a work folder and a client
// listening for what it broadcasts.
func endedFixture(t *testing.T) (*Server, string, *client) {
	t.Helper()
	a5IsolateEnv(t)
	s, dir := a5Server(t)
	work := filepath.Join(dir, "work")
	os.MkdirAll(work, 0o755)
	var err error
	if s.adapters, err = adapter.New("/usr/local/bin/conch", s.configDir); err != nil {
		t.Fatal(err)
	}
	// An outbox nobody writes out: what was broadcast stays in its queue.
	c := &client{subs: map[string]*subscription{}}
	c.out = &outbox{max: outboxMax}
	c.out.cond = sync.NewCond(&c.out.mu)
	s.mu.Lock()
	s.clients[c] = struct{}{}
	s.mu.Unlock()
	return s, work, c
}

// endedAgent starts an agent pane that runs until told to end with end,
// an awk statement — a program, since a shell in front is never taken for
// the agent — and watches it as the server does every pane.
func endedAgent(t *testing.T, s *Server, id, work, end string) (*entry, func()) {
	t.Helper()
	e := agentPane(t, s, id, "claude", work, "stty -echo; exec awk 'BEGIN { print \"working on "+id+"\"; fflush() } { "+end+" }'")
	s.runs.mu.Lock()
	_, ok := s.runs.active[id]
	s.runs.mu.Unlock()
	if !ok {
		t.Fatalf("run %s not noted", id)
	}
	go s.watch(e)
	return e, func() { e.p.SendText("go\r", false) }
}

func waitEnded(t *testing.T, s *Server, pane string) proto.EndedAgent {
	t.Helper()
	var got proto.EndedAgent
	a5WaitFor(t, "ended "+pane, func() bool {
		for _, r := range s.listEnded().Ended {
			if r.Pane == pane {
				got = r
				return true
			}
		}
		return false
	})
	return got
}

// sentEvents takes what the client was sent, keeping the events named.
func sentEvents(c *client, event string) []json.RawMessage {
	c.out.mu.Lock()
	q := c.out.q
	c.out.q, c.out.size = nil, 0
	c.out.mu.Unlock()
	var out []json.RawMessage
	for _, m := range q {
		if m.Event == event {
			out = append(out, m.Data)
		}
	}
	return out
}

// TestAgentEndedIsKept: an agent that ends by itself — exiting non-zero,
// killed, exiting 0 — is kept with why, its last screen and its place;
// one the person closed is not.
func TestAgentEndedIsKept(t *testing.T) {
	s, work, c := endedFixture(t)

	_, end := endedAgent(t, s, "p1", work, "exit 3")
	end()
	r := waitEnded(t, s, "p1")
	if r.Why != proto.EndedExited || r.ExitCode != 3 || !r.Failed() || r.Reason() != "exited with code 3" {
		t.Fatalf("non-zero exit: %+v", r)
	}
	if r.Agent != "claude" || r.Dir != work || r.ID == "" || r.Ended.IsZero() {
		t.Fatalf("what it needs to come back: %+v", r)
	}
	if !strings.Contains(strings.Join(r.Screen, "\n"), "working on p1") {
		t.Fatalf("last screen: %q", r.Screen)
	}
	if n := len(sentEvents(c, proto.EventPaneEnded)); n != 1 {
		t.Fatalf("pane.ended sent %d times", n)
	}

	e2, _ := endedAgent(t, s, "p2", work, "exit 0")
	syscall.Kill(e2.p.Info().PID, syscall.SIGKILL)
	r = waitEnded(t, s, "p2")
	if r.Why != proto.EndedKilled || r.Signal != "SIGKILL" || r.Reason() != "killed (SIGKILL)" || !r.Failed() {
		t.Fatalf("killed: %+v", r)
	}

	_, end = endedAgent(t, s, "p3", work, "exit 0")
	end()
	r = waitEnded(t, s, "p3")
	if r.Why != proto.EndedExited || r.ExitCode != 0 || r.Failed() {
		t.Fatalf("its own clean exit: %+v", r)
	}

	endedAgent(t, s, "p4", work, "exit 0")
	if perr := s.close("p4"); perr != nil {
		t.Fatal(perr)
	}
	time.Sleep(300 * time.Millisecond)
	for _, r := range s.listEnded().Ended {
		if r.Pane == "p4" {
			t.Fatalf("a pane the person closed is kept: %+v", r)
		}
	}
	if list := s.listEnded().Ended; len(list) != 3 || list[0].Pane != "p3" {
		t.Fatalf("newest first: %+v", list)
	}

	// A shell is never an agent that ended.
	sh := agentPane(t, s, "p5", "", work, "exit 1")
	go s.watch(sh)
	a5WaitFor(t, "shell exit", func() bool { return sh.info().State == proto.PaneExited })
	time.Sleep(200 * time.Millisecond)
	if len(s.listEnded().Ended) != 3 {
		t.Fatalf("a shell was kept: %+v", s.listEnded().Ended)
	}

	// Kept on disk for the next server, as a restart would read it.
	if l := loadRunLog(s.configDir, false); len(l.endedList()) != 3 {
		t.Fatalf("not kept through a restart: %+v", l.endedList())
	}
}

// TestEndedDismissAndResume: dismissing forgets the record and closes the
// ended pane; resuming starts its conversation in its folder under its
// name, says which pane it came back in before the old one goes, and
// forgets it. Neither touches a live pane that has the old pane's id.
func TestEndedDismissAndResume(t *testing.T) {
	s, work, c := endedFixture(t)

	_, end := endedAgent(t, s, "p1", work, "exit 3")
	end()
	r := waitEnded(t, s, "p1")
	if perr := s.dismissEnded(proto.EndedRef{ID: r.ID}); perr != nil {
		t.Fatal(perr)
	}
	if len(s.listEnded().Ended) != 0 {
		t.Fatal("dismissed but kept")
	}
	if _, perr := s.get("p1"); perr == nil {
		t.Fatal("the ended pane is still held")
	}
	if perr := s.dismissEnded(proto.EndedRef{ID: r.ID}); perr == nil || perr.Code != proto.ErrNotFound {
		t.Fatalf("twice: %v", perr)
	}
	sentEvents(c, "")

	e2, end := endedAgent(t, s, "p2", work, "exit 1")
	s.rename(e2, "reviewer")
	end()
	r = waitEnded(t, s, "p2")
	r.Session = "s-2"
	s.runs.addEnded(r) // as if the agent had reported its session
	sentEvents(c, "")
	info, perr := s.resumeEnded(c, proto.EndedResumeParams{ID: r.ID, Cols: 80, Rows: 24})
	if perr != nil {
		t.Fatal(perr)
	}
	if cmd := strings.Join(info.Command, " "); !strings.Contains(cmd, "--resume s-2") || info.Cwd != work || info.Name != "reviewer" {
		t.Fatalf("resumed as %s in %s named %q", cmd, info.Cwd, info.Name)
	}
	if len(s.listEnded().Ended) != 0 {
		t.Fatal("resumed but still kept")
	}
	gone := sentEvents(c, proto.EventPaneEndedGone)
	var g proto.EndedGone
	if len(gone) != 1 || json.Unmarshal(gone[0], &g) != nil || g.ID != r.ID || g.Pane != "p2" || g.Resumed != info.ID {
		t.Fatalf("gone: %s", gone)
	}
	if _, perr := s.get("p2"); perr == nil {
		t.Fatal("the ended pane is still held after resuming")
	}
	if _, perr := s.resumeEnded(c, proto.EndedResumeParams{ID: r.ID}); perr == nil || perr.Code != proto.ErrNotFound {
		t.Fatalf("resumed twice: %v", perr)
	}

	// A record from before a restart whose pane id a live pane now has.
	live := agentPane(t, s, "p9", "", work, "sleep 30")
	old := proto.EndedAgent{ID: endedID("p9", time.Now().Add(-time.Hour)), Pane: "p9", Agent: "claude", Dir: work,
		Why: proto.EndedServer, Ended: time.Now().Add(-time.Hour)}
	s.runs.addEnded(old)
	if perr := s.dismissEnded(proto.EndedRef{ID: old.ID}); perr != nil {
		t.Fatal(perr)
	}
	if live.info().State != proto.PaneRunning {
		t.Fatal("dismissing an old record closed the live pane with its id")
	}

	// A folder that is gone fails the resume and keeps the record.
	lost := proto.EndedAgent{ID: "x.1", Pane: "x", Agent: "claude", Dir: filepath.Join(work, "gone"), Why: proto.EndedExited, Ended: time.Now()}
	s.runs.addEnded(lost)
	if _, perr := s.resumeEnded(c, proto.EndedResumeParams{ID: lost.ID}); perr == nil {
		t.Fatal("resumed in a folder that is gone")
	}
	if _, ok := s.runs.endedByID(lost.ID); !ok {
		t.Fatal("a failed resume dropped the record")
	}
}

// TestEndedExpiresAndIsCapped: records older than keepEnded go, and only
// the newest maxEnded are kept.
func TestEndedExpiresAndIsCapped(t *testing.T) {
	l := loadRunLog(t.TempDir(), false)
	now := time.Now()
	l.addEnded(proto.EndedAgent{ID: "old", Ended: now.Add(-keepEnded - time.Minute)})
	if len(l.endedList()) != 0 {
		t.Fatal("expired record kept")
	}
	for i := range maxEnded + 5 {
		l.addEnded(proto.EndedAgent{ID: endedID("p", now.Add(time.Duration(i)*time.Second)), Ended: now.Add(time.Duration(i) * time.Second)})
	}
	list := l.endedList()
	if len(list) != maxEnded || !list[0].Ended.Equal(now.Add(time.Duration(maxEnded+4)*time.Second)) {
		t.Fatalf("%d kept, newest %v", len(list), list[0].Ended)
	}
}

// TestEndedByARestart: a server stopped on purpose leaves its agents kept
// as ended by the server (as well as offered in Sessions); one that went
// unasked resumes them instead, and only those it cannot are kept — and
// with auto-resume off, all of them. A log from before records loads.
func TestEndedByARestart(t *testing.T) {
	now := time.Now()
	dir := t.TempDir()
	path := filepath.Join(dir, "agent-runs.json")
	run := agentRun{Pane: "p1", Agent: "claude", Dir: "/a", Session: "s1", Account: "work", Name: "planner", Started: now.Add(-time.Hour), Seen: now}
	b, _ := json.Marshal(runLogFile{Active: []agentRun{run}, Stopped: true})
	os.WriteFile(path, b, 0o600)
	l := loadRunLog(dir, false)
	list := l.endedList()
	if len(list) != 1 || list[0].Why != proto.EndedServer || list[0].Session != "s1" || list[0].Account != "work" ||
		list[0].Name != "planner" || list[0].Reason() != "the server restarted" {
		t.Fatalf("stopped server: %+v", list)
	}
	if loadRunLog(dir, false).endedList()[0].ID != list[0].ID {
		t.Fatal("not written back as kept")
	}

	b, _ = json.Marshal(runLogFile{Active: []agentRun{run}})
	os.WriteFile(path, b, 0o600)
	if l := loadRunLog(dir, false); len(l.endedList()) != 0 || len(l.takeFresh()) != 1 {
		t.Fatal("a crash is resumed, not kept as ended")
	}

	arConfig(t, true)
	root, _ := filepath.EvalSymlinks(t.TempDir())
	sdir := arEnv(t, runLogFile{Active: []agentRun{{Pane: "p1", Agent: "claude", Dir: root, Session: "s1", Seen: now}}})
	s := New(filepath.Join(sdir, "s.sock"), sdir)
	t.Cleanup(s.Stop)
	s.autoResume()
	if list := s.listEnded().Ended; len(list) != 1 || list[0].Why != proto.EndedServer {
		t.Fatalf("auto-resume off: %+v", list)
	}

	os.WriteFile(path, []byte(`{"active":[],"interrupted":[]}`), 0o600)
	if l := loadRunLog(dir, false); len(l.endedList()) != 0 {
		t.Fatal("an older log")
	}
}

// TestSessionsMarkEnded: a session whose agent ended unasked says why, by
// its id, or for an agent that reported none by its folder and time; one
// open in a pane again is not marked; a crash sorts first, a clean exit
// does not.
func TestSessionsMarkEnded(t *testing.T) {
	now := time.Now()
	out := []proto.SessionInfo{
		{Agent: "claude", ID: "a", Dir: "/w", Updated: now.Add(-time.Hour)},
		{Agent: "claude", ID: "b", Dir: "/w", Updated: now.Add(-2 * time.Hour), PaneID: "p3"},
		{Agent: "codex", ID: "c1", Dir: "/w", Updated: now.Add(-3 * time.Hour)},
		{Agent: "codex", ID: "c2", Dir: "/w", Updated: now.Add(-20 * time.Minute)},
		{Agent: "claude", ID: "d", Dir: "/w", Updated: now},
	}
	markEnded(out, []proto.EndedAgent{
		{ID: "e1", Agent: "claude", Session: "a", Why: proto.EndedKilled, Signal: "SIGKILL"},
		{ID: "e2", Agent: "claude", Session: "b", Why: proto.EndedExited, ExitCode: 1},
		{ID: "e3", Agent: "codex", Dir: "/w", Why: proto.EndedExited, ExitCode: 2, Started: now.Add(-time.Hour), Ended: now.Add(-15 * time.Minute)},
		{ID: "e4", Agent: "claude", Session: "d", Why: proto.EndedExited},
		{ID: "e5", Agent: "gemini", Session: "zz", Why: proto.EndedServer},
	})
	if out[0].Ended != "killed (SIGKILL)" || out[0].EndedID != "e1" || !out[0].EndedFailed {
		t.Fatalf("by id: %+v", out[0])
	}
	if out[1].Ended != "" {
		t.Fatalf("open again but marked: %+v", out[1])
	}
	if out[2].Ended != "" || out[3].Ended != "exited with code 2" {
		t.Fatalf("by folder and time: %+v / %+v", out[2], out[3])
	}
	if out[4].Ended != "exited" || out[4].EndedFailed {
		t.Fatalf("clean exit: %+v", out[4])
	}
	sortSessions(out)
	if out[0].ID != "c2" || out[1].ID != "a" || out[2].ID != "d" {
		t.Fatalf("crashes first, newest first: %s %s %s", out[0].ID, out[1].ID, out[2].ID)
	}
}
