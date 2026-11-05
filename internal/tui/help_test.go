package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/keymap"
)

// What the help is for: a key list read by somebody looking for one key.
// It used to be hand-wrapped lines with three keys on some of them, so
// these say what the layout owes them — a group each, a line each, the
// details under the key they belong to, and a blank line between.
func TestHelpGroupsEveryKey(t *testing.T) {
	if len(helpGroups) < 5 {
		t.Fatalf("%d groups", len(helpGroups))
	}
	seen := map[string]bool{}
	for _, g := range helpGroups {
		if g.title == "" || len(g.entries) == 0 {
			t.Errorf("group %q has %d entries", g.title, len(g.entries))
		}
		if seen[g.title] {
			t.Errorf("two groups called %q", g.title)
		}
		seen[g.title] = true
		for _, e := range g.entries {
			switch {
			case e.what == "":
				t.Errorf("%s: %q says nothing", g.title, e.keys)
			case strings.TrimSpace(e.what) != e.what:
				t.Errorf("%s: %q is padded: %q", g.title, e.keys, e.what)
			}
			// A run of spaces is how the old help put a second key on a
			// line; here a key is a key and a column is the layout's.
			for _, l := range append([]string{e.keys, e.what}, e.more...) {
				if strings.Contains(l, "   ") {
					t.Errorf("%s: %q has a column in it: %q", g.title, e.keys, l)
				}
			}
		}
	}
}

// The layout: a heading and a rule where tops says, a blank line between
// entries, details under the description they belong to, and nothing wider
// than the box on any screen worth wrapping for.
func TestHelpLayoutIsSpacedAndFits(t *testing.T) {
	for _, w := range []int{40, 56, 72, 100} {
		lines, tops, _, _ := helpLayoutAt(w)
		if len(tops) != len(helpGroups) {
			t.Fatalf("%d: %d tops for %d groups", w, len(tops), len(helpGroups))
		}
		for i, top := range tops {
			title := ansi.Strip(lines[top])
			want := strings.ReplaceAll(helpGroups[i].title, "{prefix}", "ctrl+b")
			if title != want {
				t.Errorf("%d: group %d starts %q, want %q", w, i, title, want)
			}
			if rule := strings.TrimSpace(ansi.Strip(lines[top+1])); strings.Trim(rule, "─") != "" || rule == "" {
				t.Errorf("%d: no rule under %q: %q", w, title, rule)
			}
			if top > 0 && (ansi.Strip(lines[top-1]) != "" || ansi.Strip(lines[top-2]) != "") {
				t.Errorf("%d: %q has no blank line above it", w, title)
			}
			if ansi.Strip(lines[top+2]) != "" {
				t.Errorf("%d: %q runs straight into its first key", w, title)
			}
		}
		for i, l := range lines {
			if n := ansi.StringWidth(l); n > w {
				t.Errorf("%d: line %d is %d wide: %q", w, i, n, ansi.Strip(l))
			}
		}
		// A blank line before every entry: the space between options is
		// the point of the layout, so it is counted, not eyeballed.
		for i, top := range tops {
			end := len(lines)
			if i+1 < len(tops) {
				end = tops[i+1] - 2
			}
			blank := 0
			for _, l := range lines[top:end] {
				if ansi.Strip(l) == "" {
					blank++
				}
			}
			if want := len(helpGroups[i].entries); blank != want {
				t.Errorf("%d: %q has %d blank lines for %d entries", w, helpGroups[i].title, blank, want)
			}
		}
	}
}

// helpLayoutAt is the layout for an inner width of w, as a screen that
// wide would give it, with the keys conch ships with.
func helpLayoutAt(w int) ([]string, []int, int, int) {
	m := Model{width: w + 4, height: 40}
	m.cfg.Keys.Prefix, m.binds = "ctrl+b", keymap.Default()
	return help{}.layout(m)
}

