package pane

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// scrolledRegion finds a conversation scrolling above a footer that stays
// put, or is redrawn, and nothing else.
func TestScrolledRegion(t *testing.T) {
	convo := func(from int, footer ...string) []string {
		var s []string
		for i := from; i < from+8; i++ {
			s = append(s, fmt.Sprintf("msg %d", i))
		}
		return append(s, footer...)
	}
	footer := func(status string) []string { return []string{"────", "> ", status, "? for shortcuts"} }
	for _, c := range []struct {
		what            string
		prev, now       []string
		wantTop, wantBy int
	}{
		{"moved up under a still footer", convo(0, footer("idle")...), convo(1, footer("idle")...), 0, 1},
		{"moved up three as the status ticks", convo(0, footer("✻ 1s")...), convo(3, footer("✻ 2s")...), 0, 3},
		{"a header stays too", append([]string{"title"}, convo(0, footer("a")...)[:11]...), append([]string{"title"}, convo(2, footer("b")...)[:11]...), 1, 2},
		{"only the prompt changed", convo(0, footer("idle")...), append(convo(0)[:8], "────", "> hello", "idle", "? for shortcuts"), 0, 0},
		{"only the status changed", convo(0, footer("✻ 1s")...), convo(0, footer("✻ 2s")...), 0, 0},
		{"repainted", convo(0, footer("a")...), convo(100, footer("a")...), 0, 0},
		{"moved down: scrolled back in the agent", convo(3, footer("a")...), convo(0, footer("a")...), 0, 0},
		{"a different size", convo(0, footer("a")...), convo(1), 0, 0},
		{"nothing before", nil, convo(1, footer("a")...), 0, 0},
	} {
		top, by := scrolledRegion(c.prev, c.now)
		if top != c.wantTop || by != c.wantBy {
			t.Errorf("%s: top %d shift %d, want %d and %d", c.what, top, by, c.wantTop, c.wantBy)
		}
	}
	// Blank rows lining up say nothing: only rows with text count.
	blank := make([]string, 12)
	now := append([]string{"", "", "", "", "", "", "", "", "", "", ""}, "x")
	if top, by := scrolledRegion(blank, now); by != 0 {
		t.Errorf("blank rows: top %d shift %d", top, by)
	}
	// Too little lining up is a repaint that shares a couple of lines.
	prev := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}
	now = []string{"b", "c", "x", "y", "z", "w", "v", "u", "t", "s"}
	if _, by := scrolledRegion(prev, now); by != 0 {
		t.Errorf("a short run: shift %d", by)
	}
}

// agentFrame draws what an agent's interface looks like on the alternate
// screen: conversation lines from first, then a prompt box and a status
// line that changes as it works. Each row is placed by cursor position, the
// way such interfaces repaint, not written out with newlines.
func agentFrame(first, rows int, status string, sync bool) string {
	var b strings.Builder
	if sync {
		b.WriteString(ansi.SetModeSynchronizedOutput)
	}
	convo := rows - 4
	put := func(y int, s string) { fmt.Fprintf(&b, "\x1b[%d;1H\x1b[2K%s", y+1, s) }
	for y := 0; y < convo; y++ {
		put(y, fmt.Sprintf("msg %d", first+y))
	}
	put(convo, "────")
	put(convo+1, "> ")
	put(convo+2, status)
	put(convo+3, "? for shortcuts")
	if sync {
		b.WriteString(ansi.ResetModeSynchronizedOutput)
	}
	return b.String()
}

func agentPane(rows int) *Pane {
	p := &Pane{modes: map[ansi.Mode]bool{}, mouseModes: map[ansi.Mode]bool{}}
	p.newEmulator(40, rows)
	return p
}

// An agent's conversation scrolling above its prompt is kept, in order and
// once each, as the output arrives a frame at a time.
func TestAgentConversationIsKept(t *testing.T) {
	const rows = 12
	p := agentPane(rows)
	p.writeToEmulator([]byte("\x1b[?1049h"))
	for i := 0; i <= 30; i++ {
		p.writeToEmulator([]byte(agentFrame(i, rows, fmt.Sprintf("✻ working %ds", i), false)))
	}
	want := 30
	if len(p.alt.lines) != want || p.alt.lines[0] != "msg 0" || p.alt.lines[want-1] != fmt.Sprintf("msg %d", want-1) {
		t.Fatalf("kept %d lines, want %d from msg 0: %q", len(p.alt.lines), want, p.alt.lines)
	}
	// Typing in the prompt and the status ticking over keep nothing more.
	for i := 0; i < 5; i++ {
		p.writeToEmulator([]byte(fmt.Sprintf("\x1b[%d;3Hx\x1b[%d;1H\x1b[2Kidle %d", rows-2, rows-1, i)))
	}
	if len(p.alt.lines) != want {
		t.Fatalf("the prompt changing kept %d more lines", len(p.alt.lines)-want)
	}
	// Scrolled into the kept lines, the pane shows them.
	f := p.FrameAt(want)
	if f.History != want || f.Lines[0] != "msg 0" {
		t.Fatalf("history %d, top line %q", f.History, f.Lines[0])
	}
}

