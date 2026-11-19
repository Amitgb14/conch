package sshkey

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/sandbox"
)

// isolate gives the test a home and a CONCH_HOME of its own, so neither
// the developer's ~/.ssh nor their conch key is ever read or made.
func isolate(t *testing.T) (home string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CONCH_HOME", filepath.Join(home, "conch"))
	t.Setenv("CONCH_SSH_KEYGEN", "/bin/false")
	return home
}

// fakeKeygen writes the pair it is asked for, as ssh-keygen -f does, and
// counts its runs in a file beside it.
func fakeKeygen(t *testing.T) (count func() int) {
	t.Helper()
	dir := t.TempDir()
	keygen := filepath.Join(dir, "ssh-keygen")
	runs := filepath.Join(dir, "runs")
	os.WriteFile(keygen, []byte(`#!/bin/sh
for a in "$@"; do last=$a; done
printf x >> "`+runs+`"
printf 'private' > "$last"
printf 'ssh-ed25519 AAAAC3 conch@test\n' > "$last.pub"
`), 0o755)
	t.Setenv("CONCH_SSH_KEYGEN", keygen)
	return func() int { b, _ := os.ReadFile(runs); return len(b) }
}

func writeKey(t *testing.T, path, content string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o700)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestPublicMakesConchKeyOnce(t *testing.T) {
	isolate(t)
	runs := fakeKeygen(t)
	k, err := Public(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if k.Path != ConchKey()+".pub" || k.Line != "ssh-ed25519 AAAAC3 conch@test" || !k.Made {
		t.Fatalf("first: %+v", k)
	}
	if k.Identity() != ConchKey() {
		t.Fatalf("identity %q", k.Identity())
	}
	if fi, err := os.Stat(filepath.Dir(ConchKey())); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("key folder: %v %v", fi, err)
	}
	// The second time it is there: not made again, not said to be new.
	k, err = Public(context.Background())
	if err != nil || k.Made || runs() != 1 {
		t.Fatalf("second: %+v %v runs=%d", k, err, runs())
	}
}

