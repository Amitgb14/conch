package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/remote"
)

// promoted records what an add asked for, instead of making one.
func promoted(t *testing.T) *[]string {
	t.Helper()
	var got []string
	old := addMachineFn
	addMachineFn = func(target, label, password string, key bool, opts []string) tea.Cmd {
		got = append(got, target+" | "+label+" | "+strings.Join(opts, " "))
		return func() tea.Msg { return nil }
	}
	t.Cleanup(func() { addMachineFn = old })
	return &got
}

// A saved host conch already ssh's to becomes a machine without anybody
// retyping it: M fills in the host and the options it is saved with.
func TestPromoteSavedHostToMachine(t *testing.T) {
	m, _ := foldersFixture(t, false)
	got := promoted(t)

	id := savedSSHID("db")
	a1At(t, m, id)
	m.focus = focusSidebar
	a1Key(t, m, a2Key("M"))
	d, ok := m.overlay.(*dialog)
	if !ok {
		t.Fatalf("M opened %T", m.overlay)
	}
	if d.title != " db as a machine " {
		t.Fatalf("title %q", d.title)
	}
	// The host and its options are there to see; the label is left to be
	// typed, since the host's own name is not a machine's.
	if d.fields[0].in.Value() != "db" || d.fields[2].in.Value() != "-p 2200" || d.fields[1].in.Value() != "" {
		t.Fatalf("fields %q %q %q", d.fields[0].in.Value(), d.fields[1].in.Value(), d.fields[2].in.Value())
	}
	// What a machine is for, and what it does not take away, both said.
	text := strings.Join(d.text, " ")
	for _, want := range []string{"keep running when the link drops", "stays under SSH"} {
		if !strings.Contains(text, want) {
			t.Fatalf("dialog text lacks %q: %s", want, text)
		}
	}
	a2Run(d.submit(m, []string{"db", "prod db", "-p 2200", "", "on"}))
	if len(*got) != 1 || (*got)[0] != "db | prod db | -p 2200" {
		t.Fatalf("added %q", *got)
	}
}

// The same from a session logged in to a host, saved or not: the row the
// person is looking at is the one that offers it.
func TestPromoteFromAnSSHSession(t *testing.T) {
	m, _ := sshFixture(t, false)
	got := promoted(t)

	id := paneNodeID(localMachine, "p5") // ssh box, no saved host
	a1At(t, m, id)
	m.focus = focusSidebar
	a1Key(t, m, a2Key("M"))
	d, ok := m.overlay.(*dialog)
	if !ok {
		t.Fatalf("M on a session opened %T", m.overlay)
	}
	if d.fields[0].in.Value() != "box" || d.fields[2].in.Value() != "" {
		t.Fatalf("fields %q %q", d.fields[0].in.Value(), d.fields[2].in.Value())
	}
	a2Run(d.submit(m, []string{"box", "", "", "", ""}))
	if len(*got) != 1 || (*got)[0] != "box |  | " {
		t.Fatalf("added %q", *got)
	}

	// A session inside a project is that project's pane, started by hand,
	// and no host to promote: M there is the generic menu, as before.
	m.overlay = nil
	a1At(t, m, paneNodeID(localMachine, "p6"))
	m.focus = focusSidebar
	a1Key(t, m, a2Key("M"))
	if _, isMenu := m.overlay.(*menu); !isMenu {
		t.Fatalf("M in a project opened %T", m.overlay)
	}
}

