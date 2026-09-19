package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Amitgb14/conch/internal/buildinfo"
	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/remote"
)

func a3Isolate(t *testing.T) {
	t.Helper()
	t.Setenv("CONCH_SOCKET", "")
	t.Setenv("CONCH_PANE_ID", "")
	t.Setenv("CONCH_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")
	// Point release lookups at a server that refuses everything, so a test
	// that forgets to set its own can never reach GitHub.
	t.Setenv("CONCH_RELEASE_URL", "http://127.0.0.1:1")
}

func a3SetVersion(t *testing.T, v string) {
	t.Helper()
	old := proto.Version
	proto.Version = v
	t.Cleanup(func() { proto.Version = old })
}

func TestA3CompareEdgeCases(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"v1.2.3", "v1.2.3", 0},
		{"v1.2.3", "1.2.3", 0},
		{"1.2", "1.2.1", -1},
		{"1.2.1", "1.2", 1},
		{"1", "1.0.0", 0},
		{"2", "1.99.99", 1},
		{"0.10.0", "0.9.0", 1}, // numeric, not lexical
		{"0.9.0", "0.10.0", -1},
		{"1.0.0-rc1", "1.0.0-rc1", 0},
		{"1.0.0-beta", "1.0.0-alpha", 1},
		{"1.0.0-alpha", "1.0.0-beta", -1},
		{"0.1.0-dev", "0.1.0", -1},
		{"0.1.0", "0.1.0-dev", 1},
		{"0.2.0-dev", "0.1.9", 1}, // numbers first, then the suffix
		{"v0.2.0-rc1", "0.2.0-rc1", 0},
		{"", "", 0},
		{"", "0.0.0", 0},
		{"garbage", "0", 0}, // unparseable parts count as zero
		{"x.y.z", "0.0.1", -1},
		{"1.0.0-rc1-2", "1.0.0-rc1", 1}, // only the first dash splits
	} {
		if got := Compare(c.a, c.b); got != c.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
		if got := Compare(c.b, c.a); got != -c.want {
			t.Errorf("Compare(%q, %q) = %d, want %d (antisymmetry)", c.b, c.a, got, -c.want)
		}
	}
}

