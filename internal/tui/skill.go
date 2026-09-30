package tui

import (
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/proto"
)

// conch's own skill, per machine: whether each agent there has it, and
// installing or removing it. The command line plans first and writes only
// with -apply, and so does this — the page is the plan, and the row that
// writes says what it would write before it does.
//
// A machine at a time, through that machine's server, because the skill
// goes in the agents' folders on that machine.

// skillPlan is one machine's answer, as last read.
type skillPlan struct {
	res     *proto.AgentSkillResult
	err     string // why it could not be read, or ""
	loading bool
}

type skillMsg struct {
	machine string
	res     proto.AgentSkillResult
	err     error
	flash   string
}

// loadSkill asks a machine what installing the skill would do, writing
// nothing. A server too old to know the method says so on its own row
// rather than failing the page.
func (m *Model) loadSkill(mid string, flash string) tea.Cmd {
	if m.skill == nil {
		m.skill = map[string]*skillPlan{}
	}
	mach := m.machine(mid)
	c := m.clientOf(mid)
	switch {
	case mach == nil || c == nil:
		m.skill[mid] = &skillPlan{err: m.offlineText(mid)}
		return nil
	case len(c.MissingCapabilities([]string{proto.CapAgentSkill})) > 0:
		m.skill[mid] = &skillPlan{err: "its server predates the skill; reload or upgrade it"}
		return nil
	}
	m.skill[mid] = &skillPlan{loading: true, res: m.skillRes(mid)}
	return func() tea.Msg {
		var res proto.AgentSkillResult
		err := callCtx(c, proto.MethodAgentSkill, proto.AgentSkillParams{}, &res)
		return skillMsg{machine: mid, res: res, err: err, flash: flash}
	}
}

// applySkill writes the skill on a machine, or takes conch's copy away.
// agents narrows it to some of them — the setup view acts on the agent
// whose tab is open, the settings page on all of them.
func (m *Model) applySkill(mid string, remove bool, agents ...string) tea.Cmd {
	c := m.clientOf(mid)
	if c == nil {
		m.setFlash(m.offlineText(mid), true)
		return nil
	}
	what := "installed"
	if remove {
		what = "removed"
	}
	label := mid
	if mach := m.machine(mid); mach != nil {
		label = mach.label
	}
	return func() tea.Msg {
		var res proto.AgentSkillResult
		if err := callCtx(c, proto.MethodAgentSkill, proto.AgentSkillParams{Agents: agents, Remove: remove, Apply: true}, &res); err != nil {
			return skillMsg{machine: mid, err: err}
		}
		return skillMsg{machine: mid, res: res, flash: "conch's skill " + what + " on " + label}
	}
}

func (m *Model) receiveSkill(msg skillMsg) tea.Cmd {
	if m.skill == nil {
		m.skill = map[string]*skillPlan{}
	}
	if msg.err != nil {
		m.skill[msg.machine] = &skillPlan{err: msg.err.Error()}
		return nil
	}
	res := msg.res
	m.skill[msg.machine] = &skillPlan{res: &res}
	if msg.flash != "" {
		m.setFlash(msg.flash, false)
		// What was just written is the new plan; ask again so the rows
		// say "has it" rather than "would be written".
		return m.loadSkill(msg.machine, "")
	}
	return nil
}

// skillRes is a machine's last answer, or nil.
func (m Model) skillRes(mid string) *proto.AgentSkillResult {
	if p := m.skill[mid]; p != nil {
		return p.res
	}
	return nil
}

// skillCounts is how many of a plan's paths conch has written, how many it
// would write, and how many are somebody's own skill of that name.
func skillCounts(res *proto.AgentSkillResult) (have, todo, theirs int) {
	if res == nil {
		return 0, 0, 0
	}
	for _, ch := range res.Changes {
		switch ch.Action {
		case proto.SyncSame:
			have++
		case proto.SyncCreate, proto.SyncUpdate:
			todo++
		case proto.SyncSkip:
			theirs++
		}
	}
	return have, todo, theirs
}

