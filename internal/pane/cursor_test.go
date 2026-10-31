package pane

import (
	"strings"
	"testing"
	"time"
)

// A frame says where the cursor is while it is drawn, and not otherwise.
func TestFrameCursor(t *testing.T) {
	p := startShell(t)
	// Typed before the prompt, the text is echoed first and the prompt
	// drawn after it, leaving the cursor behind the prompt (seen on Linux).
	waitScreen(t, p, "$")
	p.SendText("printf 'abc'", false)
	waitScreen(t, p, "printf 'abc'")
	// The cursor is just after the text on its row, wherever the prompt
	// and the echo landed.
	f, lines := p.Frame(), p.PlainLines()
	if f.Cursor == nil || f.Cursor.Row < 0 || f.Cursor.Row >= f.Rows ||
		f.Cursor.Col != len(strings.TrimRight(lines[f.Cursor.Row], " ")) ||
		!strings.HasSuffix(lines[f.Cursor.Row], "printf 'abc'") {
		t.Fatalf("cursor %+v after typing; lines %q", f.Cursor, lines)
	}

	// Hidden by the program: none.
	p.SendKeys([]string{"ctrl+u"})
	p.SendText("printf '\\033[?25l'; sleep 30", false)
	p.SendKeys([]string{"enter"})
	deadline := time.Now().Add(5 * time.Second)
	for p.Frame().Cursor != nil {
		if time.Now().After(deadline) {
			t.Fatalf("a hidden cursor is still reported: %+v", p.Frame().Cursor)
		}
		time.Sleep(20 * time.Millisecond)
	}
	p.SendKeys([]string{"ctrl+c"})
	// The marker is made by the shell, so it is in the output and never
	// in the line typed.
	p.SendText("printf '\\033[?25h'; i=0; while [ $i -lt 40 ]; do i=$((i+1)); echo row-$i; done", false)
	p.SendKeys([]string{"enter"})
	waitScreen(t, p, "row-40")

	// Scrolled back into history: the cursor is not on screen.
	if f := p.FrameAt(5); f.Offset == 0 || f.Cursor != nil {
		t.Fatalf("scrolled back %d: cursor %+v", f.Offset, f.Cursor)
	}
	if f := p.FrameAt(0); f.Cursor == nil {
		t.Fatal("back at the bottom: no cursor")
	}
}
