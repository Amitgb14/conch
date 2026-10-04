package remote

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// sshValueFlags are ssh's options that take a value (its getopt string).
const sshValueFlags = "bceilmopBDEFIJLOPQRSwW"

// sshFlags are its options that don't.
const sshFlags = "1246afgknqstvxyACGKMNTVXY"

// sshRefusedFlags are options that would not make a login: another config
// in place of conch's, printing the config or version, querying, a control
// command, or forwarding stdio instead of a shell.
const sshRefusedFlags = "FGVQOW"

// NormalizeLoginArgs checks extra ssh options for a login and returns them
// in the form ssh takes. A bare Key=Value word is an -o option, so
// "KexAlgorithms=+diffie-hellman-group1-sha1" can be written as it is in
// ssh_config. Anything that isn't an option is refused: a word ssh would
// take for the host would make the real host the remote command.
func NormalizeLoginArgs(words []string) ([]string, error) {
	var out []string
	for i := 0; i < len(words); i++ {
		w := words[i]
		if strings.IndexFunc(w, func(r rune) bool { return r < ' ' || r == 0x7f }) >= 0 {
			return nil, fmt.Errorf("ssh option %q has a control character", w)
		}
		if !strings.HasPrefix(w, "-") || w == "-" {
			if k, _, ok := strings.Cut(w, "="); ok && validOptionName(k) {
				out = append(out, "-o", w)
				continue
			}
			return nil, fmt.Errorf("%q is not an ssh option (options start with -, or are Key=Value)", w)
		}
		if w == "--" {
			return nil, fmt.Errorf("-- can't be used in ssh options")
		}
		out = append(out, w)
		// A cluster like -vvv, or -p2222 with its value attached.
		for j := 1; j < len(w); j++ {
			c := w[j]
			switch {
			case strings.IndexByte(sshRefusedFlags, c) >= 0:
				return nil, fmt.Errorf("ssh option -%c can't be used for a login", c)
			case strings.IndexByte(sshValueFlags, c) >= 0:
				if j == len(w)-1 {
					if i+1 >= len(words) {
						return nil, fmt.Errorf("ssh option -%c needs a value", c)
					}
					i++
					if strings.IndexFunc(words[i], func(r rune) bool { return r < ' ' || r == 0x7f }) >= 0 {
						return nil, fmt.Errorf("ssh option -%c has a control character", c)
					}
					out = append(out, words[i])
				}
				j = len(w) // the rest of the word is its value
			case strings.IndexByte(sshFlags, c) >= 0:
			default:
				return nil, fmt.Errorf("ssh has no option -%c", c)
			}
		}
	}
	return out, nil
}

// validOptionName reports whether k looks like an ssh_config keyword.
func validOptionName(k string) bool {
	if k == "" {
		return false
	}
	for _, r := range k {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

// copyKeyScript installs a public key in the host's authorized_keys, making
// the key first when there is none: $1 is the private key's path, the rest
// the ssh command up to and including the host. ssh asks for the password
// on the terminal, while the key goes in on stdin. The pane stays open at
// the end so what happened can be read.
const copyKeyScript = `key=$1; shift
if [ ! -f "$key.pub" ]; then
  echo "No ssh key yet: making $key. ssh-keygen asks for a passphrase; leave it empty for none."
  mkdir -p "$(dirname "$key")" && chmod 700 "$(dirname "$key")"
  if ! ssh-keygen -t ed25519 -f "$key"; then
    echo "ssh-keygen failed."
    printf "Press enter to close. "; read _; exit 1
  fi
fi
echo "Copying $key.pub to the host. Enter the host's password if asked."
if "$@" 'umask 077; mkdir -p ~/.ssh && touch ~/.ssh/authorized_keys && { read -r k || [ -n "$k" ]; } && { grep -qxF "$k" ~/.ssh/authorized_keys || printf "%s\n" "$k" >> ~/.ssh/authorized_keys; }' < "$key.pub"; then
  echo; echo "conch: the key is installed. Logging in again should not ask for a password."
  status=0
else
  echo; echo "conch: copying the key failed (see above)."
  status=1
fi
printf "Press enter to close. "; read _
exit $status
`

// sshKeyNames are the keys ssh tries by default, in the order a copy picks
// one.
var sshKeyNames = []string{"id_ed25519", "id_ecdsa", "id_rsa"}

// CopyKeyCommand is a command for a terminal that sets target up for
// logins without a password: it appends this computer's public key to
// ~/.ssh/authorized_keys there (once), logging in with the password one last
// time. Without a key it makes ~/.ssh/id_ed25519 first. key is the public
// key that will be copied.
func CopyKeyCommand(target string, args []string) (command []string, key string, err error) {
	login, err := LoginCommand(target, args...)
	if err != nil {
		return nil, "", err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, "", err
	}
	private := filepath.Join(home, ".ssh", sshKeyNames[0])
	for _, name := range sshKeyNames {
		if p := filepath.Join(home, ".ssh", name); fileExists(p + ".pub") {
			private = p
			break
		}
	}
	return append([]string{"/bin/sh", "-c", copyKeyScript, "sh", private}, login...), private + ".pub", nil
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular()
}
