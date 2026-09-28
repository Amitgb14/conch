package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/proto"
)

// Giving one agent's setup to the others, from the setup view: the agent
// whose tab is open is the one it comes from, and everything it would
// write is shown before anything is written. What it did can be undone.

// syncMax is how many changes the question lists before counting the rest:
// a dialog nobody can read is not a dialog anybody answers.
const syncMax = 12

type setupSyncMsg struct {
	view *setupView
	res  proto.AgentSyncResult
	err  error
}

// syncSetup asks what it would take to give the other agents the setup of
// the agent whose tab is open. Nothing is written by this call.
func (v *setupView) syncSetup(m *Model, apply bool, undo bool) tea.Cmd {
	c := m.clientOf(v.mid)
	if c == nil {
		m.setFlash(m.offlineText(v.mid), true)
		return nil
	}
	if len(c.MissingCapabilities([]string{proto.CapAgentSync})) > 0 {
		m.setFlash("the server on this machine is too old to sync agent setup; reload it with u", true)
		return nil
	}
	from := v.agentName()
	if from == "" && !undo {
		return nil
	}
	params := proto.AgentSyncParams{Dir: v.dir, From: from, Apply: apply, Undo: undo}
	v.loading = true
	return func() tea.Msg {
		var res proto.AgentSyncResult
		err := callCtx(c, proto.MethodAgentSync, params, &res)
		return setupSyncMsg{view: v, res: res, err: err}
	}
}

// agentName is the agent whose tab is open.
func (v *setupView) agentName() string {
	if v.res == nil || v.tab >= len(v.res.Agents) {
		return ""
	}
	return v.res.Agents[v.tab].Agent
}

// receiveSync shows a plan and asks, says what came of applying one, or
// says what undoing put back.
func (v *setupView) receiveSync(m *Model, msg setupSyncMsg) tea.Cmd {
	v.loading = false
	if msg.err != nil {
		m.showError(msg.err)
		return nil
	}
	res := msg.res
	switch {
	case res.Undone:
		m.setFlash(fmt.Sprintf("put sync %s back · %s", res.Undo, counted(doneCount(res), "file")), false)
		return v.load(m)
	case res.Applied:
		text := fmt.Sprintf("gave %s %s's setup · %s", listAgents(res.To), agentLabel(res.From), counted(doneCount(res), "change"))
		if failed := failedSync(res); len(failed) > 0 {
			m.setFlash(text+" · "+strings.Join(failed, "; "), true)
			return v.load(m)
		}
		if res.Undo != "" {
			text += " · u undoes it"
		}
		m.setFlash(text, false)
		return v.load(m)
	}
	// A plan: show what it would do, and ask.
	if syncWrites(res) == 0 {
		m.setFlash(agentLabel(res.From)+"'s setup is already in every agent here: "+syncWhyNot(res), false)
		return nil
	}
	d := newConfirm("", func(m *Model) tea.Cmd { return v.syncSetup(m, true, false) })
	d.title = " Sync agent setup "
	d.text = syncLines(res)
	m.overlay = d
	return nil
}

// syncLines is the question: what would be written, where, and for whom.
func syncLines(res proto.AgentSyncResult) []string {
	lines := []string{fmt.Sprintf("Give %s %s's setup in this checkout?", listAgents(res.To), agentLabel(res.From)),
		""}
	lines = append(lines, syncChangeLines(res)...)
	return append(lines, "", "Files somebody wrote by hand are left alone, skills are linked rather than copied, and this can be undone with u.")
}

// syncChangeLines lists what a plan writes, and what it leaves out.
func syncChangeLines(res proto.AgentSyncResult) []string {
	var lines []string
	shown := 0
	for _, c := range res.Changes {
		if !writesChange(c) {
			continue
		}
		if shown == syncMax {
			lines = append(lines, fmt.Sprintf("  … %d more", syncWrites(res)-shown))
			break
		}
		shown++
		what := fmt.Sprintf("  %s %s → %s", c.Action, c.Name, c.Path)
		if c.Name == c.Path || c.Path == "" {
			what = fmt.Sprintf("  %s %s", c.Action, firstNonEmpty(c.Path, c.Name))
		}
		if c.Detail != "" {
			what += " (" + c.Detail + ")"
		}
		lines = append(lines, what)
	}
	if skipped := syncSkipped(res); skipped != "" {
		lines = append(lines, "", skipped)
	}
	return lines
}

// syncSkipped says what is being left out and why, once per reason: a
// secret conch won't copy is the one people need to know about.
func syncSkipped(res proto.AgentSyncResult) string {
	reasons := map[string]int{}
	var order []string
	for _, c := range res.Changes {
		if c.Action != proto.SyncSkip || c.Detail == "" {
			continue
		}
		if reasons[c.Detail] == 0 {
			order = append(order, c.Detail)
		}
		reasons[c.Detail]++
	}
	var parts []string
	for _, r := range order {
		parts = append(parts, fmt.Sprintf("%s: %s", counted(reasons[r], "item"), r))
	}
	if len(parts) == 0 {
		return ""
	}
	return "Left out — " + strings.Join(parts, "; ")
}

