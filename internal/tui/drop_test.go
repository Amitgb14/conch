package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
)

// dropHome makes a home folder with files named like real drops.
func dropHome(t *testing.T) (home string, files map[string]string) {
	t.Helper()
	home, _ = filepath.EvalSymlinks(t.TempDir())
	t.Setenv("HOME", home)
	t.Setenv("CONCH_SOCKET", "")
	t.Setenv("CONCH_PANE_ID", "")
	desk := filepath.Join(home, "Desktop")
	os.MkdirAll(filepath.Join(desk, "folder"), 0o700)
	files = map[string]string{}
	for key, name := range map[string]string{
		"shot":   "Screenshot 2026-09-16 at 10.02.11 AM.png",
		"plain":  "plain.png",
		"quotes": `it's "quoted" (1).png`,
		"back":   `back\slash $dollar.png`,
		"empty":  "empty.txt",
	} {
		path := filepath.Join(desk, name)
		data := []byte("data of " + key)
		if key == "empty" {
			data = nil
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		files[key] = path
	}
	files["folder"] = filepath.Join(desk, "folder")
	return home, files
}

func backslashed(path string) string { return shellQuote(path, 0) }

func TestParseDroppedPaths(t *testing.T) {
	_, f := dropHome(t)
	shotURL := (&url.URL{Scheme: "file", Path: f["shot"]}).String()
	cases := []struct {
		name  string
		text  string
		want  []string
		quote byte
	}{
		// Terminal.app, iTerm2 and Ghostty escape with backslashes; Terminal.app adds a space.
		{"terminal.app", strings.ReplaceAll(f["shot"], " ", `\ `) + " ", []string{f["shot"]}, 0},
		{"iterm2", backslashed(f["quotes"]), []string{f["quotes"]}, 0},
		{"ghostty several", backslashed(f["shot"]) + " " + backslashed(f["plain"]) + " " + backslashed(f["back"]), []string{f["shot"], f["plain"], f["back"]}, 0},
		{"single quotes", "'" + f["shot"] + "' '" + f["plain"] + "'", []string{f["shot"], f["plain"]}, '\''},
		{"double quotes", `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(f["back"]) + `"`, []string{f["back"]}, '"'},
		{"file url", shotURL, []string{f["shot"]}, 0},
		{"file url localhost", "file://localhost" + (&url.URL{Path: f["plain"]}).EscapedPath(), []string{f["plain"]}, 0},
		{"newline separated", f["plain"] + "\n" + backslashed(f["shot"]) + "\n", []string{f["plain"], f["shot"]}, 0},
		{"empty file", f["empty"], []string{f["empty"]}, 0},
		{"dot segments", filepath.Join(filepath.Dir(f["plain"]), "folder") + "/../plain.png", []string{filepath.Join(filepath.Dir(f["plain"]), "folder") + "/../plain.png"}, 0},

		{"one missing", f["plain"] + " " + f["plain"] + ".gone", nil, 0},
		{"relative", "Desktop/plain.png", nil, 0},
		{"directory", f["folder"], nil, 0},
		{"plain text", "fix the bug in " + f["plain"], nil, 0},
		{"code", "func main() {}", nil, 0},
		{"empty", "", nil, 0},
		{"spaces", " \n\t ", nil, 0},
		{"unescaped space", f["shot"], nil, 0},
		{"unterminated quote", "'" + f["plain"], nil, 0},
		{"trailing backslash", f["plain"] + `\`, nil, 0},
		{"other host", "file://studio" + f["plain"], nil, 0},
		{"outside home and temp", "/etc/hosts", nil, 0},
	}
	for _, tc := range cases {
		got, quote := parseDroppedPaths(tc.text)
		if !slices.Equal(got, tc.want) || quote != tc.quote {
			t.Errorf("%s: %q → %q quote %q, want %q quote %q", tc.name, tc.text, got, quote, tc.want, tc.quote)
		}
	}
}

// What conch pastes on the remote machine splits back into the same paths.
func TestShellQuoteRoundTrip(t *testing.T) {
	for _, path := range []string{
		"/home/a/.config/conch/uploads/2026-09-16/ab12cd34/Screenshot 2026-09-16 at 10.02.11 AM.png",
		`/u/it's "quoted" (1).png`, `/u/back\slash $dollar & ;|<>*?[]{}!#~` + "`x`.png", "/u/plain-file_1.2+3,4@5%6:7=8.png", "/u/tab\there",
	} {
		for _, q := range []byte{0, '\'', '"'} {
			quoted := shellQuote(path, q)
			words, _, ok := shellWords(quoted)
			if !ok || len(words) != 1 || words[0] != path {
				t.Errorf("quote %q: %s splits into %q", q, quoted, words)
			}
		}
	}
	if got := shellQuote("/u/Shot 1.png", 0); got != `/u/Shot\ 1.png` {
		t.Fatalf("backslashes: %s", got)
	}
}

// dropFixture is the bash pane p3, focused, on a remote machine whose
// server stores uploads.
func dropFixture(t *testing.T, caps ...string) (*Model, *machine, *a1Peer) {
	t.Helper()
	m, mach := heldTyping(t)
	mach.target, mach.label = "amit@studio", "studio"
	m.cfg.Remote.UploadDrops, m.cfg.Remote.UploadMaxMB = true, 25
	c, peer := a1FakeClient(t, caps...)
	mach.c = c
	return m, mach, peer
}

// dropNothingTyped checks that nothing was typed into p3, once everything
// sent so far has arrived.
func dropNothingTyped(t *testing.T, mach *machine, peer *a1Peer) {
	t.Helper()
	if n := peer.count(t, mach.c, proto.MethodPaneSendText, ""); n != 0 {
		t.Fatalf("typed into the pane: %q", heldTyped(peer, "p3"))
	}
}

func paste(text string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(text), Paste: true}
}

// dropRun runs a drop's commands to the end, as the program would, and
// returns each status bar message along the way.
func dropRun(t *testing.T, m *Model, cmd tea.Cmd) []string {
	t.Helper()
	flashes := []string{m.flash}
	for cmd != nil {
		var next tea.Cmd
		for _, msg := range a2Run(cmd) {
			if _, ok := msg.(dropStepMsg); !ok {
				t.Fatalf("unexpected message %T", msg)
			}
			model, c := m.update(msg) // without Update's status bar timer
			*m, next = model.(Model), c
			if m.flash != flashes[len(flashes)-1] {
				flashes = append(flashes, m.flash)
			}
		}
		cmd = next
	}
	return flashes
}

// uploadChunks decodes the fs.upload requests a peer received.
func uploadChunks(peer *a1Peer) []proto.FSUploadParams {
	var out []proto.FSUploadParams
	for _, msg := range peer.snapshot() {
		if msg.Method == proto.MethodFSUpload {
			var p proto.FSUploadParams
			json.Unmarshal(msg.Params, &p)
			out = append(out, p)
		}
	}
	return out
}

func TestDropUploadsToRemotePane(t *testing.T) {
	_, f := dropHome(t)
	big := filepath.Join(filepath.Dir(f["plain"]), "big shot.png")
	bigData := bytes.Repeat([]byte("pixels!!"), proto.UploadChunkSize/4) // two chunks exactly
	os.WriteFile(big, bigData, 0o600)

	m, _, peer := dropFixture(t, "fs.upload.v1")
	remote := "/home/amit/.config/conch/uploads/2026-09-16/ab12cd34/big shot.png"
	peer.setResult(proto.MethodFSUpload, proto.FSUploadResult{Upload: "u1", Path: remote})

	cmd := a1Key(t, m, paste(backslashed(f["plain"])+" "+backslashed(big)+" "))
	if cmd == nil || len(heldTyped(peer, "p3")) != 0 {
		t.Fatal("the local paths were typed into the remote pane")
	}
	// Focus moves on while the files upload: the paste still goes to p3.
	m.focus = focusSidebar
	flashes := dropRun(t, m, cmd)

	heldWait(t, peer, "p3", `/home/amit/.config/conch/uploads/2026-09-16/ab12cd34/big\ shot.png /home/amit/.config/conch/uploads/2026-09-16/ab12cd34/big\ shot.png `)
	for _, msg := range peer.snapshot() {
		if msg.Method == proto.MethodPaneSendText && !strings.Contains(string(msg.Params), `"paste":true`) {
			t.Fatalf("not sent as a paste: %s", msg.Params)
		}
	}
	// The fake server gives every upload the same ID, so chunks belong to
	// the upload whose first chunk came last.
	chunks := uploadChunks(peer)
	var names []string
	var got []byte
	current := ""
	for _, c := range chunks {
		if c.Name != "" {
			current = c.Name
			names = append(names, fmt.Sprintf("%s/%d", c.Name, c.Size))
		}
		if current == "big shot.png" {
			got = append(got, c.Data...)
		}
	}
	if strings.Join(names, ",") != fmt.Sprintf("plain.png/13,big shot.png/%d", len(bigData)) || len(chunks) != 3 {
		t.Fatalf("chunks: %v (%d)", names, len(chunks))
	}
	if !bytes.Equal(got, bigData) {
		t.Fatalf("big shot.png: sent %d bytes, want %d", len(got), len(bigData))
	}
	want := []string{"uploading to studio · plain.png", "uploading to studio 50% · big shot.png", "uploaded 2 files to studio"}
	if !slices.Equal(flashes, want) {
		t.Fatalf("status: %q", flashes)
	}
}

func TestDropOneFileKeepsQuoting(t *testing.T) {
	_, f := dropHome(t)
	m, _, peer := dropFixture(t, "fs.upload.v1")
	peer.setResult(proto.MethodFSUpload, proto.FSUploadResult{Upload: "u1", Path: "/home/amit/up/it's.png"})
	flashes := dropRun(t, m, a1Key(t, m, paste("'"+f["shot"]+"'")))
	heldWait(t, peer, "p3", `'/home/amit/up/it'\''s.png'`)
	if flashes[len(flashes)-1] != "uploaded to studio · it's.png" {
		t.Fatalf("status: %q", flashes)
	}
}

// Pastes that aren't drops into a remote pane are typed as they are.
func TestDropLeavesOtherPastesAlone(t *testing.T) {
	_, f := dropHome(t)
	for _, tc := range []struct {
		name  string
		setup func(m *Model, mach *machine)
		text  string
	}{
		{"local pane", func(m *Model, mach *machine) { mach.target = "" }, f["plain"]},
		{"setting off", func(m *Model, mach *machine) { m.cfg.Remote.UploadDrops = false }, f["plain"]},
		{"not paths", func(*Model, *machine) {}, "hello " + f["plain"]},
		{"missing file", func(*Model, *machine) {}, f["plain"] + ".gone"},
	} {
		m, mach, peer := dropFixture(t, "fs.upload.v1")
		tc.setup(m, mach)
		if cmd := a1Key(t, m, paste(tc.text)); cmd != nil {
			t.Errorf("%s: a drop command", tc.name)
		}
		heldWait(t, peer, "p3", tc.text)
		if len(uploadChunks(peer)) != 0 || m.flash != "" {
			t.Errorf("%s: uploaded or flashed %q", tc.name, m.flash)
		}
	}
	// Typed runes that look like a path aren't a paste.
	m, _, peer := dropFixture(t, "fs.upload.v1")
	a1Key(t, m, runes(f["plain"]))
	heldWait(t, peer, "p3", f["plain"])
}

func TestDropOnOldServerPastesAsIs(t *testing.T) {
	_, f := dropHome(t)
	m, _, peer := dropFixture(t) // no fs.upload.v1
	if cmd := a1Key(t, m, paste(f["plain"])); cmd != nil {
		t.Fatal("a drop command for an old server")
	}
	heldWait(t, peer, "p3", f["plain"])
	if m.flash != "studio needs a conch update to take dropped files" || !m.flashIsErr {
		t.Fatalf("flash %q", m.flash)
	}
}

func TestDropFailures(t *testing.T) {
	_, f := dropHome(t)

	t.Run("upload error", func(t *testing.T) {
		m, _, peer := dropFixture(t, "fs.upload.v1")
		peer.setError(proto.MethodFSUpload, "disk full")
		flashes := dropRun(t, m, a1Key(t, m, paste(f["plain"]+" "+backslashed(f["shot"]))))
		if last := flashes[len(flashes)-1]; last != "plain.png: disk full · nothing pasted" || !m.flashIsErr {
			t.Fatalf("status: %q", flashes)
		}
		dropNothingTyped(t, m.machines[0], peer)
	})

	t.Run("too big", func(t *testing.T) {
		m, _, peer := dropFixture(t, "fs.upload.v1")
		m.cfg.Remote.UploadMaxMB = 1
		huge := filepath.Join(filepath.Dir(f["plain"]), "huge.mov")
		os.WriteFile(huge, make([]byte, 1<<20+1), 0o600)
		if cmd := a1Key(t, m, paste(f["plain"]+" "+huge)); cmd != nil {
			t.Fatal("started an upload over the limit")
		}
		if m.flash != "huge.mov is 1 MB; the limit is 1 MB (settings)" || !m.flashIsErr {
			t.Fatalf("flash %q", m.flash)
		}
		dropNothingTyped(t, m.machines[0], peer)
		if len(uploadChunks(peer)) != 0 {
			t.Fatal("uploaded")
		}
	})

	t.Run("exact limit", func(t *testing.T) {
		m, _, peer := dropFixture(t, "fs.upload.v1")
		m.cfg.Remote.UploadMaxMB = 1
		exact := filepath.Join(filepath.Dir(f["plain"]), "exact.bin")
		os.WriteFile(exact, make([]byte, 1<<20), 0o600)
		peer.setResult(proto.MethodFSUpload, proto.FSUploadResult{Upload: "u", Path: "/r/exact.bin"})
		dropRun(t, m, a1Key(t, m, paste(exact)))
		heldWait(t, peer, "p3", "/r/exact.bin")
	})

	t.Run("file gone before reading", func(t *testing.T) {
		// A screenshot thumbnail's file is deleted by macOS soon after the drop.
		m, _, peer := dropFixture(t, "fs.upload.v1")
		tmp := filepath.Join(filepath.Dir(f["plain"]), "thumb.png")
		os.WriteFile(tmp, []byte("x"), 0o600)
		cmd := a1Key(t, m, paste(tmp))
		os.Remove(tmp)
		flashes := dropRun(t, m, cmd)
		if last := flashes[len(flashes)-1]; last != "thumb.png is gone; drop it again from where it was saved · nothing pasted" {
			t.Fatalf("status: %q", flashes)
		}
		dropNothingTyped(t, m.machines[0], peer)
	})

	t.Run("pane closed during upload", func(t *testing.T) {
		m, mach, peer := dropFixture(t, "fs.upload.v1")
		peer.setResult(proto.MethodFSUpload, proto.FSUploadResult{Upload: "u", Path: "/r/plain.png"})
		cmd := a1Key(t, m, paste(f["plain"]))
		mach.panes = slices.DeleteFunc(mach.panes, func(p proto.PaneInfo) bool { return p.ID == "p3" })
		flashes := dropRun(t, m, cmd)
		if last := flashes[len(flashes)-1]; last != "uploaded to studio, but the pane has closed · nothing pasted" {
			t.Fatalf("status: %q", flashes)
		}
		dropNothingTyped(t, m.machines[0], peer)
	})

	t.Run("pane replaced during upload", func(t *testing.T) {
		m, mach, peer := dropFixture(t, "fs.upload.v1")
		peer.setResult(proto.MethodFSUpload, proto.FSUploadResult{Upload: "u", Path: "/r/plain.png"})
		cmd := a1Key(t, m, paste(f["plain"]))
		mach.pane("p3").Created = time.Now().Add(time.Hour) // a restarted server's p3
		flashes := dropRun(t, m, cmd)
		if !strings.Contains(flashes[len(flashes)-1], "the pane has closed") {
			t.Fatalf("status: %q", flashes)
		}
	})

	t.Run("machine offline at the end", func(t *testing.T) {
		m, mach, peer := dropFixture(t, "fs.upload.v1")
		peer.setResult(proto.MethodFSUpload, proto.FSUploadResult{Upload: "u", Path: "/r/plain.png"})
		cmd := a1Key(t, m, paste(f["plain"]))
		msgs := a2Run(cmd) // read
		c := mach.c
		model, next := m.update(msgs[0])
		*m = model.(Model)
		msgs = a2Run(next) // the only chunk
		mach.c, mach.state = nil, stateOffline
		model, _ = m.update(msgs[0])
		*m = model.(Model)
		if m.flash != "uploaded to studio, but it is offline · nothing pasted" {
			t.Fatalf("flash %q", m.flash)
		}
		mach.c = c
		dropNothingTyped(t, mach, peer)
	})

	t.Run("machine reconnecting at the drop", func(t *testing.T) {
		m, mach, peer := dropFixture(t, "fs.upload.v1")
		c := mach.c
		mach.c, mach.state = nil, stateConnecting
		if cmd := a1Key(t, m, paste(f["plain"])); cmd != nil {
			t.Fatal("upload started while reconnecting")
		}
		if m.flash != "studio is connecting · drop the files again once it's back" || len(mach.held) != 0 {
			t.Fatalf("flash %q held %d", m.flash, len(mach.held))
		}
		mach.c = c
		dropNothingTyped(t, mach, peer)
	})
}

func TestDropStatusFitsSmallTerminals(t *testing.T) {
	_, f := dropHome(t)
	m, _, peer := dropFixture(t, "fs.upload.v1")
	peer.setResult(proto.MethodFSUpload, proto.FSUploadResult{Upload: "u", Path: "/r/x.png"})
	cmd := a1Key(t, m, paste(backslashed(f["shot"])))
	for _, size := range [][2]int{{20, 5}, {1, 1}, {39, 10}, {200, 50}} {
		next, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		*m = next.(Model)
		for i, line := range strings.Split(m.View(), "\n") {
			if w := ansi.StringWidth(line); w > size[0] {
				t.Fatalf("%dx%d: line %d is %d wide: %q", size[0], size[1], i, w, ansi.Strip(line))
			}
		}
	}
	dropRun(t, m, cmd)
}

func TestNextUploadLimit(t *testing.T) {
	for limit, want := range map[int64]int{10 << 20: 25, 25 << 20: 50, 30 << 20: 50, 250 << 20: 10, 999 << 20: 10, 0: 10} {
		if got := nextUploadLimit(limit); got != want {
			t.Errorf("after %d MB: %d, want %d", limit>>20, got, want)
		}
	}
}

func TestDropSettings(t *testing.T) {
	a2Isolate(t)
	m := a2Model()
	s := &settings{}
	toggle := a2Item(t, s.agentItems(m), "Upload files dropped into remote panes")
	if toggle.on == nil || !*toggle.on {
		t.Fatal("uploads start on")
	}
	toggle.run(m)
	if m.cfg.Remote.UploadDrops {
		t.Fatal("toggle didn't turn uploads off")
	}
	limit := a2Item(t, s.agentItems(m), "Largest file to upload")
	if limit.detail != "25 MB · enter changes" {
		t.Fatalf("detail %q", limit.detail)
	}
	msg := limit.run(m)()
	if m.cfg.Remote.UploadMaxMB != 50 || msg != nil {
		t.Fatalf("limit %d, save %v", m.cfg.Remote.UploadMaxMB, msg)
	}
	if got, _ := config.Load(); got.Remote.UploadDrops || got.Remote.UploadMaxMB != 50 {
		t.Fatalf("saved %+v", got.Remote)
	}
}

// Found end to end: in a pane at 120 columns the status bar keeps 30
// cells of the message, and "uploading big 20MB.bin to busybox… 40%" never
// showed its percentage.
func TestDropProgressLeadsWithPercentage(t *testing.T) {
	_, f := dropHome(t)
	big := filepath.Join(filepath.Dir(f["plain"]), "big 20MB.bin")
	os.WriteFile(big, make([]byte, 4*proto.UploadChunkSize), 0o600)
	m, _, peer := dropFixture(t, "fs.upload.v1")
	peer.setResult(proto.MethodFSUpload, proto.FSUploadResult{Upload: "u", Path: "/r/big 20MB.bin"})
	m.machines[0].label = "busybox"
	m.width, m.height = 120, 40
	cmd := a1Key(t, m, paste(backslashed(big)))
	var bars []string
	for cmd != nil {
		var next tea.Cmd
		for _, msg := range a2Run(cmd) {
			model, c := m.update(msg)
			*m, next = model.(Model), c
			lines := strings.Split(m.View(), "\n")
			bars = append(bars, ansi.Strip(lines[len(lines)-1]))
		}
		cmd = next
	}
	if !slices.ContainsFunc(bars, func(b string) bool { return strings.Contains(b, "uploading to busybox 50%") }) {
		t.Fatalf("no percentage in the status bar: %q", bars)
	}
}
