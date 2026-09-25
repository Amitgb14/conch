package tui

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

// filesFixture is a1Fixture's project, online through a server that can
// browse files, with the explorer open on the main checkout and its top
// level already listed.
func filesFixture(t *testing.T, caps ...string) (*Model, *a1Peer, *filesView) {
	t.Helper()
	m, _ := a1Fixture(t, false)
	if caps == nil {
		caps = []string{proto.CapFSFiles, proto.CapFSRead}
	}
	c, peer := a1FakeClient(t, caps...)
	mach := m.machines[0]
	mach.c, mach.server = c, c.Server
	a1Open(t, m, sectionID(localMachine, "r1", "files"))
	m.focus = focusMain
	fv := m.tab().focused().files
	if fv == nil {
		t.Fatal("no explorer in the focused split")
	}
	if fv.err == "" {
		fv.receiveList(filesListMsg{machine: localMachine, root: fv.root, rel: "", list: proto.FSList{Entries: []proto.FSEntry{
			{Name: "cmd", Dir: true},
			{Name: "internal", Dir: true, Status: "M"},
			{Name: "README.md", Size: 5},
			{Name: "app.tsx", Status: "?"},
			{Name: "main.go", Status: "M"},
		}}})
	}
	return m, peer, fv
}

// filesGive delivers a listing of rel, as the server's answer would.
func filesGive(fv *filesView, rel string, entries ...proto.FSEntry) {
	if fv.dirs[rel] == nil {
		fv.dirs[rel] = &filesDir{loading: true}
	}
	fv.receiveList(filesListMsg{machine: fv.machine, root: fv.root, rel: rel, list: proto.FSList{Entries: entries}})
}

func filesPlainLines(fv *filesView) []string {
	var out []string
	for _, l := range fv.lines() {
		if l.note != "" {
			out = append(out, strings.Repeat("  ", l.depth)+"("+l.note+")")
			continue
		}
		out = append(out, strings.Repeat("  ", l.depth)+l.e.Name)
	}
	return out
}

func filesSelect(t *testing.T, fv *filesView, rel string) {
	t.Helper()
	for i, l := range fv.lines() {
		if l.note == "" && l.rel == rel {
			fv.sel, fv.selPath = i, rel
			return
		}
	}
	t.Fatalf("no line %q in %v", rel, filesPlainLines(fv))
}

func TestFilesCheckout(t *testing.T) {
	m, _ := a1Fixture(t, false)
	for _, c := range []struct {
		branch, root, checked, note string
	}{
		{"", "/src/api", "main", ""},
		{"feat", "/src/api-feat", "feat", ""},
		{"nowhere", "/src/api", "main", "nowhere has no worktree · showing the main checkout"},
	} {
		root, checked, note := m.filesCheckout(localMachine, "r1", c.branch)
		if root != c.root || checked != c.checked || note != c.note {
			t.Errorf("checkout of %q = %q %q %q", c.branch, root, checked, note)
		}
	}
	if root, _, _ := m.filesCheckout(localMachine, "gone", ""); root != "" {
		t.Errorf("a missing project has a checkout: %q", root)
	}
	// A project without worktrees listed (a plain folder) is its own path.
	m.machines[0].projects[0].Worktrees = nil
	if root, checked, _ := m.filesCheckout(localMachine, "r1", ""); root != "/src/api" || checked != "" {
		t.Errorf("plain folder: %q %q", root, checked)
	}
}

func TestFilesListRequest(t *testing.T) {
	m, peer, fv := filesFixture(t)
	peer.setResult(proto.MethodFSList, proto.FSList{Entries: []proto.FSEntry{{Name: "tui", Dir: true}}})
	filesSelect(t, fv, "internal")
	msgs := a2Run(a1Key(t, m, tea.KeyMsg{Type: tea.KeyEnter}))
	msg := peer.waitMethod(t, proto.MethodFSList, `"path":"internal"`)
	var lp proto.FSListParams
	json.Unmarshal(msg.Params, &lp)
	if lp.Root != "/src/api" || !lp.Files || lp.Hidden || lp.Ignored {
		t.Fatalf("params %+v", lp)
	}
	for _, msg := range msgs {
		m.Update(msg)
	}
	if got := strings.Join(filesPlainLines(fv), ","); got != "cmd,internal,  tui,README.md,app.tsx,main.go" {
		t.Fatalf("after opening internal: %s", got)
	}
}

