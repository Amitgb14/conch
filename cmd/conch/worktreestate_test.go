package main

import (
	"testing"

	"github.com/Amitgb14/conch/internal/proto"
)

// conch worktree ls says a worktree mid-rebase is rebasing, before
// anything else about it, and names the branch the rebase is rewriting.
func TestWorktreeStateSaysRebasing(t *testing.T) {
	w := proto.StaleWorktree{Path: "/r/feat", Branch: "feat", Operation: "rebase", Panes: true}
	if got := worktreeState(w); got != "rebasing" {
		t.Fatalf("state: %q", got)
	}
	if got := worktreeLabel(w); got != "feat" {
		t.Fatalf("label: %q", got)
	}
	w.Operation = ""
	if got := worktreeState(w); got != "panes running" {
		t.Fatalf("no operation: %q", got)
	}
	if got := worktreeLabel(proto.StaleWorktree{Path: "/r/x"}); got != "(detached)" {
		t.Fatalf("detached: %q", got)
	}
}
