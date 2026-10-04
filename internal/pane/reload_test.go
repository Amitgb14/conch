package pane

import (
	"slices"
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
	waitScreen(t, p, "line-30") // the command line itself contains "red"
	time.Sleep(200 * time.Millisecond)
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

	q, err := Adopt(snap, a5Dup(t, p, ptmx))
	if err != nil {
		t.Fatal(err)
	}
	// The command survives: the tree files ssh sessions by it.
	if got, want := strings.Join(q.Info().Command, " "), strings.Join(snap.Command, " "); got != want || got == "" {
		t.Fatalf("command after adopt %q, want %q", got, want)
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

	// An adopted pane has no exec.Cmd; closing it used to crash the server.
	closed := make(chan struct{})
	go func() {
		q.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return for an adopted pane")
	}
	if q.running() {
		t.Fatal("adopted pane still running after Close")
	}
}

// waitFor polls until ok holds, failing after a few seconds.
func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		if ok() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// altPane starts a shell, fills the main screen's history, then runs a
// program on the alternate screen that scrolls lines off it and waits for
// enter before going back.
func altPane(t *testing.T) *Pane {
	t.Helper()
	p := startShell(t)
	p.SendText("seq 1 300 | sed s/^/main-/\n", false)
	waitScreen(t, p, "main-300")
	waitFor(t, "main history", func() bool { return p.History() >= 290 })
	p.SendText("printf '\\033[?1049h'; seq 1 40 | sed s/^/alt-/; read x; printf '\\033[?1049l'; echo back-$((1+1))\n", false)
	waitScreen(t, p, "alt-40")
	waitFor(t, "alternate history", func() bool { return p.History() >= 25 })
	return p
}

// A pane on the alternate screen — an agent's full-screen interface — keeps
// both screens' history through a reload. Both used to be lost.
func TestReloadOnAltScreenKeepsHistory(t *testing.T) {
	p := altPane(t)
	main, altLines := plainHistory(p), slices.Clone(p.alt.lines)

	snap, ptmx, err := p.Detach()
	if err != nil {
		t.Fatal(err)
	}
	q, err := Adopt(snap, a5Dup(t, p, ptmx))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(q.Close)

	q.emuMu.RLock()
	alt, gotAlt := q.emu.IsAltScreen(), slices.Clone(q.alt.lines)
	q.emuMu.RUnlock()
	if !alt || !slices.Equal(gotAlt, altLines) {
		t.Fatalf("alternate screen %v, history %d lines, want %d", alt, len(gotAlt), len(altLines))
	}
	if got := plainHistory(q); !slices.Equal(got, main) {
		t.Fatalf("main history after reload: %d lines, want %d\nfirst %q", len(got), len(main), got[:min(3, len(got))])
	}
	if f := q.FrameAt(len(altLines)); !strings.Contains(ansi.Strip(f.Lines[0]), "alt-") {
		t.Fatalf("scrolled back on the alternate screen: %q", f.Lines)
	}

	// Back on the main screen its history is there to scroll to.
	q.SendKeys([]string{"enter"})
	waitScreen(t, q, "back-2")
	if got := plainHistory(q); len(got) < len(main) || !slices.Equal(got[:len(main)], main) {
		t.Fatalf("main history after leaving the alternate screen: %d lines, want %d first", len(got), len(main))
	}
}

// A snapshot from an older server has no alternate history: the pane
// adopts with none, and an overlong one is cut to the cap.
func TestAdoptAltHistoryFromOtherServers(t *testing.T) {
	for _, c := range []struct {
		what string
		hist []string
		want int
	}{
		{"older server", nil, 0},
		{"over the cap", make([]string, altHistoryMax+5), altHistoryMax},
	} {
		p := altPane(t)
		snap, ptmx, err := p.Detach()
		if err != nil {
			t.Fatal(err)
		}
		snap.AltHistory = c.hist
		q, err := Adopt(snap, a5Dup(t, p, ptmx))
		if err != nil {
			t.Fatal(err)
		}
		if n := q.History(); n != c.want {
			t.Errorf("%s: history %d, want %d", c.what, n, c.want)
		}
		q.Close()
	}
}
