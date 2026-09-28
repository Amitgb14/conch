package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

func a2SetupResult() proto.AgentSetupResult {
	var items []proto.SetupItem
	for i := 0; i < 30; i++ {
		items = append(items, proto.SetupItem{Name: fmt.Sprintf("skill-%02d", i), Scope: "project", Detail: "does something useful for the agent"})
	}
	items[3].Missing = true
	return proto.AgentSetupResult{Dir: "/src/api-feat", ProjectID: "r1", Main: "/src/api", Worktree: "/src/api-feat",
		LocalFiles: []proto.LocalFile{{Path: ".env", State: proto.FileMissing}, {Path: "a.json", State: proto.FileDiffers},
			{Path: "b.txt", State: proto.FileNotIgnored}, {Path: "CLAUDE.local.md", State: proto.FileSame}},
		Agents: []proto.AgentSetup{
			{Agent: "claude", Label: "Claude Code", Groups: []proto.SetupGroup{{Title: "Instructions", Items: []proto.SetupItem{{Name: "CLAUDE.md", Scope: "project"}}}}},
			{Agent: "codex", Label: "Codex", Notes: []string{"Codex reads AGENTS.md only"}, Groups: []proto.SetupGroup{{Title: "Skills", Items: items}}},
		}}
}

func TestA2OpenSetup(t *testing.T) {
	a2Isolate(t)
	m := a2Model()
	m.rows = []row{
		{id: "b:main", kind: kindBranch, machine: localMachine, projectID: "r1", branch: "gone"},
		{id: "pane:p1", kind: kindPane, machine: localMachine, paneID: "p1"},
	}
	m.cursor = "b:main"
	if m.openSetup() != nil || m.overlay != nil || !strings.Contains(m.flash, "check the branch out first") {
		t.Fatalf("branch not checked out: %q", m.flash)
	}
	m.cursor = "pane:p1"
	m.cfg.Agents.Default = "codex"
	if m.openSetup() != nil {
		t.Fatal("no connection: nothing to load")
	}
	v := m.overlay.(*setupView)
	if v.want != "claude" || v.dir != "/src/api" || v.err != "local is online" {
		t.Fatalf("setup of a claude pane: %+v", v)
	}
	m.machines[0].c = a2Client()
	if v.load(m) != nil || !strings.Contains(v.err, "predates agent setup") {
		t.Fatalf("old server: %q", v.err)
	}
	m.machines[0].c = a2Client("agent.setup.v1")
	v.err = ""
	if v.load(m) == nil || !v.loading {
		t.Fatal("a capable server loads (the command calls it, so it isn't run)")
	}
}

