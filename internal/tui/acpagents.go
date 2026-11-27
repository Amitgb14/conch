package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/proto"
)

// Settings › Agents › a machine's page lists the agents it runs over ACP
// and adds one: the server there writes the manifest and offers the
// agent at once (agent.acp.add.v1), so the same form works for a remote
// machine as for this one.
//
// The form's first Enter is a Check — the command started once there and
// asked what it offers — whose answer is put in the form; the next Enter
// adds it, whatever the check said, since an agent not signed in yet, or
// not installed yet, is still worth describing. Changing a field after a
// check makes the next Enter check again.

// The form's fields, in order.
const (
	acpFieldCommand = iota
	acpFieldName
	acpFieldLabel
	acpFieldDirs
	acpFieldEnv
)

// acpForm is what an open form has checked so far.
type acpForm struct {
	machine string
	replace bool
	name    string // the agent being changed, whose name stays
	// checked is the values the last check was for, and res its answer.
	checked []string
	res     *proto.AgentACPCheck
	intro   []string
}

type acpCheckMsg struct {
	d      *dialog
	form   *acpForm
	values []string
	res    proto.AgentACPCheck
	err    error
}

type acpAddedMsg struct {
	machine, verb string
	av            proto.AgentAvailability
	err           error
	d             *dialog // reopened with the error, keeping what was typed
}

// canAddACP is whether machine mid's server adds ACP agents.
func (m *Model) canAddACP(mid string) bool {
	c := m.clientOf(mid)
	return c != nil && len(c.MissingCapabilities([]string{proto.CapAgentACPAdd})) == 0
}

// acpNameFor is the name a command suggests: its program and "-acp".
func acpNameFor(command string) string {
	words := strings.Fields(command)
	for len(words) > 1 && strings.Contains(words[0], "=") {
		words = words[1:]
	}
	if len(words) == 0 {
		return ""
	}
	var b strings.Builder
	for _, r := range strings.ToLower(filepath.Base(words[0])) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case b.Len() > 0 && !strings.HasSuffix(b.String(), "-"):
			b.WriteByte('-')
		}
	}
	name := strings.Trim(b.String(), "-")
	if name == "" {
		return ""
	}
	if !strings.HasSuffix(name, "-acp") {
		name += "-acp"
	}
	return name
}

// acpSpecOf reads the form's values.
func acpSpecOf(values []string) proto.AgentACPSpec {
	v := func(i int) string {
		if i < len(values) {
			return strings.TrimSpace(values[i])
		}
		return ""
	}
	spec := proto.AgentACPSpec{Command: v(acpFieldCommand), Name: v(acpFieldName), Label: v(acpFieldLabel)}
	if spec.Name == "" {
		spec.Name = acpNameFor(spec.Command)
	}
	for _, d := range strings.Split(v(acpFieldDirs), ",") {
		if d = strings.TrimSpace(d); d != "" {
			spec.Dirs = append(spec.Dirs, d)
		}
	}
	spec.Env = strings.Fields(v(acpFieldEnv))
	return spec
}

// acpCheckLines says what a check found, for the form.
func acpCheckLines(res proto.AgentACPCheck) []string {
	if res.Problem != "" {
		lines := []string{styleErr.Render("✗ " + res.Message)}
		if out := strings.TrimSpace(res.Output); out != "" {
			for _, l := range strings.Split(out, "\n") {
				lines = append(lines, styleMuted.Render("  "+l))
			}
		}
		return append(lines, styleMuted.Render("enter adds it anyway · change a field to check again"))
	}
	who := strings.TrimSpace(res.Agent + " " + res.Version)
	if who == "" {
		who = "it"
	}
	head := styleOK.Render("✓ speaks ACP") + styleMuted.Render(fmt.Sprintf(" · %s · protocol %d", who, res.ProtocolVersion))
	yes := func(ok bool, what string) string {
		if ok {
			return styleOK.Render("✓ ") + what
		}
		return styleMuted.Render("– no " + what)
	}
	lines := []string{head, "  " + yes(res.Load, "reopening sessions") + "   " + yes(res.List, "listing sessions")}
	if len(res.Auth) > 0 {
		lines = append(lines, styleWarn.Render("  may ask to sign in: "+strings.Join(res.Auth, ", ")))
	}
	return append(lines, styleMuted.Render("enter adds it"))
}

