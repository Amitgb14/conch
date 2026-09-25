package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

// harvestWait covers pushes and gh, which reach the network.
const harvestWait = 3 * time.Minute

const branchUsage = `Usage:
  conch branch commit [-cwd DIR] [-branch B] [-file PATH]... -m MESSAGE
  conch branch push   [-cwd DIR] [-branch B]
  conch branch pr     [-cwd DIR] [-branch B] [-title T] [-body B] [-draft]
  conch branch merge  [-cwd DIR] [-branch B] [-no-squash] [-m MESSAGE]
  conch branch discard [-cwd DIR] [-branch B] [-force] [-n]

Without -branch, the branch checked out in DIR (the current directory) is used.`

const worktreeUsage = `Usage:
  conch worktree ls    [-cwd DIR]
  conch worktree clean [-cwd DIR] [-y] [-force] [PATH...]

clean removes the worktrees that look finished and lose nothing, or the ones
named. Without -y it only prints what it would remove.`

// branchFlags are the flags every branch command shares.
type branchFlags struct {
	fs          *flag.FlagSet
	cwd, branch *string
}

func newBranchFlags(name string) *branchFlags {
	fs := flag.NewFlagSet("branch "+name, flag.ContinueOnError)
	return &branchFlags{
		fs:     fs,
		cwd:    fs.String("cwd", "", "a directory in the project (default: current)"),
		branch: fs.String("branch", "", "branch (default: the one checked out in the directory)"),
	}
}

// open parses the flags, connects, and resolves which branch to work on.
func (bf *branchFlags) open(args []string) (*client.Client, string, string, error) {
	if err := bf.fs.Parse(args); err != nil {
		return nil, "", "", err
	}
	if err := checkDir(*bf.cwd); err != nil {
		return nil, "", "", err
	}
	c, err := connect(true)
	if err != nil {
		return nil, "", "", err
	}
	if missing := c.MissingCapabilities([]string{"branch.harvest.v1", "project.resolve.v1"}); len(missing) > 0 {
		c.Close()
		return nil, "", "", fmt.Errorf("the conch server (pid %d) is from an older build without %s; reload it with `conch server reload`", c.Server.PID, strings.Join(missing, ", "))
	}
	id, branch, err := where(c, *bf.cwd, *bf.branch)
	if err != nil {
		c.Close()
		return nil, "", "", err
	}
	return c, id, branch, nil
}

// place asks the server which project and checkout dir is in. The server
// reads git, so a worktree made moments ago is already known and paths on
// another machine resolve there, not here.
// checkDir refuses a directory that cannot mean what it says on another
// machine, before anything connects to it.
func checkDir(dir string) error {
	switch {
	case !onRemoteMachine():
		return nil
	case dir == "":
		// This computer's working directory names nothing over there.
		return fmt.Errorf("conch -m %s needs -cwd: a directory in a project on %s", machineFlag, machineFlag)
	}
	return remoteDir(dir)
}

func place(c *client.Client, dir string) (proto.ProjectPlace, error) {
	var out proto.ProjectPlace
	if dir == "" {
		var err error
		if dir, err = os.Getwd(); err != nil {
			return out, err
		}
	}
	if err := call(c, proto.MethodProjectResolve, proto.ProjectAddParams{Path: dir}, &out); err != nil {
		return out, err
	}
	if !out.Git {
		return out, fmt.Errorf("%s is not a git repository", out.Root)
	}
	return out, nil
}

// where resolves the project containing dir and the branch to act on: the
// given one, or the branch checked out in dir.
func where(c *client.Client, dir, branch string) (projectID, name string, err error) {
	at, err := place(c, dir)
	if err != nil {
		return "", "", err
	}
	if branch != "" {
		return at.ProjectID, branch, nil
	}
	if at.Branch == "" {
		where := dir
		if where == "" {
			where, _ = os.Getwd()
		}
		return "", "", fmt.Errorf("no branch is checked out in %s; name one with -branch", where)
	}
	return at.ProjectID, at.Branch, nil
}

