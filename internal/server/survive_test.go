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

// panicsTaken counts the panics recovered from here on, for a test to
// wait until a goroutine's recover is over.
func panicsTaken(t *testing.T) <-chan string {
	t.Helper()
	ch := make(chan string, 64)
	was := tookPanic
	tookPanic = func(what string) { ch <- what }
	t.Cleanup(func() { tookPanic = was })
	return ch
}

func waitTaken(t *testing.T, ch <-chan string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(10 * time.Second):
		t.Fatal("the panic was never taken")
	}
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
	// On a goroutine of its own, waited for until its recover is over:
	// waiting for less once let the test end first and put repanic back,
	// and the late recover panicked again and ended the whole test binary
	// — now and then, on CI.
	taken := panicsTaken(t)
	goSafe("test", func() { panic("boom") })
	waitTaken(t, taken)
}

// A loop of the server's own is started again after a panic, and not
// after the server stops.
func TestKeepStartsALoopAgain(t *testing.T) {
	noRepanic(t)
	was := keepAgain
	keepAgain = time.Millisecond
	t.Cleanup(func() { keepAgain = was })
	taken := panicsTaken(t)

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
	waitTaken(t, taken)
	waitTaken(t, taken)

	// Stopped: a loop that panics is not started again.
	keepAgain = time.Hour
	s2 := &Server{quit: make(chan struct{})}
	var runs2 atomic.Int32
	s2.keep("test", func() { runs2.Add(1); panic("boom") })
	waitTaken(t, taken)
	close(s2.quit)
	time.Sleep(50 * time.Millisecond) // were it to start again, it would by now
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
