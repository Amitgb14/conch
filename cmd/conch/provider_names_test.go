package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A provider's name becomes a path — providers/NAME.toml, the draft's
// folder providers/drafts/NAME — so a name that climbs out of providers/
// is refused before anything is read, written or removed there.
func TestProviderNamesNeverLeaveTheFolder(t *testing.T) {
	home := a4Env(t)
	victim := filepath.Join(home, "victim.toml")  // providers/../victim.toml
	victimDir := filepath.Join(home, "victimdir") // providers/drafts/../../victimdir
	keep := filepath.Join(victimDir, "keep")
	if err := os.WriteFile(victim, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(victimDir, 0o700)
	os.WriteFile(keep, []byte("mine"), 0o600)

	for _, args := range [][]string{
		{"provider", "rm", "../victim"},
		{"provider", "save", "../victim"},
		{"provider", "publish", "-dry-run", "../victim"},
		{"adapt", "-new", "-name", "../../victimdir", "http://127.0.0.1:1/openapi.yaml"},
		{"adapt", "-revise", "fix it", "../../victimdir"},
	} {
		var err error
		a4Capture(t, "", func() { err = runSandbox(args) })
		if err == nil || !strings.Contains(err.Error(), "not a provider name") {
			t.Errorf("%q: %v", args, err)
		}
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("a file outside providers/ was removed: %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("a folder outside providers/ was emptied: %v", err)
	}
}
