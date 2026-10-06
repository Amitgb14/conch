package tui

import (
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/phone"
)

// pairDevices pairs names, as phones would, in the test's conch directory.
func pairDevices(t *testing.T, store *phone.Store, perms ...string) []phone.Device {
	t.Helper()
	var devs []phone.Device
	for i, perm := range perms {
		code, err := store.NewCode(perm, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		d, _, err := store.Redeem(code, []string{"iPhone", "Browser", "iPad"}[i%3], time.Now())
		if err != nil {
			t.Fatal(err)
		}
		devs = append(devs, d)
	}
	return devs
}

// Settings → Web → Devices: every paired device, what it may do changed
// in place, revoking one with a second x, and back to the settings.
func TestDevicesPanel(t *testing.T) {
	store := pairHome(t, "https://laptop.tail1234.ts.net")
	devs := pairDevices(t, store, phone.PermReply, phone.PermFull, phone.PermView)
	m := a2Model()
	s := &settings{}
	s.setTab(webTab)
	m.overlay = s
	var row settingItem
	for _, it := range s.items(m) {
		if strings.HasPrefix(it.label, "Devices") {
			row = it
		}
	}
	if !row.page || ansi.Strip(row.detail) != "3 paired · 1 full, 1 reply, 1 view" {
		t.Fatalf("settings row %+v", row)
	}
	row.run(m)
	p, ok := m.overlay.(*devicesPanel)
	if !ok || len(p.devices) != 3 {
		t.Fatalf("overlay %T", m.overlay)
	}
	b := p.render(*m)
	a2CheckBox(t, b, *m)
	out := a2Plain(b.lines)
	for _, d := range devs {
		if !strings.Contains(out, d.ID) {
			t.Fatalf("lacks %s:\n%s", d.ID, out)
		}
	}
	if !strings.Contains(out, "iPhone may reply and answer, on this computer only.") || !strings.Contains(out, "x revoke") {
		t.Fatalf("panel:\n%s", out)
	}
	// Where a device may act is shown beside what it may do, with the
	// command that changes it — a phone reaching another machine is the
	// part nobody expects, so the panel says it without being asked.
	if !strings.Contains(out, "-machine NAME adds one.") {
		t.Fatalf("the panel does not say how to give it a machine:\n%s", out)
	}
	if _, err := store.SetMachines(devs[0].ID, []string{"busybox", "vm2", "gpu-1"}); err != nil {
		t.Fatal(err)
	}
	p.load()
	out = a2Plain(p.render(*m).lines)
	if !strings.Contains(out, "on this computer, busybox, gpu-1 and vm2.") {
		t.Fatalf("the machines a device reaches:\n%s", out)
	}
	if strings.Contains(out, "-machine NAME adds one") {
		t.Errorf("it still offers to give a machine to one that has three:\n%s", out)
	}
	a2CheckBox(t, p.render(*m), *m)

	// f on the first: full, saved, and said.
	p.update(m, a2Key("f"))
	if got, _ := store.Devices(); got[0].Permission != phone.PermFull || !strings.Contains(m.flash, "may now do everything") {
		t.Fatalf("after f: %+v %q", got[0], m.flash)
	}
	p.update(m, a2Key("down"))
	p.update(m, a2Key("v"))
	p.update(m, a2Key("down"))
	p.update(m, a2Key("r"))
	got, _ := store.Devices()
	if got[1].Permission != phone.PermView || got[2].Permission != phone.PermReply {
		t.Fatalf("after v, r: %+v", got)
	}
	p.update(m, a2Key("down")) // already the last
	if p.sel != 2 {
		t.Fatalf("sel %d", p.sel)
	}

	// x asks; anything else forgets the asking; x twice revokes.
	p.update(m, a2Key("x"))
	if !strings.Contains(a2Plain(p.render(*m).lines), "x again revokes") {
		t.Fatal("no asking")
	}
	p.update(m, a2Key("up"))
	p.update(m, a2Key("down"))
	p.update(m, a2Key("x"))
	if got, _ := store.Devices(); len(got) != 3 {
		t.Fatal("revoked on one x")
	}
	p.update(m, a2Key("x"))
	if got, _ := store.Devices(); len(got) != 2 || p.sel != 1 || !strings.Contains(m.flash, "revoked iPad") {
		t.Fatalf("after x x: %+v sel %d %q", got, p.sel, m.flash)
	}

	// A click selects the device under it; outside goes back.
	b = p.render(*m)
	p.mouse(m, tea.MouseMsg{X: b.x + 3, Y: b.y + 2, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}, b)
	if p.sel != 0 {
		t.Fatalf("click on the first row: sel %d", p.sel)
	}
	p.mouse(m, tea.MouseMsg{X: b.x + 3, Y: b.y + 3, Button: tea.MouseButtonWheelDown}, b)
	if p.sel != 1 {
		t.Fatalf("wheel: sel %d", p.sel)
	}
	p.mouse(m, tea.MouseMsg{X: b.x + b.width() + 2, Y: b.y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}, b)
	if m.overlay != s {
		t.Fatalf("a click outside: overlay %T", m.overlay)
	}
	m.overlay = p
	p.update(m, a2Key("esc"))
	if m.overlay != s {
		t.Fatal("esc didn't go back to the settings")
	}
}

func TestDevicesPanelEmptyBrokenAndSizes(t *testing.T) {
	store := pairHome(t, "")
	m := a2Model()
	p := newDevicesPanel(nil)
	if out := a2Plain(p.render(*m).lines); !strings.Contains(out, "No phones or browsers are paired") {
		t.Fatalf("empty:\n%s", out)
	}
	for _, k := range []string{"f", "x", "x", "down", "up"} {
		p.update(m, a2Key(k)) // nothing to act on: nothing breaks
	}
	if devicesSummary() != "none paired" {
		t.Fatalf("summary %q", devicesSummary())
	}
	pairDevices(t, store, phone.PermFull, phone.PermFull, phone.PermFull, phone.PermFull, phone.PermFull, phone.PermFull)
	p.load()
	for _, size := range [][2]int{{1, 1}, {20, 5}, {30, 9}, {50, 12}, {80, 24}, {200, 60}} {
		m.width, m.height = size[0], size[1]
		b := p.render(*m)
		a2CheckBox(t, b, *m)
		if m.width >= 28 && b.width() > m.width {
			t.Fatalf("%v: %d wide", size, b.width())
		}
	}
	// A short screen scrolls to keep the selection in view.
	m.width, m.height = 80, 10
	for range 5 {
		p.update(m, a2Key("down"))
	}
	if out := a2Plain(p.render(*m).lines); !strings.Contains(out, p.devices[5].ID) {
		t.Fatalf("selection out of view:\n%s", out)
	}
	// A file that can't be read says so.
	writeBroken(t, store)
	p.load()
	if out := a2Plain(p.render(*m).lines); !strings.Contains(out, "Can't read the devices") || !strings.Contains(devicesSummary(), "can't be read") {
		t.Fatalf("broken:\n%s", out)
	}
}

func writeBroken(t *testing.T, store *phone.Store) {
	t.Helper()
	if err := os.WriteFile(store.Path(), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
}
