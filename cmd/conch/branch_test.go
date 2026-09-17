package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
)

// a4Harvest starts a fake server that answers the branch and worktree
// methods, and returns the work directory standing in for a task worktree.
func a4Harvest(t *testing.T) (*a4Server, string, *a4Var[map[string]proto.Message]) {
	t.Helper()
	a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	root := t.TempDir()
	root, _ = filepath.EvalSymlinks(root)
	wt := filepath.Join(root, "api.worktrees", "feat")
	os.MkdirAll(wt, 0o755)
	seen := newA4Var(map[string]proto.Message{})
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		got := seen.Get()
		got[msg.Method] = msg
		seen.Set(got)
		switch msg.Method {
		case proto.MethodProjectResolve:
			var p proto.ProjectAddParams
			json.Unmarshal(msg.Params, &p)
			if strings.Contains(p.Path, "notes") {
				return proto.ProjectPlace{ProjectID: "notes", Root: p.Path}, nil // not a repository
			}
			place := proto.ProjectPlace{ProjectID: "api", Root: root, Git: true, Base: "main"}
			switch {
			case strings.HasPrefix(p.Path, wt):
				place.Worktree, place.Branch = wt, "feat"
			case strings.HasPrefix(p.Path, root):
				place.Worktree, place.Branch = root, "main"
			}
			return place, nil
		case proto.MethodBranchCommit:
			return proto.CommitResult{Hash: "0123456789abcdef"}, nil
		case proto.MethodBranchPush:
			return nil, nil
		case proto.MethodBranchPR:
			return proto.BranchPRResult{URL: "https://github.com/o/r/pull/3"}, nil
		case proto.MethodBranchMerge:
			return proto.CommitResult{Hash: "fedcba9876543210", Into: "main"}, nil
		case proto.MethodBranchDiscard:
			var p proto.BranchDiscardParams
			json.Unmarshal(msg.Params, &p)
			res := proto.BranchDiscardResult{Worktree: wt, Uncommitted: []string{"a.go", "b.go"}, Unmerged: 1}
			if !p.DryRun {
				if !p.Force {
					return nil, proto.Errorf(proto.ErrBadRequest, "discarding feat would lose 2 uncommitted files and 1 commit")
				}
				res.Done = true
			}
			return res, nil
		case proto.MethodWorktreeStale:
			return proto.WorktreeStale{Worktrees: []proto.StaleWorktree{
				{Path: filepath.Join(root, "api.worktrees", "done"), Branch: "done", Merged: true,
					Reasons: []string{"merged into main"}, Suggested: true},
				{Path: wt, Branch: "feat", Uncommitted: 2, Unmerged: 1, Reasons: []string{"pull request closed"}},
				{Path: filepath.Join(root, "api.worktrees", "busy"), Branch: "busy", Panes: true},
				{Path: filepath.Join(root, "api.worktrees", "loose"), Reasons: []string{"detached"}, Suggested: true},
			}}, nil
		case proto.MethodWorktreeCleanup:
			var p proto.WorktreeCleanupParams
			json.Unmarshal(msg.Params, &p)
			res := proto.WorktreeCleanupResult{}
			for i, r := range p.Remove {
				if i == 0 {
					res.Removed = append(res.Removed, r.Path)
					continue
				}
				res.Failed = append(res.Failed, proto.CleanupFailure{Path: r.Path, Error: "panes are still running in it"})
			}
			return res, nil
		}
		return nil, proto.Errorf(proto.ErrUnknown, "?")
	})
	return srv, wt, seen
}

// a4Out runs fn and returns its output and error.
func a4Out(t *testing.T, fn func() error) (string, string, error) {
	t.Helper()
	var err error
	out, errOut := a4Capture(t, "", func() { err = fn() })
	return out, errOut, err
}

