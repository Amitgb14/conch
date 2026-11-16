package sandbox

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/config"
)

// conch keeps no provider of its own: the ones it had are files from the
// registry. A machine made by the old built-in still opens — its provider's
// file is installed the first time it is needed — and nothing lists a
// provider that is not installed.
func TestFormerBuiltinsAreFiles(t *testing.T) {
	t.Setenv("CONCH_HOME", t.TempDir())
	r := newFakeRegistry(t)
	data, err := os.ReadFile(filepath.Join("testdata", "registry", "daytona.toml"))
	if err != nil {
		t.Fatal(err)
	}
	e := r.publish("daytona", string(data))
	e.Format = 4
	r.index.Providers[0] = e

	if got := Names(); len(got) != 0 {
		t.Fatalf("nothing installed lists %v", got)
	}
	if got := Offered(); len(got) != 0 {
		t.Fatalf("nothing installed offers %v", got)
	}
	for _, n := range []string{"daytona", "boat", "tensorlake"} {
		if !Known(n) {
			t.Errorf("%s: a machine conch's own provider made must still open", n)
		}
	}
	if Known("devin") {
		t.Fatal("Devin Cloud is dropped")
	}

	p, err := Open("daytona", config.SandboxCfg{"daytona": {Target: "eu"}})
	if err != nil {
		t.Fatalf("open, installing the file: %v", err)
	}
	c, ok := p.(*Custom)
	if !ok || c.region != "eu" || c.File().Format != 4 {
		t.Fatalf("opened %T %+v", p, c)
	}
	if got := Names(); !slices.Equal(got, []string{"daytona"}) || ProviderLabel("daytona") != "Daytona" {
		t.Fatalf("installed: %v %q", got, ProviderLabel("daytona"))
	}
	inst, _ := ReadInstalled()
	if _, ok := inst["daytona"]; !ok {
		t.Fatal("the install is recorded, so update and remove know it")
	}
	// Opened again, it is the installed file: the registry is not asked.
	hits := r.hits["providers/daytona.toml"]
	if _, err := Open("daytona", config.SandboxCfg{}); err != nil || r.hits["providers/daytona.toml"] != hits {
		t.Fatalf("second open: %v (%d fetches)", err, r.hits["providers/daytona.toml"]-hits)
	}
}

// A former built-in whose file cannot be had says how to get it, and
// Devin Cloud says it is gone rather than "unknown".
func TestFormerBuiltinsWithoutTheRegistry(t *testing.T) {
	t.Setenv("CONCH_HOME", t.TempDir())
	newFakeRegistry(t) // an empty index
	_, err := Open("boat", config.SandboxCfg{})
	if err == nil || !strings.Contains(err.Error(), "conch sandbox provider install boat") {
		t.Fatalf("boat with no file anywhere: %v", err)
	}
	_, err = Open("devin", config.SandboxCfg{})
	if err == nil || !strings.Contains(err.Error(), "Devin Cloud") || !errors.Is(err, ErrDropped) {
		t.Fatalf("devin: %v", err)
	}
	if _, err := Open("nosuch", config.SandboxCfg{}); err == nil || !strings.Contains(err.Error(), "unknown sandbox provider") {
		t.Fatalf("an unknown one: %v", err)
	}
}

// A test never reaches the real registry, whichever package it is in and
// whether or not it set an address: opening a former built-in installs its
// file from the registry, so any test that opens one would otherwise.
func TestRegistryIsNeverRealInATest(t *testing.T) {
	t.Setenv("CONCH_PROVIDERS_URL", "")
	if got := RegistryURL(); strings.Contains(got, "github.io") {
		t.Fatalf("a test reads %s", got)
	}
}
