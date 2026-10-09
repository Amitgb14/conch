package adapter

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// write puts a manifest in an agents directory, as somebody adding an
// agent would.
func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// agentsDir is a scratch CONCH_HOME with an agents/ folder, and the
// registry built from it.
func agentsDir(t *testing.T) (string, string) {
	t.Helper()
	home := t.TempDir()
	return home, ManifestDir(home)
}

// TestAManifestAgentRunsHere: an agent conch has never heard of, started,
// prompted and resumed from a file somebody dropped in — the whole point
// of the second tier. What it says about itself is checked too: a tier
// that claimed to be supported would be a lie the Sessions view repeats.
func TestAManifestAgentRunsHere(t *testing.T) {
	home, dir := agentsDir(t)
	write(t, dir, "robo.toml", `
agent = "robo"
label = "Robo Coder"
process_names = ["robo"]

[run]
binary = "robo"
dirs = ["~/.robo/bin"]
flags = "--terminal"
prompt = "--message {prompt}"
resume = "--resume {id}"
resume_last = "--continue"
env = ["ROBO_UI=plain"]
install = "curl -fsSL https://example.invalid/robo.sh | sh"
`)
	r, err := New("/usr/local/bin/conch", home)
	if err != nil {
		t.Fatal(err)
	}
	a, ok := r.Get("robo")
	if !ok {
		t.Fatal("the manifest's agent is not in the registry")
	}
	if a.Label() != "Robo Coder" || a.Tier() != TierRunsHere {
		t.Fatalf("label %q tier %q", a.Label(), a.Tier())
	}
	// The supported five are still supported, and still first.
	if r[0].Name() != "claude" || r[0].Tier() != TierSupported {
		t.Fatalf("first adapter %s/%s", r[0].Name(), r[0].Tier())
	}

	// Started as the manifest says, through the login shell, with the
	// person's own words after conch's flags.
	argv := a.Command("/bin/zsh", "--model opus")
	line := strings.Join(argv, " ")
	for _, want := range []string{"robo", "--terminal", "--model opus"} {
		if !strings.Contains(line, want) {
			t.Errorf("the command lacks %q: %s", want, line)
		}
	}
	if !strings.Contains(line, ".robo/bin") {
		t.Errorf("the installer's folder is not searched: %s", line)
	}
	if strings.Contains(line, "~") {
		t.Errorf("~ was left for a shell that never sees it: %s", line)
	}

	// A first message and a resume, in the manifest's own words, quoted.
	if got := a.PromptArgs("say 'hi' now"); got != `--message 'say '\''hi'\'' now'` {
		t.Errorf("prompt args %s", got)
	}
	// ShellQuote leaves a plain word alone and quotes what needs it,
	// which the prompt above showed.
	if got := a.ResumeArgs("ses-1"); got != "--resume ses-1" {
		t.Errorf("resume args %s", got)
	}
	if got := a.ResumeArgs(""); got != "--continue" {
		t.Errorf("resume latest %s", got)
	}
	if got := a.PromptArgs("  "); got != "" {
		t.Errorf("an empty prompt gave %q", got)
	}
	if p, ok := a.(interface{ CanPrompt() bool }); !ok || !p.CanPrompt() {
		t.Error("a manifest that says how to pass a first message cannot")
	}
	if a.Env()[0] != "ROBO_UI=plain" {
		t.Errorf("env %v", a.Env())
	}
	if !strings.Contains(a.InstallScript(), "robo.sh") {
		t.Errorf("install %q", a.InstallScript())
	}
	// It is not installed here, and says so rather than failing.
	if av := a.Detect(context.Background(), "/bin/sh"); av.Installed {
		t.Errorf("a binary that does not exist reads as installed: %+v", av)
	}
}

