package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// The launcher, scripts/conch-sandbox.sh, is run with sh against a fake
// release in a folder of its own — by file://, or a server on loopback —
// with HOME a scratch folder, so it never reaches the network or the
// person's ~/.local/share.

var platforms = []string{"darwin-arm64", "darwin-amd64", "linux-arm64", "linux-amd64"}

// fakeRelease is a release of version: a stand-in program per platform
// that says which it is and what it was given, and their hashes.
type fakeRelease struct {
	dir  string
	sums map[string]string // platform → sha256
}

func makeRelease(t *testing.T, version string) *fakeRelease {
	t.Helper()
	r := &fakeRelease{dir: t.TempDir(), sums: map[string]string{}}
	for _, p := range platforms {
		body := fmt.Sprintf("#!/bin/sh\necho \"downloaded %s %s: $*\"\n", version, p)
		os.WriteFile(filepath.Join(r.dir, "conch-sandbox-"+version+"-"+p), []byte(body), 0o644)
		sum := sha256.Sum256([]byte(body))
		r.sums[p] = hex.EncodeToString(sum[:])
	}
	return r
}

func hostPlatform() string { return runtime.GOOS + "-" + runtime.GOARCH }

// pinned is the launcher with want= and the hashes set, as the release
// pins it, written to a file of its own.
func pinned(t *testing.T, version string, sums map[string]string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "scripts", "conch-sandbox.sh"))
	if err != nil {
		t.Fatal(err)
	}
	s := regexp.MustCompile(`(?m)^want=.*$`).ReplaceAllString(string(b), "want="+version)
	for _, p := range platforms {
		key := "sha_" + strings.ReplaceAll(p, "-", "_")
		s = regexp.MustCompile(`(?m)^`+key+`=.*$`).ReplaceAllString(s, key+"="+sums[p])
	}
	path := filepath.Join(t.TempDir(), "conch-sandbox.sh")
	os.WriteFile(path, []byte(s), 0o755)
	return path
}

// launch runs the launcher with HOME home, the builds at url, and the
// extra folders first on PATH.
func launch(t *testing.T, script, home, url string, path []string, args ...string) (int, string, string) {
	t.Helper()
	cmd := exec.Command("/bin/sh", append([]string{script}, args...)...)
	cmd.Env = []string{"HOME=" + home, "PATH=" + strings.Join(append(path, "/usr/bin", "/bin"), ":"), "CONCH_SANDBOX_URL=" + url}
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return code, out.String(), errb.String()
}

// pathCopy is a folder with a conch-sandbox in it that says version.
func pathCopy(t *testing.T, version string) string {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "conch-sandbox"), []byte(`#!/bin/sh
[ "$1" = version ] && { echo "`+version+`"; exit 0; }
echo "path copy: $*"
`), 0o755)
	return dir
}

func cached(home, version string) string {
	return filepath.Join(home, ".local", "share", "conch-sandbox", version, "conch-sandbox")
}

func TestLauncherFetchesChecksAndKeeps(t *testing.T) {
	r := makeRelease(t, "1.2.3")
	script := pinned(t, "1.2.3", r.sums)
	home := t.TempDir()
	code, out, errs := launch(t, script, home, "file://"+r.dir, nil, "create", "--provider", "x y")
	if code != 0 || out != "downloaded 1.2.3 "+hostPlatform()+": create --provider x y\n" {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	st, err := os.Stat(cached(home, "1.2.3"))
	if err != nil || st.Mode().Perm() != 0o755 {
		t.Fatalf("kept: %v %v", st, err)
	}
	// The kept copy runs with the release gone.
	if code, out, errs := launch(t, script, home, "file:///nonexistent", nil, "get"); code != 0 || !strings.Contains(out, ": get") || strings.Contains(errs, "fetching") {
		t.Fatalf("kept copy: %d %q %q", code, out, errs)
	}
	// A kept copy that is not the pinned build is fetched again.
	os.WriteFile(cached(home, "1.2.3"), []byte("#!/bin/sh\necho tampered\n"), 0o755)
	if code, out, _ := launch(t, script, home, "file://"+r.dir, nil, "get"); code != 0 || strings.Contains(out, "tampered") {
		t.Fatalf("tampered copy ran: %q", out)
	}
	// …and with the release gone, it does not run either.
	os.WriteFile(cached(home, "1.2.3"), []byte("#!/bin/sh\necho tampered\n"), 0o755)
	if code, out, errs := launch(t, script, home, "file:///nonexistent", nil, "get"); code != 1 || strings.Contains(out, "tampered") || !strings.Contains(errs, "could not fetch") {
		t.Fatalf("tampered, no release: %d %q %q", code, out, errs)
	}
	// Nothing left of the downloads but the program.
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(cached(home, "1.2.3")), ".download*")); len(left) != 0 {
		t.Fatalf("left: %v", left)
	}
}

