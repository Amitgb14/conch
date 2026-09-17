package adapter

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func a6Registry(t *testing.T) (Registry, string) {
	t.Helper()
	t.Setenv("GEMINI_CLI_SYSTEM_DEFAULTS_PATH", "")
	t.Setenv("OPENCODE_CONFIG_CONTENT", "")
	dir := filepath.Join(t.TempDir(), "nested", "conch")
	reg, err := New("/usr/local/bin/conch", dir)
	if err != nil {
		t.Fatal(err)
	}
	return reg, dir
}

func TestA6RegistryGetLabelsInstall(t *testing.T) {
	reg, dir := a6Registry(t)
	if st, err := os.Stat(dir); err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("config dir: %v %v", st, err)
	}
	if _, ok := reg.Get("aider"); ok {
		t.Fatal("unknown agent found")
	}
	labels := map[string]string{"claude": "Claude Code", "codex": "Codex", "gemini": "Gemini CLI", "opencode": "OpenCode", "devin": "Devin"}
	for name, label := range labels {
		a, ok := reg.Get(name)
		if !ok {
			t.Fatalf("%s missing", name)
		}
		if a.Label() != label {
			t.Errorf("%s label %q", name, a.Label())
		}
		script := a.InstallScript()
		if !strings.Contains(script, "Installed") {
			t.Errorf("%s install script: %q", name, script)
		}
	}
	claude, _ := reg.Get("claude")
	if !strings.Contains(claude.InstallScript(), "https://claude.ai/install.sh") || !strings.Contains(claude.InstallScript(), "wget -qO-") {
		t.Fatalf("claude install: %s", claude.InstallScript())
	}
	codex, _ := reg.Get("codex")
	if !strings.Contains(codex.InstallScript(), "| CODEX_NON_INTERACTIVE=1 sh") {
		t.Fatalf("codex install: %s", codex.InstallScript())
	}
	if codex.Env() != nil {
		t.Fatalf("codex injects nothing: %v", codex.Env())
	}
	// Claude writes no env either: its integration is --settings.
	if claude.Env() != nil {
		t.Fatalf("claude env: %v", claude.Env())
	}
	for _, f := range []string{"claude-settings.json", "gemini-defaults.json", "opencode-conch.js"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
	}
	// No temp files are left behind by the atomic writes.
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Fatalf("leftover temp file %s", e.Name())
		}
	}
}

