package server

import (
	"context"
	"sort"

	"github.com/Amitgb14/conch/internal/gitx"
	"github.com/Amitgb14/conch/internal/proto"
)

// Agents in parallel branches collide at merge time, and finding out then
// means a conflict to untangle after the work is done. So the project's
// refresh also says, for each task checkout, which other checkouts have
// touched the same files, and which files merging it into the base would
// conflict in — tried with merge-tree, which touches no checkout.

// maxOverlapFiles is how many shared files an overlap names.
const maxOverlapFiles = 5

// noteOverlaps fills in Overlaps and Conflicts for the linked worktrees of
// info. Errors leave them empty: these are hints, not the state of git.
func noteOverlaps(ctx context.Context, root string, info *proto.ProjectInfo) {
	touched := map[int][]string{}
	for i, wt := range info.Worktrees {
		if wt.Main || wt.Branch == "" {
			continue
		}
		files, err := gitx.TouchedFiles(ctx, wt.Path, info.Base)
		if err == nil && len(files) > 0 {
			touched[i] = files
		}
		if info.Base != "" && wt.Branch != info.Base {
			if conflicts, err := gitx.MergeConflicts(ctx, root, info.Base, wt.Branch); err == nil {
				info.Worktrees[i].Conflicts = conflicts
			}
		}
	}
	for i := range info.Worktrees {
		mine := touched[i]
		if len(mine) == 0 {
			continue
		}
		var overlaps []proto.Overlap
		for j, theirs := range touched {
			if j == i {
				continue
			}
			if shared := sharedFiles(mine, theirs); len(shared) > 0 {
				overlaps = append(overlaps, proto.Overlap{Branch: info.Worktrees[j].Branch, Files: shared})
			}
		}
		sort.Slice(overlaps, func(a, b int) bool { return overlaps[a].Branch < overlaps[b].Branch })
		info.Worktrees[i].Overlaps = overlaps
	}
}

// sharedFiles is what two sorted lists have in common, the first few.
func sharedFiles(a, b []string) []string {
	var out []string
	for i, j := 0, 0; i < len(a) && j < len(b) && len(out) < maxOverlapFiles; {
		switch {
		case a[i] == b[j]:
			out = append(out, a[i])
			i++
			j++
		case a[i] < b[j]:
			i++
		default:
			j++
		}
	}
	return out
}
