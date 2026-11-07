package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// A stopping server waits for the git work it started in the background:
// Run returned while a refresh still wrote in a checkout, and a test's
// folders were removed from under it (TestMoveProjectBetweenServers, on
// Linux CI).
func TestSettleWaitsForBackgroundWork(t *testing.T) {
	pm := &projectManager{}
	release := make(chan struct{})
	var finished atomic.Bool
	if !pm.background(func() {
		<-release
		finished.Store(true)
	}) {
		t.Fatal("background refused before the server stopped")
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		close(release)
	}()
	if !pm.settle(10 * time.Second) {
		t.Fatal("settle gave up on work that finishes")
	}
	if !finished.Load() {
		t.Fatal("settle returned before the work finished")
	}

	// Nothing new starts once stopping.
	ran := make(chan struct{}, 1)
	if pm.background(func() { ran <- struct{}{} }) {
		t.Fatal("background started work after settle")
	}
	select {
	case <-ran:
		t.Fatal("refused work ran anyway")
	case <-time.After(20 * time.Millisecond):
	}
}

// Work that hangs does not hold the server up past the bound.
func TestSettleGivesUpOnHungWork(t *testing.T) {
	pm := &projectManager{}
	hang := make(chan struct{})
	defer close(hang)
	pm.background(func() { <-hang })
	start := time.Now()
	if pm.settle(30 * time.Millisecond) {
		t.Fatal("settle said hung work finished")
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("settle waited %s", d)
	}
}

// Settling with nothing running returns at once, and settling twice is fine.
func TestSettleWithNothingRunning(t *testing.T) {
	pm := &projectManager{}
	for i := 0; i < 2; i++ {
		if !pm.settle(time.Second) {
			t.Fatalf("settle %d with nothing running", i+1)
		}
	}
}

// Run itself waits: a refresh's git still running when the server is
// stopped finishes before Run returns. Without the wait the fake's "done"
// was written after Run had returned — into folders a test had removed.
func TestRunWaitsForARefreshInFlight(t *testing.T) {
	a5IsolateEnv(t)
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git")
	}
	dir, err := os.MkdirTemp("", "st")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	dir, _ = filepath.EvalSymlinks(dir)
	repo := filepath.Join(dir, "repo")
	a5GitRepo(t, repo)

	// A git whose status takes a while once the mark's folder exists.
	bin := filepath.Join(dir, "bin")
	mark := filepath.Join(dir, "mark")
	os.MkdirAll(bin, 0o755)
	script := "#!/bin/sh\n" +
		"case \"$*\" in *status*) if [ -d " + mark + " ]; then touch " + mark + "/started; sleep 1; touch " + mark + "/done; fi;; esac\n" +
		"exec " + realGit + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	s := New(filepath.Join(dir, "s.sock"), dir)
	ran := make(chan struct{})
	go func() { s.Run(); close(ran) }()
	stopped := false
	defer func() {
		if !stopped {
			s.Stop()
			<-ran
		}
	}()
	p, err := s.projects.add(repo, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(mark, 0o755); err != nil {
		t.Fatal(err)
	}
	s.projects.request(p) // the ticker refreshes it in the background
	for i := 0; ; i++ {
		if _, err := os.Stat(filepath.Join(mark, "started")); err == nil {
			break
		}
		if i == 500 {
			t.Fatal("no refresh started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	s.Stop()
	select {
	case <-ran:
	case <-time.After(settleTimeout + 10*time.Second):
		t.Fatal("Run did not return")
	}
	stopped = true
	if _, err := os.Stat(filepath.Join(mark, "done")); err != nil {
		t.Fatal("Run returned while the refresh's git was still running")
	}
}

// Work refused because the server is stopping clears the flag that said it
// was under way: left set, refreshNow waited its full timeout on a refresh
// that would never come.
func TestRefusedBackgroundClearsItsFlag(t *testing.T) {
	a5IsolateEnv(t)
	s, dir := a5Server(t)
	pm := s.projects
	repo := filepath.Join(dir, "repo")
	a5GitRepo(t, repo)
	p, err := pm.add(repo, false)
	if err != nil {
		t.Fatal(err)
	}
	s.Stop()
	pm.settle(settleTimeout)

	p.mu.Lock()
	p.pending, p.refreshing = true, false
	p.lastStatus = time.Now()
	p.prPending, p.prFetching = true, false
	p.mu.Unlock()
	pm.maybeRefresh(p)
	pm.maybeFetchPRs(p)
	p.mu.Lock()
	refreshing, prFetching := p.refreshing, p.prFetching
	p.mu.Unlock()
	if refreshing || prFetching {
		t.Fatalf("refused work left refreshing=%v prFetching=%v", refreshing, prFetching)
	}

	start := time.Now()
	pm.refreshNow(p)
	if d := time.Since(start); d > gitTimeout/2 {
		t.Fatalf("refreshNow waited %s", d)
	}
}