func TestA6ClaudeSettingsFile(t *testing.T) {
	_, dir := a6Registry(t)
	b, err := os.ReadFile(filepath.Join(dir, "claude-settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, ev := range ClaudeHookEvents {
		if !strings.Contains(s, `"`+ev+`"`) {
			t.Errorf("hook %s missing", ev)
		}
	}
	if !strings.Contains(s, "/usr/local/bin/conch report claude-status") || !strings.Contains(s, "/usr/local/bin/conch report claude-hook") {
		t.Fatalf("settings: %s", s)
	}
	if !strings.HasSuffix(s, "}\n") {
		t.Fatal("settings should end with a newline")
	}
}

func TestA6ResumeAndPromptArgsEveryAgent(t *testing.T) {
	reg, _ := a6Registry(t)
	cases := map[string]struct{ id, last, quoted, prompt string }{
		"claude":   {"--resume abc-123", "--continue", `--resume 'it'\''s'`, `'hello world'`},
		"codex":    {"resume abc-123", "resume --last", `resume 'it'\''s'`, `'hello world'`},
		"gemini":   {"--resume abc-123", "--resume latest", `--resume 'it'\''s'`, `'hello world'`},
		"opencode": {"--session abc-123", "--continue", `--session 'it'\''s'`, `--prompt 'hello world'`},
		// A first message follows --, so Devin doesn't read it as a subcommand.
		"devin": {"-r abc-123", "-c", `-r 'it'\''s'`, `-- 'hello world'`},
	}
	for name, want := range cases {
		a, _ := reg.Get(name)
		if got := a.ResumeArgs("abc-123"); got != want.id {
			t.Errorf("%s ResumeArgs(id) = %q, want %q", name, got, want.id)
		}
		if got := a.ResumeArgs(""); got != want.last {
			t.Errorf("%s ResumeArgs('') = %q, want %q", name, got, want.last)
		}
		if got := a.ResumeArgs("it's"); got != want.quoted {
			t.Errorf("%s ResumeArgs(quote) = %q, want %q", name, got, want.quoted)
		}
		if got := a.PromptArgs("hello world"); got != want.prompt {
			t.Errorf("%s PromptArgs = %q, want %q", name, got, want.prompt)
		}
		for _, blank := range []string{"", "   ", "\n\t"} {
			if got := a.PromptArgs(blank); got != "" {
				t.Errorf("%s PromptArgs(%q) = %q", name, blank, got)
			}
		}
	}
	// A shell-injection attempt in an id stays a single quoted word.
	claude, _ := reg.Get("claude")
	if got := claude.ResumeArgs("x; rm -rf ~"); got != "--resume 'x; rm -rf ~'" {
		t.Fatalf("injection: %q", got)
	}
}

func TestA6CommandShapes(t *testing.T) {
	reg, dir := a6Registry(t)
	codex, _ := reg.Get("codex")
	cmd := codex.Command("/bin/bash", "   ")
	if len(cmd) != 3 || cmd[0] != "/bin/bash" || cmd[1] != "-lc" || cmd[2] != `PATH="$HOME/.local/bin:$PATH"; exec codex` {
		t.Fatalf("codex command: %q", cmd)
	}
	if cmd := codex.Command("sh", " --full-auto "); cmd[2] != `PATH="$HOME/.local/bin:$PATH"; exec codex --full-auto` {
		t.Fatalf("codex with args: %q", cmd)
	}
	opencode, _ := reg.Get("opencode")
	if cmd := opencode.Command("sh", ""); cmd[2] != `PATH="$HOME/.opencode/bin:$HOME/bin:$HOME/.local/bin:$PATH"; exec opencode` {
		t.Fatalf("opencode: %q", cmd)
	}
	claude, _ := reg.Get("claude")
	want := `PATH="$HOME/.local/bin:$PATH"; exec claude --settings ` + ShellQuote(filepath.Join(dir, "claude-settings.json"))
	if cmd := claude.Command("sh", ""); cmd[2] != want {
		t.Fatalf("claude: %q want %q", cmd[2], want)
	}

	bare := &cliAgent{name: "x", binary: "x"}
	if bare.pathSetup() != "" {
		t.Fatal("no dirs, no PATH setup")
	}
	if cmd := bare.Command("sh", "a"); cmd[2] != "exec x a" {
		t.Fatalf("bare: %q", cmd)
	}
}

func TestA6EnvRespectsUserSettings(t *testing.T) {
	t.Setenv("GEMINI_CLI_SYSTEM_DEFAULTS_PATH", "/etc/admin.json")
	t.Setenv("OPENCODE_CONFIG_CONTENT", "")
	reg, err := New("/conch", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if g, _ := reg.Get("gemini"); len(g.Env()) != 0 {
		t.Fatalf("displaced an admin's gemini defaults: %v", g.Env())
	}
	if o, _ := reg.Get("opencode"); len(o.Env()) != 1 {
		t.Fatalf("opencode env: %v", o.Env())
	}
}

func TestA6NewErrors(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs POSIX permissions as a non-root user")
	}
	t.Setenv("GEMINI_CLI_SYSTEM_DEFAULTS_PATH", "")
	t.Setenv("OPENCODE_CONFIG_CONTENT", "")

	// dir under a regular file can't be created.
	file := filepath.Join(t.TempDir(), "file")
	os.WriteFile(file, nil, 0o600)
	if _, err := New("/conch", filepath.Join(file, "sub")); err == nil {
		t.Fatal("dir under a file accepted")
	}

	// A read-only dir fails writing the claude settings.
	ro := t.TempDir()
	os.Chmod(ro, 0o500)
	t.Cleanup(func() { os.Chmod(ro, 0o700) })
	if _, err := New("/conch", ro); err == nil || !strings.Contains(err.Error(), "claude settings") {
		t.Fatalf("read-only dir: %v", err)
	}

	// A directory where the gemini file goes fails its rename.
	d := t.TempDir()
	os.Mkdir(filepath.Join(d, "gemini-defaults.json"), 0o700)
	os.WriteFile(filepath.Join(d, "gemini-defaults.json", "keep"), nil, 0o600)
	if _, err := New("/conch", d); err == nil || !strings.Contains(err.Error(), "gemini defaults") {
		t.Fatalf("gemini write: %v", err)
	}

	d = t.TempDir()
	os.Mkdir(filepath.Join(d, "opencode-conch.js"), 0o700)
	os.WriteFile(filepath.Join(d, "opencode-conch.js", "keep"), nil, 0o600)
	if _, err := New("/conch", d); err == nil || !strings.Contains(err.Error(), "opencode plugin") {
		t.Fatalf("opencode write: %v", err)
	}

	if err := writeJSON(filepath.Join(t.TempDir(), "x.json"), map[string]any{"bad": make(chan int)}); err == nil {
		t.Fatal("unencodable JSON written")
	}
}

func TestA6DetectVersions(t *testing.T) {
	home := t.TempDir()
	bin := filepath.Join(home, ".local", "bin")
	os.MkdirAll(bin, 0o755)
	t.Setenv("HOME", home)
	t.Setenv("PATH", "/usr/bin:/bin")
	a := &cliAgent{name: "fake", binary: "a6fakeagent", dirs: []string{"$HOME/.local/bin"}}

	write := func(body string) {
		os.WriteFile(filepath.Join(bin, "a6fakeagent"), []byte("#!/bin/sh\n"+body), 0o755)
	}
	write("echo 'a6fakeagent version v2.3.4-beta.1 (build 99)'\n")
	av := a.Detect(context.Background(), "/bin/sh")
	if !av.Installed || av.Version != "2.3.4-beta.1" || av.Path != filepath.Join(bin, "a6fakeagent") {
		t.Fatalf("detect: %+v", av)
	}
	// No --version output: installed, no version.
	write("exit 0\n")
	if av := a.Detect(context.Background(), "/bin/sh"); !av.Installed || av.Version != "" {
		t.Fatalf("silent: %+v", av)
	}
	// Output without a version number.
	write("echo unknown\n")
	if av := a.Detect(context.Background(), "/bin/sh"); !av.Installed || av.Version != "" {
		t.Fatalf("no number: %+v", av)
	}
	// Only the first line of --version is read.
	write("echo 'tool 1.0'; echo 'lib 9.9.9'\n")
	if av := a.Detect(context.Background(), "/bin/sh"); av.Version != "1.0" {
		t.Fatalf("first line: %+v", av)
	}
	// A shell that doesn't exist means not installed.
	if av := a.Detect(context.Background(), filepath.Join(home, "nosh")); av.Installed {
		t.Fatalf("missing shell: %+v", av)
	}
	// A canceled context means not installed.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if av := a.Detect(ctx, "/bin/sh"); av.Installed {
		t.Fatalf("canceled: %+v", av)
	}
}

