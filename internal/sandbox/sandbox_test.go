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
