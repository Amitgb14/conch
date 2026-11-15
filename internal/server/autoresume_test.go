package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	conchclient "github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
)

// arEnv is an empty home with no real agents on PATH, and a short config
// directory (a unix socket lives there) holding the run log given.
func arEnv(t *testing.T, f runLogFile) string {
	t.Helper()
	home, _ := filepath.EvalSymlinks(t.TempDir())
	for _, k := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "XDG_DATA_HOME", "CONCH_SOCKET", "CONCH_PANE_ID", "CONCH_HOME"} {
		t.Setenv(k, "")
	}
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("PATH", "/usr/bin:/bin")
	dir, err := os.MkdirTemp("", "ar")
	if err != nil {
		t.Fatal(err)
	}
	dir, _ = filepath.EvalSymlinks(dir)
	t.Cleanup(func() { os.RemoveAll(dir) })
	b, _ := json.Marshal(f)
	if err := os.WriteFile(filepath.Join(dir, "agent-runs.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	gap := autoResumeGap
	autoResumeGap = 0
	t.Cleanup(func() { autoResumeGap = gap })
	return dir
}

func arConfig(t *testing.T, noAutoResume bool) {
	t.Helper()
	load := loadAutoResumeConfig
	loadAutoResumeConfig = func() (config.Config, error) {
		var c config.Config
		c.Agents.NoAutoResume = noAutoResume
		return c, nil
	}
	t.Cleanup(func() { loadAutoResumeConfig = load })
}

func arPanes(s *Server) []proto.PaneInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []proto.PaneInfo
	for _, e := range s.panes {
		out = append(out, e.info())
	}
	return out
}

// TestRunLogFreshRuns: what the previous server was running when it went
// is handed over once for resuming — unless it was stopped on purpose, or
// this is a reload that kept the panes — and the stop is remembered only
// for the next server. A log from before accounts loads as it did.
func TestRunLogFreshRuns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent-runs.json")
	now := time.Now()
	write := func(stopped bool) {
		b, _ := json.Marshal(runLogFile{
			Active: []agentRun{
				{Pane: "p1", Agent: "claude", Dir: "/a", Session: "s1", Account: "work", Seen: now},
				{Pane: "p2", Agent: "codex", Dir: "/b", Seen: now.Add(-8 * 24 * time.Hour)}, // too old to offer
			},
			Interrupted: []agentRun{{Pane: "p3", Agent: "codex", Dir: "/c", Seen: now.Add(-time.Hour)}},
			Stopped:     stopped,
		})
		os.WriteFile(path, b, 0o600)
	}

	write(false)
	l := loadRunLog(dir, false)
	fresh := l.takeFresh()
	if len(fresh) != 1 || fresh[0].Pane != "p1" || fresh[0].Account != "work" {
		t.Fatalf("fresh after a crash: %+v", fresh)
	}
	if again := l.takeFresh(); len(again) != 0 {
		t.Fatalf("fresh is handed over once: %+v", again)
	}
	if runs := l.interruptedRuns(); len(runs) != 2 {
		t.Fatalf("still offered in Sessions until resumed: %+v", runs)
	}

	write(true)
	l = loadRunLog(dir, false)
	if fresh := l.takeFresh(); len(fresh) != 0 {
		t.Fatalf("a server stopped on purpose is not undone: %+v", fresh)
	}
	if runs := l.interruptedRuns(); len(runs) != 2 {
		t.Fatalf("its agents are offered instead: %+v", runs)
	}
	if b, _ := os.ReadFile(path); strings.Contains(string(b), `"stopped"`) {
		t.Fatalf("the stop belongs to the previous server alone: %s", b)
	}

	write(false)
	if fresh := loadRunLog(dir, true).takeFresh(); len(fresh) != 0 {
		t.Fatalf("a reload keeps its panes, nothing to resume: %+v", fresh)
	}

	l = loadRunLog(dir, false)
	l.markStopped()
	var f runLogFile
	if b, _ := os.ReadFile(path); json.Unmarshal(b, &f) != nil || !f.Stopped {
		t.Fatalf("markStopped: %s", b)
	}

	// A log written before runs carried an account.
	os.WriteFile(path, []byte(`{"active":[{"pane":"p9","agent":"claude","dir":"/d","started":"2026-11-14T10:00:00Z","seen":"`+
		now.Format(time.RFC3339Nano)+`"}],"interrupted":[]}`), 0o600)
	if fresh := loadRunLog(dir, false).takeFresh(); len(fresh) != 1 || fresh[0].Account != "" {
		t.Fatalf("an older log: %+v", fresh)
	}
}

