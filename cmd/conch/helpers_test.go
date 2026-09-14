package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// TestMain lets this test binary stand in for other programs:
//   - A4_HELPER_MODE=main runs conch's main() with the given arguments, so
//     exit codes and os.Exit paths can be checked in a subprocess;
//   - A4_HELPER_MODE=bridge is the remote end of a fake ssh (`conch bridge`);
//   - `server` as the first argument is what client.EnsureServer runs when
//     no server answers. Tests must never start a detached server, so it
//     fails at once.
func TestMain(m *testing.M) {
	switch os.Getenv("A4_HELPER_MODE") {
	case "main":
		os.Args = append([]string{"conch"}, os.Args[1:]...)
		main()
		os.Exit(0)
	case "bridge":
		nc, err := net.Dial("unix", os.Getenv("A4_BRIDGE_SOCK"))
		if err != nil {
			fmt.Fprintln(os.Stderr, "bridge dial:", err)
			os.Exit(1)
		}
		go func() { _, _ = io.Copy(os.Stdout, nc); os.Exit(0) }()
		_, _ = io.Copy(nc, os.Stdin)
		os.Exit(0)
	case "":
	default:
		os.Exit(3)
	}
	if len(os.Args) > 1 && os.Args[1] == "server" {
		fmt.Fprintln(os.Stderr, "a4: refusing to start a detached server from a test")
		os.Exit(3)
	}
	os.Exit(m.Run())
}

// a4Env isolates a test: temp HOME and CONCH_HOME, no inherited conch
// socket, pane or machine, a fake gh, and no -m flag.
func a4Env(t *testing.T) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	dir, err := os.MkdirTemp("", "a4")
	if err != nil {
		t.Fatal(err)
	}
	dir, _ = filepath.EvalSymlinks(dir)
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Setenv("CONCH_HOME", dir)
	t.Setenv("CONCH_SOCKET", filepath.Join(dir, "s.sock")) // nothing listens yet
	t.Setenv("CONCH_PANE_ID", "")
	t.Setenv("CONCH_MACHINE", "")
	t.Setenv("CONCH_SSH_CONFIG", "")
	t.Setenv("CONCH_REMOTE_BINARY", "")
	t.Setenv("CONCH_RELEASE_URL", "http://127.0.0.1:1/unused")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("A4_HELPER_MODE", "")
	gh := filepath.Join(dir, "gh")
	os.WriteFile(gh, []byte("#!/bin/sh\nexit 1\n"), 0o755)
	t.Setenv("CONCH_GH", gh)
	tmp, err := os.MkdirTemp("/tmp", "a4") // short: ssh control socket dir
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(tmp) })
	t.Setenv("TMPDIR", tmp)
	old := machineFlag
	machineFlag = ""
	t.Cleanup(func() { machineFlag = old })
	return dir
}

// a4Capture runs fn with stdin fed from input and returns what it wrote to
// stdout and stderr.
func a4Capture(t *testing.T, input string, fn func()) (string, string) {
	t.Helper()
	oldIn, oldOut, oldErr := os.Stdin, os.Stdout, os.Stderr
	inR, inW, _ := os.Pipe()
	outR, outW, _ := os.Pipe()
	errR, errW, _ := os.Pipe()
	go func() { io.WriteString(inW, input); inW.Close() }()
	var wg sync.WaitGroup
	var stdout, stderr strings.Builder
	wg.Add(2)
	go func() { defer wg.Done(); io.Copy(&stdout, outR) }()
	go func() { defer wg.Done(); io.Copy(&stderr, errR) }()
	os.Stdin, os.Stdout, os.Stderr = inR, outW, errW
	var once sync.Once
	restore := func() {
		once.Do(func() {
			os.Stdin, os.Stdout, os.Stderr = oldIn, oldOut, oldErr
			outW.Close()
			errW.Close()
			wg.Wait()
			inR.Close()
		})
	}
	defer restore() // also when fn calls t.Fatal
	fn()
	restore()
	return stdout.String(), stderr.String()
}

