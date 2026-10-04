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

func jsonText(t *testing.T, v any) string { return string(mustJSON(t, v)) }

func sshFolderRow(name string) string { return folderRowID(localMachine, "", kindSSH, name) }

// foldersFixture is sshFixture (p5 an open session to box) with saved hosts
// in the SSH section's folders: prod holds box and db, eng holds nothing,
// and web is in none.
func foldersFixture(t *testing.T, withClient bool) (*Model, *a1Peer) {
	t.Helper()
	sshEnv(t, "")
	m, peer := sshFixture(t, withClient)
	m.savedSSH = []string{"box", "db", "web"}
	m.sshInfo = map[string]sshHostInfo{"db": {Name: "prod db", Args: []string{"-p", "2200"}}}
	m.folders = map[string][]savedFolder{sshFolderKey(): {
		{Name: "prod", Members: []savedMember{{Host: "box"}, {Host: "db"}}},
		{Name: "eng"},
	}}
	// Folders are folded until opened; these tests look at what is in them.
	m.expanded[sshFolderRow("prod")], m.expanded[sshFolderRow("eng")] = true, true
	m.rebuild()
	return m, peer
}

// hostsIn are the saved hosts an SSH folder holds, in order.
func hostsIn(m *Model, folder string) []string {
	var out []string
	for _, f := range m.folders[sshFolderKey()] {
		if f.Name == folder {
			for _, mem := range f.Members {
				if mem.Host != "" {
					out = append(out, mem.Host)
				}
			}
		}
	}
	return out
}

func TestHostMembersLeavePaneClaimsAlone(t *testing.T) {
	// A host member is no pane: claiming panes passes over it, so a folder
	// mixing both still finds its panes by name and id as before.
	f := savedFolder{Name: "f", Members: []savedMember{{Host: "box"}, {Name: "zsh", ID: "p2"}, {Host: "db"}}}
	got := f.claims([]proto.PaneInfo{{ID: "p2", Name: "zsh"}, {ID: "p9", Name: "box"}}, map[string]bool{})
	if len(got) != 1 || got[0].member != 1 || got[0].pane.ID != "p2" {
		t.Fatalf("claims %+v", got)
	}
	// Written with the host, read back with it; a folder from before hosts
	// reads as it did.
	b := jsonText(t, f)
	if !strings.Contains(b, `{"host":"box"}`) {
		t.Fatalf("json %s", b)
	}
	var old savedFolder
	if err := json.Unmarshal([]byte(`{"name":"f","members":[{"name":"zsh","id":"p2"}]}`), &old); err != nil || old.Members[0].Host != "" {
		t.Fatalf("old folder %+v %v", old, err)
	}
}

func TestCleanSSHInfo(t *testing.T) {
	if got := cleanSSHInfo(nil, nil); len(got) != 0 {
		t.Fatalf("nil: %v", got)
	}
	saved := []string{"a", "b", "c", "d"}
	got := cleanSSHInfo(saved, map[string]sshHostInfo{
		"a":    {Name: " A ", Args: []string{"KexAlgorithms=+x"}},
		"b":    {Args: []string{"other-host"}}, // refused: nothing left
		"c":    {Name: "bad\nname", Group: strings.Repeat("g", maxFolderName+1)},
		"d":    {Group: " eng "},
		"gone": {Name: "x"}, // forgotten by an older build
	})
	want := map[string]sshHostInfo{
		"a": {Name: "A", Args: []string{"-o", "KexAlgorithms=+x"}},
		"d": {Group: "eng"},
	}
	if jsonText(t, got) != jsonText(t, want) {
		t.Fatalf("clean:\n got %s\nwant %s", jsonText(t, got), jsonText(t, want))
	}
}

