package buildinfo

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"testing"

	"github.com/Amitgb14/conch/internal/proto"
)

var a3Hash = regexp.MustCompile(`^[0-9a-f]{12}$`)

func TestA3HashFile(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	if err := os.WriteFile(a, []byte("conch build a"), 0o755); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("conch build a"))
	want := hex.EncodeToString(sum[:])[:12]
	if got := HashFile(a); got != want {
		t.Fatalf("HashFile = %q, want %q", got, want)
	}

	b := filepath.Join(dir, "b")
	if err := os.WriteFile(b, []byte("conch build b"), 0o755); err != nil {
		t.Fatal(err)
	}
	if HashFile(b) == HashFile(a) {
		t.Fatal("different contents, same hash")
	}

	empty := filepath.Join(dir, "empty")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := HashFile(empty); got != "e3b0c44298fc" { // sha256("")
		t.Fatalf("empty file = %q", got)
	}

	if got := HashFile(filepath.Join(dir, "missing")); got != "" {
		t.Fatalf("missing file = %q", got)
	}
	// A directory opens but can't be read.
	if got := HashFile(dir); got != "" {
		t.Fatalf("directory = %q", got)
	}
}

func TestA3HashFileUnreadable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads everything")
	}
	p := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(p, []byte("x"), 0o000); err != nil {
		t.Fatal(err)
	}
	if got := HashFile(p); got != "" {
		t.Fatalf("unreadable file = %q", got)
	}
}

func TestA3BuildIsStable(t *testing.T) {
	b := Build()
	if !a3Hash.MatchString(b) {
		t.Fatalf("Build = %q, want 12 hex digits", b)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if HashFile(exe) != b {
		t.Fatal("Build is not the hash of the running executable")
	}
	if Build() != b {
		t.Fatal("Build changed between calls")
	}
}

func TestA3IDAndSourceBuild(t *testing.T) {
	old := SourceBuild
	defer func() { SourceBuild = old }()

	SourceBuild = ""
	if ID() != Build() {
		t.Fatalf("native build: ID %q != Build %q", ID(), Build())
	}
	SourceBuild = "abcdef012345"
	if ID() != "abcdef012345" {
		t.Fatalf("cross-built: ID = %q", ID())
	}
	if Build() == "abcdef012345" {
		t.Fatal("SourceBuild must not change the binary's own hash")
	}
}

func TestA3PlatformAndCurrent(t *testing.T) {
	if Platform() != runtime.GOOS+"/"+runtime.GOARCH {
		t.Fatalf("Platform = %q", Platform())
	}
	oldSrc, oldVer := SourceBuild, proto.Version
	defer func() { SourceBuild, proto.Version = oldSrc, oldVer }()
	SourceBuild, proto.Version = "src123456789", "7.8.9"

	info := Current()
	want := Info{Version: "7.8.9", Build: Build(), BuildID: "src123456789", Platform: Platform(), Capabilities: proto.Capabilities}
	if !reflect.DeepEqual(info, want) {
		t.Fatalf("Current = %+v, want %+v", info, want)
	}

	b, err := json.Marshal(Info{Version: "1", Build: "b", Platform: "p"})
	if err != nil || string(b) != `{"version":"1","build":"b","platform":"p","capabilities":null}` {
		t.Fatalf("json = %s %v", b, err)
	}
}

func TestVersionFromModule(t *testing.T) {
	for _, c := range []struct{ current, module, want string }{
		{"0.1.0-dev", "v0.1.0", "0.1.0"}, // go install …@v0.1.0
		{"0.2.0-dev", "v0.10.12", "0.10.12"},
		{"0.1.0-dev", "(devel)", "0.1.0-dev"}, // a source checkout
		{"0.1.0-dev", "", "0.1.0-dev"},
		{"0.1.0-dev", "v0.1.1-0.20260915101010-abcdef123456", "0.1.0-dev"}, // an untagged commit
		{"0.1.0-dev", "v0.2.0-rc.1", "0.1.0-dev"},
		{"0.1.0-dev", "0.1.0", "0.1.0-dev"},
		{"0.1.0", "v0.3.0", "0.1.0"}, // the release ldflags win
		{"", "v0.3.0", ""},
	} {
		if got := versionFrom(c.current, c.module); got != c.want {
			t.Errorf("versionFrom(%q, %q) = %q, want %q", c.current, c.module, got, c.want)
		}
	}
	// In a test binary the module is (devel): nothing changes.
	before := proto.Version
	ResolveVersion()
	if proto.Version != before {
		t.Fatalf("test binary version changed to %q", proto.Version)
	}
}

// A Homebrew install is laid out as brew lays it: the binary in a keg per
// version, the formula's opt link to the current keg, and bin linking in.
func TestStableHomebrew(t *testing.T) {
	prefix := t.TempDir()
	put := func(version string) string {
		bin := filepath.Join(prefix, "Cellar", "conch", version, "bin", "conch")
		if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		return bin
	}
	link := func(to, at string) {
		os.Remove(at)
		if err := os.MkdirAll(filepath.Dir(at), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(to, at); err != nil {
			t.Fatal(err)
		}
	}
	old := put("0.1.9")
	link("../Cellar/conch/0.1.9", filepath.Join(prefix, "opt", "conch"))
	link("../Cellar/conch/0.1.9/bin/conch", filepath.Join(prefix, "bin", "conch"))
	real, _ := filepath.EvalSymlinks(prefix) // macOS: /var is /private/var
	opt := filepath.Join(real, "opt", "conch", "bin", "conch")

	// From the keg, through bin, or the opt link itself: the opt link.
	for _, from := range []string{old, filepath.Join(prefix, "bin", "conch"), filepath.Join(prefix, "opt", "conch", "bin", "conch")} {
		if got := Stable(from); got != opt {
			t.Errorf("Stable(%s) = %s, want %s", from, got, opt)
		}
		if !Homebrew(from) {
			t.Errorf("Homebrew(%s) = false", from)
		}
	}
	// Upgraded under a running conch: the opt link moved on, so the old
	// keg is not what it leads to and the path is left as it is.
	put("0.2.0")
	link("../Cellar/conch/0.2.0", filepath.Join(prefix, "opt", "conch"))
	if got := Stable(old); got != old {
		t.Errorf("after an upgrade Stable = %s", got)
	}
	// No opt link at all.
	os.Remove(filepath.Join(prefix, "opt", "conch"))
	if got := Stable(old); got != old || !Homebrew(old) {
		t.Errorf("without opt: %s %v", got, Homebrew(old))
	}

	// Not Homebrew's: left exactly as given, symlinks and all.
	plain := filepath.Join(t.TempDir(), "conch")
	os.WriteFile(plain, nil, 0o755)
	alias := filepath.Join(t.TempDir(), "conch")
	link(plain, alias)
	other := filepath.Join(prefix, "Cellar", "conch-dev", "1", "bin", "conch")
	os.MkdirAll(filepath.Dir(other), 0o755)
	os.WriteFile(other, nil, 0o755)
	for _, p := range []string{plain, alias, other, filepath.Join(prefix, "missing", "conch"), ""} {
		if got := Stable(p); got != p {
			t.Errorf("Stable(%q) = %q", p, got)
		}
		if Homebrew(p) {
			t.Errorf("Homebrew(%q) = true", p)
		}
	}
	// The test binary is nobody's keg.
	exe, _ := os.Executable()
	if got, err := Executable(); err != nil || got != exe {
		t.Errorf("Executable = %q %v, want %q", got, err, exe)
	}
}