// a4Var is a value shared between a test and its fake server's goroutines.
type a4Var[T any] struct {
	mu sync.Mutex
	v  T
}

func newA4Var[T any](v T) *a4Var[T] { return &a4Var[T]{v: v} }

func (a *a4Var[T]) Get() T {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.v
}

func (a *a4Var[T]) Set(v T) {
	a.mu.Lock()
	a.v = v
	a.mu.Unlock()
}

// a4Call is one request a fake server received.
type a4Call struct {
	Method string
	Params json.RawMessage
}

// a4Server is a scripted conch server on a unix socket.
type a4Server struct {
	sock string
	ln   net.Listener

	mu      sync.Mutex
	calls   []a4Call
	conns   []net.Conn
	hellos  int
	stopped bool

	// Hello answers the handshake; n counts handshakes from 1.
	Hello func(n int) proto.HelloResult
	// Handle answers other requests. A nil result with a nil error replies
	// {}. conn may be used to push events.
	Handle func(msg proto.Message, conn *proto.Conn) (any, *proto.Error)
}

func currentHello(n int) proto.HelloResult {
	return proto.HelloResult{Version: proto.Version, Protocol: proto.ProtocolVersion, Capabilities: proto.Capabilities,
		PID: 1000, Started: time.Unix(1700000000, 0), Platform: "linux/amd64", Hostname: "fakehost", Build: "fakebuild"}
}

func startA4Server(t *testing.T, sock string) *a4Server {
	t.Helper()
	if sock == "" {
		dir, err := os.MkdirTemp("", "a4s")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.RemoveAll(dir) })
		sock = filepath.Join(dir, "s.sock")
	}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	s := &a4Server{sock: sock, ln: ln, Hello: currentHello}
	var wg sync.WaitGroup
	t.Cleanup(func() {
		s.stop()
		wg.Wait()
	})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			s.mu.Lock()
			s.conns = append(s.conns, nc)
			s.mu.Unlock()
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer nc.Close()
				s.serve(nc)
			}()
		}
	}()
	return s
}

func (s *a4Server) serve(nc net.Conn) {
	conn := proto.NewConn(nc)
	for {
		msg, err := conn.Read()
		if err != nil {
			return
		}
		s.mu.Lock()
		s.calls = append(s.calls, a4Call{msg.Method, msg.Params})
		hello, handle := s.Hello, s.Handle
		if msg.Method == proto.MethodHello {
			s.hellos++
		}
		n := s.hellos
		s.mu.Unlock()

		var result any
		var perr *proto.Error
		switch {
		case msg.Method == proto.MethodHello:
			result = hello(n)
		case handle != nil:
			result, perr = handle(msg, conn)
		}
		if msg.Method == proto.MethodServerStop && perr == nil {
			if msg.ID != "" {
				conn.Write(proto.Message{ID: msg.ID, Result: proto.Marshal(struct{}{})})
			}
			s.stop()
			return
		}
		if msg.ID == "" {
			continue
		}
		reply := proto.Message{ID: msg.ID, Error: perr}
		if perr == nil {
			if result == nil {
				result = struct{}{}
			}
			reply.Result = proto.Marshal(result)
		}
		if conn.Write(reply) != nil {
			return
		}
	}
}

func (s *a4Server) stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return
	}
	s.stopped = true
	s.ln.Close()
	for _, c := range s.conns {
		c.Close()
	}
}

func (s *a4Server) setHandle(h func(msg proto.Message, conn *proto.Conn) (any, *proto.Error)) {
	s.mu.Lock()
	s.Handle = h
	s.mu.Unlock()
}

func (s *a4Server) setHello(h func(n int) proto.HelloResult) {
	s.mu.Lock()
	s.Hello = h
	s.mu.Unlock()
}

// methods lists the requested methods other than hello.
func (s *a4Server) methods() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, c := range s.calls {
		if c.Method != proto.MethodHello {
			out = append(out, c.Method)
		}
	}
	return out
}

