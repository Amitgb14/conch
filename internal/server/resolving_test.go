package server_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/Amitgb14/conch/internal/proto"
)

// resolveOf is the ask on the worktree at path, as the project says.
func resolveOf(p proto.ProjectInfo, path string) *proto.ResolveAsk {
	for _, w := range p.Worktrees {
		if w.Path == path {
			return w.Resolve
		}
	}
	return nil
}

// Who was asked to resolve a branch's conflict is on its worktree for as
// long as the conflict is there, so a client can say an agent is at it,
// and gone once the rebase is finished.
func TestBranchResolving(t *testing.T) {
	c, proj, repo, wt, _ := harvestFixture(t)
	if miss := c.MissingCapabilities([]string{proto.CapBranchResolving}); len(miss) > 0 {
		t.Fatalf("missing %v", miss)
	}
	os.WriteFile(filepath.Join(wt, "a.txt"), []byte("feat\n"), 0o644)
	git(t, wt, "commit", "-qam", "feat edits a")
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("main\n"), 0o644)
	git(t, repo, "commit", "-qam", "main edits a")
	cmd := exec.Command("git", "-C", wt, "rebase", "main")
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("expected a conflict:\n%s", out)
	}
	var pane proto.PaneInfo
	if err := call(t, c, proto.MethodPaneCreate, proto.PaneCreateParams{Command: []string{"/bin/sh", "-c", "sleep 30"}, Cwd: wt}, &pane); err != nil {
		t.Fatal(err)
	}

	// Mid-rebase git names no branch; the ask is by the branch all the same.
	ask := proto.BranchResolvingParams{ProjectID: proj.ID, Branch: "feat", PaneID: pane.ID}
	if err := call(t, c, proto.MethodBranchResolving, ask, nil); err != nil {
		t.Fatal(err)
	}
	got := waitProject(t, c, func(p proto.ProjectInfo) bool { return p.ID == proj.ID && resolveOf(p, wt) != nil })
	r := resolveOf(got, wt)
	if r.Pane != pane.ID || r.By != "" || r.At.IsZero() || !slices.Equal(r.Files, []string{"a.txt"}) {
		t.Errorf("ask: %+v", r)
	}

	// Taken back by the person: no longer said.
	if err := call(t, c, proto.MethodBranchResolving, proto.BranchResolvingParams{ProjectID: proj.ID, Branch: "feat"}, nil); err != nil {
		t.Fatal(err)
	}
	waitProject(t, c, func(p proto.ProjectInfo) bool { return p.ID == proj.ID && resolveOf(p, wt) == nil })

	// Asked again, then resolved: gone with the conflict.
	if err := call(t, c, proto.MethodBranchResolving, ask, nil); err != nil {
		t.Fatal(err)
	}
	waitProject(t, c, func(p proto.ProjectInfo) bool { return p.ID == proj.ID && resolveOf(p, wt) != nil })
	os.WriteFile(filepath.Join(wt, "a.txt"), []byte("main\nfeat\n"), 0o644)
	git(t, wt, "add", "a.txt")
	cmd = exec.Command("git", "-C", wt, "rebase", "--continue")
	cmd.Env = append(os.Environ(), "GIT_EDITOR=true", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("continue: %v\n%s", err, out)
	}
	waitProject(t, c, func(p proto.ProjectInfo) bool {
		for _, w := range p.Worktrees {
			if w.Path == wt {
				return w.Branch == "feat" && w.Resolve == nil
			}
		}
		return false
	})

	wantErr(t, call(t, c, proto.MethodBranchResolving, proto.BranchResolvingParams{ProjectID: proj.ID, Branch: "feat", PaneID: "nope"}, nil), "no pane")
	wantErr(t, call(t, c, proto.MethodBranchResolving, proto.BranchResolvingParams{ProjectID: proj.ID, Branch: "nope", PaneID: pane.ID}, nil), "not checked out")
	wantErr(t, call(t, c, proto.MethodBranchResolving, proto.BranchResolvingParams{ProjectID: proj.ID, PaneID: pane.ID}, nil), "")
	wantErr(t, call(t, c, proto.MethodBranchResolving, proto.BranchResolvingParams{ProjectID: "nope", Branch: "feat"}, nil), "")
}
