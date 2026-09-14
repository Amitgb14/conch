package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/buildinfo"
	"github.com/Amitgb14/conch/internal/proto"
)

func a1Chip(m *Model) string {
	chip, _ := m.statusHints()
	return strings.TrimSpace(ansi.Strip(chip))
}

func a1HintText(m *Model) string {
	_, items := m.statusHints()
	var parts []string
	for _, it := range items {
		parts = append(parts, ansi.Strip(it.text))
	}
	return strings.Join(parts, "|")
}

func TestA1StatusChips(t *testing.T) {
	m, _ := a1Fixture(t, true)
	if a1Chip(m) != "TREE" || !strings.Contains(a1HintText(m), "a project|c agent") {
		t.Fatalf("machine row: %s %s", a1Chip(m), a1HintText(m))
	}
	for id, want := range map[string]string{
		projectNodeID(localMachine, "r1"):        "t task",
		branchNodeID(localMachine, "r1", "feat"): "enter changes",
		paneNodeID(localMachine, "p3"):           "O new tab",
		cliID(localMachine):                      "/ filter",
	} {
		m.cursor = id
		if got := a1HintText(m); a1Chip(m) != "TREE" || !strings.Contains(got, want) || !strings.HasSuffix(got, "! waiting|? keys") {
			t.Errorf("%s: %s", id, got)
		}
	}
	m.cursor = sectionID(localMachine, "r1", "sessions")
	m.rows = append(m.rows, row{id: m.cursor, kind: kindSessions, machine: localMachine, projectID: "r1"})
	if !strings.Contains(a1HintText(m), "enter open sessions") {
		t.Errorf("sessions row: %s", a1HintText(m))
	}
	m.filtering = true
	if a1Chip(m) != "FILTER" {
		t.Errorf("filtering: %s", a1Chip(m))
	}
	m.filtering = false
	m.prefixArmed = true
	if a1Chip(m) != "PREFIX" {
		t.Errorf("prefix: %s", a1Chip(m))
	}
	m.prefixArmed = false

	m.focus = focusMain
	if a1Chip(m) != "SESSIONS" {
		t.Errorf("sessions focus: %s", a1Chip(m))
	}
	m.cursor = projectNodeID(localMachine, "r1")
	if a1Chip(m) != "CHANGES" {
		t.Errorf("main fallback: %s", a1Chip(m))
	}
	m.changes = &changesView{diffFile: "a.go"}
	if a1Chip(m) != "DIFF" {
		t.Errorf("diff: %s", a1Chip(m))
	}
	m.changes = nil

	a1Open(t, m, paneNodeID(localMachine, "p1"))
	m.focus = focusMain
	if a1Chip(m) != "PANE" || strings.Contains(a1HintText(m), "close split") {
		t.Errorf("pane: %s %s", a1Chip(m), a1HintText(m))
	}
	m.split(splitRight, viewOf(m.rows[indexOfRow(m.rows, paneNodeID(localMachine, "p2"))]))
	m.focus = focusMain
	if !strings.Contains(a1HintText(m), "ctrl+b x close split") {
		t.Errorf("split pane: %s", a1HintText(m))
	}
	m.tab().sync = true
	if a1Chip(m) != "SYNC" || !strings.HasPrefix(a1HintText(m), "ctrl+b S stop typing") {
		t.Errorf("sync: %s %s", a1Chip(m), a1HintText(m))
	}
	m.tab().sync = false
	m.offset = 4
	if a1Chip(m) != "HISTORY" {
		t.Errorf("history: %s", a1Chip(m))
	}
	m.scrollMode = true
	if a1Chip(m) != "SCROLL" {
		t.Errorf("scroll: %s", a1Chip(m))
	}
}

