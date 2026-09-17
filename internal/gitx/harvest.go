package gitx

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrNothingToCommit is returned by CommitChanges when there are no changes.
var ErrNothingToCommit = errors.New("nothing to commit")

// ConflictError is a merge that stopped on conflicts and was undone.
type ConflictError struct {
	Branch, Into string
	Files        []string
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("merging %s into %s conflicts in %s; nothing was changed", e.Branch, e.Into, strings.Join(e.Files, ", "))
}

// StatusFiles lists the uncommitted changes in the checkout at dir,
// untracked files included.
func StatusFiles(ctx context.Context, dir string) ([]FileChange, error) {
	return statusFiles(ctx, dir)
}

// CommitChanges commits the changes in the checkout at dir with message and
// returns the new commit's hash. With files empty every change is committed,
// untracked files included; otherwise only those paths are (a rename needs
// both its old and new path), and anything else already staged stays staged.
func CommitChanges(ctx context.Context, dir, message string, files []string) (string, error) {
	if strings.TrimSpace(message) == "" {
		return "", errors.New("a commit needs a message")
	}
	changes, err := statusFiles(ctx, dir, files...)
	if err != nil {
		return "", err
	}
	if len(changes) == 0 {
		return "", ErrNothingToCommit
	}
	for _, f := range changes {
		if f.Code == "U" {
			return "", fmt.Errorf("%s has unresolved conflicts", f.Path)
		}
	}
	if len(files) == 0 {
		if _, err := run(ctx, dir, "add", "--all"); err != nil {
			return "", err
		}
		if _, err := run(ctx, dir, "commit", "--quiet", "--message", message); err != nil {
			return "", err
		}
	} else {
		// Only paths with unstaged changes are added: a path that exists
		// neither on disk nor in the index (a rename's staged old name)
		// makes git add fail.
		add := []string{"add", "--all", "--"}
		for _, f := range changes {
			if f.Unstaged {
				add = append(add, f.Path)
			}
		}
		if len(add) > 3 {
			if _, err := run(ctx, dir, add...); err != nil {
				return "", err
			}
		}
		paths := append([]string{"--"}, files...)
		// --only commits just these paths, leaving other staged changes be.
		if _, err := run(ctx, dir, append([]string{"commit", "--quiet", "--only", "--message", message}, paths...)...); err != nil {
			return "", err
		}
	}
	return revParse(ctx, dir, "HEAD")
}

// Push pushes branch to the branch of the same name on its upstream's
// remote, or on origin (or the only remote), and makes that its upstream.
// An upstream under another name is not pushed to: a task branch started
// from origin/main tracks origin/main, and pushing there would put the
// task's commits on main (git's push.default=simple refuses it too).
func Push(ctx context.Context, root, branch string) error {
	if !localBranchExists(ctx, root, branch) {
		return fmt.Errorf("no branch %s", branch)
	}
	ref := "refs/heads/" + branch
	remote := configValue(ctx, root, "branch."+branch+".remote")
	if remote == "" || remote == "." {
		var err error
		if remote, err = pushRemote(ctx, root); err != nil {
			return err
		}
	}
	args := []string{"push", "--quiet"}
	if configValue(ctx, root, "branch."+branch+".merge") != ref {
		args = append(args, "--set-upstream")
	}
	_, err := run(ctx, root, append(args, "--", remote, ref+":"+ref)...)
	return err
}

// pushRemote picks where a branch without an upstream goes: origin, or the
// repository's only remote.
func pushRemote(ctx context.Context, root string) (string, error) {
	out, err := run(ctx, root, "remote")
	if err != nil {
		return "", err
	}
	remotes := strings.Fields(string(out))
	switch {
	case len(remotes) == 0:
		return "", errors.New("the repository has no remote to push to")
	case len(remotes) == 1:
		return remotes[0], nil
	}
	for _, r := range remotes {
		if r == "origin" {
			return r, nil
		}
	}
	return "", fmt.Errorf("no origin remote, and several others (%s)", strings.Join(remotes, ", "))
}

