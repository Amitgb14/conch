package server

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
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
				if hang {
					time.Sleep(5 * time.Second)
					return
				}
				b, _ := json.Marshal(proto.CloudAgentAnswer{Env: env})
				nc.Write(append(b, '\n'))
			}()
		}
	}()
	return socket, asks
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
	socket, asks := fakeCloudAgent(t, map[string]string{"DB_URL": "postgres://secret", "bad name": "x", "OWN": "cloud"}, false)
	owner := &client{}
	if perr := s.cloud.register(owner, socket); perr != nil {
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
		if perr := ca.register(&client{}, bad); perr == nil {
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
	ca.register(&client{}, socket)
	start := time.Now()
	if env := ca.env(); env != nil {
		t.Fatalf("given %v", env)
	}
	if took := time.Since(start); took > 2*time.Second || asks.Load() != 1 {
		t.Fatalf("took %s, asked %d", took, asks.Load())
	}
	ca.register(&client{}, filepath.Join(t.TempDir(), "gone.sock"))
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
	ca.register(&client{}, errSock)
	if env := ca.env(); env != nil {
		t.Fatalf("with an error: given %v", env)
	}
}
