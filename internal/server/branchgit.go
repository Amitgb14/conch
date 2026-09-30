package server

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/Amitgb14/conch/internal/gitx"
	"github.com/Amitgb14/conch/internal/proto"
)

// maxGitOutput caps what one branch.git call sends back: the start of a
// long log is what the panel shows, and the rest would only fill the
// protocol.
const maxGitOutput = 256 << 10

// maxGitCommands caps how many commands one call chains.
const maxGitCommands = 8

// branchGit runs the git panel's commands in the branch's worktree. A
// command that fails is part of the result, not an error: the panel shows
// git's own words for it, and what it left unfinished.
func (pm *projectManager) branchGit(gp proto.BranchGitParams) (proto.BranchGitResult, *proto.Error) {
	if len(gp.Commands) > maxGitCommands {
		return proto.BranchGitResult{}, proto.Errorf(proto.ErrBadRequest, "at most %d git commands at once", maxGitCommands)
	}
	for _, c := range gp.Commands {
		if err := gitx.CheckUserCommand(c); err != nil {
			return proto.BranchGitResult{}, proto.Errorf(proto.ErrBadRequest, "%v", err)
		}
	}
	p, perr := pm.gitProject(gp.ProjectID)
	if perr != nil {
		return proto.BranchGitResult{}, perr
	}
	if gp.Branch == "" {
		return proto.BranchGitResult{}, proto.Errorf(proto.ErrBadRequest, "no branch given")
	}
	// Fetches and pulls reach the network.
	ctx, cancel := context.WithTimeout(context.Background(), networkTimeout)
	defer cancel()
	wt, ok, perr := gitCheckout(ctx, p, gp.Branch)
	switch {
	case perr != nil:
		return proto.BranchGitResult{}, perr
	case !ok:
		return proto.BranchGitResult{}, proto.Errorf(proto.ErrBadRequest,
			"%s is not checked out anywhere; open a terminal on it to make its worktree", gp.Branch)
	}
	res := proto.BranchGitResult{Worktree: wt.Path}
	var out strings.Builder
	for _, args := range gp.Commands {
		fmt.Fprintf(&out, "$ git %s\n", quoteArgs(args))
		text, code, trunc, err := gitx.RunUser(ctx, wt.Path, args, max(maxGitOutput-out.Len(), 0))
		out.WriteString(text)
		if text != "" && !strings.HasSuffix(text, "\n") {
			out.WriteString("\n")
		}
		res.ExitCode, res.Truncated = code, res.Truncated || trunc
		if err != nil {
			res.ExitCode = -1
			fmt.Fprintf(&out, "%v\n", err)
			break
		}
		if code != 0 {
			break
		}
	}
	res.Output = out.String()
	res.InProgress = gitx.InProgress(ctx, wt.Path)
	if len(gp.Commands) > 0 {
		pm.request(p) // the tree's counts follow what the commands changed
		log.Printf("project %s: git panel ran %d command(s) on %s, exit %d", p.id, len(gp.Commands), gp.Branch, res.ExitCode)
	}
	return res, nil
}

// gitCheckout is checkout, and also finds the branch in a worktree that is
// rebasing it: git detaches HEAD there until the rebase ends, and that is
// exactly when the panel is wanted to continue or abort it.
func gitCheckout(ctx context.Context, p *project, branch string) (gitx.Worktree, bool, *proto.Error) {
	if wt, ok, perr := checkout(ctx, p, branch); ok || perr != nil {
		return wt, ok, perr
	}
	wts, err := gitx.Worktrees(ctx, p.root)
	if err != nil {
		return gitx.Worktree{}, false, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	for _, wt := range wts {
		if wt.Detached && !wt.Prunable && gitx.RebasingBranch(ctx, wt.Path) == branch {
			return wt, true, nil
		}
	}
	return gitx.Worktree{}, false, nil
}

// quoteArgs writes args back as they could be typed.
func quoteArgs(args []string) string {
	q := make([]string, len(args))
	for i, a := range args {
		if a == "" || strings.ContainsAny(a, " \t\n'\"\\$`*?") {
			a = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		}
		q[i] = a
	}
	return strings.Join(q, " ")
}
