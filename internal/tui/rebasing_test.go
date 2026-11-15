package tui

import (
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/proto"
	"github.com/charmbracelet/x/ansi"
)

// A checkout mid-rebase is on its branch's row, which says so (#230); a
// HEAD detached on purpose is still "(detached …)" and on no row.
func TestBranchRowSaysRebasing(t *testing.T) {
	m, _ := a1Fixture(t, false)
	proj := &m.machines[0].projects[0]
	proj.Worktrees[1].Detached = true
	proj.Worktrees[1].Operation = "rebase"
	proj.Worktrees[1].Status = &proto.GitStatus{Files: 1, Conflicts: 1}
	m.rebuild()

	_, _, _, _, right := m.branchParts(row{kind: kindBranch, machine: localMachine, projectID: "r1", branch: "feat"})
	if got := ansi.Strip(right); !strings.Contains(got, "rebasing") || !strings.Contains(got, "⚠1") {
		t.Fatalf("feat's row: %q", got)
	}
	_, _, _, _, right = m.branchParts(row{kind: kindBranch, machine: localMachine, projectID: "r1", branch: "main"})
	if strings.Contains(ansi.Strip(right), "rebasing") {
		t.Fatalf("main's row: %q", ansi.Strip(right))
	}
	out := a2Plain(m.projectLines(localMachine, *proj, 120))
	if !strings.Contains(out, "feat · rebasing") || strings.Contains(out, "(detached") {
		t.Fatalf("worktrees:\n%s", out)
	}

	// Detached on purpose, with nothing in progress.
	proj.Worktrees[1].Branch, proj.Worktrees[1].Operation, proj.Worktrees[1].Head = "", "", "0123456789"
	out = a2Plain(m.projectLines(localMachine, *proj, 120))
	if !strings.Contains(out, "(detached 0123456)") || strings.Contains(out, "rebasing") {
		t.Fatalf("plain detached:\n%s", out)
	}
}
