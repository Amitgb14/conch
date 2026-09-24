package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

// x on a branch whose worktree was already removed used to say only linked
// worktrees can be removed, leaving no way to be rid of the branch.
func TestA2RemoveBranchWithoutWorktree(t *testing.T) {
	m := a2Model()
	m.machines[0].c = a2Client("session.v1", harvestCapability)
	m.showAll[scoped(localMachine, "r1")] = true // feat is behind "more" otherwise
	m.rebuild()
	// feat has a worktree: x removes that, and says the branch stays.
	m.cursor = branchNodeID(localMachine, "r1", "feat")
	if cmd := m.openRemove(); cmd != nil {
		t.Fatal("removing a worktree should only confirm")
	}
	d, ok := m.overlay.(*dialog)
	if !ok || !strings.Contains(strings.Join(d.text, " ")+d.title, "worktree") {
		t.Fatalf("worktree confirm: %#v", m.overlay)
	}
	m.overlay = nil

	// The same branch with its worktree gone: x offers to delete the branch.
	m.machines[0].projects[0].Worktrees = []proto.WorktreeInfo{{Path: "/src/api", Branch: "main", Main: true}}
	m.rebuild()
	m.cursor = branchNodeID(localMachine, "r1", "feat")
	cmd := m.openRemove()
	if cmd == nil {
		t.Fatal("a branch with no worktree should still be removable")
	}
	if m.flash != "" {
		t.Fatalf("unexpected flash: %q", m.flash)
	}

	// The base branch is never offered.
	m.flash = ""
	m.cursor = branchNodeID(localMachine, "r1", "main")
	if cmd := m.openRemove(); cmd != nil {
		t.Fatal("the base branch should not be discarded")
	}
	if !strings.Contains(m.flash, "base branch") {
		t.Fatalf("flash: %q", m.flash)
	}

	// A server too old to discard says so instead of doing nothing.
	m.machines[0].c = a2Client("session.v1")
	m.flash = ""
	m.cursor = branchNodeID(localMachine, "r1", "feat")
	if cmd := m.openRemove(); cmd != nil {
		t.Fatal("no discard without the capability")
	}
	if !strings.Contains(m.flash, "too old") {
		t.Fatalf("flash: %q", m.flash)
	}
}

// The branch menu offers the same thing the key does.
func TestA2BranchMenuFollowsTheWorktree(t *testing.T) {
	m := a2Model()
	m.machines[0].c = a2Client(harvestCapability)
	r := row{kind: kindBranch, machine: localMachine, projectID: "r1", branch: "feat"}
	labels := func() string {
		var out []string
		for _, it := range newRowMenu(*m, r, 0, 0).items {
			out = append(out, it.label)
		}
		return strings.Join(out, " | ")
	}
	if got := labels(); !strings.Contains(got, "Remove worktree") {
		t.Fatalf("with a worktree: %s", got)
	}
	m.machines[0].projects[0].Worktrees = []proto.WorktreeInfo{{Path: "/src/api", Branch: "main", Main: true}}
	if got := labels(); !strings.Contains(got, "Delete branch") || strings.Contains(got, "Remove worktree") {
		t.Fatalf("without a worktree: %s", got)
	}
	// Nothing to delete on the base branch.
	r.branch = "main"
	if got := labels(); strings.Contains(got, "Delete branch") {
		t.Fatalf("base branch: %s", got)
	}
}

// A new tab starts a shell; when the shell never starts (the machine went
// away) the tab goes with it, instead of sitting there empty for good.
func TestA2FailedTabCloses(t *testing.T) {
	m := a2Model()
	m.rebuild()
	m.cursor = paneNodeID(localMachine, "p2") // a running terminal
	for _, r := range m.rows {
		if r.id == m.cursor {
			a2Run(m.show(r))
		}
	}
	before := len(m.tabs)
	m.newTab(viewRef{})
	if len(m.tabs) != before+1 {
		t.Fatalf("no tab opened: %d", len(m.tabs))
	}
	if !m.tab().focused().await {
		t.Fatal("a tab waiting for its shell should say so")
	}
	next, _ := m.Update(errMsg{errString("machine is offline")})
	*m = next.(Model)
	if len(m.tabs) != before {
		t.Fatalf("the empty tab stayed: %d tabs", len(m.tabs))
	}
	if !strings.Contains(m.flash, "offline") {
		t.Fatalf("the error should still be shown: %q", m.flash)
	}
}