// openACPForm is the Add (or, with spec, Edit) an ACP agent form for
// machine mid, coming back to back when it closes.
func (m *Model) openACPForm(mid string, spec *proto.AgentACPSpec, back overlay) tea.Cmd {
	where := mid
	if mach := m.machine(mid); mach != nil {
		where = mach.label
	}
	if !m.canAddACP(mid) {
		m.setFlash("conch on "+where+" is too old to add an agent from here: upgrade it, or use conch agent add -acp there", true)
		return nil
	}
	form := &acpForm{machine: mid, intro: []string{
		"An agent that speaks the Agent Client Protocol on its stdin and stdout. conch runs it in a pane and draws the conversation.",
		"Check the command against the agent's --help: gemini --experimental-acp, goose acp.",
	}}
	values := []string{}
	title := " Add an ACP agent on " + where + " "
	if spec != nil {
		form.replace, form.name = true, spec.Name
		title = " " + firstNonEmpty(spec.Label, spec.Name) + " on " + where + " "
		values = []string{spec.Command, spec.Name, spec.Label, strings.Join(spec.Dirs, ", "), strings.Join(spec.Env, " ")}
	}
	d := newDialog(*m, title, form.intro, []string{"Command", "Name", "Shown as", "Folders", "Environment"}, values)
	d.fields[acpFieldCommand].in.Placeholder = "gemini --experimental-acp"
	d.fields[acpFieldName].in.Placeholder = "from the command"
	d.fields[acpFieldLabel].in.Placeholder = "defaults to the name"
	d.fields[acpFieldDirs].in.Placeholder = "optional: folders to look in before PATH, comma between"
	d.fields[acpFieldEnv].in.Placeholder = "optional: NAME=value, a space between"
	d.back = back
	d.onChange = func(d *dialog) {
		d.fields[acpFieldName].in.Placeholder = firstNonEmpty(acpNameFor(d.fields[acpFieldCommand].in.Value()), "from the command")
		if form.res != nil && !sameValues(form.checked, dialogValues(d)) {
			form.res, d.text = nil, form.intro // what it checked is not what is typed now
		}
	}
	d.submit = func(m *Model, values []string) tea.Cmd { return m.submitACPForm(d, form, values) }
	m.overlay = d
	return d.focusCmd()
}

func dialogValues(d *dialog) []string {
	out := make([]string, len(d.fields))
	for i, f := range d.fields {
		out[i] = f.in.Value()
	}
	return out
}

func sameValues(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if strings.TrimSpace(a[i]) != strings.TrimSpace(b[i]) {
			return false
		}
	}
	return true
}

// submitACPForm is Enter in the form: a check first, then the add.
func (m *Model) submitACPForm(d *dialog, form *acpForm, values []string) tea.Cmd {
	spec := acpSpecOf(values)
	if form.name != "" {
		spec.Name = form.name // the file's name: another would be another agent
	}
	if spec.Command == "" {
		m.overlay = d
		d.text = append(append([]string{}, form.intro...), styleErr.Render("✗ a command is needed: the one that speaks ACP"))
		return nil
	}
	c := m.clientOf(form.machine)
	if c == nil {
		m.overlay = d
		m.setFlash(m.offlineText(form.machine), true)
		return nil
	}
	if form.res == nil || !sameValues(form.checked, values) {
		m.overlay = d
		d.text = append(append([]string{}, form.intro...), styleMuted.Render("checking "+spec.Command+"…"))
		return func() tea.Msg {
			var res proto.AgentACPCheck
			err := callCtxFor(c, proto.MethodAgentACPCheck, spec, &res, acpCheckWait)
			return acpCheckMsg{d: d, form: form, values: values, res: res, err: err}
		}
	}
	verb := "added"
	if form.replace {
		verb = "changed"
	}
	mid := form.machine
	return func() tea.Msg {
		var av proto.AgentAvailability
		err := callCtx(c, proto.MethodAgentACPAdd, proto.AgentACPAddParams{AgentACPSpec: spec, Replace: form.replace}, &av)
		return acpAddedMsg{machine: mid, verb: verb, av: av, err: err, d: d}
	}
}