func TestA4BranchCommands(t *testing.T) {
	_, wt, seen := a4Harvest(t)
	t.Chdir(wt)

	// The branch comes from the directory, and files repeat.
	out, _, err := a4Out(t, func() error {
		return runBranch([]string{"commit", "-m", "Fix it", "-file", "a.go", "-file", "b.go"})
	})
	if err != nil || out != "0123456  feat\n" {
		t.Fatalf("commit: %q %v", out, err)
	}
	var cp proto.BranchCommitParams
	json.Unmarshal(seen.Get()[proto.MethodBranchCommit].Params, &cp)
	if cp.ProjectID != "api" || cp.Branch != "feat" || cp.Message != "Fix it" || strings.Join(cp.Files, ",") != "a.go,b.go" {
		t.Fatalf("commit params %+v", cp)
	}
	if _, _, err := a4Out(t, func() error { return runBranch([]string{"commit"}) }); err == nil || !strings.Contains(err.Error(), "needs a message") {
		t.Fatalf("no message: %v", err)
	}

	out, _, err = a4Out(t, func() error { return runBranch([]string{"push"}) })
	if err != nil || out != "pushed feat\n" {
		t.Fatalf("push: %q %v", out, err)
	}

	out, _, err = a4Out(t, func() error {
		return runBranch([]string{"pr", "-title", "Add it", "-body", "why", "-draft"})
	})
	if err != nil || out != "https://github.com/o/r/pull/3\n" {
		t.Fatalf("pr: %q %v", out, err)
	}
	var pp proto.BranchPRParams
	json.Unmarshal(seen.Get()[proto.MethodBranchPR].Params, &pp)
	if pp.Title != "Add it" || pp.Body != "why" || !pp.Draft || pp.Branch != "feat" {
		t.Fatalf("pr params %+v", pp)
	}

	out, _, err = a4Out(t, func() error { return runBranch([]string{"merge", "-m", "Land it"}) })
	if err != nil || out != "fedcba9  merged feat into main\n" {
		t.Fatalf("merge: %q %v", out, err)
	}
	var mp proto.BranchMergeParams
	json.Unmarshal(seen.Get()[proto.MethodBranchMerge].Params, &mp)
	if !mp.Squash || mp.Message != "Land it" {
		t.Fatalf("merge params %+v", mp)
	}
	a4Out(t, func() error { return runBranch([]string{"merge", "-no-squash"}) })
	var plain proto.BranchMergeParams
	json.Unmarshal(seen.Get()[proto.MethodBranchMerge].Params, &plain)
	if plain.Squash {
		t.Fatal("-no-squash still squashed")
	}

	// discard: -n only reports, plain discard is refused, -force does it.
	out, _, err = a4Out(t, func() error { return runBranch([]string{"discard", "-n"}) })
	if err != nil || !strings.Contains(out, "would remove feat and "+wt) ||
		!strings.Contains(out, "loses 2 uncommitted files (a.go, b.go) and 1 commit not merged or pushed") {
		t.Fatalf("discard -n: %q %v", out, err)
	}
	if _, _, err := a4Out(t, func() error { return runBranch([]string{"discard"}) }); err == nil || !strings.Contains(err.Error(), "would lose") {
		t.Fatalf("discard: %v", err)
	}
	out, _, err = a4Out(t, func() error { return runBranch([]string{"discard", "-force"}) })
	if err != nil || !strings.HasPrefix(out, "discarded feat and ") {
		t.Fatalf("discard -force: %q %v", out, err)
	}

	// -branch names another branch; an unknown subcommand and help.
	a4Out(t, func() error { return runBranch([]string{"push", "-branch", "other"}) })
	var br proto.BranchRef
	json.Unmarshal(seen.Get()[proto.MethodBranchPush].Params, &br)
	if br.Branch != "other" {
		t.Fatalf("-branch: %+v", br)
	}
	if _, _, err := a4Out(t, func() error { return runBranch([]string{"nope"}) }); err == nil || !strings.Contains(err.Error(), "unknown branch command") {
		t.Fatalf("unknown: %v", err)
	}
	if out, _, err := a4Out(t, func() error { return runBranch(nil) }); err == nil || out != "" {
		t.Fatalf("no arguments: %q %v", out, err)
	}
	if out, _, err := a4Out(t, func() error { return runBranch([]string{"help"}) }); err != nil || !strings.Contains(out, "conch branch commit") {
		t.Fatalf("help: %q %v", out, err)
	}
}

