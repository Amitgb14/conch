package pane

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

// pty.Open must name the terminal it opened. creack/pty v1.1.20–v1.1.24
// turned the pointer for TIOCGPTN into a uintptr before the call that made
// the system call, so a GC shrinking the goroutine's stack in between had
// the kernel write the pty's number into the old stack: Open read 0, opened
// /dev/pts/0 — another pane's terminal — and the start failed with
// TIOCSCTTY's EPERM, "fork/exec …: operation not permitted". Seen in CI as
// TestCreatorEveryWayFromAfar; upstream saw about one start in 25,000 fail.
// This makes a shrink likely on every Open, as creack/pty's own test does
// (creack/pty#216), and fails within milliseconds on the broken versions.
func TestPtyOpenNamesItsOwnTerminal(t *testing.T) {
	// Hold pts/0 (or whichever comes first) so a lost write shows up as a
	// wrong name instead of passing because the right one was 0 anyway.
	p0, t0, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer p0.Close()
	defer t0.Close()

	var stop atomic.Bool
	var gc sync.WaitGroup
	gc.Add(1)
	go func() {
		defer gc.Done()
		for !stop.Load() {
			runtime.GC()
		}
	}()
	var (
		mu    sync.Mutex
		wrong []string
		opens atomic.Int64
	)
	deadline := time.Now().Add(2 * time.Second)
	var workers sync.WaitGroup
	for w := 0; w < 8; w++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for time.Now().Before(deadline) && !stop.Load() {
				// A deep stack, given back, leaves the goroutine one the GC
				// may shrink while Open runs.
				deepen(2000)
				opens.Add(1)
				if msg := openNamesItself(); msg != "" {
					mu.Lock()
					wrong = append(wrong, msg)
					mu.Unlock()
					stop.Store(true)
				}
			}
		}()
	}
	workers.Wait()
	stop.Store(true)
	gc.Wait()
	for _, msg := range wrong {
		t.Error(msg)
	}
	t.Logf("%d opens under GC pressure, %d wrong", opens.Load(), len(wrong))
}

// openNamesItself opens a pty and says what is wrong if its tty is not the
// master's own slave, or "".
func openNamesItself() string {
	ptmx, tty, err := pty.Open()
	if err != nil {
		return "open: " + err.Error()
	}
	defer ptmx.Close()
	defer tty.Close()
	n, err := unix.IoctlGetUint32(int(ptmx.Fd()), unix.TIOCGPTN)
	if err != nil {
		return "TIOCGPTN: " + err.Error()
	}
	var st unix.Stat_t
	if err := unix.Fstat(int(tty.Fd()), &st); err != nil {
		return "fstat: " + err.Error()
	}
	want := fmt.Sprintf("/dev/pts/%d", n)
	if slave := unix.Minor(uint64(st.Rdev)); tty.Name() != want || slave != n {
		return fmt.Sprintf("master is pts %d, but the tty opened is %s (pts %d)", n, tty.Name(), slave)
	}
	return ""
}

//go:noinline
func deepen(depth int) byte {
	var pad [8]byte
	pad[depth&7] = byte(depth)
	if depth > 0 {
		pad[0] += deepen(depth - 1)
	}
	return pad[depth&7]
}