// The pane arriving clears the mark, so a later error leaves the tab alone.
func TestA2ArrivedKeepsTheTab(t *testing.T) {
	m := a2Model()
	m.rebuild()
	m.cursor = paneNodeID(localMachine, "p2")
	m.newTab(viewRef{})
	m.tab().focused().await = true
	info := proto.PaneInfo{ID: "p9", Name: "zsh", State: proto.PaneRunning}
	next, _ := m.Update(createdMsg{machine: localMachine, info: info})
	*m = next.(Model)
	before := len(m.tabs)
	next, _ = m.Update(errMsg{errString("something else went wrong")})
	*m = next.(Model)
	if len(m.tabs) != before {
		t.Fatalf("a tab with a pane should stay: %d, want %d", len(m.tabs), before)
	}
}

// The last tab is never closed, however it failed: conch always has one.
func TestA2FailedTabKeepsTheLastOne(t *testing.T) {
	m := a2Model()
	m.rebuild()
	m.tabs = m.tabs[:0]
	m.newTab(viewRef{})
	m.tab().focused().await = true
	if len(m.tabs) != 1 {
		t.Fatalf("want one tab, got %d", len(m.tabs))
	}
	next, _ := m.Update(errMsg{errString("offline")})
	*m = next.(Model)
	if len(m.tabs) != 1 {
		t.Fatalf("the last tab was closed: %d", len(m.tabs))
	}
	if m.tabs[0].focused().await {
		t.Fatal("the mark should be cleared once it is given up on")
	}
}

// A split half that never gets its pane closes too, leaving the other half.
func TestA2FailedSplitCloses(t *testing.T) {
	m := a2Model()
	m.rebuild()
	m.cursor = paneNodeID(localMachine, "p2")
	m.newTab(viewRef{Row: paneNodeID(localMachine, "p2"), Kind: kindPane, Machine: localMachine, PaneID: "p2"})
	t.Cleanup(func() { m.tabs = nil })
	m.split(splitRight, viewRef{})
	if n := len(m.tab().root.leaves()); n != 2 {
		t.Fatalf("want two halves, got %d", n)
	}
	next, _ := m.Update(errMsg{errString("offline")})
	*m = next.(Model)
	if n := len(m.tab().root.leaves()); n != 1 {
		t.Fatalf("the empty half stayed: %d halves", n)
	}
}

// ctrl+b c opens a terminal wherever the tree is pointing. It used to leave
// an empty tab when there was no pane beside it — and the tab bar, which
// lists only the selected group's tabs, hid them until a pane brought that
// group on screen, so half a dozen appeared at once.
func TestA2NewTabAlwaysStartsAShell(t *testing.T) {
	m := a2Model()
	m.rebuild()
	m.cursor = branchNodeID(localMachine, "r1", "main") // a tree row, not a pane
	m.tabs = m.tabs[:0]
	if cmd := m.newShellTab(); cmd == nil {
		t.Fatal("want a shell started with the new tab")
	}
	if len(m.tabs) != 1 {
		t.Fatalf("%d tabs, want 1", len(m.tabs))
	}
	l := m.tab().focused()
	if !l.view.empty() {
		t.Fatalf("the tab should be waiting for its pane: %+v", l.view)
	}
	if !l.await {
		t.Fatal("a tab waiting for a shell has to be marked, or it lingers empty")
	}

	// Three in a row: three shells on the way, no tab left behind.
	m.newShellTab()
	m.newShellTab()
	if len(m.tabs) != 3 {
		t.Fatalf("%d tabs, want 3", len(m.tabs))
	}
	for i, tab := range m.tabs {
		if !tab.focused().await {
			t.Fatalf("tab %d is not waiting for anything", i+1)
		}
	}
	// And each failure takes one away, rather than leaving them to pile up.
	for want := 2; want >= 1; want-- {
		next, _ := m.Update(errMsg{errString("offline")})
		*m = next.(Model)
		if len(m.tabs) != want {
			t.Fatalf("after a failure: %d tabs, want %d", len(m.tabs), want)
		}
	}
}