func TestFilesTooOldAndOffline(t *testing.T) {
	m, peer, fv := filesFixture(t, "fs.v1") // a server from before the explorer
	if fv.err != filesTooOld {
		t.Fatalf("err %q", fv.err)
	}
	out := ansi.Strip(strings.Join(fv.render(*m, 80, 20), "\n"))
	if !strings.Contains(out, "too old to browse files") {
		t.Fatalf("render:\n%s", out)
	}
	if n := peer.count(t, m.machines[0].c, proto.MethodFSList, ""); n != 0 {
		t.Fatalf("asked an old server for files %d times", n)
	}
	// Keys do nothing but leave, or retry.
	if back, _ := fv.key(m, tea.KeyMsg{Type: tea.KeyEsc}); !back {
		t.Fatal("esc does not leave")
	}

	off, _ := a1Fixture(t, false) // no client: offline
	a1Open(t, off, sectionID(localMachine, "r1", "files"))
	if fv := off.tab().focused().files; fv == nil || fv.err == "" {
		t.Fatalf("offline explorer: %+v", fv)
	}
	// It lists once the machine is back.
	c, _ := a1FakeClient(t, proto.CapFSFiles)
	off.machines[0].c = c
	off.syncView()
	if fv := off.tab().focused().files; fv.err != "" || fv.dirs[""] == nil || !fv.dirs[""].loading {
		t.Fatalf("after reconnecting: err %q dirs %v", fv.err, fv.dirs)
	}
}

func TestFilesRenderFitsEverySize(t *testing.T) {
	m, _, fv := filesFixture(t)
	// A deep path and a long name.
	fv.open["internal"] = true
	filesGive(fv, "internal", proto.FSEntry{Name: "a", Dir: true})
	rel := "internal/a"
	for i := 0; i < 12; i++ {
		fv.open[rel] = true
		filesGive(fv, rel, proto.FSEntry{Name: "b", Dir: true}, proto.FSEntry{Name: strings.Repeat("long-name-", 8) + ".go", Status: "M"})
		rel += "/b"
	}
	filesGive(fv, rel, proto.FSEntry{Name: "界界界界.tsx", Status: "?", Symlink: true})
	filesSelect(t, fv, "main.go")
	fv.prev = filesPreview{rel: "main.go", res: &proto.FSReadResult{Data: "package main\n\tfunc x() {}\n// 界界界界界界界界界界界界界界界界界界界界界界界界界界\n"}}
	for _, mode := range iconModes {
		m.cfg.UI.Icons = mode
		for _, sz := range [][2]int{{1, 1}, {20, 5}, {39, 12}, {80, 24}, {99, 30}, {100, 30}, {200, 50}} {
			for _, reading := range []bool{false, true} {
				fv.reading = reading
				w, h := sz[0], sz[1]
				lines := fv.render(*m, w, h)
				if len(lines) > h {
					t.Errorf("%s %dx%d: %d lines", mode, w, h, len(lines))
				}
				for i, l := range lines {
					if lw := ansi.StringWidth(l); lw > w {
						t.Errorf("%s %dx%d reading %v: line %d is %d wide: %q", mode, w, h, reading, i, lw, ansi.Strip(l))
					}
				}
			}
		}
	}
	// Zero sizes draw nothing rather than panic.
	if lines := fv.render(*m, 0, 0); len(lines) != 0 {
		t.Fatalf("0x0: %v", lines)
	}
}

