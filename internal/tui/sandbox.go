package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/remote"
	"github.com/Amitgb14/conch/internal/sandbox"
)

// openSandboxProvider opens a provider from config.toml; tests replace it.
var openSandboxProvider = remote.OpenProvider

// setUpSandboxFn installs conch in a new sandbox and connects; tests
// replace it.
var setUpSandboxFn = remote.SetUpSandbox

// sandboxWait bounds creating, starting or stopping a sandbox.
const sandboxWait = 10 * time.Minute

// sandbox reports the provider and ID of a machine that is a sandbox.
func (mach *machine) sandbox() (provider, id string, ok bool) {
	return remote.ParseSandboxTarget(mach.target)
}

// sandboxDoneMsg ends a start, stop or delete of a machine's sandbox.
type sandboxDoneMsg struct {
	machine string
	op      string // start, stop or delete
	err     error
}

var sandboxDoing = map[string]string{"start": "starting", "stop": "stopping", "delete": "deleting"}

// sandboxOp runs op on the machine's sandbox, showing it as busy meanwhile.
func (m *Model) sandboxOp(mid, op string) tea.Cmd {
	mach := m.machine(mid)
	if mach == nil || mach.busy != "" {
		return nil
	}
	provider, id, ok := mach.sandbox()
	if !ok {
		return nil
	}
	switch op {
	case "stop":
		// The last moment its panes are known: what runs there ends with
		// the sandbox, and a stop is meant to be undone.
		m.rememberRunning(mid)
	case "delete":
		delete(m.sandboxRan, mid) // nothing to come back to
	}
	mach.busy = sandboxDoing[op]
	m.setFlash(mach.busy+" "+mach.label+"…", false)
	run := func() tea.Msg {
		p, err := openSandboxProvider(provider)
		if err == nil {
			err = p.Check()
		}
		if err != nil {
			return sandboxDoneMsg{machine: mid, op: op, err: err}
		}
		ctx, cancel := context.WithTimeout(context.Background(), sandboxWait)
		defer cancel()
		switch op {
		case "start":
			_, err = p.Start(ctx, id)
		case "stop":
			err = p.Stop(ctx, id)
		case "delete":
			if err = p.Delete(ctx, id); errors.Is(err, sandbox.ErrNotFound) {
				err = nil // already gone: forgetting it is all that's left
			}
			if err == nil {
				err = remote.RemoveMachine(mid)
			}
		}
		return sandboxDoneMsg{machine: mid, op: op, err: err}
	}
	return tea.Batch(run, m.rebuild())
}

// sandboxDone applies the end of a sandbox operation.
func (m *Model) sandboxDone(msg sandboxDoneMsg) tea.Cmd {
	mach := m.machine(msg.machine)
	if mach == nil {
		return nil
	}
	mach.busy = ""
	if msg.err != nil {
		m.setFlash(fmt.Sprintf("%s %s failed: %v", msg.op, mach.label, msg.err), true)
		return m.rebuild()
	}
	switch msg.op {
	case "start":
		m.setFlash("started "+mach.label, false)
		// What it was running is offered back once its panes are known,
		// which is after the connection (see receivePanes).
		return m.reconnect(mach.id, true)
	case "stop":
		mach.close()
		mach.state, mach.err, mach.sandboxState = stateAttention, "sandbox stopped", sandbox.StateStopped
		m.setFlash("stopped "+mach.label+" · its files are kept", false)
		return m.rebuild()
	case "delete":
		m.setFlash("deleted "+mach.label, false)
		return tea.Batch(m.syncCatalog(), m.saveState())
	}
	return nil
}

// confirmStopSandbox asks before stopping a sandbox: whatever runs there
// ends.
func (m *Model) confirmStopSandbox(mid string) {
	mach := m.machine(mid)
	if mach == nil {
		return
	}
	text := []string{fmt.Sprintf("Stop %s? Its agents and terminals end; its files stay, and it stops costing for anything but disk.", mach.label)}
	if n := workingAgents(mach.panes); n > 0 {
		text = append(text, fmt.Sprintf("%d agent%s there %s still working.", n, plural(n), map[bool]string{true: "is", false: "are"}[n == 1]))
	}
	provider, _, _ := mach.sandbox()
	if ran := runningIn(mach); len(ran) > 0 && m.cfg.Sandbox.Of(provider).RestoresRunning() {
		// Stopping ends the processes; what they were doing comes back.
		text = append(text, "conch writes down what is running and offers it back when you start it again: "+listRan(ran)+"."+resumeNote(ran))
	}
	d := newConfirm("", func(m *Model) tea.Cmd { return m.sandboxOp(mid, "stop") })
	d.text = text
	m.overlay = d
}

