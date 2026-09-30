package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/phone"
)

// pairHome gives the test a conch directory of its own: pairing writes a
// code to phone.json there, never to the developer's.
func pairHome(t *testing.T, url string) *phone.Store {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CONCH_HOME", dir)
	t.Setenv("CONCH_SOCKET", "")
	t.Setenv("CONCH_PANE_ID", "")
	store := phone.OpenStore(dir)
	if url != "" {
		if err := store.SetURL(url); err != nil {
			t.Fatal(err)
		}
	}
	return store
}

// P in the tree opens the dialog with a fresh code, which pairs a phone;
// the QR code in it is the link to that code, drawn whole.
func TestPairKeyShowsACodeThatPairs(t *testing.T) {
	store := pairHome(t, "https://laptop.tail1234.ts.net")
	m, _ := a1Fixture(t, false)
	a1At(t, m, machineID(localMachine))
	next, _ := m.handleKey(a2Key("P"))
	*m = next.(Model)
	d, ok := m.overlay.(*pairDialog)
	if !ok {
		t.Fatalf("overlay %T", m.overlay)
	}
	if d.permission != phone.PermReply || d.err != "" || len(d.code) != 7 || time.Until(d.expires) > phone.PairTTL {
		t.Fatalf("dialog %+v", d)
	}

	b := d.render(*m)
	a2CheckBox(t, b, *m)
	out := a2Plain(b.lines)
	for _, want := range []string{"Pair a phone with this computer", "Scan it with the phone's camera", d.code,
		"· reply ·", "https://laptop.tail1234.ts.net", "v/r/f new code", "y copy link"} {
		if !strings.Contains(out, want) {
			t.Fatalf("dialog lacks %q:\n%s", want, out)
		}
	}
	want, _ := phone.QRLines(phone.PairLink("https://laptop.tail1234.ts.net", d.code))
	joined := strings.Join(b.lines, "\n")
	for i, l := range want {
		if !strings.Contains(joined, l) {
			t.Fatalf("QR line %d is not in the dialog whole", i)
		}
	}

	// The code shown is the one a phone can pair with.
	dev, _, err := store.Redeem(d.code, "phone", time.Now())
	if err != nil || dev.Permission != phone.PermReply {
		t.Fatalf("pairing with the shown code: %+v %v", dev, err)
	}
}

func TestPairDialogKeys(t *testing.T) {
	store := pairHome(t, "https://laptop.tail1234.ts.net")
	m := a2Model()
	d := newPairDialog(phone.PermReply)
	m.overlay = d

	// v and f give a new code with that permission; the old one is spent.
	if closed, _ := d.update(m, a2Key("f")); closed {
		t.Fatal("f closed it")
	}
	full := m.overlay.(*pairDialog)
	if full.permission != phone.PermFull || full.code == d.code {
		t.Fatalf("after f: %+v", full)
	}
	if _, _, err := store.Redeem(d.code, "x", time.Now()); err == nil {
		t.Fatal("the replaced code still pairs")
	}
	full.update(m, a2Key("v"))
	view := m.overlay.(*pairDialog)
	if view.permission != phone.PermView {
		t.Fatalf("after v: %+v", view)
	}
	view.update(m, a2Key("r"))
	d = m.overlay.(*pairDialog)

	// y copies the link, and leaves the dialog open.
	lastClipboard = ""
	closed, cmd := d.update(m, a2Key("y"))
	a2Run(cmd)
	if closed || m.overlay != d || lastClipboard != phone.PairLink("https://laptop.tail1234.ts.net", d.code) {
		t.Fatalf("y: closed %v, clipboard %q", closed, lastClipboard)
	}
	// Something else happening is not a key.
	if closed, _ := d.update(m, tickMsg{}); closed || m.overlay != d {
		t.Fatal("a tick closed it")
	}
	for _, k := range []string{"esc", "enter", "q", "P", "x"} {
		m.overlay = d
		if closed, _ := d.update(m, a2Key(k)); !closed || m.overlay != nil {
			t.Fatalf("%s left it open", k)
		}
	}

	// A click outside closes it; one inside doesn't.
	m.overlay = d
	b := d.render(*m)
	d.mouse(m, tea.MouseMsg{X: b.x + 1, Y: b.y + 1, Action: tea.MouseActionPress}, b)
	if m.overlay != d {
		t.Fatal("a click inside closed it")
	}
	d.mouse(m, tea.MouseMsg{X: b.x + b.width() + 1, Y: b.y, Action: tea.MouseActionPress}, b)
	if m.overlay != nil {
		t.Fatal("a click outside left it open")
	}
}

