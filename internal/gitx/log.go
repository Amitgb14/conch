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
	// Commits are split by NUL (-z), which nothing in a commit can hold,
	// and fields by US, with the subject last so a US inside one (git
	// keeps control characters) stays in it: split by RS as well, a
	// subject holding either once lost its commit, and its parent's lane
	// ran off the bottom of the graph.
	args := []string{"log", "-z", "--topo-order", "--no-color", "--decorate=short",
		"--format=%H%x1f%P%x1f%an%x1f%at%x1f%D%x1f%s", "-n", strconv.Itoa(limit + 1), "--branches", "--tags"}
	// HEAD only when it names a commit: on an unborn branch it names
	// nothing, and git refuses the whole log over it.
	if head {
		args = append(args, "HEAD")
	}
	out, err := run(ctx, dir, append(args, "--")...)
	if err != nil {
		return nil, false, err
	}
	for _, rec := range strings.Split(string(out), "\x00") {
		rec = strings.TrimLeft(rec, "\n")
		f := strings.SplitN(rec, "\x1f", 6)
		if len(f) != 6 || f[0] == "" {
			continue
		}
		// Author and subject are whatever was committed, so they are made
		// safe to draw here, for every client at once. Refs are not: git
		// refuses controls below 0x20 in a ref, and a name is matched
		// against branches, so a client cleans those as it draws them.
		c := LogCommit{Hash: f[0], Author: Printable(f[2]), Subject: Printable(f[5])}
		if f[1] != "" {
			c.Parents = strings.Fields(f[1])
		}
		if s, err := strconv.ParseInt(f[3], 10, 64); err == nil {
			c.Time = time.Unix(s, 0)
		}
		for _, r := range strings.Split(f[4], ", ") {
			// "grafted" marks a shallow clone's boundary, not a ref.
			if r = strings.TrimSpace(r); r != "" && r != "grafted" {
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

// Remotes are the names of a repository's remotes (origin, upstream, a
// fork's), by which a decoration such as fork/main is told to be a
// remote's branch rather than a local one with a slash in its name.
func Remotes(ctx context.Context, dir string) []string {
	out, err := run(ctx, dir, "remote")
	if err != nil {
		return nil
	}
	return strings.Fields(string(out))
}
