package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/config"
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
	mu         sync.Mutex
	calls      []string
	checkErr   error
	opErr      error
	createErr  error
	created    sandbox.Sandbox
	spec       sandbox.Spec // what the last create asked for
	preview    string       // the link PreviewURL answers with
	previewErr error
	usage      sandbox.Usage
}

func (p *sbProvider) Usage(_ context.Context, id string, _, _ time.Time) (sandbox.Usage, error) {
	p.record("usage " + id)
	return p.usage, nil
}

func (p *sbProvider) PreviewURL(_ context.Context, id string, port int, _ time.Duration) (string, error) {
	p.record(fmt.Sprintf("preview %s %d", id, port))
	return p.preview, p.previewErr
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
	p.spec = spec
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
	if !ok || a2MenuLabels(mu) != "s Over ssh… | b New sandbox…" {
		t.Fatalf("M: %#v", m.overlay)
	}
	mu.items[0].run(m)
	if d, ok := m.overlay.(*dialog); !ok || !strings.Contains(d.title, "Add machine") {
		t.Fatalf("ssh: %#v", m.overlay)
	}

	// The providers are a level down, listed from the registry, and esc
	// goes back to the menu they were opened from.
	m.overlay = mu
	mu.items[1].run(m)
	sub, ok := m.overlay.(*menu)
	if !ok || a2MenuLabels(sub) != "d Daytona… | b boat.dev…" || sub.title != "New sandbox" {
		t.Fatalf("sandbox menu: %#v", m.overlay)
	}
	if _, _ = sub.update(m, a2Key("esc")); m.overlay == nil {
		t.Fatal("esc in the sandbox menu closed everything")
	}
	if back, ok := m.overlay.(*menu); !ok || back.title != "Add a machine" {
		t.Fatalf("esc went to %#v", m.overlay)
	}
	// A provider conch grows appears without touching the menu.
	providers := sandbox.Providers
	t.Cleanup(func() { sandbox.Providers = providers })
	sandbox.Providers = []string{"daytona", "fly"}
	if got := a2MenuLabels(newSandboxMenu(nil)); got != "d Daytona… | f Fly…" {
		t.Fatalf("another provider: %q", got)
	}
	sandbox.Providers = []string{"daytona", "dune"} // a letter already taken
	if got := a2MenuLabels(newSandboxMenu(nil)); got != "d Daytona… |  Dune…" {
		t.Fatalf("a shared letter: %q", got)
	}
	sandbox.Providers = nil
	if got := a2MenuLabels(newSandboxMenu(nil)); got != " conch knows no sandbox providers" {
		t.Fatalf("no providers: %q", got)
	}
	sandbox.Providers = providers

	// With no key the dialog says so before anything is spent.
	sub = newSandboxMenu(nil)
	sub.items[0].run(m)
	d, ok := m.overlay.(*dialog)
	if !ok || !strings.Contains(d.title, "New Daytona sandbox") || !strings.Contains(strings.Join(d.text, " "), "Needs a Daytona API key: set $DAYTONA_API_KEY") {
		t.Fatalf("sandbox dialog: %#v", m.overlay)
	}

	p := &sbProvider{created: sandbox.Sandbox{ID: "sbnew-0123456789", State: sandbox.StateStarted}}
	useSandboxProvider(t, p)
	d = newSandboxDialog(*m, "daytona")
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
	// Refused outright: nothing to delete, and the provider's own words —
	// its Create already says what failed, so conch does not repeat it.
	p3 := &sbProvider{createErr: errors.New("create sandbox: daytona: quota (403)")}
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

// Sandboxes are grouped in the tree: Sandboxes → provider → the machines
// conch made, so a fleet of them doesn't fill its top. Ordinary machines
// stay where they are.
func TestSandboxTreeGrouping(t *testing.T) {
	a2Isolate(t)
	t.Setenv("DAYTONA_API_KEY", "")
	// A second provider, to prove the grouping is not Daytona's alone.
	providers := sandbox.Providers
	t.Cleanup(func() { sandbox.Providers = providers })
	sandbox.Providers = []string{"daytona", "e2b"}
	m := a2Model()
	for _, mm := range []remote.Machine{
		{Label: "sdx01", Target: "daytona:sb-aaa"},
		{Label: "sdx02", Target: "daytona:sb-bbb"},
		{Label: "sdx01", Target: "e2b:sb-ccc"}, // the same label under another provider
		{Label: "gpu", Target: "dev@gpu"},
	} {
		if _, err := remote.SaveMachine(mm); err != nil {
			t.Fatal(err)
		}
	}
	m.syncCatalog()
	m.rebuild()

	shape := func() string {
		var b strings.Builder
		for _, r := range m.rows {
			label := r.id
			if r.kind == kindMachine {
				if mach := m.machine(r.machine); mach != nil {
					label = mach.label
				}
			}
			b.WriteString(fmt.Sprintf("%d %s\n", r.depth, label))
		}
		return b.String()
	}
	// Collapsed machines keep the shape readable.
	for _, r := range m.rows {
		if r.kind == kindMachine {
			m.expanded[r.id] = false
		}
	}
	m.rebuild()
	want := "0 local\n0 gpu\n0 sandboxes\n1 sandboxes/daytona\n2 sdx01\n2 sdx02\n1 sandboxes/e2b\n2 sdx01\n"
	if got := shape(); got != want {
		t.Fatalf("tree:\n%s\nwant:\n%s", got, want)
	}

	// The group rows say how many, and read as their provider.
	byID := map[string]row{}
	for _, r := range m.rows {
		byID[r.id] = r
	}
	if g := byID[sandboxesID()]; g.count != 3 || g.kind != kindSandboxes {
		t.Fatalf("Sandboxes row: %+v", g)
	}
	if p := byID[sandboxProviderID("daytona")]; p.count != 2 || p.branch != "daytona" {
		t.Fatalf("provider row: %+v", p)
	}
	_, _, label, _, right := m.rowParts(byID[sandboxProviderID("daytona")])
	if ansi.Strip(label) != "Daytona" || ansi.Strip(right) != "2" {
		t.Fatalf("provider row reads %q %q", label, right)
	}
	_, _, label, _, right = m.rowParts(byID[sandboxesID()])
	if ansi.Strip(label) != "Sandboxes" || ansi.Strip(right) != "3" {
		t.Fatalf("Sandboxes row reads %q %q", label, right)
	}

	// Collapsing a provider hides only its own.
	m.expanded[sandboxProviderID("daytona")] = false
	m.rebuild()
	if got := shape(); strings.Contains(got, "2 sdx02") || !strings.Contains(got, "1 sandboxes/e2b") {
		t.Fatalf("collapsed daytona:\n%s", got)
	}
	m.expanded[sandboxesID()] = false
	m.rebuild()
	if got := shape(); strings.Contains(got, "sandboxes/") {
		t.Fatalf("collapsed sandboxes:\n%s", got)
	}

	// The page lists what is in the group, with a word about cost.
	m.expanded[sandboxesID()] = true
	m.rebuild()
	page := a2Plain(m.sandboxesLines("", 80))
	for _, want := range []string{"Sandboxes  3", "sdx01", "sdx02", "Daytona sb-aaa", "E2B sb-ccc", "one that runs, costs"} {
		if !strings.Contains(page, want) {
			t.Fatalf("the page lacks %q:\n%s", want, page)
		}
	}
	if one := a2Plain(m.sandboxesLines("e2b", 80)); !strings.Contains(one, "E2B sandboxes  1") || strings.Contains(one, "sb-aaa") {
		t.Fatalf("one provider's page:\n%s", one)
	}
	// With none, the group is gone from the tree and the page says how to
	// make one.
	for _, mach := range m.machines {
		if _, _, ok := remote.ParseSandboxTarget(mach.target); ok {
			remote.RemoveMachine(mach.id)
		}
	}
	m.syncCatalog()
	m.rebuild()
	if strings.Contains(shape(), "sandboxes") {
		t.Fatalf("an empty group is still there:\n%s", shape())
	}
	if page := a2Plain(m.sandboxesLines("", 80)); !strings.Contains(page, "none yet · M → New sandbox…") {
		t.Fatalf("empty page:\n%s", page)
	}
}

// A sandbox nobody is using is stopped by conch itself, since a provider's
// own timer can't tell an agent at work from an empty machine.
func TestSandboxIdleStop(t *testing.T) {
	m, mach := sandboxModel(t)
	p := &sbProvider{}
	useSandboxProvider(t, p)
	now := time.Now()
	mach.state, mach.since = stateOnline, now.Add(-2*time.Hour)
	pane := func(state string, last time.Time) proto.PaneInfo {
		info := proto.PaneInfo{ID: "p1", State: proto.PaneRunning, LastActive: last}
		if state != "" {
			info.Agent = &proto.AgentStatus{Name: "claude", State: state}
		}
		return info
	}
	idle := func(panes ...proto.PaneInfo) time.Duration {
		mach.panes = panes
		return now.Sub(m.idleSince(mach, now))
	}

	// An agent working, or waiting for an answer, is not idle at all.
	for _, state := range []string{proto.AgentWorking, proto.AgentBlocked} {
		if d := idle(pane(state, now.Add(-time.Hour))); d != 0 {
			t.Fatalf("%s counts as idle after %v", state, d)
		}
	}
	// Otherwise it is however long since anything printed.
	if d := idle(pane(proto.AgentIdle, now.Add(-10*time.Minute))); d.Round(time.Minute) != 10*time.Minute {
		t.Fatalf("idle %v", d)
	}
	// The newest pane wins, and one that has exited says nothing.
	busy := proto.PaneInfo{ID: "p2", State: proto.PaneRunning, LastActive: now.Add(-time.Minute)}
	gone := proto.PaneInfo{ID: "p3", State: proto.PaneExited, LastActive: now}
	if d := idle(pane(proto.AgentIdle, now.Add(-time.Hour)), busy, gone); d.Round(time.Minute) != time.Minute {
		t.Fatalf("newest pane: %v", d)
	}
	// With no panes at all it counts from when conch connected.
	if d := idle(); d.Round(time.Minute) != 2*time.Hour {
		t.Fatalf("no panes: %v", d)
	}

	// Below the limit nothing happens; past it the sandbox is stopped.
	mach.panes = []proto.PaneInfo{pane(proto.AgentIdle, now.Add(-20*time.Minute))}
	if cmd := m.watchIdleSandboxes(now); cmd != nil {
		t.Fatal("stopped one that was only 20 minutes idle, with the default of 30")
	}
	mach.panes = []proto.PaneInfo{pane(proto.AgentIdle, now.Add(-31*time.Minute))}
	cmd := m.watchIdleSandboxes(now)
	if cmd == nil || !strings.Contains(m.flash, "idle for 30m") || !strings.Contains(m.flash, "files are kept") {
		t.Fatalf("31 minutes idle: %q", m.flash)
	}
	a2Run(cmd)
	if !strings.Contains(p.called(), "stop sb1") {
		t.Fatalf("stopped: %q", p.called())
	}

	// Turned off, it is left alone however long it sits; a shorter limit
	// is taken as it is.
	mach.busy = ""
	never := 0
	m.cfg.Sandbox.Set("daytona", config.ProviderCfg{IdleStop: &never})
	if cmd := m.watchIdleSandboxes(now); cmd != nil {
		t.Fatal("stopped one when the setting says never")
	}
	ten := 10
	m.cfg.Sandbox.Set("daytona", config.ProviderCfg{IdleStop: &ten})
	if cmd := m.watchIdleSandboxes(now); cmd == nil {
		t.Fatal("a shorter limit was not used")
	}

	// A machine that is offline, already busy, or not a sandbox at all is
	// left to itself.
	mach.busy = "stopping"
	if cmd := m.watchIdleSandboxes(now); cmd != nil {
		t.Fatal("stopped one that was already stopping")
	}
	mach.busy, mach.state = "", stateOffline
	if cmd := m.watchIdleSandboxes(now); cmd != nil {
		t.Fatal("stopped one that was offline")
	}
	if ssh := m.machine("gpu"); ssh != nil {
		ssh.state, ssh.since, ssh.panes = stateOnline, now.Add(-10*time.Hour), nil
		if cmd := m.watchIdleSandboxes(now); cmd != nil {
			t.Fatal("stopped an ordinary machine")
		}
	}
}

// Opening a port inside a sandbox: the menu asks which, the provider is
// asked for a link, and the link is opened and kept on the clipboard.
func TestSandboxOpenPort(t *testing.T) {
	m, mach := sandboxModel(t)
	p := &sbProvider{preview: "https://3000-tok.proxy.daytona.work"}
	useSandboxProvider(t, p)
	// Nothing reaches the network in a test: the link is taken to answer.
	old := checkPreview
	checkPreview = func(context.Context, string) (int, string) { return 200, "" }
	t.Cleanup(func() { checkPreview = old })
	mach.state = stateOnline
	m.rebuild()

	// It is in the machine's menu while the sandbox is running.
	r := row{id: machineID(mach.id), kind: kindMachine, machine: mach.id}
	if labels := a2MenuLabels(newRowMenu(*m, r, 0, 0)); !strings.Contains(labels, "o Open a port in the browser…") {
		t.Fatalf("menu: %s", labels)
	}

	cmd := m.openSandboxPort(mach.id)
	d, ok := m.overlay.(*dialog)
	if !ok || !strings.Contains(strings.Join(d.text, " "), "Anyone with the link") {
		t.Fatalf("dialog: %#v", m.overlay)
	}
	if got := d.fields[0].in.Value(); got != "3000" {
		t.Fatalf("the port offered is %q", got)
	}
	a2Run(cmd)

	// A port that isn't one is refused before anything is asked.
	for _, bad := range []string{"", "http", "0", "70000"} {
		if msgs := a2Run(d.submit(m, []string{bad})); !strings.Contains(a2ErrText(msgs), "between 1 and 65535") {
			t.Fatalf("port %q: %v", bad, msgs)
		}
	}
	if p.called() != "" {
		t.Fatalf("a bad port still asked the provider: %q", p.called())
	}

	// A good one asks for a link and remembers the port.
	msgs := a2Run(d.submit(m, []string{" 8080 "}))
	done, ok := msgs[0].(previewDoneMsg)
	if !ok || done.url != "https://3000-tok.proxy.daytona.work" || done.err != nil {
		t.Fatalf("preview: %#v", msgs)
	}
	if !strings.Contains(p.called(), "preview sb1 8080") {
		t.Fatalf("provider asked: %q", p.called())
	}
	if m.machine(mach.id).lastPort != "8080" {
		t.Fatalf("the port was not kept: %q", m.machine(mach.id).lastPort)
	}
	// The link is opened and copied, and said out loud.
	if cmd := m.receivePreview(done); cmd == nil {
		t.Fatal("nothing opened")
	}
	if !strings.Contains(m.flash, done.url) || !strings.Contains(m.flash, "copied") {
		t.Fatalf("flash %q", m.flash)
	}
	// A provider that says no is reported, not swallowed.
	m.receivePreview(previewDoneMsg{machine: mach.id, err: errString("port 3000 is daytona's own (web terminal)")})
	if !strings.Contains(m.flash, "daytona's own") {
		t.Fatalf("error flash %q", m.flash)
	}
	// A stopped sandbox has no ports to open.
	mach.state = stateAttention
	if labels := a2MenuLabels(newRowMenu(*m, r, 0, 0)); strings.Contains(labels, "Open a port") {
		t.Fatalf("offered on a stopped sandbox: %s", labels)
	}
}

// What a sandbox has run up: how long it has been going, and what that
// has cost where prices are set.
func TestSandboxSpend(t *testing.T) {
	m, mach := sandboxModel(t)
	now := time.Now()
	mach.state = stateOnline

	// Nothing is known until a provider has been asked.
	if got := m.sandboxSpend(mach, now); got != "" {
		t.Fatalf("before the first look: %q", got)
	}
	m.receiveSandboxList(sandboxListMsg{provider: "daytona", boxes: []sandbox.Sandbox{
		{ID: "sb1", State: sandbox.StateStarted, CPU: 2, Memory: 4, Disk: 10, Created: now.Add(-4*time.Hour - 12*time.Minute)},
		{ID: "elsewhere", State: sandbox.StateStarted, Created: now},
	}})
	if mach.box == nil || mach.box.CPU != 2 {
		t.Fatalf("what the provider said: %+v", mach.box)
	}
	// Running time alone, until prices are set.
	if got := m.sandboxSpend(mach, now); got != "4h 12m" {
		t.Fatalf("spend %q", got)
	}
	// With prices, what it has cost. 2 vCPU at $0.05 and 4 GiB at $0.01
	// is $0.14 an hour, so 4.2 hours is about $0.59.
	m.cfg.Sandbox.Set("daytona", config.ProviderCfg{PriceCPUHour: 0.05, PriceGiBHour: 0.01, PriceDiskGiBHour: 0.0001})
	// "~" says the figure is conch's arithmetic, not the provider's bill.
	got := m.sandboxSpend(mach, now)
	if !strings.HasPrefix(got, "4h 12m · ~$0.5") {
		t.Fatalf("with prices: %q", got)
	}
	// What the provider itself says wins, and loses the tilde.
	mach.usage = &sandbox.Usage{Known: true, Cost: 0.03}
	if got := m.sandboxSpend(mach, now); got != "4h 12m · $0.030" {
		t.Fatalf("with the provider's own figure: %q", got)
	}
	if c, told, own := m.sandboxCost(mach, now); !told || !own || c != 0.03 {
		t.Fatalf("cost %v told=%v own=%v", c, told, own)
	}
	// One that says nothing yet — its billing lags — leaves the estimate.
	mach.usage = &sandbox.Usage{Known: false}
	if got := m.sandboxSpend(mach, now); !strings.HasPrefix(got, "4h 12m · ~$0.5") {
		t.Fatalf("with nothing said yet: %q", got)
	}
	mach.usage = nil
	// A stopped sandbox still keeps its disk, and the provider still
	// charges for it: that shows as a rate, since conch is not told when
	// it stopped and a total it cannot know would be worse.
	mach.box.State = sandbox.StateStopped
	if got := m.sandboxSpend(mach, now); got != "disk $0.02/day" {
		t.Fatalf("stopped: %q", got)
	}
	if rate := m.sandboxStoppedRate(mach); rate != 10*0.0001 {
		t.Fatalf("stopped rate %v", rate)
	}
	// With no price for disk there is nothing to say.
	m.cfg.Sandbox.Set("daytona", config.ProviderCfg{PriceCPUHour: 0.05})
	if got := m.sandboxSpend(mach, now); got != "" {
		t.Fatalf("stopped with no disk price: %q", got)
	}
	m.cfg.Sandbox.Set("daytona", config.ProviderCfg{PriceCPUHour: 0.05, PriceGiBHour: 0.01, PriceDiskGiBHour: 0.0001})
	// A running one is charged the whole rate, not the disk alone.
	mach.box.State = sandbox.StateStarted
	if rate := m.sandboxStoppedRate(mach); rate != 0 {
		t.Fatalf("a running sandbox charged as stopped: %v", rate)
	}
	mach.box.State = sandbox.StateStopped
	// One that has only just started reads in words rather than 0m.
	mach.box.State, mach.box.Created = sandbox.StateStarted, now.Add(-10*time.Second)
	if got := m.sandboxSpend(mach, now); !strings.HasPrefix(got, "just now") {
		t.Fatalf("just started: %q", got)
	}
	// An ordinary machine has none of this.
	if ssh := m.machine("gpu"); ssh != nil {
		if got := m.sandboxSpend(ssh, now); got != "" {
			t.Fatalf("an ssh machine: %q", got)
		}
	}

	// How long things read.
	for _, c := range []struct {
		d    time.Duration
		want string
	}{{30 * time.Second, "just now"}, {5 * time.Minute, "5m"}, {time.Hour + 2*time.Minute, "1h 02m"},
		{26 * time.Hour, "1d 2h"}, {49 * time.Hour, "2d 1h"}} {
		if got := shortDuration(c.d); got != c.want {
			t.Fatalf("%v reads %q, want %q", c.d, got, c.want)
		}
	}
	// A rate reads in whatever unit says something: a rounding to
	// $0.000/h would tell nobody anything.
	for _, c := range []struct {
		perHour float64
		want    string
	}{
		{0, ""}, {0.0666, "$0.07/h"}, {0.01, "$0.01/h"},
		{0.0005, "$0.01/day"},
		{0.000324, "$0.23/month"},  // 3 GiB of stopped disk on Daytona
		{0.0000108, "$0.01/month"}, // a tenth of a GiB
	} {
		if got := rate(c.perHour); got != c.want {
			t.Fatalf("%v an hour reads %q, want %q", c.perHour, got, c.want)
		}
	}

	// And money, finer while it is small.
	for _, c := range []struct {
		v    float64
		want string
	}{{0.004, "$0.004"}, {0.5, "$0.50"}, {12.345, "$12.35"}} {
		if got := money(c.v); got != c.want {
			t.Fatalf("%v reads %q, want %q", c.v, got, c.want)
		}
	}

	// The provider is asked at most every couple of minutes, and not at
	// all when there is no sandbox to ask about.
	m.boxesAsked = time.Time{}
	if cmd := m.pollSandboxes(now); cmd == nil {
		t.Fatal("the first look asked nothing")
	}
	if cmd := m.pollSandboxes(now.Add(time.Minute)); cmd != nil {
		t.Fatal("asked again a minute later")
	}
	if cmd := m.pollSandboxes(now.Add(sandboxPollEvery + time.Second)); cmd == nil {
		t.Fatal("never asked again")
	}
	only := &Model{cfg: m.cfg, machines: []*machine{newMachine("gpu", "gpu", "dev@gpu")}}
	if cmd := only.pollSandboxes(now); cmd != nil {
		t.Fatal("asked with no sandboxes at all")
	}
	// An error from the provider is kept quiet: every action says for
	// itself when it can't be reached.
	before := mach.box
	m.receiveSandboxList(sandboxListMsg{provider: "daytona", err: errString("no")})
	if mach.box != before {
		t.Fatal("a failed look threw away what was known")
	}
}

// A link to a port nothing answers on opens a proxy error page, which
// says nothing useful. conch asks first and says what usually mends it,
// while keeping the link.
func TestSandboxPreviewTrouble(t *testing.T) {
	m, mach := sandboxModel(t)
	p := &sbProvider{preview: "https://3000-x.daytonaproxy01.net"}
	useSandboxProvider(t, p)
	mach.state = stateOnline

	answers := func(code int, body string) {
		old := checkPreview
		checkPreview = func(context.Context, string) (int, string) { return code, body }
		t.Cleanup(func() { checkPreview = old })
	}
	// What Daytona's proxy says when the sandbox has no server on that port.
	answers(502, `{"statusCode":502,"message":"proxy upstream error","source":"DAYTONA_DAEMON"}`)
	why := previewTrouble(context.Background(), "https://3000-x.daytonaproxy01.net", "sdx1", 3000)
	for _, want := range []string{"nothing is answering on port 3000 in sdx1", "found no server there",
		"0.0.0.0 rather than 127.0.0.1", "vite --host"} {
		if !strings.Contains(why, want) {
			t.Fatalf("the reason lacks %q: %s", want, why)
		}
	}
	// A page that answers, however it answers, is the program's business.
	for _, code := range []int{200, 302, 404, 500} {
		answers(code, "whatever")
		if why := previewTrouble(context.Background(), "u", "sdx1", 3000); why != "" {
			t.Fatalf("%d: %q", code, why)
		}
	}
	// A link that cannot be reached at all says so.
	answers(0, "dial tcp: no route to host")
	if why := previewTrouble(context.Background(), "u", "sdx1", 3000); !strings.Contains(why, "no route to host") {
		t.Fatalf("unreachable: %q", why)
	}

	// End to end: the dialog's submit reports the trouble and keeps the
	// link, rather than opening a page that says 502.
	answers(502, "proxy upstream error")
	m.openSandboxPort(mach.id)
	d := m.overlay.(*dialog)
	msgs := a2Run(d.submit(m, []string{"3000"}))
	done, ok := msgs[0].(previewDoneMsg)
	if !ok || done.err == nil || done.url == "" {
		t.Fatalf("submit: %#v", msgs)
	}
	if cmd := m.receivePreview(done); cmd == nil {
		t.Fatal("the link was not kept")
	}
	if !strings.Contains(m.flash, "nothing is answering on port 3000") || !strings.Contains(m.flash, "on your clipboard") {
		t.Fatalf("flash %q", m.flash)
	}
	if _, isNotice := m.overlay.(*dialog); !isNotice {
		t.Fatalf("no notice with the whole reason: %T", m.overlay)
	}
}

// Stopping a sandbox ends what runs in it, so conch writes down what that
// was and offers it back when the sandbox starts again: each agent on its
// own conversation, each terminal in its own folder.
func TestSandboxBringsBackWhatItWasRunning(t *testing.T) {
	m, mach := sandboxModel(t)
	p := &sbProvider{}
	useSandboxProvider(t, p)
	mach.state = stateOnline
	mach.panes = []proto.PaneInfo{
		{ID: "p1", Name: "claude", State: proto.PaneRunning, Cwd: "/home/daytona/api", Branch: "feat",
			Agent: &proto.AgentStatus{Name: "claude", State: proto.AgentWorking, SessionID: "sess-1"}},
		{ID: "p2", Name: "codex", State: proto.PaneRunning, Cwd: "/home/daytona/api",
			Agent: &proto.AgentStatus{Name: "codex", State: proto.AgentIdle}}, // no conversation saved
		{ID: "p3", Name: "zsh", State: proto.PaneRunning, Cwd: "/home/daytona", Command: []string{"/bin/zsh", "-l"}},
		{ID: "p4", Name: "gone", State: proto.PaneExited, Cwd: "/tmp"},
	}

	// Stopping writes it down.
	a2Run(m.sandboxOp(mach.id, "stop"))
	ran := m.sandboxRan[mach.id]
	if len(ran) != 3 {
		t.Fatalf("remembered %+v", ran)
	}
	if ran[0].Agent != "claude" || ran[0].Session != "sess-1" || ran[0].Dir != "/home/daytona/api" || ran[0].Branch != "feat" {
		t.Fatalf("the agent: %+v", ran[0])
	}
	if ran[2].Agent != "" || len(ran[2].Command) != 2 {
		t.Fatalf("the terminal: %+v", ran[2])
	}

	// It survives the TUI being restarted.
	if st := (Model{sandboxRan: m.sandboxRan}).savedRan(); len(st["sb"]) != 0 && len(st[mach.id]) != 3 {
		t.Fatalf("saved %+v", st)
	}

	// Coming back with nothing running, it offers them.
	mach.panes, mach.busy = nil, ""
	mach.state = stateOnline
	if cmd := m.offerRestore(mach.id); cmd != nil {
		t.Fatal("the offer should be a dialog, not a command")
	}
	d, ok := m.overlay.(*dialog)
	if !ok || !d.confirm {
		t.Fatalf("no confirmation: %T", m.overlay)
	}
	q := strings.Join(d.text, " ")
	for _, want := range []string{"was running", "Claude Code", "Codex", "terminal", "carry on where they left off", "start afresh"} {
		if !strings.Contains(q, want) {
			t.Fatalf("the question lacks %q: %s", want, q)
		}
	}

	// Yes starts them again: the one with a conversation is resumed, the
	// others are started in their own folders, and what came of it is
	// said out loud.
	c, peer := a1FakeClient(t)
	mach.c, mach.server = c, c.Server
	peer.setResult(proto.MethodSessionResume, proto.PaneInfo{ID: "np1", Name: "claude"})
	peer.setResult(proto.MethodPaneCreate, proto.PaneInfo{ID: "np2", Name: "started"})
	msgs := a2Run(m.restoreRunning(mach.id))
	if len(m.sandboxRan[mach.id]) != 0 {
		t.Fatal("it was offered twice")
	}
	if !strings.Contains(m.flash, "starting") {
		t.Fatalf("flash %q", m.flash)
	}
	var done restoreDoneMsg
	for _, msg := range msgs {
		if d, ok := msg.(restoreDoneMsg); ok {
			done = d
		}
	}
	if done.started != 3 || len(done.failed) != 0 || done.first.ID != "np1" {
		t.Fatalf("restore: %+v", done)
	}
	// One that says so is reported rather than swallowed, and the first
	// pane is shown so that something is visibly running.
	if cmd := m.receiveRestore(done); cmd == nil {
		t.Fatal("nothing was shown")
	}
	if !strings.Contains(m.flash, "started 3 of them") {
		t.Fatalf("flash %q", m.flash)
	}
	m.receiveRestore(restoreDoneMsg{machine: mach.id, failed: []string{"Claude Code in /src: no such session"}})
	if !strings.Contains(m.flash, "nothing could be started again") {
		t.Fatalf("all failed: %q", m.flash)
	}
	m.receiveRestore(restoreDoneMsg{machine: mach.id, started: 1, first: proto.PaneInfo{ID: "np1"},
		failed: []string{"Codex in /src: no such session"}})
	if !strings.Contains(m.flash, "started 1 of them") || !strings.Contains(m.flash, "Codex in /src") {
		t.Fatalf("some failed: %q", m.flash)
	}

	// Nothing is offered when something already runs there, or when the
	// sandbox is deleted.
	m.rememberRunning(mach.id)
	mach.panes = []proto.PaneInfo{{ID: "p9", State: proto.PaneRunning}}
	m.overlay = nil
	m.sandboxRan[mach.id] = ran
	m.offerRestore(mach.id)
	if m.overlay != nil {
		t.Fatal("offered while something was running")
	}
	mach.panes, mach.busy = nil, "" // as the finished stop would have left it
	a2Run(m.sandboxOp(mach.id, "delete"))
	if len(m.sandboxRan[mach.id]) != 0 {
		t.Fatal("a deleted sandbox is still remembered")
	}

	// An ordinary machine is not remembered: it keeps running without conch.
	if ssh := m.machine("gpu"); ssh != nil {
		ssh.panes = []proto.PaneInfo{{ID: "p1", State: proto.PaneRunning, Cwd: "/src"}}
		m.rememberRunning(ssh.id)
		if len(m.sandboxRan[ssh.id]) != 0 {
			t.Fatalf("remembered an ssh machine: %+v", m.sandboxRan)
		}
	}
}

// how the saved state sees it, for the test above.
func (m Model) savedRan() map[string][]ranPane { return m.sandboxRan }

// Asked not to, conch keeps no note of what a sandbox was running and
// offers nothing back.
func TestSandboxRestoreCanBeTurnedOff(t *testing.T) {
	m, mach := sandboxModel(t)
	useSandboxProvider(t, &sbProvider{})
	mach.state = stateOnline
	mach.panes = []proto.PaneInfo{{ID: "p1", Name: "claude", State: proto.PaneRunning, Cwd: "/src",
		Agent: &proto.AgentStatus{Name: "claude", SessionID: "s1"}}}
	no := false
	m.cfg.Sandbox.Set("daytona", config.ProviderCfg{Restore: &no})

	m.rememberRunning(mach.id)
	if len(m.sandboxRan[mach.id]) != 0 {
		t.Fatalf("a note was kept anyway: %+v", m.sandboxRan)
	}
	// Even one kept earlier is dropped rather than offered.
	m.sandboxRan = map[string][]ranPane{mach.id: {{Agent: "claude", Session: "s1", Dir: "/src"}}}
	mach.panes = nil
	m.offerRestore(mach.id)
	if m.overlay != nil || len(m.sandboxRan[mach.id]) != 0 {
		t.Fatalf("offered with it turned off: %T %+v", m.overlay, m.sandboxRan)
	}
	// And the stop confirmation makes no promise it won't keep.
	mach.panes = []proto.PaneInfo{{ID: "p1", State: proto.PaneRunning, Agent: &proto.AgentStatus{Name: "claude"}}}
	m.confirmStopSandbox(mach.id)
	if d, ok := m.overlay.(*dialog); !ok || strings.Contains(strings.Join(d.text, " "), "offers it back") {
		t.Fatalf("the confirmation promises a restore: %v", m.overlay)
	}
}

// A conversation that has gone is no reason to lose the agent: the resume
// is tried, and when the server says there is no such session the same
// agent is started afresh in the same folder.
func TestSandboxRestoreFallsBackToAFreshAgent(t *testing.T) {
	a2Isolate(t)
	c, peer := a1FakeClient(t)
	peer.setCodedError(proto.MethodSessionResume, proto.ErrNotFound, `no session "sess-1"`)
	peer.setResult(proto.MethodPaneCreate, proto.PaneInfo{ID: "fresh", Name: "claude"})

	info, err := restoreOne(c, ranPane{Agent: "claude", Session: "sess-1", Dir: "/src", Name: "claude"}, 80, 24)
	if err != nil || info.ID != "fresh" {
		t.Fatalf("fell back to: %+v %v", info, err)
	}
	params := lastParams[proto.PaneCreateParams](t, peer, proto.MethodPaneCreate)
	if params.Agent != "claude" || params.Cwd != "/src" || params.Cols != 80 {
		t.Fatalf("started with %+v", params)
	}

	// Any other refusal is reported rather than started over: a server
	// that is busy or broken should not quietly get a second agent.
	peer.setCodedError(proto.MethodSessionResume, proto.ErrBadRequest, "claude is not installed there")
	if _, err := restoreOne(c, ranPane{Agent: "claude", Session: "s", Dir: "/src"}, 80, 24); err == nil ||
		!strings.Contains(err.Error(), "not installed") {
		t.Fatalf("other refusal: %v", err)
	}
	// A terminal is simply started again.
	peer.setError(proto.MethodSessionResume, "")
	peer.setResult(proto.MethodPaneCreate, proto.PaneInfo{ID: "term"})
	if info, err := restoreOne(c, ranPane{Command: []string{"/bin/zsh", "-l"}, Dir: "/home"}, 80, 24); err != nil || info.ID != "term" {
		t.Fatalf("terminal: %+v %v", info, err)
	}
}

// What a sandbox has cost, period by period: where the money went, rather
// than one number on a row.
func TestSandboxUsageDetails(t *testing.T) {
	m, mach := sandboxModel(t)
	at := func(h, min int) time.Time { return time.Date(2026, 9, 26, h, min, 0, 0, time.Local) }
	u := sandbox.Usage{Known: true, Cost: 0.008919, From: at(9, 0), Periods: []sandbox.UsagePeriod{
		{From: at(16, 43), To: at(16, 45), CPU: 1, MemGiB: 1, DiskGiB: 3, Cost: 0.001762},
		{From: at(16, 45), To: at(17, 32), DiskGiB: 3, Cost: 0.000254},
		{From: at(17, 32), To: at(17, 38), CPU: 1, MemGiB: 1, DiskGiB: 3, Cost: 0.006479},
	}}
	out := strings.Join(usageLines(mach, u), "\n")
	for _, want := range []string{"Daytona sb1", "$0.009 since", "running · 1 vCPU, 1 GiB, 3 GiB disk",
		"stopped · 3 GiB disk", "keeps its disk, and is charged for it"} {
		if !strings.Contains(out, want) {
			t.Fatalf("the breakdown lacks %q:\n%s", want, out)
		}
	}
	// Newest first, so what just happened is at the top.
	first := strings.Index(out, "17:32")
	if last := strings.Index(out, "16:43"); first < 0 || last < first {
		t.Fatalf("not newest first:\n%s", out)
	}
	// A busy day is cut short rather than scrolling for ever.
	many := u
	many.Periods = nil
	for i := 0; i < usageLinesMax+5; i++ {
		many.Periods = append(many.Periods, sandbox.UsagePeriod{From: at(9, i), To: at(9, i+1), DiskGiB: 3, Cost: 0.001})
	}
	if out := strings.Join(usageLines(mach, many), "\n"); !strings.Contains(out, "… 5 earlier periods") {
		t.Fatalf("a busy day:\n%s", out)
	}
	// Nothing reported yet says why, rather than showing nothing.
	out = strings.Join(usageLines(mach, sandbox.Usage{}), "\n")
	if !strings.Contains(out, "nothing to report") || !strings.Contains(out, "settle hours behind") {
		t.Fatalf("nothing yet:\n%s", out)
	}

	// It is in the menu, and what comes back is shown and kept.
	useSandboxProvider(t, &sbProvider{usage: u})
	mach.state = stateOnline
	r := row{id: machineID(mach.id), kind: kindMachine, machine: mach.id}
	if labels := a2MenuLabels(newRowMenu(*m, r, 0, 0)); !strings.Contains(labels, "$ What it has cost…") {
		t.Fatalf("menu: %s", labels)
	}
	msgs := a2Run(m.openSandboxUsage(mach.id))
	shown, ok := msgs[0].(usageShownMsg)
	if !ok || shown.err != nil || !shown.usage.Known {
		t.Fatalf("asked: %#v", msgs)
	}
	m.receiveUsageShown(shown)
	if _, isNotice := m.overlay.(*dialog); !isNotice {
		t.Fatalf("no notice: %T", m.overlay)
	}
	if mach.usage == nil || mach.usage.Cost != u.Cost {
		t.Fatalf("not kept: %+v", mach.usage)
	}
	// The row now shows the real total, whether it is running or stopped.
	mach.box = &sandbox.Sandbox{ID: "sb1", State: sandbox.StateStopped, Disk: 3}
	if got := m.sandboxSpend(mach, time.Now()); got != "$0.009" {
		t.Fatalf("a stopped sandbox's row: %q", got)
	}
}

// A provider whose machines come in named sizes is not asked for numbers:
// the size goes where the snapshot does.
func TestSandboxDialogNamedSizes(t *testing.T) {
	m, _ := sandboxModel(t)

	labels := func(d *dialog) string {
		var out []string
		for _, f := range d.fields {
			out = append(out, strings.TrimSpace(f.label)+"|"+f.in.Placeholder)
		}
		return strings.Join(out, " · ")
	}
	// Daytona takes numbers, so all three fields are there.
	d := newSandboxDialog(*m, "daytona")
	if got := labels(d); !strings.Contains(got, "vCPUs|the snapshot's") || !strings.Contains(got, "Disk GiB|") {
		t.Fatalf("Daytona: %s", got)
	}
	// A provider that gives a sandbox a fixed life says so before making
	// one; Daytona, which stops an idle one instead, says nothing.
	if got := strings.Join(newSandboxDialog(*m, "boat").text, " "); !strings.Contains(got, "boat.dev gives it 2h 00m from now") ||
		!strings.Contains(got, "conch stops it sooner") {
		t.Fatalf("boat's dialog: %q", got)
	}
	if got := strings.Join(newSandboxDialog(*m, "daytona").text, " "); strings.Contains(got, "from now") {
		t.Fatalf("Daytona's dialog: %q", got)
	}

	// boat.dev's sizes are names: no numbers, and the snapshot field says
	// which names it takes.
	d = newSandboxDialog(*m, "boat")
	got := labels(d)
	if strings.Contains(got, "vCPUs") || strings.Contains(got, "Memory") || strings.Contains(got, "Disk") {
		t.Fatalf("boat was asked for numbers: %s", got)
	}
	if !strings.Contains(got, "Snapshot|a size (small, default, large) or a snapshot") {
		t.Fatalf("boat's snapshot field: %s", got)
	}
	if !strings.Contains(got, "Pass in|names of your environment variables") {
		t.Fatalf("boat's last field: %s", got)
	}
	// Submitting it asks for no size, and passes the variables through.
	// The dialog was built from the registry; what it submits goes to a
	// fake, so nothing is created anywhere.
	t.Setenv("A2_TOKEN", "value")
	p := &sbProvider{createErr: errors.New("far enough")}
	useSandboxProvider(t, p)
	cmd := d.submit(m, []string{"hull", "large", "A2_TOKEN"})
	if cmd == nil {
		t.Fatal("submit did nothing")
	}
	a2Run(cmd) // the job, and the steps it reports on the way
	if p.spec.Snapshot != "large" || p.spec.CPU != 0 || p.spec.Memory != 0 || p.spec.Disk != 0 ||
		p.spec.Env["A2_TOKEN"] != "value" {
		t.Fatalf("spec %+v", p.spec)
	}
	// A settings snapshot is the placeholder when there is one.
	pc := m.cfg.Sandbox.Of("boat")
	pc.Snapshot = "hull-base"
	m.cfg.Sandbox.Set("boat", pc)
	if got := labels(newSandboxDialog(*m, "boat")); !strings.Contains(got, "Snapshot|hull-base") {
		t.Fatalf("with a snapshot set: %s", got)
	}
}

// A billed period names only what the provider reported.
func TestPeriodWhat(t *testing.T) {
	for _, c := range []struct {
		p    sandbox.UsagePeriod
		want string
	}{
		{sandbox.UsagePeriod{CPU: 1, MemGiB: 1, DiskGiB: 3}, "running · 1 vCPU, 1 GiB, 3 GiB disk"},
		{sandbox.UsagePeriod{DiskGiB: 3}, "stopped · 3 GiB disk"},
		{sandbox.UsagePeriod{CPU: 4, MemGiB: 8}, "running · 4 vCPU, 8 GiB"}, // boat: machine time only
		{sandbox.UsagePeriod{}, "stopped"},
	} {
		if got := periodWhat(c.p); got != c.want {
			t.Errorf("%+v: %q, want %q", c.p, got, c.want)
		}
	}
	// The note about a stopped sandbox's disk is only there when the
	// provider charged for one.
	mach := &machine{id: "m1", label: "hull", target: "boat:bx_1"}
	now := time.Now()
	u := sandbox.Usage{Known: true, Cost: 0.25, From: now.Add(-time.Hour), To: now,
		Periods: []sandbox.UsagePeriod{{From: now.Add(-time.Hour), To: now, Cost: 0.25, CPU: 4, MemGiB: 8}}}
	got := strings.Join(usageLines(mach, u), "\n")
	if strings.Contains(got, "keeps its disk") || !strings.Contains(got, "boat.dev bx_1") {
		t.Fatalf("boat usage:\n%s", got)
	}
	u.Periods = append(u.Periods, sandbox.UsagePeriod{From: now.Add(-2 * time.Hour), To: now.Add(-time.Hour), DiskGiB: 3})
	if got := strings.Join(usageLines(mach, u), "\n"); !strings.Contains(got, "keeps its disk") {
		t.Fatalf("a stopped period:\n%s", got)
	}
}

// A job of minutes says what it is doing, and keeps saying it: a flash
// fades after four seconds and leaves you wondering whether anything is
// happening at all.
func TestWorkingLineWhileASandboxIsMade(t *testing.T) {
	m, _ := sandboxModel(t)
	m.width, m.height = 120, 30

	p := &sbProvider{created: sandbox.Sandbox{ID: "bx_1", State: sandbox.StateStarted}}
	useSandboxProvider(t, p)
	var saidTo []string
	old := setUpSandboxFn
	setUpSandboxFn = func(_ context.Context, mm remote.Machine, say func(string)) (*client.Client, error) {
		say("probing " + mm.Label + "…")
		say("copying conch to " + mm.Label + " (16 MB)…")
		return nil, errors.New("far enough")
	}
	t.Cleanup(func() { setUpSandboxFn = old })

	// The job, and then the steps it left behind — which is what the model
	// does, one stepMsg at a time, each asking for the next.
	ch := make(chan string, 16)
	a2Run(createSandboxWith("boat", sandbox.Spec{}, "hull", ch))
	for cmd := nextStep(ch); cmd != nil; {
		msg := cmd()
		st, ok := msg.(stepMsg)
		if !ok {
			break // the job closed the channel
		}
		saidTo = append(saidTo, st.step)
		cmd = nextStep(st.ch)
	}
	want := []string{"asking boat.dev for a sandbox…", "sandbox bx_1 is up · setting conch up in it…",
		"probing hull…", "copying conch to hull (16 MB)…", "setting it up failed · deleting sandbox bx_1…"}
	if strings.Join(saidTo, " | ") != strings.Join(want, " | ") {
		t.Fatalf("steps:\n got %q\nwant %q", saidTo, want)
	}

	// Each step goes to the status bar and stays there, with a spinner, and
	// it asks for the next one.
	for _, step := range saidTo {
		next, cmd := m.Update(stepMsg{step: step, ch: make(chan string)})
		*m = next.(Model)
		if m.working != step || cmd == nil {
			t.Fatalf("after %q: working %q cmd %v", step, m.working, cmd != nil)
		}
		bar := ansi.Strip(m.statusBar())
		head := step
		if r := []rune(step); len(r) > 24 { // the bar shortens a long step
			head = string(r[:24])
		}
		if !strings.Contains(bar, head) || !strings.Contains(bar, spinner[m.spin%len(spinner)]) {
			t.Fatalf("the bar does not say %q with a spinner:\n%s", head, bar)
		}
	}
	// It outlasts a flash, and hides it while it runs: the job is the news.
	m.setFlash("something else happened", false)
	if bar := ansi.Strip(m.statusBar()); strings.Contains(bar, "something else happened") ||
		!strings.Contains(bar, "setting it up failed") {
		t.Fatalf("a flash took the line:\n%s", bar)
	}
	// The spinner keeps turning while it works.
	m.ticking = false
	if cmd := m.startTicking(); cmd == nil || !m.ticking {
		t.Fatal("no spinner while working")
	}
	// Whatever ends the job clears the line.
	next, _ := m.Update(errMsg{err: errors.New("it failed")})
	if got := next.(Model).working; got != "" {
		t.Fatalf("after an error: %q", got)
	}
	m.setWorking("still going…")
	next, _ = m.Update(machineAddedMsg{m: remote.Machine{ID: "hull", Label: "hull"}})
	if got := next.(Model).working; got != "" {
		t.Fatalf("after a machine arrived: %q", got)
	}
}

// A refusal is shown once, in the provider's words: the reason already
// says what failed, so conch does not say it again.
func TestCreateSandboxRefusalReadsOnce(t *testing.T) {
	sandboxModel(t) // an isolated home, so nothing real is asked
	p := &sbProvider{createErr: errors.New("create sandbox: boat: Start the $20/month Boat plan to create sandboxes. (402); boat.dev wants a plan")}
	useSandboxProvider(t, p)
	var said string
	for _, msg := range a2Run(createSandbox("boat", sandbox.Spec{}, "hull")) {
		if e, ok := msg.(errMsg); ok {
			said = e.err.Error()
		}
	}
	if strings.HasPrefix(said, "create sandbox: create sandbox") {
		t.Fatalf("the reason says it twice: %q", said)
	}
	if !strings.HasPrefix(said, "create sandbox: boat:") {
		t.Fatalf("the reason reads %q", said)
	}
	if !strings.Contains(said, "wants a plan") {
		t.Fatalf("the provider's words are gone: %q", said)
	}
}