func TestA2SetupUpdateAndKeys(t *testing.T) {
	a2Isolate(t)
	m := a2Model()
	v := &setupView{mid: localMachine, dir: "/src/api-feat", want: "codex", loading: true}
	m.overlay = v

	other := &setupView{}
	v.update(m, setupMsg{view: other, res: a2SetupResult()})
	if v.res != nil {
		t.Fatal("a reply for another view was applied")
	}
	v.update(m, setupMsg{view: v, err: errors.New("no such dir")})
	if v.loading || v.err != "no such dir" {
		t.Fatalf("error reply: %q", v.err)
	}
	v.update(m, setupMsg{view: v, res: a2SetupResult()})
	if v.err != "" || v.res == nil || v.tab != 1 {
		t.Fatalf("first reply selects the wanted agent: tab %d", v.tab)
	}
	v.tab = 0
	v.update(m, setupMsg{view: v, res: a2SetupResult()})
	if v.tab != 0 {
		t.Fatal("a reload keeps the tab")
	}

	for _, step := range []struct {
		key string
		tab int
	}{{"tab", 1}, {"right", 0}, {"l", 1}, {"shift+tab", 0}, {"left", 1}, {"h", 0}, {"2", 1}, {"1", 0}, {"6", 0}} {
		v.scroll = 5
		v.update(m, a2Key(step.key))
		if v.tab != step.tab || (step.key != "6" && v.scroll != 0) {
			t.Fatalf("after %s tab %d scroll %d", step.key, v.tab, v.scroll)
		}
	}
	v.scroll = 0
	for _, step := range []struct {
		key  string
		want int
	}{{"down", 1}, {"j", 2}, {"up", 1}, {"k", 0}, {"pgdown", m.setupListHeight()}, {" ", 2 * m.setupListHeight()}, {"pgup", m.setupListHeight()}, {"g", 0}, {"home", 0}, {"G", 1 << 20}, {"end", 1 << 20}} {
		v.update(m, a2Key(step.key))
		if v.scroll != step.want {
			t.Fatalf("after %s scroll %d, want %d", step.key, v.scroll, step.want)
		}
	}

	// r reloads; c copies missing files (needs a connection); f edits the patterns.
	if _, cmd := v.update(m, a2Key("r")); cmd != nil || v.err != "local is online" {
		t.Fatalf("reload offline: %q", v.err)
	}
	if _, cmd := v.update(m, a2Key("c")); cmd != nil {
		t.Fatal("copy without a connection")
	}
	m.machines[0].c = a2Client()
	if _, cmd := v.update(m, a2Key("c")); cmd == nil {
		t.Fatal("copy missing files in a worktree")
	}
	m.machines[0].c = nil
	closed, cmd := v.update(m, a2Key("f"))
	d, ok := m.overlay.(*dialog)
	if !closed || cmd == nil || !ok || d.title != " Local files · api " {
		t.Fatalf("f opens the local files dialog: %#v", m.overlay)
	}
	m.overlay = v
	if v.update(m, tickMsg{}); m.overlay != v {
		t.Fatal("ticks do nothing")
	}

	// Copy results.
	v.update(m, setupCopiedMsg{view: other})
	v.update(m, setupCopiedMsg{view: v, err: errors.New("denied")})
	if m.flash != "denied" || !m.flashIsErr {
		t.Fatalf("copy error: %q", m.flash)
	}
	v.update(m, setupCopiedMsg{view: v, res: proto.WorktreeFilesResult{Copied: []string{".env", "x"}}})
	if m.flash != "copied 2 local file(s)" {
		t.Fatalf("copied: %q", m.flash)
	}
	v.update(m, setupCopiedMsg{view: v})
	if m.flash != "nothing to copy" {
		t.Fatalf("nothing copied: %q", m.flash)
	}

	for _, k := range []string{"esc", "q", "i"} {
		m.overlay = v
		if closed, _ := v.update(m, a2Key(k)); !closed || m.overlay != nil {
			t.Fatalf("%s closes", k)
		}
	}
	// No worktree, no project: c and f do nothing.
	plain := &setupView{mid: localMachine, res: &proto.AgentSetupResult{Dir: "/tmp"}}
	m.overlay = plain
	if closed, cmd := plain.update(m, a2Key("c")); closed || cmd != nil {
		t.Fatal("c outside a worktree")
	}
	if closed, _ := plain.update(m, a2Key("f")); closed || m.overlay != plain {
		t.Fatal("f outside a project")
	}
	if plain.copyMissing(m) != nil {
		t.Fatal("copyMissing outside a worktree")
	}
}

