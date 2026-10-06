package phone

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// A device reaches this computer, and whatever machines it has been given
// by name. The gateway reaches every machine in the catalog with the
// person's own credentials, so a phone must not inherit that by being
// paired, or by conch being updated under it.

// TestADeviceReachesThisComputerUntilGivenAMachine: what a device without
// a machine is shown and allowed, and what changes the moment it is given
// one. Both halves matter: the refusal has to say how to lift it, or a
// machine nobody granted looks exactly like a machine that is switched off.
func TestADeviceReachesThisComputerUntilGivenAMachine(t *testing.T) {
	f := newFixture(t)
	other := addMachine(t, f, "busybox", true)
	p := f.pair(PermFull)

	here := f.pane("claude", "stty -echo; exec cat")
	there := startPaneOn(t, other, "claude", "stty -echo; exec cat")
	f.call(proto.MethodAgentReport, proto.AgentReportParams{ID: here, Agent: "claude", Event: "Stop"}, nil)
	callOn(t, other, proto.MethodAgentReport, proto.AgentReportParams{ID: there, Agent: "claude",
		Event: "PermissionRequest", Message: "May I?"})

	// Nothing of the other machine: not its agents, not its panes, not
	// even that it exists. A machine's label and whether it is up are the
	// person's business, not a paired device's.
	var agents []Agent
	waitFor(t, "this computer's agent", func() bool { agents = p.agents(); return len(agents) == 1 })
	if agents[0].Pane != phoneID(here) || agents[0].Machine != LocalMachine {
		t.Fatalf("agents %+v", agents)
	}
	var panes PaneList
	p.get("/api/panes", &panes)
	for _, pane := range panes.Panes {
		if machineOf(pane.Pane) != LocalMachine {
			t.Fatalf("a pane from elsewhere: %+v", pane)
		}
	}
	for _, path := range []string{"/api/machines", "/api/hello"} {
		var body map[string]any
		p.get(path, &body)
		raw, _ := json.Marshal(body)
		if strings.Contains(string(raw), "busybox") {
			t.Errorf("%s names a machine this device was not given: %s", path, raw)
		}
	}

	// And it may not act on one, by any route. The refusal says what to
	// run, since nothing else would tell the person why their phone can
	// see the laptop and not the machine beside it.
	ref := composePaneID("busybox", there)
	_, e := p.post("/api/close", CloseRequest{Pane: ref}, nil)
	if e == nil || e.Code != CodeForbidden {
		t.Fatalf("closing a pane there: %+v", e)
	}
	for _, want := range []string{p.id, "-machine busybox", "this computer only"} {
		if !strings.Contains(e.Message, want) {
			t.Errorf("the refusal does not say %q: %s", want, e.Message)
		}
	}
	for name, call := range map[string]func() *APIError{
		"reply": func() *APIError { _, e := p.post("/api/reply", ReplyRequest{Pane: ref, Text: "hi"}, nil); return e },
		"answer": func() *APIError {
			_, e := p.post("/api/answer", AnswerRequest{Pane: ref, QuestionID: "q1", Choice: "1"}, nil)
			return e
		},
		"rename": func() *APIError { _, e := p.post("/api/rename", RenameRequest{Pane: ref, Name: "x"}, nil); return e },
		"newPane": func() *APIError {
			_, e := p.post("/api/panes", NewPaneRequest{Kind: KindTerminal, Machine: "busybox"}, nil)
			return e
		},
		"task": func() *APIError {
			_, e := p.post("/api/task", TaskRequest{Machine: "busybox", Project: "r1234567", Agent: "claude", Prompt: "go"}, nil)
			return e
		},
	} {
		if e := call(); e == nil || e.Code != CodeForbidden {
			t.Errorf("%s on a machine it was not given: %+v", name, e)
		}
	}
	// The projects of a machine it cannot reach are not readable either —
	// a project path says what is on that machine.
	var projects ProjectList
	if status := p.get("/api/projects?machine=busybox", &projects); status != 403 {
		t.Errorf("projects there: %d %+v", status, projects)
	}

	// Given the machine, the same device sees and does everything.
	f.grant(p, "busybox")
	waitFor(t, "both machines' agents", func() bool { agents = p.agents(); return len(agents) == 2 })
	var list MachineList
	p.get("/api/machines", &list)
	if len(list.Machines) != 2 || list.Machines[1].ID != "busybox" {
		t.Fatalf("machines %+v", list.Machines)
	}
	var cl CloseResponse
	if status, e := p.post("/api/close", CloseRequest{Pane: ref}, &cl); e != nil || status != 200 {
		t.Fatalf("closing a pane there once granted: %d %+v", status, e)
	}

	// And taken away again, at once: no reconnecting, no new pairing.
	f.grant(p)
	if _, e := p.post("/api/close", CloseRequest{Pane: composePaneID("busybox", "p9")}, nil); e == nil || e.Code != CodeForbidden {
		t.Fatalf("after the machine was taken away: %+v", e)
	}
	waitFor(t, "this computer alone again", func() bool {
		var l MachineList
		p.get("/api/machines", &l)
		return len(l.Machines) == 1
	})
}

