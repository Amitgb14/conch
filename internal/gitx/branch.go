package gitx

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Branch is a local branch with its tracking and base divergence.
type Branch struct {
	Name       string
	Upstream   string // e.g. "origin/feat", "" if none
	Gone       bool   // upstream configured but deleted
	Ahead      int    // vs upstream
	Behind     int
	BaseAhead  int // commits on branch not in base
	BaseBehind int // commits on base not in branch
	Committed  time.Time
	Subject    string
}

// Branches lists local branches, most recently committed first, with
// divergence from base. An unresolvable base leaves the base counts at 0.
func Branches(ctx context.Context, root, base string) ([]Branch, error) {
	fields := []string{"%(refname:short)", "%(upstream:short)", "%(upstream:track)",
		"%(committerdate:unix)", "%(subject)"}
	haveBase := resolves(ctx, root, base)
	aheadBehind := haveBase
	if aheadBehind {
		fields = append(fields, "%(ahead-behind:"+base+")")
	}
	out, err := forEachRef(ctx, root, fields)
	if err != nil && aheadBehind {
		// git < 2.41 rejects the atom; count per branch instead.
		var ge *gitError
		if !errors.As(err, &ge) || !strings.Contains(ge.stderr, "ahead-behind") {
			return nil, err
		}
		aheadBehind = false
		out, err = forEachRef(ctx, root, fields[:len(fields)-1])
	}
	if err != nil {
		return nil, err
	}

	var branches []Branch
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Split(line, "\x00")
		if len(f) < 5 {
			continue
		}
		b := Branch{Name: f[0], Upstream: f[1], Subject: f[4]}
		b.Gone, b.Ahead, b.Behind = parseTrack(f[2])
		if secs, err := strconv.ParseInt(f[3], 10, 64); err == nil {
			b.Committed = time.Unix(secs, 0)
		}
		switch {
		case aheadBehind && len(f) > 5:
			b.BaseAhead, b.BaseBehind = parsePair(f[5])
		case haveBase:
			b.BaseAhead, b.BaseBehind = revListCounts(ctx, root, base, "refs/heads/"+b.Name)
		}
		branches = append(branches, b)
	}
	sort.SliceStable(branches, func(i, j int) bool {
		return branches[i].Committed.After(branches[j].Committed)
	})
	return branches, nil
}

func forEachRef(ctx context.Context, root string, fields []string) ([]byte, error) {
	return run(ctx, root, "for-each-ref", "--sort=-committerdate",
		"--format="+strings.Join(fields, "%00"), "refs/heads")
}

// parseTrack parses %(upstream:track): "[ahead 2, behind 1]", "[gone]" or "".
func parseTrack(s string) (gone bool, ahead, behind int) {
	s = strings.Trim(s, "[]")
	if s == "gone" {
		return true, 0, 0
	}
	for _, part := range strings.Split(s, ", ") {
		k, v, _ := strings.Cut(part, " ")
		n, _ := strconv.Atoi(v)
		switch k {
		case "ahead":
			ahead = n
		case "behind":
			behind = n
		}
	}
	return false, ahead, behind
}

func parsePair(s string) (int, int) {
	a, b, _ := strings.Cut(strings.TrimSpace(s), " ")
	x, _ := strconv.Atoi(a)
	y, _ := strconv.Atoi(b)
	return x, y
}

// revListCounts returns (commits on rev not in base, commits on base not in rev).
func revListCounts(ctx context.Context, dir, base, rev string) (int, int) {
	out, err := run(ctx, dir, "rev-list", "--left-right", "--count", base+"..."+rev, "--")
	if err != nil {
		return 0, 0
	}
	behind, ahead := parsePair(strings.ReplaceAll(string(out), "\t", " "))
	return ahead, behind
}
