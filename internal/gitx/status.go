package gitx

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Status summarises uncommitted changes in a worktree.
type Status struct {
	Staged, Unstaged, Untracked, Conflicts int // file counts
	Files                                  int // distinct changed files incl. untracked
	Added, Deleted                         int // lines vs HEAD (tracked files; binary files count 0)
}

// Clean reports whether the worktree has no changes at all.
func (s Status) Clean() bool { return s.Files == 0 }

// FileChange is one changed file.
type FileChange struct {
	Path     string
	OrigPath string // for renames
	Code     string // one of "M","A","D","R","C","T","U" (conflict),"?" (untracked)
	Staged   bool
	Unstaged bool
	Added    int
	Deleted  int
	Binary   bool
}

// Commit is one commit in a log.
type Commit struct {
	Hash    string // full
	Subject string
	Author  string
	Time    time.Time
}

// Changes is what a worktree or branch has changed relative to a base.
type Changes struct {
	Base    string
	Files   []FileChange // sorted by path
	Commits []Commit     // newest first, capped at maxCommits
}

const maxCommits = 200

// WorktreeStatus counts the uncommitted changes in the worktree at path.
func WorktreeStatus(ctx context.Context, path string) (Status, error) {
	files, err := statusFiles(ctx, path)
	if err != nil {
		return Status{}, err
	}
	var s Status
	for _, f := range files {
		s.Files++
		switch {
		case f.Code == "U":
			s.Conflicts++
		case f.Code == "?":
			s.Untracked++
		default:
			if f.Staged {
				s.Staged++
			}
			if f.Unstaged {
				s.Unstaged++
			}
		}
	}
	stats, err := numstat(ctx, path, headOrEmptyTree(ctx, path))
	if err != nil {
		return Status{}, err
	}
	for _, st := range stats {
		s.Added += st.Added
		s.Deleted += st.Deleted
	}
	return s, nil
}

// WorktreeChanges lists uncommitted changes (staged, unstaged and untracked)
// in the worktree at path, plus the commits HEAD has that base lacks.
func WorktreeChanges(ctx context.Context, path, base string) (Changes, error) {
	files, err := statusFiles(ctx, path)
	if err != nil {
		return Changes{}, err
	}
	stats, err := numstat(ctx, path, headOrEmptyTree(ctx, path))
	if err != nil {
		return Changes{}, err
	}
	for i := range files {
		f := &files[i]
		if st, ok := stats[f.Path]; ok {
			f.Added, f.Deleted, f.Binary = st.Added, st.Deleted, st.Binary
		} else if f.Code == "?" {
			f.Added, f.Binary = countLines(filepath.Join(path, f.Path))
		}
	}
	sortFiles(files)
	ch := Changes{Base: base, Files: files}
	if resolves(ctx, path, "HEAD") && resolves(ctx, path, base) {
		if ch.Commits, err = commits(ctx, path, base+"..HEAD"); err != nil {
			return Changes{}, err
		}
	}
	return ch, nil
}

// BranchChanges lists the files branch changed since it forked from base
// and the commits it has that base lacks. It works on branches that are not
// checked out anywhere.
func BranchChanges(ctx context.Context, root, branch, base string) (Changes, error) {
	ch := Changes{Base: base}
	if !resolves(ctx, root, base) || !resolves(ctx, root, branch) {
		return ch, nil
	}
	rng := base + "..." + branch
	out, err := run(ctx, root, "diff", "--no-color", "--no-ext-diff", "--name-status", "-z", rng, "--")
	if err != nil {
		return Changes{}, err
	}
	stats, err := numstat(ctx, root, rng)
	if err != nil {
		return Changes{}, err
	}
	f := splitNUL(out)
	for i := 0; i < len(f); i++ {
		fc := FileChange{Code: f[i][:1]}
		if (fc.Code == "R" || fc.Code == "C") && i+2 < len(f) {
			fc.OrigPath, fc.Path = f[i+1], f[i+2]
			i += 2
		} else if i+1 < len(f) {
			fc.Path = f[i+1]
			i++
		}
		st := stats[fc.Path]
		fc.Added, fc.Deleted, fc.Binary = st.Added, st.Deleted, st.Binary
		ch.Files = append(ch.Files, fc)
	}
	sortFiles(ch.Files)
	if ch.Commits, err = commits(ctx, root, base+".."+branch); err != nil {
		return Changes{}, err
	}
	return ch, nil
}

