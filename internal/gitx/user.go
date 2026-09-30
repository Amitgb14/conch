package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Git commands a person types into the TUI's git panel. They run as typed,
// in one checkout, with nothing to answer: no terminal, no pager, and an
// editor that accepts what git proposes, so a merge or a rebase --continue
// takes git's own message rather than waiting for one that never comes.

// userEnv is appended to the inherited environment of a person's command.
// It leaves out gitEnv's literal pathspecs and optional locks: what they
// type should behave as it does in their own terminal.
var userEnv = []string{
	"GIT_TERMINAL_PROMPT=0",
	"GIT_PAGER=cat",
	"PAGER=cat",
	"GIT_EDITOR=true",
	"GIT_SEQUENCE_EDITOR=true",
	"GIT_MERGE_AUTOEDIT=no",
}

// CheckUserCommand refuses what the git panel must not run. A command
// starts with a subcommand: git's own options before it (-C, -c,
// --git-dir, --exec-path) would point it at another repository or at
// another program.
func CheckUserCommand(args []string) error {
	switch {
	case len(args) == 0 || strings.TrimSpace(args[0]) == "":
		return errors.New("no git command given")
	case strings.HasPrefix(args[0], "-"):
		return fmt.Errorf("git options before the command (%s) aren't taken here; start with the command, e.g. status", args[0])
	}
	return nil
}

// RunUser runs git args in dir as a person would and returns what it
// printed, stdout and stderr together, up to limit bytes (the rest is
// dropped and truncated set). A command that fails is not an error: its
// exit code says so. err is for a git that could not run at all, or ctx
// ending first.
func RunUser(ctx context.Context, dir string, args []string, limit int) (out string, code int, truncated bool, err error) {
	if err := CheckUserCommand(args); err != nil {
		return "", -1, false, err
	}
	full := append([]string{"-c", "core.quotepath=off", "-c", "color.ui=never", "-C", dir}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Env = append(os.Environ(), userEnv...)
	buf := &capped{limit: limit}
	cmd.Stdout, cmd.Stderr = buf, buf
	runErr := cmd.Run()
	if ctx.Err() != nil {
		return buf.String(), -1, buf.over, ctx.Err()
	}
	var exit *exec.ExitError
	switch {
	case runErr == nil:
		return buf.String(), 0, buf.over, nil
	case errors.As(runErr, &exit):
		return buf.String(), exit.ExitCode(), buf.over, nil
	}
	return buf.String(), -1, buf.over, fmt.Errorf("git: %w", runErr)
}

// capped keeps the first limit bytes written to it and counts the rest as
// lost, so a long log can't fill the server's memory or the protocol.
type capped struct {
	buf   bytes.Buffer
	limit int
	over  bool
}

func (c *capped) Write(p []byte) (int, error) {
	if room := c.limit - c.buf.Len(); room < len(p) {
		c.over = true
		if room > 0 {
			c.buf.Write(p[:room])
		}
		return len(p), nil
	}
	return c.buf.Write(p)
}

func (c *capped) String() string { return c.buf.String() }

// gitDir is the checkout's own git directory: .git/worktrees/NAME for a
// linked worktree.
func gitDir(ctx context.Context, dir string) (string, bool) {
	out, err := run(ctx, dir, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}

// RebasingBranch is the branch a rebase in the checkout at dir is
// rewriting, or "". git detaches HEAD while it works, so the worktree list
// shows no branch there until the rebase ends.
func RebasingBranch(ctx context.Context, dir string) string {
	gd, ok := gitDir(ctx, dir)
	if !ok {
		return ""
	}
	for _, d := range []string{"rebase-merge", "rebase-apply"} {
		if b, err := os.ReadFile(filepath.Join(gd, d, "head-name")); err == nil {
			return strings.TrimPrefix(strings.TrimSpace(string(b)), "refs/heads/")
		}
	}
	return ""
}

// InProgress names the operation left unfinished in the checkout at dir —
// "rebase", "merge", "cherry-pick", "revert" or "am" — or "" when there is
// none. Each worktree has its own, so a conflict in one doesn't show in
// the others.
func InProgress(ctx context.Context, dir string) string {
	gd, ok := gitDir(ctx, dir)
	if !ok {
		return ""
	}
	has := func(name string) bool {
		_, err := os.Stat(filepath.Join(gd, name))
		return err == nil
	}
	switch {
	case has("rebase-merge"):
		return "rebase"
	case has("rebase-apply"):
		if has("rebase-apply/applying") {
			return "am"
		}
		return "rebase"
	case has("MERGE_HEAD"):
		return "merge"
	case has("CHERRY_PICK_HEAD"):
		return "cherry-pick"
	case has("REVERT_HEAD"):
		return "revert"
	}
	return ""
}
