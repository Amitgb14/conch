package server

import (
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// What a TUI is showing is the TUI's; the server only carries it. It
// answers with the newest layout published, forgets a client's when it
// goes, and says nothing of its own when none is connected.
func TestViewStateIsCarriedNotInvented(t *testing.T) {
	b := newViewBook()
	if _, ok := b.latest(); ok {
		t.Fatal("an empty book answered")
	}
	t0 := time.Now()
	c1, c2 := &client{}, &client{}
	b.put(c1, proto.ViewState{Client: "tui", Tabs: []proto.ViewTab{{Name: "api", Panes: []string{"p1"}}}}, t0)
	b.put(c2, proto.ViewState{Client: "phone", Focus: "p2"}, t0.Add(time.Second))
	if v, ok := b.latest(); !ok || v.Client != "phone" {
		t.Fatalf("newest wins: %+v", v)
	}
	// A TUI with nothing open is a true answer, not a missing one.
	b.put(c2, proto.ViewState{Client: "phone"}, t0.Add(2*time.Second))
	if v, ok := b.latest(); !ok || v.Client != "phone" || len(v.Tabs) != 0 {
		t.Fatalf("an empty layout: %+v %v", v, ok)
	}
	b.forget(c2)
	if v, ok := b.latest(); !ok || v.Client != "tui" {
		t.Fatalf("after the phone went: %+v", v)
	}
	b.forget(c1)
	if _, ok := b.latest(); ok {
		t.Fatal("a layout nobody is looking at was still answered")
	}
}
