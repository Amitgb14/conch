package gitx

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Bringing a branch up to date with its base, for a pull request that
// conflicts with it: the base as the remote has it, a rebase onto it that
// undoes itself when it stops, and what to check before the rewritten
// branch replaces the pushed one — that the base is all there, and that
// nothing was deleted which the branch did not delete before.

// FreshBase fetches base from the remote the branch would push to and
// names it as the remote has it ("origin/main"). Without a remote, or one
// that has no such branch, it is base as this checkout has it.
func FreshBase(ctx context.Context, dir, branch, base string) string {
	if base == "" {
		return ""
	}
	remote, err := branchRemote(ctx, dir, branch)
	if err != nil {
		return base
	}
	name := strings.TrimPrefix(base, remote+"/")
	if _, err := run(ctx, dir, "fetch", "--quiet", "--", remote, "+refs/heads/"+name+":refs/remotes/"+remote+"/"+name); err != nil {
		if resolves(ctx, dir, base) {
			return base
		}
		return ""
	}
	return remote + "/" + name
}

// OntoConflictError is a rebase onto the base that stopped on conflicts
// and was undone: the branch is as it was.
type OntoConflictError struct {
	Branch, Onto string
	Files        []string
}

func (e *OntoConflictError) Error() string {
	return fmt.Sprintf("rebasing %s onto %s conflicts in %s; it was undone", e.Branch, e.Onto, strings.Join(e.Files, ", "))
}

// RebaseOnto puts branch's own commits on top of onto, in dir where it is
// checked out, and says whether that moved it. Uncommitted changes to
// tracked files stop it before it starts; one that conflicts is undone and
// comes back as an *OntoConflictError naming the files.
func RebaseOnto(ctx context.Context, dir, branch, onto string) (moved bool, err error) {
	if err := readyToRebase(ctx, dir, branch); err != nil {
		return false, err
	}
	if IsAncestor(ctx, dir, onto, "HEAD") {
		return false, nil // on top of it already
	}
	if conflicts, err := rebaseUndoing(ctx, dir, onto); len(conflicts) > 0 {
		return false, &OntoConflictError{Branch: branch, Onto: onto, Files: conflicts}
	} else if err != nil {
		return false, err
	}
	return true, nil
}

// CommitOf is the commit rev names in dir now, or "" when it names none: a
// ref read once, so what it meant then is kept when the ref moves on.
func CommitOf(ctx context.Context, dir, rev string) string {
	c, err := revParse(ctx, dir, "--verify", "--quiet", rev+"^{commit}")
	if err != nil {
		return ""
	}
	return c
}

// Deleted lists the files the checkout's HEAD has deleted since it left
// base, sorted: what a rewrite of the branch must not add to.
func Deleted(ctx context.Context, dir, base string) ([]string, error) {
	out, err := run(ctx, dir, "diff", "--name-only", "-z", "--no-renames", "--diff-filter=D", base+"...HEAD", "--")
	if err != nil {
		return nil, err
	}
	files := splitNUL(out)
	sort.Strings(files)
	return files, nil
}

// BaseChanges is what base did to files since branch left it, newest
// first and at most n commits: the other side of a conflict, for whoever
// resolves it.
func BaseChanges(ctx context.Context, dir, branch, base string, files []string, n int) string {
	mb, err := run(ctx, dir, "merge-base", base, branch)
	if err != nil {
		return ""
	}
	args := []string{"log", "--oneline", "--no-decorate", fmt.Sprintf("-%d", n), strings.TrimSpace(string(mb)) + ".." + base, "--"}
	out, err := run(ctx, dir, append(args, files...)...)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// PushedHead is the commit the remote last had for branch, as this
// checkout knows it, or "" when it was never pushed.
func PushedHead(ctx context.Context, dir, branch string) string {
	remote, err := branchRemote(ctx, dir, branch)
	if err != nil {
		return ""
	}
	head, err := revParse(ctx, dir, "--verify", "--quiet", "refs/remotes/"+remote+"/"+branch)
	if err != nil {
		return ""
	}
	return head
}

// PushOver replaces branch on its remote with the checkout's, but only if
// the remote still has expect there: anything pushed since is kept, and
// comes back as a *PushRejectedError rather than being overwritten.
func PushOver(ctx context.Context, dir, branch, expect string) error {
	remote, err := branchRemote(ctx, dir, branch)
	if err != nil {
		return err
	}
	ref := "refs/heads/" + branch
	if _, err := run(ctx, dir, "push", "--quiet", "--force-with-lease="+ref+":"+expect, "--", remote, ref+":"+ref); err != nil {
		if rejected(err) {
			return &PushRejectedError{Branch: branch, Remote: remote}
		}
		return err
	}
	return nil
}

// DiffSize is how many lines the checkout's HEAD adds and removes since
// it left base, together: the size of the pull request it would make.
func DiffSize(ctx context.Context, dir, base string) (int, error) {
	return DiffSizeAt(ctx, dir, base, "HEAD")
}

// DiffSizeAt is DiffSize for rev rather than the checkout's HEAD.
func DiffSizeAt(ctx context.Context, dir, base, rev string) (int, error) {
	out, err := run(ctx, dir, "diff", "--numstat", "--no-renames", base+"..."+rev, "--")
	if err != nil {
		return 0, err
	}
	n := 0
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		for _, c := range f[:2] {
			if v, err := strconv.Atoi(c); err == nil { // "-" for a binary file
				n += v
			}
		}
	}
	return n, nil
}

// Changed lists the files the checkout's HEAD has changed since it left
// base, sorted.
func Changed(ctx context.Context, dir, base string) ([]string, error) {
	return ChangedAt(ctx, dir, base, "HEAD")
}

// ChangedAt is Changed for rev rather than the checkout's HEAD.
func ChangedAt(ctx context.Context, dir, base, rev string) ([]string, error) {
	out, err := run(ctx, dir, "diff", "--name-only", "-z", "--no-renames", base+"..."+rev, "--")
	if err != nil {
		return nil, err
	}
	files := splitNUL(out)
	sort.Strings(files)
	return files, nil
}
