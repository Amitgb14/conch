package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/sandbox"
)

func TestUsage(t *testing.T) {
	isolate(t)
	if code, out, errs := run(t, ""); code != 2 || out != "" || !strings.Contains(errs, "Usage:") {
		t.Fatalf("no arguments: %d %q %q", code, out, errs)
	}
	for _, h := range []string{"help", "--help", "-h"} {
		if code, out, _ := run(t, "", h); code != 0 || !strings.Contains(out, "conch-sandbox create --provider P") {
			t.Fatalf("%s: %d %q", h, code, out)
		}
	}
	if code, _, errs := run(t, "", "launch"); code != 2 || !strings.Contains(errs, `unknown command "launch"`) {
		t.Fatalf("unknown: %d %q", code, errs)
	}
	if code, out, _ := run(t, "", "version"); code != 0 || strings.TrimSpace(out) != version {
		t.Fatalf("version: %d %q", code, out)
	}
	// Every verb has --help of its own, on stdout, and exits 0.
	for _, verb := range []string{"providers", "key", "create", "get", "start", "stop", "rm", "ssh-alias", "orphans", "pick"} {
		code, out, errs := run(t, "", verb, "--help")
		if code != 0 || !strings.Contains(out, "Usage: conch-sandbox "+verb) || errs != "" {
			t.Errorf("%s --help: %d %q %q", verb, code, out, errs)
		}
		if verb != "providers" && verb != "key" && verb != "orphans" && verb != "pick" && !strings.Contains(out, "-provider") {
			t.Errorf("%s --help names no flags: %q", verb, out)
		}
	}
	// A flag it does not know is a usage error, said on stderr.
	if code, out, errs := run(t, "", "get", "--nope"); code != 2 || out != "" || !strings.Contains(errs, "usage: conch-sandbox get") {
		t.Fatalf("bad flag: %d %q %q", code, out, errs)
	}
}

func TestCheckID(t *testing.T) {
	for id, ok := range map[string]bool{
		"box1": true, "8f3c2a1e-0000-4000": true, "a.b_c": true,
		"": false, "-rf": false, "a*": false, "a,b": false, "!a": false, "a?": false, "a b": false, "a/b": false, "u@h": false, "a:b": false, "a\nb": false, `a"b`: false,
	} {
		if err := checkID(id); (err == nil) != ok {
			t.Errorf("%q: %v", id, err)
		}
	}
}

// conch-sandbox links conch's provider code and nothing of its server,
// terminal interface or agents: that is what keeps it small and is the
// promise made to the tools that download it.
func TestLinksNoServer(t *testing.T) {
	gobin, err := exec.LookPath("go")
	if err != nil {
		// Under CI a skip would pass a guard nobody ran.
		if os.Getenv("CI") != "" {
			t.Fatal("no go on PATH under CI")
		}
		t.Skip("no go on PATH")
	}
	out, err := exec.Command(gobin, "list", "-deps", ".").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	allowed := map[string]bool{"config": true, "sandbox": true, "sshkey": true, "integration": true, "tracker": true}
	for _, dep := range strings.Fields(string(out)) {
		rest, ok := strings.CutPrefix(dep, "github.com/Amitgb14/conch/internal/")
		if ok && !allowed[strings.Split(rest, "/")[0]] {
			t.Errorf("conch-sandbox links %s", dep)
		}
		for _, banned := range []string{"bubbletea", "lipgloss", "charmbracelet/x/vt", "creack/pty"} {
			if strings.Contains(dep, banned) {
				t.Errorf("conch-sandbox links %s", dep)
			}
		}
	}
}