// A host that is already a machine says so rather than offering it again,
// and the row leads to the machine it became.
func TestPromoteSaysWhenItIsAlreadyAMachine(t *testing.T) {
	m, _ := foldersFixture(t, false)
	got := promoted(t)
	m.machines = append(m.machines, machineFrom(remote.Machine{ID: "db-box", Label: "db box", Target: "db"}))
	m.rebuild()

	r := m.rows[indexOfRow(m.rows, savedSSHID("db"))]
	mu := newRowMenu(*m, r, 0, 0)
	var row menuItem
	for _, it := range mu.items {
		if strings.Contains(it.label, "machine") {
			row = it
		}
	}
	if row.key != "" || row.label != "Already the machine db box" {
		t.Fatalf("menu row %q %q", row.key, row.label)
	}
	a2Run(row.run(m))
	if m.cursor != machineID("db-box") {
		t.Fatalf("the row led to %q", m.cursor)
	}

	a1At(t, m, savedSSHID("db"))
	m.focus = focusSidebar
	a1Key(t, m, a2Key("M"))
	if m.overlay != nil || !strings.Contains(m.flash, "already the machine db box") {
		t.Fatalf("overlay %T flash %q", m.overlay, m.flash)
	}
	if len(*got) != 0 {
		t.Fatalf("added anyway: %q", *got)
	}

	// And the bar drops the key as the page does: the two disagreeing —
	// "M as a machine" on the bar under "machine db box" on the page —
	// is what this is for.
	hints := func() string {
		_, items := m.statusHints()
		var out []string
		for _, it := range items {
			out = append(out, ansi.Strip(it.text))
		}
		return strings.Join(out, "|")
	}
	if got := hints(); strings.Contains(got, "as a machine") {
		t.Fatalf("the bar still offered it: %q", got)
	}
	if got := hints(); !strings.HasPrefix(got, "enter connect|e edit|K copy key|x forget") {
		t.Fatalf("it took away more than the one key: %q", got)
	}
	// A host that is not a machine still has it.
	a1At(t, m, savedSSHID("web"))
	m.focus = focusSidebar
	if got := hints(); !strings.Contains(got, "M as a machine") {
		t.Fatalf("a host that is no machine lost the key: %q", got)
	}
	a1At(t, m, savedSSHID("db"))
	m.focus = focusSidebar

	// Its page says it too, so nobody goes looking for the key.
	page := a2Plain(savedSSHLines("db", m.sshInfo["db"], m.hostFolder("db"), "db box", 100))
	if !strings.Contains(page, "machine  db box") || strings.Contains(page, "M add as a machine") {
		t.Fatalf("page:\n%s", page)
	}
	page = a2Plain(savedSSHLines("db", m.sshInfo["db"], "", "", 100))
	if !strings.Contains(page, "M add as a machine") || strings.Contains(page, "machine  ") {
		t.Fatalf("page of a host that is no machine:\n%s", page)
	}
	// Every width still fits, with the hint on two lines now.
	for _, w := range []int{1, 5, 20, 39, 60, 100, 200} {
		for _, l := range savedSSHLines("db", m.sshInfo["db"], "prod", "db box", w) {
			if ansi.StringWidth(l) > w {
				t.Fatalf("width %d: %q is %d wide", w, ansi.Strip(l), ansi.StringWidth(l))
			}
		}
	}
}

// A sandbox is reached through its provider with a token of the moment, so
// there is no host of yours to install anything on.
func TestPromoteRefusesWhatIsNoHost(t *testing.T) {
	m, _ := foldersFixture(t, false)
	got := promoted(t)

	m.savedSSH = append(m.savedSSH, "daytona:abc123")
	a2Run(m.promoteSSH("daytona:abc123"))
	if m.overlay != nil || !strings.Contains(m.flash, "sandbox") {
		t.Fatalf("sandbox: overlay %T flash %q", m.overlay, m.flash)
	}
	if msg := a2ErrText(a2Run(m.promoteSSH(""))); msg == "" {
		t.Fatal("an empty host was taken for one")
	}
	// Options a hand-edited ui.json could hold are refused here too,
	// rather than reaching the add as something ssh cannot use.
	m.sshInfo["db"] = sshHostInfo{Args: []string{"-p"}}
	if msg := a2ErrText(a2Run(m.promoteSSH("db"))); !strings.Contains(msg, "needs a value") {
		t.Fatalf("bad saved options: %q", msg)
	}
	if len(*got) != 0 {
		t.Fatalf("added: %q", *got)
	}
}

// The options a machine is reached with are its own: a change to them is a
// different way in, so the connection is made again rather than kept.
func TestMachineOptsFromTheCatalog(t *testing.T) {
	m, _ := a1Fixture(t, false)
	mach := machineFrom(remote.Machine{ID: "box", Label: "box", Target: "me@box", Opts: []string{"-p", "2222"}})
	if strings.Join(mach.opts, " ") != "-p 2222" {
		t.Fatalf("opts %q", mach.opts)
	}
	m.machines = append(m.machines, mach)
	mach.state = stateOnline

	saved := []remote.Machine{{ID: "box", Label: "box", Target: "me@box", Opts: []string{"-p", "2200"}, Enabled: true}}
	a2Run(m.applyCatalog(saved))
	again := m.machine("box")
	if again == nil || strings.Join(again.opts, " ") != "-p 2200" {
		t.Fatalf("after the catalog changed: %+v", again)
	}
	if again == mach {
		t.Fatal("the old connection was kept for a machine reached another way")
	}
	// The same options are no change at all: the machine stays connected.
	before := m.machine("box")
	a2Run(m.applyCatalog(saved))
	if m.machine("box") != before {
		t.Fatal("an unchanged catalog dropped the connection")
	}
}

