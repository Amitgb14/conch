package accounts

import (
	"errors"
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