// skillSummary is a machine's row on the skill page: what a person wants to
// know before opening it.
func (m Model) skillSummary(mid string) string {
	p := m.skill[mid]
	switch {
	case p == nil:
		return styleMuted.Render("enter reads it")
	case p.err != "":
		return styleWarn.Render(p.err)
	case p.loading && p.res == nil:
		return styleMuted.Render("reading…")
	}
	have, todo, theirs := skillCounts(p.res)
	var parts []string
	switch {
	case have > 0 && todo == 0:
		parts = append(parts, styleOK.Render("✓ every agent has it"))
	case have > 0:
		parts = append(parts, styleOK.Render(itoa(have)+" have it"), styleWarn.Render(itoa(todo)+" not"))
	case todo > 0:
		parts = append(parts, styleWarn.Render("not installed"))
	default:
		parts = append(parts, styleMuted.Render("no agent here reads skills"))
	}
	if theirs > 0 {
		parts = append(parts, styleMuted.Render(itoa(theirs)+" yours"))
	}
	return strings.Join(parts, styleMuted.Render(" · "))
}

// skillAction reads one change the way a person would say it.
func skillAction(ch proto.SkillChange) string {
	switch {
	case ch.Error != "":
		return styleErr.Render("failed: " + ch.Error)
	case ch.Action == proto.SyncSame:
		return styleOK.Render("✓ has it")
	case ch.Action == proto.SyncCreate:
		return styleWarn.Render("not installed · enter writes it")
	case ch.Action == proto.SyncUpdate:
		return styleWarn.Render("older copy · enter rewrites it")
	case ch.Action == proto.SyncRemove:
		return styleWarn.Render("conch's copy · enter takes it away")
	case ch.Action == proto.SyncSkip:
		detail := ch.Detail
		if detail == "" {
			detail = "yours, left alone"
		}
		return styleMuted.Render(detail)
	}
	return styleMuted.Render(ch.Action)
}

// skillItems is the page listing the machines, and skillMachineItems one
// machine's agents. Both follow the agents pages beside them.
func (s *settings) skillItems(m *Model) []settingItem {
	items := []settingItem{
		{header: true, label: "conch's own skill", detail: "so an agent can start, prompt and read another"},
		{label: styleMuted.Render("‹ Agents"), detail: styleMuted.Render("esc"),
			run: func(m *Model) tea.Cmd { s.openPage(""); return nil }},
		{label: styleMuted.Render("  it teaches the agents conch's own commands · docs: Agents working together")},
		{},
	}
	for _, mach := range m.machines {
		mach := mach
		items = append(items, settingItem{label: mach.label, detail: m.skillSummary(mach.id), page: true,
			run: func(m *Model) tea.Cmd {
				s.openPage("skill:" + mach.id)
				return m.loadSkill(mach.id, "")
			}})
	}
	return items
}