// params decodes the params of the last call to method into v.
func (s *a4Server) params(t *testing.T, method string, v any) bool {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.calls) - 1; i >= 0; i-- {
		if s.calls[i].Method == method {
			if err := json.Unmarshal(s.calls[i].Params, v); err != nil {
				t.Fatalf("%s params: %v", method, err)
			}
			return true
		}
	}
	return false
}

// fakeSSH writes an ssh stand-in that logs argv and acts on the remote
// script: probe, install, or bridge to A4_BRIDGE_SOCK through this binary.
const a4SSHScript = `#!/bin/sh
for a in "$@"; do printf '%s\n' "$a"; done >> "$A4_SSH_LOG"
printf '===\n' >> "$A4_SSH_LOG"
for a in "$@"; do last=$a; done
case "$last" in
*"uname -s"*)
  if [ -n "$A4_PROBE_FAIL" ]; then echo "$A4_PROBE_FAIL" >&2; exit 255; fi
  if [ -s "$A4_INSTALLED" ] && [ -f "$A4_PROBE_AFTER" ]; then /bin/cat "$A4_PROBE_AFTER"; else /bin/cat "$A4_PROBE"; fi ;;
*conch.new*)
  /bin/cat > "$A4_INSTALLED"; echo "/home/dev/.local/bin/conch" ;;
*" bridge")
  A4_HELPER_MODE=bridge exec "$A4_HELPER" ;;
*)
  exit 1 ;;
esac
`

type a4SSH struct{ dir, log string }

func newA4SSH(t *testing.T) *a4SSH {
	t.Helper()
	dir := t.TempDir()
	f := &a4SSH{dir: dir, log: filepath.Join(dir, "argv.log")}
	path := filepath.Join(dir, "ssh")
	if err := os.WriteFile(path, []byte(a4SSHScript), 0o755); err != nil {
		t.Fatal(err)
	}
	exe, _ := os.Executable()
	t.Setenv("CONCH_SSH", path)
	t.Setenv("A4_SSH_LOG", f.log)
	t.Setenv("A4_HELPER", exe)
	t.Setenv("A4_PROBE", filepath.Join(dir, "probe.txt"))
	t.Setenv("A4_INSTALLED", filepath.Join(dir, "installed.bin"))
	// Once something is installed, the machine reports what the test put
	// in probe-after.txt (if anything) instead.
	t.Setenv("A4_PROBE_AFTER", filepath.Join(dir, "probe-after.txt"))
	t.Setenv("A4_PROBE_FAIL", "")
	t.Setenv("A4_BRIDGE_SOCK", "")
	return f
}

func (f *a4SSH) setProbe(t *testing.T, s string) {
	t.Helper()
	os.WriteFile(filepath.Join(f.dir, "probe.txt"), []byte(s), 0o600)
}

func (f *a4SSH) setProbeAfterInstall(t *testing.T, s string) {
	t.Helper()
	os.WriteFile(filepath.Join(f.dir, "probe-after.txt"), []byte(s), 0o600)
}

func (f *a4SSH) argv(t *testing.T) string {
	t.Helper()
	b, _ := os.ReadFile(f.log)
	return string(b)
}

// a4RunMain runs conch's main() in a subprocess with args and returns its
// exit code and output.
func a4RunMain(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return a4RunBinary(t, exe, stdin, args...)
}

func a4Command(exe string, args ...string) *exec.Cmd {
	cmd := exec.Command(exe, args...)
	cmd.Env = append(os.Environ(), "A4_HELPER_MODE=main")
	return cmd
}

func a4RunBinary(t *testing.T, exe, stdin string, args ...string) (int, string, string) {
	t.Helper()
	cmd := a4Command(exe, args...)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		if cmd.ProcessState == nil {
			t.Fatal(err)
		}
		code = cmd.ProcessState.ExitCode()
	}
	return code, stdout.String(), stderr.String()
}
