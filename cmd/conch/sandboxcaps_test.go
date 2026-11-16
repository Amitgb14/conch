package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/integration"
	"github.com/Amitgb14/conch/internal/sandbox"
)

// installWithCapabilities installs a provider file from testdata as the
// registry would, its record holding caps.
func installWithCapabilities(t *testing.T, name, api string, caps *integration.Capabilities) {
	t.Helper()
	installProviderFile(t, name, api)
	data, err := os.ReadFile(sandbox.ProviderFilePath(name))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sandbox.InstallRegistryFile(name, data, caps); err != nil {
		t.Fatal(err)
	}
}

// A verb the provider says it cannot do is refused before anything is
// asked of it; one it says nothing about is left to its file.
func TestSandboxVerbsFollowCapabilities(t *testing.T) {
	a4Env(t)
	t.Setenv("BOAT_API_KEY", "k")
	installWithCapabilities(t, "boat", "http://127.0.0.1:1/api/v1", &integration.Capabilities{
		Operations: []string{"create", "get", "list", "stop", "delete", "snapshot_list"}})
	for verb, want := range map[string]string{
		"start":    "boat.dev cannot start a sandbox: its file has no start",
		"preview":  "cannot give a link to a port",
		"url":      "cannot give a link to a port",
		"snapshot": "cannot keep snapshots",
		"usage":    "cannot say what a sandbox has cost",
	} {
		err := runSandbox([]string{"-provider", "boat", verb, "x"})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", verb, err)
		}
	}
	// What it can do goes on to the provider (which is not there).
	if err := sandboxCan("boat", "stop"); err != nil {
		t.Errorf("stop: %v", err)
	}
	if err := sandboxCan("boat", "ls"); err != nil {
		t.Errorf("ls, which needs nothing: %v", err)
	}
	// A provider with no file says nothing.
	if err := sandboxCan("nobody", "start"); err != nil {
		t.Errorf("no file: %v", err)
	}

	// providers -json and settings -json carry them, and Can follows.
	out, _, err := a4Out(t, func() error { return runSandbox([]string{"providers", "-json"}) })
	var ps []providerJSON
	if err != nil || json.Unmarshal([]byte(out), &ps) != nil || len(ps) != 1 || ps[0].Capabilities.Can("start") || !ps[0].Capabilities.Can("stop") {
		t.Fatalf("providers -json: %s %v", out, err)
	}
	out, _, err = a4Out(t, func() error { return runSandbox([]string{"-provider", "boat", "settings", "-json"}) })
	var st sandboxSettingsJSON
	if err != nil || json.Unmarshal([]byte(out), &st) != nil {
		t.Fatalf("settings -json: %s %v", out, err)
	}
	if st.Capabilities == nil || st.Can.Snapshots || st.Can.Preview || st.Can.Usage {
		t.Errorf("boat's file has snapshots, previews and usage, its capabilities none: %+v", st.Can)
	}
	// info's does follows them too.
	if ops := sandboxOps("boat"); len(ops) != 0 {
		t.Errorf("sandboxOps: %v", ops)
	}
}

// Installed with no record — a file of one's own — its own file says.
func TestSandboxCapabilitiesFromTheFile(t *testing.T) {
	a4Env(t)
	installProviderFile(t, "boat", "http://127.0.0.1:1/api/v1")
	out, _, err := a4Out(t, func() error { return runSandbox([]string{"providers", "-json"}) })
	var ps []providerJSON
	if err != nil || json.Unmarshal([]byte(out), &ps) != nil || len(ps) != 1 {
		t.Fatalf("providers -json: %s %v", out, err)
	}
	c := ps[0].Capabilities
	if !c.Can("start") || !c.Can("snapshot_save") || !c.Can("preview") || !c.Can("usage") || c.Size != "names" {
		t.Errorf("boat from its file: %+v", c)
	}
	if ops := sandboxOps("boat"); strings.Join(ops, ",") != "snapshot,preview,usage,settings" {
		t.Errorf("sandboxOps: %v", ops)
	}
}
