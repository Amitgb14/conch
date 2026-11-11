package server

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// fakeCloudAgent answers on a socket as conch cloud agent does, with env
// for every ask, and counts the asks.
func fakeCloudAgent(t *testing.T, env map[string]string, hang bool) (socket string, asks *atomic.Int32) {
	socket, asks, _ = fakeCloudAgentKeys(t, env, hang)
	return socket, asks
}

// fakeCloudAgentKeys is fakeCloudAgent with the key of each ask.
func fakeCloudAgentKeys(t *testing.T, env map[string]string, hang bool) (socket string, asks *atomic.Int32, keys chan string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "ca")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket = filepath.Join(dir, "a.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	asks = new(atomic.Int32)
	keys = make(chan string, 16)
	go func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer nc.Close()
				line, err := bufio.NewReader(nc).ReadBytes('\n')
				if err != nil {
					return
				}
				var ask proto.CloudAgentAsk
				json.Unmarshal(line, &ask)
				asks.Add(1)
				select {
				case keys <- ask.Key:
				default:
				}
				if hang {
					time.Sleep(5 * time.Second)
					return
				}
				b, _ := json.Marshal(proto.CloudAgentAnswer{Env: env})
				nc.Write(append(b, '\n'))
			}()
		}
	}()
	return socket, asks, keys
}

// An agent started on a server with a cloud agent is given the account's
// env secrets; a shell is not; and the capability lasts as long as the
// cloud agent's connection.
func TestCloudAgentGivesAgentsTheirSecrets(t *testing.T) {
	s, p := afterFixture(t)
	if slices.Contains(s.capabilities(), proto.CapCloudAgent) {
		t.Fatal("cloud.agent.v1 claimed with no cloud agent")
	}
	for _, c := range proto.BuildCapabilities() {
		if !slices.Contains(s.capabilities(), c) {
			t.Errorf("without a cloud agent it lacks %s, and so looks like an older build", c)
		}
	}
	socket, asks, keys := fakeCloudAgentKeys(t, map[string]string{"DB_URL": "postgres://secret", "bad name": "x", "OWN": "cloud",
		"CONCH_PANE_ID": "p999", "CONCH_SOCKET": "/elsewhere", "LD_PRELOAD": "/tmp/evil.so", "DYLD_INSERT_LIBRARIES": "/tmp/evil.dylib",
		"PATH": "/evil", "BASH_ENV": "/tmp/evil.sh", "NODE_OPTIONS": "--require /tmp/evil.js", "CONCHY": "fine"}, false)
	owner := &client{}
	if perr := s.cloud.register(owner, proto.CloudAgentRegisterParams{Socket: socket, Key: "k3y"}); perr != nil {
		t.Fatal(perr)
	}
	if !slices.Contains(s.capabilities(), proto.CapCloudAgent) {
		t.Fatal("cloud.agent.v1 not claimed once registered")
	}

	run := func(agent string, env []string) string {
		t.Helper()
		out := filepath.Join(t.TempDir(), "env")
		info, perr := s.create(proto.PaneCreateParams{Agent: agent, Env: env,
			Command: []string{"/bin/sh", "-c", "env > " + out + "; sleep 30"}, Cwd: p.root, Cols: 80, Rows: 24})
		if perr != nil {
			t.Fatal(perr)
		}
		t.Cleanup(func() { s.close(info.ID) })
		a5WaitFor(t, "the pane's environment", func() bool { b, _ := os.ReadFile(out); return strings.Contains(string(b), "PATH=") })
		b, _ := os.ReadFile(out)
		return string(b)
	}
	env := run("claude", []string{"OWN=mine"})
	if !strings.Contains(env, "DB_URL=postgres://secret\n") {
		t.Fatalf("the agent was not given the secret:\n%s", env)
	}
	if strings.Contains(env, "bad name") {
		t.Fatal("a name no shell can carry was passed")
	}
	if k := <-keys; k != "k3y" {
		t.Fatalf("asked with key %q, not the registration's", k)
	}
	// conch's own variables, and those that load code or move where a
	// program looks, are not a secret's to set.
	for _, bad := range []string{"CONCH_PANE_ID=p999", "CONCH_SOCKET=/elsewhere", "LD_PRELOAD=", "DYLD_INSERT_LIBRARIES=", "PATH=/evil", "BASH_ENV=", "NODE_OPTIONS="} {
		if strings.Contains(env, bad) {
			t.Errorf("a secret set %s", bad)
		}
	}
	if !strings.Contains(env, "CONCHY=fine\n") {
		t.Error("a name merely starting like conch's was dropped")
	}
	if !strings.Contains(env, "OWN=mine\n") || strings.Contains(env, "OWN=cloud") {
		t.Fatal("the caller's own variable did not win")
	}
	if asks.Load() != 1 {
		t.Fatalf("asked %d times for one agent", asks.Load())
	}
	if shell := run("", nil); strings.Contains(shell, "DB_URL") || asks.Load() != 1 {
		t.Fatalf("a shell was given the secrets (asked %d times)", asks.Load())
	}

	// Another connection going changes nothing; the owner's does.
	s.cloud.forget(&client{})
	if !s.cloud.registered() {
		t.Fatal("dropped by somebody else's connection")
	}
	s.cloud.forget(owner)
	if slices.Contains(s.capabilities(), proto.CapCloudAgent) || s.cloud.env() != nil {
		t.Fatal("still registered after its connection went")
	}
}

func TestCloudAgentRegisterRefusesARelativeSocket(t *testing.T) {
	var ca cloudAgent
	for _, bad := range []string{"", "a.sock", "./x/a.sock"} {
		if perr := ca.register(&client{}, proto.CloudAgentRegisterParams{Socket: bad}); perr == nil {
			t.Errorf("%q: registered", bad)
		}
	}
	if ca.registered() {
		t.Fatal("registered after refusals")
	}
}

