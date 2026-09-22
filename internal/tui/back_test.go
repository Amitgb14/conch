package tui

import (
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

func TestA1BackSwapsWithTheLastPlace(t *testing.T) {
	m, _ := a1Fixture(t, false)
	for i := range m.machines[0].panes {
		if m.machines[0].panes[i].ID == "p1" {
			m.machines[0].panes[i].Agent = &proto.AgentStatus{Name: "claude", State: proto.AgentDone, Since: time.Now().Add(-time.Hour)}
		}
	}
	a1Key(t, m, a2Key("Q"))
	if m.tab().focused().view.Kind != kindReviewQueue {
		t.Fatalf("no queue: %+v", m.tab().focused().view)
	}
	// Jump to the branch the row points at.
	if _, cmd := m.queueView.key(m, a2Key("enter")); cmd != nil {
		a2Run(cmd)
	}
	if v := m.tab().focused().view; v.Kind != kindBranch {
		t.Fatalf("enter opened %+v", v)
	}
	// Back to the queue, and back again to the branch.
	a1Prefixed(t, m, a2Key("b"))
	if v := m.tab().focused().view; v.Kind != kindReviewQueue {
		t.Fatalf("back opened %+v, want the queue", v)
	}
	a1Prefixed(t, m, a2Key("b"))
	if v := m.tab().focused().view; v.Kind != kindBranch {
		t.Fatalf("back again opened %+v, want the branch", v)
	}
}

func TestA1BackWithNowhereToGo(t *testing.T) {
	m, _ := a1Fixture(t, false)
	m.prevView = viewRef{}
	a1Prefixed(t, m, a2Key("b"))
	if m.flash != "nothing to go back to" {
		t.Fatalf("flash %q", m.flash)
	}
}
