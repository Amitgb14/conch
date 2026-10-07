package tui

import (
	"encoding/json"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

// gitPanelModel is harvestModel with a server that runs git from the panel
// and prompts agents: feat is checked out at /src/api-feat with claude (p1)
// working in it.
func gitPanelModel(t *testing.T, caps ...string) (*Model, *a1Peer) {
	t.Helper()
	if len(caps) == 0 {
		caps = []string{harvestCapability, proto.CapBranchGit, proto.CapAgentPrompt, proto.CapBranchRebase}
	}
	return harvestModel(t, caps...)
}

// openPanel opens feat's panel with b on its row and returns it with what
// the opening sent.
func openPanel(t *testing.T, m *Model) (*gitPanel, []tea.Msg) {
	t.Helper()
	a1At(t, m, branchNodeID(localMachine, "r1", "feat"))
	msgs := a2Run(a1Key(t, m, runes("b")))
	g, ok := m.overlay.(*gitPanel)
	if !ok {
		t.Fatalf("b opened %T", m.overlay)
	}
	return g, msgs
}

// gitParams decodes the newest branch.git call whose params contain want.
func gitParams(t *testing.T, peer *a1Peer, want ...string) proto.BranchGitParams {
	t.Helper()
	return lastParams[proto.BranchGitParams](t, peer, proto.MethodBranchGit, want...)
}

// hitAt is where on the screen the panel drew the first span of kind that
// match accepts.
func hitAt(t *testing.T, m *Model, g *gitPanel, kind gitHitKind, match func(gitHit) bool) (x, y int) {
	t.Helper()
	b := g.render(*m)
	for _, h := range g.hits {
		if h.kind == kind && match(h) {
			return b.x + 1 + h.x0, b.y + 1 + h.line
		}
	}
	t.Fatalf("no hit of kind %d in %+v", kind, g.hits)
	return 0, 0
}

// actionAt shows the actions and finds the one with key.
func actionAt(t *testing.T, m *Model, g *gitPanel, key string) (int, int) {
	t.Helper()
	g.showActions(true)
	return hitAt(t, m, g, hitAction, func(h gitHit) bool { return h.actionsKey == key })
}

// runAction runs the action with key as the keyboard does: tab, then its key.
func runAction(t *testing.T, m *Model, key string) []tea.Msg {
	t.Helper()
	a1Key(t, m, a2Key("tab"))
	return a2Run(a1Key(t, m, runes(key)))
}

func panelText(m *Model, g *gitPanel) string {
	return ansi.Strip(strings.Join(g.render(*m).lines, "\n"))
}

// A click on a branch in the tree shows its changes as before and opens
// the command window in the middle of the screen, asking the worktree for
// its status. A click on the branch does not: it shows the changes, and the
// window is a key away, since opening a window over the right-hand side is
// more than one click on a row should do.
func TestGitPanelOpensOnKey(t *testing.T) {
	m, peer := gitPanelModel(t)
	i := indexOfRow(m.rows, branchNodeID(localMachine, "r1", "feat"))
	if i < 0 {
		t.Fatalf("no feat row: %s", render(m.rows))
	}
	y := i - m.scroll + 2
	a2Run(a1Mouse(t, m, 8, y, tea.MouseButtonLeft, tea.MouseActionPress)) // shows its changes
	if m.overlay != nil {
		t.Fatalf("a click on feat opened %T", m.overlay)
	}
	msgs := a2Run(a1Key(t, m, runes("b")))
	g, ok := m.overlay.(*gitPanel)
	if !ok || g.t != feat {
		t.Fatalf("b on feat opened %T", m.overlay)
	}
	peer.waitMethod(t, proto.MethodProjectChanges, `"branch":"feat"`) // its changes still load
	// Opening runs nothing: the files are on the changes page already. It
	// only asks whether something is left stopped.
	if p := gitParams(t, peer, `"branch":"feat"`); p.ProjectID != "r1" || len(p.Commands) != 0 {
		t.Fatalf("opening asked %+v", p)
	}
	// In the middle of the right-hand panel, clear of the tree.
	b := g.render(*m)
	a := m.mainRect()
	if b.x != a.x+(a.w-b.width())/2 || b.y != a.y+(a.h-len(b.lines))/2 || b.x < m.sidebarW || b.x+b.width() > m.width {
		t.Fatalf("not centred in %+v: %d,%d for %dx%d", a, b.x, b.y, b.width(), len(b.lines))
	}
	var done *gitPanelMsg
	for _, msg := range msgs {
		if d, ok := msg.(gitPanelMsg); ok {
			done = &d
		}
	}
	if done == nil || done.panel != g || !done.quiet {
		t.Fatalf("no quiet answer for this panel among %v", msgs)
	}

	// The window is a prompt and its output: no actions until tab.
	text := panelText(m, g)
	for _, absent := range []string{"Discard", "Stash", "Continue", "not pushed", "PR #7", "/src/api-feat", "$ git status"} {
		if strings.Contains(text, absent) {
			t.Errorf("%q shown before tab:\n%s", absent, text)
		}
	}
	for _, want := range []string{"⎇ feat", "git ❯", "tab actions", "ctrl+t → claude", "esc close"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q:\n%s", want, text)
		}
	}
}

