package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/sandbox"
)

// endpointProvider is a provider reached at an endpoint of your own, as
// sandbox-cli's sandboxd is; bind says whether it can mount a folder.
type endpointProvider struct {
	sbProvider
	bind bool
}

func (p *endpointProvider) Name() string     { return "sandbox-cli" }
func (p *endpointProvider) Endpoint() string { return "unix:///home/me/.config/sandbox/sandboxd.sock" }
func (p *endpointProvider) CanBind() bool    { return p.bind }

func trimmedLabels(d *dialog) string {
	var labels []string
	for _, f := range d.fields {
		labels = append(labels, strings.TrimSpace(f.label))
	}
	return strings.Join(labels, ",")
}

func TestSandboxDialogForAnEndpoint(t *testing.T) {
	a2Isolate(t)
	m := a2Model()
	p := &endpointProvider{bind: true}
	useSandboxProvider(t, p)

	d := newSandboxDialog(*m, "sandbox-cli")
	if got := trimmedLabels(d); got != "Label,Folder,Image,vCPUs,Memory GiB,Disk GiB,Pass in" {
		t.Fatalf("fields %s", got)
	}
	text := strings.Join(d.text, " ")
	if strings.Contains(text, "API key") || !strings.Contains(text, "at unix:///home/me/.config/sandbox/sandboxd.sock") {
		t.Fatalf("text %s", text)
	}
	// The folder is offered empty: mounting one is asked for, not assumed.
	if d.fields[1].in.Value() != "" || d.fields[1].in.Placeholder != "none · /workspace starts empty" {
		t.Fatalf("folder %q %q", d.fields[1].in.Value(), d.fields[1].in.Placeholder)
	}
	a2Run(d.submit(m, []string{"", " /work/api ", " img ", "2", "", "", ""}))
	if p.spec.Dir != "/work/api" || p.spec.Snapshot != "img" || p.spec.CPU != 2 {
		t.Fatalf("spec %+v", p.spec)
	}
	a2Run(d.submit(m, []string{"", "", "", "", "", "", ""}))
	if p.spec.Dir != "" {
		t.Fatalf("no folder, yet %q", p.spec.Dir)
	}

	// One that can't mount a folder (sandboxd elsewhere) isn't asked for one.
	p.bind = false
	d = newSandboxDialog(*m, "sandbox-cli")
	if got := trimmedLabels(d); got != "Label,Image,vCPUs,Memory GiB,Disk GiB,Pass in" {
		t.Fatalf("remote fields %s", got)
	}
	// Not running: says what is missing, and not that a key is.
	p.checkErr = fmt.Errorf("%w: sandboxd isn't running here", sandbox.ErrNotConfigured)
	if text := strings.Join(newSandboxDialog(*m, "sandbox-cli").text, " "); strings.Contains(text, "API key") || !strings.Contains(text, "sandboxd isn't running here.") {
		t.Fatalf("not running: %s", text)
	}
}

func TestSandboxFolderSuggestsTheProject(t *testing.T) {
	a2Isolate(t)
	m := a2Model()
	useSandboxProvider(t, &endpointProvider{bind: true})
	local := m.machine(localMachine)
	local.projects = []proto.ProjectInfo{{ID: "pr1", Name: "api", Path: "/work/api"}}
	m.rebuild()
	for _, r := range m.rows {
		if r.machine == localMachine && r.projectID == "pr1" {
			m.cursor = r.id
		}
	}
	if got := m.sandboxFolder(); got != "/work/api" {
		t.Fatalf("folder %q", got)
	}
	d := newSandboxDialog(*m, "sandbox-cli")
	if d.fields[1].in.Value() != "" || d.fields[1].in.Placeholder != "none, or e.g. /work/api" {
		t.Fatalf("suggested %q %q", d.fields[1].in.Value(), d.fields[1].in.Placeholder)
	}
	m.cursor = ""
	if got := m.sandboxFolder(); got != "" {
		t.Fatalf("no row: %q", got)
	}
}

