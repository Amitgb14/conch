package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func keyHome(t *testing.T, text string) {
	t.Helper()
	t.Setenv("CONCH_HOME", t.TempDir())
	if text != "" {
		if err := os.WriteFile(File(), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// One key set is one line changed: the person's comments and the rest of
// the file stay as they wrote them.
func TestSetSandboxKeyKeepsTheFile(t *testing.T) {
	for _, c := range []struct{ name, before, after string }{
		{"no file", "", "[sandbox.daytona]\napi_key = \"k-new\"\n"},
		{"a table for it, with a key",
			"# mine\n[ui] # hi\n\n[sandbox.daytona]  # my sandboxes\n  api_key = \"old\" # rotate monthly\nregion = \"us\"\n\n[sandbox.boat]\napi_key = \"b\"\n",
			"# mine\n[ui] # hi\n\n[sandbox.daytona]  # my sandboxes\n  api_key = \"k-new\"\nregion = \"us\"\n\n[sandbox.boat]\napi_key = \"b\"\n"},
		{"a table without a key",
			"[sandbox.daytona]\nregion = \"us\"\n# end\n",
			"[sandbox.daytona]\napi_key = \"k-new\"\nregion = \"us\"\n# end\n"},
		{"no table", "# mine\n[ui]\n", "# mine\n[ui]\n\n[sandbox.daytona]\napi_key = \"k-new\"\n"},
		{"no newline at the end", "# mine", "# mine\n\n[sandbox.daytona]\napi_key = \"k-new\"\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			keyHome(t, c.before)
			if err := SetSandboxKey("daytona", "k-new"); err != nil {
				t.Fatal(err)
			}
			b, _ := os.ReadFile(File())
			if string(b) != c.after {
				t.Fatalf("got:\n%s\nwant:\n%s", b, c.after)
			}
			cfg, err := Load()
			if err != nil || cfg.Sandbox["daytona"].APIKey != "k-new" {
				t.Fatalf("%+v %v", cfg.Sandbox, err)
			}
			if st, _ := os.Stat(File()); st.Mode().Perm() != 0o600 {
				t.Fatalf("mode %v", st.Mode())
			}
		})
	}
}

// A shape a line edit cannot be sure of — an inline table, dotted keys —
// is written as Save writes it, which still sets the key.
func TestSetSandboxKeyFallsBack(t *testing.T) {
	for _, before := range []string{
		"[sandbox]\ndaytona = { api_key = \"old\", region = \"us\" }\n",
		"sandbox.daytona.api_key = \"old\"\n",
		"[sandbox.daytona]\napi_key = '''\nold'''\n",
	} {
		keyHome(t, before)
		if err := SetSandboxKey("daytona", "k-new"); err != nil {
			t.Fatalf("%q: %v", before, err)
		}
		cfg, err := Load()
		if err != nil || cfg.Sandbox["daytona"].APIKey != "k-new" {
			t.Fatalf("%q: %+v %v", before, cfg.Sandbox, err)
		}
		if before[0] == '[' && strings.Contains(before, "region") && cfg.Sandbox["daytona"].Region != "us" {
			t.Fatalf("%q lost the region", before)
		}
	}
}

func TestSetSandboxKeyRefuses(t *testing.T) {
	keyHome(t, "[ui\n")
	if err := SetSandboxKey("daytona", "k"); err == nil {
		t.Fatal("wrote over a file it could not read")
	}
	if b, _ := os.ReadFile(File()); string(b) != "[ui\n" {
		t.Fatalf("%q", b)
	}
	if _, err := os.Stat(filepath.Join(Dir(), "config.toml.invalid")); err == nil {
		t.Fatal("moved aside")
	}
}