// A right-hand panel too narrow for the window gives it the whole screen;
// zoomed, the right-hand panel is the whole screen anyway.
func TestGitPanelPlacement(t *testing.T) {
	m, _ := gitPanelModel(t)
	m.width, m.sidebarW = 70, 40 // 30 columns right of the tree
	g := &gitPanel{t: feat}
	m.overlay = g
	b := g.render(*m)
	if b.x != (m.width-b.width())/2 || b.x >= m.sidebarW || b.width() < 40 {
		t.Fatalf("narrow: at %d, %d wide on %d", b.x, b.width(), m.width)
	}
	m.width, m.zoom = 160, true
	b = g.render(*m)
	if b.x != (m.width-b.width())/2 {
		t.Fatalf("zoomed: at %d, %d wide", b.x, b.width())
	}
}

// $ opens it from the keyboard where a branch is looked at: its row in
// the tree, and its changes in the right-hand panel. The status bar says
// so in both.
func TestGitPanelKeys(t *testing.T) {
	m, _ := gitPanelModel(t)
	a1At(t, m, branchNodeID(localMachine, "r1", "feat"))
	if hints := a1HintText(m); !strings.Contains(hints, "b git") {
		t.Fatalf("tree hints: %s", hints)
	}
	a1Key(t, m, runes("$"))
	if g, ok := m.overlay.(*gitPanel); !ok || g.t != feat {
		t.Fatalf("$ in the tree opened %T", m.overlay)
	}
	m.overlay = nil

	a1Open(t, m, branchNodeID(localMachine, "r1", "feat"))
	m.focus = focusMain
	if hints := a1HintText(m); !strings.HasPrefix(hints, "$ git|") {
		t.Fatalf("changes hints: %s", hints)
	}
	a1Key(t, m, runes("$"))
	if g, ok := m.overlay.(*gitPanel); !ok || g.t != feat {
		t.Fatalf("$ in the changes opened %T", m.overlay)
	}
	// Closing it leaves the keyboard where it was.
	a1Key(t, m, a2Key("esc"))
	if m.overlay != nil || m.focus != focusMain {
		t.Fatalf("esc: %T, focus %v", m.overlay, m.focus)
	}
	// Other keys still reach the changes.
	a1Key(t, m, a2Key("down"))
	if m.overlay != nil || m.focus != focusMain {
		t.Fatal("down left the changes")
	}

	// Elsewhere $ is nothing new: on a project it opens no window.
	a1At(t, m, projectNodeID(localMachine, "r1"))
	m.focus = focusSidebar
	a1Key(t, m, runes("$"))
	if m.overlay != nil {
		t.Fatalf("$ on a project opened %T", m.overlay)
	}
}

// An answer belongs to the panel that asked: one opened since for the
// same branch doesn't get it (a command run in the first shows in the
// status bar instead), and the quiet read on opening is dropped.
func TestGitPanelAnswersGoToTheirOwnPanel(t *testing.T) {
	m, _ := gitPanelModel(t)
	first, firstOpen := openPanel(t, m)
	firstRun := a2Run(first.runGit(m, "status", [][]string{{"status"}}))
	m.overlay = nil // a click elsewhere closed it
	second, secondOpen := openPanel(t, m)
	if second == first {
		t.Fatal("the same panel")
	}
	for _, msgs := range [][]tea.Msg{firstOpen, secondOpen} {
		for _, msg := range msgs {
			if gm, ok := msg.(gitPanelMsg); ok {
				gm.res.InProgress = "merge"
				m.receiveGitPanel(gm)
			}
		}
	}
	if m.flash != "" || second.inProgress != "merge" || len(second.output) != 0 {
		t.Fatalf("opening reads: flash %q, state %q, output %q", m.flash, second.inProgress, second.output)
	}
	for _, msg := range firstRun {
		gm := msg.(gitPanelMsg)
		gm.res.Output = "$ git status\n## feat\n"
		m.receiveGitPanel(gm)
	}
	if strings.Contains(strings.Join(second.output, "\n"), "## feat") || m.flash != "git status done on feat" {
		t.Fatalf("flash %q, output %q", m.flash, second.output)
	}
	m.receiveGitPrompt(gitPromptMsg{panel: first, t: feat, to: "claude"})
	if m.flash != "sent to claude" || strings.Contains(strings.Join(second.output, "\n"), "sent to") {
		t.Fatalf("prompt answer: flash %q, output %q", m.flash, second.output)
	}
}