func TestA1StatusActions(t *testing.T) {
	m, _ := a1Fixture(t, true)
	a1Open(t, m, paneNodeID(localMachine, "p2"))
	m.frames[paneKey(localMachine, "p2")] = &proto.Frame{ID: "p2", History: 50}
	m.syncView()
	m.focus = focusMain
	run := func(label string) {
		t.Helper()
		_, items := m.statusHints()
		for _, it := range items {
			if strings.Contains(ansi.Strip(it.text), label) {
				it.act(m)
				return
			}
		}
		t.Fatalf("no status item %q in %s", label, a1HintText(m))
	}
	run("scroll")
	if !m.scrollMode {
		t.Fatal("scroll action")
	}
	m.scrollMode = false
	run("zoom")
	if !m.zoom {
		t.Fatal("zoom action")
	}
	run("zoom")
	m.machines[0].panes[1].State = proto.PaneExited // no new shell for splits and tabs
	run("split down")
	if len(m.tab().root.leaves()) != 2 {
		t.Fatal("split down action")
	}
	m.tab().focus = m.tab().root.leaves()[0].id
	m.syncView()
	m.focus = focusMain
	run("close split")
	if len(m.tab().root.leaves()) != 2 || m.overlay != nil {
		// an exited pane closes via the server, without a confirm
		t.Fatalf("close split action: leaves %d overlay %T", len(m.tab().root.leaves()), m.overlay)
	}
	m.machines[0].panes[1].State = proto.PaneRunning
	m.tab().root = &layoutNode{leaf: m.tab().focused()}
	m.syncView()
	m.focus = focusMain
	m.offset = 5
	run("back to now")
	if m.offset != 0 {
		t.Fatalf("live action: offset %d", m.offset)
	}
	m.offset = 5
	run("scroll keys")
	if !m.scrollMode {
		t.Fatal("scroll keys action")
	}
	m.scrollMode, m.offset = false, 0
	run("tree")
	if m.focus != focusSidebar {
		t.Fatal("tree action")
	}
	m.prefixArmed = true
	run("tree")
	if m.prefixArmed {
		t.Fatal("prefix tree action")
	}
	m.focus = focusMain
	run("tab")
	if len(m.tabs) != 2 {
		t.Fatalf("tab action: %d tabs", len(m.tabs))
	}
	m.focus = focusMain
	a1Open(t, m, paneNodeID(localMachine, "p2"))
	m.focus = focusMain
	run("split")
	if len(m.tab().root.leaves()) != 2 {
		t.Fatal("split action")
	}
	m.focus = focusMain
	m.tab().sync = true
	run("stop typing")
	if m.tab().sync {
		t.Fatal("stop sync action")
	}
}

func TestA1PressMapsKeys(t *testing.T) {
	m, _ := a1Fixture(t, false)
	m.press("↓")
	if m.cursor != projectNodeID(localMachine, "r1") {
		t.Fatalf("↓: %s", m.cursor)
	}
	m.press("↑")
	m.press("pgdn")
	if m.cursor != m.rows[len(m.rows)-1].id {
		t.Fatalf("pgdn: %s", m.cursor)
	}
	m.press("pgup")
	m.press("space") // folds the machine
	if len(m.rows) != 1 {
		t.Fatalf("space: %d rows", len(m.rows))
	}
	m.press("enter")
	if len(m.rows) == 1 {
		t.Fatal("enter did not open the machine")
	}
	m.press("/")
	m.press("esc")
	if m.filtering {
		t.Fatal("esc")
	}
	m.cursor = paneNodeID(localMachine, "p3")
	m.press("tab")
	if m.focus != focusMain {
		t.Fatal("tab")
	}
	m.focus = focusSidebar
	m.press("⏎")
	if m.focus != focusMain {
		t.Fatal("⏎")
	}
}