// confirmDeleteSandbox asks before deleting a sandbox, listing the work
// conch last saw there that exists nowhere else.
func (m *Model) confirmDeleteSandbox(mid string) {
	mach := m.machine(mid)
	if mach == nil {
		return
	}
	_, id, _ := mach.sandbox()
	text := []string{fmt.Sprintf("Delete %s (sandbox %s) and everything in it? This can't be undone.", mach.label, id)}
	lost := remote.UnsavedWork(mach.projects)
	switch {
	case len(lost) > 0:
		text = append(text, "Not saved anywhere else:")
		for _, l := range lost {
			text = append(text, "  "+l)
		}
	case mach.state != stateOnline:
		text = append(text, "conch can't see into it now, so it can't tell whether work there is pushed.")
	}
	d := newConfirm("", func(m *Model) tea.Cmd { return m.sandboxOp(mid, "delete") })
	d.text = text
	m.overlay = d
}

func workingAgents(panes []proto.PaneInfo) int {
	n := 0
	for _, p := range panes {
		if p.Agent != nil && p.Agent.State == proto.AgentWorking {
			n++
		}
	}
	return n
}

// newAddMenu asks what kind of machine to add: one that already exists, or
// a sandbox conch makes. The providers live a level down, so the menu stays
// two lines however many of them conch grows.
func newAddMenu() *menu {
	return &menu{title: "Add a machine", items: []menuItem{
		{"s", "Over ssh…", func(m *Model) tea.Cmd {
			d := newAddMachineDialog(*m)
			m.overlay = d
			return d.focusCmd()
		}},
		{"b", "New sandbox…", func(m *Model) tea.Cmd {
			m.overlay = newSandboxMenu(newAddMenu())
			return nil
		}},
	}}
}

// newSandboxMenu lists the providers conch can make a sandbox with. It is
// built from the registry, so a new provider appears here by being in
// sandbox.Providers and needs no menu of its own.
func newSandboxMenu(back *menu) *menu {
	mu := &menu{title: "New sandbox", back: back}
	taken := map[string]bool{}
	for _, name := range sandbox.Providers {
		key := ""
		if k := strings.ToLower(name[:1]); !taken[k] {
			key, taken[k] = k, true // enter still picks one that shares a letter
		}
		mu.items = append(mu.items, menuItem{key, providerLabel(name) + "…", func(m *Model) tea.Cmd {
			d := newSandboxDialog(*m, name)
			m.overlay = d
			return d.focusCmd()
		}})
	}
	if len(mu.items) == 0 { // no provider is built in: say so rather than open nothing
		mu.items = []menuItem{{"", "conch knows no sandbox providers", func(*Model) tea.Cmd { return nil }}}
	}
	return mu
}

// sandboxSizeNames is the sizes a provider's machines come in, or nothing
// when it takes numbers. A provider conch cannot open is taken to take
// numbers: the dialog says the key is missing, and nothing is created.
func sandboxSizeNames(provider string) []string {
	p, err := openSandboxProvider(provider)
	if err != nil || p == nil {
		return nil
	}
	if ns, ok := p.(sandbox.NamedSizes); ok {
		return ns.SizeNames()
	}
	return nil
}

// providerLabel is a provider's name as people write it.
func providerLabel(name string) string { return sandbox.ProviderLabel(name) }

