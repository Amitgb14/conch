package pane

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func TestDetachReplayAdopt(t *testing.T) {
	p := startShell(t)
	// Fill the history, then colour and a bracketed-paste request.
	p.SendText("for i in $(seq 1 30); do echo line-$i; done; printf '\\033[31mred\\033[0m\\033[?2004h\\n'", false)
	p.SendKeys([]string{"enter"})
	waitScreen(t, p, "red")
	before := p.FrameAt(0)
	history := p.History()

	snap, ptmx, err := p.Detach()
	if err != nil {
		t.Fatal(err)
	}
	if snap.PID != p.Info().PID || snap.Cols != 60 || snap.Rows != 10 {
		t.Fatalf("snapshot %+v", snap)
	}
	// Output written while detached waits in the kernel.
	p.emuMu.Lock()
	p.emu.SendText("echo while-detached\r")
	p.emuMu.Unlock()
	time.Sleep(300 * time.Millisecond)

	q, err := Adopt(snap, ptmx)
	if err != nil {
		t.Fatal(err)
	}
	if q.History() < history-1 {
		t.Fatalf("history %d, had %d", q.History(), history)
	}
	after := q.FrameAt(0)
	if ansi.Strip(strings.Join(after.Lines, "\n")) != ansi.Strip(strings.Join(before.Lines, "\n")) && !strings.Contains(strings.Join(q.PlainLines(), "\n"), "while-detached") {
		t.Fatalf("screen differs:\n%s\n---\n%s", strings.Join(before.Lines, "\n"), strings.Join(after.Lines, "\n"))
	}
	if !strings.Contains(strings.Join(after.Lines, ""), "\x1b[31m") && !strings.Contains(strings.Join(after.Lines, ""), "31m") {
		t.Fatalf("colour lost: %q", after.Lines)
	}
	q.mu.Lock()
	paste := false
	for m := range q.modes {
		if m.Mode() == ansi.ModeBracketedPaste.Mode() {
			paste = true
		}
	}
	q.mu.Unlock()
	if !paste {
		t.Fatal("bracketed paste mode not replayed")
	}
	waitScreen(t, q, "while-detached") // the unread output arrived
	q.SendText("echo adopted-ok", false)
	q.SendKeys([]string{"enter"})
	waitScreen(t, q, "adopted-ok")
}
