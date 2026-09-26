package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/remote"
	"github.com/Amitgb14/conch/internal/sandbox"
)

// A stopped sandbox waits for the user, like a machine needing an install:
// retrying on a timer would only poll the provider, and starting it costs.
func TestSandboxStoppedNeedsTheUser(t *testing.T) {
	mach := newMachine("fix", "fix", "daytona:sb1")
	mach.failures = 2
	err := fmt.Errorf("connect: %w", &remote.SandboxStoppedError{Label: "fix", State: sandbox.StateStopped})
	if cmd := mach.connected(machineConnectedMsg{machine: "fix", err: err}); cmd != nil {
		t.Fatal("a stopped sandbox was scheduled for a retry")
	}
	if mach.state != stateAttention || mach.err != "sandbox stopped · conch sandbox start fix" || mach.failures != 2 {
		t.Fatalf("state %v err %q failures %d", mach.state, mach.err, mach.failures)
	}
}

func TestSandboxMachineInTUI(t *testing.T) {
	a2Isolate(t)
	m := a2Model()
	saved, _ := remote.SaveMachine(remote.Machine{Label: "fix-login", Target: "daytona:sb1"})
	m.syncCatalog()
	m.rebuild()
	mach := m.machine(saved.ID)
	if mach == nil {
		t.Fatal("sandbox not shown")
	}

	// Its page says what it is, not an ssh target.
	page := ansi.Strip(strings.Join(m.machineLines(mach, 100, 20), "\n"))
	if !strings.Contains(page, "daytona sandbox sb1") || strings.Contains(page, "ssh ") {
		t.Fatalf("page:\n%s", page)
	}
	// Shown on screen, it keeps within the width however narrow.
	m.cursor = machineID(saved.ID)
	for _, w := range []int{1, 20, 39, 100} {
		for _, l := range strings.Split(a1Sized(t, m, w, 8).View(), "\n") {
			if lw := ansi.StringWidth(l); lw > w {
				t.Fatalf("%d cols: line %d wide: %q", w, lw, ansi.Strip(l))
			}
		}
	}

	// It is no ssh host to offer.
	for _, h := range m.sshHosts() {
		if strings.Contains(h, "daytona") {
			t.Fatalf("hosts %v", m.sshHosts())
		}
	}

	// Renaming names the sandbox that stays.
	m.openRenameMachine(saved.ID)
	d, ok := m.overlay.(*dialog)
	if !ok || !strings.Contains(strings.Join(d.text, " "), "the daytona sandbox (sb1) stays") {
		t.Fatalf("dialog: %#v", m.overlay)
	}
}