func TestFilesPreviewBesideOrInstead(t *testing.T) {
	m, peer, fv := filesFixture(t)
	peer.setResult(proto.MethodFSRead, proto.FSReadResult{Path: "/src/api/main.go", Size: 13, Data: "package main\n"})

	// Wide: moving onto a file reads it for the preview beside the tree.
	fv.render(*m, 160, 30)
	filesSelect(t, fv, "app.tsx")
	cmd := a1Key(t, m, runes("j")) // onto main.go
	if fv.selPath != "main.go" {
		t.Fatalf("selected %q", fv.selPath)
	}
	for _, msg := range a2Run(cmd) {
		m.Update(msg)
	}
	peer.waitMethod(t, proto.MethodFSRead, `"path":"main.go"`)
	out := ansi.Strip(strings.Join(fv.render(*m, 160, 30), "\n"))
	if !strings.Contains(out, "│ 1  package main") || !strings.Contains(out, "main.go · 1 line · modified") {
		t.Fatalf("wide render:\n%s", out)
	}

	// Narrow: the tree alone, and enter swaps in the preview; esc swaps back.
	fv.render(*m, 80, 30)
	fv.prev = filesPreview{}
	if cmd := a1Key(t, m, runes("k")); cmd != nil {
		for _, msg := range a2Run(cmd) {
			if _, ok := msg.(filesReadMsg); ok {
				t.Fatal("a narrow tree read ahead")
			}
		}
	}
	a1Key(t, m, runes("j"))
	cmd = a1Key(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if !fv.reading {
		t.Fatal("enter did not open the preview")
	}
	for _, msg := range a2Run(cmd) {
		m.Update(msg)
	}
	out = ansi.Strip(strings.Join(fv.render(*m, 80, 30), "\n"))
	if !strings.Contains(out, "1  package main") || strings.Contains(out, "README.md") {
		t.Fatalf("narrow preview:\n%s", out)
	}
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if fv.reading || m.focus != focusMain {
		t.Fatal("esc in the preview left the explorer")
	}
	out = ansi.Strip(strings.Join(fv.render(*m, 80, 30), "\n"))
	if !strings.Contains(out, "README.md") {
		t.Fatalf("back to the tree:\n%s", out)
	}
}

func TestFilesPreviewKinds(t *testing.T) {
	m, _, fv := filesFixture(t)
	filesSelect(t, fv, "main.go")
	fv.reading = true
	render := func() string { return ansi.Strip(strings.Join(fv.render(*m, 80, 20), "\n")) }

	fv.prev = filesPreview{rel: "main.go", res: &proto.FSReadResult{Size: 1300000, Binary: true, MIME: "image/png"}}
	if out := render(); !strings.Contains(out, "binary · image/png · 1.2 MB") {
		t.Fatalf("binary:\n%s", out)
	}
	fv.prev = filesPreview{rel: "main.go", res: &proto.FSReadResult{Size: 0}}
	if out := render(); !strings.Contains(out, "(empty)") {
		t.Fatalf("empty:\n%s", out)
	}
	fv.prev = filesPreview{rel: "main.go", res: &proto.FSReadResult{Size: 3 << 20, Data: "a\nb\n", Truncated: true}}
	if out := render(); !strings.Contains(out, "2+ lines") || !strings.Contains(out, "the first 4 B of 3.0 MB") {
		t.Fatalf("truncated:\n%s", out)
	}
	fv.prev = filesPreview{rel: "main.go", err: "permission denied"}
	if out := render(); !strings.Contains(out, "permission denied") {
		t.Fatalf("error:\n%s", out)
	}
	// A file that tries to drive the terminal is drawn inert.
	fv.prev = filesPreview{rel: "main.go", res: &proto.FSReadResult{Data: "\x1b]0;owned\x07 \u009c x\n"}}
	for _, l := range fv.render(*m, 80, 20) {
		if strings.Contains(l, "\x1b]0") || strings.ContainsRune(l, 0x9c) || strings.Contains(l, "\x07") || strings.Contains(l, "\x07") {
			t.Fatalf("control characters reached the screen: %q", l)
		}
	}
	// Scrolling stays inside the file.
	fv.prev = filesPreview{rel: "main.go", res: &proto.FSReadResult{Data: strings.Repeat("x\n", 100)}}
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyEnd})
	render()
	if fv.prevScroll != 100-(20-filesTop-1) {
		t.Fatalf("scroll at the end: %d", fv.prevScroll)
	}
	a1Key(t, m, runes("g"))
	if fv.prevScroll != 0 {
		t.Fatalf("scroll at the top: %d", fv.prevScroll)
	}
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if fv.prevScroll != 0 {
		t.Fatalf("scrolled above the top: %d", fv.prevScroll)
	}
}

