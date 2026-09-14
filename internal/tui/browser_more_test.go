package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/proto"
)

func a2Listing() proto.FSList {
	return proto.FSList{Path: "/home/dev/src", Parent: "/home/dev", Home: "/home/dev", Entries: []proto.FSEntry{
		{Name: "api", Git: true}, {Name: "notes"}, {Name: "web", Project: true},
	}}
}

// a2Browser is a browser as newBrowser makes it, on machine mid.
func a2Browser(m *Model, mid string, list *proto.FSList) *browser {
	b, _ := newBrowser(m, mid, "")
	b.list = list
	return b
}

// a2DirReq is the listing request a command made, run without a connection.
func a2DirReq(t *testing.T, cmd tea.Cmd) dirListMsg {
	t.Helper()
	if cmd == nil {
		t.Fatal("no listing requested")
	}
	msg, ok := cmd().(dirListMsg)
	if !ok || msg.err == nil || msg.err.Error() != "machine is offline" {
		t.Fatalf("listing without a connection: %#v", msg)
	}
	return msg
}

func TestA2BrowserNavigate(t *testing.T) {
	m := a2Model()
	b, cmd := newBrowser(m, localMachine, "~")
	m.overlay = b
	first := a2DirReq(t, cmd)
	b.update(m, first)
	if b.err != "machine is offline" {
		t.Fatalf("listing error: %q", b.err)
	}
	b.update(m, dirListMsg{req: b.req, list: a2Listing()})
	if b.err != "" || b.list == nil || b.sel != 0 {
		t.Fatalf("listing: %+v", b)
	}

	for _, step := range []struct {
		key  string
		want int
	}{{"down", 1}, {"j", 2}, {"down", 3}, {"down", 3}, {"up", 2}, {"k", 1}, {"home", 0}, {"end", 3}, {"pgup", 0}, {"pgdown", 3}} {
		if closed, _ := b.update(m, a2Key(step.key)); closed || b.sel != step.want {
			t.Fatalf("after %s sel %d, want %d", step.key, b.sel, step.want)
		}
	}

	// Enter on ".." goes up and lands on this folder.
	b.sel = 0
	_, cmd = b.update(m, a2Key("enter"))
	a2DirReq(t, cmd)
	if b.want != "src" {
		t.Fatalf("up selects the folder we came from: %q", b.want)
	}
	b.sel = 1
	_, cmd = b.update(m, a2Key("right"))
	a2DirReq(t, cmd)
	if b.want != "" {
		t.Fatalf("entering a folder: want %q", b.want)
	}
	_, cmd = b.update(m, a2Key("backspace"))
	a2DirReq(t, cmd)
	if b.want != "src" {
		t.Fatal("left goes up")
	}
	_, cmd = b.update(m, a2Key("~"))
	a2DirReq(t, cmd)
	_, cmd = b.update(m, a2Key("H"))
	a2DirReq(t, cmd)
	if !b.hidden {
		t.Fatal("H shows hidden folders")
	}
	// An older reply is dropped.
	b.update(m, dirListMsg{req: b.req - 1, list: proto.FSList{Path: "/old"}})
	if b.list.Path != "/home/dev/src" {
		t.Fatal("stale listing applied")
	}

	// a adds the selected folder, . adds this one: both close the browser.
	b.sel = 1
	closed, cmd := b.update(m, a2Key("a"))
	if !closed || m.overlay != nil || a2ErrText(a2Run(cmd)) != "local is online" {
		t.Fatal("a adds the selection")
	}
	m.overlay = b
	closed, cmd = b.update(m, a2Key("."))
	if !closed || a2ErrText(a2Run(cmd)) != "local is online" {
		t.Fatal(". adds this folder")
	}
	m.overlay = b
	if closed, _ := b.update(m, a2Key("esc")); !closed || m.overlay != nil {
		t.Fatal("esc closes")
	}

	// Without a listing, few keys do anything.
	empty := a2Browser(m, localMachine, nil)
	for _, k := range []string{"enter", "left", "a", ".", "H"} {
		if closed, cmd := empty.update(m, a2Key(k)); closed || cmd != nil {
			t.Fatalf("%s without a listing", k)
		}
	}
	if empty.selectedPath(); empty.rows() != nil {
		t.Fatal("rows without a listing")
	}
}

