package remote

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Amitgb14/conch/internal/config"
)

// Machine is a saved remote machine. It holds no secrets: authentication is
// entirely ssh's business.
type Machine struct {
	ID      string    `json:"id"`
	Label   string    `json:"label"`
	Target  string    `json:"target"` // ssh destination: alias, user@host or ssh://user@host:port
	Enabled bool      `json:"enabled"`
	Added   time.Time `json:"added"`
}

// LocalID is the machine ID of this computer.
const LocalID = "local"

func catalogPath() string { return filepath.Join(config.Dir(), "machines.json") }

// CatalogPath is the file saved machines are kept in, for watching it.
func CatalogPath() string { return catalogPath() }

// Machines loads the saved machines.
func Machines() ([]Machine, error) {
	b, err := os.ReadFile(catalogPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var file struct {
		Machines []Machine `json:"machines"`
	}
	if err := json.Unmarshal(b, &file); err != nil {
		return nil, fmt.Errorf("%s: %w", catalogPath(), err)
	}
	return file.Machines, nil
}

func saveMachines(ms []Machine) error {
	if ms == nil {
		ms = []Machine{}
	}
	b, err := json.MarshalIndent(struct {
		Machines []Machine `json:"machines"`
	}{ms}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(config.Dir(), 0o700); err != nil {
		return err
	}
	return writeFileAtomic(catalogPath(), append(b, '\n'))
}

// writeFileAtomic replaces path with data through a uniquely named temp file
// in the same directory, so readers in other processes never see it empty or
// half written, and concurrent writers never rename each other's file away.
// The file is created 0600.
func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// lockCatalog takes an exclusive lock shared by every conch process using
// this CONCH_HOME (the TUI, scripts, `conch machine add` in another
// terminal), so a read-modify-write of machines.json can't lose another
// process's change. The returned func releases it.
func lockCatalog() (func(), error) {
	if err := os.MkdirAll(config.Dir(), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(catalogPath()+".lock", os.O_RDWR|os.O_CREATE, 0o600)
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
		return nil, fmt.Errorf("lock %s: %w", f.Name(), err)
	}
	// Closing the file drops the lock.
	return func() { f.Close() }, nil
}

// SaveMachine adds m (or updates the machine with the same target) and
// returns it with its ID filled in.
func SaveMachine(m Machine) (Machine, error) {
	unlock, err := lockCatalog()
	if err != nil {
		return m, err
	}
	defer unlock()
	ms, err := Machines()
	if err != nil {
		return m, err
	}
	for i := range ms {
		if ms[i].Target == m.Target {
			if m.Label != "" {
				ms[i].Label = m.Label
			}
			ms[i].Enabled = true
			return ms[i], saveMachines(ms)
		}
	}
	if m.Label == "" {
		m.Label = labelFor(m.Target)
	}
	m.ID = uniqueID(ms, slug(m.Label))
	m.Enabled = true
	m.Added = time.Now()
	return m, saveMachines(append(ms, m))
}

// RenameMachine changes the label of a machine found by ID or label. The ID
// stays, so saved state and pane references keep working.
func RenameMachine(ref, label string) (Machine, error) {
	label = strings.TrimSpace(label)
	if label == "" {
		return Machine{}, errors.New("a machine needs a label")
	}
	unlock, err := lockCatalog()
	if err != nil {
		return Machine{}, err
	}
	defer unlock()
	ms, err := Machines()
	if err != nil {
		return Machine{}, err
	}
	for i, m := range ms {
		if m.ID != ref && m.Label != ref {
			continue
		}
		for _, other := range ms {
			if other.ID != m.ID && other.Label == label {
				return Machine{}, fmt.Errorf("another machine is already called %q", label)
			}
		}
		ms[i].Label = label
		return ms[i], saveMachines(ms)
	}
	return Machine{}, fmt.Errorf("no machine %q", ref)
}

// RemoveMachine forgets a machine by ID or label.
func RemoveMachine(ref string) error {
	unlock, err := lockCatalog()
	if err != nil {
		return err
	}
	defer unlock()
	ms, err := Machines()
	if err != nil {
		return err
	}
	for i, m := range ms {
		if m.ID == ref || m.Label == ref {
			return saveMachines(append(ms[:i], ms[i+1:]...))
		}
	}
	return fmt.Errorf("no machine %q", ref)
}

// FindMachine resolves an ID, label or ssh target to a machine. Unknown
// references are treated as ad-hoc ssh targets.
func FindMachine(ref string) Machine {
	ms, _ := Machines()
	for _, m := range ms {
		if m.ID == ref || m.Label == ref || m.Target == ref {
			return m
		}
	}
	return Machine{ID: slug(labelFor(ref)), Label: labelFor(ref), Target: ref, Enabled: true}
}

// labelFor derives a short label from an ssh target: the host part.
func labelFor(target string) string {
	t := strings.TrimPrefix(target, "ssh://")
	if i := strings.LastIndexByte(t, '@'); i >= 0 {
		t = t[i+1:]
	}
	if i := strings.IndexByte(t, ':'); i >= 0 {
		t = t[:i]
	}
	return t
}

func slug(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			if b.Len() > 0 && !strings.HasSuffix(b.String(), "-") {
				b.WriteByte('-')
			}
		}
	}
	id := strings.Trim(b.String(), "-")
	if id == "" || id == LocalID {
		id = "machine"
	}
	return id
}

func uniqueID(ms []Machine, id string) string {
	taken := map[string]bool{LocalID: true}
	for _, m := range ms {
		taken[m.ID] = true
	}
	if !taken[id] {
		return id
	}
	for i := 2; ; i++ {
		if c := fmt.Sprintf("%s-%d", id, i); !taken[c] {
			return c
		}
	}
}

// SSHHosts lists concrete Host aliases from ~/.ssh/config (and files it
// includes directly), for suggesting machines to add.
func SSHHosts() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var hosts []string
	var read func(path string, depth int)
	read = func(path string, depth int) {
		b, err := os.ReadFile(path)
		if err != nil || depth > 3 {
			return
		}
		for _, line := range strings.Split(string(b), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			switch strings.ToLower(fields[0]) {
			case "host":
				for _, h := range fields[1:] {
					if !strings.ContainsAny(h, "*?!") && !seen[h] {
						seen[h] = true
						hosts = append(hosts, h)
					}
				}
			case "include":
				for _, pat := range fields[1:] {
					if strings.HasPrefix(pat, "~/") {
						pat = filepath.Join(home, pat[2:])
					} else if !filepath.IsAbs(pat) {
						pat = filepath.Join(home, ".ssh", pat)
					}
					matches, _ := filepath.Glob(pat)
					for _, m := range matches {
						read(m, depth+1)
					}
				}
			}
		}
	}
	read(filepath.Join(home, ".ssh", "config"), 0)
	return hosts
}