// The machine a target belongs to, which is what both the menu row and the
// host's page ask about.
func TestMachineWithTarget(t *testing.T) {
	m, _ := a1Fixture(t, false)
	if got := m.machineWithTarget(""); got != nil {
		t.Fatalf("this computer answered for an empty target: %+v", got)
	}
	m.machines = append(m.machines, machineFrom(remote.Machine{ID: "b", Label: "b", Target: "me@box"}))
	if got := m.machineWithTarget("me@box"); got == nil || got.id != "b" {
		t.Fatalf("by target: %+v", got)
	}
	if got := m.machineWithTarget("box"); got != nil {
		t.Fatalf("a target is matched as written, not resolved: %+v", got)
	}
}

// An ssh session is still not an agent, whatever the promote offers: that
// is what the machine is for, and the row must not claim otherwise.
func TestPromoteLeavesTheSessionAlone(t *testing.T) {
	m, _ := sshFixture(t, false)
	promoted(t)
	before := len(m.machines[0].panes)
	a2Run(m.promoteSSH("box"))
	d := m.overlay.(*dialog)
	a2Run(d.submit(m, []string{"box", "", "", "", ""}))
	if len(m.machines[0].panes) != before {
		t.Fatalf("panes went from %d to %d", before, len(m.machines[0].panes))
	}
	if p := m.pane(localMachine, "p5"); p == nil || p.State != proto.PaneRunning {
		t.Fatalf("the session: %+v", p)
	}
}

// M where no host is selected still asks what to add — and offers the
// hosts already saved, so the answer is never to type one out again.
func TestAddMachineMenuOffersSavedHosts(t *testing.T) {
	m, _ := foldersFixture(t, false)
	promoted(t)

	keys := func(mu *menu) string {
		var out []string
		for _, it := range mu.items {
			out = append(out, it.key+" "+it.label)
		}
		return strings.Join(out, " | ")
	}
	mu := m.addMachineMenu()
	if got := keys(mu); !strings.HasPrefix(got, "s Over ssh… | h A host you already ssh to…") {
		t.Fatalf("add menu %q", got)
	}
	a2Run(mu.items[1].run(m))
	hosts, ok := m.overlay.(*menu)
	if !ok {
		t.Fatalf("hosts opened %T", m.overlay)
	}
	if got := keys(hosts); got != "1 ssh box | 2 prod db | 3 ssh web" {
		t.Fatalf("hosts %q", got)
	}
	// Picking one is the promote, prefilled as from its own row.
	a2Run(hosts.items[1].run(m))
	d, ok := m.overlay.(*dialog)
	if !ok || d.fields[0].in.Value() != "db" || d.fields[2].in.Value() != "-p 2200" {
		t.Fatalf("picked: %T %+v", m.overlay, d)
	}

	// A host that is already a machine is not offered twice, and with
	// none left the row goes rather than leading to an empty menu.
	for _, target := range []string{"box", "db", "web"} {
		m.machines = append(m.machines, machineFrom(remote.Machine{ID: target, Label: target, Target: target}))
	}
	if got := keys(m.addMachineMenu()); strings.Contains(got, "already ssh to") {
		t.Fatalf("every host is a machine: %q", got)
	}
	if hs := m.promotableHosts(); len(hs) != 0 {
		t.Fatalf("promotable %q", hs)
	}
}

// The keys the help names are the keys there are: M on a host went
// missing from this list once, which nothing else would have noticed.
func TestHelpNamesThePromote(t *testing.T) {
	help := strings.Join(helpText, "\n")
	for _, want := range []string{"M is that host as a machine", "a host already saved under SSH",
		"what runs in it ends with the link"} {
		if !strings.Contains(help, want) {
			t.Fatalf("the help does not say %q", want)
		}
	}
}
