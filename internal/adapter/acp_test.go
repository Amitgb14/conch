package adapter

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// TestAnACPManifest: an [acp] table makes an agent that runs through
// conch acp, takes a first message and reopens a session by id.
func TestAnACPManifest(t *testing.T) {
	home, dir := agentsDir(t)
	write(t, dir, "speaker.toml", `
agent = "speaker"
label = "Speaker"

[acp]
command = "speaker --acp --model 'big one'"
dirs = ["/opt/speaker/bin"]
env = ["SPEAKER_QUIET=1"]
`)
	r, err := New("/usr/local/bin/conch", home)
	if err != nil {
		t.Fatal(err)
	}
	a, ok := r.Get("speaker")
	if !ok {
		t.Fatal("the ACP agent is not in the registry")
	}
	if a.Tier() != TierRunsHere || Protocol(a) != ProtocolACP {
		t.Errorf("tier %q protocol %q", a.Tier(), Protocol(a))
	}
	if _, ok := AsACP(a); !ok {
		t.Error("AsACP said no")
	}
	if cp, ok := a.(interface{ CanPrompt() bool }); !ok || !cp.CanPrompt() {
		t.Error("an ACP agent can always be given a first message")
	}
	args := a.PromptArgs("--yolo it's done") + " " + a.ResumeArgs("s-1")
	cmd := a.Command("/bin/sh", args)
	line := cmd[2]
	for _, want := range []string{
		`PATH="/opt/speaker/bin:$PATH"; exec /usr/local/bin/conch acp -agent speaker -label Speaker`,
		`-prompt '--yolo it'\''s done'`, `-load s-1`, ` -- speaker --acp --model 'big one'`,
	} {
		if !strings.Contains(line, want) {
			t.Errorf("command %q lacks %q", line, want)
		}
	}
	if a.ResumeArgs("") != "" {
		t.Error("ACP has no 'the latest here' to resume")
	}
	if strings.Join(a.Env(), " ") != "SPEAKER_QUIET=1" {
		t.Errorf("env = %v", a.Env())
	}
	if Protocol(r[0]) != "" {
		t.Error("Claude Code is not run over ACP")
	}

	// The shell reads the command as it reads a typed one: what reaches
	// conch acp is the flags, "--", then the agent's words.
	out, err := exec.Command("/bin/sh", "-c", strings.Replace(line, "exec /usr/local/bin/conch", `printf '%s\n'`, 1)).Output()
	if err != nil {
		t.Fatal(err)
	}
	words := strings.Split(strings.TrimSpace(string(out)), "\n")
	want := []string{"acp", "-agent", "speaker", "-label", "Speaker", "-prompt", "--yolo it's done", "-load", "s-1", "--", "speaker", "--acp", "--model", "big one"}
	if strings.Join(words, "|") != strings.Join(want, "|") {
		t.Errorf("words = %q\nwant    %q", words, want)
	}
}

func TestACPManifestsRefused(t *testing.T) {
	home, dir := agentsDir(t)
	write(t, dir, "both.toml", "agent = \"both\"\n[run]\nbinary = \"x\"\n[acp]\ncommand = \"x --acp\"\n")
	write(t, dir, "claude.toml", "agent = \"claude\"\n[acp]\ncommand = \"claude-acp\"\n")
	write(t, dir, "blank.toml", "agent = \"blank\"\n[acp]\ncommand = \"   \"\n")
	var problems []string
	old := ManifestProblem
	ManifestProblem = func(err error) { problems = append(problems, err.Error()) }
	defer func() { ManifestProblem = old }()
	r, err := New("/usr/local/bin/conch", home)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"both", "blank"} {
		if _, ok := r.Get(n); ok {
			t.Errorf("%s was taken", n)
		}
	}
	if a, _ := r.Get("claude"); Protocol(a) == ProtocolACP {
		t.Error("an [acp] manifest replaced a supported agent")
	}
	joined := strings.Join(problems, "\n")
	if !strings.Contains(joined, "both [run] and [acp]") || !strings.Contains(joined, "supported agent") {
		t.Errorf("problems = %s", joined)
	}
}

