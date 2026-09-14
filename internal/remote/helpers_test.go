package remote

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/buildinfo"
	"github.com/Amitgb14/conch/internal/proto"
)

// TestMain doubles as the "remote side" of a fake ssh: when the fake ssh
// script execs this test binary with A4_HELPER_MODE set, it behaves like
// `conch bridge` (or a broken one) instead of running tests.
func TestMain(m *testing.M) {
	switch os.Getenv("A4_HELPER_MODE") {
	case "":
		os.Exit(m.Run())
	case "bridge":
		nc, err := net.Dial("unix", os.Getenv("A4_BRIDGE_SOCK"))
		if err != nil {
			fmt.Fprintln(os.Stderr, "bridge dial:", err)
			os.Exit(1)
		}
		go func() { _, _ = io.Copy(os.Stdout, nc); os.Exit(0) }()
		_, _ = io.Copy(nc, os.Stdin)
		os.Exit(0)
	case "fail":
		fmt.Fprintln(os.Stderr, "dev@box: Permission denied (publickey).")
		os.Exit(255)
	case "hang":
		// Ignores stdin EOF, so bridgeConn.Close has to kill it.
		time.Sleep(30 * time.Second)
		os.Exit(0)
	default:
		os.Exit(3)
	}
}

// a4Env isolates a test from the user's home, config and conch server, and
// resets the ssh config cache (it is process-wide).
func a4Env(t *testing.T) string {
	t.Helper()
	// Not t.TempDir: tests that run the go toolchain leave its telemetry
	// uploader writing under HOME after go exits, which fails TempDir's
	// strict cleanup on Linux.
	home, err := os.MkdirTemp("", "a4home")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	dir, err := os.MkdirTemp("", "a4")
	if err != nil {
		t.Fatal(err)
	}
	dir, _ = filepath.EvalSymlinks(dir)
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Setenv("CONCH_HOME", dir)
	t.Setenv("CONCH_SOCKET", "")
	t.Setenv("CONCH_PANE_ID", "")
	t.Setenv("CONCH_SSH_CONFIG", "")
	t.Setenv("CONCH_REMOTE_BINARY", "")
	t.Setenv("CONCH_RELEASE_URL", "")
	// The ssh control socket dir is derived from TMPDIR and must stay under
	// 40 bytes, so use a short private directory rather than /tmp itself.
	tmp, err := os.MkdirTemp("/tmp", "a4")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(tmp) })
	t.Setenv("TMPDIR", tmp)
	resetSSHConfig()
	t.Cleanup(resetSSHConfig)
	return dir
}

func resetSSHConfig() {
	configOnce.Lock()
	configOnce.path = ""
	configOnce.Unlock()
}

// fakeSSH is the path of an ssh stand-in that logs its argv and acts on
// the remote script it is given.
type fakeSSH struct {
	dir  string
	log  string
	path string
}

const fakeSSHScript = `#!/bin/sh
for a in "$@"; do printf '%s\n' "$a"; done >> "$A4_SSH_LOG"
printf '===\n' >> "$A4_SSH_LOG"
for a in "$@"; do last=$a; done
case "$last" in
*"uname -s"*)
  if [ -n "$A4_PROBE_FAIL" ]; then echo "$A4_PROBE_FAIL" >&2; exit 255; fi
  /bin/cat "$A4_PROBE" ;;
*conch.new*)
  if [ -n "$A4_INSTALL_FAIL" ]; then echo "$A4_INSTALL_FAIL" >&2; exit 1; fi
  /bin/cat > "$A4_INSTALLED"; echo "/home/dev/.local/bin/conch" ;;
*" bridge")
  A4_HELPER_MODE="${A4_BRIDGE_MODE:-bridge}" exec "$A4_HELPER" ;;
*)
  if [ -n "$A4_STDIN_TO" ]; then /bin/cat > "$A4_STDIN_TO"; fi
  printf '%s' "$A4_STDOUT"
  printf '%s' "$A4_STDERR" >&2
  exit "${A4_EXIT:-0}" ;;
esac
`

func newFakeSSH(t *testing.T) *fakeSSH {
	t.Helper()
	dir := t.TempDir()
	f := &fakeSSH{dir: dir, log: filepath.Join(dir, "argv.log"), path: filepath.Join(dir, "ssh")}
	if err := os.WriteFile(f.path, []byte(fakeSSHScript), 0o755); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONCH_SSH", f.path)
	t.Setenv("A4_SSH_LOG", f.log)
	t.Setenv("A4_HELPER", exe)
	t.Setenv("A4_HELPER_MODE", "")
	t.Setenv("A4_BRIDGE_MODE", "")
	t.Setenv("A4_PROBE", filepath.Join(dir, "probe.txt"))
	t.Setenv("A4_INSTALLED", filepath.Join(dir, "installed.bin"))
	for _, k := range []string{"A4_PROBE_FAIL", "A4_INSTALL_FAIL", "A4_STDIN_TO", "A4_STDOUT", "A4_STDERR", "A4_EXIT", "A4_BRIDGE_SOCK"} {
		t.Setenv(k, "")
	}
	return f
}

// setProbe writes what the fake machine reports.
func (f *fakeSSH) setProbe(t *testing.T, s string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.dir, "probe.txt"), []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
}

// calls returns each logged invocation's argv.
func (f *fakeSSH) calls(t *testing.T) [][]string {
	t.Helper()
	b, err := os.ReadFile(f.log)
	if err != nil {
		return nil
	}
	var out [][]string
	for _, block := range strings.Split(string(b), "===\n") {
		if block == "" {
			continue
		}
		out = append(out, strings.Split(strings.TrimSuffix(block, "\n"), "\n"))
	}
	return out
}

// currentProbe is probe output for a machine with this very build installed.
func currentProbe(platform string) string {
	osName, arch, _ := strings.Cut(platform, "/")
	if arch == "amd64" {
		arch = "x86_64"
	}
	info, _ := json.Marshal(buildinfo.Current())
	return fmt.Sprintf("%s\n%s\nbin=/home/dev/.local/bin/conch\n%s\n", osName, arch, info)
}

// fakeServer speaks the conch protocol on a unix socket with a chosen hello.
type fakeServer struct {
	sock    string
	mu      sync.Mutex
	methods []string
}

func startFakeServer(t *testing.T, hello proto.HelloResult) *fakeServer {
	t.Helper()
	dir, err := os.MkdirTemp("", "a4s")
	if err != nil {
		t.Fatal(err)
	}
	fs := &fakeServer{sock: filepath.Join(dir, "s.sock")}
	ln, err := net.Listen("unix", fs.sock)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var conns []net.Conn
	t.Cleanup(func() {
		ln.Close()
		fs.mu.Lock()
		for _, c := range conns {
			c.Close()
		}
		fs.mu.Unlock()
		wg.Wait()
		os.RemoveAll(dir)
	})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			fs.mu.Lock()
			conns = append(conns, nc)
			fs.mu.Unlock()
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer nc.Close()
				conn := proto.NewConn(nc)
				for {
					msg, err := conn.Read()
					if err != nil {
						return
					}
					fs.mu.Lock()
					fs.methods = append(fs.methods, msg.Method)
					fs.mu.Unlock()
					reply := proto.Message{ID: msg.ID}
					if msg.Method == proto.MethodHello {
						reply.Result = proto.Marshal(hello)
					} else {
						reply.Result = proto.Marshal(struct{}{})
					}
					if msg.ID != "" {
						if conn.Write(reply) != nil {
							return
						}
					}
				}
			}()
		}
	}()
	return fs
}