// TestAManifestSayingLittle: every field but the binary left out. The
// agent still starts — that is the tier's promise — and what it cannot do
// it reports rather than inventing: no resume, no install, its name as its
// label.
func TestAManifestSayingLittle(t *testing.T) {
	home, dir := agentsDir(t)
	write(t, dir, "bare.toml", "agent = \"bare\"\n\n[run]\nbinary = \"bare\"\n")
	r, err := New("/usr/local/bin/conch", home)
	if err != nil {
		t.Fatal(err)
	}
	a, ok := r.Get("bare")
	if !ok {
		t.Fatal("not registered")
	}
	if a.Label() != "bare" {
		t.Errorf("label %q", a.Label())
	}
	if got := a.ResumeArgs("x"); got != "" {
		t.Errorf("an agent that cannot resume offered %q", got)
	}
	if got := a.ResumeArgs(""); got != "" {
		t.Errorf("an agent that cannot resume offered the latest: %q", got)
	}
	if c, ok := a.(interface{ CanResume() bool }); !ok || c.CanResume() {
		t.Error("it claims it can resume")
	}
	if a.InstallScript() != "" {
		t.Errorf("install %q", a.InstallScript())
	}
	// A manifest that did not say how to pass a first message cannot take
	// one. Guessing at an argument is worse than saying so: several
	// agents' prompt flags answer and exit, so a "first message" would
	// start something that is over before anybody looks.
	if got := a.PromptArgs("hello"); got != "" {
		t.Errorf("an agent that cannot take a first message was given %q", got)
	}
	if p, ok := a.(interface{ CanPrompt() bool }); !ok || p.CanPrompt() {
		t.Error("it claims it can take a first message")
	}
}

// TestManifestsThatAreRefused: the ways a file can be wrong, each refused
// with a reason and none of them taking the other agents down with it.
func TestManifestsThatAreRefused(t *testing.T) {
	home, dir := agentsDir(t)
	write(t, dir, "claude.toml", "agent = \"claude\"\n\n[run]\nbinary = \"my-claude\"\n")
	write(t, dir, "nameless.toml", "[run]\nbinary = \"x\"\n")
	write(t, dir, "shouty.toml", "agent = \"NOPE!\"\n\n[run]\nbinary = \"x\"\n")
	write(t, dir, "broken.toml", "agent = \"broken\"\nthis is not toml\n")
	write(t, dir, "detect-only.toml", "agent = \"codex\"\nprocess_names = [\"codex\"]\n")
	write(t, dir, "good.toml", "agent = \"good\"\n\n[run]\nbinary = \"good\"\n")

	var problems []string
	old := ManifestProblem
	ManifestProblem = func(err error) { problems = append(problems, err.Error()) }
	t.Cleanup(func() { ManifestProblem = old })

	r, err := New("/usr/local/bin/conch", home)
	if err != nil {
		t.Fatal(err)
	}
	// The good one is there; the bad ones are not, and nothing replaced a
	// supported agent.
	if _, ok := r.Get("good"); !ok {
		t.Error("a good manifest was lost with the bad ones")
	}
	if a, _ := r.Get("claude"); a.Tier() != TierSupported {
		t.Fatal("a manifest replaced a supported agent")
	}
	if _, ok := r.Get("NOPE!"); ok {
		t.Error("a name that could not be one was taken")
	}
	// Each refusal says which file and why.
	joined := strings.Join(problems, "\n")
	for _, want := range []string{"claude.toml", "supported agent", "nameless.toml", "no agent name",
		"shouty.toml", "is not a name", "broken.toml"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the problems do not mention %q:\n%s", want, joined)
		}
	}
	// A manifest with no [run] is detect's override and no business of
	// this package's — not an error, and not an agent.
	if strings.Contains(joined, "detect-only.toml") {
		t.Errorf("a detect override was treated as a run manifest:\n%s", joined)
	}
	if a, _ := r.Get("codex"); a.Tier() != TierSupported {
		t.Error("a detect override changed codex's tier")
	}
}

// TestTwoManifestsOneName: two files naming the same agent — the second is
// refused rather than quietly deciding by which sorts last.
func TestTwoManifestsOneName(t *testing.T) {
	home, dir := agentsDir(t)
	write(t, dir, "a-first.toml", "agent = \"twice\"\nlabel = \"First\"\n\n[run]\nbinary = \"first\"\n")
	write(t, dir, "b-second.toml", "agent = \"twice\"\nlabel = \"Second\"\n\n[run]\nbinary = \"second\"\n")
	var problems []string
	old := ManifestProblem
	ManifestProblem = func(err error) { problems = append(problems, err.Error()) }
	t.Cleanup(func() { ManifestProblem = old })
	r, err := New("/usr/local/bin/conch", home)
	if err != nil {
		t.Fatal(err)
	}
	a, ok := r.Get("twice")
	if !ok || a.Label() != "First" {
		t.Fatalf("got %v", a)
	}
	if n := strings.Count(strings.Join(problems, "\n"), "already named"); n != 1 {
		t.Errorf("problems: %v", problems)
	}
}

