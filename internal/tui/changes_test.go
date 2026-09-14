package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

func a2Changes(paths ...string) proto.Changes {
	c := proto.Changes{ProjectID: "r1", Branch: "feat", Base: "main", Worktree: "/src/api-feat"}
	for i, p := range paths {
		c.Files = append(c.Files, proto.FileChange{Path: p, Code: "M", Added: i + 1, Deleted: i})
	}
	return c
}

func TestA2ChangesReceiveKeepsSelection(t *testing.T) {
	cv := &changesView{machine: localMachine, projectID: "r1", branch: "feat", loading: true}
	if cv.receive(changesMsg{projectID: "r1", branch: "other", data: a2Changes("a")}) || cv.data != nil || !cv.loading {
		t.Fatal("a reply for another branch was applied")
	}
	if cv.receive(changesMsg{projectID: "r1", branch: "feat", data: a2Changes("a.go", "b.go", "c.go")}) {
		t.Fatal("a user's reload is not a background change")
	}
	if cv.loading || len(cv.data.Files) != 3 {
		t.Fatalf("first load: %+v", cv)
	}
	cv.sel = 1 // b.go
	// A refresh that inserts a file before it keeps b.go selected.
	if !cv.receive(changesMsg{projectID: "r1", branch: "feat", poll: true, data: a2Changes("0.go", "a.go", "b.go", "c.go")}) {
		t.Fatal("a background refresh that differs reports a change")
	}
	if cv.sel != 2 || cv.data.Files[cv.sel].Path != "b.go" {
		t.Fatalf("selection moved to %d", cv.sel)
	}
	// An identical poll changes nothing.
	if cv.receive(changesMsg{projectID: "r1", branch: "feat", poll: true, data: a2Changes("0.go", "a.go", "b.go", "c.go")}) {
		t.Fatal("an identical poll reported a change")
	}
	// When the selected file is gone the selection is clamped.
	cv.sel = 3
	cv.receive(changesMsg{projectID: "r1", branch: "feat", data: a2Changes("a.go")})
	if cv.sel != 0 {
		t.Fatalf("clamped selection %d", cv.sel)
	}
	// Poll errors keep what is shown; a user's reload shows them.
	cv.receive(changesMsg{projectID: "r1", branch: "feat", poll: true, err: errors.New("flaky")})
	if cv.err != "" || cv.data == nil {
		t.Fatalf("poll error: %q", cv.err)
	}
	cv.receive(changesMsg{projectID: "r1", branch: "feat", err: errors.New("no such branch")})
	if cv.err != "no such branch" {
		t.Fatalf("reload error: %q", cv.err)
	}
	cv.receive(changesMsg{projectID: "r1", branch: "feat", data: a2Changes()})
	if cv.err != "" || cv.sel != 0 {
		t.Fatal("a good reply clears the error")
	}

	// Diffs: only the open file's reply is applied.
	cv.diffFile = "a.go"
	cv.receive(diffMsg{projectID: "r1", branch: "feat", file: "b.go", diff: "x"})
	if cv.diff != nil {
		t.Fatal("a diff for another file was applied")
	}
	cv.receive(diffMsg{projectID: "r1", branch: "feat", file: "a.go", diff: "+one\n-two\n"})
	if strings.Join(cv.diff, "|") != "+one|-two" {
		t.Fatalf("diff lines %q", cv.diff)
	}
	cv.receive(diffMsg{projectID: "r1", branch: "feat", file: "a.go", err: errors.New("binary")})
	if cv.diffErr != "binary" {
		t.Fatalf("diff error %q", cv.diffErr)
	}
	if cv.receive(tickMsg{}) {
		t.Fatal("other messages change nothing")
	}
}

