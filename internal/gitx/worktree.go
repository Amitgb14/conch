package gitx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Worktree is one entry of `git worktree list`.
type Worktree struct {
	Path     string // absolute, symlinks resolved
	Branch   string // short name, "" when detached
	Head     string // commit hash
	Detached bool
	Main     bool // the main worktree (first entry)
	Locked   bool
	Prunable bool
}

// Worktrees lists the worktrees of the repository at root, main first.
func Worktrees(ctx context.Context, root string) ([]Worktree, error) {
	out, err := run(ctx, root, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return nil, err
	}
	var wts []Worktree
	var cur *Worktree
	// Each attribute is NUL-terminated; an empty field ends the entry.
	for _, f := range strings.Split(string(out), "\x00") {
		key, val, _ := strings.Cut(f, " ")
		switch {
		case f == "":
			cur = nil
		case key == "worktree":
			wts = append(wts, Worktree{Path: resolve(val), Main: len(wts) == 0})
			cur = &wts[len(wts)-1]
		case cur == nil:
		case key == "HEAD":
			cur.Head = val
		case key == "branch":
			cur.Branch = strings.TrimPrefix(val, "refs/heads/")
		case key == "detached":
			cur.Detached = true
		case key == "locked":
			cur.Locked = true
		case key == "prunable":
			cur.Prunable = true
		}
	}
	return wts, nil
}

// AddWorktree checks out branch at path, creating the branch from base when
// it does not exist locally.
func AddWorktree(ctx context.Context, root, path, branch, base string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("gitx: create worktree parent: %w", err)
	}
	if err := seedUnborn(ctx, root, base); err != nil {
		return err
	}
	var err error
	if localBranchExists(ctx, root, branch) {
		_, err = run(ctx, root, "worktree", "add", path, branch)
	} else {
		args := []string{"worktree", "add", "-b", branch, path}
		if base != "" {
			args = append(args, base)
		}
		_, err = run(ctx, root, args...)
	}
	return err
}

// seedUnborn gives base an empty first commit when the repository has no
// commits at all — a `git init` nobody has committed to yet — since a
// branch can only start from a commit and git otherwise refuses with
// "invalid reference". It is made with commit-tree, not commit, so what is
// staged in the main checkout stays staged and out of it. A repository
// with any branch is left alone: there base not resolving is a real error.
func seedUnborn(ctx context.Context, root, base string) error {
	if base == "" || resolves(ctx, root, base) {
		return nil
	}
	if out, err := run(ctx, root, "for-each-ref", "--count=1", "--format=%(refname)", "refs/heads"); err != nil || strings.TrimSpace(string(out)) != "" {
		return nil
	}
	if ValidBranchName(ctx, root, base) != nil {
		return nil
	}
	tree, err := run(ctx, root, "mktree") // no input: the empty tree
	if err != nil {
		return err
	}
	commit, err := run(ctx, root, "commit-tree", "-m", "Initial commit", strings.TrimSpace(string(tree)))
	if err != nil {
		var ge *gitError
		if errors.As(err, &ge) && strings.Contains(ge.stderr, "identity") {
			return fmt.Errorf("%s has no commits yet, and git has no author identity to make the first one: set user.name and user.email (git config --global), or commit once yourself", root)
		}
		return err
	}
	// The empty old value makes this fail rather than move a branch that
	// appeared meanwhile.
	_, err = run(ctx, root, "update-ref", "-m", "conch: first commit", "refs/heads/"+base, strings.TrimSpace(string(commit)), "")
	return err
}

// RemoveWorktree removes the linked worktree at path. It never forces, so a
// dirty worktree is refused with git's error.
func RemoveWorktree(ctx context.Context, root, path string) error {
	_, err := run(ctx, root, "worktree", "remove", path)
	return err
}

// ValidBranchName reports whether name is acceptable as a new branch name.
func ValidBranchName(ctx context.Context, root, name string) error {
	if _, err := run(ctx, root, "check-ref-format", "--branch", name); err != nil {
		return fmt.Errorf("invalid branch name %q", name)
	}
	return nil
}

// InitRepo makes dir a git repository on branch main with an empty first
// commit, so branches and worktrees can be made from it straight away. The
// commit is skipped (not an error) when git has no author identity.
func InitRepo(ctx context.Context, dir string) error {
	if _, err := run(ctx, dir, "init", "-b", "main"); err != nil {
		return err
	}
	_, _ = run(ctx, dir, "commit", "--allow-empty", "-m", "Initial commit")
	return nil
}

// UntrackedFiles lists files in the checkout at dir that git does not track
// and that match any of patterns, git glob pathspecs relative to dir such as
// ".env" or "config/*.local.json". With ignored it lists the files git
// ignores, otherwise the untracked files it doesn't.
func UntrackedFiles(ctx context.Context, dir string, patterns []string, ignored bool) ([]string, error) {
	var specs []string
	for _, p := range patterns {
		if p = strings.TrimSpace(p); p != "" {
			specs = append(specs, ":(glob)"+p)
		}
	}
	if len(specs) == 0 { // no pathspec would list every file
		return nil, nil
	}
	args := []string{"ls-files", "--others", "--exclude-standard", "-z"}
	if ignored {
		args = append(args, "--ignored")
	}
	args = append(args, "--")
	args = append(args, specs...)
	// gitEnv makes pathspecs literal; these are globs on purpose.
	cmd := command(ctx, dir, args...)
	cmd.Env = append(cmd.Env, "GIT_LITERAL_PATHSPECS=0")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("git ls-files: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	return splitNUL(out), nil
}
