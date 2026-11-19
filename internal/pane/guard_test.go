package pane

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// The server ended twice on these: a program's scroll region (or left and
// right margins) past the edge of a screen that had just shrunk, then a
// scroll or an insertion inside it. Each must leave the server standing,
// with repanic on as in every test, so a panic here fails the test.
func TestMarginsPastTheScreen(t *testing.T) {
	for _, tc := range []struct{ name, seq string }{
		{"scroll down (2026-11-19)", "\x1b[1;30r\x1b[T"},
		{"scroll up", "\x1b[1;30r\x1b[S"},
		{"insert lines", "\x1b[1;30r\x1b[5H\x1b[3L"},
		{"delete lines (the earlier crash)", "\x1b[1;40r\x1b[5H\x1b[3M"},
		{"index at the bottom", "\x1b[1;30r\x1b[26H\n\n\n"},
		{"reverse index", "\x1b[1;30r\x1b[H\x1bM\x1bM"},
		{"bottom far past the screen", "\x1b[1;99999r\x1b[T\x1b[S"},
		{"region wholly below the screen", "\x1b[28;30r\x1b[T\x1b[S"},
		{"right margin past the screen", "\x1b[?69h\x1b[1;100s\x1b[1;5H\x1b[3@\x1b[3P\x1b[T"},
		{"both", "\x1b[?69h\x1b[1;100s\x1b[1;50r\x1b[T\x1b[S\x1b[5L\x1b[5M"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := textPane(80, 26)
			p.feed(numbered(0, 26, 80))
			p.feed(tc.seq)
			p.feed("\x1b[r\x1b[Hstill here")
			if got := p.PlainLines()[0]; !strings.HasPrefix(got, "still here") {
				t.Fatalf("first line %q, want the pane to carry on", got)
			}
		})
	}
}

// Clamped, the region still works — to the screen's bottom, as xterm
// does — rather than being refused. Scrolling down inside rows 5 to "30"
// of 26 moves rows 5..25 down one and leaves the rows above alone. It
// also says the handler is still given the parser's own parameters: if
// the emulator stops handing those over, the clamp changes a copy, the
// emulator sets row 30, and this panics.
func TestMarginsClampedStillScroll(t *testing.T) {
	p := textPane(40, 26)
	for i := range 26 {
		if i > 0 {
			p.feed("\r\n")
		}
		p.feed(fmt.Sprintf("row %d", i))
	}
	p.feed("\x1b[5;30r\x1b[T")
	lines := p.PlainLines()
	for i, want := range map[int]string{0: "row 0", 3: "row 3", 4: "", 5: "row 4", 25: "row 24"} {
		if got := strings.TrimRight(lines[i], " "); got != want {
			t.Errorf("line %d = %q, want %q", i, got, want)
		}
	}
}

// Through a real pane's read loop, as the crash was: the program's output
// is the only way in.
func TestMarginsPastTheScreenFromAProgram(t *testing.T) {
	p := startShell(t) // 60×10
	if err := p.SendText(`printf '\033[1;30r\033[T\033[S\033[r'; echo carried-$((6*7))`, false); err != nil {
		t.Fatal(err)
	}
	if err := p.SendKeys([]string{"enter"}); err != nil {
		t.Fatal(err)
	}
	waitScreen(t, p, "carried-42")
}

func noRepanic(t *testing.T) {
	t.Helper()
	was := repanic.Load()
	repanic.Store(false)
	t.Cleanup(func() { repanic.Store(was) })
}

// A panic the clamps do not foresee is still the pane's alone: logged, the
// screen reset, and the next output drawn.
func TestEmulatorPanicIsThePanes(t *testing.T) {
	noRepanic(t)
	p := textPane(40, 10)
	p.emu.RegisterCsiHandler('z', func(ansi.Params) bool { panic("emulator bug") })
	p.feed("\x1b[2;5rbefore\x1b[zafter")
	p.feed("\r\nnext")
	got := strings.Join(p.PlainLines(), "\n")
	if !strings.Contains(got, "before") || !strings.Contains(got, "next") {
		t.Fatalf("the pane stopped drawing after the panic:\n%s", got)
	}
	// The reset put the margins back: a scroll now moves the whole screen.
	if p.emu.Height() != 10 || p.emu.Width() != 40 {
		t.Fatalf("screen is %dx%d after the reset, want 40x10", p.emu.Width(), p.emu.Height())
	}
}

// A panic in conch's own handling of a read releases the emulator's lock,
// or the pane — and every caller that waits on it — would hang instead.
func TestTookReleasesTheEmulatorOnPanic(t *testing.T) {
	noRepanic(t)
	p := textPane(40, 10)
	p.took([]byte("x"), &oscScanner{}, nil) // a nil filter panics inside the lock
	if !p.emuMu.TryLock() {
		t.Fatal("emuMu is still held after the panic")
	}
	p.emuMu.Unlock()
}

// In tests a recovered panic is raised again, so a bug is not passed off
// as a log line.
func TestRepanicInTests(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("the panic was swallowed in a test")
		}
	}()
	p := textPane(40, 10)
	p.emu.RegisterCsiHandler('z', func(ansi.Params) bool { panic("emulator bug") })
	p.feed("\x1b[z")
}
