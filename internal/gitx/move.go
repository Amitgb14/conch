package gitx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Moving a worktree to another machine: the commits of its branch travel as
// a git bundle holding only what the other repository lacks, and the
// uncommitted work as patches and files.

// RemoteURL is the URL of the repository's origin, or of its only remote;
// "" when it has neither.
func RemoteURL(ctx context.Context, root string) string {
	if out, err := run(ctx, root, "remote", "get-url", "origin"); err == nil {
		return strings.TrimSpace(string(out))
	}
	out, err := run(ctx, root, "remote")
	if err != nil {
		return ""
	}
	if names := strings.Fields(string(out)); len(names) == 1 {
		if out, err := run(ctx, root, "remote", "get-url", names[0]); err == nil {
			return strings.TrimSpace(string(out))
		}
	}
	return ""
}

// History lists up to n commits of rev's first-parent line, newest first:
// the candidates another repository may already have.
func History(ctx context.Context, dir, rev string, n int) ([]string, error) {
	out, err := run(ctx, dir, "rev-list", "--first-parent", fmt.Sprintf("-n%d", n), rev, "--")
	if err != nil {
		return nil, err
	}
	return strings.Fields(string(out)), nil
}

// HaveCommits returns the commits of list that exist in the repository at
// dir, in list's order. Anything that isn't a full hash is left out.
func HaveCommits(ctx context.Context, dir string, list []string) ([]string, error) {
	var in strings.Builder
	var asked []string
	for _, c := range list {
		if isHash(c) {
			in.WriteString(c + "^{commit}\n")
			asked = append(asked, c)
		}
	}
	if len(asked) == 0 {
		return nil, nil
	}
	cmd := command(ctx, dir, "cat-file", "--batch-check")
	cmd.Stdin = strings.NewReader(in.String())
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("git cat-file: %w", err)
	}
	var have []string
	for i, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		// "<hash> commit <size>" when found, "<name> missing" when not.
		if f := strings.Fields(line); i < len(asked) && len(f) >= 2 && f[1] == "commit" {
			have = append(have, asked[i])
		}
	}
	return have, nil
}

func isHash(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// ErrEmptyBundle means the other repository already has every commit.
var ErrEmptyBundle = errors.New("gitx: nothing to bundle")

// CreateBundle writes the commits of branch that have doesn't reach to a
// bundle at file. With have "" the whole history goes.
func CreateBundle(ctx context.Context, dir, file, branch, have string) error {
	ref := "refs/heads/" + branch
	if have != "" {
		head, err := revParse(ctx, dir, "--verify", ref+"^{commit}")
		if err != nil {
			return err
		}
		if head == have {
			return ErrEmptyBundle
		}
	}
	args := []string{"bundle", "create", "-q", file, ref}
	if have != "" {
		args = append(args, "^"+have)
	}
	_, err := run(ctx, dir, args...)
	return err
}

// FetchBundle takes branch's commits from a bundle into the repository at
// root under ref, which it may overwrite, and returns the commit.
func FetchBundle(ctx context.Context, root, file, branch, ref string) (string, error) {
	if _, err := run(ctx, root, "bundle", "verify", "-q", file); err != nil {
		return "", err
	}
	if _, err := run(ctx, root, "fetch", "-q", "--no-tags", file, "+refs/heads/"+branch+":"+ref); err != nil {
		return "", err
	}
	return revParse(ctx, root, "--verify", ref+"^{commit}")
}

// IsAncestor reports whether a is an ancestor of (or the same as) b.
func IsAncestor(ctx context.Context, dir, a, b string) bool {
	_, code, err := runCodes(ctx, dir, []int{1}, "merge-base", "--is-ancestor", a, b)
	return err == nil && code == 0
}

// SetBranch points branch at commit, creating it if needed.
func SetBranch(ctx context.Context, root, branch, commit string) error {
	_, err := run(ctx, root, "update-ref", "refs/heads/"+branch, commit)
	return err
}

// DeleteRef removes a ref such as the one FetchBundle wrote.
func DeleteRef(ctx context.Context, root, ref string) {
	_, _ = run(ctx, root, "update-ref", "-d", ref)
}

// WorkPatches are the uncommitted changes in the checkout at dir as binary
// patches: what is staged, and what is changed on top of that.
func WorkPatches(ctx context.Context, dir string) (staged, unstaged []byte, err error) {
	if staged, err = run(ctx, dir, "diff", "--binary", "--no-color", "--no-ext-diff", "--cached"); err != nil {
		return nil, nil, err
	}
	if unstaged, err = run(ctx, dir, "diff", "--binary", "--no-color", "--no-ext-diff"); err != nil {
		return nil, nil, err
	}
	return staged, unstaged, nil
}

// ApplyPatch applies a patch from WorkPatches in the checkout at dir, to the
// index as well with index. An empty patch does nothing.
func ApplyPatch(ctx context.Context, dir string, patch []byte, index bool) error {
	if len(patch) == 0 {
		return nil
	}
	f, err := os.CreateTemp("", "conch-patch-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(patch); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	args := []string{"apply", "--binary", "--whitespace=nowarn"}
	if index {
		args = append(args, "--index")
	}
	// gitEnv makes pathspecs literal; the patch file is a plain path anyway.
	_, err = run(ctx, dir, append(args, f.Name())...)
	return err
}

// OtherFiles lists the files in the checkout at dir that git doesn't track
// and doesn't ignore.
func OtherFiles(ctx context.Context, dir string) ([]string, error) {
	out, err := run(ctx, dir, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, err
	}
	return splitNUL(out), nil
}

// Clone clones url into dir, which must not exist yet or be empty. It never
// waits for a password: a server has no one to type it.
func Clone(ctx context.Context, url, dir string) error {
	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	cmd := command(ctx, parent, "clone", "-q", "--", url, dir)
	if os.Getenv("GIT_SSH_COMMAND") == "" && os.Getenv("GIT_SSH") == "" {
		cmd.Env = append(cmd.Env, "GIT_SSH_COMMAND=ssh -o BatchMode=yes")
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("git clone %s: timed out", url)
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("git clone %s: %s", url, msg)
	}
	return nil
}
