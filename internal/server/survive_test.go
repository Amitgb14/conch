package server

import (
	"sync/atomic"
	"testing"
	"time"
)

func noRepanic(t *testing.T) {
	t.Helper()
	was := repanic.Load()
	repanic.Store(false)
	t.Cleanup(func() { repanic.Store(was) })
}

// A panic on a goroutine of the server's own is that goroutine's: logged,
// its own deferred work done, and the caller told.
func TestSafelyTakesAPanic(t *testing.T) {
	noRepanic(t)
	cleaned := false
	if !safely("test", func() { defer func() { cleaned = true }(); panic("boom") }) {
		t.Fatal("a panic was not reported")
	}
	if !cleaned {
		t.Fatal("the goroutine's own defers did not run")
	}
	if safely("test", func() {}) {
		t.Fatal("no panic reported as one")
	}
	done := make(chan struct{})
	goSafe("test", func() { defer close(done); panic("boom") })
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("goSafe's goroutine never finished")
	}
}

// A loop of the server's own is started again after a panic, and not
// after the server stops.
func TestKeepStartsALoopAgain(t *testing.T) {
	noRepanic(t)
	was := keepAgain
	keepAgain = time.Millisecond
	t.Cleanup(func() { keepAgain = was })

	s := &Server{quit: make(chan struct{})}
	var runs atomic.Int32
	done := make(chan struct{})
	s.keep("test", func() {
		if runs.Add(1) < 3 {
			panic("boom")
		}
		close(done)
	})
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("ran %d times, want 3", runs.Load())
	}

	// Stopped: a loop that panics is not started again.
	keepAgain = time.Hour
	s2 := &Server{quit: make(chan struct{})}
	var runs2 atomic.Int32
	s2.keep("test", func() { runs2.Add(1); panic("boom") })
	time.Sleep(50 * time.Millisecond)
	close(s2.quit)
	time.Sleep(50 * time.Millisecond)
	if n := runs2.Load(); n != 1 {
		t.Fatalf("ran %d times, want 1", n)
	}
}

// A panic that leaves one of the server's own locks held ends the server,
// as before: carrying on would hang it instead.
func TestPanicHoldingALockStillEnds(t *testing.T) {
	noRepanic(t)
	was := lockWait
	lockWait = 50 * time.Millisecond
	t.Cleanup(func() { lockWait = was })

	s := &Server{}
	s.noteLive(true)
	t.Cleanup(func() { s.noteLive(false) })
	s.mu.Lock()
	defer s.mu.Unlock()
	if name := wedged(); name != "the server's lock" {
		t.Fatalf("wedged() = %q", name)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("a panic that left the server's lock held was swallowed")
		}
	}()
	safely("test", func() { panic("boom") })
}

// With its locks free, a panic is taken and the server carries on.
func TestPanicWithLocksFreeCarriesOn(t *testing.T) {
	noRepanic(t)
	s := &Server{}
	s.noteLive(true)
	t.Cleanup(func() { s.noteLive(false) })
	if name := wedged(); name != "" {
		t.Fatalf("wedged() = %q with every lock free", name)
	}
	if !safely("test", func() { panic("boom") }) {
		t.Fatal("not reported")
	}
}
