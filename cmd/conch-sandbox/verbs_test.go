package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/sandbox"
)

// noToken fails when the provider's token reached anything printed.
func noToken(t *testing.T, outs ...string) {
	t.Helper()
	for _, s := range outs {
		if strings.Contains(s, "tok-secret") {
			t.Fatalf("the token was printed:\n%s", s)
		}
	}
}

func TestCreateTokenKind(t *testing.T) {
	isolate(t)
	f := fakeProvider(t, "fakebox", tokenKind)
	code, out, errs := run(t, "", "create", "--provider", "fakebox", "--name", "Feat Login", "--region", "eu", "--json")
	if code != 0 {
		t.Fatalf("%d %q", code, errs)
	}
	noToken(t, out, errs)
	r := decode[result](t, out)
	if r.Schema != 1 || r.Provider != "fakebox" || r.ID != "box1" || r.State != "started" || r.Region != "eu" {
		t.Fatalf("%+v", r)
	}
	if !strings.HasPrefix(r.Name, "conch-feat-login-") {
		t.Fatalf("name %q", r.Name)
	}
	if r.Size == nil || *r.Size != (sizeJSON{CPU: 2, MemoryGiB: 4, DiskGiB: 10}) {
		t.Fatalf("size %+v", r.Size)
	}
	s := r.SSH
	if s == nil || s.Kind != "token" || s.Host != "gw.test" || s.Port != 22 || s.User != "…" || s.IdentityFile != "" || s.Alias != "conch-fakebox-box1" {
		t.Fatalf("ssh %+v", s)
	}
	if s.Expires == nil || !s.Expires.Equal(time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("expires %v", s.Expires)
	}
	// Labelled as conch's, with the region and size the file sends.
	body := f.bodies["POST /boxes"]
	for _, want := range []string{`"labels":{"conch":"1"}`, `"region":"eu"`, `"size":"small"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("create body lacks %s: %s", want, body)
		}
	}
	if !strings.Contains(errs, "Creating a Fake Box sandbox") || !strings.Contains(errs, "sandbox box1 started") {
		t.Fatalf("progress: %q", errs)
	}
	// conch's own provider code lists it as one of conch's.
	p, _, _ := ready("fakebox")
	list, err := p.List(context.Background())
	if err == nil && len(list) == 0 {
		t.Fatal("not listed as conch's")
	}

	// Without --json: words, still no token.
	code, out, errs = run(t, "", "create", "--provider", "fakebox")
	if code != 0 || !strings.Contains(out, "Fake Box sandbox box2") || !strings.Contains(out, "ssh     conch-fakebox-box2") {
		t.Fatalf("text: %d %q %q", code, out, errs)
	}
	noToken(t, out, errs)
}

func TestCreateNamedByLabel(t *testing.T) {
	isolate(t)
	f := fakeProvider(t, "fakebox", labelKind)
	if code, _, errs := run(t, "", "create", "--provider", "fakebox", "--name", "feat", "--json"); code != 0 {
		t.Fatalf("%d %q", code, errs)
	}
	// A file that sends the label as the name is handed conch's name.
	if body := f.bodies["POST /boxes"]; !strings.Contains(body, `"name":"conch-feat-`) {
		t.Fatalf("body %s", body)
	}
	// No name at all is still conch's.
	if code, _, errs := run(t, "", "create", "--provider", "fakebox", "--json"); code != 0 {
		t.Fatalf("%d %q", code, errs)
	}
	if body := f.bodies["POST /boxes"]; !strings.Contains(body, `"name":"conch-`) {
		t.Fatalf("unnamed body %s", body)
	}
}

func TestCreateKeyKind(t *testing.T) {
	home := isolate(t)
	identity := userKey(t, home)
	f := fakeProvider(t, "keybox", keyKind)
	code, out, errs := run(t, "", "create", "--json", "--provider", "keybox")
	if code != 0 {
		t.Fatalf("%d %q", code, errs)
	}
	s := decode[result](t, out).SSH
	if s == nil || s.Kind != "key" || s.Host != "10.0.0.9" || s.Port != 2222 || s.User != "root" || s.IdentityFile != identity || s.Expires != nil ||
		s.Alias != "conch-keybox-box1" {
		t.Fatalf("ssh %+v", s)
	}
	if len(f.keys) != 1 || f.keys[0] != "ssh-ed25519 AAAAmine me@here" {
		t.Fatalf("keys handed over: %q", f.keys)
	}
}

