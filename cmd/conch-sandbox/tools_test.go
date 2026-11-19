package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseMachines(t *testing.T) {
	for in, want := range map[string]string{
		`[]`: "",
		`[{"id":"m1","label":"A","target":"conch-p-1"}]`:                          "m1/A",
		`{"machines":[{"profile_id":"m2","ssh":{"target":"u@conch-p-2"}}]}`:       "m2/",
		`{"version":1,"items":[{"profileId":"m3"},"junk"],"other":[{"id":"m4"}]}`: "m3/ m4/",
	} {
		ms, err := parseMachines([]byte(in))
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		var got []string
		for _, m := range ms {
			got = append(got, m.ID+"/"+m.Label)
		}
		if strings.Join(got, " ") != want {
			t.Errorf("%s: %q", in, got)
		}
	}
	ms, _ := parseMachines([]byte(`{"machines":[{"profile_id":"m2","ssh":{"target":"u@conch-p-2"}}]}`))
	if !ms[0].has("conch-p-2") || ms[0].has("conch-p-20") || ms[0].has("conch-p") {
		t.Fatal("has")
	}
	for _, bad := range []string{`nope`, `"a string"`, `42`} {
		if _, err := parseMachines([]byte(bad)); err == nil {
			t.Errorf("%s read", bad)
		}
	}
}

// orphans are conch-sandbox's sandboxes made for the tool, still there,
// that the tool no longer has a machine for.
func TestOrphans(t *testing.T) {
	isolate(t)
	api := fakeProvider(t, "fakebox", tokenKind)
	fakeSSH(t)
	h := fakeHerdr(t)
	for range 4 {
		if code, _, errs := run(t, "", "create", "--provider", "fakebox", "--tool", "herdr"); code != 0 {
			t.Fatalf("%q", errs)
		}
	}
	removeAlias("fakebox", "box3")                      // made elsewhere (conch's own): not this tool's
	run(t, "", "stop", "--provider", "fakebox", "box4") // stopped, still keeping a disk
	api.mu.Lock()
	api.boxes["box9"] = map[string]any{"id": "box9", "name": "conch-other", "state": "running"} // no alias at all
	api.mu.Unlock()
	h.set(t, `[{"id":"m1","label":"one","target":"conch-fakebox-box1"}]`)

	code, out, errs := run(t, "", "orphans", "--tool", "herdr", "--json")
	if code != 0 {
		t.Fatalf("%d %q", code, errs)
	}
	got := decode[orphansJSON](t, out)
	if got.Schema != 1 || got.Tool != "herdr" || len(got.Orphans) != 2 ||
		got.Orphans[0].Alias != "conch-fakebox-box2" || got.Orphans[1].Alias != "conch-fakebox-box4" || got.Orphans[1].State != "stopped" {
		t.Fatalf("%s", out)
	}
	if code, out, _ := run(t, "", "orphans", "--tool", "herdr"); code != 0 || !strings.Contains(out, "conch-fakebox-box2") || strings.Contains(out, "box1") {
		t.Fatalf("text: %q", out)
	}

	// --notify says so in herdr, naming them.
	if code, _, _ := run(t, "", "orphans", "--tool", "herdr", "--notify"); code != 0 {
		t.Fatal(code)
	}
	if c := h.calls(); !strings.Contains(c, "notification show conch sandboxes --body 2 sandboxes are still there with no herdr machine: conch-fakebox-box2, conch-fakebox-box4") {
		t.Fatalf("notify:\n%s", c)
	}
	// herdr not answering: said on stderr instead, and not a failure.
	t.Setenv("HERDR_NOTIFY_FAIL", "1")
	if code, _, errs := run(t, "", "orphans", "--tool", "herdr", "--notify"); code != 0 || !strings.Contains(errs, "couldn't notify herdr") || !strings.Contains(errs, "conch-fakebox-box2") {
		t.Fatalf("notify failing: %d %q", code, errs)
	}
	// None: no notification at all.
	h.set(t, `[{"target":"conch-fakebox-box1"},{"target":"conch-fakebox-box2"},{"target":"conch-fakebox-box4"}]`)
	before := strings.Count(h.calls(), "notification")
	code, out, errs = run(t, "", "orphans", "--tool", "herdr", "--notify", "--json")
	if code != 0 || len(decode[orphansJSON](t, out).Orphans) != 0 || !strings.Contains(out, `"orphans":[]`) || strings.Count(h.calls(), "notification") != before {
		t.Fatalf("none: %q %q", out, errs)
	}

	// The herdr app, through hc: herdr's sandboxes are not its orphans,
	// only those made for it.
	fakeHC(t, `{"machines":[{"id":"a1","host":"conch-fakebox-box10"}]}`)
	code, out, _ = run(t, "", "orphans", "--tool", "herdr-app", "--json")
	if got := decode[orphansJSON](t, out); code != 0 || len(got.Orphans) != 0 {
		t.Fatalf("herdr-app sees herdr's: %s", out)
	}
	api.mu.Lock()
	api.n = 9 // the next are box10 and box11
	api.mu.Unlock()
	for range 2 {
		if code, _, errs := run(t, "", "create", "--provider", "fakebox", "--tool", "herdr-app", "--json", "--yes"); code != 0 {
			t.Fatalf("%q", errs)
		}
	}
	code, out, _ = run(t, "", "orphans", "--tool", "herdr-app", "--json")
	if got := decode[orphansJSON](t, out); code != 0 || len(got.Orphans) != 1 || got.Orphans[0].Alias != "conch-fakebox-box11" {
		t.Fatalf("herdr-app: %s", out)
	}
	// …and the herdr app's are not herdr's.
	code, out, _ = run(t, "", "orphans", "--tool", "herdr", "--json")
	if got := decode[orphansJSON](t, out); code != 0 || len(got.Orphans) != 0 {
		t.Fatalf("herdr sees the herdr app's: %s", out)
	}
}