// helpAtWidth is the same, as the argument the layout functions take.
func helpAtWidth(w int) helpAt {
	return helpAt{w: w, prefix: "ctrl+b", binds: keymap.Default()}
}

// A key's details sit under its description, not under the key, so the
// key column stays a column the eye can run down — and a key too wide for
// the column takes a line of its own rather than widening it.
func TestHelpEntryLines(t *testing.T) {
	e := helpEntry{keys: "P", what: "pair a phone", more: []string{"a QR code", "and a link"}}
	lines := e.lines(helpAtWidth(60), 6, helpGutter)
	want := []string{
		"  P         pair a phone",
		"            a QR code",
		"            and a link",
	}
	for i, l := range lines {
		if got := ansi.Strip(l); got != want[i] {
			t.Errorf("line %d: %q, want %q", i, got, want[i])
		}
	}
	if len(lines) != len(want) {
		t.Fatalf("%d lines: %q", len(lines), lines)
	}

	// A description longer than the room wraps to where it started.
	e = helpEntry{keys: "y", what: "copy a name or a path, a branch's name or a checkout's directory"}
	for i, l := range e.lines(helpAtWidth(40), 4, helpTight) {
		got := ansi.Strip(l)
		if ansi.StringWidth(got) > 40 {
			t.Errorf("line %d is %d wide: %q", i, ansi.StringWidth(got), got)
		}
		if i > 0 && !strings.HasPrefix(got, "        ") {
			t.Errorf("wrapped line %d is not under the description: %q", i, got)
		}
	}

	// A key wider than the column keeps its own line, and the
	// description goes under it.
	lines = helpEntry{keys: "ctrl/alt+arrows", what: "resize"}.lines(helpAtWidth(60), 6, helpGutter)
	if len(lines) != 2 || ansi.Strip(lines[0]) != "  ctrl/alt+arrows" || ansi.Strip(lines[1]) != "            resize" {
		t.Fatalf("a long key: %q", lines)
	}

	// A note has no key and reads from the description column.
	lines = helpEntry{what: "the tab bar lists the tabs of the selection"}.lines(helpAtWidth(60), 6, helpGutter)
	if len(lines) != 1 || ansi.Strip(lines[0]) != "            the tab bar lists the tabs of the selection" {
		t.Fatalf("a note: %q", lines)
	}
}

// Every description in the help starts in the same column, whatever group
// it is in, with a gutter wide enough to read as a second column: a key
// column that moved from group to group is what still read as cramped.
func TestHelpIsTwoColumns(t *testing.T) {
	for _, w := range []int{64, 80, 100} {
		lines, tops, box, _ := helpLayoutAt(w)
		col, gut := helpColumn(helpAtWidth(box))
		if gut < helpGutter {
			t.Errorf("%d: a gutter of %d cells", w, gut)
		}
		at := 2 + col + gut // the cell every description starts at
		for i, top := range tops {
			end := len(lines)
			if i+1 < len(tops) {
				end = tops[i+1] - 2
			}
			for _, raw := range lines[top+2 : end] {
				l := ansi.Strip(raw)
				if strings.TrimSpace(l) == "" {
					continue
				}
				// Cells, not bytes: a key can be ⚑ or 🌐.
				body := strings.TrimLeft(l, " ")
				indent := ansi.StringWidth(l) - ansi.StringWidth(body)
				switch {
				case indent == at:
					// A description that wrapped, a detail, or a note.
				case indent != 2:
					t.Errorf("%d: %q: a line indented %d, not 2 or %d: %q", w, helpGroups[i].title, indent, at, l)
				case !strings.Contains(body, "  "):
					// A key too wide for the column, alone on its line.
				case !strings.HasPrefix(ansi.TruncateLeft(l, at-1, ""), " "),
					strings.HasPrefix(ansi.TruncateLeft(l, at, ""), " "):
					t.Errorf("%d: %q: the description does not start at cell %d: %q", w, helpGroups[i].title, at, l)
				}
			}
		}
	}
}