// The + menu's "Empty tab" still means an empty tab: it is the one place
// that asks for one.
func TestA2EmptyTabStaysAvailable(t *testing.T) {
	m := a2Model()
	m.rebuild()
	m.tabs = m.tabs[:0]
	if cmd := m.newTab(viewRef{}); cmd == nil {
		t.Fatal("newTab should still focus the tab it made")
	}
	if len(m.tabs) != 1 || !m.tab().focused().view.empty() {
		t.Fatalf("want one empty tab, got %d", len(m.tabs))
	}
	if m.tab().focused().await {
		t.Fatal("an empty tab asked for on purpose is not waiting for a pane")
	}
}

// Warn puts a message on screen as the TUI starts, so settings that could
// not be read are visible instead of stopping conch.
func TestA2WarnOnStart(t *testing.T) {
	m := a2Model().Warn("settings not read (line 1); using the defaults")
	var shown string
	for _, msg := range a2Run(m.Init()) {
		if e, ok := msg.(errMsg); ok {
			shown = e.err.Error()
		}
	}
	if !strings.Contains(shown, "settings not read") {
		t.Fatalf("the warning never arrived: %q", shown)
	}
	next, _ := m.Update(errMsg{errString(shown)})
	after := next.(Model)
	if !strings.Contains(after.flash, "settings not read") || !after.flashIsErr {
		t.Fatalf("flash %q (error: %v)", after.flash, after.flashIsErr)
	}
	// Nothing to say: nothing is shown.
	quiet := a2Model().Warn("")
	for _, msg := range a2Run(quiet.Init()) {
		if _, ok := msg.(errMsg); ok {
			t.Fatal("a model with no warning should not flash one")
		}
	}
}

// Scrolling used to throw a mouse selection away, so text could only ever
// be selected within one screenful. The selection now moves with the text.
func TestA2SelectionSurvivesScrolling(t *testing.T) {
	for _, c := range []struct {
		what  string
		sel   selection
		wantA int
		wantB int
	}{
		{"finished mouse selection moves whole", selection{paneID: "p1", ay: 2, by: 4, hasContent: true}, 5, 7},
		{"one still being dragged keeps its head at the mouse", selection{paneID: "p1", ay: 2, by: 4, dragging: true}, 5, 4},
		{"a keyboard one keeps its head at the cursor", selection{paneID: "p1", ay: 2, by: 4, keyboard: true}, 5, 4},
	} {
		m := a2Model()
		m.viewing, m.viewMachine = "p1", localMachine
		fake, _ := a1FakeClient(t, "pane.scroll.v1") // one that is read: scrolling notifies
		m.machines[0].c = fake
		m.frame = &proto.Frame{History: 100, Lines: make([]string, 10)}
		sel := c.sel
		m.sel = &sel
		m.scrollPane(3)
		m.scrollPane(0) // no movement, no change
		if m.sel == nil {
			t.Fatalf("%s: the selection was dropped", c.what)
		}
		if m.sel.ay != c.wantA || m.sel.by != c.wantB {
			t.Fatalf("%s: got ay=%d by=%d, want %d and %d", c.what, m.sel.ay, m.sel.by, c.wantA, c.wantB)
		}
	}
}

// While text is being selected in a program that takes the mouse itself —
// an agent's own TUI — the wheel scrolls conch's history instead of going
// to the program, so a selection can be dragged past the screen.
func TestA2WheelScrollsWhileSelecting(t *testing.T) {
	m := a2Model()
	m.viewing, m.viewMachine = "p1", localMachine
	c, peer := a1FakeClient(t, "pane.scroll.v1")
	m.machines[0].c = c
	m.frame = &proto.Frame{Mouse: true, History: 100, Lines: make([]string, 10)}

	// No selection: the wheel belongs to the program.
	wheel := tea.MouseMsg{Button: tea.MouseButtonWheelUp, Action: tea.MouseActionPress}
	a2Run(m.paneMouse("p1", wheel, 1, 1, false, true))
	if m.offset != 0 {
		t.Fatalf("scrolled without a selection: offset %d", m.offset)
	}
	peer.waitMethod(t, proto.MethodPaneSendMouse, "")

	// Selecting, in a pane conch has history for: the wheel is ours.
	m.sel = &selection{paneID: "p1", ay: 1, by: 1, dragging: true}
	a2Run(m.paneMouse("p1", wheel, 1, 1, false, true))
	if m.offset == 0 {
		t.Fatal("the wheel did not scroll while selecting")
	}
	if m.sel == nil {
		t.Fatal("the selection went with it")
	}
}

