package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

func lc(h string, parents ...string) proto.LogCommit {
	return proto.LogCommit{Hash: h, Parents: parents, Subject: "subject of " + h}
}

func TestCommitGraphStraightLine(t *testing.T) {
	rows := commitGraph([]proto.LogCommit{lc("c", "b"), lc("b", "a"), lc("a")})
	for i, r := range rows {
		if r.lane != 0 || r.width != 1 {
			t.Errorf("row %d: lane %d width %d", i, r.lane, r.width)
		}
	}
	if len(rows[0].into) != 0 || len(rows[2].outOf) != 0 || len(rows[1].into) != 1 || len(rows[1].outOf) != 1 {
		t.Errorf("rows %+v", rows)
	}
	if commitGraph(nil) == nil || len(commitGraph(nil)) != 0 {
		t.Error("no commits: no rows")
	}
}

// A branch merged back: the merge opens a lane for the branch, the branch
// runs beside the base, and both lanes join at the commit they share.
func TestCommitGraphMerge(t *testing.T) {
	rows := commitGraph([]proto.LogCommit{lc("m", "b", "f2"), lc("f2", "f1"), lc("b", "a"), lc("f1", "a"), lc("a")})
	m, f2, b, f1, a := rows[0], rows[1], rows[2], rows[3], rows[4]
	if !m.merge() || m.lane != 0 || !eqInts(m.outOf, 0, 1) {
		t.Errorf("merge %+v", m)
	}
	if f2.lane != 1 || !eqInts(f2.into, 1) || !eqInts(f2.through, 0) || b.lane != 0 || !eqInts(b.through, 1) || f1.lane != 1 {
		t.Errorf("branch rows %+v %+v %+v", f2, b, f1)
	}
	if a.lane != 0 || !eqInts(a.into, 0, 1) || len(a.outOf) != 0 {
		t.Errorf("join %+v", a)
	}
	if f2.colors[1] != f1.colors[1] || f2.colors[1] == m.colors[0] {
		t.Errorf("a branch keeps its own colour: %v %v %v", f2.colors, f1.colors, m.colors)
	}
	// A merge into a lane already waiting for the parent points there.
	rows = commitGraph([]proto.LogCommit{lc("t", "p"), lc("m", "q", "p"), lc("q", "p"), lc("p")})
	if rows[1].lane != 1 || !eqInts(rows[1].outOf, 1, 0) {
		t.Errorf("merge into a waiting lane %+v", rows[1])
	}
	// A parent beyond the log runs off; another root takes a lane of its own.
	rows = commitGraph([]proto.LogCommit{lc("b", "a"), lc("z")})
	if !eqInts(rows[0].outOf, 0) || rows[1].lane != 1 || !eqInts(rows[1].through, 0) {
		t.Errorf("cut short %+v", rows)
	}
}

func eqInts(got []int, want ...int) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// The graph is drawn in lanes*2 cells whatever the row holds, with the
// glyphs git's graph would put there.
func TestGraphCells(t *testing.T) {
	rows := commitGraph([]proto.LogCommit{
		{Hash: "m", Parents: []string{"b", "f"}, Refs: []string{"HEAD -> main"}},
		lc("f", "a"), lc("b", "a"), lc("a")})
	want := []string{"◉─╮ ", "│ ● ", "● │ ", "●─╯ "}
	for i, r := range rows {
		got := ansi.Strip(graphCells(r, 2))
		if got != want[i] {
			t.Errorf("row %d: %q, want %q", i, got, want[i])
		}
		if ansi.StringWidth(graphCells(r, 2)) != 4 {
			t.Errorf("row %d is not 4 cells", i)
		}
	}
	// A lane crossed by a run is drawn crossed, not cut.
	rows = commitGraph([]proto.LogCommit{lc("x", "a"), lc("y", "b"), lc("z", "a"), lc("a"), lc("b")})
	if got := ansi.Strip(graphCells(rows[3], 3)); got != "●─┼─╯ " {
		t.Errorf("crossing: %q", got)
	}
	// A run that goes on past a lane meets it in a junction: an octopus
	// merge opens two lanes, two lanes joining end in one.
	rows = commitGraph([]proto.LogCommit{lc("o", "a", "b", "c"), lc("a"), lc("b"), lc("c")})
	if got := ansi.Strip(graphCells(rows[0], 3)); got != "●─┬─╮ " {
		t.Errorf("octopus: %q", got)
	}
	rows = commitGraph([]proto.LogCommit{lc("x", "a"), lc("y", "a"), lc("z", "a"), lc("a")})
	if got := ansi.Strip(graphCells(rows[3], 3)); got != "●─┴─╯ " {
		t.Errorf("three joining: %q", got)
	}
	if graphCells(rows[0], 0) != "" {
		t.Error("no lanes: nothing drawn")
	}
	// Clipped to the lanes asked for.
	if w := ansi.StringWidth(graphCells(rows[2], 1)); w != 2 {
		t.Errorf("clipped graph is %d cells", w)
	}
}

