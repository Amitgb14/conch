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

// sshEnv isolates HOME and CONCH_HOME, and writes ~/.ssh/config when
// hostsConfig isn't empty.
func sshEnv(t *testing.T, hostsConfig string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CONCH_HOME", t.TempDir())
	t.Setenv("CONCH_SOCKET", "")
	t.Setenv("CONCH_PANE_ID", "")
	t.Setenv("CONCH_SSH", "/fake/bin/ssh")
	t.Setenv("CONCH_SSH_CONFIG", "")
	if hostsConfig != "" {
		if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, ".ssh", "config"), []byte(hostsConfig), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

// sshFixture adds ssh panes to a1Fixture: p5 outside every project, p6 in
// the api project, and p7 an agent whose command happens to be ssh.
func sshFixture(t *testing.T, withClient bool) (*Model, *a1Peer) {
	t.Helper()
	m, peer := a1Fixture(t, withClient)
	login := []string{"/usr/bin/ssh", "-F", "/cfg", "--", "box"}
	m.machines[0].panes = append(m.machines[0].panes,
		proto.PaneInfo{ID: "p5", Name: "ssh box", Command: login, State: proto.PaneRunning},
		proto.PaneInfo{ID: "p6", Name: "ssh db", Command: []string{"ssh", "db"}, State: proto.PaneRunning, ProjectID: "r1"},
		proto.PaneInfo{ID: "p7", Name: "claude", Command: []string{"ssh", "gpu"}, State: proto.PaneRunning,
			Agent: &proto.AgentStatus{Name: "claude", State: proto.AgentIdle}},
	)
	m.rebuild()
	return m, peer
}

func TestSSHKindKeepsSavedKinds(t *testing.T) {
	// Views are saved in ui.json by kind number: new kinds go last.
	if kindWorkspace != 10 || kindSSH != 11 {
		t.Fatalf("kind numbers moved: workspace %d, ssh %d", kindWorkspace, kindSSH)
	}
	var v viewRef
	if err := json.Unmarshal([]byte(`{"row":"m:local/terminals","kind":4,"machine":"local"}`), &v); err != nil || v.Kind != kindTerminals {
		t.Fatalf("old saved view: %+v %v", v, err)
	}
}

func TestSSHPane(t *testing.T) {
	for _, c := range []struct {
		p    proto.PaneInfo
		want bool
	}{
		{proto.PaneInfo{}, false},
		{proto.PaneInfo{Command: []string{"zsh", "-l"}}, false},
		{proto.PaneInfo{Command: []string{"/usr/bin/ssh", "box"}}, true},
		{proto.PaneInfo{Command: []string{"ssh", "box"}, Agent: &proto.AgentStatus{Name: "claude"}}, false},
	} {
		if got := sshPane(c.p); got != c.want {
			t.Errorf("sshPane(%+v) = %v", c.p, got)
		}
	}
}

func TestSSHSectionInTree(t *testing.T) {
	m, _ := sshFixture(t, false)
	i := indexOfRow(m.rows, looseSSHID(localMachine))
	if i < 0 {
		t.Fatalf("no SSH section:\n%s", render(m.rows))
	}
	if r := m.rows[i]; r.kind != kindSSH || r.count != 1 || r.depth != 2 || !r.expandable() {
		t.Fatalf("SSH row %+v", r)
	}
	// Last in CLI: after Agents and Terminals, with its session under it.
	if m.rows[i+1].id != paneNodeID(localMachine, "p5") || i+2 != len(m.rows) {
		t.Fatalf("SSH children:\n%s", render(m.rows))
	}
	if j := indexOfRow(m.rows, looseTerminalsID(localMachine)); j < 0 || j > i || m.rows[j].count != 1 {
		t.Fatalf("CLI terminals should keep only bash:\n%s", render(m.rows))
	}
	if cli := m.rows[indexOfRow(m.rows, cliID(localMachine))]; cli.count != 4 {
		t.Fatalf("CLI counts %d panes", cli.count)
	}
	// ssh in a project stays a terminal of that project; an agent stays an agent.
	if r := m.rows[indexOfRow(m.rows, sectionID(localMachine, "r1", "terminals"))]; r.count != 2 {
		t.Fatalf("project terminals %+v", r)
	}
	if r := m.rows[indexOfRow(m.rows, machineID(localMachine)+"/agents")]; r.count != 2 {
		t.Fatalf("CLI agents %+v", r)
	}
	for id, want := range map[string]nodeKind{"p5": kindSSH, "p6": kindTerminals, "p7": kindAgents, "p3": kindTerminals, "gone": kindTerminals} {
		if got := m.paneSection(localMachine, id); got != want {
			t.Errorf("paneSection(%s) = %d, want %d", id, got, want)
		}
	}

	// Folding hides the session; the filter finds it.
	m.expanded[looseSSHID(localMachine)] = false
	m.rebuild()
	if indexOfRow(m.rows, paneNodeID(localMachine, "p5")) >= 0 {
		t.Fatal("folded SSH still lists its session")
	}
	m.filter = "ssh box"
	m.rebuild()
	if indexOfRow(m.rows, paneNodeID(localMachine, "p5")) < 0 || indexOfRow(m.rows, looseTerminalsID(localMachine)) >= 0 {
		t.Fatalf("filter:\n%s", render(m.rows))
	}

	// No ssh sessions, no section.
	m.filter = ""
	m.machines[0].panes = m.machines[0].panes[:4]
	m.rebuild()
	if indexOfRow(m.rows, looseSSHID(localMachine)) >= 0 {
		t.Fatal("empty SSH section shown")
	}

	// Another machine's ssh panes go under its own CLI.
	rm := newMachine("dev", "dev", "dev@box")
	rm.state = stateOnline
	rm.panes = []proto.PaneInfo{{ID: "q1", Name: "ssh x", Command: []string{"ssh", "x"}, State: proto.PaneRunning}}
	m.machines = append(m.machines, rm)
	m.rebuild()
	if indexOfRow(m.rows, looseSSHID("dev")) < 0 || indexOfRow(m.rows, paneNodeID("dev", "q1")) < 0 {
		t.Fatalf("remote ssh section:\n%s", render(m.rows))
	}
}

func TestSSHSectionRowAndPage(t *testing.T) {
	m, _ := sshFixture(t, false)
	glyph, _, label, _, right := m.rowParts(row{kind: kindSSH, count: 3})
	if glyph != "" || label != "SSH" || ansi.Strip(right) != "3" {
		t.Fatalf("row parts %q %q %q", glyph, label, right)
	}

	a1At(t, m, looseSSHID(localMachine))
	v := m.tab().focused().view
	if !m.previewing || v.Kind != kindSSH {
		t.Fatalf("SSH row shows %+v (previewing %v)", v, m.previewing)
	}
	if got := m.leafTitle(m.tab().focused()); got != " ssh · CLI " {
		t.Fatalf("title %q", got)
	}
	if got := m.scopeName(m.rowScope(m.rows[indexOfRow(m.rows, looseSSHID(localMachine))])); got != "CLI · ssh" {
		t.Fatalf("scope name %q", got)
	}
	out := a2Plain(m.leafLines(m.tab().focused(), 100, 20, false))
	for _, want := range []string{"SSH", "1 in CLI", "ssh box", "click a session to open it", "H ssh to a host"} {
		if !strings.Contains(out, want) {
			t.Fatalf("page lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "bash") || strings.Contains(out, "claude") {
		t.Fatalf("page lists non-ssh panes:\n%s", out)
	}
	// Terminals pages leave ssh sessions out and point at H.
	if out := a2Plain(m.sectionLines(localMachine, "", kindTerminals, 100)); strings.Contains(out, "ssh box") || !strings.Contains(out, "H ssh to a host") {
		t.Fatalf("CLI terminals page:\n%s", out)
	}
	if out := a2Plain(m.sectionLines(localMachine, "r1", kindTerminals, 100)); !strings.Contains(out, "ssh db") {
		t.Fatalf("project terminals page:\n%s", out)
	}
	for _, w := range []int{1, 5, 20, 39, 100} {
		for _, l := range m.sectionLines(localMachine, "", kindSSH, w) {
			if ansi.StringWidth(l) > w {
				t.Fatalf("width %d: line %q is %d wide", w, ansi.Strip(l), ansi.StringWidth(l))
			}
		}
	}
	if out := a2Plain(m.sectionLines(localMachine, "", kindSSH, 80)); strings.Count(out, "\n") < 3 {
		t.Fatalf("page too short:\n%s", out)
	}

	// Clicking the session in the main area opens it.
	rects, _ := m.leafRects()
	in := m.inner(rects[m.tab().focus])
	a2Run(a1Mouse(t, m, in.x+4, in.y+branchesPageHeader, a1Left, a1Press))
	if m.cursor != paneNodeID(localMachine, "p5") || m.tab().focused().view.PaneID != "p5" {
		t.Fatalf("click opened %q, shows %+v", m.cursor, m.tab().focused().view)
	}
	// Its tab belongs to CLI · ssh, not CLI · terminals.
	s := m.tabScopeOf(m.tab())
	if s.level != scopeCLI || s.section != kindSSH {
		t.Fatalf("scope %+v", s)
	}
	a1At(t, m, looseTerminalsID(localMachine))
	for _, i := range m.visibleTabs() {
		if m.tabs[i].root.leaves()[0].view.PaneID == "p5" && i != m.activeTab {
			t.Fatal("the ssh tab is listed under Terminals")
		}
	}
	a1At(t, m, looseSSHID(localMachine))
	if len(m.visibleTabs()) != 1 {
		t.Fatalf("SSH lists %d tabs", len(m.visibleTabs()))
	}
}

func TestSSHKeyOpensDialogWithoutHosts(t *testing.T) {
	sshEnv(t, "")
	m, _ := sshFixture(t, true)
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("H")})
	d, ok := m.overlay.(*dialog)
	if !ok || !strings.Contains(d.title, "SSH") {
		t.Fatalf("H opened %T", m.overlay)
	}
	a2CheckBox(t, d.render(*m), *m)

	// An empty host is refused before anything is started.
	cmd := a1Key(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if got := a2ErrText(a2Run(cmd)); !strings.Contains(got, "host is required") {
		t.Fatalf("empty host: %q", got)
	}
}

func TestSSHKeyOffline(t *testing.T) {
	sshEnv(t, "")
	m, _ := sshFixture(t, false) // no client
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("H")})
	if m.overlay != nil || !strings.Contains(m.flash, "local is") {
		t.Fatalf("offline: overlay %T, flash %q", m.overlay, m.flash)
	}
	if cmd := m.startSSH("box"); !strings.Contains(a2ErrText(a2Run(cmd)), "local is") {
		t.Fatal("startSSH offline")
	}
}