// The dialog keeps to the screen at every size, and shows the QR code only
// when all of it fits: a code cut short doesn't scan.
func TestPairDialogSizes(t *testing.T) {
	pairHome(t, "https://laptop.tail1234.ts.net")
	m := a2Model()
	d := newPairDialog(phone.PermReply)
	qrW := ansi.StringWidth(d.qr[0])
	for _, size := range [][2]int{{1, 1}, {20, 5}, {30, 10}, {39, 20}, {qrW + 3, 40}, {qrW + 4, 40}, {80, 23}, {80, 24}, {200, 60}} {
		m.width, m.height = size[0], size[1]
		b := d.render(*m)
		a2CheckBox(t, b, *m)
		if m.width >= 22 && b.width() > m.width {
			t.Fatalf("%v: %d wide", size, b.width())
		}
		shown := strings.Contains(strings.Join(b.lines, "\n"), d.qr[0])
		if shown && (len(b.lines) > m.height || b.width() > m.width) {
			t.Fatalf("%v: the QR code shown in a box of %dx%d", size, b.width(), len(b.lines))
		}
		// An ordinary terminal is room enough; a narrow or short one isn't.
		if want := size[0] >= 80 && size[1] >= 24 || size == [2]int{qrW + 4, 40}; shown != want {
			t.Fatalf("%v: QR shown %v:\n%s", size, shown, a2Plain(b.lines))
		}
		if !shown && m.width >= 40 && !strings.Contains(a2Plain(b.lines), "A larger terminal shows a QR code") {
			t.Fatalf("%v: no word about the missing code:\n%s", size, a2Plain(b.lines))
		}
		// The whole screen, dialog included, stays within its width.
		m.overlay = d
		for _, l := range strings.Split(m.View(), "\n") {
			if ansi.StringWidth(l) > m.width && m.width >= 22 {
				t.Fatalf("%v: a line is %d wide", size, ansi.StringWidth(l))
			}
		}
		m.overlay = nil
	}
}

// Without a gateway on record there is nothing to link to: the code is
// there to type, with how to start the gateway; with a store that can't
// be read, why there is no code.
func TestPairDialogWithoutAGateway(t *testing.T) {
	store := pairHome(t, "")
	m := a2Model()
	d := newPairDialog(phone.PermReply)
	out := a2Plain(d.render(*m).lines)
	if d.qr != nil || d.link() != "" || !strings.Contains(out, "conch web has never run here") || !strings.Contains(out, d.code) || strings.Contains(out, "y copy") || !strings.Contains(out, "view/reply/full") {
		t.Fatalf("no gateway:\n%s", out)
	}
	lastClipboard = "before"
	if _, cmd := d.update(m, a2Key("y")); cmd != nil || lastClipboard != "before" {
		t.Fatal("copied a link that isn't there")
	}

	os.WriteFile(filepath.Join(filepath.Dir(store.Path()), phone.StoreFile), []byte("{"), 0o600)
	d = newPairDialog(phone.PermReply)
	b := d.render(*m)
	a2CheckBox(t, b, *m)
	if out := a2Plain(b.lines); d.code != "" || !strings.Contains(out, "No pairing code:") {
		t.Fatalf("broken store:\n%s", out)
	}
}

func TestPairIsInTheHelpAndHints(t *testing.T) {
	if !strings.Contains(strings.Join(helpText, "\n"), "  P  pair a phone with this computer") {
		t.Fatal("the help doesn't list P")
	}
	pairHome(t, "")
	m, _ := a1Fixture(t, false)
	hints := func() string {
		_, items := m.statusHints()
		var s []string
		for _, it := range items {
			s = append(s, ansi.Strip(it.text))
		}
		return strings.Join(s, "|")
	}
	a1At(t, m, machineID(localMachine))
	if !strings.HasSuffix(hints(), "? keys|P phone") {
		t.Fatalf("local machine hints %q", hints())
	}
	rm := newMachine("dev", "dev", "dev@box")
	rm.state = stateOnline
	m.machines = append(m.machines, rm)
	m.rebuild()
	a1At(t, m, machineID("dev"))
	if strings.Contains(hints(), "P phone") {
		t.Fatalf("a remote machine offers pairing: %q", hints())
	}
}
