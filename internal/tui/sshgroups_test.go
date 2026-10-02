package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

func TestSSHGroupKindGoesLast(t *testing.T) {
	// Saved tabs store kinds by number: the new kind must not move others.
	if kindSandboxProvider != 16 || kindSSHGroup != 17 {
		t.Fatalf("kinds moved: provider %d, ssh group %d", kindSandboxProvider, kindSSHGroup)
	}
	if !(row{kind: kindSSHGroup}).expandable() || !pageRow(kindSSHGroup) {
		t.Fatal("a group folds and has a page")
	}
}

// groupsFixture is sshFixture (p5 an open session to box) with saved hosts
// in groups: prod holds box and db, eng holds nothing, and web has none.
func groupsFixture(t *testing.T, withClient bool) (*Model, *a1Peer) {
	t.Helper()
	sshEnv(t, "")
	m, peer := sshFixture(t, withClient)
	m.savedSSH = []string{"box", "db", "web"}
	m.sshInfo = map[string]sshHostInfo{
		"box": {Group: "prod"},
		"db":  {Group: "prod", Name: "prod db", Args: []string{"-p", "2200"}},
	}
	m.sshGroups = []string{"prod", "eng"}
	m.rebuild()
	return m, peer
}

func TestCleanSSHInfoAndGroups(t *testing.T) {
	if got := cleanSSHInfo(nil, nil); len(got) != 0 {
		t.Fatalf("nil: %v", got)
	}
	saved := []string{"a", "b", "c", "d", "e"}
	got := cleanSSHInfo(saved, map[string]sshHostInfo{
		"a":    {Group: " prod ", Name: " A ", Args: []string{"KexAlgorithms=+x"}},
		"b":    {Group: strings.Repeat("g", maxSSHGroupName+1), Args: []string{"other-host"}}, // all bad: nothing left
		"c":    {Name: "bad\nname", Group: "eng"},
		"d":    {},
		"gone": {Group: "prod"}, // forgotten by an older build
	})
	want := map[string]sshHostInfo{
		"a": {Name: "A", Group: "prod", Args: []string{"-o", "KexAlgorithms=+x"}},
		"c": {Group: "eng"},
	}
	if b1, b2 := jsonText(t, got), jsonText(t, want); b1 != b2 {
		t.Fatalf("clean:\n got %s\nwant %s", b1, b2)
	}
	groups := cleanSSHGroups([]string{"staging", " ", "staging", "x\x00", "eng "}, got, saved)
	if strings.Join(groups, ",") != "staging,eng,prod" {
		t.Fatalf("groups %q", groups)
	}
	if g := cleanSSHGroups(nil, nil, nil); g != nil {
		t.Fatalf("nil groups %q", g)
	}
}

func jsonText(t *testing.T, v any) string { return string(mustJSON(t, v)) }