func TestSSHHostMenu(t *testing.T) {
	sshEnv(t, "Host web db\n  User me\nHost *.internal\nHost web\n")
	m, peer := sshFixture(t, true)
	rm := newMachine("dev", "dev", "dev@box")
	m.machines = append(m.machines, rm, newMachine("dup", "dup", "db"))
	m.rebuild()
	if got := strings.Join(m.sshHosts(), ","); got != "web,db,dev@box" {
		t.Fatalf("hosts %q", got)
	}

	a1Key(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("H")})
	mu, ok := m.overlay.(*menu)
	if !ok {
		t.Fatalf("H opened %T", m.overlay)
	}
	var labels []string
	for _, it := range mu.items {
		labels = append(labels, it.key+" "+it.label)
	}
	if got := strings.Join(labels, "|"); got != "1 web|2 db|3 dev@box|e Enter a host…" {
		t.Fatalf("menu %q", got)
	}
	a2CheckBox(t, mu.render(*m), *m)

	// Clicking a host starts the session on this computer.
	b := mu.render(*m)
	msgs := a2Run(a1Mouse(t, m, b.x+3, b.y+3, a1Left, a1Press))
	create := peer.waitFor(t, "pane.create", func(msg proto.Message) bool { return msg.Method == proto.MethodPaneCreate })
	var p proto.PaneCreateParams
	if err := json.Unmarshal(create.Params, &p); err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	if p.Name != "ssh dev@box" || !p.NoProject || p.Cwd != home || p.Agent != "" || p.Cols <= 0 || p.Rows <= 0 {
		t.Fatalf("create params %+v", p)
	}
	if len(p.Command) != 5 || p.Command[0] != "/fake/bin/ssh" || p.Command[1] != "-F" || p.Command[3] != "--" || p.Command[4] != "dev@box" {
		t.Fatalf("command %q", p.Command)
	}
	if len(msgs) != 1 {
		t.Fatalf("messages %#v", msgs)
	}
	if _, ok := msgs[0].(createdMsg); !ok {
		t.Fatalf("got %#v", msgs[0])
	}

	// e types a host instead.
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("H")})
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	if _, ok := m.overlay.(*dialog); !ok {
		t.Fatalf("e opened %T", m.overlay)
	}
}