func newSandboxDialog(m Model, provider string) *dialog {
	label := providerLabel(provider)
	text := []string{"Creates a sandbox with " + label + ", installs conch there and adds it as a machine. It runs, and costs, until you stop it (m → Stop sandbox)."}
	if p, err := openSandboxProvider(provider); err != nil {
		text = append(text, err.Error())
	} else if err := p.Check(); err != nil {
		text = append(text, "Needs a "+label+" API key: "+strings.TrimPrefix(err.Error(), sandbox.ErrNotConfigured.Error()+": ")+".")
	}
	// A provider whose machines come in named sizes is not asked for
	// numbers: the size goes where the snapshot does, so those three
	// fields would only be refused later.
	sizeNames := sandboxSizeNames(provider)
	labels := []string{"Label", "Snapshot"}
	if len(sizeNames) == 0 {
		labels = append(labels, "vCPUs", "Memory GiB", "Disk GiB")
	}
	labels = append(labels, "Pass in")
	last := len(labels) - 1
	d := newDialog(m, " New "+label+" sandbox ", text, labels, nil)
	d.fields[0].in.Placeholder = "defaults to sandbox-<id>"
	d.fields[1].in.Placeholder = firstNonEmpty(m.cfg.Sandbox.Of(provider).Snapshot, label+"'s default")
	if len(sizeNames) > 0 {
		d.fields[1].in.Placeholder = firstNonEmpty(m.cfg.Sandbox.Of(provider).Snapshot,
			"a size ("+strings.Join(sizeNames, ", ")+") or a snapshot")
	}
	for i := 2; i < last; i++ {
		d.fields[i].in.Placeholder = "the snapshot's"
	}
	d.fields[last].in.Placeholder = "names of your environment variables, e.g. CLAUDE_CODE_OAUTH_TOKEN"
	cfg := m.cfg.Sandbox.Of(provider)
	d.submit = func(m *Model, v []string) tea.Cmd {
		var sizes [3]int
		for i, name := range []string{"vCPUs", "Memory", "Disk"} {
			if 2+i >= last {
				break // this provider was not asked for numbers
			}
			s := strings.TrimSpace(v[2+i])
			if s == "" {
				continue
			}
			n, err := strconv.Atoi(s)
			if err != nil || n < 0 {
				return func() tea.Msg { return errMsg{fmt.Errorf("%s: give a whole number", name)} }
			}
			sizes[i] = n
		}
		names := append([]string{}, cfg.Env...)
		names = append(names, strings.FieldsFunc(v[last], func(r rune) bool { return r == ',' || r == ' ' })...)
		env, err := sandbox.EnvFrom(names)
		if err != nil {
			return func() tea.Msg { return errMsg{err} }
		}
		spec := sandbox.Spec{Snapshot: strings.TrimSpace(v[1]), CPU: sizes[0], Memory: sizes[1], Disk: sizes[2], Env: env, AutoStop: cfg.AutoStop}
		m.setFlash("creating a "+label+" sandbox (a minute or two)…", false)
		return createSandbox(provider, spec, strings.TrimSpace(v[0]))
	}
	return d
}

// createSandbox makes a sandbox, sets conch up in it and saves it as a
// machine. A sandbox that can't be set up is deleted rather than left to
// cost: nobody is there to ask.
func createSandbox(provider string, spec sandbox.Spec, label string) tea.Cmd {
	return func() tea.Msg {
		p, err := openSandboxProvider(provider)
		if err == nil {
			err = p.Check()
		}
		if err != nil {
			return errMsg{err}
		}
		ctx, cancel := context.WithTimeout(context.Background(), sandboxWait)
		defer cancel()
		s, err := p.Create(ctx, spec)
		if err == nil {
			if label == "" {
				label = remote.DefaultSandboxLabel(s.ID)
			}
			m := remote.Machine{Label: label, Target: remote.SandboxTarget(provider, s.ID)}
			c, setUpErr := setUpSandboxFn(ctx, m, func(string) {})
			if setUpErr == nil {
				c.Close()
				saved, err := remote.SaveMachine(m)
				if err != nil {
					return errMsg{err}
				}
				return machineAddedMsg{m: saved, note: provider + " sandbox " + s.ID + " · runs until stopped (m → Stop sandbox)"}
			}
			err = setUpErr
		}
		if s.ID == "" {
			return errMsg{fmt.Errorf("create sandbox: %w", err)}
		}
		dctx, dcancel := context.WithTimeout(context.Background(), time.Minute)
		defer dcancel()
		if derr := p.Delete(dctx, s.ID); derr != nil && !errors.Is(derr, sandbox.ErrNotFound) {
			return errMsg{fmt.Errorf("setting up sandbox %s failed: %v; deleting it failed too (%v): conch sandbox rm %s", s.ID, err, derr, s.ID)}
		}
		return errMsg{fmt.Errorf("setting up sandbox %s failed, so it was deleted: %w", s.ID, err)}
	}
}