// A sandbox that was made but could not be readied is deleted: it costs
// while it runs, and nobody else knows it is there.
func TestCreateFailureDeletesTheBox(t *testing.T) {
	for _, c := range []struct {
		name string
		kind providerKind
		fail string
		want string
	}{
		{"ssh access refused", tokenKind, "POST boxes ssh", "ssh access"},
		{"key refused", keyKind, "POST boxes keys", "let your ssh key in"},
	} {
		t.Run(c.name, func(t *testing.T) {
			home := isolate(t)
			userKey(t, home)
			f := fakeProvider(t, "fakebox", c.kind)
			f.failOn[c.fail] = http.StatusInternalServerError
			code, out, errs := run(t, "", "create", "--provider", "fakebox", "--json")
			if code != 1 || out != "" || !strings.Contains(errs, c.want) {
				t.Fatalf("%d %q %q", code, out, errs)
			}
			if f.has("box1") || !strings.Contains(f.called(), "DELETE /boxes/box1") || !strings.Contains(errs, "deleted sandbox box1") {
				t.Fatalf("not deleted:\n%s\n%s", f.called(), errs)
			}
			if !sandbox.Deleted("fakebox", time.Now())["box1"] {
				t.Fatal("the delete is not recorded for conch sandbox ls")
			}
		})
	}

	// It came up in error: deleted too.
	isolate(t)
	f := fakeProvider(t, "fakebox", tokenKind)
	f.created = "failed"
	if code, _, errs := run(t, "", "create", "--provider", "fakebox"); code != 1 || f.has("box1") {
		t.Fatalf("error state: %d %q\n%s", code, errs, f.called())
	}

	// The delete failing too says how to do it by hand.
	isolate(t)
	f = fakeProvider(t, "fakebox", tokenKind)
	f.failOn["POST boxes ssh"] = http.StatusInternalServerError
	f.failOn["DELETE boxes"] = http.StatusInternalServerError
	code, _, errs := run(t, "", "create", "--provider", "fakebox")
	if code != 1 || !strings.Contains(errs, "conch-sandbox rm --provider fakebox box1") {
		t.Fatalf("delete failed: %d %q", code, errs)
	}

	// Create itself refused: nothing made, nothing to delete.
	isolate(t)
	f = fakeProvider(t, "fakebox", tokenKind)
	f.failOn["POST boxes"] = http.StatusForbidden
	if code, _, _ := run(t, "", "create", "--provider", "fakebox"); code != 1 || strings.Contains(f.called(), "DELETE") {
		t.Fatalf("refused: %d\n%s", code, f.called())
	}
}

// A cancelled run — the tool gave up, or somebody pressed ctrl-c — still
// deletes what it made.
func TestCreateCancelledDeletesTheBox(t *testing.T) {
	isolate(t)
	f := fakeProvider(t, "fakebox", tokenKind)
	f.created = "pending" // never comes up
	var out, errb strings.Builder
	a := &app{stdin: strings.NewReader(""), stdout: &out, stderr: &errb, wait: 300 * time.Millisecond}
	if code := a.run(context.Background(), []string{"create", "--provider", "fakebox"}); code != 1 {
		t.Fatalf("%d %q", code, errb.String())
	}
	if f.has("box1") {
		t.Fatalf("left running:\n%s\n%s", f.called(), errb.String())
	}
}

func TestCreateRefusesBeforeSpending(t *testing.T) {
	isolate(t)
	f := fakeProvider(t, "fakebox", tokenKind)
	for _, c := range []struct {
		args []string
		code int
		want string
	}{
		{[]string{"create"}, 2, "give --provider NAME"},
		{[]string{"create", "--provider", "nope"}, 1, `unknown sandbox provider "nope"; installed: fakebox`},
		{[]string{"create", "--provider", "fakebox", "extra"}, 2, "create takes no arguments"},
		{[]string{"create", "--provider", "fakebox", "--region", "mars"}, 1, "mars"},
	} {
		code, out, errs := run(t, "", c.args...)
		if code != c.code || out != "" || !strings.Contains(errs, c.want) {
			t.Errorf("%v: %d %q %q", c.args, code, out, errs)
		}
	}
	t.Setenv("FAKEBOX_API_KEY", "")
	if code, _, errs := run(t, "", "create", "--provider", "fakebox"); code != 1 || !strings.Contains(errs, "conch-sandbox key set fakebox") {
		t.Fatalf("no key: %d %q", code, errs)
	}
	if strings.Contains(f.called(), "POST /boxes") {
		t.Fatalf("a refused create reached the API:\n%s", f.called())
	}
}