func TestSSHGroupsInTree(t *testing.T) {
	m, _ := groupsFixture(t, false)
	want := []struct {
		id    string
		depth int
	}{
		{looseSSHID(localMachine), 2},
		{sshGroupID("prod"), 3},
		{paneNodeID(localMachine, "p5"), 4}, // the open session to box, in its group
		{savedSSHID("db"), 4},
		{sshGroupID("eng"), 3}, // empty, listed to drop hosts on
		{savedSSHID("web"), 3}, // no group
	}
	i := indexOfRow(m.rows, want[0].id)
	if i < 0 || i+len(want) != len(m.rows) {
		t.Fatalf("SSH section:\n%s", render(m.rows))
	}
	for j, w := range want {
		if r := m.rows[i+j]; r.id != w.id || r.depth != w.depth {
			t.Fatalf("row %d: %+v, want %+v\n%s", j, r, w, render(m.rows))
		}
	}
	// box is listed by its session alone, not also as saved.
	if indexOfRow(m.rows, savedSSHID("box")) >= 0 {
		t.Fatal("box listed twice")
	}
	if r := m.rows[i]; r.count != 3 { // the session, db and web
		t.Fatalf("SSH count %d", r.count)
	}
	if r := m.rows[i+1]; r.count != 2 || r.branch != "prod" || r.kind != kindSSHGroup {
		t.Fatalf("prod row %+v", r)
	}
	// An empty group shows no fold arrow: there is nothing to fold, and ▸
	// would say it was folded. One with hosts shows ▾ open, ▸ folded.
	if l := ansi.Strip(m.rowLine(m.rows[i+4], 40)); !strings.HasPrefix(l, "        eng") {
		t.Fatalf("empty group %q", l)
	}
	if l := ansi.Strip(m.rowLine(m.rows[i+1], 40)); !strings.HasPrefix(l, "      ▾ prod") {
		t.Fatalf("open group %q", l)
	}
	if label := ansi.Strip(m.rowLine(m.rows[indexOfRow(m.rows, savedSSHID("db"))], 40)); !strings.Contains(label, "prod db") {
		t.Fatalf("db is shown as %q", label)
	}

	// A folded group hides its hosts.
	m.expanded[sshGroupID("prod")] = false
	m.rebuild()
	if indexOfRow(m.rows, savedSSHID("db")) >= 0 || indexOfRow(m.rows, sshGroupID("prod")) < 0 {
		t.Fatalf("folded:\n%s", render(m.rows))
	}
	m.expanded[sshGroupID("prod")] = true

	// The filter matches a group's name (all of it shows), a host's name,
	// and leaves empty groups out.
	for _, c := range []struct {
		filter string
		want   []string
		not    []string
	}{
		{"prod", []string{sshGroupID("prod"), savedSSHID("db"), paneNodeID(localMachine, "p5")}, []string{sshGroupID("eng"), savedSSHID("web")}},
		{"prod db", []string{sshGroupID("prod"), savedSSHID("db")}, []string{sshGroupID("eng"), savedSSHID("web")}},
		{"web", []string{savedSSHID("web")}, []string{sshGroupID("prod"), sshGroupID("eng")}},
	} {
		m.filter = c.filter
		m.rebuild()
		for _, id := range c.want {
			if indexOfRow(m.rows, id) < 0 {
				t.Fatalf("filter %q lacks %s:\n%s", c.filter, id, render(m.rows))
			}
		}
		for _, id := range c.not {
			if indexOfRow(m.rows, id) >= 0 {
				t.Fatalf("filter %q has %s:\n%s", c.filter, id, render(m.rows))
			}
		}
	}
	m.filter = ""
	m.rebuild()

	// A group named in details for a host no longer saved holds nothing.
	m.savedSSH = []string{"web"}
	m.rebuild()
	if r := m.rows[indexOfRow(m.rows, sshGroupID("prod"))]; r.count != 0 {
		t.Fatalf("prod still counts %d:\n%s", r.count, render(m.rows))
	}

	// Remote machines have no SSH groups: saved hosts are this computer's.
	for _, r := range m.rows {
		if r.kind == kindSSHGroup && r.machine != localMachine {
			t.Fatalf("group on %s", r.machine)
		}
	}
}

func TestSSHGroupsOnlyGroupsDontMakeSection(t *testing.T) {
	sshEnv(t, "")
	m, _ := a1Fixture(t, false) // no ssh session at all
	m.sshGroups = []string{"eng"}
	m.rebuild()
	if indexOfRow(m.rows, sshGroupID("eng")) < 0 || indexOfRow(m.rows, looseSSHID(localMachine)) < 0 {
		t.Fatalf("an empty group should still be listed, to drop on:\n%s", render(m.rows))
	}
}

func TestSSHStateRoundTrip(t *testing.T) {
	m, _ := groupsFixture(t, false)
	m.statePath = filepath.Join(t.TempDir(), "ui.json")
	a2Run(m.saveState())
	st := loadUIState(m.statePath)
	if strings.Join(st.SavedSSH, ",") != "box,db,web" || strings.Join(st.SSHGroups, ",") != "prod,eng" {
		t.Fatalf("state %+v", st)
	}
	if jsonText(t, st.SSHHosts) != jsonText(t, m.sshInfo) {
		t.Fatalf("hosts %s", jsonText(t, st.SSHHosts))
	}
	// What New does with it.
	info := cleanSSHInfo(cleanSavedSSH(st.SavedSSH), st.SSHHosts)
	if jsonText(t, info) != jsonText(t, m.sshInfo) {
		t.Fatalf("reloaded %s", jsonText(t, info))
	}

	// A ui.json from before groups loads with no details.
	old := filepath.Join(t.TempDir(), "ui.json")
	os.WriteFile(old, []byte(`{"expanded":{},"saved_ssh":["box"]}`), 0o600)
	st = loadUIState(old)
	if strings.Join(st.SavedSSH, ",") != "box" || st.SSHHosts != nil || st.SSHGroups != nil {
		t.Fatalf("old state %+v", st)
	}

	// Nothing about ssh: nothing written for it.
	m.savedSSH, m.sshInfo, m.sshGroups = nil, nil, nil
	a2Run(m.saveState())
	b, _ := os.ReadFile(m.statePath)
	for _, key := range []string{"saved_ssh", "ssh_hosts", "ssh_groups"} {
		if strings.Contains(string(b), key) {
			t.Fatalf("%s written:\n%s", key, b)
		}
	}
}