func TestACPDetectAndProbe(t *testing.T) {
	bin := t.TempDir()
	// A stand-in that answers initialize and nothing else.
	script := `#!/bin/sh
case "$1" in --version) echo "speaker 1.2.3"; exit 0;; esac
read line
echo '{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":1,"agentCapabilities":{"loadSession":true}}}'
cat >/dev/null
`
	if err := os.WriteFile(filepath.Join(bin, "speaker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	a, err := newACPAgent("speaker", "Speaker", acpSpec{Command: "LANG=C speaker --acp", Dirs: []string{bin}})
	if err != nil {
		t.Fatal(err)
	}
	av := a.Detect(context.Background(), "/bin/sh")
	if !av.Installed || av.Version != "1.2.3" {
		t.Errorf("detect = %+v", av)
	}
	init, list, err := a.Probe(context.Background(), "/bin/sh", t.TempDir(), true)
	if err != nil || !init.AgentCapabilities.LoadSession || init.CanList() || list != nil {
		t.Errorf("probe = %+v %v %v", init, list, err)
	}
	if _, _, err := a.Probe(context.Background(), "/bin/sh", "", true); err == nil {
		t.Error("probed with no folder")
	}
	missing, _ := newACPAgent("nope", "Nope", acpSpec{Command: "definitely-not-installed-xyz --acp"})
	if missing.Detect(context.Background(), "/bin/sh").Installed {
		t.Error("a missing program was found")
	}
}

func TestWriteACPManifest(t *testing.T) {
	dir := t.TempDir()
	supported := func(n string) bool { return n == "claude" }
	m := ACPManifest{Name: "speaker", Label: `Speaker "big"`, Command: `speaker --acp --say 'a "b"'`, Dirs: []string{"~/.speaker/bin"}, Env: []string{"S=1 2"}}
	path, err := WriteACPManifest(dir, m, false, supported)
	if err != nil {
		t.Fatal(err)
	}
	// It loads back as the same agent, quotes and all.
	got, errs := manifestAgents(dir, map[string]bool{})
	if len(errs) > 0 || len(got) != 1 {
		t.Fatalf("loaded %v %v", got, errs)
	}
	aa := got[0].(*acpAgent)
	if s := aa.Spec(); s.Label != m.Label || s.Command != m.Command || strings.Join(s.Dirs, "|") != "~/.speaker/bin" || strings.Join(s.Env, "|") != "S=1 2" || !aa.Own() {
		t.Errorf("read back %+v own %v", s, aa.Own())
	}
	if _, err := WriteACPManifest(dir, m, false, supported); !errors.Is(err, ErrManifestExists) {
		t.Errorf("again without replace: %v", err)
	}
	m.Label = "Speaker"
	if _, err := WriteACPManifest(dir, m, true, supported); err != nil {
		t.Errorf("replace: %v", err)
	}
	for _, bad := range []ACPManifest{
		{Name: "claude", Command: "x"},
		{Name: "Bad Name", Command: "x"},
		{Name: "ok", Command: "  "},
		{Name: "ok", Command: "x\n[run]\nbinary = \"evil\""},
		{Name: "ok", Command: "x", Label: "a\rb"},
		{Name: "ok", Command: "x", Env: []string{"=v"}},
		{Name: "ok", Command: "x", Env: []string{"NO EQUALS"}},
		{Name: "ok", Command: "x", Dirs: []string{"a\nb"}},
	} {
		if _, err := WriteACPManifest(dir, bad, false, supported); err == nil {
			t.Errorf("wrote %+v", bad)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "ok.toml")); err == nil {
		t.Error("a refused manifest left a file")
	}
	// A [run] manifest is somebody's own: neither replaced nor removed.
	run := filepath.Join(dir, "robo.toml")
	os.WriteFile(run, []byte("agent = \"robo\"\n[run]\nbinary = \"robo\"\n"), 0o600)
	if _, err := WriteACPManifest(dir, ACPManifest{Name: "robo", Command: "robo --acp"}, true, nil); err == nil {
		t.Error("replaced a [run] manifest")
	}
	if err := RemoveACPManifest(dir, "robo"); err == nil {
		t.Error("removed a [run] manifest")
	}
	if err := RemoveACPManifest(dir, "speaker"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("still there after remove")
	}
	for _, n := range []string{"speaker", "../x", ""} {
		if err := RemoveACPManifest(dir, n); err == nil {
			t.Errorf("removed %q", n)
		}
	}
}

// TestCheckACP: what Settings' Check says, from stand-ins for each case.
func TestCheckACP(t *testing.T) {
	t.Cleanup(SetProbeTimeout(2 * time.Second))
	bin := t.TempDir()
	put := func(name, script string) {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	put("good", `case "$1" in --version) echo "good 2.0.1"; exit 0;; esac
read line
echo '{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":1,"agentCapabilities":{"loadSession":true,"sessionCapabilities":{"list":{}}},"agentInfo":{"name":"good-agent","version":"2.0.2"},"authMethods":[{"id":"k","name":"An API key"}]}}'
cat >/dev/null
`)
	put("dies", `case "$1" in --version) exit 0;; esac
echo "dies: no key set" >&2; exit 2
`)
	put("chatty", `case "$1" in --version) exit 0;; esac
echo "Welcome! Type /help."; sleep 10
`)
	home := t.TempDir()
	check := func(cmd string) proto.AgentACPCheck {
		return CheckACP(context.Background(), "/bin/sh", home, ACPManifest{Name: "x", Command: cmd, Dirs: []string{bin}})
	}
	if c := check("good --acp"); c.Problem != "" || c.Agent != "good-agent" || c.Version != "2.0.2" || !c.Load || !c.List || c.ProtocolVersion != 1 || strings.Join(c.Auth, ",") != "An API key" || c.Path == "" {
		t.Errorf("good = %+v", c)
	}
	if c := check("dies --acp"); c.Problem != proto.ACPExited || !strings.Contains(c.Output, "no key set") {
		t.Errorf("dies = %+v", c)
	}
	if c := check("chatty"); c.Problem != proto.ACPNotACP || !strings.Contains(c.Output, "Welcome") {
		t.Errorf("chatty = %+v", c)
	}
	if c := check("missing-xyz --acp"); c.Problem != proto.ACPNotFound || !strings.Contains(c.Message, "missing-xyz") {
		t.Errorf("missing = %+v", c)
	}
	if c := check("  "); c.Problem != proto.ACPFailed {
		t.Errorf("empty = %+v", c)
	}
}