func TestProviders(t *testing.T) {
	isolate(t)
	// None installed: an empty list, and words saying how to get one.
	code, out, _ := run(t, "", "providers", "--json")
	if code != 0 {
		t.Fatalf("%d %q", code, out)
	}
	if got := decode[providersJSON](t, out); got.Schema != 1 || got.Providers == nil || len(got.Providers) != 0 {
		t.Fatalf("empty: %s", out)
	}
	if code, _, errs := run(t, "", "providers"); code != 0 || !strings.Contains(errs, "none is installed") {
		t.Fatalf("empty, text: %d %q", code, errs)
	}

	fakeProvider(t, "fakebox", tokenKind)
	fakeProvider(t, "keybox", keyKind)
	t.Setenv("FAKEBOX_API_KEY", "")
	code, out, _ = run(t, "", "providers", "--json")
	got := decode[providersJSON](t, out)
	if code != 0 || len(got.Providers) != 2 {
		t.Fatalf("%d %s", code, out)
	}
	by := map[string]providerJSON{}
	for _, p := range got.Providers {
		by[p.Name] = p
	}
	fb := by["fakebox"]
	if fb.Ready || !strings.Contains(fb.Why, "conch-sandbox key set fakebox") || !strings.Contains(fb.Why, "$FAKEBOX_API_KEY") {
		t.Fatalf("no key: %+v", fb)
	}
	if fb.Label != "Fake Box" || fb.KeyEnv != "FAKEBOX_API_KEY" || fb.SSH != "token" {
		t.Fatalf("fakebox: %+v", fb)
	}
	if by["keybox"].SSH != "key" {
		t.Fatalf("ssh kind: %q", by["keybox"].SSH)
	}
	if len(fb.Settings) != 2 || fb.Settings[0].Key != "region" || len(fb.Settings[0].Choices) != 2 || fb.Settings[0].Choices[1].Value != "eu" {
		t.Fatalf("settings: %+v", fb.Settings)
	}
	if im := fb.Settings[1]; im.Key != "image" || !im.FromAPI || !im.Free || len(im.Choices) != 0 {
		t.Fatalf("image setting: %+v", im)
	}
	if fb.Sizes == nil || strings.Join(fb.Sizes.Names, ",") != "small,large" || fb.Sizes.Default != "small" {
		t.Fatalf("sizes: %+v", fb.Sizes)
	}

	// With the key, ready; the text table says so too.
	t.Setenv("FAKEBOX_API_KEY", "k-test")
	_, out, _ = run(t, "", "providers", "--json")
	for _, p := range decode[providersJSON](t, out).Providers {
		if !p.Ready || p.Why != "" {
			t.Fatalf("with a key: %+v", p)
		}
	}
	code, out, _ = run(t, "", "providers")
	if code != 0 || !strings.Contains(out, "PROVIDER") || !strings.Contains(out, "fakebox") || !strings.Contains(out, "token") {
		t.Fatalf("text: %d %q", code, out)
	}
	if code, _, _ := run(t, "", "providers", "extra"); code != 2 {
		t.Fatalf("an argument: %d", code)
	}
}

func TestKeySet(t *testing.T) {
	isolate(t)
	fakeProvider(t, "fakebox", tokenKind)
	t.Setenv("FAKEBOX_API_KEY", "")
	// Something of the person's own in config.toml is kept.
	cfg, _ := config.Load()
	cfg.Sandbox = config.SandboxCfg{"boat": {Region: "eu"}}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	code, out, errs := run(t, "  k-secret-1\nignored\n", "key", "set", "fakebox")
	if code != 0 || out != "" || strings.Contains(errs, "k-secret-1") {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	cfg, err := config.Load()
	if err != nil || cfg.Sandbox["fakebox"].APIKey != "k-secret-1" || cfg.Sandbox["boat"].Region != "eu" {
		t.Fatalf("saved: %+v %v", cfg.Sandbox, err)
	}
	if fi, err := os.Stat(config.File()); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("config.toml: %v %v", fi, err)
	}
	// A running conch would write its own copy back: said, and how.
	if !strings.Contains(errs, "restart it") {
		t.Fatalf("no word about a running conch: %q", errs)
	}
	// The person's comments are theirs: one line changes.
	b, _ := os.ReadFile(config.File())
	os.WriteFile(config.File(), append([]byte("# my own note\n"), b...), 0o600)
	if code, _, errs := run(t, "k-secret-1\n", "key", "set", "fakebox"); code != 0 {
		t.Fatalf("%q", errs)
	}
	if b2, _ := os.ReadFile(config.File()); string(b2) != "# my own note\n"+string(b) {
		t.Fatalf("rewrote the file:\n%s", b2)
	}
	// conch's own reading of it now has the key.
	p, err := sandbox.Open("fakebox", cfg.Sandbox)
	if err != nil || p.Check() != nil {
		t.Fatalf("open: %v", err)
	}
	// A key with no newline at the end, and a second set replacing it.
	if code, _, errs := run(t, "k-2", "key", "set", "fakebox"); code != 0 {
		t.Fatalf("no newline: %d %q", code, errs)
	}
	if cfg, _ := config.Load(); cfg.Sandbox["fakebox"].APIKey != "k-2" {
		t.Fatalf("replaced: %+v", cfg.Sandbox["fakebox"])
	}

	for _, c := range []struct {
		in, want string
		args     []string
		code     int
	}{
		{"", "no key given", []string{"key", "set", "fakebox"}, 1},
		{"\n\n", "no key given", []string{"key", "set", "fakebox"}, 1},
		{"two words\n", "spaces", []string{"key", "set", "fakebox"}, 1},
		{strings.Repeat("k", maxKey+10), "bytes or more", []string{"key", "set", "fakebox"}, 1},
		{"k\n", "unknown sandbox provider", []string{"key", "set", "nope"}, 1},
		{"k\n", "usage", []string{"key", "fakebox"}, 2},
		{"k\n", "usage", []string{"key", "get", "fakebox"}, 2},
		{"k\n", "usage", []string{"key"}, 2},
	} {
		code, _, errs := run(t, c.in, c.args...)
		if code != c.code || !strings.Contains(errs, c.want) {
			t.Errorf("%q %v: %d %q", c.in, c.args, code, errs)
		}
	}
	if cfg, _ := config.Load(); cfg.Sandbox["fakebox"].APIKey != "k-2" {
		t.Fatalf("a refused key was saved: %+v", cfg.Sandbox["fakebox"])
	}

	// A config.toml conch cannot read is not written over.
	os.WriteFile(config.File(), []byte("[sandbox\nbroken"), 0o600)
	if code, _, errs := run(t, "k3\n", "key", "set", "fakebox"); code != 1 || !strings.Contains(errs, "put it right first") {
		t.Fatalf("broken config: %d %q", code, errs)
	}
	if b, _ := os.ReadFile(config.File()); string(b) != "[sandbox\nbroken" {
		t.Fatalf("broken config written over: %q", b)
	}
}

