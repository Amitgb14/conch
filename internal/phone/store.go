package phone

import (
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
	"unicode"
)

// StoreFile is the file in conch's directory that holds the paired devices.
const StoreFile = "phone.json"

// Pairing codes: how long one lasts, and how many wrong guesses the codes
// outstanding survive. A code is six digits, so the guesses are what keeps
// it from being found by trying.
const (
	PairTTL         = 5 * time.Minute
	pairMaxFailures = 5
	maxDevices      = 64
	maxPushPerDev   = 8
)

// Device is a paired phone. Its token is kept only as a hash: the file
// alone doesn't let anyone in.
type Device struct {
	ID         string             `json:"id"`
	Name       string             `json:"name"`
	Permission string             `json:"permission"`
	TokenHash  string             `json:"token_hash"`
	Created    time.Time          `json:"created"`
	Push       []PushSubscription `json:"push,omitempty"`
}

// pairCode is a code waiting to be exchanged, kept as a hash too.
type pairCode struct {
	Hash       string    `json:"hash"`
	Permission string    `json:"permission"`
	Expires    time.Time `json:"expires"`
}

// state is what the file holds. `conch web pair` and `conch web revoke`
// write it from another process than the gateway, which is why it is a
// file and why every change is made under a lock.
type state struct {
	Devices []Device   `json:"devices"`
	Codes   []pairCode `json:"codes,omitempty"`
	// Failures counts wrong codes tried since the last one was issued.
	Failures int `json:"failures,omitempty"`
	// VAPIDKey is the private key pushes are signed with.
	VAPIDKey string `json:"vapid_key,omitempty"`
	// URL is where the gateway last listened, for `conch web pair` to say.
	URL string `json:"url,omitempty"`
	// Gateway is the last conch web: how it was started, so the TUI can
	// start it again the same way, and its process while it runs.
	Gateway *GatewayRun `json:"gateway,omitempty"`
}

// GatewayRun is a conch web as it was started. PID is 0 once it stopped.
type GatewayRun struct {
	PID     int       `json:"pid,omitempty"`
	Args    []string  `json:"args"`
	Started time.Time `json:"started"`
}

// Store reads and writes the devices file.
type Store struct {
	path string
}

// OpenStore is the store in conch's directory dir. Nothing is read or
// created until it is used.
func OpenStore(dir string) *Store {
	return &Store{path: filepath.Join(dir, StoreFile)}
}

// Path is the file the store keeps.
func (s *Store) Path() string { return s.path }

// read loads the file. A missing or empty one is an empty state; one that
// can't be read is an error, so nothing is written over it.
func (s *Store) read() (state, error) {
	var st state
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) || err == nil && len(strings.TrimSpace(string(b))) == 0 {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(b, &st); err != nil {
		return state{}, fmt.Errorf("%s: %w", s.path, err)
	}
	return st, nil
}

// update changes the file under an exclusive lock: read, fn, write. The
// write is a rename, so a reader never sees half a file. An error from fn
// leaves the file as it was.
func (s *Store) update(fn func(*state) error) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(s.path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)

	st, err := s.read()
	if err != nil {
		return err
	}
	if err := fn(&st); err != nil {
		return err
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), StoreFile+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}

