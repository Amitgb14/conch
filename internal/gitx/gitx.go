// Package gitx reads and manipulates git repositories for the sidebar:
// repository discovery, worktrees, branches, status and diffs. Everything
// shells out to the git binary.
package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ErrNotRepo is returned by Discover when a directory is not inside a git
// work tree.
var ErrNotRepo = errors.New("gitx: not a git repository")

// Repo identifies a repository by its main worktree.
type Repo struct {
	Root      string // main worktree top level (for a linked worktree dir, still the MAIN repo root)
	CommonDir string // absolute path of the shared .git dir
}

// gitEnv is appended to the inherited environment of every git process.
var gitEnv = []string{
	// Agents edit these repos concurrently; a status refresh must never take
	// index.lock out from under them.
	"GIT_OPTIONAL_LOCKS=0",
	"LC_ALL=C",
	"GIT_TERMINAL_PROMPT=0",
	// Paths we pass are file names, never globs.
	"GIT_LITERAL_PATHSPECS=1",
}

// gitError is a failed git invocation.
type gitError struct {
	args   []string
	code   int
	stderr string
}

func (e *gitError) Error() string {
	msg := e.stderr
	if msg == "" {
		msg = fmt.Sprintf("exit status %d", e.code)
	}
	return fmt.Sprintf("git %s: %s", strings.Join(e.args, " "), msg)
}

func command(ctx context.Context, dir string, args ...string) *exec.Cmd {
	full := append([]string{"-c", "core.quotepath=off", "-C", dir}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Env = append(os.Environ(), gitEnv...)
	return cmd
}

// run runs git in dir and returns its stdout.
func run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	out, _, err := runCodes(ctx, dir, nil, args...)
	return out, err
}

// runCodes is run, but exit codes listed in ok also count as success. The
// exit code is returned.
func runCodes(ctx context.Context, dir string, ok []int, args ...string) ([]byte, int, error) {
	var stdout bytes.Buffer
	return runTo(ctx, dir, &stdout, ok, args...)
}

func runTo(ctx context.Context, dir string, stdout interface {
	io.Writer
	Bytes() []byte
}, ok []int, args ...string) ([]byte, int, error) {
	var stderr bytes.Buffer
	cmd := command(ctx, dir, args...)
	cmd.Stdout = stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return stdout.Bytes(), 0, nil
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		return nil, -1, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	code := exit.ExitCode()
	for _, c := range ok {
		if c == code {
			return stdout.Bytes(), code, nil
		}
	}
	if ctx.Err() != nil {
		return nil, code, ctx.Err()
	}
	return nil, code, &gitError{args: args, code: code, stderr: strings.TrimSpace(stderr.String())}
}

// Discover finds the repository containing dir.
func Discover(ctx context.Context, dir string) (Repo, error) {
	out, err := run(ctx, dir, "rev-parse", "--path-format=absolute", "--git-common-dir", "--show-toplevel")
	if err != nil {
		var ge *gitError
		if errors.As(err, &ge) && (strings.Contains(ge.stderr, "not a git repository") ||
			strings.Contains(ge.stderr, "must be run in a work tree")) {
			return Repo{}, ErrNotRepo
		}
		return Repo{}, err
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) < 2 || lines[1] == "" {
		return Repo{}, ErrNotRepo
	}
	common, top := resolve(lines[0]), lines[1]
	root := top
	if filepath.Base(common) == ".git" {
		root = filepath.Dir(common)
	}
	return Repo{Root: resolve(root), CommonDir: common}, nil
}

// resolve cleans p and resolves symlinks when it exists, so /tmp and
// /private/tmp compare equal.
func resolve(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

// DefaultBase guesses the branch new work should start from.
func DefaultBase(ctx context.Context, root string) string {
	if out, err := run(ctx, root, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); err == nil {
		remote := strings.TrimSpace(string(out))
		if local, ok := strings.CutPrefix(remote, "origin/"); ok && localBranchExists(ctx, root, local) {
			return local
		}
		if remote != "" {
			return remote
		}
	}
	for _, b := range []string{"main", "master", "trunk", "develop"} {
		if localBranchExists(ctx, root, b) {
			return b
		}
	}
	if out, err := run(ctx, root, "symbolic-ref", "--quiet", "--short", "HEAD"); err == nil {
		if b := strings.TrimSpace(string(out)); b != "" {
			return b
		}
	}
	return "HEAD"
}

func localBranchExists(ctx context.Context, root, name string) bool {
	_, err := run(ctx, root, "rev-parse", "--verify", "--quiet", "refs/heads/"+name)
	return err == nil
}

// resolves reports whether rev names a commit.
func resolves(ctx context.Context, dir, rev string) bool {
	if rev == "" {
		return false
	}
	_, err := run(ctx, dir, "rev-parse", "--verify", "--quiet", "--end-of-options", rev+"^{commit}")
	return err == nil
}

// splitNUL splits NUL-terminated output into fields.
func splitNUL(b []byte) []string {
	s := strings.TrimSuffix(string(b), "\x00")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\x00")
}