func TestFilesTreeKeys(t *testing.T) {
	m, _, fv := filesFixture(t)
	fv.render(*m, 80, 30)
	if fv.selPath != "cmd" {
		t.Fatalf("starts on %q", fv.selPath)
	}
	a1Key(t, m, runes("G"))
	if fv.selPath != "main.go" {
		t.Fatalf("G: %q", fv.selPath)
	}
	a1Key(t, m, runes("j")) // at the bottom already
	if fv.selPath != "main.go" {
		t.Fatalf("j past the end: %q", fv.selPath)
	}
	a1Key(t, m, runes("g"))
	if fv.selPath != "cmd" {
		t.Fatalf("g: %q", fv.selPath)
	}
	// Open a folder: "reading…" until its listing comes, and the selection
	// passes over that note.
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if got := strings.Join(filesPlainLines(fv), ","); got != "cmd,  (reading…),internal,README.md,app.tsx,main.go" {
		t.Fatalf("opening: %s", got)
	}
	a1Key(t, m, runes("j"))
	if fv.selPath != "internal" {
		t.Fatalf("j over the note: %q", fv.selPath)
	}
	a1Key(t, m, runes("k"))
	filesGive(fv, "cmd", proto.FSEntry{Name: "conch", Dir: true})
	a1Key(t, m, runes("j"))
	if fv.selPath != "cmd/conch" {
		t.Fatalf("into the folder: %q", fv.selPath)
	}
	// h from inside goes to the parent; h on an open folder folds it.
	a1Key(t, m, runes("h"))
	if fv.selPath != "cmd" {
		t.Fatalf("h to the parent: %q", fv.selPath)
	}
	a1Key(t, m, runes("h"))
	if fv.open["cmd"] {
		t.Fatal("h did not fold")
	}
	// At the top with nothing to fold, h goes back to the tree.
	a1Key(t, m, runes("h"))
	if m.focus != focusSidebar {
		t.Fatal("h at the top stayed in the explorer")
	}
	m.focus = focusMain
	// An empty folder says so.
	filesSelect(t, fv, "internal")
	a1Key(t, m, runes("l"))
	filesGive(fv, "internal")
	if got := strings.Join(filesPlainLines(fv), ","); !strings.Contains(got, "internal,  (empty)") {
		t.Fatalf("empty folder: %s", got)
	}
	// A folder that failed to list says why.
	fv.dirs["internal"] = &filesDir{}
	fv.receiveList(filesListMsg{machine: localMachine, root: fv.root, rel: "internal", err: errString("permission denied")})
	if got := strings.Join(filesPlainLines(fv), ","); !strings.Contains(got, "(permission denied)") {
		t.Fatalf("failed folder: %s", got)
	}
	// Truncated listings say so on their last line.
	fv.receiveList(filesListMsg{machine: localMachine, root: fv.root, rel: "internal",
		list: proto.FSList{Entries: []proto.FSEntry{{Name: "x"}}, Truncated: true}})
	if got := strings.Join(filesPlainLines(fv), ","); !strings.Contains(got, "(… only the first 1 are shown)") {
		t.Fatalf("truncated: %s", got)
	}
	// esc leaves; q too.
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.focus != focusSidebar {
		t.Fatal("esc stayed")
	}
}

func TestFilesSelectionSurvivesARelist(t *testing.T) {
	m, _, fv := filesFixture(t)
	fv.render(*m, 80, 30)
	filesSelect(t, fv, "main.go")
	// New files land before it; the selection stays on main.go.
	filesGive(fv, "", proto.FSEntry{Name: "cmd", Dir: true}, proto.FSEntry{Name: "a.go"}, proto.FSEntry{Name: "b.go"}, proto.FSEntry{Name: "main.go"})
	fv.render(*m, 80, 30)
	if l, ok := fv.selected(fv.lines()); !ok || l.rel != "main.go" {
		t.Fatalf("selection moved to %+v", l)
	}
	// When it goes away, the selection stays near where it was.
	filesGive(fv, "", proto.FSEntry{Name: "cmd", Dir: true}, proto.FSEntry{Name: "a.go"})
	fv.render(*m, 80, 30)
	if l, ok := fv.selected(fv.lines()); !ok || l.rel != "a.go" {
		t.Fatalf("after main.go went: %+v", l)
	}
}

func TestFilesFilter(t *testing.T) {
	m, _, fv := filesFixture(t)
	filesGive(fv, "internal", proto.FSEntry{Name: "tui", Dir: true})
	filesGive(fv, "internal/tui", proto.FSEntry{Name: "queue.go"}, proto.FSEntry{Name: "model.go"})
	a1Key(t, m, runes("/"))
	for _, r := range "qgo" {
		a1Key(t, m, runes(string(r)))
	}
	// Loaded folders are searched even when closed, and lead to the match.
	if got := strings.Join(filesPlainLines(fv), ","); got != "internal,  tui,    queue.go" {
		t.Fatalf("filtered: %s", got)
	}
	out := ansi.Strip(strings.Join(fv.render(*m, 80, 20), "\n"))
	if !strings.Contains(out, "/ qgo█") {
		t.Fatalf("filter prompt:\n%s", out)
	}
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if fv.typing || fv.filter != "qgo" {
		t.Fatal("enter did not keep the filter")
	}
	a1Key(t, m, runes("/"))
	a1Key(t, m, runes("z"))
	if got := strings.Join(filesPlainLines(fv), ","); !strings.Contains(got, "nothing loaded matches qgoz") {
		t.Fatalf("no match: %s", got)
	}
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyBackspace})
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if fv.filter != "" || fv.typing {
		t.Fatal("esc did not clear")
	}
	if m.focus != focusMain {
		t.Fatal("esc while typing left the explorer")
	}
}

func TestFuzzy(t *testing.T) {
	for _, c := range []struct {
		name, pat string
		want      bool
	}{
		{"queue.go", "qgo", true},
		{"Queue.go", "QUE", true},
		{"queue.go", "", true},
		{"queue.go", "q g", true},
		{"queue.go", "ogq", false},
		{"café.go", "éo", true},
		{"", "a", false},
	} {
		if got := fuzzy(c.name, c.pat); got != c.want {
			t.Errorf("fuzzy(%q, %q) = %v", c.name, c.pat, got)
		}
	}
}