func TestA1StatusRightAndNarrowWidths(t *testing.T) {
	m, _ := a1Fixture(t, false)
	m.machines[0].panes[0].Agent.State = proto.AgentBlocked
	m.setFlash("something went wrong on the way to the thing", true)
	wide := ansi.Strip(m.statusBar())
	for _, want := range []string{"⚑ 1 waiting", "something went wrong", "✦ Ask", "⚙ Settings", versionLabel()} {
		if !strings.Contains(wide, want) {
			t.Errorf("wide bar lacks %q: %q", want, wide)
		}
	}
	for _, w := range []int{1, 10, 30, 50, 70, 90, 120} {
		m.width = w
		line, hits := m.layoutStatus()
		if ansi.StringWidth(line) != w {
			t.Errorf("width %d: bar is %d wide", w, ansi.StringWidth(line))
		}
		for _, h := range hits {
			// Items past the edge are cut off by fit and can't be clicked.
			if h.x0 < 0 || h.x1 <= h.x0 {
				t.Errorf("width %d: hit %+v out of the bar", w, h)
			}
		}
	}
	// Narrow: right side sheds the version and words first.
	m.width = 60
	narrow := ansi.Strip(m.statusBar())
	if strings.Contains(narrow, versionLabel()) || strings.Contains(narrow, "Settings") || !strings.Contains(narrow, "TREE") {
		t.Errorf("narrow bar: %q", narrow)
	}
	// Levels directly.
	if items := m.statusRightItems(rightMinimal); len(items) != 3 { // waiting, ✦, ⚙
		t.Errorf("minimal right items: %d", len(items))
	}
	m.flash = ""
	m.machines[0].warning = "server is old"
	if got := ansi.Strip(m.statusRightItems(rightFull)[1].text); got != "server is old" {
		t.Errorf("warning item %q", got)
	}
	// Quiet hours label, and clicking it while snoozed resumes alerts.
	m.snoozeUntil = time.Now().Add(time.Hour)
	items := m.statusRightItems(rightFull)
	var snooze *statusItem
	for i := range items {
		if strings.Contains(ansi.Strip(items[i].text), "snoozed until") {
			snooze = &items[i]
		}
	}
	if snooze == nil {
		t.Fatal("no snooze label")
	}
	snooze.act(m)
	if !m.snoozeUntil.IsZero() || m.flash != "notifications resumed" {
		t.Fatalf("resume: %q", m.flash)
	}
	// The waiting counter jumps to the agent.
	m.statusRightItems(rightFull)[0].act(m)
	if m.cursor != paneNodeID(localMachine, "p1") {
		t.Fatalf("waiting click went to %s", m.cursor)
	}
	// ✦ opens the command bar; the version opens its box.
	m.width = 160
	for _, it := range m.statusRightItems(rightFull) {
		text := ansi.Strip(it.text)
		switch {
		case text == "✦ Ask":
			it.act(m)
			if _, ok := m.overlay.(*askBar); !ok {
				t.Errorf("ask: %T", m.overlay)
			}
		case text == versionLabel():
			it.act(m)
			if _, ok := m.overlay.(versionInfo); !ok {
				t.Errorf("version: %T", m.overlay)
			}
		}
	}
}

func TestA1VersionInfo(t *testing.T) {
	m, _ := a1Fixture(t, false)
	if m.serverBehind() || m.tuiBehind() {
		t.Fatal("behind without a server")
	}
	box := m.overlay
	_ = box
	b := versionInfo{}.render(*m)
	got := ansi.Strip(strings.Join(b.lines, "\n"))
	if !strings.Contains(got, "not connected") || !strings.Contains(got, "any key closes") {
		t.Fatalf("offline version box:\n%s", got)
	}
	if b.x < 0 || b.y < 0 || b.x+b.width() > m.width+1 {
		t.Fatalf("box at %d,%d w %d", b.x, b.y, b.width())
	}
	c, _ := a1FakeClient(t)
	m.machines[0].c = c
	m.machines[0].server = c.Server
	m.machines[0].server.Started = time.Now().Add(-90 * time.Minute)
	got = ansi.Strip(strings.Join(versionInfo{}.render(*m).lines, "\n"))
	if !strings.Contains(got, "up to date") || !strings.Contains(got, "running 1h 30m") {
		t.Fatalf("connected version box:\n%s", got)
	}
	if buildinfo.Build() != "" {
		m.machines[0].server.Build = "someotherbuild"
		if !m.serverBehind() {
			t.Error("a different server build should be behind")
		}
		got = ansi.Strip(strings.Join(versionInfo{}.render(*m).lines, "\n"))
		if !strings.Contains(got, "outdated") || !strings.Contains(got, "predates reloading") {
			t.Errorf("outdated server box:\n%s", got)
		}
	}
	// Any key closes it; a press closes it; other messages don't.
	m.overlay = versionInfo{}
	if handled, _ := (versionInfo{}).update(m, tickMsg{}); handled || m.overlay == nil {
		t.Fatal("a tick closed the box")
	}
	if handled, cmd := (versionInfo{}).update(m, runes("u")); !handled || cmd != nil || m.overlay != nil {
		t.Fatal("u without updates should just close")
	}
	m.overlay = versionInfo{}
	(versionInfo{}).mouse(m, tea.MouseMsg{Action: tea.MouseActionMotion}, box0())
	if m.overlay == nil {
		t.Fatal("motion closed the box")
	}
	(versionInfo{}).mouse(m, tea.MouseMsg{Action: tea.MouseActionPress}, box0())
	if m.overlay != nil {
		t.Fatal("press did not close the box")
	}
	for d, want := range map[time.Duration]string{
		10 * time.Second:          "under a minute",
		5 * time.Minute:           "5m",
		3*time.Hour + time.Minute: "3h 1m",
		50 * time.Hour:            "2d 2h",
	} {
		if got := uptime(time.Now().Add(-d)); got != want {
			t.Errorf("uptime(%v) = %q, want %q", d, got, want)
		}
	}
}