func TestPutSSHHost(t *testing.T) {
	m, _ := groupsFixture(t, false)
	// Editing the host keeps its place and details.
	if err := m.putSSHHost("db", "me@db2", sshHostInfo{Name: "prod db", Group: "prod"}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(m.savedSSH, ",") != "box,me@db2,web" {
		t.Fatalf("saved %q", m.savedSSH)
	}
	if _, ok := m.sshInfo["db"]; ok || m.sshInfo["me@db2"].Name != "prod db" {
		t.Fatalf("info %v", m.sshInfo)
	}
	// Onto another saved host: refused.
	if err := m.putSSHHost("me@db2", "web", sshHostInfo{}); err == nil {
		t.Fatal("two hosts made one")
	}
	for _, bad := range []struct {
		target string
		info   sshHostInfo
	}{
		{"", sshHostInfo{}},
		{"-oProxyCommand=x", sshHostInfo{}},
		{"a b", sshHostInfo{}},
		{"ok", sshHostInfo{Args: []string{"-F", "x"}}},
		{"ok", sshHostInfo{Group: "a\nb"}},
	} {
		if err := m.putSSHHost("", bad.target, bad.info); err == nil {
			t.Fatalf("%+v accepted", bad)
		}
	}
	if slices.Contains(m.savedSSH, "ok") {
		t.Fatal("a refused host was saved")
	}
	// A new group named by a host is made.
	if err := m.putSSHHost("", "stage1", sshHostInfo{Group: "staging"}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(m.sshGroups, ",") != "prod,eng,staging" || m.savedSSH[len(m.savedSSH)-1] != "stage1" {
		t.Fatalf("groups %q saved %q", m.sshGroups, m.savedSSH)
	}
	// Emptying a host's details leaves no entry behind.
	if err := m.putSSHHost("stage1", "stage1", sshHostInfo{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.sshInfo["stage1"]; ok {
		t.Fatal("empty details kept")
	}
}

func TestEditSSHHostDialog(t *testing.T) {
	m, _ := groupsFixture(t, false)
	m.statePath = filepath.Join(t.TempDir(), "ui.json")
	a1At(t, m, savedSSHID("db"))
	m.focus = focusSidebar
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	d, ok := m.overlay.(*dialog)
	if !ok || len(d.fields) != 4 || d.fields[0].in.Value() != "db" || d.fields[1].in.Value() != "prod db" ||
		d.fields[2].in.Value() != "prod" || d.fields[3].in.Value() != "-p 2200" {
		t.Fatalf("editor %T %+v", m.overlay, m.overlay)
	}
	a2CheckBox(t, d.render(*m), *m)

	// A bad option keeps the dialog, with what was typed, and says why.
	d.fields[3].in.SetValue("-o 'KexAlgorithms=+diffie-hellman-group1-sha1' extra")
	a2Run(a1Key(t, m, tea.KeyMsg{Type: tea.KeyEnter}))
	if m.overlay != d || !m.flashIsErr || !strings.Contains(m.flash, "extra") {
		t.Fatalf("bad option: overlay %T flash %q", m.overlay, m.flash)
	}
	if m.sshInfo["db"].Args[1] != "2200" {
		t.Fatal("refused edit changed the host")
	}
	// An unclosed quote too.
	d.fields[3].in.SetValue("-o 'Kex")
	a2Run(a1Key(t, m, tea.KeyMsg{Type: tea.KeyEnter}))
	if m.overlay != d || !strings.Contains(m.flash, "quote") {
		t.Fatalf("quote: %q", m.flash)
	}

	d.fields[0].in.SetValue("admin@db.internal")
	d.fields[1].in.SetValue("")
	d.fields[2].in.SetValue("staging")
	d.fields[3].in.SetValue("-o 'KexAlgorithms=+diffie-hellman-group1-sha1' HostKeyAlgorithms=+ssh-rsa")
	a2Run(a1Key(t, m, tea.KeyMsg{Type: tea.KeyEnter}))
	if m.overlay != nil {
		t.Fatalf("still open: %q", m.flash)
	}
	info := m.sshInfo["admin@db.internal"]
	if info.Name != "" || info.Group != "staging" ||
		strings.Join(info.Args, "|") != "-o|KexAlgorithms=+diffie-hellman-group1-sha1|-o|HostKeyAlgorithms=+ssh-rsa" {
		t.Fatalf("saved %+v", info)
	}
	if indexOfRow(m.rows, savedSSHID("admin@db.internal")) < 0 || indexOfRow(m.rows, savedSSHID("db")) >= 0 {
		t.Fatalf("tree:\n%s", render(m.rows))
	}
	if st := loadUIState(m.statePath); st.SSHHosts["admin@db.internal"].Group != "staging" {
		t.Fatalf("ui.json %+v", st.SSHHosts)
	}

	// e on an ssh session edits its host, saving it.
	m, _ = groupsFixture(t, false)
	m.savedSSH, m.sshInfo = nil, nil
	m.rebuild()
	a1At(t, m, paneNodeID(localMachine, "p5"))
	m.focus = focusSidebar
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	d, ok = m.overlay.(*dialog)
	if !ok || d.title != " Save ssh host " || d.fields[0].in.Value() != "box" {
		t.Fatalf("session editor %T", m.overlay)
	}
	d.fields[2].in.SetValue("eng")
	a2Run(a1Key(t, m, tea.KeyMsg{Type: tea.KeyEnter}))
	if !slices.Contains(m.savedSSH, "box") || m.sshInfo["box"].Group != "eng" {
		t.Fatalf("session host not saved: %q %v", m.savedSSH, m.sshInfo)
	}
	if i := indexOfRow(m.rows, paneNodeID(localMachine, "p5")); i < 0 || m.rows[i].depth != 4 {
		t.Fatalf("session not in eng:\n%s", render(m.rows))
	}

	// e on a pane that isn't ssh does nothing of the sort.
	a1At(t, m, paneNodeID(localMachine, "p3"))
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	if m.overlay != nil {
		t.Fatalf("e on a shell opened %T", m.overlay)
	}
}

func TestAddHostToGroup(t *testing.T) {
	m, _ := groupsFixture(t, false)
	a1At(t, m, sshGroupID("eng"))
	m.focus = focusSidebar
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	d, ok := m.overlay.(*dialog)
	if !ok || d.title != " Add ssh host " || d.fields[2].in.Value() != "eng" {
		t.Fatalf("add %T", m.overlay)
	}
	a2Run(a1Key(t, m, tea.KeyMsg{Type: tea.KeyEnter})) // no host
	if m.overlay != d || !m.flashIsErr {
		t.Fatalf("empty host accepted: %q", m.flash)
	}
	d.fields[0].in.SetValue("ci-runner")
	a2Run(a1Key(t, m, tea.KeyMsg{Type: tea.KeyEnter}))
	if m.sshInfo["ci-runner"].Group != "eng" || indexOfRow(m.rows, savedSSHID("ci-runner")) < 0 {
		t.Fatalf("added %v\n%s", m.sshInfo, render(m.rows))
	}
	// Adding a host already saved is refused, not merged.
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	d = m.overlay.(*dialog)
	d.fields[0].in.SetValue("web")
	a2Run(a1Key(t, m, tea.KeyMsg{Type: tea.KeyEnter}))
	if m.overlay != d || !strings.Contains(m.flash, "already saved") {
		t.Fatalf("duplicate: %q", m.flash)
	}
}

func TestSSHGroupOperations(t *testing.T) {
	m, _ := groupsFixture(t, false)
	m.statePath = filepath.Join(t.TempDir(), "ui.json")

	// N on the SSH section makes a group.
	a1At(t, m, looseSSHID(localMachine))
	m.focus = focusSidebar
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("N")})
	d, ok := m.overlay.(*dialog)
	if !ok || d.title != " New ssh group " {
		t.Fatalf("N opened %T", m.overlay)
	}
	d.fields[0].in.SetValue("  staging ")
	a2Run(a1Key(t, m, tea.KeyMsg{Type: tea.KeyEnter}))
	if strings.Join(m.sshGroups, ",") != "prod,eng,staging" || m.cursor != sshGroupID("staging") {
		t.Fatalf("groups %q cursor %q", m.sshGroups, m.cursor)
	}
	for _, bad := range []string{"", "  ", "eng", strings.Repeat("x", maxSSHGroupName+1), "a\tb"} {
		if a2ErrText(a2Run(m.addSSHGroup(bad))) == "" {
			t.Fatalf("group %q made", bad)
		}
	}

	// N on a pane that isn't ssh, or anywhere else, makes nothing.
	a1At(t, m, paneNodeID(localMachine, "p3"))
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("N")})
	if m.overlay != nil {
		t.Fatalf("N on a shell opened %T", m.overlay)
	}

	// r renames a group and its hosts go with it; the fold goes too.
	m.expanded[sshGroupID("prod")] = false
	a1At(t, m, sshGroupID("prod"))
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	d = m.overlay.(*dialog)
	if d.title != " Rename ssh group " || d.fields[0].in.Value() != "prod" {
		t.Fatalf("rename %q", d.title)
	}
	d.fields[0].in.SetValue("production")
	a2Run(a1Key(t, m, tea.KeyMsg{Type: tea.KeyEnter}))
	if strings.Join(m.sshGroups, ",") != "production,eng,staging" || m.sshInfo["db"].Group != "production" ||
		m.sshInfo["box"].Group != "production" || m.expanded[sshGroupID("production")] || m.cursor != sshGroupID("production") {
		t.Fatalf("renamed: %q %v %v %q", m.sshGroups, m.sshInfo, m.expanded, m.cursor)
	}
	if _, ok := m.expanded[sshGroupID("prod")]; ok {
		t.Fatal("old fold kept")
	}
	if a2Run(m.renameSSHGroup("eng", "eng")); strings.Join(m.sshGroups, ",") != "production,eng,staging" {
		t.Fatal("renaming to itself changed something")
	}
	if a2ErrText(a2Run(m.renameSSHGroup("eng", ""))) == "" {
		t.Fatal("empty name accepted")
	}
	// Onto another group's name: merged.
	a2Run(m.renameSSHGroup("production", "eng"))
	if strings.Join(m.sshGroups, ",") != "eng,staging" || m.sshInfo["db"].Group != "eng" {
		t.Fatalf("merged: %q %v", m.sshGroups, m.sshInfo)
	}

	// x asks, then removes the group; its hosts stay, in no group.
	a1At(t, m, sshGroupID("eng"))
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	d, ok = m.overlay.(*dialog)
	if !ok || !d.confirm || !strings.Contains(strings.Join(d.text, " "), "2 saved host(s) are kept") {
		t.Fatalf("x opened %T", m.overlay)
	}
	a2Run(a1Key(t, m, tea.KeyMsg{Type: tea.KeyEnter}))
	if strings.Join(m.sshGroups, ",") != "staging" || strings.Join(m.savedSSH, ",") != "box,db,web" || m.sshInfo["db"].Group != "" ||
		m.sshInfo["db"].Name != "prod db" {
		t.Fatalf("removed: %q %q %v", m.sshGroups, m.savedSSH, m.sshInfo)
	}
	if _, ok := m.sshInfo["box"]; ok {
		t.Fatal("box kept empty details")
	}
	if indexOfRow(m.rows, sshGroupID("eng")) >= 0 {
		t.Fatalf("tree:\n%s", render(m.rows))
	}
	st := loadUIState(m.statePath)
	if strings.Join(st.SSHGroups, ",") != "staging" {
		t.Fatalf("ui.json groups %q", st.SSHGroups)
	}
	// An empty group says so.
	m.confirmRemoveSSHGroup("staging")
	if d := m.overlay.(*dialog); !strings.Contains(strings.Join(d.text, " "), "It is empty") {
		t.Fatalf("empty: %q", d.text)
	}
}