func (s *settings) skillMachineItems(m *Model, mid string) []settingItem {
	mach := m.machine(mid)
	if mach == nil {
		s.openPage("skill")
		return s.skillItems(m)
	}
	items := []settingItem{
		{header: true, label: "conch's skill · " + mach.label, detail: m.skillSummary(mid)},
		{label: styleMuted.Render("‹ Machines"), detail: styleMuted.Render("esc"),
			run: func(m *Model) tea.Cmd { s.openPage("skill"); return nil }},
	}
	p := m.skill[mid]
	switch {
	case p == nil || (p.loading && p.res == nil):
		return append(items, settingItem{label: styleMuted.Render("  reading…")})
	case p.err != "":
		return append(items, settingItem{label: styleWarn.Render("  " + p.err)})
	case len(p.res.Changes) == 0:
		return append(items, settingItem{label: styleMuted.Render("  no agent here reads skills")})
	}
	for _, ch := range p.res.Changes {
		ch := ch
		who := strings.Join(ch.Agents, ", ")
		if who == "" {
			who = ch.Path
		}
		item := settingItem{label: who, detail: skillAction(ch)}
		if ch.Action == proto.SyncCreate || ch.Action == proto.SyncUpdate {
			item.run = func(m *Model) tea.Cmd { return m.confirmSkill(mid, false) }
		}
		items = append(items, item)
	}
	have, todo, _ := skillCounts(p.res)
	items = append(items, settingItem{})
	if todo > 0 {
		items = append(items, settingItem{label: "Install it for every agent…", detail: styleMuted.Render("says what it writes first"),
			run: func(m *Model) tea.Cmd { return m.confirmSkill(mid, false) }})
	}
	if have > 0 {
		items = append(items, settingItem{label: "Take conch's copy away…", detail: styleMuted.Render("leaves a skill of your own alone"),
			run: func(m *Model) tea.Cmd { return m.confirmSkill(mid, true) }})
	}
	return append(items, settingItem{label: "Check again", detail: styleMuted.Render("on " + mach.label),
		run: func(m *Model) tea.Cmd { return m.loadSkill(mid, "") }})
}

// confirmSkill asks before writing, naming every path, since these are
// files in the agents' own folders. agents narrows it to some of them.
func (m *Model) confirmSkill(mid string, remove bool, agents ...string) tea.Cmd {
	p := m.skill[mid]
	if p == nil || p.res == nil {
		return m.loadSkill(mid, "")
	}
	label := mid
	if mach := m.machine(mid); mach != nil {
		label = mach.label
	}
	var paths []string
	for _, ch := range p.res.Changes {
		if len(agents) > 0 && !slices.ContainsFunc(ch.Agents, func(a string) bool { return slices.Contains(agents, a) }) {
			continue
		}
		switch {
		case remove && ch.Action == proto.SyncSame,
			!remove && (ch.Action == proto.SyncCreate || ch.Action == proto.SyncUpdate):
			paths = append(paths, ch.Path)
		}
	}
	if len(paths) == 0 {
		m.setFlash("nothing to do on "+label, false)
		return nil
	}
	what := "Write conch's skill to"
	if remove {
		what = "Take conch's skill from"
	}
	question := what + " " + count(len(paths), "file") + " on " + label + "?\n  " + strings.Join(paths, "\n  ")
	m.overlay = newConfirm(question, func(m *Model) tea.Cmd { return m.applySkill(mid, remove, agents...) })
	return nil
}

// skillFor is the change covering one agent on a machine, and whether the
// machine has been asked at all.
func (m Model) skillFor(mid, agent string) (proto.SkillChange, bool) {
	p := m.skill[mid]
	if p == nil || p.res == nil {
		return proto.SkillChange{}, false
	}
	for _, ch := range p.res.Changes {
		if slices.Contains(ch.Agents, agent) {
			return ch, true
		}
	}
	return proto.SkillChange{}, false
}

// skillLine is what the setup view says about conch's skill for the agent
// whose tab is open: one line, in the same shape as the groups above it.
func (m Model) skillLine(mid, agent string) string {
	p := m.skill[mid]
	switch {
	case p != nil && p.err != "":
		return styleMuted.Render("   conch's skill  ") + styleWarn.Render(p.err)
	case p == nil || (p.loading && p.res == nil):
		return styleMuted.Render("   conch's skill  reading…")
	}
	ch, ok := m.skillFor(mid, agent)
	if !ok {
		return styleMuted.Render("   conch's skill  this agent does not read skills")
	}
	hint := ""
	switch ch.Action {
	case proto.SyncSame:
		hint = styleMuted.Render(" · S takes it away")
	case proto.SyncCreate, proto.SyncUpdate:
		hint = styleMuted.Render(" · S installs it")
	}
	return styleMuted.Render("   conch's skill  ") + skillAction(ch) + hint
}
