package server_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/sshtest"
)

// The files of a host a terminal is logged in to (fs.ssh.v1), with ssh
// played by internal/sshtest.

func TestMain(m *testing.M) {
	sshtest.Main()
	os.Exit(m.Run())
}

// sshEnv is a server with a fake ssh on hand, and the host's home.
type sshEnv struct {
	c      *client.Client
	host   *sshtest.Host
	ssh    string
	remote string // the host's home
}

func newSSHEnv(t *testing.T) *sshEnv {
	t.Helper()
	h := sshtest.Install(t)
	env := &sshEnv{host: h, ssh: h.SSH, remote: h.Home}
	t.Setenv("CONCH_SOCKET", "")
	t.Setenv("CONCH_PANE_ID", "")
	env.c, _ = transferEnv(t)
	if missing := env.c.MissingCapabilities([]string{proto.CapFSSSH}); len(missing) > 0 {
		t.Fatalf("not advertised: %v", missing)
	}
	return env
}

// login starts a terminal running ssh with args, and waits until the
// server says where it is logged in.
func (env *sshEnv) login(t *testing.T, want string, args ...string) proto.PaneInfo {
	t.Helper()
	var info proto.PaneInfo
	cmd := append([]string{env.ssh}, args...)
	if err := call(t, env.c, proto.MethodPaneCreate, proto.PaneCreateParams{Command: cmd, Cwd: t.TempDir(), NoProject: true}, &info); err != nil {
		t.Fatal(err)
	}
	waitSSH(t, env.c, info.ID, want)
	return info
}

func waitSSH(t *testing.T, c *client.Client, id, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var list proto.PaneList
		if err := call(t, c, proto.MethodPaneList, nil, &list); err != nil {
			t.Fatal(err)
		}
		for _, p := range list.Panes {
			if p.ID == id && p.SSH == want {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("pane %s never said ssh %q: %+v", id, want, list.Panes)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (env *sshEnv) sessions(t *testing.T) int { return env.host.Sessions() }

func TestPaneSaysWhereSSHIsLoggedIn(t *testing.T) {
	env := newSSHEnv(t)
	pane := env.login(t, "me@box", "-p", "2222", "-L", "8080:x:80", "me@box")
	// A shell is logged in nowhere.
	shell := createCat(t, env.c, t.TempDir())
	var list proto.PaneList
	call(t, env.c, proto.MethodPaneList, nil, &list)
	for _, p := range list.Panes {
		if p.ID == shell.ID && p.SSH != "" {
			t.Fatalf("cat says ssh %q", p.SSH)
		}
	}
	// The session for files keeps the port and leaves the forward.
	var l proto.FSList
	if err := call(t, env.c, proto.MethodFSList, proto.FSListParams{SSH: pane.ID, Path: "~"}, &l); err != nil {
		t.Fatal(err)
	}
	b := env.host.Runs()
	var files string
	for _, line := range strings.Split(b, "\n") {
		if strings.HasSuffix(line, " sftp") {
			files = line
		}
	}
	if files != "-p 2222 -o BatchMode=yes -o ClearAllForwardings=yes -o RequestTTY=no -s -- me@box sftp" {
		t.Fatalf("ssh was run as:\n%s", b)
	}
}

func TestSSHFilesBrowse(t *testing.T) {
	env := newSSHEnv(t)
	pane := env.login(t, "box", "box")
	put(t, filepath.Join(env.remote, "notes.txt"), "hello from the host")
	put(t, filepath.Join(env.remote, "src", "main.go"), "package main")
	put(t, filepath.Join(env.remote, ".hidden"), "x")
	os.Symlink("notes.txt", filepath.Join(env.remote, "link"))
	os.Symlink("gone", filepath.Join(env.remote, "broken"))

	var l proto.FSList
	if err := call(t, env.c, proto.MethodFSList, proto.FSListParams{SSH: pane.ID, Path: "~"}, &l); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range l.Entries {
		names = append(names, e.Name)
	}
	if l.Path != env.remote || l.Home != env.remote || !slices.Equal(names, []string{"src", "broken", "link", "notes.txt"}) {
		t.Fatalf("listed %s (home %s): %v", l.Path, l.Home, names)
	}
	for _, e := range l.Entries {
		switch e.Name {
		case "src":
			if !e.Dir {
				t.Error("src is not a folder")
			}
		case "notes.txt":
			if e.Size != int64(len("hello from the host")) || e.ModTime.IsZero() {
				t.Errorf("notes.txt %+v", e)
			}
		case "link":
			if !e.Symlink || e.Broken {
				t.Errorf("link %+v", e)
			}
		case "broken":
			if !e.Symlink || !e.Broken {
				t.Errorf("broken %+v", e)
			}
		}
	}
	// Hidden files when asked, a folder by path, the folder above.
	call(t, env.c, proto.MethodFSList, proto.FSListParams{SSH: pane.ID, Path: "~", Hidden: true}, &l)
	if len(l.Entries) != 5 {
		t.Fatalf("with hidden: %+v", l.Entries)
	}
	call(t, env.c, proto.MethodFSList, proto.FSListParams{SSH: pane.ID, Path: "~/src"}, &l)
	if l.Path != filepath.Join(env.remote, "src") || l.Parent != env.remote || len(l.Entries) != 1 {
		t.Fatalf("src: %+v", l)
	}

	// A preview, from the start and further in.
	var r proto.FSReadResult
	if err := call(t, env.c, proto.MethodFSRead, proto.FSReadParams{SSH: pane.ID, Path: "~/notes.txt"}, &r); err != nil {
		t.Fatal(err)
	}
	if r.Data != "hello from the host" || r.Truncated || r.Path != filepath.Join(env.remote, "notes.txt") {
		t.Fatalf("read %+v", r)
	}
	call(t, env.c, proto.MethodFSRead, proto.FSReadParams{SSH: pane.ID, Path: "~/notes.txt", Offset: 6, Max: 4}, &r)
	if r.Data != "from" || !r.Truncated {
		t.Fatalf("read further in %+v", r)
	}
	wantErr(t, call(t, env.c, proto.MethodFSRead, proto.FSReadParams{SSH: pane.ID, Path: "~/src"}, &r), "not a regular file")

	// A folder made there.
	if err := call(t, env.c, proto.MethodFSMkdir, proto.FSMkdirParams{SSH: pane.ID, Path: "~/new"}, &l); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(filepath.Join(env.remote, "new")); err != nil || !st.IsDir() {
		t.Fatalf("not made: %v", err)
	}
	wantErr(t, call(t, env.c, proto.MethodFSMkdir, proto.FSMkdirParams{SSH: pane.ID, Path: "~/new"}, &l), "already exists")

	// One login for all of it.
	if n := env.sessions(t); n != 1 {
		t.Fatalf("%d sftp sessions", n)
	}
	// Nothing of this machine's leaked into it.
	if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), "new")); err == nil {
		t.Fatal("made here, not there")
	}
}