// A double click on a branch still opens it in the main area: the second
// click closes the panel the first opened, and is passed on.
func TestGitPanelClickOutsideClosesAndPassesThrough(t *testing.T) {
	m, _ := gitPanelModel(t)
	y := indexOfRow(m.rows, branchNodeID(localMachine, "r1", "feat")) - m.scroll + 2
	a1At(t, m, branchNodeID(localMachine, "r1", "feat"))
	a2Run(a1Key(t, m, runes("b")))
	if _, ok := m.overlay.(*gitPanel); !ok {
		t.Fatalf("b opened %T", m.overlay)
	}
	a1Mouse(t, m, 8, y, tea.MouseButtonLeft, tea.MouseActionRelease)
	cmd := a1Mouse(t, m, 8, y, tea.MouseButtonLeft, tea.MouseActionPress) // outside the panel: closes it
	if m.overlay != nil || cmd == nil {
		t.Fatalf("a click on the tree left %T open", m.overlay)
	}
	next, _ := m.Update(cmd()) // the same click, passed through
	*m = next.(Model)
	if m.overlay != nil {
		t.Fatalf("the passed-through click opened %T", m.overlay)
	}
	if m.cursor != branchNodeID(localMachine, "r1", "feat") {
		t.Fatalf("it landed on %q, want the branch it was over", m.cursor)
	}
}

func TestGitPanelOnlyForBranches(t *testing.T) {
	m, peer := gitPanelModel(t)
	a1At(t, m, projectNodeID(localMachine, "r1"))
	a1Key(t, m, runes("b"))
	if m.overlay != nil {
		t.Fatalf("b on a project opened %T", m.overlay)
	}
	// Right-click menu reaches it too.
	a1At(t, m, branchNodeID(localMachine, "r1", "feat"))
	mu := newRowMenu(*m, m.rows[indexOfRow(m.rows, m.cursor)], 0, 0)
	for i, it := range mu.items {
		if it.key == "b" {
			a2Run(mu.run(m, i))
		}
	}
	if _, ok := m.overlay.(*gitPanel); !ok {
		t.Fatalf("menu opened %T", m.overlay)
	}
	peer.waitMethod(t, proto.MethodBranchGit, `"branch":"feat"`)

	// A plain folder or an offline machine has none.
	m.overlay = nil
	m.machines[0].projects[0].Git = false
	if m.openGitPanel(feat); m.overlay != nil || !strings.Contains(m.flash, "git project") {
		t.Fatalf("plain folder: %T %q", m.overlay, m.flash)
	}
	m.machines[0].projects[0].Git = true
	m.machines[0].c = nil
	if m.openGitPanel(feat); m.overlay != nil {
		t.Fatalf("offline: %T", m.overlay)
	}
}

// Whatever is typed runs as git's arguments in the worktree: a leading
// "git" is dropped, quotes hold words together, and nothing is expanded.
func TestGitPanelTypedCommands(t *testing.T) {
	m, peer := gitPanelModel(t)
	g, _ := openPanel(t, m)
	g.running = ""

	a2Type(m, g, `git commit -m 'fix: the "quoted" bit' -- *.go`)
	msgs := a2Run(a1Key(t, m, a2Key("enter")))
	p := gitParams(t, peer, "commit")
	if want := []string{"commit", "-m", `fix: the "quoted" bit`, "--", "*.go"}; strings.Join(p.Commands[0], "|") != strings.Join(want, "|") {
		t.Fatalf("args %q", p.Commands[0])
	}
	if g.in.Value() != "" || g.running != "commit" {
		t.Fatalf("after enter: %q running %q", g.in.Value(), g.running)
	}
	if len(msgs) != 1 {
		t.Fatalf("answers %v", msgs)
	}
	if !strings.Contains(panelText(m, g), "running commit…") {
		t.Fatalf("no sign it runs:\n%s", panelText(m, g))
	}

	// One at a time.
	a2Type(m, g, "status")
	if cmd := a1Key(t, m, a2Key("enter")); cmd != nil || !strings.Contains(strings.Join(g.output, "\n"), "still running commit") {
		t.Fatalf("second command while one runs: %v %q", cmd, g.output)
	}
	g.running = ""

	// Nothing typed, only "git", or an open quote: nothing is sent.
	for _, text := range []string{"", "   ", "git", `log --grep 'open`} {
		g.in.SetValue(text)
		if cmd := a1Key(t, m, a2Key("enter")); cmd != nil {
			t.Fatalf("%q sent something", text)
		}
	}
	out := strings.Join(g.output, "\n")
	if !strings.Contains(out, "type a git command after git") || !strings.Contains(out, "quote is left open") {
		t.Fatalf("output %q", out)
	}

	// ↑ and ↓ walk back through what was typed.
	a1Key(t, m, a2Key("up"))
	if g.in.Value() != `log --grep 'open` {
		t.Fatalf("up: %q", g.in.Value())
	}
	for i := 0; i < 4; i++ { // one past the oldest stays there
		a1Key(t, m, a2Key("up"))
	}
	if !strings.HasPrefix(g.in.Value(), "git commit") {
		t.Fatalf("oldest: %q", g.in.Value())
	}
	for i := 0; i < 4; i++ {
		a1Key(t, m, a2Key("down"))
	}
	if g.in.Value() != "" {
		t.Fatalf("past the newest: %q", g.in.Value())
	}
}

