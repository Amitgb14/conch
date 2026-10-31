package server

import "testing"

func TestPaneSizesRule(t *testing.T) {
	var z paneSizes
	a, b := &client{}, &client{}
	if !z.ask(a, "p1", 80, 24) || !z.ask(b, "p1", 100, 30) {
		t.Fatal("before any input every resize applies")
	}
	if size, ok := z.input(a, "p1"); !ok || size != [2]int{80, 24} {
		t.Fatalf("input from a: %v %v", size, ok)
	}
	if z.ask(b, "p1", 90, 28) {
		t.Fatal("b resized a pane a has")
	}
	if !z.ask(a, "p1", 81, 25) {
		t.Fatal("a's own resize refused")
	}
	if !z.ask(b, "p2", 50, 10) {
		t.Fatal("another pane is not a's")
	}
	if _, ok := (&paneSizes{}).input(a, "p9"); ok {
		t.Fatal("a size for a client that never asked")
	}
	z.forget(a)
	if !z.ask(b, "p1", 70, 20) {
		t.Fatal("a left, but still holds p1")
	}
	z.drop("p1")
	if _, ok := z.input(b, "p1"); ok {
		t.Fatal("a closed pane's sizes kept")
	}
	z.forget(&client{}) // a client never seen: nothing to do
}