// ] and [ move by group, which is how a list this long is got through.
func TestHelpMovesByGroup(t *testing.T) {
	m := &Model{width: 104, height: 24}
	h := newHelp()
	m.overlay = h
	_, tops, _, _ := h.layout(*m)
	shown := func() string { return a2Plain(h.render(*m).lines) }

	if closed, _ := h.update(m, a2Key("[")); closed || h.top != 0 {
		t.Fatalf("[ at the top: %d", h.top)
	}
	all, _, _, rows := h.layout(*m)
	// The last groups are shorter than a screen, so their heading stops
	// where the text ends rather than at the top.
	at := func(i int) int { return min(tops[i], max(len(all)-rows, 0)) }
	for i := 1; i < len(tops); i++ {
		if closed, _ := h.update(m, a2Key("]")); closed || m.overlay == nil {
			t.Fatalf("] closed the help at group %d", i)
		}
		if h.top != at(i) {
			t.Fatalf("] %d times: top %d, want %d", i, h.top, at(i))
		}
		if want := helpGroups[i].title; !strings.Contains(shown(), want) {
			t.Fatalf("group %d is not on screen:\n%s", i, shown())
		}
	}
	h.update(m, a2Key("]"))
	if h.top != at(len(tops)-1) {
		t.Fatalf("] past the last group: %d", h.top)
	}
	h.top = tops[len(tops)-2]
	for i := len(tops) - 3; i >= 0; i-- {
		h.update(m, a2Key("["))
		if h.top != tops[i] {
			t.Fatalf("[ back to group %d: top %d, want %d", i, h.top, tops[i])
		}
	}
	// tab and shift+tab do the same, and neither closes the help.
	h.update(m, a2Key("tab"))
	if h.top != tops[1] || m.overlay == nil {
		t.Fatalf("tab: %d", h.top)
	}
	h.update(m, a2Key("shift+tab"))
	if h.top != tops[0] || m.overlay == nil {
		t.Fatalf("shift+tab: %d", h.top)
	}
	// From the middle of a group, [ goes to the top of that group first.
	h.scroll(*m, tops[2]+3, 0)
	h.update(m, a2Key("["))
	if h.top != tops[2] {
		t.Fatalf("[ from inside group 2: %d, want %d", h.top, tops[2])
	}
}

// The help follows the width it is given: it wraps rather than running off
// the edge, so a narrow screen shows more lines, not less text.
func TestHelpWrapsToTheScreen(t *testing.T) {
	wide, _, _, _ := helpLayoutAt(100)
	narrow, _, _, _ := helpLayoutAt(44)
	if len(narrow) <= len(wide) {
		t.Fatalf("narrow %d lines, wide %d", len(narrow), len(wide))
	}
	words := func(lines []string) int {
		n := 0
		for _, l := range lines {
			n += len(strings.Fields(ansi.Strip(l)))
		}
		return n
	}
	if words(narrow) != words(wide) {
		t.Errorf("%d words narrow, %d wide: text was lost, not wrapped", words(narrow), words(wide))
	}
	// However much room there is, the text stops at helpWide.
	huge, _, w, _ := helpLayoutAt(400)
	if w > helpWide {
		t.Errorf("a 400-column screen lays the help out %d wide", w)
	}
	for _, l := range huge {
		if n := ansi.StringWidth(l); n > helpWide {
			t.Errorf("a line %d wide: %q", n, ansi.Strip(l))
		}
	}
}