// An agent runs on the alternate screen, where conch has no history to
// scroll, so the wheel has to keep going to the agent — holding on to it
// left the pane unscrollable for as long as the selection lasted.
func TestA2WheelReachesAnAgentAfterSelecting(t *testing.T) {
	m := a2Model()
	m.viewing, m.viewMachine = "p1", localMachine
	c, peer := a1FakeClient(t, "pane.scroll.v1")
	m.machines[0].c = c
	m.frame = &proto.Frame{Mouse: true, AltScreen: true, History: 0, Lines: make([]string, 10)}
	m.sel = &selection{paneID: "p1", ax: 1, ay: 1, bx: 5, by: 1, hasContent: true}

	wheel := tea.MouseMsg{Button: tea.MouseButtonWheelUp, Action: tea.MouseActionPress}
	a2Run(m.paneMouse("p1", wheel, 1, 1, false, true))
	peer.waitMethod(t, proto.MethodPaneSendMouse, "")
	if m.offset != 0 {
		t.Fatalf("there is no history to scroll here: offset %d", m.offset)
	}
	if m.sel != nil {
		t.Fatal("the selection should go: the text under it has moved")
	}
}

// Reading a long answer out of an agent: the pane can only ever show a
// screenful, so Y opens the conversation the agent saves, to scroll through
// and copy from.
func TestA2OpenTranscript(t *testing.T) {
	m := a2Model()
	m.width, m.height = 100, 30
	c, peer := a1FakeClient(t, "session.v1", proto.CapSessionHandoff)
	m.machines[0].c = c
	sess := proto.SessionInfo{Agent: "claude", ID: "c1", Dir: "/src/api", Title: "Refactor auth", PaneID: "p1"}
	peer.setResult(proto.MethodSessionExport, proto.SessionExport{Name: "claude-c1.md", Doc: "first line\nsecond line\nthird line"})

	cmd := m.openTranscript(localMachine, sess)
	v, ok := m.overlay.(*transcriptView)
	if !ok {
		t.Fatalf("no conversation view: %T (%q)", m.overlay, m.flash)
	}
	if !v.loading {
		t.Fatal("it should say it is reading")
	}
	for _, msg := range a2Run(cmd) {
		if tm, ok := msg.(transcriptMsg); ok {
			v.receive(tm)
		}
	}
	if v.loading || v.err != "" {
		t.Fatalf("after reading: loading=%v err=%q", v.loading, v.err)
	}
	if out := a2Plain(v.render(*m).lines); !strings.Contains(out, "second line") || !strings.Contains(out, "Refactor auth") {
		t.Fatalf("the conversation is not shown:\n%s", out)
	}

	// A conversation with nothing in it, and a request that fails.
	v2 := &transcriptView{}
	v2.receive(transcriptMsg{doc: "  \n "})
	if v2.err == "" {
		t.Fatal("an empty conversation should say so")
	}
	v3 := &transcriptView{}
	v3.receive(transcriptMsg{err: errString("no claude session c9")})
	if !strings.Contains(v3.err, "no claude session") {
		t.Fatalf("error: %q", v3.err)
	}

	// A run that saved nothing, and a server too old to read one.
	m.flash, m.overlay = "", nil
	if cmd := m.openTranscript(localMachine, proto.SessionInfo{Agent: "claude"}); cmd != nil || m.overlay != nil {
		t.Fatal("no ID: nothing to read")
	}
	if !strings.Contains(m.flash, "saved no conversation") {
		t.Fatalf("flash %q", m.flash)
	}
	m.flash = ""
	m.machines[0].c = a2Client("session.v1")
	if cmd := m.openTranscript(localMachine, sess); cmd != nil || m.overlay != nil {
		t.Fatal("an older server should not be asked")
	}
	if !strings.Contains(m.flash, "too old") {
		t.Fatalf("flash %q", m.flash)
	}
}