func box0() box { return box{} }

func TestA1UIStateFiles(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CONCH_HOME", dir)
	if got := uiStatePath(); got != filepath.Join(dir, "ui.json") {
		t.Fatalf("uiStatePath %q", got)
	}
	// Missing, broken, and partial files give usable defaults.
	st := loadUIState(filepath.Join(dir, "missing.json"))
	if st.Expanded == nil || st.ShowAll == nil {
		t.Fatal("missing file")
	}
	broken := filepath.Join(dir, "broken.json")
	if err := os.WriteFile(broken, []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	if st := loadUIState(broken); st.Expanded == nil || st.ShowAll == nil || st.SidebarWidth != 0 {
		t.Fatal("broken file")
	}
	partial := filepath.Join(dir, "partial.json")
	if err := os.WriteFile(partial, []byte(`{"sidebar_width": 40}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if st := loadUIState(partial); st.Expanded == nil || st.ShowAll == nil || st.SidebarWidth != 40 {
		t.Fatalf("partial file: %+v", st)
	}

	// saveState writes a copy of the model's state when a path is set.
	m, _ := a1Fixture(t, false)
	a1Open(t, m, paneNodeID(localMachine, "p1"))
	m.expanded["p:r1"] = false
	m.showAll["r1"] = true
	m.sidebarW = 33
	path := filepath.Join(dir, "nested", "ui.json")
	m.statePath = path
	cmd := m.saveState()
	m.expanded["p:r1"] = true // changes after the snapshot don't leak in
	if cmd() != nil {
		t.Fatal("saveState reports a message")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved uiState
	if err := json.Unmarshal(b, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Expanded["p:r1"] || !saved.ShowAll["r1"] || saved.SidebarWidth != 33 || len(saved.Tabs) != 1 {
		t.Fatalf("saved %+v", saved)
	}
	back := loadUIState(path)
	var restored Model
	restored.restoreTabs(back.Tabs, back.ActiveTab)
	if len(restored.tabs) != 1 || restored.tab().focused().view.PaneID != "p1" {
		t.Fatal("round trip lost the tab")
	}
	// Without a path nothing is written.
	m.statePath = ""
	if m.saveState()() != nil {
		t.Fatal("saveState without a path")
	}
	// A path that can't be created fails quietly.
	blocker := filepath.Join(dir, "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := saveUIState(filepath.Join(blocker, "ui.json"), uiState{}); err == nil {
		t.Fatal("saving under a file should fail")
	}
}

func TestA1ClipboardTools(t *testing.T) {
	if runtime.GOOS == "darwin" {
		if tools := clipboardTools(); len(tools) != 1 || tools[0][0] != "pbcopy" {
			t.Fatalf("darwin tools %v", tools)
		}
		return
	}
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("DISPLAY", "")
	if tools := clipboardTools(); len(tools) != 0 {
		t.Fatalf("no display: %v", tools)
	}
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	t.Setenv("DISPLAY", ":0")
	if tools := clipboardTools(); len(tools) != 3 || tools[0][0] != "wl-copy" || tools[1][0] != "xclip" {
		t.Fatalf("displays: %v", tools)
	}
}
