package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Conch.app's check: the latest release against the app's own version,
// pre-releases sorting before their release, and a development build
// never asking at all.
func TestCheckApp(t *testing.T) {
	a3Isolate(t)
	rs := a3NewReleaseServer(t)
	ctx := context.Background()
	for _, c := range []struct {
		current, latest string
		newer           bool
	}{
		{"0.2.0", "0.2.1", true},
		{"v0.2.0", "0.10.0", true},
		{"0.2.1", "0.2.1", false},
		{"0.2.2", "0.2.1", false},
		{"0.2.1-rc.1", "0.2.1", true},
		{"0.2.1-rc.1", "0.2.1-rc.2", true},
		{"0.2.1", "0.2.1-rc.2", false},
	} {
		rs.mu.Lock()
		rs.latest = c.latest
		rs.mu.Unlock()
		got, err := CheckApp(ctx, c.current)
		if err != nil {
			t.Fatalf("%s → %s: %v", c.current, c.latest, err)
		}
		if got.Newer != c.newer || got.Latest != c.latest || got.Dev || got.Current != strings.TrimPrefix(c.current, "v") {
			t.Errorf("%s → %s: %+v", c.current, c.latest, got)
		}
		if c.newer && !strings.HasSuffix(got.Notes, "/tag/v"+c.latest) {
			t.Errorf("notes: %q", got.Notes)
		}
		if c.newer && got.Asset != "" && got.Asset != "Conch_"+c.latest+"_macos_arm64.zip" {
			t.Errorf("asset: %q", got.Asset)
		}
	}
}

func TestCheckAppDevBuildNeverAsks(t *testing.T) {
	a3Isolate(t)
	rs := a3NewReleaseServer(t)
	rs.latest = "99.0.0"
	for _, v := range []string{"0.2.1-dev", "dev", "", "0.3.0-dev+abc"} {
		got, err := CheckApp(context.Background(), v)
		if err != nil || !got.Dev || got.Newer || got.Latest != "" {
			t.Errorf("%q: %+v %v", v, got, err)
		}
	}
	if len(rs.requests) != 0 {
		t.Fatalf("a development build asked: %v", rs.requests)
	}
}