// TestRunLogNotesAccount: the account an agent runs on and its pane's name
// are recorded, so it comes back on that account and under that name.
func TestRunLogNotesAccount(t *testing.T) {
	l := loadRunLog(t.TempDir(), false)
	info := proto.PaneInfo{ID: "p1", State: proto.PaneRunning, Account: "work", Agent: &proto.AgentStatus{Name: "claude"}}
	l.note(info, "/d", time.Now())
	if l.active["p1"].Account != "work" {
		t.Fatalf("account not noted: %+v", l.active["p1"])
	}
	info.Account = "home"
	l.note(info, "/d", time.Now())
	if l.active["p1"].Account != "home" {
		t.Fatalf("a changed account is noted at once: %+v", l.active["p1"])
	}
	info.Name = "reviewer"
	l.note(info, "/d", time.Now())
	if l.active["p1"].Name != "reviewer" {
		t.Fatalf("a rename is noted at once: %+v", l.active["p1"])
	}
}

// TestAutoResumeAfterACrash: a server starting after one that went
// unasked resumes each agent it was running, on its own conversation, with
// nobody connected — and skips what it cannot: a folder that is gone, and
// the same conversation twice. Those it cannot stay in Sessions.
func TestAutoResumeAfterACrash(t *testing.T) {
	arConfig(t, false)
	now := time.Now()
	root, _ := filepath.EvalSymlinks(t.TempDir())
	api, web := filepath.Join(root, "api"), filepath.Join(root, "web")
	os.MkdirAll(api, 0o755)
	os.MkdirAll(web, 0o755)
	gone := filepath.Join(root, "gone")
	dir := arEnv(t, runLogFile{Active: []agentRun{
		{Pane: "p1", Agent: "claude", Dir: gone, Session: "lost", Started: now.Add(-time.Hour), Seen: now},
		{Pane: "p2", Agent: "claude", Dir: api, Session: "s-api", Started: now.Add(-time.Hour), Seen: now},
		{Pane: "p3", Agent: "claude", Dir: api, Session: "s-api", Started: now.Add(-time.Hour), Seen: now}, // the same again
		{Pane: "p4", Agent: "claude", Dir: web, Session: "s-web", Name: "reviewer", Started: now.Add(-time.Hour), Seen: now},
	}})
	s := New(filepath.Join(dir, "s.sock"), dir)
	go s.Run()
	t.Cleanup(s.StopAndWait)

	// They are resumed in order, so the last one there means every one
	// before it was decided.
	deadline := time.Now().Add(20 * time.Second)
	var panes []proto.PaneInfo
	for {
		panes = arPanes(s)
		last := false
		for _, p := range panes {
			last = last || strings.Contains(strings.Join(p.Command, " "), "--resume s-web")
		}
		if last {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("not resumed: %+v", panes)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(panes) != 2 {
		t.Fatalf("want s-api and s-web once each, got %d panes: %+v", len(panes), panes)
	}
	for _, p := range panes {
		cmd := strings.Join(p.Command, " ")
		if !(p.Cwd == api && strings.Contains(cmd, "--resume s-api")) && !(p.Cwd == web && strings.Contains(cmd, "--resume s-web")) {
			t.Fatalf("resumed in the wrong place: %s in %s", cmd, p.Cwd)
		}
		if p.Cwd == web && p.Name != "reviewer" {
			t.Fatalf("back under another name: %q", p.Name)
		}
	}
	runs := s.runs.interruptedRuns()
	if len(runs) != 1 || runs[0].Dir != gone {
		t.Fatalf("only the one that could not come back stays offered: %+v", runs)
	}
}

// TestAutoResumeTurnedOff: with [agents] no_auto_resume, nothing starts;
// the runs wait in Sessions as before.
func TestAutoResumeTurnedOff(t *testing.T) {
	arConfig(t, true)
	root, _ := filepath.EvalSymlinks(t.TempDir())
	dir := arEnv(t, runLogFile{Active: []agentRun{{Pane: "p1", Agent: "claude", Dir: root, Session: "s1", Seen: time.Now()}}})
	s := New(filepath.Join(dir, "s.sock"), dir)
	t.Cleanup(s.Stop)
	s.autoResume() // no adapters needed: it must not get as far as starting one
	if p := arPanes(s); len(p) != 0 {
		t.Fatalf("started with auto-resume off: %+v", p)
	}
	if runs := s.runs.interruptedRuns(); len(runs) != 1 {
		t.Fatalf("left for Sessions: %+v", runs)
	}
}

// TestServerStopIsRemembered: conch server stop, through the protocol,
// writes that it was asked to, so the next server leaves its agents for I.
func TestServerStopIsRemembered(t *testing.T) {
	arConfig(t, false)
	dir := arEnv(t, runLogFile{})
	sock := filepath.Join(dir, "s.sock")
	s := New(sock, dir)
	go s.Run()
	t.Cleanup(s.StopAndWait)
	var c *conchclient.Client
	var err error
	for range 100 {
		if c, err = conchclient.Dial(sock, "test"); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.Call(ctx, proto.MethodServerStop, nil, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.ran:
	case <-time.After(10 * time.Second):
		t.Fatal("the server did not stop")
	}
	var f runLogFile
	if b, _ := os.ReadFile(filepath.Join(dir, "agent-runs.json")); json.Unmarshal(b, &f) != nil || !f.Stopped {
		t.Fatalf("the stop is not recorded: %s", b)
	}
}