func TestPublicPrefersTheUsersOwnKey(t *testing.T) {
	home := isolate(t)
	// An unusable id_ed25519.pub is passed over for a usable id_rsa.pub,
	// the order ssh itself tries them in.
	writeKey(t, filepath.Join(home, ".ssh", "id_ed25519.pub"), "-----BEGIN junk")
	writeKey(t, filepath.Join(home, ".ssh", "id_rsa.pub"), "ssh-rsa AAAAB3 me@here\n")
	k, err := Public(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if k.Line != "ssh-rsa AAAAB3 me@here" || k.Path != filepath.Join(home, ".ssh", "id_rsa.pub") || k.Made {
		t.Fatalf("%+v", k)
	}
	if k.Identity() != filepath.Join(home, ".ssh", "id_rsa") {
		t.Fatalf("identity %q", k.Identity())
	}
	// ecdsa comes before rsa.
	writeKey(t, filepath.Join(home, ".ssh", "id_ecdsa.pub"), "ecdsa-sha2-nistp256 AAAAE2 me@here")
	if k, _ := Public(context.Background()); !strings.HasPrefix(k.Line, "ecdsa-") {
		t.Fatalf("order: %+v", k)
	}
}

func TestPublicFailures(t *testing.T) {
	isolate(t)
	// ssh-keygen failing says so.
	if _, err := Public(context.Background()); err == nil || !strings.Contains(err.Error(), "create an ssh key") {
		t.Fatalf("keygen failure: %v", err)
	}
	// A conch key that is there but unusable is not regenerated over.
	writeKey(t, ConchKey()+".pub", "not a key")
	if _, err := Public(context.Background()); err == nil || !strings.Contains(err.Error(), "no usable public key") {
		t.Fatalf("bad key: %v", err)
	}
	// A folder that cannot be made.
	isolate(t)
	t.Setenv("CONCH_HOME", "/dev/null/conch")
	if _, err := Public(context.Background()); err == nil {
		t.Fatal("no folder, yet a key")
	}
	// A cancelled context stops ssh-keygen before it runs.
	isolate(t)
	fakeKeygen(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Public(ctx); err == nil {
		t.Fatal("cancelled, yet a key")
	}
}

func TestValidLine(t *testing.T) {
	for s, want := range map[string]bool{
		"ssh-ed25519 AAAA me":          true,
		"ecdsa-sha2-nistp256 AAAA":     true,
		"sk-ssh-ed25519@openssh.com A": true,
		"ssh-rsa":                      false,
		"":                             false,
		"   ":                          false,
		"-----BEGIN OPENSSH":           false,
	} {
		if ValidLine(s) != want {
			t.Errorf("%q: %v", s, !want)
		}
	}
}

// provider is the least a sandbox.Provider is, plus whichever key kind a
// test gives it.
type provider struct{ sandbox.Provider }

type authorizer struct {
	provider
	wants bool
	err   error
	got   []string
}

func (a *authorizer) AuthorizeKey(_ context.Context, id, key string) error {
	a.got = append(a.got, id+" "+key)
	return a.err
}
func (a *authorizer) AuthorizesKey() bool { return a.wants }

type accountKeyed struct {
	provider
	on bool
}

func (a accountKeyed) AccountKey() bool { return a.on }

func TestAuthorize(t *testing.T) {
	home := isolate(t)
	writeKey(t, filepath.Join(home, ".ssh", "id_ed25519.pub"), "ssh-ed25519 AAAAmine me")
	mine := filepath.Join(home, ".ssh", "id_ed25519")
	ctx := context.Background()

	// A provider that takes the key through its API is given it.
	a := &authorizer{wants: true}
	if id, err := Authorize(ctx, a, "box1", nil); err != nil || id != mine {
		t.Fatalf("authorize: %q %v", id, err)
	}
	if len(a.got) != 1 || a.got[0] != "box1 ssh-ed25519 AAAAmine me" {
		t.Fatalf("given %q", a.got)
	}
	// One whose file does not ask for it is given nothing and offers none.
	a = &authorizer{wants: false}
	if id, err := Authorize(ctx, a, "box1", nil); err != nil || id != "" || len(a.got) != 0 {
		t.Fatalf("not wanted: %q %v %q", id, err, a.got)
	}
	// The provider refusing the key is an error.
	a = &authorizer{wants: true, err: errors.New("quota")}
	if _, err := Authorize(ctx, a, "box1", nil); err == nil || !strings.Contains(err.Error(), "quota") {
		t.Fatalf("refused: %v", err)
	}
	// A provider letting in the account's keys offers the same key.
	if id, err := Authorize(ctx, accountKeyed{on: true}, "box1", nil); err != nil || id != mine {
		t.Fatalf("account: %q %v", id, err)
	}
	if id, err := Authorize(ctx, accountKeyed{on: false}, "box1", nil); err != nil || id != "" {
		t.Fatalf("account off: %q %v", id, err)
	}
	// A token provider needs no key at all.
	if id, err := Authorize(ctx, provider{}, "box1", nil); err != nil || id != "" {
		t.Fatalf("token: %q %v", id, err)
	}
}

func TestAuthorizeMakesKeyAndSaysSo(t *testing.T) {
	isolate(t)
	fakeKeygen(t)
	made := 0
	for range 2 {
		id, err := Authorize(context.Background(), accountKeyed{on: true}, "b", func() { made++ })
		if err != nil || id != ConchKey() {
			t.Fatalf("%q %v", id, err)
		}
	}
	if made != 1 {
		t.Fatalf("told %d times of a new key", made)
	}
	// No key to be had is an error, before anything is asked of the API.
	isolate(t)
	a := &authorizer{wants: true}
	if _, err := Authorize(context.Background(), a, "b", nil); err == nil || len(a.got) != 0 {
		t.Fatalf("no key: %v %q", err, a.got)
	}
	if _, err := Authorize(context.Background(), accountKeyed{on: true}, "b", nil); err == nil {
		t.Fatal("no key for an account provider")
	}
}

func TestUseSetsTheCreateKey(t *testing.T) {
	isolate(t)
	fakeKeygen(t)
	old := sandbox.PublicKey
	t.Cleanup(func() { sandbox.PublicKey = old })
	made := 0
	Use(func() { made++ })
	for range 2 {
		line, err := sandbox.PublicKey(context.Background())
		if err != nil || line != "ssh-ed25519 AAAAC3 conch@test" {
			t.Fatalf("%q %v", line, err)
		}
	}
	if made != 1 {
		t.Fatalf("made %d", made)
	}
	// Without a callback, and with no key to be had.
	isolate(t)
	Use(nil)
	if _, err := sandbox.PublicKey(context.Background()); err == nil {
		t.Fatal("no key")
	}
}