// The frame names the group being read, since a heading scrolled off the
// top is no longer there to say.
func TestHelpFrameNamesTheGroup(t *testing.T) {
	m := &Model{width: 120, height: 24}
	h := newHelp()
	m.overlay = h
	_, tops, _, _ := h.layout(*m)
	all, _, _, rows := h.layout(*m)
	end := max(len(all)-rows, 0) // the last groups cannot reach the top
	for i, g := range helpGroups {
		// The frame names the group at the top of the view, and three
		// lines in — where the heading itself has scrolled off — it is
		// still the one that says which group this is.
		for _, at := range []int{tops[i], tops[i] + 3} {
			h.top = at
			want := g.title
			if at > end {
				want = helpGroupAt(tops, end)
			}
			if top := a2Plain(h.render(*m).lines); !strings.Contains(top, "Keys · "+want+" · ") {
				t.Fatalf("group %d (%s) at %d: the frame says\n%s", i, g.title, at, strings.SplitN(top, "\n", 2)[0])
			}
		}
	}
	if got := helpGroupAt(nil, 0); got != helpGroups[0].title {
		t.Errorf("no groups at all: %q", got)
	}
}

// helpSays is what the other tests ask the help with, so it has to find
// what is there and not what isn't.
func TestHelpSays(t *testing.T) {
	if !helpSays("Tree") || !helpSays("review queue") || !helpSays("detach (agents keep running)") {
		t.Error("helpSays cannot find the help's own text")
	}
	if helpSays("conch does not say this anywhere") {
		t.Error("helpSays found text that is not there")
	}
}

// A rebound key is shown wherever conch names a key: the help's own key
// column, its group titles, the status bar and the menus. The help was the
// one place somebody would look to find out why x did nothing, so it is
// the one place that must not still say x.
func TestHelpShowsReboundKeys(t *testing.T) {
	m := Model{width: 110, height: 40}
	m.cfg.Keys.Prefix = "ctrl+a"
	var problems []string
	m.binds, problems = keymap.Resolve(map[string]string{
		"split_right": `\`,
		"zoom":        "f",
		"close_split": "",
	})
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	h := newHelp()
	lines, _, _, _ := h.layout(m)
	all := ansi.Strip(strings.Join(lines, "\n"))
	for _, want := range []string{
		"Splits and tabs (ctrl+a, then)", // the prefix, not ctrl+b
		"Workspaces (ctrl+a, then)",
		`\ `,       // split_right, where it is now
		"ctrl+a f", // zoom, from inside a pane
		"— ",       // close_split, unbound
		"ctrl+a : in a pane",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("the help does not say %q", want)
		}
	}
	for _, gone := range []string{"ctrl+b", "% or v"} {
		if strings.Contains(all, gone) {
			t.Errorf("the help still says %q", gone)
		}
	}
	// The key column is wide enough for the keys it now shows.
	col, gut := helpColumn(helpAt{w: 100, prefix: m.cfg.Keys.Prefix, binds: m.binds})
	if col < len("ctrl+a f") || gut != helpGutter {
		t.Errorf("column %d, gutter %d", col, gut)
	}
}

// What conch says about a key elsewhere comes from the same table: a hint
// for an unbound action is left out rather than offered.
func TestKeyHintFollowsTheBinding(t *testing.T) {
	m := Model{}
	m.cfg.Keys.Prefix = "ctrl+a"
	if got := m.keyHint(keymap.CloseSplit); got != "ctrl+a x" {
		t.Errorf("default: %q", got)
	}
	if got := m.keyNote(keymap.Redraw); got != " (ctrl+a r)" {
		t.Errorf("note: %q", got)
	}
	m.binds, _ = keymap.Resolve(map[string]string{"close_split": "C-x", "redraw": ""})
	if got := m.keyHint(keymap.CloseSplit); got != "ctrl+a ctrl+x" {
		t.Errorf("rebound: %q", got)
	}
	if got := m.keyHint(keymap.Redraw); got != "" {
		t.Errorf("unbound still offers %q", got)
	}
	if got := m.keyNote(keymap.Redraw); got != "" {
		t.Errorf("unbound note %q", got)
	}
	// The space key reads as a word, not as a blank in the middle of a hint.
	m.binds, _ = keymap.Resolve(map[string]string{"zoom": " "})
	if got := m.keyHint(keymap.Zoom); got != "ctrl+a space" {
		t.Errorf("space: %q", got)
	}
}