func TestA3CompareSemverNumericPrerelease(t *testing.T) {
	// SemVer 2.0 §11: numeric pre-release identifiers compare numerically,
	// so rc.10 is newer than rc.2, and numbers sort before words.
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"1.0.0-rc.10", "1.0.0-rc.2", 1},
		{"1.0.0-rc.2", "1.0.0-rc.10", -1},
		{"1.0.0-1", "1.0.0-alpha", -1}, // numeric before alphanumeric
		{"1.0.0-alpha", "1.0.0-1", 1},
		{"1.0.0-alpha", "1.0.0-alpha.1", -1}, // a prefix sorts first
		{"1.0.0-alpha.1", "1.0.0-alpha", 1},
		{"1.0.0-alpha.beta", "1.0.0-alpha.1", 1},
		{"1.0.0-rc.1", "1.0.0-rc.1", 0},
		{"1.0.0-beta.11", "1.0.0-beta.2", 1},
	} {
		if got := Compare(c.a, c.b); got != c.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestA3CompareBuildMetadata(t *testing.T) {
	// SemVer build metadata (+...) must be ignored for precedence.
	for _, c := range [][2]string{{"1.0.1+build.5", "1.0.1"}, {"1.0.1-rc.1+x", "1.0.1-rc.1"}, {"v0.1.0+abc", "0.1.0"}} {
		if got := Compare(c[0], c[1]); got != 0 {
			t.Errorf("Compare(%q, %q) = %d, want 0", c[0], c[1], got)
		}
	}
	if Compare("1.0.2+old", "1.0.1+new") != 1 {
		t.Error("metadata changed the order")
	}
}

func TestA3SameBuildMixed(t *testing.T) {
	a3SetVersion(t, "0.4.0")
	// A release client talking to a development server falls back to build IDs.
	if same, ok := SameBuild(proto.HelloResult{Version: "0.4.0-dev", BuildID: buildinfo.ID()}); !same || !ok {
		t.Fatalf("dev server, same build ID: %v %v", same, ok)
	}
	if same, ok := SameBuild(proto.HelloResult{Version: "0.4.0-dev", BuildID: "nope"}); same || !ok {
		t.Fatalf("dev server, other build ID: %v %v", same, ok)
	}
	if _, ok := SameBuild(proto.HelloResult{Version: "0.1.0-dev"}); ok {
		t.Fatal("dev server without build ID can't tell")
	}
	if _, ok := SameBuild(proto.HelloResult{}); ok {
		t.Fatal("server reporting nothing can't tell")
	}
	if same, ok := SameBuild(proto.HelloResult{Version: "0.5.0"}); same || !ok {
		t.Fatalf("newer release server: %v %v", same, ok)
	}

	// A development client ignores matching version strings.
	a3SetVersion(t, "0.4.0-dev")
	if same, ok := SameBuild(proto.HelloResult{Version: "0.4.0-dev", BuildID: "other"}); same || !ok {
		t.Fatalf("dev client, same version but other build: %v %v", same, ok)
	}
}

func TestA3Executable(t *testing.T) {
	got, err := Executable()
	if err != nil {
		t.Fatal(err)
	}
	exe, _ := os.Executable()
	want, err := filepath.EvalSymlinks(exe)
	if err != nil {
		t.Fatal(err)
	}
	if got != want || !filepath.IsAbs(got) {
		t.Fatalf("Executable = %q, want %q", got, want)
	}
}

// a3ReleaseServer fakes the release download host: /latest redirects to the
// tag page, /v<version>/ serves checksums and archives.
type a3ReleaseServer struct {
	*httptest.Server
	mu       sync.Mutex
	latest   string // "" = no Location header
	assets   map[string][]byte
	requests []string
}

func a3NewReleaseServer(t *testing.T) *a3ReleaseServer {
	t.Helper()
	rs := &a3ReleaseServer{assets: map[string][]byte{}}
	rs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rs.mu.Lock()
		defer rs.mu.Unlock()
		rs.requests = append(rs.requests, r.Method+" "+r.URL.Path)
		if r.URL.Path == "/latest" {
			if rs.latest != "" {
				w.Header().Set("Location", "https://example.invalid/Amitgb14/conch/releases/tag/v"+rs.latest)
				w.WriteHeader(http.StatusFound)
				return
			}
			w.WriteHeader(http.StatusNotFound)
			return
		}
		b, ok := rs.assets[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(rs.Close)
	t.Setenv("CONCH_RELEASE_URL", rs.URL+"/")
	return rs
}

func a3Archive(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func (rs *a3ReleaseServer) publish(t *testing.T, version string, archive []byte, sum string) {
	t.Helper()
	asset := fmt.Sprintf("conch_%s_%s.tar.gz", version, strings.ReplaceAll(buildinfo.Platform(), "/", "_"))
	if sum == "" {
		h := sha256.Sum256(archive)
		sum = hex.EncodeToString(h[:])
	}
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.assets["/v"+version+"/"+asset] = archive
	rs.assets["/v"+version+"/checksums.txt"] = []byte("0000 conch_" + version + "_other_os.tar.gz\n" + sum + "  " + asset + "\n")
}

func TestA3NewerReleaseDevBuildNeverAsks(t *testing.T) {
	a3Isolate(t)
	rs := a3NewReleaseServer(t)
	rs.latest = "99.0.0"
	a3SetVersion(t, "0.1.0-dev")
	rel, err := NewerRelease(context.Background())
	if rel != nil || err != nil {
		t.Fatalf("dev build: %+v %v", rel, err)
	}
	if len(rs.requests) != 0 {
		t.Fatalf("dev build contacted the release server: %v", rs.requests)
	}
}

func TestA3NewerRelease(t *testing.T) {
	a3Isolate(t)
	rs := a3NewReleaseServer(t)
	a3SetVersion(t, "0.2.0")

	for _, c := range []struct {
		latest string
		want   string
	}{
		{"0.3.0", "0.3.0"},
		{"0.10.0", "0.10.0"},
		{"0.2.0", ""},
		{"0.1.5", ""},
		{"0.2.0-rc1", ""},
	} {
		rs.mu.Lock()
		rs.latest = c.latest
		rs.mu.Unlock()
		rel, err := NewerRelease(context.Background())
		if err != nil {
			t.Fatalf("latest %s: %v", c.latest, err)
		}
		switch {
		case c.want == "" && rel != nil:
			t.Errorf("latest %s: reported %+v as newer than 0.2.0", c.latest, rel)
		case c.want != "" && (rel == nil || rel.Version != c.want):
			t.Errorf("latest %s: got %+v", c.latest, rel)
		}
	}
	rs.mu.Lock()
	defer rs.mu.Unlock()
	for _, r := range rs.requests {
		if r != "HEAD /latest" {
			t.Fatalf("unexpected request %q", r)
		}
	}
}

func TestA3NewerReleaseErrors(t *testing.T) {
	a3Isolate(t)
	a3SetVersion(t, "0.2.0")

	rs := a3NewReleaseServer(t) // no latest: 404 without a Location
	if rel, err := NewerRelease(context.Background()); err == nil || rel != nil || !strings.Contains(err.Error(), "no published release") {
		t.Fatalf("no release: %+v %v", rel, err)
	}
	rs.Close()
	if rel, err := NewerRelease(context.Background()); err == nil || rel != nil {
		t.Fatalf("server down: %+v %v", rel, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a3NewReleaseServer(t).latest = "9.9.9"
	if _, err := NewerRelease(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}
}

func TestA3InstallRelease(t *testing.T) {
	a3Isolate(t)
	rs := a3NewReleaseServer(t)
	bin := []byte("#!/bin/sh\necho new conch\n")
	rs.publish(t, "1.2.3", a3Archive(t, map[string][]byte{"README.md": []byte("docs"), "conch_1.2.3/conch": bin}), "")

	dir := t.TempDir()
	exe := filepath.Join(dir, "bin", "conch")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := InstallRelease(context.Background(), "v1.2.3", exe); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(exe)
	if err != nil || !bytes.Equal(got, bin) {
		t.Fatalf("installed %q %v", got, err)
	}
	info, _ := os.Stat(exe)
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %v", info.Mode())
	}
	if _, err := os.Stat(exe + ".new"); !os.IsNotExist(err) {
		t.Fatalf("temporary file left behind: %v", err)
	}
}

func TestA3InstallReleaseFailures(t *testing.T) {
	a3Isolate(t)
	rs := a3NewReleaseServer(t)
	good := a3Archive(t, map[string][]byte{"conch": []byte("bin")})
	rs.publish(t, "1.0.0", good, "")
	rs.publish(t, "2.0.0", good, strings.Repeat("ab", 32))                               // wrong checksum
	rs.publish(t, "3.0.0", a3Archive(t, map[string][]byte{"notconch": []byte("x")}), "") // no binary

	exe := filepath.Join(t.TempDir(), "conch")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		version, want string
	}{
		{"4.0.0", "404"},
		{"2.0.0", "checksum mismatch"},
		{"3.0.0", "no conch binary"},
	} {
		err := InstallRelease(context.Background(), c.version, exe)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("version %s: %v, want %q", c.version, err, c.want)
		}
		if b, _ := os.ReadFile(exe); string(b) != "old" {
			t.Fatalf("version %s: failed install replaced the binary with %q", c.version, b)
		}
	}

	// The download succeeds but the destination can't be written.
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(blocker, "sub", "conch")
	err := InstallRelease(context.Background(), "1.0.0", bad)
	if err == nil || !strings.HasPrefix(err.Error(), "replace "+bad+": ") {
		t.Fatalf("unwritable destination: %v", err)
	}
}

// a3FakeConn starts a client over an in-memory pipe whose server advertises
// caps and records the requests it receives.
type a3FakeConn struct {
	mu   sync.Mutex
	reqs []proto.Message
}

func (f *a3FakeConn) requests() []proto.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]proto.Message(nil), f.reqs...)
}

