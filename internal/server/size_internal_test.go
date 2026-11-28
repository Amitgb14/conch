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

// A pane let go of by the client that held it goes back to the client
// that asked most recently: the laptop, once the phone is put away.
func TestPaneSizesHandBack(t *testing.T) {
	var z paneSizes
	mac, phone, other := &client{}, &client{}, &client{}
	z.ask(mac, "p1", 120, 40)
	z.ask(phone, "p1", 44, 13)
	z.input(phone, "p1")

	// The phone stops showing it: back to the Mac's size, and the Mac's
	// own resizes apply again.
	if hb, ok := z.release(phone, "p1"); !ok || hb.pane != "p1" || hb.size != [2]int{120, 40} {
		t.Fatalf("released: %+v %v", hb, ok)
	}
	if !z.ask(mac, "p1", 130, 42) {
		t.Fatal("the Mac does not have the pane back")
	}

	// A client that does not hold the pane lets go: nothing moves.
	z.ask(phone, "p1", 44, 13)
	z.input(mac, "p1")
	if _, ok := z.release(phone, "p1"); ok {
		t.Fatal("the phone let go of a pane the Mac held, and it moved")
	}

	// The phone goes altogether while holding it: the same.
	z.ask(phone, "p1", 44, 13)
	z.ask(other, "p1", 90, 30) // asked after the Mac: it is the newest
	z.input(phone, "p1")
	back := z.forget(phone)
	if len(back) != 1 || back[0].size != [2]int{90, 30} {
		t.Fatalf("forgot the phone: %+v", back)
	}

	// The last client lets go: nobody to give it to.
	z.forget(other)
	if back := z.forget(mac); len(back) != 0 {
		t.Fatalf("handed to nobody: %+v", back)
	}
	if _, ok := z.release(&client{}, "p9"); ok {
		t.Fatal("a pane never asked about moved")
	}
}
