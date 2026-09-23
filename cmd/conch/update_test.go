package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/buildinfo"
	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
)

// a4Feed serves the releases feed and the latest-release redirect, without
// any downloadable asset: enough for conch update list.
func a4Feed(t *testing.T, latest string, versions ...string) {
	t.Helper()
	var b strings.Builder
	b.WriteString(`<feed>`)
	for _, v := range versions {
		fmt.Fprintf(&b, `<entry><link href="https://github.com/Amitgb14/conch/releases/tag/v%s"/></entry>`, v)
	}
	b.WriteString(`</feed>`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/releases.atom":
			w.Write([]byte(b.String()))
		case r.URL.Path == "/latest" && latest != "":
			w.Header().Set("Location", "https://github.com/Amitgb14/conch/releases/tag/v"+latest)
			w.WriteHeader(http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("CONCH_RELEASE_URL", srv.URL)
}

// a4Kept writes a kept binary and the note of the version to go back to,
// as an earlier update would have left them.
func a4Kept(t *testing.T, version, body string) string {
	t.Helper()
	dir := filepath.Join(os.Getenv("CONCH_HOME"), "versions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "conch-"+version)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "previous"), []byte(version+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestA4UpdateList(t *testing.T) {
	a4Env(t)
	a4Feed(t, "0.2.0", "0.1.0", "0.2.0", "0.1.9")
	a4Kept(t, "0.1.0", "an older conch")

	var err error
	out, _ := a4Capture(t, "", func() { err = runUpdate([]string{"list"}) })
	if err != nil {
		t.Fatal(err)
	}
	// Newest first, whatever order the feed gave.
	if i, j := strings.Index(out, "0.2.0"), strings.Index(out, "0.1.9"); i < 0 || j < i {
		t.Fatalf("not sorted newest first:\n%s", out)
	}
	for _, want := range []string{
		"0.2.0  (latest, what conch update installs)",
		"0.1.0  (kept, no download needed)",
		"running " + proto.Version, // a development build is in no release list
		"conch update rollback goes back to 0.1.0",
		"conch update VERSION installs any of these",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}

	// ls is the same command, and a feed that can't be reached is an error,
	// not an empty list.
	out2, _ := a4Capture(t, "", func() { err = runUpdate([]string{"ls"}) })
	if err != nil || out2 != out {
		t.Fatalf("ls: %q %v", out2, err)
	}
	t.Setenv("CONCH_RELEASE_URL", "http://127.0.0.1:1/gone")
	a4Capture(t, "", func() { err = runUpdate([]string{"list"}) })
	if err == nil || !strings.Contains(err.Error(), "list conch releases") {
		t.Fatalf("unreachable feed: %v", err)
	}
}

// TestA4UpdateListRunningRelease marks the running version in the list and
// leaves the rollback line out when there is nothing to go back to.
func TestA4UpdateListRunningRelease(t *testing.T) {
	a4Env(t)
	old := proto.Version
	proto.Version = "0.1.9"
	defer func() { proto.Version = old }()
	a4Feed(t, "0.2.0", "0.2.0", "0.1.9")

	var err error
	out, _ := a4Capture(t, "", func() { err = runUpdate([]string{"list"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "* 0.1.9  (running)") {
		t.Fatalf("running release not marked:\n%s", out)
	}
	if strings.Contains(out, "rollback goes back") || strings.Contains(out, "running 0.1.9\n") {
		t.Fatalf("unexpected footer:\n%s", out)
	}
}

func TestA4UpdateRollbackWithoutHistory(t *testing.T) {
	a4Env(t)
	var err error
	a4Capture(t, "", func() { err = runUpdate([]string{"rollback"}) })
	if err == nil || !strings.Contains(err.Error(), "no earlier version to go back to") {
		t.Fatalf("nothing recorded: %v", err)
	}

	// A note pointing at the running version: nothing to do, and the
	// binary is left alone.
	exe, _ := os.Executable()
	before, _ := os.Stat(exe)
	a4Kept(t, proto.Version, "this very conch")
	out, _ := a4Capture(t, "", func() { err = runUpdate([]string{"rollback"}) })
	if err != nil || !strings.Contains(out, "is already the version to go back to") {
		t.Fatalf("note names the running version: %q %v", out, err)
	}
	if after, _ := os.Stat(exe); after.ModTime() != before.ModTime() || after.Size() != before.Size() {
		t.Fatal("test binary modified")
	}
}

func TestA4UpdateAlreadyInstalled(t *testing.T) {
	a4Env(t) // CONCH_RELEASE_URL points nowhere: naming the running version asks nobody
	var err error
	for _, arg := range []string{proto.Version, "v" + proto.Version} {
		out, _ := a4Capture(t, "", func() { err = runUpdate([]string{arg}) })
		if err != nil || out != "conch "+proto.Version+" is already installed\n" {
			t.Fatalf("%s: %q %v", arg, out, err)
		}
	}
}

func TestA4UpdateBadArgs(t *testing.T) {
	a4Env(t)
	var err error
	for _, args := range [][]string{{"0.1.0", "0.2.0"}, {"-x"}, {"--force"}} {
		a4Capture(t, "", func() { err = runUpdate(args) })
		if err == nil || !strings.Contains(err.Error(), "usage: conch update") {
			t.Fatalf("args %v: %v", args, err)
		}
	}
	out, _ := a4Capture(t, "", func() { err = runUpdate([]string{"--help"}) })
	if err != nil || !strings.Contains(out, "usage: conch update") {
		t.Fatalf("help: %q %v", out, err)
	}
}

// TestA4UpdateMovesBack runs conch update rollback from a copy of the test
// binary: the kept copy goes back over it, with nothing downloaded.
func TestA4UpdateMovesBack(t *testing.T) {
	if testing.Short() {
		t.Skip("copies the test binary")
	}
	a4Env(t)
	exe, _ := os.Executable()
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	copyPath := filepath.Join(dir, "conch")
	if err := os.WriteFile(copyPath, data, 0o755); err != nil {
		t.Fatal(err)
	}
	kept := a4Kept(t, "0.0.9", "#!/bin/sh\necho conch 0.0.9\n")

	code, out, errOut := a4RunBinary(t, copyPath, "", "update", "rollback")
	if code != 0 {
		t.Fatalf("rollback exit %d: %q %q", code, out, errOut)
	}
	real, _ := filepath.EvalSymlinks(copyPath)
	if !strings.Contains(out, "moved back "+real+": "+proto.Version+" → 0.0.9") {
		t.Fatalf("output %q", out)
	}
	if !strings.Contains(errOut, "installing the conch 0.0.9 kept at "+kept) {
		t.Fatalf("stderr %q", errOut)
	}
	if b, _ := os.ReadFile(copyPath); string(b) != "#!/bin/sh\necho conch 0.0.9\n" {
		t.Fatalf("binary not replaced: %d bytes", len(b))
	}
	// The version it left is kept and noted, so the way forward is there too.
	note, _ := os.ReadFile(filepath.Join(os.Getenv("CONCH_HOME"), "versions", "previous"))
	if strings.TrimSpace(string(note)) != proto.Version {
		t.Fatalf("note %q", note)
	}
	back := filepath.Join(os.Getenv("CONCH_HOME"), "versions", "conch-"+proto.Version)
	if st, err := os.Stat(back); err != nil || st.Size() != int64(len(data)) {
		t.Fatalf("outgoing binary not kept: %v %v", st, err)
	}
}

// TestA4UpdateArgAliases: every spelling of the three sub-commands reaches
// the same place.
func TestA4UpdateArgAliases(t *testing.T) {
	a4Env(t)
	a4Feed(t, "0.2.0", "0.2.0", "0.1.9")
	var err error
	for _, arg := range []string{"list", "ls", "--list", "releases"} {
		out, _ := a4Capture(t, "", func() { err = runUpdate([]string{arg}) })
		if err != nil || !strings.Contains(out, "conch update VERSION installs any of these") {
			t.Fatalf("%s: %q %v", arg, out, err)
		}
	}
	// Nothing has been recorded to go back to, which is rollback's answer
	// whichever name it was called by.
	for _, arg := range []string{"rollback", "back", "--rollback", "downgrade"} {
		a4Capture(t, "", func() { err = runUpdate([]string{arg}) })
		if err == nil || !strings.Contains(err.Error(), "no earlier version to go back to") {
			t.Fatalf("%s: %v", arg, err)
		}
	}
	for _, arg := range []string{"-h", "--help", "help"} {
		out, _ := a4Capture(t, "", func() { err = runUpdate([]string{arg}) })
		if err != nil || out != updateUsage+"\n" {
			t.Fatalf("%s: %q %v", arg, out, err)
		}
	}
}

// TestA4UpdateLatestByEveryName: no argument, a blank one and "latest" all
// ask upstream for the newest release.
func TestA4UpdateLatestByEveryName(t *testing.T) {
	a4Env(t)
	a4Release(t, proto.Version, []byte("same"))
	var err error
	for _, args := range [][]string{nil, {}, {""}, {"  "}, {"latest"}} {
		out, _ := a4Capture(t, "", func() { err = runUpdate(args) })
		if err != nil || out != "conch "+proto.Version+" is the latest release\n" {
			t.Fatalf("args %v: %q %v", args, out, err)
		}
	}
}

// TestA4UpdateListWithoutALatest: the releases are listed even when the
// latest-release page can't be reached; none of them is marked latest.
func TestA4UpdateListWithoutALatest(t *testing.T) {
	a4Env(t)
	a4Feed(t, "", "0.2.0", "0.1.9") // /latest is a 404
	var err error
	out, _ := a4Capture(t, "", func() { err = runUpdate([]string{"list"}) })
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"  0.2.0\n", "  0.1.9\n", "running " + proto.Version} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "latest") {
		t.Fatalf("marked a latest with no latest to be had:\n%s", out)
	}
}

// TestA4UpdateMovesBackToANamedReleaseAndReloads: naming an older
// published release downloads it, reloads the running server onto it and
// says how to come forward again — not how to upgrade machines.
func TestA4UpdateMovesBackToANamedReleaseAndReloads(t *testing.T) {
	if testing.Short() {
		t.Skip("copies the test binary")
	}
	a4Env(t)
	exe, _ := os.Executable()
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	copyPath := filepath.Join(t.TempDir(), "conch")
	if err := os.WriteFile(copyPath, data, 0o755); err != nil {
		t.Fatal(err)
	}
	old := []byte("#!/bin/sh\necho conch 0.0.1\n")
	a4Release(t, "0.0.1", old)

	srv := startA4Server(t, config.SocketPath())
	var reloaded atomic.Bool
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		if msg.Method == proto.MethodServerReload {
			reloaded.Store(true)
		}
		return nil, nil
	})
	srv.setHello(func(n int) proto.HelloResult {
		h := currentHello(n)
		if reloaded.Load() {
			h.Started = h.Started.Add(time.Hour)
		}
		return h
	})

	code, out, errOut := a4RunBinary(t, copyPath, "", "update", "0.0.1")
	if code != 0 {
		t.Fatalf("exit %d: %q %q", code, out, errOut)
	}
	real, _ := filepath.EvalSymlinks(copyPath)
	for _, want := range []string{
		"moved back " + real + ": " + proto.Version + " → 0.0.1",
		"server reloaded onto it; panes keep running",
		"back on 0.0.1: conch update returns to the latest release",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in %q", want, out)
		}
	}
	if strings.Contains(out, "remote machines:") {
		t.Fatalf("the forward hint on the way back: %q", out)
	}
	// It was never kept here, so it came down from the release host.
	if !strings.Contains(errOut, "downloading conch 0.0.1 for "+buildinfo.Platform()) {
		t.Fatalf("stderr %q", errOut)
	}
	if b, _ := os.ReadFile(copyPath); string(b) != string(old) {
		t.Fatalf("binary not replaced: %d bytes", len(b))
	}
	// Coming forward needs no download: the outgoing binary was kept.
	kept := filepath.Join(os.Getenv("CONCH_HOME"), "versions", "conch-"+proto.Version)
	if st, err := os.Stat(kept); err != nil || st.Size() != int64(len(data)) {
		t.Fatalf("outgoing binary not kept: %v %v", st, err)
	}
}
