package cloud

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/config"
)

// A signed-in conch keeps $CONCH_HOME/cloud.json: which server, which
// device, who. Its tokens go in the macOS Keychain where there is one,
// and otherwise in that same file, which must then be the owner's alone
// (0600): a file anybody else can read holds a login anybody else can
// use, so it is refused rather than used. The file's presence is what
// "signed in" means, so knowing that costs no request and no Keychain
// prompt.

// Account is what cloud.json holds.
type Account struct {
	API      string `json:"api"`
	DeviceID string `json:"device_id"`
	Handle   string `json:"handle,omitempty"`
	// Store says where the tokens are: "keychain", or "file" (here).
	Store         string    `json:"store"`
	AccessToken   string    `json:"access_token,omitempty"`
	RefreshToken  string    `json:"refresh_token,omitempty"`
	AccessExpires time.Time `json:"access_expires,omitzero"`
}

const (
	storeKeychain = "keychain"
	storeFile     = "file"
)

// tokens are a device's credentials.
type tokens struct {
	Access  string    `json:"access_token"`
	Refresh string    `json:"refresh_token"`
	Expires time.Time `json:"access_expires"`
}

// Keychain keeps a secret per account name. Tests replace it (SetKeychain)
// so that none ever reaches the developer's own Keychain.
type Keychain interface {
	Get(account string) (string, error)
	Set(account, secret string) error
	Delete(account string) error
}

// keychain is the macOS Keychain through security(1), or nil where there
// is none — and in a test binary, which never touches the real one.
var keychain = func() Keychain {
	if runtime.GOOS != "darwin" || testing.Testing() {
		return nil
	}
	return securityKeychain{}
}()

// SetKeychain replaces the Keychain (nil: tokens in the file), returning
// the one it replaced. For tests.
func SetKeychain(k Keychain) Keychain {
	old := keychain
	keychain = k
	return old
}

// keychainService names conch's Keychain items.
const keychainService = "conch-cloud"

// keychainAccount is the item for this CONCH_HOME: a second conch home on
// the same Mac is a second sign-in.
func keychainAccount() string { return config.Dir() }

type securityKeychain struct{}

// security(1) is driven through its interactive mode on stdin, so the
// secret is never in an argv that ps shows.
func (securityKeychain) Set(account, secret string) error {
	cmd := security("-i")
	cmd.Stdin = strings.NewReader(fmt.Sprintf("add-generic-password -U -s %s -a %s -w %s\n",
		quoteSecurity(keychainService), quoteSecurity(account), quoteSecurity(secret)))
	out, err := cmd.CombinedOutput()
	if err != nil || bytes.Contains(out, []byte("rror")) {
		return fmt.Errorf("keychain: %s %v", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func (securityKeychain) Get(account string) (string, error) {
	out, err := security("find-generic-password", "-s", keychainService, "-a", account, "-w").Output()
	if err != nil {
		return "", fmt.Errorf("keychain: no %s item for %s", keychainService, account)
	}
	return strings.TrimSpace(string(out)), nil
}

func (securityKeychain) Delete(account string) error {
	out, err := security("delete-generic-password", "-s", keychainService, "-a", account).CombinedOutput()
	if err != nil && !bytes.Contains(out, []byte("could not be found")) {
		return fmt.Errorf("keychain: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// security runs security(1) with HOME the account's own: it finds the
// login keychain under $HOME/Library, so a conch run with another HOME —
// a scratch one, a sandboxed tool's — would otherwise find no keychain and
// lose the sign-in.
func security(args ...string) *exec.Cmd {
	cmd := exec.Command("/usr/bin/security", args...)
	if u, err := user.Current(); err == nil && u.HomeDir != "" {
		cmd.Env = append(os.Environ(), "HOME="+u.HomeDir)
	}
	return cmd
}

// quoteSecurity quotes a word for security -i, which splits like a shell.
func quoteSecurity(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// AccountPath is cloud.json.
func AccountPath() string { return filepath.Join(config.Dir(), "cloud.json") }

// SignedIn says whether this conch has a Conch Cloud account, from the
// file alone.
func SignedIn() bool {
	_, err := os.Stat(AccountPath())
	return err == nil
}

// LoadAccount reads cloud.json: ErrNotSignedIn without one.
func LoadAccount() (*Account, error) {
	f, err := os.Open(AccountPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotSignedIn
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	var a Account
	if err := json.NewDecoder(f).Decode(&a); err != nil {
		return nil, fmt.Errorf("%s: %w", AccountPath(), err)
	}
	if a.Store == storeFile && st.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("%s holds your Conch Cloud sign-in and others can read it (mode %04o): refusing it; run chmod 600 %s, or conch logout and sign in again",
			AccountPath(), st.Mode().Perm(), AccountPath())
	}
	if a.API == "" {
		return nil, fmt.Errorf("%s names no server; conch logout and sign in again", AccountPath())
	}
	return &a, nil
}

// tokens reads the account's tokens from wherever they are kept.
func (a *Account) tokens() (tokens, error) {
	if a.Store != storeKeychain {
		return tokens{Access: a.AccessToken, Refresh: a.RefreshToken, Expires: a.AccessExpires}, nil
	}
	if keychain == nil {
		return tokens{}, errors.New("this sign-in is kept in a Keychain this conch cannot reach; conch logout and sign in again")
	}
	v, err := keychain.Get(keychainAccount())
	if err != nil {
		return tokens{}, err
	}
	b, err := base64.StdEncoding.DecodeString(v)
	if err != nil {
		return tokens{}, fmt.Errorf("keychain: %w", err)
	}
	var t tokens
	if err := json.Unmarshal(b, &t); err != nil {
		return tokens{}, fmt.Errorf("keychain: %w", err)
	}
	return t, nil
}

// save writes the account with t, the tokens going to the Keychain when
// there is one.
func (a *Account) save(t tokens) error {
	if err := os.MkdirAll(config.Dir(), 0o700); err != nil {
		return err
	}
	out := *a
	out.AccessToken, out.RefreshToken, out.AccessExpires = "", "", time.Time{}
	if keychain != nil {
		b, _ := json.Marshal(t)
		if err := keychain.Set(keychainAccount(), base64.StdEncoding.EncodeToString(b)); err != nil {
			return err
		}
		out.Store = storeKeychain
	} else {
		out.Store = storeFile
		out.AccessToken, out.RefreshToken, out.AccessExpires = t.Access, t.Refresh, t.Expires
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	if err := writePrivate(AccountPath(), append(b, '\n')); err != nil {
		return err
	}
	*a = out
	return nil
}

// forget removes the sign-in: the Keychain item and the file.
func forget() error {
	var errs []error
	if a, err := LoadAccount(); err == nil && a.Store == storeKeychain && keychain != nil {
		errs = append(errs, keychain.Delete(keychainAccount()))
	}
	if err := os.Remove(AccountPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// writePrivate replaces path with data, 0600, through a temp file in the
// same directory so a reader never sees half of it.
func writePrivate(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// lock takes the account's lock, shared by every conch process on this
// CONCH_HOME. A refresh token is single use and a reused one signs the
// device out (the control plane takes reuse for theft), so two processes
// — the TUI and a bridge it started — must never both refresh with the
// same one: whoever comes second finds the new tokens on disk instead.
func lock() (func(), error) {
	if err := os.MkdirAll(config.Dir(), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(config.Dir(), "cloud.lock"), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
		if !errors.Is(err, syscall.EINTR) {
			break
		}
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return func() { f.Close() }, nil
}
