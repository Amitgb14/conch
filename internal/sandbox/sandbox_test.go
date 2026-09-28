package sandbox

import (
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/config"
)

func TestEnvFrom(t *testing.T) {
	t.Setenv("SB_A", "1")
	t.Setenv("SB_EMPTY", "")
	got, err := EnvFrom([]string{"SB_A", " SB_A "})
	if err != nil || len(got) != 1 || got["SB_A"] != "1" {
		t.Fatalf("got %v %v", got, err)
	}
	if got, err := EnvFrom(nil); err != nil || len(got) != 0 {
		t.Fatalf("none: %v %v", got, err)
	}
	for name, want := range map[string]string{
		"SB_EMPTY": "$SB_EMPTY isn't set here", "SB_NEVER_SET_ANYWHERE": "isn't set here",
		"": "give a variable name", "A=B": "give a variable name", "A B": "give a variable name",
	} {
		if _, err := EnvFrom([]string{name}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v, want %q", name, err, want)
		}
	}
}

func TestOpenAndKnown(t *testing.T) {
	if !Known("daytona") || Known("e2b") || Known("") {
		t.Fatal("Known")
	}
	if p, err := Open("daytona", config.SandboxCfg{}); err != nil || p.Name() != "daytona" {
		t.Fatalf("open daytona: %v %v", p, err)
	}
	if _, err := Open("e2b", config.SandboxCfg{}); err == nil || !strings.Contains(err.Error(), `unknown sandbox provider "e2b"`) {
		t.Fatalf("unknown: %v", err)
	}
}

// What a state means, what a period says it was doing, and what a provider
// is called: small answers the tree leans on.
func TestStatesLabelsAndPeriods(t *testing.T) {
	for _, c := range []struct {
		state  State
		moving bool
	}{
		{StateStarting, true}, {StateStopping, true}, {"archiving", true}, {"resizing", true},
		{"snapshotting", true}, {"forking", true}, {"pausing", true}, {"creating", true},
		{StateStarted, false}, {StateStopped, false}, {StateArchived, false}, {StateError, false}, {"", false},
	} {
		if got := c.state.Moving(); got != c.moving {
			t.Errorf("%q moving = %v, want %v", c.state, got, c.moving)
		}
	}
	// A period was running when it was charged for a machine, not just disk.
	for _, c := range []struct {
		p    UsagePeriod
		want bool
	}{
		{UsagePeriod{CPU: 1}, true}, {UsagePeriod{MemGiB: 2}, true}, {UsagePeriod{CPU: 4, MemGiB: 8}, true},
		{UsagePeriod{DiskGiB: 3}, false}, {UsagePeriod{}, false},
	} {
		if got := c.p.Running(); got != c.want {
			t.Errorf("%+v running = %v, want %v", c.p, got, c.want)
		}
	}
	// Providers are named as people write them, and an unknown one is
	// capitalised rather than left bare.
	for name, want := range map[string]string{
		"daytona": "Daytona", "boat": "boat.dev", "e2b": "E2B", "fly": "Fly", "": "",
	} {
		if got := ProviderLabel(name); got != want {
			t.Errorf("ProviderLabel(%q) = %q, want %q", name, got, want)
		}
	}
	// boat's machines come in sizes, and those are the sizes create takes.
	sizes := NewBoat(config.ProviderCfg{}).SizeNames()
	if strings.Join(sizes, ",") != "small,default,large" {
		t.Fatalf("boat's sizes: %v", sizes)
	}
	sizes[0] = "tampered"
	if again := NewBoat(config.ProviderCfg{}).SizeNames(); again[0] != "small" {
		t.Fatalf("the list is not a copy: %v", again)
	}
}
