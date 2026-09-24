package pane

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
)

func screen(lines ...string) []string { return lines }

// scrolledBy is what tells scrolling from a repaint, so it decides whether
// anything is kept at all.
func TestScrolledBy(t *testing.T) {
	old := screen("one", "two", "three", "four", "five")
	for _, c := range []struct {
		what string
		now  []string
		want int
	}{
		{"unchanged", screen("one", "two", "three", "four", "five"), 0},
		{"moved up one", screen("two", "three", "four", "five", "six"), 1},
		{"moved up two", screen("three", "four", "five", "six", "seven"), 2},
		{"repainted", screen("a", "b", "c", "d", "e"), 0},
		{"moved too far to tell", screen("five", "a", "b", "c", "d"), 0},
		{"a different size", screen("two", "three"), 0},
		{"nothing before", nil, 0},
	} {
		if got := scrolledBy(old, c.now); got != c.want {
			t.Errorf("%s: scrolled by %d, want %d", c.what, got, c.want)
		}
	}
	if got := scrolledBy(nil, screen("a", "b")); got != 0 {
		t.Errorf("no previous screen: %d", got)
	}
	// A screen that has gone blank matches anything, and means nothing.
	blank := screen("", "", "", "", "")
	if got := scrolledBy(old, blank); got != 0 {
		t.Errorf("a blank screen: %d", got)
	}
}

// What scrolls off is kept, in order, and the oldest is dropped once there
// is too much of it.
func TestAltScrollKeepsWhatScrolledOff(t *testing.T) {
	var a altScroll
	rows := 5
	line := func(i int) string { return fmt.Sprintf("line %d", i) }
	now := make([]string, rows)
	for i := range now {
		now[i] = line(i)
	}
	a.note(now)
	if len(a.lines) != 0 {
		t.Fatalf("the first look keeps nothing: %v", a.lines)
	}
	// Scroll one line at a time, as a program printing output does.
	for i := rows; i < rows+2000; i++ {
		copy(now, now[1:])
		now[rows-1] = line(i)
		a.note(now)
	}
	if len(a.lines) != 2000 {
		t.Fatalf("kept %d lines, want 2000", len(a.lines))
	}
	if a.lines[0] != line(0) || a.lines[1999] != line(1999) {
		t.Fatalf("out of order: %q … %q", a.lines[0], a.lines[1999])
	}
	// Past the cap, the oldest go.
	for i := rows + 2000; i < rows+altHistoryMax+500; i++ {
		copy(now, now[1:])
		now[rows-1] = line(i)
		a.note(now)
	}
	if len(a.lines) != altHistoryMax {
		t.Fatalf("kept %d lines, want the cap of %d", len(a.lines), altHistoryMax)
	}
	if a.lines[0] == line(0) {
		t.Fatal("the oldest lines should have gone")
	}
	// A repaint keeps nothing; leaving the screen forgets it all.
	before := len(a.lines)
	a.note(screen("completely", "different", "screen", "of", "text"))
	if len(a.lines) != before {
		t.Fatalf("a repaint kept %d lines", len(a.lines)-before)
	}
	a.reset()
	if len(a.lines) != 0 || a.prev != nil {
		t.Fatal("reset should forget everything")
	}
	// An empty look changes nothing.
	a.note(nil)
	if len(a.lines) != 0 {
		t.Fatal("an empty screen should be ignored")
	}
}

// End to end: a program on the alternate screen, printing more than fits.
func TestPaneKeepsAltScreenHistory(t *testing.T) {
	p := startShell(t)
	// Enter the alternate screen and print a hundred numbered lines.
	if err := p.SendText("printf '\\033[?1049h'; i=1; while [ $i -le 100 ]; do echo \"alt line $i\"; i=$((i+1)); done", false); err != nil {
		t.Fatal(err)
	}
	if err := p.SendKeys([]string{"enter"}); err != nil {
		t.Fatal(err)
	}
	waitScreen(t, p, "alt line 100")

	deadline := time.Now().Add(5 * time.Second)
	for p.History() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if p.History() == 0 {
		t.Fatal("nothing was kept as the alternate screen scrolled")
	}
	f := p.Frame()
	if !f.AltScreen {
		t.Fatal("the program should be on the alternate screen")
	}
	if f.History != p.History() {
		t.Fatalf("frame says %d lines of history, pane says %d", f.History, p.History())
	}
	// Scrolled back, the older lines are there to read. The whole line is
	// compared: "alt line 1" is also the start of "alt line 10" to "100".
	back := p.FrameAt(f.History)
	if !slices.Contains(back.Lines, "alt line 1") {
		t.Fatalf("the oldest lines are not in the history:\n%s", strings.Join(back.Lines, "\n"))
	}
	if back.Offset != f.History {
		t.Fatalf("offset %d, want %d", back.Offset, f.History)
	}
	// Leaving the alternate screen gives the main screen's own history back.
	if err := p.SendText("printf '\\033[?1049l'; echo back-on-main", false); err != nil {
		t.Fatal(err)
	}
	p.SendKeys([]string{"enter"})
	waitScreen(t, p, "back-on-main")
	if f := p.Frame(); f.AltScreen {
		t.Fatal("it should be back on the main screen")
	}
}

