package phone

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/proto"
)

// attachFile posts data to /api/attach as the app does: the file as the
// body, the pane and its name in the query.
func (p *fakePhone) attachFile(pane, name string, data []byte, out *AttachResponse) (int, *APIError) {
	p.t.Helper()
	q := url.Values{}
	if pane != "" {
		q.Set("pane", pane)
	}
	if name != "" {
		q.Set("name", name)
	}
	req, err := http.NewRequest("POST", p.base+"/api/attach?"+q.Encode(), bytes.NewReader(data))
	if err != nil {
		p.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "image/png")
	req.AddCookie(&http.Cookie{Name: CookieName, Value: p.token})
	if p.csrf != "" {
		req.Header.Set(CSRFHeader, p.csrf)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		p.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, decodeResult(p.t, res.StatusCode, b, out)
}

// termIn starts a terminal in dir on this computer.
func (f *fixture) termIn(dir string) string {
	f.t.Helper()
	var info proto.PaneInfo
	f.call(proto.MethodPaneCreate, proto.PaneCreateParams{
		Command: []string{"/bin/sh", "-c", "exec sleep 60"}, Cwd: dir, Cols: 80, Rows: 24}, &info)
	return info.ID
}

func mustAttach(t *testing.T, p *fakePhone, pane, name string, data []byte) AttachResponse {
	t.Helper()
	var got AttachResponse
	if status, e := p.attachFile(pane, name, data, &got); status != 200 {
		t.Fatalf("attach %s to %s: %d %+v", name, pane, status, e)
	}
	return got
}

func readExclude(t *testing.T, repo string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repo, ".git", "info", "exclude"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(b)
}

// An agent's attachment lands in its worktree, beside its work, and the
// repository — the shared one, not the worktree's own git folder — is told
// to leave it out of commits, once.
func TestAttachToAnAgent(t *testing.T) {
	f := newFixture(t)
	p := f.pair(PermFull)
	repo, feature := projectRepo(t, f.dir)
	var proj proto.ProjectInfo
	f.call(proto.MethodProjectAdd, proto.ProjectAddParams{Path: repo}, &proj)
	// git init writes a stock exclude file; one that ends without a
	// newline must not have the line run into its last.
	os.WriteFile(filepath.Join(repo, ".git", "info", "exclude"), []byte("# mine\n*.log"), 0o644)
	var agent proto.PaneInfo
	f.call(proto.MethodPaneCreate, proto.PaneCreateParams{Agent: "claude",
		Command: []string{"/bin/sh", "-c", "exec sleep 60"}, Cwd: feature, Cols: 80, Rows: 24}, &agent)
	waitFor(t, "the agent on its branch", func() bool {
		var list proto.PaneList
		f.call(proto.MethodPaneList, nil, &list)
		for _, pn := range list.Panes {
			if pn.ID == agent.ID && pn.Agent != nil && pn.Branch == "feature" && pn.ProjectID == proj.ID {
				return true
			}
		}
		return false
	})

	png := []byte("\x89PNG\r\n\x1a\nnot really a picture\x00\xff")
	got := mustAttach(t, p, "local:"+agent.ID, "settings.png", png)
	want := AttachResponse{Pane: "local:" + agent.ID, Path: filepath.Join(feature, ".conch", "attachments", "settings.png"),
		Rel: ".conch/attachments/settings.png", Size: int64(len(png))}
	if got != want {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	if b, err := os.ReadFile(want.Path); err != nil || !bytes.Equal(b, png) {
		t.Fatalf("the file: %q %v", b, err)
	}
	if ex := readExclude(t, repo); ex != "# mine\n*.log\n.conch/attachments/\n" {
		t.Fatalf("exclude: %q", ex)
	}
	// The worktree's own git folder has no exclude of its own to use.
	if _, err := os.Stat(filepath.Join(repo, ".git", "worktrees", "api-feature", "info", "exclude")); err == nil {
		t.Error("an exclude was written in the worktree's git folder")
	}
	cmd := exec.Command("git", "status", "--porcelain", "--untracked-files=all")
	cmd.Dir = feature
	if out, err := cmd.Output(); err != nil || strings.Contains(string(out), ".conch") {
		t.Fatalf("git sees the attachment: %s %v", out, err)
	}

	// Again under the same name: numbered, and the line not added twice.
	again := mustAttach(t, p, agent.ID, "settings.png", []byte("two"))
	if again.Rel != ".conch/attachments/settings-2.png" || again.Pane != "local:"+agent.ID {
		t.Fatalf("second: %+v", again)
	}
	if b, _ := os.ReadFile(want.Path); !bytes.Equal(b, png) {
		t.Fatal("the first file was written over")
	}
	if ex := readExclude(t, repo); strings.Count(ex, attachExclude) != 1 {
		t.Fatalf("exclude after two: %q", ex)
	}
}

// A terminal's attachment lands in its folder, wherever in a repository
// that is; a repository whose exclude already keeps all of .conch out is
// left alone; a folder in no repository gets no exclude anywhere.
func TestAttachToATerminal(t *testing.T) {
	f := newFixture(t)
	p := f.pair(PermFull)
	repo := filepath.Join(f.dir, "repo")
	sub := filepath.Join(repo, "docs", "guide")
	os.MkdirAll(sub, 0o755)
	gitIn(t, repo, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(repo, ".git", "info", "exclude"), []byte("/.conch/\n"), 0o644)
	term := f.termIn(sub)
	got := mustAttach(t, p, "local:"+term, "notes.txt", []byte("hello"))
	if got.Path != filepath.Join(sub, ".conch", "attachments", "notes.txt") || got.Rel != ".conch/attachments/notes.txt" || got.Size != 5 || got.Note != "" {
		t.Fatalf("got %+v", got)
	}
	if ex := readExclude(t, repo); ex != "/.conch/\n" {
		t.Fatalf("an exclude that already covers it was changed: %q", ex)
	}

	// No repository: the file still lands, and nothing else is made.
	plain := filepath.Join(f.dir, "plain")
	os.MkdirAll(plain, 0o755)
	got = mustAttach(t, p, "local:"+f.termIn(plain), "a.txt", []byte("x"))
	if got.Rel != ".conch/attachments/a.txt" {
		t.Fatalf("plain folder: %+v", got)
	}
	entries, _ := os.ReadDir(filepath.Join(plain, ".conch"))
	if len(entries) != 1 || entries[0].Name() != "attachments" {
		t.Fatalf(".conch in a plain folder holds %v", entries)
	}
}

// A repository with no exclude file at all — or no info folder — gets one.
func TestAttachMakesTheExcludeFile(t *testing.T) {
	f := newFixture(t)
	p := f.pair(PermFull)
	repo := filepath.Join(f.dir, "bare-ish")
	os.MkdirAll(repo, 0o755)
	gitIn(t, repo, "init", "-q", "-b", "main")
	os.RemoveAll(filepath.Join(repo, ".git", "info"))
	mustAttach(t, p, "local:"+f.termIn(repo), "x.bin", []byte{0})
	if ex := readExclude(t, repo); ex != ".conch/attachments/\n" {
		t.Fatalf("exclude: %q", ex)
	}
}

// What the phone calls a file is a name and nothing more: no folders, no
// dot files, nothing that hides itself, and the same name twice is
// numbered before its extension.
func TestAttachNames(t *testing.T) {
	long := strings.Repeat("é", 150)
	for in, want := range map[string]string{
		"settings.png":                  "settings.png",
		"../../etc/passwd":              "passwd",
		`C:\Users\me\photo.HEIC`:        "photo.HEIC",
		".bashrc":                       "bashrc",
		"...":                           "attachment",
		"":                              "attachment",
		"  /  ":                         "attachment",
		"a\x00b\nc\x1b[31m.txt":         "abc[31m.txt",
		"evil\u202egnp.exe":             "evilgnp.exe",
		"\xff\xfebad.txt":               "bad.txt",
		"  spaced name.pdf ":            "spaced name.pdf",
		long + ".jpeg":                  strings.Repeat("é", 95) + ".jpeg",
		long:                            strings.Repeat("é", 100),
		"x." + strings.Repeat("y", 200): "x." + strings.Repeat("y", 98),
	} {
		got := attachName(in)
		if got != want {
			t.Errorf("attachName(%q) = %q, want %q", in, got, want)
		}
	}
	entries := func(names ...string) []proto.FSEntry {
		var es []proto.FSEntry
		for _, n := range names {
			es = append(es, proto.FSEntry{Name: n})
		}
		return es
	}
	for _, c := range []struct {
		name  string
		taken []proto.FSEntry
		want  string
	}{
		{"a.png", nil, "a.png"},
		{"a.png", entries("b.png"), "a.png"},
		{"a.png", entries("a.png"), "a-2.png"},
		{"a.png", entries("A.PNG", "a-2.png"), "a-3.png"},
		{"notes", entries("notes"), "notes-2"},
		{"x.tar.gz", entries("x.tar.gz"), "x.tar-2.gz"},
	} {
		if got := freeName(c.name, c.taken); got != c.want {
			t.Errorf("freeName(%q, %v) = %q, want %q", c.name, c.taken, got, c.want)
		}
	}
}

// Through the gateway, a name with folders in it lands in the attachments
// folder all the same.
func TestAttachNameCannotLeaveTheFolder(t *testing.T) {
	f := newFixture(t)
	p := f.pair(PermFull)
	dir := filepath.Join(f.dir, "w")
	os.MkdirAll(dir, 0o755)
	term := "local:" + f.termIn(dir)
	got := mustAttach(t, p, term, "../../../escape.txt", []byte("x"))
	if got.Path != filepath.Join(dir, ".conch", "attachments", "escape.txt") {
		t.Fatalf("got %+v", got)
	}
	got = mustAttach(t, p, term, ".hidden", []byte("x"))
	if got.Rel != ".conch/attachments/hidden" {
		t.Fatalf("got %+v", got)
	}
}

// A .conch or attachments folder that is a symlink is not followed out of
// the pane's folder: the file goes to conch's uploads folder, and the
// answer says so.
func TestAttachDoesNotFollowSymlinks(t *testing.T) {
	f := newFixture(t)
	p := f.pair(PermFull)
	outside := filepath.Join(f.dir, "outside")
	os.MkdirAll(outside, 0o755)
	for _, link := range []string{".conch", ".conch/attachments"} {
		t.Run(link, func(t *testing.T) {
			dir, _ := os.MkdirTemp(f.dir, "w")
			os.MkdirAll(filepath.Join(dir, ".conch"), 0o755)
			os.Remove(filepath.Join(dir, link))
			if err := os.Symlink(outside, filepath.Join(dir, link)); err != nil {
				t.Fatal(err)
			}
			got := mustAttach(t, p, "local:"+f.termIn(dir), "a.txt", []byte("secret"))
			if got.Rel != "" || !strings.Contains(got.Note, "symlink") || strings.HasPrefix(got.Path, dir) ||
				!strings.HasPrefix(got.Path, filepath.Join(f.dir, "uploads")) {
				t.Fatalf("got %+v", got)
			}
			if b, err := os.ReadFile(got.Path); err != nil || string(b) != "secret" {
				t.Fatalf("the file in the uploads folder: %q %v", b, err)
			}
			if es, _ := os.ReadDir(outside); len(es) != 0 {
				t.Fatalf("written through the symlink: %v", es)
			}
		})
	}
}

// A pane whose folder is gone still takes the file, in conch's uploads
// folder, and the answer says why.
func TestAttachToAPaneWithNoFolder(t *testing.T) {
	f := newFixture(t)
	p := f.pair(PermFull)
	dir := filepath.Join(f.dir, "gone")
	os.MkdirAll(dir, 0o755)
	term := f.termIn(dir)
	os.RemoveAll(dir)
	got := mustAttach(t, p, "local:"+term, "a.txt", []byte("x"))
	if got.Rel != "" || got.Note == "" || !strings.HasPrefix(got.Path, filepath.Join(f.dir, "uploads")) || filepath.Base(got.Path) != "a.txt" {
		t.Fatalf("got %+v", got)
	}
}

// The body may be 25 MB, where every other route takes 64 KB; one byte
// more is refused with 413 before the file is saved anywhere.
func TestAttachSizeLimit(t *testing.T) {
	f := newFixture(t)
	p := f.pair(PermFull)
	dir := filepath.Join(f.dir, "w")
	os.MkdirAll(dir, 0o755)
	term := "local:" + f.termIn(dir)

	big := bytes.Repeat([]byte("0123456789abcdef"), maxAttach/16)
	got := mustAttach(t, p, term, "big.bin", big)
	if got.Size != maxAttach {
		t.Fatalf("got %+v", got)
	}
	if st, err := os.Stat(got.Path); err != nil || st.Size() != maxAttach {
		t.Fatalf("the file: %v %v", st, err)
	}
	status, e := p.attachFile(term, "bigger.bin", append(big, 'x'), nil)
	if status != 413 || e.Code != CodeBadRequest || !strings.Contains(e.Message, "25 MB") {
		t.Fatalf("one byte over: %d %+v", status, e)
	}
	if _, err := os.Stat(filepath.Join(dir, ".conch", "attachments", "bigger.bin")); err == nil {
		t.Fatal("the file over the limit was saved")
	}
	// Other routes keep their own limit.
	if status, e := p.post("/api/rename", RenameRequest{Pane: term, Name: strings.Repeat("x", maxBody)}, nil); status != 413 || e.Code != CodeBadRequest {
		t.Fatalf("rename with a big body: %d %+v", status, e)
	}
	// An empty file is still a file.
	if got := mustAttach(t, p, term, "empty.txt", nil); got.Size != 0 || got.Rel != ".conch/attachments/empty.txt" {
		t.Fatalf("empty: %+v", got)
	}
}

// Saving a file on a machine is the full permission's, and like every
// change needs the CSRF token; a pane is addressed as everywhere else.
func TestAttachRefusals(t *testing.T) {
	f := newFixture(t)
	dir := filepath.Join(f.dir, "w")
	os.MkdirAll(dir, 0o755)
	term := "local:" + f.termIn(dir)
	for _, perm := range []string{PermView, PermReply} {
		if status, e := f.pair(perm).attachFile(term, "a.txt", []byte("x"), nil); status != 403 || e.Code != CodeForbidden {
			t.Errorf("%s: %d %+v", perm, status, e)
		}
	}
	p := f.pair(PermFull)
	noCSRF := *p
	noCSRF.csrf = ""
	if status, e := noCSRF.attachFile(term, "a.txt", []byte("x"), nil); status != 403 || e.Code != CodeForbidden || !strings.Contains(e.Message, CSRFHeader) {
		t.Errorf("no csrf: %d %+v", status, e)
	}
	for _, c := range []struct {
		pane, name string
		status     int
		code       string
	}{
		{"", "a.txt", 400, CodeBadRequest},
		{term, "", 400, CodeBadRequest},
		{term, "   ", 400, CodeBadRequest},
		{"local:", "a.txt", 400, CodeBadRequest},
		{"Bad Machine:p1", "a.txt", 400, CodeBadRequest},
		{"local:p999", "a.txt", 404, CodeNotFound},
		{"nowhere:p1", "a.txt", 403, CodeForbidden}, // a machine this device was not given
	} {
		if status, e := p.attachFile(c.pane, c.name, []byte("x"), nil); status != c.status || e.Code != c.code {
			t.Errorf("pane %q name %q: %d %+v", c.pane, c.name, status, e)
		}
	}
	if status, b := p.do("GET", "/api/attach?pane="+term+"&name=a", nil); status != 405 {
		t.Errorf("GET: %d %s", status, b)
	}
	if es, _ := os.ReadDir(filepath.Join(dir, ".conch", "attachments")); len(es) != 0 {
		t.Fatalf("a refused attach saved %v", es)
	}
}

// A pane on another machine gets the file on that machine, through its own
// conch — and only for a device given that machine, whose conch can take
// a file into a folder.
func TestAttachOnAnotherMachine(t *testing.T) {
	f := newFixture(t)
	other := addMachine(t, f, "busybox", true)
	p := f.pair(PermFull)
	dir, _ := os.MkdirTemp(f.dir, "remote")
	gitIn(t, dir, "init", "-q", "-b", "main")
	var info proto.PaneInfo
	callOn(t, other, proto.MethodPaneCreate, proto.PaneCreateParams{
		Command: []string{"/bin/sh", "-c", "exec sleep 60"}, Cwd: dir, Cols: 80, Rows: 24}, &info)
	pane := "busybox:" + info.ID

	if status, e := p.attachFile(pane, "a.txt", []byte("x"), nil); status != 403 || e.Code != CodeForbidden {
		t.Fatalf("a machine the device wasn't given: %d %+v", status, e)
	}
	f.grant(p, "busybox")
	waitMachine(t, f, p, "busybox", MachineOnline)
	got := mustAttach(t, p, pane, "shot.jpg", []byte("jpeg"))
	want := AttachResponse{Pane: pane, Path: filepath.Join(dir, ".conch", "attachments", "shot.jpg"), Rel: ".conch/attachments/shot.jpg", Size: 4}
	if got != want {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	if b, err := os.ReadFile(want.Path); err != nil || string(b) != "jpeg" {
		t.Fatalf("the file: %q %v", b, err)
	}
	if ex := readExclude(t, dir); !strings.HasSuffix(ex, "\n.conch/attachments/\n") {
		t.Fatalf("exclude: %q", ex)
	}
	// The same pane ID on this computer is another pane — here, none.
	if status, e := p.attachFile("local:"+info.ID, "a.txt", []byte("x"), nil); status != 404 || e.Code != CodeNotFound {
		t.Fatalf("local:%s: %d %+v", info.ID, status, e)
	}
}

// A machine whose conch cannot put a file in a folder says so, by name,
// rather than leaving it somewhere the agent would not look.
func TestAttachOnAnOlderConch(t *testing.T) {
	f := newFixture(t)
	other := addMachine(t, f, "busybox", true, runningAnOlderConch(proto.CapFSTransfer))
	p := f.pair(PermFull)
	f.grant(p, "busybox")
	waitMachine(t, f, p, "busybox", MachineOnline)
	dir, _ := os.MkdirTemp(f.dir, "remote")
	var info proto.PaneInfo
	callOn(t, other, proto.MethodPaneCreate, proto.PaneCreateParams{
		Command: []string{"/bin/sh", "-c", "exec sleep 60"}, Cwd: dir, Cols: 80, Rows: 24}, &info)
	status, e := p.attachFile("busybox:"+info.ID, "a.txt", []byte("x"), nil)
	if status != 503 || e.Code != CodeServerUnavailable || !strings.Contains(e.Message, "busybox") {
		t.Fatalf("%d %+v", status, e)
	}
	if _, err := os.Stat(filepath.Join(dir, ".conch")); err == nil {
		t.Fatal("something was made there")
	}
}
