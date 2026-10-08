package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/Amitgb14/conch/internal/proto"
)

// Pairing a unified diff into two columns. Within a run of removals and
// the additions after it, the first pairs with the first and so on, and
// whatever is left over stands alone — the only pairing that can be made
// without guessing which line became which.
func TestSplitRowsPairsRuns(t *testing.T) {
	rows := splitRows([]string{
		"@@ -1,6 +1,6 @@",
		" ctx",
		"-old one",
		"-old two",
		"+new one",
		"+new two",
		"+new three",
		" tail",
	})
	type pair struct{ l, r string }
	var got []pair
	for _, r := range rows {
		if r.header != "" {
			got = append(got, pair{"@@", "@@"})
			continue
		}
		got = append(got, pair{r.left, r.right})
	}
	want := []pair{
		{"@@", "@@"},
		{" ctx", " ctx"},
		{"-old one", "+new one"},
		{"-old two", "+new two"},
		{"", "+new three"}, // nothing it replaces: the right side alone
		{" tail", " tail"},
	}
	if len(got) != len(want) {
		t.Fatalf("%d rows, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d is %+v, want %+v", i, got[i], want[i])
		}
	}

	// A removal with nothing after it keeps the left side alone, and an
	// addition on its own keeps the right.
	rows = splitRows([]string{"-gone", " ctx", "+added"})
	if rows[0].left != "-gone" || rows[0].right != "" {
		t.Errorf("a removal alone: %+v", rows[0])
	}
	if rows[2].left != "" || rows[2].right != "+added" {
		t.Errorf("an addition alone: %+v", rows[2])
	}
	// The file headers belong to neither column.
	rows = splitRows([]string{"diff --git a/x b/x", "--- a/x", "+++ b/x", "@@ -1 +1 @@"})
	for i, r := range rows {
		if r.header == "" {
			t.Errorf("row %d took a side: %+v", i, r)
		}
	}
	// And nothing at all is no rows, not a panic.
	if rows := splitRows(nil); len(rows) != 0 {
		t.Errorf("%+v", rows)
	}
}

// Every row remembers where its text came from, so the gutter, the hunk
// cursor and the word marks find their line — and so scrolling, which is
// counted in diff lines everywhere else, still lands in the right place.
func TestSplitRowsKeepTheirPlace(t *testing.T) {
	diff := []string{"@@ -1,3 +1,3 @@", " ctx", "-old", "+new", " tail"}
	rows := splitRows(diff)
	for _, r := range rows {
		for _, at := range []int{r.at, r.leftAt, r.rightAt} {
			if at >= len(diff) {
				t.Fatalf("a row points past the diff: %+v", r)
			}
		}
	}
	// The replacement row points at both of its source lines.
	var rep splitRow
	for _, r := range rows {
		if r.left == "-old" {
			rep = r
		}
	}
	if rep.leftAt != 2 || rep.rightAt != 3 {
		t.Fatalf("the replacement points at %d and %d", rep.leftAt, rep.rightAt)
	}
	// Scrolling to a diff line finds the row that shows it.
	if got := firstRowAt(rows, 3); rows[got].rightAt != 3 && rows[got].leftAt != 2 {
		t.Errorf("scrolling to line 3 landed on %+v", rows[got])
	}
	if got := firstRowAt(rows, 0); got != 0 {
		t.Errorf("the top is row %d", got)
	}
	// Past the end lands on the last row rather than out of range.
	if got := firstRowAt(rows, 999); got != len(rows)-1 {
		t.Errorf("past the end: row %d of %d", got, len(rows))
	}
}

// What reaches the screen: two columns, the old on the left and the new
// on the right, with everything the unified view carries.
func TestRenderSplitDraws(t *testing.T) {
	was := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	applyTheme("conch", "")
	t.Cleanup(func() { lipgloss.SetColorProfile(was); applyTheme("conch", "") })

	m := a2Model()
	cv := &changesView{diffFile: "a.go", data: &proto.Changes{Worktree: "/w"}, diff: []string{
		"@@ -1,3 +1,3 @@",
		" func main() {",
		"-    count := 1",
		"+    total := 1",
		" }",
	}}
	out := cv.renderDiff(*m, 140, 12)
	plain := a2Plain(out)
	// Both sides of the change are on one row, in that order.
	var row string
	for _, l := range strings.Split(plain, "\n") {
		if strings.Contains(l, "count") {
			row = l
		}
	}
	if row == "" {
		t.Fatalf("no row with the old line:\n%s", plain)
	}
	if !strings.Contains(row, "total") {
		t.Fatalf("the new line is not beside the old one: %q", row)
	}
	if strings.Index(row, "count") > strings.Index(row, "total") {
		t.Errorf("the new line is to the left of the old one: %q", row)
	}
	// The word marks survive the arrangement.
	joined := strings.Join(out, "\n")
	if !strings.Contains(joined, styleDiffRemoved.Render("count")) || !strings.Contains(joined, styleDiffAdded.Render("total")) {
		t.Errorf("the changed words are not marked in two columns")
	}
	// Nothing drawn is wider than the terminal.
	for i, l := range out {
		if got := ansi.StringWidth(l); got > 140 {
			t.Errorf("line %d is %d wide", i, got)
		}
	}
}