func TestA2SetupRender(t *testing.T) {
	a2Isolate(t)
	m := a2Model()
	m.height = 30
	v := &setupView{mid: localMachine, loading: true}
	b := v.render(*m)
	a2CheckBox(t, b, *m)
	if out := a2Plain(b.lines); !strings.Contains(out, "loading…") || !strings.Contains(out, " loading… tab agent") {
		t.Fatalf("loading:\n%s", out)
	}
	v.loading, v.err = false, "the server on this machine predates agent setup; restart it to use this build"
	if out := a2Plain(v.render(*m).lines); !strings.Contains(out, "predates agent setup") {
		t.Fatalf("error:\n%s", out)
	}
	v.err = ""
	res := a2SetupResult()
	v.res = &res
	labels := strings.Join(v.tabLabels(), "|")
	if labels != " 1 Claude Code | 2 Codex ✗1 " {
		t.Fatalf("tab labels %q", labels)
	}
	b = v.render(*m)
	a2CheckBox(t, b, *m)
	out := a2Plain(b.lines)
	for _, want := range []string{"Agent setup", "/src/api-feat · worktree of /src/api", "Local files from the main checkout", "c copy missing · f edit patterns",
		"✗ .env  missing", "≠ a.json  differs", "not copied: git doesn't ignore it", "✓ CLAUDE.local.md", "Instructions 1", "CLAUDE.md"} {
		if !strings.Contains(out, want) {
			t.Fatalf("render lacks %q:\n%s", want, out)
		}
	}
	v.tab = 1
	out = a2Plain(v.render(*m).lines)
	for _, want := range []string{"⚠ Codex reads AGENTS.md only", "Skills 30", "only in the main checkout", "more lines"} {
		if !strings.Contains(out, want) {
			t.Fatalf("codex tab lacks %q:\n%s", want, out)
		}
	}
	v.scroll = 1 << 20
	v.render(*m)
	if body := v.body(*m, 96); v.scroll != len(body)-m.setupListHeight() {
		t.Fatalf("scroll clamps to the end: %d of %d", v.scroll, len(body))
	}

	// A plain checkout of a git project shows its patterns; no local files, a hint.
	v.res = &proto.AgentSetupResult{Dir: "/src/api", ProjectID: "r1", Agents: res.Agents[:1]}
	if out := a2Plain(v.body(*m, 80)); !strings.Contains(out, "New worktrees get the ignored files matching") || !strings.Contains(out, "(none)") {
		t.Fatalf("project patterns:\n%s", out)
	}
	m.machines[0].projects[0].LocalFiles = []string{".env", "*.local.json"}
	if out := a2Plain(v.body(*m, 80)); !strings.Contains(out, ".env *.local.json") {
		t.Fatalf("patterns:\n%s", out)
	}
	v.res = &proto.AgentSetupResult{Dir: "/w", Main: "/m", Worktree: "/w"}
	v.tab = 3
	if out := a2Plain(v.body(*m, 80)); !strings.Contains(out, "no ignored files match") || strings.Contains(out, "c copy missing") {
		t.Fatalf("worktree without local files:\n%s", out)
	}
	if missingCount(proto.AgentSetup{}) != 0 {
		t.Fatal("missingCount")
	}
	if got := wrapIndent("a b c", 6); strings.Join(got, "|") != " a b|  c" && strings.Join(got, "|") != " a b| c" {
		t.Fatalf("wrapIndent %q", got)
	}
}

func TestA2SetupMouse(t *testing.T) {
	a2Isolate(t)
	m := a2Model()
	res := a2SetupResult()
	v := &setupView{mid: localMachine, res: &res}
	m.overlay = v
	b := v.render(*m)
	v.mouse(m, tea.MouseMsg{X: b.x + 2, Y: b.y + 5, Button: tea.MouseButtonWheelDown}, b)
	v.mouse(m, tea.MouseMsg{X: b.x + 2, Y: b.y + 5, Button: tea.MouseButtonWheelDown}, b)
	v.mouse(m, tea.MouseMsg{X: b.x + 2, Y: b.y + 5, Button: tea.MouseButtonWheelUp}, b)
	if v.scroll != 3 {
		t.Fatalf("wheel scroll %d", v.scroll)
	}
	// Clicking the second tab label.
	x := b.x + 1 + ansi.StringWidth(v.tabLabels()[0]) + 2
	v.mouse(m, tea.MouseMsg{X: x, Y: b.y + 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}, b)
	if v.tab != 1 || v.scroll != 0 {
		t.Fatalf("tab click: tab %d", v.tab)
	}
	v.mouse(m, tea.MouseMsg{X: b.x + 1, Y: b.y + 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}, b)
	if v.tab != 0 {
		t.Fatal("first tab click")
	}
	v.mouse(m, tea.MouseMsg{X: b.x + b.width() - 2, Y: b.y + 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}, b)
	v.mouse(m, tea.MouseMsg{X: b.x + 2, Y: b.y + 4, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}, b)
	if v.tab != 0 || m.overlay != v {
		t.Fatal("clicks past the tabs or in the body")
	}
	v.mouse(m, tea.MouseMsg{X: 0, Y: 0, Action: tea.MouseActionMotion}, b)
	if m.overlay != v {
		t.Fatal("motion outside")
	}
	v.mouse(m, tea.MouseMsg{X: 0, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}, b)
	if m.overlay != nil {
		t.Fatal("a click outside closes")
	}
}