func TestA6SanitizeEnv(t *testing.T) {
	in := []string{
		"PATH=/bin", "CLAUDECODE=1", "CLAUDE_CODE_SSE_PORT=1234", "CONCH_PANE_ID=p1",
		"CLAUDECODE_EXTRA=keep", "HOME=/h", "NOEQUALS", "CLAUDE_PID", "CONCH_SOCKET=/s",
	}
	orig := append([]string(nil), in...)
	got := SanitizeEnv(in)
	want := []string{"PATH=/bin", "CLAUDECODE_EXTRA=keep", "HOME=/h", "NOEQUALS", "CONCH_SOCKET=/s"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("SanitizeEnv = %q, want %q", got, want)
	}
	if strings.Join(in, "|") != strings.Join(orig, "|") {
		t.Fatalf("input modified: %q", in)
	}
	if got := SanitizeEnv(nil); len(got) != 0 {
		t.Fatalf("nil: %q", got)
	}
	// Appending to the result never writes into the input's backing array.
	small := make([]string, 1, 4)
	small[0] = "A=1"
	out := SanitizeEnv(small)
	out = append(out, "B=2")
	if small[:2][1] == "B=2" {
		t.Fatal("result aliases the input")
	}
}

func TestA6ShellQuoteAndCleanTitle(t *testing.T) {
	for in, want := range map[string]string{
		"":             "''",
		"plain":        "plain",
		"a/b.c:d@e%f":  "a/b.c:d@e%f",
		"with space":   "'with space'",
		"it's":         `'it'\''s'`,
		"$HOME":        "'$HOME'",
		"ünïcode":      "'ünïcode'",
		"semi;colon":   "'semi;colon'",
		"new\nline":    "'new\nline'",
		"k=v,x+y":      "k=v,x+y",
		"back`tick`":   "'back`tick`'",
		"glob*":        "'glob*'",
		"'":            `''\'''`,
		"tab\tbetween": "'tab\tbetween'",
	} {
		if got := ShellQuote(in); got != want {
			t.Errorf("ShellQuote(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{
		"":                            "",
		"OC | Fix bug":                "Fix bug",
		"OC | opencode":               "",
		"◇  Ready (repo)":             "",
		"✋ Action Required":           "",
		"⠋ Claude Code":               "",
		"[ . ] Action Required (x) y": "y",
		"   42 tests":                 "42 tests",
	} {
		if got := CleanTitle(in); got != want {
			t.Errorf("CleanTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

// Devin for Terminal installs through conch like the other agents: its
// official installer, run in a pane, puts devin in ~/.local/bin.
func TestDevinAdapter(t *testing.T) {
	reg, _ := a6Registry(t)
	devin, ok := reg.Get("devin")
	if !ok {
		t.Fatal("devin missing from the registry")
	}
	script := devin.InstallScript()
	for _, want := range []string{"https://cli.devin.ai/install.sh", "| bash", "wget -qO-", `$HOME/.local/bin/devin" --version`, "devin auth login"} {
		if !strings.Contains(script, want) {
			t.Fatalf("install script lacks %q:\n%s", want, script)
		}
	}
	if devin.Env() != nil {
		t.Fatalf("devin injects env: %v", devin.Env())
	}
	// The binary is found in ~/.local/bin even from a shell started before
	// the install, and a task's first message is passed after --.
	cmd := devin.Command("/bin/zsh", devin.PromptArgs("fix the failing test"))
	if len(cmd) != 3 || cmd[1] != "-lc" || cmd[2] != `PATH="$HOME/.local/bin:$PATH"; exec devin -- 'fix the failing test'` {
		t.Fatalf("devin command: %q", cmd)
	}
	if cmd := devin.Command("sh", devin.ResumeArgs("brisk-otter")); cmd[2] != `PATH="$HOME/.local/bin:$PATH"; exec devin -r brisk-otter` {
		t.Fatalf("devin resume: %q", cmd)
	}
	// Not installed in an empty home: offered for install, not launched.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", "/usr/bin:/bin")
	if av := devin.Detect(context.Background(), "/bin/sh"); av.Installed {
		t.Fatalf("devin found in an empty home: %+v", av)
	}
	// Installed: the version comes from devin --version.
	bin := filepath.Join(os.Getenv("HOME"), ".local", "bin")
	os.MkdirAll(bin, 0o755)
	os.WriteFile(filepath.Join(bin, "devin"), []byte("#!/bin/sh\necho 'devin 3000.10.21'\n"), 0o755)
	av := devin.Detect(context.Background(), "/bin/sh")
	if !av.Installed || av.Version != "3000.10.21" || !strings.HasSuffix(av.Path, "/.local/bin/devin") {
		t.Fatalf("installed devin: %+v", av)
	}
}
