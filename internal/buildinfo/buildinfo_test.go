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
