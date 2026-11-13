package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/cloud"
	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/remote"
)

const testCloudID = "m_aaaaaaaaaaaaaaaa"

func cloudModel(t *testing.T) (*Model, *machine) {
	t.Helper()
	a2Isolate(t)
	m := a2Model()
	if err := remote.SyncCloudMachines([]cloud.Machine{{ID: testCloudID, Name: "box", State: "running"}}); err != nil {
		t.Fatal(err)
	}
	m.syncCatalog()
	m.rebuild()
	mach := m.machine("box")
	if mach == nil {
		t.Fatal("the cloud machine is not in the tree")
	}
	return m, mach
}

// No account: the tick asks nothing, and a tree with no cloud machine has
// no cloud rows.
func TestCloudNothingWithoutAnAccount(t *testing.T) {
	a2Isolate(t)
	t.Setenv("CONCH_CLOUD", "1")
	m := a2Model()
	if cmd := m.askCloud(); cmd != nil {
		t.Fatal("asked Conch Cloud with no account")
	}
	for _, r := range m.rows {
		_, _, _, _, rt := m.rowParts(r)
		if strings.Contains(ansi.Strip(rt), "☁") {
			t.Fatalf("a cloud chip with no account: %+v", r)
		}
	}
}

// A machine that sleeps waits for the person, as a stopped sandbox does:
// waking it costs the plan's hours.
func TestCloudAsleepNeedsThePerson(t *testing.T) {
	m, mach := cloudModel(t)
	if cmd := mach.connected(machineConnectedMsg{machine: mach.id, err: &remote.CloudAsleepError{Label: "box", State: "asleep"}}); cmd != nil {
		t.Fatal("an asleep machine was scheduled for a retry")
	}
	if mach.state != stateAttention || mach.sandboxState != "asleep" {
		t.Fatalf("state %v %q", mach.state, mach.sandboxState)
	}
	r := row{id: machineID(mach.id), kind: kindMachine, machine: mach.id}
	_, _, _, _, rt := m.rowParts(r)
	if ansi.Strip(rt) != "☁ asleep" {
		t.Fatalf("row %q", ansi.Strip(rt))
	}
	menu := a2MenuLabels(newRowMenu(*m, r, 0, 0))
	if !strings.Contains(menu, "w Wake") || strings.Contains(menu, "Install") || strings.Contains(menu, "Remove machine") {
		t.Fatalf("menu %s", menu)
	}
	page := ansi.Strip(strings.Join(m.machineLines(mach, 100, 30), "\n"))
	if !strings.Contains(page, "m → Wake") || !strings.Contains(page, "conch cloud machine wake box") {
		t.Fatalf("page:\n%s", page)
	}
}

// The chips, for each state the control plane says, and at every width.
func TestCloudChips(t *testing.T) {
	m, mach := cloudModel(t)
	r := row{id: machineID(mach.id), kind: kindMachine, machine: mach.id}
	right := func() string { _, _, _, _, rt := m.rowParts(r); return ansi.Strip(rt) }
	mach.state = stateOffline
	for state, want := range map[string]string{"asleep": "☁ asleep", "sleeping": "☁ sleeping", "waking": "☁ waking",
		"creating": "☁ creating", "error": "☁ error", "archived": "☁ archived"} {
		m.cloudState = map[string]cloud.Machine{testCloudID: {ID: testCloudID, State: state}}
		if got := right(); got != want {
			t.Errorf("%s: %q", state, got)
		}
		for _, size := range [][2]int{{1, 1}, {20, 5}, {39, 12}, {120, 40}} {
			for _, l := range strings.Split(a1Sized(t, m, size[0], size[1]).View(), "\n") {
				if lw := ansi.StringWidth(l); lw > size[0] {
					t.Fatalf("%s at %dx%d: a line %d wide", state, size[0], size[1], lw)
				}
			}
		}
	}
	// Online: the cloud mark, and Sleep on its menu.
	mach.state = stateOnline
	m.cloudState = map[string]cloud.Machine{testCloudID: {ID: testCloudID, State: "running"}}
	if got := right(); !strings.HasPrefix(got, "☁") {
		t.Fatalf("online %q", got)
	}
	if menu := a2MenuLabels(newRowMenu(*m, r, 0, 0)); !strings.Contains(menu, "S Sleep") || strings.Contains(menu, "Wake") {
		t.Fatalf("online menu %s", menu)
	}
	mach.busy = "waking"
	if got := right(); got != "☁ waking" {
		t.Fatalf("busy %q", got)
	}
}