// A sandbox in use by Orca, or made by hand, is in use where herdr cannot
// see: never offered to herdr as forgotten, nor to the herdr app.
func TestOrphansAreTheToolsOwn(t *testing.T) {
	isolate(t)
	fakeProvider(t, "fakebox", tokenKind)
	fakeSSH(t)
	fakeHerdr(t)
	fakeHC(t, `[]`)
	t.Setenv("ORCA_WORKSPACE_NAME", "w")
	for _, args := range [][]string{
		{"--from-orca", "--format", "orca"},        // box1, Orca's
		{"--format", "orca"},                       // box2, Orca's too
		{"--yes"},                                  // box3, the person's
		{"--tool", "herdr-app", "--json", "--yes"}, // box4, the herdr app's
	} {
		if code, _, errs := run(t, "", append([]string{"create", "--provider", "fakebox"}, args...)...); code != 0 {
			t.Fatalf("%v: %q", args, errs)
		}
	}
	for alias, tool := range map[string]string{"conch-fakebox-box1": "orca", "conch-fakebox-box2": "orca", "conch-fakebox-box3": "cli", "conch-fakebox-box4": "herdr-app"} {
		if own, err := aliasOwner(alias); err != nil || own.Tool != tool {
			t.Errorf("%s: %+v %v", alias, own, err)
		}
	}
	code, out, _ := run(t, "", "orphans", "--tool", "herdr", "--json")
	if got := decode[orphansJSON](t, out); code != 0 || len(got.Orphans) != 0 {
		t.Fatalf("herdr: %s", out)
	}
	code, out, _ = run(t, "", "orphans", "--tool", "herdr-app", "--json")
	if got := decode[orphansJSON](t, out); code != 0 || len(got.Orphans) != 1 || got.Orphans[0].Alias != "conch-fakebox-box4" {
		t.Fatalf("herdr-app: %s", out)
	}
	// A start under Orca keeps the alias Orca's; ssh-alias keeps it too.
	if code, _, errs := run(t, "", "start", "--provider", "fakebox", "box1", "--format", "orca"); code != 0 {
		t.Fatalf("%q", errs)
	}
	if code, _, errs := run(t, "", "ssh-alias", "--provider", "fakebox", "box1", "--yes"); code != 0 {
		t.Fatalf("%q", errs)
	}
	if own, _ := aliasOwner("conch-fakebox-box1"); own.Tool != "orca" {
		t.Fatalf("%+v", own)
	}
	// --tool is one of the four, and Orca's flags are Orca's.
	for _, args := range [][]string{{"--tool", "tmux"}, {"--tool", "herdr", "--format", "orca"}, {"--tool", "cli", "--from-orca"}} {
		if code, _, _ := run(t, "", append([]string{"create", "--provider", "fakebox"}, args...)...); code != 2 {
			t.Errorf("%v: %d", args, code)
		}
	}
}