func a3Client(t *testing.T, hello proto.HelloResult, reply func(proto.Message) (any, *proto.Error)) (*client.Client, *a3FakeConn) {
	t.Helper()
	cli, srv := net.Pipe()
	f := &a3FakeConn{}
	go func() {
		conn := proto.NewConn(srv)
		defer conn.Close()
		for {
			msg, err := conn.Read()
			if err != nil {
				return
			}
			var result any = hello
			var perr *proto.Error
			if msg.Method != proto.MethodHello {
				f.mu.Lock()
				f.reqs = append(f.reqs, msg)
				f.mu.Unlock()
				result, perr = reply(msg)
			}
			resp := proto.Message{ID: msg.ID, Error: perr}
			if perr == nil {
				resp.Result = proto.Marshal(result)
			}
			if conn.Write(resp) != nil {
				return
			}
		}
	}()
	c, err := client.New(cli, "a3")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c, f
}

func a3ReloadReply(msg proto.Message) (any, *proto.Error) {
	var p proto.ServerReloadParams
	_ = json.Unmarshal(msg.Params, &p)
	if p.Binary == "/refuse" {
		return nil, proto.Errorf(proto.ErrBadRequest, "not executable")
	}
	return proto.ServerReloadResult{Binary: p.Binary}, nil
}