// What git printed shows in the panel from its first line; a failure says
// its exit code, and something left unfinished says so and changes the
// actions.
func TestGitPanelShowsResults(t *testing.T) {
	m, _ := gitPanelModel(t)
	g, _ := openPanel(t, m)
	g.running = "rebase"
	m.receiveGitPanel(gitPanelMsg{panel: g, t: feat, label: "rebase", res: proto.BranchGitResult{
		Output:     "$ git rebase main\nCONFLICT (content): Merge conflict in a.txt\r\n",
		ExitCode:   1,
		InProgress: "rebase",
	}})
	text := panelText(m, g)
	for _, want := range []string{"$ git rebase main", "CONFLICT (content): Merge conflict in a.txt", "exit 1",
		"rebase stopped · tab to continue, abort or hand it to the agent"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in\n%s", want, text)
		}
	}
	if g.running != "" {
		t.Fatal("still running")
	}
	g.showActions(true)
	text = panelText(m, g)
	for _, want := range []string{"c  Continue rebase", "a  Abort rebase", "x  Ask claude to resolve", "s  Status"} {
		if !strings.Contains(text, want) {
			t.Errorf("actions: missing %q in\n%s", want, text)
		}
	}
	if strings.Contains(text, "CONFLICT") {
		t.Error("the actions are drawn over the output, not beside it")
	}
	g.showActions(false)

	// Once it is continued or aborted the notice goes.
	m.receiveGitPanel(gitPanelMsg{panel: g, t: feat, label: "rebase", res: proto.BranchGitResult{Output: "$ git rebase --abort\n"}})
	if text = panelText(m, g); strings.Contains(text, "rebase stopped") {
		t.Fatalf("notice stayed:\n%s", text)
	}

	// Progress redrawn with \r keeps its last state; colours are dropped.
	g.add("Receiving objects:  10%\rReceiving objects: 100%, done.\n\x1b[31mred\x1b[0m\ttab")
	n := len(g.output)
	if g.output[n-2] != "Receiving objects: 100%, done." || g.output[n-1] != "red    tab" {
		t.Fatalf("cleaned %q", g.output[n-2:])
	}

	// A cut-off answer says so; the output keeps a bounded number of lines.
	m.receiveGitPanel(gitPanelMsg{panel: g, t: feat, label: "log", res: proto.BranchGitResult{Output: strings.Repeat("line\n", 800), Truncated: true}})
	if len(g.output) != gitPanelKeep || !strings.Contains(g.output[len(g.output)-1], "cut off") {
		t.Fatalf("kept %d lines, last %q", len(g.output), g.output[len(g.output)-1])
	}

	// An error from the call itself shows in the panel.
	m.receiveGitPanel(gitPanelMsg{panel: g, t: feat, label: "fetch", err: errString("feat is not checked out anywhere")})
	if g.output[len(g.output)-1] != "feat is not checked out anywhere" {
		t.Fatalf("error: %q", g.output[len(g.output)-1])
	}

	// Closed: the status bar tells.
	m.overlay = nil
	m.receiveGitPanel(gitPanelMsg{panel: g, t: feat, label: "pull", res: proto.BranchGitResult{ExitCode: 128}})
	if !strings.Contains(m.flash, "git pull on feat failed (exit 128)") {
		t.Fatalf("closed, failed: %q", m.flash)
	}
	m.receiveGitPanel(gitPanelMsg{panel: g, t: feat, label: "pull", err: errString("connection lost")})
	if m.flash != "pull: connection lost" {
		t.Fatalf("closed, error: %q", m.flash)
	}
	m.receiveGitPanel(gitPanelMsg{panel: g, t: feat, label: "fetch"})
	if m.flash != "git fetch done on feat" {
		t.Fatalf("closed, done: %q", m.flash)
	}
}