// A sandbox that can only be deleted is stopped once, says why, and is
// then left to run: asking again every half minute would only fail again.
func TestSandboxThatCannotStop(t *testing.T) {
	m, mach := sandboxModel(t)
	p := &sbProvider{opErr: fmt.Errorf("%w: sandboxd can't suspend one", sandbox.ErrCannotStop)}
	useSandboxProvider(t, p)
	now := time.Now()
	mach.state, mach.since = stateOnline, now.Add(-2*time.Hour)
	cmd := m.watchIdleSandboxes(now)
	if cmd == nil {
		t.Fatal("not stopped")
	}
	for _, msg := range a2Run(cmd) {
		if done, ok := msg.(sandboxDoneMsg); ok {
			m.sandboxDone(done)
		}
	}
	if !mach.cannotStop || !strings.Contains(m.flash, "can't be stopped, only deleted") || mach.state != stateOnline {
		t.Fatalf("cannot stop: %v %q %v", mach.cannotStop, m.flash, mach.state)
	}
	if cmd := m.watchIdleSandboxes(now.Add(time.Hour)); cmd != nil {
		t.Fatal("asked to stop it again")
	}
}

func TestSettingsForAnEndpointProvider(t *testing.T) {
	m, _ := sandboxModel(t)
	p := &endpointProvider{}
	useSandboxProvider(t, p)
	s := &settings{}
	s.setTab(sandboxTab)
	s.open("sandbox-cli")
	plain := func() string {
		var b strings.Builder
		for _, it := range s.items(m) {
			b.WriteString(ansi.Strip(it.label) + "|" + ansi.Strip(it.detail) + "\n")
		}
		return b.String()
	}
	page := plain()
	for _, want := range []string{"sandbox-cli|✓ unix:///home/me/.config/sandbox/sandboxd.…", "Context|the one sandbox-cli is using",
		"Endpoint|from the context", "Token|not set · $SANDBOXD_TOKEN is used", "Image|sandbox-cli's default", "Network|sandboxd's default",
		"Allow|nothing", "Bring back what was running", "Stop when idle", "Pass in|nothing"} {
		if !strings.Contains(page, want) {
			t.Fatalf("page lacks %q:\n%s", want, page)
		}
	}
	for _, not := range []string{"API key", "Region", "Price"} {
		if strings.Contains(page, not) {
			t.Fatalf("page has %q:\n%s", not, page)
		}
	}
	item := func(label string) settingItem {
		for _, it := range s.items(m) {
			if ansi.Strip(it.label) == label {
				return it
			}
		}
		t.Fatalf("no %q", label)
		return settingItem{}
	}
	// Each is checked as it is set.
	for _, c := range []struct{ label, bad, want, good string }{
		{"Network", "wide", "give none, allowlist or open", "open"},
		{"Endpoint", "ftp://x", "give unix:///path", "https://box.test:7443"},
	} {
		item(c.label).run(m)
		d := m.overlay.(*dialog)
		if got := a2ErrText(a2Run(d.submit(m, []string{c.bad}))); !strings.Contains(got, c.want) {
			t.Fatalf("%s %q: %q", c.label, c.bad, got)
		}
		a2Run(d.submit(m, []string{c.good}))
	}
	item("Allow").run(m)
	a2Run(m.overlay.(*dialog).submit(m, []string{"api.anthropic.com, api.openai.com"}))
	cfg := m.cfg.Sandbox.Of("sandbox-cli")
	if cfg.Network != "open" || cfg.APIURL != "https://box.test:7443" || strings.Join(cfg.Allow, " ") != "api.anthropic.com api.openai.com" {
		t.Fatalf("saved %+v", cfg)
	}
	// Not running: the line says what is missing, not a key.
	p.checkErr = fmt.Errorf("%w: sandboxd isn't running here", sandbox.ErrNotConfigured)
	if got := ansi.Strip(providerState(m, "sandbox-cli")); got != "sandboxd isn't running here" {
		t.Fatalf("state %q", got)
	}
}
