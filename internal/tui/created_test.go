package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

// a5Helpers puts a reviewer and a tester under p1, the agent that started
// them, and one pane nobody started.
func a5Helpers(t *testing.T) *Model {
	t.Helper()
	m, _ := a1Fixture(t, false)
	mach := m.machines[0]
	mach.panes = append(mach.panes,
		proto.PaneInfo{ID: "p8", Name: "reviewer", State: proto.PaneRunning, CreatedBy: "p1",
			Agent: &proto.AgentStatus{Name: "codex", State: proto.AgentWorking}},
		proto.PaneInfo{ID: "p9", Name: "tester", State: proto.PaneRunning, CreatedBy: "p1",
			Agent: &proto.AgentStatus{Name: "claude", State: proto.AgentIdle}},
	)
	m.rebuild()
	return m
}

func TestHelpersReadAsSomebodysHelpers(t *testing.T) {
	m := a5Helpers(t)
	mach := m.machines[0]
	row := func(id string) string {
		i := indexOfRow(m.rows, paneNodeID(localMachine, id))
		if i < 0 {
			t.Fatalf("no row for %s", id)
		}
		g, gs, label, ls, right := m.rowParts(m.rows[i])
		_ = g
		_ = gs
		_ = ls
		return ansi.Strip(label + " " + right)
	}

	// A helper says whose it is; the agent that started them says how many.
	if got := row("p8"); !strings.Contains(got, "↳ for claude") {
		t.Fatalf("the helper's row: %q", got)
	}
	if got := row("p1"); !strings.Contains(got, "⑂2") {
		t.Fatalf("the creator's row: %q", got)
	}
	// A pane nobody started says nothing either way.
	if got := row("p4"); strings.Contains(got, "↳") || strings.Contains(got, "⑂") {
		t.Fatalf("a pane with no lineage: %q", got)
	}

	// One of them waiting marks the creator's row.
	mach.panes[len(mach.panes)-2].Agent.State = proto.AgentBlocked
	if got := row("p1"); !strings.Contains(got, "⑂2!") {
		t.Fatalf("a waiting helper does not show on its creator: %q", got)
	}
	// A helper that has ended is not counted any more.
	mach.panes[len(mach.panes)-1].State = proto.PaneExited
	total, waiting := m.helpersOf(localMachine, "p1")
	if total != 1 || waiting != 1 {
		t.Fatalf("helpers after one ended: %d total, %d waiting", total, waiting)
	}
	// A creator that has gone leaves the helper without a claim about it.
	mach.panes = mach.panes[1:]
	m.rebuild()
	if got := row("p8"); strings.Contains(got, "↳") {
		t.Fatalf("the creator is gone but the row still names it: %q", got)
	}
}

func TestQueueNamesWhoAHelperWorksFor(t *testing.T) {
	m := a5Helpers(t)
	mach := m.machines[0]
	for i := range mach.panes {
		if mach.panes[i].ID == "p8" {
			mach.panes[i].Agent.State = proto.AgentBlocked
		}
	}
	var found string
	for _, it := range m.queueItemsAll() {
		if it.paneID == "p8" {
			found = it.detail
		}
	}
	if !strings.Contains(found, "waiting for an answer for claude") {
		t.Fatalf("the queue row reads %q", found)
	}
	// An agent nobody started keeps the plain wording.
	for _, it := range m.queueItemsAll() {
		if it.paneID == "p4" && strings.Contains(it.detail, " for ") {
			t.Fatalf("a pane with no creator reads %q", it.detail)
		}
	}
}

func TestNotificationAndJumpNameTheCreator(t *testing.T) {
	m := a5Helpers(t)
	m.cfg.Notify.Enabled, m.cfg.Notify.Waiting, m.cfg.Notify.Desktop = true, true, true
	mach := m.machines[0]
	i := 0
	for j := range mach.panes {
		if mach.panes[j].ID == "p8" {
			i = j
		}
	}
	old := mach.panes[i]
	mach.panes[i].Agent = &proto.AgentStatus{Name: "codex", State: proto.AgentBlocked}
	info := mach.panes[i]

	// The notification says whose helper is waiting; a bare agent doesn't
	// gain words it has no business with.
	if got := attentionBody(info) + m.forWhom(localMachine, info); !strings.Contains(got, "for claude") {
		t.Fatalf("the notification reads %q", got)
	}
	if m.notifyAttention(mach, old, info) == nil {
		t.Fatal("nothing was sent")
	}
	// Jumping to it says the same thing.
	m.viewMachine, m.viewing = "", ""
	m.jumpToAttention()
	if !strings.Contains(m.flash, "for claude") {
		t.Fatalf("the jump says %q", m.flash)
	}
	// A pane nobody started keeps the plain words.
	plain := proto.PaneInfo{ID: "p4", Name: "codex", State: proto.PaneRunning,
		Agent: &proto.AgentStatus{Name: "codex", State: proto.AgentBlocked}}
	if who := m.forWhom(localMachine, plain); who != "" {
		t.Fatalf("a pane with no creator gained %q", who)
	}
}
