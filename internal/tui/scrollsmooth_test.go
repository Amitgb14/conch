package tui

import (
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// frameAt sends the viewed pane a frame at offset o of history lines, as
// the server renders it.
func frameAt(t *testing.T, m *Model, o, history int) {
	t.Helper()
	f := proto.Frame{ID: m.viewing, Lines: []string{"x"}, Offset: o, History: history}
	m.handleEvent(m.machines[0], eventMsg(t, proto.EventPaneFrame, f))
}

// A quick turn of the wheel asks for one scroll per notch, and the frames
// for the earlier notches land after the later ones were asked for. They
// once set the offset back, so the next notch started from there and the
// scroll stuttered and stalled.
func TestWheelOutrunsItsFrames(t *testing.T) {
	m, in, _ := selScrollFixture(t, proto.Frame{ID: "p3"})
	for range 3 {
		a1Mouse(t, m, in.x+1, in.y+1, a1WheelUp, a1Press)
	}
	if m.offset != 9 {
		t.Fatalf("three notches: offset %d", m.offset)
	}
	frameAt(t, m, 3, 60) // for the first notch
	frameAt(t, m, 6, 60) // for the second
	if m.offset != 9 || m.asked == nil {
		t.Fatalf("pulled back by late frames: offset %d", m.offset)
	}
	if m.frame.Offset != 6 {
		t.Fatalf("a late frame is still shown: %d", m.frame.Offset)
	}
	m.syncView() // redrawing the tabs takes the frame's offset too
	if m.offset != 9 {
		t.Fatalf("syncView pulled back to %d", m.offset)
	}
	a1Mouse(t, m, in.x+1, in.y+1, a1WheelUp, a1Press)
	if m.offset != 12 {
		t.Fatalf("fourth notch from %d", m.offset)
	}
	frameAt(t, m, 12, 60)
	if m.offset != 12 || m.asked != nil {
		t.Fatalf("caught up: offset %d asked %+v", m.offset, m.asked)
	}

	// Back down to live: a late frame still scrolled back does not hold it.
	m.scrollPane(-12)
	frameAt(t, m, 3, 60)
	if m.offset != 0 {
		t.Fatalf("down to live: offset %d", m.offset)
	}
	frameAt(t, m, 0, 60)
	if m.offset != 0 || m.asked != nil {
		t.Fatalf("live: offset %d asked %+v", m.offset, m.asked)
	}
}

// Output arriving while the view is scrolled back moves the offset the
// server renders at, keeping the view on the same text: a frame anchored
// that way has the scroll asked for, and its offset is the view's.
func TestWheelFrameAnchoredByOutput(t *testing.T) {
	m, _, _ := selScrollFixture(t, proto.Frame{ID: "p3"})
	m.scrollPane(10)
	frameAt(t, m, 14, 64) // four lines of output since
	if m.offset != 14 || m.asked != nil {
		t.Fatalf("anchored frame: offset %d asked %+v", m.offset, m.asked)
	}
	// And output with no scroll in flight is followed as it always was.
	frameAt(t, m, 16, 66)
	if m.offset != 16 {
		t.Fatalf("following output: offset %d", m.offset)
	}
}

// A frame that never matches what was asked — the server clamped it, or
// the history was trimmed — is believed once the scroll has had time to
// arrive, rather than leaving the view's offset wrong for good.
func TestWheelAskGivesWayToTheServer(t *testing.T) {
	m, _, _ := selScrollFixture(t, proto.Frame{ID: "p3"})
	m.scrollPane(30)
	frameAt(t, m, 20, 40)
	if m.offset != 30 {
		t.Fatalf("too soon: offset %d", m.offset)
	}
	m.asked.at = time.Now().Add(-scrollSettle)
	frameAt(t, m, 20, 40)
	if m.offset != 20 || m.asked != nil {
		t.Fatalf("settled: offset %d asked %+v", m.offset, m.asked)
	}
}

// A scroll asked of one pane is forgotten when another is shown: its
// frames say nothing about the new one's offset.
func TestWheelAskForgottenWithThePane(t *testing.T) {
	m, _, _ := selScrollFixture(t, proto.Frame{ID: "p3"})
	m.scrollPane(9)
	m.viewing = "other"
	m.syncView()
	if m.asked != nil {
		t.Fatalf("still asked: %+v", m.asked)
	}
}

// useWheelClock makes the wheel's events arrive at the times next gives,
// for the rest of the test.
func useWheelClock(t *testing.T, next func() time.Time) {
	restore := wheelClock
	wheelClock = next
	t.Cleanup(func() { wheelClock = restore })
}

// A notch on its own moves three lines; each event after it in a burst —
// a trackpad's swipe — moves one, so the history follows the finger.
func TestWheelStep(t *testing.T) {
	var now time.Time
	useWheelClock(t, func() time.Time { return now })
	var m Model
	base := time.Unix(1000, 0)
	for i, c := range []struct {
		at   time.Duration
		want int
	}{
		{0, 3},                                  // the first ever: a notch
		{10 * time.Millisecond, 1},              // a burst
		{20 * time.Millisecond, 1},              // still one
		{20 * time.Millisecond, 1},              // two in the same instant
		{20*time.Millisecond + wheelBurst, 3},   // exactly the gap: a notch
		{20*time.Millisecond + 3*wheelBurst, 3}, // well apart: a notch
		{5 * time.Millisecond, 3},               // the clock went back: a notch
	} {
		now = base.Add(c.at)
		if got := m.wheelStep(); got != c.want {
			t.Fatalf("event %d at %v: step %d, want %d", i, c.at, got, c.want)
		}
	}
}

// A swipe over a pane scrolls its history a line an event after the
// first, both ways.
func TestTrackpadScrollsALineAtATime(t *testing.T) {
	m, in, _ := selScrollFixture(t, proto.Frame{ID: "p3"})
	now := time.Unix(1000, 0)
	useWheelClock(t, func() time.Time { now = now.Add(8 * time.Millisecond); return now })
	for range 5 {
		a1Mouse(t, m, in.x+1, in.y+1, a1WheelUp, a1Press)
	}
	if m.offset != 3+4 {
		t.Fatalf("swipe up: offset %d", m.offset)
	}
	now = now.Add(time.Second) // the finger lifted
	for range 3 {
		a1Mouse(t, m, in.x+1, in.y+1, a1WheelDown, a1Press)
	}
	if m.offset != 7-3-2 {
		t.Fatalf("swipe down: offset %d", m.offset)
	}
	// Never past live, however small the step.
	for range 5 {
		a1Mouse(t, m, in.x+1, in.y+1, a1WheelDown, a1Press)
	}
	if m.offset != 0 {
		t.Fatalf("past live: offset %d", m.offset)
	}
}