// The actions run with a click or their key, and what can lose work asks
// first; the answer comes back to the panel.
func TestGitPanelActions(t *testing.T) {
	m, peer := gitPanelModel(t)
	g, _ := openPanel(t, m)
	g.running = ""

	x, y := actionAt(t, m, g, "l")
	a2Run(a1Mouse(t, m, x, y, tea.MouseButtonLeft, tea.MouseActionPress))
	if p := gitParams(t, peer, "--graph"); p.Commands[0][0] != "log" {
		t.Fatalf("log sent %q", p.Commands)
	}
	if g.onActions {
		t.Fatal("the actions stayed over the output")
	}
	g.running = ""

	// tab shows them; esc goes back to the prompt, not away.
	a1Key(t, m, a2Key("tab"))
	if !g.onActions || !strings.Contains(panelText(m, g), "arrows move") {
		t.Fatal("tab didn't show the actions")
	}
	a1Key(t, m, a2Key("esc"))
	if g.onActions || m.overlay != g || !g.in.Focused() {
		t.Fatalf("esc from the actions: %T onActions %v", m.overlay, g.onActions)
	}

	runAction(t, m, "r")
	if p := gitParams(t, peer, `["rebase","main"]`); len(p.Commands) != 1 {
		t.Fatalf("rebase %q", p.Commands)
	}
	g.running = ""

	// Undo asks; no comes back to the panel with nothing sent.
	before := peer.count(t, m.machines[0].c, proto.MethodBranchGit, "reset")
	runAction(t, m, "U")
	d, ok := m.overlay.(*dialog)
	if !ok || !d.confirm || !strings.Contains(d.text[0], "Undo the last commit on feat") {
		t.Fatalf("undo opened %T", m.overlay)
	}
	a1Key(t, m, runes("n"))
	if m.overlay != g || peer.count(t, m.machines[0].c, proto.MethodBranchGit, "reset") != before {
		t.Fatalf("no: overlay %T", m.overlay)
	}
	runAction(t, m, "U")
	a2Run(a1Key(t, m, runes("y")))
	if m.overlay != g {
		t.Fatalf("yes left %T", m.overlay)
	}
	if p := gitParams(t, peer, "--soft"); strings.Join(p.Commands[0], " ") != "reset --soft HEAD~1" {
		t.Fatalf("undo %q", p.Commands)
	}
	g.running = ""

	// Discarding can't be undone: enter doesn't answer it.
	runAction(t, m, "X")
	d = m.overlay.(*dialog)
	if !d.yesOnly {
		t.Fatal("discard takes enter")
	}
	if a1Key(t, m, a2Key("enter")); m.overlay != d {
		t.Fatalf("enter answered it: %T", m.overlay)
	}
	a2Run(a1Key(t, m, runes("y")))
	if p := gitParams(t, peer, "clean"); len(p.Commands) != 2 || p.Commands[0][0] != "reset" || strings.Join(p.Commands[1], " ") != "clean -fd" {
		t.Fatalf("discard %q", p.Commands)
	}
	g.running = ""

	// Arrows move through the grid: ← → wrap, ↑ ↓ go a row, not past the ends.
	a1Key(t, m, a2Key("tab"))
	panelText(m, g) // lays them out
	n := len(g.actions(*m))
	g.sel = 0
	a1Key(t, m, a2Key("left"))
	if g.sel != n-1 {
		t.Fatalf("left from the first: %d of %d", g.sel, n)
	}
	a1Key(t, m, a2Key("down"))
	if g.sel != n-1 {
		t.Fatalf("down from the last: %d", g.sel)
	}
	a1Key(t, m, a2Key("right"))
	a1Key(t, m, a2Key("down"))
	if g.sel != g.cols {
		t.Fatalf("down from the first: %d, %d columns", g.sel, g.cols)
	}
	a1Key(t, m, a2Key("up"))
	a1Key(t, m, a2Key("up"))
	if g.sel != 0 {
		t.Fatalf("up past the top: %d", g.sel)
	}
	a2Run(a1Key(t, m, a2Key("enter"))) // the first is Status
	peer.waitMethod(t, proto.MethodBranchGit, `["status","--short","--branch"]`)
	if g.onActions {
		t.Fatal("enter left the actions up")
	}

	a1Key(t, m, a2Key("esc"))
	if m.overlay != nil {
		t.Fatalf("esc left %T", m.overlay)
	}
}

// Base branches have nothing to rebase onto or merge in, and with no agent
// on the branch the prompt is only git's.
func TestGitPanelOnTheBase(t *testing.T) {
	m, _ := gitPanelModel(t)
	g := &gitPanel{t: harvestTarget{machine: localMachine, projectID: "r1", branch: "main"}}
	for _, a := range g.actions(*m) {
		if strings.HasPrefix(a.label, "Rebase on") || strings.HasPrefix(a.label, "Merge ") {
			t.Fatalf("the base offers %q", a.label)
		}
	}
	m.overlay = g
	if text := panelText(m, g); strings.Contains(text, "ctrl+t") {
		t.Fatalf("offers an agent that isn't there:\n%s", text)
	}
	g.nextTarget(*m)
	if g.agent != "" {
		t.Fatalf("aimed at %q", g.agent)
	}
}

