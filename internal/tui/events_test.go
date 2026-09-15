package tui

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

func eventMsg(t *testing.T, event string, data any) proto.Message {
	t.Helper()
	b, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	return proto.Message{Event: event, Data: b}
}

func TestHandleEventFrames(t *testing.T) {
	m, _ := a1Fixture(t, false)
	mach := m.machines[0]
	frame := proto.Frame{ID: "p1", Lines: []string{"hello"}, Offset: 3}

	// Not subscribed: dropped.
	if m.handleEvent(mach, eventMsg(t, proto.EventPaneFrame, frame)) != nil || m.frames[paneKey(localMachine, "p1")] != nil {
		t.Fatal("unsubscribed frame kept")
	}
	m.subscribed[paneKey(localMachine, "p1")] = true
	m.handleEvent(mach, eventMsg(t, proto.EventPaneFrame, frame))
	if f := m.frames[paneKey(localMachine, "p1")]; f == nil || f.Lines[0] != "hello" || m.frame != nil {
		t.Fatal("subscribed frame not stored, or shown while not viewed")
	}
	// The viewed pane's frame is what the main area draws, with its offset.
	m.viewMachine, m.viewing = localMachine, "p1"
	m.handleEvent(mach, eventMsg(t, proto.EventPaneFrame, frame))
	if m.frame == nil || m.offset != 3 {
		t.Fatalf("viewed frame: %+v offset %d", m.frame, m.offset)
	}
	// Malformed data changes nothing.
	m.frame = nil
	if m.handleEvent(mach, proto.Message{Event: proto.EventPaneFrame, Data: []byte("{bad")}) != nil || m.frame != nil {
		t.Fatal("malformed frame")
	}
}

func TestHandleEventPaneLifecycle(t *testing.T) {
	m, peer := a1Fixture(t, true)
	mach := m.machines[0]
	n := len(mach.panes)

	// Created: added once, listed in the tree.
	zsh := proto.PaneInfo{ID: "p9", Name: "zsh", State: proto.PaneRunning}
	if m.handleEvent(mach, eventMsg(t, proto.EventPaneCreated, zsh)); len(mach.panes) != n+1 || indexOfRow(m.rows, paneNodeID(localMachine, "p9")) < 0 {
		t.Fatal("created")
	}
	if m.handleEvent(mach, eventMsg(t, proto.EventPaneCreated, zsh)) != nil || len(mach.panes) != n+1 {
		t.Fatal("a repeated create added the pane twice")
	}
	if m.handleEvent(mach, proto.Message{Event: proto.EventPaneCreated, Data: []byte("[]")}) != nil {
		t.Fatal("malformed create")
	}

	// Updated: an unknown pane is ignored; an agent is remembered as one.
	if m.handleEvent(mach, eventMsg(t, proto.EventPaneUpdated, proto.PaneInfo{ID: "nope"})) != nil {
		t.Fatal("unknown pane updated")
	}
	if m.handleEvent(mach, proto.Message{Event: proto.EventPaneUpdated, Data: []byte("x")}) != nil {
		t.Fatal("malformed update")
	}
	zsh.Agent = &proto.AgentStatus{Name: "claude", State: proto.AgentIdle}
	if m.handleEvent(mach, eventMsg(t, proto.EventPaneUpdated, zsh)); !mach.agents["p9"] || m.pane(localMachine, "p9").Agent == nil {
		t.Fatal("update")
	}
	zsh.Agent = nil

	// A pane that stops while typed into hands focus back to the tree.
	m.viewMachine, m.viewing, m.focus = localMachine, "p9", focusMain
	stopped := zsh
	stopped.State = proto.PaneExited
	m.handleEvent(mach, eventMsg(t, proto.EventPaneUpdated, stopped))
	if m.focus != focusSidebar {
		t.Fatal("focus stayed on a stopped pane")
	}

	// Exited on its own: closed, like tmux.
	exited := proto.PaneInfo{ID: "p9", Name: "zsh", State: proto.PaneExited, Created: time.Now().Add(-time.Hour)}
	a2Run(m.handleEvent(mach, eventMsg(t, proto.EventPaneExited, exited)))
	peer.waitMethod(t, proto.MethodPaneClose, `"p9"`)

	// Failed right after starting: kept so its error can be read.
	failed := proto.PaneInfo{ID: "p3", Name: "bash", State: proto.PaneExited, ExitCode: 127, Created: time.Now()}
	a2Run(m.handleEvent(mach, eventMsg(t, proto.EventPaneExited, failed)))
	if got := peer.count(t, mach.c, proto.MethodPaneClose, `"p3"`); got != 0 {
		t.Fatalf("a pane that failed at once was closed %d times", got)
	}

	// An agent installer's exit reports instead of closing.
	mach.installers["p4"] = "codex"
	ok := proto.PaneInfo{ID: "p4", Name: "install", State: proto.PaneExited, Created: time.Now().Add(-time.Hour)}
	m.handleEvent(mach, eventMsg(t, proto.EventPaneExited, ok))
	if m.flashIsErr || m.flash == "" || mach.installers["p4"] != "" {
		t.Fatalf("installer done: %q", m.flash)
	}
	mach.installers["p4"] = "codex"
	ok.ExitCode = 1
	m.handleEvent(mach, eventMsg(t, proto.EventPaneExited, ok))
	if !m.flashIsErr {
		t.Fatalf("installer failed: %q", m.flash)
	}

	// Closed: gone from the machine, its frame, subscription and tabs.
	a1Open(t, m, paneNodeID(localMachine, "p9"))
	key := paneKey(localMachine, "p9")
	m.subscribed[key], m.frames[key] = true, &proto.Frame{}
	mach.sizes["p9"] = [2]int{80, 24}
	m.focus = focusMain
	if m.handleEvent(mach, eventMsg(t, proto.EventPaneClosed, proto.PaneRef{ID: "p9"})) == nil {
		t.Fatal("close returned nothing")
	}
	if m.pane(localMachine, "p9") != nil || m.subscribed[key] || m.frames[key] != nil || mach.agents["p9"] || len(mach.sizes) != 0 ||
		m.focus != focusSidebar || indexOfRow(m.rows, paneNodeID(localMachine, "p9")) >= 0 {
		t.Fatal("closed pane left behind")
	}
	for _, tb := range m.tabs {
		if tabShows(tb, paneNodeID(localMachine, "p9")) {
			t.Fatal("a tab still shows the closed pane")
		}
	}
	if m.handleEvent(mach, eventMsg(t, proto.EventPaneClosed, proto.PaneRef{ID: "p9"})) != nil {
		t.Fatal("closing an unknown pane")
	}
}

