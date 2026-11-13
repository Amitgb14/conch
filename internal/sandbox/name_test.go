package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckName(t *testing.T) {
	for _, n := range []string{"acme", "ab", "a-1", strings.Repeat("a", 31)} {
		if err := CheckName(n); err != nil {
			t.Errorf("%q: %v", n, err)
		}
	}
	for _, n := range []string{"", "a", "../x", "a/b", "a.b", "..", "Acme", "-a", "1a", "a b", "a\x00", strings.Repeat("a", 32)} {
		if err := CheckName(n); err == nil {
			t.Errorf("%q was taken as a name", n)
		}
	}
}

// The registry's index is somebody else's: an entry named to climb out of
// providers/ is left out, and installing or parsing under such a name
// writes nothing anywhere.
func TestRegistryNamesStayInTheFolder(t *testing.T) {
	t.Setenv("CONCH_HOME", t.TempDir())
	scratch := t.TempDir()
	tmp := filepath.Join(scratch, "tmp")
	os.Mkdir(tmp, 0o700)
	t.Setenv("TMPDIR", tmp) // ParseProviderFile's folder is made in here

	r := newFakeRegistry(t)
	r.publish("acme", registryFile("acme"))
	r.publish("../../evil", registryFile("evil"))
	ix, err := FetchIndex(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ix.Providers) != 1 || ix.Providers[0].Name != "acme" {
		t.Errorf("index %+v", ix.Providers)
	}

	// tmp/conch-providerNNN/../../escaped.toml is scratch/escaped.toml.
	for _, name := range []string{"../../escaped", "../escaped"} {
		if _, _, err := ParseProviderFile(name, []byte(registryFile("escaped"))); err == nil {
			t.Errorf("parse %q", name)
		}
		if _, err := InstallProviderFile(name, []byte(registryFile("escaped"))); err == nil {
			t.Errorf("install %q", name)
		}
	}
	for _, p := range []string{filepath.Join(scratch, "escaped.toml"), filepath.Join(tmp, "escaped.toml")} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s was written", p)
		}
	}
	if m, _ := ReadInstalled(); len(m) != 0 {
		t.Fatalf("recorded %v", m)
	}
}