func TestLauncherRefusesTheWrongBuild(t *testing.T) {
	r := makeRelease(t, "1.2.3")
	sums := map[string]string{}
	for p := range r.sums {
		sums[p] = strings.Repeat("0", 64)
	}
	script := pinned(t, "1.2.3", sums)
	home := t.TempDir()
	code, out, errs := launch(t, script, home, "file://"+r.dir, nil, "get")
	if code != 1 || out != "" || !strings.Contains(errs, "is not the build this launcher pins") || !strings.Contains(errs, r.sums[hostPlatform()]) {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	if _, err := os.Stat(cached(home, "1.2.3")); err == nil {
		t.Fatal("kept a build with the wrong hash")
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(cached(home, "1.2.3")), ".download*")); len(left) != 0 {
		t.Fatalf("left: %v", left)
	}
	// One not pinned for this platform at all.
	delete(sums, hostPlatform())
	if code, _, errs := launch(t, pinned(t, "1.2.3", sums), home, "file://"+r.dir, nil, "get"); code != 1 || !strings.Contains(errs, "pins no build of conch-sandbox 1.2.3 for "+hostPlatform()) {
		t.Fatalf("no pin: %d %q", code, errs)
	}
	// None there to fetch.
	if code, _, errs := launch(t, pinned(t, "1.2.4", r.sums), home, "file://"+r.dir, nil, "get"); code != 1 || !strings.Contains(errs, "could not fetch file://"+r.dir+"/conch-sandbox-1.2.4-"+hostPlatform()) {
		t.Fatalf("missing: %d %q", code, errs)
	}
}

// An OS or CPU no build is made for is said, and nothing is fetched.
func TestLauncherUnknownPlatform(t *testing.T) {
	r := makeRelease(t, "1.2.3")
	script := pinned(t, "1.2.3", r.sums)
	for _, c := range []struct{ s, m, want string }{
		{"Plan9", "x86_64", "no conch-sandbox build for Plan9"},
		{"Linux", "riscv64", "no conch-sandbox build for riscv64"},
		{"FreeBSD", "amd64", "no conch-sandbox build for FreeBSD"},
	} {
		bin := t.TempDir()
		os.WriteFile(filepath.Join(bin, "uname"), []byte("#!/bin/sh\ncase $1 in -s) echo "+c.s+";; -m) echo "+c.m+";; esac\n"), 0o755)
		home := t.TempDir()
		code, out, errs := launch(t, script, home, "file://"+r.dir, []string{bin}, "get")
		if code != 1 || out != "" || !strings.Contains(errs, c.want) {
			t.Errorf("%s %s: %d %q %q", c.s, c.m, code, out, errs)
		}
		if _, err := os.Stat(filepath.Join(home, ".local")); err == nil {
			t.Errorf("%s %s: fetched", c.s, c.m)
		}
	}
	// uname's other names for the CPUs there are builds for.
	for m, p := range map[string]string{"aarch64": "linux-arm64", "amd64": "linux-amd64", "x86_64": "linux-amd64", "arm64": "linux-arm64"} {
		bin := t.TempDir()
		os.WriteFile(filepath.Join(bin, "uname"), []byte("#!/bin/sh\ncase $1 in -s) echo Linux;; -m) echo "+m+";; esac\n"), 0o755)
		// The stand-in for linux is a script, so it runs here too.
		code, out, errs := launch(t, script, t.TempDir(), "file://"+r.dir, []string{bin}, "get")
		if code != 0 || !strings.Contains(out, "downloaded 1.2.3 "+p+": get") {
			t.Errorf("%s: %d %q %q", m, code, out, errs)
		}
	}
}

