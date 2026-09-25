package tui

import (
	"encoding/json"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/Amitgb14/conch/internal/proto"
)

// searchFixture shows terminal p2 in scroll mode, with history, on a
// machine whose server can search; lines are the frame's.
func searchFixture(t *testing.T, lines []string, caps ...string) (*Model, *a1Peer) {
	t.Helper()
	m, _ := a1Fixture(t, false)
	if caps == nil {
		caps = []string{proto.CapPaneSearch}
	}
	c, peer := a1FakeClient(t, caps...)
	m.machines[0].c, m.machines[0].server = c, c.Server
	a1Open(t, m, paneNodeID(localMachine, "p2"))
	_, rows := m.paneArea()
	if lines == nil {
		lines = make([]string, rows)
	}
	m.frames[paneKey(localMachine, "p2")] = &proto.Frame{ID: "p2", Lines: lines, History: 500}
	m.syncView()
	m.focus = focusMain
	a1Prefixed(t, m, runes("["))
	if !m.scrollMode {
		t.Fatal("not in scroll mode")
	}
	return m, peer
}

func typeText(t *testing.T, m *Model, s string) {
	t.Helper()
	for _, r := range s {
		a1Key(t, m, runes(string(r)))
	}
}

// searched runs the search a key started and returns what the server was
// asked, after handing the answer back to the model.
func searched(t *testing.T, m *Model, peer *a1Peer, cmd tea.Cmd) proto.PaneSearchParams {
	t.Helper()
	if cmd == nil {
		t.Fatal("no search was started")
	}
	msg := cmd()
	sr, ok := msg.(searchResultMsg)
	if !ok {
		t.Fatalf("got %T, want searchResultMsg", msg)
	}
	next, _ := m.Update(sr)
	*m = next.(Model)
	var p proto.PaneSearchParams
	for _, got := range peer.snapshot() {
		if got.Method == proto.MethodPaneSearch {
			p = proto.PaneSearchParams{} // omitted fields are false, not the last one's
			_ = json.Unmarshal(got.Params, &p)
		}
	}
	return p
}

func TestScrollSearchTyping(t *testing.T) {
	m, peer := searchFixture(t, nil)
	_, rows := m.paneArea()
	a1Key(t, m, runes("/"))
	if !m.search.typing || !m.search.back {
		t.Fatalf("/ should start a search back: %+v", m.search)
	}
	typeText(t, m, "erxx")
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyBackspace})
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyBackspace})
	typeText(t, m, "ror")
	a1Key(t, m, tea.KeyMsg{Type: tea.KeySpace})
	if m.search.input != "error " {
		t.Fatalf("input %q", m.search.input)
	}
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyBackspace})
	// Keys that mean something in scroll mode are text while typing.
	if !m.scrollMode || m.offset != 0 {
		t.Fatal("typing left scroll mode or scrolled")
	}

	peer.setResult(proto.MethodPaneSearch, proto.PaneSearchResult{Found: true, Line: 100, Col: 7, Width: 5, History: 500})
	p := searched(t, m, peer, a1Key(t, m, tea.KeyMsg{Type: tea.KeyEnter}))
	if p.ID != "p2" || p.Query != "error" || !p.Backward || p.Line != 500+rows-1 || p.Col != 0 {
		t.Fatalf("asked %+v", p)
	}
	if m.search.typing || m.search.query != "error" {
		t.Fatalf("after enter: %+v", m.search)
	}
	// Line 100 is far off screen: it comes to the middle, cursor on it.
	top := 500 - m.offset
	if 100 < top || 100 >= top+rows || m.curY != 100-top || m.curX != 7 {
		t.Fatalf("offset %d top %d cursor %d,%d", m.offset, top, m.curX, m.curY)
	}
	if m.curY != rows/2 {
		t.Fatalf("match on row %d, want the middle %d", m.curY, rows/2)
	}
	peer.waitMethod(t, proto.MethodPaneScroll, "")
}

func TestScrollSearchCancelKeepsLastQuery(t *testing.T) {
	m, _ := searchFixture(t, nil)
	m.search.query = "before"
	a1Key(t, m, runes("?"))
	if m.search.back {
		t.Fatal("? should search towards the live screen")
	}
	typeText(t, m, "abc")
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyCtrlU})
	if m.search.input != "" {
		t.Fatalf("ctrl+u left %q", m.search.input)
	}
	// Backspace with nothing typed stops, as vim does.
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyBackspace})
	if m.search.typing || !m.scrollMode {
		t.Fatalf("backspace on empty: %+v", m.search)
	}
	a1Key(t, m, runes("/"))
	typeText(t, m, "x")
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.search.typing || !m.scrollMode || m.search.query != "before" {
		t.Fatalf("esc should stop typing and keep scroll mode and the last query: %+v scroll %v", m.search, m.scrollMode)
	}
	// Other keys (arrows) are ignored while typing.
	a1Key(t, m, runes("/"))
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if !m.search.typing || m.search.input != "" {
		t.Fatalf("up while typing: %+v", m.search)
	}
	// Leaving and entering scroll mode drops a half-typed query.
	m.scrollMode = false
	m.enterScrollMode()
	if m.search.typing {
		t.Fatal("still typing in a new scroll mode")
	}
}