func TestSSHFilesCopyBothWays(t *testing.T) {
	env := newSSHEnv(t)
	pane := env.login(t, "box", "box")
	src, dest := t.TempDir(), t.TempDir()
	big := manyChunks(5)
	put(t, filepath.Join(src, "big.bin"), big)
	put(t, filepath.Join(src, "site", "index.html"), "<h1>hi</h1>")
	put(t, filepath.Join(src, "site", "bin", "run.sh"), "#!/bin/sh\n")
	os.Chmod(filepath.Join(src, "site", "bin", "run.sh"), 0o755)
	os.Symlink("index.html", filepath.Join(src, "site", "home.html"))
	os.Mkdir(filepath.Join(env.remote, "in"), 0o755)

	up := func(name string, replace bool) (string, error) {
		u, err := client.OpenUpload(env.c, filepath.Join(src, name))
		if err != nil {
			t.Fatal(err)
		}
		defer u.Close()
		return u.Into("~/in", replace).On(pane.ID).Run(transferCtx(t))
	}
	down := func(path string) (string, error) {
		d := client.NewDownload(env.c, path, dest, false).On(pane.ID)
		defer d.Close()
		return d.Run(transferCtx(t))
	}

	for _, name := range []string{"big.bin", "site"} {
		there, err := up(name, false)
		if err != nil || there != filepath.Join(env.remote, "in", name) {
			t.Fatalf("upload %s: %q %v", name, there, err)
		}
		back, err := down(there)
		if err != nil || back != filepath.Join(dest, name) {
			t.Fatalf("download %s: %q %v", name, back, err)
		}
	}
	if got(t, filepath.Join(env.remote, "in", "big.bin")) != big || got(t, filepath.Join(dest, "big.bin")) != big ||
		got(t, filepath.Join(dest, "site", "index.html")) != "<h1>hi</h1>" {
		t.Fatal("not the same bytes")
	}
	if st, err := os.Stat(filepath.Join(env.remote, "in", "site", "bin", "run.sh")); err != nil || st.Mode().Perm() != 0o755 {
		t.Fatalf("the script there: %v %v", st, err)
	}
	if target, err := os.Readlink(filepath.Join(dest, "site", "home.html")); err != nil || target != "index.html" {
		t.Fatalf("the link back: %q %v", target, err)
	}

	// Again: numbered, then written over.
	if there, err := up("big.bin", false); err != nil || there != filepath.Join(env.remote, "in", "big 2.bin") {
		t.Fatalf("again: %q %v", there, err)
	}
	put(t, filepath.Join(src, "big.bin"), "smaller")
	if there, err := up("big.bin", true); err != nil || there != filepath.Join(env.remote, "in", "big.bin") || got(t, there) != "smaller" {
		t.Fatalf("replaced: %q %v", there, err)
	}
	// Nothing half sent left on the host.
	entries, _ := os.ReadDir(filepath.Join(env.remote, "in"))
	for _, e := range entries {
		if strings.Contains(e.Name(), ".part") {
			t.Fatalf("left behind: %s", e.Name())
		}
	}
	// A missing folder there, and a missing file.
	u, _ := client.OpenUpload(env.c, filepath.Join(src, "big.bin"))
	defer u.Close()
	if _, err := u.Into("~/nope", false).On(pane.ID).Run(transferCtx(t)); err == nil || !strings.Contains(err.Error(), "doesn't exist") {
		t.Fatalf("to a missing folder: %v", err)
	}
	if _, err := down("~/nope"); err == nil || !strings.Contains(err.Error(), "doesn't exist") {
		t.Fatalf("a missing file: %v", err)
	}
}

