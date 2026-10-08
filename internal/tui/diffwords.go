package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Reading a diff, the question is almost never "which lines changed" — the
// +/− already say that — but "what changed in this line". A renamed
// variable in a hundred-character line is two characters, and finding them
// by eye is the slow part of reviewing in a terminal.
//
// So a removed line and the added line that replaces it are compared, and
// only the part that differs is picked out. The comparison is deliberately
// cheap: the common start, the common end, and whatever is left in the
// middle. No diff algorithm inside a diff line — a word-level LCS looks
// cleverer and, on two lines that happen to share a few letters, picks out
// scattered fragments that read as noise. A single changed middle is what
// an eye follows.

// pairedChange reports whether the removed and added lines at i are a
// replacement worth comparing: one "-" line followed by one "+" line.
// Several of each in a row are an insertion or a deletion of a block,
// where there is no "this became that" to show.
func pairedChange(lines []string, i int) bool {
	if i < 0 || i+1 >= len(lines) {
		return false
	}
	if !isDiffMinus(lines[i]) || !isDiffPlus(lines[i+1]) {
		return false
	}
	// The line before must not be another removal, nor the one after
	// another addition: that is a block, not a replacement.
	if i > 0 && isDiffMinus(lines[i-1]) {
		return false
	}
	if i+2 < len(lines) && isDiffPlus(lines[i+2]) {
		return false
	}
	return true
}

// isDiffMinus and isDiffPlus skip the file headers, which start with the
// same characters and are not content.
func isDiffMinus(l string) bool { return strings.HasPrefix(l, "-") && !strings.HasPrefix(l, "---") }
func isDiffPlus(l string) bool  { return strings.HasPrefix(l, "+") && !strings.HasPrefix(l, "+++") }

// changedRunes is the part of old and new that differs: the index where
// they stop agreeing from the left, and where they stop agreeing from the
// right, in runes. Both are relative to each line's own length, since the
// lines differ in length.
//
// It works in runes rather than bytes so that a change inside a word with
// an accent, or beside an emoji, does not cut a character in half.
func changedRunes(old, new string) (from, oldTo, newTo int) {
	o, n := []rune(old), []rune(new)
	for from < len(o) && from < len(n) && o[from] == n[from] {
		from++
	}
	oldTo, newTo = len(o), len(n)
	for oldTo > from && newTo > from && o[oldTo-1] == n[newTo-1] {
		oldTo--
		newTo--
	}
	return from, oldTo, newTo
}

// worthHighlighting says whether picking out the middle helps. Two lines
// with nothing in common are a rewrite: marking the whole of each adds
// colour and no information, and the +/− said it already.
func worthHighlighting(old, new string, from, oldTo, newTo int) bool {
	o, n := []rune(old), []rune(new)
	if from == 0 && oldTo == len(o) && newTo == len(n) {
		return false // nothing shared at either end
	}
	// Something has to be left to mark, on at least one side; two
	// identical lines cannot happen in a diff but cost nothing to refuse.
	return oldTo > from || newTo > from
}

// highlightWords renders a line with the changed middle picked out, the
// rest in the line's own colour. The prefix (+ or −) keeps the plain
// colour: it belongs to the line, not to what changed in it.
func highlightWords(line string, from, to int, base, mark lipgloss.Style) string {
	r := []rune(line)
	// The marker character is column 0 and is never part of the change.
	from, to = clamp(from, 1, len(r)), clamp(to, 1, len(r))
	if from >= to {
		return base.Render(line)
	}
	return base.Render(string(r[:from])) + mark.Render(string(r[from:to])) + base.Render(string(r[to:]))
}

// wordChange is the span of one line that differs from its pair, in runes
// and including the +/− marker at column 0, so a renderer can cut the
// line without counting it again.
type wordChange struct{ from, to int }

// wordChanges is every line of the diff that has a changed middle worth
// marking, by index. Worked out for the whole diff at once: it does not
// change while the reader scrolls, and a diff is read many more times
// than it arrives.
//
// The tab expansion the renderer does is applied here too, or the spans
// would be counted against a different string from the one drawn.
func (cv *changesView) wordChanges() map[int]wordChange {
	out := map[int]wordChange{}
	if cv == nil || len(cv.diff) == 0 {
		return out
	}
	expanded := make([]string, len(cv.diff))
	for i, l := range cv.diff {
		expanded[i] = strings.ReplaceAll(l, "\t", "    ")
	}
	for i := range expanded {
		if !pairedChange(expanded, i) {
			continue
		}
		// Past the marker on each side: what is compared is the content.
		old, new := expanded[i], expanded[i+1]
		from, oldTo, newTo := changedRunes(old[1:], new[1:])
		if !worthHighlighting(old[1:], new[1:], from, oldTo, newTo) {
			continue
		}
		out[i] = wordChange{from: from + 1, to: oldTo + 1}
		out[i+1] = wordChange{from: from + 1, to: newTo + 1}
	}
	return out
}
