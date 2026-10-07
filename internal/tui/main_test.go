package tui

import (
	"os/exec"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// Nothing in this suite may reach out of the process. A test once put its
// own fixture on the developer's clipboard — through the OSC 52 escape
// conch prints, which the terminal running `go test` obligingly honoured —
// and the same would go for a browser window, a desktop notification or
// the bell. Every one of those is a variable in the code precisely so this
// can turn it off for all of them at once; a test that wants to check one
// replaces it again for itself.
func TestMain(m *testing.M) {
	putClipboard = func(text string) { lastClipboard = text }
	openInBrowser = func(url string) error { lastOpened = url; return nil }
	runOutward = func(*exec.Cmd) {}
	startOutward = func(*exec.Cmd) error { return nil }
	ringBell = func() { bells++ }
	// The pointer shape is an escape to the real terminal: a test that
	// sent it would leave the developer's own pointer as a hand.
	setPointerShape = func(shape string) { pointerShapes = append(pointerShapes, shape) }
	signalProcess = func(pid int, sig syscall.Signal) error { signalled = append(signalled, pid); return nil }
	// Each wheel event a second after the last, so a test's notches are
	// notches: one sent straight after another is not a trackpad's burst.
	var wheelTicks atomic.Int64
	wheelClock = func() time.Time { return time.Unix(wheelTicks.Add(1), 0) }
	m.Run()
}

// What the suite's stubs saw last, for tests that care.
var (
	lastClipboard string
	lastOpened    string
	bells         int
	signalled     []int
	// pointerShapes is every shape asked for, in order: the point of the
	// feature is that it is asked for once per crossing, not per motion.
	pointerShapes []string
)