func TestRefs(t *testing.T) {
	refs := []string{"HEAD -> main", "origin/main", "origin/HEAD", "tag: v1", "conch/feat"}
	if got := ansi.Strip(refsText(refs)); got != "(HEAD → main, origin/main, tag: v1, conch/feat) " {
		t.Errorf("refs %q", got)
	}
	if got := localBranches(refs); !eqStrings(got, "main", "conch/feat") {
		t.Errorf("local branches %q", got)
	}
	if refsText(nil) != "" || len(localBranches([]string{"HEAD", "origin/x", "tag: t"})) != 0 {
		t.Error("no refs: nothing")
	}
	if headOf([]string{"HEAD"}) != "HEAD" || headOf(nil) != "" {
		t.Error("detached HEAD")
	}
}

func eqStrings(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func commitsFixture() []proto.LogCommit {
	now := time.Now()
	cs := []proto.LogCommit{
		{Hash: "aaaaaaa1111", Parents: []string{"bbbbbbb2222", "fffffff9999"}, Subject: "Merge pull request #12 from conch/feat",
			Author: "Amit", Time: now.Add(-time.Hour), Refs: []string{"HEAD -> main", "origin/main"}},
		{Hash: "fffffff9999", Parents: []string{"ccccccc3333"}, Subject: "Feature work", Author: "Amit", Time: now.Add(-2 * time.Hour), Refs: []string{"feat"}},
		{Hash: "bbbbbbb2222", Parents: []string{"ccccccc3333"}, Subject: "Fix on main", Author: "Amit", Time: now.Add(-3 * time.Hour)},
		{Hash: "ccccccc3333", Subject: "Initial commit with a subject long enough to need cutting at a narrow width",
			Author: "Amit", Time: now.Add(-48 * time.Hour), Refs: []string{"tag: v0.1"}},
	}
	return cs
}

// The window never draws past the screen, at any size, picked row or not.
func TestCommitsRenderFits(t *testing.T) {
	m := a2Model()
	v := &commitsView{mid: localMachine, pid: "r1", limit: commitsPage}
	v.receive(commitsMsg{v: v, res: proto.LogResult{Commits: commitsFixture(), More: true}})
	for _, sz := range [][2]int{{1, 1}, {20, 5}, {30, 10}, {39, 12}, {80, 24}, {120, 40}, {250, 70}} {
		m.width, m.height = sz[0], sz[1]
		for _, sel := range []int{0, 3} {
			v.sel = sel
			b := v.render(*m)
			for i, l := range b.lines {
				if w := ansi.StringWidth(l); b.x+w > max(m.width, 4) {
					t.Errorf("%dx%d line %d is %d wide at x %d: %q", sz[0], sz[1], i, w, b.x, ansi.Strip(l))
				}
			}
			if sz[1] >= 5 && len(b.lines) > sz[1] {
				t.Errorf("%dx%d: %d lines", sz[0], sz[1], len(b.lines))
			}
		}
	}
	m.width, m.height = 120, 40
	text := ansi.Strip(strings.Join(v.render(*m).lines, "\n"))
	for _, want := range []string{"Commits · api", "aaaaaaa", "(HEAD → main, origin/main)", "Merge pull request #12", "tag: v0.1", "newest 4", "Amit · 1h"} {
		if !strings.Contains(text, want) {
			t.Errorf("render lacks %q:\n%s", want, text)
		}
	}
}

func TestCommitsKeys(t *testing.T) {
	m := a2Model()
	v := &commitsView{mid: localMachine, pid: "r1", limit: commitsPage, focus: "feat"}
	m.overlay = v
	// A history arriving for a window that has since closed is dropped.
	if _, cmd := v.update(m, commitsMsg{v: &commitsView{}}); cmd != nil || len(v.rows) != 0 {
		t.Fatal("a stale history was taken in")
	}
	v.update(m, commitsMsg{v: v, res: proto.LogResult{Commits: commitsFixture()}})
	if v.sel != 1 || v.focus != "" {
		t.Fatalf("opened at a branch: sel %d focus %q", v.sel, v.focus)
	}
	press := func(k string) tea.Cmd {
		var msg tea.KeyMsg
		switch k {
		case "down", "up", "end", "home", "esc", "enter", "pgdown":
			msg = tea.KeyMsg{Type: map[string]tea.KeyType{"down": tea.KeyDown, "up": tea.KeyUp, "end": tea.KeyEnd,
				"home": tea.KeyHome, "esc": tea.KeyEsc, "enter": tea.KeyEnter, "pgdown": tea.KeyPgDown}[k]}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		_, cmd := v.update(m, msg)
		return cmd
	}
	press("down")
	press("j")
	if v.sel != 3 {
		t.Errorf("down twice: %d", v.sel)
	}
	press("down") // the last: stays
	press("home")
	if v.sel != 0 {
		t.Errorf("home: %d", v.sel)
	}
	press("end")
	press("k")
	if v.sel != 2 {
		t.Errorf("end, up: %d", v.sel)
	}
	// y copies the hash picked.
	if msgs := a2Run(press("y")); len(msgs) != 1 || !strings.Contains(fmtMsg(msgs[0]), "copied 11 characters") {
		t.Errorf("copy: %v", msgs)
	}
	// enter on a commit no branch points at says so and stays.
	press("enter")
	if m.overlay != v || !strings.Contains(m.flash, "no branch") {
		t.Errorf("enter on a bare commit: overlay %v flash %q", m.overlay, m.flash)
	}
	// enter on the feature branch's commit opens its changes and closes.
	v.sel = 1
	press("enter")
	if m.overlay != nil || m.cursor != branchNodeID(localMachine, "r1", "feat") {
		t.Errorf("enter on feat: overlay %v cursor %q", m.overlay, m.cursor)
	}
	m.overlay = v
	press("esc")
	if m.overlay != nil {
		t.Error("esc leaves it open")
	}
}

func fmtMsg(msg tea.Msg) string {
	if f, ok := msg.(flashMsg); ok {
		return string(f)
	}
	return ""
}

// Scrolling to the last commit read asks for older ones while there are
// more, up to the server's most; not past it, and not twice at once.
func TestCommitsReadMoreAtTheEnd(t *testing.T) {
	m := a2Model()
	m.machines[0].c = a2Client(proto.CapProjectLog)
	v := &commitsView{mid: localMachine, pid: "r1", limit: commitsPage}
	v.receive(commitsMsg{v: v, res: proto.LogResult{Commits: commitsFixture(), More: true}})
	if cmd := v.move(m, 1); cmd != nil || v.limit != commitsPage {
		t.Fatal("asked for more before the end")
	}
	if cmd := v.move(m, 10); cmd == nil || v.limit != 2*commitsPage || !v.loading {
		t.Fatalf("at the end: limit %d loading %v", v.limit, v.loading)
	}
	if cmd := v.move(m, 1); cmd != nil {
		t.Error("asked again while reading")
	}
	v.loading, v.limit = false, commitsMaxRead
	if cmd := v.move(m, 1); cmd != nil {
		t.Error("asked past the server's most")
	}
	// The pick survives a read that brings more.
	v.sel = 2
	v.receive(commitsMsg{v: v, res: proto.LogResult{Commits: commitsFixture()}})
	if v.sel != 2 || v.more {
		t.Errorf("after reading again: sel %d more %v", v.sel, v.more)
	}
	// A failed read says why and keeps what was shown.
	v.receive(commitsMsg{v: v, err: errTest("boom")})
	if v.err != "boom" || len(v.rows) != 4 || !strings.Contains(ansi.Strip(strings.Join(v.render(*m).lines, "")), "error: boom") {
		t.Errorf("failed read: %q rows %d", v.err, len(v.rows))
	}
}

type errTest string

func (e errTest) Error() string { return string(e) }

func TestCommitsMouse(t *testing.T) {
	m := a2Model()
	v := &commitsView{mid: localMachine, pid: "r1", limit: commitsPage}
	v.receive(commitsMsg{v: v, res: proto.LogResult{Commits: commitsFixture()}})
	m.overlay = v
	b := v.render(*m)
	v.mouse(m, tea.MouseMsg{X: b.x + 5, Y: b.y + 3, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}, b)
	if v.sel != 2 {
		t.Errorf("click on the third row: %d", v.sel)
	}
	v.mouse(m, tea.MouseMsg{X: b.x + 5, Y: b.y + 3, Action: tea.MouseActionPress, Button: tea.MouseButtonWheelUp}, b)
	if v.sel != 0 {
		t.Errorf("wheel up: %d", v.sel)
	}
	v.mouse(m, tea.MouseMsg{X: 0, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}, box{lines: b.lines, x: 5, y: 5})
	if m.overlay != nil {
		t.Error("a click outside leaves it open")
	}
}

// It opens only on a git project whose server can read the history, and
// says why not otherwise; both row menus offer it.
func TestOpenCommits(t *testing.T) {
	m := a2Model()
	m.machines[0].c = a2Client()
	if cmd := m.openCommits(localMachine, "r1", ""); cmd != nil || m.overlay != nil || !strings.Contains(m.flash, "reload") {
		t.Errorf("old server: overlay %v flash %q", m.overlay, m.flash)
	}
	m.machines[0].c = a2Client(proto.CapProjectLog)
	m.machines[0].projects[0].Git = false
	if cmd := m.openCommits(localMachine, "r1", ""); cmd != nil || m.overlay != nil || !strings.Contains(m.flash, "git project") {
		t.Errorf("not git: flash %q", m.flash)
	}
	if m.openCommits(localMachine, "nope", ""); m.overlay != nil {
		t.Error("unknown project opened")
	}
	m.machines[0].projects[0].Git = true
	if cmd := m.openCommits(localMachine, "r1", "feat"); cmd == nil {
		t.Fatal("no read asked for")
	}
	if v, ok := m.overlay.(*commitsView); !ok || v.focus != "feat" || !v.loading {
		t.Errorf("opened %#v", m.overlay)
	}
	m.overlay = nil
	for _, r := range []row{{kind: kindProject, machine: localMachine, projectID: "r1"},
		{kind: kindBranch, machine: localMachine, projectID: "r1", branch: "feat"}} {
		id := r.kind
		found := false
		for _, it := range newRowMenu(*m, r, 0, 0).items {
			if it.key == "G" && strings.HasPrefix(it.label, "Commit graph") {
				found = true
				it.run(m)
			}
		}
		if _, ok := m.overlay.(*commitsView); !found || !ok {
			t.Errorf("%v: menu item %v, opened %T", id, found, m.overlay)
		}
		m.overlay = nil
	}
}