func TestA2BrowserInputs(t *testing.T) {
	m := a2Model()
	b := a2Browser(m, localMachine, nil)
	b.update(m, dirListMsg{req: b.req, list: a2Listing()})
	m.overlay = b

	// Filter as you type; esc in the filter clears it.
	b.update(m, a2Key("/"))
	if b.mode != filterMode {
		t.Fatal("/ starts filtering")
	}
	a2Type(m, b, "we")
	if b.filter != "we" || len(b.rows()) != 1 {
		t.Fatalf("filter %q rows %d", b.filter, len(b.rows()))
	}
	b.update(m, tickMsg{}) // other messages go to the input
	b.update(m, a2Key("esc"))
	if b.mode != browseMode || b.filter != "" {
		t.Fatal("esc leaves and clears the filter")
	}
	b.update(m, a2Key("/"))
	a2Type(m, b, "no")
	b.update(m, a2Key("enter"))
	if b.mode != browseMode || b.filter != "no" {
		t.Fatalf("enter keeps the filter: %q", b.filter)
	}
	// esc in browse mode clears a filter before closing.
	if closed, _ := b.update(m, a2Key("q")); closed || b.filter != "" || m.overlay == nil {
		t.Fatal("q clears the filter first")
	}

	// New folder: invalid names are refused.
	b.update(m, a2Key("n"))
	a2Type(m, b, "a/b")
	b.update(m, a2Key("enter"))
	if m.flash != "a folder name can't be empty or contain /" || b.mode != browseMode {
		t.Fatalf("bad folder name: %q", m.flash)
	}
	b.update(m, a2Key("n"))
	a2Type(m, b, "fresh")
	if _, cmd := b.update(m, a2Key("enter")); cmd == nil {
		t.Fatal("a new folder is created")
	}
	// New project.
	m.flash = ""
	b.update(m, a2Key("p"))
	b.update(m, a2Key("enter"))
	if m.flash != "a project name can't be empty or contain /" {
		t.Fatalf("empty project name: %q", m.flash)
	}
	b.update(m, a2Key("p"))
	a2Type(m, b, "newproj")
	_, cmd := b.update(m, a2Key("enter"))
	if a2ErrText(a2Run(cmd)) != "local is online" || m.overlay != nil {
		t.Fatal("creating a project closes the browser")
	}
	m.overlay = b
	// Go to a path.
	b.update(m, a2Key("g"))
	if b.mode != gotoMode || b.input.Value() != "/home/dev/src" {
		t.Fatalf("go to starts at the current path: %q", b.input.Value())
	}
	_, cmd = b.update(m, a2Key("enter"))
	a2DirReq(t, cmd)
	// Esc leaves an input without acting.
	b.update(m, a2Key("n"))
	if _, cmd := b.update(m, a2Key("esc")); cmd != nil || b.mode != browseMode {
		t.Fatal("esc leaves the new folder input")
	}
	// Input without a listing does nothing.
	nolist := a2Browser(m, localMachine, nil)
	nolist.update(m, a2Key("g"))
	if nolist.input.Value() != "~" {
		t.Fatalf("go to without a listing starts at home: %q", nolist.input.Value())
	}
	if _, cmd := nolist.update(m, a2Key("enter")); cmd != nil {
		t.Fatal("enter without a listing")
	}
}

