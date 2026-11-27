package tui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/adapter"
	"github.com/Amitgb14/conch/internal/proto"
)

// acpModel is a2Model with this computer's server faked, answering for
// ACP agents, and the settings open on this computer's agents page.
func acpModel(t *testing.T, caps ...string) (*Model, *settings, *a1Peer) {
	t.Helper()
	a2Isolate(t)
	m := a2Model()
	c, peer := a1FakeClient(t, append([]string{"agent.install.v1"}, caps...)...)
	mach := m.machine(localMachine)
	mach.c = c
	mach.agentList = []proto.AgentAvailability{
		{Name: "claude", Label: "Claude Code", Installed: true, Version: "2.1.309", Tier: proto.TierSupported},
		{Name: "gemini-acp", Label: "Gemini (ACP)", Installed: true, Tier: proto.TierRunsHere, Protocol: proto.ProtocolACP, Sessions: true, Removable: true,
			ACP: &proto.AgentACPSpec{Name: "gemini-acp", Label: "Gemini (ACP)", Command: "gemini --experimental-acp", Dirs: []string{"~/.local/bin"}, Env: []string{"GEMINI_MODEL=pro"}}},
		{Name: "shipped-acp", Label: "Shipped", Installed: false, Tier: proto.TierRunsHere, Protocol: proto.ProtocolACP,
			ACP: &proto.AgentACPSpec{Name: "shipped-acp", Command: "shipped acp"}},
	}
	mach.available = map[string]proto.AgentAvailability{}
	for _, a := range mach.agentList {
		mach.available[a.Name] = a
	}
	s := &settings{tab: agentsTab, page: "agents:" + localMachine}
	m.overlay = s
	return m, s, peer
}

func itemLabels(items []settingItem) string {
	var out []string
	for _, it := range items {
		out = append(out, ansi.Strip(it.label+" | "+it.detail))
	}
	return strings.Join(out, "\n")
}