func TestScrollSearchEmptyEnterRepeats(t *testing.T) {
	m, peer := searchFixture(t, nil)
	a1Key(t, m, runes("/"))
	if cmd := a1Key(t, m, tea.KeyMsg{Type: tea.KeyEnter}); cmd != nil {
		t.Fatal("no query ever: nothing to search")
	}
	m.search.query = "again"
	peer.setResult(proto.MethodPaneSearch, proto.PaneSearchResult{Found: true, Line: 499, History: 500})
	a1Key(t, m, runes("/"))
	if p := searched(t, m, peer, a1Key(t, m, tea.KeyMsg{Type: tea.KeyEnter})); p.Query != "again" {
		t.Fatalf("empty enter searched %q", p.Query)
	}
}

func TestScrollSearchNextAndPrevious(t *testing.T) {
	m, peer := searchFixture(t, nil)
	if cmd := a1Key(t, m, runes("n")); cmd != nil || !m.scrollMode {
		t.Fatal("n without a query should do nothing and stay")
	}
	m.search.query, m.search.back = "x", true
	peer.setResult(proto.MethodPaneSearch, proto.PaneSearchResult{Found: true, Line: 510, Col: 3, History: 500})
	if p := searched(t, m, peer, a1Key(t, m, runes("n"))); !p.Backward {
		t.Fatal("n after / should look back")
	}
	if p := searched(t, m, peer, a1Key(t, m, runes("N"))); p.Backward {
		t.Fatal("N after / should look forward")
	}
	m.search.back = false
	if p := searched(t, m, peer, a1Key(t, m, runes("n"))); p.Backward {
		t.Fatal("n after ? should look forward")
	}
	// An on-screen match moves only the cursor.
	if m.offset != 0 || m.curY != 10 || m.curX != 3 {
		t.Fatalf("on-screen match: offset %d cursor %d,%d", m.offset, m.curX, m.curY)
	}
}

func TestScrollSearchResults(t *testing.T) {
	m, _ := searchFixture(t, nil)
	cols, rows := m.paneArea()
	show := func(msg searchResultMsg) {
		msg.machine, msg.pane = localMachine, "p2"
		next, _ := m.Update(msg)
		*m = next.(Model)
	}
	show(searchResultMsg{query: "gone"})
	if !m.flashIsErr || !strings.Contains(m.flash, `"gone" not found`) {
		t.Fatalf("not found: %q", m.flash)
	}
	show(searchResultMsg{err: errString("connection lost")})
	if !m.flashIsErr || !strings.Contains(m.flash, "connection lost") {
		t.Fatalf("error: %q", m.flash)
	}
	show(searchResultMsg{back: true, res: proto.PaneSearchResult{Found: true, Line: 510, History: 500, Wrapped: true}})
	if m.flashIsErr || !strings.Contains(m.flash, "from the bottom") {
		t.Fatalf("wrapped back: %q", m.flash)
	}
	show(searchResultMsg{res: proto.PaneSearchResult{Found: true, Line: 0, History: 500, Wrapped: true}})
	if !strings.Contains(m.flash, "from the oldest") || m.offset != 500 || m.curY != 0 {
		t.Fatalf("wrapped forward to the first line: %q offset %d y %d", m.flash, m.offset, m.curY)
	}
	// Past the edges it clamps: a column beyond the pane, the last line.
	show(searchResultMsg{res: proto.PaneSearchResult{Found: true, Line: 500 + rows - 1, Col: cols + 50, History: 500}})
	if m.offset != 0 || m.curY != rows-1 || m.curX != cols-1 {
		t.Fatalf("last line: offset %d cursor %d,%d", m.offset, m.curX, m.curY)
	}
	// History that grew since the frame still scrolls the whole way.
	show(searchResultMsg{res: proto.PaneSearchResult{Found: true, Line: 0, History: 800}})
	if m.offset != 800 || m.curY != 0 {
		t.Fatalf("grown history: offset %d y %d", m.offset, m.curY)
	}
	// A keyboard selection's head follows to the match.
	m.sel = &selection{paneID: "p2", keyboard: true, hasContent: true}
	show(searchResultMsg{res: proto.PaneSearchResult{Found: true, Line: 800 - 800 + 2, Col: 4, History: 800}})
	if m.sel.bx != 4 || m.sel.by != m.curY {
		t.Fatalf("selection head %d,%d cursor %d,%d", m.sel.bx, m.sel.by, m.curX, m.curY)
	}

	// Answers that come too late are dropped: another pane, or scroll mode over.
	x, y := m.curX, m.curY
	next, _ := m.Update(searchResultMsg{machine: localMachine, pane: "p3", res: proto.PaneSearchResult{Found: true, Line: 400, History: 500}})
	*m = next.(Model)
	m.scrollMode = false
	show(searchResultMsg{res: proto.PaneSearchResult{Found: true, Line: 400, History: 500}})
	if m.curX != x || m.curY != y {
		t.Fatal("a stale answer moved the cursor")
	}
}