func TestA4BranchNeedsABranchAndARepository(t *testing.T) {
	_, wt, _ := a4Harvest(t)
	outside := t.TempDir()
	t.Chdir(outside)
	// A directory that is in no worktree of the project: say so.
	if _, _, err := a4Out(t, func() error { return runBranch([]string{"push"}) }); err == nil || !strings.Contains(err.Error(), "no branch is checked out") {
		t.Fatalf("outside a worktree: %v", err)
	}
	// -branch works from anywhere.
	if _, _, err := a4Out(t, func() error { return runBranch([]string{"push", "-branch", "feat"}) }); err != nil {
		t.Fatalf("with -branch: %v", err)
	}
	// A plain folder is not a git repository.
	notes := filepath.Join(t.TempDir(), "notes")
	os.MkdirAll(notes, 0o755)
	if _, _, err := a4Out(t, func() error { return runBranch([]string{"push", "-cwd", notes}) }); err == nil || !strings.Contains(err.Error(), "not a git repository") {
		t.Fatalf("plain folder: %v", err)
	}
	// -cwd picks the worktree.
	if _, _, err := a4Out(t, func() error { return runBranch([]string{"push", "-cwd", wt}) }); err != nil {
		t.Fatalf("-cwd: %v", err)
	}
}

func TestA4BranchOldServer(t *testing.T) {
	srv, wt, _ := a4Harvest(t)
	t.Chdir(wt)
	srv.setHello(func(n int) proto.HelloResult {
		h := currentHello(n)
		var kept []string
		for _, cap := range h.Capabilities {
			if !strings.HasPrefix(cap, "branch.") && !strings.HasPrefix(cap, "worktree.cleanup") && cap != "project.resolve.v1" {
				kept = append(kept, cap)
			}
		}
		h.Capabilities = kept
		return h
	})
	for _, args := range [][]string{{"commit", "-m", "x"}, {"push"}, {"pr"}, {"merge"}, {"discard"}} {
		_, _, err := a4Out(t, func() error { return runBranch(args) })
		if err == nil || !strings.Contains(err.Error(), "branch.harvest.v1") || !strings.Contains(err.Error(), "older build") {
			t.Fatalf("branch %v: %v", args, err)
		}
	}
	for _, args := range [][]string{{"ls"}, {"clean"}} {
		_, _, err := a4Out(t, func() error { return runWorktree(args) })
		if err == nil || !strings.Contains(err.Error(), "worktree.cleanup.v1") {
			t.Fatalf("worktree %v: %v", args, err)
		}
	}
}

