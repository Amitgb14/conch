package cloud

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Amitgb14/conch/internal/config"
)

// Key pinning (PROTOCOL §3.2). The control plane hands out each machine's
// Noise key, and the tunnel is only as private as that key is the
// machine's own: a control plane that handed out another could read
// everything. So the first key seen for a machine is kept, and a
// different one later is refused — unless the machine was recreated
// (its created_at moved), which makes a new key, and is re-pinned. The
// first connection is taken on trust; the docs say so (§3.5).

type pin struct {
	ServerKey string    `json:"server_key"`
	CreatedAt time.Time `json:"created_at"`
	Pinned    time.Time `json:"pinned"`
}

// KeysPath is where pinned keys are kept.
func KeysPath() string { return filepath.Join(config.Dir(), "cloud-keys.json") }

// KeyChangedError is a machine whose key is not the one pinned, while
// the machine was not recreated.
type KeyChangedError struct {
	Machine string // its name, or id
	ID      string
	Pinned  time.Time
}

func (e *KeyChangedError) Error() string {
	return fmt.Sprintf("refusing to connect to %s: Conch Cloud gave a key for it that is not the one it had when conch first connected (%s), and the machine was not recreated since — "+
		"someone other than the machine could be answering. If you know why it changed, run: conch cloud machine forget-key %s",
		e.Machine, e.Pinned.Local().Format("2 Jan 2006 15:04"), e.ID)
}

func loadPins() (map[string]pin, error) {
	b, err := os.ReadFile(KeysPath())
	if errors.Is(err, os.ErrNotExist) {
		return map[string]pin{}, nil
	}
	if err != nil {
		return nil, err
	}
	var f struct {
		Machines map[string]pin `json:"machines"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", KeysPath(), err)
	}
	if f.Machines == nil {
		f.Machines = map[string]pin{}
	}
	return f.Machines, nil
}

func savePins(pins map[string]pin) error {
	if err := os.MkdirAll(config.Dir(), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(struct {
		Machines map[string]pin `json:"machines"`
	}{pins}, "", "  ")
	if err != nil {
		return err
	}
	return writePrivate(KeysPath(), append(b, '\n'))
}

// checkPin compares key with the one pinned for machine m, pinning it on
// first use and when the machine was recreated.
func checkPin(m Machine, key string) error {
	if key == "" {
		return errors.New("Conch Cloud gave no key for " + m.Name)
	}
	unlock, err := lock()
	if err != nil {
		return err
	}
	defer unlock()
	pins, err := loadPins()
	if err != nil {
		return err
	}
	p, ok := pins[m.ID]
	switch {
	case ok && p.ServerKey == key:
		return nil
	case ok && p.CreatedAt.Equal(m.CreatedAt):
		name := m.Name
		if name == "" {
			name = m.ID
		}
		return &KeyChangedError{Machine: name, ID: m.ID, Pinned: p.Pinned}
	}
	pins[m.ID] = pin{ServerKey: key, CreatedAt: m.CreatedAt, Pinned: time.Now().UTC()}
	return savePins(pins)
}

// ForgetKey drops the key pinned for machine id, so the next connection
// pins whatever it is given.
func ForgetKey(id string) error {
	unlock, err := lock()
	if err != nil {
		return err
	}
	defer unlock()
	pins, err := loadPins()
	if err != nil {
		return err
	}
	if _, ok := pins[id]; !ok {
		return fmt.Errorf("no key is pinned for %s", id)
	}
	delete(pins, id)
	return savePins(pins)
}