// syncWhyNot says why a plan has nothing to do.
func syncWhyNot(res proto.AgentSyncResult) string {
	same, skipped := 0, 0
	for _, c := range res.Changes {
		switch c.Action {
		case proto.SyncSame:
			same++
		case proto.SyncSkip:
			skipped++
		}
	}
	switch {
	case skipped > 0 && same > 0:
		return fmt.Sprintf("%s already there, %s left alone", counted(same, "item"), counted(skipped, "item"))
	case skipped > 0:
		return counted(skipped, "item") + " left alone"
	}
	return counted(same, "item") + " already there"
}

func writesChange(c proto.SyncChange) bool {
	switch c.Action {
	case proto.SyncCreate, proto.SyncUpdate, proto.SyncLink, proto.SyncRemove:
		return true
	}
	return false
}

// syncWrites counts what a plan would change.
func syncWrites(res proto.AgentSyncResult) int {
	n := 0
	for _, c := range res.Changes {
		if writesChange(c) {
			n++
		}
	}
	return n
}

func doneCount(res proto.AgentSyncResult) int {
	n := 0
	for _, c := range res.Changes {
		if c.Done {
			n++
		}
	}
	return n
}

func failedSync(res proto.AgentSyncResult) []string {
	var out []string
	for _, c := range res.Changes {
		if c.Error != "" {
			out = append(out, c.Name+": "+c.Error)
		}
	}
	return out
}

// listAgents names the agents in words: "Codex, Gemini CLI and OpenCode".
func listAgents(names []string) string {
	var labels []string
	for _, n := range names {
		labels = append(labels, agentLabel(n))
	}
	switch len(labels) {
	case 0:
		return "the other agents"
	case 1:
		return labels[0]
	}
	return strings.Join(labels[:len(labels)-1], ", ") + " and " + labels[len(labels)-1]
}

// The other half: the setup in your home — ~/.claude and the rest — which
// follows you from project to project. It is machine-wide, so it is asked
// for from Settings rather than from a row that names a folder, and it is
// the riskier half: there is no git status to show what changed, so the
// plan carries the whole of it and a file that is a link into a dotfiles
// repository is left alone rather than written through.

// openUserSync asks the local server what giving the other agents this
// one's own setup would write.
func (m *Model) openUserSync(from string) tea.Cmd {
	return m.userSync(from, false, false)
}

// undoUserSync puts the last one back.
func (m *Model) undoUserSync() tea.Cmd { return m.userSync("", false, true) }

func (m *Model) userSync(from string, apply, undo bool) tea.Cmd {
	c := m.clientOf(localMachine)
	if c == nil {
		m.setFlash(m.offlineText(localMachine), true)
		return nil
	}
	if len(c.MissingCapabilities([]string{proto.CapAgentSyncUser})) > 0 {
		m.setFlash("the server on this computer is too old to sync your own setup; reload it", true)
		return nil
	}
	params := proto.AgentSyncParams{User: true, Dir: "~", From: from, Apply: apply, Undo: undo}
	return func() tea.Msg {
		var res proto.AgentSyncResult
		err := callCtx(c, proto.MethodAgentSync, params, &res)
		return userSyncMsg{res: res, err: err, from: from}
	}
}

type userSyncMsg struct {
	res  proto.AgentSyncResult
	err  error
	from string
}

// receiveUserSync shows the plan and asks, or says what came of it.
func (m *Model) receiveUserSync(msg userSyncMsg) tea.Cmd {
	if msg.err != nil {
		m.showError(msg.err)
		return nil
	}
	res := msg.res
	switch {
	case res.Undone:
		m.setFlash(fmt.Sprintf("put your setup back as it was · %s", counted(doneCount(res), "file")), false)
		return nil
	case res.Applied:
		text := fmt.Sprintf("gave %s %s's setup, in your home · %s", listAgents(res.To), agentLabel(res.From), counted(doneCount(res), "change"))
		if failed := failedSync(res); len(failed) > 0 {
			m.setFlash(text+" · "+strings.Join(failed, "; "), true)
			return nil
		}
		m.setFlash(text+" · Settings → Agents puts it back", false)
		return nil
	}
	if syncWrites(res) == 0 {
		m.setFlash(agentLabel(res.From)+"'s setup is already in every agent in your home: "+syncWhyNot(res), false)
		return nil
	}
	from := msg.from
	d := newConfirm("", func(m *Model) tea.Cmd { return m.userSync(from, true, false) })
	d.title = " Sync your own agent setup "
	d.text = append([]string{"This writes in your home, where there is no git status to show what changed — the record under conch's folder is what puts it back."},
		syncLines(res)...)
	m.overlay = d
	return nil
}
