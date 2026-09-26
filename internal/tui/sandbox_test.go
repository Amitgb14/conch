package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
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
	if mach.state != stateAttention || mach.err != "sandbox stopped" || mach.sandboxState != sandbox.StateStopped || mach.failures != 2 {
		t.Fatalf("state %v err %q sandbox %q failures %d", mach.state, mach.err, mach.sandboxState, mach.failures)
	}
	// On its way somewhere (seen on a real sandbox mid-stop): ask again
	// later, as for any dropped connection, rather than wait on the user
	// for a state that is about to change.
	moving := newMachine("fix", "fix", "daytona:sb1")
	for _, st := range []sandbox.State{sandbox.StateStopping, sandbox.StateStarting, sandbox.StateCreating, "archiving"} {
		err := &remote.SandboxStoppedError{Label: "fix", State: st}
		if cmd := moving.connected(machineConnectedMsg{machine: "fix", err: err}); cmd == nil {
			t.Fatalf("%s: no retry", st)
		}
		if moving.state != stateOffline || moving.sandboxState != "" {
			t.Fatalf("%s: state %v sandbox %q", st, moving.state, moving.sandboxState)
		}
	}
	for _, st := range []sandbox.State{sandbox.StateArchived, sandbox.StateError} {
		if cmd := moving.connected(machineConnectedMsg{err: &remote.SandboxStoppedError{State: st}}); cmd != nil || moving.sandboxState != st {
			t.Fatalf("%s: settled states wait for the user", st)
		}
	}

	// Connecting again after it was started forgets it was stopped.
	mach.attach(&client.Client{Events: make(chan proto.Message)})
	if mach.sandboxState != "" || mach.state != stateOnline {
		t.Fatalf("after attach: %q %v", mach.sandboxState, mach.state)
	}
}

// sbProvider is a sandbox provider for TUI tests: it records what it was
// asked and answers with the errors it is given.
type sbProvider struct {
	mu        sync.Mutex
	calls     []string
	checkErr  error
	opErr     error
	createErr error
	created   sandbox.Sandbox
}

func (p *sbProvider) record(s string) {
	p.mu.Lock()
	p.calls = append(p.calls, s)
	p.mu.Unlock()
}

func (p *sbProvider) called() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return strings.Join(p.calls, ",")
}

func (p *sbProvider) Name() string { return "daytona" }
func (p *sbProvider) Check() error { return p.checkErr }
func (p *sbProvider) Create(_ context.Context, spec sandbox.Spec) (sandbox.Sandbox, error) {
	p.record(fmt.Sprintf("create cpu=%d env=%d", spec.CPU, len(spec.Env)))
	return p.created, p.createErr
}
func (p *sbProvider) Get(context.Context, string) (sandbox.Sandbox, error) {
	return sandbox.Sandbox{}, nil
}
func (p *sbProvider) List(context.Context) ([]sandbox.Sandbox, error) { return nil, nil }
func (p *sbProvider) Start(_ context.Context, id string) (sandbox.Sandbox, error) {
	p.record("start " + id)
	return sandbox.Sandbox{ID: id, State: sandbox.StateStarted}, p.opErr
}
func (p *sbProvider) Stop(_ context.Context, id string) error {
	p.record("stop " + id)
	return p.opErr
}
func (p *sbProvider) Delete(_ context.Context, id string) error {
	p.record("delete " + id)
	return p.opErr
}
func (p *sbProvider) SSHAccess(context.Context, string) (sandbox.Access, error) {
	return sandbox.Access{}, errors.New("no ssh in TUI tests")
}

func useSandboxProvider(t *testing.T, p *sbProvider) {
	t.Helper()
	old := openSandboxProvider
	openSandboxProvider = func(string) (sandbox.Provider, error) { return p, nil }
	t.Cleanup(func() { openSandboxProvider = old })
}

// sandboxModel is a model with a saved sandbox machine and an ssh one.
func sandboxModel(t *testing.T) (*Model, *machine) {
	t.Helper()
	a2Isolate(t)
	t.Setenv("DAYTONA_API_KEY", "")
	m := a2Model()
	saved, _ := remote.SaveMachine(remote.Machine{Label: "fix", Target: "daytona:sb1"})
	remote.SaveMachine(remote.Machine{Label: "gpu", Target: "dev@gpu"})
	m.syncCatalog()
	m.rebuild()
	return m, m.machine(saved.ID)
}

