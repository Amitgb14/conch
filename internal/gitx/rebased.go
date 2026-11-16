package gitx

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// A branch just rebased — by an agent asked to resolve a conflict, or by
// anybody — is a rewrite nobody has looked at yet: its commits are new
// ones, and the remote still has the old. What follows reads that rewrite
// back from the branch's reflog, so the person can see what the
// resolution changed, push it over the old one, or put the branch back.

// Rebase is a branch's newest rebase: its tip before and after, and the
// commit it was put on.
type Rebase struct {
	Branch        string
	Before, After string
	Onto          string
	At            time.Time
}

// finished is the reflog's word for a rebase that ended: "rebase
// (finish): refs/heads/x onto <hash>", "rebase -i (finish): …", and the
// "rebase finished: …" of older gits.
var finished = regexp.MustCompile(`^rebase\b.*\bfinish(?:ed)?\)?: refs/heads/(.+) onto ([0-9a-f]+)$`)

// LastRebase is branch's rebase when that is the newest thing done to it,
// or nil: a commit, reset or push since and it is no longer news.
func LastRebase(ctx context.Context, dir, branch string) (*Rebase, error) {
	ref := "refs/heads/" + branch
	if CommitOf(ctx, dir, ref) == "" {
		return nil, nil
	}
	out, err := run(ctx, dir, "reflog", "show", "--date=unix", "--format=%H%x00%gd%x00%gs", "-n", "2", ref, "--")
	if err != nil {
		return nil, nil // a branch without a reflog has no rebase to show
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		return nil, nil
	}
	newest := strings.SplitN(lines[0], "\x00", 3)
	prior := strings.SplitN(lines[1], "\x00", 3)
	if len(newest) < 3 || len(prior) < 1 {
		return nil, nil
	}
	m := finished.FindStringSubmatch(newest[2])
	if m == nil || m[1] != branch {
		return nil, nil
	}
	r := &Rebase{Branch: branch, Before: prior[0], After: newest[0], Onto: m[2]}
	// refs/heads/x@{1731600000}
	if i := strings.LastIndex(newest[1], "@{"); i >= 0 {
		if s, err := strconv.ParseInt(strings.TrimSuffix(newest[1][i+2:], "}"), 10, 64); err == nil {
			r.At = time.Unix(s, 0)
		}
	}
	if r.Before == r.After {
		return nil, nil // already on top: nothing was rewritten
	}
	return r, nil
}

// How a commit came through a rebase.
const (
	RewriteSame    = "same"    // the same change on a new parent
	RewriteChanged = "changed" // its change is different: a conflict was resolved in it
	RewriteDropped = "dropped" // gone: the base had it already, or it was left out
	RewriteAdded   = "added"   // new in the rewrite
)

// Rewrite is one commit before and after the rebase, by short hash.
type Rewrite struct {
	Old, New string
	Subject  string
	Change   string
}

// rangeLine is a header of git range-diff: "1:  abc1234 ! 1:  def5678 subject".
var rangeLine = regexp.MustCompile(`^\s*(?:\d+|-):\s+([0-9a-f]+|-+)\s+([<>=!])\s+(?:\d+|-):\s+([0-9a-f]+|-+)\s+(.*)$`)

// Rewritten pairs the branch's commits before the rebase with those
// after, oldest first, saying of each whether its change is what it was.
// A commit whose conflict was resolved is the one that changed.
func Rewritten(ctx context.Context, dir string, r *Rebase) ([]Rewrite, error) {
	// The old commits are those Before has and the base it was put on
	// does not (the symmetric range's left side, as range-diff reads it).
	mb, err := run(ctx, dir, "merge-base", r.Onto, r.Before)
	if err != nil {
		return nil, fmt.Errorf("%s and %s share no history", short(r.Onto), short(r.Before))
	}
	out, err := run(ctx, dir, "range-diff", "--no-color", "--creation-factor=100", "-s",
		strings.TrimSpace(string(mb))+".."+r.Before, r.Onto+".."+r.After)
	if err != nil {
		return nil, err
	}
	var cs []Rewrite
	for _, l := range strings.Split(string(out), "\n") {
		m := rangeLine.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		c := Rewrite{Subject: m[4]}
		if !strings.HasPrefix(m[1], "-") {
			c.Old = m[1]
		}
		if !strings.HasPrefix(m[3], "-") {
			c.New = m[3]
		}
		c.Change = map[string]string{"=": RewriteSame, "!": RewriteChanged, "<": RewriteDropped, ">": RewriteAdded}[m[2]]
		cs = append(cs, c)
	}
	return cs, nil
}

// cutNote ends a diff cut short.
const cutNote = "\n… (cut short)\n"

// RewriteDiff is git range-diff's account of the rebase: for each commit
// that changed, the diff of its diff — what resolving the conflict did.
// Longer than max (when max > 0) and it is cut there.
func RewriteDiff(ctx context.Context, dir string, r *Rebase, max int) (string, error) {
	mb, err := run(ctx, dir, "merge-base", r.Onto, r.Before)
	if err != nil {
		return "", fmt.Errorf("%s and %s share no history", short(r.Onto), short(r.Before))
	}
	out, err := run(ctx, dir, "range-diff", "--no-color", "--creation-factor=100",
		strings.TrimSpace(string(mb))+".."+r.Before, r.Onto+".."+r.After)
	if err != nil {
		return "", err
	}
	s := string(out)
	if max > 0 && len(s) > max {
		s = s[:max] + cutNote
	}
	return s, nil
}

// UndoRebase puts branch back at before, in dir where it is checked out,
// if it is still at after and has nothing uncommitted: the rewrite stays
// in the reflog, so this too can be undone.
func UndoRebase(ctx context.Context, dir, branch, before, after string) error {
	if err := readyToRebase(ctx, dir, branch); err != nil {
		return err
	}
	if head, _ := revParse(ctx, dir, "HEAD"); head != after {
		return fmt.Errorf("%s has moved on since the rebase (it is at %s now), so it is not undone", branch, short(head))
	}
	if CommitOf(ctx, dir, before) == "" {
		return fmt.Errorf("%s, where %s was before the rebase, is not in this repository", short(before), branch)
	}
	if _, err := run(ctx, dir, "reset", "--keep", "--quiet", before); err != nil {
		return err
	}
	return nil
}

func short(h string) string {
	if len(h) > 8 {
		return h[:8]
	}
	return h
}