func TestFilesToAgent(t *testing.T) {
	m, peer, fv := filesFixture(t)
	// Nobody works in the main checkout: p1 is in the feat worktree.
	filesSelect(t, fv, "main.go")
	a1Key(t, m, runes("a"))
	if !m.flashIsErr || !strings.Contains(m.flash, "no agent") {
		t.Fatalf("flash %q", m.flash)
	}
	// In feat's worktree, the agent there gets the path relative to itself.
	a1Key(t, m, runes("w"))
	if fv.root != "/src/api-feat" || m.tab().focused().view.Branch != "feat" {
		t.Fatalf("w: root %q view %+v", fv.root, m.tab().focused().view)
	}
	filesGive(fv, "", proto.FSEntry{Name: "internal", Dir: true})
	fv.open["internal"] = true
	filesGive(fv, "internal", proto.FSEntry{Name: "my file.go"})
	filesSelect(t, fv, "internal/my file.go")
	a1Key(t, m, runes("a"))
	msg := peer.waitMethod(t, proto.MethodPaneSendText, `"id":"p1"`)
	var sp proto.PaneSendTextParams
	json.Unmarshal(msg.Params, &sp)
	if sp.Text != `"internal/my file.go" ` || !sp.Paste {
		t.Fatalf("sent %+v", sp)
	}
	if !strings.Contains(m.flash, "Claude Code") {
		t.Fatalf("flash %q", m.flash)
	}
	// An agent in a folder below the checkout gets an absolute path.
	m.machines[0].panes[0].Cwd = "/src/api-feat/sub"
	filesSelect(t, fv, "internal")
	a1Key(t, m, runes("a"))
	peer.waitMethod(t, proto.MethodPaneSendText, `"text":"/src/api-feat/internal "`)
}

func TestFilesAgentChoice(t *testing.T) {
	m, peer, fv := filesFixture(t)
	now := time.Now()
	mach := m.machines[0]
	mach.panes = append(mach.panes,
		proto.PaneInfo{ID: "old", Name: "codex", State: proto.PaneRunning, Cwd: "/src/api", Agent: &proto.AgentStatus{Name: "codex", Since: now.Add(-time.Hour)}},
		proto.PaneInfo{ID: "new", Name: "claude", State: proto.PaneRunning, Cwd: "/src/api", Agent: &proto.AgentStatus{Name: "claude", Since: now}},
		proto.PaneInfo{ID: "gone", Name: "claude", State: proto.PaneExited, Cwd: "/src/api", Agent: &proto.AgentStatus{Name: "claude", Since: now.Add(time.Hour)}},
		proto.PaneInfo{ID: "near", Name: "claude", State: proto.PaneRunning, Cwd: "/src/api2", Agent: &proto.AgentStatus{Name: "claude", Since: now.Add(time.Hour)}},
	)
	filesSelect(t, fv, "README.md")
	a1Key(t, m, runes("a"))
	peer.waitMethod(t, proto.MethodPaneSendText, `"id":"new"`) // the most recently busy
	// One shown beside the explorer wins over a busier one elsewhere.
	m.tab().root = &layoutNode{dir: splitRight, ratio: 0.5, a: m.tab().root, b: &layoutNode{leaf: m.newLeaf(viewRef{Row: "x", Kind: kindPane, Machine: localMachine, PaneID: "old"})}}
	a1Key(t, m, runes("a"))
	peer.waitMethod(t, proto.MethodPaneSendText, `"id":"old"`)
	if !withinDir("/src/api", "/src/api/") || withinDir("/src/api2", "/src/api") || withinDir("/x", "") {
		t.Fatal("withinDir")
	}
}