func TestGetStartStopRm(t *testing.T) {
	isolate(t)
	f := fakeProvider(t, "fakebox", tokenKind)
	if code, _, errs := run(t, "", "create", "--provider", "fakebox", "--name", "x"); code != 0 {
		t.Fatalf("create: %q", errs)
	}
	// Flags before or after the ID.
	code, out, _ := run(t, "", "get", "box1", "--provider", "fakebox", "--json")
	r := decode[result](t, out)
	if code != 0 || r.ID != "box1" || r.State != "started" || r.SSH != nil {
		t.Fatalf("get: %d %+v", code, r)
	}
	code, out, _ = run(t, "", "stop", "--provider=fakebox", "--json", "box1")
	if r := decode[result](t, out); code != 0 || r.State != "stopped" || r.ID != "box1" {
		t.Fatalf("stop: %d %s", code, out)
	}
	if code, out, _ := run(t, "", "get", "--provider", "fakebox", "box1"); code != 0 || !strings.Contains(out, "stopped") {
		t.Fatalf("get text: %q", out)
	}
	// Start asks for a fresh token, and keeps it out of what it prints.
	before := strings.Count(f.called(), "/ssh")
	code, out, errs := run(t, "", "start", "--provider", "fakebox", "box1", "--json")
	r = decode[result](t, out)
	if code != 0 || r.State != "started" || r.SSH == nil || r.SSH.User != "…" || strings.Count(f.called(), "/ssh") != before+1 {
		t.Fatalf("start: %d %s %q", code, out, errs)
	}
	noToken(t, out, errs)

	code, out, _ = run(t, "", "rm", "--provider", "fakebox", "box1", "--json")
	if r := decode[result](t, out); code != 0 || r.State != "deleted" || f.has("box1") {
		t.Fatalf("rm: %d %s", code, out)
	}
	if !sandbox.Deleted("fakebox", time.Now())["box1"] {
		t.Fatal("rm not recorded")
	}
	// Gone already: still done, and said.
	code, out, errs = run(t, "", "rm", "--provider", "fakebox", "box1", "--json")
	if code != 0 || decode[result](t, out).State != "deleted" || !strings.Contains(errs, "already gone") {
		t.Fatalf("rm again: %d %q %q", code, out, errs)
	}
	for _, verb := range []string{"get", "start", "stop"} {
		if code, _, errs := run(t, "", verb, "--provider", "fakebox", "box1"); code != 1 || !strings.Contains(errs, "Fake Box has no sandbox box1") {
			t.Errorf("%s of a gone box: %d %q", verb, code, errs)
		}
	}
	// Given wrongly.
	for _, args := range [][]string{
		{"get", "--provider", "fakebox"},
		{"get", "--provider", "fakebox", "a", "b"},
		{"stop", "--provider", "fakebox", "a/b"},
		{"rm", "box1"},
	} {
		if code, out, errs := run(t, "", args...); code != 2 || out != "" || !strings.Contains(errs, "usage:") {
			t.Errorf("%v: %d %q %q", args, code, out, errs)
		}
	}
	// A provider whose file cannot stop is refused before the API.
	if err := can("nope-not-installed", "stop"); err != nil {
		t.Fatalf("a provider that says nothing is left to its file: %v", err)
	}
}

// A provider whose file has no way in over ssh is never opened (conch
// reaches every sandbox over ssh): create and start are refused before the
// provider is asked anything, and ~/.ssh/config is left alone.
func TestNoSSHWayInIsRefusedFirst(t *testing.T) {
	home := bare(t)
	api := fakeProvider(t, "fakebox", noSSHKind)
	for _, args := range [][]string{
		{"create", "--provider", "fakebox", "--yes"},
		{"start", "--provider", "fakebox", "box1"},
	} {
		code, out, errs := run(t, "", args...)
		if code != 1 || out != "" || !strings.Contains(errs, "[ssh] host and user") {
			t.Errorf("%v: %d %q %q", args, code, out, errs)
		}
	}
	if c := api.called(); c != "" {
		t.Fatalf("the provider was asked:\n%s", c)
	}
	if _, err := os.Stat(filepath.Join(home, ".ssh", "config")); !os.IsNotExist(err) {
		t.Fatalf("~/.ssh/config: %v", err)
	}
}

// The token in an alias is used later and again, so conch-sandbox asks for
// the longest life the file allows — which conch itself never does.
func TestAliasTokenAsksForTheLongestLife(t *testing.T) {
	isolate(t)
	api := fakeProvider(t, "fakebox", tokenKind)
	path := sandbox.ProviderFilePath("fakebox")
	b, _ := os.ReadFile(path)
	from := "[ssh.access]\ncall = \"POST /boxes/{id}/ssh\"\n"
	if !strings.Contains(string(b), from) {
		t.Fatal("the fake's file changed")
	}
	os.WriteFile(path, []byte(strings.Replace(string(b), from, "max_secs = 86400\n"+from+"body = { ttl = \"{expires_secs}\" }\n", 1)), 0o600)
	if code, _, errs := run(t, "", "create", "--provider", "fakebox"); code != 0 {
		t.Fatalf("%q", errs)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if got := api.bodies["POST /boxes/box1/ssh"]; got != `{"ttl":86400}` {
		t.Fatalf("asked %s", got)
	}
}
