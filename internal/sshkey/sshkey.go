// Package sshkey is the ssh key conch reaches machines with, and the step
// that lets a sandbox in with it. It is shared by conch and conch-sandbox,
// so both offer the same key and find the one the other made.
package sshkey

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/sandbox"
)

// Keygen is ssh-keygen; $CONCH_SSH_KEYGEN overrides it.
func Keygen() string {
	if b := os.Getenv("CONCH_SSH_KEYGEN"); b != "" {
		return b
	}
	return "ssh-keygen"
}

// ConchKey is the private key conch creates for machines when the user
// has none of their own.
func ConchKey() string { return filepath.Join(config.Dir(), "ssh", "id_ed25519") }

// DefaultNames are the keys ssh would try by itself, in its own order.
var DefaultNames = []string{"id_ed25519", "id_ecdsa", "id_rsa"}

// Key is a public key to authorize on machines.
type Key struct {
	Line string // the public key, one line
	Path string // its .pub file; the private half is beside it
	Made bool   // conch's key was created just now
}

// Identity is the private key to offer for this public key.
func (k Key) Identity() string { return strings.TrimSuffix(k.Path, ".pub") }

// Public returns a public key to authorize on machines: the user's own
// default key, else conch's, which is created when missing.
func Public(ctx context.Context) (Key, error) {
	if home, err := os.UserHomeDir(); err == nil {
		for _, name := range DefaultNames {
			p := filepath.Join(home, ".ssh", name+".pub")
			if b, err := os.ReadFile(p); err == nil && ValidLine(string(b)) {
				return Key{Line: strings.TrimSpace(string(b)), Path: p}, nil
			}
		}
	}
	key := ConchKey()
	made := false
	if _, err := os.Stat(key + ".pub"); err != nil {
		if err := os.MkdirAll(filepath.Dir(key), 0o700); err != nil {
			return Key{}, err
		}
		host, _ := os.Hostname()
		cmd := exec.CommandContext(ctx, Keygen(), "-q", "-t", "ed25519", "-N", "", "-C", "conch@"+host, "-f", key)
		if out, err := cmd.CombinedOutput(); err != nil {
			return Key{}, fmt.Errorf("create an ssh key: %v: %s", err, strings.TrimSpace(string(out)))
		}
		made = true
	}
	b, err := os.ReadFile(key + ".pub")
	if err != nil || !ValidLine(string(b)) {
		return Key{}, fmt.Errorf("no usable public key in %s", key+".pub")
	}
	return Key{Line: strings.TrimSpace(string(b)), Path: key + ".pub", Made: made}, nil
}

// ValidLine reports whether s looks like an OpenSSH public key line.
func ValidLine(s string) bool {
	f := strings.Fields(s)
	return len(f) >= 2 && (strings.HasPrefix(f[0], "ssh-") || strings.HasPrefix(f[0], "ecdsa-") || strings.HasPrefix(f[0], "sk-"))
}

// Use makes the key the one a provider file's create body is given as
// {public_key}, so the key later offered is the key that was put there.
// made, when set, hears that conch's key was just created.
func Use(made func()) {
	sandbox.PublicKey = func(ctx context.Context) (string, error) {
		k, err := Public(ctx)
		if err == nil && k.Made && made != nil {
			made()
		}
		return k.Line, err
	}
}

// Authorize readies sandbox id for a login with this computer's key and
// returns the private key ssh has to offer, or "" when the provider hands
// out a secret instead. A provider that takes a key through its API is
// given it; one that lets in the keys registered with the person's account
// is offered the same key, which its Note says to register. made, when
// set, hears that conch's key was just created.
func Authorize(ctx context.Context, p sandbox.Provider, id string, made func()) (string, error) {
	public := func() (Key, error) {
		k, err := Public(ctx)
		if err == nil && k.Made && made != nil {
			made()
		}
		return k, err
	}
	if ka, ok := sandbox.As[sandbox.KeyAuthorizer](p); ok && AuthorizesKey(p) {
		k, err := public()
		if err != nil {
			return "", err
		}
		if err := ka.AuthorizeKey(ctx, id, k.Line); err != nil {
			return "", err
		}
		return k.Identity(), nil
	}
	if ak, ok := sandbox.As[sandbox.AccountKeyed](p); ok && ak.AccountKey() {
		k, err := public()
		if err != nil {
			return "", err
		}
		return k.Identity(), nil
	}
	return "", nil
}

// AuthorizesKey says whether a provider that can take a key wants one: a
// provider described by a file says so in it.
func AuthorizesKey(p sandbox.Provider) bool {
	if a, ok := p.(interface{ AuthorizesKey() bool }); ok {
		return a.AuthorizesKey()
	}
	return true
}
