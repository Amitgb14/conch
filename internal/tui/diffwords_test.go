package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

// What changed *inside* a line is the thing a diff does not tell you: the
// +/− already said which lines moved. A renamed variable in a long line is
// two characters, and finding them by eye is the slow part of reviewing in
// a terminal.

func TestChangedRunes(t *testing.T) {
	for _, c := range []struct {
		what               string
		old, new           string
		from, oldTo, newTo int
	}{
		{"a word in the middle", "if err != nil {", "if err2 != nil {", 6, 6, 7},
		{"the end only", "return count", "return total", 7, 12, 12},
		{"the start only", "oldName := 1", "newName := 1", 0, 3, 3},
		{"an insertion", "f(a)", "f(a, b)", 3, 3, 6},
		{"a deletion", "f(a, b)", "f(a)", 3, 6, 3},
		{"nothing shared", "abc", "xyz", 0, 3, 3},
		{"identical", "same", "same", 4, 4, 4},
		{"empty against text", "", "new", 0, 0, 3},
	} {
		from, oldTo, newTo := changedRunes(c.old, c.new)
		if from != c.from || oldTo != c.oldTo || newTo != c.newTo {
			t.Errorf("%s: %q→%q gave %d,%d,%d, want %d,%d,%d",
				c.what, c.old, c.new, from, oldTo, newTo, c.from, c.oldTo, c.newTo)
		}
	}

	// Runes, not bytes: a change beside an accent or an emoji must not cut
	// a character in half.
	from, oldTo, newTo := changedRunes("café serves tea", "café serves coffee")
	if from != 12 || oldTo != 15 || newTo != 18 {
		t.Fatalf("accented: %d,%d,%d", from, oldTo, newTo)
	}
	if from, _, _ := changedRunes("🎉 party", "🎉 parade"); from != 5 {
		t.Errorf("after an emoji: %d", from)
	}
}

// Only a replacement — one line out, one line in — has a "this became
// that" to show. A block of removals followed by a block of additions is
// not that, and marking it would invent a pairing nobody asked for.
func TestPairedChange(t *testing.T) {
	for _, c := range []struct {
		what  string
		lines []string
		at    int
		want  bool
	}{
		{"one for one", []string{" ctx", "-old", "+new", " ctx"}, 1, true},
		{"two removed", []string{"-a", "-b", "+c", " x"}, 1, false},
		{"two added", []string{"-a", "+b", "+c"}, 0, false},
		{"removal alone", []string{" x", "-a", " y"}, 1, false},
		{"addition alone", []string{" x", "+a", " y"}, 1, false},
		{"the file header", []string{"--- a/x.go", "+++ b/x.go"}, 0, false},
		{"past the end", []string{"-a"}, 0, false},
		{"before the start", []string{"-a", "+b"}, -1, false},
	} {
		if got := pairedChange(c.lines, c.at); got != c.want {
			t.Errorf("%s: %v", c.what, got)
		}
	}
}

// Two lines with nothing in common are a rewrite: marking all of both adds
// colour and no information.
func TestWorthHighlighting(t *testing.T) {
	old, new := "abc", "xyz"
	from, oldTo, newTo := changedRunes(old, new)
	if worthHighlighting(old, new, from, oldTo, newTo) {
		t.Error("a rewrite was marked")
	}
	old, new = "if err != nil {", "if err2 != nil {"
	from, oldTo, newTo = changedRunes(old, new)
	if !worthHighlighting(old, new, from, oldTo, newTo) {
		t.Error("a two-character change was not marked")
	}
}