// TestAFileFromBeforeMachinesLoadsWithTheLaptopAlone: the upgrade. A
// phone.json written by a conch that had never heard of machines has no
// field for them, and the device it describes must come up reaching this
// computer and nothing else — nobody's phone gains a machine because they
// updated conch.
func TestAFileFromBeforeMachinesLoadsWithTheLaptopAlone(t *testing.T) {
	f := newFixture(t)
	addMachine(t, f, "busybox", true)
	p := f.pair(PermFull)

	// Rewrite the file as the older build wrote it: no "machines" key at
	// all, anywhere.
	path := filepath.Join(f.dir, StoreFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var st map[string]any
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatal(err)
	}
	for _, d := range st["devices"].([]any) {
		delete(d.(map[string]any), "machines")
	}
	old, _ := json.Marshal(st)
	if strings.Contains(string(old), "machines") {
		t.Fatalf("the fixture file still has a machines field: %s", old)
	}
	if err := os.WriteFile(path, old, 0o600); err != nil {
		t.Fatal(err)
	}

	devs, err := f.store.Devices()
	if err != nil || len(devs) != 1 {
		t.Fatalf("devices %+v: %v", devs, err)
	}
	if len(devs[0].Machines) != 0 {
		t.Errorf("an older file loaded with machines %v", devs[0].Machines)
	}
	if !devs[0].Reaches(LocalMachine) || devs[0].Reaches("busybox") {
		t.Error("an older file's device does not reach this computer only")
	}
	// And it is refused in practice, not only in the record.
	if _, e := p.post("/api/close", CloseRequest{Pane: "busybox:p1"}, nil); e == nil || e.Code != CodeForbidden {
		t.Fatalf("a device from an older file: %+v", e)
	}
	var list MachineList
	p.get("/api/machines", &list)
	if len(list.Machines) != 1 || list.Machines[0].ID != LocalMachine {
		t.Fatalf("machines %+v", list.Machines)
	}
}

