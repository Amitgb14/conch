package remote

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Amitgb14/conch/internal/config"
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

// sshKeygenBinary is ssh-keygen; $CONCH_SSH_KEYGEN overrides it.
func sshKeygenBinary() string {
	if b := os.Getenv("CONCH_SSH_KEYGEN"); b != "" {
		return b
	}
	return "ssh-keygen"
}

// conchKey is the key conch creates for machines when the user has none.
func conchKey() string { return filepath.Join(config.Dir(), "ssh", "id_ed25519") }

// publicKey returns a public key to authorize on machines: the user's own
// default key, else conch's, which is created when missing.
func publicKey(ctx context.Context) (line, path string, err error) {
	if home, err := os.UserHomeDir(); err == nil {
		for _, name := range []string{"id_ed25519", "id_ecdsa", "id_rsa"} {
			p := filepath.Join(home, ".ssh", name+".pub")
			if b, err := os.ReadFile(p); err == nil && validKeyLine(string(b)) {
				return strings.TrimSpace(string(b)), p, nil
			}
		}
	}
	key := conchKey()
	if !exists(key + ".pub") {
		if err := os.MkdirAll(filepath.Dir(key), 0o700); err != nil {
			return "", "", err
		}
		host, _ := os.Hostname()
		cmd := exec.CommandContext(ctx, sshKeygenBinary(), "-q", "-t", "ed25519", "-N", "", "-C", "conch@"+host, "-f", key)
		if out, err := cmd.CombinedOutput(); err != nil {
			return "", "", fmt.Errorf("create an ssh key: %v: %s", err, strings.TrimSpace(string(out)))
		}
		resetConfig() // the generated config names the new key
	}
	b, err := os.ReadFile(key + ".pub")
	if err != nil || !validKeyLine(string(b)) {
		return "", "", fmt.Errorf("no usable public key in %s", key+".pub")
	}
	return strings.TrimSpace(string(b)), key + ".pub", nil
}

func validKeyLine(s string) bool {
	f := strings.Fields(s)
	return len(f) >= 2 && (strings.HasPrefix(f[0], "ssh-") || strings.HasPrefix(f[0], "ecdsa-") || strings.HasPrefix(f[0], "sk-"))
}

func resetConfig() {
	configOnce.Lock()
	configOnce.path = ""
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
func SetUpKeyLogin(ctx context.Context, target, password string) (string, error) {
	line, path, err := publicKey(ctx)
	if err != nil {
		return "", err
	}
	ap, err := newAskpass(password)
	if err != nil {
		return "", err
	}
	defer ap.close()
	tr := &sshTransport{target: target, opts: sshOpts{askpass: ap}}
	if _, err := runScript(ctx, tr, authorizeScript, []byte(line+"\n")); err != nil {
		return path, fmt.Errorf("add the key: %w", err)
	}
	if _, err := runScript(ctx, sshKeyOnly(target), "true", nil); err != nil {
		return path, fmt.Errorf("%w: %v", ErrKeyLoginUnverified, err)
	}
	return path, nil
}