func TestForgetSSHDropsDetails(t *testing.T) {
	m, _ := groupsFixture(t, false)
	a2Run(m.forgetSSH("db"))
	if _, ok := m.sshInfo["db"]; ok || slices.Contains(m.savedSSH, "db") {
		t.Fatalf("db kept: %q %v", m.savedSSH, m.sshInfo)
	}
	if !slices.Contains(m.sshGroups, "prod") {
		t.Fatal("forgetting a host removed its group")
	}
}

func TestMoveSSHMenu(t *testing.T) {
	m, _ := groupsFixture(t, false)
	mu := newMoveSSHMenu(*m, "db", 0, 0)
	var labels []string
	for _, it := range mu.items {
		labels = append(labels, it.key+" "+it.label)
	}
	if got := strings.Join(labels, "|"); got != "1 prod  (now)|2 eng|0 No group|N New group…" {
		t.Fatalf("menu %q", got)
	}
	a2CheckBox(t, mu.render(*m), *m)
	m.overlay = mu
	a2Run(a1Key(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2")}))
	if m.sshInfo["db"].Group != "eng" {
		t.Fatalf("db in %q", m.sshInfo["db"].Group)
	}
	// A host in no group isn't offered "No group".
	mu = newMoveSSHMenu(*m, "web", 0, 0)
	for _, it := range mu.items {
		if it.key == "0" {
			t.Fatal("no group offered to a host in none")
		}
	}
	// New group… makes it with the host in it.
	m.overlay = mu
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("N")})
	d := m.overlay.(*dialog)
	d.fields[0].in.SetValue("qa")
	a2Run(a1Key(t, m, tea.KeyMsg{Type: tea.KeyEnter}))
	if m.sshInfo["web"].Group != "qa" || !slices.Contains(m.sshGroups, "qa") {
		t.Fatalf("web %v groups %q", m.sshInfo["web"], m.sshGroups)
	}
	// Moving to where it already is changes nothing.
	if cmd := m.moveSSHToGroup("web", "qa"); cmd != nil {
		t.Fatal("no-op move did something")
	}
	// The menus of a saved host and an ssh session offer it.
	for _, id := range []string{savedSSHID("web"), paneNodeID(localMachine, "p5")} {
		mu := newRowMenu(*m, m.rows[indexOfRow(m.rows, id)], 0, 0)
		if !slices.ContainsFunc(mu.items, func(it menuItem) bool { return strings.Contains(it.label, "group") }) ||
			!slices.ContainsFunc(mu.items, func(it menuItem) bool { return it.key == "K" }) {
			t.Fatalf("%s menu lacks group/key items", id)
		}
	}
	// A shell's menu doesn't.
	mu = newRowMenu(*m, m.rows[indexOfRow(m.rows, paneNodeID(localMachine, "p3"))], 0, 0)
	if slices.ContainsFunc(mu.items, func(it menuItem) bool { return it.key == "K" }) {
		t.Fatal("shell offers to copy a key")
	}
}