func TestFilesCopyEditDiff(t *testing.T) {
	m, peer, fv := filesFixture(t)
	filesSelect(t, fv, "main.go")
	if cmd := a1Key(t, m, runes("y")); cmd == nil {
		t.Fatal("y returned no copy")
	}
	// e starts the machine's editor on the file, in the checkout.
	peer.setResult(proto.MethodPaneCreate, proto.PaneInfo{ID: "ed", State: proto.PaneRunning})
	msgs := a2Run(a1Key(t, m, runes("e")))
	if len(msgs) != 1 {
		t.Fatalf("e: %v", msgs)
	}
	if cm, ok := msgs[0].(createdMsg); !ok || cm.info.ID != "ed" {
		t.Fatalf("e: %#v", msgs[0])
	}
	msg := peer.waitMethod(t, proto.MethodPaneCreate, "")
	var pc proto.PaneCreateParams
	json.Unmarshal(msg.Params, &pc)
	if pc.Cwd != "/src/api" || len(pc.Command) != 5 || pc.Command[4] != "/src/api/main.go" || !strings.Contains(pc.Command[2], "EDITOR") {
		t.Fatalf("edit pane %+v", pc)
	}
	// d opens the diff of a changed file on the checkout's branch.
	a1Key(t, m, runes("d"))
	if m.changes == nil || m.changes.branch != "main" || m.changes.diffFile != "main.go" {
		t.Fatalf("d: %+v", m.changes)
	}
	// ... and says why not for an unchanged one.
	a1Open(t, m, sectionID(localMachine, "r1", "files"))
	m.focus = focusMain
	fv = m.tab().focused().files
	filesSelect(t, fv, "README.md")
	a1Key(t, m, runes("d"))
	if !strings.Contains(m.flash, "no uncommitted changes") {
		t.Fatalf("flash %q", m.flash)
	}
}

func TestFilesHiddenAndIgnoredRelist(t *testing.T) {
	m, peer, fv := filesFixture(t)
	a2Run(a1Key(t, m, runes(".")))
	peer.waitMethod(t, proto.MethodFSList, `"hidden":true`)
	a2Run(a1Key(t, m, runes("i")))
	peer.waitMethod(t, proto.MethodFSList, `"ignored":true`)
	if !fv.hidden || !fv.ignored {
		t.Fatal("toggles")
	}
	a1Key(t, m, runes("r"))
	if !fv.dirs[""].loading {
		t.Fatal("r did not read again")
	}
}

func TestFilesWorktreeEvent(t *testing.T) {
	m, peer, fv := filesFixture(t)
	fv.open["internal"] = true
	filesGive(fv, "internal", proto.FSEntry{Name: "x.go"})
	fv.dirs["cmd"] = &filesDir{entries: []proto.FSEntry{{Name: "y.go"}}} // read once, folded since
	fv.prev = filesPreview{rel: "internal/x.go", res: &proto.FSReadResult{Data: "old"}}

	other := m.filesEvent(localMachine, proto.WorktreeChanged{ProjectID: "r1", Worktree: "/src/api-feat", Paths: []string{"internal/x.go"}})
	if other != nil && len(a2Run(other)) > 0 {
		t.Fatal("another worktree's edit reached the explorer")
	}
	cmd := m.filesEvent(localMachine, proto.WorktreeChanged{ProjectID: "r1", Worktree: "/src/api", Paths: []string{"internal/x.go"}})
	if cmd == nil {
		t.Fatal("no re-read")
	}
	if fv.dirs["cmd"] != nil {
		t.Fatal("a folded folder was kept")
	}
	if !fv.dirs[""].loading || !fv.dirs["internal"].loading || !fv.prev.loading || fv.prev.res == nil {
		t.Fatalf("what shows is read again, kept on screen meanwhile: %+v %+v", fv.dirs, fv.prev)
	}
	a2Run(cmd)
	peer.waitMethod(t, proto.MethodFSRead, `"path":"internal/x.go"`)
	peer.waitMethod(t, proto.MethodFSList, `"path":"internal"`)
}

func TestFilesProjectUpdateRelists(t *testing.T) {
	m, _, fv := filesFixture(t)
	if cmd := m.filesProjectUpdated(localMachine, "r1"); cmd != nil {
		t.Fatal("re-read with nothing changed")
	}
	m.machines[0].projects[0].Worktrees[0].Head = "abc123" // a commit
	if cmd := m.filesProjectUpdated(localMachine, "r1"); cmd == nil || !fv.dirs[""].loading {
		t.Fatal("a commit did not re-read the listing")
	}
}

