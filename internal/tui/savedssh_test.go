package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

func TestSavedSSHKindGoesLast(t *testing.T) {
	// Saved tabs store kinds by number: the new kind must not move others.
	if kindReviewQueue != 12 || kindSavedSSH != 13 {
		t.Fatalf("kinds moved: queue %d, saved ssh %d", kindReviewQueue, kindSavedSSH)
	}
}

func TestSSHTargetOfPane(t *testing.T) {
	for _, c := range []struct {
		p    proto.PaneInfo
		want string
	}{
		{proto.PaneInfo{}, ""},
		{proto.PaneInfo{Command: []string{"/usr/bin/ssh", "-F", "c", "--", "box"}}, "box"},
		{proto.PaneInfo{Command: []string{"ssh", "box"}}, ""}, // not started by conch
		{proto.PaneInfo{Command: []string{"zsh", "--", "box"}}, ""},
		{proto.PaneInfo{Command: []string{"ssh", "--", "box"}, Agent: &proto.AgentStatus{Name: "claude"}}, ""},
		{proto.PaneInfo{Command: []string{"--", "box"}}, ""},
	} {
		if got := sshTarget(c.p); got != c.want {
			t.Errorf("sshTarget(%q) = %q, want %q", c.p.Command, got, c.want)
		}
	}
}

func TestCleanAndIdleSavedSSH(t *testing.T) {
	if got := cleanSavedSSH(nil); got != nil {
		t.Fatalf("nil: %q", got)
	}
	got := cleanSavedSSH([]string{"box", "", "-oProxyCommand=x", "a b", "box", "me@db", "x\n"})
	if strings.Join(got, ",") != "box,me@db" {
		t.Fatalf("clean %q", got)
	}
	login := func(target string) proto.PaneInfo {
		return proto.PaneInfo{Command: []string{"ssh", "-F", "c", "--", target}}
	}
	if got := idleSavedSSH([]string{"a", "b", "c"}, []proto.PaneInfo{login("b"), login("zzz")}); strings.Join(got, ",") != "a,c" {
		t.Fatalf("idle %q", got)
	}
	if got := idleSavedSSH(nil, []proto.PaneInfo{login("a")}); got != nil {
		t.Fatalf("idle with none saved %q", got)
	}
}

func TestSavedSSHInTree(t *testing.T) {
	m, _ := sshFixture(t, false) // p5 is an open session to "box"
	m.savedSSH = []string{"box", "ssh://me@db:2222"}
	m.rebuild()

	// A host with a session open is listed by the session alone.
	if indexOfRow(m.rows, savedSSHID("box")) >= 0 {
		t.Fatalf("box listed twice:\n%s", render(m.rows))
	}
	i := indexOfRow(m.rows, savedSSHID("ssh://me@db:2222"))
	if i < 0 {
		t.Fatalf("saved host missing:\n%s", render(m.rows))
	}
	if r := m.rows[i]; r.kind != kindSavedSSH || r.depth != 3 || r.machine != localMachine || r.expandable() {
		t.Fatalf("saved row %+v", r)
	}
	// After the open sessions, last in the tree.
	if m.rows[i-1].id != paneNodeID(localMachine, "p5") || i != len(m.rows)-1 {
		t.Fatalf("order:\n%s", render(m.rows))
	}
	if r := m.rows[indexOfRow(m.rows, looseSSHID(localMachine))]; r.count != 2 {
		t.Fatalf("SSH count %d", r.count)
	}
	if r := m.rows[indexOfRow(m.rows, cliID(localMachine))]; r.count != 5 {
		t.Fatalf("CLI count %d", r.count)
	}

	// The session ends: the host is listed saved again.
	m.machines[0].panes = m.machines[0].panes[:4]
	m.rebuild()
	if indexOfRow(m.rows, savedSSHID("box")) < 0 {
		t.Fatalf("box not back:\n%s", render(m.rows))
	}

	// With nothing running outside projects, CLI and SSH still hold them.
	m.machines[0].panes = m.machines[0].panes[:2]
	m.rebuild()
	for _, id := range []string{cliID(localMachine), looseSSHID(localMachine), savedSSHID("box")} {
		if indexOfRow(m.rows, id) < 0 {
			t.Fatalf("no %s:\n%s", id, render(m.rows))
		}
	}
	if indexOfRow(m.rows, looseTerminalsID(localMachine)) >= 0 {
		t.Fatal("an empty Terminals section appeared")
	}

	// The filter matches hosts by name; the waiting filter leaves them out.
	m.filter = "db"
	m.rebuild()
	if indexOfRow(m.rows, savedSSHID("ssh://me@db:2222")) < 0 || indexOfRow(m.rows, savedSSHID("box")) >= 0 {
		t.Fatalf("filter:\n%s", render(m.rows))
	}
	m.filter = waitingFilter
	m.rebuild()
	if indexOfRow(m.rows, savedSSHID("box")) >= 0 {
		t.Fatal("waiting filter lists a saved host")
	}
	m.filter = ""

	// Folded SSH hides them.
	m.expanded[looseSSHID(localMachine)] = false
	m.rebuild()
	if indexOfRow(m.rows, savedSSHID("box")) >= 0 {
		t.Fatal("folded SSH lists a saved host")
	}
	m.expanded[looseSSHID(localMachine)] = true

	// Only this computer lists them: ssh sessions start here.
	rm := newMachine("dev", "dev", "dev@box")
	rm.state = stateOnline
	m.machines = append(m.machines, rm)
	m.rebuild()
	if indexOfRow(m.rows, looseSSHID("dev")) >= 0 {
		t.Fatalf("remote machine lists saved hosts:\n%s", render(m.rows))
	}
}

