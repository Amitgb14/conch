package adapter

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// agyLine is the shell line conch starts Antigravity CLI with, given
// what config.toml says.
func agyLine(t *testing.T, configTOML string) []string {
	t.Helper()
	// The manifest's ~/.local/bin is expanded when it is read: a scratch
	// home, so no line can reach a real agy.
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	if configTOML != "" {
		if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(configTOML), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	reg, err := New("/conch", dir)
	if err != nil {
		t.Fatal(err)
	}
	a, ok := reg.Get("antigravity")
	if !ok {
		t.Fatal("no antigravity")
	}
	return a.Command("/bin/sh", "-i 'hi'")
}

// A Gemini API key is named, never kept: the line refers to the variable
// the person keeps it in, which their login shell fills in, and the
// provider is switched for this launch alone with --settings.
func TestAntigravityAPIKeyEnv(t *testing.T) {
	plain := agyLine(t, "")
	if strings.Contains(plain[2], "GEMINI_API_KEY") || strings.Contains(plain[2], "--settings") {
		t.Fatalf("no key asked for, none given: %q", plain[2])
	}
	line := agyLine(t, "[agents]\nantigravity_api_key_env = \"MY_GEMINI_KEY\"\n")[2]
	for _, want := range []string{`GEMINI_API_KEY="$MY_GEMINI_KEY"`, `exec agy --settings '{"modelProvider":"gemini"}' -i 'hi'`} {
		if !strings.Contains(line, want) {
			t.Errorf("missing %q in %q", want, line)
		}
	}
	// A name that is not a variable's is not put in a shell line.
	for _, bad := range []string{"MY KEY", "X;rm -rf ~", "$(id)", "1ST", "A-B"} {
		if l := agyLine(t, "[agents]\nantigravity_api_key_env = \""+bad+"\"\n")[2]; strings.Contains(l, "GEMINI_API_KEY") || strings.Contains(l, bad) {
			t.Errorf("%q made it into the line: %q", bad, l)
		}
	}
}

// Run for real by a shell, with a stand-in agy: the key reaches it from
// the variable named, and a variable that is empty stops the launch with
// its name said, before agy can start without a key.
func TestAntigravityAPIKeyEnvRuns(t *testing.T) {
	bin := t.TempDir()
	agy := filepath.Join(bin, "agy")
	if err := os.WriteFile(agy, []byte("#!/bin/sh\necho \"key=$GEMINI_API_KEY args=$*\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	line := agyLine(t, "[agents]\nantigravity_api_key_env = \"MY_GEMINI_KEY\"\n")
	run := func(key string) (string, error) {
		cmd := exec.Command(line[0], line[1:]...)
		cmd.Env = []string{"PATH=" + bin + ":/usr/bin:/bin", "HOME=" + os.Getenv("HOME"), "MY_GEMINI_KEY=" + key}
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	out, err := run("k-123")
	if err != nil || strings.TrimSpace(out) != `key=k-123 args=--settings {"modelProvider":"gemini"} -i hi` {
		t.Fatalf("%q %v", out, err)
	}
	out, err = run("")
	if err == nil || !strings.Contains(out, "MY_GEMINI_KEY") || strings.Contains(out, "key=") {
		t.Fatalf("an empty key started it: %q %v", out, err)
	}
}