// TestASocketIsToldOnlyAboutItsMachines: the socket is where a phone
// spends its time, so the same rule has to hold for a connection that was
// opened before a machine was given or taken away — and a frame is the
// whole screen of a pane, which is the leak that would matter.
func TestASocketIsToldOnlyAboutItsMachines(t *testing.T) {
	f := newFixture(t)
	other := addMachine(t, f, "busybox", true)
	p := f.pair(PermFull)
	there := startPaneOn(t, other, "claude", "stty -echo; exec cat")
	ref := composePaneID("busybox", there)

	s := p.socket()
	s.send(ClientMessage{Type: MsgAgentsWatch, ID: "1"})
	m := s.next("the machine list", func(m ServerMessage) bool { return m.Type == MsgMachines })
	if len(*m.Machines) != 1 || (*m.Machines)[0].ID != LocalMachine {
		t.Fatalf("a socket of a device with no machines was told %+v", *m.Machines)
	}
	// Opening a pane there is refused, and nothing of it comes back.
	s.send(ClientMessage{Type: MsgFrameOpen, ID: "2", Pane: ref})
	for _, msg := range s.until("ping-1") {
		if msg.Type == MsgError && msg.ID == "2" {
			if msg.Error.Code != CodeForbidden {
				t.Errorf("opening a pane there: %+v", msg.Error)
			}
			continue
		}
		if msg.Type == MsgFrame && msg.Frame != nil {
			t.Fatalf("a frame from a machine this device was not given: %+v", msg.Frame)
		}
	}

	// Given the machine, this same socket joins it and the pane opens.
	f.grant(p, "busybox")
	s.next("busybox on the socket", func(m ServerMessage) bool {
		return m.Type == MsgMachines && slices.ContainsFunc(*m.Machines, func(mm Machine) bool {
			return mm.ID == "busybox" && mm.State == MachineOnline
		})
	})
	s.send(ClientMessage{Type: MsgFrameOpen, ID: "3", Pane: ref})
	s.next("a frame from busybox", func(m ServerMessage) bool {
		return m.Type == MsgFrame && m.Frame != nil && m.Frame.Pane == ref
	})

	// Taken away while the socket is open: the machine goes from the list,
	// and no further frame arrives even though the pane is still open
	// there and still being written to.
	f.grant(p)
	s.next("busybox gone from the socket", func(m ServerMessage) bool {
		return m.Type == MsgMachines && !slices.ContainsFunc(*m.Machines, func(mm Machine) bool { return mm.ID == "busybox" })
	})
	callOn(t, other, proto.MethodPaneSendText, proto.PaneSendTextParams{ID: there, Text: "after it was taken away\r"})
	time.Sleep(200 * time.Millisecond) // long enough for a frame to have come
	for _, msg := range s.until("ping-2") {
		if msg.Type == MsgFrame && msg.Frame != nil && machineOf(msg.Frame.Pane) == "busybox" {
			t.Fatalf("a frame after the machine was taken away: %+v", msg.Frame)
		}
	}
	s.send(ClientMessage{Type: MsgKeys, ID: "4", Pane: ref, Keys: []string{"x"}})
	for _, msg := range s.until("ping-3") {
		if msg.Type == MsgError && msg.ID == "4" && msg.Error.Code != CodeForbidden {
			t.Errorf("typing there after it was taken away: %+v", msg.Error)
		}
	}
}

