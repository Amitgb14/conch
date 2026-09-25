package sessions

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// a7Env is an Env with a short grace, so a test never waits a real one out.
func a7Env(t *testing.T, home, grace string) Env {
	t.Cleanup(forgetSlow)
	return Env{Home: home, Getenv: func(k string) string {
		if k == "CONCH_SESSION_GRACE" {
			return grace
		}
		return ""
	}}
}

func TestSlowListAnswersInTime(t *testing.T) {
	e := a7Env(t, t.TempDir(), "2s")
	got, complete := slowList(e, "k", func() []Session { return []Session{{Agent: "devin", ID: "a"}} })
	if !complete || a6IDs(got) != "devin:a" {
		t.Fatalf("got %q complete=%v", a6IDs(got), complete)
	}
}

func TestSlowListTooSlowIsIncomplete(t *testing.T) {
	e := a7Env(t, t.TempDir(), "20ms")
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	start := time.Now()
	got, complete := slowList(e, "k", func() []Session {
		<-release
		return []Session{{Agent: "devin", ID: "late"}}
	})
	if complete || got != nil {
		t.Fatalf("got %q complete=%v, want nothing and incomplete", a6IDs(got), complete)
	}
	if took := time.Since(start); took > time.Second {
		t.Fatalf("waited %v, want about the grace", took)
	}
}

// A store that answered before, then hangs, is served its last answer — and
// the list still says it is incomplete, so the caller asks again.
func TestSlowListServesTheLastAnswer(t *testing.T) {
	e := a7Env(t, t.TempDir(), "20ms")
	if _, complete := slowList(e, "k", func() []Session { return []Session{{Agent: "devin", ID: "a"}} }); !complete {
		t.Fatal("first answer should be complete")
	}
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	got, complete := slowList(e, "k", func() []Session { <-release; return nil })
	if complete || a6IDs(got) != "devin:a" {
		t.Fatalf("got %q complete=%v, want the last answer and incomplete", a6IDs(got), complete)
	}
}

// A hung refresh is joined, not started again, however many lists ask.
func TestSlowListOneRefreshAtATime(t *testing.T) {
	e := a7Env(t, t.TempDir(), "20ms")
	var calls atomic.Int32
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	fn := func() []Session { calls.Add(1); <-release; return nil }
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); slowList(e, "k", fn) }()
	}
	wg.Wait()
	if n := calls.Load(); n != 1 {
		t.Fatalf("ran the store %d times, want 1", n)
	}
}