// Scrolling and selecting in the conversation: dragging past the bottom
// keeps going, and what is released is what gets copied.
func TestA2TranscriptSelectAndScroll(t *testing.T) {
	m := a2Model()
	m.width, m.height = 100, 20
	v := &transcriptView{title: "long one"}
	var b strings.Builder
	for i := 0; i < 200; i++ {
		b.WriteString("line " + itoa(i) + "\n")
	}
	v.receive(transcriptMsg{doc: b.String()})
	box := v.render(*m)
	if len(v.lines) < 200 || v.rows < 3 {
		t.Fatalf("laid out %d lines, %d rows", len(v.lines), v.rows)
	}

	// It opens on the newest part: that is what you came to read.
	if v.off != v.maxOff() {
		t.Fatalf("opened at %d, want the end (%d)", v.off, v.maxOff())
	}
	// g and G jump to either end, however far away they are.
	v.update(m, a2Key("g"))
	if v.off != 0 {
		t.Fatalf("g: %d, want the top", v.off)
	}
	v.update(m, a2Key("G"))
	if v.off != v.maxOff() {
		t.Fatalf("G: %d, want the end (%d)", v.off, v.maxOff())
	}
	v.update(m, a2Key("home"))
	if v.off != 0 {
		t.Fatalf("home: %d", v.off)
	}
	v.update(m, a2Key("end"))
	if v.off != v.maxOff() {
		t.Fatalf("end: %d", v.off)
	}

	// The wheel scrolls, and stops at either end.
	v.off = 0
	v.mouse(m, tea.MouseMsg{Button: tea.MouseButtonWheelUp}, box)
	if v.off != 0 {
		t.Fatalf("scrolled above the top: %d", v.off)
	}
	v.mouse(m, tea.MouseMsg{Button: tea.MouseButtonWheelDown}, box)
	if v.off != 3 {
		t.Fatalf("wheel down: %d", v.off)
	}
	for i := 0; i < 500; i++ {
		v.mouse(m, tea.MouseMsg{Button: tea.MouseButtonWheelDown}, box)
	}
	if v.off != v.maxOff() {
		t.Fatalf("scrolled past the end: %d of %d", v.off, v.maxOff())
	}
	// Paging moves by a screen, and stops at the ends too.
	v.off = v.maxOff()
	v.update(m, a2Key("pgup"))
	if v.off != v.maxOff()-max(v.rows-1, 1) {
		t.Fatalf("pgup: %d of %d", v.off, v.maxOff())
	}
	for i := 0; i < 500; i++ {
		v.update(m, a2Key("pgup"))
	}
	if v.off != 0 {
		t.Fatalf("paging up past the top: %d", v.off)
	}

	// Drag from the first line downwards, past the bottom: it keeps going.
	v.off = 0
	press := tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: box.x + 1, Y: box.y + 1}
	v.mouse(m, press, box)
	if v.sel == nil || !v.sel.dragging {
		t.Fatal("pressing should start a selection")
	}
	below := tea.MouseMsg{Action: tea.MouseActionMotion, X: box.x + 6, Y: box.y + 1 + v.rows + 2}
	for i := 0; i < 5; i++ {
		v.mouse(m, below, box)
	}
	if v.off == 0 {
		t.Fatal("dragging below the text should scroll it")
	}
	if !v.sel.hasContent {
		t.Fatal("the selection should have grown")
	}
	release := tea.MouseMsg{Action: tea.MouseActionRelease, X: box.x + 6, Y: box.y + 1 + v.rows - 1}
	cmd := v.mouse(m, release, box)
	if cmd == nil {
		t.Fatal("releasing should copy what was selected")
	}
	var copied string
	for _, msg := range a2Run(cmd) {
		if f, ok := msg.(flashMsg); ok {
			copied = string(f)
		}
	}
	if !strings.Contains(copied, "copied") {
		t.Fatalf("copy flash: %q", copied)
	}

	// a copies the whole conversation; esc closes without copying.
	m.overlay = v
	closed, cmd := v.update(m, a2Key("a"))
	if !closed || cmd == nil {
		t.Fatal("a should copy everything and close")
	}
	m.overlay = v
	if closed, cmd := v.update(m, a2Key("esc")); !closed || cmd != nil {
		t.Fatal("esc should close without copying")
	}
	// y with nothing selected says what to do rather than copying nothing.
	v.sel, m.flash, m.overlay = nil, "", v
	if closed, cmd := v.update(m, a2Key("y")); closed || cmd != nil {
		t.Fatal("y with no selection should not copy")
	}
	if !strings.Contains(m.flash, "drag over the text") {
		t.Fatalf("flash %q", m.flash)
	}
}

