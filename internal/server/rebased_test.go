package server_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

// rebasedFixture is feat pushed, then rebased onto a main that changed
// the same line, its conflict resolved in feat's worktree — as an agent
// asked to resolve it leaves things. It returns feat's tips before and
// after.
func rebasedFixture(t *testing.T) (c *client.Client, proj proto.ProjectInfo, wt, origin, before, after string) {
	t.Helper()
	c, proj, repo, wt, origin := harvestFixture(t)
	git(t, repo, "push", "-q", "origin", "main")
	os.WriteFile(filepath.Join(wt, "a.txt"), []byte("feat\n"), 0o644)
	git(t, wt, "commit", "-qam", "feat edits a")
	os.WriteFile(filepath.Join(wt, "b.txt"), []byte("b\n"), 0o644)
	git(t, wt, "add", "b.txt")
	git(t, wt, "commit", "-qm", "feat adds b")
	if err := call(t, c, proto.MethodBranchPush, proto.BranchPushParams{ProjectID: proj.ID, Branch: "feat"}, nil); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("main\n"), 0o644)
	git(t, repo, "commit", "-qam", "main edits a")
	before = gitOut(t, wt, "rev-parse", "HEAD")
	cmd := exec.Command("git", "-C", wt, "rebase", "main")
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("expected a conflict:\n%s", out)
	}
	os.WriteFile(filepath.Join(wt, "a.txt"), []byte("main\nfeat\n"), 0o644)
	git(t, wt, "add", "a.txt")
	cmd = exec.Command("git", "-C", wt, "rebase", "--continue")
	cmd.Env = append(os.Environ(), "GIT_EDITOR=true", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("continue: %v\n%s", err, out)
	}
	return c, proj, wt, origin, before, gitOut(t, wt, "rev-parse", "HEAD")
}

// A resolved rebase is read back for review, pushed over the old branch
// with a lease, and then no longer listed.
func TestBranchRebasedReviewAndPush(t *testing.T) {
	c, proj, wt, origin, before, after := rebasedFixture(t)
	if miss := c.MissingCapabilities([]string{proto.CapBranchRebased}); len(miss) > 0 {
		t.Fatalf("missing %v", miss)
	}
	var got proto.BranchRebased
	if err := call(t, c, proto.MethodBranchRebased, proto.BranchRebasedParams{ProjectID: proj.ID}, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Rebases) != 1 {
		t.Fatalf("%+v", got)
	}
	r := got.Rebases[0]
	if r.Branch != "feat" || r.Before != before || r.After != after || r.Remote != before || r.At.IsZero() || r.Diff != "" {
		t.Errorf("rebase: %+v", r)
	}
	if len(r.Commits) != 2 || r.Commits[0].Change != "changed" || r.Commits[0].Subject != "feat edits a" || r.Commits[1].Change != "same" {
		t.Errorf("commits: %+v", r.Commits)
	}
	// The resolution itself, asked of one branch.
	if err := call(t, c, proto.MethodBranchRebased, proto.BranchRebasedParams{ProjectID: proj.ID, Branch: "feat", Diff: true}, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Rebases) != 1 || !strings.Contains(got.Rebases[0].Diff, "main") {
		t.Errorf("diff: %+v", got)
	}
	if err := call(t, c, proto.MethodBranchRebased, proto.BranchRebasedParams{ProjectID: proj.ID, Branch: "main"}, &got); err != nil || len(got.Rebases) != 0 {
		t.Errorf("main was not rebased: %+v %v", got, err)
	}

	// A plain push of the rewrite is refused, and so is one over a commit
	// the remote does not have.
	err := call(t, c, proto.MethodBranchPush, proto.BranchPushParams{ProjectID: proj.ID, Branch: "feat"}, nil)
	wantErr(t, err, "")
	err = call(t, c, proto.MethodBranchPush, proto.BranchPushParams{ProjectID: proj.ID, Branch: "feat", Over: after}, nil)
	wantErr(t, err, "")
	if gitOut(t, origin, "rev-parse", "feat") != before {
		t.Fatal("the remote's branch was replaced")
	}
	if err := call(t, c, proto.MethodBranchPush, proto.BranchPushParams{ProjectID: proj.ID, Branch: "feat", Over: before}, nil); err != nil {
		t.Fatal(err)
	}
	if gitOut(t, origin, "rev-parse", "feat") != after {
		t.Fatal("the rebased branch was not pushed")
	}
	if err := call(t, c, proto.MethodBranchRebased, proto.BranchRebasedParams{ProjectID: proj.ID}, &got); err != nil || len(got.Rebases) != 0 {
		t.Errorf("pushed, still listed: %+v %v", got, err)
	}
	_ = wt
}

// Undoing puts the branch back where it was, once.
func TestBranchRebaseUndo(t *testing.T) {
	c, proj, wt, _, before, after := rebasedFixture(t)
	undo := proto.BranchRebaseUndoParams{ProjectID: proj.ID, Branch: "feat", Before: before, After: after}
	os.WriteFile(filepath.Join(wt, "b.txt"), []byte("dirty\n"), 0o644)
	wantErr(t, call(t, c, proto.MethodBranchRebaseUndo, undo, nil), "uncommitted")
	git(t, wt, "checkout", "--", "b.txt")
	if err := call(t, c, proto.MethodBranchRebaseUndo, undo, nil); err != nil {
		t.Fatal(err)
	}
	if gitOut(t, wt, "rev-parse", "HEAD") != before {
		t.Fatal("not put back")
	}
	wantErr(t, call(t, c, proto.MethodBranchRebaseUndo, undo, nil), "moved on")
	var got proto.BranchRebased
	if err := call(t, c, proto.MethodBranchRebased, proto.BranchRebasedParams{ProjectID: proj.ID}, &got); err != nil || len(got.Rebases) != 0 {
		t.Errorf("undone, still listed: %+v %v", got, err)
	}
	wantErr(t, call(t, c, proto.MethodBranchRebaseUndo, proto.BranchRebaseUndoParams{ProjectID: proj.ID, Branch: "nope", Before: before, After: after}, nil), "not checked out")
	wantErr(t, call(t, c, proto.MethodBranchRebaseUndo, proto.BranchRebaseUndoParams{ProjectID: proj.ID, Branch: "feat"}, nil), "")
	wantErr(t, call(t, c, proto.MethodBranchRebased, proto.BranchRebasedParams{ProjectID: "nope"}, &got), "")
}