func TestA4WorktreeCommands(t *testing.T) {
	_, wt, seen := a4Harvest(t)
	t.Chdir(wt)

	out, _, err := a4Out(t, func() error { return runWorktree([]string{"ls"}) })
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"BRANCH", "done", "merged into main (clean)", "feat", "pull request closed",
		"2 uncommitted files and 1 commit not merged or pushed", "busy", "panes running", "(detached)", "detached (clean)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("ls lacks %q:\n%s", want, out)
		}
	}

	// clean without -y only says what it would do.
	out, _, err = a4Out(t, func() error { return runWorktree([]string{"clean"}) })
	if err != nil || !strings.Contains(out, "would remove done") || !strings.Contains(out, "2 to remove; run again with -y") {
		t.Fatalf("clean dry run: %q %v", out, err)
	}
	if strings.Contains(out, "feat") || strings.Contains(out, "busy") {
		t.Fatalf("clean picked more than the finished ones:\n%s", out)
	}
	if _, ok := seen.Get()[proto.MethodWorktreeCleanup]; ok {
		t.Fatal("a dry run removed worktrees")
	}

	// Named worktrees that would lose work are kept unless forced.
	out, _, err = a4Out(t, func() error { return runWorktree([]string{"clean", "feat"}) })
	if err != nil || !strings.Contains(out, "keeping feat: loses 2 uncommitted files and 1 commit") || !strings.Contains(out, "nothing to clean up") {
		t.Fatalf("named lossy: %q %v", out, err)
	}
	out, _, err = a4Out(t, func() error { return runWorktree([]string{"clean", "-force", "feat"}) })
	if err != nil || !strings.Contains(out, "would remove feat") {
		t.Fatalf("named forced: %q %v", out, err)
	}

	// -y removes them; a failure is reported on stderr and fails the command.
	out, errOut, err := a4Out(t, func() error { return runWorktree([]string{"clean", "-y"}) })
	if err == nil || !strings.Contains(err.Error(), "1 worktree could not be removed") {
		t.Fatalf("clean -y: %v", err)
	}
	if !strings.Contains(out, "removing done") || !strings.Contains(out, "removed 1 worktree") {
		t.Fatalf("clean -y output: %q", out)
	}
	if !strings.Contains(errOut, "kept loose: panes are still running in it") {
		t.Fatalf("clean -y stderr: %q", errOut)
	}
	var cp proto.WorktreeCleanupParams
	json.Unmarshal(seen.Get()[proto.MethodWorktreeCleanup].Params, &cp)
	if len(cp.Remove) != 2 || cp.Remove[0].Force || cp.ProjectID != "api" {
		t.Fatalf("cleanup params %+v", cp)
	}

	if _, _, err := a4Out(t, func() error { return runWorktree([]string{"nope"}) }); err == nil || !strings.Contains(err.Error(), "unknown worktree command") {
		t.Fatalf("unknown: %v", err)
	}
	if out, _, err := a4Out(t, func() error { return runWorktree([]string{"help"}) }); err != nil || !strings.Contains(out, "conch worktree clean") {
		t.Fatalf("help: %q %v", out, err)
	}
}

func TestA4WorktreeNothingToList(t *testing.T) {
	srv, wt, _ := a4Harvest(t)
	t.Chdir(wt)
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		switch msg.Method {
		case proto.MethodProjectResolve:
			return proto.ProjectPlace{ProjectID: "api", Root: wt, Git: true, Base: "main", Worktree: wt, Branch: "main"}, nil
		case proto.MethodWorktreeStale:
			return proto.WorktreeStale{}, nil
		}
		return nil, proto.Errorf(proto.ErrUnknown, "?")
	})
	if out, _, err := a4Out(t, func() error { return runWorktree([]string{"ls"}) }); err != nil || !strings.Contains(out, "no worktrees besides") {
		t.Fatalf("empty ls: %q %v", out, err)
	}
	if out, _, err := a4Out(t, func() error { return runWorktree(nil) }); err != nil || !strings.Contains(out, "no worktrees besides") {
		t.Fatalf("bare worktree: %q %v", out, err)
	}
	if out, _, err := a4Out(t, func() error { return runWorktree([]string{"clean", "-y"}) }); err != nil || !strings.Contains(out, "nothing to clean up") {
		t.Fatalf("empty clean: %q %v", out, err)
	}
}

func TestA4BranchOnAnotherMachine(t *testing.T) {
	_, wt, _ := a4Harvest(t)
	t.Chdir(wt)
	old := machineFlag
	machineFlag = "devbox"
	t.Cleanup(func() { machineFlag = old })

	// This computer's directory means nothing there.
	for _, args := range [][]string{{"push"}, {"commit", "-m", "x"}} {
		_, _, err := a4Out(t, func() error { return runBranch(args) })
		if err == nil || !strings.Contains(err.Error(), "needs -cwd") {
			t.Fatalf("branch %v without -cwd: %v", args, err)
		}
	}
	if _, _, err := a4Out(t, func() error { return runWorktree([]string{"ls"}) }); err == nil || !strings.Contains(err.Error(), "needs -cwd") {
		t.Fatalf("worktree ls without -cwd: %v", err)
	}
	// A relative -cwd is refused rather than resolved here.
	if _, _, err := a4Out(t, func() error { return runBranch([]string{"push", "-cwd", "src/api"}) }); err == nil || !strings.Contains(err.Error(), "absolute path") {
		t.Fatalf("relative -cwd: %v", err)
	}
}