// Commit, push and a rejected push go through conch's own dialogs and come
// back to the panel.
func TestGitPanelCommitAndPush(t *testing.T) {
	m, peer := gitPanelModel(t)
	g, _ := openPanel(t, m)
	g.running = ""

	runAction(t, m, "C")
	d, ok := m.overlay.(*dialog)
	if !ok || d.back != g {
		t.Fatalf("commit opened %T", m.overlay)
	}
	a1Key(t, m, a2Key("esc"))
	if m.overlay != g {
		t.Fatalf("esc from the commit left %T", m.overlay)
	}
	runAction(t, m, "C")
	a2Type(m, m.overlay, "panel commit")
	peer.setResult(proto.MethodBranchCommit, proto.CommitResult{Hash: "abcdef0123"})
	msgs := a2Run(a1Key(t, m, a2Key("enter")))
	if m.overlay != g {
		t.Fatalf("after committing: %T", m.overlay)
	}
	next, _ := m.Update(msgs[0])
	*m = next.(Model)
	if !strings.Contains(strings.Join(g.output, "\n"), "committed abcdef0 on feat") {
		t.Fatalf("commit not reported: %q", g.output)
	}

	// Push: a rejection asks, then comes back.
	peer.setCodedError(proto.MethodBranchPush, proto.ErrPushRejected, "the remote has 2 commits feat lacks")
	msgs = runAction(t, m, "p")
	next, _ = m.Update(msgs[0])
	*m = next.(Model)
	d, ok = m.overlay.(*dialog)
	if !ok || d.back != g {
		t.Fatalf("rejected push opened %T", m.overlay)
	}
	a1Key(t, m, runes("n"))
	if m.overlay != g {
		t.Fatalf("no to rebasing left %T", m.overlay)
	}
}

// A long agent name is cut in the hint, so esc close still fits.
func TestGitPanelLongAgentName(t *testing.T) {
	m, _ := gitPanelModel(t)
	m.width = 100
	m.machines[0].panes[0].Name = "Git worktree graphical UI for the whole team"
	m.machines[0].panes[0].CustomName = true
	g, _ := openPanel(t, m)
	text := panelText(m, g)
	if !strings.Contains(text, "ctrl+t → Git worktree gr…") || !strings.Contains(text, "esc close") || strings.Contains(text, "running") {
		t.Fatalf("hint:\n%s", text)
	}
}

func TestGitPanelFitHint(t *testing.T) {
	hint := []string{"enter run", "tab actions", "↑↓ history", "pgup/pgdn", "ctrl+t → claude", "esc close"}
	for _, c := range []struct {
		w    int
		want string
	}{
		{200, "enter run · tab actions · ↑↓ history · pgup/pgdn · ctrl+t → claude · esc close"},
		{60, "enter run · tab actions · ctrl+t → claude · esc close"},
		{50, "tab actions · ctrl+t → claude · esc close"},
		{40, "tab actions · ctrl+t → claude · esc close"},
		{5, "tab actions · ctrl+t → claude · esc close"}, // nothing more to drop; fit cuts it
	} {
		if got := fitHint(append([]string(nil), hint...), c.w); got != c.want {
			t.Errorf("%d: %q", c.w, got)
		}
	}
	if got := fitHint(nil, 10); got != "" {
		t.Errorf("none: %q", got)
	}
}

// The state read on opening doesn't hold up a command typed at once, and
// doesn't undo what that command found.
func TestGitPanelOpeningReadIsQuiet(t *testing.T) {
	m, peer := gitPanelModel(t)
	g, opening := openPanel(t, m)
	if g.running != "" {
		t.Fatalf("opening shows %q running", g.running)
	}
	a2Type(m, g, "rebase main")
	a2Run(a1Key(t, m, a2Key("enter")))
	gitParams(t, peer, "rebase")
	if g.running != "rebase" {
		t.Fatalf("typed command running %q", g.running)
	}
	for _, msg := range opening {
		if gm, ok := msg.(gitPanelMsg); ok {
			gm.res.InProgress = "" // read before the rebase stopped
			m.receiveGitPanel(gm)
		}
	}
	if g.running != "rebase" || len(g.output) != 0 {
		t.Fatalf("the opening answer: running %q output %q", g.running, g.output)
	}
	m.receiveGitPanel(gitPanelMsg{panel: g, t: feat, label: "rebase", res: proto.BranchGitResult{ExitCode: 1, InProgress: "rebase"}})
	m.receiveGitPanel(gitPanelMsg{panel: g, t: feat, label: "state", quiet: true, res: proto.BranchGitResult{InProgress: "rebase"}})
	if g.inProgress != "rebase" || g.running != "" {
		t.Fatalf("after: %q running %q", g.inProgress, g.running)
	}
}

