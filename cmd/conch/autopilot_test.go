package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
)

// conch autopilot push hands the server the policy config.toml's [verify]
// says, as the TUI does; an older server without autopilot is told
// nothing and said so; a config that does not read sends nothing.
func TestAutopilotPush(t *testing.T) {
	dir := a4Env(t)
	toml := "[verify]\nauto_fix = 2\nauto_merge = true\n[verify.commands]\nr1 = \"go test ./...\"\nr2 = \"  \"\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := startA4Server(t, config.SocketPath())
	out, _ := a4Capture(t, "", func() {
		if err := runAutopilot([]string{"push"}); err != nil {
			t.Error(err)
		}
	})
	var sent []proto.AutopilotPolicy
	srv.mu.Lock()
	for _, c := range srv.calls {
		if c.Method == proto.MethodAutopilotSet {
			var p proto.AutopilotPolicy
			json.Unmarshal(c.Params, &p) //nolint:errcheck
			sent = append(sent, p)
		}
	}
	srv.mu.Unlock()
	if len(sent) != 1 || sent[0].AutoFix != 2 || !sent[0].AutoMerge || sent[0].Checks["r1"] != "go test ./..." || len(sent[0].Checks) != 1 {
		t.Fatalf("sent %+v", sent)
	}
	if !strings.Contains(out, "auto_fix 2") || !strings.Contains(out, "1 check") {
		t.Fatalf("said %q", out)
	}

	// An older server: nothing sent, and said.
	srv.Hello = func(n int) proto.HelloResult {
		h := currentHello(n)
		h.Capabilities = nil
		return h
	}
	out, _ = a4Capture(t, "", func() { _ = runAutopilot([]string{"push"}) })
	if !strings.Contains(out, "no autopilot") {
		t.Fatalf("older server: %q", out)
	}

	if err := runAutopilot(nil); err == nil || !strings.Contains(err.Error(), "autopilot push") {
		t.Fatalf("no subcommand: %v", err)
	}
	os.WriteFile(filepath.Join(dir, "config.toml"), []byte("[verify\nbroken"), 0o600) //nolint:errcheck
	if err := runAutopilot([]string{"push"}); err == nil || !strings.Contains(err.Error(), "nothing sent") {
		t.Fatalf("broken config: %v", err)
	}
}
