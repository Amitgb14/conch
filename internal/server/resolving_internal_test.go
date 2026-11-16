package server

import (
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// When an ask is forgotten: only on a fresh read of git, once the branch
// shows no conflict and has moved, or has not moved for askGrace; and
// with its worktree.
func TestApplyAsks(t *testing.T) {
	now := time.Now()
	ask := func(head string, at time.Time) resolveAsk {
		return resolveAsk{ResolveAsk: proto.ResolveAsk{Pane: "p1", At: at}, head: head}
	}
	info := func(ws ...proto.WorktreeInfo) proto.ProjectInfo { return proto.ProjectInfo{Worktrees: ws} }
	stopped := proto.WorktreeInfo{Path: "/w/a", Rebasing: "a", Head: "h2", Status: &proto.GitStatus{Conflicts: 1}}
	clean := proto.WorktreeInfo{Path: "/w/a", Branch: "a", Head: "h1"}
	moved := proto.WorktreeInfo{Path: "/w/a", Branch: "a", Head: "h3"}
	coming := proto.WorktreeInfo{Path: "/w/a", Branch: "a", Head: "h3", Conflicts: []string{"x.go"}}

	cases := []struct {
		name  string
		w     proto.WorktreeInfo
		at    time.Time
		fresh bool
		kept  bool
	}{
		{"stopped mid-rebase", stopped, now.Add(-time.Hour), true, true},
		{"will conflict", coming, now.Add(-time.Hour), true, true},
		{"not started yet", clean, now, true, true},
		{"never started", clean, now.Add(-askGrace - time.Second), true, false},
		{"rebase finished", moved, now, true, false},
		{"an older read", moved, now, false, true},
	}
	for _, c := range cases {
		p := &project{asks: map[string]resolveAsk{"a": ask("h1", c.at)}}
		in := info(c.w)
		p.applyAsks(&in, now, c.fresh)
		if got := in.Worktrees[0].Resolve != nil; got != c.kept {
			t.Errorf("%s: shown %v, want %v", c.name, got, c.kept)
		}
		if _, got := p.asks["a"]; got != c.kept {
			t.Errorf("%s: kept %v, want %v", c.name, got, c.kept)
		}
	}

	// Its worktree gone: forgotten, but not on an older read.
	p := &project{asks: map[string]resolveAsk{"a": ask("h1", now)}}
	in := info(proto.WorktreeInfo{Path: "/w/b", Branch: "b", Resolve: &proto.ResolveAsk{Pane: "stale"}})
	p.applyAsks(&in, now, false)
	if len(p.asks) != 1 || in.Worktrees[0].Resolve != nil {
		t.Errorf("older read: %v %+v", p.asks, in.Worktrees[0].Resolve)
	}
	p.applyAsks(&in, now, true)
	if len(p.asks) != 0 {
		t.Errorf("worktree gone, still asked: %v", p.asks)
	}
	// Nothing asked: nothing shown, and a nil map is fine.
	p = &project{}
	in = info(stopped)
	p.applyAsks(&in, now, true)
	if in.Worktrees[0].Resolve != nil {
		t.Error("shown with no ask")
	}
}