// ctrl+t or a click on the prompt's label aims it at an agent on the
// branch: what is typed, or an action, goes to it as its next message.
func TestGitPanelTalksToTheAgent(t *testing.T) {
	m, peer := gitPanelModel(t)
	g, _ := openPanel(t, m)
	g.running = ""

	a1Key(t, m, tea.KeyMsg{Type: tea.KeyCtrlT})
	if text := panelText(m, g); g.agent != "p1" || !strings.Contains(text, "claude ❯") || !strings.Contains(text, "ctrl+t → git") {
		t.Fatalf("ctrl+t aimed at %q:\n%s", g.agent, text)
	}
	a2Type(m, g, "rebase on main and fix what breaks")
	msgs := a2Run(a1Key(t, m, a2Key("enter")))
	p := lastParams[proto.AgentPromptParams](t, peer, proto.MethodAgentPrompt)
	if p.ID != "p1" || p.Text != "rebase on main and fix what breaks" {
		t.Fatalf("prompt %+v", p)
	}
	next, _ := m.Update(msgs[0])
	*m = next.(Model)
	if !strings.Contains(strings.Join(g.output, "\n"), "sent to claude") {
		t.Fatalf("output %q", g.output)
	}

	// Its actions are instructions.
	runAction(t, m, "r")
	peer.waitMethod(t, proto.MethodAgentPrompt, "Review the uncommitted changes")

	// A refusal (it waits on a question) says why.
	peer.setError(proto.MethodAgentPrompt, "claude in p1 is waiting on a question")
	msgs = runAction(t, m, "c")
	next, _ = m.Update(msgs[0])
	*m = next.(Model)
	if !strings.Contains(strings.Join(g.output, "\n"), "not sent to claude: claude in p1 is waiting on a question") {
		t.Fatalf("refusal %q", g.output)
	}

	// ctrl+t again: back to git. A click on the label does the same.
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyCtrlT})
	if g.agent != "" {
		t.Fatalf("ctrl+t twice: %q", g.agent)
	}
	x, y := hitAt(t, m, g, hitTarget, func(gitHit) bool { return true })
	a1Mouse(t, m, x, y, tea.MouseButtonLeft, tea.MouseActionPress)
	if g.agent != "p1" {
		t.Fatalf("click aimed at %q", g.agent)
	}
	// The agent leaves the branch: the prompt is git's again.
	m.machines[0].panes[0].Agent = nil
	if strings.Contains(panelText(m, g), "claude ❯") || g.agent != "" {
		t.Fatal("still aimed at a pane with no agent")
	}
}

// Asking the agent to resolve a conflict from git's own actions.
func TestGitPanelHandsAConflictToTheAgent(t *testing.T) {
	m, peer := gitPanelModel(t)
	g, _ := openPanel(t, m)
	g.running, g.inProgress = "", "merge"
	x, y := actionAt(t, m, g, "x")
	a2Run(a1Mouse(t, m, x, y, tea.MouseButtonLeft, tea.MouseActionPress))
	if p := lastParams[proto.AgentPromptParams](t, peer, proto.MethodAgentPrompt); p.ID != "p1" || !strings.Contains(p.Text, "git merge --continue") {
		t.Fatalf("resolve %+v", p)
	}
	x, y = actionAt(t, m, g, "c")
	a2Run(a1Mouse(t, m, x, y, tea.MouseButtonLeft, tea.MouseActionPress))
	if p := gitParams(t, peer, "--continue"); strings.Join(p.Commands[0], " ") != "merge --continue" {
		t.Fatalf("continue %q", p.Commands)
	}
}

// An older server: the panel still opens, says what it can't do, and the
// agent is reached the older way.
func TestGitPanelOlderServers(t *testing.T) {
	m, peer := gitPanelModel(t, harvestCapability, "agent.broadcast.v1")
	g, _ := openPanel(t, m)
	if g.noGit == "" || !strings.Contains(strings.Join(g.output, "\n"), "predates running git") {
		t.Fatalf("no note: %q", g.output)
	}
	a2Type(m, g, "status")
	if cmd := a1Key(t, m, a2Key("enter")); cmd != nil {
		t.Fatal("ran git on a server without branch.git")
	}
	if peer.count(t, m.machines[0].c, proto.MethodBranchGit, "") != 0 {
		t.Fatal("branch.git was sent")
	}
	g.setTarget("p1")
	a2Type(m, g, "hello")
	a2Run(a1Key(t, m, a2Key("enter")))
	if p := lastParams[proto.AgentBroadcastParams](t, peer, proto.MethodAgentBroadcast); len(p.IDs) != 1 || p.IDs[0] != "p1" || p.Text != "hello" {
		t.Fatalf("broadcast %+v", p)
	}

	// Older still: no way to message an agent.
	m2, _ := gitPanelModel(t, harvestCapability)
	g2, _ := openPanel(t, m2)
	g2.setTarget("p1")
	a2Type(m2, g2, "hello")
	if cmd := a1Key(t, m2, a2Key("enter")); cmd != nil || !strings.Contains(strings.Join(g2.output, "\n"), "predates sending agents messages") {
		t.Fatalf("no prompt capability: %q", g2.output)
	}
}