// stringList collects a flag given several times.
type stringList []string

func (l *stringList) String() string     { return strings.Join(*l, ",") }
func (l *stringList) Set(v string) error { *l = append(*l, v); return nil }

func runBranch(args []string) error {
	if len(args) == 0 {
		return errors.New(branchUsage)
	}
	sub, args := args[0], args[1:]
	switch sub {
	case "commit":
		return branchCommit(args)
	case "push":
		return branchPush(args)
	case "pr":
		return branchPR(args)
	case "merge":
		return branchMerge(args)
	case "discard":
		return branchDiscard(args)
	case "help", "-h", "--help":
		fmt.Print(branchUsage + "\n")
		return nil
	}
	return fmt.Errorf("unknown branch command %q\n\n%s", sub, branchUsage)
}

func branchCommit(args []string) error {
	bf := newBranchFlags("commit")
	msg := bf.fs.String("m", "", "commit message (required)")
	var files stringList
	bf.fs.Var(&files, "file", "commit only this path (repeat for more; a rename needs both its paths)")
	c, id, branch, err := bf.open(args)
	if err != nil {
		return err
	}
	defer c.Close()
	if strings.TrimSpace(*msg) == "" {
		return errors.New("a commit needs a message: -m MESSAGE")
	}
	var res proto.CommitResult
	params := proto.BranchCommitParams{ProjectID: id, Branch: branch, Message: *msg, Files: files}
	if err := callFor(c, proto.MethodBranchCommit, params, &res, harvestWait); err != nil {
		return err
	}
	fmt.Printf("%s  %s\n", res.Hash[:min(len(res.Hash), 7)], branch)
	return nil
}

func branchPush(args []string) error {
	bf := newBranchFlags("push")
	rebase := bf.fs.Bool("rebase", false, "when the remote is ahead, take its commits and put this branch's on top")
	c, id, branch, err := bf.open(args)
	if err != nil {
		return err
	}
	defer c.Close()
	if *rebase {
		if miss := c.MissingCapabilities([]string{proto.CapBranchRebase}); len(miss) > 0 {
			return fmt.Errorf("the server there predates -rebase; pull --rebase in the worktree, then push")
		}
	}
	var res proto.BranchPushResult
	params := proto.BranchPushParams{ProjectID: id, Branch: branch, Rebase: *rebase}
	if err := callFor(c, proto.MethodBranchPush, params, &res, harvestWait); err != nil {
		var perr *proto.Error
		if errors.As(err, &perr) {
			switch perr.Code {
			case proto.ErrPushRejected:
				return fmt.Errorf("%v; push -rebase takes them and puts %s's own commits on top", err, branch)
			case proto.ErrRebaseConflict:
				// Sorting a conflict out is work in the worktree, so say
				// what to run there rather than only what stopped.
				return fmt.Errorf("%v\nsort it out in the worktree:\n  git pull --rebase\n"+
					"  fix the files it names, then git rebase --continue\n"+
					"  conch branch push, or git push, once it is done", err)
			}
		}
		return err
	}
	if res.Took > 0 {
		fmt.Printf("pushed %s after taking %d commit(s) from the remote\n", branch, res.Took)
		return nil
	}
	fmt.Printf("pushed %s\n", branch)
	return nil
}

func branchPR(args []string) error {
	bf := newBranchFlags("pr")
	title := bf.fs.String("title", "", "pull request title (default: from the commits)")
	body := bf.fs.String("body", "", "pull request description")
	draft := bf.fs.Bool("draft", false, "open it as a draft")
	c, id, branch, err := bf.open(args)
	if err != nil {
		return err
	}
	defer c.Close()
	var res proto.BranchPRResult
	params := proto.BranchPRParams{ProjectID: id, Branch: branch, Title: *title, Body: *body, Draft: *draft}
	if err := callFor(c, proto.MethodBranchPR, params, &res, harvestWait); err != nil {
		return err
	}
	fmt.Println(res.URL)
	return nil
}

