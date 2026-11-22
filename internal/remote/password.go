package remote

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Amitgb14/conch/internal/sshkey"
)

// askpass hands a password to ssh without a terminal: ssh runs
// $SSH_ASKPASS for each prompt, and this one prints the password from a
// private file. Host key questions are answered "no" (password runs accept
// new host keys through StrictHostKeyChecking instead), so a password is
// never typed where a yes/no is expected.
type askpass struct {
	dir string
}

const askpassScript = `#!/bin/sh
case "$1" in
*yes/no*|*fingerprint*) echo no ;;
*) /bin/cat "$(dirname "$0")/password" ;;
esac
`

func newAskpass(password string) (*askpass, error) {
	dir, err := os.MkdirTemp("", "conch-askpass-")
	if err != nil {
		return nil, err
	}
	a := &askpass{dir: dir}
	if err := os.WriteFile(filepath.Join(dir, "password"), []byte(password), 0o600); err != nil {
		a.close()
		return nil, err
	}
	if err := os.WriteFile(a.script(), []byte(askpassScript), 0o700); err != nil {
		a.close()
		return nil, err
	}
	return a, nil
}

func (a *askpass) script() string { return filepath.Join(a.dir, "askpass") }

// env is this process's environment with ssh told to use the helper.
func (a *askpass) env() []string {
	env := make([]string, 0, len(os.Environ())+3)
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "SSH_ASKPASS=") && !strings.HasPrefix(kv, "SSH_ASKPASS_REQUIRE=") {
			env = append(env, kv)
		}
	}
	env = append(env, "SSH_ASKPASS="+a.script(), "SSH_ASKPASS_REQUIRE=force")
	if os.Getenv("DISPLAY") == "" {
		env = append(env, "DISPLAY=conch") // older OpenSSH uses askpass only with a display
	}
	return env
}

// close removes the password.
func (a *askpass) close() { os.RemoveAll(a.dir) }

// ---- key login ----

// conchKey is the key conch creates for machines when the user has none.
func conchKey() string { return sshkey.ConchKey() }

// loginKeys are the private keys ssh should offer: conch's own, then the
// user's defaults, whichever exist. Only keys that are there are named, so
// ssh is not asked for a file it cannot read.
func loginKeys() []string {
	var keys []string
	if key := conchKey(); exists(key) {
		keys = append(keys, key)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return keys
	}
	for _, name := range sshkey.DefaultNames {
		if key := filepath.Join(home, ".ssh", name); exists(key) {
			keys = append(keys, key)
		}
	}
	return keys
}

// publicKey returns a public key to authorize on machines: the user's own
// default key, else conch's, which is created when missing.
func publicKey(ctx context.Context) (line, path string, err error) {
	k, err := sshkey.Public(ctx)
	if err != nil {
		return "", "", err
	}
	if k.Made {
		resetConfig() // the generated config names the new key
	}
	return k.Line, k.Path, nil
}

func validKeyLine(s string) bool { return sshkey.ValidLine(s) }

func resetConfig() {
	configOnce.Lock()
	configOnce.path, configOnce.login = "", ""
	configOnce.Unlock()
}

// authorizeScript appends the key read from stdin to authorized_keys unless
// it is there already.
const authorizeScript = `umask 077; mkdir -p "$HOME/.ssh" && touch "$HOME/.ssh/authorized_keys" && ` +
	`IFS= read -r key && { grep -qxF "$key" "$HOME/.ssh/authorized_keys" || printf '%s\n' "$key" >> "$HOME/.ssh/authorized_keys"; }`

// ErrKeyLoginUnverified means the key was added but logging in with it
// still failed (the server may not allow keys).
var ErrKeyLoginUnverified = errors.New("the key was added, but logging in with it still fails")

// SetUpKeyLogin uses password once to authorize a public key on target,
// then checks that key login works without it. It returns the key's path.
// opts are the machine's extra ssh options: the key has to go to the host
// as conch will reach it, not to whatever a bare target resolves to.
func SetUpKeyLogin(ctx context.Context, target, password string, opts ...string) (string, error) {
	line, path, err := publicKey(ctx)
	if err != nil {
		return "", err
	}
	ap, err := newAskpass(password)
	if err != nil {
		return "", err
	}
	defer ap.close()
	tr := &sshTransport{target: target, opts: sshOpts{askpass: ap, args: opts}}
	if _, err := runScript(ctx, tr, authorizeScript, []byte(line+"\n")); err != nil {
		return path, fmt.Errorf("add the key: %w", err)
	}
	if _, err := runScript(ctx, sshKeyOnly(target, opts...), "true", nil); err != nil {
		return path, fmt.Errorf("%w: %v", ErrKeyLoginUnverified, err)
	}
	return path, nil
}
