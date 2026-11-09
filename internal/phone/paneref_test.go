package phone

import (
	"errors"
	"testing"

	"github.com/Amitgb14/conch/internal/remote"
)

// A machine's Ref is what the phone puts before a pane's ID for conch's
// commands, so it must reach that machine and no other: its label only
// when nothing else in the catalog answers to it — a disabled machine,
// or one this device was not given, included, since the commands look
// through all of them.
func TestMachineRefIsUnambiguous(t *testing.T) {
	oldConnect, oldSaved := setMachineHooks(nil, func() ([]remote.Machine, error) {
		return []remote.Machine{
			{ID: "devbox", Label: "devbox", Target: "alice@devbox", Enabled: true},
			{ID: "devbox-2", Label: "devbox", Target: "bob@devbox", Enabled: true},
			{ID: "old", Label: "gpu", Target: "old-gpu", Enabled: false},
			{ID: "gpu-1", Label: "gpu", Target: "gpu", Enabled: true},
			{ID: "bb", Label: "busybox", Target: "busybox", Enabled: true},
			{ID: "loc", Label: "local", Target: "loc", Enabled: true},
		}, nil
	})
	t.Cleanup(func() { setMachineHooks(oldConnect, oldSaved) })

	// What a device given only some of them is shown: not devbox, not old.
	list := machineList([]machineState{
		{ID: LocalMachine, Label: "this computer"},
		{ID: "devbox-2", Label: "devbox"},
		{ID: "gpu-1", Label: "gpu"},
		{ID: "bb", Label: "busybox"},
		{ID: "loc", Label: "local"},
		{ID: "gone", Label: "gone-label"}, // listed, but no longer in the catalog
	}, nil)
	want := map[string]string{LocalMachine: "", "devbox-2": "devbox-2", "gpu-1": "gpu-1", "bb": "busybox", "loc": "loc", "gone": "gone"}
	for _, m := range list {
		if m.Ref != want[m.ID] {
			t.Errorf("%s: ref %q, want %q", m.ID, m.Ref, want[m.ID])
		}
	}

	// A catalog that cannot be read: every machine by its ID, which is
	// never wrong.
	setMachineHooks(nil, func() ([]remote.Machine, error) { return nil, errors.New("broken") })
	for _, m := range machineList([]machineState{{ID: "bb", Label: "busybox"}}, nil) {
		if m.Ref != "bb" {
			t.Errorf("no catalog: ref %q", m.Ref)
		}
	}
}