func TestFilesMouse(t *testing.T) {
	m, peer, fv := filesFixture(t)
	peer.setResult(proto.MethodFSList, proto.FSList{Entries: []proto.FSEntry{{Name: "tui", Dir: true}}})
	fv.render(*m, 80, 30)
	press := tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}
	// The second body line is internal: a click selects it, another opens it.
	fv.mouse(m, press, 5, filesTop+1)
	if fv.selPath != "internal" || fv.open["internal"] {
		t.Fatalf("first click: %q open %v", fv.selPath, fv.open)
	}
	for _, msg := range a2Run(fv.mouse(m, press, 5, filesTop+1)) {
		m.Update(msg)
	}
	if !fv.open["internal"] || strings.Join(filesPlainLines(fv), ",") != "cmd,internal,  tui,README.md,app.tsx,main.go" {
		t.Fatalf("second click: %v", filesPlainLines(fv))
	}
	// The wheel moves the selection.
	fv.mouse(m, tea.MouseMsg{Button: tea.MouseButtonWheelDown}, 5, filesTop+1)
	if fv.selPath != "internal/tui" {
		t.Fatalf("wheel: %q", fv.selPath)
	}
	// A click on a note or past the end does nothing.
	fv.mouse(m, press, 5, filesTop+20)
	if fv.selPath != "internal/tui" {
		t.Fatalf("click past the end: %q", fv.selPath)
	}
	// The breadcrumb: the path shows internal; clicking it folds back up.
	fv.render(*m, 80, 30)
	var crumb filesCrumb
	for _, c := range fv.crumbs {
		if c.rel == "internal" {
			crumb = c
		}
	}
	if crumb.rel == "" {
		t.Fatalf("no crumb for internal: %+v", fv.crumbs)
	}
	fv.mouse(m, press, crumb.x0, 1)
	if fv.selPath != "internal" || fv.open["internal"] {
		t.Fatalf("crumb: %q open %v", fv.selPath, fv.open)
	}
	fv.mouse(m, press, 1, 1) // the checkout itself
	if fv.selPath != "cmd" || len(fv.open) != 0 {
		t.Fatalf("root crumb: %q open %v", fv.selPath, fv.open)
	}
	// Wide: a click in the preview gives it the keys; the wheel there scrolls it.
	fv.render(*m, 160, 30)
	filesSelect(t, fv, "main.go")
	fv.prev = filesPreview{rel: "main.go", res: &proto.FSReadResult{Data: strings.Repeat("x\n", 100)}}
	fv.mouse(m, tea.MouseMsg{Button: tea.MouseButtonWheelDown}, fv.treeW+10, 10)
	if fv.prevScroll != 3 || fv.selPath != "main.go" {
		t.Fatalf("wheel over the preview: scroll %d sel %q", fv.prevScroll, fv.selPath)
	}
	fv.mouse(m, press, fv.treeW+10, 10)
	if !fv.reading {
		t.Fatal("a click in the preview did not focus it")
	}
}

// Through the model: a click on the split reaches the explorer.
func TestFilesMouseThroughModel(t *testing.T) {
	m, _, fv := filesFixture(t)
	m.focus = focusSidebar
	m.View()
	rects, _ := m.leafRects()
	r := rects[m.tab().focus]
	next, _ := m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: r.x + 5, Y: r.y + 1 + filesTop + 2})
	*m = next.(Model)
	if m.focus != focusMain || m.tab().focused().files.selPath != "README.md" {
		t.Fatalf("focus %v sel %q", m.focus, fv.selPath)
	}
}

func TestFilesOpenWithF(t *testing.T) {
	m, _ := a1Fixture(t, false)
	c, _ := a1FakeClient(t, proto.CapFSFiles, proto.CapFSRead)
	m.machines[0].c = c
	m.focus = focusSidebar
	// From a branch: that branch's worktree.
	a1At(t, m, branchNodeID(localMachine, "r1", "feat"))
	a1Key(t, m, runes("f"))
	l := m.tab().focused()
	if l.view.Kind != kindFiles || l.view.Branch != "feat" || l.files == nil || l.files.root != "/src/api-feat" || m.focus != focusMain {
		t.Fatalf("f on feat: %+v files %+v", l.view, l.files)
	}
	// From the project: the main checkout, in the same split.
	m.focus = focusSidebar
	a1At(t, m, projectNodeID(localMachine, "r1"))
	a1Key(t, m, runes("f"))
	l = m.tab().focused()
	if l.view.Branch != "" || l.files.root != "/src/api" {
		t.Fatalf("f on the project: %+v root %q", l.view, l.files.root)
	}
	// Outside every project there is nothing to browse.
	m.focus = focusSidebar
	a1At(t, m, paneNodeID(localMachine, "p3"))
	a1Key(t, m, runes("f"))
	if !m.flashIsErr || !strings.Contains(m.flash, "select a project") {
		t.Fatalf("f outside a project: %q", m.flash)
	}
}