func TestA2BrowserRender(t *testing.T) {
	m := a2Model()
	b := a2Browser(m, localMachine, nil)
	bx := b.render(*m)
	a2CheckBox(t, bx, *m)
	if out := a2Plain(bx.lines); !strings.Contains(out, "…") || !strings.Contains(out, "0 folders") || !strings.Contains(out, " Add project ") {
		t.Fatalf("empty:\n%s", out)
	}
	list := a2Listing()
	for i := 0; i < 40; i++ {
		list.Entries = append(list.Entries, proto.FSEntry{Name: fmt.Sprintf("dir%02d", i)})
	}
	list.Truncated = true
	b.list = &list
	out := a2Plain(b.render(*m).lines)
	for _, want := range []string{"~/src", "44 folders", "first 2000 shown", "↰ ..", "◆ api", "git", "◆ web", "project", "› notes", "n new folder"} {
		if !strings.Contains(out, want) {
			t.Fatalf("render lacks %q:\n%s", want, out)
		}
	}
	b.sel = 43
	b.render(*m)
	if b.scroll == 0 {
		t.Fatal("render scrolls to the selection")
	}
	b.sel = 0
	b.render(*m)
	if b.scroll != 0 {
		t.Fatal("render scrolls back up")
	}
	for mode, want := range map[browserMode]string{filterMode: "filter", newFolderMode: "new folder", newProjectMode: "new project", gotoMode: "go to"} {
		b.mode = mode
		if out := a2Plain(b.render(*m).lines); !strings.Contains(out, want) {
			t.Fatalf("mode %d lacks %q:\n%s", mode, want, out)
		}
	}
	b.mode, b.err = browseMode, "permission denied"
	if out := a2Plain(b.render(*m).lines); !strings.Contains(out, "permission denied") {
		t.Fatal("error status")
	}
	b.err, b.filter = "", "dir1"
	if out := a2Plain(b.render(*m).lines); !strings.Contains(out, "/dir1  10 matching · esc clears") {
		t.Fatalf("filter status:\n%s", out)
	}

	m.machines = append(m.machines, &machine{id: "box", label: "devbox"})
	remote := a2Browser(m, "box", &proto.FSList{Path: "/very/long/" + strings.Repeat("x", 200)})
	bx = remote.render(Model{width: 50, height: 10, machines: m.machines})
	a2CheckBox(t, bx, Model{width: 50, height: 10})
	if out := a2Plain(bx.lines); !strings.Contains(out, "Add project on devbox") || !strings.Contains(out, "…") {
		t.Fatalf("remote narrow:\n%s", out)
	}
}

func TestA2BrowserMouse(t *testing.T) {
	m := a2Model()
	l := a2Listing()
	b := a2Browser(m, localMachine, &l)
	m.overlay = b
	bx := b.render(*m)
	b.mouse(m, tea.MouseMsg{X: bx.x + 2, Y: bx.y + 4, Button: tea.MouseButtonWheelDown}, bx)
	if b.sel != 3 {
		t.Fatalf("wheel down: %d", b.sel)
	}
	b.mouse(m, tea.MouseMsg{X: bx.x + 2, Y: bx.y + 4, Button: tea.MouseButtonWheelUp}, bx)
	if b.sel != 0 {
		t.Fatalf("wheel up: %d", b.sel)
	}
	row := func(i int) int { return bx.y + 1 + browserRowsTop + i }
	press := func(y int) tea.Cmd {
		return b.mouse(m, tea.MouseMsg{X: bx.x + 3, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}, bx)
	}
	if press(row(2)) != nil || b.sel != 2 {
		t.Fatalf("click selects: %d", b.sel)
	}
	// A second click soon after opens the folder.
	a2DirReq(t, press(row(2)))
	b.lastClick = time.Now().Add(-time.Hour)
	if press(row(1)) != nil || b.sel != 1 {
		t.Fatal("a slow second click only selects")
	}
	if press(row(10)) != nil || press(bx.y) != nil || b.sel != 1 {
		t.Fatal("clicks past the rows or on the path")
	}
	b.mouse(m, tea.MouseMsg{X: bx.x + 3, Y: row(0), Action: tea.MouseActionMotion}, bx)
	b.mode = filterMode
	if press(row(0)) != nil || b.sel != 1 {
		t.Fatal("clicks while typing")
	}
	b.mode = browseMode
	b.mouse(m, tea.MouseMsg{X: 0, Y: 0, Action: tea.MouseActionMotion}, bx)
	if m.overlay == nil {
		t.Fatal("motion outside")
	}
	b.mouse(m, tea.MouseMsg{X: 0, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}, bx)
	if m.overlay != nil {
		t.Fatal("a click outside closes")
	}
}
