package server

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/gitx"
	"github.com/Amitgb14/conch/internal/proto"
)

// apLog is the log while a test runs, where every autopilot note is
// written as well as broadcast.
type apLog struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *apLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *apLog) count(text string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Count(l.b.String(), text)
}

func apNotes(t *testing.T) *apLog {
	t.Helper()
	l := &apLog{}
	old := log.Writer()
	log.SetOutput(l)
	t.Cleanup(func() { log.SetOutput(old) })
	return l
}

// Every map in autopilot.json is there once it loads, whatever the file
// left out — one added later included.
func TestAutopilotLoadMakesEveryMap(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "autopilot.json"), []byte(`{}`), 0o600)
	a := loadAutopilot(dir)
	v := reflect.ValueOf(a.saved)
	for i := 0; i < v.NumField(); i++ {
		if f := v.Field(i); f.Kind() == reflect.Map && f.IsNil() {
			t.Errorf("%s is nil after loading", v.Type().Field(i).Name)
		}
	}
}

// A green pull request at a commit its check has not passed on waits, and
// says why, wherever that check cannot run: not checked out here, checked
// out at another commit, or a commit that could not be read. Nothing is
// started that could not run.
func TestAutopilotMergeGatesWhereTheCheckCannotRun(t *testing.T) {
	s, p, _, wt, _, _ := shipFixture(t)
	head := apGit(t, wt, "rev-parse", "HEAD")
	apGit(t, p.root, "branch", "side", head) // a branch with no worktree
	s.autopilot.set(proto.AutopilotPolicy{AutoMerge: true, Checks: map[string]string{p.id: "true"}})
	info := p.snapshot()

	for i := 0; i < 2; i++ {
		if why := s.mergeGates(p, info, "side", proto.PRInfo{Number: 9, Head: head}, head); !strings.Contains(why, "once side is checked out there") {
			t.Fatalf("no worktree: %q", why)
		}
	}
	// Pushed by hand, while the branch here has moved on.
	pushed := "f00ba12345678901234567890123456789012345"
	if why := s.mergeGates(p, info, "api/model", proto.PRInfo{Number: 8, Head: pushed}, pushed); !strings.Contains(why, "has not passed on f00ba12") {
		t.Fatalf("another commit: %q", why)
	}
	if why := s.mergeGates(p, info, "side", proto.PRInfo{Number: 9}, ""); !strings.Contains(why, "could not be read") {
		t.Fatalf("no commit: %q", why)
	}
	time.Sleep(100 * time.Millisecond)
	for _, pane := range s.list() {
		if strings.HasPrefix(pane.Name, proto.CheckPrefix) {
			t.Fatalf("started %s where it cannot run", pane.Name)
		}
	}
	// Passed there: nothing holds it.
	s.autopilot.checked(branchKey(p.id, "side"), head, true)
	if why := s.mergeGates(p, info, "side", proto.PRInfo{Number: 9, Head: head}, head); why != "" {
		t.Fatalf("passed: %q", why)
	}
}

// A check that cannot start says so once, not at every tick that asks for
// it, and again only after one has started.
func TestAutopilotCheckThatCannotStartIsSaidOnce(t *testing.T) {
	s, p, _, wt, _, _ := shipFixture(t)
	notes := apNotes(t)
	os.RemoveAll(wt) // git still lists it
	for i := 0; i < 3; i++ {
		s.autopilotStartCheck(p.id, "api/model", "true")
	}
	if n := notes.count("could not run the check"); n != 1 {
		t.Fatalf("said %d times", n)
	}
	s.unsay(branchKey(p.id, "api/model"), "check")
	s.autopilotStartCheck(p.id, "api/model", "true")
	if n := notes.count("could not run the check"); n != 2 {
		t.Fatalf("after it was unsaid: %d", n)
	}
}

func TestFinishedTurn(t *testing.T) {
	for _, tc := range []struct {
		prev, now string
		want      bool
	}{
		{proto.AgentWorking, proto.AgentDone, true},
		{proto.AgentBlocked, proto.AgentDone, true},
		{proto.AgentIdle, proto.AgentDone, true},
		{proto.AgentDone, proto.AgentDone, false},
		{"", proto.AgentDone, false},
		{proto.AgentWorking, proto.AgentIdle, false},
		{proto.AgentWorking, proto.AgentBlocked, false},
	} {
		if got := finishedTurn(tc.prev, tc.now); got != tc.want {
			t.Errorf("%q -> %q: %v", tc.prev, tc.now, got)
		}
	}
}

