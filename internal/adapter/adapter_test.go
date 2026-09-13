package adapter

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCleanTitle(t *testing.T) {
	for in, want := range map[string]string{
		"✳ Fix the login form":             "Fix the login form",
		"Claude Code":                      "",
		"[ ! ] Action Required agentprobe": "agentprobe",
		"⠙ agentprobe":                     "agentprobe",
		"✋  Action Required (api)":         "",
		"✦  Working… (api)":                "",
		"✦  Reading the test files":        "Reading the test files",
		"◇  Ready (api)":                   "",
		"OC | Add health check":            "Add health check",
		"OpenCode":                         "",
	} {
		if got := CleanTitle(in); got != want {
			t.Errorf("CleanTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRegistryAndIntegrationFiles(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GEMINI_CLI_SYSTEM_DEFAULTS_PATH", "")
	t.Setenv("OPENCODE_CONFIG_CONTENT", "")
	reg, err := New("/opt/conch bin/conch", dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, a := range reg {
		names = append(names, a.Name())
	}
	if strings.Join(names, ",") != "claude,codex,gemini,opencode" {
		t.Fatalf("registry order: %v", names)
	}

	claude, _ := reg.Get("claude")
	if cmd := claude.Command("/bin/zsh", "--model opus"); !strings.Contains(cmd[2], "exec claude --settings") || !strings.HasSuffix(cmd[2], "--model opus") {
		t.Fatalf("claude command: %q", cmd)
	}

	gemini, _ := reg.Get("gemini")
	if env := gemini.Env(); len(env) != 1 || !strings.HasPrefix(env[0], "GEMINI_CLI_SYSTEM_DEFAULTS_PATH=") {
		t.Fatalf("gemini env: %v", env)
	}
	var defaults struct {
		Hooks map[string][]hookGroup `json:"hooks"`
	}
	b, _ := os.ReadFile(filepath.Join(dir, "gemini-defaults.json"))
	if json.Unmarshal(b, &defaults) != nil || !strings.Contains(defaults.Hooks["AfterAgent"][0].Hooks[0].Command, "'/opt/conch bin/conch' report gemini-hook") {
		t.Fatalf("gemini defaults: %s", b)
	}

	opencode, _ := reg.Get("opencode")
	env := opencode.Env()
	if len(env) != 1 || !strings.Contains(env[0], `"plugin":["file://`+dir+`/opencode-conch.js"]`) {
		t.Fatalf("opencode env: %v", env)
	}
	plugin, _ := os.ReadFile(filepath.Join(dir, "opencode-conch.js"))
	if !strings.Contains(string(plugin), `const conch = "/opt/conch bin/conch";`) {
		t.Fatalf("plugin: %s", plugin)
	}

	// An environment the user already set is left alone.
	t.Setenv("OPENCODE_CONFIG_CONTENT", `{"theme":"x"}`)
	reg, _ = New("/conch", t.TempDir())
	if oc, _ := reg.Get("opencode"); len(oc.Env()) != 0 {
		t.Fatalf("displaced the user's OPENCODE_CONFIG_CONTENT: %v", oc.Env())
	}

	// Detection finds a binary in the installer's directory and reads its version.
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, ".opencode", "bin"), 0o755)
	os.WriteFile(filepath.Join(home, ".opencode", "bin", "opencode"), []byte("#!/bin/sh\necho 1.18.16\n"), 0o755)
	t.Setenv("HOME", home)
	t.Setenv("PATH", "/usr/bin:/bin") // not the developer's own installs
	if av := opencode.Detect(context.Background(), "/bin/sh"); !av.Installed || av.Version != "1.18.16" {
		t.Fatalf("detect: %+v", av)
	}
	codex, _ := reg.Get("codex")
	if av := codex.Detect(context.Background(), "/bin/sh"); av.Installed {
		t.Fatalf("codex found in an empty home: %+v", av)
	}
}