func TestA2LocalFilesDialog(t *testing.T) {
	m := a2Model()
	proj := m.machines[0].projects[0]
	proj.LocalFilesDefault = true
	proj.LocalFiles = []string{".env"}
	if d := newLocalFilesDialog(*m, localMachine, proj); d.fields[0].in.Value() != "default" {
		t.Fatalf("default patterns: %q", d.fields[0].in.Value())
	}
	proj.LocalFilesDefault = false
	proj.LocalFiles = []string{".env", "config/*.json"}
	d := newLocalFilesDialog(*m, localMachine, proj)
	if d.fields[0].in.Value() != ".env config/*.json" {
		t.Fatalf("patterns: %q", d.fields[0].in.Value())
	}
	for _, v := range []string{" default ", ".env, x"} {
		if msg := a2ErrText(a2Run(d.submit(m, []string{v}))); msg != "local is online" {
			t.Fatalf("submit %q offline: %q", v, msg)
		}
	}
}

// s gives the other agents the setup of the agent whose tab is open. The
// plan is shown and answered before anything is written.
func TestA2SetupSync(t *testing.T) {
	a2Isolate(t)
	m := a2Model()
	v := &setupView{mid: localMachine, dir: "/src/api", want: "claude"}
	m.overlay = v
	v.update(m, setupMsg{view: v, res: a2SetupResult()})

	// Offline, and a server too old for it.
	if _, cmd := v.update(m, a2Key("s")); cmd != nil || !strings.Contains(m.flash, "local is online") {
		t.Fatalf("offline: %q", m.flash)
	}
	m.machines[0].c = a2Client("agent.setup.v1")
	if _, cmd := v.update(m, a2Key("s")); cmd != nil || !strings.Contains(m.flash, "too old to sync agent setup") {
		t.Fatalf("an old server: %q", m.flash)
	}
	m.machines[0].c = a2Client("agent.setup.v1", proto.CapAgentSync)
	if _, cmd := v.update(m, a2Key("s")); cmd == nil || !v.loading {
		t.Fatal("s asks for a plan")
	}

	// The plan: a question naming what it would write and what it leaves.
	plan := proto.AgentSyncResult{Dir: "/src/api", From: "claude", To: []string{"codex", "gemini", "opencode"},
		Changes: []proto.SyncChange{
			{Agent: "codex", Kind: proto.SyncInstructions, Name: "AGENTS.md", Path: "AGENTS.md", Action: proto.SyncCreate, Detail: "a copy of CLAUDE.md"},
			{Agent: "codex", Kind: proto.SyncSkill, Name: "review", Path: ".agents/skills/review", Action: proto.SyncLink},
			{Agent: "gemini", Kind: proto.SyncSkill, Name: "review", Path: ".agents/skills/review", Action: proto.SyncSame, Detail: "the same folder as Codex"},
			{Agent: "gemini", Kind: proto.SyncMCP, Name: "paid", Action: proto.SyncSkip, Detail: "conch does not copy secrets"},
			{Agent: "opencode", Kind: proto.SyncMCP, Name: "paid", Action: proto.SyncSkip, Detail: "conch does not copy secrets"},
		}}
	v.update(m, setupSyncMsg{view: &setupView{}, res: plan})
	if _, ok := m.overlay.(*dialog); ok {
		t.Fatal("a reply for another view was shown")
	}
	v.update(m, setupSyncMsg{view: v, res: plan})
	d, ok := m.overlay.(*dialog)
	if !ok || !d.confirm || v.loading {
		t.Fatalf("plan: %#v", m.overlay)
	}
	text := strings.Join(d.text, "\n")
	for _, want := range []string{"Give Codex, Gemini CLI and OpenCode Claude Code's setup", "create AGENTS.md (a copy of CLAUDE.md)",
		"link review → .agents/skills/review", "Left out — 2 items: conch does not copy secrets", "can be undone with u"} {
		if !strings.Contains(text, want) {
			t.Fatalf("the question lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "the same folder as Codex") {
		t.Fatalf("a question listing what it would not do:\n%s", text)
	}
	// Answering yes writes; the dialog's own submit is what does it.
	if cmd := d.submit(m, nil); cmd == nil {
		t.Fatal("yes did nothing")
	}

	// What came of it.
	applied := plan
	applied.Applied, applied.Undo = true, "20260926-101500"
	for i := range applied.Changes {
		applied.Changes[i].Done = writesChange(applied.Changes[i])
	}
	m.overlay = v
	v.update(m, setupSyncMsg{view: v, res: applied})
	if !strings.Contains(m.flash, "gave Codex, Gemini CLI and OpenCode Claude Code's setup · 2 changes") ||
		!strings.Contains(m.flash, "u undoes it") {
		t.Fatalf("applied: %q", m.flash)
	}
	// One that failed says so, and says it loudly.
	failed := applied
	failed.Changes = append([]proto.SyncChange{}, applied.Changes...)
	failed.Changes[0] = proto.SyncChange{Agent: "codex", Name: "AGENTS.md", Action: proto.SyncCreate, Error: "permission denied"}
	v.update(m, setupSyncMsg{view: v, res: failed})
	if !strings.Contains(m.flash, "AGENTS.md: permission denied") || !m.flashIsErr {
		t.Fatalf("a change that failed: %q (err %v)", m.flash, m.flashIsErr)
	}

	// Nothing to do says why, and asks nothing.
	nothing := proto.AgentSyncResult{Dir: "/src/api", From: "claude", To: []string{"codex"},
		Changes: []proto.SyncChange{
			{Agent: "codex", Kind: proto.SyncSkill, Name: "review", Action: proto.SyncSame},
			{Agent: "codex", Kind: proto.SyncMCP, Name: "paid", Action: proto.SyncSkip, Detail: "conch does not copy secrets"},
		}}
	m.overlay = v
	v.update(m, setupSyncMsg{view: v, res: nothing})
	if _, ok := m.overlay.(*dialog); ok {
		t.Fatalf("asked about nothing: %#v", m.overlay)
	}
	if !strings.Contains(m.flash, "already in every agent here") || !strings.Contains(m.flash, "1 item already there, 1 item left alone") {
		t.Fatalf("nothing to do: %q", m.flash)
	}

	// u puts it back, and says what it put back.
	if _, cmd := v.update(m, a2Key("u")); cmd == nil {
		t.Fatal("u asks the server")
	}
	undone := proto.AgentSyncResult{Dir: "/src/api", Undone: true, Undo: "20260926-101500",
		Changes: []proto.SyncChange{{Kind: proto.SyncInstructions, Name: "AGENTS.md", Action: proto.SyncRemove, Done: true}}}
	v.update(m, setupSyncMsg{view: v, res: undone})
	if !strings.Contains(m.flash, "put sync 20260926-101500 back · 1 file") {
		t.Fatalf("undone: %q", m.flash)
	}
	// A refusal from the server is shown as it came.
	v.update(m, setupSyncMsg{view: v, err: errors.New("conch has no sync to undo in /src/api")})
	if !strings.Contains(m.flash+strings.Join(noticeText(m.overlay), " "), "no sync to undo") {
		t.Fatalf("a refusal: %q", m.flash)
	}
	// The hint says both keys.
	if got := strings.Join(v.render(*m).lines, "\n"); !strings.Contains(ansi.Strip(got), "s sync to others · u undo") {
		t.Fatalf("hint:\n%s", got)
	}
}

// noticeText is a notice dialog's lines, or nothing.
func noticeText(o overlay) []string {
	if d, ok := o.(*dialog); ok {
		return d.text
	}
	return nil
}

// Your own setup — ~/.claude and the rest — is asked for from Settings,
// since it is machine-wide, and the question says where it writes.
func TestUserSyncFromSettings(t *testing.T) {
	a2Isolate(t)
	m := a2Model()

	// Offline, and a server too old for it.
	if cmd := m.openUserSync("claude"); cmd != nil || !strings.Contains(m.flash, "local is online") {
		t.Fatalf("offline: %q", m.flash)
	}
	m.machines[0].c = a2Client("agent.setup.v1", proto.CapAgentSync)
	if cmd := m.openUserSync("claude"); cmd != nil || !strings.Contains(m.flash, "too old to sync your own setup") {
		t.Fatalf("an old server: %q", m.flash)
	}
	m.machines[0].c = a2Client("agent.setup.v1", proto.CapAgentSync, proto.CapAgentSyncUser)
	if cmd := m.openUserSync("claude"); cmd == nil {
		t.Fatal("it should ask the server")
	}

	// The plan: the same lines as a checkout's, with a warning of its own.
	plan := proto.AgentSyncResult{Dir: "/Users/x", From: "claude", To: []string{"codex", "gemini"},
		Changes: []proto.SyncChange{
			{Agent: "codex", Kind: proto.SyncInstructions, Name: "AGENTS.md", Path: "~/.codex/AGENTS.md", Action: proto.SyncCreate, Detail: "a copy of ~/.claude/CLAUDE.md"},
			{Agent: "codex", Kind: proto.SyncMCP, Name: "gh", Path: "~/.codex/config.toml", Action: proto.SyncSkip, Detail: "it is a link into somewhere else (a dotfiles repository?): conch leaves it alone"},
			{Agent: "gemini", Kind: proto.SyncSkill, Name: "tide", Path: "~/.agents/skills/tide", Action: proto.SyncLink},
		}}
	m.receiveUserSync(userSyncMsg{res: plan, from: "claude"})
	d, ok := m.overlay.(*dialog)
	if !ok || !d.confirm || !strings.Contains(d.title, "your own agent setup") {
		t.Fatalf("plan: %#v", m.overlay)
	}
	text := strings.Join(d.text, "\n")
	for _, want := range []string{"no git status to show what changed", "create AGENTS.md → ~/.codex/AGENTS.md",
		"link tide → ~/.agents/skills/tide", "a dotfiles repository?"} {
		if !strings.Contains(text, want) {
			t.Fatalf("the question lacks %q:\n%s", want, text)
		}
	}
	if cmd := d.submit(m, nil); cmd == nil {
		t.Fatal("yes did nothing")
	}

	// What came of it, and where to put it back.
	applied := plan
	applied.Applied = true
	for i := range applied.Changes {
		applied.Changes[i].Done = writesChange(applied.Changes[i])
	}
	m.receiveUserSync(userSyncMsg{res: applied})
	if !strings.Contains(m.flash, "gave Codex and Gemini CLI Claude Code's setup, in your home") ||
		!strings.Contains(m.flash, "Settings → Agents puts it back") {
		t.Fatalf("applied: %q", m.flash)
	}
	// Undoing, and a refusal from the server.
	if cmd := m.undoUserSync(); cmd == nil {
		t.Fatal("undo asks the server")
	}
	m.receiveUserSync(userSyncMsg{res: proto.AgentSyncResult{Undone: true,
		Changes: []proto.SyncChange{{Name: "AGENTS.md", Action: proto.SyncRemove, Done: true}}}})
	if !strings.Contains(m.flash, "put your setup back as it was · 1 file") {
		t.Fatalf("undone: %q", m.flash)
	}
	m.receiveUserSync(userSyncMsg{err: errors.New("conch has no sync to undo in your home")})
	if !strings.Contains(m.flash+strings.Join(noticeText(m.overlay), " "), "no sync to undo") {
		t.Fatalf("a refusal: %q", m.flash)
	}
	// Nothing to do says so rather than asking.
	m.overlay = nil
	m.receiveUserSync(userSyncMsg{res: proto.AgentSyncResult{From: "claude", To: []string{"codex"},
		Changes: []proto.SyncChange{{Agent: "codex", Kind: proto.SyncSkill, Name: "tide", Action: proto.SyncSame}}}})
	if _, ok := m.overlay.(*dialog); ok {
		t.Fatalf("asked about nothing: %#v", m.overlay)
	}
	if !strings.Contains(m.flash, "already in every agent in your home") {
		t.Fatalf("nothing to do: %q", m.flash)
	}
}