// A program that says it is done has finished its turn, whatever conch
// saw before: one told no while it waited on the person, or one quick
// enough to finish between two looks. Its branch ships.
func TestAutopilotFinishNotSeenWorking(t *testing.T) {
	for _, before := range []string{"blocked:kind=permission", ""} {
		t.Run("from "+before, func(t *testing.T) {
			s, p, _, wt, _, ghLog := shipFixture(t)
			e := agentPane(t, s, "p50", "claude", wt, `stty -echo; exec awk '{ printf "\033]7501;state=%s\007", $0; fflush() }'`)
			t.Cleanup(func() { e.p.Close() })
			e.mu.Lock()
			e.project = p
			e.mu.Unlock()
			state := func() string { s.observe(e); st, _ := agentState(e); return st }
			if before != "" {
				e.p.SendText(before+"\r", false)
				a5WaitFor(t, "blocked", func() bool { return state() == proto.AgentBlocked })
			} else if st := state(); st != proto.AgentIdle {
				t.Fatalf("starts %q", st)
			}
			e.p.SendText("done\r", false)
			a5WaitFor(t, "done", func() bool { return state() == proto.AgentDone })
			a5WaitFor(t, "the pull request", func() bool { return strings.Contains(readCalls(ghLog), "pr create") })
		})
	}
}

// Shipping reads the branch afresh, but in turn with a refresh already
// under way rather than beside it, where the slower read would be kept.
func TestAutopilotShipWaitsForARefreshUnderWay(t *testing.T) {
	s, p, _, _, _, ghLog := shipFixture(t)
	p.mu.Lock()
	p.refreshing = true
	was := p.lastStatus
	p.mu.Unlock()
	done := make(chan string, 1)
	go func() { done <- s.ship(p.id, "api/model") }()
	time.Sleep(200 * time.Millisecond)
	p.mu.Lock()
	read := p.lastStatus != was
	p.mu.Unlock()
	if read || readCalls(ghLog) != "" {
		t.Fatal("read the branch beside a refresh under way")
	}
	p.mu.Lock()
	p.refreshing = false
	p.mu.Unlock()
	if why := <-done; why != "" || !strings.Contains(readCalls(ghLog), "pr create") {
		t.Fatalf("after the refresh: %q %q", why, readCalls(ghLog))
	}
	p.mu.Lock()
	refreshing := p.refreshing
	p.mu.Unlock()
	if refreshing {
		t.Fatal("left marked as refreshing, so the ticker never refreshes it again")
	}
}

// A branch rebased onto its base is still on top of it after the base
// moves on — another pull request merged, fetched for another branch —
// and goes out once its check passes.
func TestAutopilotRewriteSurvivesTheBaseMoving(t *testing.T) {
	s, p, _, wt, origin := resolveFixture(t, "other.txt", "theirs\n")
	key := branchKey(p.id, "api/model")
	flag := filepath.Join(t.TempDir(), "go")
	s.autopilot.set(proto.AutopilotPolicy{AutoResolve: 1, Checks: map[string]string{p.id: "while [ ! -f " + flag + " ]; do sleep 0.05; done"}})
	s.autopilotResolve(key)
	s.autopilot.mu.Lock()
	rw := s.autopilot.saved.Rewrites[key]
	s.autopilot.mu.Unlock()
	if rw.At == "" || rw.At != apGit(t, wt, "rev-parse", "origin/main") {
		t.Fatalf("rebased onto %+v, origin/main is %s", rw, apGit(t, wt, "rev-parse", "origin/main"))
	}
	a5WaitFor(t, "the check started", func() bool { return s.checkBusy(key) })

	other := filepath.Join(t.TempDir(), "other")
	apGit(t, filepath.Dir(other), "clone", "-q", "-b", "main", origin, other)
	os.WriteFile(filepath.Join(other, "later.txt"), []byte("later\n"), 0o644)
	apGit(t, other, "add", ".")
	apGit(t, other, "-c", "user.name=o", "-c", "user.email=o@example.com", "commit", "-q", "-m", "main moves on")
	apGit(t, other, "push", "-q", "origin", "main")
	gitx.FreshBase(context.Background(), wt, "api/model", "main")
	if apGit(t, wt, "rev-parse", "origin/main") == rw.At {
		t.Fatal("origin/main did not move")
	}

	os.WriteFile(flag, nil, 0o644)
	a5WaitFor(t, "the rebased branch pushed", func() bool {
		return apGit(t, origin, "rev-parse", "api/model") == apGit(t, wt, "rev-parse", "HEAD")
	})
	// An older build's rewrite, with no commit, is still checked by name.
	if (rewrite{Base: "origin/main"}).onto() != "origin/main" {
		t.Fatal("an old rewrite has nothing to be on top of")
	}
}

