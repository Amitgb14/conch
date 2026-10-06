package phone

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/remote"
	"github.com/Amitgb14/conch/internal/server"
)

// A second machine in these tests is a second real conch server on its own
// socket: the gateway reaches machines through two replaceable variables —
// savedMachines (the catalog) and machineConnect (ssh) — so nothing here
// goes near the network, and what is exercised is the gateway's own
// bookkeeping rather than a fake of it.
type otherMachine struct {
	id, label string
	sock      string
	c         *client.Client // the test's own connection, for making panes there
	srv       *server.Server
}

// addMachine starts a second server and makes the gateway reach it as a
// machine of that id. reachable false leaves it in the catalog but
// refusing, as a machine that is switched off does.
func addMachine(t *testing.T, f *fixture, id string, reachable bool) *otherMachine {
	t.Helper()
	dir, err := os.MkdirTemp("", "ph2")
	if err != nil {
		t.Fatal(err)
	}
	dir, _ = filepath.EvalSymlinks(dir)
	sock := filepath.Join(dir, "s.sock")
	srv := server.New(sock, dir)
	go srv.Run()
	m := &otherMachine{id: id, label: id, sock: sock, srv: srv}
	t.Cleanup(func() { srv.Stop(); time.Sleep(50 * time.Millisecond); os.RemoveAll(dir) })
	for range 100 {
		if m.c, err = client.Dial(sock, "test2"); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if m.c == nil {
		t.Fatalf("second server never answered: %v", err)
	}
	t.Cleanup(func() { m.c.Close() })
	go func() {
		for range m.c.Events {
		}
	}()

	saved := []remote.Machine{{ID: id, Label: id, Target: id + ".example", Enabled: true}}
	oldConnect, oldSaved := setMachineHooks(func(_ context.Context, want remote.Machine) (*client.Client, error) {
		if want.ID != id {
			return nil, errors.New("no such machine in this test")
		}
		if !reachable {
			return nil, errors.New("ssh: connect to host " + want.Target + " port 22: Host is unreachable\nand a second line nobody needs")
		}
		return client.Dial(sock, "conch-web-test-2")
	}, func() ([]remote.Machine, error) { return saved, nil })
	t.Cleanup(func() { setMachineHooks(oldConnect, oldSaved) })
	return m
}

// waitMachine waits for the gateway to settle on a state for a machine,
// since reaching one happens in the background.
func waitMachine(t *testing.T, f *fixture, p *fakePhone, id, state string) Machine {
	t.Helper()
	var got Machine
	waitFor(t, "machine "+id+" to be "+state, func() bool {
		var list MachineList
		if p.get("/api/machines", &list) != 200 {
			return false
		}
		for _, m := range list.Machines {
			if m.ID == id && m.State == state {
				got = m
				return true
			}
		}
		return false
	})
	return got
}

// TestPaneIDsCarryTheirMachine: a pane id is a server's own, so p3 exists
// on every machine. The phone is given machine:pane and may send back
// either that or a bare id, which means this computer — an app built
// before machines keeps working.
func TestPaneIDsCarryTheirMachine(t *testing.T) {
	if got := composePaneID(LocalMachine, "p3"); got != "local:p3" {
		t.Fatalf("composed %q", got)
	}
	for _, c := range []struct{ in, machine, pane string }{
		{"local:p3", "local", "p3"},
		{"p3", "local", "p3"},   // bare: this computer
		{"p12", "local", "p12"}, // more than one digit
		{"busybox:p1", "busybox", "p1"},
		{"vm-1:p99", "vm-1", "p99"},
	} {
		machine, pane, aerr := splitPaneID(c.in)
		if aerr != nil || machine != c.machine || pane != c.pane {
			t.Errorf("%q split to %q %q (%v)", c.in, machine, pane, aerr)
		}
	}
	for _, bad := range []string{"", ":", ":p3", "local:", "local:x3", "local:p", "local:p3x", "LOCAL:p3",
		"busy box:p1", "busybox:p3:p4", "p3 ", "pp3"} {
		if _, _, aerr := splitPaneID(bad); aerr == nil {
			t.Errorf("%q was accepted", bad)
		} else if aerr.Code != CodeBadRequest {
			t.Errorf("%q: %s, want bad_request", bad, aerr.Code)
		}
	}
	// A machine's id never holds a colon (remote.slug), so the split is on
	// the first one and nothing is ambiguous.
	if machineIDOK("has:colon") || machineIDOK("Upper") || machineIDOK("") || machineIDOK("sp ace") {
		t.Error("machineIDOK accepts an id the catalog could not make")
	}
	if !machineIDOK("busybox") || !machineIDOK("vm-1") || !machineIDOK("local") {
		t.Error("machineIDOK refuses an id the catalog does make")
	}
}

// TestMachinesWithNoCatalog: the usual phone reaches one machine, and
// everything about it must read as it did before machines existed.
func TestMachinesWithNoCatalog(t *testing.T) {
	f := newFixture(t)
	p := f.pair(PermFull)
	pane := f.pane("claude", "stty -echo; exec cat")

	var list MachineList
	if status := p.get("/api/machines", &list); status != 200 {
		t.Fatal(status)
	}
	if len(list.Machines) != 1 {
		t.Fatalf("machines %+v", list.Machines)
	}
	m := list.Machines[0]
	if m.ID != LocalMachine || m.State != MachineOnline || m.Agents != 1 || m.Detail != "" {
		t.Fatalf("this computer %+v", m)
	}
	// Its panes are addressed with the machine in front, and a bare id
	// still works — the app may have been cached before machines.
	agents := p.agents()
	if len(agents) != 1 || agents[0].Pane != phoneID(pane) || agents[0].Machine != LocalMachine {
		t.Fatalf("agents %+v", agents)
	}
	var res RenameRequest
	if status, e := p.post("/api/rename", RenameRequest{Pane: pane, Name: "bare"}, &res); status != 200 {
		t.Fatalf("a bare pane id: %d %+v", status, e)
	}
	if res.Pane != phoneID(pane) {
		t.Errorf("a bare id was not answered with the full one: %+v", res)
	}
}

// TestMachinesListAndMerge: a second machine's agents arrive in the same
// list, ordered by what needs the person first wherever it runs, and each
// machine says how many it contributed.
func TestMachinesListAndMerge(t *testing.T) {
	f := newFixture(t)
	other := addMachine(t, f, "busybox", true)
	p := f.pair(PermFull)

	here := f.pane("claude", "stty -echo; exec cat")
	there := startPaneOn(t, other, "claude", "stty -echo; exec cat")
	waitMachine(t, f, p, "busybox", MachineOnline)

	// The one that is waiting comes first, whichever machine it is on.
	f.call(proto.MethodAgentReport, proto.AgentReportParams{ID: here, Agent: "claude", Event: "Stop"}, nil)
	callOn(t, other, proto.MethodAgentReport, proto.AgentReportParams{ID: there, Agent: "claude",
		Event: "PermissionRequest", Message: "May I?"})

	var agents []Agent
	waitFor(t, "both machines' agents", func() bool {
		agents = p.agents()
		return len(agents) == 2 && agents[0].State == StateWaiting
	})
	if agents[0].Pane != composePaneID("busybox", there) || agents[0].Machine != "busybox" {
		t.Fatalf("the waiting one is %+v", agents[0])
	}
	if agents[1].Pane != phoneID(here) || agents[1].Machine != LocalMachine {
		t.Fatalf("this computer's is %+v", agents[1])
	}
	var list MachineList
	p.get("/api/machines", &list)
	if len(list.Machines) != 2 || list.Machines[0].ID != LocalMachine {
		t.Fatalf("machines %+v", list.Machines)
	}
	if list.Machines[0].Agents != 1 || list.Machines[1].Agents != 1 {
		t.Errorf("each machine counts its own: %+v", list.Machines)
	}

	// A pane there is acted on by its composite id, and answered with it.
	var rep ReplyResponse
	status, e := p.post("/api/reply", ReplyRequest{Pane: composePaneID("busybox", there), Text: "carry on"}, &rep)
	if status != 409 || e == nil || e.Code != CodeAgentBlocked {
		t.Fatalf("a reply to a waiting agent there: %d %+v", status, e)
	}
	var cl CloseResponse
	if status, e := p.post("/api/close", CloseRequest{Pane: composePaneID("busybox", there)}, &cl); status != 200 {
		t.Fatalf("closing a pane there: %d %+v", status, e)
	}
	if cl.Pane != composePaneID("busybox", there) {
		t.Errorf("close answered %+v", cl)
	}
	// And a machine nobody has is not found, while a pane shape that makes
	// no sense is a bad request.
	if _, e := p.post("/api/close", CloseRequest{Pane: "nowhere:p1"}, nil); e == nil || e.Code != CodeNotFound {
		t.Errorf("a machine that is not there: %+v", e)
	}
	if _, e := p.post("/api/close", CloseRequest{Pane: "busybox:nonsense"}, nil); e == nil || e.Code != CodeBadRequest {
		t.Errorf("a pane that is not a pane: %+v", e)
	}
}

// TestMachineOfflineDoesNotTakeTheOthers: the point of listing a machine's
// state at all. One that will not answer must leave the rest of the list
// alone and say what is wrong, in one line.
func TestMachineOfflineDoesNotTakeTheOthers(t *testing.T) {
	f := newFixture(t)
	addMachine(t, f, "vm1", false)
	p := f.pair(PermReply)
	here := f.pane("claude", "stty -echo; exec cat")

	m := waitMachine(t, f, p, "vm1", MachineOffline)
	if !strings.Contains(m.Detail, "Host is unreachable") {
		t.Fatalf("it does not say what is wrong: %+v", m)
	}
	if strings.Contains(m.Detail, "\n") || strings.Contains(m.Detail, "second line") {
		t.Errorf("the detail is more than a line: %q", m.Detail)
	}
	if m.Agents != 0 {
		t.Errorf("an offline machine counted %d agents", m.Agents)
	}
	// This computer's agents are all there.
	agents := p.agents()
	if len(agents) != 1 || agents[0].Pane != phoneID(here) {
		t.Fatalf("agents %+v", agents)
	}
	// Asking for a pane on it says so as itself, rather than not_found:
	// trying again shortly is the thing to do.
	if _, e := p.post("/api/reply", ReplyRequest{Pane: "vm1:p1", Text: "hi"}, nil); e == nil || e.Code != CodeServerUnavailable {
		t.Errorf("a pane on an offline machine: %+v", e)
	}
}

// TestMachineArrivesOnTheSocket: a phone left open hears about a machine
// coming up, and then follows its agents like any other.
func TestMachineArrivesOnTheSocket(t *testing.T) {
	old := machineJoinEvery
	machineJoinEvery = 100 * time.Millisecond
	t.Cleanup(func() { machineJoinEvery = old })

	f := newFixture(t)
	other := addMachine(t, f, "busybox", true)
	p := f.pair(PermFull)

	// Reaching a machine happens in the background, and a socket is told
	// when one comes up — but a socket opened *after* it came up is told
	// by agents.watch instead, which sends the machines before the list.
	// Waiting for either in turn was a race with itself: next() throws
	// away what it is not waiting for, so the machines message could be
	// eaten by the wait for the list and never come again. So: let it come
	// up first, then open the socket, where the order is the handler's.
	waitMachine(t, f, p, "busybox", MachineOnline)
	s := p.socket()
	s.send(ClientMessage{Type: MsgAgentsWatch, ID: "w1"})
	s.next("the machines", func(m ServerMessage) bool {
		if m.Type != MsgMachines || m.Machines == nil {
			return false
		}
		for _, mc := range *m.Machines {
			if mc.ID == "busybox" && mc.State == MachineOnline {
				return true
			}
		}
		return false
	})
	s.next("the list", func(m ServerMessage) bool { return m.Type == MsgAgents })

	// An agent that starts there arrives by itself, addressed with its
	// machine, which is the whole point of following every machine.
	there := startPaneOn(t, other, "claude", "stty -echo; exec cat")
	s.next("the agent on busybox", func(m ServerMessage) bool {
		return m.Type == MsgAgent && m.Agent != nil && m.Agent.Pane == composePaneID("busybox", there) &&
			m.Agent.Machine == "busybox"
	})
}

// startPaneOn makes a pane on another machine's server.
func startPaneOn(t *testing.T, m *otherMachine, agent, script string) string {
	t.Helper()
	var info proto.PaneInfo
	callOn(t, m, proto.MethodPaneCreate, proto.PaneCreateParams{Agent: agent,
		Command: []string{"/bin/sh", "-c", script}, Cols: 80, Rows: 24, NoProject: true}, &info)
	if agent != "" {
		waitFor(t, agent+" detected on "+m.id, func() bool {
			var list proto.PaneList
			callOn(t, m, proto.MethodPaneList, nil, &list)
			for _, p := range list.Panes {
				if p.ID == info.ID && p.Agent != nil {
					return true
				}
			}
			return false
		})
	}
	return info.ID
}

// callOn makes one call on another machine's server.
func callOn(t *testing.T, m *otherMachine, method string, params any, out ...any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var dst any
	if len(out) > 0 {
		dst = out[0]
	}
	if err := m.c.Call(ctx, method, params, dst); err != nil {
		t.Fatalf("%s on %s: %v", method, m.id, err)
	}
}

// TestStartSomethingOnAnotherMachine: the phone can start a terminal or an
// agent where the work is, not only on the laptop — and a project ID is
// that machine's own, so it is looked up there.
func TestStartSomethingOnAnotherMachine(t *testing.T) {
	f := newFixture(t)
	other := addMachine(t, f, "busybox", true)
	p := f.pair(PermFull)
	waitMachine(t, f, p, "busybox", MachineOnline)

	var res NewPaneResponse
	status, e := p.post("/api/panes", NewPaneRequest{Kind: KindTerminal, Machine: "busybox", Name: "build"}, &res)
	if status != 200 {
		t.Fatalf("a terminal on busybox: %d %+v", status, e)
	}
	if !strings.HasPrefix(res.Pane, "busybox:") {
		t.Fatalf("it answered %q", res.Pane)
	}
	// It is there, and not here.
	var theirs proto.PaneList
	callOn(t, other, proto.MethodPaneList, nil, &theirs)
	if len(theirs.Panes) != 1 || theirs.Panes[0].Name != "build" {
		t.Fatalf("panes on busybox %+v", theirs.Panes)
	}
	var mine proto.PaneList
	f.call(proto.MethodPaneList, nil, &mine)
	if len(mine.Panes) != 0 {
		t.Fatalf("it started here instead: %+v", mine.Panes)
	}
	// A machine nobody has, and one that is merely not up.
	if _, e := p.post("/api/panes", NewPaneRequest{Kind: KindTerminal, Machine: "nowhere"}, nil); e == nil || e.Code != CodeNotFound {
		t.Errorf("a machine that is not there: %+v", e)
	}
	if _, e := p.post("/api/panes", NewPaneRequest{Kind: KindTerminal, Machine: "NOPE!"}, nil); e == nil || e.Code != CodeBadRequest {
		t.Errorf("a machine id that could not exist: %+v", e)
	}
	// No machine named is this computer, as it was before machines.
	if status, e := p.post("/api/panes", NewPaneRequest{Kind: KindTerminal, Name: "here"}, &res); status != 200 {
		t.Fatalf("a terminal with no machine named: %d %+v", status, e)
	}
	if !strings.HasPrefix(res.Pane, "local:") {
		t.Errorf("it answered %q", res.Pane)
	}
}

// TestMachineGoneFromTheCatalog: a machine taken out of the catalog, or
// turned off in it, stops being listed and its connection is dropped.
func TestMachineGoneFromTheCatalog(t *testing.T) {
	f := newFixture(t)
	addMachine(t, f, "busybox", true)
	p := f.pair(PermView)
	waitMachine(t, f, p, "busybox", MachineOnline)

	// Turned off in the catalog: not listed at all, as the contract says.
	setMachineHooks(nil, func() ([]remote.Machine, error) {
		return []remote.Machine{{ID: "busybox", Label: "busybox", Target: "busybox.example", Enabled: false}}, nil
	})
	waitFor(t, "busybox to go", func() bool {
		var list MachineList
		p.get("/api/machines", &list)
		return len(list.Machines) == 1 && list.Machines[0].ID == LocalMachine
	})
	// And a catalog that cannot be read leaves this computer listed rather
	// than inventing machines or failing the list.
	setMachineHooks(nil, func() ([]remote.Machine, error) { return nil, errors.New("machines.json is nonsense") })
	var list MachineList
	if status := p.get("/api/machines", &list); status != 200 {
		t.Fatalf("a broken catalog: %d", status)
	}
	if len(list.Machines) != 1 || list.Machines[0].ID != LocalMachine || list.Machines[0].State != MachineOnline {
		t.Fatalf("machines %+v", list.Machines)
	}
}

// TestProjectsComeFromTheMachineAsked: a project ID is a server's own, so
// the new-task form must be offered the projects of the machine it is
// about, not this computer's.
func TestProjectsComeFromTheMachineAsked(t *testing.T) {
	f := newFixture(t)
	other := addMachine(t, f, "busybox", true)
	p := f.pair(PermFull)
	waitMachine(t, f, p, "busybox", MachineOnline)

	// A project here, and a different one there. The paths come back as
	// the server resolved them, so the IDs are what is compared.
	here := t.TempDir()
	var hp proto.ProjectInfo
	f.call(proto.MethodProjectAdd, proto.ProjectAddParams{Path: here}, &hp)
	there := t.TempDir()
	var tp proto.ProjectInfo
	callOn(t, other, proto.MethodProjectAdd, proto.ProjectAddParams{Path: there}, &tp)

	var mine ProjectList
	if status := p.get("/api/projects", &mine); status != 200 {
		t.Fatal(status)
	}
	if len(mine.Projects) != 1 || mine.Projects[0].ID != hp.ID {
		t.Fatalf("this computer's projects %+v", mine.Projects)
	}
	var theirs ProjectList
	if status := p.get("/api/projects?machine=busybox", &theirs); status != 200 {
		t.Fatal(status)
	}
	if len(theirs.Projects) != 1 || theirs.Projects[0].ID != tp.ID {
		t.Fatalf("busybox's projects %+v", theirs.Projects)
	}
	// A machine that is not there says so rather than answering with this
	// computer's, which would start work in the wrong place.
	var none ProjectList
	if status := p.get("/api/projects?machine=nowhere", &none); status != 404 {
		t.Fatalf("a machine that is not there: %d %+v", status, none)
	}
}

// TestMachineComesUpWhileThePhoneWatches: the other half of the arrival,
// and the one a phone left open depends on — a machine that was not
// answering starts to, and the socket says so without being asked. Driven
// by making the connect succeed, rather than by waiting on a race.
func TestMachineComesUpWhileThePhoneWatches(t *testing.T) {
	oldJoin, oldRetry := machineJoinEvery, machineRetry
	machineJoinEvery, machineRetry = 100*time.Millisecond, 100*time.Millisecond
	t.Cleanup(func() { machineJoinEvery, machineRetry = oldJoin, oldRetry })

	f := newFixture(t)
	other := addMachine(t, f, "busybox", false) // refusing, at first
	p := f.pair(PermFull)
	waitMachine(t, f, p, "busybox", MachineOffline)

	s := p.socket()
	s.send(ClientMessage{Type: MsgAgentsWatch, ID: "w1"})
	s.next("the list", func(m ServerMessage) bool { return m.Type == MsgAgents })

	// Now it answers. The gateway reaches it on its next look and the
	// socket joins it; either of them tells the phone.
	setMachineHooks(func(_ context.Context, want remote.Machine) (*client.Client, error) {
		return client.Dial(other.sock, "conch-web-test-2")
	}, nil)
	s.next("busybox coming up", func(m ServerMessage) bool {
		if m.Type != MsgMachines || m.Machines == nil {
			return false
		}
		for _, mc := range *m.Machines {
			if mc.ID == "busybox" && mc.State == MachineOnline {
				return true
			}
		}
		return false
	})

	// And its agents arrive from then on, which is what the phone wanted.
	there := startPaneOn(t, other, "claude", "stty -echo; exec cat")
	s.next("the agent on busybox", func(m ServerMessage) bool {
		return m.Type == MsgAgent && m.Agent != nil && m.Agent.Pane == composePaneID("busybox", there)
	})
}