// A cloud agent that is gone, or does not answer, holds an agent's start
// up for a moment at most: it starts without the secrets.
func TestCloudAgentThatDoesNotAnswer(t *testing.T) {
	was := cloudAskWait
	cloudAskWait = 200 * time.Millisecond
	t.Cleanup(func() { cloudAskWait = was })

	var ca cloudAgent
	socket, asks := fakeCloudAgent(t, nil, true)
	ca.register(&client{}, proto.CloudAgentRegisterParams{Socket: socket})
	start := time.Now()
	if env := ca.env(); env != nil {
		t.Fatalf("given %v", env)
	}
	if took := time.Since(start); took > 2*time.Second || asks.Load() != 1 {
		t.Fatalf("took %s, asked %d", took, asks.Load())
	}
	ca = cloudAgent{}
	ca.register(&client{}, proto.CloudAgentRegisterParams{Socket: filepath.Join(t.TempDir(), "gone.sock")})
	if env := ca.env(); env != nil {
		t.Fatalf("gone: given %v", env)
	}
	// An answer with an error says why and gives nothing.
	dir, _ := os.MkdirTemp("", "ca")
	defer os.RemoveAll(dir)
	errSock := filepath.Join(dir, "e.sock")
	ln, _ := net.Listen("unix", errSock)
	defer ln.Close()
	go func() {
		nc, err := ln.Accept()
		if err != nil {
			return
		}
		bufio.NewReader(nc).ReadBytes('\n')
		nc.Write([]byte(`{"error":"the control plane is down","env":{"A":"1"}}` + "\n"))
		nc.Close()
	}()
	ca = cloudAgent{}
	ca.register(&client{}, proto.CloudAgentRegisterParams{Socket: errSock})
	if env := ca.env(); env != nil {
		t.Fatalf("with an error: given %v", env)
	}
}

// Whoever registers decides what every agent's environment holds, so a
// registration is never taken from inside a pane, nor over a live one —
// unless by root, as the real cloud agent runs, over a user's.
func TestCloudAgentRegisterIsGuarded(t *testing.T) {
	p := proto.CloudAgentRegisterParams{Socket: "/run/conch-cloud/agent.sock"}
	var ca cloudAgent
	for name, c := range map[string]*client{
		"a shell's pane":           {pane: "p1"},
		"a pane, as root":          {pane: "p1", root: true},
		"acting for another agent": {actFor: proto.ActForParams{ID: "busybox:p2"}},
	} {
		if perr := ca.register(c, p); perr == nil || perr.Code != proto.ErrOutOfScope {
			t.Errorf("%s: %v", name, perr)
		}
	}
	if ca.registered() {
		t.Fatal("registered from a pane")
	}

	first, second := &client{}, &client{}
	if perr := ca.register(first, p); perr != nil {
		t.Fatal(perr)
	}
	if perr := ca.register(first, proto.CloudAgentRegisterParams{Socket: "/run/b.sock", Key: "k"}); perr != nil {
		t.Fatalf("the holder registering again: %v", perr)
	}
	if perr := ca.register(second, p); perr == nil || perr.Code != proto.ErrCloudAgentHeld {
		t.Fatalf("over a live registration: %v", perr)
	}
	if ca.socket != "/run/b.sock" || ca.key != "k" {
		t.Fatalf("changed by a refused register: %q %q", ca.socket, ca.key)
	}
	root := &client{root: true}
	if perr := ca.register(root, p); perr != nil {
		t.Fatalf("root over a user's: %v", perr)
	}
	ca.forget(first) // the one replaced going changes nothing
	if ca.owner != root {
		t.Fatal("dropped when the replaced connection went")
	}
	if perr := ca.register(&client{root: true}, p); perr == nil || perr.Code != proto.ErrCloudAgentHeld {
		t.Fatalf("root over root's: %v", perr)
	}
	if perr := ca.register(second, p); perr == nil {
		t.Fatal("a user's over root's")
	}
	ca.forget(root)
	if perr := ca.register(second, p); perr != nil {
		t.Fatalf("once it is gone: %v", perr)
	}
}

// Through the server, a connection from inside a pane is refused too.
func TestCloudAgentRegisterFromAPaneThroughDispatch(t *testing.T) {
	s, _ := afterFixture(t)
	msg := proto.Message{Method: proto.MethodCloudAgentRegister, Params: proto.Marshal(proto.CloudAgentRegisterParams{Socket: "/run/a.sock"})}
	if _, perr := s.dispatch(&client{pane: "p1", subs: map[string]*subscription{}}, msg); perr == nil || perr.Code != proto.ErrOutOfScope {
		t.Fatalf("from a pane: %v", perr)
	}
	if s.cloud.registered() {
		t.Fatal("registered from a pane")
	}
}

// peerUID is the user at the other end, which is how root is told apart.
func TestPeerUID(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("conch asks the kernel only on linux and darwin")
	}
	dir, err := os.MkdirTemp("", "uid")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	ln, err := net.Listen("unix", filepath.Join(dir, "s"))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		if nc, err := net.Dial("unix", filepath.Join(dir, "s")); err == nil {
			time.Sleep(time.Second)
			nc.Close()
		}
	}()
	nc, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	if got := peerUID(nc); got != os.Getuid() {
		t.Fatalf("peer uid %d, want %d", got, os.Getuid())
	}
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	if got := peerUID(a); got != -1 {
		t.Fatalf("not a unix socket: %d", got)
	}
}