func TestDragSSHHostOntoGroup(t *testing.T) {
	m, peer := groupsFixture(t, true)
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

	// web (no group) onto eng.
	a2Run(a1Mouse(t, m, 10, a1RowY(t, m, savedSSHID("web")), a1Left, a1Press))
	if m.sshDrag == nil || m.sshDrag.target != "web" {
		t.Fatalf("drag %+v", m.sshDrag)
	}
	engY := a1RowY(t, m, sshGroupID("eng"))
	a2Run(a1Mouse(t, m, 10, engY, a1Left, a1Motion))
	if m.sshDropRow() != sshGroupID("eng") {
		t.Fatalf("drop row %q", m.sshDropRow())
	}
	// The drop is marked, and every line still fits.
	if v := ansi.Strip(m.View()); !strings.Contains(v, "drop here") {
		t.Fatalf("drop not marked:\n%s", v)
	}
	// One space between the expander and the name, as on every other row
	// (a group has no glyph).
	if l := ansi.Strip(m.rowLine(m.rows[indexOfRow(m.rows, sshGroupID("eng"))], 30)); !strings.HasPrefix(l, "        eng") {
		t.Fatalf("drop row %q", l)
	}
	for _, w := range []int{1, 8, 30} {
		if l := m.rowLine(m.rows[indexOfRow(m.rows, sshGroupID("eng"))], w); ansi.StringWidth(l) > w {
			t.Fatalf("width %d: %q", w, ansi.Strip(l))
		}
	}
	// Hover doesn't steal the motion while dragging.
	m.cfg.UI.Hover = true
	a2Run(a1Mouse(t, m, 10, engY, tea.MouseButtonNone, a1Motion))
	if m.sshDrag == nil {
		t.Fatal("hover ended the drag")
	}
	a2Run(a1Mouse(t, m, 10, engY, a1Left, a1Release))
	if m.sshDrag != nil || m.sshInfo["web"].Group != "eng" {
		t.Fatalf("not moved: %v", m.sshInfo)
	}
	if i := indexOfRow(m.rows, savedSSHID("web")); i < 0 || m.rows[i].depth != 4 || parentID(m.rows, i) != sshGroupID("eng") {
		t.Fatalf("tree:\n%s", render(m.rows))
	}
	if st := loadUIState(m.statePath); st.SSHHosts["web"].Group != "eng" {
		t.Fatalf("ui.json %v", st.SSHHosts)
	}
	if creates() != 0 {
		t.Fatal("a drag connected")
	}

	// db onto a host in eng lands in eng too.
	a2Run(a1Mouse(t, m, 10, a1RowY(t, m, savedSSHID("db")), a1Left, a1Press))
	webY := a1RowY(t, m, savedSSHID("web"))
	a2Run(a1Mouse(t, m, 10, webY, a1Left, a1Motion))
	a2Run(a1Mouse(t, m, 10, webY, a1Left, a1Release))
	if m.sshInfo["db"].Group != "eng" {
		t.Fatalf("db in %q", m.sshInfo["db"].Group)
	}

	// The open session to box onto the SSH section: out of its group.
	sshY := a1RowY(t, m, looseSSHID(localMachine))
	a2Run(a1Mouse(t, m, 10, a1RowY(t, m, paneNodeID(localMachine, "p5")), a1Left, a1Press))
	a2Run(a1Mouse(t, m, 10, sshY, a1Left, a1Motion))
	a2Run(a1Mouse(t, m, 10, sshY, a1Left, a1Release))
	if g := m.sshInfo["box"].Group; g != "" || !slices.Contains(m.savedSSH, "box") {
		t.Fatalf("box in %q", g)
	}

	// Let go somewhere that isn't ssh — a project, the pane area — and
	// nothing moves.
	before := jsonText(t, m.sshInfo)
	for _, at := range [][2]int{{10, a1RowY(t, m, projectNodeID(localMachine, "r1"))}, {m.sidebarW + 10, 10}} {
		a2Run(a1Mouse(t, m, 10, a1RowY(t, m, savedSSHID("db")), a1Left, a1Press))
		a2Run(a1Mouse(t, m, at[0], at[1], a1Left, a1Motion))
		if m.sshDropRow() != "" {
			t.Fatalf("%v is a drop target", at)
		}
		a2Run(a1Mouse(t, m, at[0], at[1], a1Left, a1Release))
		if m.sshDrag != nil || jsonText(t, m.sshInfo) != before {
			t.Fatalf("let go at %v moved something: %s", at, jsonText(t, m.sshInfo))
		}
	}
	// Away and back to the row it started on: not a click, nothing happens.
	dbY := a1RowY(t, m, savedSSHID("db"))
	a2Run(a1Mouse(t, m, 10, dbY, a1Left, a1Press))
	a2Run(a1Mouse(t, m, 10, sshY, a1Left, a1Motion))
	a2Run(a1Mouse(t, m, 10, dbY, a1Left, a1Release))
	if m.sshDrag != nil || jsonText(t, m.sshInfo) != before || creates() != 0 || m.overlay != nil {
		t.Fatalf("returning to the start did something: %d creates", creates())
	}
}

