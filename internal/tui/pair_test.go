package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/config"
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
	if d.permission != phone.PermFull || d.err != "" || len(d.code) != 7 || time.Until(d.expires) > phone.PairTTL {
		t.Fatalf("dialog %+v", d)
	}

	b := d.render(*m)
	a2CheckBox(t, b, *m)
	out := a2Plain(b.lines)
	for _, want := range []string{"Pair a phone with this computer", "Scan it with the phone's camera", d.code,
		"· full ·", "https://laptop.tail1234.ts.net", "v/r/f new code", "click or y/c copies link/code"} {
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
	if err != nil || dev.Permission != phone.PermFull {
		t.Fatalf("pairing with the shown code: %+v %v", dev, err)
	}
}

func TestPairDialogKeys(t *testing.T) {
	store := pairHome(t, "https://laptop.tail1234.ts.net")
	m := a2Model()
	d := newPairDialog(config.WebCfg{}, phone.PermReply)
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
	d.mouse(m, tea.MouseMsg{X: b.x + 1, Y: b.y + 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}, b)
	if m.overlay != d {
		t.Fatal("a click inside closed it")
	}
	d.mouse(m, tea.MouseMsg{X: b.x + b.width() + 1, Y: b.y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}, b)
	if m.overlay != nil {
		t.Fatal("a click outside left it open")
	}
}

