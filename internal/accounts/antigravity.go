package accounts

import (
	"errors"
	"os"
	"os/exec"
)

// How an agent is signed in, where conch can tell (proto.AgentAvailability.Auth).
const (
	AuthSignedIn  = "signed-in"
	AuthSignedOut = "signed-out"
	AuthAPIKey    = "api-key"
)

// keychainHas reports whether the macOS Keychain holds a generic password
// for service and account. It runs security without -w, which reads the
// item's attributes and never its secret, so nothing is unlocked and
// nobody is asked. CONCH_SECURITY names another security, as it does for
// the server's own secrets, which is how a test keeps this away from the
// developer's Keychain; and it is a variable so tests can answer for it.
var keychainHas = func(service, account string) (bool, error) {
	bin := "security"
	if b := os.Getenv("CONCH_SECURITY"); b != "" {
		bin = b
	}
	err := exec.Command(bin, "find-generic-password", "-s", service, "-a", account).Run()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return true, nil
	case errors.As(err, &ee) && ee.ExitCode() == 44: // errSecItemNotFound
		return false, nil
	}
	return false, err
}

// AntigravityAuth says how Antigravity CLI is signed in on this machine:
// on a Gemini API key when conch starts it with one (keyEnv, the
// [agents] antigravity_api_key_env setting); else by whether its Google
// sign-in is in the Keychain, where agy 1.5.16 keeps it as service
// "gemini", account "antigravity" and /logout removes it. "" when conch
// cannot tell: another system's keyring, or a lookup that failed.
func AntigravityAuth(goos, keyEnv string) string {
	if keyEnv != "" {
		return AuthAPIKey
	}
	if goos != "darwin" {
		return ""
	}
	has, err := keychainHas("gemini", "antigravity")
	switch {
	case err != nil:
		return ""
	case has:
		return AuthSignedIn
	}
	return AuthSignedOut
}
