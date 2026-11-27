package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/adapter"
	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/server"
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
		{"a later supported agent", []string{"goose", "-command", "my-goose"}, "supported agent"},
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
	// Nothing was said about a first message or a resume, so it says it
	// will do neither — and does not claim it will give it a first
	// message, which this very line once did.
	for _, want := range []string{"given a first message", "resumed", "read for saved conversations", "rather than pretending"} {
		if !strings.Contains(out, want) {
			t.Errorf("it does not say %q: %q", want, out)
		}
	}
	if strings.Contains(out, "and gives it a first message") {
		t.Errorf("it promised a first message it cannot pass: %q", out)
	}

	// Told how, it says neither of those — only the sessions, which are
	// never read for this tier.
	out, err = runAgentAdd(t, "fuller", "-command", "fuller", "-prompt", "-m {prompt}", "-resume", "-r {id}")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "given a first message") || strings.Contains(out, "nor resumed") {
		t.Errorf("it said an agent that can do both cannot: %q", out)
	}
	if !strings.Contains(out, "read for saved conversations") {
		t.Errorf("it stopped saying the one thing that is always true: %q", out)
	}
}

// The agents add refuses are exactly the ones with an adapter: a list here
// that fell behind the registry would let a manifest take a supported
// agent's name, and the registry would then refuse to load it.
func TestSupportedAgentMatchesTheRegistry(t *testing.T) {
	reg, err := adapter.New("/usr/local/bin/conch", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range reg {
		if got, want := supportedAgent(a.Name()), a.Tier() == adapter.TierSupported; got != want {
			t.Errorf("%s: supportedAgent says %v, the registry's tier is %q", a.Name(), got, a.Tier())
		}
	}
}

// -acp writes an [acp] table the adapter loads as an agent run over the
// protocol, and the detection side reads without screen rules.
func TestAgentAddACP(t *testing.T) {
	dir := a4Env(t)
	out, err := runAgentAdd(t, "speaker", "-acp", "speaker --acp 'a b'", "-label", "Speaker", "-dir", "/opt/s/bin", "-env", "S=1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "runs over ACP") || !strings.Contains(out, "loadSession") {
		t.Errorf("out = %q", out)
	}
	r, err := adapter.New("/usr/local/bin/conch", dir)
	if err != nil {
		t.Fatal(err)
	}
	a, ok := r.Get("speaker")
	if !ok || adapter.Protocol(a) != adapter.ProtocolACP || a.Label() != "Speaker" {
		t.Fatalf("loaded %v as %q", ok, adapter.Protocol(a))
	}
	if line := a.Command("/bin/sh", "")[2]; !strings.Contains(line, "-- speaker --acp 'a b'") || !strings.Contains(line, "/opt/s/bin") {
		t.Errorf("command %q", line)
	}
	for _, bad := range [][]string{
		{"x", "-acp", "x --acp", "-command", "x"},
		{"x", "-acp", "x --acp", "-prompt", "{prompt}"},
		{"x", "-acp", "  "},
	} {
		if _, err := runAgentAdd(t, bad...); err == nil {
			t.Errorf("agent add %q was taken", bad)
		}
	}
	if _, err := os.Stat(filepath.Join(adapter.ManifestDir(config.Dir()), "x.toml")); err == nil {
		t.Error("a refused add left a file")
	}
}

// With a server running, -acp asks it: the agent is offered at once, with
// no reload, and a second add of the name is refused until -force.
func TestAgentAddACPThroughTheServer(t *testing.T) {
	dir := a4Env(t)
	srv := server.New(config.SocketPath(), dir)
	go srv.Run()
	t.Cleanup(func() { srv.StopAndWait() })
	for i := 0; ; i++ {
		if c, err := client.Dial(config.SocketPath(), "test"); err == nil {
			c.Close()
			break
		}
		if i == 200 {
			t.Fatal("no server")
		}
		time.Sleep(20 * time.Millisecond)
	}
	out, err := runAgentAdd(t, "speaker", "-acp", "/bin/sh --acp", "-label", "Speaker")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "in every agent menu now") || strings.Contains(out, "Reload the server") {
		t.Errorf("out = %q", out)
	}
	c, err := client.Dial(config.SocketPath(), "test")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var st proto.AgentStatusResult
	if err := c.Call(t.Context(), proto.MethodAgentStatus, nil, &st); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range st.Agents {
		found = found || a.Name == "speaker" && a.Protocol == proto.ProtocolACP && a.Removable
	}
	if !found {
		t.Error("the server does not offer it")
	}
	if _, err := runAgentAdd(t, "speaker", "-acp", "/bin/sh --acp"); err == nil || !strings.Contains(err.Error(), "-force") {
		t.Errorf("again: %v", err)
	}
	if _, err := runAgentAdd(t, "speaker", "-acp", "/bin/sh --acp", "-force"); err != nil {
		t.Errorf("with -force: %v", err)
	}
}