// acpCheckWait is how long the form waits on a check: the server gives
// the agent 20 seconds to answer, after looking for it.
const acpCheckWait = 45 * time.Second

func (m *Model) receiveACPCheck(msg acpCheckMsg) tea.Cmd {
	if m.overlay != msg.d {
		return nil // the form was closed meanwhile
	}
	if msg.err != nil {
		msg.d.text = append(append([]string{}, msg.form.intro...), styleErr.Render("✗ "+msg.err.Error()))
		return nil
	}
	res := msg.res
	msg.form.res, msg.form.checked = &res, msg.values
	msg.d.text = append(append([]string{}, msg.form.intro...), acpCheckLines(res)...)
	return nil
}

func (m *Model) receiveACPAdded(msg acpAddedMsg) tea.Cmd {
	if msg.err != nil {
		if msg.d != nil {
			m.overlay = msg.d
		}
		m.setFlash(msg.err.Error(), true)
		return nil
	}
	where := msg.machine
	var cmd tea.Cmd
	if mach := m.machine(msg.machine); mach != nil {
		where = mach.label
		cmd = mach.checkAgents()
	}
	note := " — in the c menu now"
	if !msg.av.Installed && msg.verb != "removed" {
		note = " — not installed there yet"
	}
	if msg.verb == "removed" {
		note = ""
	}
	m.setFlash(fmt.Sprintf("%s %s on %s%s", firstNonEmpty(msg.av.Label, msg.av.Name), msg.verb, where, note), false)
	return cmd
}

// removeACP asks before taking an ACP agent's manifest away.
func (m *Model) removeACP(mid string, a proto.AgentAvailability, back overlay) tea.Cmd {
	c := m.clientOf(mid)
	if c == nil {
		m.setFlash(m.offlineText(mid), true)
		return nil
	}
	label := firstNonEmpty(a.Label, a.Name)
	d := newConfirm(fmt.Sprintf("Remove %s? Its manifest agents/%s.toml goes; the program and its sessions stay.", label, a.Name),
		func(m *Model) tea.Cmd {
			return func() tea.Msg {
				err := callCtx(c, proto.MethodAgentACPRemove, proto.AgentACPRemoveParams{Name: a.Name}, nil)
				return acpAddedMsg{machine: mid, verb: "removed", av: a, err: err}
			}
		})
	d.back = back
	m.overlay = d
	return nil
}

// acpItems is the Over ACP section of machine mid's agents page.
func (s *settings) acpItems(m *Model, mid string) []settingItem {
	mach := m.machine(mid)
	if mach == nil {
		return nil
	}
	items := []settingItem{{}, {header: true, label: "Over ACP", detail: "agents that speak the Agent Client Protocol"}}
	for _, a := range mach.agentList {
		if a.Protocol != proto.ProtocolACP {
			continue
		}
		a := a
		label := firstNonEmpty(a.Label, a.Name)
		what := ""
		if a.ACP != nil {
			what = a.ACP.Command
		}
		if a.SessionsUnread() {
			what += " · no sessions"
		} else {
			what += " · lists sessions"
		}
		if !a.Installed {
			what += " · not installed"
		}
		it := settingItem{label: label, detail: styleMuted.Render(what)}
		if a.Removable {
			it.detail += styleMuted.Render(" · enter edits · d removes")
			it.run = func(m *Model) tea.Cmd {
				spec := proto.AgentACPSpec{Name: a.Name, Label: a.Label, Command: what}
				if a.ACP != nil {
					spec = *a.ACP
				}
				return m.openACPForm(mid, &spec, s)
			}
			it.del = func(m *Model) tea.Cmd { return m.removeACP(mid, a, s) }
		}
		items = append(items, it)
	}
	add := settingItem{label: "+ Add an ACP agent…", detail: styleMuted.Render("a"),
		run: func(m *Model) tea.Cmd { return m.openACPForm(mid, nil, s) }}
	if !m.canAddACP(mid) {
		add.detail = styleMuted.Render("upgrade conch on " + mach.label + " to add one here")
	}
	return append(items, add)
}
