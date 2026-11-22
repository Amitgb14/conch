package main

import (
	"context"
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
	"syscall"
	"testing"
	"time"
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
		body := fmt.Sprintf("#!/bin/sh\necho \"downloaded %s %s: $*\"\n[ \"${1:-}\" = stdin ] && cat\n[ \"${1:-}\" = env ] && env\nexit 0\n", version, p)
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

// launcherRun is one run of the launcher with everything said: how sh is
// started, its environment and its stdin. A watchdog kills its whole
// process group if it runs on — a launcher that calls itself would.
type launcherRun struct {
	argv  []string // sh and its arguments
	env   []string
	stdin string
	limit time.Duration // the watchdog's; 0 is 30s
}

func (r launcherRun) do(t *testing.T) (int, string, string) {
	t.Helper()
	limit := r.limit
	if limit == 0 {
		limit = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(t.Context(), limit)
	defer cancel()
	cmd := exec.Command(r.argv[0], r.argv[1:]...)
	cmd.Env, cmd.Stdin = r.env, strings.NewReader(r.stdin)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var err error
	select {
	case err = <-done:
	case <-ctx.Done():
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-done
		t.Fatalf("still running after %v (%v): %q %q", limit, r.argv, out.String(), errb.String())
	}
	// Anything it left in its group goes too.
	syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if ee.ExitCode() < 0 {
			t.Logf("%v: %v", r.argv, ee)
		}
		return ee.ExitCode(), out.String(), errb.String()
	} else if err != nil {
		t.Fatal(err)
	}
	return 0, out.String(), errb.String()
}

func launcherEnv(home, url string, path ...string) []string {
	return []string{"HOME=" + home, "PATH=" + strings.Join(append(path, "/usr/bin", "/bin"), ":"), "CONCH_SANDBOX_URL=" + url}
}

// The launcher itself on PATH as conch-sandbox — copied there, or a
// package that installed it under that name — asks itself its version
// once, not for ever: it fetches and runs the pinned build.
func TestLauncherOnPathIsNotAForkBomb(t *testing.T) {
	r := makeRelease(t, "1.2.3")
	script := pinned(t, "1.2.3", r.sums)
	onPath := t.TempDir()
	b, _ := os.ReadFile(script)
	os.WriteFile(filepath.Join(onPath, "conch-sandbox"), b, 0o755)
	for _, run := range []launcherRun{
		{argv: []string{"/bin/sh", script, "get", "--json"}},
		{argv: []string{filepath.Join(onPath, "conch-sandbox"), "get", "--json"}},
	} {
		// A launcher calling itself makes hundreds of processes a second:
		// the watchdog is short, and kills the whole group.
		home := t.TempDir()
		run.env, run.limit = launcherEnv(home, "file://"+r.dir, onPath), 5*time.Second
		code, out, errs := run.do(t)
		if code != 0 || out != "downloaded 1.2.3 "+hostPlatform()+": get --json\n" {
			t.Fatalf("%v: %d %q %q", run.argv, code, out, errs)
		}
	}
}

// What stops it: a launcher started under another (CONCH_SANDBOX_LAUNCHER
// set) asks no conch-sandbox on PATH, however new, and one that asks
// passes the mark on to what it runs.
func TestLauncherGuard(t *testing.T) {
	r := makeRelease(t, "1.2.3")
	script := pinned(t, "1.2.3", r.sums)
	newer := t.TempDir()
	os.WriteFile(filepath.Join(newer, "conch-sandbox"), []byte("#!/bin/sh\n[ \"$1\" = version ] && { echo 9.9.9; exit 0; }\necho \"path copy [$CONCH_SANDBOX_LAUNCHER]\"\n"), 0o755)
	run := launcherRun{argv: []string{"/bin/sh", script, "get"}, env: append(launcherEnv(t.TempDir(), "file://"+r.dir, newer), "CONCH_SANDBOX_LAUNCHER=1")}
	if code, out, errs := run.do(t); code != 0 || out != "downloaded 1.2.3 "+hostPlatform()+": get\n" {
		t.Fatalf("nested: %d %q %q", code, out, errs)
	}
	run.env = launcherEnv(t.TempDir(), "file://"+r.dir, newer)
	if code, out, errs := run.do(t); code != 0 || !strings.HasPrefix(out, "path copy [") || out == "path copy []\n" {
		t.Fatalf("not nested: %d %q %q", code, out, errs)
	}
}

// Piped into sh -s, as Orca's recipes run it: a conch-sandbox on PATH
// that reads stdin when asked its version takes none of the script, and
// the launcher goes on to the pinned build. From a file, the program gets
// the launcher's stdin (key set reads the key there).
func TestLauncherFromStdin(t *testing.T) {
	r := makeRelease(t, "1.2.3")
	script := pinned(t, "1.2.3", r.sums)
	b, _ := os.ReadFile(script)
	greedy := t.TempDir()
	os.WriteFile(filepath.Join(greedy, "conch-sandbox"), []byte("#!/bin/sh\ncat >/dev/null\necho 0.0.1\n"), 0o755)
	shells := []string{"/bin/sh"}
	if d, err := exec.LookPath("dash"); err == nil {
		shells = append(shells, d)
	}
	for _, sh := range shells {
		for _, path := range [][]string{nil, {greedy}} {
			home := t.TempDir()
			run := launcherRun{argv: []string{sh, "-s", "--", "get", "--json", "a b"}, env: launcherEnv(home, "file://"+r.dir, path...), stdin: string(b)}
			code, out, errs := run.do(t)
			if code != 0 || out != "downloaded 1.2.3 "+hostPlatform()+": get --json a b\n" {
				t.Errorf("%s -s, PATH %v: %d %q %q", sh, path, code, out, errs)
			}
		}
		// The program's stdin, from a file and through sh -s.
		run := launcherRun{argv: []string{sh, script, "stdin"}, env: launcherEnv(t.TempDir(), "file://"+r.dir), stdin: "the key\n"}
		if code, out, errs := run.do(t); code != 0 || !strings.HasSuffix(out, ": stdin\nthe key\n") {
			t.Errorf("%s, stdin through: %d %q %q", sh, code, out, errs)
		}
		run = launcherRun{argv: []string{sh, "-s", "--", "stdin"}, env: launcherEnv(t.TempDir(), "file://"+r.dir), stdin: string(b)}
		if code, out, errs := run.do(t); code != 0 || out != "downloaded 1.2.3 "+hostPlatform()+": stdin\n" {
			t.Errorf("%s -s, stdin at its end: %d %q %q", sh, code, out, errs)
		}
	}
	// A conch-sandbox on PATH new enough gets the stdin too.
	reader := t.TempDir()
	os.WriteFile(filepath.Join(reader, "conch-sandbox"), []byte("#!/bin/sh\n[ \"$1\" = version ] && { echo 9.9.9; exit 0; }\necho \"path copy: $*\"; cat\n"), 0o755)
	run := launcherRun{argv: []string{"/bin/sh", script, "key", "set", "p"}, env: launcherEnv(t.TempDir(), "file:///nonexistent", reader), stdin: "k-1\n"}
	if code, out, errs := run.do(t); code != 0 || out != "path copy: key set p\nk-1\n" {
		t.Fatalf("path copy's stdin: %d %q %q", code, out, errs)
	}
}

// The kept copy's folder is the user's alone, whatever the umask: nobody
// else can swap the program between its check and its run.
func TestLauncherKeepsItPrivate(t *testing.T) {
	r := makeRelease(t, "1.2.3")
	script := pinned(t, "1.2.3", r.sums)
	home := t.TempDir()
	run := launcherRun{argv: []string{"/bin/sh", "-c", `umask 0; exec /bin/sh "$0" "$@"`, script, "--fetch"}, env: launcherEnv(home, "file://"+r.dir)}
	if code, _, errs := run.do(t); code != 0 {
		t.Fatalf("%q", errs)
	}
	dir := filepath.Dir(cached(home, "1.2.3"))
	if st, _ := os.Stat(dir); st.Mode().Perm() != 0o700 {
		t.Fatalf("folder %v", st.Mode())
	}
	if st, _ := os.Stat(cached(home, "1.2.3")); st.Mode().Perm() != 0o755 {
		t.Fatalf("program %v", st.Mode())
	}
	// conch-sandbox's own folder, which holds the version's, is closed too:
	// whoever could write it could swap the version's folder.
	if st, _ := os.Stat(filepath.Dir(dir)); st.Mode().Perm() != 0o700 {
		t.Errorf("%s made %v", filepath.Dir(dir), st.Mode())
	}
	// What holds them, made here too, is made as the umask says (0 here).
	for _, p := range []string{filepath.Join(home, ".local"), filepath.Join(home, ".local", "share")} {
		if st, _ := os.Stat(p); st.Mode().Perm() != 0o777 {
			t.Errorf("%s made %v, not as the umask says", p, st.Mode())
		}
	}
	// Both made open before are closed again.
	os.Chmod(dir, 0o777)
	os.Chmod(filepath.Dir(dir), 0o777)
	if code, _, errs := run.do(t); code != 0 {
		t.Fatalf("%q", errs)
	}
	for _, p := range []string{dir, filepath.Dir(dir)} {
		if st, _ := os.Stat(p); st.Mode().Perm() != 0o700 {
			t.Fatalf("%s left %v", p, st.Mode())
		}
	}
}

// Without curl and wget, or without a sha256 tool, it says which it
// lacks rather than blaming the network or the build.
func TestLauncherSaysWhatItLacks(t *testing.T) {
	r := makeRelease(t, "1.2.3")
	script := pinned(t, "1.2.3", r.sums)
	tools := func(names ...string) string {
		dir := t.TempDir()
		for _, n := range names {
			for _, d := range []string{"/usr/bin", "/bin"} {
				if _, err := os.Stat(filepath.Join(d, n)); err == nil {
					os.Symlink(filepath.Join(d, n), filepath.Join(dir, n))
					break
				}
			}
		}
		return dir
	}
	base := []string{"sh", "uname", "mkdir", "chmod", "mktemp", "mv", "rm", "cut", "cat", "env", "perl"}
	for _, c := range []struct {
		have []string
		want string
	}{
		{append(base, "sha256sum", "shasum"), "needs curl or wget to fetch conch-sandbox-1.2.3-"},
		{append(base, "curl", "wget"), "needs sha256sum or shasum"},
	} {
		run := launcherRun{argv: []string{"/bin/sh", script, "get"},
			env: []string{"HOME=" + t.TempDir(), "PATH=" + tools(c.have...), "CONCH_SANDBOX_URL=file://" + r.dir}}
		code, out, errs := run.do(t)
		if code != 1 || out != "" || !strings.Contains(errs, c.want) || strings.Contains(errs, "not the build") || strings.Contains(errs, "could not fetch") {
			t.Errorf("%v: %d %q %q", c.have, code, out, errs)
		}
	}
}

// The guard against a launcher asking itself its version is for the
// launcher alone: the program it runs, and what that starts (herdr, ssh,
// git), are not given it.
func TestLauncherGuardStaysWithTheLauncher(t *testing.T) {
	r := makeRelease(t, "1.2.3")
	script := pinned(t, "1.2.3", r.sums)
	for _, extra := range [][]string{nil, {"CONCH_SANDBOX_LAUNCHER=1"}} {
		run := launcherRun{argv: []string{"/bin/sh", script, "env"}, env: append(launcherEnv(t.TempDir(), "file://"+r.dir), extra...)}
		code, out, errs := run.do(t)
		if code != 0 || !strings.Contains(out, "HOME=") || strings.Contains(out, "CONCH_SANDBOX_LAUNCHER") {
			t.Fatalf("%v: %d %q %q", extra, code, out, errs)
		}
	}
}