func TestMigrateSSHGroups(t *testing.T) {
	// Nothing to move: the folders are left as they are, nil included.
	if got := migrateSSHGroups(nil, nil, []string{"a"}, map[string]sshHostInfo{"a": {Name: "A"}}); got != nil {
		t.Fatalf("nothing to move: %v", got)
	}
	saved := []string{"a", "b", "c", "d"}
	info := map[string]sshHostInfo{
		"a": {Group: "prod"},
		"b": {Group: "staging", Name: "B"},
		"c": {Group: "prod"},
		"d": {Group: "eng"}, // already in a folder: it stays there
	}
	folders := map[string][]savedFolder{
		sshFolderKey():                          {{Name: "prod", Members: []savedMember{{Name: "vm1", ID: "p1"}}}, {Name: "mine", Members: []savedMember{{Host: "d"}}}},
		folderKey(localMachine, "", kindAgents): {{Name: "agents"}},
	}
	got := migrateSSHGroups(folders, []string{"staging", "prod", " ", "x\x00", "eng"}, saved, info)
	fs := got[sshFolderKey()]
	var names []string
	for _, f := range fs {
		names = append(names, f.Name)
	}
	if strings.Join(names, ",") != "prod,mine,staging,eng" {
		t.Fatalf("folders %q", names)
	}
	if jsonText(t, fs[0].Members) != `[{"name":"vm1","id":"p1"},{"host":"a"},{"host":"c"}]` ||
		jsonText(t, fs[2].Members) != `[{"host":"b"}]` || len(fs[3].Members) != 0 {
		t.Fatalf("members %s", jsonText(t, fs))
	}
	if len(got[folderKey(localMachine, "", kindAgents)]) != 1 {
		t.Fatal("another section's folders touched")
	}
	// The groups are gone from the details; what else they held stays.
	if jsonText(t, info) != `{"b":{"name":"B"}}` {
		t.Fatalf("info %s", jsonText(t, info))
	}
	// Run again (the next start): nothing more happens.
	again := migrateSSHGroups(got, nil, saved, info)
	if jsonText(t, again) != jsonText(t, got) {
		t.Fatal("a second run changed the folders")
	}
}

func TestMigrateFromUIJSON(t *testing.T) {
	// A ui.json written by the build with ssh groups, read the way New reads it.
	path := filepath.Join(t.TempDir(), "ui.json")
	os.WriteFile(path, []byte(`{"expanded":{},"saved_ssh":["box","db"],
		"ssh_hosts":{"db":{"name":"prod db","group":"prod","args":["-p","2200"]}},"ssh_groups":["prod","eng"]}`), 0o600)
	st := loadUIState(path)
	saved := cleanSavedSSH(st.SavedSSH)
	info := cleanSSHInfo(saved, st.SSHHosts)
	folders := migrateSSHGroups(st.Folders, st.SSHGroups, saved, info)
	if jsonText(t, folders[sshFolderKey()]) != `[{"name":"prod","members":[{"host":"db"}]},{"name":"eng"}]` {
		t.Fatalf("folders %s", jsonText(t, folders))
	}
	if info["db"].Name != "prod db" || info["db"].Group != "" || len(info["db"].Args) != 2 {
		t.Fatalf("info %+v", info)
	}
	// Saved again, the groups aren't written.
	m := &Model{savedSSH: saved, sshInfo: info, folders: folders, statePath: path}
	a2Run(m.saveState())
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), "ssh_groups") || strings.Contains(string(b), `"group"`) {
		t.Fatalf("groups written again:\n%s", b)
	}
}