// A read from the pty can end partway through a line, and the rest of it
// arrives with the output that scrolls it. How the output is split depends
// on the machine's timing, so every split has to keep every line: once, it
// lost all but the last thirty on macOS.
func TestAltScrollKeepsLinesWhateverTheReadSize(t *testing.T) {
	var b strings.Builder
	b.WriteString("\x1b[?1049h\x1b[H")
	for i := 1; i <= 100; i++ {
		fmt.Fprintf(&b, "alt line %d\r\n", i)
	}
	out := []byte(b.String())
	const rows = 8
	for size := 1; size <= len(out); size++ {
		p := &Pane{emu: vt.NewEmulator(40, rows)}
		for i := 0; i < len(out); i += size {
			p.writeToEmulator(out[i:min(i+size, len(out))])
		}
		// The screen ends on a blank line below "alt line 100", so the
		// rest scrolled off.
		want := 100 - (rows - 1)
		if len(p.alt.lines) != want || p.alt.lines[0] != "alt line 1" || p.alt.lines[want-1] != fmt.Sprintf("alt line %d", want) {
			t.Fatalf("reads of %d bytes kept %d lines, want %d from \"alt line 1\": %q", size, len(p.alt.lines), want, p.alt.lines)
		}
	}
}

// chunkByLines keeps the emulator from swallowing a whole screen between
// two looks.
func TestChunkByLines(t *testing.T) {
	got := chunkByLines([]byte("a\nb\nc\nd\ne"), 2)
	if len(got) != 3 || string(got[0]) != "a\nb\n" || string(got[1]) != "c\nd\n" || string(got[2]) != "e" {
		t.Fatalf("split into %d: %q", len(got), got)
	}
	if got := chunkByLines([]byte("no newlines here"), 4); len(got) != 1 {
		t.Fatalf("a chunk without newlines: %q", got)
	}
	if got := chunkByLines(nil, 4); got != nil {
		t.Fatalf("nothing to split: %q", got)
	}
	if got := chunkByLines([]byte("a\nb\n"), 1); len(got) != 2 {
		t.Fatalf("one line at a time: %q", got)
	}
	if got := chunkByLines([]byte("a\nb\n"), 0); len(got) != 2 {
		t.Fatalf("a count below one is one: %q", got)
	}
	// Every byte comes out once, in order.
	in := []byte("one\ntwo\nthree\nfour\n")
	var out []byte
	for _, c := range chunkByLines(in, 3) {
		out = append(out, c...)
	}
	if string(out) != string(in) {
		t.Fatalf("lost or reordered: %q", out)
	}
}

// A screen whose bottom rows are still blank has scrolled all the same.
func TestScrolledByIgnoresTheBlankTail(t *testing.T) {
	prev := screen("one", "two", "three", "four", "", "", "")
	now := screen("two", "three", "four", "five", "six", "", "")
	if got := scrolledBy(prev, now); got != 1 {
		t.Fatalf("scrolled by %d, want 1", got)
	}
	// With too little of the old screen left, it will not guess.
	prev = screen("one", "two", "", "", "", "", "")
	now = screen("two", "three", "four", "", "", "", "")
	if got := scrolledBy(prev, now); got != 0 {
		t.Fatalf("too little to tell: %d", got)
	}
	// A last line caught half written may have grown by the next look.
	prev = screen("one", "two", "thr", "", "")
	now = screen("two", "three", "four", "", "")
	if got := scrolledBy(prev, now); got != 1 {
		t.Fatalf("a half-written last line: scrolled by %d, want 1", got)
	}
	// Only the last line may grow, and only by being added to.
	for _, c := range []struct {
		what      string
		prev, now []string
	}{
		{"a line above the last grew", screen("one", "tw", "three", ""), screen("two", "three", "four", "")},
		{"the last line changed", screen("one", "two", "three", ""), screen("two", "tree", "four", "")},
		{"the last line shrank", screen("one", "two", "three", ""), screen("two", "thr", "", "")},
	} {
		if got := scrolledBy(c.prev, c.now); got != 0 {
			t.Errorf("%s: scrolled by %d, want 0", c.what, got)
		}
	}
	// A screen that was blank has nothing to have scrolled away.
	if got := scrolledBy(screen("", "", ""), screen("a", "b", "c")); got != 0 {
		t.Fatalf("a blank screen: %d", got)
	}
}
