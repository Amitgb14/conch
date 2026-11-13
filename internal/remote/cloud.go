package remote

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/Amitgb14/conch/internal/cloud"
)

// A Conch Cloud machine is saved as a machine whose target is
// "conch:<machine id>". It is reached through the relay rather than ssh:
// a script runs as `conch cloud exec ID -- SCRIPT` and the protocol goes
// through `conch cloud bridge ID` — this binary, which signs the request
// for the tunnel with this device's Conch Cloud sign-in (PROTOCOL §3.2).
// Everything above — probing, bridging, panes — is the same as for any
// machine. An older conch reading the catalog tries ssh to that target,
// fails to resolve it and shows the machine offline.

// CloudPrefix starts a cloud machine's catalog target.
const CloudPrefix = "conch:"

// CloudTarget is the catalog target of cloud machine id.
func CloudTarget(id string) string { return CloudPrefix + id }

// ParseCloudTarget is the machine id of a cloud machine's target.
func ParseCloudTarget(target string) (string, bool) {
	id, ok := strings.CutPrefix(target, CloudPrefix)
	if !ok || !cloud.IsMachineID(id) {
		return "", false
	}
	return id, true
}

// IsCloud reports whether the machine is a Conch Cloud machine.
func (m Machine) IsCloud() bool {
	_, ok := ParseCloudTarget(m.Target)
	return ok
}

// cloudExecutable is the conch run for `conch cloud exec` and `bridge`:
// this one. Tests replace it.
var cloudExecutable = func() (string, error) { return os.Executable() }

// CloudAsleepError is a cloud machine that sleeps, so nothing answers
// there. Waking it uses the account's hours, so it is the person's call:
// a background connection never does.
type CloudAsleepError struct {
	Label string
	State string
}

func (e *CloudAsleepError) Error() string {
	return fmt.Sprintf("%s is %s", e.Label, e.State)
}

// openCloud is the signed-in client; tests replace it.
var openCloud = cloud.Open

// cloudTransportFor reaches cloud machine id, asking the control plane
// its state first: an asleep machine is said rather than woken.
func cloudTransportFor(ctx context.Context, label, id string) (Transport, error) {
	c, err := openCloud()
	if err != nil {
		return nil, fmt.Errorf("%s is a Conch Cloud machine: %w", label, err)
	}
	m, err := c.Machine(ctx, id)
	switch {
	case errors.Is(err, cloud.ErrNotFound):
		return nil, fmt.Errorf("cloud machine %s (%s) no longer exists", label, id)
	case err != nil:
		return nil, err
	}
	switch m.State {
	case cloud.StateAsleep, cloud.StateSleeping, cloud.StateArchived:
		return nil, &CloudAsleepError{Label: label, State: m.State}
	case cloud.StateError:
		detail := m.StateDetail
		if detail == "" {
			detail = "no reason given"
		}
		return nil, fmt.Errorf("%s is in error: %s", label, detail)
	case cloud.StateDestroying:
		return nil, fmt.Errorf("%s is being destroyed", label)
	}
	return CloudTransport(label, id)
}

// CloudTransport reaches cloud machine id through this conch's
// `conch cloud exec` and `conch cloud bridge`, whatever its state: a
// machine asleep is woken by the first of them.
func CloudTransport(label, id string) (Transport, error) {
	bin, err := cloudExecutable()
	if err != nil {
		return nil, err
	}
	return &cloudTransport{bin: bin, id: id, name: label}, nil
}

type cloudTransport struct {
	bin, id, name string
	bridge        bool
}

func (t *cloudTransport) Command(ctx context.Context, script string) (*exec.Cmd, error) {
	var cmd *exec.Cmd
	if t.bridge {
		// The bridge is the machine's own conch server, whatever conch
		// the probe found there: the tunnel's /bridge reaches its socket.
		cmd = exec.CommandContext(ctx, t.bin, "cloud", "bridge", t.id)
	} else {
		cmd = exec.CommandContext(ctx, t.bin, "cloud", "exec", t.id, "--", script)
	}
	cmd.Env = append(os.Environ(), "CONCH_SOCKET=", "CONCH_PANE_ID=")
	cmd.WaitDelay = 2 * time.Second
	return cmd, nil
}

func (t *cloudTransport) Describe() string { return t.name }

func (t *cloudTransport) interactive() bool { return false }

func (t *cloudTransport) forBridge() Transport {
	return &cloudTransport{bin: t.bin, id: t.id, name: t.name, bridge: true}
}

// noInstall: the machine's image carries its conch, and its server is
// the agent's to run; a binary copied there would not be the one the
// tunnel reaches.
func (t *cloudTransport) noInstall() bool { return true }

// failed keeps conch's own last words; 255 is a tunnel that did not
// connect or dropped, worth trying again.
func (t *cloudTransport) failed(err error, stderr string) error {
	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(stderr), "\n") {
		if l = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), "conch:")); l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) > 3 {
		lines = lines[len(lines)-3:]
	}
	msg := strings.Join(lines, "\n")
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == CloudExitConnection {
		return &ConnectionError{Err: err, Stderr: msg}
	}
	if msg == "" {
		return err
	}
	return fmt.Errorf("%s: %s", t.name, msg)
}

// Exit statuses `conch cloud exec` takes for itself, as ssh takes 255: the
// tunnel did not connect or dropped (worth trying again), or it never
// will without the person (signed out, a changed key).
const (
	CloudExitConnection = 255
	CloudExitRefused    = 254
)

// SyncCloudMachines makes the catalog's cloud machines the account's: a
// machine new to it is added, under its name where that is free, one the
// account no longer has is removed, and a renamed one keeps its ID.
// Nothing else in the catalog is touched.
func SyncCloudMachines(ms []cloud.Machine) error {
	unlock, err := lockCatalog()
	if err != nil {
		return err
	}
	defer unlock()
	saved, err := Machines()
	if err != nil {
		return err
	}
	want := map[string]cloud.Machine{}
	for _, m := range ms {
		if m.State != cloud.StateDestroying {
			want[CloudTarget(m.ID)] = m
		}
	}
	changed := false
	out := saved[:0:0]
	have := map[string]bool{}
	for _, s := range saved {
		if !s.IsCloud() {
			out = append(out, s)
			continue
		}
		if _, ok := want[s.Target]; !ok {
			changed = true
			continue
		}
		have[s.Target] = true
		out = append(out, s)
	}
	for _, m := range ms {
		t := CloudTarget(m.ID)
		if _, ok := want[t]; !ok || have[t] {
			continue
		}
		label := m.Name
		if label == "" {
			label = m.ID
		}
		n := Machine{Label: label, Target: t, Enabled: true, Added: time.Now()}
		n.ID = uniqueID(out, slug(label))
		if labelTaken(out, n.Label, n.ID) {
			n.Label = n.ID
		}
		out = append(out, n)
		have[t] = true
		changed = true
	}
	if !changed {
		return nil
	}
	return saveMachines(out)
}

// ForgetCloudMachines removes every cloud machine from the catalog: the
// device signed out, so none of them can be reached.
func ForgetCloudMachines() error { return SyncCloudMachines(nil) }

// CloudExecutableForTest makes path the conch run for a cloud machine's
// exec and bridge, returning a func that puts it back. Tests in other
// packages use it; nothing else calls it.
func CloudExecutableForTest(path string) func() {
	old := cloudExecutable
	cloudExecutable = func() (string, error) { return path, nil }
	return func() { cloudExecutable = old }
}