// Two columns are for a wide terminal. Narrow, they would be two columns
// of wrapped code, which is worse than the unified diff rather than a
// smaller version of it — so the unified one is what a narrow screen gets,
// and `s` is how somebody says they disagree either way.
func TestSplitOnlyWhereThereIsRoom(t *testing.T) {
	m := a2Model()
	cv := &changesView{diffFile: "a.go", data: &proto.Changes{Worktree: "/w"}, diff: []string{
		"@@ -1,2 +1,2 @@", "-old", "+new",
	}}
	wide := a2Plain(cv.renderDiff(*m, 140, 10))
	narrow := a2Plain(cv.renderDiff(*m, 70, 10))
	onOneRow := func(out string) bool {
		for _, l := range strings.Split(out, "\n") {
			if strings.Contains(l, "old") && strings.Contains(l, "new") {
				return true
			}
		}
		return false
	}
	if !onOneRow(wide) {
		t.Errorf("a wide terminal did not get two columns:\n%s", wide)
	}
	if onOneRow(narrow) {
		t.Errorf("a narrow terminal was given two columns:\n%s", narrow)
	}
	// s says otherwise, both ways.
	cv.unsplit = true
	if onOneRow(a2Plain(cv.renderDiff(*m, 140, 10))) {
		t.Errorf("s did not turn the columns off")
	}
	cv.unsplit = false
	if !onOneRow(a2Plain(cv.renderDiff(*m, 140, 10))) {
		t.Errorf("s did not turn them back on")
	}
	// The hint says which way s goes, in both arrangements.
	if out := a2Plain(cv.renderDiff(*m, 160, 10)); !strings.Contains(out, "s unified") {
		t.Errorf("in two columns the hint offers:\n%s", out)
	}
	cv.unsplit = true
	if out := a2Plain(cv.renderDiff(*m, 160, 10)); !strings.Contains(out, "s side by side") {
		t.Errorf("in one column the hint offers:\n%s", out)
	}
}

// TestSplitWrapsLongLines: a column that cuts a line is worse than no
// column at all — the end of a line is often exactly where the change
// is. Reported from use: "side by side long line not shows full line".
func TestSplitWrapsLongLines(t *testing.T) {
	was := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	applyTheme("conch", "")
	t.Cleanup(func() { lipgloss.SetColorProfile(was); applyTheme("conch", "") })

	m := a2Model()
	long := strings.Repeat("abcdefghij", 12) // 120 characters, past any column
	cv := &changesView{diffFile: "a.go", data: &proto.Changes{Worktree: "/w"}, diff: []string{
		"@@ -1,2 +1,2 @@",
		"-" + long + "END-OF-THE-OLD",
		"+" + long + "END-OF-THE-NEW",
	}}
	out := cv.renderDiff(*m, 140, 20)
	plain := a2Plain(out)
	// Each side read down its own column, as an eye does: the whole line
	// is there, wrapped, rather than cut at the column.
	var left, right strings.Builder
	rows := 0
	for _, l := range strings.Split(plain, "\n") {
		l, r, ok := strings.Cut(l, "│")
		if !ok {
			continue // the header, the hunk line
		}
		rows++
		left.WriteString(strings.TrimRight(strings.TrimSpace(l), " "))
		right.WriteString(strings.TrimRight(strings.TrimSpace(r), " "))
	}
	if !strings.Contains(left.String(), long+"END-OF-THE-OLD") {
		t.Errorf("the old line is not all there:\n%s", left.String())
	}
	if !strings.Contains(right.String(), long+"END-OF-THE-NEW") {
		t.Errorf("the new line is not all there:\n%s", right.String())
	}
	// Both sides wrap over the same rows, so they can still be compared.
	if rows < 2 {
		t.Errorf("a 134-character line took %d rows at 140 columns", rows)
	}
	// Still inside the terminal, on every row.
	for i, l := range out {
		if got := ansi.StringWidth(l); got > 140 {
			t.Errorf("row %d is %d wide", i, got)
		}
	}

	// The gutter marks the line once, not once per wrapped row.
	cv.fresh, cv.freshUntil = map[int]bool{1: true}, time.Now().Add(time.Minute)
	marks := 0
	for _, l := range strings.Split(a2Plain(cv.renderDiff(*m, 140, 20)), "\n") {
		if strings.HasPrefix(l, "▌") {
			marks++
		}
	}
	if marks != 1 {
		t.Errorf("the gutter marked %d rows of one line", marks)
	}
}

// TestDiffSettingDecides: which way a diff opens is the reader's to
// choose, not conch's. The setting is the starting point; `s` says
// otherwise for the file in front of them.
func TestDiffSettingDecides(t *testing.T) {
	m := a2Model()
	cv := &changesView{diffFile: "a.go", data: &proto.Changes{Worktree: "/w"},
		diff: []string{"@@ -1,2 +1,2 @@", "-old", "+new"}}
	sideBySide := func(w int) bool {
		for _, l := range strings.Split(a2Plain(cv.renderDiff(*m, w, 10)), "\n") {
			if strings.Contains(l, "old") && strings.Contains(l, "new") {
				return true
			}
		}
		return false
	}

	// auto: the width decides.
	m.cfg.UI.Diff = ""
	if !sideBySide(140) || sideBySide(70) {
		t.Error("auto did not follow the width")
	}
	// side: two columns however narrow — somebody who asks for them on a
	// narrow terminal has said they would rather wrap.
	m.cfg.UI.Diff = diffSide
	if !sideBySide(140) || !sideBySide(70) {
		t.Error("“always side by side” did not")
	}
	// unified: one column however wide.
	m.cfg.UI.Diff = diffUnified
	if sideBySide(140) {
		t.Error("“always one column” gave two")
	}
	// Nonsense in the file reads as auto rather than as nothing.
	m.cfg.UI.Diff = "sideways"
	if !sideBySide(140) || sideBySide(70) {
		t.Error("an unknown setting did not fall back to auto")
	}

	// s overrides whichever way the setting points.
	m.cfg.UI.Diff = diffUnified
	cv.unsplit = true
	if !sideBySide(140) {
		t.Error("s did not turn columns on against the setting")
	}
	m.cfg.UI.Diff = diffSide
	if sideBySide(140) {
		t.Error("s did not turn columns off against the setting")
	}
}
