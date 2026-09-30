package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
)

// a3Skill is a machine's answer with one change per agent.
func a3Skill(actions ...[2]string) *proto.AgentSkillResult {
	res := &proto.AgentSkillResult{}
	for _, a := range actions {
		res.Changes = append(res.Changes, proto.SkillChange{
			Path: "~/." + a[0] + "/skills/conch/SKILL.md", Agents: []string{a[0]}, Action: a[1]})
	}
	return res
}

func a3Model(t *testing.T) *Model {
	t.Helper()
	t.Setenv("CONCH_HOME", t.TempDir())
	m := &Model{cfg: config.Default(), width: 100, height: 40}
	m.machines = []*machine{
		{id: localMachine, label: "local", state: stateOnline},
		{id: "box", label: "box", state: stateOnline},
	}
	m.skill = map[string]*skillPlan{}
	return m
}

func a3Rows(s *settings, m *Model) map[string]string {
	rows := map[string]string{}
	for _, it := range s.agentItems(m) {
		rows[ansi.Strip(it.label)] = ansi.Strip(it.detail)
	}
	return rows
}

func TestSkillPageReadsEachMachine(t *testing.T) {
	m := a3Model(t)
	s := &settings{tab: 2}

	// The tab offers it beside the library, and opening it is a page.
	row, ok := "", false
	for _, it := range s.agentItems(m) {
		if strings.HasPrefix(ansi.Strip(it.label), "conch's own skill") {
			row, ok = ansi.Strip(it.detail), true
		}
	}
	if !ok || !strings.Contains(row, "start, prompt and read") {
		t.Fatalf("the row on the tab: %q (found %v)", row, ok)
	}

	// A machine nobody has asked about yet says so rather than lying.
	s.openPage("skill")
	if got := a3Rows(s, m)["local"]; !strings.Contains(got, "enter reads it") {
		t.Fatalf("unread machine: %q", got)
	}

	// Every agent has it, one is the person's own.
	m.skill[localMachine] = &skillPlan{res: a3Skill(
		[2]string{"claude", proto.SyncSame}, [2]string{"codex", proto.SyncSame},
		[2]string{"gemini", proto.SyncSkip})}
	// Half installed on the other machine.
	m.skill["box"] = &skillPlan{res: a3Skill(
		[2]string{"claude", proto.SyncSame}, [2]string{"codex", proto.SyncCreate})}
	rows := a3Rows(s, m)
	if !strings.Contains(rows["local"], "every agent has it") || !strings.Contains(rows["local"], "1 yours") {
		t.Fatalf("local's summary: %q", rows["local"])
	}
	if !strings.Contains(rows["box"], "1 have it") || !strings.Contains(rows["box"], "1 not") {
		t.Fatalf("box's summary: %q", rows["box"])
	}

	// A machine's page names each agent and what would happen to it.
	s.openPage("skill:box")
	rows = a3Rows(s, m)
	if rows["claude"] != "✓ has it" || !strings.Contains(rows["codex"], "not installed") {
		t.Fatalf("box's page: %v", rows)
	}
	for _, want := range []string{"Install it for every agent…", "Take conch's copy away…", "Check again", "‹ Machines"} {
		if _, ok := rows[want]; !ok {
			t.Fatalf("no %q among %v", want, rows)
		}
	}

	// Writing asks first, naming every path it would write.
	for _, it := range s.agentItems(m) {
		if ansi.Strip(it.label) == "Install it for every agent…" {
			it.run(m)
		}
	}
	d, ok := m.overlay.(*dialog)
	if !ok {
		t.Fatalf("install opened %T, not a question", m.overlay)
	}
	text := strings.Join(d.text, "\n")
	if !strings.Contains(text, "~/.codex/skills/conch/SKILL.md") || strings.Contains(text, "~/.claude/skills") {
		t.Fatalf("the question names the wrong paths:\n%s", text)
	}
	m.overlay = nil

	// Nothing to do says so instead of asking.
	m.skill["box"] = &skillPlan{res: a3Skill([2]string{"claude", proto.SyncSame})}
	m.confirmSkill("box", false)
	if m.overlay != nil || !strings.Contains(m.flash, "nothing to do on box") {
		t.Fatalf("nothing to do: overlay %T flash %q", m.overlay, m.flash)
	}
}

func TestSkillPageEdges(t *testing.T) {
	m := a3Model(t)
	s := &settings{tab: 2}

	// A machine that is offline, one whose server is too old, one that read
	// nothing, and one with no agent that takes skills.
	m.machines[1].state = stateOffline
	if got := m.loadSkill("box", ""); got != nil {
		t.Fatal("an offline machine was asked anyway")
	}
	s.openPage("skill:box")
	if got := a3Rows(s, m)["  box is offline"]; got != "" {
		// the row is the label itself; just check it is there
	}
	if !strings.Contains(strings.Join(a3Labels(s, m), "|"), "box is offline") {
		t.Fatalf("offline page: %v", a3Labels(s, m))
	}
	m.skill["box"] = &skillPlan{err: "its server predates the skill; reload or upgrade it"}
	if !strings.Contains(strings.Join(a3Labels(s, m), "|"), "predates the skill") {
		t.Fatalf("old server: %v", a3Labels(s, m))
	}
	m.skill["box"] = &skillPlan{res: &proto.AgentSkillResult{}}
	if !strings.Contains(strings.Join(a3Labels(s, m), "|"), "no agent here reads skills") {
		t.Fatalf("no agents: %v", a3Labels(s, m))
	}
	m.skill["box"] = &skillPlan{loading: true}
	if !strings.Contains(strings.Join(a3Labels(s, m), "|"), "reading…") {
		t.Fatalf("loading: %v", a3Labels(s, m))
	}

	// A machine that has gone while its page was open falls back to the list.
	s.openPage("skill:nope")
	if s.agentItems(m); s.page != "skill" {
		t.Fatalf("a machine that is not there left %q open", s.page)
	}
	// esc from the list goes back to the tab.
	s.openPage("")
	if s.page != "" {
		t.Fatalf("page %q", s.page)
	}

	// An answer that failed is kept as the machine's reason, and one that
	// applied asks again so the rows stop saying "would".
	m.receiveSkill(skillMsg{machine: "box", err: errString("no")})
	if m.skill["box"].err != "no" {
		t.Fatalf("failed answer: %+v", m.skill["box"])
	}
	m.machines[1].state = stateOnline
	m.receiveSkill(skillMsg{machine: "box", res: *a3Skill([2]string{"claude", proto.SyncSame}), flash: "conch's skill installed on box"})
	if m.flash != "conch's skill installed on box" {
		t.Fatalf("flash %q", m.flash)
	}
}

func a3Labels(s *settings, m *Model) []string {
	var out []string
	for _, it := range s.agentItems(m) {
		out = append(out, ansi.Strip(it.label))
	}
	return out
}