func TestDragUnsavedSessionSavesIt(t *testing.T) {
	m, _ := groupsFixture(t, false)
	m.savedSSH, m.sshInfo = []string{"web"}, nil
	m.rebuild()
	engY := a1RowY(t, m, sshGroupID("eng"))
	a2Run(a1Mouse(t, m, 10, a1RowY(t, m, paneNodeID(localMachine, "p5")), a1Left, a1Press))
	a2Run(a1Mouse(t, m, 10, engY, a1Left, a1Motion))
	a2Run(a1Mouse(t, m, 10, engY, a1Left, a1Release))
	if strings.Join(m.savedSSH, ",") != "web,box" || m.sshInfo["box"].Group != "eng" {
		t.Fatalf("saved %q info %v", m.savedSSH, m.sshInfo)
	}
	// The session in a project (p6) isn't a host to drag: it is a pane of
	// that project, and ssh started it by hand.
	if t2 := m.sshTargetOfRow(m.rows[indexOfRow(m.rows, paneNodeID(localMachine, "p6"))]); t2 != "" {
		t.Fatalf("p6 draggable as %q", t2)
	}
}

func TestStartSSHUsesSavedOptionsAndName(t *testing.T) {
	m, peer := groupsFixture(t, true)
	m.machines[0].panes = m.machines[0].panes[:4]
	m.rebuild()
	a1At(t, m, savedSSHID("db"))
	m.focus = focusSidebar
	a2Run(a1Key(t, m, tea.KeyMsg{Type: tea.KeyEnter}))
	create := peer.waitFor(t, "pane.create", func(msg proto.Message) bool { return msg.Method == proto.MethodPaneCreate })
	var p proto.PaneCreateParams
	json.Unmarshal(create.Params, &p)
	n := len(p.Command)
	if p.Name != "prod db" || n < 4 || strings.Join(p.Command[n-4:], "|") != "-p|2200|--|db" {
		t.Fatalf("params %q %q", p.Name, p.Command)
	}
	// The session is still found for its host, so it sits in prod.
	pane := proto.PaneInfo{ID: "p9", Name: p.Name, Command: p.Command, State: proto.PaneRunning}
	if sshTarget(pane) != "db" {
		t.Fatalf("target %q", sshTarget(pane))
	}
}

