package tui

import (
	"fmt"

	"github.com/Amitgb14/conch/internal/proto"
)

// A run's gate (conch run, internal/server/graph.go) waits for the
// person, who may be looking at anything but the coordinator's pane: so
// the moment a task comes to stand at one, the status line says so, and
// what to type. Said once per gate, not at every change to its run.
func (m *Model) runChanged(mach *machine, r proto.Run) {
	if mach.gates == nil {
		mach.gates = map[string]bool{}
	}
	for _, t := range r.Tasks {
		key := r.ID + "/" + t.ID
		if !t.GateWaits() {
			delete(mach.gates, key)
			continue
		}
		if mach.gates[key] {
			continue
		}
		mach.gates[key] = true
		where := ""
		if mach.id != "" && mach.id != localMachine {
			where = "-m " + mach.id + " "
		}
		m.setFlash(fmt.Sprintf("run %s: %s waits for your approval — %s · conch %srun approve %s %s",
			r.Name, t.ID, t.Gate, where, r.Name, t.ID), false)
	}
}