// Stopping a sandbox nobody is using. A provider's own idle timer counts
// only what reaches it from outside — an ssh connection, an API call — so
// an agent working quietly inside looks idle to it and would be stopped
// mid-task. conch watches what it can see instead: an agent working, or a
// pane printing. When neither has happened for [sandbox.<provider>]
// idle_stop minutes (30 unless set, 0 never), the sandbox is paused, which
// keeps its files and, where the provider can, its memory.

// idleCheckEvery is how often the idle watch looks. Minutes are what is
// being measured, so looking every half minute is often enough.
const idleCheckEvery = 30 * time.Second

// idleSince is when a machine was last doing something: the newest of its
// panes' last output, or when an agent there was last working. A machine
// with no panes at all counts from when conch connected to it.
func (m Model) idleSince(mach *machine, now time.Time) time.Time {
	last := mach.since
	for _, p := range mach.panes {
		if p.State != proto.PaneRunning {
			continue
		}
		if p.Agent != nil && (p.Agent.State == proto.AgentWorking || p.Agent.NeedsAttention()) {
			return now // working, or waiting for an answer: not idle at all
		}
		if p.LastActive.After(last) {
			last = p.LastActive
		}
	}
	return last
}

// watchIdleSandboxes stops the sandboxes nothing is happening in. It says
// so first: the flash names what went and why, and the tree shows it
// stopped, so a sandbox never disappears without a word.
func (m *Model) watchIdleSandboxes(now time.Time) tea.Cmd {
	var cmds []tea.Cmd
	for _, mach := range m.machines {
		provider, _, ok := mach.sandbox()
		if !ok || mach.state != stateOnline || mach.busy != "" {
			continue
		}
		mins := m.cfg.Sandbox.Of(provider).IdleMinutes()
		if mins <= 0 {
			continue // never, by configuration
		}
		if now.Sub(m.idleSince(mach, now)) < time.Duration(mins)*time.Minute {
			continue
		}
		cmd := m.sandboxOp(mach.id, "stop")
		// After the op, whose own flash says only that it is stopping:
		// why it is stopping is the part worth reading.
		m.setFlash(fmt.Sprintf("%s was idle for %dm · stopping it; its files are kept", mach.label, mins), false)
		cmds = append(cmds, cmd)
	}
	return tea.Batch(cmds...)
}

// Looking at what runs inside a sandbox. An agent that starts a dev
// server there has nothing to show for it: the port is inside somebody
// else's machine. A provider that can hand out a link to one does, and
// conch opens it here, where the browser is.

// previewFor is how long a preview link is asked to last. Long enough to
// read what is there without leaving a link lying about for a day.
const previewFor = time.Hour

// lastPreviewPort is offered again next time, per machine: the same dev
// server usually.
type previewDoneMsg struct {
	machine, url string
	err          error
}

// openSandboxPort asks which port, then opens its link in the browser.
func (m *Model) openSandboxPort(mid string) tea.Cmd {
	mach := m.machine(mid)
	if mach == nil {
		return nil
	}
	provider, id, ok := mach.sandbox()
	if !ok {
		return nil
	}
	p, err := openSandboxProvider(provider)
	if err == nil {
		if _, can := p.(sandbox.Previewer); !can {
			err = fmt.Errorf("%s cannot give a link to a port", providerLabel(provider))
		}
	}
	if err != nil {
		m.setFlash(err.Error(), true)
		return nil
	}
	d := newDialog(*m, " Open a port on "+ansi.Truncate(mach.label, 30, "…")+" ",
		[]string{"A link to a port inside the sandbox, good for an hour. Anyone with the link can reach that port, so don't paste it about."},
		[]string{"Port"}, []string{firstNonEmpty(mach.lastPort, "3000")})
	d.submit = func(m *Model, v []string) tea.Cmd {
		port, err := strconv.Atoi(strings.TrimSpace(v[0]))
		if err != nil || port < 1 || port > 65535 {
			return func() tea.Msg { return errMsg{fmt.Errorf("%q: give a port between 1 and 65535", v[0])} }
		}
		if mach := m.machine(mid); mach != nil {
			mach.lastPort = strconv.Itoa(port)
		}
		m.setFlash(fmt.Sprintf("asking %s for a link to port %d…", providerLabel(provider), port), false)
		label := mach.label
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			link, err := p.(sandbox.Previewer).PreviewURL(ctx, id, port, previewFor)
			if err != nil {
				return previewDoneMsg{machine: mid, err: err}
			}
			// A link to a port nothing answers on opens a proxy error
			// page, which says nothing useful. Ask first, and say what
			// it usually means.
			if why := previewTrouble(ctx, link, label, port); why != "" {
				return previewDoneMsg{machine: mid, url: link, err: errString(why)}
			}
			return previewDoneMsg{machine: mid, url: link}
		}
	}
	m.overlay = d
	return d.focusCmd()
}

