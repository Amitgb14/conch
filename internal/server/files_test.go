package server

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/Amitgb14/conch/internal/proto"
)

func filesGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func filesWrite(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// filesRepo is a project with committed, changed, untracked and ignored
// files, added to s.
func filesRepo(t *testing.T, s *Server, dir string) string {
	t.Helper()
	repo := filepath.Join(dir, "repo")
	a5GitRepo(t, repo)
	filesWrite(t, repo, ".gitignore", "node_modules/\n*.log\n")
	filesWrite(t, repo, "cmd/main.go", "package main\n")
	filesWrite(t, repo, "internal/tui/model.go", "package tui\n")
	filesWrite(t, repo, "README.md", "# hi\n")
	filesGit(t, repo, "add", "-A")
	filesGit(t, repo, "commit", "-q", "-m", "files")
	filesWrite(t, repo, "internal/tui/model.go", "package tui\n\nvar x = 1\n")
	filesWrite(t, repo, "internal/tui/app.tsx", "export {}\n")
	filesWrite(t, repo, "node_modules/pkg/index.js", "x\n")
	filesWrite(t, repo, "debug.log", "x\n")
	filesWrite(t, repo, ".env", "SECRET=1\n")
	if _, err := s.projects.add(repo, false); err != nil {
		t.Fatal(err)
	}
	return repo
}

func entryNames(l proto.FSList) string {
	var names []string
	for _, e := range l.Entries {
		names = append(names, e.Name)
	}
	return strings.Join(names, ",")
}

func findEntry(l proto.FSList, name string) proto.FSEntry {
	for _, e := range l.Entries {
		if e.Name == name {
			return e
		}
	}
	return proto.FSEntry{}
}

func TestListCheckoutFilesAndStatus(t *testing.T) {
	a5IsolateEnv(t)
	s, dir := a5Server(t)
	repo := filesRepo(t, s, dir)

	top, perr := s.listCheckout(proto.FSListParams{Root: repo, Files: true})
	if perr != nil {
		t.Fatal(perr)
	}
	// Folders first; dotfiles, .git and what git ignores left out.
	if got := entryNames(top); got != "cmd,internal,README.md" {
		t.Fatalf("top: %s", got)
	}
	if top.Path != repo || top.Parent != "" {
		t.Fatalf("top path %q parent %q", top.Path, top.Parent)
	}
	if e := findEntry(top, "internal"); !e.Dir || e.Status != "M" {
		t.Fatalf("internal carries its files' status: %+v", e)
	}
	if e := findEntry(top, "cmd"); e.Status != "" {
		t.Fatalf("clean folder has a status: %+v", e)
	}
	if e := findEntry(top, "README.md"); e.Dir || e.Size != 5 || e.ModTime.IsZero() || e.Status != "" {
		t.Fatalf("README: %+v", e)
	}

	tui, perr := s.listCheckout(proto.FSListParams{Root: repo, Path: "internal/tui", Files: true})
	if perr != nil {
		t.Fatal(perr)
	}
	if got := entryNames(tui); got != "app.tsx,model.go" {
		t.Fatalf("tui: %s", got)
	}
	if findEntry(tui, "model.go").Status != "M" || findEntry(tui, "app.tsx").Status != "?" {
		t.Fatalf("statuses: %+v", tui.Entries)
	}
	if tui.Parent != filepath.Join(repo, "internal") {
		t.Fatalf("parent %q", tui.Parent)
	}

	// Hidden and ignored on request, and marked.
	all, _ := s.listCheckout(proto.FSListParams{Root: repo, Files: true, Hidden: true, Ignored: true})
	if got := entryNames(all); got != ".git,cmd,internal,node_modules,.env,.gitignore,debug.log,README.md" {
		t.Fatalf("all: %s", got)
	}
	if !findEntry(all, "node_modules").Ignored || !findEntry(all, "debug.log").Ignored || findEntry(all, "cmd").Ignored {
		t.Fatalf("ignored marks: %+v", all.Entries)
	}
	inside, _ := s.listCheckout(proto.FSListParams{Root: repo, Path: "node_modules/pkg", Files: true, Ignored: true})
	if e := findEntry(inside, "index.js"); !e.Ignored {
		t.Fatalf("a file inside an ignored folder is ignored: %+v", inside.Entries)
	}
	// Without Files only folders come back, as for the picker.
	dirs, _ := s.listCheckout(proto.FSListParams{Root: repo})
	if got := entryNames(dirs); got != "cmd,internal" {
		t.Fatalf("folders only: %s", got)
	}
}

func TestListCheckoutStatusFollowsEdits(t *testing.T) {
	a5IsolateEnv(t)
	s, dir := a5Server(t)
	repo := filesRepo(t, s, dir)
	l, _ := s.listCheckout(proto.FSListParams{Root: repo, Files: true})
	if findEntry(l, "README.md").Status != "" {
		t.Fatal("README changed already")
	}
	filesWrite(t, repo, "README.md", "# changed\n")
	// The cached status stands until the watcher (or its age) drops it.
	s.files.drop(repo)
	l, _ = s.listCheckout(proto.FSListParams{Root: repo, Files: true})
	if findEntry(l, "README.md").Status != "M" {
		t.Fatalf("after an edit: %+v", l.Entries)
	}
}

func TestListCheckoutConfined(t *testing.T) {
	a5IsolateEnv(t)
	s, dir := a5Server(t)
	repo := filesRepo(t, s, dir)
	outside := filepath.Join(dir, "outside")
	filesWrite(t, outside, "secret.txt", "no\n")

	for _, lp := range []proto.FSListParams{
		{Root: outside, Files: true},          // not a project
		{Root: "relative", Files: true},       // not absolute
		{Root: repo, Path: "..", Files: true}, // climbs out
		{Root: repo, Path: "cmd/../../outside", Files: true},
		{Root: repo, Path: outside, Files: true}, // absolute
		{Root: repo, Path: "missing", Files: true},
		{Root: repo, Path: "README.md", Files: true}, // not a folder
	} {
		if _, perr := s.listCheckout(lp); perr == nil {
			t.Errorf("listed %+v", lp)
		}
	}
	// A symlink out of the checkout shows, but cannot be followed.
	if err := os.Symlink(outside, filepath.Join(repo, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(repo, "secret")); err != nil {
		t.Fatal(err)
	}
	s.files.drop(repo)
	top, _ := s.listCheckout(proto.FSListParams{Root: repo, Files: true})
	if e := findEntry(top, "escape"); !e.Symlink || !e.Dir {
		t.Fatalf("escape link: %+v", e)
	}
	if _, perr := s.listCheckout(proto.FSListParams{Root: repo, Path: "escape", Files: true}); perr == nil || !strings.Contains(perr.Message, "outside") {
		t.Fatalf("followed a link out: %v", perr)
	}
	if _, perr := s.readFile(proto.FSReadParams{Root: repo, Path: "secret"}); perr == nil || !strings.Contains(perr.Message, "outside") {
		t.Fatalf("read through a link out: %v", perr)
	}
	// fs.list without a root still refuses files.
	if _, perr := s.dispatch(nil, proto.Message{Method: proto.MethodFSList, Params: proto.Marshal(proto.FSListParams{Path: repo, Files: true})}); perr == nil {
		t.Fatal("files without a root")
	}
}

func TestListCheckoutSymlinksAndWorktrees(t *testing.T) {
	a5IsolateEnv(t)
	s, dir := a5Server(t)
	repo := filesRepo(t, s, dir)
	os.Symlink("README.md", filepath.Join(repo, "readme-link"))
	os.Symlink("cmd", filepath.Join(repo, "cmd-link"))
	os.Symlink("nowhere", filepath.Join(repo, "dangling"))
	top, perr := s.listCheckout(proto.FSListParams{Root: repo, Files: true})
	if perr != nil {
		t.Fatal(perr)
	}
	if e := findEntry(top, "readme-link"); !e.Symlink || e.Dir || e.Broken || e.Size != 5 {
		t.Fatalf("file link: %+v", e)
	}
	if e := findEntry(top, "cmd-link"); !e.Symlink || !e.Dir {
		t.Fatalf("folder link: %+v", e)
	}
	if e := findEntry(top, "dangling"); !e.Symlink || !e.Broken || e.Dir {
		t.Fatalf("broken link: %+v", e)
	}
	in, perr := s.listCheckout(proto.FSListParams{Root: repo, Path: "cmd-link", Files: true})
	if perr != nil || entryNames(in) != "main.go" {
		t.Fatalf("through a link inside: %s %v", entryNames(in), perr)
	}
	if r, perr := s.readFile(proto.FSReadParams{Root: repo, Path: "readme-link"}); perr != nil || r.Data != "# hi\n" {
		t.Fatalf("read a link: %+v %v", r, perr)
	}
	if _, perr := s.readFile(proto.FSReadParams{Root: repo, Path: "dangling"}); perr == nil {
		t.Fatal("read a broken link")
	}

	// A worktree of the project is a root of its own.
	wt := filepath.Join(dir, "wt")
	filesGit(t, repo, "worktree", "add", "-q", "-b", "feature", wt)
	p := s.projects.containing(repo)
	s.projects.refresh(p, "")
	wl, perr := s.listCheckout(proto.FSListParams{Root: wt, Files: true})
	if perr != nil || !strings.Contains(entryNames(wl), "README.md") {
		t.Fatalf("worktree: %s %v", entryNames(wl), perr)
	}
}

func TestListCheckoutEdges(t *testing.T) {
	a5IsolateEnv(t)
	s, dir := a5Server(t)
	plain := filepath.Join(dir, "plain")
	os.MkdirAll(filepath.Join(plain, "empty"), 0o755)
	if _, err := s.projects.add(plain, false); err != nil {
		t.Fatal(err)
	}
	// A folder that isn't a git checkout lists with no statuses.
	l, perr := s.listCheckout(proto.FSListParams{Root: plain, Files: true})
	if perr != nil || entryNames(l) != "empty" || l.Entries[0].Status != "" {
		t.Fatalf("plain: %+v %v", l, perr)
	}
	e, perr := s.listCheckout(proto.FSListParams{Root: plain, Path: "empty", Files: true})
	if perr != nil || len(e.Entries) != 0 || e.Entries == nil {
		t.Fatalf("empty folder: %+v %v", e, perr)
	}
	// Over the cap it says so.
	big := filepath.Join(plain, "big")
	os.MkdirAll(big, 0o755)
	for i := 0; i <= maxListEntries; i++ {
		os.WriteFile(filepath.Join(big, fmt.Sprintf("f%05d", i)), nil, 0o644)
	}
	b, perr := s.listCheckout(proto.FSListParams{Root: plain, Path: "big", Files: true})
	if perr != nil || !b.Truncated || len(b.Entries) != maxListEntries {
		t.Fatalf("big: %d truncated %v %v", len(b.Entries), b.Truncated, perr)
	}
	// A folder that can't be read says why.
	if os.Getuid() != 0 {
		locked := filepath.Join(plain, "locked")
		os.MkdirAll(locked, 0o000)
		t.Cleanup(func() { os.Chmod(locked, 0o755) })
		if _, perr := s.listCheckout(proto.FSListParams{Root: plain, Path: "locked", Files: true}); perr == nil {
			t.Fatal("listed a folder without permission")
		}
	}
}

func TestReadFile(t *testing.T) {
	a5IsolateEnv(t)
	s, dir := a5Server(t)
	repo := filesRepo(t, s, dir)

	r, perr := s.readFile(proto.FSReadParams{Root: repo, Path: "cmd/main.go"})
	if perr != nil || r.Data != "package main\n" || r.Binary || r.Truncated || r.Size != 13 || r.Path != filepath.Join(repo, "cmd", "main.go") {
		t.Fatalf("read: %+v %v", r, perr)
	}
	// Zero bytes is text with nothing in it.
	filesWrite(t, repo, "empty.txt", "")
	if r, perr := s.readFile(proto.FSReadParams{Root: repo, Path: "empty.txt"}); perr != nil || r.Binary || r.Data != "" || r.Size != 0 {
		t.Fatalf("empty: %+v %v", r, perr)
	}
	// Binary is described, not sent.
	filesWrite(t, repo, "logo.png", "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	if r, perr := s.readFile(proto.FSReadParams{Root: repo, Path: "logo.png"}); perr != nil || !r.Binary || r.Data != "" || r.MIME != "image/png" {
		t.Fatalf("binary: %+v %v", r, perr)
	}
	filesWrite(t, repo, "latin1.txt", "caf\xe9\n")
	if r, _ := s.readFile(proto.FSReadParams{Root: repo, Path: "latin1.txt"}); !r.Binary {
		t.Fatalf("invalid UTF-8 is not text: %+v", r)
	}
	// Over the cap: the start, and truncated — a character cut in two at
	// the end doesn't make it binary.
	big := strings.Repeat("é", proto.FSReadMax) // two bytes each
	filesWrite(t, repo, "big.txt", "x"+big)
	r, perr = s.readFile(proto.FSReadParams{Root: repo, Path: "big.txt"})
	if perr != nil || !r.Truncated || r.Binary || len(r.Data) != proto.FSReadMax {
		t.Fatalf("big: len %d truncated %v binary %v %v", len(r.Data), r.Truncated, r.Binary, perr)
	}
	r, _ = s.readFile(proto.FSReadParams{Root: repo, Path: "big.txt", Max: 10 << 20})
	if len(r.Data) != proto.FSReadMax {
		t.Fatalf("max is capped: %d", len(r.Data))
	}
	r, _ = s.readFile(proto.FSReadParams{Root: repo, Path: "cmd/main.go", Offset: 8, Max: 4})
	if r.Data != "main" || !r.Truncated {
		t.Fatalf("offset: %+v", r)
	}
	r, _ = s.readFile(proto.FSReadParams{Root: repo, Path: "cmd/main.go", Offset: 100})
	if r.Data != "" || r.Truncated {
		t.Fatalf("past the end: %+v", r)
	}

	for _, rp := range []proto.FSReadParams{
		{Root: repo, Path: ""},
		{Root: repo, Path: "cmd"},
		{Root: repo, Path: "missing.go"},
		{Root: repo, Path: "../repo/README.md"},
		{Root: repo, Path: "/etc/hosts"},
		{Root: repo, Path: "README.md", Offset: -1},
		{Root: filepath.Join(dir, "elsewhere"), Path: "README.md"},
	} {
		if _, perr := s.readFile(rp); perr == nil {
			t.Errorf("read %+v", rp)
		}
	}
	// A fifo would block a read forever; it is refused.
	fifo := filepath.Join(repo, "pipe")
	if err := syscall.Mkfifo(fifo, 0o644); err == nil {
		if _, perr := s.readFile(proto.FSReadParams{Root: repo, Path: "pipe"}); perr == nil {
			t.Fatal("read a fifo")
		}
	}
	// A file that goes between the listing and the read.
	os.Remove(filepath.Join(repo, "README.md"))
	if _, perr := s.readFile(proto.FSReadParams{Root: repo, Path: "README.md"}); perr == nil {
		t.Fatal("read a vanished file")
	}
}

func TestIsBinary(t *testing.T) {
	for _, c := range []struct {
		data      string
		truncated bool
		want      bool
	}{
		{"", false, false},
		{"hello\n", false, false},
		{"a\x00b", false, true},
		{"caf\xc3", false, true}, // cut short in a whole file: not text
		{"caf\xc3", true, false}, // cut short by the cap: text
		{"\xff\xfe\xfd\xfc\xfbabc", true, true},
	} {
		if got := isBinary([]byte(c.data), c.truncated); got != c.want {
			t.Errorf("isBinary(%q, %v) = %v", c.data, c.truncated, got)
		}
	}
}
