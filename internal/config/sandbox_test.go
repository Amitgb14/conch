package config

import (
	"os"
	"path/filepath"
	"testing"
)

// A config.toml from before provider files said region and image as target
// and snapshot; it still loads, and reads as the new keys. The new keys win
// when both are set, and an empty provider has nothing to say.
func TestSandboxRegionImageFromOldKeys(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CONCH_HOME", home)
	old := "[sandbox.daytona]\ntarget = \"eu\"\nsnapshot = \"conch-dev\"\n\n[sandbox.boat]\nsnapshot = \"large\"\nregion = \"us\"\ntarget = \"eu\"\nimage = \"mine\"\nsize = \"small\"\ncpu = 2\nmemory = 4\ndisk = 10\n"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	d := cfg.Sandbox.Of("daytona")
	if d.RegionOr() != "eu" || d.ImageOr() != "conch-dev" {
		t.Fatalf("daytona: region %q image %q", d.RegionOr(), d.ImageOr())
	}
	b := cfg.Sandbox.Of("boat")
	if b.RegionOr() != "us" || b.ImageOr() != "mine" || b.Size != "small" || b.CPU != 2 || b.Memory != 4 || b.Disk != 10 {
		t.Fatalf("boat: %+v", b)
	}
	var none ProviderCfg
	if none.RegionOr() != "" || none.ImageOr() != "" {
		t.Fatal("an empty provider has a region or image")
	}
}
