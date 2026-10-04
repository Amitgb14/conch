package pane

import (
	"fmt"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"unsafe"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
)

// textPane is a pane with an emulator and no program, for feeding output
// straight in as the read loop would.
func textPane(cols, rows int) *Pane {
	p := &Pane{modes: map[ansi.Mode]bool{}, mouseModes: map[ansi.Mode]bool{}, state: "running", cursorVisible: true}
	p.newEmulator(cols, rows)
	return p
}

func (p *Pane) feed(s string) {
	p.emuMu.Lock()
	defer p.emuMu.Unlock()
	p.writeToEmulator([]byte(s))
}

// numbered prints lines "line 0" to "line n-1", each in a colour and padded
// out with x to width columns.
func numbered(from, n, width int) string {
	var b strings.Builder
	for i := from; i < from+n; i++ {
		l := fmt.Sprintf("line %d ", i)
		fmt.Fprintf(&b, "\x1b[3%dm%s%s\x1b[0m\r\n", i%8, l, strings.Repeat("x", max(0, width-len(l))))
	}
	return b.String()
}

func plainHistory(p *Pane) []string {
	p.emuMu.RLock()
	defer p.emuMu.RUnlock()
	var out []string
	for _, l := range p.hist {
		out = append(out, ansi.Strip(l))
	}
	return out
}

// What scrolls off the main screen moves into the pane's history as text,
// in order, and the emulator keeps none of it.
func TestHistoryTakesWhatScrolledOff(t *testing.T) {
	p := textPane(40, 10)
	p.feed(numbered(0, 25, 20))
	// 25 lines and the cursor's blank one below them: 16 scrolled off.
	if got := p.History(); got != 16 {
		t.Fatalf("history %d, want 16", got)
	}
	if n := p.emu.ScrollbackLen(); n != 0 {
		t.Fatalf("the emulator still holds %d lines", n)
	}
	h := plainHistory(p)
	for i, l := range h {
		if !strings.HasPrefix(l, fmt.Sprintf("line %d ", i)) {
			t.Fatalf("history line %d is %q", i, l)
		}
	}
	// Colour survives in the text.
	if !strings.Contains(p.hist[1], "\x1b[31m") {
		t.Fatalf("colour lost: %q", p.hist[1])
	}
}

// One burst far larger than the screen loses nothing: it goes into the
// emulator half a screen at a time.
func TestHistoryKeepsAWholeBurst(t *testing.T) {
	p := textPane(40, 10)
	p.feed(numbered(0, 3000, 10))
	h := plainHistory(p)
	if len(h) != 2991 {
		t.Fatalf("history %d, want 2991", len(h))
	}
	for i, l := range h {
		if !strings.HasPrefix(l+" ", fmt.Sprintf("line %d ", i)) {
			t.Fatalf("history line %d is %q", i, l)
		}
	}
}

// The history keeps the last historyMax lines, as the emulator did.
func TestHistoryIsCapped(t *testing.T) {
	p := textPane(40, 10)
	p.feed(numbered(0, historyMax+500, 10))
	h := plainHistory(p)
	if len(h) != historyMax {
		t.Fatalf("history %d, want %d", len(h), historyMax)
	}
	if !strings.HasPrefix(h[0], "line 491 ") || !strings.HasPrefix(h[len(h)-1], fmt.Sprintf("line %d ", historyMax+490)) {
		t.Fatalf("kept %q … %q", h[0], h[len(h)-1])
	}
}

// Frames scrolled back into history show the same lines, with their
// colour, that the emulator's cells did.
func TestFrameAtReadsHistory(t *testing.T) {
	p := textPane(30, 5)
	p.feed(numbered(0, 20, 12))
	f := p.FrameAt(p.History())
	if f.Offset != 16 || f.History != 16 {
		t.Fatalf("offset %d history %d", f.Offset, f.History)
	}
	for y, l := range f.Lines {
		if want := fmt.Sprintf("line %d ", y); !strings.HasPrefix(ansi.Strip(l), want) {
			t.Fatalf("row %d is %q, want %q", y, ansi.Strip(l), want)
		}
	}
	if !strings.Contains(f.Lines[2], "\x1b[32m") {
		t.Fatalf("colour lost: %q", f.Lines[2])
	}
	// Half history, half screen.
	f = p.FrameAt(2)
	if got := ansi.Strip(f.Lines[0]); !strings.HasPrefix(got, "line 14 ") {
		t.Fatalf("top row %q", got)
	}
	if got := ansi.Strip(f.Lines[2]); !strings.HasPrefix(got, "line 16 ") {
		t.Fatalf("first screen row %q", got)
	}
}

