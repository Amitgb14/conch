package gitx

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// One commit as a graph's side panel shows it, and what changed between
// two commits — Conch.app's commit graph, where a click opens a commit and
// a shift-click compares two.

// CommitDetail is a commit, its message whole, and the files it changed.
type CommitDetail struct {
	Hash    string
	Parents []string
	Author  string
	Email   string
	Time    time.Time
	Subject string
	Body    string
	// Files are what it changed against its first parent (the root commit:
	// against nothing).
	Files []FileStat
}

// FileStat is one file a commit or a range changed.
type FileStat struct {
	Path     string
	OrigPath string // a rename's old name
	Code     string // A M D R C T
	Added    int
	Deleted  int
	Binary   bool
}

var revPattern = regexp.MustCompile(`^[0-9A-Za-z._/@^~{}-]{1,200}$`)

// validRev keeps a revision from being taken as an option or a path.
func validRev(rev string) error {
	if rev == "" || strings.HasPrefix(rev, "-") || !revPattern.MatchString(rev) || strings.Contains(rev, "..") {
		return errors.New("not a commit: " + rev)
	}
	return nil
}

// ShowCommit reads a commit and the files it changed.
func ShowCommit(ctx context.Context, dir, rev string) (CommitDetail, error) {
	if err := validRev(rev); err != nil {
		return CommitDetail{}, err
	}
	out, err := run(ctx, dir, "show", "-s", "--no-color", "--format=%H%x00%P%x00%an%x00%ae%x00%ct%x00%s%x00%b", rev, "--")
	if err != nil {
		return CommitDetail{}, err
	}
	f := strings.SplitN(string(out), "\x00", 7)
	if len(f) < 7 {
		return CommitDetail{}, errors.New("unexpected git show output")
	}
	ct, _ := strconv.ParseInt(f[4], 10, 64)
	d := CommitDetail{Hash: f[0], Author: f[2], Email: f[3], Time: time.Unix(ct, 0), Subject: f[5], Body: strings.TrimSpace(f[6])}
	if p := strings.TrimSpace(f[1]); p != "" {
		d.Parents = strings.Fields(p)
	}
	base := d.Hash + "^"
	if len(d.Parents) == 0 {
		base = emptyTree(ctx, dir)
	}
	d.Files, err = diffStat(ctx, dir, base, d.Hash)
	return d, err
}

// Compare is what changed from one commit to another, and how many
// commits that took.
type Compare struct {
	Files   []FileStat
	Commits int
}

// CompareCommits is what changed between from and to, and the commits
// reachable from to but not from.
func CompareCommits(ctx context.Context, dir, from, to string) (Compare, error) {
	if err := validRev(from); err != nil {
		return Compare{}, err
	}
	if err := validRev(to); err != nil {
		return Compare{}, err
	}
	files, err := diffStat(ctx, dir, from, to)
	if err != nil {
		return Compare{}, err
	}
	out, err := run(ctx, dir, "rev-list", "--count", from+".."+to, "--")
	if err != nil {
		return Compare{}, err
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(out)))
	return Compare{Files: files, Commits: n}, nil
}

// CommitFileDiff is one file's diff in a commit, or between two commits
// when from is given.
func CommitFileDiff(ctx context.Context, dir, from, to, path string) (string, error) {
	if err := validRev(to); err != nil {
		return "", err
	}
	if from == "" {
		from = to + "^"
		if _, err := run(ctx, dir, "rev-parse", "--verify", "--quiet", from); err != nil {
			from = emptyTree(ctx, dir)
		}
	} else if err := validRev(from); err != nil {
		return "", err
	}
	if path == "" || strings.HasPrefix(path, "-") {
		return "", errors.New("no file given")
	}
	out, err := run(ctx, dir, "diff", "--no-color", "--no-ext-diff", "-M", from, to, "--", path)
	return string(out), err
}

// diffStat is every file changed from a to b, with its status and counts.
func diffStat(ctx context.Context, dir, a, b string) ([]FileStat, error) {
	names, err := run(ctx, dir, "diff", "--no-color", "--no-ext-diff", "-M", "--name-status", "-z", a, b, "--")
	if err != nil {
		return nil, err
	}
	nums, err := run(ctx, dir, "diff", "--no-color", "--no-ext-diff", "-M", "--numstat", "-z", a, b, "--")
	if err != nil {
		return nil, err
	}
	stats := parseNumstat(nums)
	var out []FileStat
	f := splitNUL(names)
	for i := 0; i < len(f); i++ {
		code := f[i]
		if code == "" {
			continue
		}
		fs := FileStat{Code: code[:1]}
		if (fs.Code == "R" || fs.Code == "C") && i+2 < len(f) {
			fs.OrigPath, fs.Path = f[i+1], f[i+2]
			i += 2
		} else if i+1 < len(f) {
			fs.Path = f[i+1]
			i++
		}
		if st, ok := stats[fs.Path]; ok {
			fs.Added, fs.Deleted, fs.Binary = st.Added, st.Deleted, st.Binary
		}
		out = append(out, fs)
	}
	return out, nil
}

// parseNumstat reads `git diff --numstat -z` into stats by new path.
func parseNumstat(out []byte) map[string]lineStat {
	stats := map[string]lineStat{}
	f := splitNUL(out)
	for i := 0; i < len(f); i++ {
		parts := strings.SplitN(f[i], "\t", 3)
		if len(parts) < 3 {
			continue
		}
		path := parts[2]
		if path == "" && i+2 < len(f) {
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
	return stats
}

func emptyTree(ctx context.Context, dir string) string {
	if out, err := run(ctx, dir, "hash-object", "-t", "tree", "/dev/null"); err == nil {
		return strings.TrimSpace(string(out))
	}
	return "4b825dc642cb6eb9a060e54bf8d69288fbee4904"
}