func TestSavedSSHRowAndPage(t *testing.T) {
	m, _ := sshFixture(t, false)
	m.savedSSH = []string{"ssh://me@db:2222/"}
	m.rebuild()
	id := savedSSHID("ssh://me@db:2222/")
	glyph, _, label, _, right := m.rowParts(m.rows[indexOfRow(m.rows, id)])
	if glyph != "○" || label != "ssh me@db:2222" || ansi.Strip(right) != "saved" {
		t.Fatalf("row parts %q %q %q", glyph, label, right)
	}

	// Moving onto it shows its page, grouped with CLI · ssh.
	a1At(t, m, id)
	v := m.tab().focused().view
	if v.Kind != kindSavedSSH || v.Row != id {
		t.Fatalf("shows %+v", v)
	}
	if got := m.leafTitle(m.tab().focused()); got != " ssh · me@db:2222 " {
		t.Fatalf("title %q", got)
	}
	if s := m.rowScope(m.rows[indexOfRow(m.rows, id)]); s.level != scopeCLI || s.section != kindSSH || s.machine != localMachine {
		t.Fatalf("scope %+v", s)
	}
	out := a2Plain(m.leafLines(m.tab().focused(), 100, 20, false))
	for _, want := range []string{"ssh me@db:2222", "not connected", "x forget"} {
		if !strings.Contains(out, want) {
			t.Fatalf("page lacks %q:\n%s", want, out)
		}
	}
	for _, w := range []int{1, 5, 20, 39, 100} {
		for _, l := range savedSSHLines("a-very-long-host-name.example.internal", w) {
			if ansi.StringWidth(l) > w {
				t.Fatalf("width %d: %q is %d wide", w, ansi.Strip(l), ansi.StringWidth(l))
			}
		}
	}
	for _, size := range [][2]int{{1, 1}, {20, 5}, {39, 12}, {200, 50}} {
		m.width, m.height = size[0], size[1]
		for _, l := range strings.Split(m.View(), "\n") {
			if ansi.StringWidth(l) > m.width {
				t.Fatalf("%v: line %q too wide", size, ansi.Strip(l))
			}
		}
	}

	// Its hints and menu offer connecting and forgetting.
	_, items := m.statusHints()
	var hints []string
	for _, it := range items {
		hints = append(hints, ansi.Strip(it.text))
	}
	if got := strings.Join(hints, "|"); !strings.HasPrefix(got, "enter connect|x forget|H ssh|m menu") {
		t.Fatalf("hints %q", got)
	}
	mu := newRowMenu(*m, m.rows[indexOfRow(m.rows, id)], 0, 0)
	if mu.title != "ssh me@db:2222" || len(mu.items) != 2 || mu.items[0].key != "enter" || mu.items[1].key != "x" {
		t.Fatalf("menu %q %+v", mu.title, mu.items)
	}
}