// After the pane narrows, lines kept while it was wider are cut to fit:
// in frames, in search and in the reload replay.
func TestHistoryAfterNarrowing(t *testing.T) {
	p := textPane(60, 5)
	p.feed(numbered(0, 10, 50))
	p.emuMu.Lock()
	p.emu.Resize(20, 5)
	p.emuMu.Unlock()
	for _, l := range p.FrameAt(p.History()).Lines {
		if w := ansi.StringWidth(l); w > 20 {
			t.Fatalf("a row %d wide in 20 columns: %q", w, l)
		}
	}
	lines, history := p.textLines()
	if history != 6 || ansi.StringWidth(lines[0]) != 20 {
		t.Fatalf("search text %q, history %d", lines[0], history)
	}
	if r := p.Search("xxxxxxxxxxxxxxxxxxxxxxxx", 0, -1, false); r.Found {
		t.Fatalf("found text past the width: %+v", r)
	}
	// Replayed into a fresh emulator of the new width, no line wraps.
	p.emuMu.RLock()
	replay := p.replayLocked(nil, true)
	p.emuMu.RUnlock()
	q := textPane(20, 5)
	q.feed(replay)
	if q.History() != p.History() {
		t.Fatalf("replayed history %d, want %d", q.History(), p.History())
	}
}

// ESC [3J erases the history; ESC [2J keeps the screen in it, as the
// emulator did.
func TestHistoryErase(t *testing.T) {
	p := textPane(40, 5)
	p.feed(numbered(0, 10, 10))
	p.feed("\x1b[H\x1b[2J")
	if h := plainHistory(p); len(h) != 10 || !strings.HasPrefix(h[9], "line 9") {
		t.Fatalf("after ESC[2J: %q", h)
	}
	// Lines scrolled off before ESC[3J in the same write go too.
	p.feed(numbered(10, 10, 10) + "\x1b[3J" + numbered(20, 7, 10))
	// The cursor stays at the bottom, so the blank screen scrolls up first.
	h := slices.DeleteFunc(plainHistory(p), func(l string) bool { return l == "" })
	if len(h) != 3 || !strings.HasPrefix(h[0], "line 20 ") {
		t.Fatalf("after ESC[3J: %q", h)
	}
	if n := p.emu.ScrollbackLen(); n != 0 {
		t.Fatalf("the emulator still holds %d lines", n)
	}
	// On the alternate screen it leaves the main screen's history alone.
	p.feed("\x1b[?1049h\x1b[3J\x1b[?1049l")
	if got := len(plainHistory(p)); got != 7 {
		t.Fatalf("ESC[3J on the alternate screen left %d of 7", got)
	}
}

// Repaint clears conch's copy of the screen without saving it: the stale
// screen must not land in history above the program's fresh draw, and what
// was already there stays as it was.
func TestRepaintLeavesHistoryAlone(t *testing.T) {
	for _, c := range []struct {
		what   string
		lines  int // printed on a five-row screen
		before int // lines in history before the repaint
	}{
		{"nothing scrolled", 3, 0},
		{"history kept", 8, 4},
		{"alternate screen", 0, 0},
	} {
		p := textPane(40, 5)
		p.state = "exited" // so Repaint does not try to resize a terminal
		p.changed = make(chan struct{})
		if c.what == "alternate screen" {
			p.feed("\x1b[?1049h" + "on the alternate screen")
		}
		p.feed(numbered(0, c.lines, 10))
		was := plainHistory(p)
		if len(was) != c.before {
			t.Fatalf("%s: history %d before the repaint, want %d", c.what, len(was), c.before)
		}
		if err := p.Repaint(); err != nil {
			t.Fatal(err)
		}
		if n := p.emu.ScrollbackLen(); n != 0 {
			t.Errorf("%s: emulator kept %d lines", c.what, n)
		}
		if got := plainHistory(p); !slices.Equal(got, was) {
			t.Errorf("%s: history %q after the repaint, want %q", c.what, got, was)
		}
		if screen := strings.TrimSpace(strings.Join(p.PlainLines(), "")); screen != "" {
			t.Errorf("%s: screen not cleared: %q", c.what, screen)
		}
		// The program's next output, drawn from the top, still scrolls
		// into history as usual: six lines on five rows push two off.
		p.feed(numbered(100, 6, 10))
		want := append(was, "line 100 x", "line 101 x")
		if c.what == "alternate screen" {
			want = was // the main screen's history is not the alternate one's
		}
		if got := plainHistory(p); !slices.Equal(got, want) {
			t.Errorf("%s: history after more output %q, want %q", c.what, got, want)
		}
	}
}

// Links in a line keep working after it scrolls into history.
func TestHistoryKeepsLinks(t *testing.T) {
	p := textPane(40, 3)
	p.feed(ansi.SetHyperlink("https://example.com") + "a link" + ansi.ResetHyperlink() + "\r\n" + numbered(0, 5, 10))
	if !strings.Contains(p.hist[0], "https://example.com") || ansi.Strip(p.hist[0]) != "a link" {
		t.Fatalf("history line %q", p.hist[0])
	}
}

// Wide characters take two cells in search, as on the screen.
func TestHistorySearchWideCharacters(t *testing.T) {
	p := textPane(20, 3)
	p.feed("日本 here\r\n" + numbered(0, 5, 8))
	r := p.Search("here", p.History(), 0, true)
	if !r.Found || r.Line != 0 || r.Col != 5 {
		t.Fatalf("search %+v", r)
	}
}