// TestSetMachinesChecksWhatItIsGiven: the store is where a machine is
// granted, and it is the only guard against a name that would be
// meaningless, duplicated, or this computer said twice.
func TestSetMachinesChecksWhatItIsGiven(t *testing.T) {
	dir := t.TempDir()
	s := OpenStore(dir)
	code, err := s.NewCode(PermFull, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	dev, _, err := s.Redeem(code, "phone", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(dev.Machines) != 0 || dev.Reaches("busybox") {
		t.Fatalf("a device is paired with machines: %+v", dev.Machines)
	}

	// Sorted, deduplicated, and this computer dropped: it is always
	// reached, so saying so adds nothing to the record.
	if found, err := s.SetMachines(dev.ID, []string{"vm2", "busybox", "vm2", LocalMachine, " ", "gpu-1"}); !found || err != nil {
		t.Fatalf("set: found=%v err=%v", found, err)
	}
	devs, _ := s.Devices()
	if got := devs[0].Machines; !slices.Equal(got, []string{"busybox", "gpu-1", "vm2"}) {
		t.Fatalf("machines %v", got)
	}
	for _, m := range []string{LocalMachine, "busybox", "gpu-1", "vm2"} {
		if !devs[0].Reaches(m) {
			t.Errorf("it does not reach %q", m)
		}
	}
	if devs[0].Reaches("vm3") {
		t.Error("it reaches a machine nobody gave it")
	}

	// A name that could not be a machine is refused, and nothing changes.
	for _, bad := range []string{"NOPE!", "has space", "a:b", "../etc", "Busybox"} {
		if _, err := s.SetMachines(dev.ID, []string{"busybox", bad}); err == nil {
			t.Errorf("%q was accepted as a machine", bad)
		}
	}
	devs, _ = s.Devices()
	if got := devs[0].Machines; !slices.Equal(got, []string{"busybox", "gpu-1", "vm2"}) {
		t.Fatalf("a refused name changed the record: %v", got)
	}

	// Cleared back to this computer alone, and a device that isn't there
	// is reported rather than invented.
	if found, err := s.SetMachines(dev.ID, nil); !found || err != nil {
		t.Fatalf("clear: found=%v err=%v", found, err)
	}
	devs, _ = s.Devices()
	if len(devs[0].Machines) != 0 {
		t.Fatalf("machines after clearing: %v", devs[0].Machines)
	}
	if found, _ := s.SetMachines("d_nope", []string{"busybox"}); found {
		t.Error("a device that is not there was found")
	}
	// Only this computer named is the same as none: nothing to record.
	if found, err := s.SetMachines(dev.ID, []string{LocalMachine}); !found || err != nil {
		t.Fatalf("local only: found=%v err=%v", found, err)
	}
	devs, _ = s.Devices()
	if len(devs[0].Machines) != 0 {
		t.Errorf("naming this computer recorded %v", devs[0].Machines)
	}
	// Setting a machine does not touch what the device may do.
	if devs[0].Permission != PermFull {
		t.Errorf("permission %q", devs[0].Permission)
	}
}

// TestPushesOnlyGoWhereTheDeviceReaches: a notification is the one thing
// that reaches a phone nobody is holding, and it names a pane and taps
// through to it. A device that may not see a machine is not told its
// agents are waiting either — the leak would be the agent's name and the
// project it is in, on a lock screen.
func TestPushesOnlyGoWhereTheDeviceReaches(t *testing.T) {
	ps := newPushService(t)
	f := newFixtureWith(t, pushOptions{
		allowed: func(e string) bool { return strings.HasPrefix(e, ps.srv.URL+"/") },
		client:  ps.srv.Client(),
	})
	other := addMachine(t, f, "busybox", true)
	granted, laptopOnly := f.pair(PermView), f.pair(PermView)
	f.grant(granted, "busybox")
	for p, path := range map[*fakePhone]string{granted: "/granted", laptopOnly: "/laptop-only"} {
		status, b := p.do("POST", "/api/push/subscribe", PushSubscription{
			Endpoint: ps.srv.URL + path, Keys: ps.keys(), On: []string{"waiting", "done"}})
		if status != 204 {
			t.Fatalf("subscribe %s: %d %s", path, status, b)
		}
	}
	// The gateway has to have reached busybox and be watching it before
	// anything there is news.
	waitMachine(t, f, granted, "busybox", MachineOnline)
	there := startPaneOn(t, other, "claude", "stty -echo; exec cat")
	ref := composePaneID("busybox", there)

	waiting := func() []pushed {
		var out []pushed
		for _, p := range ps.all() {
			if p.msg.Pane == ref {
				out = append(out, p)
			}
		}
		return out
	}
	// It may take a second for the watcher to pick the machine up, so the
	// report is made until one push lands rather than once.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && len(waiting()) == 0 {
		callOn(t, other, proto.MethodAgentReport, proto.AgentReportParams{ID: there, Agent: "claude",
			Event: "PermissionRequest", Message: "May I?"})
		time.Sleep(100 * time.Millisecond)
		callOn(t, other, proto.MethodAgentReport, proto.AgentReportParams{ID: there, Agent: "claude",
			Event: "UserPromptSubmit"})
		time.Sleep(100 * time.Millisecond)
	}
	got := waiting()
	if len(got) == 0 {
		t.Fatalf("no push about a pane on busybox at all; got %+v", ps.all())
	}
	for _, p := range got {
		if p.path != "/granted" {
			t.Fatalf("a push about busybox reached %s, a device that may not see it: %+v", p.path, p.msg)
		}
	}
	// And the laptop's own agents still reach both: this takes away a
	// machine, not notifications.
	here := f.pane("claude", "stty -echo; exec cat")
	f.call(proto.MethodAgentReport, proto.AgentReportParams{ID: here, Agent: "claude",
		Event: "PermissionRequest", Message: "May I?"}, nil)
	ps.waitFor("waiting", here, "/granted", "/laptop-only")
}
