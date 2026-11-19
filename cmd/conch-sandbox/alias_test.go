package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestAliasTokenKind(t *testing.T) {
	isolate(t)
	fakeProvider(t, "fakebox", tokenKind)
	if code, _, errs := run(t, "", "create", "--provider", "fakebox"); code != 0 {
		t.Fatalf("%q", errs)
	}
	text := readFile(t, aliasFile())
	for _, want := range []string{
		"# >>> conch-sandbox conch-fakebox-box1\nHost conch-fakebox-box1\n",
		"  HostName gw.test\n", "  Port 22\n", "  User tok-secret-box1-1\n",
		"  StrictHostKeyChecking accept-new\n", "  HostKeyAlias conch-fakebox-box1\n",
		"  UserKnownHostsFile " + knownHostsFile() + "\n",
		"until 2026-11-20T12:00:00Z", "# <<< conch-sandbox conch-fakebox-box1\n",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "IdentityFile") {
		t.Fatalf("a token alias names a key:\n%s", text)
	}
	if !strings.HasPrefix(text, aliasHeader) {
		t.Fatalf("no header:\n%s", text)
	}
	for path, mode := range map[string]os.FileMode{aliasFile(): 0o600, filepath.Dir(aliasFile()): 0o700} {
		if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != mode {
			t.Fatalf("%s: %v %v", path, fi, err)
		}
	}

	// start writes the fresh token over the old, in the one block.
	run(t, "", "stop", "--provider", "fakebox", "box1")
	code, out, errs := run(t, "", "start", "--provider", "fakebox", "box1", "--json")
	if code != 0 || decode[result](t, out).SSH.Alias != "conch-fakebox-box1" {
		t.Fatalf("start: %d %s %q", code, out, errs)
	}
	text = readFile(t, aliasFile())
	if !strings.Contains(text, "User tok-secret-box1-2\n") || strings.Contains(text, "tok-secret-box1-1") ||
		strings.Count(text, "Host conch-fakebox-box1") != 1 {
		t.Fatalf("after start:\n%s", text)
	}
	noToken(t, out, errs)

	// A sandbox with no alias gets none from start: ssh-alias makes one.
	if code, _, errs := run(t, "", "create", "--provider", "fakebox"); code != 0 {
		t.Fatalf("%q", errs)
	}
	removeAlias("conch-fakebox-box2")
	code, out, _ = run(t, "", "start", "--provider", "fakebox", "box2", "--json")
	if code != 0 || decode[result](t, out).SSH.Alias != "" || hasAlias("conch-fakebox-box2") {
		t.Fatalf("start without an alias: %s", out)
	}
}

func TestAliasKeyKind(t *testing.T) {
	home := isolate(t)
	identity := userKey(t, home)
	fakeProvider(t, "keybox", keyKind)
	if code, _, errs := run(t, "", "create", "--provider", "keybox"); code != 0 {
		t.Fatalf("%q", errs)
	}
	text := readFile(t, aliasFile())
	for _, want := range []string{"Host conch-keybox-box1\n", "  HostName 10.0.0.9\n", "  Port 2222\n", "  User root\n",
		"  IdentityFile " + identity + "\n", "  IdentitiesOnly yes\n", "  StrictHostKeyChecking accept-new\n"} {
		if !strings.Contains(text, want) {
			t.Fatalf("lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "token") {
		t.Fatalf("a key alias speaks of a token:\n%s", text)
	}
}

// rm takes the block out, and the host keys kept for it, and nothing else.
func TestAliasRemovedWithTheSandbox(t *testing.T) {
	isolate(t)
	fakeProvider(t, "fakebox", tokenKind)
	for range 2 {
		if code, _, errs := run(t, "", "create", "--provider", "fakebox"); code != 0 {
			t.Fatalf("%q", errs)
		}
	}
	os.WriteFile(knownHostsFile(), []byte("conch-fakebox-box1 ssh-ed25519 AAA1\n[conch-fakebox-box1]:2222 ssh-ed25519 AAA2\n"+
		"conch-fakebox-box10 ssh-ed25519 AAA3\nconch-fakebox-box2,1.2.3.4 ssh-ed25519 AAA4\n"), 0o600)
	code, _, errs := run(t, "", "rm", "--provider", "fakebox", "box1")
	if code != 0 || !strings.Contains(errs, "removed ssh alias conch-fakebox-box1") {
		t.Fatalf("%d %q", code, errs)
	}
	if hasAlias("conch-fakebox-box1") || !hasAlias("conch-fakebox-box2") {
		t.Fatalf("after rm:\n%s", readFile(t, aliasFile()))
	}
	if got := readFile(t, knownHostsFile()); got != "conch-fakebox-box10 ssh-ed25519 AAA3\nconch-fakebox-box2,1.2.3.4 ssh-ed25519 AAA4\n" {
		t.Fatalf("known hosts:\n%s", got)
	}
	// The last one out leaves the header, so the Include still names a file.
	run(t, "", "rm", "--provider", "fakebox", "box2")
	if got := readFile(t, aliasFile()); got != aliasHeader {
		t.Fatalf("empty:\n%q", got)
	}
	if fi, _ := os.Stat(aliasFile()); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode())
	}
	if got := readFile(t, knownHostsFile()); got != "conch-fakebox-box10 ssh-ed25519 AAA3\n" {
		t.Fatalf("known hosts:\n%s", got)
	}
	// rm of a sandbox with no alias says nothing about one.
	if _, _, errs := run(t, "", "rm", "--provider", "fakebox", "box2"); strings.Contains(errs, "alias") {
		t.Fatalf("%q", errs)
	}
}

