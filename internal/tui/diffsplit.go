package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Side by side, where the terminal is wide enough for it. A unified diff
// asks the reader to hold the old line in their head while they look at
// the new one three rows down; two columns put them beside each other and
// the eye does the work instead.
//
// It is not better on a narrow screen — two columns of forty characters
// wrap every line of real code — so it is what a wide terminal gets and
// `s` is how somebody who disagrees says so.

// sideBySideMin is the width at which two columns stop being cramped:
// two useful columns and the gutter between them. Below it the unified
// diff is the better reading, not a consolation.
const sideBySideMin = 100

// splitRow is one row of the two-column view. Either side may be empty —
// a removal with nothing replacing it, or the other way about — and the
// indexes point back into the diff so the gutter, the hunk cursor and the
// word marks find their line.
type splitRow struct {
	left, right string // the text, marker and all, or "" for nothing there
	leftAt      int    // index into the diff, or -1
	rightAt     int
	// header is a hunk's @@ line, drawn across both columns because it
	// belongs to neither.
	header string
	at     int // the header's own index into the diff
}

// splitRows pairs a unified diff into rows. Within a run of removals
// followed by additions, the first removal pairs with the first addition,
// the second with the second, and whatever is left over stands alone —
// which is what a reader does by eye, and is the only pairing that can be
// made without guessing.
func splitRows(diff []string) []splitRow {
	var rows []splitRow
	for i := 0; i < len(diff); {
		l := diff[i]
		switch {
		case strings.HasPrefix(l, "@@"):
			rows = append(rows, splitRow{header: l, at: i, leftAt: -1, rightAt: -1})
			i++
		case isDiffMinus(l) || isDiffPlus(l):
			// Take the whole run of removals, then the whole run of
			// additions that follows it: they are one change.
			start := i
			for i < len(diff) && isDiffMinus(diff[i]) {
				i++
			}
			dels := diff[start:i]
			addStart := i
			for i < len(diff) && isDiffPlus(diff[i]) {
				i++
			}
			adds := diff[addStart:i]
			for n := 0; n < len(dels) || n < len(adds); n++ {
				r := splitRow{leftAt: -1, rightAt: -1}
				if n < len(dels) {
					r.left, r.leftAt = dels[n], start+n
				}
				if n < len(adds) {
					r.right, r.rightAt = adds[n], addStart+n
				}
				rows = append(rows, r)
			}
		case strings.HasPrefix(l, " ") || l == "":
			// Context: the same line on both sides.
			rows = append(rows, splitRow{left: l, right: l, leftAt: i, rightAt: i})
			i++
		default:
			// The file headers and anything else git prints: across both
			// columns, as a hunk header is.
			rows = append(rows, splitRow{header: l, at: i, leftAt: -1, rightAt: -1})
			i++
		}
	}
	return rows
}

// firstRowAt is the row to start drawing at for a diff-line offset, so
// scrolling, following what just changed and jumping between hunks all
// keep working in the units they already use: lines of the diff.
func firstRowAt(rows []splitRow, line int) int {
	for i, r := range rows {
		for _, at := range []int{r.at, r.leftAt, r.rightAt} {
			if at >= 0 && at >= line {
				return i
			}
		}
	}
	return max(len(rows)-1, 0)
}

// renderSplit draws the diff in two columns. It keeps everything the
// unified view keeps — the hunk cursor and its mark, the gutter for lines
// that just changed, the word marks inside a replaced line — because they
// are what the view is for; only the arrangement differs.
func (cv *changesView) renderSplit(m Model, w, h int, marks map[int]int, hunks []string, fresh map[int]bool, words map[int]wordChange) []string {
	rows := splitRows(cv.diff)
	// Two columns and a rule between them. The gutter and the hunk
	// cursor live at the far left, as they do in the unified view, so an
	// eye that has learnt where to look does not have to learn again.
	const gutter = 3 // gutter, cursor, mark
	col := (w - gutter - 3) / 2
	if col < 10 {
		return nil // caller falls back to unified; this cannot be read
	}
	var lines []string
	for i := firstRowAt(rows, cv.diffScroll); i < len(rows) && len(lines) < h; i++ {
		r := rows[i]
		if r.header != "" {
			lines = append(lines, cv.splitHeaderLine(r, w, gutter, marks, hunks))
			continue
		}
		left := cv.splitCell(r.left, r.leftAt, col, fresh, words, styleErr, styleDiffRemoved)
		right := cv.splitCell(r.right, r.rightAt, col, fresh, words, styleOK, styleDiffAdded)
		mark := " "
		if (r.leftAt >= 0 && fresh[r.leftAt]) || (r.rightAt >= 0 && fresh[r.rightAt]) {
			mark = styleWarn.Render("▌")
		}
		lines = append(lines, mark+"  "+left+styleMuted.Render(" │ ")+right)
	}
	return lines
}

// splitHeaderLine draws a hunk header, or any other line that belongs to
// neither column, across the whole width — with the cursor and mark a
// hunk gets in the unified view.
func (cv *changesView) splitHeaderLine(r splitRow, w, gutter int, marks map[int]int, hunks []string) string {
	prefix := "   "
	style := styleMuted
	if strings.HasPrefix(r.header, "@@") {
		style = styleWork
		if hunk, ok := marks[r.at]; ok && len(hunks) > 0 && cv.data != nil && cv.data.Worktree != "" {
			cursor, mark := " ", " "
			if hunk == cv.hunkSel {
				cursor = "▸"
			}
			if cv.markedHunk(hunk) {
				mark = styleOK.Render("✓")
			}
			prefix = " " + cursor + mark
		}
	}
	return prefix + fit(style.Render(expandTabs(r.header)), max(w-gutter, 1))
}

// splitCell is one side of a row: the line, marked where it differs from
// the line opposite, cut to the column. An empty side is blank rather
// than a dash or a tilde — the eye reads the gap, and a filler character
// would be mistaken for content in a diff of all things.
func (cv *changesView) splitCell(line string, at, col int, fresh map[int]bool, words map[int]wordChange, base, mark lipgloss.Style) string {
	if line == "" && at < 0 {
		return strings.Repeat(" ", col)
	}
	l := expandTabs(line)
	var styled string
	switch {
	case at >= 0 && (isDiffMinus(l) || isDiffPlus(l)):
		if wc, ok := words[at]; ok {
			styled = highlightWords(l, wc.from, wc.to, base, mark)
		} else {
			styled = base.Render(l)
		}
	default:
		styled = l // context, in the terminal's own colour
	}
	return fit(styled, col)
}

// expandTabs is the renderer's own tab handling, kept in one place so the
// word marks and the drawing agree on what a line looks like.
func expandTabs(s string) string { return strings.ReplaceAll(s, "\t", "    ") }

// splitView is whether this diff is drawn in two columns: wide enough,
// and not turned off with `s` for this view.
func (cv *changesView) splitView(w int) bool { return w >= sideBySideMin && !cv.unsplit }
