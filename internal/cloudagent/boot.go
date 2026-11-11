// Package cloudagent is `conch cloud agent`: the program that runs inside a
// Conch Cloud machine, beside its conch server. It is the VM's side of the
// conch-cloud contract (conch-cloud/contract: openapi.yaml's /v1/vm/*
// endpoints and PROTOCOL.md §3–§8): it says hello to the control plane with
// the machine token the host injected, reports what the agents are doing
// so the machine can sleep, joins the relay as the room's gateway and
// carries conch's own protocol to the local server, and hands agents their
// secrets and git its credentials without writing either down.
package cloudagent

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"syscall"
)

// Default places on a cloud machine (PROTOCOL §3.1, §4).
const (
	DefaultBootPath = "/run/conch-cloud/boot.json"
	DefaultStateDir = "/var/lib/conch/cloud"
	DefaultKeyPath  = DefaultStateDir + "/noise.key"
	// DefaultAgentSocket is the agent's own socket (local.go) when it runs
	// as root: in /run, so it goes with the boot.
	DefaultAgentSocket = "/run/conch/cloud-agent.sock"
)

// Boot is /run/conch-cloud/boot.json, as the control plane's inject writes
// it (PROTOCOL §4): which machine this is, the API to call, the token to
// call it with and the conch release the machine should run.
type Boot struct {
	MachineID    string `json:"machine_id"`
	API          string `json:"api"`
	MachineToken string `json:"machine_token"`
	ConchVersion string `json:"conch_version"`
}

// machineIDPattern is the contract's MachineId (openapi.yaml parameters).
var machineIDPattern = regexp.MustCompile(`^m_[a-z0-9]{16}$`)

// maxBoot is far more than boot.json ever holds.
const maxBoot = 64 << 10

// errBootMissing is boot.json not there yet: on a machine that has just
// started, the host has not injected it.
var errBootMissing = errors.New("boot.json is not there yet")

// ReadBoot reads and checks boot.json. The token in it is the machine's
// whole identity to the control plane, so a file anyone else on the
// machine could read — or one somebody else owns — is refused rather than
// used: the host writes it root-only, 0600, and anything wider means
// something other than the host made it.
func ReadBoot(path string) (Boot, error) {
	f, err := openPlain(path)
	if errors.Is(err, os.ErrNotExist) {
		return Boot{}, fmt.Errorf("%s: %w", path, errBootMissing)
	}
	if err != nil {
		return Boot{}, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return Boot{}, err
	}
	if !st.Mode().IsRegular() {
		return Boot{}, fmt.Errorf("%s is not a file", path)
	}
	if perm := st.Mode().Perm(); perm&0o077 != 0 {
		return Boot{}, fmt.Errorf("%s is readable by others (mode %04o): it holds the machine token, so only its owner may read it (0600)", path, perm)
	}
	if sys, ok := st.Sys().(*syscall.Stat_t); ok && int(sys.Uid) != os.Geteuid() {
		return Boot{}, fmt.Errorf("%s belongs to uid %d, not to this process (uid %d)", path, sys.Uid, os.Geteuid())
	}
	b, err := io.ReadAll(io.LimitReader(f, maxBoot+1))
	if err != nil {
		return Boot{}, err
	}
	if len(b) > maxBoot {
		return Boot{}, fmt.Errorf("%s is over %d bytes", path, maxBoot)
	}
	var boot Boot
	if err := json.Unmarshal(b, &boot); err != nil {
		return Boot{}, fmt.Errorf("%s: %w", path, err)
	}
	if err := boot.check(); err != nil {
		return Boot{}, fmt.Errorf("%s: %w", path, err)
	}
	return boot, nil
}

// openPlain opens path for reading only if it is a file of its own: not
// through a symlink, and without waiting on a FIFO, which would hang the
// agent's every look at boot.json (the type is checked after, on what was
// opened).
func openPlain(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if errors.Is(err, syscall.ELOOP) {
		return nil, fmt.Errorf("%s is a symlink; it must be the file itself", path)
	}
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if !st.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("%s is not a file", path)
	}
	return f, nil
}

func (b Boot) check() error {
	var missing []string
	for name, v := range map[string]string{"machine_id": b.MachineID, "api": b.API, "machine_token": b.MachineToken} {
		if strings.TrimSpace(v) == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		return fmt.Errorf("missing %s", strings.Join(missing, ", "))
	}
	if !machineIDPattern.MatchString(b.MachineID) {
		return fmt.Errorf("machine_id %q is not a machine id (m_ and 16 letters or digits)", b.MachineID)
	}
	u, err := url.Parse(b.API)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return fmt.Errorf("api %q is not an http(s) URL", b.API)
	}
	if strings.ContainsAny(b.MachineToken, " \t\r\n") {
		return errors.New("machine_token has white space in it")
	}
	return nil
}