func acpParams[T any](t *testing.T, msg proto.Message) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(msg.Params, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestSettingsListsACPAgents(t *testing.T) {
	m, s, _ := acpModel(t, proto.CapAgentACPAdd)
	text := itemLabels(s.items(m))
	for _, want := range []string{"Over ACP", "Gemini (ACP) | gemini --experimental-acp · lists sessions · enter edits · d removes",
		"Shipped | shipped acp · no sessions · not installed", "+ Add an ACP agent… | a"} {
		if !strings.Contains(text, want) {
			t.Errorf("no %q in:\n%s", want, text)
		}
	}
	// Each once: an ACP agent is not also in the list above.
	if strings.Count(text, "Gemini (ACP)") != 1 {
		t.Errorf("listed twice:\n%s", text)
	}
	// A server that cannot add one says what would.
	m, s, _ = acpModel(t)
	if text := itemLabels(s.items(m)); !strings.Contains(text, "upgrade conch on") {
		t.Errorf("an old server's page:\n%s", text)
	}
	s.update(m, a2Key("a"))
	if _, ok := m.overlay.(*dialog); ok {
		t.Error("the form opened for a server that cannot add one")
	}
}

// TestAddACPAgentFromTheTUI: a opens the form, the first enter checks, the
// answer shows in it, the second adds — and a change after the check
// checks again rather than adding what was not checked.
func TestAddACPAgentFromTheTUI(t *testing.T) {
	m, s, peer := acpModel(t, proto.CapAgentACPAdd)
	peer.setResult(proto.MethodAgentACPCheck, proto.AgentACPCheck{Agent: "goose", Version: "1.18.0", ProtocolVersion: 1, Load: true, Auth: []string{"a key"}})
	peer.setResult(proto.MethodAgentACPAdd, proto.AgentAvailability{Name: "goose-acp", Label: "Goose", Installed: true, Protocol: proto.ProtocolACP})

	s.update(m, a2Key("a"))
	d, ok := m.overlay.(*dialog)
	if !ok {
		t.Fatalf("a opened %T", m.overlay)
	}
	a2Type(m, d, "goose acp")
	if got := d.fields[acpFieldName].in.Placeholder; got != "goose-acp" {
		t.Errorf("name placeholder %q", got)
	}
	d.setFocus(acpFieldLabel)
	a2Type(m, d, "Goose")

	// First enter: a check, the form kept.
	_, cmd := d.update(m, a2Key("enter"))
	if m.overlay != d || cmd == nil {
		t.Fatalf("enter did not check: overlay %T", m.overlay)
	}
	m.receiveACPCheck(cmd().(acpCheckMsg))
	text := ansi.Strip(strings.Join(d.text, "\n"))
	for _, want := range []string{"✓ speaks ACP", "goose 1.18.0", "✓ reopening sessions", "– no listing sessions", "may ask to sign in: a key", "enter adds it"} {
		if !strings.Contains(text, want) {
			t.Errorf("no %q in:\n%s", want, text)
		}
	}
	ck := acpParams[proto.AgentACPSpec](t, peer.waitFor(t, "check", func(msg proto.Message) bool { return msg.Method == proto.MethodAgentACPCheck }))
	if ck.Command != "goose acp" || ck.Name != "goose-acp" {
		t.Errorf("checked %+v", ck)
	}

	// A change after the check: the next enter checks again.
	d.setFocus(acpFieldEnv)
	a2Type(m, d, "GOOSE_X=1")
	if strings.Contains(ansi.Strip(strings.Join(d.text, "\n")), "speaks ACP") {
		t.Error("a check of other values still shown")
	}
	_, cmd = d.update(m, a2Key("enter"))
	if _, ok := cmd().(acpCheckMsg); !ok {
		t.Fatal("a changed form was added without a check")
	}
	m.overlay = d
	_, cmd = d.update(m, a2Key("enter"))
	m.receiveACPCheck(cmd().(acpCheckMsg))

	// Second enter: added.
	m.overlay = d
	_, cmd = d.update(m, a2Key("enter"))
	added, ok := cmd().(acpAddedMsg)
	if !ok {
		t.Fatalf("second enter did not add")
	}
	m.receiveACPAdded(added)
	if !strings.Contains(m.flash, "Goose added on local") {
		t.Errorf("flash %q", m.flash)
	}
	add := acpParams[proto.AgentACPAddParams](t, peer.waitFor(t, "add", func(msg proto.Message) bool { return msg.Method == proto.MethodAgentACPAdd }))
	if add.Name != "goose-acp" || add.Label != "Goose" || add.Command != "goose acp" || strings.Join(add.Env, " ") != "GOOSE_X=1" || add.Replace {
		t.Errorf("added %+v", add)
	}
	if m.overlay != s {
		t.Errorf("after adding, the overlay is %T, not the settings page", m.overlay)
	}
}

func TestACPFormRefusalsAndFailures(t *testing.T) {
	m, s, peer := acpModel(t, proto.CapAgentACPAdd)
	s.update(m, a2Key("a"))
	d := m.overlay.(*dialog)
	// No command: said in the form, nothing asked.
	if _, cmd := d.update(m, a2Key("enter")); cmd != nil || m.overlay != d {
		t.Error("an empty form was sent")
	}
	if !strings.Contains(ansi.Strip(strings.Join(d.text, "\n")), "a command is needed") {
		t.Errorf("text %q", d.text)
	}
	// A check that found a problem: said with the agent's last words, and
	// enter adds anyway.
	peer.setResult(proto.MethodAgentACPCheck, proto.AgentACPCheck{Problem: proto.ACPExited, Message: "it exited before answering initialize", Output: "Error: GEMINI_API_KEY is not set"})
	a2Type(m, d, "gemini --experimental-acp")
	_, cmd := d.update(m, a2Key("enter"))
	m.receiveACPCheck(cmd().(acpCheckMsg))
	text := ansi.Strip(strings.Join(d.text, "\n"))
	if !strings.Contains(text, "✗ it exited") || !strings.Contains(text, "GEMINI_API_KEY is not set") || !strings.Contains(text, "adds it anyway") {
		t.Errorf("problem shown as:\n%s", text)
	}
	// The add refused by the server: the form comes back with what was typed.
	peer.setError(proto.MethodAgentACPAdd, "an agent called gemini-acp was added here already")
	m.overlay = d
	_, cmd = d.update(m, a2Key("enter"))
	m.receiveACPAdded(cmd().(acpAddedMsg))
	if m.overlay != d || !strings.Contains(m.flash, "added here already") || d.fields[acpFieldCommand].in.Value() != "gemini --experimental-acp" {
		t.Errorf("after a refusal: overlay %T flash %q", m.overlay, m.flash)
	}
	// A check answered after the form was closed changes nothing.
	m.overlay = s
	m.receiveACPCheck(acpCheckMsg{d: d, form: &acpForm{}, res: proto.AgentACPCheck{Agent: "late"}})
	if strings.Contains(strings.Join(d.text, ""), "late") {
		t.Error("a late check was put in a closed form")
	}
}

func TestEditAndRemoveACPAgentFromTheTUI(t *testing.T) {
	m, s, peer := acpModel(t, proto.CapAgentACPAdd)
	peer.setResult(proto.MethodAgentACPCheck, proto.AgentACPCheck{Agent: "gemini", ProtocolVersion: 1})
	items := s.items(m)
	sel := -1
	for i, it := range items {
		if strings.HasPrefix(ansi.Strip(it.label), "Gemini (ACP)") {
			sel = i
		}
	}
	if sel < 0 {
		t.Fatal("no row")
	}
	s.sel = sel
	s.update(m, a2Key("enter"))
	d, ok := m.overlay.(*dialog)
	if !ok {
		t.Fatalf("enter opened %T", m.overlay)
	}
	if got := dialogValues(d); strings.Join(got, "|") != "gemini --experimental-acp|gemini-acp|Gemini (ACP)|~/.local/bin|GEMINI_MODEL=pro" {
		t.Errorf("form = %q", got)
	}
	// Even with the name field changed, it is this agent that changes.
	d.setFocus(acpFieldName)
	a2Type(m, d, "x")
	_, cmd := d.update(m, a2Key("enter"))
	m.receiveACPCheck(cmd().(acpCheckMsg))
	m.overlay = d
	_, cmd = d.update(m, a2Key("enter"))
	m.receiveACPAdded(cmd().(acpAddedMsg))
	add := acpParams[proto.AgentACPAddParams](t, peer.waitFor(t, "add", func(msg proto.Message) bool { return msg.Method == proto.MethodAgentACPAdd }))
	if add.Name != "gemini-acp" || !add.Replace || strings.Join(add.Dirs, ",") != "~/.local/bin" {
		t.Errorf("changed %+v", add)
	}

	// d removes, after asking.
	m.overlay = s
	s.sel = sel
	s.update(m, a2Key("d"))
	conf, ok := m.overlay.(*dialog)
	if !ok || !conf.confirm || !strings.Contains(strings.Join(conf.text, " "), "agents/gemini-acp.toml") {
		t.Fatalf("d opened %T %+v", m.overlay, conf)
	}
	m.receiveACPAdded(conf.submit(m, nil)().(acpAddedMsg))
	rm := acpParams[proto.AgentACPRemoveParams](t, peer.waitFor(t, "remove", func(msg proto.Message) bool { return msg.Method == proto.MethodAgentACPRemove }))
	if rm.Name != "gemini-acp" || !strings.Contains(m.flash, "removed") {
		t.Errorf("removed %+v, flash %q", rm, m.flash)
	}
	// A shipped one is neither edited nor removed.
	for i, it := range s.items(m) {
		if strings.HasPrefix(ansi.Strip(it.label), "Shipped") && (it.run != nil || it.del != nil) {
			t.Errorf("row %d of a shipped agent acts", i)
		}
	}
}

func TestACPNameFor(t *testing.T) {
	for cmd, want := range map[string]string{
		"gemini --experimental-acp": "gemini-acp", "goose acp": "goose-acp", "/opt/bin/Claude_Code_ACP": "claude-code-acp",
		"LANG=C codex-acp": "codex-acp", "": "", "  ": "", "!!!": "",
	} {
		if got := acpNameFor(cmd); got != want {
			t.Errorf("acpNameFor(%q) = %q, want %q", cmd, got, want)
		}
	}
	spec := acpSpecOf([]string{" goose acp ", "", "", "a, ,b", "A=1  B=2"})
	if spec.Name != "goose-acp" || strings.Join(spec.Dirs, "|") != "a|b" || strings.Join(spec.Env, "|") != "A=1|B=2" {
		t.Errorf("spec %+v", spec)
	}
}

// The form fits the screen at every width a dialog is drawn at.
func TestACPFormFits(t *testing.T) {
	for _, w := range []int{20, 40, 80, 200} {
		m, s, _ := acpModel(t, proto.CapAgentACPAdd)
		m.width, m.height = w, 30
		s.update(m, a2Key("a"))
		d := m.overlay.(*dialog)
		d.text = append(d.text, acpCheckLines(proto.AgentACPCheck{Problem: proto.ACPNotACP, Message: strings.Repeat("long ", 30), Output: strings.Repeat("x", 300)})...)
		for i, l := range d.render(*m).lines {
			if ansi.StringWidth(l) > w {
				t.Errorf("width %d: line %d is %d wide", w, i, ansi.StringWidth(l))
			}
		}
	}
}

// The agent menu ends with a way to add one, where the server can.
func TestAgentMenuAddsAnACPAgent(t *testing.T) {
	m, _, _ := acpModel(t, proto.CapAgentACPAdd)
	mu := newAgentMenu(*m, m.machine(localMachine))
	last := mu.items[len(mu.items)-1]
	if last.key != "+" || !strings.Contains(ansi.Strip(last.label), "Add an ACP agent…") {
		t.Fatalf("last item %q %q", last.key, ansi.Strip(last.label))
	}
	text := ""
	for _, it := range mu.items {
		text += ansi.Strip(it.label) + "\n"
	}
	if !strings.Contains(text, "Start Gemini (ACP)  over ACP") {
		t.Errorf("menu:\n%s", text)
	}
	m.overlay = mu
	mu.update(m, a2Key("+"))
	if d, ok := m.overlay.(*dialog); !ok || !strings.Contains(d.title, "Add an ACP agent") {
		t.Errorf("+ opened %T", m.overlay)
	}
	m, _, _ = acpModel(t)
	mu = newAgentMenu(*m, m.machine(localMachine))
	if mu.items[len(mu.items)-1].key == "+" {
		t.Error("offered to a server that cannot add one")
	}
}

func TestACPCheckSaysHowToInstall(t *testing.T) {
	text := ansi.Strip(strings.Join(acpCheckLines(proto.AgentACPCheck{Problem: proto.ACPNotFound,
		Message: "claude-agent-acp was not found on PATH for your login shell", Install: "npm install -g @agentclientprotocol/claude-agent-acp"}), "\n"))
	if !strings.Contains(text, "✗ claude-agent-acp was not found") || !strings.Contains(text, "install it: npm install -g @agentclientprotocol/claude-agent-acp") {
		t.Errorf("lines:\n%s", text)
	}
	if text := ansi.Strip(strings.Join(acpCheckLines(proto.AgentACPCheck{Problem: proto.ACPNotFound, Message: "robo was not found"}), "\n")); strings.Contains(text, "install it") {
		t.Errorf("an install offered for a command conch does not know:\n%s", text)
	}
}

// The form waits longer than the server gives an agent to answer, plus a
// login shell's look for it first: a slow agent is said to be slow by
// the server, not cut off by the form. And the server gives one a minute
// — claude-agent-acp 0.113 took 14 s to answer initialize the first time.
func TestACPCheckWaitsForASlowAgent(t *testing.T) {
	if adapter.ProbeTimeout() < time.Minute {
		t.Errorf("an agent has %s to answer; a node adapter's first start took 14 s", adapter.ProbeTimeout())
	}
	if acpCheckWait < adapter.ProbeTimeout()+20*time.Second {
		t.Errorf("the form waits %s, the server %s", acpCheckWait, adapter.ProbeTimeout())
	}
}