// Once a late answer lands it is there for the next list, which is complete.
func TestSlowListLateAnswerLands(t *testing.T) {
	e := a7Env(t, t.TempDir(), "20ms")
	release := make(chan struct{})
	fn := func() []Session { <-release; return []Session{{Agent: "devin", ID: "late"}} }
	if _, complete := slowList(e, "k", fn); complete {
		t.Fatal("want the first list incomplete")
	}
	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for {
		got, complete := slowList(e, "k", fn)
		if complete && a6IDs(got) == "devin:late" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("late answer never landed: %q complete=%v", a6IDs(got), complete)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Answers are kept per store and per set of directories.
func TestSlowKeyIsPerStoreAndDirs(t *testing.T) {
	e := Env{Home: "/h"}
	keys := map[string]bool{
		slowKey("devin", e, []string{"/a"}):               true,
		slowKey("devin", e, []string{"/b"}):               true,
		slowKey("opencode", e, []string{"/a"}):            true,
		slowKey("devin", e, []string{"/a", "/b"}):         true,
		slowKey("devin", Env{Home: "/g"}, []string{"/a"}): true,
	}
	if len(keys) != 5 {
		t.Fatalf("keys collide: %d distinct, want 5", len(keys))
	}
}

func TestGraceOf(t *testing.T) {
	for _, c := range []struct {
		set  string
		want time.Duration
	}{{"", slowGrace}, {"nonsense", slowGrace}, {"0s", slowGrace}, {"-1s", slowGrace}, {"250ms", 250 * time.Millisecond}} {
		e := Env{Getenv: func(string) string { return c.set }}
		if got := graceOf(e); got != c.want {
			t.Errorf("grace %q: got %v, want %v", c.set, got, c.want)
		}
	}
	if got := graceOf(Env{}); got != slowGrace { // no Getenv at all
		t.Errorf("empty env: got %v", got)
	}
}

// The whole point: a hung Devin CLI does not hold up the agents whose
// sessions are files, and the list says it is incomplete.
func TestListStatusHungDevinDoesNotBlock(t *testing.T) {
	home := t.TempDir()
	work := t.TempDir() // devin runs in the folder, so it has to exist
	a7Session(t, home, work, "claude-one")
	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	// A devin that never answers within the grace.
	if err := os.WriteFile(filepath.Join(bin, "devin"), []byte("#!/bin/sh\nsleep 5\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(forgetSlow)
	e := Env{Home: home, Getenv: func(k string) string {
		switch k {
		case "PATH":
			return bin
		case "CONCH_SESSION_GRACE":
			return "50ms"
		}
		return ""
	}}
	start := time.Now()
	got, complete := ListStatus(e, []string{work}, 0)
	took := time.Since(start)
	if complete {
		t.Error("want the list marked incomplete while devin hangs")
	}
	if a6IDs(got) != "claude:claude-one" {
		t.Errorf("got %q, want the claude session anyway", a6IDs(got))
	}
	if took > 5*time.Second {
		t.Errorf("waited %v for a hung devin", took)
	}
	// List itself still works, and drops the flag.
	if ids := a6IDs(List(e, []string{work}, 0)); !strings.Contains(ids, "claude:claude-one") {
		t.Errorf("List: got %q", ids)
	}
}

// Nothing slow in the way: a list is complete.
func TestListStatusCompleteWithoutSlowStores(t *testing.T) {
	home := t.TempDir()
	work := t.TempDir()
	a7Session(t, home, work, "only-one")
	a6NoTools(t)
	e := a7Env(t, home, "2s")
	got, complete := ListStatus(e, []string{work}, 0)
	if !complete || a6IDs(got) != "claude:only-one" {
		t.Fatalf("got %q complete=%v", a6IDs(got), complete)
	}
	if _, complete := ListStatus(e, nil, 0); !complete { // no directories at all
		t.Error("an empty list should still be complete")
	}
}

// a7Session writes one Claude session for dir under home.
func a7Session(t *testing.T, home, dir, id string) {
	t.Helper()
	proj := filepath.Join(home, ".claude", "projects", claudeDirName(dir))
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","cwd":"` + dir + `","timestamp":"2026-09-20T10:00:00Z","message":{"content":"hello there"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, id+".jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
}

// List answers from the stores themselves. The background path can hand a
// caller an answer that was started before it asked — which is right for a
// list on screen, and wrong for search, sharing or usage, which want what
// is there now. This is the bug that made a store's own test flake.
func TestListDoesNotServeAnOlderAnswer(t *testing.T) {
	home := t.TempDir()
	work := t.TempDir()
	a7Session(t, home, work, "claude-one")
	t.Cleanup(forgetSlow)

	// A slow store whose answer changes between calls.
	var mu sync.Mutex
	answer := "first"
	slow := func() []Session {
		mu.Lock()
		id := answer
		mu.Unlock()
		time.Sleep(50 * time.Millisecond) // long enough to still be running
		return []Session{{Agent: "devin", ID: id}}
	}
	key := slowKey("devin", Env{Home: home}, []string{work})
	e := Env{Home: home, Getenv: func(k string) string {
		if k == "CONCH_SESSION_GRACE" {
			return "2s"
		}
		return ""
	}}

	// The background path: a second call while the first is still running
	// joins it and gets that answer.
	go slowList(e, key, slow)
	time.Sleep(10 * time.Millisecond)
	mu.Lock()
	answer = "second"
	mu.Unlock()
	if got, _ := slowList(e, key, slow); len(got) != 1 || got[0].ID != "first" {
		t.Fatalf("the background path should join the running look: %v", got)
	}

	// List asks again and waits, so it sees the new answer.
	forgetSlow()
	mu.Lock()
	answer = "third"
	mu.Unlock()
	found := List(e, []string{work}, 0)
	var ids []string
	for _, s := range found {
		ids = append(ids, s.Agent+":"+s.ID)
	}
	if !strings.Contains(strings.Join(ids, ","), "claude:claude-one") {
		t.Fatalf("the file stores are still read: %v", ids)
	}
	// And nothing of the slow store's was cached into it.
	if _, complete := ListStatus(e, []string{work}, 0); !complete {
		t.Error("a list with nothing slow to wait for should be complete")
	}
}