// statusFiles parses `git status --porcelain=v2 -z`.
func statusFiles(ctx context.Context, dir string, pathspec ...string) ([]FileChange, error) {
	args := append([]string{"status", "--porcelain=v2", "-z", "--untracked-files=normal", "--"}, pathspec...)
	out, err := run(ctx, dir, args...)
	if err != nil {
		return nil, err
	}
	var files []FileChange
	f := splitNUL(out)
	for i := 0; i < len(f); i++ {
		e := f[i]
		if len(e) < 2 {
			continue
		}
		switch e[0] {
		case '1', '2':
			// 1 XY sub mH mI mW hH hI path
			// 2 XY sub mH mI mW hH hI Xscore path NUL origPath
			n := 9
			if e[0] == '2' {
				n = 10
			}
			parts := strings.SplitN(e, " ", n)
			if len(parts) < n {
				continue
			}
			x, y := parts[1][0], parts[1][1]
			fc := FileChange{Path: parts[n-1], Staged: x != '.', Unstaged: y != '.'}
			fc.Code = string(x)
			if x == '.' {
				fc.Code = string(y)
			}
			if e[0] == '2' && i+1 < len(f) {
				i++
				fc.OrigPath = f[i]
			}
			files = append(files, fc)
		case 'u':
			// u XY sub m1 m2 m3 mW h1 h2 h3 path
			if parts := strings.SplitN(e, " ", 11); len(parts) == 11 {
				files = append(files, FileChange{Path: parts[10], Code: "U", Staged: true, Unstaged: true})
			}
		case '?':
			files = append(files, FileChange{Path: e[2:], Code: "?", Unstaged: true})
		}
	}
	return files, nil
}

type lineStat struct {
	Added, Deleted int
	Binary         bool
}

// numstat runs `git diff --numstat -z <rev>` and returns stats keyed by the
// (new) path.
func numstat(ctx context.Context, dir, rev string) (map[string]lineStat, error) {
	out, err := run(ctx, dir, "diff", "--no-color", "--no-ext-diff", "--numstat", "-z", rev, "--")
	if err != nil {
		return nil, err
	}
	stats := map[string]lineStat{}
	f := splitNUL(out)
	for i := 0; i < len(f); i++ {
		parts := strings.SplitN(f[i], "\t", 3)
		if len(parts) < 3 {
			continue
		}
		path := parts[2]
		if path == "" && i+2 < len(f) { // rename: "a\td\t" NUL orig NUL new
			path = f[i+2]
			i += 2
		}
		var st lineStat
		if parts[0] == "-" {
			st.Binary = true
		} else {
			st.Added, _ = strconv.Atoi(parts[0])
			st.Deleted, _ = strconv.Atoi(parts[1])
		}
		stats[path] = st
	}
	return stats, nil
}

// headOrEmptyTree returns "HEAD", or the empty tree when the repository has
// no commits yet, so diffs against it still work.
func headOrEmptyTree(ctx context.Context, dir string) string {
	if resolves(ctx, dir, "HEAD") {
		return "HEAD"
	}
	// Ask git rather than hard-coding the SHA-1 hash, for SHA-256 repos.
	if out, err := run(ctx, dir, "hash-object", "-t", "tree", "/dev/null"); err == nil {
		return strings.TrimSpace(string(out))
	}
	return "4b825dc642cb6eb9a060e54bf8d69288fbee4904"
}

func commits(ctx context.Context, dir, rng string) ([]Commit, error) {
	out, err := run(ctx, dir, "log", "-z", "--no-color", "-n", strconv.Itoa(maxCommits),
		"--format=%H%x00%s%x00%an%x00%ct", rng, "--")
	if err != nil {
		return nil, err
	}
	f := splitNUL(out)
	var cs []Commit
	for i := 0; i+3 < len(f); i += 4 {
		secs, _ := strconv.ParseInt(strings.TrimSpace(f[i+3]), 10, 64)
		cs = append(cs, Commit{
			Hash:    strings.TrimSpace(f[i]),
			Subject: f[i+1],
			Author:  f[i+2],
			Time:    time.Unix(secs, 0),
		})
	}
	return cs, nil
}

// countLines counts lines in an untracked file, treating files with a NUL
// in the first 8KiB as binary (as git does). Large files count 0.
func countLines(path string) (int, bool) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxDiff {
		return 0, false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	if bytes.IndexByte(b[:min(len(b), 8000)], 0) >= 0 {
		return 0, true
	}
	n := bytes.Count(b, []byte{'\n'})
	if len(b) > 0 && b[len(b)-1] != '\n' {
		n++
	}
	return n, false
}

func sortFiles(files []FileChange) {
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
}