// A frame drawn inside a synchronized update is only looked at once it is
// finished, however the reads split it: halfway through, the screen is part
// new and part old.
func TestAgentFramesSplitAcrossReads(t *testing.T) {
	const rows = 12
	var out strings.Builder
	out.WriteString("\x1b[?1049h")
	for i := 0; i <= 20; i++ {
		out.WriteString(agentFrame(i*2, rows, fmt.Sprintf("status %d", i), true))
	}
	b := []byte(out.String())
	for _, size := range []int{1, 7, 64, 100, 333, len(b)} {
		p := agentPane(rows)
		for i := 0; i < len(b); i += size {
			p.writeToEmulator(b[i:min(i+size, len(b))])
		}
		if p.synchronizing() {
			t.Fatalf("reads of %d: still inside a synchronized update", size)
		}
		want := 40
		if len(p.alt.lines) != want {
			t.Fatalf("reads of %d bytes kept %d lines, want %d: %q", size, len(p.alt.lines), want, p.alt.lines)
		}
		for i, l := range p.alt.lines {
			if l != fmt.Sprintf("msg %d", i) {
				t.Fatalf("reads of %d bytes: line %d is %q", size, i, l)
			}
		}
	}
}

// Leaving the alternate screen forgets the frame compared against, so a
// program coming back starts afresh.
func TestAgentFrameBaseResets(t *testing.T) {
	p := agentPane(12)
	p.writeToEmulator([]byte("\x1b[?1049h" + agentFrame(0, 12, "a", false)))
	if p.alt.base == nil {
		t.Fatal("the finished frame was not kept to compare against")
	}
	p.writeToEmulator([]byte("\x1b[?1049l"))
	if p.alt.base != nil || p.alt.lines != nil {
		t.Fatal("leaving the alternate screen kept its frames")
	}
	var a altScroll
	a.noteFrame(nil)
	if a.base != nil {
		t.Fatal("an empty look was kept")
	}
}

// splitAfter cuts after each separator and loses nothing.
func TestSplitAfter(t *testing.T) {
	for _, c := range []struct {
		in   string
		want []string
	}{
		{"a|b|c", []string{"a|", "b|", "c"}},
		{"a|", []string{"a|"}},
		{"|", []string{"|"}},
		{"abc", []string{"abc"}},
		{"", nil},
	} {
		got := splitAfter([]byte(c.in), "|")
		var s []string
		for _, g := range got {
			s = append(s, string(g))
		}
		if fmt.Sprint(s) != fmt.Sprint(c.want) || len(s) != len(c.want) {
			t.Errorf("%q: got %q, want %q", c.in, s, c.want)
		}
	}
}

// The wheel over an agent scrolls its own view back and forward again;
// the lines that come back off the top are kept once, not twice.
func TestAgentScrollingItsOwnViewKeepsNoDuplicates(t *testing.T) {
	const rows = 12
	p := agentPane(rows)
	p.writeToEmulator([]byte("\x1b[?1049h"))
	for i := 0; i <= 10; i++ {
		p.writeToEmulator([]byte(agentFrame(i, rows, "working", false)))
	}
	if len(p.alt.lines) != 10 {
		t.Fatalf("kept %d, want 10", len(p.alt.lines))
	}
	// Back five, a line at a time, then forward seven: two of those are new.
	for i := 9; i >= 5; i-- {
		p.writeToEmulator([]byte(agentFrame(i, rows, "idle", false)))
	}
	for i := 6; i <= 12; i++ {
		p.writeToEmulator([]byte(agentFrame(i, rows, "idle", false)))
	}
	if len(p.alt.lines) != 12 {
		t.Fatalf("kept %d, want 12: %q", len(p.alt.lines), p.alt.lines)
	}
	for i, l := range p.alt.lines {
		if l != fmt.Sprintf("msg %d", i) {
			t.Fatalf("line %d is %q: %q", i, l, p.alt.lines)
		}
	}
	// Back again, then a jump straight to somewhere else: it starts over,
	// and the output after it is all new.
	p.writeToEmulator([]byte(agentFrame(9, rows, "idle", false)))
	if p.alt.back != 3 {
		t.Fatalf("back %d, want 3", p.alt.back)
	}
	p.writeToEmulator([]byte(agentFrame(40, rows, "idle", false)))
	if p.alt.back != 0 {
		t.Fatalf("a jump left back at %d", p.alt.back)
	}
	p.writeToEmulator([]byte(agentFrame(41, rows, "idle", false)))
	if last := p.alt.lines[len(p.alt.lines)-1]; last != "msg 40" {
		t.Fatalf("after the jump the new line was not kept: %q", last)
	}
	// Typing in the prompt while scrolled back does not forget where it is.
	p.writeToEmulator([]byte(agentFrame(38, rows, "idle", false)))
	p.writeToEmulator([]byte(fmt.Sprintf("\x1b[%d;3Hhello", rows-2)))
	if p.alt.back != 3 {
		t.Fatalf("typing changed back to %d", p.alt.back)
	}
}