// checkPreview fetches a preview link to see whether anything answers on
// it; tests replace it.
var checkPreview = func(ctx context.Context, link string) (int, string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return 0, ""
	}
	// The proxy's warning page is for browsers; this asks past it.
	req.Header.Set("X-Daytona-Skip-Preview-Warning", "true")
	c := &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := c.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	return resp.StatusCode, string(body)
}

// previewTrouble says what is wrong when nothing answers on a port, in
// the words that mend it. It returns "" when the link is worth opening —
// including for a page that answers with an error of its own, which is
// the program's business rather than conch's.
func previewTrouble(ctx context.Context, link, label string, port int) string {
	code, body := checkPreview(ctx, link)
	switch {
	case code == 0:
		return fmt.Sprintf("%s could not be reached: %s", link, body)
	case code != http.StatusBadGateway && code != http.StatusServiceUnavailable:
		return ""
	}
	// The proxy reached the sandbox and found nothing listening. A server
	// bound to localhost is the usual reason: the proxy arrives on the
	// sandbox's own interface, where loopback cannot be seen.
	why := fmt.Sprintf("nothing is answering on port %d in %s.", port, label)
	if strings.Contains(body, "DAYTONA_DAEMON") || strings.Contains(body, "upstream") {
		why += " The proxy reached the sandbox but found no server there:"
	}
	return why + " start one, and have it listen on 0.0.0.0 rather than 127.0.0.1 — a server bound to localhost cannot be reached from outside the sandbox (next dev --hostname 0.0.0.0, vite --host, rails s -b 0.0.0.0)."
}

// receivePreview opens the link, and keeps it on the clipboard: a browser
// that doesn't open leaves the user with the link rather than nothing.
func (m *Model) receivePreview(msg previewDoneMsg) tea.Cmd {
	if msg.err != nil {
		if msg.url != "" {
			// The link is good even when nothing answers yet: keep it on
			// the clipboard so it can be tried again in a moment.
			m.showError(errString(errText(msg.err) + " The link is on your clipboard: " + msg.url))
			return copyText(msg.url)
		}
		m.showError(msg.err)
		return nil
	}
	m.setFlash("opened "+msg.url+" · copied", false)
	return tea.Batch(openURL(msg.url), copyText(msg.url))
}

// Keeping a sandbox to make others from: a checkout, its dependencies and
// an agent's login, set up once. What the provider needs of the sandbox
// first is the provider's own to say — Daytona wants a container sandbox
// stopped — so its refusal is shown as it is rather than guessed at.

type snapshotDoneMsg struct {
	machine, name string
	err           error
}

func (m *Model) openSnapshotDialog(mid string) tea.Cmd {
	mach := m.machine(mid)
	if mach == nil {
		return nil
	}
	provider, id, ok := mach.sandbox()
	if !ok {
		return nil
	}
	p, err := openSandboxProvider(provider)
	if err == nil {
		if _, can := p.(sandbox.Snapshotter); !can {
			err = fmt.Errorf("%s cannot keep snapshots", providerLabel(provider))
		}
	}
	if err != nil {
		m.setFlash(err.Error(), true)
		return nil
	}
	text := []string{"Keeps this sandbox as it stands, to make others from: M → New sandbox… takes the name as its snapshot."}
	if mach.state == stateOnline {
		text = append(text, providerLabel(provider)+" may want it stopped first; it will say so, and nothing is changed if it does.")
	}
	d := newDialog(*m, " Keep "+ansi.Truncate(mach.label, 30, "…")+" ", text, []string{"Name"},
		[]string{mach.label + "-" + time.Now().Format("2006-01-02-1504")})
	d.submit = func(m *Model, v []string) tea.Cmd {
		name := strings.TrimSpace(v[0])
		if name == "" {
			return func() tea.Msg { return errMsg{errString("a snapshot needs a name")} }
		}
		m.setFlash("keeping "+mach.label+" as "+name+"…", false)
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), sandboxWait)
			defer cancel()
			err := p.(sandbox.Snapshotter).Snapshot(ctx, id, name)
			return snapshotDoneMsg{machine: mid, name: name, err: err}
		}
	}
	m.overlay = d
	return d.focusCmd()
}