func TestKeepLast(t *testing.T) {
	for _, c := range []struct {
		in   []string
		n    int
		want []string
	}{
		{nil, 3, nil},
		{[]string{"a"}, 3, []string{"a"}},
		{[]string{"a", "b", "c"}, 3, []string{"a", "b", "c"}},
		{[]string{"a", "b", "c", "d"}, 3, []string{"b", "c", "d"}},
		{[]string{"a", "b"}, 0, []string{}},
	} {
		in := slices.Clone(c.in)
		got := keepLast(in, c.n)
		if !slices.Equal(got, c.want) {
			t.Errorf("keepLast(%q, %d) = %q, want %q", c.in, c.n, got, c.want)
		}
		// What was dropped is let go.
		for i := 0; i < len(in)-len(got); i++ {
			if in[i] != "" {
				t.Errorf("keepLast(%q, %d) kept %q in the array", c.in, c.n, in[i])
			}
		}
	}
}

func TestFitWidth(t *testing.T) {
	red := "\x1b[31mhello world\x1b[m"
	if got := fitWidth(red, 20); got != red {
		t.Fatalf("a line that fits changed: %q", got)
	}
	if got := ansi.Strip(fitWidth(red, 5)); got != "hello" {
		t.Fatalf("cut to %q", got)
	}
	if got := fitWidth("", 0); got != "" {
		t.Fatalf("empty: %q", got)
	}
	if got := historyText("\x1b[44mab   \x1b[m", 10); got != "ab" {
		t.Fatalf("history text %q", got)
	}
}

// The alternate screen keeps no scrollback of cells: nothing reads it, and
// an agent's interface filled it with ten thousand lines.
func TestAltScreenKeepsNoCells(t *testing.T) {
	p := textPane(40, 10)
	p.feed("\x1b[?1049h" + numbered(0, 200, 10) + "\x1b[2J")
	f := reflect.ValueOf(p.emu).Elem().FieldByName("scrs")
	if !f.IsValid() {
		t.Fatal("the emulator no longer has scrs: dropAltScrollback needs another way in")
	}
	alt := (*vt.Screen)(unsafe.Pointer(f.Index(1).UnsafeAddr()))
	if sb := alt.Scrollback(); sb != nil {
		t.Fatalf("the alternate screen keeps %d lines of cells", sb.Len())
	}
	if p.History() == 0 {
		t.Fatal("altscroll kept nothing")
	}
	// The main screen still has its own.
	p.feed("\x1b[?1049l" + numbered(0, 20, 10))
	if p.History() == 0 {
		t.Fatal("no history on the main screen")
	}
}

// heapOf reports how much heap fill leaves allocated.
func heapOf(fill func() any) uint64 {
	var a, b runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&a)
	keep := fill()
	runtime.GC()
	runtime.ReadMemStats(&b)
	runtime.KeepAlive(keep)
	if b.HeapAlloc < a.HeapAlloc {
		return 0
	}
	return b.HeapAlloc - a.HeapAlloc
}

// A pane with a full history of wide, coloured lines stays small. Kept as
// cells, this took around 145 MB.
func TestHistoryMemory(t *testing.T) {
	for _, c := range []struct {
		what  string
		setup string
	}{
		{"main screen", ""},
		{"alternate screen", "\x1b[?1049h"},
	} {
		used := heapOf(func() any {
			p := textPane(120, 40)
			p.feed(c.setup)
			for i := 0; i < historyMax+2000; i += 500 {
				p.feed(numbered(i, 500, 118))
			}
			return p
		})
		if limit := uint64(30 << 20); used > limit {
			t.Errorf("%s: a full pane holds %d MB, want under %d", c.what, used>>20, limit>>20)
		}
		t.Logf("%s: %.1f MB", c.what, float64(used)/(1<<20))
	}
}

// A replay from an older server, holding the whole history, is adopted
// into text a piece at a time.
func TestAdoptReplaysHistoryIntoText(t *testing.T) {
	p := startShell(t)
	snap, ptmx, err := p.Detach()
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for i := 0; i < historyMax+300; i++ {
		fmt.Fprintf(&b, "\x1b[0mold %d\r\n", i)
	}
	b.WriteString("\x1b[0m$ ")
	snap.Replay = b.String()
	q, err := Adopt(snap, ptmx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(q.Close)
	q.emuMu.RLock()
	sb, h := q.emu.ScrollbackLen(), slices.Clone(q.hist)
	q.emuMu.RUnlock()
	if sb != 0 {
		t.Fatalf("the emulator holds %d lines", sb)
	}
	if len(h) != historyMax {
		t.Fatalf("history %d, want %d", len(h), historyMax)
	}
	// The screen is the last rows of the replay; above it, the history ends
	// with the line before them.
	last := historyMax + 300 - snap.Rows
	if got := ansi.Strip(h[len(h)-1]); got != fmt.Sprintf("old %d", last) {
		t.Fatalf("history ends with %q, want old %d", got, last)
	}
}