func TestSandboxRowAndMenu(t *testing.T) {
	m, mach := sandboxModel(t)
	r := row{id: machineID(mach.id), kind: kindMachine, machine: mach.id}
	right := func() string { _, _, _, _, rt := m.rowParts(r); return ansi.Strip(rt) }
	menu := func() string { return a2MenuLabels(newRowMenu(*m, r, 0, 0)) }

	// Stopped: its state, and Start instead of an install.
	mach.state, mach.err, mach.sandboxState = stateAttention, "sandbox stopped", sandbox.StateStopped
	if right() != "stopped" {
		t.Fatalf("row %q", right())
	}
	if got := menu(); !strings.Contains(got, "s Start sandbox") || strings.Contains(got, "Install") ||
		strings.Contains(got, "Stop sandbox") || !strings.Contains(got, "D Delete sandbox…") {
		t.Fatalf("stopped menu: %s", got)
	}
	page := ansi.Strip(strings.Join(m.machineLines(mach, 100, 30), "\n"))
	if !strings.Contains(page, "sandbox stopped: its files are kept") || !strings.Contains(page, "m → Start sandbox") {
		t.Fatalf("stopped page:\n%s", page)
	}

	// Running: Stop and Delete, and removing it says the sandbox stays.
	mach.state, mach.err, mach.sandboxState = stateOnline, "", ""
	got := menu()
	for _, want := range []string{"S Stop sandbox…", "D Delete sandbox…", "x Remove machine (the sandbox keeps running)"} {
		if !strings.Contains(got, want) {
			t.Fatalf("online menu lacks %q: %s", want, got)
		}
	}
	if strings.Contains(got, "Start sandbox") {
		t.Fatalf("online menu: %s", got)
	}

	// Busy: nothing to choose until it is done.
	mach.busy = "stopping"
	if right() != "stopping" || menu() != " stopping…" {
		t.Fatalf("busy: row %q menu %q", right(), menu())
	}
	page = ansi.Strip(strings.Join(m.machineLines(mach, 100, 30), "\n"))
	if !strings.Contains(page, "stopping the sandbox…") {
		t.Fatalf("busy page:\n%s", page)
	}
	for _, w := range []int{1, 20, 39} {
		for _, l := range strings.Split(a1Sized(t, m, w, 8).View(), "\n") {
			if lw := ansi.StringWidth(l); lw > w {
				t.Fatalf("%d cols: line %d wide", w, lw)
			}
		}
	}

	// An ssh machine offers none of it.
	gpu := row{kind: kindMachine, machine: "gpu"}
	if got := a2MenuLabels(newRowMenu(*m, gpu, 0, 0)); strings.Contains(got, "sandbox") {
		t.Fatalf("ssh machine menu: %s", got)
	}
}

func TestSandboxStartStopDelete(t *testing.T) {
	m, mach := sandboxModel(t)
	p := &sbProvider{}
	useSandboxProvider(t, p)
	run := func(cmd tea.Cmd) sandboxDoneMsg {
		t.Helper()
		for _, msg := range a2Run(cmd) {
			if done, ok := msg.(sandboxDoneMsg); ok {
				return done
			}
		}
		t.Fatal("no sandboxDoneMsg")
		return sandboxDoneMsg{}
	}

	// Start: busy while it runs, then connects with installing allowed.
	mach.state, mach.sandboxState = stateAttention, sandbox.StateStopped
	cmd := m.sandboxOp(mach.id, "start")
	if mach.busy != "starting" || m.sandboxOp(mach.id, "stop") != nil {
		t.Fatalf("busy %q, or a second operation was let through", mach.busy)
	}
	done := run(cmd)
	if done.err != nil || p.called() != "start sb1" {
		t.Fatalf("start: %v %s", done.err, p.called())
	}
	if next := m.sandboxDone(done); next == nil || mach.busy != "" || mach.state != stateConnecting {
		t.Fatalf("after start: busy %q state %v", mach.busy, mach.state)
	}

	// Stop: the machine shows stopped at once rather than as lost.
	mach.state = stateOnline
	m.confirmStopSandbox(mach.id)
	d, ok := m.overlay.(*dialog)
	if !ok || !strings.Contains(strings.Join(d.text, " "), "Stop fix?") {
		t.Fatalf("stop confirm: %#v", m.overlay)
	}
	done = run(d.submit(m, nil))
	m.sandboxDone(done)
	if mach.state != stateAttention || mach.sandboxState != sandbox.StateStopped || mach.c != nil || !strings.Contains(m.flash, "stopped fix") {
		t.Fatalf("after stop: %v %q %q", mach.state, mach.sandboxState, m.flash)
	}

	// A failure says so and leaves the machine as it was.
	p.opErr = errors.New("daytona: busy (409)")
	done = run(m.sandboxOp(mach.id, "start"))
	m.sandboxDone(done)
	if mach.busy != "" || mach.sandboxState != sandbox.StateStopped || !strings.Contains(m.flash, "start fix failed: daytona: busy (409)") {
		t.Fatalf("failed start: %q %q", mach.busy, m.flash)
	}

	// Delete: gone from Daytona and from the catalog; already gone counts.
	p.opErr = sandbox.ErrNotFound
	m.confirmDeleteSandbox(mach.id)
	d = m.overlay.(*dialog)
	done = run(d.submit(m, nil))
	if done.err != nil {
		t.Fatalf("delete: %v", done.err)
	}
	m.sandboxDone(done)
	if m.machine(mach.id) != nil {
		t.Fatal("still in the tree")
	}
	if ms, _ := remote.Machines(); len(ms) != 1 || ms[0].ID != "gpu" {
		t.Fatalf("catalog %+v", ms)
	}

	// Without a key nothing is asked of the provider.
	m2, mach2 := sandboxModel(t)
	p2 := &sbProvider{checkErr: sandbox.ErrNotConfigured}
	useSandboxProvider(t, p2)
	done = run(m2.sandboxOp(mach2.id, "stop"))
	if !errors.Is(done.err, sandbox.ErrNotConfigured) || p2.called() != "" {
		t.Fatalf("not configured: %v %s", done.err, p2.called())
	}
	// Not a sandbox, or gone meanwhile: nothing happens.
	if m2.sandboxOp("gpu", "stop") != nil || m2.sandboxOp("nope", "stop") != nil {
		t.Fatal("ran on a machine that isn't a sandbox")
	}
	if m2.sandboxDone(sandboxDoneMsg{machine: "nope", op: "stop"}) != nil {
		t.Fatal("done for a machine that is gone")
	}
}

