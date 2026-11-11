package cloudagent

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/flynn/noise"

	"github.com/Amitgb14/conch/internal/relay"
)

// LoadKey is the machine's Noise static key (PROTOCOL §3.1): made once,
// kept on the machine's own disk at path, 0600, and the same for the life
// of the machine — clients pin its public half, and the control plane
// marks a machine that shows a different one as broken. made says it was
// made just now, which is how a first boot is told from a wake.
//
// A key file anyone else could read is refused, not repaired: whoever
// could read it could have answered as this machine, so it is no longer
// this machine's alone, and making a new one would fail the pin anyway.
func LoadKey(path string) (key noise.DHKey, made bool, err error) {
	f, err := openPlain(path)
	switch {
	case err == nil:
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			return noise.DHKey{}, false, err
		}
		if perm := st.Mode().Perm(); perm&0o077 != 0 {
			return noise.DHKey{}, false, fmt.Errorf("%s is readable by others (mode %04o): this machine's key may have leaked; it must be 0600", path, perm)
		}
		if sys, ok := st.Sys().(*syscall.Stat_t); ok && int(sys.Uid) != os.Geteuid() {
			return noise.DHKey{}, false, fmt.Errorf("%s belongs to uid %d, not to this process (uid %d)", path, sys.Uid, os.Geteuid())
		}
		b, err := io.ReadAll(io.LimitReader(f, 4096))
		if err != nil {
			return noise.DHKey{}, false, err
		}
		key, err := relay.KeyFromPrivate(strings.TrimSpace(string(b)))
		if err != nil {
			return noise.DHKey{}, false, fmt.Errorf("%s: %w", path, err)
		}
		return key, false, nil
	case !errors.Is(err, os.ErrNotExist):
		return noise.DHKey{}, false, err
	}
	key, err = relay.NewKey()
	if err != nil {
		return noise.DHKey{}, false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return noise.DHKey{}, false, err
	}
	// O_EXCL: two agents starting at once must not each make a key and
	// say hello with different ones; the loser reads the winner's.
	f, err = os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return LoadKey(path)
	}
	if err != nil {
		return noise.DHKey{}, false, err
	}
	_, werr := f.WriteString(relay.EncodeKey(key.Private) + "\n")
	serr := f.Sync()
	cerr := f.Close()
	if err := errors.Join(werr, serr, cerr); err != nil {
		os.Remove(path)
		return noise.DHKey{}, false, err
	}
	return key, true, nil
}