func TestA2ChangesKeys(t *testing.T) {
	m := a2Model()
	m.width = 0 // pane area 80x24
	cv := &changesView{machine: localMachine, projectID: "r1", branch: "feat"}
	// Before data arrives nothing is selectable.
	for _, k := range []string{"down", "enter", "y"} {
		if back, cmd := cv.key(m, a2Key(k)); back || cmd != nil || cv.sel != 0 {
			t.Fatalf("%s without data", k)
		}
	}
	d := a2Changes("a.go", "b.go")
	cv.data = &d
	for _, step := range []struct {
		key  string
		want int
	}{{"down", 1}, {"j", 1}, {"up", 0}, {"k", 0}} {
		cv.key(m, a2Key(step.key))
		if cv.sel != step.want {
			t.Fatalf("after %s sel %d", step.key, cv.sel)
		}
	}
	if _, cmd := cv.key(m, a2Key("y")); cmd == nil {
		t.Fatal("y copies the path")
	}
	_, cmd := cv.key(m, a2Key("R"))
	if msg := a2Run(cmd)[0].(changesMsg); msg.err == nil || msg.err.Error() != "machine is offline" || msg.poll || !cv.loading {
		t.Fatalf("reload offline: %+v", msg)
	}
	cv.loading = false
	// o opens the branch's pull request (the command opens a browser, so it isn't run).
	if _, cmd := cv.key(m, a2Key("o")); cmd == nil {
		t.Fatal("o with a pull request")
	}
	noPR := &changesView{machine: localMachine, projectID: "r1", branch: "main", data: &d}
	if _, cmd := noPR.key(m, a2Key("o")); cmd != nil {
		t.Fatal("o without a pull request")
	}

	// Enter opens the diff.
	cv.sel = 1
	_, cmd = cv.key(m, a2Key("enter"))
	if cv.diffFile != "b.go" || cv.diff != nil {
		t.Fatalf("diff open: %q", cv.diffFile)
	}
	if msg := a2Run(cmd)[0].(diffMsg); msg.file != "b.go" || msg.err == nil {
		t.Fatalf("diff offline: %+v", msg)
	}
	var lines []string
	for i := 0; i < 100; i++ {
		lines = append(lines, fmt.Sprintf("+line %d", i))
	}
	cv.diff = lines
	page := 22 // pane height 24 - 2
	for _, step := range []struct {
		key  string
		want int
	}{{"down", 1}, {"j", 2}, {"up", 1}, {"k", 0}, {"up", 0}, {"pgdown", page}, {" ", 2 * page}, {"f", 3 * page}, {"b", 2 * page}, {"pgup", page},
		{"G", 100 - page}, {"end", 100 - page}, {"g", 0}, {"G", 78}, {"home", 0}} {
		if back, _ := cv.key(m, a2Key(step.key)); back || cv.diffScroll != step.want {
			t.Fatalf("after %q diff scroll %d, want %d", step.key, cv.diffScroll, step.want)
		}
	}
	if _, cmd := cv.key(m, a2Key("y")); cmd == nil {
		t.Fatal("y copies the diff")
	}
	if back, _ := cv.key(m, a2Key("tab")); !back || cv.diffFile == "" {
		t.Fatal("tab leaves the diff open and goes back")
	}
	if cmd := cv.refreshDiff(m); cmd == nil {
		t.Fatal("an open diff refreshes")
	}
	if back, _ := cv.key(m, a2Key("esc")); back || cv.diffFile != "" {
		t.Fatal("esc closes the diff")
	}
	if cv.refreshDiff(m) != nil {
		t.Fatal("no diff to refresh")
	}
	for _, k := range []string{"esc", "q", "left", "h", "tab"} {
		if back, _ := cv.key(m, a2Key(k)); !back {
			t.Fatalf("%s goes back to the tree", k)
		}
	}
}

func TestA2ChangesPoll(t *testing.T) {
	m := a2Model()
	cv := &changesView{machine: localMachine, projectID: "r1", branch: "feat", loading: true}
	if cv.poll(m) != nil {
		t.Fatal("no poll while a reload is in flight")
	}
	cv.loading = false
	cmd := cv.poll(m)
	if cv.loading {
		t.Fatal("a poll is not a visible reload")
	}
	if msg := cmd().(changesMsg); !msg.poll || msg.branch != "feat" {
		t.Fatalf("poll message %+v", msg)
	}

	// pollChanges schedules one refresh while a changes view is on screen.
	if m.pollChanges() != nil || m.changesPolling {
		t.Fatal("nothing on screen to poll")
	}
	m2 := a2Model()
	d := a2Changes("a.go")
	cv.data = &d
	m2.tabs = []*tab{{name: "1", root: &layoutNode{leaf: &leaf{id: 1, changes: cv}}, focus: 1}}
	if m2.pollChanges() == nil || !m2.changesPolling || m2.pollChanges() != nil {
		t.Fatal("one poll at a time")
	}
	// The poll tick re-reads visible views and schedules the next one.
	next, cmd := m2.Update(changesPollMsg{})
	if cmd == nil || !next.(Model).changesPolling {
		t.Fatal("the poll tick schedules the next")
	}
	// A background change refreshes an open diff.
	cv.diffFile = "a.go"
	nm := next.(Model)
	_, cmd = nm.Update(changesMsg{projectID: "r1", branch: "feat", poll: true, data: a2Changes("a.go", "b.go")})
	found := false
	for _, msg := range a2Run(cmd) {
		if dm, ok := msg.(diffMsg); ok && dm.file == "a.go" {
			found = true
		}
	}
	if !found || len(cv.data.Files) != 2 {
		t.Fatal("a changed branch refreshes the open diff")
	}
}

