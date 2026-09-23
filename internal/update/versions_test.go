package update

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/buildinfo"
	"github.com/Amitgb14/conch/internal/config"
)

// a3Exe writes a stand-in executable holding body.
func a3Exe(t *testing.T, body string) string {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "bin", "conch")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return exe
}

// a3Feed publishes a releases feed listing versions, as GitHub's does.
func (rs *a3ReleaseServer) a3Feed(versions ...string) {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><feed xmlns="http://www.w3.org/2005/Atom">`)
	for _, v := range versions {
		fmt.Fprintf(&b, `<entry><title>v%s</title><link rel="alternate" type="text/html" href="https://example.invalid/Amitgb14/conch/releases/tag/v%s"/></entry>`, v, v)
	}
	b.WriteString(`</feed>`)
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.assets["/releases.atom"] = []byte(b.String())
}

// TestA3MoveBackAndForth walks the round trip: an update keeps the outgoing
// binary and notes it, and the move back needs no download.
func TestA3MoveBackAndForth(t *testing.T) {
	a3Isolate(t)
	rs := a3NewReleaseServer(t)
	a3SetVersion(t, "0.2.0")
	newBin := []byte("#!/bin/sh\necho 0.3.0\n")
	rs.publish(t, "0.3.0", a3Archive(t, map[string][]byte{"conch_0.3.0/conch": newBin}), "")

	exe := a3Exe(t, "old 0.2.0 binary")
	if err := InstallRelease(context.Background(), "v0.3.0", exe); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exe); string(b) != string(newBin) {
		t.Fatalf("installed %q", b)
	}
	kept, ok := Kept("0.2.0")
	if !ok {
		t.Fatal("the replaced binary was not kept")
	}
	if b, _ := os.ReadFile(kept); string(b) != "old 0.2.0 binary" {
		t.Fatalf("kept %q", b)
	}
	if got := Previous(); got != "0.2.0" {
		t.Fatalf("Previous() = %q", got)
	}

	// Now running the new one: going back uses the kept copy, with no
	// request for 0.2.0 (it was never even published here).
	a3SetVersion(t, "0.3.0")
	before := len(rs.requests)
	if err := InstallRelease(context.Background(), Previous(), exe); err != nil {
		t.Fatalf("move back: %v", err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "old 0.2.0 binary" {
		t.Fatalf("after rollback %q", b)
	}
	rs.mu.Lock()
	reqs := append([]string(nil), rs.requests[before:]...)
	rs.mu.Unlock()
	if len(reqs) != 0 {
		t.Fatalf("rollback downloaded: %v", reqs)
	}
	// And the version it left is the one to go back to now.
	if got := Previous(); got != "0.3.0" {
		t.Fatalf("Previous() after rollback = %q", got)
	}
	if _, ok := Kept("0.3.0"); !ok {
		t.Fatal("0.3.0 was not kept on the way back")
	}
	if fi, err := os.Stat(exe); err != nil || fi.Mode().Perm() != 0o755 {
		t.Fatalf("mode %v %v", fi, err)
	}
}

func TestA3PreviousEdgeCases(t *testing.T) {
	a3Isolate(t)
	a3SetVersion(t, "0.2.0")
	if got := Previous(); got != "" {
		t.Fatalf("nothing recorded: %q", got)
	}
	// Whitespace and a "v" are tolerated in a hand-written note.
	os.MkdirAll(versionsDir(), 0o700)
	os.WriteFile(previousFile(), []byte("  v0.1.9  \n"), 0o600)
	if got := Previous(); got != "0.1.9" {
		t.Fatalf("Previous() = %q", got)
	}
	setPrevious("")
	if got := Previous(); got != "0.1.9" {
		t.Fatalf("empty version overwrote the note: %q", got)
	}

	// A local build nobody kept can't be fetched, and says so instead of
	// asking GitHub for a version it never published.
	err := InstallRelease(context.Background(), "0.1.0-dev", a3Exe(t, "x"))
	if err == nil || !strings.Contains(err.Error(), "not a published release and no copy of it was kept") {
		t.Fatalf("dev build without a kept copy: %v", err)
	}
	// With a copy kept, the same move works offline.
	exe := a3Exe(t, "current")
	if err := os.WriteFile(keptName("0.1.0-dev"), []byte("the old dev build"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := InstallRelease(context.Background(), "0.1.0-dev", exe); err != nil {
		t.Fatalf("move back to a kept dev build: %v", err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "the old dev build" {
		t.Fatalf("exe = %q", b)
	}
}

func TestA3KeptEdgeCases(t *testing.T) {
	a3Isolate(t)
	if _, ok := Kept(""); ok {
		t.Fatal("empty version reported as kept")
	}
	if _, ok := Kept("9.9.9"); ok {
		t.Fatal("kept without a versions directory")
	}
	os.MkdirAll(versionsDir(), 0o700)
	// An empty file is not a binary.
	os.WriteFile(keptName("9.9.9"), nil, 0o755)
	if _, ok := Kept("9.9.9"); ok {
		t.Fatal("empty file reported as kept")
	}
	// A directory of that name isn't either.
	os.MkdirAll(keptName("8.8.8"), 0o700)
	if _, ok := Kept("8.8.8"); ok {
		t.Fatal("directory reported as kept")
	}
	// Version strings never escape the versions directory.
	for _, v := range []string{"../../etc/passwd", "v1.2.3+build/x", "1.0.0"} {
		path := keptName(v)
		if dir := filepath.Dir(path); dir != versionsDir() {
			t.Fatalf("version %q kept at %q", v, path)
		}
	}
	if got := filepath.Base(keptName("v1.2.3+build/x")); got != "conch-1.2.3+build_x" {
		t.Fatalf("kept name %q", got)
	}
	if got := keptName("  "); got != "" {
		t.Fatalf("blank version: %q", got)
	}
	// Nothing to copy: keep is silent and leaves no file behind.
	keep(filepath.Join(t.TempDir(), "missing"), "7.7.7")
	if _, ok := Kept("7.7.7"); ok {
		t.Fatal("kept a binary that was not there")
	}
	keep(a3Exe(t, "body"), "")
	if ents, _ := os.ReadDir(versionsDir()); len(ents) != 2 {
		t.Fatalf("versions dir holds %d entries, want the empty file and the directory", len(ents))
	}
}

func TestA3PruneKeepsTheNewest(t *testing.T) {
	a3Isolate(t)
	os.MkdirAll(versionsDir(), 0o700)
	now := time.Now()
	versions := []string{"0.1.0", "0.2.0", "0.3.0", "0.4.0", "0.5.0"}
	for i, v := range versions {
		os.WriteFile(keptName(v), []byte("binary "+v), 0o755)
		os.Chtimes(keptName(v), now, now.Add(time.Duration(i)*time.Minute))
	}
	// Files that aren't kept binaries are left alone.
	os.WriteFile(previousFile(), []byte("0.5.0\n"), 0o600)
	prune()
	for _, v := range versions[:len(versions)-keptLimit] {
		if _, ok := Kept(v); ok {
			t.Errorf("%s was not pruned", v)
		}
	}
	for _, v := range versions[len(versions)-keptLimit:] {
		if _, ok := Kept(v); !ok {
			t.Errorf("%s was pruned", v)
		}
	}
	if got := Previous(); got != "0.5.0" {
		t.Fatalf("the note was pruned: %q", got)
	}
	// keep prunes as it goes, so the directory never grows past the limit.
	exe := a3Exe(t, "body")
	for _, v := range []string{"1.0.0", "1.1.0", "1.2.0", "1.3.0"} {
		keep(exe, v)
	}
	kept := 0
	ents, _ := os.ReadDir(versionsDir())
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), "conch-") {
			kept++
		}
	}
	if kept != keptLimit {
		t.Fatalf("%d kept binaries, want %d", kept, keptLimit)
	}
	// prune on a missing directory is a no-op, not a panic.
	os.RemoveAll(versionsDir())
	prune()
}

func TestA3Releases(t *testing.T) {
	a3Isolate(t)
	rs := a3NewReleaseServer(t)

	// No feed there at all.
	if _, err := Releases(context.Background()); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("missing feed: %v", err)
	}
	rs.a3Feed() // published nothing yet
	if _, err := Releases(context.Background()); err == nil || !strings.Contains(err.Error(), "no releases found") {
		t.Fatalf("empty feed: %v", err)
	}
	rs.a3Feed("0.2.0", "0.10.0", "0.2.0", "0.9.0", "1.0.0-rc.1")
	got, err := Releases(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"1.0.0-rc.1", "0.10.0", "0.9.0", "0.2.0"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("Releases() = %v, want %v", got, want)
	}

	rs.Close()
	if _, err := Releases(context.Background()); err == nil || !strings.Contains(err.Error(), "list conch releases") {
		t.Fatalf("server down: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Releases(ctx); err == nil {
		t.Fatal("cancelled context: no error")
	}
}

// TestA3VersionsDirFollowsConchHome keeps kept binaries out of the user's
// real config directory.
func TestA3VersionsDirFollowsConchHome(t *testing.T) {
	a3Isolate(t)
	if want := filepath.Join(config.Dir(), "versions"); versionsDir() != want {
		t.Fatalf("versionsDir() = %q, want %q", versionsDir(), want)
	}
	if !strings.HasPrefix(versionsDir(), os.Getenv("CONCH_HOME")) {
		t.Fatalf("versionsDir() = %q, outside CONCH_HOME", versionsDir())
	}
	_ = buildinfo.Platform()
}

// TestA3KeepSurvivesAnUnwritableCache: nothing can be kept when the
// versions folder can't be made, and an update still goes through.
func TestA3KeepSurvivesAnUnwritableCache(t *testing.T) {
	a3Isolate(t)
	rs := a3NewReleaseServer(t)
	a3SetVersion(t, "0.2.0")
	bin := []byte("#!/bin/sh\necho 0.3.0\n")
	rs.publish(t, "0.3.0", a3Archive(t, map[string][]byte{"conch/conch": bin}), "")
	// A file where the versions directory would go.
	if err := os.MkdirAll(config.Dir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(versionsDir(), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	exe := a3Exe(t, "old")
	keep(exe, "0.2.0")
	setPrevious("0.2.0")
	if _, ok := Kept("0.2.0"); ok || Previous() != "" {
		t.Fatalf("kept %v, previous %q", ok, Previous())
	}
	if err := InstallRelease(context.Background(), "0.3.0", exe); err != nil {
		t.Fatalf("install: %v", err)
	}
	if b, _ := os.ReadFile(exe); string(b) != string(bin) {
		t.Fatalf("exe = %q", b)
	}
}

// TestA3KeptTakesAVPrefixEitherWay: a tag ("v1.2.3") and a version
// ("1.2.3") name the same kept binary, so a move back by tag needs no
// download either.
func TestA3KeptTakesAVPrefixEitherWay(t *testing.T) {
	a3Isolate(t) // release downloads would go to a port nothing listens on
	a3SetVersion(t, "2.0.0")
	keep(a3Exe(t, "the 1.2.3 binary"), "v1.2.3")
	for _, v := range []string{"1.2.3", "v1.2.3", "  v1.2.3\n"} {
		path, ok := Kept(v)
		if !ok || filepath.Base(path) != "conch-1.2.3" {
			t.Fatalf("Kept(%q) = %q %v", v, path, ok)
		}
	}
	exe := a3Exe(t, "the running binary")
	if err := InstallRelease(context.Background(), "v1.2.3", exe); err != nil {
		t.Fatalf("install by tag: %v", err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "the 1.2.3 binary" {
		t.Fatalf("exe = %q", b)
	}
	if got := Previous(); got != "2.0.0" {
		t.Fatalf("Previous() = %q", got)
	}
}

// TestA3KeepCannotWrite: a versions folder that exists but can't be
// written leaves no file — and no half-written one — behind.
func TestA3KeepCannotWrite(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes to a read-only directory")
	}
	a3Isolate(t)
	if err := os.MkdirAll(versionsDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(versionsDir(), 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(versionsDir(), 0o700) }) // so t.TempDir can clean up

	keep(a3Exe(t, "a binary"), "4.4.4")
	if _, ok := Kept("4.4.4"); ok {
		t.Fatal("kept a binary under an unwritable folder")
	}
	ents, err := os.ReadDir(versionsDir())
	if err != nil || len(ents) != 0 {
		t.Fatalf("%d entries left behind: %v", len(ents), err)
	}
	setPrevious("4.4.4")
	if got := Previous(); got != "" {
		t.Fatalf("Previous() = %q", got)
	}
}

// TestA3BinaryFallsBackToTheRelease: a kept copy that can't be read is
// not fatal — the published release is downloaded instead.
func TestA3BinaryFallsBackToTheRelease(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads an unreadable file")
	}
	a3Isolate(t)
	rs := a3NewReleaseServer(t)
	a3SetVersion(t, "0.2.0")
	bin := []byte("#!/bin/sh\necho 0.3.0\n")
	rs.publish(t, "0.3.0", a3Archive(t, map[string][]byte{"conch/conch": bin}), "")
	if err := os.MkdirAll(versionsDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keptName("0.3.0"), []byte("not readable"), 0o000); err != nil {
		t.Fatal(err)
	}
	if _, ok := Kept("0.3.0"); !ok {
		t.Fatal("the kept file is there; Kept should see it")
	}

	exe := a3Exe(t, "old")
	if err := InstallRelease(context.Background(), "0.3.0", exe); err != nil {
		t.Fatalf("install: %v", err)
	}
	if b, _ := os.ReadFile(exe); string(b) != string(bin) {
		t.Fatalf("exe = %q", b)
	}
}

// TestA3InstallReleaseFailureChangesNothing: a download that fails leaves
// the executable, the kept copies and the note exactly as they were.
func TestA3InstallReleaseFailureChangesNothing(t *testing.T) {
	a3Isolate(t)
	a3NewReleaseServer(t) // nothing published
	a3SetVersion(t, "0.2.0")
	setPrevious("0.1.0")

	exe := a3Exe(t, "the running binary")
	if err := InstallRelease(context.Background(), "9.9.9", exe); err == nil {
		t.Fatal("a release that isn't published was accepted")
	}
	if b, _ := os.ReadFile(exe); string(b) != "the running binary" {
		t.Fatalf("exe = %q", b)
	}
	if _, ok := Kept("0.2.0"); ok {
		t.Fatal("kept the outgoing binary although nothing was installed")
	}
	if got := Previous(); got != "0.1.0" {
		t.Fatalf("Previous() = %q", got)
	}
}

// TestA3ReleasesKeepsFeedOrder: build metadata doesn't order releases, so
// versions that compare equal stay in the order the feed gave them.
func TestA3ReleasesKeepsFeedOrder(t *testing.T) {
	a3Isolate(t)
	rs := a3NewReleaseServer(t)
	rs.a3Feed("1.0.0+b", "1.0.0+a", "0.9.0")
	got, err := Releases(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"1.0.0+b", "1.0.0+a", "0.9.0"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("Releases() = %v, want %v", got, want)
	}
}

// TestA3KeepOverSomethingThatIsNotAFile: a directory left where a kept
// binary belongs can't be replaced, and the attempt leaves no half-written
// file next to it.
func TestA3KeepOverSomethingThatIsNotAFile(t *testing.T) {
	a3Isolate(t)
	stale := keptName("6.6.6")
	if err := os.MkdirAll(filepath.Join(stale, "inside"), 0o700); err != nil {
		t.Fatal(err)
	}
	keep(a3Exe(t, "a binary"), "6.6.6")
	if _, ok := Kept("6.6.6"); ok {
		t.Fatal("a directory reported as a kept binary")
	}
	if _, err := os.Stat(stale + ".new"); !os.IsNotExist(err) {
		t.Fatalf("half-written file left behind: %v", err)
	}
	// And that version has to be downloaded again.
	if _, err := binary(context.Background(), "6.6.6"); err == nil {
		t.Fatal("the directory was read as a binary")
	}
}
