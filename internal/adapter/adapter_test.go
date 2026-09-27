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
	if strings.Join(names, ",") != "claude,codex,gemini,opencode,devin" {
		t.Fatalf("registry order: %v", names)
	}

	claude, _ := reg.Get("claude")
	if got := claude.PromptArgs("fix it's tests"); got != `'fix it'\''s tests'` {
		t.Fatalf("claude prompt args: %s", got)
	}
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
	for name, want := range map[string][2]string{
		"claude":   {"--resume 'a b'", "--continue"},
		"codex":    {"resume 'a b'", "resume --last"},
		"gemini":   {"--resume 'a b'", "--resume latest"},
		"opencode": {"--session 'a b'", "--continue"},
		"devin":    {"-r 'a b'", "-c"},
	} {
		ad, _ := reg.Get(name)
		if got := [2]string{ad.ResumeArgs("a b"), ad.ResumeArgs("")}; got != want {
			t.Errorf("%s resume args: %q, want %q", name, got, want)
		}
	}
	if got := opencode.PromptArgs("add tests"); got != "--prompt 'add tests'" {
		t.Fatalf("opencode prompt args: %s", got)
	}
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

// The files an agent is launched with are written again before each
// launch. They live in conch's config folder, and that folder can go —
// somebody clearing ~/.config in a sandbox — after which Claude started
// with a --settings file that is no longer there fails with "Settings file
// not found".
func TestEnsureWritesTheFilesAgain(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "conch")
	reg, err := New("/usr/local/bin/conch", dir)
	if err != nil {
		t.Fatal(err)
	}
	files := []string{"claude-settings.json", "gemini-defaults.json", "opencode-conch.js"}
	before := map[string][]byte{}
	for _, name := range files {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("%s was not written at all: %v", name, err)
		}
		before[name] = b
	}

	// The whole folder goes, as ~/.config going takes it.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"claude", "gemini", "opencode"} {
		ad, ok := reg.Get(name)
		if !ok {
			t.Fatalf("no %s adapter", name)
		}
		pr, ok := ad.(Preparer)
		if !ok {
			t.Fatalf("%s cannot write its files again", name)
		}
		if err := pr.Ensure(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	for _, name := range files {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("%s did not come back: %v", name, err)
		}
		if string(b) != string(before[name]) {
			t.Fatalf("%s came back different:\n%s", name, b)
		}
	}
	// The folder is the user's own, not the world's.
	st, err := os.Stat(dir)
	if err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("the folder came back as %v (%v)", st.Mode().Perm(), err)
	}
	// An agent with nothing of its own to write says so by doing nothing.
	if ad, ok := reg.Get("codex"); ok {
		if pr, ok := ad.(Preparer); ok {
			if err := pr.Ensure(); err != nil {
				t.Fatalf("codex: %v", err)
			}
		}
	}
}
