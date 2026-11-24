package adapter

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// claudeSettings reads conch's claude-settings.json back as a map.
func claudeSettings(t *testing.T, dir string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "claude-settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("%v: %s", err, b)
	}
	return m
}

func writeConfig(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func idleRegistry(t *testing.T, config string) (Registry, string) {
	t.Helper()
	t.Setenv("GEMINI_CLI_SYSTEM_DEFAULTS_PATH", "")
	t.Setenv("OPENCODE_CONFIG_CONTENT", "")
	dir := filepath.Join(t.TempDir(), "conch")
	if config != "" {
		writeConfig(t, dir, config)
	}
	reg, err := New("/usr/local/bin/conch", dir)
	if err != nil {
		t.Fatal(err)
	}
	return reg, dir
}

func TestClaudeIdleCompaction(t *testing.T) {
	cases := []struct {
		name, config string
		want         any // nil: the key is left out, Claude's default
	}{
		{"no config.toml", "", nil},
		{"key unset", "[agents]\ndefault = \"claude\"\n", nil},
		{"off", "[agents]\nclaude_idle_compaction = false\n", false},
		{"on", "[agents]\nclaude_idle_compaction = true\n", true},
		// A file that does not parse says nothing about this key: Claude's
		// default, and the hooks are still written.
		{"malformed", "[agents\nclaude_idle_compaction = false\n", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, dir := idleRegistry(t, c.config)
			m := claudeSettings(t, dir)
			got, ok := m["idleCompaction"]
			if c.want == nil {
				if ok {
					t.Fatalf("idleCompaction written: %v", got)
				}
			} else if got != c.want {
				t.Fatalf("idleCompaction = %v, want %v", got, c.want)
			}
			if m["hooks"] == nil || m["statusLine"] == nil {
				t.Fatalf("hooks or status line lost: %v", m)
			}
		})
	}
}

// The setting is read again before each launch, so a change in Settings
// reaches the next Claude without restarting the server.
func TestClaudeIdleCompactionFollowsTheSetting(t *testing.T) {
	reg, dir := idleRegistry(t, "")
	claude, _ := reg.Get("claude")
	pr := claude.(Preparer)
	steps := []struct {
		config string
		want   any
	}{
		{"[agents]\nclaude_idle_compaction = false\n", false},
		{"[agents]\nclaude_idle_compaction = true\n", true},
		{"[agents]\n", nil},
	}
	for _, s := range steps {
		writeConfig(t, dir, s.config)
		if err := pr.Ensure(); err != nil {
			t.Fatal(err)
		}
		got, ok := claudeSettings(t, dir)["idleCompaction"]
		if s.want == nil && ok || s.want != nil && got != s.want {
			t.Fatalf("after %q: idleCompaction = %v (%v), want %v", s.config, got, ok, s.want)
		}
	}
}