func TestSSHHostMenuFitsScreen(t *testing.T) {
	var cfg strings.Builder
	for i := range 60 {
		cfg.WriteString("Host h" + string(rune('a'+i%26)) + string(rune('a'+i/26)) + "\n")
	}
	sshEnv(t, cfg.String())
	m, _ := sshFixture(t, true)
	for _, size := range [][2]int{{160, 40}, {40, 12}, {20, 5}, {20, 4}, {1, 1}} {
		m.width, m.height = size[0], size[1]
		m.overlay = nil
		a1Key(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("H")})
		mu, ok := m.overlay.(*menu)
		if !ok {
			t.Fatalf("%v: %T", size, m.overlay)
		}
		// Two lines of border and at least one host: a 1-line screen can't fit.
		if b := mu.render(*m); len(b.lines) > max(m.height, 4) {
			t.Fatalf("%v: menu %d lines", size, len(b.lines))
		}
		if last := mu.items[len(mu.items)-1]; last.key != "e" {
			t.Fatalf("%v: typing a host isn't offered", size)
		}
	}
}

func TestSSHStart(t *testing.T) {
	sshEnv(t, "")
	m, peer := sshFixture(t, true)

	// Hosts ssh would read as options, or as several words, never start.
	for _, bad := range []string{"", "-oProxyCommand=touch x", "a b", "box\n"} {
		if got := a2ErrText(a2Run(m.startSSH(bad))); got == "" {
			t.Fatalf("%q started", bad)
		}
	}
	for _, method := range peer.methods() {
		if method == proto.MethodPaneCreate {
			t.Fatal("a bad host reached the server")
		}
	}

	// The dialog trims what was typed.
	d := newSSHDialog(*m)
	m.overlay = d
	d.fields[0].in.SetValue("  ssh://me@box:2222  ")
	cmd := a1Key(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	a2Run(cmd)
	create := peer.waitFor(t, "pane.create", func(msg proto.Message) bool { return msg.Method == proto.MethodPaneCreate })
	var p proto.PaneCreateParams
	json.Unmarshal(create.Params, &p)
	if p.Name != "ssh me@box:2222" || p.Command[len(p.Command)-1] != "ssh://me@box:2222" {
		t.Fatalf("params %+v", p)
	}

	// The server refusing is reported.
	peer.setError(proto.MethodPaneCreate, "no pty")
	if got := a2ErrText(a2Run(m.startSSH("box"))); !strings.Contains(got, "no pty") {
		t.Fatalf("server error %q", got)
	}

	// The new pane lands in the SSH section and is shown.
	peer.setError(proto.MethodPaneCreate, "")
	info := proto.PaneInfo{ID: "p9", Name: "ssh box", Command: []string{"/fake/bin/ssh", "-F", "c", "--", "box"}, State: proto.PaneRunning}
	next, _ := m.Update(createdMsg{machine: localMachine, info: info})
	*m = next.(Model)
	if m.cursor != paneNodeID(localMachine, "p9") || m.focus != focusMain {
		t.Fatalf("created: cursor %q focus %v", m.cursor, m.focus)
	}
	if r := m.rows[indexOfRow(m.rows, looseSSHID(localMachine))]; r.count != 2 {
		t.Fatalf("SSH count %d", r.count)
	}
}

func TestSSHTabMenu(t *testing.T) {
	sshEnv(t, "")
	m, peer := sshFixture(t, true)
	a1At(t, m, cliID(localMachine))
	mr := m.mainRect()
	_, hits := m.tabBar(mr.w)
	plus := -1
	for _, h := range hits {
		if h.tab == -1 {
			plus = mr.x + h.x0
		}
	}
	a1Mouse(t, m, plus, mr.y, a1Left, a1Press)
	mu, ok := m.overlay.(*menu)
	if !ok {
		t.Fatalf("+ opened %T", m.overlay)
	}
	var keys []string
	for _, it := range mu.items {
		keys = append(keys, it.key)
	}
	if strings.Join(keys, "") != "tncH" || !strings.Contains(mu.items[1].label, "local") {
		t.Fatalf("items %+v", mu.items)
	}
	b := mu.render(*m)
	a2CheckBox(t, b, *m)
	if b.y != mr.y+1 {
		t.Fatalf("menu at y %d, want under the bar", b.y)
	}

	// SSH to a host… opens the ssh dialog (no hosts configured).
	a1Mouse(t, m, b.x+2, b.y+4, a1Left, a1Press)
	if d, ok := m.overlay.(*dialog); !ok || !strings.Contains(d.title, "SSH") {
		t.Fatalf("SSH item opened %T", m.overlay)
	}

	// Agent… opens the agent menu for the selection.
	m.overlay = newTabMenu(*m, 0, 0)
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	if mu, ok := m.overlay.(*menu); !ok || !strings.HasPrefix(mu.title, "Start an agent") {
		t.Fatalf("agent item opened %T", m.overlay)
	}

	// Terminal starts a shell where the selection is.
	m.overlay = newTabMenu(*m, 0, 0)
	a2Run(a1Key(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")}))
	create := peer.waitFor(t, "pane.create", func(msg proto.Message) bool { return msg.Method == proto.MethodPaneCreate })
	var p proto.PaneCreateParams
	json.Unmarshal(create.Params, &p)
	if len(p.Command) != 0 || !p.NoProject {
		t.Fatalf("terminal params %+v", p)
	}

	// Offline, Agent… says so.
	m.machines[0].c = nil
	m.overlay = newTabMenu(*m, 0, 0)
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	if m.overlay != nil || !strings.Contains(m.flash, "local is") {
		t.Fatalf("offline agent: %T %q", m.overlay, m.flash)
	}
}

func TestSSHRowMenus(t *testing.T) {
	m, _ := sshFixture(t, false)
	rm := newMachine("dev", "dev", "dev@box")
	rm.state = stateOnline
	rm.panes = []proto.PaneInfo{{ID: "q1", Name: "zsh", State: proto.PaneRunning}}
	m.machines = append(m.machines, rm)
	m.rebuild()
	has := func(r row) bool {
		for _, it := range newRowMenu(*m, r, 0, 0).items {
			if it.key == "H" {
				return true
			}
		}
		return false
	}
	for _, c := range []struct {
		r    row
		want bool
	}{
		{row{kind: kindCLI, machine: localMachine, id: cliID(localMachine)}, true},
		{row{kind: kindTerminals, machine: localMachine, id: looseTerminalsID(localMachine)}, true},
		{row{kind: kindSSH, machine: localMachine, id: looseSSHID(localMachine)}, true},
		{row{kind: kindMachine, machine: localMachine, id: machineID(localMachine)}, true},
		{row{kind: kindTerminals, machine: localMachine, projectID: "r1"}, false},
		{row{kind: kindCLI, machine: "dev", id: cliID("dev")}, false},
		{row{kind: kindMachine, machine: "dev", id: machineID("dev")}, false},
	} {
		if got := has(c.r); got != c.want {
			t.Errorf("row %+v: SSH item %v, want %v", c.r, got, c.want)
		}
	}
	// The item sits right after New terminal on CLI rows.
	items := newRowMenu(*m, row{kind: kindCLI, machine: localMachine}, 0, 0).items
	if items[2].key != "n" || items[3].key != "H" || items[4].key != "M" {
		t.Fatalf("CLI menu order %+v", items)
	}
}

func TestSSHBroadcast(t *testing.T) {
	m, _ := sshFixture(t, true)
	names := func(ts []broadcastTarget) string {
		var out []string
		for _, bt := range ts {
			out = append(out, bt.pane.ID)
		}
		return strings.Join(out, ",")
	}

	a1At(t, m, looseSSHID(localMachine))
	label, targets := m.broadcastScope(false)
	if label != "CLI · SSH" || names(targets) != "p5" || !targets[0].on || !targets[0].ssh {
		t.Fatalf("SSH: %q %s %+v", label, names(targets), targets)
	}
	a1At(t, m, looseTerminalsID(localMachine))
	if label, targets := m.broadcastScope(false); label != "CLI · Terminals" || names(targets) != "p3" {
		t.Fatalf("CLI terminals: %q %s", label, names(targets))
	}
	a1At(t, m, sectionID(localMachine, "r1", "terminals"))
	if _, targets := m.broadcastScope(false); names(targets) != "p2,p6" {
		t.Fatalf("project terminals: %s", names(targets))
	}
	a1At(t, m, paneNodeID(localMachine, "p5"))
	if label, _ := m.broadcastScope(false); label != "CLI · SSH" {
		t.Fatalf("ssh pane label %q", label)
	}

	// The whole CLI lists agents, then terminals, then ssh sessions.
	a1At(t, m, cliID(localMachine))
	_, targets = m.broadcastScope(false)
	if names(targets) != "p4,p7,p3,p5" {
		t.Fatalf("CLI order %s", names(targets))
	}
	d := newBroadcastDialog(*m, "CLI", targets)
	var headings []string
	for _, r := range d.rows() {
		if r.target < 0 {
			headings = append(headings, r.heading)
		}
	}
	if got := strings.Join(headings, "|"); got != "CLI|Agents|Terminals|SSH" {
		t.Fatalf("headings %q", got)
	}
}

func TestSSHStatusHints(t *testing.T) {
	m, _ := sshFixture(t, true)
	rm := newMachine("dev", "dev", "dev@box")
	rm.state = stateOnline
	rm.panes = []proto.PaneInfo{{ID: "q1", Name: "zsh", State: proto.PaneRunning}}
	m.machines = append(m.machines, rm)
	m.rebuild()
	for id, want := range map[string]bool{
		cliID(localMachine):                        true,
		looseTerminalsID(localMachine):             true,
		looseSSHID(localMachine):                   true,
		sectionID(localMachine, "r1", "terminals"): false,
		cliID("dev"):                               false,
		looseTerminalsID("dev"):                    false,
	} {
		m.cursor = id
		got := a1HintText(m)
		if strings.Contains(got, "H ssh") != want || !strings.HasSuffix(got, "! waiting|? keys") {
			t.Errorf("%s: %s", id, got)
		}
	}
}

func TestSSHName(t *testing.T) {
	for in, want := range map[string]string{
		"box":                      "box",
		"dev@box":                  "dev@box",
		"ssh://dev@localhost:2299": "dev@localhost:2299",
		"ssh://box":                "box",
		"ssh://box/":               "box",
		"ssh://":                   "ssh://",
		"ssh:///":                  "ssh:///",
		"SSH://box":                "SSH://box",
		"myssh://box":              "myssh://box",
	} {
		if got := sshName(in); got != want {
			t.Errorf("sshName(%q) = %q, want %q", in, got, want)
		}
	}
}