func TestCheckAppErrors(t *testing.T) {
	a3Isolate(t)
	a3NewReleaseServer(t) // nothing published
	if _, err := CheckApp(context.Background(), "0.2.0"); err == nil || !strings.Contains(err.Error(), "no published release") {
		t.Fatalf("no release: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := CheckApp(ctx, "0.2.0"); err == nil {
		t.Fatal("cancelled: no error")
	}
}

// fakeApp makes a bundle with a version in its Info.plist.
func fakeApp(t *testing.T, dir, version string) string {
	t.Helper()
	return fakeAppAt(filepath.Join(dir, "Conch.app"), version)
}

func fakeAppAt(app, version string) string {
	os.MkdirAll(filepath.Join(app, "Contents", "MacOS"), 0o755)
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
	<key>CFBundleIdentifier</key><string>dev.conch.mac</string>
	<key>CFBundleShortVersionString</key><string>` + version + `</string>
</dict></plist>`
	os.WriteFile(filepath.Join(app, "Contents", "Info.plist"), []byte(plist), 0o644)
	os.WriteFile(filepath.Join(app, "Contents", "MacOS", "Conch"), []byte("v"+version), 0o755)
	return app
}

func TestAppReplaceable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write anywhere")
	}
	dir := t.TempDir()
	app := fakeApp(t, dir, "0.2.0")
	if err := AppReplaceable(app); err != nil {
		t.Fatalf("a writable install: %v", err)
	}
	// Translocated: macOS ran it from a random read-only copy.
	if err := AppReplaceable("/private/var/folders/xy/T/AppTranslocation/1234-ABCD/d/Conch.app"); err == nil || !strings.Contains(err.Error(), "Applications") {
		t.Fatalf("translocated: %v", err)
	}
	// Not a bundle, or not there.
	if err := AppReplaceable(filepath.Join(dir, "Missing.app")); err == nil {
		t.Fatal("missing: no error")
	}
	if err := AppReplaceable(dir); err == nil || !strings.Contains(err.Error(), ".app") {
		t.Fatalf("not a bundle: %v", err)
	}
	if err := AppReplaceable(""); err == nil {
		t.Fatal("empty: no error")
	}

	// A folder this user cannot write, as /Applications is to somebody who
	// is not an admin.
	locked := filepath.Join(t.TempDir(), "Applications")
	os.Mkdir(locked, 0o755)
	lockedApp := fakeApp(t, locked, "0.2.0")
	os.Chmod(locked, 0o555)
	t.Cleanup(func() { os.Chmod(locked, 0o755) })
	if err := AppReplaceable(lockedApp); err == nil || !strings.Contains(err.Error(), "can't write") {
		t.Fatalf("unwritable folder: %v", err)
	}
	os.Chmod(locked, 0o755)
	// A bundle installed by an admin: the folder is writable, the app is not.
	os.Chmod(filepath.Join(lockedApp, "Contents"), 0o555)
	t.Cleanup(func() { os.Chmod(filepath.Join(lockedApp, "Contents"), 0o755) })
	if err := AppReplaceable(lockedApp); err == nil || !strings.Contains(err.Error(), "can't write") {
		t.Fatalf("unwritable bundle: %v", err)
	}
	// Probing left nothing behind.
	if left, _ := os.ReadDir(locked); len(left) != 1 {
		t.Fatalf("left behind: %v", left)
	}
}

// A disk image is read-only, which says itself as an unwritable folder
// under /Volumes; the advice is to copy it to Applications.
func TestAppReplaceableFromADiskImage(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write anywhere")
	}
	vol := filepath.Join(t.TempDir(), "Volumes", "Conch")
	os.MkdirAll(vol, 0o755)
	app := fakeApp(t, vol, "0.2.0")
	os.Chmod(vol, 0o555)
	t.Cleanup(func() { os.Chmod(vol, 0o755) })
	old := volumesDir
	volumesDir = filepath.Dir(vol)
	t.Cleanup(func() { volumesDir = old })
	if err := AppReplaceable(app); err == nil || !strings.Contains(err.Error(), "disk image") {
		t.Fatalf("dmg: %v", err)
	}
}

// fakeTools answers codesign and spctl as they would for apps signed by
// the teams given (by path; "" for ad hoc), with the failures asked for.
type fakeTools struct {
	team       map[string]string
	broken     map[string]bool // codesign --verify fails
	notarized  map[string]bool
	calls      []string
	spctlFails bool
	unzip      func(zip, dest string) error
}

func (f *fakeTools) run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	path := args[len(args)-1]
	switch {
	case name == "codesign" && args[0] == "--verify":
		if f.broken[path] {
			return []byte(path + ": a sealed resource is missing or invalid"), errors.New("exit status 1")
		}
		return nil, nil
	case name == "codesign" && args[0] == "-dv":
		team := f.team[path]
		if team == "" {
			team = "not set"
		}
		return []byte("Executable=" + path + "/Contents/MacOS/Conch\nIdentifier=dev.conch.mac\nTeamIdentifier=" + team + "\n"), nil
	case name == "ditto" && f.unzip != nil:
		if err := f.unzip(args[2], args[3]); err != nil {
			return []byte(err.Error()), err
		}
		return nil, nil
	case name == "spctl":
		if f.spctlFails {
			return []byte(path + ": rejected\nsource=Unnotarized Developer ID"), errors.New("exit status 3")
		}
		if f.notarized[path] {
			return []byte(path + ": accepted\nsource=Notarized Developer ID\norigin=Developer ID Application: X (" + f.team[path] + ")"), nil
		}
		return []byte(path + ": accepted\nsource=Developer ID\n"), nil
	}
	return nil, errors.New("unexpected " + name)
}

func withTools(t *testing.T, f *fakeTools) {
	t.Helper()
	old := appTool
	appTool = f.run
	t.Cleanup(func() { appTool = old })
}

func TestVerifyApp(t *testing.T) {
	ctx := context.Background()
	const running, next = "/Applications/Conch.app", "/tmp/x/Conch.app"
	good := func() *fakeTools {
		return &fakeTools{team: map[string]string{running: "ABCDE12345", next: "ABCDE12345"}, notarized: map[string]bool{next: true}}
	}
	f := good()
	withTools(t, f)
	if err := VerifyApp(ctx, next, running); err != nil {
		t.Fatalf("good: %v (%v)", err, f.calls)
	}
	if !strings.Contains(strings.Join(f.calls, "\n"), "codesign --verify --deep --strict "+next) {
		t.Fatalf("not verified deep and strict: %v", f.calls)
	}

	for _, c := range []struct {
		name string
		edit func(*fakeTools)
		want string
	}{
		{"broken signature", func(f *fakeTools) { f.broken = map[string]bool{next: true} }, "signature"},
		{"another team", func(f *fakeTools) { f.team[next] = "ZZZZZ99999" }, "ZZZZZ99999"},
		{"unsigned download", func(f *fakeTools) { delete(f.team, next) }, "not signed"},
		{"running app ad hoc", func(f *fakeTools) { delete(f.team, running) }, "isn't signed by a developer"},
		{"not notarized", func(f *fakeTools) { f.notarized = nil }, "notarized"},
		{"gatekeeper refuses", func(f *fakeTools) { f.spctlFails = true }, "Gatekeeper"},
	} {
		f := good()
		c.edit(f)
		withTools(t, f)
		if err := VerifyApp(ctx, next, running); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}

// appRelease publishes Conch.app version on the test release server: its
// "zip" is the version, which the fake ditto unpacks into a bundle.
func (rs *a3ReleaseServer) publishApp(version, zipBody string) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	asset := "Conch_" + version + "_macos_arm64.zip"
	h := sha256.Sum256([]byte(zipBody))
	rs.assets["/v"+version+"/"+asset] = []byte(zipBody)
	rs.assets["/v"+version+"/"+asset+".sha256"] = []byte(hex.EncodeToString(h[:]) + "  " + asset + "\n")
}

// installFixture is a writable install of 0.2.0, signed and notarized as
// far as the fake tools say, with 0.2.1 published.
func installFixture(t *testing.T) (app string, rs *a3ReleaseServer, f *fakeTools) {
	t.Helper()
	a3Isolate(t)
	rs = a3NewReleaseServer(t)
	rs.publishApp("0.2.1", "0.2.1")
	app = fakeApp(t, t.TempDir(), "0.2.0")
	f = &fakeTools{team: map[string]string{app: "ABCDE12345"}, notarized: map[string]bool{}}
	f.unzip = func(zip, dest string) error {
		v, err := os.ReadFile(zip)
		if err != nil {
			return err
		}
		next := fakeAppAt(filepath.Join(dest, "Conch.app"), string(v))
		f.team[next] = "ABCDE12345"
		f.notarized[next] = true
		return nil
	}
	withTools(t, f)
	old := appArch
	appArch = "arm64"
	t.Cleanup(func() { appArch = old })
	return app, rs, f
}

func appVersionOf(t *testing.T, app string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(app, "Contents", "MacOS", "Conch"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimPrefix(string(b), "v")
}

func TestInstallApp(t *testing.T) {
	app, _, _ := installFixture(t)
	ctx := context.Background()
	var said []string
	if err := InstallApp(ctx, "0.2.1", app, func(s string) { said = append(said, s) }); err != nil {
		t.Fatalf("install: %v (%v)", err, said)
	}
	if got := appVersionOf(t, app); got != "0.2.1" {
		t.Fatalf("installed %s", got)
	}
	if len(said) == 0 {
		t.Fatal("said nothing while it worked")
	}
	// The one before is kept, out of LaunchServices' way, and named.
	if v, ok := PreviousApp(app); !ok || v != "0.2.0" {
		t.Fatalf("previous: %q %v", v, ok)
	}
	// Nothing else is left beside the app: no staging, no probe.
	entries, _ := os.ReadDir(filepath.Dir(app))
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if strings.Join(names, ",") != ".Conch.app.previous,Conch.app" {
		t.Fatalf("left beside the app: %v", names)
	}

	// Rolling back puts 0.2.0 in place and keeps 0.2.1 to go forward again.
	if err := RollbackApp(app); err != nil {
		t.Fatal(err)
	}
	if got := appVersionOf(t, app); got != "0.2.0" {
		t.Fatalf("rolled back to %s", got)
	}
	if v, _ := PreviousApp(app); v != "0.2.1" {
		t.Fatalf("after rollback, previous is %q", v)
	}
}

// Only one previous app is kept: a second update replaces it.
func TestInstallAppKeepsOnePrevious(t *testing.T) {
	app, rs, _ := installFixture(t)
	rs.publishApp("0.2.2", "0.2.2")
	ctx := context.Background()
	for _, v := range []string{"0.2.1", "0.2.2"} {
		if err := InstallApp(ctx, v, app, func(string) {}); err != nil {
			t.Fatalf("%s: %v", v, err)
		}
	}
	if got := appVersionOf(t, app); got != "0.2.2" {
		t.Fatalf("installed %s", got)
	}
	if v, _ := PreviousApp(app); v != "0.2.1" {
		t.Fatalf("previous %q", v)
	}
}

// Every refusal leaves the running app exactly as it was, and nothing
// beside it.
func TestInstallAppFailuresChangeNothing(t *testing.T) {
	for _, c := range []struct {
		name string
		edit func(t *testing.T, app string, rs *a3ReleaseServer, f *fakeTools)
		want string
	}{
		{"bad checksum", func(t *testing.T, app string, rs *a3ReleaseServer, f *fakeTools) {
			rs.mu.Lock()
			rs.assets["/v0.2.1/Conch_0.2.1_macos_arm64.zip.sha256"] = []byte(strings.Repeat("0", 64) + "  Conch_0.2.1_macos_arm64.zip\n")
			rs.mu.Unlock()
		}, "checksum"},
		{"another team", func(t *testing.T, app string, rs *a3ReleaseServer, f *fakeTools) {
			inner := f.unzip
			f.unzip = func(zip, dest string) error {
				err := inner(zip, dest)
				f.team[filepath.Join(dest, "Conch.app")] = "ZZZZZ99999"
				return err
			}
		}, "team"},
		{"not notarized", func(t *testing.T, app string, rs *a3ReleaseServer, f *fakeTools) {
			inner := f.unzip
			f.unzip = func(zip, dest string) error {
				err := inner(zip, dest)
				f.notarized[filepath.Join(dest, "Conch.app")] = false
				return err
			}
		}, "notarized"},
		{"wrong version inside", func(t *testing.T, app string, rs *a3ReleaseServer, f *fakeTools) {
			rs.publishApp("0.2.1", "0.1.0")
		}, "0.1.0"},
		{"no app in the zip", func(t *testing.T, app string, rs *a3ReleaseServer, f *fakeTools) {
			f.unzip = func(zip, dest string) error { return os.WriteFile(filepath.Join(dest, "README"), nil, 0o644) }
		}, "no Conch.app"},
		{"unzip fails", func(t *testing.T, app string, rs *a3ReleaseServer, f *fakeTools) {
			f.unzip = func(zip, dest string) error { return errors.New("ditto: truncated archive") }
		}, "truncated"},
		{"no such release", func(t *testing.T, app string, rs *a3ReleaseServer, f *fakeTools) {
			rs.mu.Lock()
			rs.assets = map[string][]byte{}
			rs.mu.Unlock()
		}, "404"},
		{"intel", func(t *testing.T, app string, rs *a3ReleaseServer, f *fakeTools) { appArch = "amd64" }, "Apple Silicon"},
		{"translocated", nil, "Translocation"},
	} {
		t.Run(c.name, func(t *testing.T) {
			app, rs, f := installFixture(t)
			target := app
			if c.edit != nil {
				c.edit(t, app, rs, f)
			} else {
				target = "/private/var/folders/x/AppTranslocation/1/d/Conch.app"
			}
			err := InstallApp(context.Background(), "0.2.1", target, func(string) {})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want %q", err, c.want)
			}
			if got := appVersionOf(t, app); got != "0.2.0" {
				t.Fatalf("the running app became %s", got)
			}
			if entries, _ := os.ReadDir(filepath.Dir(app)); len(entries) != 1 {
				t.Fatalf("left beside the app: %v", entries)
			}
			if _, ok := PreviousApp(app); ok {
				t.Fatal("a previous app from a failed install")
			}
		})
	}
}

// The swap's second step failing puts the running app back.
func TestReplaceAppRestoresOnFailure(t *testing.T) {
	dir := t.TempDir()
	app := fakeApp(t, dir, "0.2.0")
	next := fakeAppAt(filepath.Join(dir, ".stage", "Conch.app"), "0.2.1")
	old := appRename
	n := 0
	appRename = func(from, to string) error {
		n++
		if n == 2 {
			return errors.New("disk full")
		}
		return os.Rename(from, to)
	}
	t.Cleanup(func() { appRename = old })
	if err := replaceApp(app, next); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("got %v", err)
	}
	if got := appVersionOf(t, app); got != "0.2.0" {
		t.Fatalf("running app is %s", got)
	}
}

func TestRollbackAppWithNothingKept(t *testing.T) {
	app := fakeApp(t, t.TempDir(), "0.2.0")
	if err := RollbackApp(app); err == nil || !strings.Contains(err.Error(), "no previous") {
		t.Fatalf("got %v", err)
	}
	if _, ok := PreviousApp(app); ok {
		t.Fatal("previous with none kept")
	}
	if got := appVersionOf(t, app); got != "0.2.0" {
		t.Fatalf("app is %s", got)
	}
}
