package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/sshtest"
)

// sshFilesFixture is box (a real server, remoteFilesFixture's) with a
// terminal on it where `ssh me@host` was typed — internal/sshtest playing
// ssh — listed in the tree once box says where it is logged in.
func sshFilesFixture(t *testing.T) (m *Model, host *sshtest.Host, pane proto.PaneInfo) {
	t.Helper()
	host = sshtest.Install(t)
	os.MkdirAll(filepath.Join(host.Home, "logs"), 0o755)
	os.WriteFile(filepath.Join(host.Home, "logs", "app.log"), []byte("started"), 0o644)
	os.WriteFile(filepath.Join(host.Home, "motd"), []byte("welcome to host"), 0o644)
	m, _, _ = remoteFilesFixture(t)
	box := m.machine("box")
	if err := callCtx(box.c, proto.MethodPaneCreate, proto.PaneCreateParams{Command: []string{host.SSH, "me@host"}, Cwd: t.TempDir(), NoProject: true}, &pane); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		var list proto.PaneList
		if err := callCtx(box.c, proto.MethodPaneList, nil, &list); err != nil {
			t.Fatal(err)
		}
		for _, p := range list.Panes {
			if p.ID == pane.ID && p.SSH == "me@host" {
				box.setPanes(list.Panes)
				m.rebuild()
				return m, host, p
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("never logged in: %+v", list.Panes)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// openSSHFilesView is f on the terminal's row, as a person does it.
func openSSHFilesView(t *testing.T, m *Model, pane proto.PaneInfo) *filesView {
	t.Helper()
	m.cursor, m.focus = paneNodeID("box", pane.ID), focusSidebar
	pump(t, m, a1Key(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")}))
	fv := m.tab().focused().files
	if fv == nil || fv.ssh != pane.ID {
		t.Fatalf("no explorer of the host: %+v", fv)
	}
	pump(t, m, fv.load(m))
	return fv
}

func TestSSHTerminalHasFiles(t *testing.T) {
	m, host, pane := sshFilesFixture(t)
	// A Files row under the terminal, saying which host.
	id := paneNodeID("box", pane.ID) + "/files"
	i := indexOfRow(m.rows, id)
	if i < 0 {
		t.Fatalf("no Files row under the terminal:\n%s", render(m.rows))
	}
	if r := m.rows[i]; r.kind != kindFiles || r.paneID != pane.ID || r.projectID != "" || r.label != "me@host" {
		t.Fatalf("row %+v", r)
	}
	if p := indexOfRow(m.rows, paneNodeID("box", pane.ID)); p != i-1 || m.rows[p].kids == 0 {
		t.Fatalf("not right under the terminal (%d, %d)", p, i)
	}

	// The row fits however narrow the sidebar gets (its inside, at the
	// narrowest the sidebar goes, and wider).
	for _, w := range []int{minSidebarWidth - 2, 30, 60} {
		for _, l := range m.sidebarLines(w, 40) {
			if ansi.StringWidth(l) > w {
				t.Fatalf("width %d: %q", w, l)
			}
		}
	}

	fv := openSSHFilesView(t, m, pane)
	for _, sz := range [][2]int{{1, 1}, {20, 5}, {39, 10}, {200, 50}} {
		for _, l := range fv.render(*m, sz[0], sz[1]) {
			if ansi.StringWidth(l) > sz[0] {
				t.Fatalf("%v: %q", sz, l)
			}
		}
	}
	if m.cursor != id || m.focus != focusMain {
		t.Fatalf("cursor %q", m.cursor)
	}
	if fv.root != host.Home || fv.home != host.Home {
		t.Fatalf("root %q home %q, want the host's %q", fv.root, fv.home, host.Home)
	}
	if got := strings.Join(filesPlainLines(fv), ","); got != "logs,motd" {
		t.Fatalf("listed %s", got)
	}
	// Titled by the host and where the ssh runs; ~ is the host's home.
	out := strings.Join(fv.render(*m, 120, 12), "\n")
	if !strings.Contains(out, "me@host (ssh from box)") || !strings.Contains(ansi.Strip(out), " ~") {
		t.Fatalf("render:\n%s", out)
	}
	if got := m.viewLabel(m.tab().focused().view); got != "me@host" {
		t.Fatalf("tab label %q", got)
	}
	// Into a folder, and a preview.
	filesSelect(t, fv, "logs")
	filesKey(t, m, "enter")
	if got := strings.Join(filesPlainLines(fv), ","); got != "logs,  app.log,motd" {
		t.Fatalf("opened: %s", got)
	}
	filesSelect(t, fv, "motd")
	filesKey(t, m, "enter")
	if fv.prev.res == nil || fv.prev.res.Data != "welcome to host" {
		t.Fatalf("preview: %+v", fv.prev)
	}
	// Nothing listed was box's own home.
	if strings.Contains(strings.Join(filesPlainLines(fv), ","), "notes.txt") {
		t.Fatal("box's own files")
	}
}

func TestSSHTerminalFilesCopy(t *testing.T) {
	m, host, pane := sshFilesFixture(t)
	fv := openSSHFilesView(t, m, pane)
	dl := downloadsDir()
	t.Cleanup(func() {
		entries, _ := os.ReadDir(dl)
		for _, e := range entries {
			os.RemoveAll(filepath.Join(dl, e.Name()))
		}
	})

	// D brings a file and a folder from the host.
	filesSelect(t, fv, "motd")
	filesKey(t, m, "D")
	if fileText(t, filepath.Join(dl, "motd")) != "welcome to host" || m.flashIsErr {
		t.Fatalf("download: %q", m.flash)
	}
	filesSelect(t, fv, "logs")
	filesKey(t, m, "D")
	if fileText(t, filepath.Join(dl, "logs", "app.log")) != "started" {
		t.Fatalf("folder: %q", m.flash)
	}

	// U sends one there, into the folder selected, and says so by the host.
	src := filepath.Join(t.TempDir(), "ca.pem")
	os.WriteFile(src, []byte("cert"), 0o600)
	filesKey(t, m, "U")
	d, ok := m.overlay.(*dialog)
	if !ok || !strings.Contains(d.title, "me@host") || !strings.Contains(strings.Join(d.text, " "), "~/logs") {
		t.Fatalf("dialog: %+v", m.overlay)
	}
	m.overlay = nil
	pump(t, m, d.submit(m, []string{src, ""}))
	if fileText(t, filepath.Join(host.Home, "logs", "ca.pem")) != "cert" {
		t.Fatalf("upload: %q", m.flash)
	}
	if !strings.Contains(m.flash, "uploaded ca.pem to me@host · ~/logs/ca.pem") {
		t.Fatalf("flash %q", m.flash)
	}
	if got := strings.Join(filesPlainLines(fv), ","); !strings.Contains(got, "  ca.pem") {
		t.Fatalf("not shown after: %s", got)
	}
	// Not on box: box's own home has no logs folder.
	if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), "logs")); err == nil {
		t.Fatal("copied to box rather than the host")
	}
}

// TestSSHTerminalFilesFromThisComputer: a terminal on this computer is the
// commonest, and its host's files are not this computer's — the refusal
// of a machine's own Files on this computer does not apply.
func TestSSHTerminalFilesFromThisComputer(t *testing.T) {
	m, _ := a1Fixture(t, false)
	fv := &filesView{machine: localMachine, ssh: "p1", host: "me@host"}
	m.flash = ""
	m.copyClient(fv)
	if strings.Contains(m.flash, "on this computer already") {
		t.Fatalf("refused as this computer's: %q", m.flash)
	}
}

func TestSSHFilesRowOnlyWhileLoggedIn(t *testing.T) {
	mid := "box"
	p := proto.PaneInfo{ID: "p1", State: proto.PaneRunning, SSH: "me@host"}
	if rows := subagentRows(mid, p, 4); len(rows) != 1 || rows[0].kind != kindFiles || rows[0].depth != 4 {
		t.Fatalf("logged in: %+v", rows)
	}
	p.SSH = ""
	if rows := subagentRows(mid, p, 4); len(rows) != 0 {
		t.Fatalf("logged out: %+v", rows)
	}
	p.SSH, p.State = "me@host", proto.PaneExited
	if rows := subagentRows(mid, p, 4); len(rows) != 0 {
		t.Fatalf("exited: %+v", rows)
	}
	// An agent's subagents come after it.
	p.State, p.Agent = proto.PaneRunning, &proto.AgentStatus{Subagents: []proto.Subagent{{ID: "s1"}}}
	if rows := subagentRows(mid, p, 4); len(rows) != 2 || rows[0].kind != kindFiles || rows[1].kind != kindSubagent {
		t.Fatalf("with a subagent: %+v", rows)
	}
}

func TestTildeUnder(t *testing.T) {
	for _, c := range []struct{ home, p, want string }{
		{"/home/me", "/home/me", "~"},
		{"/home/me", "/home/me/logs/a", "~/logs/a"},
		{"/home/me", "/home/meat", "/home/meat"},
		{"/home/me", "/etc", "/etc"},
		{"", "/x", "/x"},
		{"/", "/x", "/x"},
	} {
		if got := tildeUnder(c.home, c.p); got != c.want {
			t.Errorf("%q under %q: %q", c.p, c.home, got)
		}
	}
}

// TestSSHTerminalLogsOut: the explorer of a terminal's host says when the
// terminal leaves it, keeps its name, and comes back when it logs in again.
func TestSSHTerminalLogsOut(t *testing.T) {
	m, host, pane := sshFilesFixture(t)
	fv := openSSHFilesView(t, m, pane)
	box := m.machine("box")

	out := pane
	out.SSH = ""
	pump(t, m, m.handleEvent(box, eventMsg(t, proto.EventPaneUpdated, out)))
	if !strings.Contains(fv.err, "no longer logged in to me@host") {
		t.Fatalf("err %q", fv.err)
	}
	if indexOfRow(m.rows, paneNodeID("box", pane.ID)+"/files") >= 0 {
		t.Fatal("the Files row stayed")
	}
	// The cursor, which was on the Files row, goes to the terminal.
	if m.cursor != paneNodeID("box", pane.ID) {
		t.Fatalf("cursor %q", m.cursor)
	}
	// An explorer still open says so, under the host's name.
	if got := m.sshHostOf("box", pane.ID); got != "me@host" {
		t.Fatalf("host after: %q", got)
	}
	if !strings.Contains(strings.Join(fv.render(*m, 120, 10), "\n"), "no longer logged in") {
		t.Fatal("not said on screen")
	}

	// In again, to the same host: listed again.
	pump(t, m, m.handleEvent(box, eventMsg(t, proto.EventPaneUpdated, pane)))
	if fv.err != "" || fv.root != host.Home || len(filesPlainLines(fv)) == 0 {
		t.Fatalf("back: err %q root %q", fv.err, fv.root)
	}
	// To another host: that host's files, from its home. (The stand-in
	// serves the same folder for every host.)
	other := pane
	other.SSH = "me@other"
	pump(t, m, m.handleEvent(box, eventMsg(t, proto.EventPaneUpdated, other)))
	if fv.host != "me@other" || fv.err != "" || fv.root != host.Home {
		t.Fatalf("another host: %q err %q root %q", fv.host, fv.err, fv.root)
	}
}
