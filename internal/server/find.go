package server

import (
	"context"
	"io/fs"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Amitgb14/conch/internal/gitx"
	"github.com/Amitgb14/conch/internal/proto"
)

// findWalkMax is how many files a checkout that is not a git repository
// is walked for: a folder of a million is answered from its first part
// rather than not at all.
const findWalkMax = 50_000

// findFiles answers fs.find: the files of a checkout matching a few
// letters, for Conch.app's search. The checkout must be a project or one
// of its worktrees, as for fs.list with a Root.
func (s *Server) findFiles(fp proto.FSFindParams) (proto.FSFindResult, *proto.Error) {
	root, perr := s.checkoutRoot(fp.Root)
	if perr != nil {
		return proto.FSFindResult{}, perr
	}
	limit := fp.Limit
	if limit <= 0 {
		limit = proto.FSFindDefault
	}
	limit = min(limit, proto.FSFindMax)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	files, err := gitx.Files(ctx, root)
	if err != nil {
		files = walkFiles(root, findWalkMax)
	}
	paths, more := matchFiles(files, fp.Query, limit)
	return proto.FSFindResult{Root: root, Paths: paths, Truncated: more}, nil
}

// walkFiles lists a folder that git does not know, leaving out hidden
// folders and node_modules, as anyone searching would.
func walkFiles(root string, max int) []string {
	var out []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && (strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if rel, err := filepath.Rel(root, p); err == nil {
			out = append(out, filepath.ToSlash(rel))
		}
		if len(out) >= max {
			return filepath.SkipAll
		}
		return nil
	})
	return out
}

// matchFiles is the files matching query, best first, at most limit, and
// whether more matched. A file's name containing the query is best, the
// path containing it next, then the letters in order anywhere; within each,
// the shorter path first, then by name. Case is ignored. An empty query
// matches nothing: listing a whole checkout is fs.list's.
func matchFiles(files []string, query string, limit int) ([]string, bool) {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return []string{}, false
	}
	type hit struct {
		path string
		rank int
	}
	var hits []hit
	for _, f := range files {
		lf := strings.ToLower(f)
		switch {
		case strings.Contains(strings.ToLower(path.Base(f)), q):
			hits = append(hits, hit{f, 0})
		case strings.Contains(lf, q):
			hits = append(hits, hit{f, 1})
		case inOrder(lf, q):
			hits = append(hits, hit{f, 2})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]
		if a.rank != b.rank {
			return a.rank < b.rank
		}
		if len(a.path) != len(b.path) {
			return len(a.path) < len(b.path)
		}
		return a.path < b.path
	})
	out := make([]string, 0, min(len(hits), limit))
	for i := 0; i < len(hits) && i < limit; i++ {
		out = append(out, hits[i].path)
	}
	return out, len(hits) > limit
}

// inOrder reports whether q's characters appear in s in order.
func inOrder(s, q string) bool {
	for _, r := range q {
		i := strings.IndexRune(s, r)
		if i < 0 {
			return false
		}
		s = s[i+len(string(r)):]
	}
	return true
}