func TestSSHFoldersInTree(t *testing.T) {
	m, _ := foldersFixture(t, false)
	// Folded until opened, which is what a folder does with nothing said
	// about it: what sits under an open one is then its own, and nothing
	// one step out can be read as more of it.
	clear(m.expanded)
	m.rebuild()
	want := []struct {
		id    string
		depth int
	}{
		{looseSSHID(localMachine), 2},
		{sshFolderRow("prod"), 3},
		{sshFolderRow("eng"), 3}, // empty, listed to drop hosts on
		{savedSSHID("web"), 3},   // in no folder
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

	// Opened, it holds what it held: the session to box, and the saved db.
	m.expanded[sshFolderRow("prod")] = true
	m.rebuild()
	opened := []struct {
		id    string
		depth int
	}{
		{sshFolderRow("prod"), 3},
		{paneNodeID(localMachine, "p5"), 4}, // the open session to box, in its host's folder
		{savedSSHID("db"), 4},
		{sshFolderRow("eng"), 3},
		{savedSSHID("web"), 3},
	}
	at := indexOfRow(m.rows, sshFolderRow("prod"))
	for j, w := range opened {
		if r := m.rows[at+j]; r.id != w.id || r.depth != w.depth {
			t.Fatalf("opened row %d: %+v, want %+v\n%s", j, r, w, render(m.rows))
		}
	}
	if indexOfRow(m.rows, savedSSHID("box")) >= 0 {
		t.Fatal("box listed twice")
	}
	if r := m.rows[i]; r.count != 3 { // the session, db and web
		t.Fatalf("SSH count %d", r.count)
	}
	if r := m.rows[i+1]; r.count != 2 || r.label != "prod" || r.kind != kindFolder || r.section != kindSSH {
		t.Fatalf("prod row %+v", r)
	}
	if label := ansi.Strip(m.rowLine(m.rows[indexOfRow(m.rows, savedSSHID("db"))], 40)); !strings.Contains(label, "prod db") {
		t.Fatalf("db is shown as %q", label)
	}

	// Both open from here on, since what follows looks at what is in them.
	m.expanded[sshFolderRow("prod")], m.expanded[sshFolderRow("eng")] = true, true
	m.rebuild()

	// A pane member elsewhere naming box's session doesn't split it from
	// its host: the host decides.
	m.folders[sshFolderKey()][1].Members = []savedMember{{Name: "ssh box", ID: "p5"}}
	m.rebuild()
	if j := indexOfRow(m.rows, paneNodeID(localMachine, "p5")); parentID(m.rows, j) != sshFolderRow("prod") {
		t.Fatalf("session left its host:\n%s", render(m.rows))
	}
	// Once box is forgotten its host member holds nothing, and the pane
	// member puts the session in eng, as any pane.
	m.savedSSH = []string{"db", "web"}
	m.rebuild()
	if j := indexOfRow(m.rows, paneNodeID(localMachine, "p5")); parentID(m.rows, j) != sshFolderRow("eng") {
		t.Fatalf("pane member ignored:\n%s", render(m.rows))
	}
	m.savedSSH = []string{"box", "db", "web"}
	m.folders[sshFolderKey()][1].Members = nil

	// A folded folder hides its hosts.
	m.expanded[sshFolderRow("prod")] = false
	m.rebuild()
	if indexOfRow(m.rows, savedSSHID("db")) >= 0 || indexOfRow(m.rows, sshFolderRow("prod")) < 0 {
		t.Fatalf("folded:\n%s", render(m.rows))
	}
	m.expanded[sshFolderRow("prod")] = true

	// The filter finds a host by its name; folders left empty by it go.
	for _, c := range []struct {
		filter string
		want   []string
		not    []string
	}{
		{"prod db", []string{sshFolderRow("prod"), savedSSHID("db")}, []string{sshFolderRow("eng"), savedSSHID("web")}},
		{"web", []string{savedSSHID("web")}, []string{sshFolderRow("prod"), sshFolderRow("eng")}},
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

	// No SSH folders: the section reads as it did before folders.
	m.folders = nil
	m.rebuild()
	i = indexOfRow(m.rows, looseSSHID(localMachine))
	if got := render(m.rows[i:]); !strings.HasPrefix(got, "    m:local/ssh\n      pane:p5\n      sshsaved:db\n      sshsaved:web\n") {
		t.Fatalf("without folders:\n%s", got)
	}
}

func TestSSHStateRoundTrip(t *testing.T) {
	m, _ := foldersFixture(t, false)
	m.statePath = filepath.Join(t.TempDir(), "ui.json")
	a2Run(m.saveState())
	st := loadUIState(m.statePath)
	if strings.Join(st.SavedSSH, ",") != "box,db,web" || st.SSHGroups != nil {
		t.Fatalf("state %+v", st)
	}
	if jsonText(t, st.SSHHosts) != jsonText(t, m.sshInfo) || jsonText(t, st.Folders) != jsonText(t, m.folders) {
		t.Fatalf("hosts %s folders %s", jsonText(t, st.SSHHosts), jsonText(t, st.Folders))
	}
	// Nothing about ssh: nothing written for it.
	m.savedSSH, m.sshInfo, m.folders = nil, nil, nil
	a2Run(m.saveState())
	b, _ := os.ReadFile(m.statePath)
	for _, key := range []string{"saved_ssh", "ssh_hosts", "ssh_groups", "folders"} {
		if strings.Contains(string(b), key) {
			t.Fatalf("%s written:\n%s", key, b)
		}
	}
}

func TestPutSSHHost(t *testing.T) {
	m, _ := foldersFixture(t, false)
	// Editing the host keeps its place, its details and its folder.
	if err := m.putSSHHost("db", "me@db2", sshHostInfo{Name: "prod db"}, "prod"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(m.savedSSH, ",") != "box,me@db2,web" || strings.Join(hostsIn(m, "prod"), ",") != "box,me@db2" {
		t.Fatalf("saved %q prod %q", m.savedSSH, hostsIn(m, "prod"))
	}
	if _, ok := m.sshInfo["db"]; ok || m.sshInfo["me@db2"].Name != "prod db" {
		t.Fatalf("info %v", m.sshInfo)
	}
	// Onto another saved host: refused.
	if err := m.putSSHHost("me@db2", "web", sshHostInfo{}, ""); err == nil {
		t.Fatal("two hosts made one")
	}
	for _, bad := range []struct {
		target, folder string
		info           sshHostInfo
	}{
		{"", "", sshHostInfo{}},
		{"-oProxyCommand=x", "", sshHostInfo{}},
		{"a b", "", sshHostInfo{}},
		{"ok", "", sshHostInfo{Args: []string{"-F", "x"}}},
		{"ok", "a\nb", sshHostInfo{}},
		{"ok", strings.Repeat("x", maxFolderName+1), sshHostInfo{}},
	} {
		if err := m.putSSHHost("", bad.target, bad.info, bad.folder); err == nil {
			t.Fatalf("%+v accepted", bad)
		}
	}
	if slices.Contains(m.savedSSH, "ok") {
		t.Fatal("a refused host was saved")
	}
	// A folder named for a host that isn't there is made.
	if err := m.putSSHHost("", "stage1", sshHostInfo{}, "staging"); err != nil {
		t.Fatal(err)
	}
	if m.hostFolder("stage1") != "staging" || !m.expanded[sshFolderRow("staging")] {
		t.Fatalf("stage1 in %q", m.hostFolder("stage1"))
	}
	// Moved between folders: in one only. No folder: in none, the folder kept.
	m.putSSHHost("stage1", "stage1", sshHostInfo{}, "eng")
	if m.hostFolder("stage1") != "eng" || len(hostsIn(m, "staging")) != 0 {
		t.Fatalf("moved: eng %q staging %q", hostsIn(m, "eng"), hostsIn(m, "staging"))
	}
	m.putSSHHost("stage1", "stage1", sshHostInfo{}, "")
	if m.hostFolder("stage1") != "" || len(m.foldersIn(localMachine, "", kindSSH)) != 3 {
		t.Fatalf("out: %q", m.hostFolder("stage1"))
	}
	if _, ok := m.sshInfo["stage1"]; ok {
		t.Fatal("empty details kept")
	}
}

func TestEditSSHHostDialog(t *testing.T) {
	m, _ := foldersFixture(t, false)
	m.statePath = filepath.Join(t.TempDir(), "ui.json")
	a1At(t, m, savedSSHID("db"))
	m.focus = focusSidebar
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	d, ok := m.overlay.(*dialog)
	if !ok || len(d.fields) != 4 || d.fields[0].in.Value() != "db" || d.fields[1].in.Value() != "prod db" ||
		strings.TrimSpace(d.fields[2].label) != "Folder" || d.fields[2].in.Value() != "prod" || d.fields[3].in.Value() != "-p 2200" {
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
	if info.Name != "" || m.hostFolder("admin@db.internal") != "staging" ||
		strings.Join(info.Args, "|") != "-o|KexAlgorithms=+diffie-hellman-group1-sha1|-o|HostKeyAlgorithms=+ssh-rsa" {
		t.Fatalf("saved %+v in %q", info, m.hostFolder("admin@db.internal"))
	}
	if j := indexOfRow(m.rows, savedSSHID("admin@db.internal")); j < 0 || parentID(m.rows, j) != sshFolderRow("staging") ||
		indexOfRow(m.rows, savedSSHID("db")) >= 0 {
		t.Fatalf("tree:\n%s", render(m.rows))
	}
	if st := loadUIState(m.statePath); !strings.Contains(jsonText(t, st.Folders), `"host":"admin@db.internal"`) {
		t.Fatalf("ui.json %s", jsonText(t, st.Folders))
	}

	// e on an ssh session edits its host, saving it.
	m, _ = foldersFixture(t, false)
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
	if !slices.Contains(m.savedSSH, "box") || m.hostFolder("box") != "eng" {
		t.Fatalf("session host not saved: %q", m.savedSSH)
	}
	if j := indexOfRow(m.rows, paneNodeID(localMachine, "p5")); parentID(m.rows, j) != sshFolderRow("eng") {
		t.Fatalf("session not in eng:\n%s", render(m.rows))
	}

	// e on a pane that isn't ssh does nothing of the sort.
	a1At(t, m, paneNodeID(localMachine, "p3"))
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	if m.overlay != nil {
		t.Fatalf("e on a shell opened %T", m.overlay)
	}
}

func TestAddHostToFolder(t *testing.T) {
	m, _ := foldersFixture(t, false)
	a1At(t, m, sshFolderRow("eng"))
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
	if m.hostFolder("ci-runner") != "eng" || indexOfRow(m.rows, savedSSHID("ci-runner")) < 0 {
		t.Fatalf("added in %q\n%s", m.hostFolder("ci-runner"), render(m.rows))
	}
	// Adding a host already saved is refused, not merged.
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	d = m.overlay.(*dialog)
	d.fields[0].in.SetValue("web")
	a2Run(a1Key(t, m, tea.KeyMsg{Type: tea.KeyEnter}))
	if m.overlay != d || !strings.Contains(m.flash, "already saved") {
		t.Fatalf("duplicate: %q", m.flash)
	}

	// a on a folder of Terminals is still "add a project", not a host.
	m.overlay = nil
	m.newFolder(localMachine, "", kindTerminals, "scratch")
	m.rebuild()
	a1At(t, m, folderRowID(localMachine, "", kindTerminals, "scratch"))
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if d, ok := m.overlay.(*dialog); ok && strings.Contains(d.title, "ssh") {
		t.Fatal("a on a Terminals folder asked for an ssh host")
	}
}

func TestNewFolderFromSSHRows(t *testing.T) {
	m, _ := foldersFixture(t, false)
	m.focus = focusSidebar
	// N on a saved host makes a folder in SSH, like N on the section.
	for _, id := range []string{savedSSHID("web"), looseSSHID(localMachine)} {
		m.overlay = nil
		a1At(t, m, id)
		a1Key(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("N")})
		d, ok := m.overlay.(*dialog)
		if !ok || d.title != " New folder " {
			t.Fatalf("N on %s opened %T (flash %q)", id, m.overlay, m.flash)
		}
		d.fields[0].in.SetValue("from-" + id[:4])
		a2Run(a1Key(t, m, tea.KeyMsg{Type: tea.KeyEnter}))
	}
	if n := len(m.foldersIn(localMachine, "", kindSSH)); n != 4 {
		t.Fatalf("%d SSH folders", n)
	}
}

func TestForgetSSHLeavesItsFolder(t *testing.T) {
	m, _ := foldersFixture(t, false)
	a2Run(m.forgetSSH("db"))
	if _, ok := m.sshInfo["db"]; ok || slices.Contains(m.savedSSH, "db") || slices.Contains(hostsIn(m, "prod"), "db") {
		t.Fatalf("db kept: %q %v %q", m.savedSSH, m.sshInfo, hostsIn(m, "prod"))
	}
	if strings.Join(hostsIn(m, "prod"), ",") != "box" {
		t.Fatalf("prod %q", hostsIn(m, "prod"))
	}
}

func TestRemoveSSHFolderKeepsHosts(t *testing.T) {
	m, _ := foldersFixture(t, false)
	a1At(t, m, sshFolderRow("prod"))
	m.focus = focusSidebar
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if d, ok := m.overlay.(*dialog); !ok || !d.confirm {
		t.Fatalf("x opened %T", m.overlay)
	}
	a2Run(a1Key(t, m, tea.KeyMsg{Type: tea.KeyEnter}))
	if strings.Join(m.savedSSH, ",") != "box,db,web" || m.hostFolder("db") != "" {
		t.Fatalf("after remove: %q in %q", m.savedSSH, m.hostFolder("db"))
	}
	if j := indexOfRow(m.rows, savedSSHID("db")); j < 0 || m.rows[j].depth != 3 {
		t.Fatalf("db not back in SSH:\n%s", render(m.rows))
	}
}

func TestMoveSSHMenu(t *testing.T) {
	m, _ := foldersFixture(t, false)
	dbRow := m.rows[indexOfRow(m.rows, savedSSHID("db"))]
	mu := newMoveSSHMenu(*m, dbRow, 0, 0)
	var labels []string
	for _, it := range mu.items {
		labels = append(labels, it.key+" "+it.label)
	}
	if got := strings.Join(labels, "|"); got != "1 prod  (now)|2 eng|0 No folder|N New folder…" {
		t.Fatalf("menu %q", got)
	}
	a2CheckBox(t, mu.render(*m), *m)
	m.overlay = mu
	a2Run(a1Key(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2")}))
	if m.hostFolder("db") != "eng" {
		t.Fatalf("db in %q", m.hostFolder("db"))
	}
	// A host in no folder isn't offered "No folder".
	webRow := m.rows[indexOfRow(m.rows, savedSSHID("web"))]
	mu = newMoveSSHMenu(*m, webRow, 0, 0)
	if slices.ContainsFunc(mu.items, func(it menuItem) bool { return it.key == "0" }) {
		t.Fatal("no folder offered to a host in none")
	}
	// New folder… makes it with the host in it.
	m.overlay = mu
	a1Key(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("N")})
	d := m.overlay.(*dialog)
	d.fields[0].in.SetValue("qa")
	a2Run(a1Key(t, m, tea.KeyMsg{Type: tea.KeyEnter}))
	if m.hostFolder("web") != "qa" {
		t.Fatalf("web in %q", m.hostFolder("web"))
	}
	if cmd := m.moveSSHToFolder(m.rows[indexOfRow(m.rows, savedSSHID("web"))], "qa"); cmd != nil {
		t.Fatal("no-op move did something")
	}

	// A session to a host that isn't saved goes in as a pane, as any
	// pane does; it isn't saved by it.
	m.savedSSH = []string{"db", "web"}
	m.rebuild()
	p5 := m.rows[indexOfRow(m.rows, paneNodeID(localMachine, "p5"))]
	a2Run(newMoveSSHMenu(*m, p5, 0, 0).run(m, 0)) // into prod
	if slices.Contains(m.savedSSH, "box") || !strings.Contains(jsonText(t, m.folders[sshFolderKey()][0].Members), `{"name":"ssh box","id":"p5"}`) {
		t.Fatalf("session's host saved %q, or the pane not in prod: %s", m.savedSSH, jsonText(t, m.folders))
	}
	if j := indexOfRow(m.rows, paneNodeID(localMachine, "p5")); parentID(m.rows, j) != sshFolderRow("prod") {
		t.Fatalf("session not in prod:\n%s", render(m.rows))
	}
	p5 = m.rows[indexOfRow(m.rows, paneNodeID(localMachine, "p5"))]
	if mu := newMoveSSHMenu(*m, p5, 0, 0); !strings.Contains(mu.items[0].label, "(now)") {
		t.Fatalf("its folder not marked: %q", mu.items[0].label)
	}

	// The menus of a saved host and an ssh session offer moving and K.
	for _, id := range []string{savedSSHID("web"), paneNodeID(localMachine, "p5")} {
		mu := newRowMenu(*m, m.rows[indexOfRow(m.rows, id)], 0, 0)
		if !slices.ContainsFunc(mu.items, func(it menuItem) bool { return strings.Contains(it.label, "folder") }) ||
			!slices.ContainsFunc(mu.items, func(it menuItem) bool { return it.key == "K" }) {
			t.Fatalf("%s menu lacks folder/key items", id)
		}
	}
	mu = newRowMenu(*m, m.rows[indexOfRow(m.rows, paneNodeID(localMachine, "p3"))], 0, 0)
	if slices.ContainsFunc(mu.items, func(it menuItem) bool { return it.key == "K" }) {
		t.Fatal("shell offers to copy a key")
	}
}

func TestDragSavedHostIntoFolder(t *testing.T) {
	m, peer := foldersFixture(t, true)
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

	// web (in no folder) onto eng.
	a2Run(a1Mouse(t, m, 10, a1RowY(t, m, savedSSHID("web")), a1Left, a1Press))
	if m.rowDrag != savedSSHID("web") {
		t.Fatalf("drag %q", m.rowDrag)
	}
	engY := a1RowY(t, m, sshFolderRow("eng"))
	a2Run(a1Mouse(t, m, 10, engY, a1Left, a1Motion))
	if m.rowDrop != sshFolderRow("eng") {
		t.Fatalf("drop row %q", m.rowDrop)
	}
	a2Run(a1Mouse(t, m, 10, engY, a1Left, a1Release))
	if m.rowDrag != "" || m.hostFolder("web") != "eng" {
		t.Fatalf("not moved: %q", m.hostFolder("web"))
	}
	if j := indexOfRow(m.rows, savedSSHID("web")); parentID(m.rows, j) != sshFolderRow("eng") {
		t.Fatalf("tree:\n%s", render(m.rows))
	}
	if st := loadUIState(m.statePath); !strings.Contains(jsonText(t, st.Folders), `{"name":"eng","members":[{"host":"web"}]}`) {
		t.Fatalf("ui.json %s", jsonText(t, st.Folders))
	}
	if creates() != 0 {
		t.Fatal("a drag connected")
	}

	// The open session to box onto the SSH section: its host leaves prod.
	sshY := a1RowY(t, m, looseSSHID(localMachine))
	a2Run(a1Mouse(t, m, 10, a1RowY(t, m, paneNodeID(localMachine, "p5")), a1Left, a1Press))
	a2Run(a1Mouse(t, m, 10, sshY, a1Left, a1Motion))
	a2Run(a1Mouse(t, m, 10, sshY, a1Left, a1Release))
	if m.hostFolder("box") != "" || !slices.Contains(m.savedSSH, "box") {
		t.Fatalf("box in %q", m.hostFolder("box"))
	}
	// And back onto eng: the host goes, with its session.
	engY = a1RowY(t, m, sshFolderRow("eng"))
	a2Run(a1Mouse(t, m, 10, a1RowY(t, m, paneNodeID(localMachine, "p5")), a1Left, a1Press))
	a2Run(a1Mouse(t, m, 10, engY, a1Left, a1Motion))
	a2Run(a1Mouse(t, m, 10, engY, a1Left, a1Release))
	if m.hostFolder("box") != "eng" {
		t.Fatalf("box in %q", m.hostFolder("box"))
	}
	if jsonText(t, m.folders[sshFolderKey()][1].Members) != `[{"host":"web"},{"host":"box"}]` {
		t.Fatalf("eng holds %s (the session as a pane too?)", jsonText(t, m.folders[sshFolderKey()][1].Members))
	}

	// Let go on a project, or out in the panes: nothing moves.
	before := jsonText(t, m.folders)
	for _, at := range [][2]int{{10, a1RowY(t, m, projectNodeID(localMachine, "r1"))}, {m.sidebarW + 10, 10}} {
		a2Run(a1Mouse(t, m, 10, a1RowY(t, m, savedSSHID("db")), a1Left, a1Press))
		a2Run(a1Mouse(t, m, at[0], at[1], a1Left, a1Motion))
		a2Run(a1Mouse(t, m, at[0], at[1], a1Left, a1Release))
		if m.rowDrag != "" || jsonText(t, m.folders) != before {
			t.Fatalf("let go at %v moved something: %s", at, jsonText(t, m.folders))
		}
	}
	if creates() != 0 {
		t.Fatal("a drag connected")
	}

	// A press and release on a saved host is a click: it connects.
	dbY := a1RowY(t, m, savedSSHID("db"))
	a2Run(a1Mouse(t, m, 10, dbY, a1Left, a1Press))
	if creates() != 0 {
		t.Fatal("connected on the press, before knowing it wasn't a drag")
	}
	a2Run(a1Mouse(t, m, 10, dbY, a1Left, a1Release))
	peer.waitFor(t, "pane.create for db", func(msg proto.Message) bool { return msg.Method == proto.MethodPaneCreate })
}

func TestSavedHostClickWithoutFolders(t *testing.T) {
	// With no SSH folder there is nowhere to drag to: the press connects,
	// as it always did.
	m, peer := foldersFixture(t, true)
	m.folders = nil
	m.rebuild()
	a2Run(a1Mouse(t, m, 10, a1RowY(t, m, savedSSHID("web")), a1Left, a1Press))
	if m.rowDrag != "" {
		t.Fatalf("a drag started with nowhere to drop: %q", m.rowDrag)
	}
	peer.waitFor(t, "pane.create", func(msg proto.Message) bool { return msg.Method == proto.MethodPaneCreate })
}

func TestDragUnsavedSessionGoesInAsPane(t *testing.T) {
	m, _ := foldersFixture(t, false)
	m.savedSSH, m.sshInfo = []string{"web"}, nil
	m.rebuild()
	engY := a1RowY(t, m, sshFolderRow("eng"))
	a2Run(a1Mouse(t, m, 10, a1RowY(t, m, paneNodeID(localMachine, "p5")), a1Left, a1Press))
	a2Run(a1Mouse(t, m, 10, engY, a1Left, a1Motion))
	a2Run(a1Mouse(t, m, 10, engY, a1Left, a1Release))
	if slices.Contains(m.savedSSH, "box") {
		t.Fatal("dragging a session saved its host")
	}
	if jsonText(t, m.folders[sshFolderKey()][1].Members) != `[{"name":"ssh box","id":"p5"}]` {
		t.Fatalf("eng holds %s", jsonText(t, m.folders[sshFolderKey()][1].Members))
	}
	// The session in a project (p6) is no host to move: it is that
	// project's pane, started by hand.
	if t2 := m.sshTargetOfRow(m.rows[indexOfRow(m.rows, paneNodeID(localMachine, "p6"))]); t2 != "" {
		t.Fatalf("p6 taken for a host: %q", t2)
	}
}

func TestSSHFolderHintsAndMenu(t *testing.T) {
	m, _ := foldersFixture(t, false)
	id := sshFolderRow("prod")
	a1At(t, m, id)
	m.focus = focusSidebar
	hints := func() string {
		_, items := m.statusHints()
		var out []string
		for _, it := range items {
			out = append(out, ansi.Strip(it.text))
		}
		return strings.Join(out, "|")
	}
	if got := hints(); !strings.HasPrefix(got, "a add host|N new folder|x remove") {
		t.Fatalf("SSH folder hints %q", got)
	}
	mu := newRowMenu(*m, m.rows[indexOfRow(m.rows, id)], 0, 0)
	var keys []string
	for _, it := range mu.items {
		keys = append(keys, it.key)
	}
	if mu.title != "prod" || strings.Join(keys, ",") != "a,H,N,x" {
		t.Fatalf("menu %q %q", mu.title, keys)
	}
	// A folder of Terminals offers no host.
	m.newFolder(localMachine, "", kindTerminals, "scratch")
	m.rebuild()
	tid := folderRowID(localMachine, "", kindTerminals, "scratch")
	a1At(t, m, tid)
	if got := hints(); !strings.HasPrefix(got, "N new folder") {
		t.Fatalf("Terminals folder hints %q", got)
	}
	mu = newRowMenu(*m, m.rows[indexOfRow(m.rows, tid)], 0, 0)
	if len(mu.items) != 2 {
		t.Fatalf("Terminals folder menu %+v", mu.items)
	}
	// The saved host's page names its folder; every size stays in width.
	out := a2Plain(savedSSHLines("db", m.sshInfo["db"], m.hostFolder("db"), 100))
	for _, want := range []string{"prod db", "host     db", "folder   prod", "options  -p 2200", "drag into a folder"} {
		if !strings.Contains(out, want) {
			t.Fatalf("page lacks %q:\n%s", want, out)
		}
	}
	for _, w := range []int{1, 5, 20, 39, 100} {
		for _, l := range savedSSHLines("db", m.sshInfo["db"], "prod", w) {
			if ansi.StringWidth(l) > w {
				t.Fatalf("width %d: %q", w, ansi.Strip(l))
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
}

func TestStartSSHUsesSavedOptionsAndName(t *testing.T) {
	m, peer := foldersFixture(t, true)
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
	// The session is still found for its host, so it sits in prod with it.
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
	m, peer := foldersFixture(t, true)
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
	a1At(t, m, sshFolderRow("prod"))
	if cmd := a1Key(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("K")}); cmd != nil {
		t.Fatal("K on a folder did something")
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