func TestA2ChangesMouse(t *testing.T) {
	m := a2Model()
	m.width = 0
	cv := &changesView{machine: localMachine, projectID: "r1", branch: "main"}
	if cv.mouse(m, tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}, 3, 5) != nil {
		t.Fatal("a click before data")
	}
	d := a2Changes("a.go", "b.go", "c.go")
	cv.data = &d
	cv.mouse(m, tea.MouseMsg{Button: tea.MouseButtonWheelDown}, 0, 0)
	cv.mouse(m, tea.MouseMsg{Button: tea.MouseButtonWheelDown}, 0, 0)
	cv.mouse(m, tea.MouseMsg{Button: tea.MouseButtonWheelDown}, 0, 0)
	cv.mouse(m, tea.MouseMsg{Button: tea.MouseButtonWheelUp}, 0, 0)
	if cv.sel != 1 {
		t.Fatalf("wheel sel %d", cv.sel)
	}
	if cv.filesTop(*m) != 4 {
		t.Fatalf("files top without a PR: %d", cv.filesTop(*m))
	}
	if cv.mouse(m, tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}, 3, 1) != nil || cv.diffFile != "" {
		t.Fatal("a click above the files")
	}
	if cv.mouse(m, tea.MouseMsg{Action: tea.MouseActionMotion}, 3, 6) != nil {
		t.Fatal("motion")
	}
	cmd := cv.mouse(m, tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}, 3, 6)
	if cmd == nil || cv.sel != 2 || cv.diffFile != "c.go" {
		t.Fatalf("one click opens the diff: sel %d file %q", cv.sel, cv.diffFile)
	}
	cv.diff = make([]string, 50)
	cv.mouse(m, tea.MouseMsg{Button: tea.MouseButtonWheelDown}, 0, 0)
	cv.mouse(m, tea.MouseMsg{Button: tea.MouseButtonWheelDown}, 0, 0)
	cv.mouse(m, tea.MouseMsg{Button: tea.MouseButtonWheelUp}, 0, 0)
	if cv.diffScroll != 3 {
		t.Fatalf("diff wheel scroll %d", cv.diffScroll)
	}
	if cv.mouse(m, tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}, 3, 6) != nil {
		t.Fatal("clicks in a diff do nothing")
	}
	feat := &changesView{machine: localMachine, projectID: "r1", branch: "feat"}
	if feat.filesTop(*m) != 5 {
		t.Fatal("a PR line pushes the files down")
	}
}

