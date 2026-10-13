package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/server"
)

// remoteFilesFixture is a1Fixture with a second machine, box, reached
// through a real server whose home is a fresh folder, and box's own Files
// open in the focused split and listed. Both ends are this computer, which
// the copying cannot tell.
func remoteFilesFixture(t *testing.T) (m *Model, home string, fv *filesView) {
	t.Helper()
	t.Setenv("CONCH_SOCKET", "")
	t.Setenv("CONCH_PANE_ID", "")
	home, _ = filepath.EvalSymlinks(t.TempDir())
	t.Setenv("HOME", home)
	os.MkdirAll(filepath.Join(home, "code"), 0o755)
	os.WriteFile(filepath.Join(home, "code", "main.go"), []byte("package main"), 0o644)
	os.WriteFile(filepath.Join(home, "notes.txt"), []byte("hello"), 0o644)
	os.WriteFile(filepath.Join(home, ".secret"), []byte("s"), 0o600)

	dir, err := os.MkdirTemp("", "x")
	if err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, "s.sock")
	srv := server.New(sock, dir)
	go srv.Run()
	t.Cleanup(func() { srv.Stop(); time.Sleep(50 * time.Millisecond); os.RemoveAll(dir) })
	var c *client.Client
	for i := 0; ; i++ {
		if c, err = client.Dial(sock, "test"); err == nil {
			break
		}
		if i == 200 {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Cleanup(func() { c.Close() })

	m, _ = a1Fixture(t, false)
	box := newMachine("box", "box", "box")
	box.c, box.server, box.state = c, c.Server, stateOnline
	m.machines = append(m.machines, box)
	m.rebuild()
	a1Open(t, m, machineFilesID("box"))
	m.focus = focusMain
	fv = m.tab().focused().files
	if fv == nil || !fv.browsing() {
		t.Fatalf("no machine explorer: %+v", fv)
	}
	pump(t, m, fv.load(m))
	return m, home, fv
}

// pump runs what a view asked for and hands the answers back, until there
// is nothing left to do: what a running program does between keys.
func pump(t *testing.T, m *Model, cmd tea.Cmd) {
	t.Helper()
	for i := 0; cmd != nil; i++ {
		if i == 1000 {
			t.Fatal("never settles")
		}
		var next []tea.Cmd
		for _, msg := range a2Run(cmd) {
			switch msg := msg.(type) {
			case copyStepMsg:
				next = append(next, m.copyStep(msg))
			case filesListMsg, filesReadMsg:
				next = append(next, m.filesReceive(msg))
			case errMsg:
				t.Fatalf("error: %v", msg.err)
			}
		}
		cmd = tea.Batch(next...)
	}
}

func filesKey(t *testing.T, m *Model, key string) {
	t.Helper()
	k := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	switch key {
	case "enter":
		k = tea.KeyMsg{Type: tea.KeyEnter}
	case "backspace":
		k = tea.KeyMsg{Type: tea.KeyBackspace}
	}
	pump(t, m, a1Key(t, m, k))
}

func fileText(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return string(b)
}

func TestMachineFilesBrowse(t *testing.T) {
	m, home, fv := remoteFilesFixture(t)
	if fv.root != home {
		t.Fatalf("root %q, want home %q", fv.root, home)
	}
	// Dotfiles are shown, as in a checkout, until `.` hides them.
	if got := strings.Join(filesPlainLines(fv), ","); got != "code,.secret,notes.txt" {
		t.Fatalf("home: %s", got)
	}
	out := strings.Join(fv.render(*m, 120, 12), "\n")
	if !strings.Contains(out, "Files  box") || !strings.Contains(ansi.Strip(out), " ~") || !strings.Contains(out, "U upload") {
		t.Fatalf("render:\n%s", out)
	}
	for _, l := range fv.render(*m, 120, 12) {
		if ansi.StringWidth(l) > 120 {
			t.Fatalf("line wider than the view: %q", l)
		}
	}
	// Into a folder, and a preview from it.
	filesSelect(t, fv, "code")
	filesKey(t, m, "enter")
	filesSelect(t, fv, "code/main.go")
	filesKey(t, m, "enter")
	if fv.prev.res == nil || fv.prev.res.Data != "package main" {
		t.Fatalf("preview: %+v", fv.prev)
	}
	// Up a folder, and up again with backspace; then home.
	filesKey(t, m, "-")
	if fv.root != filepath.Dir(home) || fv.dirs[""] == nil || fv.dirs[""].entries == nil {
		t.Fatalf("up: root %q", fv.root)
	}
	filesKey(t, m, "backspace")
	if fv.root != filepath.Dir(filepath.Dir(home)) {
		t.Fatalf("up again: root %q", fv.root)
	}
	filesKey(t, m, "~")
	if fv.root != home || strings.Join(filesPlainLines(fv), ",") != "code,.secret,notes.txt" {
		t.Fatalf("home: %q %v", fv.root, filesPlainLines(fv))
	}
	// What only a checkout has is said, not done.
	for _, k := range []string{"d", "w", "i"} {
		m.flash = ""
		filesKey(t, m, k)
		if !strings.Contains(m.flash, "not a checkout") {
			t.Fatalf("%s: %q", k, m.flash)
		}
	}
	// y copies the path there.
	filesSelect(t, fv, "notes.txt")
	a2Run(a1Key(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")}))
	if lastClipboard != filepath.Join(home, "notes.txt") {
		t.Fatalf("copied %q", lastClipboard)
	}
	// The status bar says what the keys are.
	if chip, items := m.statusHints(); !strings.Contains(chip, "FILES") || !hintsHave(items, "upload") || !hintsHave(items, "home") {
		t.Fatalf("hints: %q %+v", chip, items)
	}
	if got := m.viewLabel(m.tab().focused().view); got != "box" {
		t.Fatalf("tab label %q", got)
	}
}

func hintsHave(items []statusItem, text string) bool {
	for _, it := range items {
		if strings.Contains(ansi.Strip(it.text), text) {
			return true
		}
	}
	return false
}

func TestMachineFilesUpAtTheTop(t *testing.T) {
	m, _, fv := remoteFilesFixture(t)
	for i := 0; i < 64 && fv.root != "/"; i++ {
		filesKey(t, m, "-")
	}
	if fv.root != "/" {
		t.Fatalf("never reached /: %q", fv.root)
	}
	filesKey(t, m, "-")
	if fv.root != "/" || fv.err != "" {
		t.Fatalf("past /: %q %q", fv.root, fv.err)
	}
}

func TestMachineFilesDownload(t *testing.T) {
	m, home, fv := remoteFilesFixture(t)
	dl := downloadsDir()
	t.Cleanup(func() {
		entries, _ := os.ReadDir(dl)
		for _, e := range entries {
			os.RemoveAll(filepath.Join(dl, e.Name()))
		}
	})
	filesSelect(t, fv, "notes.txt")
	filesKey(t, m, "D")
	if fileText(t, filepath.Join(dl, "notes.txt")) != "hello" || !strings.Contains(m.flash, "downloaded notes.txt") || m.flashIsErr {
		t.Fatalf("download: %q", m.flash)
	}
	// Again: numbered, never over the first.
	os.WriteFile(filepath.Join(home, "notes.txt"), []byte("changed"), 0o644)
	filesKey(t, m, "D")
	if fileText(t, filepath.Join(dl, "notes 2.txt")) != "changed" || fileText(t, filepath.Join(dl, "notes.txt")) != "hello" {
		t.Fatalf("again: %q", m.flash)
	}
	// A folder comes whole.
	filesSelect(t, fv, "code")
	filesKey(t, m, "D")
	if fileText(t, filepath.Join(dl, "code", "main.go")) != "package main" {
		t.Fatalf("folder: %q", m.flash)
	}
	// Gone since it was listed: an error, and nothing half-written here.
	os.Remove(filepath.Join(home, "notes.txt"))
	filesSelect(t, fv, "notes.txt")
	filesKey(t, m, "D")
	if !m.flashIsErr || !strings.Contains(m.flash, "notes.txt") {
		t.Fatalf("gone: %q", m.flash)
	}
	entries, _ := os.ReadDir(dl)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".part") {
			t.Fatalf("left %s", e.Name())
		}
	}
}

func TestMachineFilesUpload(t *testing.T) {
	m, home, fv := remoteFilesFixture(t)
	src := t.TempDir()
	pem := filepath.Join(src, "ca.pem")
	os.WriteFile(pem, []byte("cert"), 0o600)
	site := filepath.Join(src, "site")
	os.MkdirAll(site, 0o755)
	os.WriteFile(filepath.Join(site, "index.html"), []byte("<h1>"), 0o644)

	// U asks for a path here, and sends it to the folder selected.
	filesSelect(t, fv, "code")
	filesKey(t, m, "U")
	d, ok := m.overlay.(*dialog)
	if !ok || !strings.Contains(d.title, "box") || !strings.Contains(strings.Join(d.text, " "), "~/code") {
		t.Fatalf("dialog: %+v", m.overlay)
	}
	m.overlay = nil
	pump(t, m, d.submit(m, []string{pem, ""}))
	if fileText(t, filepath.Join(home, "code", "ca.pem")) != "cert" || !strings.Contains(m.flash, "uploaded ca.pem to box") {
		t.Fatalf("upload: %q", m.flash)
	}
	// The folder is opened and listed again, so what arrived is seen.
	if got := strings.Join(filesPlainLines(fv), ","); !strings.Contains(got, "  ca.pem") {
		t.Fatalf("not shown: %s", got)
	}
	// Again: numbered; with Replace, written over.
	pump(t, m, d.submit(m, []string{pem, ""}))
	if fileText(t, filepath.Join(home, "code", "ca 2.pem")) != "cert" {
		t.Fatalf("again: %q", m.flash)
	}
	os.WriteFile(pem, []byte("new"), 0o600)
	pump(t, m, d.submit(m, []string{pem, "on"}))
	if fileText(t, filepath.Join(home, "code", "ca.pem")) != "new" {
		t.Fatalf("replace: %q", m.flash)
	}
	// A path here that isn't: said, nothing sent.
	m.flash = ""
	if cmd := d.submit(m, []string{filepath.Join(src, "nope"), ""}); cmd != nil || !m.flashIsErr {
		t.Fatalf("missing path: %q", m.flash)
	}
	if cmd := d.submit(m, []string{"  ", ""}); cmd != nil || !m.flashIsErr {
		t.Fatalf("empty path: %q", m.flash)
	}

	// Dropped on the view — a paste of paths — files and folders go to the
	// folder of the file selected, here the top.
	filesSelect(t, fv, "notes.txt")
	paste := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(shellQuote(pem, 0) + " " + shellQuote(site, 0)), Paste: true}
	pump(t, m, a1Key(t, m, paste))
	if fileText(t, filepath.Join(home, "ca.pem")) != "new" || fileText(t, filepath.Join(home, "site", "index.html")) != "<h1>" {
		t.Fatalf("drop: %q", m.flash)
	}
	// A paste that isn't paths is not typed anywhere.
	before := fv.filter
	pump(t, m, a1Key(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hello world"), Paste: true}))
	if fv.filter != before || fv.typing {
		t.Fatal("a paste became a filter")
	}
}

func TestUploadDialogExpandsHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for in, want := range map[string]string{"~": home, "~/a.txt": filepath.Join(home, "a.txt"), "/abs/x": "/abs/x"} {
		if got, err := expandHome(in); err != nil || got != want {
			t.Errorf("expandHome(%q) = %q %v, want %q", in, got, err, want)
		}
	}
	if _, err := expandHome(""); err == nil {
		t.Error("empty path")
	}
	if got, _ := expandHome("rel"); !filepath.IsAbs(got) {
		t.Errorf("relative: %q", got)
	}
}

func TestFilesCopyRefused(t *testing.T) {
	// This computer's checkout: there is nothing to copy.
	m, _, fv := filesFixture(t)
	filesSelect(t, fv, "README.md")
	for _, k := range []string{"D", "U"} {
		m.flash = ""
		if cmd := a1Key(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}); cmd != nil || !strings.Contains(m.flash, "on this computer") || m.overlay != nil {
			t.Fatalf("%s here: %q", k, m.flash)
		}
	}
	// No U or D in the hints here either.
	if _, items := m.statusHints(); hintsHave(items, "upload") {
		t.Fatal("upload offered on this computer")
	}

	// A machine whose conch can browse a checkout but not copy.
	c, _ := a1FakeClient(t, proto.CapFSFiles, proto.CapFSRead)
	old := newMachine("old", "old", "old")
	old.c, old.server, old.state = c, c.Server, stateOnline
	m.machines = append(m.machines, old)
	m.flash = ""
	if _, _, ok := m.copyClient(&filesView{machine: "old"}); ok || !strings.Contains(m.flash, "conch machine upgrade") {
		t.Fatalf("old: %q", m.flash)
	}
	// Browsing it says so in place of a listing.
	ofv := &filesView{machine: "old", dirs: map[string]*filesDir{}, open: map[string]bool{}}
	if cmd := ofv.load(m); cmd != nil || ofv.err != browseTooOld {
		t.Fatalf("browse old: %q", ofv.err)
	}
	// An offline machine.
	gone := newMachine("gone", "gone", "gone")
	m.machines = append(m.machines, gone)
	m.flash = ""
	if _, _, ok := m.copyClient(&filesView{machine: "gone"}); ok || !m.flashIsErr {
		t.Fatalf("offline: %q", m.flash)
	}
}

func TestMachineFilesTinyRender(t *testing.T) {
	m, _, fv := remoteFilesFixture(t)
	for _, size := range [][2]int{{1, 1}, {20, 5}, {39, 8}, {200, 50}} {
		for _, l := range fv.render(*m, size[0], size[1]) {
			if w := ansi.StringWidth(l); w > size[0] {
				t.Fatalf("%dx%d: line of %d: %q", size[0], size[1], w, l)
			}
		}
	}
}