// The account's word: a machine woken elsewhere is connected to; a
// sign-in that ended takes the cloud machines out of the tree.
func TestCloudMachinesMsg(t *testing.T) {
	m, mach := cloudModel(t)
	mach.state, mach.sandboxState = stateAttention, "asleep"
	if cmd := m.cloudMachines(cloudMachinesMsg{machines: []cloud.Machine{{ID: testCloudID, Name: "box", State: "running"}}}); cmd == nil {
		t.Fatal("nothing done")
	}
	if mach.state != stateConnecting {
		t.Fatalf("a machine that woke was not connected to: %v", mach.state)
	}
	if m.cloudState[testCloudID].State != "running" {
		t.Fatalf("state %+v", m.cloudState)
	}
	m.cloudMachines(cloudMachinesMsg{err: cloud.ErrSignedOut})
	m.catalogStamp = ""
	m.syncCatalog()
	if m.machine("box") != nil || m.cloudState != nil {
		t.Fatal("the cloud machine outlived the sign-in")
	}
}

// Signed in, askCloud asks; the answer it brings syncs the catalog.
func TestCloudAskSyncs(t *testing.T) {
	a2Isolate(t)
	os.MkdirAll(os.Getenv("CONCH_HOME"), 0o700)
	os.WriteFile(filepath.Join(os.Getenv("CONCH_HOME"), "cloud.json"), []byte(`{"api":"http://x","store":"file"}`), 0o600)
	old := openCloud
	t.Cleanup(func() { openCloud = old })
	asked := 0
	openCloud = func() (*cloud.Client, error) { asked++; return nil, cloud.ErrSignedOut }
	m := a2Model()
	// Signed in but Conch Cloud hidden, with no cloud machine: nothing.
	if cmd := m.askCloud(); cmd != nil {
		t.Fatal("asked Conch Cloud while it is hidden")
	}
	t.Setenv("CONCH_CLOUD", "1")
	cmd := m.askCloud()
	if cmd == nil {
		t.Fatal("signed in, nothing asked")
	}
	msg := cmd().(cloudMachinesMsg)
	if asked != 1 || !errors.Is(msg.err, cloud.ErrSignedOut) {
		t.Fatalf("asked %d, %v", asked, msg.err)
	}
}

// Hidden is not broken: a cloud machine already in the tree keeps being
// asked about, and keeps its Wake, with Conch Cloud switched off.
func TestCloudHiddenKeepsAMachineSomebodyHas(t *testing.T) {
	m, mach := cloudModel(t)
	if config.CloudEnabled(m.cfg) {
		t.Fatal("the switch is on in this test")
	}
	os.WriteFile(filepath.Join(os.Getenv("CONCH_HOME"), "cloud.json"), []byte(`{"api":"http://x","store":"file"}`), 0o600)
	old := openCloud
	t.Cleanup(func() { openCloud = old })
	openCloud = func() (*cloud.Client, error) { return nil, cloud.ErrSignedOut }
	if m.askCloud() == nil {
		t.Fatal("a cloud machine in the tree is no longer asked about")
	}
	mach.connected(machineConnectedMsg{machine: mach.id, err: &remote.CloudAsleepError{Label: "box", State: "asleep"}})
	r := row{id: machineID(mach.id), kind: kindMachine, machine: mach.id}
	if menu := a2MenuLabels(newRowMenu(*m, r, 0, 0)); !strings.Contains(menu, "w Wake") {
		t.Fatalf("an asleep cloud machine has no Wake: %s", menu)
	}
	// The command it names works only with the switch on, so it says so.
	page := ansi.Strip(strings.Join(m.machineLines(mach, 120, 30), "\n"))
	if !strings.Contains(page, "CONCH_CLOUD=1 conch cloud machine wake box") {
		t.Fatalf("page:\n%s", page)
	}
	t.Setenv("CONCH_CLOUD", "1")
	page = ansi.Strip(strings.Join(m.machineLines(mach, 120, 30), "\n"))
	if !strings.Contains(page, "run: conch cloud machine wake box") {
		t.Fatalf("on, page:\n%s", page)
	}
}