// hashSecret is how a token or a code is kept. Tokens are 256 random bits,
// so a plain hash is enough; a code is short, and is protected by its five
// minutes and its few guesses rather than by the hash.
func hashSecret(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// codeDigits is a pairing code as it is compared: its digits alone, so
// "438-219", "438 219" and "438219" are the same code.
func codeDigits(code string) string {
	var b strings.Builder
	for _, r := range code {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// NewCode issues a one-time pairing code for a device with permission,
// good for PairTTL. It replaces any code still outstanding: one pairing
// at a time.
func (s *Store) NewCode(permission string, now time.Time) (string, error) {
	if !ValidPermission(permission) {
		return "", fmt.Errorf("unknown permission %q: view, reply or full", permission)
	}
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	digits := fmt.Sprintf("%06d", n.Int64())
	err = s.update(func(st *state) error {
		st.Codes = []pairCode{{Hash: hashSecret(digits), Permission: permission, Expires: now.Add(PairTTL)}}
		st.Failures = 0
		return nil
	})
	if err != nil {
		return "", err
	}
	return digits[:3] + "-" + digits[3:], nil
}

// ErrPairExpired is a code that is wrong, used, or past its time. Which is
// not said: a guesser learns nothing from the difference.
var ErrPairExpired = errors.New("that pairing code has expired or was already used; run `conch web pair` for a new one")

// Redeem exchanges a pairing code for a new device and its token. The code
// is spent whether or not anything after it goes wrong, and a wrong one
// counts against every code outstanding.
func (s *Store) Redeem(code, name string, now time.Time) (Device, string, error) {
	digits := codeDigits(code)
	var dev Device
	var token string
	var verdict error
	err := s.update(func(st *state) error {
		live := st.Codes[:0:0]
		for _, c := range st.Codes {
			if now.Before(c.Expires) {
				live = append(live, c)
			}
		}
		st.Codes = live
		i := slices.IndexFunc(st.Codes, func(c pairCode) bool { return c.Hash == hashSecret(digits) })
		if i < 0 || len(digits) != 6 {
			if len(st.Codes) > 0 {
				if st.Failures++; st.Failures >= pairMaxFailures {
					st.Codes, st.Failures = nil, 0
				}
			}
			verdict = ErrPairExpired
			return nil // the count is worth writing
		}
		permission := st.Codes[i].Permission
		st.Codes = slices.Delete(st.Codes, i, i+1)
		if len(st.Devices) >= maxDevices {
			verdict = fmt.Errorf("%d devices are paired already; revoke one with `conch web revoke`", maxDevices)
			return nil
		}
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			return err
		}
		token = base64.RawURLEncoding.EncodeToString(raw)
		id, err := newDeviceID(st.Devices)
		if err != nil {
			return err
		}
		dev = Device{ID: id, Name: cleanName(name), Permission: permission, TokenHash: hashSecret(token), Created: now.UTC()}
		st.Devices = append(st.Devices, dev)
		return nil
	})
	if err == nil {
		err = verdict
	}
	if err != nil {
		return Device{}, "", err
	}
	return dev, token, nil
}

// newDeviceID is a short ID no other device has, like "d_7h2k".
func newDeviceID(have []Device) (string, error) {
	enc := base32.StdEncoding.WithPadding(base32.NoPadding)
	for range 100 {
		raw := make([]byte, 5)
		if _, err := rand.Read(raw); err != nil {
			return "", err
		}
		id := "d_" + strings.ToLower(enc.EncodeToString(raw))[:4]
		if !slices.ContainsFunc(have, func(d Device) bool { return d.ID == id }) {
			return id, nil
		}
	}
	return "", errors.New("no free device ID")
}

// cleanName is a device's name as it is kept: what the phone called
// itself, without anything that would draw on a terminal listing it.
func cleanName(name string) string {
	name = strings.Map(func(r rune) rune {
		switch {
		case unicode.IsSpace(r):
			return ' '
		case !unicode.IsPrint(r):
			return -1
		}
		return r
	}, name)
	name = strings.Join(strings.Fields(name), " ")
	if r := []rune(name); len(r) > 64 {
		name = string(r[:64])
	}
	if name == "" {
		return "phone"
	}
	return name
}

// Devices lists the paired devices, oldest first.
func (s *Store) Devices() ([]Device, error) {
	st, err := s.read()
	return st.Devices, err
}

// Revoke removes a device and reports whether there was one. Its token
// stops working at once, and a running gateway closes its sockets.
func (s *Store) Revoke(id string) (bool, error) {
	found := false
	err := s.update(func(st *state) error {
		n := len(st.Devices)
		st.Devices = slices.DeleteFunc(st.Devices, func(d Device) bool { return d.ID == id })
		found = len(st.Devices) != n
		return nil
	})
	return found, err
}

// SetPermission changes what a paired device may do, and reports whether
// there was one. A running gateway reads it on the device's next request
// or socket message; nothing has to be paired again.
func (s *Store) SetPermission(id, permission string) (bool, error) {
	if !ValidPermission(permission) {
		return false, fmt.Errorf("unknown permission %q: view, reply or full", permission)
	}
	found := false
	err := s.update(func(st *state) error {
		for i := range st.Devices {
			if st.Devices[i].ID == id {
				st.Devices[i].Permission, found = permission, true
			}
		}
		return nil
	})
	return found, err
}

// GatewayStarted records a conch web now serving, as process pid with args.
func (s *Store) GatewayStarted(pid int, args []string, now time.Time) error {
	return s.update(func(st *state) error {
		st.Gateway = &GatewayRun{PID: pid, Args: append([]string{}, args...), Started: now.UTC()}
		return nil
	})
}

// GatewayStopped records that conch web pid has stopped, keeping how it
// was started. A newer gateway's record is left alone.
func (s *Store) GatewayStopped(pid int) error {
	return s.update(func(st *state) error {
		if st.Gateway != nil && st.Gateway.PID == pid {
			st.Gateway.PID = 0
		}
		return nil
	})
}

// Gateway is the last conch web's record, and whether its process is
// still there.
func (s *Store) Gateway() (GatewayRun, bool) {
	st, err := s.read()
	if err != nil || st.Gateway == nil {
		return GatewayRun{}, false
	}
	run := *st.Gateway
	return run, run.PID > 0 && processAlive(run.PID)
}

// processAlive reports whether a process exists: one we may not signal
// is still there.
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// SetURL records where the gateway listens.
func (s *Store) SetURL(url string) error {
	return s.update(func(st *state) error { st.URL = url; return nil })
}

// URL is where the gateway last listened, or "".
func (s *Store) URL() string {
	st, _ := s.read()
	return st.URL
}

// Subscribe keeps a device's push subscription, replacing the one it had
// for the same endpoint.
func (s *Store) Subscribe(deviceID string, sub PushSubscription) error {
	return s.update(func(st *state) error {
		i := slices.IndexFunc(st.Devices, func(d Device) bool { return d.ID == deviceID })
		if i < 0 {
			return errors.New("no such device")
		}
		d := &st.Devices[i]
		d.Push = slices.DeleteFunc(d.Push, func(p PushSubscription) bool { return p.Endpoint == sub.Endpoint })
		d.Push = append(d.Push, sub)
		if len(d.Push) > maxPushPerDev {
			d.Push = d.Push[len(d.Push)-maxPushPerDev:]
		}
		return nil
	})
}

// Unsubscribe drops a device's subscription for endpoint, if it has one.
func (s *Store) Unsubscribe(deviceID, endpoint string) error {
	return s.update(func(st *state) error {
		i := slices.IndexFunc(st.Devices, func(d Device) bool { return d.ID == deviceID })
		if i < 0 {
			return nil
		}
		d := &st.Devices[i]
		d.Push = slices.DeleteFunc(d.Push, func(p PushSubscription) bool { return p.Endpoint == endpoint })
		return nil
	})
}

// VAPIDKey is the key pushes are signed with. It is made the first time
// it is asked for and kept, so subscriptions made against it go on working.
func (s *Store) VAPIDKey() (*ecdh.PrivateKey, error) {
	var key *ecdh.PrivateKey
	err := s.update(func(st *state) error {
		if st.VAPIDKey != "" {
			raw, err := base64.RawURLEncoding.DecodeString(st.VAPIDKey)
			if err == nil {
				key, err = ecdh.P256().NewPrivateKey(raw)
			}
			if err != nil {
				return errors.New("the push key in " + s.path + " can't be read")
			}
			return nil
		}
		var err error
		if key, err = ecdh.P256().GenerateKey(rand.Reader); err != nil {
			return err
		}
		st.VAPIDKey = base64.RawURLEncoding.EncodeToString(key.Bytes())
		return nil
	})
	return key, err
}

// VAPIDPublicKey is the key a phone subscribes to pushes with, as browsers
// want it: the uncompressed P-256 point, base64url.
func (s *Store) VAPIDPublicKey() (string, error) {
	key, err := s.VAPIDKey()
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()), nil
}