func TestA2ChangesRender(t *testing.T) {
	m := a2Model()
	w, h := 80, 20
	cv := &changesView{machine: localMachine, projectID: "r1", branch: "feat"}
	out := a2Plain(cv.render(*m, w, h))
	for _, want := range []string{"feat", "o open PR", "api · 2 ahead, 1 behind main", "#7", "Add feature", "loading…"} {
		if !strings.Contains(out, want) {
			t.Fatalf("loading render lacks %q:\n%s", want, out)
		}
	}
	cv.err = "git failed"
	if out := a2Plain(cv.render(*m, w, h)); !strings.Contains(out, "git failed") {
		t.Fatalf("error render:\n%s", out)
	}
	cv.err = ""

	d := a2Changes()
	d.Worktree = ""
	for i := 0; i < 30; i++ {
		d.Files = append(d.Files, proto.FileChange{Path: fmt.Sprintf("f%02d.go", i), Code: "M", Added: 1})
	}
	d.Files[1] = proto.FileChange{Path: "img.png", Code: "A", Binary: true}
	d.Files[2] = proto.FileChange{Path: "new.go", OrigPath: "old.go", Code: "R", Deleted: 4}
	for i := 0; i < 9; i++ {
		d.Commits = append(d.Commits, proto.CommitInfo{Hash: fmt.Sprintf("abcdef123%d", i), Subject: fmt.Sprintf("commit %d", i), Time: time.Now()})
	}
	d.Commits[0].Hash = "abc"
	cv.data = &d
	m.focus = focusMain
	lines := cv.render(*m, w, 40)
	out = a2Plain(lines)
	for _, want := range []string{"not checked out", "Files changed since main  30 files", "binary", "old.go → new.go", "… 5 more",
		"Commits ahead of main  9", "abc commit 0", "abcdef1 commit 1", "… 3 more"} { // the commits' own "… more" fits too
		if !strings.Contains(out, want) {
			t.Fatalf("render lacks %q:\n%s", want, out)
		}
	}
	if len(lines) > 40 {
		t.Fatalf("%d lines for a height of 40", len(lines))
	}
	for i, l := range lines {
		if lw := ansi.StringWidth(l); lw > w {
			t.Fatalf("line %d is %d wide:\n%s", i, lw, out)
		}
	}
	if out := a2Plain(cv.render(*m, w, 60)); !strings.Contains(out, "f29.go") || !strings.Contains(out, "… 3 more") {
		t.Fatalf("tall render shows every file and elides commits:\n%s", out)
	}
	// Selecting a file further down scrolls the list.
	cv.sel = 29
	cv.render(*m, w, 40)
	if cv.scroll == 0 {
		t.Fatal("the list scrolls to the selection")
	}
	cv.sel = 0
	cv.render(*m, w, 40)
	if cv.scroll != 0 {
		t.Fatalf("scroll back up: %d", cv.scroll)
	}

	empty := a2Changes()
	cv = &changesView{machine: localMachine, projectID: "r1", branch: "main", data: &empty}
	if out := a2Plain(cv.render(*m, w, h)); !strings.Contains(out, "Uncommitted changes  0 files") || !strings.Contains(out, "nothing here") || strings.Contains(out, "ahead,") {
		t.Fatalf("empty render:\n%s", out)
	}
	// An unknown project still renders.
	(&changesView{machine: "nope", projectID: "x", branch: "b", data: &empty}).render(*m, 10, 3)
}

func TestA2ChangesRenderDiff(t *testing.T) {
	cv := &changesView{diffFile: "a.go"}
	if out := a2Plain(cv.renderDiff(60, 10)); !strings.Contains(out, "loading…") || !strings.Contains(out, "esc back") {
		t.Fatalf("loading diff:\n%s", out)
	}
	cv.diffErr = "too big"
	if out := a2Plain(cv.renderDiff(60, 10)); !strings.Contains(out, "too big") {
		t.Fatalf("diff error:\n%s", out)
	}
	cv.diffErr = ""
	cv.diff = []string{"diff --git a b", "index 1..2", "--- a/a.go", "+++ b/a.go", "@@ -1 +1 @@", "-old\tx", "+new \x1b[31mred", " same", "extra1", "extra2"}
	lines := cv.renderDiff(60, 6)
	if len(lines) != 6 {
		t.Fatalf("diff fills the height: %d lines", len(lines))
	}
	cv.diffScroll = 5
	out := a2Plain(cv.renderDiff(60, 10))
	if !strings.Contains(out, "-old    x") || !strings.Contains(out, "+new red") || strings.Contains(out, "diff --git") {
		t.Fatalf("scrolled diff:\n%s", out)
	}
	for _, l := range cv.renderDiff(60, 10) {
		if strings.Contains(l, "\x1b[31m") {
			t.Fatal("escape sequences from the diff reach the terminal")
		}
	}
}

func TestA2DiffHelpers(t *testing.T) {
	if got := ansi.Strip(diffStat(3, 2)); got != "+3 −2" {
		t.Fatalf("diffStat %q", got)
	}
	if diffStat(0, 0) != "" || ansi.Strip(diffStat(0, 1)) != "−1" {
		t.Fatal("diffStat zero sides")
	}
	for code, want := range map[string]string{"A": "ok", "?": "ok", "D": "err", "U": "err", "R": "work", "C": "work", "M": "warn"} {
		style := map[string]string{"ok": styleOK.Render("x"), "err": styleErr.Render("x"), "work": styleWork.Render("x"), "warn": styleWarn.Render("x")}[want]
		if codeStyle(code).Render("x") != style {
			t.Errorf("code %s is not styled %s", code, want)
		}
	}
	now := time.Now()
	for d, want := range map[time.Duration]string{10 * time.Second: "now", 5 * time.Minute: "5m", 3 * time.Hour: "3h", 72 * time.Hour: "3d"} {
		if got := ago(now.Add(-d)); got != want {
			t.Errorf("ago(%v) = %q", d, got)
		}
	}
	old := now.Add(-400 * 24 * time.Hour)
	if ago(old) != old.Format("Jan 2006") {
		t.Fatalf("old dates show the month: %q", ago(old))
	}
}