func TestSandboxConfirmsSayWhatIsLost(t *testing.T) {
	m, mach := sandboxModel(t)
	mach.state = stateOnline
	mach.panes = []proto.PaneInfo{{ID: "p1", Agent: &proto.AgentStatus{State: proto.AgentWorking}}, {ID: "p2", Agent: &proto.AgentStatus{State: proto.AgentWorking}}}
	mach.projects = []proto.ProjectInfo{{Name: "api", Branches: []proto.BranchInfo{{Name: "feat", BaseAhead: 3}}}}

	m.confirmStopSandbox(mach.id)
	if text := strings.Join(m.overlay.(*dialog).text, " "); !strings.Contains(text, "2 agents there are still working") {
		t.Fatalf("stop: %s", text)
	}
	m.confirmDeleteSandbox(mach.id)
	if text := strings.Join(m.overlay.(*dialog).text, "\n"); !strings.Contains(text, "Delete fix (sandbox sb1)") || !strings.Contains(text, "api feat: 3 commits on no remote") {
		t.Fatalf("delete: %s", text)
	}
	// Offline with nothing seen: it says it can't tell, not that it's safe.
	mach.state, mach.projects = stateOffline, nil
	m.confirmDeleteSandbox(mach.id)
	if text := strings.Join(m.overlay.(*dialog).text, " "); !strings.Contains(text, "can't tell whether work there is pushed") {
		t.Fatalf("offline delete: %s", text)
	}
	// Online with everything pushed: just the question.
	mach.state = stateOnline
	m.confirmDeleteSandbox(mach.id)
	if n := len(m.overlay.(*dialog).text); n != 1 {
		t.Fatalf("clean delete has %d lines", n)
	}
	m.confirmStopSandbox("nope")
	m.confirmDeleteSandbox("nope")
}

