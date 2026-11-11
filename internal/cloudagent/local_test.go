package cloudagent

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

// TestEnvOnlyForTheServer: the agent's socket is the person's user's, so
// any of that user's processes can reach it; env secrets go only to the
// conch server it registered with, which sends the key it was given.
func TestEnvOnlyForTheServer(t *testing.T) {
	const secret = "s3cr3t-only-for-agents"
	r := newRig(t)
	r.cloud.handle("GET /v1/vm/env", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{"env": map[string]string{"DATABASE_URL": secret}})
	})
	r.startServer()
	r.run()
	r.cloud.waitFor("POST /v1/vm/hello", 1, 10*time.Second)
	for name, key := range map[string]string{"no key": "", "a wrong key": strings.Repeat("0", 64), "a prefix of it": r.agent.serverKey[:8]} {
		ans, err := Ask(r.opts.AgentSocket, proto.CloudAgentAsk{Op: proto.CloudAskEnv, Key: key})
		if err == nil || len(ans.Env) != 0 || !strings.Contains(err.Error(), "registered with") {
			t.Errorf("%s: %+v %v", name, ans, err)
		}
	}
	if n := len(r.cloud.requests("GET /v1/vm/env")); n != 0 {
		t.Fatalf("the control plane was asked %d times for askers without the key", n)
	}
	// The server, which has the key, still gets them for an agent.
	ag := r.pane(proto.PaneCreateParams{Agent: "claude", Command: []string{"/bin/sh", "-c", `printf 'v=%s.\n' "$DATABASE_URL"; exec cat`}})
	r.waitScreen(ag.ID, "v="+secret+".")
	if strings.Contains(r.log.String(), r.agent.serverKey) {
		t.Error("the key is in the agent's log")
	}
}

// TestAgentWaitsOutAnotherRegistration: a connection that registered first
// — a process posing as the cloud agent — keeps the real one out only as
// long as it stays; agents then take secrets from the real one.
func TestAgentWaitsOutAnotherRegistration(t *testing.T) {
	const secret = "real-agent-secret"
	r := newRig(t)
	r.cloud.handle("GET /v1/vm/env", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{"env": map[string]string{"DATABASE_URL": secret}})
	})
	r.startServer()
	imp, err := client.Dial(r.opts.ServerSocket, "impostor")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for range imp.Events {
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := imp.Call(ctx, proto.MethodCloudAgentRegister, proto.CloudAgentRegisterParams{Socket: filepath.Join(r.dir, "imp.sock")}, nil); err != nil {
		t.Fatal(err)
	}
	r.run()
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(r.log.String(), "another cloud agent registered") {
		if time.Now().After(deadline) {
			t.Fatal("the agent never said it was kept out")
		}
		time.Sleep(20 * time.Millisecond)
	}
	imp.Close()
	deadline = time.Now().Add(10 * time.Second)
	for {
		ag := r.pane(proto.PaneCreateParams{Agent: "claude", Command: []string{"/bin/sh", "-c", `printf 'v=%s.\n' "$DATABASE_URL"; exec cat`}})
		if s := r.waitScreen(ag.ID, "v="); strings.Contains(s, "v="+secret+".") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("agents never got the real cloud agent's secrets")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestServerScriptRunsAsTheUser: what starts the server for another user
// makes its directories and log itself — root never opens or chowns a
// path in that user's home, where a symlink could send it anywhere.
// Run here as this user, without the credentials (which need root).
func TestServerScriptRunsAsTheUser(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "fake conch")
	os.WriteFile(exe, []byte("#!/bin/sh\necho \"args=$*\"\necho \"sock=$CONCH_SOCKET\"\necho oops >&2\n"), 0o755)
	home := filepath.Join(dir, "home dir")
	os.Mkdir(home, 0o755)
	sock := filepath.Join(home, ".config", "conch", "conch.sock")
	logPath := filepath.Join(home, ".config", "conch", "logs", "server.log")
	u := &User{Name: "someone", UID: os.Getuid(), GID: os.Getgid(), Home: home, Shell: "/bin/sh"}
	for range 2 { // the second time the directories are there and the log is appended to
		cmd := serverCommand(exe, sock, logPath, u)
		if out, err := cmd.CombinedOutput(); err != nil || len(out) != 0 {
			t.Fatalf("%v: %s", err, out)
		}
	}
	b, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if want := "args=server\nsock=" + sock + "\noops\n"; string(b) != strings.Repeat(want, 2) {
		t.Fatalf("log:\n%s", b)
	}
	for path, mode := range map[string]os.FileMode{logPath: 0o600, filepath.Dir(logPath): 0o700, filepath.Dir(sock): 0o700} {
		if st, err := os.Stat(path); err != nil || st.Mode().Perm() != mode {
			t.Errorf("%s: %v %v, want %v", path, st.Mode(), err, mode)
		}
	}
	// A log that cannot be opened is the script's failure, not a write
	// somewhere else.
	os.Remove(logPath)
	os.Chmod(filepath.Dir(logPath), 0o500)
	defer os.Chmod(filepath.Dir(logPath), 0o700)
	if os.Geteuid() != 0 {
		if err := serverCommand(exe, sock, logPath, u).Run(); err == nil {
			t.Fatal("started with no log to write")
		} else if _, ok := err.(*exec.ExitError); !ok {
			t.Fatal(err)
		}
	}
}