// Somebody's own lines in the file — a Host of their own, a comment — are
// kept through every write and removal, in their place.
func TestAliasFileKeepsWhatIsNotOurs(t *testing.T) {
	isolate(t)
	fakeProvider(t, "fakebox", tokenKind)
	mine := "# mine\nHost dev\n  HostName dev.example\n"
	os.MkdirAll(filepath.Dir(aliasFile()), 0o700)
	os.WriteFile(aliasFile(), []byte(mine), 0o600)
	// conch's own generated config beside it is not touched.
	conchs := filepath.Join(filepath.Dir(aliasFile()), "config")
	os.WriteFile(conchs, []byte("# Generated by conch\n"), 0o600)

	run(t, "", "create", "--provider", "fakebox")
	run(t, "", "ssh-alias", "--provider", "fakebox", "box1") // again: replaced, not added
	text := readFile(t, aliasFile())
	if !strings.HasPrefix(text, mine) || strings.Count(text, "Host conch-fakebox-box1") != 1 || strings.Contains(text, aliasHeader) {
		t.Fatalf("after writing:\n%s", text)
	}
	if code, _, errs := run(t, "", "ssh-alias", "--provider", "fakebox", "box1", "--remove"); code != 0 {
		t.Fatalf("%q", errs)
	}
	if got := readFile(t, aliasFile()); got != mine {
		t.Fatalf("after removing:\n%q", got)
	}
	if got := readFile(t, conchs); got != "# Generated by conch\n" {
		t.Fatalf("conch's config: %q", got)
	}
}

func TestWithout(t *testing.T) {
	lines := strings.Split(strings.Join([]string{
		"a",
		beginMark + "x", "Host x", endMark + "x",
		"b",
		beginMark + "lost", "Host lost", // its end mark gone
		beginMark + "y", "Host y", endMark + "y",
		beginMark + "tail", "Host tail",
	}, "\n"), "\n")
	for name, want := range map[string]string{
		"x":    "a b " + beginMark + "lost",
		"lost": "a " + beginMark + "x",
		"tail": endMark + "y",
		"none": "",
	} {
		out, found := without(lines, name)
		got := strings.Join(out, " ")
		if found != (want != "") || !strings.Contains(got, want) || (found && strings.Contains(got, "Host "+name)) {
			t.Errorf("%s: %v %q", name, found, got)
		}
		if name == "lost" && !strings.Contains(got, "Host y") {
			t.Errorf("a lost end took the next block: %q", got)
		}
	}
}

