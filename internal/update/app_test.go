package update

import (
	"context"
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
	app := filepath.Join(dir, "Conch.app")
	if err := os.MkdirAll(filepath.Join(app, "Contents", "MacOS"), 0o755); err != nil {
		t.Fatal(err)
	}
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
