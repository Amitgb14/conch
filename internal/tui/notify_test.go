package tui

import (
	"testing"
	"time"

	"github.com/amitghadge/conch/internal/proto"
)

func TestNextAttention(t *testing.T) {
	at := func(id, state string, sec int) scopedPane {
		p := scopedPane{machine: localMachine, PaneInfo: proto.PaneInfo{ID: id}}
		if state != "" {
			p.Agent = &proto.AgentStatus{Name: "claude", State: state, Since: time.Unix(int64(sec), 0)}
		}
		return p
	}
	panes := []scopedPane{
		at("p1", proto.AgentDone, 1),
		at("p2", "", 0),
		at("p3", proto.AgentBlocked, 5),
		at("p4", proto.AgentWorking, 0),
		at("p5", proto.AgentBlocked, 2),
	}
	for _, tc := range []struct{ current, want string }{
		{"p2", "p5"}, // oldest blocked first
		{"p5", "p3"},
		{"p3", "p1"}, // then done
		{"p1", "p5"}, // wraps
	} {
		if _, got := nextAttention(panes, tc.current); got != tc.want {
			t.Errorf("from %s: got %s, want %s", tc.current, got, tc.want)
		}
	}
	if _, got := nextAttention([]scopedPane{at("p1", proto.AgentIdle, 0)}, ""); got != "" {
		t.Errorf("idle pane offered: %s", got)
	}
}
