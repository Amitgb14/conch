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

func TestA4TaskAttempts(t *testing.T) {
	// The naming is shared with the TUI; this covers the CLI's own flags.
	existing := []proto.BranchInfo{{Name: "conch/fix-tests/claude"}}
	plan := attemptPlan("conch", []string{"claude", "codex"}, 0, "", "Fix the tests", existing)
	var got []string
	for _, a := range plan {
		got = append(got, a.agent+"@"+a.branch)
	}
	if strings.Join(got, " ") != "claude@conch/fix-tests/claude-2 codex@conch/fix-tests/codex" {
		t.Fatalf("plan: %v", got)
	}
	if one := attemptPlan("conch", []string{"claude"}, 1, "", "Fix the tests", nil); len(one) != 1 || one[0].branch != "" {
		t.Fatalf("one attempt keeps the plain name: %+v", one)
	}
	if got := splitAgents(" Claude ,codex, "); strings.Join(got, ",") != "claude,codex" {
		t.Fatalf("splitAgents: %v", got)
	}
}

func TestA4TaskRunsEveryAttempt(t *testing.T) {
	a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	var calls a4Var[[]proto.TaskCreateParams]
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		switch msg.Method {
		case proto.MethodProjectAdd:
			return proto.ProjectInfo{ID: "api", Name: "api", Path: "/src/api", Git: true, Base: "main"}, nil
		case proto.MethodTaskCreate:
			var p proto.TaskCreateParams
			json.Unmarshal(msg.Params, &p)
			calls.Set(append(calls.Get(), p))
			if p.Agent == "codex" {
				return nil, proto.Errorf(proto.ErrBadRequest, "codex is not installed")
			}
			return proto.PaneInfo{ID: "p" + p.Branch, Cwd: "/src/api-x", Branch: p.Branch}, nil
		}
		return nil, proto.Errorf(proto.ErrUnknown, "?")
	})
	work := t.TempDir()

	// One agent, no -n: a single plain task, as before.
	out, _, err := a4Out(t, func() error { return runTask([]string{"-cwd", work, "Fix the tests"}) })
	if err != nil || !strings.Contains(out, "p") {
		t.Fatalf("plain task: %q %v", out, err)
	}
	if got := calls.Get(); len(got) != 1 || got[0].Branch != "" {
		t.Fatalf("plain task params: %+v", got)
	}

	// Three attempts across two agents: the failing one is reported and the
	// others still start.
	calls.Set(nil)
	out, errOut, err := a4Out(t, func() error {
		return runTask([]string{"-cwd", work, "-agent", "claude,codex", "-n", "3", "Fix the tests"})
	})
	if err == nil || !strings.Contains(err.Error(), "1 of 3 attempts could not start") {
		t.Fatalf("partial failure: %v", err)
	}
	if !strings.Contains(errOut, "codex is not installed") {
		t.Fatalf("stderr: %q", errOut)
	}
	if lines := strings.Count(strings.TrimSpace(out), "\n") + 1; lines != 2 {
		t.Fatalf("%d started, want 2: %q", lines, out)
	}
	var branches []string
	for _, c := range calls.Get() {
		branches = append(branches, c.Agent+"@"+c.Branch)
	}
	// The attempts sit under the project's own name, not conch's.
	if strings.Join(branches, " ") != "claude@api/fix-tests/claude codex@api/fix-tests/codex claude@api/fix-tests/claude-2" {
		t.Fatalf("attempts: %v", branches)
	}

	// Guard rails on -n.
	for _, args := range [][]string{{"-n", "-1"}, {"-n", "99"}} {
		if _, _, err := a4Out(t, func() error { return runTask(append([]string{"-cwd", work}, append(args, "p")...)) }); err == nil {
			t.Fatalf("%v was accepted", args)
		}
	}
	if _, _, err := a4Out(t, func() error { return runTask([]string{"-cwd", work}) }); err == nil || !strings.Contains(err.Error(), "usage:") {
		t.Fatalf("no prompt: %v", err)
	}
}

// push -rebase asks the server to take what the remote has first, and a
// push the remote is ahead of says what mends it.
func TestA4BranchPushRebase(t *testing.T) {
	srv, wt, seen := a4Harvest(t)
	t.Chdir(wt)
	rejected, conflict := newA4Var(true), newA4Var(false)
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		got := seen.Get()
		got[msg.Method] = msg
		seen.Set(got)
		switch msg.Method {
		case proto.MethodProjectResolve:
			return proto.ProjectPlace{ProjectID: "api", Root: wt, Git: true, Base: "main", Worktree: wt, Branch: "feat"}, nil
		case proto.MethodBranchPush:
			var p proto.BranchPushParams
			json.Unmarshal(msg.Params, &p)
			if p.Rebase {
				if conflict.Get() {
					return nil, proto.Errorf(proto.ErrRebaseConflict,
						"rebasing feat onto origin/feat conflicts in a.go; nothing was changed or pushed")
				}
				return proto.BranchPushResult{Took: 2}, nil
			}
			if rejected.Get() {
				return nil, proto.Errorf(proto.ErrPushRejected, "feat on origin has commits this checkout does not have")
			}
			return proto.BranchPushResult{}, nil
		}
		return nil, nil
	})

	// Rejected: the reason, and what to run instead of reading git's hints.
	_, _, err := a4Out(t, func() error { return runBranch([]string{"push"}) })
	if err == nil || !strings.Contains(err.Error(), "commits this checkout does not have") ||
		!strings.Contains(err.Error(), "push -rebase") {
		t.Fatalf("rejected push: %v", err)
	}
	// -rebase takes them and says how many.
	out, _, err := a4Out(t, func() error { return runBranch([]string{"push", "-rebase"}) })
	if err != nil || !strings.Contains(out, "pushed feat after taking 2 commit(s) from the remote") {
		t.Fatalf("push -rebase: %q %v", out, err)
	}
	var p proto.BranchPushParams
	json.Unmarshal(seen.Get()[proto.MethodBranchPush].Params, &p)
	if !p.Rebase || p.Branch != "feat" {
		t.Fatalf("params %+v", p)
	}
	// An ordinary push that works says so, with nothing about rebasing.
	rejected.Set(false)
	out, _, err = a4Out(t, func() error { return runBranch([]string{"push"}) })
	if err != nil || strings.TrimSpace(out) != "pushed feat" {
		t.Fatalf("plain push: %q %v", out, err)
	}

	// A rebase stopped by a conflict says what to run in the worktree.
	conflict.Set(true)
	_, _, err = a4Out(t, func() error { return runBranch([]string{"push", "-rebase"}) })
	if err == nil {
		t.Fatal("a conflict should fail")
	}
	for _, want := range []string{"conflicts in a.go", "sort it out in the worktree",
		"git pull --rebase", "git rebase --continue", "conch branch push, or git push"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the conflict lacks %q: %v", want, err)
		}
	}
	conflict.Set(false)

	// A server too old for -rebase says what to do by hand, and is not asked.
	srv.setHello(func(n int) proto.HelloResult {
		h := currentHello(n)
		var kept []string
		for _, cap := range h.Capabilities {
			if cap != proto.CapBranchRebase {
				kept = append(kept, cap)
			}
		}
		h.Capabilities = kept
		return h
	})
	if _, _, err := a4Out(t, func() error { return runBranch([]string{"push", "-rebase"}) }); err == nil ||
		!strings.Contains(err.Error(), "predates -rebase") {
		t.Fatalf("old server: %v", err)
	}
}