func branchMerge(args []string) error {
	bf := newBranchFlags("merge")
	noSquash := bf.fs.Bool("no-squash", false, "make a merge commit instead of one squashed commit")
	msg := bf.fs.String("m", "", "commit message (default: git's own)")
	c, id, branch, err := bf.open(args)
	if err != nil {
		return err
	}
	defer c.Close()
	var res proto.CommitResult
	params := proto.BranchMergeParams{ProjectID: id, Branch: branch, Squash: !*noSquash, Message: *msg}
	if err := callFor(c, proto.MethodBranchMerge, params, &res, harvestWait); err != nil {
		return err
	}
	fmt.Printf("%s  merged %s into %s\n", res.Hash[:min(len(res.Hash), 7)], branch, res.Into)
	return nil
}

func branchDiscard(args []string) error {
	bf := newBranchFlags("discard")
	force := bf.fs.Bool("force", false, "discard even when that loses uncommitted files or commits")
	dry := bf.fs.Bool("n", false, "only print what discarding would remove and lose")
	c, id, branch, err := bf.open(args)
	if err != nil {
		return err
	}
	defer c.Close()
	var res proto.BranchDiscardResult
	params := proto.BranchDiscardParams{ProjectID: id, Branch: branch, Force: *force, DryRun: *dry}
	if err := callFor(c, proto.MethodBranchDiscard, params, &res, harvestWait); err != nil {
		return err
	}
	what := "would remove"
	if res.Done {
		what = "discarded"
	}
	line := fmt.Sprintf("%s %s", what, branch)
	if res.Worktree != "" {
		line += " and " + res.Worktree
	}
	if lost := lossText(len(res.Uncommitted), res.Unmerged, res.Uncommitted); lost != "" {
		line += "; loses " + lost
	}
	fmt.Println(line)
	return nil
}