func TestHandleEventProjects(t *testing.T) {
	m, _ := a1Fixture(t, false)
	mach := m.machines[0]

	renamed := mach.projects[0]
	renamed.Name = "api2"
	if m.handleEvent(mach, eventMsg(t, proto.EventProjectUpdated, renamed)); len(mach.projects) != 1 || mach.projects[0].Name != "api2" {
		t.Fatalf("updated: %+v", mach.projects)
	}
	web := proto.ProjectInfo{ID: "r2", Name: "web", Path: "/src/web"}
	m.handleEvent(mach, eventMsg(t, proto.EventProjectUpdated, web))
	if len(mach.projects) != 2 || indexOfRow(m.rows, projectNodeID(localMachine, "r2")) < 0 {
		t.Fatal("a new project isn't listed")
	}
	if m.rows[indexOfRow(m.rows, workspaceID(localMachine))].count != 2 {
		t.Fatal("workspace count")
	}
	if m.handleEvent(mach, proto.Message{Event: proto.EventProjectUpdated, Data: []byte("1")}) != nil {
		t.Fatal("malformed project")
	}

	if m.handleEvent(mach, eventMsg(t, proto.EventProjectRemoved, proto.ProjectRef{ID: "r2"})); len(mach.projects) != 1 {
		t.Fatal("removed")
	}
	m.handleEvent(mach, eventMsg(t, proto.EventProjectRemoved, proto.ProjectRef{ID: "gone"}))
	if len(mach.projects) != 1 {
		t.Fatal("removing an unknown project removed another")
	}
	// The last project removed: no Workspace row.
	m.handleEvent(mach, eventMsg(t, proto.EventProjectRemoved, proto.ProjectRef{ID: "r1"}))
	if indexOfRow(m.rows, workspaceID(localMachine)) >= 0 {
		t.Fatal("empty workspace listed")
	}
	if m.handleEvent(mach, proto.Message{Event: proto.EventProjectRemoved, Data: []byte("x")}) != nil {
		t.Fatal("malformed removal")
	}
	if m.handleEvent(mach, proto.Message{Event: "future.event"}) != nil {
		t.Fatal("unknown event")
	}
}