func TestSSHAsksToSave(t *testing.T) {
	sshEnv(t, "")
	m, peer := sshFixture(t, true)
	m.statePath = filepath.Join(t.TempDir(), "ui.json")
	creates := func() int {
		n := 0
		for _, method := range peer.methods() {
			if method == proto.MethodPaneCreate {
				n++
			}
		}
		return n
	}

	// A bad host is refused before any question.
	if got := a2ErrText(a2Run(m.connectSSH("-oProxyCommand=x"))); got == "" || m.overlay != nil {
		t.Fatalf("bad host: %q, overlay %T", got, m.overlay)
	}

	// Enter takes the default: connect without saving.
	if cmd := m.connectSSH("box"); cmd != nil {
		t.Fatal("connected before asking")
	}
	mu, ok := m.overlay.(*menu)
	if !ok || !strings.Contains(mu.title, "box") || mu.sel != 0 || mu.items[0].key != "n" {
		t.Fatalf("question %T %+v", m.overlay, m.overlay)
	}
	a2CheckBox(t, mu.render(*m), *m)
	a2Run(a1Key(t, m, tea.KeyMsg{Type: tea.KeyEnter}))
	peer.waitFor(t, "pane.create", func(msg proto.Message) bool { return msg.Method == proto.MethodPaneCreate })
	if len(m.savedSSH) != 0 {
		t.Fatalf("saved by default: %q", m.savedSSH)
	}
	if _, err := os.Stat(m.statePath); err == nil {
		b, _ := os.ReadFile(m.statePath)
		if strings.Contains(string(b), "saved_ssh") {
			t.Fatalf("ui.json saved the host:\n%s", b)
		}
	}

	// esc asks nothing more and starts nothing.
	before := creates()
	m.connectSSH("box")
	a2Run(a1Key(t, m, tea.KeyMsg{Type: tea.KeyEsc}))
	if m.overlay != nil || len(m.savedSSH) != 0 || creates() != before {
		t.Fatalf("esc: overlay %T saved %q", m.overlay, m.savedSSH)
	}

	// y saves and connects; the host is written to ui.json.
	m.connectSSH("box")
	a2Run(a1Key(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")}))
	if strings.Join(m.savedSSH, ",") != "box" {
		t.Fatalf("saved %q", m.savedSSH)
	}
	if creates() != before+1 {
		peer.waitFor(t, "second pane.create", func(msg proto.Message) bool { return creates() == before+1 })
	}
	if st := loadUIState(m.statePath); strings.Join(st.SavedSSH, ",") != "box" {
		t.Fatalf("ui.json holds %q", st.SavedSSH)
	}

	// A saved host connects without asking again, and isn't saved twice.
	if cmd := m.connectSSH("box"); cmd == nil || m.overlay != nil {
		t.Fatalf("saved host asked again: %T", m.overlay)
	}
	if a2Run(m.saveSSH("box")); len(m.savedSSH) != 1 {
		t.Fatalf("saved twice %q", m.savedSSH)
	}
	// Saved hosts come first in the H menu.
	if hosts := m.sshHosts(); len(hosts) == 0 || hosts[0] != "box" {
		t.Fatalf("hosts %q", hosts)
	}
}

func TestSavedSSHConnectsAndForgets(t *testing.T) {
	sshEnv(t, "")
	m, peer := sshFixture(t, true)
	m.machines[0].panes = m.machines[0].panes[:4] // no session to box
	m.savedSSH = []string{"box", "db"}
	m.rebuild()
	id := savedSSHID("box")

	// Enter on the row connects straight away.
	a1At(t, m, id)
	a2Run(a1Key(t, m, tea.KeyMsg{Type: tea.KeyEnter}))
	if m.overlay != nil {
		t.Fatalf("enter asked %T", m.overlay)
	}
	create := peer.waitFor(t, "pane.create", func(msg proto.Message) bool { return msg.Method == proto.MethodPaneCreate })
	var p proto.PaneCreateParams
	json.Unmarshal(create.Params, &p)
	if p.Name != "ssh box" || p.Command[len(p.Command)-1] != "box" || !p.NoProject {
		t.Fatalf("params %+v", p)
	}

	// So does a click on it.
	i := indexOfRow(m.rows, savedSSHID("db")) - m.scroll
	a2Run(a1Mouse(t, m, 6, 2+i, a1Left, a1Press))
	peer.waitFor(t, "pane.create for db", func(msg proto.Message) bool {
		if msg.Method != proto.MethodPaneCreate {
			return false
		}
		var p proto.PaneCreateParams
		json.Unmarshal(msg.Params, &p)
		return p.Name == "ssh db"
	})

	// x asks, then forgets it; a no leaves it.
	m.statePath = filepath.Join(t.TempDir(), "ui.json")
	a1At(t, m, id)
	m.focus = focusSidebar
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if d, ok := m.overlay.(*dialog); !ok || !d.confirm || !strings.Contains(strings.Join(d.text, " "), "Forget ssh box") {
		t.Fatalf("x opened %T", m.overlay)
	}
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if len(m.savedSSH) != 2 {
		t.Fatalf("esc forgot: %q", m.savedSSH)
	}
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	a2Run(a1Key(t, m, tea.KeyMsg{Type: tea.KeyEnter}))
	if strings.Join(m.savedSSH, ",") != "db" || indexOfRow(m.rows, id) >= 0 {
		t.Fatalf("after forget: %q\n%s", m.savedSSH, render(m.rows))
	}
	if _, l := m.tabShowing(id); l != nil {
		t.Fatal("a tab still shows the forgotten host")
	}
	if st := loadUIState(m.statePath); strings.Join(st.SavedSSH, ",") != "db" {
		t.Fatalf("ui.json holds %q", st.SavedSSH)
	}
	// Forgetting what isn't saved does nothing.
	if cmd := m.forgetSSH("nope"); cmd != nil || len(m.savedSSH) != 1 {
		t.Fatal("forgot an unsaved host")
	}
}

func TestSavedSSHState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ui.json")

	// A file from before saved hosts loads with none.
	if err := os.WriteFile(path, []byte(`{"expanded":{"m:local":true},"sidebar_width":30}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if st := loadUIState(path); st.SavedSSH != nil || st.SidebarWidth != 30 {
		t.Fatalf("old file: %+v", st)
	}
	// They round-trip, and none leaves the key out.
	if err := saveUIState(path, uiState{SavedSSH: []string{"box", "me@db"}}); err != nil {
		t.Fatal(err)
	}
	if st := loadUIState(path); strings.Join(st.SavedSSH, ",") != "box,me@db" {
		t.Fatalf("round trip %q", st.SavedSSH)
	}
	if err := saveUIState(path, uiState{}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); strings.Contains(string(b), "saved_ssh") {
		t.Fatalf("empty list written:\n%s", b)
	}
	// A hand-edited list is cleaned when the TUI takes it.
	if got := cleanSavedSSH([]string{"box", "box", "-x"}); strings.Join(got, ",") != "box" {
		t.Fatalf("clean %q", got)
	}

	// The model's list is copied into what is saved, not shared.
	m, _ := sshFixture(t, false)
	m.statePath = filepath.Join(dir, "m.json")
	m.savedSSH = []string{"box"}
	cmd := m.saveState()
	m.savedSSH[0] = "changed"
	cmd()
	if st := loadUIState(m.statePath); strings.Join(st.SavedSSH, ",") != "box" {
		t.Fatalf("saved %q", st.SavedSSH)
	}
}