// A request to commit that never reached the agent is made again at the
// next finish, rather than taken for one it ignored.
func TestAutopilotAskToCommitAgainAfterAFailedAsk(t *testing.T) {
	s, p, e, wt, _, _ := shipFixture(t)
	os.WriteFile(filepath.Join(wt, "loose.go"), []byte("package api\n"), 0o644)
	s.autopilot.prompt = func(proto.AgentPromptParams) (proto.AgentPromptResult, *proto.Error) {
		return proto.AgentPromptResult{}, proto.Errorf(proto.ErrBadRequest, "pane is restarting")
	}
	if why := s.ship(p.id, "api/model"); !strings.Contains(why, "failed: pane is restarting") {
		t.Fatalf("failed ask: %q", why)
	}
	s.autopilot.prompt = nil
	if why := s.ship(p.id, "api/model"); !strings.Contains(why, "asked") || strings.Contains(why, "goes out") {
		t.Fatalf("after the failed ask: %q", why)
	}
	a5WaitFor(t, "the request on the agent's screen", func() bool { return strings.Contains(screenText(e), "uncommitted changes") })
}

// A pull request that stopped conflicting while a pass was under way is
// not rebased on the strength of the pass's list, nor minded again.
func TestAutopilotResolveSkipsAConflictGone(t *testing.T) {
	s, p, _, wt, _ := resolveFixture(t, "other.txt", "theirs\n")
	key := branchKey(p.id, "api/model")
	head := apGit(t, wt, "rev-parse", "HEAD")
	for _, at := range []string{"gone", conflictUnwatched} {
		s.autopilot.mu.Lock()
		if at == "gone" {
			delete(s.autopilot.saved.Conflicted, key)
		} else {
			s.autopilot.saved.Conflicted[key] = at
		}
		s.autopilot.mu.Unlock()
		s.autopilotResolve(key)
		if got, ok := apConflicted(s, key); at == "gone" && ok || at != "gone" && got != at {
			t.Fatalf("%s: minded again at %q", at, got)
		}
		if apGit(t, wt, "rev-parse", "HEAD") != head {
			t.Fatalf("%s: rebased", at)
		}
	}
}

// Removing a project forgets what the autopilot kept about it in memory
// too: one added again under the same id is told its allowance is spent,
// and hears what was said before.
func TestAutopilotForgetsWhatItKeepsInMemory(t *testing.T) {
	a := loadAutopilot(t.TempDir())
	key, other := branchKey("p", "x"), branchKey("q", "x")
	for _, k := range []string{key, other} {
		a.capSaid[k], a.capSaid[ciCount(k)] = true, true
		a.said[k+"\x00resolve"] = "said"
		a.gateMemo[k] = gateResult{rev: "r"}
		a.recheck[k] = true
		a.lastCheck[k] = "p3"
		a.unshipped[k] = "why"
		a.saved.Resolves[k] = 1
	}
	a.forget("p")
	for name, has := range map[string]bool{
		"capSaid": a.capSaid[key] || a.capSaid[ciCount(key)], "said": a.said[key+"\x00resolve"] != "",
		"gateMemo": a.gateMemo[key].rev != "", "recheck": a.recheck[key], "lastCheck": a.lastCheck[key] != "",
		"unshipped": a.unshipped[key] != "", "resolves": a.saved.Resolves[key] != 0,
	} {
		if has {
			t.Errorf("%s kept for a removed project", name)
		}
	}
	if !a.capSaid[other] || a.said[other+"\x00resolve"] == "" || a.saved.Resolves[other] != 1 {
		t.Fatal("forgot another project's")
	}
}