// The whole thing, as the renderer uses it: the spans are against the
// line the renderer draws, tabs expanded and the marker counted.
func TestWordChangesAgainstWhatIsDrawn(t *testing.T) {
	cv := &changesView{diff: []string{
		"@@ -1,3 +1,3 @@",
		" func main() {",
		"-\tcount := 1",
		"+\ttotal := 1",
		" }",
	}}
	got := cv.wordChanges()
	if len(got) != 2 {
		t.Fatalf("marked %d lines: %+v", len(got), got)
	}
	// The removed line, with its tab expanded to four spaces as the
	// renderer expands it: "-    count := 1".
	drawn := strings.ReplaceAll(cv.diff[2], "\t", "    ")
	wc := got[2]
	if string([]rune(drawn)[wc.from:wc.to]) != "count" {
		t.Errorf("marked %q of %q", string([]rune(drawn)[wc.from:wc.to]), drawn)
	}
	drawnNew := strings.ReplaceAll(cv.diff[3], "\t", "    ")
	wn := got[3]
	if string([]rune(drawnNew)[wn.from:wn.to]) != "total" {
		t.Errorf("marked %q of %q", string([]rune(drawnNew)[wn.from:wn.to]), drawnNew)
	}
	// The marker is never part of the change: it belongs to the line.
	if wc.from < 1 || wn.from < 1 {
		t.Errorf("the +/− was marked: %+v %+v", wc, wn)
	}

	// A block replacement is left alone.
	cv2 := &changesView{diff: []string{"@@", "-a", "-b", "+c", "+d"}}
	if got := cv2.wordChanges(); len(got) != 0 {
		t.Errorf("a block was paired up: %+v", got)
	}
	// And an empty diff asks nothing of it.
	if got := (&changesView{}).wordChanges(); len(got) != 0 {
		t.Errorf("%+v", got)
	}
}

// What reaches the screen: the changed middle in its own style, the rest
// of the line in the line's colour, and the text itself unaltered.
func TestHighlightWordsDraws(t *testing.T) {
	was := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	applyTheme("conch", "")
	t.Cleanup(func() { lipgloss.SetColorProfile(was); applyTheme("conch", "") })

	line := "-    count := 1"
	out := highlightWords(line, 5, 10, styleErr, styleDiffRemoved)
	if ansi.Strip(out) != line {
		t.Fatalf("the text changed: %q", ansi.Strip(out))
	}
	if !strings.Contains(out, styleDiffRemoved.Render("count")) {
		t.Errorf("the changed word is not marked: %q", out)
	}
	// A span that collapses, or one past the end, draws the plain line
	// rather than panicking on a slice.
	if got := ansi.Strip(highlightWords(line, 9, 9, styleErr, styleDiffRemoved)); got != line {
		t.Errorf("empty span: %q", got)
	}
	if got := ansi.Strip(highlightWords(line, 2, 500, styleErr, styleDiffRemoved)); got != line {
		t.Errorf("span past the end: %q", got)
	}
	if got := ansi.Strip(highlightWords("", 1, 3, styleErr, styleDiffRemoved)); got != "" {
		t.Errorf("empty line: %q", got)
	}
}

// TestRenderDiffMarksTheChangedWords: the pure functions above can all be
// right while the renderer ignores them — taking the call out of
// renderDiff left every test above passing, which is the whole reason
// this one exists.
func TestRenderDiffMarksTheChangedWords(t *testing.T) {
	was := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	applyTheme("conch", "")
	t.Cleanup(func() { lipgloss.SetColorProfile(was); applyTheme("conch", "") })

	m := a2Model()
	cv := &changesView{diffFile: "a.go", diff: []string{
		"@@ -1,3 +1,3 @@",
		" func main() {",
		"-    count := 1",
		"+    total := 1",
		" }",
	}}
	out := strings.Join(cv.renderDiff(*m, 80, 10), "\n")
	if !strings.Contains(out, styleDiffRemoved.Render("count")) {
		t.Errorf("the removed word is not marked:\n%q", out)
	}
	if !strings.Contains(out, styleDiffAdded.Render("total")) {
		t.Errorf("the added word is not marked:\n%q", out)
	}
	// The text is untouched by the marking, and the line still reads as a
	// removal or an addition.
	plain := ansi.Strip(out)
	if !strings.Contains(plain, "-    count := 1") || !strings.Contains(plain, "+    total := 1") {
		t.Errorf("the lines changed:\n%s", plain)
	}

	// A block replacement is drawn plainly: no pairing to show.
	cv2 := &changesView{diffFile: "a.go", diff: []string{"@@ -1,2 +1,2 @@", "-alpha", "-beta", "+gamma", "+delta"}}
	out2 := strings.Join(cv2.renderDiff(*m, 80, 10), "\n")
	for _, word := range []string{"alpha", "beta", "gamma", "delta"} {
		if strings.Contains(out2, styleDiffRemoved.Render(word)) || strings.Contains(out2, styleDiffAdded.Render(word)) {
			t.Errorf("a block replacement marked %q:\n%q", word, out2)
		}
	}
}