// The herdr app's mod opens a workspace in project_root: there is one
// without a repository too, the sandbox's home.
func TestHerdrAppGetsARoot(t *testing.T) {
	isolate(t)
	fakeProvider(t, "fakebox", tokenKind)
	f := fakeSSH(t)
	code, out, errs := run(t, "", "create", "--provider", "fakebox", "--name", "n", "--tool", "herdr-app", "--json", "--yes")
	if code != 0 {
		t.Fatalf("%q", errs)
	}
	if r := decode[result](t, out); r.ProjectRoot != f.home || r.SSH == nil || r.SSH.Alias != "conch-fakebox-box1" {
		t.Fatalf("%s", out)
	}
	// The person's create asks nothing of the sandbox.
	if code, out, _ := run(t, "", "create", "--provider", "fakebox", "--json", "--yes"); code != 0 || decode[result](t, out).ProjectRoot != "" {
		t.Fatalf("cli: %s", out)
	}
}

func TestOrphansFailures(t *testing.T) {
	isolate(t)
	api := fakeProvider(t, "fakebox", tokenKind)
	// No herdr: cannot tell, so it fails rather than calling all orphans.
	if code, _, errs := run(t, "", "orphans", "--tool", "herdr"); code != 1 || !strings.Contains(errs, "list herdr's machines") {
		t.Fatalf("%d %q", code, errs)
	}
	fakeHerdr(t)
	// A provider that cannot list is said, and the rest go on.
	api.failOn["GET boxes"] = 500
	code, out, errs := run(t, "", "orphans", "--tool", "herdr", "--json")
	if code != 0 || !strings.Contains(errs, "couldn't list fakebox") || len(decode[orphansJSON](t, out).Problems) != 1 {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	for _, args := range [][]string{{"orphans"}, {"orphans", "--tool", "tmux"}, {"orphans", "--tool", "herdr", "x"}} {
		if code, _, _ := run(t, "", args...); code != 2 {
			t.Errorf("%v: %d", args, code)
		}
	}
}

// runPick runs pick at a "terminal" whose answers are in, from dir.
func runPick(t *testing.T, in, dir string) (int, string, string) {
	t.Helper()
	var out, errb strings.Builder
	a := &app{stdin: strings.NewReader(in), stdout: &out, stderr: &errb, terminal: true, wait: 30 * time.Second, retry: time.Millisecond, dir: dir}
	code := a.run(context.Background(), []string{"pick", "--tool", "herdr"})
	return code, out.String(), errb.String()
}

func TestPick(t *testing.T) {
	isolate(t)
	api := fakeProvider(t, "fakebox", tokenKind)
	f := fakeSSH(t)
	h := fakeHerdr(t)
	// One left over, which herdr forgot: offered for deletion first.
	run(t, "", "create", "--provider", "fakebox", "--tool", "herdr")
	// Answers: delete the orphan; provider 1 (Enter); name; region 3 (eu);
	// size 2 (large); a repository and its branch.
	in := "y\n\nmy box\n3\n2\nhttps://github.com/o/r\nmain\n"
	code, out, errs := runPick(t, in, t.TempDir())
	if code != 0 {
		t.Fatalf("%d %q", code, errs)
	}
	if api.has("box1") || !strings.Contains(errs, "Delete conch-fakebox-box1 (Fake Box, started)?") {
		t.Fatalf("orphan:\n%s", errs)
	}
	body := api.bodies["POST /boxes"]
	if !strings.Contains(body, `"region":"eu"`) || !strings.Contains(body, `"size":"large"`) || !strings.Contains(body, `"name":"conch-my-box-`) {
		t.Fatalf("create: %s", body)
	}
	if !strings.Contains(f.gitCalls(), "--branch main -- https://github.com/o/r") {
		t.Fatalf("clone:\n%s", f.gitCalls())
	}
	root := realHome(t, f, "my-box")
	calls := h.calls()
	for _, want := range []string{"machine add conch-fakebox-box2 --label Fake Box my box",
		"--machine m1 workspace create --cwd " + root + " --label my box --focus"} {
		if !strings.Contains(calls, want) {
			t.Fatalf("herdr lacks %q:\n%s", want, calls)
		}
	}
	if !strings.Contains(out, "conch-fakebox-box2") || !strings.Contains(out, root) {
		t.Fatalf("out: %q", out)
	}
}

func TestPickNoRepository(t *testing.T) {
	isolate(t)
	fakeProvider(t, "fakebox", tokenKind)
	f := fakeSSH(t)
	h := fakeHerdr(t)
	// Every default: one provider, "sandbox", the default region and
	// size, no repository (there is none here): the workspace is in home.
	code, _, errs := runPick(t, "\n\n\n\n\n", t.TempDir())
	if code != 0 {
		t.Fatalf("%d %q", code, errs)
	}
	if strings.Contains(f.gitCalls(), "clone") || !strings.Contains(h.calls(), "workspace create --cwd "+f.home+" --label sandbox") {
		t.Fatalf("%s\n%s", f.gitCalls(), h.calls())
	}
	// A wrong number is asked again; three is the end of it.
	code, _, errs = runPick(t, "9\nx\n0\n", t.TempDir())
	if code != 1 || !strings.Contains(errs, "give a number from 1 to") || !strings.Contains(errs, "no choice made") {
		t.Fatalf("%d %q", code, errs)
	}
	// The input ending halfway is an answer of no.
	if code, _, errs := runPick(t, "1\n", t.TempDir()); code != 1 || !strings.Contains(errs, "the input ended") {
		t.Fatalf("%d %q", code, errs)
	}
}

func TestPickFailures(t *testing.T) {
	isolate(t)
	// No terminal.
	if code, _, errs := run(t, "", "pick", "--tool", "herdr"); code != 1 || !strings.Contains(errs, "use conch-sandbox create") {
		t.Fatalf("%d %q", code, errs)
	}
	if code, _, _ := run(t, "", "pick", "--tool", "herdr-app"); code != 2 {
		t.Fatal("herdr-app has a dialog of its own")
	}
	// Nothing installed, then nothing with a key.
	fakeHerdr(t)
	if code, _, errs := runPick(t, "", t.TempDir()); code != 1 || !strings.Contains(errs, "none is installed") {
		t.Fatalf("%d %q", code, errs)
	}
	fakeProvider(t, "fakebox", tokenKind)
	t.Setenv("FAKEBOX_API_KEY", "")
	if code, _, errs := runPick(t, "", t.TempDir()); code != 1 || !strings.Contains(errs, "conch-sandbox key set fakebox") {
		t.Fatalf("%d %q", code, errs)
	}
	// herdr refusing the machine keeps the sandbox, and says how to go on.
	t.Setenv("FAKEBOX_API_KEY", "k-test")
	fakeSSH(t)
	bad := filepath.Join(t.TempDir(), "herdr")
	os.WriteFile(bad, []byte("#!/bin/sh\ncase \"$1 $2\" in \"machine list\") echo '[]';; *) exit 1;; esac\n"), 0o755)
	t.Setenv("HERDR_BIN_PATH", bad)
	if code, _, errs := runPick(t, "\n\n\n\n\n", t.TempDir()); code != 1 || !strings.Contains(errs, "conch-sandbox rm --machine conch-fakebox-box1") {
		t.Fatalf("%d %q", code, errs)
	}
}

func TestGitDefaults(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	git := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	if r := gitDefaults(context.Background(), dir); r != (repo{}) {
		t.Fatalf("not a repository: %+v", r)
	}
	git("init", "-q", "-b", "feat-x")
	git("commit", "-q", "--allow-empty", "-m", "x")
	if r := gitDefaults(context.Background(), dir); r != (repo{}) {
		t.Fatalf("no remote: %+v", r)
	}
	git("remote", "add", "origin", "git@github.com:o/r.git")
	r := gitDefaults(context.Background(), dir)
	if r.URL != "https://github.com/o/r.git" || r.Branch != "feat-x" || len(r.Ref) != 40 {
		t.Fatalf("%+v", r)
	}
	for in, want := range map[string]string{
		"https://gitlab.com/o/r":       "https://gitlab.com/o/r",
		"git@github.com:o/r.git":       "https://github.com/o/r.git",
		"ssh://git@github.com/o/r.git": "https://github.com/o/r.git",
		"ssh://git@host:2222/o/r.git":  "",
		"/srv/repo.git":                "",
		"git@:o/r":                     "",
		"http://insecure.example/r":    "",
	} {
		if got := httpsOf(in); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
}

// hc is $HC_BIN_PATH where that is set — the Dock's PATH may not have
// it — else PATH's.
func TestHCBin(t *testing.T) {
	isolate(t)
	fakeProvider(t, "fakebox", tokenKind)
	fakeHC(t, `[]`)
	hc := os.Getenv("HC_BIN_PATH")
	if code, _, errs := run(t, "", "orphans", "--tool", "herdr-app"); code != 0 {
		t.Fatalf("by HC_BIN_PATH: %q", errs)
	}
	t.Setenv("HC_BIN_PATH", "")
	if hcBin() != "hc" {
		t.Fatal(hcBin())
	}
	if code, _, errs := run(t, "", "orphans", "--tool", "herdr-app"); code != 1 || !strings.Contains(errs, "hc machine ls --json") {
		t.Fatalf("none on PATH: %d %q", code, errs)
	}
	t.Setenv("PATH", filepath.Dir(hc)+":/usr/bin:/bin")
	if code, _, errs := run(t, "", "orphans", "--tool", "herdr-app"); code != 0 {
		t.Fatalf("by PATH: %q", errs)
	}
}