// Merge merges branch into the branch checked out at dir, which must have
// no uncommitted changes to tracked files. With squash the branch's changes
// become one new commit; otherwise a merge commit is always made. message
// "" uses git's own. A merge that conflicts is undone and returned as a
// *ConflictError, so the checkout is left as it was.
func Merge(ctx context.Context, dir, branch string, squash bool, message string) (string, error) {
	into, err := revParse(ctx, dir, "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	if !localBranchExists(ctx, dir, branch) {
		return "", fmt.Errorf("no branch %s", branch)
	}
	files, err := statusFiles(ctx, dir)
	if err != nil {
		return "", err
	}
	for _, f := range files {
		if f.Code != "?" {
			return "", fmt.Errorf("%s has uncommitted changes (%s); commit or stash them first", into, f.Path)
		}
	}
	if n := revCount(ctx, dir, "HEAD.."+branch); n == 0 {
		return "", fmt.Errorf("%s has nothing that %s lacks", branch, into)
	}

	var args []string
	if squash {
		args = []string{"merge", "--quiet", "--squash", "--", branch}
	} else {
		args = []string{"merge", "--quiet", "--no-ff", "--no-edit"}
		if message != "" {
			args = append(args, "--message", message)
		}
		args = append(args, "--", branch)
	}
	if _, err := run(ctx, dir, args...); err != nil {
		conflicts := conflictFiles(ctx, dir)
		undoMerge(dir, squash)
		if len(conflicts) > 0 {
			return "", &ConflictError{Branch: branch, Into: into, Files: conflicts}
		}
		return "", err
	}
	if squash {
		commit := []string{"commit", "--quiet", "--no-edit"}
		if message != "" {
			commit = []string{"commit", "--quiet", "--message", message}
		}
		if _, err := run(ctx, dir, commit...); err != nil {
			undoMerge(dir, true) // e.g. a commit hook refused it
			return "", err
		}
	}
	return revParse(ctx, dir, "HEAD")
}

// undoMerge puts a checkout back as it was before a merge that failed. It
// runs even when the caller's context has expired.
func undoMerge(dir string, squash bool) {
	ctx := context.Background()
	if !squash {
		if _, err := run(ctx, dir, "merge", "--abort"); err == nil {
			return
		}
	}
	// A squash leaves no MERGE_HEAD to abort; the checkout was clean before,
	// so resetting the merge's changes restores it.
	_, _ = run(ctx, dir, "reset", "--quiet", "--merge")
}

func conflictFiles(ctx context.Context, dir string) []string {
	out, err := run(ctx, dir, "diff", "--name-only", "--diff-filter=U", "-z")
	if err != nil {
		return nil
	}
	return splitNUL(out)
}

// Unmerged counts the commits on branch that are in none of base, base's
// upstream and branch's upstream, i.e. what deleting the branch would lose.
// A branch whose changes base (or its upstream) already has, say because it
// was squash-merged, counts 0.
func Unmerged(ctx context.Context, root, branch, base string) int {
	if !resolves(ctx, root, "refs/heads/"+branch) {
		return 0
	}
	var bases []string
	for _, rev := range []string{base, base + "@{upstream}"} {
		if base != "" && resolves(ctx, root, rev) {
			bases = append(bases, rev)
		}
	}
	args := append([]string{"rev-list", "--count", "refs/heads/" + branch, "--not"}, bases...)
	if up := branch + "@{upstream}"; resolves(ctx, root, up) {
		args = append(args, up)
	}
	out, err := run(ctx, root, append(args, "--")...)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(out)))
	for _, b := range bases {
		if n > 0 && mergesCleanly(ctx, root, branch, b) {
			return 0
		}
	}
	return n
}

// mergesCleanly reports whether merging branch into base would change
// nothing: base already has all of the branch's changes.
func mergesCleanly(ctx context.Context, root, branch, base string) bool {
	// merge-tree --write-tree needs git 2.38; older gits just say no.
	out, err := run(ctx, root, "merge-tree", "--write-tree", "--no-messages", base, "refs/heads/"+branch)
	if err != nil {
		return false
	}
	tree := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	baseTree, err := revParse(ctx, root, base+"^{tree}")
	return err == nil && tree == baseTree
}

// BranchHead returns the commit a local branch points at.
func BranchHead(ctx context.Context, root, branch string) (string, error) {
	return revParse(ctx, root, "--verify", "--quiet", "refs/heads/"+branch)
}

// DeleteBranch deletes a local branch, merged or not.
func DeleteBranch(ctx context.Context, root, branch string) error {
	_, err := run(ctx, root, "branch", "--delete", "--force", "--", branch)
	return err
}

// ForceRemoveWorktree removes the linked worktree at path, discarding its
// uncommitted changes.
func ForceRemoveWorktree(ctx context.Context, root, path string) error {
	_, err := run(ctx, root, "worktree", "remove", "--force", path)
	return err
}

func revParse(ctx context.Context, dir string, args ...string) (string, error) {
	out, err := run(ctx, dir, append([]string{"rev-parse"}, args...)...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func revCount(ctx context.Context, dir, rng string) int {
	out, err := run(ctx, dir, "rev-list", "--count", rng, "--")
	if err != nil {
		return -1
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(out)))
	return n
}

func configValue(ctx context.Context, dir, key string) string {
	out, err := run(ctx, dir, "config", "--get", key)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