// The dialog keeps to the screen at every size, and shows the QR code only
// when all of it fits: a code cut short doesn't scan.
func TestPairDialogSizes(t *testing.T) {
	pairHome(t, "https://laptop.tail1234.ts.net")
	m := a2Model()
	d := newPairDialog(config.WebCfg{}, phone.PermReply)
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
		switch size {
		case [2]int{80, 24}, [2]int{200, 60}, [2]int{qrW + 4, 40}:
			if !shown {
				t.Fatalf("%v: no QR code:\n%s", size, a2Plain(b.lines))
			}
		case [2]int{1, 1}, [2]int{20, 5}, [2]int{30, 10}, [2]int{39, 20}, [2]int{qrW + 3, 40}:
			if shown {
				t.Fatalf("%v: a QR code that can't fit", size)
			}
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
	d := newPairDialog(config.WebCfg{}, phone.PermReply)
	out := a2Plain(d.render(*m).lines)
	if d.qr != nil || d.link() != "" || !strings.Contains(out, "Settings → Web") || !strings.Contains(out, d.code) || strings.Contains(out, "y/c") || !strings.Contains(out, "view/reply/full") {
		t.Fatalf("no gateway:\n%s", out)
	}
	lastClipboard = "before"
	if _, cmd := d.update(m, a2Key("y")); cmd != nil || lastClipboard != "before" {
		t.Fatal("copied a link that isn't there")
	}

	os.WriteFile(filepath.Join(filepath.Dir(store.Path()), phone.StoreFile), []byte("{"), 0o600)
	d = newPairDialog(config.WebCfg{}, phone.PermReply)
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

// The status bar's ☏ opens the dialog; the dialog says whether conch web
// runs, and s starts it again as it was last started.
func TestPairFromTheStatusBarAndStartingTheGateway(t *testing.T) {
	store := pairHome(t, "https://laptop.tail1234.ts.net")
	m, _ := a1Fixture(t, false)
	var phoneItem *statusItem
	var texts []string
	for _, it := range m.statusRightItems(rightFull, false) {
		texts = append(texts, ansi.Strip(it.text))
		if ansi.Strip(it.text) == "🌐" {
			it := it
			phoneItem = &it
		}
	}
	joined := strings.Join(texts, "|")
	if phoneItem == nil || !strings.Contains(joined, "⚙ Settings |🌐|"+versionLabel()) {
		t.Fatalf("right items %q", joined)
	}
	phoneItem.act(m)
	d, ok := m.overlay.(*pairDialog)
	if !ok || d.permission != phone.PermFull || d.code == "" {
		t.Fatalf("overlay %T %+v", m.overlay, m.overlay)
	}
	// With icons only it is still there; on the narrowest bar it gives
	// way to the monitor in the corner.
	has := func(level int) bool {
		for _, it := range m.statusRightItems(level, false) {
			if ansi.Strip(it.text) == "🌐" {
				return true
			}
		}
		return false
	}
	if !has(rightIcons) || has(rightMinimal) {
		t.Fatalf("🌐 at icons %v, at minimal %v", has(rightIcons), has(rightMinimal))
	}

	// conch web never ran: nothing to start, and s does nothing.
	var started []*exec.Cmd
	old := startOutward
	t.Cleanup(func() { startOutward = old })
	startOutward = func(c *exec.Cmd) error { started = append(started, c); return nil }
	if out := a2Plain(d.render(*m).lines); strings.Contains(out, "isn't running") {
		t.Fatalf("a start offered with nothing to start from:\n%s", out)
	}
	d.update(m, a2Key("s"))
	if len(started) != 0 {
		t.Fatal("started with no record of how")
	}

	// It ran once and stopped: s starts it again, the same way, in the
	// background, and the dialog looks again a moment later.
	store.GatewayStarted(1<<30, []string{"-url", "https://laptop.tail1234.ts.net"}, time.Now())
	m.width, m.height = 80, 24
	d = newPairDialog(config.WebCfg{}, phone.PermFull)
	m.overlay = d
	b := d.render(*m)
	a2CheckBox(t, b, *m)
	if out := a2Plain(b.lines); !strings.Contains(out, "conch web isn't running — s starts it") || !strings.Contains(strings.Join(b.lines, "\n"), d.qr[0]) {
		t.Fatalf("not running, at 80x24:\n%s", out)
	}
	closed, cmd := d.update(m, a2Key("s"))
	if closed || cmd == nil || len(started) != 1 || !d.starting {
		t.Fatalf("s: closed %v cmd %v started %d", closed, cmd != nil, len(started))
	}
	c := started[0]
	if exe, _ := os.Executable(); c.Path != exe || strings.Join(c.Args[1:], " ") != "web -url https://laptop.tail1234.ts.net" ||
		c.SysProcAttr == nil || !c.SysProcAttr.Setsid || c.Stdout == nil {
		t.Fatalf("started %v %+v", c.Args, c.SysProcAttr)
	}
	if !strings.Contains(a2Plain(d.render(*m).lines), "starting conch web") {
		t.Fatal("no word while it starts")
	}
	d.update(m, a2Key("s")) // not twice
	if len(started) != 1 {
		t.Fatal("started twice")
	}
	// The look again: it didn't come up (nothing really ran here).
	d.update(m, pairCheckMsg{})
	if d.starting || d.running || !strings.Contains(d.err, "web.log") {
		t.Fatalf("after the check: %+v", d)
	}
	// Running: said so, and s does nothing.
	store.GatewayStarted(os.Getpid(), []string{"-url", "x"}, time.Now())
	d = newPairDialog(config.WebCfg{}, phone.PermFull)
	if !d.running || !strings.Contains(a2Plain(d.render(*m).lines), "conch web is running") {
		t.Fatalf("running: %+v", d)
	}
	d.update(m, a2Key("s"))
	if len(started) != 1 {
		t.Fatal("started one that runs")
	}
}

// Settings → Web: the address phones open and the port, saved in [web];
// conch web started and stopped from there; and the pairing dialog using
// the address set, starting conch web with no flags since it reads them.
func TestSettingsPhoneTab(t *testing.T) {
	store := pairHome(t, "")
	m, _ := a1Fixture(t, false)
	s := &settings{}
	s.setTab(webTab)
	if settingsTabs[webTab] != "Web" {
		t.Fatalf("tabs %v", settingsTabs)
	}
	find := func(label string) settingItem {
		t.Helper()
		for _, it := range s.items(m) {
			if strings.HasPrefix(ansi.Strip(it.label), label) {
				return it
			}
		}
		t.Fatalf("no item %q", label)
		return settingItem{}
	}
	plain := func() string {
		var out []string
		for _, it := range s.items(m) {
			out = append(out, ansi.Strip(it.label+" "+it.detail))
		}
		return strings.Join(out, "\n")
	}
	if out := plain(); !strings.Contains(out, "Address phones open not set") || !strings.Contains(out, "Port 8722") ||
		!strings.Contains(out, "Set the address first") || strings.Contains(out, "Start conch web") {
		t.Fatalf("nothing set:\n%s", out)
	}
	submit := func(label, value string) error {
		t.Helper()
		find(label).run(m)
		d, ok := m.overlay.(*dialog)
		if !ok {
			t.Fatalf("%s: overlay %T", label, m.overlay)
		}
		for _, msg := range a2Run(d.submit(m, []string{value})) {
			if e, ok := msg.(errMsg); ok {
				return e.err
			}
		}
		return nil
	}
	for _, bad := range []string{"laptop.ts.net", "ftp://x", "https://x/?a=1", "https://me@x"} {
		if submit("Address phones open", bad) == nil {
			t.Errorf("address %q taken", bad)
		}
	}
	for _, bad := range []string{"0", "70000", "http"} {
		if submit("Port", bad) == nil {
			t.Errorf("port %q taken", bad)
		}
	}
	if m.cfg.Web != (config.WebCfg{}) {
		t.Fatalf("refused values were kept: %+v", m.cfg.Web)
	}
	if err := submit("Address phones open", " https://laptop.tail1234.ts.net/ "); err != nil {
		t.Fatal(err)
	}
	if err := submit("Port", "9000"); err != nil {
		t.Fatal(err)
	}
	saved, _ := config.Load()
	if m.cfg.Web != (config.WebCfg{URL: "https://laptop.tail1234.ts.net", Port: 9000}) || saved.Web != m.cfg.Web {
		t.Fatalf("set %+v, saved %+v", m.cfg.Web, saved.Web)
	}
	if out := plain(); !strings.Contains(out, "conch web isn't running") || !strings.Contains(out, "tailscale serve --bg http://127.0.0.1:9000") {
		t.Fatalf("set:\n%s", out)
	}

	// Start: conch web with no flags — it reads what was just set.
	var started []*exec.Cmd
	old := startOutward
	t.Cleanup(func() { startOutward = old })
	startOutward = func(c *exec.Cmd) error { started = append(started, c); return nil }
	find("Start conch web").run(m)
	if len(started) != 1 || strings.Join(started[0].Args[1:], " ") != "web" || !started[0].SysProcAttr.Setsid {
		t.Fatalf("started %v", started)
	}

	// The dialog: the address set, and s starting it the same way.
	find("Pair a phone").run(m)
	d, ok := m.overlay.(*pairDialog)
	if !ok || d.link() != phone.PairLink("https://laptop.tail1234.ts.net", d.code) {
		t.Fatalf("dialog %+v", m.overlay)
	}
	d.update(m, a2Key("s"))
	if len(started) != 2 || strings.Join(started[1].Args[1:], " ") != "web" {
		t.Fatalf("s started %v", started[len(started)-1].Args)
	}

	// Running: Stop signals that process, and nothing else.
	store.GatewayStarted(4242, nil, time.Now())
	signalled = nil
	defer func() { signalled = nil }()
	if !processAliveForTest(4242) {
		// No process 4242 here: record this one instead, which is alive.
		store.GatewayStarted(os.Getpid(), nil, time.Now())
	}
	if out := plain(); !strings.Contains(out, "conch web is running") {
		t.Fatalf("running:\n%s", out)
	}
	find("Stop conch web").run(m)
	run, _ := store.Gateway()
	if len(signalled) != 1 || signalled[0] != run.PID {
		t.Fatalf("signalled %v, want %d", signalled, run.PID)
	}
}

func processAliveForTest(pid int) bool { return syscall.Kill(pid, 0) == nil }

// The dialog draws over the screen, so nothing in it can be selected: a
// click copies instead — the code on its line, the link anywhere else —
// and c copies the code. Settings → Web's commands copy the same way.
func TestPairAndSettingsCopy(t *testing.T) {
	pairHome(t, "https://laptop.tail1234.ts.net")
	m := a2Model()
	m.width, m.height = 100, 40
	d := newPairDialog(config.WebCfg{}, phone.PermFull)
	m.overlay = d
	b := d.render(*m)
	click := func(x, y int) {
		t.Helper()
		lastClipboard = ""
		a2Run(d.mouse(m, tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}, b))
	}
	if d.codeRow < 1 || !strings.Contains(ansi.Strip(b.lines[d.codeRow]), d.code) {
		t.Fatalf("code row %d: %q", d.codeRow, ansi.Strip(b.lines[max(d.codeRow, 0)]))
	}
	click(b.x+5, b.y+d.codeRow)
	if lastClipboard != d.code || m.overlay != d {
		t.Fatalf("click on the code: %q, overlay %T", lastClipboard, m.overlay)
	}
	click(b.x+5, b.y+3) // in the QR code
	if lastClipboard != d.link() {
		t.Fatalf("click elsewhere: %q", lastClipboard)
	}
	lastClipboard = ""
	a2Run(d.mouse(m, tea.MouseMsg{X: b.x + 5, Y: b.y + 3, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease}, b))
	if lastClipboard != "" {
		t.Fatal("a release copied")
	}
	lastClipboard = ""
	_, cmd := d.update(m, a2Key("c"))
	a2Run(cmd)
	if lastClipboard != d.code || m.overlay != d {
		t.Fatalf("c: %q", lastClipboard)
	}
	if !strings.Contains(a2Plain(b.lines), "click or y/c copies link/code") {
		t.Fatalf("hint:\n%s", a2Plain(b.lines))
	}
	click(b.x+b.width()+2, b.y) // outside closes
	if m.overlay != nil {
		t.Fatal("a click outside left it open")
	}

	// Without a gateway address there is no link: a click copies the code.
	pairHome(t, "")
	d = newPairDialog(config.WebCfg{}, phone.PermFull)
	m.overlay = d
	b = d.render(*m)
	click(b.x+3, b.y+b.width()/2%len(b.lines))
	if lastClipboard != d.code {
		t.Fatalf("no link, click: %q", lastClipboard)
	}

	// Settings → Web: each command copies.
	s := &settings{}
	s.setTab(webTab)
	want := map[string]bool{"tailscale serve --bg http://127.0.0.1:8722": false, "conch web devices": false}
	for _, it := range s.items(m) {
		for cmd := range want {
			if strings.HasSuffix(ansi.Strip(it.label), cmd) {
				lastClipboard = ""
				a2Run(it.run(m))
				want[cmd] = lastClipboard == cmd
			}
		}
	}
	for cmd, copied := range want {
		if !copied {
			t.Errorf("%q not copied", cmd)
		}
	}
}