func TestSSHDialogTakesOptions(t *testing.T) {
	sshEnv(t, "")
	m, peer := sshFixture(t, true)
	d := newSSHDialog(*m)
	m.overlay = d
	if len(d.fields) != 2 {
		t.Fatalf("fields %d", len(d.fields))
	}
	d.fields[0].in.SetValue("old-switch")
	d.fields[1].in.SetValue("-F /etc/x")
	a2Run(a1Key(t, m, tea.KeyMsg{Type: tea.KeyEnter}))
	if m.overlay != d || !m.flashIsErr {
		t.Fatalf("bad option: %T %q", m.overlay, m.flash)
	}
	d.fields[1].in.SetValue("KexAlgorithms=+diffie-hellman-group1-sha1")
	a2Run(a1Key(t, m, tea.KeyMsg{Type: tea.KeyEnter}))
	mu, ok := m.overlay.(*menu) // save it?
	if !ok {
		t.Fatalf("no question: %T", m.overlay)
	}
	m.statePath = filepath.Join(t.TempDir(), "ui.json")
	a2Run(mu.run(m, 1)) // save and connect
	create := peer.waitFor(t, "pane.create", func(msg proto.Message) bool { return msg.Method == proto.MethodPaneCreate })
	var p proto.PaneCreateParams
	json.Unmarshal(create.Params, &p)
	if !strings.Contains(strings.Join(p.Command, " "), "-o KexAlgorithms=+diffie-hellman-group1-sha1 -- old-switch") {
		t.Fatalf("command %q", p.Command)
	}
	if got := m.sshInfo["old-switch"].Args; strings.Join(got, " ") != "-o KexAlgorithms=+diffie-hellman-group1-sha1" {
		t.Fatalf("saved options %q", got)
	}
}

