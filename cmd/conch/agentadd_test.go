package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/adapter"
	"github.com/Amitgb14/conch/internal/config"
)

func runAgentAdd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var err error
	out, _ := a4Capture(t, "", func() { err = agentAdd(args) })
	return out, err
}

// `conch agent add` writes the manifest for an agent conch has never heard
// of, so nobody has to learn the format to try one. What it writes has to
// be a manifest the adapter package will actually load — which this test
// holds it to by loading it.
func TestAgentAddWritesAManifestThatLoads(t *testing.T) {
	dir := a4Env(t)
	out, err := runAgentAdd(t, "robo", "-command", "robo", "-label", "Robo Coder",
		"-prompt", "--message {prompt}", "-resume", "--resume {id}", "-resume-last", "--continue",
		"-flags", "--terminal", "-dir", "~/.robo/bin", "-env", "ROBO_UI=plain")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(adapter.ManifestDir(config.Dir()), "robo.toml")
	if !strings.Contains(out, path) {
		t.Errorf("it does not say where it wrote: %q", out)
	}
	if !strings.Contains(out, "runs here") || !strings.Contains(out, "reload") {
		t.Errorf("it does not say what happens next: %q", out)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Detection has something to go on even when nobody wrote rules: the
	// program conch starts is the program it looks for.
	if !strings.Contains(string(b), `process_names = ["robo"]`) {
		t.Errorf("no process name:\n%s", b)
	}

	r, err := adapter.New("/usr/local/bin/conch", dir)
	if err != nil {
		t.Fatal(err)
	}
	a, ok := r.Get("robo")
	if !ok {
		t.Fatalf("the manifest it wrote does not load:\n%s", b)
	}
	if a.Label() != "Robo Coder" || a.Tier() != adapter.TierRunsHere {
		t.Errorf("label %q tier %q", a.Label(), a.Tier())
	}
	if got := a.PromptArgs("hello"); got != "--message hello" {
		t.Errorf("prompt %q", got)
	}
	if got := a.ResumeArgs("s1"); got != "--resume s1" {
		t.Errorf("resume %q", got)
	}
	if line := strings.Join(a.Command("/bin/zsh", ""), " "); !strings.Contains(line, "--terminal") {
		t.Errorf("flags %q", line)
	}
}

// The ways of asking for one that must be refused, each with its reason —
// and none of them leaving a file behind.
func TestAgentAddRefusals(t *testing.T) {
	dir := a4Env(t)
	manifests := adapter.ManifestDir(config.Dir())

	for _, c := range []struct {
		what string
		args []string
		says string
	}{
		{"no name", []string{"-command", "x"}, "usage"},
		{"no command", []string{"robo"}, "usage"},
		{"a name that could not be one", []string{"Robo!", "-command", "x"}, "not an agent name"},
		{"a supported agent", []string{"claude", "-command", "my-claude"}, "supported agent"},
	} {
		if _, err := runAgentAdd(t, c.args...); err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: %v", c.what, err)
		}
	}
	if files, _ := filepath.Glob(filepath.Join(manifests, "*.toml")); len(files) != 0 {
		t.Fatalf("a refused add left files: %v", files)
	}

	// Writing one twice is refused unless asked twice: a manifest somebody
	// has edited by hand is not overwritten by a repeated command.
	if _, err := runAgentAdd(t, "robo", "-command", "robo"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(manifests, "robo.toml")
	if err := os.WriteFile(path, []byte("agent = \"robo\"\n# edited by hand\n\n[run]\nbinary = \"robo\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runAgentAdd(t, "robo", "-command", "robo"); err == nil || !strings.Contains(err.Error(), "-force") {
		t.Fatalf("a second add: %v", err)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), "edited by hand") {
		t.Fatal("it overwrote a manifest without being asked twice")
	}
	if _, err := runAgentAdd(t, "robo", "-command", "robo", "-force"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); strings.Contains(string(b), "edited by hand") {
		t.Fatal("-force did not overwrite")
	}
	_ = dir
}

// An agent that cannot be resumed says so when it is added, rather than
// leaving somebody to find out from an empty Sessions view.
func TestAgentAddSaysWhatItWillNotDo(t *testing.T) {
	a4Env(t)
	out, err := runAgentAdd(t, "bare", "-command", "bare")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "not resumed") || !strings.Contains(out, "rather than pretending") {
		t.Errorf("it does not say what it will not do: %q", out)
	}
	out, err = runAgentAdd(t, "resumable", "-command", "resumable", "-resume", "-r {id}")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "not resumed") {
		t.Errorf("it said an agent that resumes does not: %q", out)
	}
}