func (m *Model) receiveSnapshot(msg snapshotDoneMsg) tea.Cmd {
	if msg.err != nil {
		m.showError(msg.err)
		return nil
	}
	m.setFlash("keeping it as "+msg.name+" · usable in a few minutes · M → New sandbox… takes it as the snapshot", false)
	return nil
}

// What a sandbox has run up. A provider bills by the hour it is started,
// which is easy to forget about; the tree says how long each has been
// running, and what that has cost where prices are set. conch ships no
// price list — providers change theirs and a stale one misleads — so the
// money only appears once [sandbox.<provider>] price_cpu_hour and its
// neighbours are filled in.

// sandboxPollEvery is how often the providers are asked about their
// sandboxes. Sizes don't change and start times move slowly, so this is
// about keeping a clock honest rather than watching anything.
const sandboxPollEvery = 2 * time.Minute

type sandboxListMsg struct {
	provider string
	boxes    []sandbox.Sandbox
	err      error
}

// sandboxUsageMsg is what a provider says one of its sandboxes has cost.
type sandboxUsageMsg struct {
	machine string
	usage   sandbox.Usage
}

// usageEvery is how often a provider is asked what a sandbox has cost.
// Their billing settles hours behind, so asking often would be asking the
// same question again.
const usageEvery = 15 * time.Minute

// usageSince is how far back to ask. A sandbox that has run for longer
// than this is asked about the whole of it — from when it started.
const usageSince = 30 * 24 * time.Hour