// Y in the tree opens the conversation of the agent on that row.
func TestA2OpenTranscriptFromTheTree(t *testing.T) {
	m := a2Model()
	m.rebuild()
	c, peer := a1FakeClient(t, "session.v1", proto.CapSessionHandoff)
	m.machines[0].c = c
	peer.setResult(proto.MethodSessionExport, proto.SessionExport{Name: "c.md", Doc: "a long answer"})

	// A terminal has no conversation; nor has an agent whose sessions are
	// not read yet, which asks for them.
	if cmd := m.copyAgentConversation(row{kind: kindPane, machine: localMachine, paneID: "p2"}); cmd != nil {
		t.Fatal("a shell has no conversation")
	}
	if !strings.Contains(m.flash, "no agent") {
		t.Fatalf("flash %q", m.flash)
	}
	agentRow := row{kind: kindPane, machine: localMachine, paneID: "p1"}
	m.flash = ""
	_ = m.copyAgentConversation(agentRow)
	if !strings.Contains(m.flash, "no saved conversation") {
		t.Fatalf("flash %q", m.flash)
	}
	// With the session known, it copies.
	m.sessions = map[string]*sessionsData{sessionsKey(localMachine, "r1"): {list: []proto.SessionInfo{
		{Agent: "claude", ID: "c1", Dir: "/src/api", PaneID: "p1"},
	}}}
	cmd := m.copyAgentConversation(agentRow)
	v, ok := m.overlay.(*transcriptView)
	if !ok {
		t.Fatalf("no conversation view: %T (%q)", m.overlay, m.flash)
	}
	for _, msg := range a2Run(cmd) {
		if tm, ok := msg.(transcriptMsg); ok {
			v.receive(tm)
		}
	}
	if v.err != "" || !strings.Contains(v.doc, "a long answer") {
		t.Fatalf("view: err=%q doc=%q", v.err, v.doc)
	}
	// Any other row says what to select.
	m.flash, m.overlay = "", nil
	if cmd := m.copyAgentConversation(row{kind: kindBranch, machine: localMachine, branch: "feat"}); cmd != nil {
		t.Fatal("a branch has no conversation")
	}
	if !strings.Contains(m.flash, "select an agent") {
		t.Fatalf("flash %q", m.flash)
	}
}

// The conversation is drawn inside its box at any terminal size, and what a
// conversation holds — people paste terminal output into agents — never
// reaches the screen as escapes: drawn as they are, the terminal obeys them
// and the display repeats rows and jumps about.
func TestA2TranscriptFitsAndIsPlain(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {100, 30}, {80, 24}, {60, 20}, {40, 12}, {30, 8}, {20, 5}} {
		m := a2Model()
		m.width, m.height = size[0], size[1]
		v := &transcriptView{title: "a rather long conversation title that will not fit"}
		var b strings.Builder
		for i := 0; i < 50; i++ {
			b.WriteString(strings.Repeat("x", i*7) + " word " + strings.Repeat("y", i) + "\n")
		}
		b.WriteString("✅ done ⏵⏵ auto mode · 日本語のテキストがここにあります · " + strings.Repeat("→", 60) + "\n")
		b.WriteString("\tindented\twith\ttabs " + strings.Repeat("─", 120) + "\n")
		b.WriteString(strings.Repeat("🎉", 80) + "\n")
		// What a conversation really holds: pasted terminal output.
		b.WriteString("\x1b[<64;23;4M mouse codes \x1b[2J\x1b[H cleared \x1b[31mred\x1b[0m\r overwritten\n")
		b.WriteString("bell \a backspace \b and \x9b a C1 sequence\n")
		v.receive(transcriptMsg{doc: b.String()})
		box := v.render(*m)
		for _, l := range box.lines {
			if strings.ContainsAny(ansi.Strip(l), "\x1b\a\b\r") {
				t.Fatalf("%dx%d: a control character reached the screen: %q", size[0], size[1], l)
			}
		}
		want := ansi.StringWidth(box.lines[0])
		for i, l := range box.lines {
			if got := ansi.StringWidth(l); got != want {
				t.Errorf("%dx%d line %d is %d wide, want %d: %q", size[0], size[1], i, got, want, ansi.Strip(l))
			}
		}
		if want > size[0] {
			t.Errorf("%dx%d: the box is %d wide, wider than the screen", size[0], size[1], want)
		}
		if len(box.lines) > size[1] {
			t.Errorf("%dx%d: the box is %d tall, taller than the screen", size[0], size[1], len(box.lines))
		}
		// With a selection over it, too.
		v.sel = &selection{ax: 2, ay: 1, bx: 40, by: 6, hasContent: true}
		for i, l := range v.render(*m).lines {
			if got := ansi.StringWidth(l); got != want {
				t.Errorf("selected %dx%d line %d is %d wide, want %d", size[0], size[1], i, got, want)
			}
		}
	}
}