func TestSSHFilesRefused(t *testing.T) {
	env := newSSHEnv(t)
	var l proto.FSList
	// A pane that is not ssh, and one that does not exist.
	cat := createCat(t, env.c, t.TempDir())
	wantErr(t, call(t, env.c, proto.MethodFSList, proto.FSListParams{SSH: cat.ID, Path: "~"}, &l), "not logged in anywhere with ssh")
	wantErr(t, call(t, env.c, proto.MethodFSList, proto.FSListParams{SSH: "p999", Path: "~"}, &l), "no pane")

	// A host that wants a password, and one without sftp: ssh's reason.
	denied := env.login(t, "denied", "denied")
	wantErr(t, call(t, env.c, proto.MethodFSList, proto.FSListParams{SSH: denied.ID, Path: "~"}, &l), "wants a password")
	nosftp := env.login(t, "nosftp", "nosftp")
	wantErr(t, call(t, env.c, proto.MethodFSList, proto.FSListParams{SSH: nosftp.ID, Path: "~"}, &l), "has no sftp server")

	// A checkout is not on the host; an upload there names its folder.
	box := env.login(t, "box", "box")
	wantErr(t, call(t, env.c, proto.MethodFSList, proto.FSListParams{SSH: box.ID, Root: "/x"}, &l), "a checkout is on this machine")
	var res proto.FSUploadResult
	wantErr(t, call(t, env.c, proto.MethodFSUpload, proto.FSUploadParams{SSH: box.ID, Name: "f", Size: 1, Data: []byte("x"), Final: true}, &res), "needs the folder there")
	if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), "f")); err == nil {
		t.Fatal("put here instead")
	}
}

// TestSSHFilesFollowTheLogin: a session is for one login. The terminal
// leaving the host ends it; logging in somewhere else makes another.
func TestSSHFilesFollowTheLogin(t *testing.T) {
	env := newSSHEnv(t)
	pane := env.login(t, "box", "box")
	var l proto.FSList
	if err := call(t, env.c, proto.MethodFSList, proto.FSListParams{SSH: pane.ID, Path: "~"}, &l); err != nil {
		t.Fatal(err)
	}
	if err := call(t, env.c, proto.MethodPaneClose, proto.PaneRef{ID: pane.ID}, nil); err != nil {
		t.Fatal(err)
	}
	wantErr(t, call(t, env.c, proto.MethodFSList, proto.FSListParams{SSH: pane.ID, Path: "~"}, &l), "no pane")

	// A second terminal, to another host.
	other := env.login(t, "me@other", "-l", "me", "me@other")
	if err := call(t, env.c, proto.MethodFSList, proto.FSListParams{SSH: other.ID, Path: "~"}, &l); err != nil {
		t.Fatal(err)
	}
	if n := env.sessions(t); n != 2 {
		t.Fatalf("%d sftp sessions, want one each", n)
	}
}
