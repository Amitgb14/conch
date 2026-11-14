package pane

import (
	"strings"
	"testing"
	"time"
)

// readsPane runs a program that prints each read(2) of its input on a line
// of its own, as od shows bytes: two keys read together share a line.
func readsPane(t *testing.T) *Pane {
	t.Helper()
	p, err := Start(Options{
		ID:      "t1",
		Command: []string{"/bin/sh", "-c", `stty -icanon -echo -isig -ixon -iexten min 1 time 0; echo ready; while :; do dd bs=64 count=1 2>/dev/null | od -An -c | tr -s ' ' | sed 's/^/read:/'; done`},
		Env:     []string{"ENV="},
		Cols:    60,
		Rows:    20,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	waitScreen(t, p, "ready")
	return p
}

func readLines(p *Pane) []string {
	var out []string
	for _, l := range p.PlainLines() {
		// BSD od ends with a line of its own, which is not a read.
		if l = strings.TrimSpace(l); strings.HasPrefix(l, "read:") && strings.TrimSpace(l[5:]) != "" {
			out = append(out, strings.TrimSpace(l[5:]))
		}
	}
	return out
}

func waitReads(t *testing.T, p *Pane, n int) []string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if r := readLines(p); len(r) >= n {
			return r
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("wanted %d reads; screen:\n%s", n, strings.Join(p.PlainLines(), "\n"))
	return nil
}

// Two escapes sent apart reach the program as two reads, each alone, and
// a key typed meanwhile waits until the pause after the last has passed.
func TestSendKeysApartArriveAlone(t *testing.T) {
	p := readsPane(t)
	const gap = 300 * time.Millisecond
	done := make(chan time.Time, 1)
	start := time.Now()
	go func() {
		if err := p.SendKeysApart([]string{"escape", "escape"}, gap); err != nil {
			t.Error(err)
		}
		done <- time.Now()
	}()
	time.Sleep(50 * time.Millisecond)
	if err := p.SendText("x", false); err != nil {
		t.Fatal(err)
	}
	typed := time.Now()
	apart := <-done
	if apart.Sub(start) < 2*gap {
		t.Errorf("returned after %v, before both pauses", apart.Sub(start))
	}
	if typed.Before(apart.Add(-10 * time.Millisecond)) {
		t.Errorf("text went in at %v, before the keys were done at %v", typed.Sub(start), apart.Sub(start))
	}
	got := waitReads(t, p, 3)
	if got[0] != "033" || got[1] != "033" || got[2] != "x" {
		t.Fatalf("reads %q, want each key alone: 033, 033, x", got)
	}
}

func TestSendKeysApartRefuses(t *testing.T) {
	p := readsPane(t)
	if err := p.SendKeysApart([]string{"escape", "ctrl+nope"}, time.Millisecond); err == nil {
		t.Fatal("a bad key was taken")
	}
	// Nothing at all is sent when one key is bad.
	p.SendText("z", false)
	if got := waitReads(t, p, 1); got[0] != "z" {
		t.Fatalf("reads %q", got)
	}
	if err := p.SendKeysApart(nil, time.Millisecond); err != nil {
		t.Fatalf("no keys: %v", err)
	}
	p.Close()
	<-p.Done()
	if err := p.SendKeysApart([]string{"escape"}, time.Millisecond); err == nil {
		t.Fatal("sent to a pane that has exited")
	}
}

// A pane that ends during the pause ends the wait with it.
func TestSendKeysApartPaneEnds(t *testing.T) {
	p := readsPane(t)
	go func() { time.Sleep(50 * time.Millisecond); p.Close() }()
	start := time.Now()
	err := p.SendKeysApart([]string{"escape"}, 10*time.Second)
	if err == nil || time.Since(start) > 5*time.Second {
		t.Fatalf("err %v after %v", err, time.Since(start))
	}
}
