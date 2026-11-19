package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The herdr app knows a sandbox only by its machine, the alias: every
// verb it calls takes --machine.
func TestMachineFlag(t *testing.T) {
	isolate(t)
	api := fakeProvider(t, "fakebox", tokenKind)
	// What the herdr app's mod runs, non-interactively.
	code, out, errs := run(t, "", "create", "--provider", "fakebox", "--name", "N", "--json", "--yes", "--size", "large")
	if code != 0 || decode[result](t, out).SSH.Alias != "conch-fakebox-box1" {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	if !strings.Contains(api.bodies["POST /boxes"], `"size":"large"`) {
		t.Fatalf("size: %s", api.bodies["POST /boxes"])
	}
	for _, verb := range []string{"stop", "start", "get"} {
		if code, out, errs := run(t, "", verb, "--machine", "conch-fakebox-box1", "--json"); code != 0 || decode[result](t, out).ID != "box1" {
			t.Fatalf("%s: %d %q %q", verb, code, out, errs)
		}
	}
	// A provider given as well must be the alias's own.
	if code, _, errs := run(t, "", "get", "--machine", "conch-fakebox-box1", "--provider", "other"); code != 1 || !strings.Contains(errs, "is a fakebox sandbox, not other") {
		t.Fatalf("other provider: %d %q", code, errs)
	}
	if code, _, _ := run(t, "", "get", "--machine", "conch-fakebox-box1", "--provider", "fakebox"); code != 0 {
		t.Fatal("its own provider refused")
	}
	// Two ways to say which: refused.
	if code, _, errs := run(t, "", "get", "--machine", "conch-fakebox-box1", "--provider", "fakebox", "box1"); code != 2 || !strings.Contains(errs, "give one sandbox") {
		t.Fatalf("both: %d %q", code, errs)
	}
	// An alias that isn't conch-sandbox's: one somebody wrote by hand that
	// looks like ours, and one that is not there at all.
	f, _ := os.OpenFile(aliasFile(), os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("\nHost conch-fakebox-box9\n  HostName 1.2.3.4\n")
	f.WriteString(beginMark + "conch-fakebox-box8\nHost conch-fakebox-box8\n" + endMark + "conch-fakebox-box8\n")
	f.WriteString(beginMark + "conch-fakebox-box7\n" + metaMark + "provider=fakebox id=box6\n" + endMark + "conch-fakebox-box7\n")
	f.Close()
	for _, m := range []string{"conch-fakebox-box9", "conch-fakebox-box8", "conch-fakebox-box7", "myserver", "conch-nope-x"} {
		if code, _, errs := run(t, "", "rm", "--machine", m); code != 1 || !strings.Contains(errs, "is not one of conch-sandbox's ssh aliases") {
			t.Errorf("%s: %d %q", m, code, errs)
		}
	}
	if !api.has("box1") {
		t.Fatal("deleted by a refused rm")
	}
	code, _, errs = run(t, "", "rm", "--machine", "conch-fakebox-box1")
	if code != 0 || api.has("box1") || hasAlias("conch-fakebox-box1") {
		t.Fatalf("rm: %d %q", code, errs)
	}
	// Gone with its alias: --machine has nothing to go on.
	if code, _, _ := run(t, "", "rm", "--machine", "conch-fakebox-box1"); code != 1 {
		t.Fatal("rm of a forgotten alias")
	}
}

// A provider or an id with - in it is read back from the alias's own
// line, not split at a guess.
func TestMachineWithDashes(t *testing.T) {
	isolate(t)
	fakeProvider(t, "fake-box", tokenKind)
	if code, _, errs := run(t, "", "create", "--provider", "fake-box"); code != 0 {
		t.Fatalf("%q", errs)
	}
	al := alias{Provider: "fake-box", ID: "a-b-c", Host: "h", User: "u", Root: "/home/me/my dir/x=y&z"}
	if err := setAlias(al); err != nil {
		t.Fatal(err)
	}
	own, err := aliasOwner("conch-fake-box-a-b-c")
	if err != nil || own != (owner{Provider: "fake-box", ID: "a-b-c", Root: "/home/me/my dir/x=y&z", Tool: "cli"}) {
		t.Fatalf("%+v %v", own, err)
	}
	// Rewritten with a fresh token, it keeps its root.
	al.Root, al.User = "", "u2"
	setAlias(al)
	if own, _ := aliasOwner("conch-fake-box-a-b-c"); own.Root != "/home/me/my dir/x=y&z" {
		t.Fatalf("root lost: %+v", own)
	}
	if code, out, _ := run(t, "", "ssh-alias", "--machine", "conch-fake-box-a-b-c", "--remove", "--json"); code != 0 ||
		!decode[aliasJSON](t, out).Removed || hasAlias("conch-fake-box-a-b-c") {
		t.Fatalf("remove: %s", out)
	}
	if code, out, _ := run(t, "", "get", "--machine", "conch-fake-box-box1", "--json"); code != 0 || decode[result](t, out).Provider != "fake-box" {
		t.Fatalf("get: %s", out)
	}
}

func TestParseMeta(t *testing.T) {
	for line, ok := range map[string]bool{
		metaMark + "provider=p id=i":             true,
		metaMark + "id=i provider=p root=%2Fx":   true,
		metaMark + "provider=p":                  false,
		metaMark + "id=i":                        false,
		"# conch-sandbox: p i":                   false,
		"  " + metaMark + "provider=p id=i":      false,
		metaMark + "provider=p id=i extra=thing": true,
	} {
		if _, got := parseMeta(line); got != ok {
			t.Errorf("%q: %v", line, got)
		}
	}
	// The tool it was made for; none said is the person's.
	if o, _ := parseMeta(metaMark + "provider=p id=i tool=herdr-app"); o.Tool != "herdr-app" {
		t.Errorf("%+v", o)
	}
	if o, _ := parseMeta(metaMark + "provider=p id=i"); o.Tool != "cli" {
		t.Errorf("%+v", o)
	}
}

func TestFromOrca(t *testing.T) {
	isolate(t)
	api := fakeProvider(t, "fakebox", tokenKind)
	f := fakeSSH(t)
	t.Setenv("ORCA_WORKSPACE_NAME", "feat-login")
	t.Setenv("ORCA_REPO_URL", "https://github.com/o/r.git")
	t.Setenv("ORCA_REPO_BRANCH", "feat-login")
	t.Setenv("ORCA_REPO_REF", "0123abcd")
	code, out, errs := run(t, "", "create", "--provider", "fakebox", "--from-orca", "--format", "orca")
	if code != 0 {
		t.Fatalf("%d %q", code, errs)
	}
	o := decode[orcaResult](t, out)
	root := realHome(t, f, "feat-login")
	want := orcaResult{SchemaVersion: 2, CheckoutMode: "provisioned-root",
		Connection: orcaConnection{Type: "ssh", ProjectRoot: root,
			Target: orcaTarget{Label: "Fake Box feat-login", Host: "gw.test", Port: 22, Username: "tok-secret-box1-1"}},
		UserData: orcaUserData{Provider: "fakebox", ID: "box1"}}
	if o != want {
		t.Fatalf("got  %+v\nwant %+v", o, want)
	}
	if !strings.Contains(f.gitCalls(), "-B feat-login 0123abcd") || !strings.Contains(api.bodies["POST /boxes"], `"name":"conch-feat-login-`) {
		t.Fatalf("from Orca's variables:\n%s\n%s", f.gitCalls(), api.bodies["POST /boxes"])
	}
	// The token is Orca's only way in, so it is there; on stderr, never.
	noToken(t, errs)

	// Orca hands userData back; resume is start with a new token.
	run(t, "", "stop", "--provider", "fakebox", "--from-orca") // unset: refused
	t.Setenv("ORCA_VM_INSTANCE_ID", `{"provider":"fakebox","id":"box1"}`)
	if code, _, errs := run(t, "", "stop", "--provider", "fakebox", "--from-orca"); code != 0 {
		t.Fatalf("suspend: %q", errs)
	}
	code, out, errs = run(t, "", "start", "--provider", "fakebox", "--from-orca", "--format", "orca")
	o = decode[orcaResult](t, out)
	if code != 0 || o.Connection.Target.Username != "tok-secret-box1-2" || o.Connection.ProjectRoot != root ||
		o.Connection.Target.Label != "Fake Box feat-login" || o.UserData.ID != "box1" {
		t.Fatalf("resume: %d %+v %q", code, o, errs)
	}
	// The id alone, as ORCA_VM_INSTANCE_ID may also be.
	t.Setenv("ORCA_VM_INSTANCE_ID", "box1")
	if code, _, errs := run(t, "", "rm", "--provider", "fakebox", "--from-orca"); code != 0 || api.has("box1") {
		t.Fatalf("destroy: %q", errs)
	}
}

func TestFromOrcaRefusals(t *testing.T) {
	isolate(t)
	fakeProvider(t, "fakebox", tokenKind)
	for env, want := range map[string]string{
		"":                                 "ORCA_VM_INSTANCE_ID is not set",
		`{"provider":"other","id":"b"}`:    "that sandbox is other's, not fakebox's",
		`{"provider":"fakebox"}`:           "neither an id nor",
		`{nope`:                            "neither an id nor",
		"a b":                              "not a sandbox ID",
		`{"provider":"fakebox","id":"a*"}`: "not a sandbox ID",
	} {
		t.Setenv("ORCA_VM_INSTANCE_ID", env)
		if code, _, errs := run(t, "", "stop", "--provider", "fakebox", "--from-orca"); code == 0 || !strings.Contains(errs, want) {
			t.Errorf("%q: %d %q", env, code, errs)
		}
	}
	// userData names its provider, so --provider may be left out.
	t.Setenv("ORCA_VM_INSTANCE_ID", `{"provider":"fakebox","id":"nobox"}`)
	if code, _, errs := run(t, "", "get", "--from-orca"); code != 1 || !strings.Contains(errs, "Fake Box has no sandbox nobox") {
		t.Fatalf("%d %q", code, errs)
	}
	for _, args := range [][]string{
		{"create", "--provider", "fakebox", "--json", "--format", "orca"},
		{"create", "--provider", "fakebox", "--format", "yaml"},
		{"stop", "--provider", "fakebox", "b", "--format", "orca"},
	} {
		if code, out, _ := run(t, "", args...); code != 2 || out != "" {
			t.Errorf("%v: %d %q", args, code, out)
		}
	}
}

// Orca connects by the address it is given, so it needs no Include; with
// one, a key sandbox is given as its alias so ssh offers the key it was
// set up with.
func TestOrcaWithoutAndWithInclude(t *testing.T) {
	home := bare(t)
	userKey(t, home)
	fakeProvider(t, "keybox", keyKind)
	f := fakeSSH(t)
	code, out, errs := run(t, "", "create", "--provider", "keybox", "--format", "orca")
	if code != 0 {
		t.Fatalf("%q", errs)
	}
	o := decode[orcaResult](t, out)
	if o.Connection.Target.Host != "10.0.0.9" || o.Connection.Target.Port != 2222 || o.Connection.Target.Username != "root" ||
		o.Connection.ProjectRoot != f.home {
		t.Fatalf("%+v", o)
	}
	if !hasAlias("conch-keybox-box1") {
		t.Fatal("no alias written")
	}
	if _, err := os.Stat(filepath.Join(home, ".ssh", "config")); err == nil {
		t.Fatal("~/.ssh/config written without asking")
	}
	// --yes adds the line, and the key sandbox is given by its alias.
	code, out, _ = run(t, "", "create", "--provider", "keybox", "--format", "orca", "--yes")
	if o := decode[orcaResult](t, out); code != 0 || o.Connection.Target.Host != "conch-keybox-box2" || !includePresent() {
		t.Fatalf("%+v", o)
	}
}

func TestFromHerdr(t *testing.T) {
	isolate(t)
	api := fakeProvider(t, "fakebox", tokenKind)
	h := fakeHerdr(t)
	for range 2 {
		run(t, "", "create", "--provider", "fakebox", "--yes")
	}
	// The context names the machine's target…
	t.Setenv("HERDR_PLUGIN_CONTEXT_JSON", `{"workspace":{"id":"w1","machine":{"target":"conch-fakebox-box1"}}}`)
	if code, out, errs := run(t, "", "stop", "--from-herdr", "--json"); code != 0 || decode[result](t, out).ID != "box1" {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	// …or only herdr's id for it, which herdr's list turns into the target.
	h.set(t, `[{"id":"m7","label":"Fake Box two","target":"me@conch-fakebox-box2"}]`)
	t.Setenv("HERDR_PLUGIN_CONTEXT_JSON", `{"workspace_id":"w2","machine_id":"m7"}`)
	if code, out, errs := run(t, "", "get", "--from-herdr", "--json"); code != 0 || decode[result](t, out).ID != "box2" {
		t.Fatalf("by id: %d %q %q", code, out, errs)
	}
	// Delete also removes herdr's machine.
	code, _, errs := run(t, "", "rm", "--from-herdr")
	if code != 0 || api.has("box2") || !strings.Contains(h.calls(), "machine remove m7") || !strings.Contains(errs, "removed herdr's machine Fake Box two") {
		t.Fatalf("rm: %d %q\n%s", code, errs, h.calls())
	}
	for ctx, want := range map[string]string{
		"":                    "HERDR_PLUGIN_CONTEXT_JSON is not set",
		"{bad":                "not JSON",
		`{"machine":"local"}`: "not on one of conch-sandbox's sandboxes",
		`{"target":"conch-fakebox-box1","machine":"conch-x-y"}`: "", // only one is ours
		// A name anywhere else is not the workspace's machine.
		`{"a":"conch-fakebox-box1"}`:                              "not on one of",
		`{"workspace":{"label":"conch-fakebox-box1","cwd":"/x"}}`: "not on one of",
	} {
		t.Setenv("HERDR_PLUGIN_CONTEXT_JSON", ctx)
		code, _, errs := run(t, "", "get", "--from-herdr")
		if want == "" {
			if code != 0 {
				t.Errorf("%q: %d %q", ctx, code, errs)
			}
			continue
		}
		if code == 0 || !strings.Contains(errs, want) {
			t.Errorf("%q: %d %q", ctx, code, errs)
		}
	}
	setAlias(alias{Provider: "fakebox", ID: "box5", Host: "h", User: "u"})
	t.Setenv("HERDR_PLUGIN_CONTEXT_JSON", `[{"target":"conch-fakebox-box1"},{"machine":{"host":"conch-fakebox-box5"}}]`)
	if code, _, errs := run(t, "", "get", "--from-herdr"); code != 1 || !strings.Contains(errs, "more than one sandbox: conch-fakebox-box1, conch-fakebox-box5") {
		t.Fatalf("two: %d %q", code, errs)
	}
}

// Orca's repository address is the workspace's origin, usually git@…:
// cloned from its https address. One that has none is refused.
func TestFromOrcaSSHRemote(t *testing.T) {
	isolate(t)
	api := fakeProvider(t, "fakebox", tokenKind)
	f := fakeSSH(t)
	t.Setenv("ORCA_WORKSPACE_NAME", "w")
	t.Setenv("ORCA_REPO_URL", "git@github.com:o/r.git")
	t.Setenv("ORCA_REPO_BRANCH", "feat")
	t.Setenv("ORCA_REPO_REF", "abcdef1")
	if code, _, errs := run(t, "", "create", "--provider", "fakebox", "--from-orca", "--format", "orca"); code != 0 {
		t.Fatalf("%q", errs)
	}
	if git := f.gitCalls(); !strings.Contains(git, "clone --quiet -- https://github.com/o/r.git ") {
		t.Fatalf("git:\n%s", git)
	}
	t.Setenv("ORCA_REPO_URL", "/srv/git/r.git")
	if code, _, errs := run(t, "", "create", "--provider", "fakebox", "--from-orca", "--format", "orca"); code != 2 || !strings.Contains(errs, `"/srv/git/r.git": give the repository's https:// address`) {
		t.Fatalf("%d %q", code, errs)
	}
	if n := strings.Count(api.called(), "POST /boxes\n"); n != 1 {
		t.Fatalf("%d made:\n%s", n, api.called())
	}
}

// A workspace here labelled as a sandbox's herdr machine is ("feat-login",
// as pick labels them) is not on that sandbox: labels never pick one, so
// rm --from-herdr deletes nothing of somebody else's.
func TestFromHerdrIgnoresLabels(t *testing.T) {
	isolate(t)
	api := fakeProvider(t, "fakebox", tokenKind)
	h := fakeHerdr(t)
	run(t, "", "create", "--provider", "fakebox", "--yes")
	h.set(t, `[{"id":"m1","label":"feat-login","target":"conch-fakebox-box1"}]`)
	for _, ctx := range []string{
		`{"workspace":{"id":"w1","label":"feat-login"}}`,
		`{"workspace":{"id":"w1","label":"feat-login","machine_id":"local"}}`,
		`{"machine_id":"feat-login"}`,
	} {
		t.Setenv("HERDR_PLUGIN_CONTEXT_JSON", ctx)
		if code, _, errs := run(t, "", "rm", "--from-herdr"); code != 1 || !strings.Contains(errs, "not on one of conch-sandbox's sandboxes") {
			t.Fatalf("%s: %d %q", ctx, code, errs)
		}
	}
	if !api.has("box1") || strings.Contains(h.calls(), "machine remove") {
		t.Fatalf("deleted:\n%s", h.calls())
	}
	// Its id, in the workspace's machine, is.
	t.Setenv("HERDR_PLUGIN_CONTEXT_JSON", `{"workspace":{"id":"w1","label":"feat-login","machine":{"id":"m1"}}}`)
	if code, out, errs := run(t, "", "get", "--from-herdr", "--json"); code != 0 || decode[result](t, out).ID != "box1" {
		t.Fatalf("%d %q", code, errs)
	}
}