// The conversation view's own handling: keys, the message it waits for, and
// the states it can be in before there is anything to show.
func TestA2TranscriptKeysAndStates(t *testing.T) {
	m := a2Model()
	m.width, m.height = 100, 30
	v := &transcriptView{title: "keys", loading: true}
	m.overlay = v

	// While it is reading, the view says so and has nothing to scroll.
	if out := a2Plain(v.render(*m).lines); !strings.Contains(out, "reading the conversation") {
		t.Fatalf("loading:\n%s", out)
	}
	if v.position() != "" || v.mouse(m, tea.MouseMsg{Button: tea.MouseButtonWheelDown}, box{}) != nil {
		t.Fatal("nothing to scroll before it has arrived")
	}
	// The message reaches it through update, as it does in the model.
	if closed, cmd := v.update(m, transcriptMsg{doc: strings.Repeat("a line\n", 200)}); closed || cmd != nil {
		t.Fatal("the conversation arriving should not close the view")
	}
	v.render(*m)

	// Every way of moving.
	v.off = 50
	for _, c := range []struct {
		key  string
		want int
	}{{"up", 49}, {"k", 48}, {"down", 49}, {"j", 50}} {
		v.update(m, a2Key(c.key))
		if v.off != c.want {
			t.Fatalf("%s: %d, want %d", c.key, v.off, c.want)
		}
	}
	page := max(v.rows-1, 1)
	v.off = 100
	v.update(m, a2Key("pgdown"))
	if v.off != 100+page {
		t.Fatalf("pgdown: %d", v.off)
	}
	// An unknown key does nothing at all.
	before := v.off
	if closed, cmd := v.update(m, a2Key("Z")); closed || cmd != nil || v.off != before {
		t.Fatalf("an unknown key moved something: %d", v.off)
	}
	// And a message that is not for it is ignored.
	if closed, cmd := v.update(m, tickMsg{}); closed || cmd != nil {
		t.Fatal("a tick should pass it by")
	}

	// A selection is drawn picked out, and y copies it.
	v.off = 0
	v.sel = &selection{ax: 0, ay: 0, bx: 4, by: 1, hasContent: true}
	if out := v.render(*m).lines[1]; !strings.Contains(out, "\x1b[7m") && !strings.Contains(out, "\x1b[") {
		t.Fatalf("the selection is not marked: %q", out)
	}
	m.overlay = v
	closed, cmd := v.update(m, a2Key("y"))
	if !closed || cmd == nil || m.overlay != nil {
		t.Fatal("y should copy the selection and close")
	}
}