// lossText says what removing something loses. names, when given, lists the
// uncommitted files themselves.
func lossText(files, commits int, names []string) string {
	var parts []string
	if files > 0 {
		part := fmt.Sprintf("%d uncommitted file%s", files, plural(files))
		if len(names) > 0 {
			part += " (" + strings.Join(names, ", ") + ")"
		}
		parts = append(parts, part)
	}
	if commits > 0 {
		parts = append(parts, fmt.Sprintf("%d commit%s not merged or pushed", commits, plural(commits)))
	}
	return strings.Join(parts, " and ")
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func runWorktree(args []string) error {
	if len(args) == 0 {
		args = []string{"ls"}
	}
	sub, args := args[0], args[1:]
	switch sub {
	case "ls", "list":
		return worktreeList(args)
	case "clean", "cleanup":
		return worktreeClean(args)
	case "help", "-h", "--help":
		fmt.Print(worktreeUsage + "\n")
		return nil
	}
	return fmt.Errorf("unknown worktree command %q\n\n%s", sub, worktreeUsage)
}

// openWorktrees connects and lists the project's worktrees.
func openWorktrees(fs *flag.FlagSet, cwd *string, args []string) (*client.Client, string, proto.WorktreeStale, error) {
	var stale proto.WorktreeStale
	if err := fs.Parse(args); err != nil {
		return nil, "", stale, err
	}
	if err := checkDir(*cwd); err != nil {
		return nil, "", stale, err
	}
	c, err := connect(true)
	if err != nil {
		return nil, "", stale, err
	}
	if missing := c.MissingCapabilities([]string{"worktree.cleanup.v1", "project.resolve.v1"}); len(missing) > 0 {
		c.Close()
		return nil, "", stale, fmt.Errorf("the conch server (pid %d) is from an older build without %s; reload it with `conch server reload`", c.Server.PID, strings.Join(missing, ", "))
	}
	at, err := place(c, *cwd)
	if err != nil {
		c.Close()
		return nil, "", stale, err
	}
	id := at.ProjectID
	if err := callFor(c, proto.MethodWorktreeStale, proto.ProjectRef{ID: id}, &stale, harvestWait); err != nil {
		c.Close()
		return nil, "", stale, err
	}
	return c, id, stale, nil
}

func worktreeList(args []string) error {
	fs := flag.NewFlagSet("worktree ls", flag.ContinueOnError)
	cwd := fs.String("cwd", "", "a directory in the project (default: current)")
	c, _, stale, err := openWorktrees(fs, cwd, args)
	if err != nil {
		return err
	}
	defer c.Close()
	if len(stale.Worktrees) == 0 {
		fmt.Println("no worktrees besides the main checkout")
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "BRANCH\tSTATE\tLOSES\tPATH")
	for _, w := range stale.Worktrees {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", worktreeLabel(w), worktreeState(w), orDash(lossText(w.Uncommitted, w.Unmerged, nil)), w.Path)
	}
	return tw.Flush()
}

func worktreeLabel(w proto.StaleWorktree) string {
	if w.Branch == "" {
		return "(detached)"
	}
	return w.Branch
}

// worktreeState is what the TUI's list shows in a word: why it can't go, or
// why it looks finished.
func worktreeState(w proto.StaleWorktree) string {
	switch {
	case w.Panes:
		return "panes running"
	case w.Locked:
		return "locked"
	case w.Base:
		return "base branch"
	case len(w.Reasons) > 0:
		state := strings.Join(w.Reasons, ", ")
		if w.Suggested {
			state += " (clean)"
		}
		return state
	}
	return "in use"
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func worktreeClean(args []string) error {
	fs := flag.NewFlagSet("worktree clean", flag.ContinueOnError)
	cwd := fs.String("cwd", "", "a directory in the project (default: current)")
	yes := fs.Bool("y", false, "remove them, rather than only printing what would go")
	force := fs.Bool("force", false, "remove even when that loses uncommitted files or commits")
	c, id, stale, err := openWorktrees(fs, cwd, args)
	if err != nil {
		return err
	}
	defer c.Close()

	wanted := map[string]bool{}
	for _, p := range fs.Args() {
		wanted[p] = true
	}
	var remove []proto.CleanupWorktree
	for _, w := range stale.Worktrees {
		switch {
		case len(wanted) > 0 && !wanted[w.Path] && !wanted[w.Branch]:
			continue
		case len(wanted) == 0 && !w.Suggested:
			continue
		}
		lost := lossText(w.Uncommitted, w.Unmerged, nil)
		if lost != "" && !*force {
			fmt.Printf("keeping %s: loses %s (-force to remove it anyway)\n", worktreeLabel(w), lost)
			continue
		}
		remove = append(remove, proto.CleanupWorktree{Path: w.Path, Force: *force})
		what := "would remove"
		if *yes {
			what = "removing"
		}
		fmt.Printf("%s %s  %s\n", what, worktreeLabel(w), w.Path)
	}
	if len(remove) == 0 {
		fmt.Println("nothing to clean up")
		return nil
	}
	if !*yes {
		fmt.Printf("%d to remove; run again with -y\n", len(remove))
		return nil
	}
	var res proto.WorktreeCleanupResult
	params := proto.WorktreeCleanupParams{ProjectID: id, Remove: remove}
	if err := callFor(c, proto.MethodWorktreeCleanup, params, &res, harvestWait); err != nil {
		return err
	}
	fmt.Printf("removed %d worktree%s\n", len(res.Removed), plural(len(res.Removed)))
	if len(res.Failed) == 0 {
		return nil
	}
	for _, f := range res.Failed {
		fmt.Fprintf(os.Stderr, "kept %s: %s\n", filepath.Base(f.Path), f.Error)
	}
	return fmt.Errorf("%d worktree%s could not be removed", len(res.Failed), plural(len(res.Failed)))
}
