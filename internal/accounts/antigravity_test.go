package accounts

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// keychainAt stands in for the Keychain for a test: what it holds, or a
// lookup that fails.
func keychainAt(t *testing.T, has bool, err error) *[]string {
	t.Helper()
	var asked []string
	old := keychainHas
	keychainHas = func(service, account string) (bool, error) {
		asked = append(asked, service+"/"+account)
		return has, err
	}
	t.Cleanup(func() { keychainHas = old })
	return &asked
}

// agy keeps its Google sign-in in the Keychain (service gemini, account
// antigravity — agy 1.5.16 on a Mac; /logout removes it), so whether
// there is one is asked of it by name, never by reading the token.
func TestAntigravityAuth(t *testing.T) {
	asked := keychainAt(t, true, nil)
	if got := AntigravityAuth("darwin", ""); got != AuthSignedIn {
		t.Errorf("signed in: %q", got)
	}
	if len(*asked) != 1 || (*asked)[0] != "gemini/antigravity" {
		t.Errorf("asked %v", *asked)
	}
	keychainAt(t, false, nil)
	if got := AntigravityAuth("darwin", ""); got != AuthSignedOut {
		t.Errorf("signed out: %q", got)
	}
	// A lookup that fails says nothing either way.
	keychainAt(t, false, errors.New("security: no such program"))
	if got := AntigravityAuth("darwin", ""); got != "" {
		t.Errorf("failed lookup: %q", got)
	}
	// A Gemini API key, set up for conch's launches (#366), is how it
	// signs in, whatever the Keychain holds; nothing is asked.
	asked = keychainAt(t, false, nil)
	if got := AntigravityAuth("darwin", "MY_GEMINI_KEY"); got != AuthAPIKey || len(*asked) != 0 {
		t.Errorf("api key: %q, asked %v", got, *asked)
	}
	// Elsewhere its keyring is not one conch reads: not known.
	asked = keychainAt(t, true, nil)
	if got := AntigravityAuth("linux", ""); got != "" || len(*asked) != 0 {
		t.Errorf("linux: %q, asked %v", got, *asked)
	}
}

// An Antigravity CLI account is a Gemini API key, by the variable that
// holds it, and a folder of its own for agy's files: its Google sign-in is
// one Keychain item whatever the folder, so a folder alone would be the
// same account with its conversations elsewhere.
func TestAntigravityAccountIsAKey(t *testing.T) {
	home := t.TempDir()
	var b Book
	a, err := b.AddWithKey("antigravity", "work", "", "WORK_GEMINI_KEY", home)
	if err != nil {
		t.Fatal(err)
	}
	if a.KeyEnv != "WORK_GEMINI_KEY" || a.Dir != filepath.Join(home, "accounts", "antigravity", "work") {
		t.Fatalf("%+v", a)
	}
	if st, err := os.Stat(a.Dir); err != nil || !st.IsDir() {
		t.Fatalf("folder not made: %v", err)
	}
	// Saved and read back with its key's name, never a key.
	path := filepath.Join(home, "accounts.json")
	if err := b.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil || len(got.Accounts) != 1 || got.Accounts[0].KeyEnv != "WORK_GEMINI_KEY" {
		t.Fatalf("%+v %v", got, err)
	}

	ch, err := got.Choose("antigravity", "work", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	env := strings.Join(ch.Env("antigravity"), " ")
	if env != "CONCH_AGY_DIR="+a.Dir+" CONCH_AGY_KEY_ENV=WORK_GEMINI_KEY" {
		t.Errorf("env: %q", env)
	}
	// Its own account is the sign-in it already has: nothing set.
	if ch, _ := got.Choose("antigravity", "", "", nil); len(ch.Env("antigravity")) != 0 {
		t.Errorf("own account: %v", ch.Env("antigravity"))
	}
	// Pinned and failed over to, it carries its key too.
	if err := got.Pin(home, "work"); err != nil {
		t.Fatal(err)
	}
	if ch, _ := got.Choose("antigravity", "", home, nil); ch.KeyEnv != "WORK_GEMINI_KEY" {
		t.Errorf("pinned: %+v", ch)
	}
	got.Pins, got.FailoverAt = nil, 80
	used := func(agent, account string) float64 {
		if account == "" {
			return 90
		}
		return 0
	}
	if ch, _ := got.Choose("antigravity", "", home, used); ch.Account != "work" || ch.KeyEnv != "WORK_GEMINI_KEY" {
		t.Errorf("failover: %+v", ch)
	}
}

func TestAntigravityAccountRefuses(t *testing.T) {
	home := t.TempDir()
	var b Book
	cases := []struct{ agent, keyEnv, why string }{
		{"antigravity", "", "Gemini API key"},
		{"antigravity", "1KEY", "variable"},
		{"antigravity", "KEY; rm -rf ~", "variable"},
		{"antigravity", "$KEY", "variable"},
		{"claude", "KEY", "signs in"},
	}
	for _, c := range cases {
		if _, err := b.AddWithKey(c.agent, "work", "", c.keyEnv, home); err == nil || !strings.Contains(err.Error(), c.why) {
			t.Errorf("AddWithKey(%q, %q) = %v, want an error saying %q", c.agent, c.keyEnv, err, c.why)
		}
	}
	if _, err := b.Add("antigravity", "work", "", home); err == nil {
		t.Error("an Antigravity account without a key was added")
	}
	if len(b.Accounts) != 0 {
		t.Errorf("kept: %+v", b.Accounts)
	}
}
