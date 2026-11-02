package gitx

import (
	"context"
	"strconv"
	"strings"
	"time"
)

// LogCommit is one commit of a repository's history, with what a graph
// needs to draw it: its parents, and the refs that point at it.
type LogCommit struct {
	Hash    string
	Parents []string
	Author  string
	Time    time.Time
	Subject string
	// Refs as git decorates them: "HEAD -> main", "origin/main",
	// "tag: v1.0".
	Refs []string
}

// Log is the history reachable from every local branch and tag and from
// HEAD, newest first in topological order, so a commit always comes
// before its parents. It reads at most limit commits; more says there
// were others. A repository with no commits yet has no history, not an
// error.
func Log(ctx context.Context, dir string, limit int) (commits []LogCommit, more bool, err error) {
	if limit <= 0 {
		return nil, false, nil
	}
	if _, err := run(ctx, dir, "rev-parse", "--git-dir"); err != nil {
		return nil, false, err
	}
	head := resolves(ctx, dir, "HEAD")
	if !head && !hasRefs(ctx, dir) {
		return nil, false, nil
	}
	// Fields are split by US and commits by RS, which a subject cannot
	// hold: git stops %s at the first line.
	args := []string{"log", "--topo-order", "--no-color", "--decorate=short",
		"--format=%H%x1f%P%x1f%an%x1f%at%x1f%D%x1f%s%x1e", "-n", strconv.Itoa(limit + 1), "--branches", "--tags"}
	// HEAD only when it names a commit: on an unborn branch it names
	// nothing, and git refuses the whole log over it.
	if head {
		args = append(args, "HEAD")
	}
	out, err := run(ctx, dir, append(args, "--")...)
	if err != nil {
		return nil, false, err
	}
	for _, rec := range strings.Split(string(out), "\x1e") {
		rec = strings.TrimLeft(rec, "\n")
		f := strings.Split(rec, "\x1f")
		if len(f) != 6 || f[0] == "" {
			continue
		}
		c := LogCommit{Hash: f[0], Author: f[2], Subject: f[5]}
		if f[1] != "" {
			c.Parents = strings.Fields(f[1])
		}
		if s, err := strconv.ParseInt(f[3], 10, 64); err == nil {
			c.Time = time.Unix(s, 0)
		}
		for _, r := range strings.Split(f[4], ", ") {
			if r = strings.TrimSpace(r); r != "" {
				c.Refs = append(c.Refs, r)
			}
		}
		commits = append(commits, c)
	}
	if len(commits) > limit {
		return commits[:limit], true, nil
	}
	return commits, false, nil
}

// hasRefs says whether a repository has any branch or tag at all: a HEAD
// that names an unborn branch beside other branches still has history.
func hasRefs(ctx context.Context, dir string) bool {
	out, err := run(ctx, dir, "for-each-ref", "--count=1", "--format=%(refname)", "refs/heads", "refs/tags")
	return err == nil && strings.TrimSpace(string(out)) != ""
}