// A conch-sandbox on PATH runs when it is the pinned version or newer;
// an older one, a pre-release of it, or one that cannot say, does not.
func TestLauncherPathCopy(t *testing.T) {
	r := makeRelease(t, "1.2.3")
	script := pinned(t, "1.2.3", r.sums)
	for have, used := range map[string]bool{
		"1.2.3": true, "v1.2.3": true, "1.2.4": true, "1.10.0": true, "2.0.0": true, "1.3": true, "1.2.4-dev": true,
		"1.2.2": false, "1.2.3-dev": false, "0.9.9": false, "1.2": false, "dev": false, "": false, "1..3": false, "x1.2.3": false,
	} {
		code, out, errs := launch(t, script, t.TempDir(), "file://"+r.dir, []string{pathCopy(t, have)}, "get", "--json")
		if code != 0 {
			t.Errorf("%q: %d %q", have, code, errs)
			continue
		}
		if got := out == "path copy: get --json\n"; got != used {
			t.Errorf("%q: used %v (%q)", have, got, out)
		}
	}
	// A pre-release pinned is met by itself and by its release.
	pre := makeRelease(t, "1.3.0-rc1")
	script = pinned(t, "1.3.0-rc1", pre.sums)
	for have, used := range map[string]bool{"1.3.0-rc1": true, "1.3.0": true, "1.3.0-rc2": false, "1.2.9": false} {
		_, out, _ := launch(t, script, t.TempDir(), "file://"+pre.dir, []string{pathCopy(t, have)}, "get")
		if got := strings.HasPrefix(out, "path copy"); got != used {
			t.Errorf("%q for 1.3.0-rc1: used %v", have, got)
		}
	}
}