func TestFilesSavedAndRestored(t *testing.T) {
	m, _, _ := filesFixture(t)
	a1Key(t, m, runes("w")) // the feat worktree
	saved := m.savedTabs()
	b, _ := json.Marshal(saved)
	var back []savedTab
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	m2, _ := a1Fixture(t, false)
	c, _ := a1FakeClient(t, proto.CapFSFiles)
	m2.machines[0].c = c
	m2.tabs, m2.preview, m2.previewing = nil, nil, false // as at start-up
	m2.restoreTabs(back, 0)
	m2.focus = focusMain // what is on screen is the restored tab, not the cursor's
	m2.syncView()
	var found *filesView
	for _, tb := range m2.tabs {
		for _, l := range tb.root.leaves() {
			if l.files != nil {
				found = l.files
			}
		}
	}
	if found == nil || found.root != "/src/api-feat" {
		t.Fatalf("restored explorer: %+v", found)
	}
	// A ui.json from before the explorer loads as it did.
	path := filepath.Join(t.TempDir(), "ui.json")
	if err := saveUIState(path, uiState{Tabs: []savedTab{{Name: "x", Root: &savedNode{View: &viewRef{Row: "q", Kind: kindReviewQueue}}}}}); err != nil {
		t.Fatal(err)
	}
	if st := loadUIState(path); len(st.Tabs) != 1 || st.Tabs[0].Root.View.Kind != kindReviewQueue {
		t.Fatalf("old state: %+v", st)
	}
}

func TestFilesLabelsAndStatusBar(t *testing.T) {
	m, _, fv := filesFixture(t)
	v := m.tab().focused().view
	if got := m.viewLabel(v); got != "api" {
		t.Fatalf("viewLabel %q", got)
	}
	m.View()
	bar := ansi.Strip(m.statusBar())
	if !strings.Contains(bar, "FILES") {
		t.Fatalf("status bar: %s", bar)
	}
	fv.reading = true
	if bar := ansi.Strip(m.statusBar()); !strings.Contains(bar, "FILE ") {
		t.Fatalf("status bar reading: %s", bar)
	}
	fv.reading, fv.typing = false, true
	if bar := ansi.Strip(m.statusBar()); !strings.Contains(bar, "FIND") {
		t.Fatalf("status bar typing: %s", bar)
	}
	// Placed there, an agent starts in the checkout.
	fv.typing = false
	m.focus = focusSidebar
	a1At(t, m, sectionID(localMachine, "r1", "files"))
	if pl := m.contextPlace(); pl.dir != "/src/api" || pl.projectID != "r1" {
		t.Fatalf("place %+v", pl)
	}
}

func TestPreviewText(t *testing.T) {
	for _, c := range []struct {
		in   string
		w    int
		want string
	}{
		{"plain", 10, "plain"},
		{"\tx", 10, "    x"},
		{"ab\tx", 10, "ab  x"},
		{"a\x1bb", 10, "a·b"},
		{"a\u009cb", 10, "a·b"},
		{"a\x9cb", 10, "a\ufffdb"}, // not UTF-8: the server sends such a file as binary
		{"a\x7fb", 10, "a·b"},
		{"abcdefghij", 5, "abcd…"},
		{"界界界", 4, "界…"},
		{"", 4, ""},
	} {
		if got := previewText(c.in, c.w); got != c.want {
			t.Errorf("previewText(%q, %d) = %q, want %q", c.in, c.w, got, c.want)
		}
	}
	if sizeText(0) != "0 B" || sizeText(2048) != "2.0 KB" || sizeText(5<<30) != "5.0 GB" {
		t.Fatal("sizeText")
	}
	if statusWord("?") != "untracked" || statusWord("X") != "X" {
		t.Fatal("statusWord")
	}
}

func TestFilesMachineGoesAway(t *testing.T) {
	m, _, fv := filesFixture(t)
	m.machines[0].c = nil // offline mid-browse
	a1Key(t, m, runes("r"))
	if fv.err == "" {
		t.Fatal("r on an offline machine said nothing")
	}
	out := ansi.Strip(strings.Join(fv.render(*m, 80, 10), "\n"))
	if !strings.Contains(out, fv.err) {
		t.Fatalf("render:\n%s", out)
	}
	// Answers that were on their way still land harmlessly.
	m.Update(filesListMsg{machine: localMachine, root: fv.root, rel: "gone", err: errString("closed")})
	m.Update(filesReadMsg{machine: localMachine, root: fv.root, rel: "x", err: errString("closed")})
	// Reading ahead or sending to an agent says it is offline.
	fv.err = ""
	filesSelect(t, fv, "main.go")
	if cmd := fv.read(m, "main.go"); cmd != nil || fv.prev.err == "" {
		t.Fatalf("read offline: %+v", fv.prev)
	}
	a1Key(t, m, runes("a"))
	if !m.flashIsErr {
		t.Fatal("a offline")
	}
}

func TestFilesPreviewTooOld(t *testing.T) {
	m, _, fv := filesFixture(t, proto.CapFSFiles) // lists, but cannot read
	filesSelect(t, fv, "main.go")
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	out := ansi.Strip(strings.Join(fv.render(*m, 80, 10), "\n"))
	if !strings.Contains(out, "too old to preview files") {
		t.Fatalf("render:\n%s", out)
	}
}