// The awkward corners of dragging: pressing outside the text, dragging above
// the top, and letting go without having moved.
func TestA2TranscriptDragCorners(t *testing.T) {
	m := a2Model()
	m.width, m.height = 100, 24
	v := &transcriptView{}
	v.receive(transcriptMsg{doc: strings.Repeat("some text here\n", 100)})
	b := v.render(*m)

	// Pressing below the last row starts nothing.
	v.mouse(m, tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft,
		X: b.x + 2, Y: b.y + 1 + v.rows + 3}, b)
	if v.sel != nil {
		t.Fatal("a press outside the text should not select")
	}
	// Pressing and letting go without moving selects nothing.
	press := tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: b.x + 2, Y: b.y + 2}
	v.mouse(m, press, b)
	if v.sel == nil {
		t.Fatal("a press in the text should start a selection")
	}
	if cmd := v.mouse(m, tea.MouseMsg{Action: tea.MouseActionRelease, X: b.x + 2, Y: b.y + 2}, b); cmd != nil {
		t.Fatal("letting go without moving should copy nothing")
	}
	if v.sel != nil {
		t.Fatal("and should leave no selection behind")
	}
	// Dragging above the first row scrolls back.
	v.off = 40
	v.mouse(m, press, b)
	v.mouse(m, tea.MouseMsg{Action: tea.MouseActionMotion, X: b.x + 2, Y: b.y - 3}, b)
	if v.off != 39 {
		t.Fatalf("dragging above the text: %d, want 39", v.off)
	}
	if !v.sel.hasContent {
		t.Fatal("the selection should have grown upwards")
	}
}

// Opening one that cannot be read: an offline machine, and a server that
// answers with an error.
func TestA2TranscriptCannotBeRead(t *testing.T) {
	m := a2Model()
	m.machines[0].c = nil
	if cmd := m.openTranscript(localMachine, proto.SessionInfo{Agent: "claude", ID: "c1"}); cmd != nil {
		t.Fatal("an offline machine has nothing to read")
	}
	if m.flash == "" {
		t.Fatal("it should say why")
	}

	c, peer := a1FakeClient(t, "session.v1", proto.CapSessionHandoff)
	m.machines[0].c = c
	peer.setError(proto.MethodSessionExport, "no claude session c1 in /src/api")
	cmd := m.openTranscript(localMachine, proto.SessionInfo{Agent: "claude", ID: "c1", Dir: "/src/api"})
	v := m.overlay.(*transcriptView)
	for _, msg := range a2Run(cmd) {
		if tm, ok := msg.(transcriptMsg); ok {
			v.receive(tm)
		}
	}
	if !strings.Contains(v.err, "no claude session") {
		t.Fatalf("error not shown: %q", v.err)
	}
	if out := a2Plain(v.render(*m).lines); !strings.Contains(out, "no claude session") {
		t.Fatalf("the error should be on screen:\n%s", out)
	}
}

// plainText on its own: what a conversation can hold and what may be drawn.
func TestA2PlainText(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"plain", "plain"},
		{"\x1b[31mred\x1b[0m", "red"},
		{"one\ntwo", "one\ntwo"},
		{"keep\ttabs", "keep\ttabs"},
		{"over\rwritten", "overwritten"},
		{"bell\a and \bbackspace", "bell and backspace"},
		{"\x9b31m C1 introducer", " C1 introducer"}, // a real C1 sequence, taken whole
		{"\x00\x01\x02null and friends", "null and friends"},
		{"", ""},
		{"日本語 ✅ stays", "日本語 ✅ stays"},
	} {
		if got := plainText(c.in); got != c.want {
			t.Errorf("plainText(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// sessionOfPane: no sessions read yet, no pane, and nothing matching.
func TestA2SessionOfPane(t *testing.T) {
	m := a2Model()
	if _, ok := m.sessionOfPane(localMachine, "r1", "p1"); ok {
		t.Fatal("nothing is loaded yet")
	}
	m.sessions = map[string]*sessionsData{sessionsKey(localMachine, "r1"): {list: []proto.SessionInfo{
		{Agent: "claude", ID: "c1", PaneID: "p9"},
		{Agent: "codex", ID: "c2"},
	}}}
	if _, ok := m.sessionOfPane(localMachine, "r1", ""); ok {
		t.Fatal("no pane, no conversation")
	}
	if _, ok := m.sessionOfPane(localMachine, "r1", "p1"); ok {
		t.Fatal("no session claims that pane")
	}
	s, ok := m.sessionOfPane(localMachine, "r1", "p9")
	if !ok || s.ID != "c1" {
		t.Fatalf("got %+v %v", s, ok)
	}
}
