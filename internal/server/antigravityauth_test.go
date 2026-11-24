package server_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// agent.status says how Antigravity CLI is signed in: from a Keychain
// that only a stand-in security answers here, and as a Gemini API key
// once conch starts it on one. No other agent is said to be either.
func TestAgentStatusSaysHowAntigravitySignsIn(t *testing.T) {
	sec := filepath.Join(t.TempDir(), "security")
	log := sec + ".log"
	if err := os.WriteFile(sec, []byte("#!/bin/sh\necho \"$*\" >> "+log+"\nexit ${SECURITY_EXIT:-0}\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONCH_SECURITY", sec)
	c, dir := startServerWithAgents(t, map[string]string{
		// The person's own manifest of that name, on a program every
		// machine has, so it is installed whatever this one has.
		"antigravity": "agent = \"antigravity\"\nlabel = \"Antigravity CLI\"\nprocess_names = [\"agy\"]\n\n[run]\nbinary = \"sh\"\n",
	})
	auth := func() map[string]string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		var status proto.AgentStatusResult
		if err := c.Call(ctx, proto.MethodAgentStatus, nil, &status); err != nil {
			t.Fatal(err)
		}
		out := map[string]string{}
		for _, a := range status.Agents {
			if a.Auth != "" {
				out[a.Name] = a.Auth
			}
		}
		return out
	}
	got := auth()
	if runtimeIsDarwin() {
		if len(got) != 1 || got["antigravity"] != "signed-in" {
			t.Fatalf("signed in: %v", got)
		}
		if b, _ := os.ReadFile(log); string(b) != "find-generic-password -s gemini -a antigravity\n" {
			t.Errorf("security was asked %q", b)
		}
	} else if len(got) != 0 {
		t.Fatalf("a keyring conch does not read was answered for: %v", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("[agents]\nantigravity_api_key_env = \"MY_GEMINI_KEY\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := auth(); len(got) != 1 || got["antigravity"] != "api-key" {
		t.Fatalf("api key: %v", got)
	}
}

func runtimeIsDarwin() bool { return runtime.GOOS == "darwin" }