func TestKeySetFromATerminal(t *testing.T) {
	isolate(t)
	fakeProvider(t, "fakebox", tokenKind)
	var out, errb strings.Builder
	a := &app{stdin: strings.NewReader("not read"), stdout: &out, stderr: &errb, terminal: true,
		readSecret: func() (string, error) { return "k-typed", nil }}
	if code := a.run(t.Context(), []string{"key", "set", "fakebox"}); code != 0 {
		t.Fatalf("%d %q", code, errb.String())
	}
	if !strings.Contains(errb.String(), "Fake Box API key (not shown)") {
		t.Fatalf("no prompt: %q", errb.String())
	}
	if cfg, _ := config.Load(); cfg.Sandbox["fakebox"].APIKey != "k-typed" {
		t.Fatalf("%+v", cfg.Sandbox)
	}
	// A provider conch-sandbox keeps no file for yet, never.
	if _, err := os.Stat(filepath.Join(config.Dir(), "ssh", "config")); err == nil {
		t.Fatal("key set wrote ssh aliases")
	}
}

// make conch-sandbox builds this program with its version set, which the
// launcher compares with the one a plugin pins.
func TestMakeTarget(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	mk := string(b)
	for _, want := range []string{"\nconch-sandbox:\n", "-X main.version=$(VERSION)", "-o bin/conch-sandbox ./cmd/conch-sandbox",
		"\nsandbox-release:\n\tscripts/sandbox-release.sh $(VERSION)\n"} {
		if !strings.Contains(mk, want) {
			t.Errorf("Makefile lacks %q", want)
		}
	}
}

// A release builds conch-sandbox after conch (release.sh empties dist/)
// and publishes the builds, their sums and the pinned launcher, on both
// of the publish step's paths.
func TestReleaseWorkflow(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	wf := string(b)
	conch, sb := strings.Index(wf, `scripts/release.sh "${GITHUB_REF_NAME}"`), strings.Index(wf, `scripts/sandbox-release.sh "${GITHUB_REF_NAME}"`)
	if conch < 0 || sb < conch {
		t.Fatalf("sandbox-release.sh is not run after release.sh")
	}
	files := "dist/conch-sandbox-* dist/SHA256SUMS dist/conch-sandbox.sh"
	if strings.Count(wf, files) != 2 || !strings.Contains(wf, "gh release upload \"$TAG\" dist/*.tar.gz dist/checksums.txt "+files) ||
		!strings.Contains(wf, "gh release create \"$TAG\" dist/*.tar.gz dist/checksums.txt "+files) {
		t.Fatalf("release.yml does not publish %s on both paths", files)
	}
}