func TestSandboxAddMenuAndDialog(t *testing.T) {
	m, _ := sandboxModel(t)
	m.cursor = machineID(localMachine)
	next, _ := m.handleKey(a2Key("M"))
	*m = next.(Model)
	mu, ok := m.overlay.(*menu)
	if !ok || a2MenuLabels(mu) != "s Over ssh… | d New Daytona sandbox…" {
		t.Fatalf("M: %#v", m.overlay)
	}
	mu.items[0].run(m)
	if d, ok := m.overlay.(*dialog); !ok || !strings.Contains(d.title, "Add machine") {
		t.Fatalf("ssh: %#v", m.overlay)
	}

	// With no key the dialog says so before anything is spent.
	mu.items[1].run(m)
	d, ok := m.overlay.(*dialog)
	if !ok || !strings.Contains(d.title, "New Daytona sandbox") || !strings.Contains(strings.Join(d.text, " "), "Needs a Daytona API key: set $DAYTONA_API_KEY") {
		t.Fatalf("sandbox dialog: %#v", m.overlay)
	}

	p := &sbProvider{created: sandbox.Sandbox{ID: "sbnew-0123456789", State: sandbox.StateStarted}}
	useSandboxProvider(t, p)
	d = newSandboxDialog(*m)
	if strings.Contains(strings.Join(d.text, " "), "Needs") {
		t.Fatalf("configured, yet: %v", d.text)
	}
	errOf := func(cmd tea.Cmd) string { return a2ErrText(a2Run(cmd)) }
	if got := errOf(d.submit(m, []string{"", "", "two", "", "", ""})); !strings.Contains(got, "vCPUs: give a whole number") {
		t.Fatalf("bad cpu: %q", got)
	}
	if got := errOf(d.submit(m, []string{"", "", "", "-1", "", ""})); !strings.Contains(got, "Memory: give a whole number") {
		t.Fatalf("negative memory: %q", got)
	}
	t.Setenv("SB_UNSET", "")
	if got := errOf(d.submit(m, []string{"", "", "", "", "", "SB_UNSET"})); !strings.Contains(got, "$SB_UNSET isn't set here") {
		t.Fatalf("unset env: %q", got)
	}
	if p.called() != "" {
		t.Fatalf("created before the form was right: %s", p.called())
	}

	// A good form creates it, sets conch up and adds the machine.
	var setUp []string
	old := setUpSandboxFn
	setUpSandboxFn = func(_ context.Context, mm remote.Machine, _ func(string)) (*client.Client, error) {
		setUp = append(setUp, mm.Label+" "+mm.Target)
		c, _ := a1FakeClient(t)
		return c, nil
	}
	t.Cleanup(func() { setUpSandboxFn = old })
	t.Setenv("SB_TOKEN", "v")
	msgs := a2Run(d.submit(m, []string{" dt ", "", "2", "", "", "SB_TOKEN"}))
	added, ok := msgs[0].(machineAddedMsg)
	if !ok || added.m.Label != "dt" || added.m.Target != "daytona:sbnew-0123456789" || !strings.Contains(added.note, "runs until stopped") {
		t.Fatalf("msgs %#v", msgs)
	}
	if p.called() != "create cpu=2 env=1" || strings.Join(setUp, ",") != "dt daytona:sbnew-0123456789" {
		t.Fatalf("calls %s, set up %v", p.called(), setUp)
	}
	if ms, _ := remote.Machines(); len(ms) != 3 {
		t.Fatalf("catalog %+v", ms)
	}
	if !strings.Contains(m.flash, "creating a Daytona sandbox") {
		t.Fatalf("flash %q", m.flash)
	}
}

func TestSandboxCreateFailures(t *testing.T) {
	a2Isolate(t)
	old := setUpSandboxFn
	t.Cleanup(func() { setUpSandboxFn = old })

	// Setting conch up fails: the sandbox is deleted, not left costing.
	p := &sbProvider{created: sandbox.Sandbox{ID: "sb9"}}
	useSandboxProvider(t, p)
	setUpSandboxFn = func(context.Context, remote.Machine, func(string)) (*client.Client, error) {
		return nil, errors.New("probe: exit 255")
	}
	got := a2ErrText(a2Run(createSandbox("daytona", sandbox.Spec{}, "")))
	if !strings.Contains(got, "setting up sandbox sb9 failed, so it was deleted: probe: exit 255") || p.called() != "create cpu=0 env=0,delete sb9" {
		t.Fatalf("%q %s", got, p.called())
	}
	// And if deleting fails too, it says how to clean up.
	p.opErr = errors.New("daytona: down (503)")
	got = a2ErrText(a2Run(createSandbox("daytona", sandbox.Spec{}, "")))
	if !strings.Contains(got, "deleting it failed too (daytona: down (503)): conch sandbox rm sb9") {
		t.Fatalf("%q", got)
	}
	// A build that fails still leaves an ID to delete.
	p2 := &sbProvider{created: sandbox.Sandbox{ID: "sb10"}, createErr: errors.New("sandbox sb10 is build_failed")}
	useSandboxProvider(t, p2)
	if got := a2ErrText(a2Run(createSandbox("daytona", sandbox.Spec{}, ""))); !strings.Contains(got, "so it was deleted") || p2.called() != "create cpu=0 env=0,delete sb10" {
		t.Fatalf("%q %s", got, p2.called())
	}
	// Refused outright: nothing to delete.
	p3 := &sbProvider{createErr: errors.New("daytona: quota (403)")}
	useSandboxProvider(t, p3)
	if got := a2ErrText(a2Run(createSandbox("daytona", sandbox.Spec{}, ""))); got != "create sandbox: daytona: quota (403)" || p3.called() != "create cpu=0 env=0" {
		t.Fatalf("%q %s", got, p3.called())
	}
	// No key: not even a create.
	p4 := &sbProvider{checkErr: sandbox.ErrNotConfigured}
	useSandboxProvider(t, p4)
	if got := a2ErrText(a2Run(createSandbox("daytona", sandbox.Spec{}, ""))); !strings.Contains(got, "not configured") || p4.called() != "" {
		t.Fatalf("%q %s", got, p4.called())
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