func TestSSHAliasVerb(t *testing.T) {
	isolate(t)
	fakeProvider(t, "fakebox", tokenKind)
	run(t, "", "create", "--provider", "fakebox")
	removeAlias("conch-fakebox-box1")
	code, out, errs := run(t, "", "ssh-alias", "--provider", "fakebox", "box1")
	if code != 0 || out != "conch-fakebox-box1\n" || !hasAlias("conch-fakebox-box1") {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	code, out, errs = run(t, "", "ssh-alias", "--provider", "fakebox", "box1", "--json")
	r := decode[result](t, out)
	if code != 0 || r.SSH == nil || r.SSH.Alias != "conch-fakebox-box1" || r.SSH.User != "…" {
		t.Fatalf("json: %d %s", code, out)
	}
	noToken(t, out, errs)

	code, out, _ = run(t, "", "ssh-alias", "--provider", "fakebox", "box1", "--remove", "--json")
	if a := decode[aliasJSON](t, out); code != 0 || !a.Removed || a.Alias != "conch-fakebox-box1" {
		t.Fatalf("remove: %s", out)
	}
	// Removing what is not there is not a failure; nor is a provider whose
	// file is gone, which is never opened.
	code, out, errs = run(t, "", "ssh-alias", "--provider", "gone", "box9", "--remove")
	if code != 0 || out != "" || !strings.Contains(errs, "no ssh alias conch-gone-box9") {
		t.Fatalf("remove nothing: %d %q %q", code, out, errs)
	}
	// A stopped sandbox has no way in to write.
	run(t, "", "stop", "--provider", "fakebox", "box1")
	if code, _, errs := run(t, "", "ssh-alias", "--provider", "fakebox", "box1"); code != 1 || !strings.Contains(errs, "start it first") {
		t.Fatalf("stopped: %d %q", code, errs)
	}
	if code, _, errs := run(t, "", "ssh-alias", "--provider", "fakebox", "nobox"); code != 1 || !strings.Contains(errs, "has no sandbox nobox") {
		t.Fatalf("missing: %d %q", code, errs)
	}
	for _, args := range [][]string{
		{"ssh-alias", "box1"},
		{"ssh-alias", "--provider", "fakebox"},
		{"ssh-alias", "--provider", "fakebox", "a*"},
		{"ssh-alias", "--provider", "Bad Name", "box1", "--remove"},
	} {
		if code, _, errs := run(t, "", args...); code != 2 || !strings.Contains(errs, "usage:") {
			t.Errorf("%v: %d %q", args, code, errs)
		}
	}
}

// A provider's reply that would add lines of its own to the file is
// refused, and the sandbox made for it deleted.
func TestAliasRefusesWhatWouldInjectConfig(t *testing.T) {
	isolate(t)
	f := fakeProvider(t, "fakebox", tokenKind)
	f.token = "tok\nProxyCommand evil"
	code, _, errs := run(t, "", "create", "--provider", "fakebox")
	if code != 1 || !strings.Contains(errs, "cannot go in an ssh config") || f.has("box1") {
		t.Fatalf("%d %q", code, errs)
	}
	if _, err := os.Stat(aliasFile()); err == nil && strings.Contains(readFile(t, aliasFile()), "evil") {
		t.Fatal("written")
	}
	for v, ok := range map[string]bool{"plain": true, "with space": true, "": false, `a"b`: false, "a\rb": false, "a\x00": false} {
		got, err := configValue("x", v)
		if (err == nil) != ok {
			t.Errorf("%q: %v", v, err)
		}
		if v == "with space" && got != `"with space"` {
			t.Errorf("quoted %q", got)
		}
	}
	for _, port := range []int{-1, 70000} {
		if _, err := (alias{Provider: "p", ID: "i", Host: "h", User: "u", Port: port}).block(); err == nil {
			t.Errorf("port %d", port)
		}
	}
	exp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("x", 3600))
	b, _ := alias{Provider: "p", ID: "i", Host: "h", User: "u", Expires: &exp}.block()
	if !strings.Contains(b, "until 2026-01-02T02:04:05Z") {
		t.Errorf("expiry in UTC: %s", b)
	}
}

func TestHostsName(t *testing.T) {
	for line, want := range map[string]bool{
		"conch-a-b ssh-ed25519 K":         true,
		"[conch-a-b]:2222 ssh-ed25519 K":  true,
		"x,conch-a-b ssh-ed25519 K":       true,
		"conch-a-bc ssh-ed25519 K":        false,
		"[conch-a-b]:22x ssh-ed25519 K":   false,
		"[conch-a-bc]:2222 ssh-ed25519 K": false,
		"":                                false,
		"# conch-a-b":                     false,
	} {
		if hostsName(line, "conch-a-b") != want {
			t.Errorf("%q: %v", line, !want)
		}
	}
}
