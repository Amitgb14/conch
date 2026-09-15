package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

func TestWorkspaceRow(t *testing.T) {
	m, _ := a1Fixture(t, false)
	ws := workspaceID(localMachine)
	i := indexOfRow(m.rows, ws)
	if i != 1 || m.rows[i].depth != 1 || m.rows[indexOfRow(m.rows, projectNodeID(localMachine, "r1"))].depth != 2 {
		t.Fatalf("tree:\n%s", render(m.rows))
	}
	r := m.rows[i]

	// Sidebar line: glyph, label and project count, within the width.
	for _, w := range []int{8, 20, 30} {
		line := m.rowLine(r, w)
		if ansi.StringWidth(line) > w {
			t.Fatalf("width %d: %q", w, ansi.Strip(line))
		}
	}
	if got := ansi.Strip(m.rowLine(r, 30)); !strings.Contains(got, "▾ ▤ Workspace") || !strings.HasSuffix(got, "1") {
		t.Fatalf("row: %q", got)
	}

	// Selecting it previews its page: every project with what runs in it.
	a1At(t, m, ws)
	v := m.tab().focused().view
	if v.Kind != kindWorkspace {
		t.Fatalf("view %+v", v)
	}
	l := m.tab().focused()
	page := ansi.Strip(strings.Join(m.leafLines(l, 90, 20, false), "\n"))
	for _, want := range []string{"Workspace  1 projects on local", "◆ api", "/src/api", "1 agents · 1 terminals", "a add a project"} {
		if !strings.Contains(page, want) {
			t.Fatalf("page lacks %q:\n%s", want, page)
		}
	}
	if strings.Contains(page, "bash") || strings.Contains(page, "codex") {
		t.Fatalf("page lists CLI panes:\n%s", page)
	}
	if got := m.leafTitle(l); got != " local · Workspace " {
		t.Fatalf("title %q", got)
	}
	if got := m.viewLabel(v); got != "Workspace" {
		t.Fatalf("tab label %q", got)
	}

	// Projects sorted by name, git errors and no projects.
	mach := m.machines[0]
	mach.projects = append(mach.projects, proto.ProjectInfo{ID: "r0", Name: "Zeta", Path: "/src/zeta", Error: "not a repo"},
		proto.ProjectInfo{ID: "r2", Name: "alpha", Path: "/src/alpha"})
	page = ansi.Strip(strings.Join(m.workspaceLines(mach, 90), "\n"))
	if a, api, z := strings.Index(page, "alpha"), strings.Index(page, "api"), strings.Index(page, "Zeta"); !(a < api && api < z) || !strings.Contains(page, "git error") {
		t.Fatalf("order or error:\n%s", page)
	}
	m.workspaceLines(mach, 1) // a tiny split doesn't panic; the frame cuts lines to fit
	mach.projects = nil
	if page = ansi.Strip(strings.Join(m.workspaceLines(mach, 90), "\n")); !strings.Contains(page, "no projects yet") {
		t.Fatalf("empty:\n%s", page)
	}
}

func TestWorkspaceMenuHintsBroadcastBrain(t *testing.T) {
	m, _ := a1Fixture(t, true)
	ws := workspaceID(localMachine)
	a1At(t, m, ws)
	r, _ := m.selectedRow()

	mu := newRowMenu(*m, r, 0, 0)
	var keys []string
	for _, it := range mu.items {
		keys = append(keys, it.key)
	}
	if mu.title != "Workspace" || strings.Join(keys, "") != "atB" {
		t.Fatalf("menu %q %v", mu.title, keys)
	}

	_, items := m.statusHints()
	var hints []string
	for _, it := range items {
		hints = append(hints, ansi.Strip(it.text))
	}
	if got := strings.Join(hints, " "); !strings.Contains(got, "a project") || !strings.Contains(got, "B broadcast") || strings.Contains(got, "R reconnect") {
		t.Fatalf("hints: %s", got)
	}

	// Broadcast: the projects' agents and terminals, none of CLI's.
	label, targets := m.broadcastScope(false)
	var ids []string
	for _, tg := range targets {
		ids = append(ids, tg.pane.ID)
	}
	if label != "Workspace" || strings.Join(ids, ",") != "p1,p2" {
		t.Fatalf("broadcast %q %v", label, ids)
	}
	m.machines = append(m.machines, newMachine("box", "devbox", "x"))
	if label, _ := m.broadcastScope(false); label != "local › Workspace" {
		t.Fatalf("multi-machine label %q", label)
	}

	if got := m.world().Selected; got != "the projects on machine local" {
		t.Fatalf("brain: %q", got)
	}
}