func TestA3Reload(t *testing.T) {
	a3Isolate(t)
	c, f := a3Client(t, proto.HelloResult{Capabilities: []string{"server.reload.v1"}}, a3ReloadReply)

	if err := Reload(context.Background(), c, "/new/conch"); err != nil {
		t.Fatal(err)
	}
	if err := Reload(context.Background(), c, ""); err != nil {
		t.Fatal(err)
	}
	reqs := f.requests()
	if len(reqs) != 2 || reqs[0].Method != proto.MethodServerReload || string(reqs[0].Params) != `{"binary":"/new/conch"}` || string(reqs[1].Params) != `{}` {
		t.Fatalf("requests = %+v", reqs)
	}

	var perr *proto.Error
	if err := Reload(context.Background(), c, "/refuse"); !errors.As(err, &perr) || perr.Code != proto.ErrBadRequest {
		t.Fatalf("server refusal: %v", err)
	}
}

func TestA3ReloadOldServer(t *testing.T) {
	a3Isolate(t)
	c, f := a3Client(t, proto.HelloResult{Capabilities: []string{"pane.v1"}}, a3ReloadReply)
	err := Reload(context.Background(), c, "/x")
	if err == nil || !strings.Contains(err.Error(), "predates reloading") {
		t.Fatalf("got %v", err)
	}
	if len(f.requests()) != 0 {
		t.Fatal("reload sent to a server that can't reload")
	}
}

func TestA3MachineWithoutPlatform(t *testing.T) {
	a3Isolate(t)
	c, f := a3Client(t, proto.HelloResult{Capabilities: []string{"server.reload.v1"}}, a3ReloadReply)
	said := 0
	err := Machine(context.Background(), c, remote.SSH("box", false), func(string) { said++ })
	if err == nil || !strings.Contains(err.Error(), "doesn't report its platform") {
		t.Fatalf("got %v", err)
	}
	if said != 0 || len(f.requests()) != 0 {
		t.Fatalf("acted without a platform: said %d, requests %v", said, f.requests())
	}
}

// a3FakeSSH installs a fake ssh that saves its stdin and prints the path the
// real install script would.
func a3FakeSSH(t *testing.T, exit int) (stdinFile string) {
	t.Helper()
	// A short TMPDIR keeps the ssh control directory inside the test.
	tmp, err := os.MkdirTemp("/tmp", "a3")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(tmp) })
	t.Setenv("TMPDIR", tmp)

	dir := t.TempDir()
	stdinFile = filepath.Join(dir, "stdin")
	script := filepath.Join(dir, "ssh")
	body := fmt.Sprintf("#!/bin/sh\ncat > %q\nif [ %d -ne 0 ]; then echo 'Permission denied (publickey).' >&2; exit %d; fi\necho /home/u/.local/bin/conch\n", stdinFile, exit, exit)
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONCH_SSH", script)
	t.Setenv("CONCH_SSH_CONFIG", "")
	return stdinFile
}

func TestA3Machine(t *testing.T) {
	a3Isolate(t)
	stdin := a3FakeSSH(t, 0)
	c, f := a3Client(t, proto.HelloResult{Platform: buildinfo.Platform(), Capabilities: []string{"server.reload.v1"}}, a3ReloadReply)

	var said []string
	if err := Machine(context.Background(), c, remote.SSH("box", false), func(s string) { said = append(said, s) }); err != nil {
		t.Fatal(err)
	}
	if len(said) == 0 || said[len(said)-1] != "reloading the server" {
		t.Fatalf("progress = %q", said)
	}
	// The binary shipped is this build's executable (same platform).
	exe, _ := os.Executable()
	if got, want := buildinfo.HashFile(stdin), buildinfo.HashFile(exe); got == "" || got != want {
		t.Fatalf("copied binary hash %q, want %q", got, want)
	}
	reqs := f.requests()
	if len(reqs) != 1 || reqs[0].Method != proto.MethodServerReload || string(reqs[0].Params) != `{"binary":"/home/u/.local/bin/conch"}` {
		t.Fatalf("requests = %+v", reqs)
	}
}

func TestA3MachineInstallFails(t *testing.T) {
	a3Isolate(t)
	a3FakeSSH(t, 255)
	c, f := a3Client(t, proto.HelloResult{Platform: buildinfo.Platform(), Capabilities: []string{"server.reload.v1"}}, a3ReloadReply)
	err := Machine(context.Background(), c, remote.SSH("box", false), func(string) {})
	if err == nil || !strings.Contains(err.Error(), "Permission denied") {
		t.Fatalf("got %v", err)
	}
	if len(f.requests()) != 0 {
		t.Fatal("reloaded after a failed install")
	}
}