// pollSandboxes asks each provider what it has, once every
// sandboxPollEvery, and only while conch has a sandbox to ask about.
func (m *Model) pollSandboxes(now time.Time) tea.Cmd {
	if now.Sub(m.boxesAsked) < sandboxPollEvery {
		return nil
	}
	want := map[string]bool{}
	for _, mach := range m.machines {
		if provider, _, ok := mach.sandbox(); ok {
			want[provider] = true
		}
	}
	if len(want) == 0 {
		return nil
	}
	m.boxesAsked = now
	var cmds []tea.Cmd
	cmds = append(cmds, m.askUsage(now)...)
	for provider := range want {
		cmds = append(cmds, func() tea.Msg {
			p, err := openSandboxProvider(provider)
			if err == nil {
				err = p.Check()
			}
			if err != nil {
				return sandboxListMsg{provider: provider, err: err}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			boxes, err := p.List(ctx)
			return sandboxListMsg{provider: provider, boxes: boxes, err: err}
		})
	}
	return tea.Batch(cmds...)
}

// askUsage asks each provider what its sandboxes have cost. A provider
// that cannot say is not asked again for this round; one that says
// nothing yet — its figures lag — leaves the estimate showing.
func (m *Model) askUsage(now time.Time) []tea.Cmd {
	if now.Sub(m.usageAsked) < usageEvery {
		return nil
	}
	m.usageAsked = now
	var cmds []tea.Cmd
	for _, mach := range m.machines {
		provider, id, ok := mach.sandbox()
		if !ok {
			continue
		}
		from := now.Add(-usageSince)
		if mach.box != nil && !mach.box.Created.IsZero() && mach.box.Created.Before(from) {
			from = mach.box.Created
		}
		mid := mach.id
		cmds = append(cmds, func() tea.Msg {
			p, err := openSandboxProvider(provider)
			if err != nil || p.Check() != nil {
				return nil
			}
			metered, can := p.(sandbox.Metered)
			if !can {
				return nil
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			u, err := metered.Usage(ctx, id, from, now)
			if err != nil {
				return nil // a background look; every action says for itself
			}
			return sandboxUsageMsg{machine: mid, usage: u}
		})
	}
	return cmds
}

// receiveUsage keeps what a provider said one of its sandboxes has cost.
func (m *Model) receiveUsage(msg sandboxUsageMsg) tea.Cmd {
	if mach := m.machine(msg.machine); mach != nil && msg.usage.Known {
		mach.usage = &msg.usage
	}
	return nil
}

// receiveSandboxList keeps what the provider said against each machine. An
// error is not shown: this is a background look, and every action says for
// itself when the provider can't be reached.
func (m *Model) receiveSandboxList(msg sandboxListMsg) tea.Cmd {
	if msg.err != nil {
		return nil
	}
	byID := make(map[string]sandbox.Sandbox, len(msg.boxes))
	for _, b := range msg.boxes {
		byID[b.ID] = b
	}
	for _, mach := range m.machines {
		provider, id, ok := mach.sandbox()
		if !ok || provider != msg.provider {
			continue
		}
		if b, found := byID[id]; found {
			box := b
			mach.box = &box
		}
	}
	return nil
}

// runningFor is how long a sandbox has been up, or 0 when it isn't or
// nothing is known yet.
func (mach *machine) runningFor(now time.Time) time.Duration {
	if mach.box == nil || mach.box.State != sandbox.StateStarted || mach.box.Created.IsZero() {
		return 0
	}
	return now.Sub(mach.box.Created)
}

// sandboxCost is what a machine's sandbox has cost: what the provider
// says, when it says anything, and otherwise what the prices in the
// settings work out to. told is false when neither can say; own is true
// when the figure is the provider's own rather than conch's arithmetic.
func (m Model) sandboxCost(mach *machine, now time.Time) (cost float64, told, own bool) {
	provider, _, ok := mach.sandbox()
	if !ok {
		return 0, false, false
	}
	if mach.usage != nil && mach.usage.Known {
		return mach.usage.Cost, true, true
	}
	cfg := m.cfg.Sandbox.Of(provider)
	if mach.box == nil || !cfg.Priced() {
		return 0, false, false
	}
	up := mach.runningFor(now)
	if up <= 0 {
		// Stopped: conch is not told when it stopped, so it cannot say
		// what it has cost. The rate it keeps costing is what it can say.
		return 0, false, false
	}
	hourly := cfg.CostPerHour(mach.box.CPU, mach.box.Memory, mach.box.Disk)
	return hourly * up.Hours(), hourly > 0, false
}

// sandboxSpend is what a sandbox has been up for and what that has cost,
// as the tree and the sandbox pages show it: "4h 12m · $0.28". A stopped
// one keeps its disk, and the provider keeps charging for it, so that
// shows as a rate — conch is not told when it stopped, and a total it
// cannot know is worse than the rate it can.
func (m Model) sandboxSpend(mach *machine, now time.Time) string {
	var parts []string
	if up := mach.runningFor(now); up > 0 {
		parts = append(parts, shortDuration(up))
	}
	// What it has cost, whether it is running or stopped: a stopped
	// sandbox has still cost whatever it cost, and that is the number
	// worth seeing.
	if cost, told, own := m.sandboxCost(mach, now); told && cost > 0 {
		// "~" says the figure is conch's arithmetic on the prices in the
		// settings, not what the provider has billed.
		parts = append(parts, map[bool]string{false: "~"}[own]+money(cost))
	} else if r := m.sandboxStoppedRate(mach); r > 0 {
		// Nothing billed yet, but the disk it keeps is not free.
		parts = append(parts, "disk "+rate(r))
	}
	return strings.Join(parts, " · ")
}

// sandboxStoppedRate is what a stopped sandbox costs an hour for the disk
// it keeps, or 0 when it is running, unknown or no price is set.
func (m Model) sandboxStoppedRate(mach *machine) float64 {
	provider, _, ok := mach.sandbox()
	if !ok || mach.box == nil || mach.box.State == sandbox.StateStarted {
		return 0
	}
	return m.cfg.Sandbox.Of(provider).StoppedCostPerHour(mach.box.Disk)
}

// shortDuration reads as a person would say it: 42m, 4h 12m, 3d 4h.
func shortDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh %02dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd %dh", int(d.Hours())/24, int(d.Hours())%24)
	}
}

// money is a cost in dollars, to the cent, or finer while it is small
// enough that cents say nothing.
func money(v float64) string {
	if v < 0.1 {
		return fmt.Sprintf("$%.3f", v)
	}
	return fmt.Sprintf("$%.2f", v)
}

// rate reads a cost per hour in whatever unit says something: a few cents
// an hour as it is, a fraction of a cent by the day or the month. A rate
// that rounds to $0.000/h tells nobody anything, and the whole point of
// showing what a stopped sandbox costs is that it is not nothing.
func rate(perHour float64) string {
	switch {
	case perHour <= 0:
		return ""
	case perHour >= 0.01:
		return fmt.Sprintf("$%.2f/h", perHour)
	case perHour*24 >= 0.01:
		return fmt.Sprintf("$%.2f/day", perHour*24)
	default:
		return fmt.Sprintf("$%.2f/month", perHour*24*30)
	}
}