// A click elsewhere closes the panel and still happens; the wheel scrolls
// its output.
func TestGitPanelMouse(t *testing.T) {
	m, _ := gitPanelModel(t)
	g, _ := openPanel(t, m)
	g.running = ""
	g.add(strings.Repeat("row\n", 40))
	b := g.render(*m)
	g.scroll = 0
	a1Mouse(t, m, b.x+2, b.y+2, tea.MouseButtonWheelDown, tea.MouseActionPress)
	if g.scroll != 3 {
		t.Fatalf("wheel down: %d", g.scroll)
	}
	a1Mouse(t, m, b.x+2, b.y+2, tea.MouseButtonWheelUp, tea.MouseActionPress)
	a1Mouse(t, m, b.x+2, b.y+2, tea.MouseButtonWheelUp, tea.MouseActionPress)
	if g.scroll != 0 {
		t.Fatalf("wheel up past the top: %d", g.scroll)
	}
	swipe(3, func() { a1Mouse(t, m, b.x+2, b.y+2, tea.MouseButtonWheelDown, tea.MouseActionPress) })
	if g.scroll != 3+1+1 {
		t.Fatalf("swipe down: %d", g.scroll)
	}
	g.scroll = 0
	a1Key(t, m, a2Key("pgdown"))
	if g.scroll != gitPanelOutputMax/2 {
		t.Fatalf("pgdown: %d", g.scroll)
	}
	if !strings.Contains(panelText(m, g), "pgup/pgdn") {
		t.Fatal("no sign of more to scroll")
	}

	// A click on the prompt from the actions puts the keyboard back there.
	a1Key(t, m, a2Key("tab"))
	x, y := hitAt(t, m, g, hitPrompt, func(gitHit) bool { return true })
	a1Mouse(t, m, x, y, tea.MouseButtonLeft, tea.MouseActionPress)
	if g.onActions || !g.in.Focused() {
		t.Fatal("click on the prompt")
	}
	// Motion and release outside do nothing; a right click inside neither.
	a1Mouse(t, m, 0, 0, tea.MouseButtonNone, tea.MouseActionMotion)
	a1Mouse(t, m, 0, 0, tea.MouseButtonLeft, tea.MouseActionRelease)
	a1Mouse(t, m, b.x+2, b.y+1, tea.MouseButtonRight, tea.MouseActionPress)
	if m.overlay != g {
		t.Fatalf("closed by %T", m.overlay)
	}
	cmd := a1Mouse(t, m, 0, m.height-3, tea.MouseButtonLeft, tea.MouseActionPress)
	if m.overlay != nil || cmd == nil {
		t.Fatal("a click outside didn't close it")
	}
	if msg, ok := cmd().(tea.MouseMsg); !ok || msg.X != 0 || msg.Y != m.height-3 {
		t.Fatalf("passed on %v", msg)
	}
}

// Never wider or taller than the screen, however small; small while empty,
// growing with its output.
func TestGitPanelSizes(t *testing.T) {
	// Short and narrow: the actions scroll to keep the chosen one in view.
	m, _ := gitPanelModel(t)
	m.width, m.height = 39, 12
	g := &gitPanel{t: feat, onActions: true}
	m.overlay = g
	g.sel = len(g.actions(*m)) - 1
	panelText(m, g)
	if !strings.Contains(panelText(m, g), "Discard changes") {
		t.Fatalf("the last action is out of sight:\n%s", panelText(m, g))
	}

	for _, size := range [][2]int{{1, 1}, {3, 3}, {20, 5}, {39, 12}, {80, 24}, {240, 70}} {
		for _, actions := range []bool{false, true} {
			m, _ := gitPanelModel(t)
			m.width, m.height = size[0], size[1]
			g := &gitPanel{t: feat, inProgress: "rebase"}
			g.in.Placeholder = gitPlaceholder
			g.onActions = actions
			m.overlay = g
			empty := len(g.render(*m).lines)
			g.add(strings.Repeat("a very long line of git output that goes on and on ", 10) + strings.Repeat("\nmore", 12))
			b := g.render(*m)
			if !actions && size[1] >= 24 && len(b.lines) <= empty {
				t.Errorf("%dx%d: %d lines empty, %d with output", size[0], size[1], empty, len(b.lines))
			}
			if size[1] >= 12 && len(b.lines) > size[1]-1 {
				t.Errorf("%dx%d actions %v: %d lines tall", size[0], size[1], actions, len(b.lines))
			}
			for i, l := range b.lines {
				if w := ansi.StringWidth(l); size[0] >= 3 && w > size[0] {
					t.Errorf("%dx%d actions %v: line %d is %d wide", size[0], size[1], actions, i, w)
				}
			}
			if size[0] >= 3 && (b.x < 0 || b.x+b.width() > size[0]) {
				t.Errorf("%dx%d: box at x %d w %d", size[0], size[1], b.x, b.width())
			}
			for i, l := range strings.Split(m.View(), "\n") {
				if ansi.StringWidth(l) > size[0] {
					t.Errorf("%dx%d: screen line %d too wide", size[0], size[1], i)
				}
			}
		}
	}
}

// The protocol carries the commands as a list of argument lists.
func TestGitPanelParamsShape(t *testing.T) {
	b, _ := json.Marshal(proto.BranchGitParams{ProjectID: "r1", Branch: "feat", Commands: [][]string{{"status"}}})
	if string(b) != `{"project_id":"r1","branch":"feat","commands":[["status"]]}` {
		t.Fatal(string(b))
	}
}
