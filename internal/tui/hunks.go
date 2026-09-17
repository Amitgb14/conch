package tui

import (
	"slices"
	"strings"
)

// Marking single hunks of a file's diff for the next commit. What is marked
// is the hunk's own text, so a hunk that changed underneath drops its mark
// instead of committing something else.

// fileHunks are the marked hunks of one file's diff.
type fileHunks struct {
	header string   // the diff's lines before its first hunk
	marked []string // hunk texts, in the order they appear
}

// splitHunks cuts a unified diff into the header and its hunks, with the
// line each hunk starts at.
func splitHunks(diff []string) (header string, hunks []string, at []int) {
	var b strings.Builder
	for i, l := range diff {
		if strings.HasPrefix(l, "@@") {
			hunks = append(hunks, l+"\n")
			at = append(at, i)
			continue
		}
		if len(hunks) == 0 {
			b.WriteString(l + "\n")
			continue
		}
		hunks[len(hunks)-1] += l + "\n"
	}
	return b.String(), hunks, at
}

// hunksOf is the open diff's hunks and where they start.
func (cv *changesView) hunksOf() (header string, hunks []string, at []int) {
	if cv.diffFile == "" || cv.diff == nil {
		return "", nil, nil
	}
	return splitHunks(cv.diff)
}

// markedHunk reports whether the hunk at index i of the open diff is marked.
func (cv *changesView) markedHunk(i int) bool {
	fh := cv.hunks[cv.diffFile]
	if fh == nil {
		return false
	}
	_, hunks, _ := cv.hunksOf()
	if i < 0 || i >= len(hunks) {
		return false
	}
	for _, text := range fh.marked {
		if text == hunks[i] {
			return true
		}
	}
	return false
}

// toggleHunk marks or unmarks hunk i of the open diff. Marking a hunk drops
// the whole-file mark: a file is committed either whole or by hunks.
func (cv *changesView) toggleHunk(i int) {
	header, hunks, _ := cv.hunksOf()
	if i < 0 || i >= len(hunks) {
		return
	}
	if cv.hunks == nil {
		cv.hunks = map[string]*fileHunks{}
	}
	delete(cv.marked, cv.diffFile) // a file goes whole or by hunks, not both
	fh := cv.hunks[cv.diffFile]
	if fh == nil {
		fh = &fileHunks{}
		cv.hunks[cv.diffFile] = fh
	}
	fh.header = header
	text := hunks[i]
	for j, m := range fh.marked {
		if m == text {
			fh.marked = append(fh.marked[:j], fh.marked[j+1:]...)
			if len(fh.marked) == 0 {
				delete(cv.hunks, cv.diffFile)
			}
			return
		}
	}
	// Keep them in the order they appear, so the patch reads like the diff.
	var kept []string
	for _, h := range hunks {
		if h == text || slices.Contains(fh.marked, h) {
			kept = append(kept, h)
		}
	}
	fh.marked = kept
}

// pruneHunks drops marks whose hunk is no longer in the file's diff, after
// a refresh changed it.
func (cv *changesView) pruneHunks(file string, diff []string) {
	fh := cv.hunks[file]
	if fh == nil {
		return
	}
	header, hunks, _ := splitHunks(diff)
	fh.header = header
	var kept []string
	for _, h := range hunks {
		if slices.Contains(fh.marked, h) {
			kept = append(kept, h)
		}
	}
	if fh.marked = kept; len(kept) == 0 {
		delete(cv.hunks, file)
	}
}

// commitSelection is what the next commit takes: whole files, hunks as a
// patch, or everything when both are empty.
type commitSelection struct {
	files []string // whole files, by path
	patch string   // the marked hunks, as one diff
	hunks int      // how many hunks the patch holds
	inned int      // how many files those hunks are in
}

// selection gathers what is marked in this view.
func (cv *changesView) selection() commitSelection {
	sel := commitSelection{files: cv.markedPaths()}
	if cv.data == nil {
		return sel
	}
	var b strings.Builder
	for _, f := range cv.data.Files { // in the order the files are shown
		fh := cv.hunks[f.Path]
		if fh == nil || len(fh.marked) == 0 {
			continue
		}
		b.WriteString(fh.header)
		for _, h := range fh.marked {
			b.WriteString(h)
		}
		sel.hunks += len(fh.marked)
		sel.inned++
	}
	sel.patch = b.String()
	return sel
}

// clearMarks forgets every mark, once they have been committed.
func (cv *changesView) clearMarks() {
	cv.marked, cv.hunks = nil, nil
}