func TestScrollSearchOldServer(t *testing.T) {
	m, _ := searchFixture(t, nil, "pane.v1") // a server without pane.search
	m.search.query = "x"
	if cmd := a1Key(t, m, runes("n")); cmd != nil {
		t.Fatal("searched a server that can't")
	}
	if !m.flashIsErr || !strings.Contains(m.flash, "too old") {
		t.Fatalf("flash %q", m.flash)
	}
}

func TestScrollSearchView(t *testing.T) {
	was := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor) // so a highlight is more than its text
	t.Cleanup(func() { lipgloss.SetColorProfile(was) })
	m, _ := searchFixture(t, nil)
	cols, rows := m.paneArea()
	lines := make([]string, rows)
	lines[0] = "\x1b[32mbuild error\x1b[0m and Error and ERROR"
	lines[1] = "日本 error"
	m.frame.Lines = lines
	m.search.query = "error"
	l := m.tab().root.leaves()[0]
	out := m.leafLines(l, cols, rows, true)
	if len(out) != rows {
		t.Fatalf("%d lines", len(out))
	}
	for i, o := range out {
		if ansi.StringWidth(o) > cols {
			t.Fatalf("line %d is %d wide in %d: %q", i, ansi.StringWidth(o), cols, o)
		}
	}
	if !strings.Contains(out[0], styleSearchMatch.Render("error")) || !strings.Contains(out[0], styleSearchMatch.Render("ERROR")) {
		t.Fatalf("matches not highlighted: %q", out[0])
	}
	if strings.TrimRight(ansi.Strip(out[0]), " ") != ansi.Strip(lines[0]) {
		t.Fatalf("text changed: %q", ansi.Strip(out[0]))
	}

	// Typing: the prompt takes the bottom line and the matches wait.
	m.search.typing, m.search.back, m.search.input = true, true, "err"
	out = m.leafLines(l, cols, rows, true)
	if got := strings.TrimRight(ansi.Strip(out[rows-1]), " "); got != "/err█" {
		t.Fatalf("prompt %q", got)
	}
	if strings.Contains(out[0], styleSearchMatch.Render("error")) {
		t.Fatal("matches drawn while typing")
	}
	// A query longer than the pane shows its end.
	m.search.input = strings.Repeat("a", cols*2) + "END"
	out = m.leafLines(l, cols, rows, true)
	if last := ansi.Strip(out[rows-1]); ansi.StringWidth(last) > cols || !strings.Contains(last, "END█") {
		t.Fatalf("long prompt %q (%d wide in %d)", last, ansi.StringWidth(last), cols)
	}
}

func TestMatchSpans(t *testing.T) {
	cases := []struct {
		plain, q string
		want     [][2]int
	}{
		{"error Error ERROR", "error", [][2]int{{0, 5}, {6, 11}, {12, 17}}},
		{"error Error ERROR", "Error", [][2]int{{6, 11}}},
		{"日本 error", "error", [][2]int{{5, 10}}},
		{"日本", "本", [][2]int{{2, 4}}},
		{"aaaa", "aa", [][2]int{{0, 2}, {2, 4}}}, // no overlaps
		{"abc", "", nil},
		{"", "a", nil},
		{"ab", "abc", nil},
	}
	for _, c := range cases {
		got := matchSpans(c.plain, c.q)
		if len(got) != len(c.want) {
			t.Fatalf("%q in %q: %v, want %v", c.q, c.plain, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("%q in %q: %v, want %v", c.q, c.plain, got, c.want)
			}
		}
	}
}

func TestHighlightSpansStaysInWidth(t *testing.T) {
	for _, w := range []int{1, 3, 10, 40} {
		line := "\x1b[1mhello\x1b[0m world hello"
		out := highlightSpans(line, [][2]int{{0, 5}, {12, 17}, {30, 40}}, w)
		if ansi.StringWidth(out) > w {
			t.Fatalf("w=%d: %d wide: %q", w, ansi.StringWidth(out), out)
		}
	}
	if got := highlightSpans("abc", nil, 3); got != "abc" {
		t.Fatalf("no spans changed the line: %q", got)
	}
}

func TestScrollSearchStatusHints(t *testing.T) {
	m, _ := searchFixture(t, nil)
	text := func() string {
		chip, items := m.statusHints()
		var b strings.Builder
		b.WriteString(ansi.Strip(chip))
		for _, it := range items {
			b.WriteString(" " + ansi.Strip(it.text))
		}
		return b.String()
	}
	if s := text(); !strings.Contains(s, "SCROLL") || !strings.Contains(s, "search up") || strings.Contains(s, "next") {
		t.Fatalf("scroll hints: %s", s)
	}
	m.search.query = "x"
	if s := text(); !strings.Contains(s, "next") || !strings.Contains(s, "previous") {
		t.Fatalf("with a query: %s", s)
	}
	m.search.typing = true
	if s := text(); !strings.Contains(s, "SEARCH") || !strings.Contains(s, "find") {
		t.Fatalf("typing: %s", s)
	}
}