func TestCopySSHKey(t *testing.T) {
	m, peer := groupsFixture(t, true)
	a1At(t, m, savedSSHID("db"))
	m.focus = focusSidebar
	msgs := a2Run(a1Key(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("K")}))
	create := peer.waitFor(t, "pane.create", func(msg proto.Message) bool { return msg.Method == proto.MethodPaneCreate })
	var p proto.PaneCreateParams
	json.Unmarshal(create.Params, &p)
	n := len(p.Command)
	if p.Command[0] != "/bin/sh" || p.Name != "copy key to db" || !p.NoProject || strings.Join(p.Command[n-4:], "|") != "-p|2200|--|db" {
		t.Fatalf("params %q %q", p.Name, p.Command)
	}
	// Not an ssh session: it isn't listed as one, so it can't be mistaken
	// for a login to db.
	if sshPane(proto.PaneInfo{Command: p.Command}) {
		t.Fatal("key copy taken for a session")
	}
	var note string
	for _, msg := range msgs {
		if c, ok := msg.(createdMsg); ok {
			note = c.note
		}
	}
	if !strings.Contains(note, "id_ed25519.pub") || !strings.Contains(note, "db") {
		t.Fatalf("note %q", note)
	}

	// Offline, it says so.
	m.machines[0].c = nil
	if got := a2ErrText(a2Run(m.copySSHKey("db"))); got == "" {
		t.Fatal("no error offline")
	}
	// K on something else does nothing.
	m.overlay = nil
	a1At(t, m, sshGroupID("prod"))
	if cmd := a1Key(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("K")}); cmd != nil {
		t.Fatal("K on a group did something")
	}
}

func TestSSHGroupPageAndHints(t *testing.T) {
	m, _ := groupsFixture(t, false)
	id := sshGroupID("prod")
	a1At(t, m, id)
	m.focus = focusSidebar
	m.syncView()
	if got := m.leafTitle(m.tab().focused()); got != " ssh group · prod " {
		t.Fatalf("title %q", got)
	}
	out := a2Plain(m.sshGroupLines("prod", 100))
	for _, want := range []string{"ssh group prod", "prod db", "-p 2200", "not connected", "ssh box", "connected", "drag hosts"} {
		if !strings.Contains(out, want) {
			t.Fatalf("page lacks %q:\n%s", want, out)
		}
	}
	if out := a2Plain(m.sshGroupLines("eng", 100)); !strings.Contains(out, "No hosts yet") {
		t.Fatalf("empty page:\n%s", out)
	}
	for _, w := range []int{1, 5, 20, 39, 100} {
		for _, g := range []string{"prod", "eng"} {
			for _, l := range m.sshGroupLines(g, w) {
				if ansi.StringWidth(l) > w {
					t.Fatalf("width %d: %q", w, ansi.Strip(l))
				}
			}
		}
		for _, l := range savedSSHLines("db", m.sshInfo["db"], w) {
			if ansi.StringWidth(l) > w {
				t.Fatalf("saved width %d: %q", w, ansi.Strip(l))
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
	m.width, m.height = 160, 40

	hints := func() string {
		_, items := m.statusHints()
		var out []string
		for _, it := range items {
			out = append(out, ansi.Strip(it.text))
		}
		return strings.Join(out, "|")
	}
	if got := hints(); !strings.HasPrefix(got, "a add host|r rename|x remove") {
		t.Fatalf("group hints %q", got)
	}
	a1At(t, m, looseSSHID(localMachine))
	if got := hints(); !strings.HasPrefix(got, "H ssh|N new group") {
		t.Fatalf("SSH hints %q", got)
	}
	mu := newRowMenu(*m, m.rows[indexOfRow(m.rows, looseSSHID(localMachine))], 0, 0)
	if !slices.ContainsFunc(mu.items, func(it menuItem) bool { return it.key == "N" }) {
		t.Fatal("SSH menu lacks New group")
	}
	mu = newRowMenu(*m, m.rows[indexOfRow(m.rows, id)], 0, 0)
	if mu.title != "ssh group prod" || len(mu.items) != 4 {
		t.Fatalf("group menu %q %d", mu.title, len(mu.items))
	}
	if s := m.rowScope(m.rows[indexOfRow(m.rows, id)]); s.level != scopeCLI || s.section != kindSSH {
		t.Fatalf("scope %+v", s)
	}
}

func TestSSHArgsText(t *testing.T) {
	for _, c := range []struct {
		in   []string
		want string
	}{
		{nil, ""},
		{[]string{"-p", "22"}, "-p 22"},
		{[]string{"-o", "ProxyCommand ssh -W %h:%p jump"}, "-o 'ProxyCommand ssh -W %h:%p jump'"},
		{[]string{"-i", "it's"}, `-i 'it'\''s'`},
		{[]string{"-o", ""}, "-o ''"},
	} {
		if got := sshArgsText(c.in); got != c.want {
			t.Fatalf("%q: %q, want %q", c.in, got, c.want)
		}
		// What is shown reads back the same.
		back, err := parseSSHArgs(sshArgsText(c.in))
		if c.in != nil && c.in[1] != "" && (err != nil || strings.Join(back, "|") != strings.Join(c.in, "|")) {
			t.Fatalf("%q read back as %q %v", c.in, back, err)
		}
	}
}
