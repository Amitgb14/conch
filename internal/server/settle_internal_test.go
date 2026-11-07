package server

import (
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