// TestWithNoManifestsOfYourOwn: the usual case — the five with adapters,
// and the agents conch ships a manifest for, each saying which it is.
func TestWithNoManifestsOfYourOwn(t *testing.T) {
	home := t.TempDir()
	r, err := New("/usr/local/bin/conch", home)
	if err != nil {
		t.Fatal(err)
	}
	supported, runs := 0, 0
	for _, a := range r {
		switch a.Tier() {
		case TierSupported:
			supported++
		case TierRunsHere:
			runs++
		default:
			t.Errorf("%s has tier %q", a.Name(), a.Tier())
		}
	}
	if supported != 5 {
		t.Errorf("%d supported agents", supported)
	}
	if runs == 0 {
		t.Error("conch ships no manifests at all")
	}

	// What the shipped manifests promise, which is deliberately little:
	// every one of them starts, and says for itself whether it can be
	// given a first message or resumed, rather than being assumed to.
	for _, name := range []string{"aider", "amp", "cursor", "grok", "kilo", "pi"} {
		a, ok := r.Get(name)
		if !ok {
			t.Fatalf("no manifest for %s", name)
		}
		if a.Tier() != TierRunsHere {
			t.Errorf("%s is %q", name, a.Tier())
		}
		if a.Label() == "" || a.Label() == name && name != "grok" {
			t.Errorf("%s has no label of its own: %q", name, a.Label())
		}
		if len(a.Command("/bin/sh", "")) == 0 {
			t.Errorf("%s has no command", name)
		}
	}
	// Aider cannot be given a first message: its --message answers and
	// exits. Cursor can, as an argument. Those are the two shapes.
	aider, _ := r.Get("aider")
	if got := aider.PromptArgs("hello"); got != "" {
		t.Errorf("aider was given a first message: %q", got)
	}
	cursor, _ := r.Get("cursor")
	if got := cursor.PromptArgs("hello"); got != "hello" {
		t.Errorf("cursor prompt args %q", got)
	}
	if got := cursor.ResumeArgs("c1"); got != "--resume c1" {
		t.Errorf("cursor resume %q", got)
	}
	// Amp resumes by thread, with a subcommand rather than a flag.
	amp, _ := r.Get("amp")
	if got := amp.ResumeArgs("T-abc"); got != "threads continue T-abc" {
		t.Errorf("amp resume %q", got)
	}
	// Kilo has only "the last one", which is what its docs document.
	kilo, _ := r.Get("kilo")
	if got := kilo.ResumeArgs("x"); got != "" {
		t.Errorf("kilo resumed by id: %q", got)
	}
	if got := kilo.ResumeArgs(""); got != "--continue" {
		t.Errorf("kilo resume last %q", got)
	}
	// Pi takes a first message as an argument — its --print is the one
	// that answers and exits, so that is not what conch passes — and
	// reopens a session with --session, not with --resume, which opens a
	// picker rather than taking an id.
	pi, ok := r.Get("pi")
	if !ok {
		t.Fatal("pi is not in the registry")
	}
	if got := pi.PromptArgs("hello"); got != "hello" {
		t.Errorf("pi prompt args %q", got)
	}
	// A message of several words reaches it as one argument.
	if got := pi.PromptArgs("fix the tests"); got != "'fix the tests'" {
		t.Errorf("pi prompt args for a sentence: %q", got)
	}
	if got := pi.ResumeArgs("8f3a"); got != "--session 8f3a" {
		t.Errorf("pi resume %q", got)
	}
	if got := pi.ResumeArgs(""); got != "--continue" {
		t.Errorf("pi resume last %q", got)
	}
	if pi.InstallScript() == "" {
		t.Error("pi has no installer, so the menu cannot offer one")
	}
	if pi.Tier() != TierRunsHere {
		t.Errorf("pi tier %q", pi.Tier())
	}
}