// --fetch fetches and checks, says where, and runs nothing — even with a
// copy on PATH that would do.
func TestLauncherFetchOnly(t *testing.T) {
	r := makeRelease(t, "1.2.3")
	script := pinned(t, "1.2.3", r.sums)
	home := t.TempDir()
	code, out, errs := launch(t, script, home, "file://"+r.dir, []string{pathCopy(t, "9.9.9")}, "--fetch")
	if code != 0 || out != cached(home, "1.2.3")+"\n" || strings.Contains(out, "downloaded") {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	if _, err := os.Stat(cached(home, "1.2.3")); err != nil {
		t.Fatal(err)
	}
	// Again: nothing to fetch.
	if code, out, errs := launch(t, script, home, "file:///nonexistent", nil, "--fetch"); code != 0 || out != cached(home, "1.2.3")+"\n" || strings.Contains(errs, "fetching") {
		t.Fatalf("again: %d %q %q", code, out, errs)
	}
}

// Over HTTP, as from the release, on loopback: $CONCH_PORT where it is
// given (a port this worktree may use), else any.
func TestLauncherOverHTTP(t *testing.T) {
	r := makeRelease(t, "1.2.3")
	addr := "127.0.0.1:0"
	if p := os.Getenv("CONCH_PORT"); p != "" {
		addr = "127.0.0.1:" + p
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		ln, err = net.Listen("tcp", "127.0.0.1:0")
	}
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.FileServer(http.Dir(r.dir))}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	url := "http://" + ln.Addr().String()
	script := pinned(t, "1.2.3", r.sums)
	if code, out, errs := launch(t, script, t.TempDir(), url, nil, "get"); code != 0 || !strings.Contains(out, "downloaded 1.2.3") {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	if code, _, errs := launch(t, pinned(t, "1.2.9", r.sums), t.TempDir(), url, nil, "get"); code != 1 || !strings.Contains(errs, "could not fetch "+url+"/conch-sandbox-1.2.9-") {
		t.Fatalf("404: %d %q", code, errs)
	}
}

// The release script builds every platform, sums them and pins the
// launcher — run here with a go that writes stand-ins, so nothing is
// compiled; the launcher it pins then fetches from what it made.
func TestSandboxRelease(t *testing.T) {
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "go"), []byte(`#!/bin/sh
args="$*" out=
while [ $# -gt 0 ]; do [ "$1" = -o ] && out=$2; shift; done
printf '#!/bin/sh\necho "built %s %s cgo=%s: $*"\n' "$GOOS" "$GOARCH" "$CGO_ENABLED" > "$out"
chmod 755 "$out"
echo "$GOOS/$GOARCH" >> "$(dirname "$out")/go.log"
echo "$args" >> "$(dirname "$out")/args.log"
`), 0o755)
	dist := t.TempDir()
	os.WriteFile(filepath.Join(dist, "conch_1.2.3_linux_amd64.tar.gz"), []byte("conch's"), 0o644)
	cmd := exec.Command("/bin/sh", filepath.Join("..", "..", "scripts", "sandbox-release.sh"), "v1.2.3", dist)
	cmd.Env = append(os.Environ(), "GO="+filepath.Join(bin, "go"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	if b, _ := os.ReadFile(filepath.Join(dist, "conch_1.2.3_linux_amd64.tar.gz")); string(b) != "conch's" {
		t.Fatal("conch's own archive went")
	}
	if b, _ := os.ReadFile(filepath.Join(dist, "go.log")); string(b) != "darwin/arm64\ndarwin/amd64\nlinux/arm64\nlinux/amd64\n" {
		t.Fatalf("built %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dist, "args.log")); strings.Count(string(b), "-X main.version=1.2.3 ") != 4 || strings.Count(string(b), "./cmd/conch-sandbox") != 4 {
		t.Fatalf("go was asked %q", b)
	}
	sums, _ := os.ReadFile(filepath.Join(dist, "SHA256SUMS"))
	launcher, _ := os.ReadFile(filepath.Join(dist, "conch-sandbox.sh"))
	if !strings.Contains(string(launcher), "\nwant=1.2.3\n") {
		t.Fatalf("not pinned:\n%s", launcher)
	}
	lines := strings.Split(strings.TrimSpace(string(sums)), "\n")
	if len(lines) != 4 {
		t.Fatalf("SHA256SUMS:\n%s", sums)
	}
	for _, l := range lines {
		f := strings.Fields(l)
		b, err := os.ReadFile(filepath.Join(dist, f[1]))
		sum := sha256.Sum256(b)
		if err != nil || hex.EncodeToString(sum[:]) != f[0] {
			t.Fatalf("%s: %v", l, err)
		}
		p := strings.TrimPrefix(f[1], "conch-sandbox-1.2.3-")
		if !strings.Contains(string(launcher), "\nsha_"+strings.ReplaceAll(p, "-", "_")+"="+f[0]+"\n") {
			t.Fatalf("%s not pinned:\n%s", p, launcher)
		}
	}
	// What it pinned fetches what it built, and the version went in.
	code, out, errs := launch(t, filepath.Join(dist, "conch-sandbox.sh"), t.TempDir(), "file://"+dist, nil, "get")
	if code != 0 || !strings.Contains(out, "built "+runtime.GOOS+" "+runtime.GOARCH+" cgo=0: get") {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	// A version that could break the sed is refused.
	cmd = exec.Command("/bin/sh", filepath.Join("..", "..", "scripts", "sandbox-release.sh"), "1.2/3", dist)
	cmd.Env = append(os.Environ(), "GO="+filepath.Join(bin, "go"))
	if err := cmd.Run(); err == nil {
		t.Fatal("1.2/3 taken")
	}
}