// Where the money went. A provider bills a sandbox in periods, one per
// stretch of it doing the same thing, so the periods say what was paid
// for: running, or stopped and keeping its disk. The row has room for a
// total; this has room for the rest.

type usageShownMsg struct {
	machine string
	usage   sandbox.Usage
	err     error
}

// openSandboxUsage asks the provider what this sandbox has cost, and
// shows where it went.
func (m *Model) openSandboxUsage(mid string) tea.Cmd {
	mach := m.machine(mid)
	if mach == nil {
		return nil
	}
	provider, id, ok := mach.sandbox()
	if !ok {
		return nil
	}
	p, err := openSandboxProvider(provider)
	if err == nil {
		err = p.Check()
	}
	metered, can := p.(sandbox.Metered)
	if err == nil && !can {
		err = fmt.Errorf("%s does not say what a sandbox has cost", providerLabel(provider))
	}
	if err != nil {
		m.setFlash(err.Error(), true)
		return nil
	}
	m.setFlash("asking "+providerLabel(provider)+" what "+mach.label+" has cost…", false)
	from := time.Now().Add(-usageSince)
	if mach.box != nil && !mach.box.Created.IsZero() && mach.box.Created.Before(from) {
		from = mach.box.Created
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		u, err := metered.Usage(ctx, id, from, time.Now())
		return usageShownMsg{machine: mid, usage: u, err: err}
	}
}

// receiveUsageShown puts the periods on screen, newest first.
func (m *Model) receiveUsageShown(msg usageShownMsg) tea.Cmd {
	mach := m.machine(msg.machine)
	if mach == nil {
		return nil
	}
	if msg.err != nil {
		m.showError(msg.err)
		return nil
	}
	m.flash = ""
	m.overlay = newNotice(" "+ansi.Truncate(mach.label, 30, "…")+" · usage ", usageLines(mach, msg.usage))
	if msg.usage.Known {
		mach.usage = &msg.usage
	}
	return nil
}

// usageLinesMax is how many periods are listed; a sandbox started and
// stopped all day has more than anybody reads.
const usageLinesMax = 12

// periodWhat says what the sandbox was doing for a billed period, naming
// only what the provider reported: a provider that charges for machine
// time alone says nothing about disk.
func periodWhat(p sandbox.UsagePeriod) string {
	var parts []string
	add := func(n int, unit string) {
		if n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, unit))
		}
	}
	add(p.CPU, "vCPU")
	add(p.MemGiB, "GiB")
	add(p.DiskGiB, "GiB disk")
	state := "stopped"
	if p.Running() {
		state = "running"
	}
	if len(parts) == 0 {
		return state
	}
	return state + " · " + strings.Join(parts, ", ")
}

// usageLines is what a sandbox has cost, and where it went.
func usageLines(mach *machine, u sandbox.Usage) []string {
	provider, id, _ := mach.sandbox()
	head := providerLabel(provider) + " " + id
	if !u.Known || len(u.Periods) == 0 {
		return []string{head, "",
			providerLabel(provider) + " has nothing to report for this sandbox yet.",
			"Its figures settle hours behind what is running, so a sandbox made today may not appear until tomorrow."}
	}
	lines := []string{head, "", fmt.Sprintf("%s since %s", money(u.Cost), u.From.Local().Format("2 Jan 15:04")), ""}
	periods := u.Periods
	if n := len(periods) - usageLinesMax; n > 0 {
		periods = periods[n:]
		lines = append(lines, fmt.Sprintf("… %d earlier periods", n))
	}
	disk := false
	for i := len(periods) - 1; i >= 0; i-- { // newest first
		p := periods[i]
		if !p.Running() && p.DiskGiB > 0 {
			disk = true
		}
		lines = append(lines, fmt.Sprintf("%s  %-8s  %s  %s",
			p.From.Local().Format("2 Jan 15:04"), shortDuration(p.To.Sub(p.From)), money(p.Cost), periodWhat(p)))
	}
	if disk {
		// Only where the provider charges for it: boat.dev keeps a stopped
		// sandbox's disk for nothing.
		return append(lines, "", "A stopped sandbox keeps its disk, and is charged for it, until it is deleted.")
	}
	return lines
}
